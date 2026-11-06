package ui

import (
	"image/color"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// textRun is a span of identically styled characters.
type textRun struct {
	text   string
	fg, bg color.Color
	bold   bool
}

// runBuilder merges adjacent characters with equal style into one run, so a
// row needs a handful of canvas objects instead of one widget per character.
type runBuilder struct {
	runs   []textRun
	cur    []byte
	fg, bg color.Color
	bold   bool
	open   bool
}

func (b *runBuilder) add(s string, fg, bg color.Color, bold bool) {
	if b.open && fg == b.fg && bg == b.bg && bold == b.bold {
		b.cur = append(b.cur, s...)
		return
	}
	b.flush()
	b.cur = append(b.cur[:0], s...)
	b.fg, b.bg, b.bold, b.open = fg, bg, bold, true
}

func (b *runBuilder) flush() {
	if b.open && len(b.cur) > 0 {
		b.runs = append(b.runs, textRun{string(b.cur), b.fg, b.bg, b.bold})
	}
	b.open = false
	b.cur = b.cur[:0]
}

func markColor(m *bitMark) color.Color {
	if m == nil {
		return nil
	}
	return m.Color
}

const fastPad = 4

func fastStyle(bold bool) fyne.TextStyle { return fyne.TextStyle{Monospace: true, Bold: bold} }
func fastLine() float32 {
	return fyne.MeasureText("M", theme.TextSize(), fastStyle(false)).Height + 2
}
func fastCell() float32 { return fyne.MeasureText("0", theme.TextSize(), fastStyle(false)).Width }

// fastText is an ad-hoc virtualized text surface: it draws only the visible
// rows with pooled canvas.Text/Rectangle objects and scrolls by itself.
type fastText struct {
	widget.BaseWidget
	rows              func() int
	row               func(int) []textRun
	grid              bool
	off, xoff         float32
	dragging, dragBar bool
	onTap             func(row int, x float32)
	onHover           func(row int, x float32, ev *desktop.MouseEvent)
	onOut             func()
}

func newFastText(rows func() int, row func(int) []textRun, grid bool) *fastText {
	t := &fastText{rows: rows, row: row, grid: grid}
	t.ExtendBaseWidget(t)
	return t
}

func (t *fastText) maxOff() float32 {
	m := float32(t.rows())*fastLine() - t.Size().Height
	if m < 0 {
		m = 0
	}
	return m
}

func (t *fastText) clamp() {
	if t.off > t.maxOff() {
		t.off = t.maxOff()
	}
	if t.off < 0 {
		t.off = 0
	}
	if t.xoff < 0 {
		t.xoff = 0
	}
}

func (t *fastText) scrollTo(row int) {
	lh := fastLine()
	y := float32(row) * lh
	h := t.Size().Height
	if y < t.off {
		t.off = y
	} else if y+lh > t.off+h {
		t.off = y + lh - h
	}
	t.clamp()
}

func (t *fastText) rowAt(y float32) int {
	r := int((y + t.off) / fastLine())
	if r < 0 {
		r = 0
	}
	return r
}

func (t *fastText) Scrolled(ev *fyne.ScrollEvent) {
	t.off -= ev.Scrolled.DY
	t.xoff -= ev.Scrolled.DX
	t.clamp()
	t.Refresh()
}

func (t *fastText) Tapped(ev *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap(t.rowAt(ev.Position.Y), ev.Position.X+t.xoff-fastPad)
	}
}

func (t *fastText) Dragged(ev *fyne.DragEvent) {
	if !t.dragging {
		t.dragging = true
		t.dragBar = ev.Position.X >= t.Size().Width-16
	}
	if !t.dragBar {
		return
	}
	h := t.Size().Height
	ch := float32(t.rows()) * fastLine()
	if h <= 0 || ch <= h {
		return
	}
	t.off += ev.Dragged.DY * ch / h
	t.clamp()
	t.Refresh()
}

func (t *fastText) DragEnd() { t.dragging = false }

func (t *fastText) MouseIn(ev *desktop.MouseEvent) { t.MouseMoved(ev) }
func (t *fastText) MouseMoved(ev *desktop.MouseEvent) {
	if t.onHover != nil {
		t.onHover(t.rowAt(ev.Position.Y), ev.Position.X+t.xoff-fastPad, ev)
	}
}
func (t *fastText) MouseOut() {
	if t.onOut != nil {
		t.onOut()
	}
}

func (t *fastText) CreateRenderer() fyne.WidgetRenderer {
	r := &fastRenderer{t: t, bar: canvas.NewRectangle(theme.ScrollBarColor())}
	r.rebuild()
	return r
}

type fastRenderer struct {
	t     *fastText
	texts []*canvas.Text
	rects []*canvas.Rectangle
	bar   *canvas.Rectangle
	objs  []fyne.CanvasObject
}

func (r *fastRenderer) Layout(fyne.Size)                { r.rebuild() }
func (r *fastRenderer) MinSize() fyne.Size              { return fyne.NewSize(60, fastLine()*2) }
func (r *fastRenderer) Refresh()                        { r.rebuild(); canvas.Refresh(r.t) }
func (r *fastRenderer) Objects() []fyne.CanvasObject   { return r.objs }
func (r *fastRenderer) Destroy()                        {}

func (r *fastRenderer) text(i int) *canvas.Text {
	for len(r.texts) <= i {
		r.texts = append(r.texts, canvas.NewText("", theme.ForegroundColor()))
	}
	return r.texts[i]
}

func (r *fastRenderer) rect(i int) *canvas.Rectangle {
	for len(r.rects) <= i {
		r.rects = append(r.rects, canvas.NewRectangle(theme.SelectionColor()))
	}
	return r.rects[i]
}

func (r *fastRenderer) rebuild() {
	t := r.t
	size := t.Size()
	t.clamp()
	lh, cw, ts := fastLine(), fastCell(), theme.TextSize()
	fgDef := theme.ForegroundColor()
	n := t.rows()
	first := int(t.off / lh)
	y := float32(first)*lh - t.off
	ti, ri := 0, 0
	for row := first; row < n && y < size.Height; row++ {
		x := float32(fastPad) - t.xoff
		for _, run := range t.row(row) {
			var w float32
			if t.grid {
				w = cw * float32(utf8.RuneCountInString(run.text))
			} else {
				w = fyne.MeasureText(run.text, ts, fastStyle(run.bold)).Width
			}
			if x >= 0 {
				if run.bg != nil {
					rect := r.rect(ri)
					ri++
					if rect.FillColor != run.bg {
						rect.FillColor = run.bg
						rect.Refresh()
					}
					rect.Move(fyne.NewPos(x, y))
					rect.Resize(fyne.NewSize(w, lh))
				}
				txt := r.text(ti)
				ti++
				fg := run.fg
				if fg == nil {
					fg = fgDef
				}
				if txt.Text != run.text || txt.Color != fg || txt.TextStyle.Bold != run.bold || txt.TextSize != ts {
					txt.Text, txt.Color, txt.TextStyle, txt.TextSize = run.text, fg, fastStyle(run.bold), ts
					txt.Refresh()
				}
				txt.Move(fyne.NewPos(x, y+1))
				txt.Resize(fyne.NewSize(w, lh-2))
			}
			x += w
			if x > size.Width {
				break
			}
		}
		y += lh
	}
	r.objs = r.objs[:0]
	for i := 0; i < ri; i++ {
		r.objs = append(r.objs, r.rects[i])
	}
	for i := 0; i < ti; i++ {
		r.objs = append(r.objs, r.texts[i])
	}
	if ch := float32(n) * lh; ch > size.Height && size.Height > 0 {
		th := max(size.Height*size.Height/ch, 20)
		ty := t.off / t.maxOff() * (size.Height - th)
		r.bar.Move(fyne.NewPos(size.Width-8, ty))
		r.bar.Resize(fyne.NewSize(6, th))
		r.objs = append(r.objs, r.bar)
	}
}
