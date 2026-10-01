package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (u *ui) home() {
	list := tview.NewList().ShowSecondaryText(true).SetWrapAround(false).
		SetMainTextStyle(tcell.StyleDefault.Foreground(ink).Background(canvas)).
		SetSecondaryTextStyle(tcell.StyleDefault.Foreground(muted).Background(canvas)).
		SetSelectedTextColor(canvas).SetSelectedBackgroundColor(accent)
	list.SetBackgroundColor(canvas)
	list.AddItem("Firmware updates", "Upgrade or downgrade Wuji gloves with a local OTA ZIP.", 0, u.openFirmware)
	list.AddItem("Repair device network", "Recover Ethernet connectivity for gloves and Hand 2 · Linux / sudo.", 0, u.networkSetup)
	list.AddItem("Quit", "Exit Wuji Helper.", 0, u.app.Stop)
	list.SetBorderPadding(1, 0, 1, 1)
	u.workspace(-1, "What would you like to do?", "↑↓ Choose a task    Enter Open    Ctrl+C Quit", list, list)
}

func (u *ui) openFirmware() {
	path, err := exec.LookPath(u.cli.binary)
	if err != nil {
		details := panel("Wuji CLI not found", "Firmware updates require the official Wuji CLI.\n\nRun make install, or launch with --wuji /path/to/wuji.\nNetwork repair can run without the CLI.")
		actions := u.actionForm().AddButton("Try again", u.openFirmware).AddButton("Main menu", u.home)
		actions.SetCancelFunc(u.home)
		body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(details, 0, 1, false).AddItem(actions, 3, 0, true)
		u.workspace(0, "Install Wuji CLI to continue", "←→ Actions    Enter Choose    Esc Main menu", body, actions)
		return
	}
	u.cli.binary = path
	u.scan()
}

func (u *ui) networkSetup() {
	adapters, err := readNetworkAdapters()
	u.networkForm(adapters, err, u.startNetworkRepair)
}

// The submit callback keeps UI tests independent of real privilege elevation.
func (u *ui) networkForm(adapters []networkAdapter, inventoryErr error, submit func([]string)) {
	details := panel("Repair scope", "Factory IPs: gloves .100 / .101; Hand 2 .110 / .111.\nHost IPs: .10 / .11 for gloves; .20 / .21 for Hand 2.\n\nProbe selected wired adapters, then add host addresses and /32 routes only for responders. Set per-interface ARP and reverse-path filtering.\nKeep existing addresses (including Quest 10.42), Wi-Fi and default routes.\nChanges last until reboot or network reconfiguration. This does not reboot devices.\n\nSelect only adapters connected to Wuji devices. IP replies do not prove device identity. Adapters carrying a default route are unchecked.")
	form := u.actionForm()
	if u.networkSelected == nil {
		u.networkSelected = map[string]bool{}
	}
	selected := u.networkSelected
	for _, a := range adapters {
		name := a.Name
		checked, known := selected[name]
		if !known {
			checked = !a.DefaultRoute
			selected[name] = checked
		}
		label := name
		if a.DefaultRoute {
			label += " (default route)"
		}
		var addresses []string
		for _, address := range a.Addresses {
			addresses = append(addresses, address.Local)
		}
		if len(addresses) > 0 {
			label += " · " + strings.Join(addresses, ", ")
		}
		form.AddCheckbox(label, checked, func(on bool) { selected[name] = on })
		form.GetFormItem(form.GetFormItemCount() - 1).(*tview.Checkbox).SetUncheckedString("-").SetCheckedString("x").SetActivatedStyle(tcell.StyleDefault.Foreground(canvas).Background(accent))
	}
	if inventoryErr != nil {
		details.SetText(inventoryErr.Error()).SetTextColor(warning)
	} else if len(adapters) == 0 {
		details.SetText("No connected wired adapters found.\n\nPower on the device, connect its Ethernet/USB network adapter, and choose Refresh.\nWi-Fi, virtual interfaces, and bridge/bond members are excluded.")
	}
	form.AddButton("Repair with sudo", func() {
		var names []string
		for _, a := range adapters {
			if selected[a.Name] {
				names = append(names, a.Name)
			}
		}
		if len(names) == 0 {
			details.SetText("Select at least one adapter with Space, then choose Repair with sudo.").SetTextColor(warning)
			return
		}
		u.networkConfirm(names, submit)
	}).AddButton("Refresh", u.networkSetup).AddButton("Main menu", u.home)
	form.GetButton(0).SetDisabled(inventoryErr != nil || len(adapters) == 0)
	form.SetCancelFunc(u.home)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(details, 0, 1, false).AddItem(form, min(12, 2*len(adapters)+4), 0, true)
	root := u.workspace(-2, "Repair device network", "↑↓ Controls  ←→ Actions  Space Select  PgUp/PgDn Details  Esc Menu", body, form)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyPgUp || e.Key() == tcell.KeyPgDn {
			details.InputHandler()(e, func(tview.Primitive) {})
			return nil
		}
		return e
	})
}

func (u *ui) networkConfirm(names []string, submit func([]string)) {
	details := panel("Confirm network changes", fmt.Sprintf("Adapters: %s\n\nProbe .100/.101 (gloves) and .110/.111 (Hand 2). For responders, add host IPs .10/.11/.20/.21, exact /32 routes, and per-interface ARP settings.\n\nOnly continue if these adapters connect to your Wuji devices. Any existing exact routes to these four IPs may be replaced.\n\nSudo will ask for your password in the terminal if needed. The password is handled by sudo, not this application.\nFailed repairs attempt to restore changes; cleanup errors appear in the report.", strings.Join(names, ", ")))
	actions := u.actionForm().AddButton("Back", u.networkSetup).AddButton("Apply repair", func() { submit(names) })
	actions.SetCancelFunc(u.networkSetup)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(details, 0, 1, false).AddItem(actions, 3, 0, true)
	root := u.workspace(-2, "Confirm adapters and apply repair", "←→ Actions    Enter Choose    PgUp/PgDn Details    Esc Back", body, actions)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyPgUp || e.Key() == tcell.KeyPgDn {
			details.InputHandler()(e, func(tview.Primitive) {})
			return nil
		}
		return e
	})
}

func repairCommand(executable string, names []string, uid int) *exec.Cmd {
	args := []string{"--repair-network", "--interfaces", strings.Join(names, ","), "--yes"}
	if uid == 0 {
		return exec.Command(executable, args...)
	}
	return exec.Command("sudo", append([]string{"--", executable}, args...)...)
}

func (u *ui) startNetworkRepair(names []string) {
	var report bytes.Buffer
	var repairErr error
	// Suspend restores a real terminal for sudo's password prompt and live output.
	if !u.app.Suspend(func() {
		executable, err := os.Executable()
		if err != nil {
			repairErr = err
			return
		}
		// Let the helper handle Ctrl+C and rollback; keep the parent alive for its report.
		interrupted := make(chan os.Signal, 1)
		signal.Notify(interrupted, os.Interrupt)
		defer signal.Stop(interrupted)
		cmd := repairCommand(executable, names, os.Geteuid())
		cmd.Stdin = os.Stdin
		output := io.MultiWriter(os.Stdout, &report)
		cmd.Stdout, cmd.Stderr = output, output
		repairErr = cmd.Run()
	}) {
		repairErr = fmt.Errorf("could not suspend the terminal for sudo")
	}
	u.failed = repairErr != nil
	title := "Network repair complete"
	if repairErr != nil {
		title = "Network repair did not complete"
		fmt.Fprintf(&report, "\n%s\n", repairErr)
	}
	u.networkReport(title, report.String())
}

func (u *ui) networkReport(title, report string) {
	view := panel("Repair report", report)
	actions := u.actionForm().AddButton("Check devices", func() { u.checkNetworkDevices(report) }).AddButton("Repair again", u.networkSetup).AddButton("Main menu", u.home)
	actions.SetCancelFunc(u.home)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, false).AddItem(actions, 3, 0, true)
	root := u.workspace(-2, title, "PgUp/PgDn Scroll  ←→ Actions  Enter Choose  Esc Menu", body, actions)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyPgUp || e.Key() == tcell.KeyPgDn {
			view.InputHandler()(e, func(tview.Primitive) {})
			return nil
		}
		return e
	})
}

func (u *ui) checkNetworkDevices(report string) {
	if _, err := exec.LookPath(u.cli.binary); err != nil {
		u.networkReport("Wuji CLI required for device checks", report+"\n\nInstall Wuji CLI with make install to verify device responses. Ping reachability alone does not verify firmware or UDP service health.")
		return
	}
	view := panel("Checking devices", "Discovering devices and probing each serial with Wuji CLI…")
	u.workspace(-2, "Check device responsiveness", "Ctrl+C Quit", view, view)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		devices, err := u.cli.scan(ctx)
		var result strings.Builder
		result.WriteString(report + "\n\nWuji CLI device checks\n")
		if err != nil {
			fmt.Fprintf(&result, "Discovery failed: %s\n", err)
		} else if len(devices) == 0 {
			result.WriteString("No devices discovered. Check UDP firewall rules, custom IP/port settings, other connected clients, and device power.\n")
		}
		for _, d := range devices {
			if d.Problem != "" {
				fmt.Fprintf(&result, "%s: not responding · %s\n", d.SN, d.Problem)
			} else {
				fmt.Fprintf(&result, "%s · %s · %s: responds to Wuji CLI\n", d.SN, d.Kind, d.Address)
			}
		}
		result.WriteString("\nDiscovery can include other connected devices. Network repair cannot recover every device-side firmware hang. USB-only Wuji Hand needs no Ethernet repair.")
		u.app.QueueUpdateDraw(func() { u.networkReport("Device checks finished", result.String()) })
	}()
}
