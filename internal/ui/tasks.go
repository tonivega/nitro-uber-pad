package ui

import (
 "fmt"
 "io"
 "strings"

 "fyne.io/fyne/v2"
 "fyne.io/fyne/v2/container"
 "fyne.io/fyne/v2/dialog"
 "fyne.io/fyne/v2/widget"
 "nitrobutton.com/nitro-uber-pad/internal/script"
)

const maxLuaDocument = 16 << 20

func (a *App) runDocumentLua() {
 ed := a.current()
 if ed == nil { return }
 var src string
 name := ed.title
 if ed.mode != modeHex { src = ed.entry.SelectedText() }
 if src != "" {
  name += " (selection)"
 } else {
  // Flush the current view first, then read every edited/original page. Never
  // execute only the visible page while claiming to execute the document.
  if err := ed.commit(); err != nil { dialog.ShowError(err,a.win); return }
  var b strings.Builder
  for k:=int64(0);k<ed.doc.PageCount();k++ {
   page,err := ed.doc.Page(k)
   if err != nil { dialog.ShowError(err,a.win); return }
   if b.Len()+len(page)>maxLuaDocument {
    dialog.ShowInformation("Run Lua","Document exceeds the 16 MiB execution limit. Select a smaller script instead.",a.win); return
   }
   b.WriteString(page)
  }
  src = b.String()
 }
 if len(src)>maxLuaDocument { dialog.ShowInformation("Run Lua","Selection exceeds the 16 MiB execution limit.",a.win); return }
 a.queueDocumentLua(ed,name,src)
}

func (a *App) queueDocumentLua(ed *Editor,name,src string) {
 if len(src)>maxLuaDocument { dialog.ShowInformation("Run Lua","Source exceeds the 16 MiB execution limit.",a.win); return }
 id:=a.engine.StartWithSetup(name,src,0,func() error {
  if ed.item==nil || a.editors[ed.item]!=ed { return fmt.Errorf("target document was closed before execution") }
  a.tabs.Select(ed.item)
  return nil
 })
 a.Notify(fmt.Sprintf("Lua task #%d queued for %s — Task Runner to inspect or cancel",id,ed.title))
}

func (a *App) runLuaFile() {
 ed:=a.current(); if ed==nil { return }
 // The picker reads source only; it does not open a tab or change the target.
 dialog.ShowFileOpen(func(r fyne.URIReadCloser,err error) {
  if err!=nil { dialog.ShowError(err,a.win); return }
  if r==nil { return }
  defer r.Close()
  data,err:=io.ReadAll(io.LimitReader(r,maxLuaDocument+1))
  if err!=nil { dialog.ShowError(err,a.win); return }
  a.queueDocumentLua(ed,r.URI().Name(),string(data))
 },a.win)
}

func (a *App) showLuaTextbox() {
 ed:=a.current(); if ed==nil { return }
 src:=widget.NewMultiLineEntry()
 src.TextStyle=fyne.TextStyle{Monospace:true}
 src.Wrapping=fyne.TextWrapOff
 src.SetText("-- Runs against the captured document, not this textbox.\n-- Only run trusted Lua: scripts have filesystem/network access.\n-- Hex: editor.mark_bits(0, 11, \"#FF6655\", \"Sync bits\")\n-- Text: editor.mark_text(0, 5, \"#55BBFF\", \"First five UTF-8 bytes\")\n")
 help:=widget.NewLabel("Target: "+ed.title+". Run queues a source snapshot. Text range colors appear in the annotation preview; Ctrl+hover shows labels.")
 help.Wrapping=fyne.TextWrapWord
 run:=widget.NewButton("Run on target document",func(){ a.queueDocumentLua(ed,"Lua textbox: "+ed.title,src.Text) })
 a.showWindow("lua_textbox","Lua — trusted code only",container.NewBorder(help,run,nil,nil,src),fyne.NewSize(850,560))
}

func (a *App) loadKitchenSink() {
 a.NewTab()
 a.SetText(script.KitchenSink)
 a.Notify("Kitchen sink loaded (not executed). Run Lua / Mod+Enter when ready.")
}

// Manual refresh avoids a panel-owned polling goroutine and keeps widget
// updates on the UI event path. Inspection/cancellation does not wait for Lua.
func (a *App) showTaskRunner() {
 var tasks []script.Task
 selected := 0
 detail := widget.NewMultiLineEntry()
 detail.TextStyle = fyne.TextStyle{Monospace:true}
 detail.SetMinRowsVisible(6)
 list := widget.NewList(func() int { return len(tasks) },func() fyne.CanvasObject { return widget.NewLabel("") },func(i widget.ListItemID,o fyne.CanvasObject) {
  if i>=len(tasks) { return }; t := tasks[i]
  label := o.(*widget.Label); label.Wrapping = fyne.TextTruncate
  label.SetText(fmt.Sprintf("#%d  [%s] %s — %s",t.ID,t.Kind,t.State,t.Name))
 })
 show := func(t script.Task) {
  detail.SetText(fmt.Sprintf("#%d %s: %s\nState: %s\nCreated: %s\nFinished: %v\nExit code: %d\nError: %s\n\n%s",t.ID,t.Kind,t.Name,t.State,t.Created.Format("15:04:05"),t.Finished,t.ExitCode,t.Error,t.Output))
 }
 list.OnSelected = func(i widget.ListItemID) { if i<len(tasks) { selected = tasks[i].ID; show(tasks[i]) } }
 refresh := func() {
  tasks = a.engine.Tasks(); list.UnselectAll(); list.Refresh()
  for i,t := range tasks { if t.ID==selected { list.Select(i); return } }
  selected = 0
  if len(tasks)>0 { list.Select(0) } else { detail.SetText("No tasks in this session.") }
 }
 buttons := container.NewHBox(
  widget.NewButton("Refresh",refresh),
  widget.NewButton("Cancel selected",func(){ a.engine.CancelTask(selected); refresh() }),
  widget.NewButton("Cancel all",func(){ a.engine.CancelAll(); refresh() }),
  widget.NewButton("Clear finished",func(){ a.engine.ClearFinishedTasks(); refresh() }),
  widget.NewButton("Copy details",func(){ a.win.Clipboard().SetContent(detail.Text) }),
 )
 help := widget.NewLabel("Lua is serialized; processes may run concurrently. Refresh for updates. Cancel interrupts Lua/host requests; a canceled dialog's callback is disabled. Output is capped at 1 MiB per process.")
 help.Wrapping = fyne.TextWrapWord
 split := container.NewVSplit(list,detail); split.Offset = 0.55
 refresh()
 a.showWindow("tasks","Task Runner — this session",container.NewBorder(container.NewVBox(help,buttons),nil,nil,nil,split),fyne.NewSize(950,650))
}
