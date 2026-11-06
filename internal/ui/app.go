// Package ui contains the Fyne user interface.
package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nitrobutton.com/nitro-uber-pad/internal/config"
	"nitrobutton.com/nitro-uber-pad/internal/document"
	"nitrobutton.com/nitro-uber-pad/internal/script"
)

var errNoDoc = errors.New("no active document")

// App is the main application.
type App struct {
	fa              fyne.App
	win             fyne.Window
	cfg             *config.Config
	tabs            *container.DocTabs
	editors         map[*container.TabItem]*Editor
	engine          *script.Engine
	statusLabel     *widget.Label
	noticeLabel     *widget.Button
	events          eventStore
	notices         eventStore
	noticeGeneration atomic.Uint64
	stopped         chan struct{}
	scriptActions   []config.Action
	toolbar         *fyne.Container
	shortcuts       map[string]config.Action
	canvasShortcuts []fyne.Shortcut
	untitled        int
}

// Run starts the editor, opening any files given as arguments.
func Run(args []string) {
	fa := app.NewWithID("com.nitrobutton.uberpad")
	cfg, cfgErr := config.Load()
	a := &App{fa: fa, cfg: cfg, editors: map[*container.TabItem]*Editor{}, stopped: make(chan struct{})}
	a.win = fa.NewWindow(config.AppName + " " + config.Version)
	a.engine = script.New(a)

	a.tabs = container.NewDocTabs()
	a.tabs.CloseIntercept = a.requestClose
	a.tabs.OnSelected = func(*container.TabItem) {
		a.updateStatus()
		if ed := a.current(); ed != nil {
			if ed.mode == modeHex {
				a.win.Canvas().Focus(ed.hex)
			} else {
				a.win.Canvas().Focus(ed.entry)
			}
		}
	}

	a.statusLabel = widget.NewLabel("")
	a.statusLabel.Wrapping = fyne.TextTruncate
	a.noticeLabel = widget.NewButton("Notifications", a.showNotifications)
	historyBtn := widget.NewButtonWithIcon("History", theme.HistoryIcon(), a.showHistory)
	recentBtn := widget.NewButtonWithIcon("Recent", theme.FolderOpenIcon(), a.showRecent)
	status := container.NewBorder(nil, nil, nil, container.NewHBox(a.noticeLabel, recentBtn, historyBtn), a.statusLabel)
	a.toolbar = container.NewHBox()
	a.win.SetContent(container.NewBorder(a.toolbar, status, nil, nil, a.tabs))
	a.rebuildActions()

	opened := false
	for _, p := range args {
		if err := a.OpenFile(p); err == nil {
			opened = true
		}
	}
	if !opened {
		a.NewTab()
	}
	a.win.SetCloseIntercept(a.requestQuit)
	a.win.Resize(fyne.NewSize(1100, 750))
	if cfgErr != nil {
		dialog.ShowError(cfgErr, a.win)
	}
	a.win.ShowAndRun()
}

func (a *App) pageSize() int64 { return a.cfg.PageSize }

func (a *App) saveConfig() {
	if err := a.cfg.Save(); err != nil {
		dialog.ShowError(fmt.Errorf("saving config: %w", err), a.win)
	}
}

func (a *App) current() *Editor {
	if it := a.tabs.Selected(); it != nil {
		return a.editors[it]
	}
	return nil
}

// rebuildActions (re)creates menus, toolbar and shortcuts from the config.
func (a *App) rebuildActions() {
	c := a.win.Canvas()
	for _, s := range a.canvasShortcuts {
		c.RemoveShortcut(s)
	}
	a.canvasShortcuts = nil
	a.shortcuts = map[string]config.Action{}
	a.toolbar.Objects = nil

	order := []string{"File", "Edit"}
	menus := map[string][]*fyne.MenuItem{"File": nil, "Edit": nil}
	add := func(menu string, it *fyne.MenuItem) {
		if _, ok := menus[menu]; !ok {
			order = append(order, menu)
		}
		menus[menu] = append(menus[menu], it)
	}
	var bad []string
	actions := append(append([]config.Action(nil), a.cfg.Actions...), a.scriptActions...)
	actions = append(actions, inspectionActions()...)
	for _, act := range actions {
		act := act
		run := func() { a.runAction(act) }
		item := fyne.NewMenuItem(act.Label, run)
		if strings.TrimSpace(act.Shortcut) != "" {
			sc, err := ParseShortcut(act.Shortcut)
			if err != nil {
				bad = append(bad, act.Label+": "+err.Error())
			} else {
				item.Shortcut = sc
				a.shortcuts[sc.ShortcutName()] = act
				c.AddShortcut(sc, func(fyne.Shortcut) { run() })
				a.canvasShortcuts = append(a.canvasShortcuts, sc)
			}
		}
		menu := strings.TrimSpace(act.Menu)
		if menu == "" {
			menu = "Tools"
		}
		add(menu, item)
		if act.Toolbar {
			a.toolbar.Add(widget.NewButton(act.Label, run))
		}
	}
	add("File", fyne.NewMenuItemSeparator())
	add("File", fyne.NewMenuItem("Recent Files...", a.showRecent))
	add("File", fyne.NewMenuItem("Settings...", a.showSettings))
	add("File", fyne.NewMenuItem("Reload from Disk", func() {
		if ed := a.current(); ed != nil {
			ed.ReloadPrompt()
		}
	}))
	add("View", fyne.NewMenuItem("Change History", a.showHistory))
	add("View", fyne.NewMenuItem("Notification History", a.showNotifications))
	add("View", fyne.NewMenuItem("Activity Logs", a.showLogs))
	add("View", fyne.NewMenuItem("Task Runner", a.showTaskRunner))
	add("Tools", fyne.NewMenuItem("Run Selection / Document as Lua", a.runDocumentLua))
	add("Tools", fyne.NewMenuItem("Load Kitchen Sink Script", a.loadKitchenSink))
	// Built-in entry points are present even with an older/custom configuration.
	runLua := config.Action{ID: "__run_document_lua", Label: "Run Lua", Script: "ui.run_document()"}
	if sc, err := ParseShortcut("Mod+Enter"); err == nil {
		if _, used := a.shortcuts[sc.ShortcutName()]; !used {
			a.shortcuts[sc.ShortcutName()] = runLua
			c.AddShortcut(sc, func(fyne.Shortcut) { a.runDocumentLua() })
			a.canvasShortcuts = append(a.canvasShortcuts, sc)
		}
	}
	a.toolbar.Add(widget.NewButton("Run Lua", a.runDocumentLua))
	a.toolbar.Add(widget.NewButton("Tasks", a.showTaskRunner))
	add("Edit", fyne.NewMenuItem("Decode Unicode Escapes", func() {
		if ed := a.current(); ed != nil {
			ed.DecodeEscapes()
		}
	}))

	var all []*fyne.Menu
	for _, name := range order {
		if len(menus[name]) > 0 {
			all = append(all, fyne.NewMenu(name, menus[name]...))
		}
	}
	all = append(all, fyne.NewMenu("Help",
		fyne.NewMenuItem("Scripting Help & Snippets", a.showScriptHelp),
		fyne.NewMenuItem("About", a.showAbout)))
	a.win.SetMainMenu(fyne.NewMainMenu(all...))
	a.toolbar.Refresh()
	if len(bad) > 0 {
		dialog.ShowError(errors.New("invalid shortcuts:\n"+strings.Join(bad, "\n")), a.win)
	}
}

func (a *App) handleShortcut(s fyne.Shortcut) bool {
	act, ok := a.shortcuts[s.ShortcutName()]
	if !ok {
		return false
	}
	a.runAction(act)
	return true
}

func (a *App) runAction(act config.Action) {
	if a.runInspectionAction(act.ID) { return }
	if act.ID == "__run_document_lua" { a.runDocumentLua(); return }
	name := act.ID
	if name == "" {
		name = act.Label
	}
	if err := a.engine.Run(name, act.Script); err != nil {
		dialog.ShowError(fmt.Errorf("action %q: %w", act.Label, err), a.win)
	}
}

func (a *App) addEditor(ed *Editor) {
	item := container.NewTabItem(ed.title, ed.content)
	ed.item = item
	a.editors[item] = ed
	a.tabs.Append(item)
	a.tabs.Select(item)
	ed.loadPage(0)
	go ed.watch()
}

func (a *App) closeEditor(item *container.TabItem) {
	ed := a.editors[item]
	delete(a.editors, item)
	a.tabs.Remove(item)
	if ed != nil {
		ed.close()
	}
	a.updateStatus()
}

func (a *App) requestClose(item *container.TabItem) {
	ed := a.editors[item]
	if ed != nil && ed.Modified() {
		dialog.ShowConfirm("Unsaved changes", ed.title+" has unsaved changes. Close anyway?", func(ok bool) {
			if ok {
				a.closeEditor(item)
			}
		}, a.win)
		return
	}
	a.closeEditor(item)
}

func (a *App) requestQuit() {
	n := 0
	for _, ed := range a.editors {
		if ed.Modified() {
			n++
		}
	}
	quit := func() {
		close(a.stopped)
		a.engine.Close()
		for _, ed := range a.editors {
			ed.close()
		}
		_ = a.cfg.Save()
		a.win.Close()
	}
	if n == 0 {
		quit()
		return
	}
	dialog.ShowConfirm("Unsaved changes", fmt.Sprintf("%d document(s) have unsaved changes. Quit anyway?", n),
		func(ok bool) {
			if ok {
				quit()
			}
		}, a.win)
}

func (a *App) saveAsDialog(ed *Editor) {
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if w == nil {
			return
		}
		p := w.URI().Path()
		w.Close()
		if err := ed.SaveAs(p); err != nil && !errors.Is(err, errDiskChanged) {
			dialog.ShowError(err, a.win)
		}
	}, a.win)
	name := ed.title
	if ed.doc.IsNew() {
		name = "untitled.txt"
	}
	d.SetFileName(name)
	d.Show()
}

func humanSize(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func countWords(s string) int {
	n, in := 0, false
	for _, r := range s {
		if unicode.IsSpace(r) {
			in = false
		} else if !in {
			in = true
			n++
		}
	}
	return n
}

func (a *App) updateStatus() {
	ed := a.current()
	if ed == nil {
		a.statusLabel.SetText("No document open")
		return
	}
	d := ed.doc
	text := ed.entry.Text
	lines := "counting..."
	if d.IsNew() {
		lines = strconv.Itoa(strings.Count(text, "\n") + 1)
	} else if n := d.LineCount(); n >= 0 {
		lines = strconv.FormatInt(n, 10)
	}
	state := "saved"
	if ed.Modified() {
		state = "modified"
	}
	if ed.diskChanged.Load() {
		state += ", CHANGED ON DISK"
	}
	pending := d.EditCount()
	if ed.dirty {
		pending++
	}
	path := d.Path()
	if path == "" {
		path = "(not saved yet)"
	}
	a.statusLabel.SetText(fmt.Sprintf("%s | Page %d/%d | %s on disk | %s lines | %s | %d pending edit(s) | %s | %s",
		ed.position(), ed.page+1, d.PageCount(), humanSize(d.Size()), lines,
		ed.viewInfo(), pending, state, path))
}

// ---- script.Host implementation ----

func (a *App) Text() string {
	if ed := a.current(); ed != nil {
		return ed.Text()
	}
	return ""
}

func (a *App) SetText(s string) {
	if ed := a.current(); ed != nil {
		ed.SetText(s)
	}
}

func (a *App) Insert(s string) {
	if ed := a.current(); ed != nil {
		ed.Insert(s)
	}
}

func (a *App) Path() string {
	if ed := a.current(); ed != nil {
		return ed.doc.Path()
	}
	return ""
}

func (a *App) Title() string {
	if ed := a.current(); ed != nil {
		return ed.title
	}
	return ""
}

func (a *App) IsNew() bool {
	if ed := a.current(); ed != nil {
		return ed.doc.IsNew()
	}
	return true
}

func (a *App) Modified() bool {
	if ed := a.current(); ed != nil {
		return ed.Modified()
	}
	return false
}

func (a *App) Size() int64 {
	if ed := a.current(); ed != nil {
		return ed.doc.Size()
	}
	return 0
}

func (a *App) Save(force bool) error {
	ed := a.current()
	if ed == nil {
		return errNoDoc
	}
	return ed.Save(force)
}

func (a *App) Cursor() (int, int) {
	if ed := a.current(); ed != nil {
		return ed.Cursor()
	}
	return 0, 0
}

func (a *App) SetCursor(row, col int) {
	if ed := a.current(); ed != nil {
		ed.SetCursor(row, col)
	}
}

func (a *App) DiskChanged() bool {
	if ed := a.current(); ed != nil {
		return ed.diskChanged.Load() || ed.doc.ChangedOnDisk()
	}
	return false
}

func (a *App) Reload() error {
	ed := a.current()
	if ed == nil {
		return errNoDoc
	}
	return ed.reload()
}

func (a *App) HTTPTimeout() time.Duration {
	return time.Duration(a.cfg.HTTPTimeout) * time.Second
}

func (a *App) SaveAs(p string) error {
	ed := a.current()
	if ed == nil {
		return errNoDoc
	}
	return ed.SaveAs(config.ExpandHome(p))
}

func (a *App) SaveAsDialog() {
	if ed := a.current(); ed != nil {
		a.saveAsDialog(ed)
	}
}

func (a *App) Page() (int64, int64) {
	if ed := a.current(); ed != nil {
		return ed.page, ed.doc.PageCount()
	}
	return 0, 0
}

func (a *App) GotoPage(k int64) {
	if ed := a.current(); ed != nil {
		ed.gotoPage(k)
	}
}

func (a *App) Prompt(title, label, def string, cb func(string, bool)) {
	e := widget.NewEntry()
	e.SetText(def)
	d := dialog.NewForm(title, "OK", "Cancel", []*widget.FormItem{widget.NewFormItem(label, e)},
		func(ok bool) { cb(e.Text, ok) }, a.win)
	d.Resize(fyne.NewSize(420, 160))
	d.Show()
	a.win.Canvas().Focus(e)
}

func (a *App) Confirm(title, msg string, cb func(bool)) {
	dialog.ShowConfirm(title, msg, cb, a.win)
}

func (a *App) Alert(msg string) { dialog.ShowInformation(config.AppName, msg, a.win) }


// OpenFile opens path in a tab (or selects it if already open).
func (a *App) OpenFile(path string) error {
	if strings.HasPrefix(strings.ToLower(path), "https://") || strings.HasPrefix(strings.ToLower(path), "http://") {
		return a.OpenURL(path)
	}
	abs, err := filepath.Abs(config.ExpandHome(path))
	if err != nil {
		return err
	}
	for it, ed := range a.editors {
		if sameDocumentPath(ed.doc.Path(), abs) {
			a.tabs.Select(it)
			return nil
		}
	}
	doc, err := document.Open(abs, a.pageSize())
	if err != nil {
		a.Log("file", fmt.Sprintf("Open failed %q: %v", abs, err))
		return err
	}
	a.Log("file", fmt.Sprintf("Opened %q (%d bytes, page size %d)", abs, doc.Size(), a.pageSize()))
	if cur := a.current(); cur != nil && cur.doc.IsNew() && !cur.Modified() {
		a.closeEditor(cur.item)
	}
	a.addEditor(newEditor(a, doc, filepath.Base(abs)))
	a.cfg.AddRecent(abs)
	a.saveConfig()
	return nil
}

func (a *App) OpenDialog() {
	dialog.ShowFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if r == nil {
			return
		}
		p := r.URI().Path()
		r.Close()
		if err := a.OpenFile(p); err != nil {
			dialog.ShowError(err, a.win)
		}
	}, a.win)
}

func (a *App) NewTab() {
	a.untitled++
	doc, _ := document.Open("", a.pageSize())
	a.addEditor(newEditor(a, doc, fmt.Sprintf("Untitled %d", a.untitled)))
}

func (a *App) CloseTab() {
	if it := a.tabs.Selected(); it != nil {
		a.requestClose(it)
	}
}

func (a *App) GetVar(k string) (string, bool) {
	v, ok := a.cfg.Vars[k]
	return v, ok
}

func (a *App) SetVar(k, v string) {
	a.cfg.Vars[k] = v
	a.saveConfig()
}

func (a *App) Vars() map[string]string {
	out := make(map[string]string, len(a.cfg.Vars))
	for k, v := range a.cfg.Vars {
		out[k] = v
	}
	return out
}
