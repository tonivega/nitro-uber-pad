package script

import (
	"math"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func TestTimerValidation(t *testing.T) {
	for _, v := range []float64{0, -1, math.NaN(), math.Inf(1), math.MaxFloat64} {
		if _, err := secondsDuration(v); err == nil { t.Fatalf("accepted %v", v) }
	}
	if d, err := secondsDuration(0.25); err != nil || d != 250*time.Millisecond { t.Fatal(d, err) }
}

func TestTimerCallbacksCancelAndClose(t *testing.T) {
	// Successful timer paths do not need a UI host. The mutex serializes all Lua access.
	e := &Engine{L: lua.NewState(), timers: make(map[int]*scheduledTimer)}
	e.registerTimers()
	defer e.Close()
	if err := e.Run("timers", `
count = 0
local id
id = timer.every(0.001, function()
  count = count + 1
  timer.cancel(id)
end)
timer.after(0.001, "once = 42")
local canceled = timer.after(0.01, function() error("must not run") end)
assert(timer.cancel(canceled))
assert(not timer.cancel(canceled))
`); err != nil { t.Fatal(err) }
	deadline := time.Now().Add(2*time.Second)
	for {
		e.mu.Lock()
		done := len(e.timers) == 0
		count, once := e.L.GetGlobal("count"), e.L.GetGlobal("once")
		e.mu.Unlock()
		if done {
			if count != lua.LNumber(1) || once != lua.LNumber(42) { t.Fatal(count, once) }
			break
		}
		if time.Now().After(deadline) { t.Fatal("timers did not complete") }
		time.Sleep(time.Millisecond)
	}
	if err := e.Run("pending", `timer.after(60, function() end)`); err != nil { t.Fatal(err) }
	e.Close()
	if len(e.timers) != 0 { t.Fatal("timers survived close") }
	if err := e.Run("closed", "return"); err == nil { t.Fatal("closed engine accepted script") }
}
