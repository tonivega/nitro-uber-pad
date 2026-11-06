package document

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestDownloadedBufferSaveAndCleanup(t *testing.T) {
 data:=[]byte(strings.Repeat("abc\n",3000)+"\x00\xff")
 d,err:=OpenBuffer(data,4096); if err!=nil { t.Fatal(err) }
 source:=d.sourceTemp
 if !d.IsNew() || d.Size()!=int64(len(data)) { t.Fatal("not an unsaved paged download") }
 target:=filepath.Join(t.TempDir(),"saved.bin")
 if _,err:=d.Save(target); err!=nil { t.Fatal(err) }
 got,err:=os.ReadFile(target); if err!=nil || string(got)!=string(data) { t.Fatal("download bytes changed",err) }
 d.Close()
 if _,err:=os.Stat(source); !os.IsNotExist(err) { t.Fatal("temporary source survived save") }
 d,err=OpenBuffer([]byte("discard"),4096); if err!=nil { t.Fatal(err) }
 source=d.sourceTemp; d.Close()
 if _,err:=os.Stat(source); !os.IsNotExist(err) { t.Fatal("temporary source survived close") }
}
func TestSetPagesAtomicValidation(t *testing.T) {
 d,err:=OpenBuffer([]byte("original"),4096); if err!=nil { t.Fatal(err) }; defer d.Close()
 if err:=d.SetPage(0,"pending"); err!=nil { t.Fatal(err) }
 if err:=d.SetPages(map[int64]string{0:"changed",99:"invalid"}); err==nil { t.Fatal("accepted invalid page") }
 got,err:=d.Page(0); if err!=nil || got!="pending" { t.Fatal(got,err) }
 if err:=d.SetPages(map[int64]string{0:"replacement\x00"}); err!=nil { t.Fatal(err) }
 got,err=d.Page(0); if err!=nil || got!="replacement\x00" { t.Fatal(got,err) }
}
