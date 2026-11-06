package script

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
	"nitrobutton.com/nitro-uber-pad/internal/config"
)

// Optional host extensions preserve compatibility with small embedders.
type Tab struct {
	Index int
	Path, Title string
	Modified, Selected bool
}

type tabHost interface {
	TabInfo() []Tab
	SelectTab(path string, index int) error
}

type actionHost interface {
	ScriptActions() []config.Action
	SetScriptAction(config.Action) error
	RemoveScriptAction(string) error
}

func (e *Engine) log(category, detail string) {
	if h, ok := e.h.(interface{ Log(string, string) }); ok { h.Log(category, detail) }
}

func (e *Engine) registerWorkspace() {
	L := e.L
	ui := L.GetGlobal("ui").(*lua.LTable)
	L.SetFuncs(ui, map[string]lua.LGFunction{
		"tabs": func(L *lua.LState) int {
			h, ok := e.h.(tabHost)
			if !ok { return ret(L, fmt.Errorf("tab API unavailable")) }
			out := L.NewTable()
			for _, tab := range h.TabInfo() {
				t := L.NewTable()
				t.RawSetString("index", lua.LNumber(tab.Index))
				if tab.Path != "" { t.RawSetString("path", lua.LString(tab.Path)) }
				t.RawSetString("title", lua.LString(tab.Title))
				t.RawSetString("modified", lua.LBool(tab.Modified))
				t.RawSetString("selected", lua.LBool(tab.Selected))
				out.Append(t)
			}
			L.Push(out)
			return 1
		},
		"select_tab": func(L *lua.LState) int {
			h, ok := e.h.(tabHost)
			if !ok { return ret(L, fmt.Errorf("tab API unavailable")) }
			if L.Get(1).Type() == lua.LTNumber {
				return ret(L, h.SelectTab("", L.CheckInt(1)))
			}
			return ret(L, h.SelectTab(config.ExpandHome(L.CheckString(1)), 0))
		},
		"action": func(L *lua.LState) int {
			h, ok := e.h.(actionHost)
			if !ok { return ret(L, fmt.Errorf("action API unavailable")) }
			t := L.CheckTable(1)
			str := func(key string) string { return lua.LVAsString(t.RawGetString(key)) }
			return ret(L, h.SetScriptAction(config.Action{
				ID: str("id"), Label: str("label"), Menu: str("menu"),
				Shortcut: str("shortcut"), Toolbar: lua.LVAsBool(t.RawGetString("toolbar")), Script: str("script"),
			}))
		},
		"remove_action": func(L *lua.LState) int {
			h, ok := e.h.(actionHost)
			if !ok { return ret(L, fmt.Errorf("action API unavailable")) }
			return ret(L, h.RemoveScriptAction(L.CheckString(1)))
		},
		"actions": func(L *lua.LState) int {
			h, ok := e.h.(actionHost)
			if !ok { return ret(L, fmt.Errorf("action API unavailable")) }
			out := L.NewTable()
			for _, a := range h.ScriptActions() {
				t := L.NewTable()
				for k, v := range map[string]string{"id": a.ID, "label": a.Label, "menu": a.Menu, "shortcut": a.Shortcut, "script": a.Script} {
					t.RawSetString(k, lua.LString(v))
				}
				t.RawSetString("toolbar", lua.LBool(a.Toolbar))
				out.Append(t)
			}
			L.Push(out)
			return 1
		},
	})
}
