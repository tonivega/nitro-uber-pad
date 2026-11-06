package ui

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	hexRow      = 16
	hexAsciiCol = hexRow*3 + 3 // column of the first text character of a row
)

// hexCol returns the column of the first hex digit of byte j in a row.
func hexCol(j int) int {
	if j >= 8 {
		return j*3 + 1
	}
	return j * 3
}

// hexView is a virtualized hex editor with file offsets in the left zone.
// Typing a hex digit overwrites the nibble under the cursor in place.
type hexView struct {
	widget.BaseWidget
	base      int64
	data      []byte
	marks     []bitMark
	cur       int // cursor position in nibbles (2 per byte)
	focused   bool
	list      *widget.List
	content   fyne.CanvasObject
	onChange  func()
	onCursor  func()
	onShortcut func(fyne.Shortcut) bool
}

func newHexView(onChange func(), onCursor func()) *hexView {
	h := &hexView{onChange: onChange, onCursor: onCursor}
	h.list = widget.NewList(
		func() int { return (len(h.data) + hexRow - 1) / hexRow },
		func() fyne.CanvasObject { return newHexCell(h) },
		func(i widget.ListItemID, o fyne.CanvasObject) { o.(*hexCell).setRow(i) })
	h.ExtendBaseWidget(h)

	bold := fyne.TextStyle{Monospace: true, Bold: true}
	var hdr strings.Builder
	for j := 0; j < hexRow; j++ {
		fmt.Fprintf(&hdr, "%02X ", j)
		if j == 7 {
			hdr.WriteByte(' ')
		}
	}
	hdr.WriteString(" |text|")
	header := container.NewHBox(
		widget.NewLabelWithStyle("      OFFSET", fyne.TextAlignTrailing, bold),
		widget.NewLabelWithStyle(hdr.String(), fyne.TextAlignLeading, bold))
	help := widget.NewLabel("Hex view: type 0-9 / A-F to overwrite the nibble at the cursor in place; " +
		"click either digit to edit it directly; arrows, Home/End, PgUp/PgDn move. No dialog, no insertion.")
	help.Wrapping = fyne.TextTruncate
	h.content = container.NewBorder(container.NewVBox(help, header), nil, nil, nil, h)
	return h
}

func (h *hexView) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(h.list) }

func (h *hexView) setData(base int64, data []byte) {
	h.base, h.data = base, data
	if m := len(h.data)*2 - 1; h.cur > m {
		h.cur = m
	}
	if h.cur < 0 {
		h.cur = 0
	}
	h.list.Refresh()
}

func (h *hexView) setCursor(n int) {
	if m := len(h.data)*2 - 1; n > m {
		n = m
	}
	if n < 0 {
		n = 0
	}
	h.cur = n
	if len(h.data) > 0 {
		h.list.ScrollTo(n / 2 / hexRow)
	}
	h.list.Refresh()
	if h.onCursor != nil {
		h.onCursor()
	}
}

func (h *hexView) focus() {
	if c := fyne.CurrentApp().Driver().CanvasForObject(h); c != nil {
		c.Focus(h)
	}
}

// tapAt places the cursor at a character column of a row.
func (h *hexView) tapAt(row, col int) {
	if len(h.data) > 0 {
		j, nib := 0, 0
		if col >= hexAsciiCol {
			j = col - hexAsciiCol
		} else {
			c := max(col, 0)
			if c > hexCol(7)+2 {
				c--
			}
			j, nib = c/3, min(c%3, 1)
		}
		j = max(0, min(j, hexRow-1))
		idx := min(row*hexRow+j, len(h.data)-1)
		h.setCursor(idx*2 + nib)
	}
	h.focus()
}

func (h *hexView) TypedShortcut(s fyne.Shortcut) {
	if h.onShortcut != nil {
		h.onShortcut(s)
	}
}

func (h *hexView) FocusGained() { h.focused = true; h.list.Refresh() }
func (h *hexView) FocusLost()   { h.focused = false; h.list.Refresh() }

func hexDigit(r rune) (byte, bool) {
	switch {
	case r >= '0' && r <= '9':
		return byte(r - '0'), true
	case r >= 'a' && r <= 'f':
		return byte(r-'a') + 10, true
	case r >= 'A' && r <= 'F':
		return byte(r-'A') + 10, true
	}
	return 0, false
}

// TypedRune overwrites the nibble under the cursor and advances it.
func (h *hexView) TypedRune(r rune) {
	v, ok := hexDigit(r)
	if !ok || len(h.data) == 0 {
		return
	}
	i := h.cur / 2
	if h.cur%2 == 0 {
		h.data[i] = v<<4 | h.data[i]&0x0F
	} else {
		h.data[i] = h.data[i]&0xF0 | v
	}
	if h.onChange != nil {
		h.onChange()
	}
	h.setCursor(h.cur + 1)
}

func (h *hexView) TypedKey(ev *fyne.KeyEvent) {
	rowStart := h.cur / (2 * hexRow) * 2 * hexRow
	switch ev.Name {
	case fyne.KeyLeft:
		h.setCursor(h.cur - 1)
	case fyne.KeyRight:
		h.setCursor(h.cur + 1)
	case fyne.KeyUp:
		h.setCursor(h.cur - 2*hexRow)
	case fyne.KeyDown:
		h.setCursor(h.cur + 2*hexRow)
	case fyne.KeyPageUp:
		h.setCursor(h.cur - 32*hexRow)
	case fyne.KeyPageDown:
		h.setCursor(h.cur + 32*hexRow)
	case fyne.KeyHome:
		h.setCursor(rowStart)
	case fyne.KeyEnd:
		h.setCursor(rowStart + 2*hexRow - 1)
	}
}

func hexSeg(s string, hl, focused bool) *widget.TextSegment {
	st := widget.RichTextStyle{Inline: true, TextStyle: fyne.TextStyle{Monospace: true}}
	if hl {
		st.TextStyle.Bold = true
		if focused {
			st.ColorName = theme.ColorNamePrimary
		}
	}
	return &widget.TextSegment{Style: st, Text: s}
}

// Colored segments retain standard text/selection behavior while supplying an
// explicit Lua-chosen color, independent of theme color names.
type coloredHexSegment struct {
	*widget.TextSegment
	shade color.Color
}
func (s *coloredHexSegment) Visual() fyne.CanvasObject {
	o := s.TextSegment.Visual()
	s.Update(o)
	return o
}
func (s *coloredHexSegment) Update(o fyne.CanvasObject) {
	s.TextSegment.Update(o)
	if text, ok := o.(*canvas.Text); ok { text.Color = s.shade; text.Refresh() }
}
func coloredHex(s string, shade color.Color, bold bool) widget.RichTextSegment {
	segment := hexSeg(s, bold, false)
	return &coloredHexSegment{TextSegment:segment, shade:shade}
}
func (h *hexView) markAt(start,end int64) *bitMark {
	// Last registration wins when annotations overlap.
	for i:=len(h.marks)-1;i>=0;i-- {
		m:=&h.marks[i]
		if m.Start<end && m.End>start { return m }
	}
	return nil
}
func (h *hexView) annotatedSegments(row int, line string) []widget.RichTextSegment {
	var out []widget.RichTextSegment
	off:=row*hexRow
	for col,r:=range line {
		j,half:=-1,0
		for b:=0;b<hexRow;b++ {
			if col==hexCol(b) || col==hexCol(b)+1 { j=b; half=col-hexCol(b); break }
		}
		if col>=hexAsciiCol && col<hexAsciiCol+min(hexRow,len(h.data)-off) { j=col-hexAsciiCol; half=0 }
		highlight:=j>=0 && off+j==h.cur/2 && (col>=hexAsciiCol || half==h.cur%2)
		var mark *bitMark
		if j>=0 && off+j<len(h.data) {
			start:=(h.base+int64(off+j))*8
			if col<hexAsciiCol { start+=int64(half*4); mark=h.markAt(start,start+4) } else { mark=h.markAt(start,start+8) }
		}
		if mark!=nil { out=append(out,coloredHex(string(r),mark.Color,highlight)) } else { out=append(out,hexSeg(string(r),highlight,h.focused)) }
	}
	start:=(h.base+int64(off))*8
	end:=start+int64(min(hexRow,len(h.data)-off))*8
	for _,m:=range h.marks {
		if m.Start<end && m.End>start {
			out=append(out,coloredHex(fmt.Sprintf("  [%d:%d) %s",m.Start,m.End,m.Label),m.Color,false))
		}
	}
	// Show exact individual bits as well: nibble coloring alone cannot express
	// fields such as the 11-bit MPEG frame sync or a 1-bit protection flag.
	out=append(out,hexSeg("  bits: ",false,false))
	for j:=off;j<min(off+hexRow,len(h.data));j++ {
		for bit:=0;bit<8;bit++ {
			s:=fmt.Sprint((h.data[j]>>uint(7-bit))&1)
			p:=(h.base+int64(j))*8+int64(bit)
			if m:=h.markAt(p,p+1); m!=nil { out=append(out,coloredHex(s,m.Color,false)) } else { out=append(out,hexSeg(s,false,false)) }
		}
		out=append(out,hexSeg(" ",false,false))
	}
	return out
}

// rowSegments renders row i, highlighting the cursor nibble and its text char.
func (h *hexView) rowSegments(i int) (string, []widget.RichTextSegment) {
	off := i * hexRow
	if off >= len(h.data) {
		return "", []widget.RichTextSegment{hexSeg(" ", false, false)}
	}
	b := h.data[off:min(off+hexRow, len(h.data))]
	var sb strings.Builder
	for j := 0; j < hexRow; j++ {
		if j < len(b) {
			fmt.Fprintf(&sb, "%02X ", b[j])
		} else {
			sb.WriteString("   ")
		}
		if j == 7 {
			sb.WriteByte(' ')
		}
	}
	sb.WriteString(" |")
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	sb.WriteByte('|')
	line := sb.String()
	offStr := fmt.Sprintf("%012X", h.base+int64(off))
	if len(h.marks)>0 { return offStr,h.annotatedSegments(i,line) }
	cb := h.cur/2 - off
	if cb < 0 || cb >= len(b) {
		return offStr, []widget.RichTextSegment{hexSeg(line, false, false)}
	}
	p1 := hexCol(cb) + h.cur%2
	p2 := hexAsciiCol + cb
	parts := []struct {
		s  string
		hl bool
	}{{line[:p1], false}, {line[p1 : p1+1], true}, {line[p1+1 : p2], false}, {line[p2 : p2+1], true}, {line[p2+1:], false}}
	var segs []widget.RichTextSegment
	for _, p := range parts {
		if p.s != "" {
			segs = append(segs, hexSeg(p.s, p.hl, h.focused))
		}
	}
	return offStr, segs
}

// hexCell is one row of the hex list: offset + bytes + text.
type hexCell struct {
	widget.BaseWidget
	h    *hexView
	row  int
	off  *widget.RichText
	text *widget.RichText
	box  *fyne.Container
}

func newHexCell(h *hexView) *hexCell {
	c := &hexCell{h: h, row: -1}
	c.off = widget.NewRichText(hexSeg("000000000000", true, false))
	c.text = widget.NewRichText(hexSeg(strings.Repeat(" ", hexAsciiCol+hexRow+1), false, false))
	c.box = container.NewHBox(c.off, c.text)
	c.ExtendBaseWidget(c)
	return c
}

func (c *hexCell) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(c.box) }

func (c *hexCell) setRow(i int) {
	c.row = i
	off, segs := c.h.rowSegments(i)
	start := (c.h.base+int64(i*hexRow))*8
	if m:=c.h.markAt(start,start+hexRow*8); m!=nil {
		c.off.Segments=[]widget.RichTextSegment{coloredHex(off,m.Color,true)}
	} else { c.off.Segments=[]widget.RichTextSegment{hexSeg(off,true,false)} }
	c.off.Refresh()
	c.text.Segments = segs
	c.text.Refresh()
}

func (c *hexCell) Tapped(ev *fyne.PointEvent) {
	cw := fyne.MeasureText("0", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
	if cw <= 0 || c.row < 0 {
		return
	}
	x := ev.Position.X - c.text.Position().X - theme.InnerPadding()
	col := 0
	if x > 0 {
		col = int(x / cw)
	}
	c.h.tapAt(c.row, col)
}

// A double-click has exactly the same in-place semantics as a single click.
func (c *hexCell) DoubleTapped(ev *fyne.PointEvent) { c.Tapped(ev) }

