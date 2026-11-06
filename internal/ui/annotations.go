package ui

import (
	"fmt"
	"image/color"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Tooltips belong to the preview control, not to documents or global mouse state.
type markTooltip struct {
	popup *widget.PopUp
	label string
}

func (t *markTooltip) hide() {
	if t.popup != nil {
		t.popup.Hide()
		t.popup = nil
	}
	t.label = ""
}

func (t *markTooltip) show(owner fyne.CanvasObject, pos fyne.Position, label string) {
	if label == "" {
		t.hide()
		return
	}
	if t.popup != nil && label == t.label {
		return
	}
	t.hide()
	c := fyne.CurrentApp().Driver().CanvasForObject(owner)
	if c == nil {
		return
	}
	text := widget.NewLabel(label)
	text.Wrapping = fyne.TextWrapWord
	t.popup = widget.NewPopUp(container.NewGridWrap(fyne.NewSize(440, 130), container.NewVScroll(text)), c)
	t.popup.ShowAtPosition(pos.Add(fyne.NewPos(12, 22)))
	t.label = label
}

func markLabels(marks []bitMark, start, end int64) string {
	var lines []string
	// Include all overlapping meanings, newest/color-winning annotation first.
	for i := len(marks) - 1; i >= 0; i-- {
		m := marks[i]
		if m.Start < end && m.End > start {
			lines = append(lines, fmt.Sprintf("Bits [%d,%d): %s", m.Start, m.End, m.Label))
			if len(lines) == 12 {
				lines = append(lines, "More overlapping annotations omitted")
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// markedText is the colored text preview drawn by the custom fastText
// control. A per-byte winning-mark index is built once per refresh and only
// visible rows are materialized, as merged color runs.
type markedText struct {
	widget.BaseWidget
	ed        *Editor
	view      *fastText
	text      string
	lines     []int
	spans     []lineSpan
	idx       []int32
	truncated bool
	tooltip   markTooltip
}

func newMarkedText(ed *Editor) *markedText {
	v := &markedText{ed: ed}
	v.view = newFastText(v.rowCount, v.rowRuns, false)
	v.view.onHover = v.hover
	v.view.onOut = v.tooltip.hide
	v.ExtendBaseWidget(v)
	return v
}

func (v *markedText) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(v.view) }

func (v *markedText) rowCount() int {
	if v.truncated {
		return len(v.lines) + 1
	}
	return len(v.lines)
}

func (v *markedText) lineEnd(row int) int {
	if row+1 < len(v.lines) {
		return v.lines[row+1] - 1
	}
	return len(v.text)
}

// local maps a preview byte to a page-local original byte offset.
func (v *markedText) local(off, row, lineStart int) int {
	if v.ed.mode == modeWindow && row < len(v.spans) {
		return int(v.spans[row].start) + off - lineStart
	}
	return off
}

func (v *markedText) load() {
	ed := v.ed
	text := ed.entry.Text
	limit := len(text)
	if limit > 128*1024 {
		limit = 128 * 1024
		for limit > 0 && !utf8.RuneStart(text[limit]) {
			limit--
		}
	}
	v.truncated = limit < len(text)
	v.text = text[:limit]
	v.spans = append(v.spans[:0], ed.spans...)
	v.lines = append(v.lines[:0], 0)
	for i := 0; i < len(v.text); i++ {
		if v.text[i] == '\n' {
			v.lines = append(v.lines, i+1)
		}
	}
	n := max(int(ed.src.n), len(v.text))
	if cap(v.idx) < n {
		v.idx = make([]int32, n)
	} else {
		v.idx = v.idx[:n]
	}
	for i := range v.idx {
		v.idx[i] = -1
	}
	lo := ed.src.base * 8
	for k, m := range ed.marks {
		if m.End-lo <= 0 {
			continue
		}
		s := max((m.Start-lo)/8, 0)
		e := min((m.End-lo+7)/8, int64(n))
		for b := s; b < e; b++ {
			v.idx[b] = int32(k)
		}
	}
	v.view.Refresh()
}

func (v *markedText) rowRuns(row int) []textRun {
	if row >= len(v.lines) {
		return []textRun{{text: "[Preview limited to 128 KiB; document is unchanged]"}}
	}
	start, end := v.lines[row], v.lineEnd(row)
	var b runBuilder
	for off := start; off < end; {
		r, n := utf8.DecodeRuneInString(v.text[off:end])
		loc := v.local(off, row, start)
		k := int32(-1)
		for j := max(loc, 0); j < loc+n && j < len(v.idx); j++ {
			if v.idx[j] > k {
				k = v.idx[j]
			}
		}
		var fg color.Color
		if k >= 0 && int(k) < len(v.ed.marks) {
			fg = v.ed.marks[k].Color
		}
		s := v.text[off : off+n]
		if r == '\t' {
			s = "    "
		}
		b.add(s, fg, nil, false)
		off += n
	}
	b.flush()
	return b.runs
}

func (v *markedText) hover(row int, x float32, ev *desktop.MouseEvent) {
	if ev.Modifier&fyne.KeyModifierControl == 0 || v.ed.src == nil || row >= len(v.lines) || x < 0 {
		v.tooltip.hide()
		return
	}
	start, end := v.lines[row], v.lineEnd(row)
	for off := start; off < end; {
		r, n := utf8.DecodeRuneInString(v.text[off:end])
		s := string(r)
		if r == '\t' {
			s = "    "
		}
		w := fyne.MeasureText(s, theme.TextSize(), fastStyle(false)).Width
		if x < w {
			bit := (v.ed.src.base + int64(v.local(off, row, start))) * 8
			v.tooltip.show(v, ev.AbsolutePosition, markLabels(v.ed.marks, bit, bit+int64(n*8)))
			return
		}
		x -= w
		off += n
	}
	v.tooltip.hide()
}

func (ed *Editor) refreshAnnotations() {
	if ed.hex != nil {
		ed.hex.setMarks(ed.marks)
	}
	if ed.annotationBox == nil {
		return
	}
	v := ed.annotationText
	v.tooltip.hide()
	if len(ed.marks) == 0 || ed.src == nil {
		ed.annotationBox.Hide()
		if ed.annotationToggle != nil {
			ed.annotationToggle.Hide()
		}
		return
	}
	if ed.annotationToggle != nil {
		label := "Hide annotations"
		if ed.annotationsHidden {
			label = "Show annotations"
		}
		ed.annotationToggle.SetText(label)
		ed.annotationToggle.Show()
	}
	if ed.annotationsHidden {
		ed.annotationBox.Hide()
		return
	}
	ed.refreshAnnotationLegend()
	ed.annotationBox.Show()
	if ed.mode == modeHex {
		v.Hide()
		return
	}
	v.Show()
	v.load()
}
