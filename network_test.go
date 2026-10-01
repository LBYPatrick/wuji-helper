package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type networkExit int

func (e networkExit) Error() string { return fmt.Sprintf("exit %d", e) }
func (e networkExit) ExitCode() int { return int(e) }

type fakeNetwork struct {
	adapters  []networkAdapter
	defaults  []map[string]string
	hits      map[string]bool
	conflicts map[string]bool
	routes    map[string]string
	settings  map[string]string
	calls     []string
	fail      string
}

func newFakeNetwork(t *testing.T) (*fakeNetwork, networkSystem) {
	t.Helper()
	f := &fakeNetwork{
		adapters: []networkAdapter{
			{Name: "enxleft", LinkType: "ether", Addresses: []networkAddress{{"10.42.0.1", 24}}},
			{Name: "enxright", LinkType: "ether"},
		},
		hits:      map[string]bool{"enxleft/192.168.1.100": true, "enxright/192.168.1.101": true},
		conflicts: map[string]bool{}, routes: map[string]string{}, settings: map[string]string{},
	}
	root := t.TempDir()
	for _, a := range f.adapters {
		if err := os.MkdirAll(filepath.Join(root, a.Name, "device"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, a.Name, "carrier"), []byte("1\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return f, networkSystem{run: f.run, sysRoot: root}
}

func (f *fakeNetwork) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.fail != "" && strings.Contains(call, f.fail) {
		f.fail = ""
		return nil, fmt.Errorf("injected failure")
	}
	arg := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch {
	case call == "arping -V":
		return []byte("arping from iputils 20240905"), nil
	case call == "ip -j addr show":
		return json.Marshal(f.adapters)
	case call == "ip -j -4 route show default":
		return json.Marshal(f.defaults)
	case name == "arping" && len(args) > 1:
		nic := arg("-I")
		ip := args[len(args)-1]
		if args[0] == "-D" {
			if f.conflicts[nic+"/"+ip] {
				return nil, networkExit(1)
			}
			return nil, nil
		}
		if f.hits[nic+"/"+ip] {
			return nil, nil
		}
		return nil, networkExit(1)
	case strings.HasPrefix(call, "ip -4 addr "):
		cidr := strings.Split(args[3], "/")
		for i := range f.adapters {
			if f.adapters[i].Name == arg("dev") {
				if args[2] == "add" {
					prefix := 24
					if cidr[1] == "32" {
						prefix = 32
					}
					f.adapters[i].Addresses = append(f.adapters[i].Addresses, networkAddress{cidr[0], prefix})
				} else {
					for j, a := range f.adapters[i].Addresses {
						if a.Local == cidr[0] {
							f.adapters[i].Addresses = append(f.adapters[i].Addresses[:j], f.adapters[i].Addresses[j+1:]...)
							break
						}
					}
				}
			}
		}
	case strings.HasPrefix(call, "ip -j -4 route get "):
		route := strings.Fields(f.routes[args[4]+"/32"])
		record := map[string]string{}
		for i := 0; i+1 < len(route); i++ {
			if route[i] == "dev" {
				record["dev"] = route[i+1]
			}
			if route[i] == "src" {
				record["prefsrc"] = route[i+1]
			}
		}
		return json.Marshal([]map[string]string{record})
	case strings.HasPrefix(call, "ip -4 route show exact "):
		return []byte(f.routes[args[4]]), nil
	case strings.HasPrefix(call, "ip -4 route replace "):
		f.routes[args[3]] = strings.Join(args[3:], " ")
	case strings.HasPrefix(call, "ip -4 route del "):
		delete(f.routes, args[3])
	case name == "sysctl" && args[0] == "-n":
		value := f.settings[args[1]]
		if value == "" {
			value = "0"
		}
		return []byte(value), nil
	case name == "sysctl" && args[0] == "-w":
		pair := strings.SplitN(args[1], "=", 2)
		f.settings[pair[0]] = pair[1]
	}
	return nil, nil
}

func TestNetworkRepairBothDeviceTypes(t *testing.T) {
	f, s := newFakeNetwork(t)
	// A glove and hand share an adapter; neither should hide the other.
	f.hits["enxleft/192.168.1.110"] = true
	f.hits["enxright/192.168.1.111"] = true
	var log strings.Builder
	if err := s.repair(context.Background(), []string{"enxleft", "enxright"}, &log); err != nil {
		t.Fatal(err)
	}
	for _, target := range networkTargets {
		route := f.routes[target.IP+"/32"]
		if !strings.Contains(route, "src "+target.HostIP) {
			t.Errorf("wrong route for %s: %s", target.IP, route)
		}
	}
	if addressOwner(f.adapters, "10.42.0.1") != "enxleft" {
		t.Fatal("removed Quest address")
	}
	if addressOwner(f.adapters, "192.168.1.250") != "" {
		t.Fatal("left probe address")
	}
	calls := strings.Join(f.calls, "\n")
	for _, forbidden := range []string{"neigh flush dev", "route replace default", "link set", "conf/all", "10.42.0.1/24 dev"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("unrelated mutation: %s", forbidden)
		}
	}
	if !strings.Contains(log.String(), "UDP service health is not yet verified") {
		t.Fatal("missing service-health distinction")
	}
	// Repeat should reuse host addresses and keep the same number of addresses.
	count := len(f.adapters[0].Addresses) + len(f.adapters[1].Addresses)
	if err := s.repair(context.Background(), []string{"enxleft", "enxright"}, &log); err != nil {
		t.Fatal(err)
	}
	if count != len(f.adapters[0].Addresses)+len(f.adapters[1].Addresses) {
		t.Fatal("repeat duplicated addresses")
	}
}

func TestNetworkProbeAndApplyFailures(t *testing.T) {
	for _, scenario := range []string{"no responders", "ambiguous", "host conflict", "probe failure", "route failure", "route lookup failure", "ping failure", "canceled", "cancel during apply"} {
		t.Run(scenario, func(t *testing.T) {
			f, s := newFakeNetwork(t)
			f.routes["192.168.1.100/32"] = "192.168.1.100/32 via 10.42.0.2 dev enxleft metric 100"
			beforeRoutes := map[string]string{"192.168.1.100/32": f.routes["192.168.1.100/32"]}
			ctx := context.Background()
			switch scenario {
			case "no responders":
				f.hits = map[string]bool{}
			case "ambiguous":
				f.hits["enxright/192.168.1.100"] = true
			case "host conflict":
				f.conflicts["enxright/192.168.1.11"] = true
			case "probe failure":
				f.fail = "arping -I enxleft"
			case "route failure":
				f.fail = "route replace 192.168.1.101/32"
			case "route lookup failure":
				f.fail = "ip -j -4 route get"
			case "ping failure":
				f.fail = "ping -n"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if scenario == "cancel during apply" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				s.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
					out, err := f.run(ctx, name, args...)
					if strings.Contains(name+" "+strings.Join(args, " "), "route replace 192.168.1.100/32 dev") {
						cancel()
					}
					return out, err
				}
			}
			var log strings.Builder
			if err := s.repair(ctx, []string{"enxleft", "enxright"}, &log); err == nil {
				t.Fatal("expected failure")
			}
			if addressOwner(f.adapters, "192.168.1.250") != "" {
				t.Fatal("probe address leaked")
			}
			for _, target := range networkTargets {
				if addressOwner(f.adapters, target.HostIP) != "" {
					t.Fatalf("host address leaked: %s", target.HostIP)
				}
			}
			if addressOwner(f.adapters, "10.42.0.1") != "enxleft" {
				t.Fatal("lost existing address")
			}
			if !reflect.DeepEqual(f.routes, beforeRoutes) {
				t.Fatalf("routes not restored: %v", f.routes)
			}
			for key, value := range f.settings {
				if value != "0" {
					t.Errorf("setting not restored: %s=%s", key, value)
				}
			}
		})
	}
}

func TestNetworkInventoryAndValidation(t *testing.T) {
	f, s := newFakeNetwork(t)
	f.defaults = []map[string]string{{"dev": "enxleft"}}
	if err := os.Mkdir(filepath.Join(s.sysRoot, "enxright", "wireless"), 0700); err != nil {
		t.Fatal(err)
	}
	adapters, _, err := s.inventory(context.Background())
	if err != nil || len(adapters) != 1 || !adapters[0].DefaultRoute {
		t.Fatalf("inventory=%v err=%v", adapters, err)
	}
	for _, names := range [][]string{nil, {"enxright"}, {"missing"}, {"../../etc"}} {
		if err := s.repair(context.Background(), names, &strings.Builder{}); err == nil {
			t.Fatalf("accepted %v", names)
		}
	}
	for _, name := range []string{"", ".", "..", "../eth0", "-eth0", "eth0,eth1", "eth0\n"} {
		if validInterface(name) {
			t.Fatalf("accepted interface %q", name)
		}
	}
	for _, test := range []struct {
		os  string
		uid int
		yes bool
		ok  bool
	}{{"darwin", 0, true, false}, {"linux", 1000, true, false}, {"linux", 0, false, false}, {"linux", 0, true, true}} {
		if err := checkRepairInvocation(test.os, test.uid, test.yes); (err == nil) != test.ok {
			t.Fatalf("guard: %+v: %v", test, err)
		}
	}
	cmd := repairCommand("/tmp/wuji helper", []string{"eth0", "enp1s0"}, 1000)
	if !reflect.DeepEqual(cmd.Args, []string{"sudo", "--", "/tmp/wuji helper", "--repair-network", "--interfaces", "eth0,enp1s0", "--yes"}) {
		t.Fatalf("wrong sudo arguments: %q", cmd.Args)
	}
	if cmd := repairCommand("/tmp/wuji-helper", []string{"eth0"}, 0); cmd.Path != "/tmp/wuji-helper" {
		t.Fatalf("root unnecessarily uses sudo: %v", cmd.Args)
	}
}

func TestNetworkProbeAddressCollision(t *testing.T) {
	f, s := newFakeNetwork(t)
	f.conflicts["enxleft/192.168.1.250"] = true
	if _, err := s.probe(context.Background(), f.adapters[0], f.adapters, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "addr add 192.168.1.251/32") {
		t.Fatal("did not avoid probe address conflict")
	}
	if addressOwner(f.adapters, "192.168.1.251") != "" {
		t.Fatal("probe address not removed")
	}
}

func TestNetworkCleanupErrorsAreReported(t *testing.T) {
	for _, phase := range []string{"probe", "rollback"} {
		t.Run(phase, func(t *testing.T) {
			f, s := newFakeNetwork(t)
			s.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				call := name + " " + strings.Join(args, " ")
				if phase == "probe" && strings.Contains(call, "addr del 192.168.1.250") {
					return nil, fmt.Errorf("cleanup blocked")
				}
				if phase == "rollback" && (strings.HasPrefix(call, "ping -n") || strings.Contains(call, "addr del 192.168.1.10/24")) {
					return nil, fmt.Errorf("cleanup blocked")
				}
				return f.run(ctx, name, args...)
			}
			err := s.repair(context.Background(), []string{"enxleft"}, &strings.Builder{})
			if err == nil || !strings.Contains(err.Error(), "cleanup blocked") {
				t.Fatalf("cleanup error hidden: %v", err)
			}
			if phase == "rollback" && !strings.Contains(err.Error(), "ROLLBACK INCOMPLETE") {
				t.Fatalf("missing rollback warning: %v", err)
			}
		})
	}
}

func TestNetworkRetainsExistingRouteMetric(t *testing.T) {
	f, s := newFakeNetwork(t)
	f.routes["192.168.1.100/32"] = "192.168.1.100/32 dev enxright metric 100"
	if err := s.repair(context.Background(), []string{"enxleft"}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.routes["192.168.1.100/32"], "metric 100") {
		t.Fatal("route metric was not preserved")
	}
}

func TestNetworkRejectsOtherArpingImplementations(t *testing.T) {
	f, s := newFakeNetwork(t)
	s.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "arping" && args[0] == "-V" {
			return []byte("ARPing 2.23"), nil
		}
		return f.run(ctx, name, args...)
	}
	if err := s.repair(context.Background(), []string{"enxleft"}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "iputils arping") {
		t.Fatalf("unexpected result: %v", err)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "addr add") {
			t.Fatal("mutated before prerequisites were checked")
		}
	}
}

func TestNetworkInventoryIncludesUnconfiguredAdapters(t *testing.T) {
	f, s := newFakeNetwork(t)
	f.adapters[1].Addresses = []networkAddress{{"fe80::1234", 64}}
	adapters, _, err := s.inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) != 2 || adapters[1].Name != "enxright" || len(adapters[1].Addresses) != 0 {
		t.Fatalf("IPv4-unconfigured adapter missing: %+v", adapters)
	}
	for _, call := range f.calls {
		if call == "ip -j -4 addr show" {
			t.Fatal("IPv4 family filter hides unconfigured links")
		}
	}
}
