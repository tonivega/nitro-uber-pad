package ui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nitrobutton.com/nitro-uber-pad/internal/config"
	"nitrobutton.com/nitro-uber-pad/internal/document"
	"nitrobutton.com/nitro-uber-pad/internal/script"
)

// showRecent shows the last (up to 100) opened files, paginated.
func (a *App) showRecent() {
	const per = 20
	items := append([]string(nil), a.cfg.Recent...)
	page, offset := 0, 0
	var cur []string
	var dlg fyne.Window
	label := widget.NewLabel("")
	list := widget.NewList(
		func() int { return len(cur) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(cur) {
				return
			}
			mark := ""
			if isURL(cur[i]) {
				mark = "  (URL)"
			} else if _, err := os.Stat(cur[i]); err != nil {
				mark = "  (missing)"
			}
			o.(*widget.Label).SetText(fmt.Sprintf("%3d. %s%s", offset+i+1, cur[i], mark))
		})
	pages := func() int {
		n := (len(items) + per - 1) / per
		if n == 0 {
			n = 1
		}
		return n
	}
	refresh := func() {
		if page >= pages() {
			page = pages() - 1
		}
		if page < 0 {
			page = 0
		}
		offset = page * per
		cur = items[offset:min(offset+per, len(items))]
		label.SetText(fmt.Sprintf("Page %d / %d  (%d files)", page+1, pages(), len(items)))
		list.UnselectAll()
		list.Refresh()
	}
	list.OnSelected = func(i widget.ListItemID) {
		if i < 0 || i >= len(cur) {
			return
		}
		p := cur[i]
		if dlg != nil {
			dlg.Close()
		}
		if err := a.OpenFile(p); err != nil {
			dialog.ShowError(err, a.win)
		}
	}
	prev := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		if page > 0 {
			page--
			refresh()
		}
	})
	next := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		if page < pages()-1 {
			page++
			refresh()
		}
	})
	clearBtn := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Clear recent files", "Remove all entries from the recent list?", func(ok bool) {
			if !ok {
				return
			}
			a.cfg.Recent = nil
			items = nil
			a.saveConfig()
			page = 0
			refresh()
		}, a.win)
	})
	refresh()
	top := container.NewHBox(prev, label, next, layout.NewSpacer(), clearBtn)
	dlg = a.showWindow("recent", "Recent files and URLs (last 100)", container.NewBorder(top, nil, nil, nil, list), fyne.NewSize(820, 560))
}

// showHistory shows pending changes and the .nup.log history of the current file.
func (a *App) showHistory() {
	ed := a.current()
	if ed == nil {
		return
	}
	var b strings.Builder
	pend := ed.doc.Pending()
	if ed.dirty || len(pend) > 0 {
		b.WriteString("Unsaved changes (applied on save):\n")
		if ed.dirty {
			fmt.Fprintf(&b, "    page %d  (currently being edited)\n", ed.page+1)
		}
		for _, c := range pend {
			fmt.Fprintf(&b, "    page %d  offset [%d,%d)  %d -> %d bytes\n", c.Page+1, c.Start, c.End, c.End-c.Start, c.NewLen)
		}
		b.WriteString("\n")
	}
	title := "Change history"
	if ed.doc.IsNew() {
		b.WriteString("This document has not been saved yet; history is recorded on save.\n")
	} else {
		lp := document.LogPath(ed.doc.Path())
		title += " - " + lp
		log, err := document.ReadLogTail(lp, 256*1024)
		switch {
		case errors.Is(err, os.ErrNotExist):
			b.WriteString("No saved history yet.\n")
		case err != nil:
			fmt.Fprintf(&b, "Error reading %s: %v\n", lp, err)
		default:
			b.WriteString("Saved history:\n")
			b.WriteString(log)
		}
	}
	lbl := widget.NewLabelWithStyle(b.String(), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	a.showWindow("history", title, container.NewScroll(lbl), fyne.NewSize(860, 560))
}

type kvRow struct {
	key, val *widget.Entry
}

// showSettings edits variables, scripted actions/shortcuts and general options.
func (a *App) showSettings() {
	// ---- variables ----
	var rows []*kvRow
	varsBox := container.NewVBox()
	var rebuildVars func()
	addRow := func(k, v string) {
		ke := widget.NewEntry()
		ke.SetPlaceHolder("key")
		ke.SetText(k)
		ve := widget.NewMultiLineEntry()
		ve.SetPlaceHolder("value")
		ve.SetMinRowsVisible(2)
		ve.SetText(v)
		rows = append(rows, &kvRow{ke, ve})
	}
	keys := make([]string, 0, len(a.cfg.Vars))
	for k := range a.cfg.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		addRow(k, a.cfg.Vars[k])
	}
	rebuildVars = func() {
		varsBox.Objects = nil
		for _, r := range rows {
			r := r
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				for i, x := range rows {
					if x == r {
						rows = append(rows[:i], rows[i+1:]...)
						break
					}
				}
				rebuildVars()
			})
			varsBox.Add(container.NewBorder(nil, nil, nil, del, container.NewGridWithColumns(2, r.key, r.val)))
		}
		varsBox.Refresh()
	}
	rebuildVars()
	addVar := widget.NewButtonWithIcon("Add variable", theme.ContentAddIcon(), func() {
		addRow("", "")
		rebuildVars()
	})
	varsHelp := widget.NewLabel("Variables are available to scripts via config.get(key) / config.set(key, value).")
	varsTab := container.NewBorder(container.NewVBox(varsHelp, container.NewHBox(addVar)), nil, nil, nil, container.NewVScroll(varsBox))

	// ---- actions ----
	actions := append([]config.Action(nil), a.cfg.Actions...)
	actBox := container.NewVBox()
	var rebuildActs func()
	rebuildActs = func() {
		actBox.Objects = nil
		for i := range actions {
			i := i
			act := actions[i]
			sc := act.Shortcut
			if sc == "" {
				sc = "-"
			}
			tb := ""
			if act.Toolbar {
				tb = "  [toolbar]"
			}
			info := widget.NewLabel(fmt.Sprintf("%s  (%s)  menu: %s  shortcut: %s%s", act.Label, act.ID, act.Menu, sc, tb))
			edit := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
				a.editAction(actions[i], func(n config.Action) {
					actions[i] = n
					rebuildActs()
				})
			})
			up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
				if i > 0 {
					actions[i-1], actions[i] = actions[i], actions[i-1]
					rebuildActs()
				}
			})
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				actions = append(actions[:i], actions[i+1:]...)
				rebuildActs()
			})
			actBox.Add(container.NewBorder(nil, nil, nil, container.NewHBox(up, edit, del), info))
		}
		actBox.Refresh()
	}
	rebuildActs()
	addAct := widget.NewButtonWithIcon("Add action", theme.ContentAddIcon(), func() {
		a.editAction(config.Action{Menu: "Tools", Script: "ui.notify(\"Hello from \" .. editor.title())"}, func(n config.Action) {
			actions = append(actions, n)
			rebuildActs()
		})
	})
	resetAct := widget.NewButtonWithIcon("Reset to defaults", theme.ViewRefreshIcon(), func() {
		dialog.ShowConfirm("Reset actions", "Replace all actions with the built-in defaults?", func(ok bool) {
			if ok {
				actions = config.DefaultActions()
				rebuildActs()
			}
		}, a.win)
	})
	actHelp := widget.NewLabel("Shortcuts: Mod (Ctrl / Cmd on macOS), Ctrl, Shift, Alt, Super + key, e.g. Mod+Shift+S")
	actsTab := container.NewBorder(container.NewVBox(actHelp, container.NewHBox(addAct, resetAct)), nil, nil, nil, container.NewVScroll(actBox))

	// ---- general ----
	ps := widget.NewEntry()
	ps.SetText(strconv.FormatInt(a.cfg.PageSize, 10))
	to := widget.NewSelectEntry([]string{"5", "10", "15", "30", "60", "120", "300"})
	to.SetText(strconv.Itoa(a.cfg.HTTPTimeout))
	general := widget.NewForm(
		widget.NewFormItem("Page size (bytes)", ps),
		widget.NewFormItem("HTTP timeout (seconds)", to),
		widget.NewFormItem("Config file", widget.NewLabel(config.Path())),
	)
	genTab := container.NewVBox(general,
		widget.NewLabel("Page size applies to files opened afterwards (minimum 4096)."),
		widget.NewLabel("HTTP timeout is the default for http.get / http.post / http.request in scripts (scripts can override it)."))

	tabs := container.NewAppTabs(
		container.NewTabItem("Variables", varsTab),
		container.NewTabItem("Actions & Shortcuts", actsTab),
		container.NewTabItem("Scripted UI", a.scriptedUISettings()),
		container.NewTabItem("General", genTab),
	)
	saved := false
	save := func(ok bool) {
		if !ok {
			return
		}
		size, err := strconv.ParseInt(strings.TrimSpace(ps.Text), 10, 64)
		if err != nil || size < 4096 {
			dialog.ShowError(errors.New("page size must be a number >= 4096; settings not saved"), a.win)
			return
		}
		timeout, err := strconv.Atoi(strings.TrimSpace(to.Text))
		if err != nil || timeout <= 0 {
			dialog.ShowError(errors.New("HTTP timeout must be a number of seconds > 0; settings not saved"), a.win)
			return
		}
		vars := map[string]string{}
		for _, r := range rows {
			if k := strings.TrimSpace(r.key.Text); k != "" {
				vars[k] = r.val.Text
			}
		}
		// A settings edit must not silently steal a runtime shortcut or ID.
		for _, runtimeAction := range a.scriptActions {
			for _, configured := range actions {
				if configured.ID == runtimeAction.ID {
					dialog.ShowError(fmt.Errorf("ID %q belongs to a runtime action; remove it in Scripted UI first", configured.ID), a.win)
					return
				}
				left, le := ParseShortcut(configured.Shortcut)
				right, re := ParseShortcut(runtimeAction.Shortcut)
				if le == nil && re == nil && left.ShortcutName() == right.ShortcutName() {
					dialog.ShowError(fmt.Errorf("shortcut %q belongs to runtime action %q", configured.Shortcut, runtimeAction.ID), a.win)
					return
				}
			}
		}
		a.cfg.Vars = vars
		a.cfg.Actions = actions
		a.cfg.PageSize = size
		a.cfg.HTTPTimeout = timeout
		a.saveConfig()
		a.rebuildActions()
		a.Notify("Settings saved")
		saved = true
	}
	a.showConfirmWindow("settings", "Settings", "Save", tabs, fyne.NewSize(920, 640), func() bool {
		saved = false
		save(true)
		return saved
	})
}

// editAction shows a form to edit one scripted action.
func (a *App) editAction(act config.Action, done func(config.Action)) {
	id := widget.NewEntry()
	id.SetText(act.ID)
	label := widget.NewEntry()
	label.SetText(act.Label)
	menu := widget.NewEntry()
	menu.SetText(act.Menu)
	menu.SetPlaceHolder("File, Edit, View, Tools...")
	sc := widget.NewEntry()
	sc.SetText(act.Shortcut)
	sc.SetPlaceHolder("e.g. Mod+Shift+S")
	tb := widget.NewCheck("Show in toolbar", nil)
	tb.SetChecked(act.Toolbar)
	src := widget.NewMultiLineEntry()
	src.TextStyle = fyne.TextStyle{Monospace: true}
	src.Wrapping = fyne.TextWrapOff
	src.SetText(act.Script)
	helpBtn := widget.NewButtonWithIcon("Scripting Help & Snippets", theme.HelpIcon(), a.showScriptHelp)
	form := widget.NewForm(
		widget.NewFormItem("ID", id),
		widget.NewFormItem("Label", label),
		widget.NewFormItem("Menu", menu),
		widget.NewFormItem("Shortcut", sc),
		widget.NewFormItem("", tb),
	)
	top := container.NewVBox(form, container.NewHBox(widget.NewLabel("Script (Lua):"), layout.NewSpacer(), helpBtn))
	content := container.NewBorder(top, nil, nil, nil, src)
	accepted := false
	submit := func(ok bool) {
		if !ok {
			return
		}
		if strings.TrimSpace(label.Text) == "" {
			dialog.ShowError(errors.New("label is required"), a.win)
			return
		}
		shortcut := strings.TrimSpace(sc.Text)
		if shortcut != "" {
			if _, err := ParseShortcut(shortcut); err != nil {
				dialog.ShowError(err, a.win)
				return
			}
		}
		idv := strings.TrimSpace(id.Text)
		if idv == "" {
			idv = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(label.Text), " ", "_"))
		}
		done(config.Action{
			ID:       idv,
			Label:    strings.TrimSpace(label.Text),
			Menu:     strings.TrimSpace(menu.Text),
			Shortcut: shortcut,
			Toolbar:  tb.Checked,
			Script:   src.Text,
		})
		accepted = true
	}
	a.showConfirmWindow("edit_action", "Edit action", "OK", content, fyne.NewSize(780, 620), func() bool {
		accepted = false
		submit(true)
		return accepted
	})
}

// showScriptHelp shows the scripting API reference and advanced snippets.
// The texts are shown in entries so they can be selected and copied.
func (a *App) showScriptHelp() {
	mk := func(s string) fyne.CanvasObject {
		e := widget.NewMultiLineEntry()
		e.TextStyle = fyne.TextStyle{Monospace: true}
		e.Wrapping = fyne.TextWrapOff
		e.SetText(s)
		copyBtn := widget.NewButtonWithIcon("Copy all", theme.ContentCopyIcon(), func() {
			a.win.Clipboard().SetContent(s)
			a.Notify("Copied to clipboard")
		})
		return container.NewBorder(container.NewHBox(layout.NewSpacer(), copyBtn), nil, nil, nil, e)
	}
	tabs := container.NewAppTabs(
		container.NewTabItem("API reference", mk(script.APIHelp)),
		container.NewTabItem("Snippets (advanced)", mk(script.Snippets)),
	)
	a.showWindow("script_help", "Scripting Help & Snippets", tabs, fyne.NewSize(900, 640))
}

// showAbout shows version and contact information.
func (a *App) showAbout() {
	site, _ := url.Parse("https://www.nitrobutton.com")
	mail, _ := url.Parse("mailto:nfn@nitrobutton.com?subject=Nitro%20UberPad")
	content := container.NewVBox(
		widget.NewLabelWithStyle(config.AppName+" "+config.Version, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("A multiplatform, scriptable text editor for files of any size."),
		widget.NewHyperlink("https://www.nitrobutton.com", site),
		widget.NewLabel("For requests, comments or suggestions:"),
		widget.NewHyperlink("nfn@nitrobutton.com", mail),
		widget.NewLabel("Configuration: "+config.Path()),
	)
	a.showWindow("about", "About", content, fyne.NewSize(480, 300))
}
