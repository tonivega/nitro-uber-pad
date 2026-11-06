package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEventStoreBoundedAndSnapshot(t *testing.T) {
	var s eventStore
	for i := 0; i < 5; i++ { s.add(3, "test", fmt.Sprint(i)) }
	got := s.snapshot()
	if len(got) != 3 || got[0].detail != "2" || got[2].detail != "4" { t.Fatal(got) }
	got[0].detail = "mutated"
	if s.snapshot()[0].detail != "2" { t.Fatal("snapshot aliases store") }
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); for j := 0; j < 100; j++ { s.add(3, "test", "concurrent"); _ = s.snapshot() } }()
	}
	wg.Wait()
	if len(s.snapshot()) != 3 { t.Fatal("unbounded store") }
}

func TestSameDocumentPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(p, []byte("hello"), 0600); err != nil { t.Fatal(err) }
	if !sameDocumentPath(p, filepath.Join(filepath.Dir(p), "unused", "..", "file.txt")) { t.Fatal("clean paths differ") }
	if sameDocumentPath("", "") || sameDocumentPath(p, p+"missing") { t.Fatal("unrelated documents matched") }
}
