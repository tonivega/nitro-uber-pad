// Package document implements paged, seek/read based access to files of any size.
//
// The file is split into a deterministic grid of pages (in original file
// coordinates) whose boundaries are aligned to line starts. Only the visible
// page is loaded, using a read cache that also keeps data before and after the
// requested range to avoid excessive disk access while navigating. Edits are
// stored per page (in memory, or spilled to temp files when large) together
// with their original byte ranges and are applied in a streaming way on save.
package document

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	spillThreshold = 1 << 20  // single edits above this go to disk
	memBudget      = 32 << 20 // total in-memory edit budget
	copyChunk      = 1 << 20
)

// Change describes a pending or applied page replacement.
type Change struct {
	Page, Start, End, NewLen int64
}

type edit struct {
	start, end int64
	mem        []byte
	file       string
	n          int64
}

func (e *edit) content() (string, error) {
	if e.file != "" {
		b, err := os.ReadFile(e.file)
		return string(b), err
	}
	return string(e.mem), nil
}

func (e *edit) writeTo(w io.Writer) error {
	if e.file != "" {
		f, err := os.Open(e.file)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}
	_, err := w.Write(e.mem)
	return err
}

// Document is a file opened for paged editing.
type Document struct {
	mu       sync.Mutex
	path     string
	sourceTemp string // backing store for an unsaved downloaded buffer
	f        *os.File
	size     int64
	pageSize int64
	cache    []byte
	cacheOff int64
	edits    map[int64]*edit
	memUsed  int64
	lines    int64
	lineIdx  []int64 // newlines before each pageSize boundary (from the background scan)
	gen      int64
	onChange func()
	modTime  time.Time
}

// Open opens path for paged editing. An empty path creates a new, unsaved document.
func Open(path string, pageSize int64) (*Document, error) {
	if pageSize < 4096 {
		pageSize = 64 * 1024
	}
	d := &Document{path: path, pageSize: pageSize, edits: map[int64]*edit{}, lines: 1}
	if path == "" {
		return d, nil
	}
	if err := d.openFile(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Document) openFile() error {
	f, err := os.Open(d.path)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if st.IsDir() {
		f.Close()
		return fmt.Errorf("%s is a directory", d.path)
	}
	d.f = f
	d.size = st.Size()
	d.modTime = st.ModTime()
	d.cache = nil
	d.cacheOff = 0
	d.lineIdx = nil
	gen := atomic.AddInt64(&d.gen, 1)
	atomic.StoreInt64(&d.lines, -1)
	go d.countLines(d.path, gen)
	return nil
}

// countLines scans the file in the background with its own handle.
func (d *Document) countLines(path string, gen int64) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, d.pageSize)
	idx := []int64{0}
	var n int64
	for {
		if atomic.LoadInt64(&d.gen) != gen {
			return
		}
		k, err := io.ReadFull(f, buf)
		n += int64(bytes.Count(buf[:k], []byte{'\n'}))
		if k == len(buf) {
			idx = append(idx, n)
		}
		if err != nil {
			break
		}
	}
	d.mu.Lock()
	if atomic.LoadInt64(&d.gen) != gen {
		d.mu.Unlock()
		return
	}
	d.lineIdx = idx
	atomic.StoreInt64(&d.lines, n+1)
	cb := d.onChange
	d.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// SetOnChange registers a callback for background updates (e.g. line count).
func (d *Document) SetOnChange(fn func()) {
	d.mu.Lock()
	d.onChange = fn
	d.mu.Unlock()
}

// readAt returns n bytes at off using a seek/read cache that keeps one page
// of data before and after the requested range. Caller holds d.mu.
// The returned slice aliases the cache and must not be retained.
func (d *Document) readAt(off, n int64) ([]byte, error) {
	if d.f == nil || n <= 0 || off >= d.size {
		return nil, nil
	}
	if off+n > d.size {
		n = d.size - off
	}
	if d.cache != nil && off >= d.cacheOff && off+n <= d.cacheOff+int64(len(d.cache)) {
		s := off - d.cacheOff
		return d.cache[s : s+n], nil
	}
	start := off - d.pageSize
	if start < 0 {
		start = 0
	}
	end := off + n + d.pageSize
	if end > d.size {
		end = d.size
	}
	buf := d.cache
	if int64(cap(buf)) < end-start {
		buf = make([]byte, end-start)
	}
	buf = buf[:end-start]
	if _, err := d.f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(d.f, buf); err != nil {
		d.cache = nil
		return nil, err
	}
	d.cache = buf
	d.cacheOff = start
	s := off - start
	return buf[s : s+n], nil
}

// align maps a raw grid offset to the start of the next line (deterministic).
func (d *Document) align(o int64) int64 {
	if o <= 0 {
		return 0
	}
	if o >= d.size {
		return d.size
	}
	b, err := d.readAt(o-1, d.pageSize/4)
	if err == nil {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			return o + int64(i)
		}
	}
	// very long line: cut at the grid, backing off to a UTF-8 rune start
	for k := 0; k < 3 && o > 0; k++ {
		b, err = d.readAt(o, 1)
		if err != nil || len(b) != 1 || b[0]&0xC0 != 0x80 {
			break
		}
		o--
	}
	return o
}

func (d *Document) pageRange(k int64) (int64, int64) {
	start := d.align(k * d.pageSize)
	end := d.align((k + 1) * d.pageSize)
	if end < start {
		end = start
	}
	return start, end
}

func (d *Document) pageCount() int64 {
	if d.size == 0 {
		return 1
	}
	return (d.size + d.pageSize - 1) / d.pageSize
}

// PageCount returns the number of pages.
func (d *Document) PageCount() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pageCount()
}

// Page returns the current (possibly edited) content of page k.
func (d *Document) Page(k int64) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.edits[k]; ok {
		return e.content()
	}
	s, e := d.pageRange(k)
	b, err := d.readAt(s, e-s)
	return string(b), err
}

// PageBounds returns the original byte range [start,end) of page k.
func (d *Document) PageBounds(k int64) (int64, int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pageRange(k)
}

// EditedPage returns the pending edited content of page k, if any.
func (d *Document) EditedPage(k int64) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.edits[k]
	if !ok {
		return "", false, nil
	}
	s, err := e.content()
	return s, true, err
}

// ReadAt returns a copy of up to n original bytes at off, read with seek/read
// through the read-ahead/behind cache.
func (d *Document) ReadAt(off, n int64) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, err := d.readAt(off, n)
	return append([]byte(nil), b...), err
}

// PageFirstLine returns the 0-based line index (in the file on disk) of the
// first line of page k. ok is false while the background line scan runs.
func (d *Document) PageFirstLine(k int64) (int64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if k <= 0 {
		return 0, true
	}
	if d.f == nil || k >= int64(len(d.lineIdx)) {
		return 0, false
	}
	start, _ := d.pageRange(k)
	base := k * d.pageSize
	n := d.lineIdx[k]
	if start > base {
		b, err := d.readAt(base, start-base)
		if err != nil {
			return 0, false
		}
		n += int64(bytes.Count(b, []byte{'\n'}))
	}
	return n, true
}

func (d *Document) dropEdit(k int64, e *edit) {
	if e.file != "" {
		_ = os.Remove(e.file)
	} else {
		d.memUsed -= e.n
	}
	delete(d.edits, k)
}

// SetPage stores new content for page k (kept until Save).
func (d *Document) SetPage(k int64, text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	start, end := d.pageRange(k)
	if old, ok := d.edits[k]; ok {
		d.dropEdit(k, old)
	}
	orig, err := d.readAt(start, end-start)
	if err != nil {
		return err
	}
	if string(orig) == text {
		return nil
	}
	e := &edit{start: start, end: end, n: int64(len(text))}
	if e.n > spillThreshold || d.memUsed+e.n > memBudget {
		f, err := os.CreateTemp("", "nup-edit-*.tmp")
		if err != nil {
			return err
		}
		if _, err := io.WriteString(f, text); err != nil {
			f.Close()
			os.Remove(f.Name())
			return err
		}
		f.Close()
		e.file = f.Name()
	} else {
		e.mem = []byte(text)
		d.memUsed += e.n
	}
	d.edits[k] = e
	return nil
}

func (d *Document) sortedKeys() []int64 {
	keys := make([]int64, 0, len(d.edits))
	for k := range d.edits {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// Pending lists unsaved page edits.
func (d *Document) Pending() []Change {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Change
	for _, k := range d.sortedKeys() {
		e := d.edits[k]
		out = append(out, Change{k, e.start, e.end, e.n})
	}
	return out
}

// copyRange streams original bytes [from,to) to w using seeks and chunked reads.
func (d *Document) copyRange(w io.Writer, from, to int64) error {
	if to <= from {
		return nil
	}
	if d.f == nil {
		return errors.New("no source file")
	}
	if _, err := d.f.Seek(from, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, copyChunk)
	for rem := to - from; rem > 0; {
		n := int64(len(buf))
		if rem < n {
			n = rem
		}
		if _, err := io.ReadFull(d.f, buf[:n]); err != nil {
			return err
		}
		if _, err := w.Write(buf[:n]); err != nil {
			return err
		}
		rem -= n
	}
	return nil
}

// Save applies all edits, writing to target (or the current path when empty)
// via a temp file + rename, then reopens the document on the result.
func (d *Document) Save(target string) ([]Change, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if target == "" {
		target = d.path
	}
	if target == "" {
		return nil, errors.New("no file name")
	}
	tmp := target + ".nup.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	fail := func(err error) ([]Change, error) {
		out.Close()
		os.Remove(tmp)
		return nil, err
	}
	w := bufio.NewWriterSize(out, copyChunk)
	var pos int64
	var changes []Change
	for _, k := range d.sortedKeys() {
		e := d.edits[k]
		if err := d.copyRange(w, pos, e.start); err != nil {
			return fail(err)
		}
		if err := e.writeTo(w); err != nil {
			return fail(err)
		}
		changes = append(changes, Change{k, e.start, e.end, e.n})
		pos = e.end
	}
	if err := d.copyRange(w, pos, d.size); err != nil {
		return fail(err)
	}
	if err := w.Flush(); err != nil {
		return fail(err)
	}
	_ = out.Sync()
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if d.f != nil {
		d.f.Close()
		d.f = nil
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(target) // Windows cannot rename over existing files
		if err2 := os.Rename(tmp, target); err2 != nil {
			if d.path != "" {
				_ = d.openFile()
			}
			return nil, err2
		}
	}
	d.clearEdits()
	if d.sourceTemp != "" {
		_ = os.Remove(d.sourceTemp)
		d.sourceTemp = ""
	}
	d.path = target
	return changes, d.openFile()
}

func (d *Document) clearEdits() {
	for k, e := range d.edits {
		d.dropEdit(k, e)
	}
	d.memUsed = 0
}

// Close releases the file handle and temp files.
func (d *Document) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	atomic.AddInt64(&d.gen, 1)
	if d.f != nil {
		d.f.Close()
		d.f = nil
	}
	d.clearEdits()
	if d.sourceTemp != "" {
		_ = os.Remove(d.sourceTemp)
		d.sourceTemp = ""
	}
}

// OpenBuffer creates an unsaved, disk-backed buffer. Saving requires a local
// target; the temporary source is removed on save or close.
func OpenBuffer(data []byte, pageSize int64) (*Document, error) {
	f, err := os.CreateTemp("", "nup-download-*.tmp")
	if err != nil { return nil, err }
	name := f.Name()
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil { err = closeErr }
	if err != nil { os.Remove(name); return nil, err }
	d, err := Open(name, pageSize)
	if err != nil { os.Remove(name); return nil, err }
	d.mu.Lock()
	d.path, d.sourceTemp = "", name
	d.mu.Unlock()
	return d, nil
}

// SetPages stages every replacement before changing the edit set. Errors leave
// all existing edits intact. Coordinates remain in the original page grid.
func (d *Document) SetPages(pages map[int64]string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	staged := make(map[int64]*edit)
	cleanup := func() { for _, e := range staged { if e != nil && e.file != "" { os.Remove(e.file) } } }
	for k, text := range pages {
		if k < 0 || k >= d.pageCount() { cleanup(); return fmt.Errorf("page out of range: %d", k) }
		start, end := d.pageRange(k)
		original, err := d.readAt(start, end-start)
		if err != nil { cleanup(); return err }
		if string(original) == text { staged[k] = nil; continue }
		e := &edit{start:start, end:end, n:int64(len(text))}
		// Disk staging also bounds the memory used by a whole-file operation.
		f, err := os.CreateTemp("", "nup-edit-*.tmp")
		if err != nil { cleanup(); return err }
		e.file = f.Name()
		staged[k] = e
		_, err = io.WriteString(f, text)
		ce := f.Close()
		if err == nil { err = ce }
		if err != nil { cleanup(); return err }
	}
	for k, e := range staged {
		if old := d.edits[k]; old != nil { d.dropEdit(k, old) }
		if e != nil { d.edits[k] = e }
	}
	return nil
}

// ChangedOnDisk reports whether the file was modified (or removed) by another
// program since it was opened, reloaded or saved by this document.
func (d *Document) ChangedOnDisk() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.path == "" || d.f == nil {
		return false
	}
	st, err := os.Stat(d.path)
	if err != nil {
		return true
	}
	return !st.ModTime().Equal(d.modTime) || st.Size() != d.size
}

// Reload discards all pending edits and reopens the file from disk.
func (d *Document) Reload() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.path == "" {
		return errors.New("document has no file on disk")
	}
	if d.f != nil {
		d.f.Close()
		d.f = nil
	}
	d.clearEdits()
	return d.openFile()
}

func (d *Document) Path() string { d.mu.Lock(); defer d.mu.Unlock(); return d.path }
func (d *Document) IsNew() bool   { return d.Path() == "" }
func (d *Document) Size() int64   { d.mu.Lock(); defer d.mu.Unlock(); return d.size }
func (d *Document) EditCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.edits)
}
func (d *Document) Modified() bool { return d.EditCount() > 0 }

// LineCount returns the number of lines on disk, or -1 while counting.
func (d *Document) LineCount() int64 { return atomic.LoadInt64(&d.lines) }

// LogPath returns the change log path for a file.
func LogPath(p string) string { return p + ".nup.log" }

// AppendLog appends lines to a change log.
func AppendLog(p string, lines []string) error {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.WriteString(f, strings.Join(lines, "\n")+"\n")
	return err
}

// ReadLogTail reads at most max bytes from the end of a log using seek+read.
func ReadLogTail(p string, max int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	off := st.Size() - max
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return "", err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
		b = append([]byte("... (older entries omitted)\n"), b...)
	}
	return string(b), nil
}
