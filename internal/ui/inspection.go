package ui

import (
 "fmt"
 "image/color"
 "io"
 "net/http"
 "net/url"
 "path"
 "strconv"
 "strings"
 "time"
 "unicode/utf8"

 "fyne.io/fyne/v2"
 "fyne.io/fyne/v2/container"
 "fyne.io/fyne/v2/dialog"
 "fyne.io/fyne/v2/driver/desktop"
 "fyne.io/fyne/v2/widget"
 "nitrobutton.com/nitro-uber-pad/internal/config"
 "nitrobutton.com/nitro-uber-pad/internal/document"
)

const positionHelp = "Text: page[:line[:column]], all 1-based; line and column are inside that page, columns count Unicode characters. Hex: absolute edited-buffer byte offset, 0-based; decimal or 0x hexadecimal. Navigation loads the appropriate page. Enter moves immediately. Ctrl+G opens this dialog."

type positionEntry struct {
 widget.Entry
 app *App
 help string
 popup *widget.PopUp
}
func newPositionEntry(a *App,help string) *positionEntry {
 e:=&positionEntry{app:a,help:help}; e.ExtendBaseWidget(e); return e
}
func (e *positionEntry) MouseIn(ev *desktop.MouseEvent) {
 label:=widget.NewLabel(e.help); label.Wrapping=fyne.TextWrapWord
 c:=fyne.CurrentApp().Driver().CanvasForObject(e); if c==nil { return }
 e.popup=widget.NewPopUp(container.NewGridWrap(fyne.NewSize(440,110),label),c)
 e.popup.ShowAtPosition(ev.AbsolutePosition.Add(fyne.NewPos(10,22)))
}
func (e *positionEntry) MouseMoved(*desktop.MouseEvent) {}
func (e *positionEntry) MouseOut() { if e.popup!=nil { e.popup.Hide(); e.popup=nil } }

func inspectionActions() []config.Action {
 return []config.Action{
  {ID:"__find",Label:"Find…",Menu:"Edit",Shortcut:"Ctrl+F"},
  {ID:"__replace",Label:"Replace…",Menu:"Edit",Shortcut:"Ctrl+R"},
  {ID:"__goto",Label:"Go to cursor…",Menu:"Edit",Shortcut:"Ctrl+G",Toolbar:true},
  {ID:"__open_url",Label:"Open URL…",Menu:"File"},
  {ID:"__toggle_view",Label:"Toggle text / hex",Menu:"View"},
  {ID:"__search_history",Label:"Search / Replace Usage",Menu:"View"},
 }
}
func (a *App) runInspectionAction(id string) bool {
 switch id {
 case "__find": a.showSearch(false)
 case "__replace": a.showSearch(true)
 case "__goto": a.showPosition()
 case "__open_url": a.Prompt("Open URL","HTTP(S) URL:","https://",func(s string,ok bool){ if ok { if err:=a.OpenURL(s); err!=nil { dialog.ShowError(err,a.win) } } })
 case "__toggle_view": if ed:=a.current(); ed!=nil { mode:="hex"; if ed.mode==modeHex { mode="text" }; if err:=a.SetView(mode); err!=nil { dialog.ShowError(err,a.win) } }
 case "__search_history": a.showSearchHistory()
 default: return false
 }
 return true
}
func (a *App) showPosition() {
 ed:=a.current(); if ed==nil { return }
 entry:=newPositionEntry(a,positionHelp)
 if ed.mode==modeHex { entry.SetPlaceHolder("offset, e.g. 4096 or 0x1000") } else { entry.SetPlaceHolder("page[:line[:column]], e.g. 3:12:7") }
 status:=widget.NewLabel("")
 var d fyne.Window
 jump:=func(){
  if a.current()!=ed { status.SetText("Select the original document first."); return }
  if err:=ed.jumpPosition(entry.Text); err!=nil { status.SetText(err.Error()); return }
  entry.MouseOut(); d.Close()
  if ed.mode==modeHex { a.win.Canvas().Focus(ed.hex) } else { a.win.Canvas().Focus(ed.entry) }
 }
 entry.OnSubmitted=func(string){jump()}
 help:=widget.NewLabel(positionHelp); help.Wrapping=fyne.TextWrapWord
 d=a.showWindow("goto","Go to cursor",container.NewVBox(help,entry,status,widget.NewButton("Go",jump)),fyne.NewSize(570,260))
 d.Canvas().Focus(entry)
}
func positivePosition(s string) (int64,error) {
 n,err:=strconv.ParseInt(strings.TrimSpace(s),10,64)
 if err!=nil || n<1 { return 0,fmt.Errorf("page, line and column must be positive decimal integers") }
 return n,nil
}
func (ed *Editor) jumpPosition(value string) error {
 if ed.mode==modeHex {
  s:=strings.TrimSpace(value); base:=10
  if strings.HasPrefix(strings.ToLower(s),"0x") { s=s[2:]; base=16 }
  off,err:=strconv.ParseInt(s,base,64)
  if err!=nil || off<0 { return fmt.Errorf("offset must be a non-negative decimal or 0x hexadecimal byte offset") }
  return ed.gotoOffset(off)
 }
 parts:=strings.Split(strings.TrimSpace(value),":")
 if len(parts)<1 || len(parts)>3 { return fmt.Errorf("expected page[:line[:column]]") }
 nums:=[]int64{1,1,1}
 for i,p:=range parts { n,err:=positivePosition(p); if err!=nil { return err }; nums[i]=n }
 if nums[0]>ed.doc.PageCount() { return fmt.Errorf("page out of range") }
 if err:=ed.commit(); err!=nil { return err }
 text,err:=ed.doc.Page(nums[0]-1); if err!=nil { return err }
 if _,ok:=checkText(nil,[]byte(text),true); !ok { return fmt.Errorf("target page contains non-text bytes; use hex mode and an offset") }
 lines:=strings.Split(text,"\n")
 if nums[1]>int64(len(lines)) { return fmt.Errorf("line out of range within page") }
 line:=lines[int(nums[1]-1)]
 if nums[2]>int64(utf8.RuneCountInString(line))+1 { return fmt.Errorf("column out of range") }
 off:=0
 for i:=0;i<int(nums[1]-1);i++ { off+=len(lines[i])+1 }
 off+=len(string([]rune(line)[:int(nums[2]-1)]))
 ed.loadPage(nums[0]-1); ed.byteCursor(off)
 return nil
}

// Offsets are in the current edited buffer, not the potentially stale disk image.
func (ed *Editor) gotoOffset(offset int64) error {
 if offset<0 { return fmt.Errorf("negative offset") }
 if err:=ed.commit(); err!=nil { return err }
 remaining:=offset
 for k:=int64(0);k<ed.doc.PageCount();k++ {
  s,err:=ed.doc.Page(k); if err!=nil { return err }
  if remaining<int64(len(s)) || offset==0 && len(s)==0 && ed.doc.PageCount()==1 {
   ed.loadPage(k); ed.byteCursor(int(remaining)); return nil
  }
  remaining-=int64(len(s))
 }
 return fmt.Errorf("offset outside the edited buffer")
}
func (a *App) GotoOffset(off int64) error {
 ed:=a.current(); if ed==nil { return errNoDoc }; return ed.gotoOffset(off)
}
func (a *App) View() string { if ed:=a.current(); ed!=nil && ed.mode==modeHex { return "hex" }; return "text" }
func (a *App) SetView(mode string) error {
 ed:=a.current(); if ed==nil { return errNoDoc }
 if mode!="text" && mode!="hex" { return fmt.Errorf("view must be text or hex") }
 text,err:=ed.pageText(); if err!=nil { return err }
 if mode=="text" { if _,ok:=checkText(nil,[]byte(text),true); !ok { return fmt.Errorf("non-text bytes cannot be displayed as text") } }
 if err:=ed.commit(); err!=nil { return err }
 ed.forceHex=mode=="hex"; ed.loadPage(ed.page); return nil
}

// Lua inspection coordinates use original file offsets (the same as hex row
// offsets), plus a page-local byte offset. ReadBytes returns edited bytes.
func (a *App) ReadBytes(page,offset,count int64) (string,error) {
 ed:=a.current(); if ed==nil { return "",errNoDoc }
 if err:=ed.commit(); err!=nil { return "",err }
 if page<0 || page>=ed.doc.PageCount() || offset<0 || count<0 || count>maxSearchBytes { return "",fmt.Errorf("invalid page/offset/count (64 MiB read limit)") }
 s,err:=ed.doc.Page(page); if err!=nil { return "",err }
 if offset>int64(len(s)) || count>int64(len(s))-offset { return "",fmt.Errorf("read exceeds page; read the next page explicitly") }
 return s[offset:offset+count],nil
}
func (a *App) InspectionPage(page int64) (int64,int64,error) {
 ed:=a.current(); if ed==nil { return 0,0,errNoDoc }
 if page<0 || page>=ed.doc.PageCount() { return 0,0,fmt.Errorf("page out of range") }
 if err:=ed.commit(); err!=nil { return 0,0,err }
 s,err:=ed.doc.Page(page); if err!=nil { return 0,0,err }
 start,_:=ed.doc.PageBounds(page)
 return start,int64(len(s)),nil
}

type bitMark struct { Start,End int64; Color color.NRGBA; Label string; Meaning,Possible string }
func parseMarkColor(s string) (color.NRGBA,error) {
 if len(s)!=7 || s[0]!='#' { return color.NRGBA{},fmt.Errorf("color must be #RRGGBB") }
 n,err:=strconv.ParseUint(s[1:],16,24)
 if err!=nil { return color.NRGBA{},fmt.Errorf("color must be #RRGGBB") }
 return color.NRGBA{R:uint8(n>>16),G:uint8(n>>8),B:uint8(n),A:255},nil
}
func (a *App) MarkBits(start,length int64,shade,label string) error {
 ed:=a.current(); if ed==nil { return errNoDoc }
 m,err:=validatedBitMark(start,length,shade,label); if err!=nil { return err }
 if len(ed.marks)>=10000 { return fmt.Errorf("10,000 annotation limit; clear marks first") }
 ed.marks=append(ed.marks,m)
 ed.refreshAnnotations(); return nil
}
func validatedBitMark(start,length int64,shade,label string) (bitMark,error) {
 c,err:=parseMarkColor(shade); if err!=nil { return bitMark{},err }
 if start<0 || length<=0 || start>1<<53 || length>(1<<53)-start { return bitMark{},fmt.Errorf("invalid bit range (maximum exact Lua integer: 2^53)") }
 if utf8.RuneCountInString(label)>200 { return bitMark{},fmt.Errorf("label exceeds 200 characters") }
 return bitMark{Start:start,End:start+length,Color:c,Label:label},nil
}
func (a *App) ClearMarks() { if ed:=a.current(); ed!=nil { ed.marks=nil; ed.refreshAnnotations() } }

func (a *App) OpenURL(raw string) error {
 u,err:=url.Parse(strings.TrimSpace(raw))
 if err!=nil || u.Host=="" || u.Scheme!="http" && u.Scheme!="https" { return fmt.Errorf("expected an HTTP(S) URL") }
 req,err:=http.NewRequest(http.MethodGet,u.String(),nil); if err!=nil { return err }
 req.Header.Set("User-Agent","NitroUberPad/"+config.Version)
 safe:=*u; safe.User=nil; safe.RawQuery=""; safe.Fragment=""
 a.Log("network","Open URL "+safe.String())
 timeout:=a.HTTPTimeout(); if timeout<=0 { timeout=30*time.Second }
 resp,err:=(&http.Client{Timeout:timeout}).Do(req); if err!=nil { return fmt.Errorf("download failed: %v",err) }
 defer resp.Body.Close()
 if resp.StatusCode<200 || resp.StatusCode>=300 { return fmt.Errorf("download HTTP %d",resp.StatusCode) }
 data,err:=io.ReadAll(io.LimitReader(resp.Body,maxSearchBytes+1)); if err!=nil { return err }
 if len(data)>maxSearchBytes { return fmt.Errorf("download exceeds 64 MiB limit") }
 doc,err:=document.OpenBuffer(data,a.pageSize()); if err!=nil { return err }
 title:=path.Base(u.Path); if title=="." || title=="/" || title=="" { title=u.Host }
 a.addEditor(newEditor(a,doc,title+" (URL, unsaved)"))
 a.cfg.AddRecent(u.String()); a.saveConfig()
 a.Notify(fmt.Sprintf("Downloaded %d bytes. Save As to keep a local copy; closing discards the temporary download.",len(data)))
 return nil
}
