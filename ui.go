package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var (
	canvas  = tcell.NewHexColor(0x11161c)
	surface = tcell.NewHexColor(0x1c2530)
	ink     = tcell.NewHexColor(0xe6edf3)
	muted   = tcell.NewHexColor(0x9cabbc)
	accent  = tcell.NewHexColor(0x83dfc3)
	warning = tcell.NewHexColor(0xf1c57b)
)

func textView(text string) *tview.TextView {
	v := tview.NewTextView().SetText(text).SetTextColor(ink).SetWordWrap(true)
	v.SetBackgroundColor(canvas)
	return v
}

func panel(title, text string) *tview.TextView {
	v := textView(text)
	v.SetBorder(true).SetBorderColor(surface).SetTitle(" "+title+" ").SetTitleColor(muted).SetTitleAlign(tview.AlignLeft).SetBorderPadding(0, 0, 1, 1)
	v.SetFocusFunc(func() { v.SetBorderColor(accent) })
	v.SetBlurFunc(func() { v.SetBorderColor(surface) })
	return v
}

func (u *ui) actionForm() *tview.Form {
	f := tview.NewForm().SetLabelColor(ink).SetFieldBackgroundColor(surface).SetFieldTextColor(ink).
		SetButtonBackgroundColor(surface).SetButtonTextColor(ink).
		SetButtonActivatedStyle(tcell.StyleDefault.Background(accent).Foreground(canvas)).
		SetButtonDisabledStyle(tcell.StyleDefault.Background(canvas).Foreground(muted))
	f.SetBackgroundColor(canvas)
	f.SetBorderPadding(0, 0, 0, 0)
	f.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
			// Horizontal arrows belong to the text cursor while editing a path.
			if _, editing := u.app.GetFocus().(*tview.InputField); editing && (e.Key() == tcell.KeyLeft || e.Key() == tcell.KeyRight) {
				return e
			}
			moveFormFocus(f, e.Key())
			u.app.SetFocus(f)
			return nil
		}
		return e
	})
	return f
}

// moveFormFocus follows Ashley's directional focus: nearest row vertically,
// nearest control on the same row horizontally. Arrows do not wrap at edges.
func moveFormFocus(form *tview.Form, key tcell.Key) {
	type control struct {
		index, x, y int
		focused     bool
	}
	var controls []control
	for i := 0; i < form.GetFormItemCount()+form.GetButtonCount(); i++ {
		var p tview.Primitive
		if i < form.GetFormItemCount() {
			p = form.GetFormItem(i)
		} else {
			button := form.GetButton(i - form.GetFormItemCount())
			if button.IsDisabled() {
				continue
			}
			p = button
		}
		x, y, _, _ := p.GetRect()
		controls = append(controls, control{i, x, y, p.HasFocus()})
	}
	for _, from := range controls {
		if !from.focused {
			continue
		}
		target, best := from.index, int(^uint(0)>>1)
		for _, to := range controls {
			dx, dy := to.x-from.x, to.y-from.y
			score := best
			switch {
			case key == tcell.KeyUp && dy < 0:
				score = -dy*1000 + absDistance(dx)
			case key == tcell.KeyDown && dy > 0:
				score = dy*1000 + absDistance(dx)
			case key == tcell.KeyLeft && dy == 0 && dx < 0:
				score = -dx
			case key == tcell.KeyRight && dy == 0 && dx > 0:
				score = dx
			}
			if score < best {
				target, best = to.index, score
			}
		}
		form.SetFocus(target)
		return
	}
}

func absDistance(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// workspaceFrame caps line length on large terminals without shrinking content
// below the available terminal size.
type workspaceFrame struct{ *tview.Flex }

func (f *workspaceFrame) Draw(screen tcell.Screen) {
	x, y, width, height := f.GetRect()
	screen.Fill(' ', tcell.StyleDefault.Background(canvas).Foreground(ink))
	if width > 104 {
		x += (width - 104) / 2
		width = 104
	}
	if height > 36 {
		y += (height - 36) / 2
		height = 36
	}
	f.Flex.SetRect(x, y, width, height)
	f.Flex.Draw(screen)
}

// workspace keeps navigation and action hints in the same place on every screen.
func (u *ui) workspace(step int, title, hint string, body tview.Primitive, focus tview.Primitive) *tview.Flex {
	header := textView("WUJI  /  FIRMWARE HELPER").SetTextColor(accent)
	steps := []string{"1 Gloves", "2 Package", "3 Review", "4 Install"}
	for i := range steps {
		if i == step {
			steps[i] = "[" + steps[i] + "]"
		}
	}
	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.SetBackgroundColor(canvas)
	root.SetBorderPadding(1, 0, 2, 2)
	root.AddItem(header, 2, 0, false).
		AddItem(textView(strings.Join(steps, "   ")).SetTextColor(muted), 2, 0, false).
		AddItem(textView(title).SetTextColor(accent), 2, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(textView(hint).SetTextColor(muted), 2, 0, false)
	u.app.SetRoot(&workspaceFrame{root}, true).SetFocus(focus)
	return root
}

func (u *ui) scan() {
	body := panel("Finding your gloves", "Checking USB and the local network…\n\nKeep gloves powered on and connected. Device details appear when discovery finishes.")
	u.workspace(0, "Connect your gloves", "Ctrl+C  Quit", body, body)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		devices, err := u.cli.scan(ctx)
		u.app.QueueUpdateDraw(func() {
			if err != nil {
				details := panel("Discovery failed", "Could not reach the Wuji CLI. Check the connection and try again.\n\n"+err.Error())
				actions := u.actionForm().AddButton("Try again", u.scan).AddButton("Quit", u.app.Stop)
				body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(details, 0, 1, false).AddItem(actions, 3, 0, true)
				u.workspace(0, "Let's reconnect", "←→ Actions    Enter Choose    Ctrl+C Quit", body, actions)
				return
			}
			u.devices = devices
			// Keep deliberate selections only while the same glove remains reachable.
			selected := map[string]bool{}
			for _, d := range devices {
				if selectable(d) && u.selected[d.SN] {
					selected[d.SN] = true
				}
			}
			u.selected = selected
			u.choose()
		})
	}()
}

func selectable(d device) bool { return d.Problem == "" && strings.EqualFold(d.Kind, "wuji_glove") }

func (u *ui) targets() []device {
	var result []device
	for _, d := range u.devices {
		if selectable(d) && u.selected[d.SN] {
			result = append(result, d)
		}
	}
	return result
}

func (u *ui) choose() {
	list := tview.NewList().ShowSecondaryText(true).SetWrapAround(false).
		SetMainTextStyle(tcell.StyleDefault.Foreground(ink).Background(canvas)).
		SetSecondaryTextStyle(tcell.StyleDefault.Foreground(muted).Background(canvas)).
		SetSelectedTextColor(canvas).SetSelectedBackgroundColor(accent).SetSelectedFocusOnly(true)
	list.SetBackgroundColor(canvas)
	list.SetBorder(true).SetBorderColor(surface).SetTitle(" Discovered devices ").SetTitleColor(muted).SetTitleAlign(tview.AlignLeft)
	details := panel("Device details", "Connect gloves by USB or the same local network, then press R to rescan.")
	summary := textView("").SetTextColor(muted)
	actions := u.actionForm()
	refresh := func() {
		ready := 0
		for i, d := range u.devices {
			mark := "[-]"
			label := d.SN + "  /  Unavailable"
			secondary := "Cannot select · see device details"
			if selectable(d) {
				ready++
				mark = "[ ]"
				if u.selected[d.SN] {
					mark = "[x]"
				}
				label = fmt.Sprintf("%s glove  /  %s", d.Side, d.SN)
				secondary = fmt.Sprintf("Firmware %s  ·  %s", d.Version, d.Transport)
			}
			list.SetItemText(i, tview.Escape(mark+"  "+label), tview.Escape(secondary))
		}
		summary.SetTextColor(muted).SetText(fmt.Sprintf("%d selected  /  %d ready  /  %d unavailable", len(u.targets()), ready, len(u.devices)-ready))
	}
	for range u.devices {
		list.AddItem("", "", 0, nil)
	}
	showDetails := func(i int) {
		if i < 0 || i >= len(u.devices) {
			return
		}
		d := u.devices[i]
		info := fmt.Sprintf("Serial: %s   /   %s %s\nFirmware: %s", d.SN, d.Transport, d.Address, d.Version)
		if d.Problem != "" {
			info += "\nUnavailable: " + d.Problem
		} else if !selectable(d) {
			info += "\nUnsupported device: " + d.Kind
		} else {
			info += "\nSpace selects this glove. Only selected gloves will be updated."
		}
		details.SetText(info).ScrollToBeginning()
	}
	list.SetChangedFunc(func(i int, _, _ string, _ rune) { showDetails(i) })
	next := func() {
		if len(u.targets()) == 0 {
			summary.SetText("Select at least one ready glove with Space.").SetTextColor(warning)
			return
		}
		u.packageForm(u.packagePath)
	}
	actions.AddButton("Continue", next).AddButton("Rescan", u.scan).AddButton("Quit", u.app.Stop)
	actions.SetCancelFunc(func() { u.app.SetFocus(list) })
	list.SetSelectedFunc(func(int, string, string, rune) { next() })
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(summary, 2, 0, false).
		AddItem(list, 0, 1, true).AddItem(details, 5, 0, false).AddItem(actions, 3, 0, false)
	focus := tview.Primitive(list)
	if len(u.devices) == 0 {
		summary.SetText("No devices found. Power on a glove, connect it, then rescan.")
		focus = actions
		actions.SetFocus(1)
	} else {
		list.SetCurrentItem(0)
		showDetails(0)
	}
	refresh()
	if len(u.devices) == 0 {
		summary.SetText("No devices found. Connect a glove, then choose Rescan.")
	}
	root := u.workspace(0, "Choose the gloves to update", "↑↓ Move / actions  ←→ Buttons  Space Select  Enter Continue  R Rescan", body, focus)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch {
		case e.Key() == tcell.KeyDown && u.app.GetFocus() == list && list.GetCurrentItem() == list.GetItemCount()-1:
			actions.SetFocus(0)
			u.app.SetFocus(actions)
			return nil
		case e.Key() == tcell.KeyUp && actions.HasFocus() && list.GetItemCount() > 0:
			u.app.SetFocus(list)
			return nil
		case e.Rune() == 'r' || e.Rune() == 'R':
			u.scan()
			return nil
		case e.Rune() == 'q':
			u.app.Stop()
			return nil
		case e.Key() == tcell.KeyTab && u.app.GetFocus() == list:
			u.app.SetFocus(actions)
			return nil
		case e.Key() == tcell.KeyBacktab:
			u.app.SetFocus(list)
			return nil
		case e.Rune() == ' ' && u.app.GetFocus() == list:
			i := list.GetCurrentItem()
			if i >= 0 && i < len(u.devices) && selectable(u.devices[i]) {
				sn := u.devices[i].SN
				u.selected[sn] = !u.selected[sn]
				refresh()
			}
			return nil
		case e.Rune() == 'a' || e.Rune() == 'A':
			all := true
			for _, d := range u.devices {
				if selectable(d) && !u.selected[d.SN] {
					all = false
				}
			}
			for _, d := range u.devices {
				if selectable(d) {
					u.selected[d.SN] = !all
				}
			}
			refresh()
			return nil
		}
		return e
	})
}

func (u *ui) packageForm(value string) {
	form := u.actionForm()
	form.AddInputField("OTA ZIP", value, 0, nil, func(s string) { u.packagePath = s })
	u.packagePath = value
	status := panel("Local firmware package", fmt.Sprintf("%d glove(s) selected. Paste the path to the OTA ZIP supplied by Wuji.\n~/, spaces, and quoted paths are supported.\n\nAn older package downgrades firmware. Nothing is installed until you confirm.", len(u.targets())))
	busy := false
	check := func() {
		if busy {
			return
		}
		busy = true
		status.SetText("Checking package…\nCopying the ZIP and verifying its integrity. Your gloves are unchanged.")
		form.GetFormItem(0).(*tview.InputField).SetDisabled(true)
		form.GetButton(0).SetDisabled(true)
		form.GetButton(1).SetDisabled(true)
		input := u.packagePath
		go func() {
			p, err := preparePackage(input)
			u.app.QueueUpdateDraw(func() {
				busy = false
				if err != nil {
					status.SetText("Could not use this package\n\n" + err.Error() + "\n\nEdit the path above and try again.").SetTextColor(warning)
					form.GetFormItem(0).(*tview.InputField).SetDisabled(false)
					form.GetButton(0).SetDisabled(false)
					form.GetButton(1).SetDisabled(false)
					form.SetFocus(0)
					u.app.SetFocus(form)
					return
				}
				u.pkg.cleanup()
				u.pkg = p
				u.review()
			})
		}()
	}
	form.AddButton("Review package", check).AddButton("Back to gloves", u.choose)
	form.SetCancelFunc(func() {
		if !busy {
			u.choose()
		}
	})
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 6, 0, true).AddItem(status, 0, 1, false)
	root := u.workspace(1, "Choose your firmware package", "↑↓ Fields / actions  ←→ Edit / buttons  Enter Choose  Esc Back", body, form)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if busy {
			return nil
		}
		if e.Key() == tcell.KeyEnter && u.app.GetFocus() == form.GetFormItem(0) {
			check()
			return nil
		}
		return e
	})
}

func (u *ui) review() {
	p := u.pkg
	var lines []string
	for _, d := range u.targets() {
		lines = append(lines, fmt.Sprintf("  %s glove / %s\n  %s → %s  ·  %s %s", d.Side, d.SN, d.Version, p.Version, d.Transport, d.Address))
	}
	plan := panel("Package & targets", fmt.Sprintf("%s\nDeclared version: %s  /  %.1f KiB\n\n%s\n\nKeep gloves powered and connected; close other apps using them.\nOlder firmware downgrades. Same-version installs may be skipped.\nGloves reboot after flashing. Installation stops on the first failure.\n\nSource: %s\nSHA-256: %s\nWuji CLI verifies the manifest, firmware digest and compatibility.", filepath.Base(p.Original), p.Version, float64(p.Size)/1024, strings.Join(lines, "\n\n"), p.Original, p.SHA256))
	form := u.actionForm()
	confirmed := false
	form.AddCheckbox("I reviewed the targets and firmware", false, func(checked bool) { confirmed = checked; form.GetButton(0).SetDisabled(!checked) })
	form.GetFormItem(0).(*tview.Checkbox).SetUncheckedString("-").SetCheckedString("x").SetActivatedStyle(tcell.StyleDefault.Foreground(canvas).Background(accent))
	form.AddButton("Install firmware", func() {
		if confirmed {
			u.install()
		}
	}).AddButton("Back to package", func() { u.packageForm(u.packagePath) }).AddButton("Cancel", u.app.Stop)
	form.GetButton(0).SetDisabled(true)
	form.SetCancelFunc(func() { u.packageForm(u.packagePath) })
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(plan, 0, 1, false).AddItem(form, 5, 0, true)
	root := u.workspace(2, fmt.Sprintf("Review installation on %d glove(s)", len(u.targets())), "↑↓ Controls  ←→ Buttons  Space Confirm  PgUp/PgDn Details  Esc Back", body, form)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyPgUp || e.Key() == tcell.KeyPgDn {
			plan.InputHandler()(e, func(tview.Primitive) {})
			return nil
		}
		return e
	})
}
