package script

import (
 "testing"
 lua "github.com/yuin/gopher-lua"
)
func TestEmbeddedMP3TemplateCompiles(t *testing.T) {
 L:=lua.NewState(); defer L.Close()
 if _,err:=L.LoadString(MP3Template); err!=nil { t.Fatal(err) }
}

// Exercise real embedded Lua parsing and frame arithmetic without GUI/network.
func TestEmbeddedMP3TemplateFrameBoundaries(t *testing.T) {
 L:=lua.NewState(); defer L.Close()
 if err:=L.DoString(`
local bytes=string.char(255,251,144,0)..string.rep(string.char(0),413)
bytes=bytes..bytes
local marks={}
editor={
 set_view=function() return true end,
 page=function() return 1,2 end,
 page_info=function(p) if p==1 then return 0,419 else return 419,415 end end,
 read_bytes=function(p,o,n) local base=p==1 and 0 or 419; return bytes:sub(base+o+1,base+o+n) end,
 set_marks=function(fields)
  assert(#marks==0, 'template must publish only once')
  for _,m in ipairs(fields) do
   assert(m.start>=0 and m.length>0 and m.start+m.length<=#bytes*8)
   assert(type(m.meaning)=='string' and type(m.possible)=='string')
   marks[#marks+1]={m.start,m.length,m.label}
  end
  return true
 end,
 goto_offset=function(n) assert(n==0); return true end,
}
ui={notify=function() end}
function verify()
 local frames=0
 for _,m in ipairs(marks) do if m[3]:find('end is exclusive',1,true) then frames=frames+1 end end
 -- The second frame spans the editor page boundary and is split into two marks.
 assert(frames==3)
end
`); err!=nil { t.Fatal(err) }
 fn,err:=L.LoadString(MP3Template); if err!=nil { t.Fatal(err) }
 L.Push(fn)
 if err:=L.PCall(0,1,nil); err!=nil { t.Fatal(err) }
 if L.Get(-1)!=lua.LNumber(2) { t.Fatal("wrong frame count",L.Get(-1)) }
 if err:=L.DoString(`verify()`); err!=nil { t.Fatal(err) }
}

func TestStringInspectionAPI(t *testing.T) {
 e:=&Engine{L:lua.NewState()}; defer e.L.Close()
 e.registerInspection()
 if err:=e.L.DoString(`
assert(string.char(0,255):byte(2)==255)
assert(str.replace("a.b.a", ".", "$1")=="a$1b$1a")
local split=str.split("a,,b,",","); assert(#split==4 and split[2]=="" and split[4]=="")
local matches=assert(str.regex_find("café 42", "[0-9]+"))
assert(matches[1].start==6 and matches[1].finish==8)
local value,count=str.regex_replace("a12 b34", "([a-z])([0-9]+)", "$2:${1}:$$")
assert(value=="12:a:$ 34:b:$" and count==2)
local bad,why=str.regex_find("hello","["); assert(bad==nil and why)
local empty=str.regex_find("ab","^"); assert(#empty==1 and empty[1].start==empty[1].finish)
`); err!=nil { t.Fatal(err) }
}
