package script

import (
	"context"
	"fmt"
	"math"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type scheduledTimer struct {
	timer *time.Timer
	fn *lua.LFunction
	interval time.Duration
	taskID int
	ctx context.Context
	stopCancel func() bool
}

// Engine.mu protects both the Lua state and timer registry. A repeating timer
// is rearmed only after its callback returns, so it never builds a callback backlog.
func (e *Engine) schedule(delay, interval time.Duration, fn *lua.LFunction) int {
	e.nextTimer++
	id := e.nextTimer
	taskID, ctx := e.newTask("timer", fmt.Sprintf("Timer #%d (repeat %s)", id, interval))
	t := &scheduledTimer{fn: fn, interval: interval, taskID: taskID, ctx: ctx}
	e.setTaskState(taskID, "scheduled")
	e.timers[id] = t
	t.timer = time.AfterFunc(delay, func() { e.fireTimer(id, t) })
	// Wake canceled timers to release their Lua closures under the interpreter lock.
	t.stopCancel = context.AfterFunc(ctx, func() { e.fireTimer(id, t) })
	e.log("timer", fmt.Sprintf("Created #%d: due=%s repeat=%s", id, time.Now().Add(delay).Format(time.RFC3339Nano), interval))
	return id
}

func (e *Engine) fireTimer(id int, t *scheduledTimer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.timers[id] != t { return }
	if t.ctx.Err() != nil {
		t.timer.Stop()
		delete(e.timers, id)
		e.finishTask(t.taskID, t.ctx.Err())
		return
	}
	e.setTaskState(t.taskID, "running")
	e.L.SetContext(t.ctx)
	defer e.L.RemoveContext()
	start := time.Now()
	e.log("script", fmt.Sprintf("Start timer #%d", id))
	err := e.L.CallByParam(lua.P{Fn: t.fn, NRet: 0, Protect: true})
	e.log("script", fmt.Sprintf("Finish timer #%d in %s; error=%v", id, time.Since(start), err))
	if err != nil {
		delete(e.timers, id)
		e.finishTask(t.taskID, err)
		if e.h != nil { e.h.Notify(fmt.Sprintf("Timer #%d stopped: %v", id, err)) }
		return
	}
	if e.timers[id] != t { return } // callback canceled itself
	if t.interval == 0 {
		delete(e.timers, id)
		e.finishTask(t.taskID, nil)
		return
	}
	e.setTaskState(t.taskID, "scheduled")
	t.timer = time.AfterFunc(t.interval, func() { e.fireTimer(id, t) })
}

// Close cancels timers and releases Lua. Waiting callbacks check closed before
// touching Lua; no callback survives shutdown. Close is idempotent.
func (e *Engine) Close() {
	e.taskMu.Lock()
	e.shuttingDown = true
	for _, t := range e.tasks { t.cancel() }
	e.taskMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed { return }
	e.closed = true
	for id, t := range e.timers {
		t.timer.Stop()
		if t.stopCancel != nil { t.stopCancel() }
		e.finishTask(t.taskID, context.Canceled)
		delete(e.timers, id)
	}
	e.log("script", "Engine closed; all timers canceled")
	e.L.Close()
}

func secondsDuration(n float64) (time.Duration, error) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n >= float64(math.MaxInt64)/float64(time.Second) {
		return 0, fmt.Errorf("seconds must be finite, positive and within the duration range")
	}
	d := time.Duration(n * float64(time.Second))
	if d < time.Millisecond { d = time.Millisecond }
	return d, nil
}

func (e *Engine) registerTimers() {
	L := e.L
	callback := func(L *lua.LState) *lua.LFunction {
		if fn, ok := L.Get(2).(*lua.LFunction); ok { return fn }
		fn, err := L.LoadString(L.CheckString(2))
		if err != nil { L.ArgError(2, err.Error()); return nil }
		return fn
	}
	relative := func(repeat bool) lua.LGFunction {
		return func(L *lua.LState) int {
			d, err := secondsDuration(float64(L.CheckNumber(1)))
			if err != nil { L.ArgError(1, err.Error()); return 0 }
			interval := time.Duration(0)
			if repeat { interval = d }
			L.Push(lua.LNumber(e.schedule(d, interval, callback(L))))
			return 1
		}
	}
	L.SetGlobal("timer", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"after": relative(false),
		"every": relative(true),
		"at": func(L *lua.LState) int {
			var when time.Time
			if n, ok := L.Get(1).(lua.LNumber); ok {
				v := float64(n)
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 253402300799 {
					L.ArgError(1, "invalid Unix timestamp"); return 0
				}
				whole, frac := math.Modf(v)
				when = time.Unix(int64(whole), int64(frac*1e9))
			} else {
				var err error
				when, err = time.Parse(time.RFC3339Nano, L.CheckString(1))
				if err != nil { L.ArgError(1, err.Error()); return 0 }
			}
			d := time.Until(when)
			if d < 0 { d = 0 }
			L.Push(lua.LNumber(e.schedule(d, 0, callback(L))))
			return 1
		},
		"cancel": func(L *lua.LState) int {
			id := L.CheckInt(1)
			t, ok := e.timers[id]
			if ok {
				t.timer.Stop()
				e.CancelTask(t.taskID)
				e.finishTask(t.taskID, context.Canceled)
				delete(e.timers, id)
				e.log("timer", fmt.Sprintf("Canceled #%d", id))
			}
			L.Push(lua.LBool(ok))
			return 1
		},
	}))
}
