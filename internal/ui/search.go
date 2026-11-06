package ui

import (
 "fmt"
 "regexp"
 "sort"
 "strings"
 "time"
 "unicode/utf8"

 "fyne.io/fyne/v2"
 "fyne.io/fyne/v2/container"
 "fyne.io/fyne/v2/dialog"
 "fyne.io/fyne/v2/widget"
 "nitrobutton.com/nitro-uber-pad/internal/config"
)

const maxSearchBytes = 64 << 20
const maxSearchMatches = 100000

type searchSnapshot struct {
 ids []int64
 pages []string
 starts []int
 text string
}

// Snapshot edited bytes, not the disk image or just the long-line window.
func (ed *Editor) searchSnapshot(page int64, whole bool) (*searchSnapshot, error) {
 if err := ed.commit(); err != nil { return nil, err }
 s := &searchSnapshot{}
 first, end := page, page+1
 if whole { first, end = 0, ed.doc.PageCount() }
 var b strings.Builder
 for k:=first; k<end; k++ {
  text,err := ed.doc.Page(k)
  if err != nil { return nil,err }
  if b.Len()+len(text)>maxSearchBytes { return nil,fmt.Errorf("search scope exceeds the 64 MiB snapshot limit; use a page or selection") }
  s.ids=append(s.ids,k); s.pages=append(s.pages,text); s.starts=append(s.starts,b.Len())
  b.WriteString(text)
 }
 s.text=b.String()
 return s,nil
}

func searchRegexp(pattern string, regex bool) (*regexp.Regexp,error) {
 if pattern=="" { return nil,fmt.Errorf("enter a non-empty search pattern") }
 if !regex { pattern=regexp.QuoteMeta(pattern) }
 return regexp.Compile(pattern)
}

// A boundary inside a replaced match belongs after that replacement. This
// preserves ordered, non-overlapping pages even for matches spanning pages.
func mappedBoundary(boundary int, matches [][]int, replacements []string) int {
 delta:=0
 for i,m:=range matches {
  if m[0]>=boundary { break }
  if m[1]>boundary { return m[0]+delta+len(replacements[i]) }
  delta+=len(replacements[i])-(m[1]-m[0])
 }
 return boundary+delta
}

func replaceSnapshot(s *searchSnapshot, matches [][]int, replacements []string) (map[int64]string,error) {
 var b strings.Builder
 pos:=0
 for i,m:=range matches {
  if b.Len()+m[0]-pos+len(replacements[i])>maxSearchBytes { return nil,fmt.Errorf("replacement exceeds the 64 MiB output limit") }
  b.WriteString(s.text[pos:m[0]]); b.WriteString(replacements[i]); pos=m[1]
 }
 if b.Len()+len(s.text)-pos>maxSearchBytes { return nil,fmt.Errorf("replacement exceeds the 64 MiB output limit") }
 b.WriteString(s.text[pos:])
 result:=b.String()
 out:=make(map[int64]string)
 for i,id:=range s.ids {
  start:=mappedBoundary(s.starts[i],matches,replacements)
  end:=len(result)
  if i+1<len(s.ids) { end=mappedBoundary(s.starts[i+1],matches,replacements) }
  out[id]=result[start:end]
 }
 return out,nil
}

func (s *searchSnapshot) locate(off int) (int64,int) {
 i:=sort.Search(len(s.starts),func(i int) bool { return s.starts[i]>off })-1
 if i<0 { i=0 }
 return s.ids[i],off-s.starts[i]
}

// byteCursor maps a full-page byte position into either a text window or hex.
func (ed *Editor) byteCursor(off int) {
 if ed.mode==modeHex { ed.hex.setCursor(off*2); return }
 text:=ed.Text()
 off=max(0,min(off,len(text)))
 row:=strings.Count(text[:off],"\n")
 start:=strings.LastIndexByte(text[:off],'\n')+1
 if ed.mode==modeWindow {
  ed.setColOff(int64(off-start))
  if row<len(ed.spans) { start=int(ed.spans[row].start) }
 }
 col:=utf8.RuneCountInString(text[min(start,off):off])
 ed.SetCursor(row,col)
}

func visibleByteOffset(text string,row,col int) int {
 off:=0
 for row>0 {
  i:=strings.IndexByte(text[off:],'\n'); if i<0 { return len(text) }
  off+=i+1; row--
 }
 for col>0 && off<len(text) && text[off]!='\n' { _,n:=utf8.DecodeRuneInString(text[off:]); off+=n; col-- }
 return off
}

func (ed *Editor) selectionBounds() (int,int,bool) {
 if ed.mode==modeHex { return 0,0,false }
 selected:=ed.entry.SelectedText()
 if selected=="" { return 0,0,false }
 text:=ed.entry.Text
 cursor:=visibleByteOffset(text,ed.entry.CursorRow,ed.entry.CursorColumn)
 start:=cursor
 if cursor>=len(selected) && text[cursor-len(selected):cursor]==selected { start=cursor-len(selected) } else if !strings.HasPrefix(text[cursor:],selected) { return 0,0,false }
 end:=start+len(selected)
 if ed.mode==modeWindow {
  // A window selection spanning hidden bytes is ambiguous; require a single
  // displayed line rather than silently replacing unselected hidden content.
  if strings.Contains(selected,"\n") { return 0,0,false }
  row:=strings.Count(text[:start],"\n")
  visibleStart:=strings.LastIndexByte(text[:start],'\n')+1
  if row>=len(ed.spans) { return 0,0,false }
  base:=int(ed.spans[row].start)-visibleStart
  start+=base; end+=base
 }
 return start,end,true
}

func (a *App) recordSearch(spec config.SearchTemplate,op string,count int) {
 a.cfg.SearchHistory=append(a.cfg.SearchHistory,config.SearchUse{SearchTemplate:spec,Operation:op,Count:count,At:time.Now().Format(time.RFC3339)})
 if len(a.cfg.SearchHistory)>100 { a.cfg.SearchHistory=a.cfg.SearchHistory[len(a.cfg.SearchHistory)-100:] }
 a.Log("search",fmt.Sprintf("%s: scope=%s regex=%t count=%d",op,spec.Scope,spec.Regex,count))
 a.saveConfig()
}

func (a *App) showSearch(replace bool) {
 ed:=a.current(); if ed==nil { return }
 origin:=ed
 selStart,selEnd,hasSelection:=ed.selectionBounds()
 find:=newF3Entry(func(){ a.findNext() }); find.SetPlaceHolder("Search text or Go/RE2 regular expression")
 replacement:=widget.NewEntry(); replacement.SetPlaceHolder("Replacement; regex: $1, ${name}, $$ for a literal dollar")
 regex:=widget.NewCheck("Regular expression (Go/RE2)",nil)
 scope:=widget.NewSelect([]string{"Current page","Whole file","Selection"},nil)
 scope.SetSelected("Current page")
 if hasSelection { scope.SetSelected("Selection"); find.SetText(ed.entry.SelectedText()) }
 result:=widget.NewLabel("Case-sensitive. Whole-file matches can span page boundaries. No disk writes until Save.")
 result.Wrapping=fyne.TextWrapWord
 name:=widget.NewEntry(); name.SetPlaceHolder("Template name")
 templates:=widget.NewSelect(nil,nil)
 refreshTemplates:=func() { var names []string; for _,t:=range a.cfg.SearchTemplates { names=append(names,t.Name) }; templates.Options=names; templates.Refresh() }
 templates.OnChanged=func(v string) {
  for _,t:=range a.cfg.SearchTemplates { if t.Name==v { name.SetText(v); find.SetText(t.Find); replacement.SetText(t.Replace); regex.SetChecked(t.Regex); scope.SetSelected(t.Scope); return } }
 }
 refreshTemplates()
 spec:=func() config.SearchTemplate { return config.SearchTemplate{Name:name.Text,Find:find.Text,Replace:replacement.Text,Regex:regex.Checked,Scope:scope.Selected} }
 saveTemplate:=widget.NewButton("Save template",func(){
  t:=spec(); t.Name=strings.TrimSpace(t.Name)
  if t.Name=="" { result.SetText("Enter a template name."); return }
  if _,err:=searchRegexp(t.Find,t.Regex); err!=nil { result.SetText(err.Error()); return }
  for i,old:=range a.cfg.SearchTemplates { if old.Name==t.Name { a.cfg.SearchTemplates[i]=t; a.saveConfig(); refreshTemplates(); return } }
  a.cfg.SearchTemplates=append(a.cfg.SearchTemplates,t); a.saveConfig(); refreshTemplates()
 })
 deleteTemplate:=widget.NewButton("Delete template",func(){
  for i,t:=range a.cfg.SearchTemplates { if t.Name==name.Text { a.cfg.SearchTemplates=append(a.cfg.SearchTemplates[:i],a.cfg.SearchTemplates[i+1:]...); break } }
  a.saveConfig(); refreshTemplates()
 })
 next:=0
 lastKey:=""
 run:=func(doReplace,all bool) {
  ed:=a.current(); if ed==nil { result.SetText("No document is open."); return }
  if ed!=origin { origin=ed; hasSelection=false; lastKey="" }
  t:=spec()
  re,err:=searchRegexp(t.Find,t.Regex); if err!=nil { result.SetText(err.Error()); return }
  if t.Scope=="Selection" && !hasSelection { result.SetText("Select text before opening this dialog. In a long-line window, select within one displayed line."); return }
  s,err:=ed.searchSnapshot(ed.page,t.Scope=="Whole file"); if err!=nil { result.SetText(err.Error()); return }
  lo,hi:=0,len(s.text)
  if t.Scope=="Selection" { lo,hi=selStart,selEnd; if hi>len(s.text) { result.SetText("Selection changed; reopen Find/Replace."); return } }
  subject:=s.text[lo:hi]
  matches:=re.FindAllStringSubmatchIndex(subject,maxSearchMatches+1)
  if len(matches)>maxSearchMatches { result.SetText("More than 100,000 matches; narrow the scope or pattern. Nothing replaced."); return }
  pageKey:=ed.page; if t.Scope=="Whole file" { pageKey=-1 }
  key:=fmt.Sprintf("%q/%t/%s/%d",t.Find,t.Regex,t.Scope,pageKey)
  if key!=lastKey { next=lo; lastKey=key }
  var chosen [][]int
  var values []string
  for _,m:=range matches {
   value:=t.Replace
   if t.Regex { value=string(re.ExpandString(nil,t.Replace,subject,m)) }
   absolute:=[]int{m[0]+lo,m[1]+lo}
   if all || absolute[0]>=next { chosen=append(chosen,absolute); values=append(values,value); if !all { break } }
  }
  if !all && len(chosen)==0 && len(matches)>0 {
   m:=matches[0]; chosen=[][]int{{m[0]+lo,m[1]+lo}}
   value:=t.Replace; if t.Regex { value=string(re.ExpandString(nil,t.Replace,subject,m)) }; values=[]string{value}
  }
  op:="find"; if doReplace { op="replace" }
  if len(chosen)==0 { result.SetText("No matches."); a.recordSearch(t,op,0); return }
  first:=chosen[0][0]
  page,local:=s.locate(first)
  if doReplace {
   pages,err:=replaceSnapshot(s,chosen,values); if err!=nil { result.SetText(err.Error()); return }
   if err:=ed.doc.SetPages(pages); err!=nil { result.SetText(err.Error()); return }
   ed.marks=nil
   if t.Scope=="Selection" { for i,m:=range chosen { selEnd+=len(values[i])-(m[1]-m[0]) } }
   next=first+len(values[0])
   if chosen[0][0]==chosen[0][1] { next++ }
   ed.loadPage(page); ed.byteCursor(local)
  } else {
   next=chosen[0][1]
   if chosen[0][0]==chosen[0][1] { _,size:=utf8.DecodeRuneInString(s.text[first:]); if size==0 { size=1 }; next=first+size }
   ed.gotoPage(page); ed.byteCursor(local)
  }
  a.recordSearch(t,op,len(chosen))
  result.SetText(fmt.Sprintf("%s: %d match(es); first at page %d, byte %d. %d total match(es) in scope.",op,len(chosen),page+1,local,len(matches)))
 }
 find.OnSubmitted=func(string){ run(false,false) }
 buttons:=container.NewHBox(widget.NewButton("Find next (wrap)",func(){run(false,false)}),widget.NewButton("Usage history",a.showSearchHistory))
 form:=widget.NewForm(widget.NewFormItem("Find",find),widget.NewFormItem("Mode",regex),widget.NewFormItem("Scope",scope))
 title:="Find"
 if replace {
  title="Find and replace"
  form.Append("Replace with",replacement)
  buttons.Add(widget.NewButton("Replace next",func(){run(true,false)}))
  buttons.Add(widget.NewButton("Replace all",func(){
   dialog.ShowConfirm("Replace all", "Replace every match in the chosen scope? Changes remain unsaved.",func(ok bool){if ok {run(true,true)}},a.parentWin("search"))
  }))
 }
 body:=container.NewVBox(form,buttons,result,widget.NewSeparator(),widget.NewLabel("Persistent templates (both search and replacement)"),templates,name,container.NewHBox(saveTemplate,deleteTemplate))
 // Non-modal: the document stays navigable/editable; F3 repeats Find next.
 a.searchNext=func(){ run(false,false) }
 w:=a.showWindow("search",title+" — F3 finds next",container.NewVScroll(body),fyne.NewSize(760,520))
 w.Canvas().Focus(find)
}

func (a *App) showSearchHistory() {
 var b strings.Builder
 for i:=len(a.cfg.SearchHistory)-1;i>=0;i-- {
  u:=a.cfg.SearchHistory[i]
  fmt.Fprintf(&b,"%s  %s  count=%d  scope=%s regex=%t template=%q\n  find=%q replace=%q\n",u.At,u.Operation,u.Count,u.Scope,u.Regex,u.Name,u.Find,u.Replace)
 }
 text:=widget.NewMultiLineEntry(); text.SetText(b.String()); text.SetMinRowsVisible(12)
 clear:=widget.NewButton("Clear usage history",func(){a.cfg.SearchHistory=nil; a.saveConfig(); text.SetText("")})
 a.showWindow("search_history","Search / replace usage — latest 100 (persisted)",container.NewBorder(clear,nil,nil,nil,text),fyne.NewSize(800,500))
}
