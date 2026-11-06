package ui

import (
 "image/color"
 "testing"

 "fyne.io/fyne/v2/test"
 "fyne.io/fyne/v2/widget"
)

func TestHexAnnotationCache(t *testing.T) {
 a:=test.NewApp(); defer a.Quit()
 h:=newHexView(nil,nil)
 h.setData(0,make([]byte,48))
 marks:=[]bitMark{{Start:0,End:384,Color:color.NRGBA{R:255,A:255},Label:"field"}}
 h.setMarks(marks)
 first:=h.rowRuns(0)
 other:=h.rowRuns(2)
 h.setMarks(marks)
 if !h.idxValid || &first[0]!=&h.rowRuns(0)[0] { t.Fatal("unchanged marks rebuilt the index/row") }
 h.setCursor(2)
 if &other[0]!=&h.rowRuns(2)[0] { t.Fatal("cursor movement rebuilt an unrelated row") }
 before:=h.rowRuns(0)
 h.data[0]=255
 if &before[0]==&h.rowRuns(0)[0] { t.Fatal("edited bytes reused stale runs") }
 // The caller's slice may be mutated in place; cached definitions must not alias it.
 marks[0].Label="changed"
 h.setMarks(marks)
 if h.marks[0].Label!="changed" || h.rowCache!=nil { t.Fatal("changed definition was not invalidated") }
 h.setMarks(nil)
 h.rowRuns(0)
 if len(h.bitIdx)!=0 { t.Fatal("cleared marks retained colors") }
 h.setData(4096,[]byte{1})
 if h.rowCache!=nil { t.Fatal("new page retained old row cache") }
}

func TestAnnotationLegendRetainsDefinitionsAndUpdatesValues(t *testing.T) {
 a:=test.NewApp(); defer a.Quit()
 h:=newHexView(nil,nil)
 h.setData(0,[]byte{0})
 ed:=&Editor{mode:modeHex,hex:h,src:&pageSource{base:0,n:1},annotationLegend:widget.NewRichText(),marks:[]bitMark{{Start:0,End:1,Label:"flag",Meaning:"enabled",Possible:"0 or 1"}}}
 ed.refreshAnnotationLegend()
 label:=ed.annotationLegend.Segments[0]
 value:=ed.legendCache.values[0].segment
 old:=value.Text
 ed.refreshAnnotationLegend()
 if ed.annotationLegend.Segments[0]!=label || ed.legendCache.values[0].segment!=value { t.Fatal("unchanged legend was rebuilt") }
 h.data[0]=128
 ed.refreshAnnotationLegend()
 if ed.annotationLegend.Segments[0]!=label || ed.legendCache.values[0].segment!=value || value.Text==old { t.Fatal("live edit did not update only the value") }
 ed.marks[0].Meaning="new interpretation"
 ed.refreshAnnotationLegend()
 if ed.annotationLegend.Segments[0]==label { t.Fatal("metadata change did not rebuild definition") }
 label=ed.annotationLegend.Segments[0]
 ed.src.base=4096
 ed.refreshAnnotationLegend()
 if ed.annotationLegend.Segments[0]==label || len(ed.legendCache.values)!=0 { t.Fatal("page navigation reused stale legend") }
}
