package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type networkTarget struct{ Name, IP, HostIP string }

var networkTargets = []networkTarget{
	{"Left glove", "192.168.1.100", "192.168.1.10"},
	{"Right glove", "192.168.1.101", "192.168.1.11"},
	{"Left Hand 2", "192.168.1.110", "192.168.1.20"},
	{"Right Hand 2", "192.168.1.111", "192.168.1.21"},
}

type networkAddress struct {
	Local     string `json:"local"`
	PrefixLen int    `json:"prefixlen"`
}

type networkAdapter struct {
	Name         string           `json:"ifname"`
	LinkType     string           `json:"link_type"`
	Master       json.RawMessage  `json:"master"`
	Addresses    []networkAddress `json:"addr_info"`
	DefaultRoute bool
}

type networkSystem struct {
	run     func(context.Context, string, ...string) ([]byte, error)
	sysRoot string
}

func localNetworkSystem() networkSystem {
	return networkSystem{run: runNetworkCommand, sysRoot: "/sys/class/net"}
}

// Privileged commands use only system tools, never executables on a user's PATH.
func runNetworkCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	var binary string
	for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			binary = path
			break
		}
	}
	if binary == "" {
		return nil, fmt.Errorf("%s is required; install iproute2, iputils-ping, iputils-arping and procps", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (s networkSystem) inventory(ctx context.Context) ([]networkAdapter, []networkAdapter, error) {
	data, err := s.run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return nil, nil, err
	}
	var all []networkAdapter
	if err = json.Unmarshal(data, &all); err != nil {
		return nil, nil, fmt.Errorf("read adapters: %w", err)
	}
	data, err = s.run(ctx, "ip", "-j", "-4", "route", "show", "default")
	if err != nil {
		return nil, nil, err
	}
	var routes []struct {
		Dev string `json:"dev"`
	}
	if err = json.Unmarshal(data, &routes); err != nil {
		return nil, nil, fmt.Errorf("read default routes: %w", err)
	}
	// Do not pass -4 to addr show: iproute2 would hide links with no IPv4
	// address. Keep every link, then filter addresses for this IPv4 repair.
	for i := range all {
		var ipv4 []networkAddress
		for _, address := range all[i].Addresses {
			if net.ParseIP(address.Local).To4() != nil {
				ipv4 = append(ipv4, address)
			}
		}
		all[i].Addresses = ipv4
	}
	var candidates []networkAdapter
	for _, a := range all {
		// Require a physical, connected Ethernet adapter; exclude Wi-Fi and bridge/bond members.
		if !validInterface(a.Name) || a.LinkType != "ether" || (len(a.Master) > 0 && string(a.Master) != "null") {
			continue
		}
		base := filepath.Join(s.sysRoot, a.Name)
		if _, err := os.Stat(filepath.Join(base, "device")); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, "wireless")); err == nil {
			continue
		}
		carrier, err := os.ReadFile(filepath.Join(base, "carrier"))
		if err != nil || strings.TrimSpace(string(carrier)) != "1" {
			continue
		}
		for _, r := range routes {
			if r.Dev == a.Name {
				a.DefaultRoute = true
			}
		}
		candidates = append(candidates, a)
	}
	return candidates, all, nil
}

func validInterface(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00\n\r\t ,") && !strings.HasPrefix(name, "-")
}

func addressOwner(all []networkAdapter, ip string) string {
	for _, a := range all {
		for _, address := range a.Addresses {
			if address.Local == ip {
				return a.Name
			}
		}
	}
	return ""
}

func exitOne(err error) bool {
	var status interface{ ExitCode() int }
	return errors.As(err, &status) && status.ExitCode() == 1
}

// rollback runs in reverse order with a fresh context, including after Ctrl+C.
type networkChange struct {
	name string
	args []string
}

func (s networkSystem) rollback(changes []networkChange) error {
	var result error
	for i := len(changes) - 1; i >= 0; i-- {
		_, err := s.run(context.Background(), changes[i].name, changes[i].args...)
		result = errors.Join(result, err)
	}
	return result
}

type networkMatch struct {
	Target  networkTarget
	Adapter string
}

func (s networkSystem) probe(ctx context.Context, a networkAdapter, all []networkAdapter, log io.Writer) (_ []networkMatch, result error) {
	source := ""
	for _, address := range a.Addresses {
		if strings.HasPrefix(address.Local, "192.168.1.") {
			source = address.Local
			break
		}
	}
	if source == "" {
		for suffix := 250; suffix <= 254; suffix++ {
			candidate := fmt.Sprintf("192.168.1.%d", suffix)
			if addressOwner(all, candidate) != "" {
				continue
			}
			// Duplicate-address detection needs no configured IPv4 address.
			_, err := s.run(ctx, "arping", "-D", "-I", a.Name, "-c", "2", "-w", "2", candidate)
			if exitOne(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			source = candidate
			break
		}
		if source == "" {
			return nil, fmt.Errorf("%s: no free probe address in .250–.254", a.Name)
		}
		cidr := source + "/32"
		if _, err := s.run(ctx, "ip", "-4", "addr", "add", cidr, "dev", a.Name, "noprefixroute"); err != nil {
			return nil, err
		}
		defer func() {
			_, err := s.run(context.Background(), "ip", "-4", "addr", "del", cidr, "dev", a.Name)
			if err != nil {
				result = errors.Join(result, fmt.Errorf("remove probe address: %w", err))
			}
		}()
	}
	var matches []networkMatch
	for _, target := range networkTargets {
		if addressOwner(all, target.IP) != "" {
			return nil, fmt.Errorf("%s is assigned to this computer; resolve the address conflict first", target.IP)
		}
		_, err := s.run(ctx, "arping", "-I", a.Name, "-s", source, "-c", "1", "-w", "2", target.IP)
		if exitOne(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(log, "  %s responds on %s (%s expected; IP alone does not verify identity).\n", target.IP, a.Name, target.Name)
		matches = append(matches, networkMatch{target, a.Name})
	}
	return matches, nil
}

// repair changes only selected Ethernet interfaces and exact factory-IP routes.
// It intentionally does not write persistent NetworkManager or sysctl config.
func (s networkSystem) repair(ctx context.Context, names []string, log io.Writer) (result error) {
	candidates, all, err := s.inventory(ctx)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("select at least one wired adapter")
	}
	var selected []networkAdapter
	seen := map[string]bool{}
	for _, name := range names {
		found := false
		for _, a := range candidates {
			if a.Name == name {
				found = true
				if !seen[name] {
					selected = append(selected, a)
					seen[name] = true
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("%q is not a connected, physical Ethernet adapter; refresh the adapter list", name)
		}
	}
	// Check prerequisites before even temporary changes.
	for _, command := range []struct {
		name string
		args []string
	}{
		{"arping", []string{"-V"}}, {"ping", []string{"-V"}}, {"sysctl", []string{"--version"}},
	} {
		output, err := s.run(ctx, command.name, command.args...)
		if err != nil {
			return err
		}
		if command.name == "arping" && !strings.Contains(strings.ToLower(string(output)), "iputils") {
			return fmt.Errorf("network repair requires iputils arping; install iputils-arping (other arping implementations use different flags)")
		}
	}
	fmt.Fprintln(log, "1/3 · Probing the selected wired adapters")
	var matches []networkMatch
	mapped := map[string]string{}
	for _, a := range selected {
		fmt.Fprintf(log, "Checking %s…\n", a.Name)
		found, err := s.probe(ctx, a, all, log)
		if err != nil {
			return err
		}
		for _, m := range found {
			if previous := mapped[m.Target.IP]; previous != "" {
				return fmt.Errorf("%s responds on both %s and %s; select only the intended adapter and retry", m.Target.IP, previous, m.Adapter)
			}
			mapped[m.Target.IP] = m.Adapter
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("no factory-IP devices responded; check power, cables and configured IPs (custom IPs are not probed)")
	}
	var changes []networkChange
	defer func() {
		if result != nil && len(changes) > 0 {
			fmt.Fprintln(log, "Repair did not complete. Restoring changed addresses, routes and ARP settings…")
			if err := s.rollback(changes); err != nil {
				result = errors.Join(result, fmt.Errorf("ROLLBACK INCOMPLETE: %w", err))
			} else {
				fmt.Fprintln(log, "Previous configuration restored.")
			}
		}
	}()
	change := func(name string, args []string, undo networkChange) error {
		if _, err := s.run(ctx, name, args...); err != nil {
			return err
		}
		changes = append(changes, undo)
		return nil
	}
	fmt.Fprintln(log, "2/3 · Applying host addresses, per-interface ARP settings and /32 routes")
	configured := map[string]bool{}
	for _, m := range matches {
		host := m.Target.HostIP
		owner := addressOwner(all, host)
		if owner != "" && owner != m.Adapter {
			return fmt.Errorf("host address %s already belongs to %s; refusing to duplicate it", host, owner)
		}
		if owner == "" {
			if _, err := s.run(ctx, "arping", "-D", "-I", m.Adapter, "-c", "2", "-w", "2", host); err != nil {
				return fmt.Errorf("host address %s is not available: %w", host, err)
			}
			if err := change("ip", []string{"-4", "addr", "add", host + "/24", "dev", m.Adapter, "noprefixroute"}, networkChange{"ip", []string{"-4", "addr", "del", host + "/24", "dev", m.Adapter}}); err != nil {
				return err
			}
		}
		if !configured[m.Adapter] {
			for _, setting := range []struct{ key, value string }{{"arp_filter", "1"}, {"arp_ignore", "1"}, {"arp_announce", "2"}, {"rp_filter", "2"}} {
				key := "net/ipv4/conf/" + m.Adapter + "/" + setting.key
				previous, err := s.run(ctx, "sysctl", "-n", key)
				if err != nil {
					return err
				}
				if err := change("sysctl", []string{"-w", key + "=" + setting.value}, networkChange{"sysctl", []string{"-w", key + "=" + strings.TrimSpace(string(previous))}}); err != nil {
					return err
				}
			}
			configured[m.Adapter] = true
		}
		destination := m.Target.IP + "/32"
		previous, err := s.run(ctx, "ip", "-4", "route", "show", "exact", destination)
		if err != nil {
			return err
		}
		old := strings.TrimSpace(string(previous))
		if strings.Contains(old, "\n") || strings.Contains(old, "nexthop") {
			return fmt.Errorf("%s has a complex route; configure it manually", destination)
		}
		undo := networkChange{"ip", []string{"-4", "route", "del", destination}}
		if old != "" {
			undo.args = []string{"-4", "route", "replace"}
			for _, field := range strings.Fields(old) {
				if field != "linkdown" {
					undo.args = append(undo.args, field)
				}
			}
		}
		replacement := []string{"-4", "route", "replace", destination, "dev", m.Adapter, "src", host, "scope", "link"}
		// Route replacement is keyed by metric as well as prefix. Reuse the old
		// metric so rollback does not leave an extra, higher-priority route behind.
		fields := strings.Fields(old)
		for i, field := range fields {
			if field == "metric" && i+1 < len(fields) {
				replacement = append(replacement, "metric", fields[i+1])
			}
		}
		if err := change("ip", replacement, undo); err != nil {
			return err
		}
		fmt.Fprintf(log, "  %s: %s → %s via %s\n", m.Target.Name, host, m.Target.IP, m.Adapter)
	}
	fmt.Fprintln(log, "3/3 · Verifying network reachability")
	for _, m := range matches {
		// Verify the route normal clients will actually use, including policy routing.
		route, err := s.run(ctx, "ip", "-j", "-4", "route", "get", m.Target.IP)
		if err != nil {
			return err
		}
		var resolved []struct {
			Dev     string `json:"dev"`
			Source  string `json:"prefsrc"`
			Gateway string `json:"gateway"`
		}
		if err := json.Unmarshal(route, &resolved); err != nil {
			return fmt.Errorf("read route to %s: %w", m.Target.IP, err)
		}
		if len(resolved) != 1 || resolved[0].Dev != m.Adapter || resolved[0].Source != m.Target.HostIP || resolved[0].Gateway != "" {
			return fmt.Errorf("routing policy still sends %s through an unexpected interface or source address", m.Target.IP)
		}
		// Clear only this device's dynamic neighbor entry, never the adapter's whole cache.
		if _, err := s.run(ctx, "ip", "-4", "neigh", "flush", "to", m.Target.IP, "dev", m.Adapter); err != nil {
			return err
		}
		if _, err := s.run(ctx, "ping", "-n", "-c", "2", "-W", "1", m.Target.IP); err != nil {
			return fmt.Errorf("%s on %s still does not answer ping: %w", m.Target.IP, m.Adapter, err)
		}
		fmt.Fprintf(log, "  %s on %s: ping OK\n", m.Target.IP, m.Adapter)
	}
	fmt.Fprintln(log, "Host network repair completed. UDP service health is not yet verified.\nUse Check devices in the TUI to test Wuji CLI responses. If the device still hangs, close other clients and check power/firmware.\nChanges are temporary; a reboot or network manager may reset them. Wi-Fi, default routes and unrelated addresses were not changed.")
	return nil
}

func checkRepairInvocation(goos string, uid int, confirmed bool) error {
	if goos != "linux" {
		return fmt.Errorf("network repair is supported on Linux only")
	}
	if !confirmed {
		return fmt.Errorf("review the repair screen first, or pass --yes with --repair-network and --interfaces")
	}
	if uid != 0 {
		return fmt.Errorf("network repair requires sudo; use the TUI or sudo wuji-helper --repair-network --interfaces <adapter,...> --yes")
	}
	return nil
}

func readNetworkAdapters() ([]networkAdapter, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("network repair requires Linux (iproute2 and per-interface ARP settings); firmware updates remain available on this platform")
	}
	candidates, _, err := localNetworkSystem().inventory(context.Background())
	return candidates, err
}
