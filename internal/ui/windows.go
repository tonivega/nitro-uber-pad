package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"nitrobutton.com/nitro-uber-pad/internal/config"
)

func isURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func (a *App) savedSize(key string, def fyne.Size) fyne.Size {
	if s, ok := a.cfg.WindowSizes[key]; ok && s.W >= 100 && s.H >= 80 {
		return fyne.NewSize(s.W, s.H)
	}
	return def
}

func (a *App) rememberSize(key string, s fyne.Size) {
	if s.Width < 100 || s.Height < 80 {
		return
	}
	if a.cfg.WindowSizes == nil {
		a.cfg.WindowSizes = map[string]config.WinSize{}
	}
	a.cfg.WindowSizes[key] = config.WinSize{W: s.Width, H: s.Height}
}

func (a *App) parentWin(key string) fyne.Window {
	if w, ok := a.windows[key]; ok {
		return w
	}
	return a.win
}

// openWindow shows content in a real, movable, resizable OS window with a
// native close button. Its size is remembered per key. Reused windows (one
// per key) replace their content when opened again. Escape closes; F3 finds next.
func (a *App) openWindow(key, title string, content fyne.CanvasObject, def fyne.Size, reuse bool, onClose func()) fyne.Window {
	if reuse {
		if w, ok := a.windows[key]; ok {
			w.SetTitle(title)
			w.SetContent(content)
			w.Show()
			w.RequestFocus()
			return w
		}
	}
	w := a.fa.NewWindow(title)
	w.SetContent(content)
	w.Resize(a.savedSize(key, def))
	w.SetOnDropped(a.dropped)
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyEscape:
			w.Close()
		case fyne.KeyF3:
			a.findNext()
		}
	})
	if reuse {
		a.windows[key] = w
	}
	a.openWins[w] = true
	w.SetOnClosed(func() {
		a.rememberSize(key, w.Canvas().Size())
		delete(a.openWins, w)
		if a.windows[key] == w {
			delete(a.windows, key)
			if key == "search" {
				a.searchNext = nil
			}
		}
		if onClose != nil {
			onClose()
		}
		a.saveConfig()
	})
	w.Show()
	return w
}

func (a *App) showWindow(key, title string, content fyne.CanvasObject, def fyne.Size) fyne.Window {
	return a.openWindow(key, title, content, def, true, nil)
}

// showConfirmWindow adds Cancel/OK buttons; the window closes when onOK returns true.
func (a *App) showConfirmWindow(key, title, ok string, content fyne.CanvasObject, def fyne.Size, onOK func() bool) fyne.Window {
	var w fyne.Window
	okb := widget.NewButton(ok, func() {
		if onOK() && w != nil {
			w.Close()
		}
	})
	okb.Importance = widget.HighImportance
	cancel := widget.NewButton("Cancel", func() {
		if w != nil {
			w.Close()
		}
	})
	w = a.showWindow(key, title, container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), cancel, okb), nil, nil, content), def)
	return w
}

func (a *App) Prompt(title, label, def string, cb func(string, bool)) {
	e := widget.NewEntry()
	e.SetText(def)
	done := false
	var w fyne.Window
	finish := func(ok bool) {
		if done {
			return
		}
		done = true
		if w != nil {
			w.Close()
		}
		cb(e.Text, ok)
	}
	e.OnSubmitted = func(string) { finish(true) }
	ok := widget.NewButton("OK", func() { finish(true) })
	ok.Importance = widget.HighImportance
	cancel := widget.NewButton("Cancel", func() { finish(false) })
	body := container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), cancel, ok), nil, nil,
		container.NewVBox(widget.NewLabel(label), e))
	w = a.openWindow("prompt", title, body, fyne.NewSize(440, 170), false, func() { finish(false) })
	w.Canvas().Focus(e)
}

func (a *App) Confirm(title, msg string, cb func(bool)) {
	done := false
	var w fyne.Window
	finish := func(ok bool) {
		if done {
			return
		}
		done = true
		if w != nil {
			w.Close()
		}
		cb(ok)
	}
	label := widget.NewLabel(msg)
	label.Wrapping = fyne.TextWrapWord
	yes := widget.NewButton("Yes", func() { finish(true) })
	yes.Importance = widget.HighImportance
	no := widget.NewButton("No", func() { finish(false) })
	w = a.openWindow("confirm", title, container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), no, yes), nil, nil,
		container.NewVScroll(label)), fyne.NewSize(460, 200), false, func() { finish(false) })
}

func (a *App) Alert(msg string) {
	label := widget.NewLabel(msg)
	label.Wrapping = fyne.TextWrapWord
	var w fyne.Window
	ok := widget.NewButton("OK", func() { w.Close() })
	w = a.openWindow("alert", config.AppName, container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), ok), nil, nil,
		container.NewVScroll(label)), fyne.NewSize(460, 200), false, nil)
}

// dropped opens files (or URLs) dragged from other programs.
func (a *App) dropped(_ fyne.Position, uris []fyne.URI) {
	for _, u := range uris {
		p := u.Path()
		if s := u.Scheme(); s == "http" || s == "https" {
			p = u.String()
		}
		if err := a.OpenFile(p); err != nil {
			a.Notify("Cannot open dropped item: " + err.Error())
		}
	}
	a.win.RequestFocus()
}

// findNext repeats the last search (F3), or opens Find when none is active.
func (a *App) findNext() {
	if a.searchNext != nil {
		a.searchNext()
		return
	}
	a.showSearch(false)
}

type f3Entry struct {
	widget.Entry
	f3 func()
}

func newF3Entry(f func()) *f3Entry {
	e := &f3Entry{f3: f}
	e.ExtendBaseWidget(e)
	return e
}

func (e *f3Entry) TypedKey(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyF3 && e.f3 != nil {
		e.f3()
		return
	}
	e.Entry.TypedKey(ev)
}
