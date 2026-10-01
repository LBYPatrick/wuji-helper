package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestInteractiveWorkflow(t *testing.T) {
	for _, scenario := range []struct {
		action        string
		width, height int
	}{
		{"install", 140, 40}, {"cancel", 140, 40}, {"failure", 140, 40}, {"install", 80, 24},
	} {
		action := scenario.action
		t.Run(fmt.Sprintf("%s-%dx%d", action, scenario.width, scenario.height), func(t *testing.T) {
			c, trace := fakeCLI(t)
			if action == "failure" {
				t.Setenv("WUJI_HELPER_TEST_FAIL", "1")
				t.Setenv("WUJI_HELPER_TEST_TWO", "1")
			}
			path := testZIP(t, map[string]string{"manifest.json": `{"version":"0.10.1"}`, "firmware.bin": "firmware"})
			screen := tcell.NewSimulationScreen("UTF-8")
			app := tview.NewApplication().SetScreen(screen)
			screen.SetSize(scenario.width, scenario.height)
			u := &ui{app: app, cli: c, selected: map[string]bool{}}
			u.configure()
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
			waitText("left glove  /  G1")
			screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
			if action == "failure" {
				waitText("right glove  /  G2")
				key(tcell.KeyDown)
				screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
			}
			key(tcell.KeyEnter)
			waitText("Choose your firmware package")
			for _, r := range path {
				screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
			}
			key(tcell.KeyDown)
			key(tcell.KeyEnter)
			waitText("I reviewed the targets and firmware")
			waitText("left glove / G1")
			if _, err := os.Stat(trace); !os.IsNotExist(err) {
				t.Fatal("flashed before confirmation")
			}
			if action == "cancel" {
				key(tcell.KeyDown)
				key(tcell.KeyRight)
				key(tcell.KeyEnter)
			} else {
				screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
				key(tcell.KeyDown)
				key(tcell.KeyEnter)
				if action == "failure" {
					waitText("STOPPED:")
				} else {
					waitText("Wuji CLI completed.")
				}
				waitText("Done")
				key(tcell.KeyLeft)
				key(tcell.KeyRight)
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

// exerciseUI starts the real event loop; all inspection stays on that loop.
func exerciseUI(t *testing.T, devices []device) (*ui, func(tcell.Key, rune), func(string), func() string) {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	app := tview.NewApplication().SetScreen(screen)
	screen.SetSize(80, 24)
	u := &ui{app: app, devices: devices, selected: map[string]bool{}}
	u.configure()
	u.choose()
	done := make(chan error, 1)
	go func() { done <- app.Run() }()
	t.Cleanup(func() { app.Stop(); <-done; u.pkg.cleanup() })
	snapshot := func() string {
		var result string
		app.QueueUpdateDraw(func() {
			cells, width, _ := screen.GetContents()
			var b strings.Builder
			for i, cell := range cells {
				if len(cell.Runes) > 0 {
					b.WriteRune(cell.Runes[0])
				} else {
					b.WriteRune(' ')
				}
				if (i+1)%width == 0 {
					b.WriteByte('\n')
				}
			}
			result = b.String()
		})
		return result
	}
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(snapshot(), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("missing %q:\n%s", want, snapshot())
	}
	key := func(k tcell.Key, r rune) { screen.PostEventWait(tcell.NewEventKey(k, r, tcell.ModNone)) }
	return u, key, wait, snapshot
}

func TestSelectionAndPackageRecovery(t *testing.T) {
	devices := []device{
		{SN: "G1", Kind: "wuji_glove", Side: "left", Version: "0.11.0", Transport: "usb"},
		{SN: "G2", Kind: "wuji_glove", Side: "right", Version: "0.11.0", Transport: "network"},
		{SN: "offline", Problem: "Connection timed out"},
	}
	u, key, wait, snapshot := exerciseUI(t, devices)
	wait("Serial: G1")
	key(tcell.KeyEnter, 0)
	wait("Select at least one ready glove")
	key(tcell.KeyRune, 'a')
	wait("2 selected  /  2 ready  /  1 unavailable")
	key(tcell.KeyDown, 0)
	key(tcell.KeyDown, 0)
	wait("Connection timed out")
	key(tcell.KeyRune, ' ')
	wait("2 selected  /  2 ready  /  1 unavailable")
	key(tcell.KeyEnter, 0)
	wait("Choose your firmware package")
	key(tcell.KeyRune, '/')
	key(tcell.KeyEnter, 0)
	wait("Could not use this package")
	key(tcell.KeyEscape, 0)
	wait("2 selected  /  2 ready  /  1 unavailable")
	key(tcell.KeyEnter, 0)
	wait("OTA ZIP")
	u.app.QueueUpdateDraw(func() {
		if u.packagePath != "/" {
			t.Errorf("lost package path: %q", u.packagePath)
		}
	})
	key(tcell.KeyCtrlU, 0)
	path := testZIP(t, map[string]string{"manifest.json": `{"version":"0.10.1"}`, "firmware.bin": "firmware"})
	for _, r := range path {
		key(tcell.KeyRune, r)
	}
	key(tcell.KeyEnter, 0)
	wait("I reviewed the targets and firmware")
	t.Log("Review at 80x24:\n" + snapshot())
	// An unchecked review skips the disabled install button and goes back.
	key(tcell.KeyTab, 0)
	key(tcell.KeyEnter, 0)
	wait("Choose your firmware package")
	key(tcell.KeyEnter, 0)
	wait("I reviewed the targets and firmware")
	key(tcell.KeyEscape, 0)
	wait("Choose your firmware package")
}

func TestEmptyDiscoveryLayout(t *testing.T) {
	_, _, wait, snapshot := exerciseUI(t, nil)
	wait("No devices found")
	wait("Rescan")
	t.Log("Empty state at 80x24:\n" + snapshot())
}

func TestArrowNavigation(t *testing.T) {
	u, key, wait, _ := exerciseUI(t, []device{
		{SN: "G1", Kind: "wuji_glove", Side: "left", Version: "0.11.0"},
		{SN: "G2", Kind: "wuji_glove", Side: "right", Version: "0.11.0"},
	})
	waitFocus := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var got string
		for time.Now().Before(deadline) {
			u.app.QueueUpdateDraw(func() {
				switch p := u.app.GetFocus().(type) {
				case *tview.Button:
					got = p.GetLabel()
				case *tview.InputField:
					got = "path"
				case *tview.Checkbox:
					got = "confirmation"
				case *tview.List:
					got = "gloves"
				}
			})
			if got == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("focus=%q, want %q", got, want)
	}
	wait("Serial: G1")
	key(tcell.KeyRune, ' ')
	key(tcell.KeyDown, 0)
	wait("Serial: G2")
	key(tcell.KeyDown, 0)
	waitFocus("Continue")
	key(tcell.KeyRight, 0)
	waitFocus("Rescan")
	key(tcell.KeyRight, 0)
	waitFocus("Quit")
	key(tcell.KeyUp, 0)
	waitFocus("gloves")
	key(tcell.KeyDown, 0)
	waitFocus("Continue")
	key(tcell.KeyEnter, 0)
	wait("Choose your firmware package")
	key(tcell.KeyRune, 'a')
	key(tcell.KeyRune, 'c')
	key(tcell.KeyLeft, 0)
	key(tcell.KeyRune, 'b')
	wait("abc")
	key(tcell.KeyRight, 0)
	key(tcell.KeyRune, 'd')
	wait("abcd")
	key(tcell.KeyDown, 0)
	waitFocus("Review package")
	key(tcell.KeyRight, 0)
	waitFocus("Back to gloves")
	key(tcell.KeyLeft, 0)
	waitFocus("Review package")
	key(tcell.KeyUp, 0)
	waitFocus("path")
	key(tcell.KeyCtrlU, 0)
	path := testZIP(t, map[string]string{"manifest.json": `{"version":"0.10.1"}`, "firmware.bin": "firmware"})
	for _, r := range path {
		key(tcell.KeyRune, r)
	}
	key(tcell.KeyDown, 0)
	key(tcell.KeyEnter, 0)
	wait("I reviewed the targets and firmware")
	key(tcell.KeyDown, 0)
	waitFocus("Back to package") // Disabled Install is skipped.
	key(tcell.KeyLeft, 0)
	waitFocus("Back to package")
	key(tcell.KeyUp, 0)
	waitFocus("confirmation")
	key(tcell.KeyRune, ' ')
	key(tcell.KeyDown, 0)
	waitFocus("Install firmware")
	key(tcell.KeyRight, 0)
	waitFocus("Back to package")
	key(tcell.KeyRight, 0)
	waitFocus("Cancel")
	key(tcell.KeyUp, 0)
	waitFocus("confirmation")
	key(tcell.KeyEscape, 0)
	wait("Choose your firmware package")
}
