package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"nitrobutton.com/nitro-uber-pad/internal/config"
	"nitrobutton.com/nitro-uber-pad/internal/script"
)

func sameDocumentPath(a, b string) bool {
	if a == "" || b == "" { return false }
	a, ea := filepath.Abs(config.ExpandHome(a))
	b, eb := filepath.Abs(config.ExpandHome(b))
	if ea != nil || eb != nil { return false }
	if a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b)) { return true }
	sa, ea := os.Stat(a)
	sb, eb := os.Stat(b)
	return ea == nil && eb == nil && os.SameFile(sa, sb)
}

func (a *App) TabInfo() []script.Tab {
	out := make([]script.Tab, 0, len(a.tabs.Items))
	for i, it := range a.tabs.Items {
		if ed := a.editors[it]; ed != nil {
			out = append(out, script.Tab{Index: i+1, Path: ed.doc.Path(), Title: ed.title,
				Modified: ed.Modified(), Selected: a.tabs.Selected() == it})
		}
	}
	return out
}

func (a *App) SelectTab(path string, index int) error {
	if path == "" {
		if index < 1 || index > len(a.tabs.Items) { return fmt.Errorf("tab index out of range") }
		a.tabs.Select(a.tabs.Items[index-1])
		return nil
	}
	for _, it := range a.tabs.Items {
		if ed := a.editors[it]; ed != nil && sameDocumentPath(ed.doc.Path(), path) {
			a.tabs.Select(it)
			return nil
		}
	}
	return fmt.Errorf("no open tab for %q", path)
}

func (a *App) ScriptActions() []config.Action {
	return append([]config.Action(nil), a.scriptActions...)
}

func (a *App) SetScriptAction(act config.Action) error {
	act.ID = strings.TrimSpace(act.ID)
	act.Label = strings.TrimSpace(act.Label)
	if act.ID == "" || act.Label == "" { return fmt.Errorf("action id and label are required") }
	var shortcut string
	if strings.TrimSpace(act.Shortcut) != "" {
		sc, err := ParseShortcut(act.Shortcut)
		if err != nil { return err }
		shortcut = sc.ShortcutName()
		for _, builtin := range inspectionActions() {
			if b, err := ParseShortcut(builtin.Shortcut); err == nil && b.ShortcutName() == shortcut {
				return fmt.Errorf("shortcut is reserved for %s", builtin.Label)
			}
		}
	}
	for _, builtin := range inspectionActions() {
		if act.ID == builtin.ID { return fmt.Errorf("reserved built-in action ID") }
	}
	all := append(append([]config.Action(nil), a.cfg.Actions...), a.scriptActions...)
	for i, old := range all {
		if i < len(a.cfg.Actions) && old.ID == act.ID {
			return fmt.Errorf("%q belongs to configured actions; use Settings to edit it", act.ID)
		}
		if i >= len(a.cfg.Actions) && old.ID == act.ID { continue }
		if shortcut != "" && old.Shortcut != "" {
			if sc, err := ParseShortcut(old.Shortcut); err == nil && sc.ShortcutName() == shortcut {
				return fmt.Errorf("shortcut already belongs to %q", old.Label)
			}
		}
	}
	for i := range a.scriptActions {
		if a.scriptActions[i].ID == act.ID {
			a.scriptActions[i] = act
			a.rebuildActions()
			a.Log("ui", fmt.Sprintf("Updated runtime action %q (menu=%q shortcut=%q toolbar=%t)", act.ID, act.Menu, act.Shortcut, act.Toolbar))
			return nil
		}
	}
	a.scriptActions = append(a.scriptActions, act)
	a.rebuildActions()
	a.Log("ui", fmt.Sprintf("Registered runtime action %q (menu=%q shortcut=%q toolbar=%t)", act.ID, act.Menu, act.Shortcut, act.Toolbar))
	return nil
}

func (a *App) RemoveScriptAction(id string) error {
	for i, act := range a.scriptActions {
		if act.ID == id {
			a.scriptActions = append(a.scriptActions[:i], a.scriptActions[i+1:]...)
			a.rebuildActions()
			a.Log("ui", fmt.Sprintf("Removed runtime action %q", id))
			return nil
		}
	}
	return fmt.Errorf("runtime action %q not found (configured actions are not removable through Lua)", id)
}

// This is intentionally separate from persisted Actions & Shortcuts. Refresh
// takes a new snapshot, including controls materialized by a timer or script.
func (a *App) scriptedUISettings() fyne.CanvasObject {
	box := container.NewVBox()
	var refresh func()
	refresh = func() {
		box.Objects = nil
		for _, act := range a.ScriptActions() {
			act := act
			info := widget.NewLabel(fmt.Sprintf("%s (%s) • menu: %s • shortcut: %s • toolbar: %t", act.Label, act.ID, act.Menu, act.Shortcut, act.Toolbar))
			info.Wrapping = fyne.TextWrapWord
			source := widget.NewMultiLineEntry()
			source.TextStyle = fyne.TextStyle{Monospace: true}
			source.SetMinRowsVisible(2)
			source.SetText(act.Script)
			source.Disable()
			remove := widget.NewButton("Remove", func() {
				_ = a.RemoveScriptAction(act.ID)
				refresh()
			})
			box.Add(container.NewVBox(container.NewBorder(nil, nil, nil, remove, info), source, widget.NewSeparator()))
		}
		if len(a.scriptActions) == 0 { box.Add(widget.NewLabel("No runtime controls. Register one with ui.action{...}.")) }
		box.Refresh()
	}
	refresh()
	help := widget.NewLabel("Materialized by Lua • session-only • separate from configured controls. Use ui.action to update; ui.remove_action to remove.")
	help.Wrapping = fyne.TextWrapWord
	return container.NewBorder(container.NewVBox(help, widget.NewButton("Refresh runtime controls", refresh)), nil, nil, nil, container.NewVScroll(box))
}
