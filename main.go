// Wuji firmware bouncer provides a terminal UI for local glove firmware packages.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type ui struct {
	app      *tview.Application
	cli      cli
	devices  []device
	selected map[string]bool
	pkg      *firmwarePackage
	flashing bool
	failed   bool
}

func main() {
	binary := flag.String("wuji", "wuji", "path to the official Wuji CLI executable")
	flag.Parse()
	path, err := exec.LookPath(*binary)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Wuji CLI is required. Install it from https://github.com/wuji-technology/wuji-cli or use --wuji /path/to/wuji.")
		os.Exit(1)
	}
	u := &ui{app: tview.NewApplication(), cli: cli{binary: path}, selected: map[string]bool{}}
	u.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			if !u.flashing {
				u.app.Stop()
			}
			return nil
		}
		return event
	})
	u.scan()
	err = u.app.Run()
	u.pkg.cleanup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if u.failed {
		os.Exit(1)
	}
}

func (u *ui) show(p tview.Primitive) { u.app.SetRoot(p, true).SetFocus(p) }
func panel(title, text string) *tview.TextView {
	v := tview.NewTextView().SetDynamicColors(false).SetText(text)
	v.SetBorder(true).SetTitle(title)
	return v
}
func (u *ui) message(text string, retry func()) {
	m := tview.NewModal().SetText(text).AddButtons([]string{"Retry", "Quit"}).SetDoneFunc(func(_ int, label string) {
		if label == "Retry" {
			retry()
		} else {
			u.app.Stop()
		}
	})
	u.show(m)
}
func (u *ui) scan() {
	u.show(panel(" Wuji Firmware Bouncer ", "Scanning USB and local-network devices; probing each serial…\nCtrl+C to quit."))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		devices, err := u.cli.scan(ctx)
		u.app.QueueUpdateDraw(func() {
			if err != nil {
				u.message(err.Error(), u.scan)
				return
			}
			u.devices = devices
			u.selected = map[string]bool{}
			u.choose()
		})
	}()
}
func (u *ui) choose() {
	form := tview.NewForm()
	count := 0
	var unavailable []string
	for _, d := range u.devices {
		if d.Problem != "" {
			unavailable = append(unavailable, d.SN+": "+d.Problem)
			continue
		}
		if !strings.EqualFold(d.Kind, "wuji_glove") {
			unavailable = append(unavailable, d.SN+": "+d.Kind+" (not a glove)")
			continue
		}
		count++
		sn := d.SN
		form.AddCheckbox(fmt.Sprintf("%s glove | SN %s | firmware %s | %s %s", d.Side, sn, d.Version, d.Transport, d.Address), u.selected[sn], func(checked bool) { u.selected[sn] = checked })
	}
	form.SetBorder(true).SetTitle(" 1 · Select gloves (Space toggles) ")
	form.AddButton("Next", func() {
		if len(u.targets()) == 0 {
			u.alert("Select at least one reachable Wuji glove.", u.choose)
			return
		}
		u.packageForm("")
	}).AddButton("Rescan", u.scan).AddButton("Quit", func() { u.app.Stop() })
	info := fmt.Sprintf("%d reachable glove(s). Tab to move; Space to select; Enter activates buttons.\n", count)
	if count == 0 {
		info += "Connect and power on a glove, then choose Rescan.\n"
	}
	if len(unavailable) > 0 {
		info += "\nUnavailable devices:\n" + strings.Join(unavailable, "\n")
	}
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(panel(" Discovery ", info), 0, 1, false).AddItem(form, 0, 2, true)
	u.show(root)
}
func (u *ui) alert(text string, back func()) {
	u.show(tview.NewModal().SetText(text).AddButtons([]string{"OK"}).SetDoneFunc(func(int, string) { back() }))
}
func (u *ui) targets() []device {
	var result []device
	for _, d := range u.devices {
		if u.selected[d.SN] {
			result = append(result, d)
		}
	}
	return result
}
func (u *ui) packageForm(value string) {
	form := tview.NewForm()
	form.AddInputField("OTA ZIP path", value, 60, nil, nil)
	form.SetBorder(true).SetTitle(" 2 · Package from Wuji (~/ paths supported) ")
	form.AddButton("Review plan", func() {
		input := form.GetFormItem(0).(*tview.InputField).GetText()
		u.show(panel(" Checking package ", "Copying package and checking ZIP integrity…"))
		go func() {
			p, err := preparePackage(input)
			u.app.QueueUpdateDraw(func() {
				if err != nil {
					u.alert(err.Error(), func() { u.packageForm(input) })
					return
				}
				u.pkg.cleanup()
				u.pkg = p
				u.review()
			})
		}()
	}).AddButton("Back", u.choose).AddButton("Quit", func() { u.app.Stop() })
	u.show(form)
}
func (u *ui) review() {
	p := u.pkg
	var lines []string
	for _, d := range u.targets() {
		lines = append(lines, fmt.Sprintf("%s glove   SN %s   current: %s   %s %s", d.Side, d.SN, d.Version, d.Transport, d.Address))
	}
	text := fmt.Sprintf("Install this package on %d selected glove(s):\n\n%s\n\nPackage: %s\nVersion declared in manifest: %s\nSize: %d bytes\nSHA-256: %s\n\nAn older package DOWNGRADES firmware; a newer package upgrades it.\nWuji CLI verifies manifest, firmware digest, and device compatibility.\nSame-version installs may be skipped. Devices reboot after flashing.\nKeep devices powered and connected. Release other apps using the gloves.\nDevices are processed sequentially; stop on the first failure.\n", len(lines), strings.Join(lines, "\n"), p.Original, p.Version, p.Size, p.SHA256)
	buttons := tview.NewForm().AddButton("Confirm & install", u.install).AddButton("Back", func() { u.packageForm(p.Original) }).AddButton("Cancel", func() { u.app.Stop() })
	buttons.SetBorder(true).SetTitle(" 3 · Confirm upgrade / downgrade ")
	plan := panel(" Installation plan (PgUp/PgDn to scroll) ", text)
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(plan, 0, 1, false).AddItem(buttons, 5, 0, true)
	root.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyPgUp || event.Key() == tcell.KeyPgDn {
			plan.InputHandler()(event, func(tview.Primitive) {})
			return nil
		}
		return event
	})
	u.show(root)
}
func (u *ui) install() {
	u.flashing = true
	log := tview.NewTextView().SetScrollable(true).SetChangedFunc(func() { u.app.Draw() })
	log.SetBorder(true).SetTitle(" 4 · Installing — keep gloves connected ")
	u.show(log)
	targets := u.targets()
	go func() {
		failed := false
		for i, d := range targets {
			fmt.Fprintf(log, "\n[%d/%d] %s\n", i+1, len(targets), d.SN)
			// Recheck identity/type immediately before each write. A failed probe must
			// not turn a stale discovery entry into an implicit firmware target.
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			data, err := u.cli.query(ctx, "ping", "--sn", d.SN, "--json")
			cancel()
			if err == nil {
				kind, _, probeErr := probeInfo(data, d.SN)
				err = probeErr
				if err == nil && !strings.EqualFold(kind, "wuji_glove") {
					err = fmt.Errorf("selected serial is no longer a Wuji glove")
				}
			}
			if err == nil {
				side, version, detailsErr := u.cli.gloveDetails(context.Background(), d.SN)
				err = detailsErr
				if err == nil && (side != d.Side || version != d.Version) {
					err = fmt.Errorf("glove details changed since confirmation (%s, %s); rescan and review a new plan", side, version)
				}
			}
			if err == nil {
				err = u.cli.flash(context.Background(), d.SN, u.pkg, log)
			}
			if err != nil {
				fmt.Fprintf(log, "\nSTOPPED: %v\nRemaining devices were not flashed.\n", err)
				failed = true
				break
			}
		}
		if !failed {
			fmt.Fprintln(log, "\nWuji CLI completed. Review each report above for ok/skipped status.")
		}
		u.app.QueueUpdateDraw(func() {
			u.flashing = false
			u.failed = failed
			buttons := tview.NewForm().AddButton("Done", func() { u.app.Stop() })
			root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(log, 0, 1, true).AddItem(buttons, 3, 0, false)
			u.show(root)
			log.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEnter || e.Rune() == 'q' {
					u.app.Stop()
					return nil
				}
				return e
			})
		})
	}()
}
