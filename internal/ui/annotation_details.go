package ui

import (
 "fmt"
 "strings"

 "fyne.io/fyne/v2/widget"
 "nitrobutton.com/nitro-uber-pad/internal/script"
)

// Values are read from the current edited page, not cached when a template ran.
// A partial field is identified explicitly rather than interpreted as a whole.
func (ed *Editor) annotationValue(m bitMark) string {
 text,err:=ed.pageText()
 return ed.annotationValueIn(m,text,err)
}

// annotationValueIn reuses one page snapshot for many fields.
func (ed *Editor) annotationValueIn(m bitMark,text string,err error) string {
 if ed.src==nil { return "unavailable" }
 if err!=nil { return "unavailable: "+err.Error() }
 start,end:=m.Start-ed.src.base*8,m.End-ed.src.base*8
 if start<0 || end>int64(len(text))*8 { return "cross-page field (inspect its other page)" }
 if end-start>53 { return fmt.Sprintf("%d bits (numeric display limited to 53 bits)",end-start) }
 var value uint64
 var binary strings.Builder
 for i:=start;i<end;i++ {
  bit:=(text[i/8]>>uint(7-i%8))&1
  value=value*2+uint64(bit)
  binary.WriteByte('0'+bit)
 }
 return fmt.Sprintf("%s₂ = %d (0x%X)",binary.String(),value,value)
}

func (a *App) SetMarkDetails(meaning,possible string) error {
 ed:=a.current()
 if ed==nil { return errNoDoc }
 if len(ed.marks)==0 { return fmt.Errorf("no annotation to describe") }
 if len(meaning)>4096 || len(possible)>4096 { return fmt.Errorf("annotation metadata exceeds 4096 bytes") }
 m:=&ed.marks[len(ed.marks)-1]
 m.Meaning,m.Possible=meaning,possible
 ed.refreshAnnotations()
 return nil
}

func (a *App) AnnotationInfo() []script.Annotation {
 ed:=a.current()
 if ed==nil { return nil }
 out:=make([]script.Annotation,0,len(ed.marks))
 text,terr:=ed.pageText()
 for _,m:=range ed.marks {
  out=append(out,script.Annotation{Start:m.Start,Length:m.End-m.Start,
   Color:fmt.Sprintf("#%02X%02X%02X",m.Color.R,m.Color.G,m.Color.B),
   Label:m.Label,Meaning:m.Meaning,Possible:m.Possible,Value:ed.annotationValueIn(m,text,terr)})
 }
 return out
}

// Keep field definitions and their rich-text segments for the current page.
// Nibble edits update only live value segments; cursor moves and repaints do
// not redefine colors, ranges, interpretations or possible-value explanations.
type annotationLegendCache struct {
 valid bool
 base int64
 length int
 marks []bitMark
 values []annotationLegendValue
}
type annotationLegendValue struct {
 mark bitMark
 segment *widget.TextSegment
}

func (ed *Editor) refreshAnnotationLegend() {
 if ed.annotationLegend==nil || ed.src==nil { return }
 text,err:=ed.pageText()
 if err!=nil { text="" }
 cache:=&ed.legendCache
 same:=cache.valid && cache.base==ed.src.base && cache.length==len(text) && len(cache.marks)==len(ed.marks)
 if same {
  for i,m:=range ed.marks { if cache.marks[i]!=m { same=false; break } }
 }
 if same {
  changed:=false
  for _,v:=range cache.values {
   value:="Value: "+ed.annotationValueIn(v.mark,text,err)+"\n"
   if v.segment.Text!=value { v.segment.Text=value; changed=true }
  }
  if changed { ed.annotationLegend.Refresh() }
  return
 }
 *cache=annotationLegendCache{valid:true,base:ed.src.base,length:len(text),marks:append([]bitMark(nil),ed.marks...)}
 var segments []widget.RichTextSegment
 start,end:=ed.src.base*8,(ed.src.base+int64(len(text)))*8
 shown:=0
 for _,m:=range ed.marks {
  if m.Start>=end || m.End<=start { continue }
  if shown==300 { segments=append(segments,hexSeg("\nMore fields on this page omitted from the panel. Lua editor.marks() returns all fields.\n",false,false)); break }
  shown++
  segments=append(segments,coloredHex(m.Label+"\n",m.Color,true))
  possible:=m.Possible
  if possible=="" {
   if m.End-m.Start<=53 { possible=fmt.Sprintf("unsigned 0 … %.0f",float64(uint64(1)<<uint(m.End-m.Start))-1) } else { possible="arbitrary bit sequence" }
  }
  segments=append(segments,hexSeg(fmt.Sprintf("Bits [%d,%d) • %d bits\n",m.Start,m.End,m.End-m.Start),false,false))
  value:=hexSeg("Value: "+ed.annotationValueIn(m,text,err)+"\n",false,false)
  cache.values=append(cache.values,annotationLegendValue{mark:m,segment:value})
  segments=append(segments,value)
  detail:=""
  if m.Meaning!="" { detail+="Interpretation: "+m.Meaning+"\n" }
  detail+="Possible values: "+possible+"\n\n"
  segments=append(segments,hexSeg(detail,false,false))
 }
 if shown==0 { segments=append(segments,hexSeg("No annotations overlap this page. Navigate to another page.\n",false,false)) }
 ed.annotationLegend.Segments=segments
 ed.annotationLegend.Refresh()
}

// SetMarks validates the complete replacement before publishing it. Templates
// can define thousands of fields with a single index/panel refresh, and errors
// leave the previous annotations intact.
func (a *App) SetMarks(fields []script.Annotation) error {
 ed:=a.current()
 if ed==nil { return errNoDoc }
 if len(fields)>10000 { return fmt.Errorf("10,000 annotation limit") }
 marks:=make([]bitMark,0,len(fields))
 for _,field:=range fields {
  m,err:=validatedBitMark(field.Start,field.Length,field.Color,field.Label)
  if err!=nil { return err }
  if len(field.Meaning)>4096 || len(field.Possible)>4096 { return fmt.Errorf("annotation metadata exceeds 4096 bytes") }
  m.Meaning,m.Possible=field.Meaning,field.Possible
  marks=append(marks,m)
 }
 ed.marks=marks
 ed.refreshAnnotations()
 return nil
}
