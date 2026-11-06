package ui

import (
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

var namedKeys = map[string]fyne.KeyName{
	"enter": fyne.KeyReturn, "return": fyne.KeyReturn, "tab": fyne.KeyTab,
	"space": fyne.KeySpace, "esc": fyne.KeyEscape, "escape": fyne.KeyEscape,
	"up": fyne.KeyUp, "down": fyne.KeyDown, "left": fyne.KeyLeft, "right": fyne.KeyRight,
	"home": fyne.KeyHome, "end": fyne.KeyEnd, "pageup": fyne.KeyPageUp, "pagedown": fyne.KeyPageDown,
	"delete": fyne.KeyDelete, "backspace": fyne.KeyBackspace, "insert": fyne.KeyInsert,
}

// ParseShortcut parses strings like "Mod+Shift+S", "Ctrl+Alt+F5".
// "Mod" is Ctrl on Windows/Linux and Cmd on macOS.
func ParseShortcut(s string) (*desktop.CustomShortcut, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty shortcut")
	}
	var mod fyne.KeyModifier
	var key fyne.KeyName
	for _, p := range strings.Split(s, "+") {
		t := strings.TrimSpace(p)
		switch strings.ToLower(t) {
		case "mod", "cmdorctrl", "primary":
			mod |= fyne.KeyModifierShortcutDefault
		case "ctrl", "control":
			mod |= fyne.KeyModifierControl
		case "shift":
			mod |= fyne.KeyModifierShift
		case "alt", "option", "opt":
			mod |= fyne.KeyModifierAlt
		case "super", "cmd", "command", "win", "meta":
			mod |= fyne.KeyModifierSuper
		case "":
		default:
			if key != "" {
				return nil, fmt.Errorf("more than one key in %q", s)
			}
			if k, ok := namedKeys[strings.ToLower(t)]; ok {
				key = k
			} else {
				key = fyne.KeyName(strings.ToUpper(t))
			}
		}
	}
	if key == "" {
		return nil, fmt.Errorf("no key in %q", s)
	}
	if mod == 0 {
		return nil, fmt.Errorf("%q needs a modifier (Mod, Ctrl, Shift, Alt, Super)", s)
	}
	return &desktop.CustomShortcut{KeyName: key, Modifier: mod}, nil
}

// shortcutEntry is a multi-line entry that forwards configured shortcuts to the app
// before the entry handles them itself.
type shortcutEntry struct {
	widget.Entry
	app *App
}

func newShortcutEntry(a *App) *shortcutEntry {
	e := &shortcutEntry{app: a}
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapOff
	e.TextStyle = fyne.TextStyle{Monospace: true}
	e.ExtendBaseWidget(e)
	return e
}

func (e *shortcutEntry) TypedShortcut(s fyne.Shortcut) {
	if e.app.handleShortcut(s) {
		return
	}
	e.Entry.TypedShortcut(s)
}
