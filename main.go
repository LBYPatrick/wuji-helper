// Wuji Helper provides a terminal UI for local glove firmware packages.
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
	app         *tview.Application
	cli         cli
	devices     []device
	selected    map[string]bool
	packagePath string
	pkg         *firmwarePackage
	flashing    bool
	failed      bool
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
	u.configure()
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

func (u *ui) configure() {
	u.app.EnablePaste(true)
	u.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			if !u.flashing {
				u.app.Stop()
			}
			return nil
		}
		return event
	})
}

func (u *ui) install() {
	u.flashing = true
	log := panel("Live output", "").SetScrollable(true).SetChangedFunc(func() { u.app.Draw() })
	targets := u.targets()
	states := make([]string, len(targets))
	for i := range states {
		states[i] = "Waiting"
	}
	summary := panel("Device progress", "")
	update := func() {
		var lines []string
		for i, d := range targets {
			lines = append(lines, fmt.Sprintf("%-12s %s glove / %s", states[i], d.Side, d.SN))
		}
		completed := 0
		current := 0
		for i, state := range states {
			if state == "Completed" {
				completed++
			}
			if state == "Installing" || state == "Failed" {
				current = i
			}
		}
		summary.SetTitle(fmt.Sprintf(" Device progress · %d/%d completed ", completed, len(targets)))
		summary.SetText(strings.Join(lines, "\n")).ScrollTo(current, 0)
	}
	update()
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(summary, 6, 0, false).AddItem(log, 0, 1, true)
	root := u.workspace(3, "Installing · keep gloves connected", "↑↓ Scroll  ← Devices / → Output  End Follow live  Quit locked during install", body, log)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyLeft {
			u.app.SetFocus(summary)
			return nil
		}
		if e.Key() == tcell.KeyRight {
			u.app.SetFocus(log)
			return nil
		}
		if e.Key() == tcell.KeyTab {
			u.toggleReportFocus(log, summary)
			return nil
		}
		return e
	})
	go func() {
		failed := false
		for i, d := range targets {
			u.app.QueueUpdateDraw(func() { states[i] = "Installing"; update() })
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
				u.app.QueueUpdateDraw(func() {
					states[i] = "Failed"
					for j := i + 1; j < len(states); j++ {
						states[j] = "Not started"
					}
					update()
				})
				break
			}
			u.app.QueueUpdateDraw(func() { states[i] = "Completed"; update() })
		}
		if !failed {
			fmt.Fprintln(log, "\nWuji CLI completed. Review each report above for ok/skipped status.")
		}
		u.app.QueueUpdateDraw(func() {
			u.flashing = false
			u.failed = failed
			title := "Installation complete"
			if failed {
				title = "Installation stopped · review the failed glove"
			}
			buttons := u.actionForm().AddButton("Done", u.app.Stop)
			body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(summary, 6, 0, false).AddItem(log, 0, 1, true).AddItem(buttons, 3, 0, false)
			root := u.workspace(3, title, "↑↓ / PgUp/PgDn Scroll  ← Devices / → Output  Enter / Q Done", body, log)
			root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyLeft {
					u.app.SetFocus(summary)
					return nil
				}
				if e.Key() == tcell.KeyRight {
					u.app.SetFocus(log)
					return nil
				}
				if e.Key() == tcell.KeyTab {
					u.toggleReportFocus(log, summary)
					return nil
				}
				if e.Key() == tcell.KeyEnter || e.Rune() == 'q' {
					u.app.Stop()
					return nil
				}
				return e
			})
		})
	}()
}

func (u *ui) toggleReportFocus(log, summary *tview.TextView) {
	if u.app.GetFocus() == log {
		u.app.SetFocus(summary)
	} else {
		u.app.SetFocus(log)
	}
}
