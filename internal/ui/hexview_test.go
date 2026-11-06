package ui

import (
	"bytes"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestHexNibbleOverwrite(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	changes := 0
	h := newHexView(func() { changes++ }, nil)
	h.setData(0, []byte{0x12, 0x34})
	h.TypedRune('a')
	if !bytes.Equal(h.data, []byte{0xa2, 0x34}) || h.cur != 1 { t.Fatal(h.data, h.cur) }
	h.TypedRune('F')
	if !bytes.Equal(h.data, []byte{0xaf, 0x34}) || h.cur != 2 { t.Fatal(h.data, h.cur) }
	h.TypedRune('z')
	if changes != 2 || len(h.data) != 2 { t.Fatal("invalid input changed data") }
	h.setCursor(3)
	h.TypedRune('0')
	if h.data[1] != 0x30 || h.cur != 3 { t.Fatal("EOF must clamp without inserting") }
}

func TestHexClickDigitAndDoubleClick(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	h := newHexView(nil, nil)
	h.setData(0, make([]byte, 19))
	for _, j := range []int{0, 7, 8, 15} {
		h.tapAt(0, hexCol(j)+1)
		if h.cur != j*2+1 { t.Fatalf("byte %d: cursor=%d", j, h.cur) }
	}
	h.tapAt(1, hexCol(15))
	if h.cur != 36 { t.Fatal("partial row should clamp to final byte", h.cur) }
	h.view.Tapped(&fyne.PointEvent{Position: fyne.NewPos(0, 0)})
	before := h.cur
	h.view.Tapped(&fyne.PointEvent{Position: fyne.NewPos(0, 0)})
	if h.cur != before || len(h.data) != 19 { t.Fatal("repeated click must only position cursor") }
	if runs := h.rowRuns(0); len(runs) == 0 { t.Fatal("no row output") }
	h.setData(0, nil)
	h.TypedRune('f')
	h.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	if h.cur != 0 { t.Fatal("empty page cursor") }
}
