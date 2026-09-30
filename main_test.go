package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestInteractiveWorkflow(t *testing.T) {
	for _, action := range []string{"install", "cancel", "failure"} {
		t.Run(action, func(t *testing.T) {
			c, trace := fakeCLI(t)
			if action == "failure" {
				t.Setenv("BOUNCER_TEST_FAIL", "1")
				t.Setenv("BOUNCER_TEST_TWO", "1")
			}
			path := testZIP(t, map[string]string{"manifest.json": `{"version":"0.10.1"}`, "firmware.bin": "firmware"})
			screen := tcell.NewSimulationScreen("UTF-8")
			app := tview.NewApplication().SetScreen(screen)
			screen.SetSize(140, 40)
			u := &ui{app: app, cli: c, selected: map[string]bool{}}
			u.scan()
			done := make(chan error, 1)
			go func() { done <- app.Run() }()
			stopped := false
			defer func() {
				if !stopped {
					app.Stop()
					<-done
				}
				u.pkg.cleanup()
			}()
			waitText := func(want string) {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				var last string
				for time.Now().Before(deadline) {
					app.QueueUpdateDraw(func() {
						cells, _, _ := screen.GetContents()
						var b strings.Builder
						for _, cell := range cells {
							if len(cell.Runes) > 0 {
								b.WriteRune(cell.Runes[0])
							}
						}
						last = b.String()
					})
					if strings.Contains(last, want) {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("screen never showed %q; screen: %s", want, last)
			}
			key := func(k tcell.Key) { screen.PostEventWait(tcell.NewEventKey(k, 0, tcell.ModNone)) }
			waitText("left glove | SN G1 | firmware 0.11.0")
			screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
			key(tcell.KeyTab)
			if action == "failure" {
				waitText("right glove | SN G2 | firmware 0.11.0")
				screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
				key(tcell.KeyTab)
			}
			key(tcell.KeyEnter)
			waitText("OTA ZIP path")
			for _, r := range path {
				screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
			}
			key(tcell.KeyTab)
			key(tcell.KeyEnter)
			waitText("Confirm & install")
			waitText("left glove   SN G1   current: 0.11.0")
			if _, err := os.Stat(trace); !os.IsNotExist(err) {
				t.Fatal("flashed before confirmation")
			}
			if action == "cancel" {
				key(tcell.KeyTab)
				key(tcell.KeyTab)
				key(tcell.KeyEnter)
			} else {
				key(tcell.KeyEnter)
				if action == "failure" {
					waitText("STOPPED:")
				} else {
					waitText("Wuji CLI completed.")
				}
				waitText("Done")
				key(tcell.KeyEnter)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
				stopped = true
			case <-time.After(5 * time.Second):
				t.Fatal("TUI did not exit")
			}
			if action == "cancel" {
				if _, err := os.Stat(trace); !os.IsNotExist(err) {
					t.Fatal("cancel flashed firmware")
				}
			}
			if action == "failure" {
				args, err := os.ReadFile(trace)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(args), "G2") {
					t.Fatal("flashed the second glove after a failure")
				}
			}
			if u.failed != (action == "failure") {
				t.Fatalf("failure state=%v", u.failed)
			}
		})
	}
}
