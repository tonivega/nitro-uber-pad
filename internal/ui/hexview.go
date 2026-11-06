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
	hexOffCols  = 14           // 12 offset digits + 2 spaces
	hexDigits   = "0123456789ABCDEF"
)

// hexCol returns the column of the first hex digit of byte j in a row.
func hexCol(j int) int {
	if j >= 8 {
		return j*3 + 1
	}
	return j * 3
}

// hexView is a virtualized hex editor drawn by the custom fastText control.
// Only visible rows are materialized as merged color runs, and annotation
// lookups use a per-bit index, so thousands of marks stay fast.
type hexView struct {
	widget.BaseWidget
	base       int64
	data       []byte
	marks      []bitMark
	cur        int // cursor position in nibbles (2 per byte)
	focused    bool
	view       *fastText
	clip       *container.Scroll
	content    fyne.CanvasObject
	onChange   func()
	onCursor   func()
	onF3       func()
	onShortcut func(fyne.Shortcut) bool
	bitIdx     []int32
	idxValid   bool
	rowCache   map[int]hexRowCache
}

// Cache only materialized rows. Byte snapshots detect in-place nibble edits;
// a page or annotation change invalidates every cached row.
type hexRowCache struct {
	data      string
	cursor    int
	focused   bool
	selection color.Color
	runs      []textRun
}

func newHexView(onChange func(), onCursor func()) *hexView {
	h := &hexView{onChange: onChange, onCursor: onCursor}
	h.view = newFastText(func() int { return (len(h.data) + hexRow - 1) / hexRow }, h.rowRuns, true)
	h.view.onTap = func(row int, x float32) {
		cw := fastCell()
		col := 0
		if cw > 0 {
			col = int(x/cw) - hexOffCols
		}
		h.tapAt(row, max(col, 0))
	}
	h.clip = container.NewScroll(h.view)
	h.clip.Direction = container.ScrollNone
	h.ExtendBaseWidget(h)
	var hdr strings.Builder
	hdr.WriteString("OFFSET        ")
	for j := 0; j < hexRow; j++ {
		fmt.Fprintf(&hdr, "%02X ", j)
		if j == 7 {
			hdr.WriteByte(' ')
		}
	}
	hdr.WriteString(" |text|")
	header := widget.NewLabelWithStyle(hdr.String(), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true, Bold: true})
	help := widget.NewLabel("Hex view: type 0-9 / A-F to overwrite the nibble at the cursor in place; " +
		"click either digit to edit it directly; arrows, Home/End, PgUp/PgDn move; F3 finds next.")
	help.Wrapping = fyne.TextTruncate
	h.content = container.NewBorder(container.NewVBox(help, header), nil, nil, nil, h)
	return h
}

func (h *hexView) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(h.clip) }

func (h *hexView) setData(base int64, data []byte) {
	h.base, h.data, h.idxValid = base, data, false
	h.rowCache = nil
	if m := len(h.data)*2 - 1; h.cur > m {
		h.cur = m
	}
	if h.cur < 0 {
		h.cur = 0
	}
	h.view.Refresh()
}

func (h *hexView) setMarks(m []bitMark) {
	same := len(m) == len(h.marks)
	if same {
		for i := range m {
			if m[i] != h.marks[i] {
				same = false
				break
			}
		}
	}
	if same {
		return
	}
	// Keep an independent definition snapshot: callers may mutate their slice.
	h.marks = append([]bitMark(nil), m...)
	h.idxValid = false
	h.rowCache = nil
	h.view.Refresh()
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
		h.view.scrollTo(n / 2 / hexRow)
	}
	h.view.Refresh()
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

func (h *hexView) FocusGained() { h.focused = true; h.view.Refresh() }
func (h *hexView) FocusLost()   { h.focused = false; h.view.Refresh() }

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
	case fyne.KeyF3:
		if h.onF3 != nil {
			h.onF3()
		}
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

// ensureIdx builds a per-bit "winning mark" index for the page once per
// data/mark change; later registrations win overlaps.
func (h *hexView) ensureIdx() {
	if h.idxValid {
		return
	}
	h.idxValid = true
	if len(h.marks) == 0 {
		h.bitIdx = h.bitIdx[:0]
		return
	}
	n := len(h.data) * 8
	if cap(h.bitIdx) < n {
		h.bitIdx = make([]int32, n)
	} else {
		h.bitIdx = h.bitIdx[:n]
	}
	for i := range h.bitIdx {
		h.bitIdx[i] = -1
	}
	lo := h.base * 8
	for k, m := range h.marks {
		s, e := max(m.Start-lo, 0), min(m.End-lo, int64(n))
		for b := s; b < e; b++ {
			h.bitIdx[b] = int32(k)
		}
	}
}

func (h *hexView) markIn(s, e int) *bitMark {
	best := int32(-1)
	for b := max(s, 0); b < e && b < len(h.bitIdx); b++ {
		if h.bitIdx[b] > best {
			best = h.bitIdx[b]
		}
	}
	if best < 0 || int(best) >= len(h.marks) {
		return nil
	}
	return &h.marks[best]
}

// rowRuns reuses unchanged rows, including rows unaffected by cursor movement.
func (h *hexView) rowRuns(i int) []textRun {
	if i < 0 || i >= (len(h.data)+hexRow-1)/hexRow {
		return nil
	}
	off := i * hexRow
	end := min(off+hexRow, len(h.data))
	data := string(h.data[off:end])
	cursor := -1
	if h.cur/2 >= off && h.cur/2 < end {
		cursor = h.cur
	}
	var selection color.Color
	if h.focused {
		selection = theme.SelectionColor()
	}
	if cached, ok := h.rowCache[i]; ok && cached.data == data &&
		cached.cursor == cursor && cached.focused == h.focused && cached.selection == selection {
		return cached.runs
	}
	runs := h.buildRowRuns(i)
	if h.rowCache == nil {
		h.rowCache = make(map[int]hexRowCache)
	}
	h.rowCache[i] = hexRowCache{data: data, cursor: cursor, focused: h.focused, selection: selection, runs: runs}
	return runs
}

// buildRowRuns renders offset, hex digits, text, labels and bits.
func (h *hexView) buildRowRuns(i int) []textRun {
	off := i * hexRow
	if off >= len(h.data) {
		return nil
	}
	h.ensureIdx()
	end := min(off+hexRow, len(h.data))
	var b runBuilder
	var sel color.Color
	if h.focused {
		sel = theme.SelectionColor()
	}
	curByte := h.cur / 2
	b.add(fmt.Sprintf("%012X", h.base+int64(off)), markColor(h.markIn(off*8, end*8)), nil, true)
	b.add("  ", nil, nil, false)
	for j := 0; j < hexRow; j++ {
		p := off + j
		if p >= end {
			b.add("   ", nil, nil, false)
		} else {
			v := h.data[p]
			for half := 0; half < 2; half++ {
				nib := v >> 4
				if half == 1 {
					nib = v & 15
				}
				fg := markColor(h.markIn(p*8+half*4, p*8+half*4+4))
				var bg color.Color
				bold := false
				if p == curByte && half == h.cur%2 {
					bold, bg = true, sel
				}
				b.add(hexDigits[nib:nib+1], fg, bg, bold)
			}
			b.add(" ", nil, nil, false)
		}
		if j == 7 {
			b.add(" ", nil, nil, false)
		}
	}
	b.add(" |", nil, nil, false)
	for p := off; p < end; p++ {
		c := h.data[p]
		s := "."
		if c >= 0x20 && c < 0x7f {
			s = string(rune(c))
		}
		var bg color.Color
		bold := p == curByte
		if bold {
			bg = sel
		}
		b.add(s, markColor(h.markIn(p*8, p*8+8)), bg, bold)
	}
	b.add("|", nil, nil, false)
	if len(h.marks) > 0 {
		var seen []int32
		for bit := off * 8; bit < end*8 && bit < len(h.bitIdx); bit++ {
			k := h.bitIdx[bit]
			if k < 0 {
				continue
			}
			dup := false
			for _, s := range seen {
				if s == k {
					dup = true
					break
				}
			}
			if !dup && len(seen) < 6 {
				seen = append(seen, k)
			}
		}
		for _, k := range seen {
			m := h.marks[k]
			b.add(fmt.Sprintf("  [%d:%d) %s", m.Start, m.End, m.Label), m.Color, nil, false)
		}
		b.add("  bits: ", nil, nil, false)
		for p := off; p < end; p++ {
			for bit := 0; bit < 8; bit++ {
				fg := markColor(h.markIn(p*8+bit, p*8+bit+1))
				if (h.data[p]>>uint(7-bit))&1 == 1 {
					b.add("1", fg, nil, false)
				} else {
					b.add("0", fg, nil, false)
				}
			}
			b.add(" ", nil, nil, false)
		}
	}
	b.flush()
	return b.runs
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

// Colored segments (used by the annotation legend) keep standard text
// behavior while supplying an explicit Lua-chosen color.
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
	if text, ok := o.(*canvas.Text); ok {
		text.Color = s.shade
		text.Refresh()
	}
}

func coloredHex(s string, shade color.Color, bold bool) widget.RichTextSegment {
	return &coloredHexSegment{TextSegment: hexSeg(s, bold, false), shade: shade}
}
