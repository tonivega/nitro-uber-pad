package script

import (
 "os"
 "path/filepath"
 "testing"
 "time"

 lua "github.com/yuin/gopher-lua"
)

func executionEngine() *Engine {
 e := &Engine{L:lua.NewState(),timers:make(map[int]*scheduledTimer)}
 e.registerExecution()
 e.registerTimers()
 return e
}
func waitTask(t *testing.T,e *Engine,id int) Task {
 t.Helper()
 deadline := time.Now().Add(3*time.Second)
 for time.Now().Before(deadline) {
  for _,task := range e.Tasks() { if task.ID==id && terminal(task.State) { return task } }
  time.Sleep(time.Millisecond)
 }
 t.Fatal("task did not finish"); return Task{}
}
func TestBackgroundTimeoutAndCancel(t *testing.T) {
 e := executionEngine(); defer e.Close()
 id := e.Start("timeout","while true do end",20*time.Millisecond)
 if task := waitTask(t,e,id); task.State!="timed out" { t.Fatal(task) }
 id = e.Start("cancel","while true do end",0)
 if !e.CancelTask(id) { t.Fatal("cancel rejected") }
 if task := waitTask(t,e,id); task.State!="canceled" { t.Fatal(task) }
 id = e.Start("success","answer = 42",0)
 if task := waitTask(t,e,id); task.State!="completed" { t.Fatal(task) }
 e.mu.Lock(); answer := e.L.GetGlobal("answer"); e.mu.Unlock()
 if answer != lua.LNumber(42) { t.Fatal(answer) }
}
func TestVariableFiles(t *testing.T) {
 e := executionEngine(); defer e.Close()
 path := filepath.Join(t.TempDir(),"bytes.bin")
 e.L.SetGlobal("path",lua.LString(path))
 if err := e.Run("files",`local value = string.char(0,255).."hello"; assert(fs.write(path,value)); assert(fs.read(path)==value); local v,err=fs.read(path.."missing"); assert(v==nil and err)`); err!=nil { t.Fatal(err) }
 data,err := os.ReadFile(path); if err!=nil || len(data)!=7 { t.Fatal(data,err) }
}
func TestProgramArgumentsEnvironmentAndExit(t *testing.T) {
 e := executionEngine(); defer e.Close()
 executable,err := os.Executable(); if err!=nil { t.Fatal(err) }
 e.L.SetGlobal("executable",lua.LString(executable))
 if err := e.Run("process",`local r=assert(process.run{program=executable,args={"-test.run=TestProgramHelper","--","one argument with spaces"},env={NUP_EXEC_HELPER="1"},timeout=2}); assert(r.exit_code==0,r.error); assert(r.output:find("one argument with spaces",1,true),r.output); local bad=process.run{program="nup-no-such-program-12345"}; assert(bad.state=="failed" and bad.exit_code==-1)`); err!=nil { t.Fatal(err) }
}
func TestProgramHelper(t *testing.T) {
 if os.Getenv("NUP_EXEC_HELPER")!="1" { return }
 args := os.Args
 os.Stdout.WriteString(args[len(args)-1])
 os.Exit(0)
}
func TestTimerPanelCancellation(t *testing.T) {
 e := executionEngine(); defer e.Close()
 if err := e.Run("timer",`timer.every(60,"error('should not run')")`); err!=nil { t.Fatal(err) }
 for _,task := range e.Tasks() {
  if task.Kind=="timer" {
   if !e.CancelTask(task.ID) { t.Fatal("cannot cancel timer") }
   if state := waitTask(t,e,task.ID).State; state!="canceled" { t.Fatal(state) }
  }
 }
}
