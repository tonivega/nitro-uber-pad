package script

import (
 "context"
 "fmt"
 "os"
 "os/exec"
 "sort"
 "strings"
 "sync"
 "time"

 lua "github.com/yuin/gopher-lua"
 "nitrobutton.com/nitro-uber-pad/internal/config"
)

// Task has no Lua values and may safely be inspected while the interpreter is busy.
type Task struct {
 ID int
 Kind, Name, State, Error, Output string
 Created, Finished time.Time
 ExitCode int
}
type executionTask struct {
 Task
 ctx context.Context
 cancel context.CancelFunc
}

func terminal(state string) bool {
 return state == "completed" || state == "failed" || state == "canceled" || state == "timed out"
}

func (e *Engine) newTask(kind, name string) (int, context.Context) {
 return e.newTimedTask(kind, name, 0)
}
func (e *Engine) newTimedTask(kind, name string, timeout time.Duration) (int, context.Context) {
 e.taskMu.Lock()
 defer e.taskMu.Unlock()
 if e.tasks == nil { e.tasks = make(map[int]*executionTask) }
 // Keep at most 200 finished records; never evict live work.
 var finished []int
 for id, t := range e.tasks { if terminal(t.State) { finished = append(finished, id) } }
 sort.Ints(finished)
 for len(finished) >= 200 { delete(e.tasks, finished[0]); finished = finished[1:] }
 ctx, cancel := context.WithCancel(context.Background())
 if timeout > 0 { cancel(); ctx, cancel = context.WithTimeout(context.Background(), timeout) }
 e.nextTask++
 id := e.nextTask
 e.tasks[id] = &executionTask{Task: Task{ID:id, Kind:kind, Name:name, State:"queued", Created:time.Now(), ExitCode:-1}, ctx:ctx, cancel:cancel}
 if e.shuttingDown { cancel() }
 return id, ctx
}
func (e *Engine) setTaskState(id int, state string) {
 e.taskMu.Lock(); defer e.taskMu.Unlock()
 if t := e.tasks[id]; t != nil && !terminal(t.State) { t.State = state }
}
func (e *Engine) finishTask(id int, err error) {
 e.taskMu.Lock(); defer e.taskMu.Unlock()
 t := e.tasks[id]
 if t == nil { return }
 if !terminal(t.State) {
  t.State = "completed"
  if err != nil { t.State = "failed"; t.Error = err.Error() }
  if t.ctx.Err() == context.Canceled { t.State = "canceled" }
  if t.ctx.Err() == context.DeadlineExceeded { t.State = "timed out" }
  t.Finished = time.Now()
 }
 t.cancel()
}
func (e *Engine) Tasks() []Task {
 e.taskMu.Lock(); defer e.taskMu.Unlock()
 out := make([]Task, 0, len(e.tasks))
 for _, t := range e.tasks {
  snapshot := t.Task
  if !terminal(snapshot.State) && t.ctx.Err() != nil {
   snapshot.State = "canceling"
   if t.ctx.Err() == context.DeadlineExceeded { snapshot.State = "timeout pending" }
  }
  out = append(out, snapshot)
 }
 sort.Slice(out, func(i,j int) bool { return out[i].ID > out[j].ID })
 return out
}
// CancelTask never acquires the interpreter lock: it can interrupt busy Lua.
func (e *Engine) CancelTask(id int) bool {
 e.taskMu.Lock(); defer e.taskMu.Unlock()
 t := e.tasks[id]
 if t == nil || terminal(t.State) || t.ctx.Err() != nil { return false }
 t.cancel()
 if t.Kind == "timer" && t.State != "running" || t.Kind == "dialog" && t.State == "waiting" {
  t.State = "canceled"; t.Finished = time.Now()
 }
 return true
}
func (e *Engine) CancelAll() {
 for _, t := range e.Tasks() { e.CancelTask(t.ID) }
}
func (e *Engine) ClearFinishedTasks() {
 e.taskMu.Lock(); defer e.taskMu.Unlock()
 for id,t := range e.tasks { if terminal(t.State) { delete(e.tasks,id) } }
}

// Start queues a source snapshot, not a Lua function. All executions share the
// interpreter and are serialized; Go process tasks can run concurrently.
func (e *Engine) Start(name, src string, timeout time.Duration) int {
 return e.StartWithSetup(name,src,timeout,nil)
}

// StartWithSetup selects/validates a captured document after acquiring the Lua
// execution lock, not when the job is queued. Source is already snapshotted.
func (e *Engine) StartWithSetup(name, src string, timeout time.Duration, setup func() error) int {
 id, ctx := e.newTimedTask("lua", name, timeout)
 go func() {
  e.mu.Lock(); defer e.mu.Unlock()
  if e.closed || ctx.Err() != nil { e.finishTask(id, context.Canceled); return }
  e.setTaskState(id,"running")
  if setup != nil {
   if err := setup(); err != nil { e.finishTask(id,err); return }
  }
  e.L.SetContext(ctx); defer e.L.RemoveContext()
  fn, err := e.L.Load(strings.NewReader(src), name)
  if err == nil { e.L.Push(fn); err = e.L.PCall(0,0,nil) }
  e.finishTask(id,err)
  e.log("script",fmt.Sprintf("Background Lua #%d finished; error=%v",id,err))
 }()
 return id
}

// Drain output continuously without unbounded memory consumption or pipe
// deadlock. stdout and stderr share this synchronized, capped capture.
type outputCapture struct { mu sync.Mutex; data []byte; truncated bool }
func (b *outputCapture) Write(p []byte) (int,error) {
 b.mu.Lock(); defer b.mu.Unlock()
 n := len(p); keep := min(n, (1<<20)-len(b.data))
 b.data = append(b.data,p[:keep]...)
 if keep < n { b.truncated = true }
 return n,nil
}
func (b *outputCapture) String() string {
 b.mu.Lock(); defer b.mu.Unlock()
 s := string(b.data); if b.truncated { s += "\n[output truncated at 1 MiB]" }; return s
}

type programOptions struct {
 name, dir, stdin string
 args, env []string
 timeout time.Duration
 background bool
}
func (e *Engine) program(id int, ctx context.Context, o programOptions) Task {
 e.setTaskState(id,"running")
 cmd := exec.CommandContext(ctx,o.name,o.args...)
 cmd.Dir,cmd.Env,cmd.Stdin = o.dir,o.env,strings.NewReader(o.stdin)
 // Descendants inheriting pipes must not prevent task completion indefinitely.
 cmd.WaitDelay = time.Second
 var output outputCapture
 cmd.Stdout,cmd.Stderr = &output,&output
 err := cmd.Run()
 code := -1
 if cmd.ProcessState != nil { code = cmd.ProcessState.ExitCode() }
 e.taskMu.Lock()
 if t := e.tasks[id]; t != nil { t.Output = output.String(); t.ExitCode = code }
 e.taskMu.Unlock()
 e.finishTask(id,err)
 e.log("process",fmt.Sprintf("Process #%d finished; exit=%d",id,code))
 e.taskMu.Lock(); defer e.taskMu.Unlock()
 if t := e.tasks[id]; t != nil { return t.Task }
 return Task{ID:id, ExitCode:code}
}

func luaTask(L *lua.LState, t Task) *lua.LTable {
 out := L.NewTable()
 out.RawSetString("id",lua.LNumber(t.ID))
 for k,v := range map[string]string{"kind":t.Kind,"name":t.Name,"state":t.State,"error":t.Error,"output":t.Output,"created":t.Created.Format(time.RFC3339)} { out.RawSetString(k,lua.LString(v)) }
 out.RawSetString("exit_code",lua.LNumber(t.ExitCode))
 return out
}
func optionalTimeout(L *lua.LState, v lua.LValue) time.Duration {
 if v == lua.LNil || v == lua.LNumber(0) { return 0 }
 n,ok := v.(lua.LNumber)
 if !ok { L.RaiseError("timeout must be a positive number of seconds, 0 or nil"); return 0 }
 d,err := secondsDuration(float64(n)); if err != nil { L.RaiseError("%s",err); return 0 }; return d
}

func (e *Engine) registerExecution() {
 L := e.L
 L.SetGlobal("task",L.SetFuncs(L.NewTable(),map[string]lua.LGFunction{
  "run":func(L *lua.LState) int {
   src := L.CheckString(1); name := L.OptString(2,"background script")
   id := e.Start(name,src,optionalTimeout(L,L.Get(3)))
   L.Push(lua.LNumber(id)); return 1
  },
  "list":func(L *lua.LState) int { out := L.NewTable(); for _,t := range e.Tasks() { out.Append(luaTask(L,t)) }; L.Push(out); return 1 },
  "cancel":func(L *lua.LState) int { L.Push(lua.LBool(e.CancelTask(L.CheckInt(1)))); return 1 },
  "sleep":func(L *lua.LState) int {
   d,err := secondsDuration(float64(L.CheckNumber(1))); if err != nil { return ret(L,err) }
   ctx := L.Context(); if ctx == nil { ctx = context.Background() }
   timer := time.NewTimer(d); defer timer.Stop()
   select { case <-timer.C: return ret(L,nil); case <-ctx.Done(): return ret(L,ctx.Err()) }
  },
 }))
 L.SetGlobal("process",L.SetFuncs(L.NewTable(),map[string]lua.LGFunction{
  "run":func(L *lua.LState) int {
   t := L.CheckTable(1)
   name,ok := t.RawGetString("program").(lua.LString)
   if !ok || name == "" { return ret(L,fmt.Errorf("program is required")) }
   o := programOptions{name:string(name),env:os.Environ(),timeout:optionalTimeout(L,t.RawGetString("timeout")),background:lua.LVAsBool(t.RawGetString("background"))}
   if v := t.RawGetString("cwd"); v != lua.LNil { o.dir = config.ExpandHome(v.String()) }
   if v := t.RawGetString("stdin"); v != lua.LNil { o.stdin = v.String() }
   if v := t.RawGetString("args"); v != lua.LNil {
    a,ok := v.(*lua.LTable); if !ok { return ret(L,fmt.Errorf("args must be an array")) }
    for i:=1;i<=a.Len();i++ { s,ok := a.RawGetInt(i).(lua.LString); if !ok { return ret(L,fmt.Errorf("args must contain strings")) }; o.args = append(o.args,string(s)) }
   }
   if v := t.RawGetString("env"); v != lua.LNil {
    env,ok := v.(*lua.LTable); if !ok { return ret(L,fmt.Errorf("env must be a table")) }
    valid := true
    env.ForEach(func(k,v lua.LValue) {
     key,ks := k.(lua.LString); value,vs := v.(lua.LString)
     if !ks || !vs || key == "" || strings.ContainsAny(string(key),"=\x00") || strings.ContainsRune(string(value),0) { valid = false; return }
     o.env = append(o.env,string(key)+"="+string(value))
    })
    if !valid { return ret(L,fmt.Errorf("env requires valid string keys and values")) }
   }
   id,ctx := e.newTimedTask("process",o.name,o.timeout)
   if o.background {
    go e.program(id,ctx,o)
    L.Push(lua.LNumber(id)); return 1
   }
   // A foreground program is also canceled when its invoking script stops.
   parent := L.Context()
   var stop func() bool
   if parent != nil { stop = context.AfterFunc(parent,func(){ e.CancelTask(id) }); defer stop() }
   result := e.program(id,ctx,o)
   L.Push(luaTask(L,result)); return 1
  },
 }))
 fs := L.SetFuncs(L.NewTable(),map[string]lua.LGFunction{
  "read":func(L *lua.LState) int {
   data,err := os.ReadFile(config.ExpandHome(L.CheckString(1)))
   if err != nil { return ret(L,err) }; L.Push(lua.LString(data)); return 1
  },
  "write":func(L *lua.LState) int {
   path,data := config.ExpandHome(L.CheckString(1)),L.CheckString(2)
   return ret(L,os.WriteFile(path,[]byte(data),0600))
  },
 })
 L.SetGlobal("fs",fs)
 // Route Lua's legacy shell entry point through the observable process API.
 // Explicit process.run is preferred: it does not parse a shell command.
 if osTable,ok := L.GetGlobal("os").(*lua.LTable); ok {
  osTable.RawSetString("execute",L.NewFunction(func(L *lua.LState) int {
   command := L.CheckString(1)
   o := programOptions{name:"/bin/sh",args:[]string{"-c",command},env:os.Environ()}
   if os.PathSeparator == '\\' { o.name = "cmd.exe"; o.args = []string{"/C",command} }
   id,ctx := e.newTask("process","os.execute")
   if parent := L.Context(); parent != nil { stop := context.AfterFunc(parent,func(){ e.CancelTask(id) }); defer stop() }
   result := e.program(id,ctx,o)
   L.Push(lua.LNumber(result.ExitCode)); return 1
  }))
 }
}
