package ui

import (
 "strings"
 "testing"
)

func TestSearchReplacementCrossPage(t *testing.T) {
 s:=&searchSnapshot{ids:[]int64{0,1,2},pages:[]string{"ab","cd","ef"},starts:[]int{0,2,4},text:"abcdef"}
 out,err:=replaceSnapshot(s,[][]int{{1,5}},[]string{"XYZ"})
 if err!=nil { t.Fatal(err) }
 if out[0]+out[1]+out[2]!="aXYZf" { t.Fatal(out) }
 if out[0]!="aXYZ" || out[1]!="" || out[2]!="f" { t.Fatal(out) }
}
func TestLiteralAndRegexSearch(t *testing.T) {
 literal,err:=searchRegexp("a.b",false); if err!=nil { t.Fatal(err) }
 if literal.MatchString("axb") || !literal.MatchString("a.b") { t.Fatal("literal interpreted as regex") }
 if _,err:=searchRegexp("[",true); err==nil { t.Fatal("invalid regex accepted") }
 re,_:=searchRegexp("(?m)^",true)
 if len(re.FindAllStringIndex("one\ntwo",-1))!=2 { t.Fatal("zero-width matches") }
 s:=&searchSnapshot{ids:[]int64{0,1},starts:[]int{0,2},text:"abcd"}
 out,err:=replaceSnapshot(s,[][]int{{2,2}},[]string{"$literal"})
 if err!=nil || out[0]+out[1]!="ab$literalcd" { t.Fatal(out,err) }
}
func TestSearchUnicodePosition(t *testing.T) {
 text:="αβ\n😀z"
 if off:=visibleByteOffset(text,1,1); text[off:]!="z" { t.Fatal(off) }
 if n,err:=positivePosition("0"); err==nil { t.Fatal(n) }
 if _,err:=parseMarkColor("#GG0000"); err==nil { t.Fatal("invalid color") }
 c,err:=parseMarkColor("#123456"); if err!=nil || c.R!=0x12 || c.B!=0x56 { t.Fatal(c,err) }
}
func TestReplacementOutputLimit(t *testing.T) {
 s:=&searchSnapshot{ids:[]int64{0},starts:[]int{0},text:"aa"}
 if _,err:=replaceSnapshot(s,[][]int{{0,1},{1,2}},[]string{strings.Repeat("x",maxSearchBytes/2+1),strings.Repeat("x",maxSearchBytes/2+1)}); err==nil { t.Fatal("limit ignored") }
}
func TestTextGate(t *testing.T) {
 for _,s:=range []string{"\x00","\xff","abc\x7f","\xc3"} { if _,ok:=checkText(nil,[]byte(s),true); ok { t.Fatalf("accepted %q",s) } }
 if _,ok:=checkText(nil,[]byte("café 😀\n\t"),true); !ok { t.Fatal("valid text rejected") }
}
