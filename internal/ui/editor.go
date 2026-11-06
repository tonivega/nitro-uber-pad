package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nitrobutton.com/nitro-uber-pad/internal/document"
)

type viewMode int

const (
	modeText   viewMode = iota // plain text with line numbers
	modeWindow                 // very long lines shown through a horizontal byte window
	modeHex                    // non-representable data shown as a hex dump
)

const (
	scanChunk   = 64 * 1024
	longLine    = 2000 // lines longer than this (bytes) use the horizontal window
	windowBytes = 1024 // bytes of each long line read at a time
)

type lineSpan struct{ start, end int64 }

// pageSource reads the bytes of one page, either from the pending edit or from
// the file on disk (seek/read through the document cache).
type pageSource struct {
	doc    *document.Document
	base   int64
	n      int64
	edited bool
	mem    []byte
}

func (s *pageSource) read(off, n int64) ([]byte, error) {
	if off < 0 {
		off = 0
	}
	if off+n > s.n {
		n = s.n - off
	}
	if n <= 0 {
		return nil, nil
	}
	if s.edited {
		return s.mem[off : off+n], nil
	}
	return s.doc.ReadAt(s.base+off, n)
}

// checkText validates UTF-8 text incrementally; carry holds an incomplete rune.
func checkText(carry, b []byte, final bool) ([]byte, bool) {
	buf := append(carry, b...)
	for i := 0; i < len(buf); {
		c := buf[i]
		if c < utf8.RuneSelf {
			if (c < 0x20 && c != '\n' && c != '\r' && c != '\t' && c != '\f') || c == 0x7f {
				return nil, false
			}
			i++
			continue
		}
		if !final && !utf8.FullRune(buf[i:]) {
			return append([]byte(nil), buf[i:]...), true
		}
		r, sz := utf8.DecodeRune(buf[i:])
		if (r == utf8.RuneError && sz <= 1) || unicode.Is(unicode.Cc, r) {
			return nil, false
		}
		i += sz
	}
	return nil, true
}

// scanSource finds line spans, the longest line and whether the page is text,
// reading it in bounded chunks.
func scanSource(src *pageSource) ([]lineSpan, int64, bool, error) {
	var lines []lineSpan
	var maxLen, lineStart, off int64
	var carry []byte
	text := true
	add := func(end int64) {
		lines = append(lines, lineSpan{lineStart, end})
		if end-lineStart > maxLen {
			maxLen = end - lineStart
		}
	}
	for off < src.n {
		b, err := src.read(off, scanChunk)
		if err != nil {
			return nil, 0, false, err
		}
		if len(b) == 0 {
			break
		}
		for i, c := range b {
			if c == '\n' {
				add(off + int64(i))
				lineStart = off + int64(i) + 1
			}
		}
		off += int64(len(b))
		if text {
			carry, text = checkText(carry, b, off >= src.n)
		}
	}
	add(src.n)
	return lines, maxLen, !text, nil
}

// Editor is one tab: a paged view on a Document.
type Editor struct {
	app       *App
	doc       *document.Document
	entry     *shortcutEntry
	item      *container.TabItem
	content   fyne.CanvasObject
	title     string
	page      int64
	dirty     bool
	loading   bool
	pageLabel *widget.Label

	mode      viewMode
	forceHex  bool
	marks     []bitMark
	annotationBox *fyne.Container
	annotationText *markedText
	annotationLegend *widget.RichText
	legendCache annotationLegendCache
	annotationToggle *widget.Button
	annotationsHidden bool
	src       *pageSource
	lines     []lineSpan
	spans     []lineSpan
	maxLine   int64
	colOff    int64
	firstLine int64
	lineKnown bool
	gutterKey string

	gutter       *widget.Label
	gutterScroll *container.Scroll
	escroll      *container.Scroll
	textView     *fyne.Container
	hex          *hexView
	info         *widget.Label
	hbar         *fyne.Container
	hslider      *widget.Slider
	hlabel       *widget.Label
	modeLabel    *widget.Label

	banner      *fyne.Container
	diskChanged atomic.Bool
	stop        chan struct{}
	stopOnce    sync.Once
}

func newEditor(a *App, doc *document.Document, title string) *Editor {
	ed := &Editor{app: a, doc: doc, title: title, stop: make(chan struct{})}
	ed.entry = newShortcutEntry(a)
	ed.entry.OnChanged = func(string) {
		if ed.loading {
			return
		}
		if _, ok := checkText(nil, []byte(ed.entry.Text), true); !ok {
			ed.SetText(ed.entry.Text)
			return
		}
		ed.updateGutter()
		ed.markDirty()
	}
	ed.entry.OnCursorChanged = func() {
		if ed.loading {
			return
		}
		ed.followWindow()
		ed.updateInfo()
		ed.syncGutter()
		time.AfterFunc(60*time.Millisecond, ed.syncGutter)
		if a.current() == ed {
			a.updateStatus()
		}
	}
	doc.SetOnChange(func() {
		if !ed.lineKnown {
			ed.firstLine, ed.lineKnown = ed.doc.PageFirstLine(ed.page)
			ed.updateGutter()
		}
		if a.current() == ed {
			a.updateStatus()
		}
	})

	// line number gutter, kept in sync with the entry's own vertical scroll
	ed.gutter = widget.NewLabelWithStyle("1", fyne.TextAlignTrailing, fyne.TextStyle{Monospace: true})
	ed.gutterScroll = container.NewVScroll(ed.gutter)
	ed.gutterScroll.OnScrolled = func(p fyne.Position) {
		if sc := ed.entryScroll(); sc != nil {
			sc.Offset.Y = p.Y
			sc.Refresh()
		}
	}

	// horizontal window controls for very long lines
	ed.hslider = widget.NewSlider(0, 1)
	ed.hslider.Step = windowBytes / 2
	ed.hslider.OnChanged = func(v float64) { ed.setColOff(int64(v)) }
	ed.hlabel = widget.NewLabel("")
	home := widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() { ed.setColOff(0) })
	left := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { ed.setColOff(ed.colOff - windowBytes/2) })
	right := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() { ed.setColOff(ed.colOff + windowBytes/2) })
	ed.hbar = container.NewBorder(nil, nil, container.NewHBox(widget.NewLabel("Long lines:"), home, left),
		container.NewHBox(right, ed.hlabel), ed.hslider)
	ed.hbar.Hide()

	editArea := container.NewBorder(nil, nil, ed.gutterScroll, nil, ed.entry)
	ed.textView = container.NewBorder(nil, ed.hbar, nil, nil, editArea)
	ed.hex = newHexView(ed.markDirty, func() {
		if a.current() == ed {
			a.updateStatus()
		}
	})
	ed.hex.onShortcut = a.handleShortcut
	ed.hex.onF3 = a.findNext
	ed.hex.content.Hide()
	ed.info = widget.NewLabel("")
	ed.info.Wrapping = fyne.TextTruncate

	first := widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() { ed.gotoPage(0) })
	prev := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { ed.gotoPage(ed.page - 1) })
	next := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() { ed.gotoPage(ed.page + 1) })
	last := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() { ed.gotoPage(ed.doc.PageCount() - 1) })
	ed.pageLabel = widget.NewLabel("")
	ed.modeLabel = widget.NewLabel("")
	goEntry := newPositionEntry(a, positionHelp)
	goEntry.SetPlaceHolder("page[:line[:col]] / offset")
	jump := func() {
		if err := ed.jumpPosition(goEntry.Text); err != nil { dialog.ShowError(err, a.win) }
	}
	goEntry.OnSubmitted = func(string) { jump() }
	goBtn := widget.NewButton("Go", jump)
	ed.annotationToggle = widget.NewButton("Hide annotations", func() {
		ed.annotationsHidden = !ed.annotationsHidden
		ed.refreshAnnotations()
	})
	ed.annotationToggle.Hide()
	pager := container.NewHBox(first, prev, ed.pageLabel, next, last, ed.modeLabel, ed.annotationToggle, layout.NewSpacer(),
		widget.NewLabel("Cursor:"),
		container.NewGridWrap(fyne.NewSize(220, goEntry.MinSize().Height), goEntry), goBtn)
	// banner shown when the file is changed on disk by another program
	bannerMsg := widget.NewLabelWithStyle("This file has been changed on disk by another program. Saving will ask before overwriting it.",
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	bannerMsg.Wrapping = fyne.TextTruncate
	reloadBtn := widget.NewButtonWithIcon("Reload from disk", theme.ViewRefreshIcon(), ed.ReloadPrompt)
	ed.banner = container.NewStack(canvas.NewRectangle(theme.WarningColor()),
		container.NewBorder(nil, nil, widget.NewIcon(theme.WarningIcon()), reloadBtn, bannerMsg))
	ed.banner.Hide()

	ed.annotationText = newMarkedText(ed)
	ed.annotationLegend = widget.NewRichText()
	ed.annotationLegend.Wrapping = fyne.TextWrapWord
	annotationSplit := container.NewVSplit(ed.annotationText, container.NewVScroll(ed.annotationLegend))
	annotationSplit.Offset = 0.35
	collapse := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		ed.annotationsHidden = true
		ed.refreshAnnotations()
	})
	ed.annotationBox = container.NewBorder(container.NewBorder(nil, nil, nil, collapse,
		widget.NewLabel("Annotations — current page; values are MSB-first")), nil, nil, nil, annotationSplit)
	ed.annotationBox.Hide()
	split := container.NewHSplit(container.NewStack(ed.textView, ed.hex.content), ed.annotationBox)
	split.Offset = 0.65
	ed.content = container.NewBorder(ed.banner, container.NewVBox(ed.info, pager), nil, nil, split)
	return ed
}

func (ed *Editor) markDirty() {
	ed.dirty = true
	ed.refreshAnnotations()
	ed.refreshTitle()
	ed.app.updateStatus()
}

// Modified reports unsaved changes in the page being edited or the document.
func (ed *Editor) Modified() bool { return ed.dirty || ed.doc.Modified() }

func (ed *Editor) refreshTitle() {
	if ed.item == nil {
		return
	}
	t := ed.title
	if ed.Modified() {
		t = "* " + t
	}
	if ed.diskChanged.Load() {
		t = "! " + t
	}
	if ed.item.Text != t {
		ed.item.Text = t
		ed.app.tabs.Refresh()
	}
}

func (ed *Editor) newSource(k int64) (*pageSource, error) {
	start, end := ed.doc.PageBounds(k)
	src := &pageSource{doc: ed.doc, base: start, n: end - start}
	s, ok, err := ed.doc.EditedPage(k)
	if err != nil {
		return nil, err
	}
	if ok {
		src.edited, src.mem, src.n = true, []byte(s), int64(len(s))
	}
	return src, nil
}

// pageText returns the full text of the current page including unsaved view edits.
func (ed *Editor) pageText() (string, error) {
	switch ed.mode {
	case modeWindow:
		return ed.windowCommitText()
	case modeHex:
		return string(ed.hex.data), nil
	}
	return ed.entry.Text, nil
}

// commit stores the edited page text in the document's change set.
func (ed *Editor) commit() error {
	if !ed.dirty {
		return nil
	}
	text, err := ed.pageText()
	if err != nil {
		return err
	}
	if err := ed.doc.SetPage(ed.page, text); err != nil {
		return err
	}
	ed.dirty = false
	return nil
}

func (ed *Editor) gotoPage(k int64) {
	if err := ed.commit(); err != nil {
		dialog.ShowError(err, ed.app.win)
		return
	}
	ed.loadPage(k)
}

func (ed *Editor) loadPage(k int64) {
	n := ed.doc.PageCount()
	if k >= n {
		k = n - 1
	}
	if k < 0 {
		k = 0
	}
	src, err := ed.newSource(k)
	if err != nil {
		dialog.ShowError(err, ed.app.win)
		return
	}
	lines, maxLen, binary, err := scanSource(src)
	if err != nil {
		dialog.ShowError(err, ed.app.win)
		return
	}
	ed.page, ed.src, ed.lines, ed.maxLine = k, src, lines, maxLen
	ed.firstLine, ed.lineKnown = ed.doc.PageFirstLine(k)
	mode := modeText
	if maxLen > longLine {
		mode = modeWindow
	}
	if binary || ed.forceHex {
		mode = modeHex
	}
	ed.mode = mode
	ed.colOff = 0
	ed.dirty = false
	if err := ed.render(); err != nil {
		dialog.ShowError(err, ed.app.win)
	}
	ed.pageLabel.SetText(fmt.Sprintf("Page %d / %d", k+1, n))
	ed.refreshTitle()
	ed.app.updateStatus()
	if mode == modeHex {
		ed.app.Notify("Page contains non-text data: hex view (type hex digits to edit nibbles in place)")
		ed.app.win.Canvas().Focus(ed.hex)
	} else {
		ed.app.win.Canvas().Focus(ed.entry)
	}
}

// render shows the current page in the current mode.
func (ed *Editor) render() error {
	ed.loading = true
	defer func() { ed.loading = false; ed.refreshAnnotations() }()
	switch ed.mode {
	case modeHex:
		b, err := ed.src.read(0, ed.src.n)
		if err != nil {
			return err
		}
		ed.hex.cur = 0
		ed.hex.setMarks(ed.marks)
		ed.hex.setData(ed.src.base, append([]byte(nil), b...))
		ed.textView.Hide()
		ed.info.Hide()
		ed.hex.content.Show()
		ed.modeLabel.SetText("[hex]")
		return nil
	case modeWindow:
		text, err := ed.windowText()
		if err != nil {
			return err
		}
		ed.entry.SetText(text)
		ed.hslider.Max = float64(max(ed.maxLine, 1))
		ed.hslider.SetValue(float64(ed.colOff))
		ed.hlabel.SetText(fmt.Sprintf("bytes %d-%d of %d", ed.colOff, min(ed.colOff+windowBytes, ed.maxLine), ed.maxLine))
		ed.hbar.Show()
		ed.modeLabel.SetText("[long lines]")
	default:
		b, err := ed.src.read(0, ed.src.n)
		if err != nil {
			return err
		}
		ed.entry.SetText(string(b))
		ed.hbar.Hide()
		ed.modeLabel.SetText("[text]")
	}
	ed.entry.CursorRow, ed.entry.CursorColumn = 0, 0
	ed.entry.Refresh()
	ed.hex.content.Hide()
	ed.textView.Show()
	ed.info.Show()
	ed.updateGutter()
	ed.updateInfo()
	return nil
}

// windowText reads only a window of each line (seek/read per line), aligned to
// UTF-8 rune boundaries, and records the byte span shown for every line.
func (ed *Editor) windowText() (string, error) {
	var sb strings.Builder
	ed.spans = ed.spans[:0]
	for i, ln := range ed.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		s := min(ln.start+ed.colOff, ln.end)
		e := min(s+windowBytes, ln.end)
		if e <= s {
			ed.spans = append(ed.spans, lineSpan{s, s})
			continue
		}
		b, err := ed.src.read(s, min(e+3, ln.end)-s)
		if err != nil {
			return "", err
		}
		lo := 0
		for lo < len(b) && lo < 3 && b[lo]&0xC0 == 0x80 {
			lo++
		}
		hi := min(int(e-s), len(b))
		for hi < len(b) && hi > lo && b[hi]&0xC0 == 0x80 {
			hi--
		}
		if hi < lo {
			hi = lo
		}
		sb.Write(b[lo:hi])
		ed.spans = append(ed.spans, lineSpan{s + int64(lo), s + int64(hi)})
	}
	return sb.String(), nil
}

// windowCommitText splices the edited windows back into the full page lines.
func (ed *Editor) windowCommitText() (string, error) {
	edited := strings.Split(ed.entry.Text, "\n")
	full, err := ed.src.read(0, ed.src.n)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	n := len(ed.spans)
	for i, sp := range ed.spans {
		if i >= len(ed.lines) {
			break
		}
		ln := ed.lines[i]
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.Write(full[ln.start:sp.start])
		if i < len(edited) {
			if i < n-1 {
				sb.WriteString(edited[i])
			} else {
				sb.WriteString(strings.Join(edited[i:], "\n"))
			}
		}
		sb.Write(full[sp.end:ln.end])
	}
	return sb.String(), nil
}

// refreshAfterCommit commits view edits and rescans the page source.
func (ed *Editor) refreshAfterCommit() error {
	if !ed.dirty {
		return nil
	}
	if err := ed.commit(); err != nil {
		return err
	}
	src, err := ed.newSource(ed.page)
	if err != nil {
		return err
	}
	lines, maxLen, _, err := scanSource(src)
	if err != nil {
		return err
	}
	ed.src, ed.lines, ed.maxLine = src, lines, maxLen
	return nil
}

// setColOff moves the horizontal window, loading the new part of each line.
func (ed *Editor) setColOff(c int64) {
	if ed.loading || ed.mode != modeWindow {
		return
	}
	if c > ed.maxLine-windowBytes/2 {
		c = ed.maxLine - windowBytes/2
	}
	if c < 0 {
		c = 0
	}
	if c == ed.colOff {
		return
	}
	if err := ed.refreshAfterCommit(); err != nil {
		dialog.ShowError(err, ed.app.win)
		return
	}
	row := ed.entry.CursorRow
	ed.colOff = c
	if err := ed.render(); err != nil {
		dialog.ShowError(err, ed.app.win)
		return
	}
	if row < len(ed.lines) {
		ed.entry.CursorRow = row
		ed.entry.Refresh()
	}
	ed.app.updateStatus()
}

// followWindow loads more of a long line when the cursor reaches the window end.
func (ed *Editor) followWindow() {
	if ed.mode != modeWindow {
		return
	}
	row, col := ed.entry.CursorRow, ed.entry.CursorColumn
	if row >= len(ed.spans) || row >= len(ed.lines) || col == 0 {
		return
	}
	sp, ln := ed.spans[row], ed.lines[row]
	line := lineAt(ed.entry.Text, row)
	if sp.end >= ln.end || col < utf8.RuneCountInString(line) {
		return
	}
	inLine := sp.start - ln.start + int64(len(line))
	ed.setColOff(ed.colOff + windowBytes/2)
	if row >= len(ed.spans) || row >= len(ed.lines) {
		return
	}
	nl := lineAt(ed.entry.Text, row)
	idx := inLine - (ed.spans[row].start - ed.lines[row].start)
	idx = max(0, min(idx, int64(len(nl))))
	ed.entry.CursorRow = row
	ed.entry.CursorColumn = utf8.RuneCountInString(nl[:idx])
	ed.entry.Refresh()
}

// updateGutter refreshes the line numbers (absolute file lines when known).
func (ed *Editor) updateGutter() {
	if ed.mode == modeHex {
		return
	}
	n := strings.Count(ed.entry.Text, "\n") + 1
	key := fmt.Sprintf("%d/%d/%v", n, ed.firstLine, ed.lineKnown)
	if key == ed.gutterKey {
		return
	}
	ed.gutterKey = key
	var sb strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if ed.lineKnown {
			sb.WriteString(strconv.FormatInt(ed.firstLine+int64(i)+1, 10))
		} else {
			sb.WriteString("+" + strconv.Itoa(i))
		}
	}
	ed.gutter.SetText(sb.String())
	ed.syncGutter()
}

func findScroll(objs []fyne.CanvasObject) *container.Scroll {
	for _, o := range objs {
		switch v := o.(type) {
		case *container.Scroll:
			return v
		case *fyne.Container:
			if s := findScroll(v.Objects); s != nil {
				return s
			}
		}
	}
	return nil
}

// entryScroll locates the entry's internal scroller so the gutter can follow it.
func (ed *Editor) entryScroll() *container.Scroll {
	r := test.WidgetRenderer(ed.entry)
	if r == nil {
		return nil
	}
	sc := findScroll(r.Objects())
	if sc != nil && sc != ed.escroll {
		ed.escroll = sc
		prev := sc.OnScrolled
		sc.OnScrolled = func(p fyne.Position) {
			if prev != nil {
				prev(p)
			}
			ed.syncGutter()
		}
	}
	return sc
}

func (ed *Editor) syncGutter() {
	if ed.mode == modeHex {
		return
	}
	sc := ed.entryScroll()
	if sc == nil || ed.gutterScroll.Offset.Y == sc.Offset.Y {
		return
	}
	ed.gutterScroll.Offset.Y = sc.Offset.Y
	ed.gutterScroll.Refresh()
}

func (ed *Editor) updateInfo() {
	if ed.mode == modeHex {
		return
	}
	ed.info.SetText(inspectText(lineAt(ed.entry.Text, ed.entry.CursorRow), ed.entry.CursorColumn))
}

// Cursor returns the 0-based cursor position: line/column in text views,
// 16-byte row / byte column in the hex view.
func (ed *Editor) Cursor() (int, int) {
	if ed.mode == modeHex {
		b := ed.hex.cur / 2
		return b / hexRow, b % hexRow
	}
	return ed.entry.CursorRow, ed.entry.CursorColumn
}

// SetCursor moves the cursor (0-based, clamped to the page content).
func (ed *Editor) SetCursor(row, col int) {
	row, col = max(row, 0), max(col, 0)
	if ed.mode == modeHex {
		ed.hex.setCursor((row*hexRow + min(col, hexRow-1)) * 2)
		ed.app.win.Canvas().Focus(ed.hex)
		return
	}
	text := ed.entry.Text
	row = min(row, strings.Count(text, "\n"))
	col = min(col, utf8.RuneCountInString(lineAt(text, row)))
	ed.entry.CursorRow, ed.entry.CursorColumn = row, col
	ed.entry.Refresh()
	ed.app.win.Canvas().Focus(ed.entry)
	ed.updateInfo()
	ed.syncGutter()
	if ed.app.current() == ed {
		ed.app.updateStatus()
	}
}

// watch polls the file on disk and flags external modifications.
func (ed *Editor) watch() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	stamp := ed.diskStamp()
	for {
		select {
		case <-ed.stop:
			return
		case <-t.C:
			next := ed.diskStamp()
			if next != stamp && ed.doc.ChangedOnDisk() {
				ed.app.Log("disk", fmt.Sprintf("Changed %q: %s -> %s (poll interval 2s)", ed.doc.Path(), stamp, next))
			}
			stamp = next
			if !ed.diskChanged.Load() && ed.doc.ChangedOnDisk() {
				ed.showDiskChanged()
			}
		}
	}
}

func (ed *Editor) diskStamp() string {
	p := ed.doc.Path()
	if p == "" {
		return "unsaved"
	}
	st, err := os.Stat(p)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("size=%d modified=%s", st.Size(), st.ModTime().Format(time.RFC3339Nano))
}

// close stops the watcher and releases the document exactly once.
func (ed *Editor) close() {
	ed.stopOnce.Do(func() {
		close(ed.stop)
		ed.app.Log("file", fmt.Sprintf("Closed %q (unsaved=%t)", ed.doc.Path(), ed.Modified()))
		ed.doc.Close()
	})
}

// showDiskChanged shows the visual "changed on disk" indication.
func (ed *Editor) showDiskChanged() {
	ed.diskChanged.Store(true)
	ed.banner.Show()
	ed.refreshTitle()
	ed.app.Notify(ed.title + " changed on disk")
	if ed.app.current() == ed {
		ed.app.updateStatus()
	}
}

// ReloadPrompt reloads the file from disk, asking first if there are unsaved changes.
func (ed *Editor) ReloadPrompt() {
	if ed.doc.IsNew() {
		ed.app.Notify("Not saved yet: nothing to reload")
		return
	}
	do := func() {
		if err := ed.reload(); err != nil {
			dialog.ShowError(err, ed.app.win)
		}
	}
	if ed.Modified() {
		dialog.ShowConfirm("Reload from disk", "Discard your unsaved changes to "+ed.title+" and load the version on disk?",
			func(ok bool) {
				if ok {
					do()
				}
			}, ed.app.win)
		return
	}
	do()
}

// reload discards pending edits and reloads the file from disk.
func (ed *Editor) reload() error {
	if ed.doc.IsNew() {
		return errors.New("document has not been saved yet")
	}
	ed.dirty = false
	if err := ed.doc.Reload(); err != nil {
		ed.app.Log("file", fmt.Sprintf("Reload failed %q: %v", ed.doc.Path(), err))
		return err
	}
	ed.marks = nil
	ed.app.Log("file", fmt.Sprintf("Reloaded %q (%d bytes; pending edits discarded)", ed.doc.Path(), ed.doc.Size()))
	ed.diskChanged.Store(false)
	ed.banner.Hide()
	ed.loadPage(ed.page)
	ed.app.Notify("Reloaded " + ed.doc.Path())
	return nil
}

// position describes the cursor for the status bar.
func (ed *Editor) position() string {
	if ed.mode == modeHex {
		if ed.src == nil || len(ed.hex.data) == 0 {
			return "Hex"
		}
		i := ed.hex.cur / 2
		half := "high"
		if ed.hex.cur%2 == 1 {
			half = "low"
		}
		o := ed.hex.base + int64(i)
		return fmt.Sprintf("Hex @ offset %d (0x%X), byte %02X, %s nibble", o, o, ed.hex.data[i], half)
	}
	row := ed.entry.CursorRow
	ln := fmt.Sprintf("+%d", row)
	if ed.lineKnown {
		ln = strconv.FormatInt(ed.firstLine+int64(row)+1, 10)
	}
	col := ed.entry.CursorColumn + 1
	if ed.mode == modeWindow && row < len(ed.spans) && row < len(ed.lines) {
		return fmt.Sprintf("Ln %s, Col %d (line byte ~%d)", ln, col, ed.spans[row].start-ed.lines[row].start+int64(col-1))
	}
	return fmt.Sprintf("Ln %s, Col %d", ln, col)
}

// viewInfo summarizes the current view for the status bar.
func (ed *Editor) viewInfo() string {
	switch ed.mode {
	case modeHex:
		return fmt.Sprintf("hex view, %d bytes", len(ed.hex.data))
	case modeWindow:
		return fmt.Sprintf("long lines (max %s), window at byte %d", humanSize(ed.maxLine), ed.colOff)
	}
	t := ed.entry.Text
	return fmt.Sprintf("page: %d chars, %d words", utf8.RuneCountInString(t), countWords(t))
}

// Text returns the full text of the current page.
func (ed *Editor) Text() string {
	t, err := ed.pageText()
	if err != nil {
		return ""
	}
	return t
}

// SetText replaces the current page text.
func (ed *Editor) SetText(s string) {
	if err := ed.doc.SetPage(ed.page, s); err != nil {
		dialog.ShowError(err, ed.app.win)
		return
	}
	ed.dirty = false
	ed.marks = nil // content coordinates may have changed
	ed.loadPage(ed.page)
}

// Insert inserts s at the cursor position.
func (ed *Editor) Insert(s string) {
	if ed.mode == modeHex {
		ed.app.Alert("Text insertion is not available in the hex view")
		return
	}
	r := []rune(ed.entry.Text)
	row, col := ed.entry.CursorRow, ed.entry.CursorColumn
	off := 0
	for row > 0 && off < len(r) {
		if r[off] == '\n' {
			row--
		}
		off++
	}
	off += col
	if off > len(r) {
		off = len(r)
	}
	nr, nc := ed.entry.CursorRow, col
	for _, c := range s {
		if c == '\n' {
			nr++
			nc = 0
		} else {
			nc++
		}
	}
	ed.entry.SetText(string(r[:off]) + s + string(r[off:]))
	ed.entry.CursorRow, ed.entry.CursorColumn = nr, nc
	ed.entry.Refresh()
	ed.markDirty()
}

var errDiskChanged = errors.New("file changed on disk: confirm overwriting it in the dialog, or reload it")

// Save saves an existing file in place. When the file was changed on disk by
// another program and force is false, a confirmation dialog is shown and
// errDiskChanged is returned (the dialog saves if confirmed).
func (ed *Editor) Save(force bool) error {
	if ed.doc.IsNew() {
		return errors.New("document has never been saved; use save_as")
	}
	return ed.saveChecked(ed.doc.Path(), force)
}

// SaveAs saves to a new path and switches the tab to it.
func (ed *Editor) SaveAs(p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	if abs == ed.doc.Path() {
		return ed.saveChecked(abs, false)
	}
	return ed.saveTo(abs)
}

func (ed *Editor) saveChecked(target string, force bool) error {
	if force || !(ed.diskChanged.Load() || ed.doc.ChangedOnDisk()) {
		return ed.saveTo(target)
	}
	ed.showDiskChanged()
	dialog.ShowConfirm("File changed on disk",
		filepath.Base(target)+" was modified by another program after it was opened.\n"+
			"Saving will overwrite those changes. Save anyway?\n"+
			"(Use \"Reload from disk\" to load the version on disk instead.)",
		func(ok bool) {
			if !ok {
				return
			}
			if err := ed.saveTo(target); err != nil {
				dialog.ShowError(err, ed.app.win)
				return
			}
			ed.app.Notify("Saved " + target)
		}, ed.app.win)
	return errDiskChanged
}

func (ed *Editor) saveTo(target string) error {
	if err := ed.commit(); err != nil {
		return err
	}
	oldSize := ed.doc.Size()
	changes, err := ed.doc.Save(target)
	if err != nil {
		ed.app.Log("file", fmt.Sprintf("Save failed %q: %v", target, err))
		return err
	}
	ed.app.Log("file", fmt.Sprintf("Saved %q: %d -> %d bytes, %d page replacement(s)", target, oldSize, ed.doc.Size(), len(changes)))
	ed.title = filepath.Base(target)
	ed.writeLog(target, oldSize, changes)
	ed.app.cfg.AddRecent(target)
	ed.app.saveConfig()
	ed.diskChanged.Store(false)
	ed.banner.Hide()
	ed.loadPage(ed.page)
	return nil
}

func (ed *Editor) writeLog(target string, oldSize int64, changes []document.Change) {
	now := time.Now().Format("2006-01-02 15:04:05")
	lines := []string{fmt.Sprintf("[%s] SAVE %s  size %d -> %d bytes, %d page edit(s)",
		now, target, oldSize, ed.doc.Size(), len(changes))}
	for _, c := range changes {
		old := c.End - c.Start
		lines = append(lines, fmt.Sprintf("    page %d  offset [%d,%d)  %d -> %d bytes (%+d)",
			c.Page+1, c.Start, c.End, old, c.NewLen, c.NewLen-old))
	}
	if err := document.AppendLog(document.LogPath(target), lines); err != nil {
		ed.app.Notify("history log: " + err.Error())
	}
}
