package script

import (
 "fmt"
 "regexp"
 "strings"

 lua "github.com/yuin/gopher-lua"
)

// Optional extension keeps the core Host usable by existing integrations.
type inspectionHost interface {
 OpenURL(string) error
 View() string
 SetView(string) error
 GotoOffset(int64) error
 InspectionPage(int64) (int64,int64,error)
 ReadBytes(int64,int64,int64) (string,error)
 MarkBits(int64,int64,string,string) error
 ClearMarks()
}
const stringLimit = 64 << 20
const matchLimit = 100000

func regexReplace(s,pattern,replacement string) (string,int,error) {
 re,err:=regexp.Compile(pattern); if err!=nil { return "",0,err }
 matches:=re.FindAllStringSubmatchIndex(s,matchLimit+1)
 if len(matches)>matchLimit { return "",0,fmt.Errorf("more than 100,000 matches") }
 var out strings.Builder
 pos:=0
 for _,m:=range matches {
  value:=re.ExpandString(nil,replacement,s,m)
  if out.Len()+m[0]-pos+len(value)>stringLimit { return "",0,fmt.Errorf("output exceeds 64 MiB") }
  out.WriteString(s[pos:m[0]]); out.Write(value); pos=m[1]
 }
 if out.Len()+len(s)-pos>stringLimit { return "",0,fmt.Errorf("output exceeds 64 MiB") }
 out.WriteString(s[pos:]); return out.String(),len(matches),nil
}

func (e *Engine) registerInspection() {
 L:=e.L
 if editor,ok:=L.GetGlobal("editor").(*lua.LTable); ok {
  host:=func() (inspectionHost,error) { h,ok:=e.h.(inspectionHost); if !ok { return nil,fmt.Errorf("inspection API unavailable") }; return h,nil }
  L.SetFuncs(editor,map[string]lua.LGFunction{
   "open_url":func(L *lua.LState) int { h,err:=host(); if err!=nil { return ret(L,err) }; return ret(L,h.OpenURL(L.CheckString(1))) },
   "view":func(L *lua.LState) int { h,err:=host(); if err!=nil { return ret(L,err) }; L.Push(lua.LString(h.View())); return 1 },
   "set_view":func(L *lua.LState) int { h,err:=host(); if err!=nil { return ret(L,err) }; return ret(L,h.SetView(L.CheckString(1))) },
   "goto_offset":func(L *lua.LState) int { h,err:=host(); if err!=nil { return ret(L,err) }; return ret(L,h.GotoOffset(L.CheckInt64(1))) },
   "page_info":func(L *lua.LState) int {
    h,err:=host(); if err!=nil { return ret(L,err) }
    current,_:=e.h.Page(); page:=L.OptInt64(1,current+1)-1
    base,n,err:=h.InspectionPage(page); if err!=nil { return ret(L,err) }
    L.Push(lua.LNumber(base)); L.Push(lua.LNumber(n)); return 2
   },
   "read_bytes":func(L *lua.LState) int {
    h,err:=host(); if err!=nil { return ret(L,err) }
    s,err:=h.ReadBytes(L.CheckInt64(1)-1,L.CheckInt64(2),L.CheckInt64(3)); if err!=nil { return ret(L,err) }
    L.Push(lua.LString(s)); return 1
   },
   "mark_bits":func(L *lua.LState) int {
    h,err:=host(); if err!=nil { return ret(L,err) }
    meaning,possible:="",""
    var details annotationHost
    if L.Get(5)!=lua.LNil {
     t:=L.CheckTable(5)
     meaning,possible=lua.LVAsString(t.RawGetString("meaning")),lua.LVAsString(t.RawGetString("possible"))
     if len(meaning)>4096 || len(possible)>4096 { return ret(L,fmt.Errorf("annotation metadata exceeds 4096 bytes")) }
     var ok bool
     details,ok=e.h.(annotationHost); if !ok { return ret(L,fmt.Errorf("annotation metadata API unavailable")) }
    }
    if err:=h.MarkBits(L.CheckInt64(1),L.CheckInt64(2),L.CheckString(3),L.OptString(4,"")); err!=nil { return ret(L,err) }
    if details!=nil { return ret(L,details.SetMarkDetails(meaning,possible)) }
    return ret(L,nil)
   },
   "set_marks":func(L *lua.LState) int {
    h,ok:=e.h.(annotationBatchHost); if !ok { return ret(L,fmt.Errorf("batch annotation API unavailable")) }
    list:=L.CheckTable(1)
    if list.Len()>10000 { return ret(L,fmt.Errorf("10,000 annotation limit")) }
    fields:=make([]Annotation,0,list.Len())
    for i:=1;i<=list.Len();i++ {
     t,ok:=list.RawGetInt(i).(*lua.LTable); if !ok { return ret(L,fmt.Errorf("mark %d must be a table",i)) }
     start,sok:=t.RawGetString("start").(lua.LNumber)
     length,lok:=t.RawGetString("length").(lua.LNumber)
     if !sok || !lok || !(start>=0 && start<=1<<53) || !(length>0 && length<=1<<53-start) || start!=lua.LNumber(int64(start)) || length!=lua.LNumber(int64(length)) {
      return ret(L,fmt.Errorf("mark %d has an invalid integer bit range",i))
     }
     fields=append(fields,Annotation{Start:int64(start),Length:int64(length),Color:lua.LVAsString(t.RawGetString("color")),Label:lua.LVAsString(t.RawGetString("label")),Meaning:lua.LVAsString(t.RawGetString("meaning")),Possible:lua.LVAsString(t.RawGetString("possible"))})
    }
    return ret(L,h.SetMarks(fields))
   },
   "marks":func(L *lua.LState) int {
    h,ok:=e.h.(annotationHost); if !ok { return ret(L,fmt.Errorf("annotation metadata API unavailable")) }
    out:=L.NewTable()
    for _,m:=range h.AnnotationInfo() {
     t:=L.NewTable()
     t.RawSetString("start",lua.LNumber(m.Start)); t.RawSetString("length",lua.LNumber(m.Length))
     for k,v:=range map[string]string{"color":m.Color,"label":m.Label,"meaning":m.Meaning,"possible":m.Possible,"value":m.Value} { t.RawSetString(k,lua.LString(v)) }
     out.Append(t)
    }
    L.Push(out); return 1
   },
   "clear_marks":func(L *lua.LState) int { h,err:=host(); if err!=nil { return ret(L,err) }; h.ClearMarks(); return ret(L,nil) },
   "mark_text":func(L *lua.LState) int {
    h,err:=host(); if err!=nil { return ret(L,err) }
    start,length:=L.CheckInt64(1),L.CheckInt64(2)
    if start<0 || length<=0 || start>(1<<50) || length>(1<<50)-start { return ret(L,fmt.Errorf("invalid byte range")) }
    return ret(L,h.MarkBits(start*8,length*8,L.CheckString(3),L.OptString(4,"")))
   },
   "offset_info":func(L *lua.LState) int {
    h,err:=host(); if err!=nil { return ret(L,err) }
    offset:=L.CheckInt64(1)
    if offset<0 { return ret(L,fmt.Errorf("negative offset")) }
    _,pages:=e.h.Page()
    for p:=int64(0);p<pages;p++ {
     base,n,err:=h.InspectionPage(p); if err!=nil { return ret(L,err) }
     if offset<n {
      L.Push(lua.LNumber(p+1)); L.Push(lua.LNumber(offset)); L.Push(lua.LNumber(base+offset)); return 3
     }
     offset-=n
    }
    return ret(L,fmt.Errorf("offset outside edited buffer"))
   },
   "read_range":func(L *lua.LState) int {
    h,err:=host(); if err!=nil { return ret(L,err) }
    offset,count:=L.CheckInt64(1),L.CheckInt64(2)
    if offset<0 || count<0 || count>stringLimit { return ret(L,fmt.Errorf("invalid offset/count; 64 MiB read limit")) }
    _,pages:=e.h.Page()
    var out strings.Builder
    for p:=int64(0);p<pages && count>0;p++ {
     _,n,err:=h.InspectionPage(p); if err!=nil { return ret(L,err) }
     if offset>=n { offset-=n; continue }
     take:=n-offset; if take>count { take=count }
     s,err:=h.ReadBytes(p,offset,take); if err!=nil { return ret(L,err) }
     out.WriteString(s); count-=take; offset=0
    }
    if count!=0 { return ret(L,fmt.Errorf("read exceeds edited buffer")) }
    L.Push(lua.LString(out.String())); return 1
   },
   "apply_template":func(L *lua.LState) int {
    if L.CheckString(1)!="mp3" { return ret(L,fmt.Errorf("unknown template; expected mp3")) }
    fn,err:=L.Load(strings.NewReader(MP3Template),"internal:mp3")
    if err!=nil { return ret(L,err) }
    L.Push(fn)
    if err:=L.PCall(0,1,nil); err!=nil { return ret(L,err) }
    L.Insert(lua.LTrue,L.GetTop()); return 2
   },
  })
 }
 L.SetGlobal("str",L.SetFuncs(L.NewTable(),map[string]lua.LGFunction{
  "contains":func(L *lua.LState) int { L.Push(lua.LBool(strings.Contains(L.CheckString(1),L.CheckString(2)))); return 1 },
  "trim":func(L *lua.LState) int { L.Push(lua.LString(strings.TrimSpace(L.CheckString(1)))); return 1 },
  "lower":func(L *lua.LState) int { L.Push(lua.LString(strings.ToLower(L.CheckString(1)))); return 1 },
  "upper":func(L *lua.LState) int { L.Push(lua.LString(strings.ToUpper(L.CheckString(1)))); return 1 },
  "split":func(L *lua.LState) int {
   s,sep:=L.CheckString(1),L.CheckString(2)
   if sep=="" { return ret(L,fmt.Errorf("separator must not be empty")) }
   if len(s)>stringLimit || strings.Count(s,sep)>=matchLimit { return ret(L,fmt.Errorf("split exceeds limits")) }
   out:=L.NewTable(); for _,v:=range strings.Split(s,sep) { out.Append(lua.LString(v)) }; L.Push(out); return 1
  },
  "replace":func(L *lua.LState) int {
   s,old,value:=L.CheckString(1),L.CheckString(2),L.CheckString(3)
   if old=="" { return ret(L,fmt.Errorf("old text must not be empty")) }
   count:=strings.Count(s,old)
   if int64(len(s))+int64(count)*(int64(len(value))-int64(len(old)))>stringLimit { return ret(L,fmt.Errorf("output exceeds 64 MiB")) }
   L.Push(lua.LString(strings.ReplaceAll(s,old,value))); return 1
  },
  "regex_find":func(L *lua.LState) int {
   s,pattern:=L.CheckString(1),L.CheckString(2)
   if len(s)>stringLimit { return ret(L,fmt.Errorf("input exceeds 64 MiB")) }
   re,err:=regexp.Compile(pattern); if err!=nil { return ret(L,err) }
   matches:=re.FindAllStringIndex(s,matchLimit+1); if len(matches)>matchLimit { return ret(L,fmt.Errorf("more than 100,000 matches")) }
   out:=L.NewTable()
   for _,m:=range matches { t:=L.NewTable(); t.RawSetString("start",lua.LNumber(m[0])); t.RawSetString("finish",lua.LNumber(m[1])); t.RawSetString("text",lua.LString(s[m[0]:m[1]])); out.Append(t) }
   L.Push(out); return 1
  },
  "regex_replace":func(L *lua.LState) int {
   s,pattern,replacement:=L.CheckString(1),L.CheckString(2),L.CheckString(3)
   if len(s)>stringLimit { return ret(L,fmt.Errorf("input exceeds 64 MiB")) }
   out,n,err:=regexReplace(s,pattern,replacement); if err!=nil { return ret(L,err) }
   L.Push(lua.LString(out)); L.Push(lua.LNumber(n)); return 2
  },
 }))
}
