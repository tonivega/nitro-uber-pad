// Package script embeds a Lua interpreter and exposes editor functionality to it.
package script

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"

	"nitrobutton.com/nitro-uber-pad/internal/config"
)

// Host is implemented by the UI and operates on the active document.
type Host interface {
	Text() string
	SetText(string)
	Insert(string)
	Path() string
	Title() string
	IsNew() bool
	Modified() bool
	Size() int64
	Save(force bool) error
	SaveAs(path string) error
	SaveAsDialog()
	Page() (int64, int64)
	GotoPage(int64)
	Prompt(title, label, def string, cb func(string, bool))
	Confirm(title, msg string, cb func(bool))
	Alert(msg string)
	Notify(msg string)
	OpenFile(path string) error
	OpenDialog()
	NewTab()
	CloseTab()
	GetVar(key string) (string, bool)
	SetVar(key, value string)
	Vars() map[string]string
	Cursor() (int, int)
	SetCursor(row, col int)
	DiskChanged() bool
	Reload() error
	HTTPTimeout() time.Duration
}

// APIHelp documents the scripting API.
const APIHelp = `Nitro UberPad scripting (Lua 5.1)

SEARCH, STRINGS AND BINARY INSPECTION
Ctrl+F: Find; Ctrl+R: Replace; Ctrl+G: Go to cursor (Enter moves immediately).
Find/replace supports plain text or Go/RE2 regex, selection/page/whole-file
scope, cross-page matches, persistent templates and latest 100 usages.
Case-sensitive. Regex replacement: $1, ${name}, $$; plain replacements literal.
Snapshots/output: 64 MiB; 100,000 matches. Save applies pending edits to disk.
Selection is captured when opening the dialog; in long-line windows select
within one displayed line. Usage history persists patterns/replacements;
View > Search / Replace Usage can clear it. Do not put secrets in templates.

Lua's standard string library is available: string.byte/char/sub/find/match/
gmatch/gsub/format/lower/upper/len/rep/reverse (Lua patterns, NOT regex).
str.contains(s, part), str.trim(s), str.lower(s), str.upper(s)
str.split(s, separator) -> array (literal separator, preserves empty fields)
str.replace(s, old, new) -> string (literal, binary-safe)
str.regex_find(s, pattern) -> {start, finish, text}[] | nil, err
  Go/RE2, 0-based byte offsets, exclusive finish; max 100,000 matches.
str.regex_replace(s, pattern, replacement) -> string, count | nil, err
  Go/RE2 replacement syntax, not Lua patterns. 64 MiB output limit.

editor.open_url(url) -> true | nil, err; HTTP(S), max 64 MiB; unsaved tab.
  Also File > Open URL, editor.open(url), or a URL command-line argument.
  Download is data, never evaluated. Save As keeps a local copy; otherwise
  the temporary backing file is deleted on close. Requests are synchronous.
editor.view() -> "text" or "hex"
editor.set_view("hex"|"text") -> true | nil, err
  Text pages may use hex; non-text pages cannot use text. Long lines use windows.
editor.goto_offset(n) -> true | nil, err
  Absolute 0-based byte offset in the EDITED concatenated buffer, loads its page.
editor.page_info([page]) -> original_base, edited_length | nil, err
editor.read_bytes(page, offset, count) -> binary string | nil, err
  page is 1-based; offset is 0-based within that edited page. No cross-page read.
editor.apply_template("mp3") -> true, frame_count | nil, err
  Embedded offline Layer III inspector: follows calculated frame boundaries
  across pages, includes ID3, skips unsupported headers, checks truncation.
  Bounded to 400 frames / 8 MiB resynchronisation scanning; not an audio decoder.
editor.read_range(offset, count) -> binary string | nil, err
  Concatenated edited bytes across pages; 0-based offset; max 64 MiB per call.
editor.offset_info(offset) -> page, local_offset, original_offset | nil, err
  Maps an edited-buffer offset to a 1-based page and its original coordinates.
editor.mark_text(start, length, "#RRGGBB", label) -> true | nil, err
  Original-coordinate UTF-8 BYTE ranges, not rune indices. Text colors appear
  in the split annotation preview without replacing the editable text widget.
editor.mark_bits(start, length, "#RRGGBB", label [, metadata]) -> true | nil, err
  metadata = {meaning="interpretation", possible="value meanings / allowed values"}
  Both metadata strings are limited to 4096 bytes. The split panel shows
  colors, ranges, live raw values (up to 53 bits), meanings and possible values.
editor.marks() -> {start, length, color, label, meaning, possible, value}[]
  All registered marks, including other pages; value is a display string.
  Cross-page fields are identified explicitly; use read_range for arithmetic.
editor.clear_marks()
  Marks are tab/session-local, MSB-first, half-open bit ranges in ORIGINAL file
  offset coordinates (matching hex offsets), not concatenated edited offsets.
  Use page_info(page).base*8 + local_bit for any page. Up to 10,000 marks;
  later colors win overlaps. Hex shows colored nibbles, individual bits, range
  labels and row offsets. Marks do not change bytes or save into the file.
  After length-changing edits, clear/reapply the inspection template: original
  page coordinates do not account for inserted/deleted bytes. SetText and
  search replacements clear marks automatically. Kitchen Sink has opt-in MP3.

EXECUTION AND FILE VARIABLES
Tools > Run Selection / Document as Lua; toolbar Run Lua; Mod+Enter.
Selection wins; otherwise the complete edited document is executed (16 MiB limit).
Source is snapshotted before execution. Only run trusted code.
View > Task Runner shows scripts, processes, timers and pending Lua dialog callbacks.
Refresh for updates; cancel selected/all, inspect output/errors, clear finished.

task.run(code [, name [, timeout]]) -> task ID; queued background Lua source
  Lua shares one serialized interpreter, not parallel Lua states. No timeout by
  default; timeout seconds include queue time. 0/nil means unlimited.
task.list() -> {id, kind, name, state, error, output, exit_code, created}[]
task.cancel(task_id) -> boolean; independent of timer IDs
task.sleep(seconds) -> true | nil, err; cancellable, blocks other Lua executions
process.run{program=, args={...}, env={KEY="value"}, cwd=, stdin=,
            timeout=, background=false}
  Foreground -> result table (state, exit_code, output, error, id).
  Background -> task ID. Invalid options -> nil, err.
  args are literal strings, not a shell command. env overrides inherited values.
  timeout is seconds; omitted/nil/0 means none. Timeout and cancellation kill
  the direct child, NOT its entire descendant tree. Avoid shell-detached jobs:
  descendants started by external programs cannot be tracked by this runner.
  stdout+stderr are combined, drained, and capped at 1 MiB. Exit code -1 means
  start failure or termination without an exit code. Inspect state/error too.
  os.execute(command) is tracked but invokes a platform shell; prefer process.run.
fs.read(path) -> binary-safe string | nil, err
fs.write(path, value) -> true | nil, err; overwrites directly, no GUI/tab changes
  Paths expand ~. Reads materialize the whole file; writes do not create parents.
  Use nup.mkdir first. Do not overwrite an open modified document inadvertently.
Tools > Load Kitchen Sink Script loads an embedded, editable integration demo.
Tools > Load and Run Lua File on Current Document reads source without opening
another document. Tools > Lua Textbox for Current Document edits/runs source
against the captured tab. Both work in hex and text modes, queue cancellable
source snapshots, enforce 16 MiB, and fail if the target tab was closed before
execution. Only run trusted code; Lua has filesystem/network capabilities.

Cancellation is cooperative in Go host functions: Lua loops, HTTP, sleep and
processes are cancellable; disk operations finish before returning. Dialog
cancellation disables the Lua callback but does not dismiss the visible dialog.
Task history keeps the latest 200 completed records plus all live work. Tasks
are session-only and canceled on quit. Internal editor indexing/watchers are
owned by documents, stop on close, and are not user-executable script tasks.

editor.text()                 -> text of the current page
editor.set_text(s)            replace the current page text
editor.insert(s)              insert s at the cursor
editor.path()                 -> file path or nil when new
editor.title()                -> tab title
editor.is_new()               -> true if never saved
editor.is_modified()          -> true if there are unsaved changes
editor.size()                 -> size on disk in bytes
editor.save([force])          -> true | nil, err   (existing files only). If the file
                              was changed on disk by another program a confirmation
                              dialog is shown and err contains "changed on disk";
                              force=true overwrites without asking
editor.disk_changed()         -> true if the file was modified by another program
editor.reload()               -> true | nil, err  reload from disk (discards edits)
editor.save_as(path)          -> true | nil, err
editor.save_as_dialog()       show the Save As dialog
editor.page()                 -> current page (1-based), page count
editor.goto_page(n)           go to page n (1-based)
editor.cursor()               -> line, column (1-based, inside the current page;
                              in the hex view: 16-byte row, byte in the row)
editor.set_cursor(line [, col])  move the cursor (values are clamped)

ui.alert(msg)                 information dialog
ui.notify(msg)                message in the status bar (print() too)
ui.prompt(title, label, fn [, default])  fn(value) / fn(nil) on cancel
ui.confirm(title, msg, fn)    fn(true|false)
ui.open(path)                 -> true | nil, err
ui.open_dialog()  ui.new_tab()  ui.close_tab()
ui.tabs()                     -> ordered {index, path, title, modified, selected}[]
                              path is nil for unsaved tabs; index is 1-based
ui.select_tab(path_or_index)  -> true | nil, err; does not open missing tabs
editor.open(path)             alias for ui.open(path); no file picker
editor.path([tab_index])      optional index reads another tab's path
editor.save_as(path)          writes directly without a file picker (overwrites target)

ui.action{ id=, label=, menu=, shortcut=, toolbar=, script= }
                              -> true | nil, err; creates/replaces a runtime action
                              script is Lua source, not a function; IDs must not
                              collide with configured actions. Shortcuts must be unique.
ui.remove_action(id)          -> true | nil, err; runtime actions only
ui.actions()                 -> runtime action tables (Settings > Scripted UI)
                              Configured actions are retained. Runtime actions last
                              this session; register them again in your setup script.

timer.after(seconds, fn_or_code) -> timer ID (one-shot)
timer.every(seconds, fn_or_code) -> timer ID (repeats after each callback finishes)
timer.at(unix_seconds_or_RFC3339, fn_or_code) -> one-shot ID; past times run next
                              e.g. timer.at("2030-01-01T09:00:00Z", function() ... end)
timer.cancel(id)              -> boolean (also safe inside its callback)
                              Durations must be finite and > 0. Timers are session-only,
                              serialized with scripts, non-overlapping, canceled on quit.
                              Busy scripts/HTTP delay callbacks; not a real-time scheduler.
                              A repeating timer stops on callback error (see Activity Logs).

config.get(key)               -> value of a settings variable or nil
config.set(key, value)        set and persist a variable
config.all()                  -> table of all variables

nup.date([layout])            Go time layout, default "2006-01-02"
nup.home()  nup.expand(p)  nup.join(a, b, ...)  nup.mkdir(p)
nup.exists(p)  nup.basename(p)  nup.dirname(p)  nup.os  nup.version
nup.ulen(s)                   -> number of characters (UTF-8 runes) in s
nup.eval(code [, name])       -> true, results... | false, err
                              Compiles and runs Lua code held in a string or
                              variable, like eval() in PHP. This is useful for
                              additional scripting or integrations that are dynamic
                              from outside the app: macros stored in settings
                              variables, code downloaded from an HTTP server,
                              code generated by other tools...

http.get(url [, timeout [, headers]])
    -> body, status, err, headers
http.post(url, body [, content_type [, timeout [, headers]]])
    -> body, status, err, headers   (content_type default text/plain; charset=utf-8)
http.request{method=, url=, body=, headers={...}, timeout=}
    -> body, status, err, headers
  timeout is in seconds; when omitted (nil) the default selected in
  Settings > General > HTTP timeout is used. status is the HTTP code
  (200, 404, 500...). On network errors / timeouts: nil, 0, err.
  Response headers are returned with lower-case names.
  Requests are synchronous: the editor waits until they finish or time out.
http.timeout()                -> default timeout in seconds
http.urlencode(s)             -> s escaped for URL queries / form bodies

See the Snippets tab (Help > Scripting Help & Snippets) for advanced examples.
`

// Engine runs action scripts.
type Engine struct {
	mu sync.Mutex
	L  *lua.LState
	h  Host
	timers map[int]*scheduledTimer
	nextTimer int
	closed bool
	taskMu sync.Mutex
	tasks map[int]*executionTask
	nextTask int
	shuttingDown bool
}

// New creates an engine bound to host.
func New(h Host) *Engine {
	e := &Engine{L: lua.NewState(), h: h, timers: make(map[int]*scheduledTimer)}
	e.register()
	return e
}

// Run executes a script.
func (e *Engine) Run(name, src string) (err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed { return fmt.Errorf("script engine is closed") }
	id, ctx := e.newTask("script", name)
	defer func() { e.finishTask(id, err) }()
	e.L.SetContext(ctx)
	defer e.L.RemoveContext()
	e.setTaskState(id, "running")
	start := time.Now()
	e.log("script", fmt.Sprintf("Start %q (%d source bytes)", name, len(src)))
	defer func() {
		e.log("script", fmt.Sprintf("Finish %q in %s; error=%v", name, time.Since(start), err))
	}()
	fn, err := e.L.Load(strings.NewReader(src), name)
	if err != nil { return err }
	e.L.Push(fn)
	return e.L.PCall(0, 0, nil)
}

func (e *Engine) callback(id int, ctx context.Context, fn *lua.LFunction, args ...lua.LValue) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || ctx.Err() != nil { return }
	e.setTaskState(id, "running")
	e.L.SetContext(ctx)
	defer e.L.RemoveContext()
	start := time.Now()
	e.log("script", "Start dialog callback")
	err := e.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, args...)
	e.finishTask(id, err)
	e.log("script", fmt.Sprintf("Finish dialog callback in %s; error=%v", time.Since(start), err))
	if err != nil {
		e.h.Alert("Script error: " + err.Error())
	}
}

func ret(L *lua.LState, err error) int {
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

func pushStrOrNil(L *lua.LState, s string, ok bool) int {
	if !ok {
		L.Push(lua.LNil)
	} else {
		L.Push(lua.LString(s))
	}
	return 1
}

func (e *Engine) register() {
	L, h := e.L, e.h
	L.SetGlobal("editor", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"text":     func(L *lua.LState) int { L.Push(lua.LString(h.Text())); return 1 },
		"set_text": func(L *lua.LState) int { h.SetText(L.CheckString(1)); return 0 },
		"insert":   func(L *lua.LState) int { h.Insert(L.CheckString(1)); return 0 },
		"path": func(L *lua.LState) int {
			p := h.Path()
			if L.GetTop() > 0 && L.Get(1) != lua.LNil {
				tabs, ok := h.(tabHost)
				if !ok { return ret(L, fmt.Errorf("tab API unavailable")) }
				i := L.CheckInt(1)
				all := tabs.TabInfo()
				if i < 1 || i > len(all) { return ret(L, fmt.Errorf("tab index out of range")) }
				p = all[i-1].Path
			}
			return pushStrOrNil(L, p, p != "")
		},
		"open": func(L *lua.LState) int { return ret(L, h.OpenFile(config.ExpandHome(L.CheckString(1)))) },
		"title":          func(L *lua.LState) int { L.Push(lua.LString(h.Title())); return 1 },
		"is_new":         func(L *lua.LState) int { L.Push(lua.LBool(h.IsNew())); return 1 },
		"is_modified":    func(L *lua.LState) int { L.Push(lua.LBool(h.Modified())); return 1 },
		"size":           func(L *lua.LState) int { L.Push(lua.LNumber(h.Size())); return 1 },
		"save":           func(L *lua.LState) int { return ret(L, h.Save(L.OptBool(1, false))) },
		"disk_changed":   func(L *lua.LState) int { L.Push(lua.LBool(h.DiskChanged())); return 1 },
		"reload":         func(L *lua.LState) int { return ret(L, h.Reload()) },
		"cursor": func(L *lua.LState) int {
			r, c := h.Cursor()
			L.Push(lua.LNumber(r + 1))
			L.Push(lua.LNumber(c + 1))
			return 2
		},
		"set_cursor": func(L *lua.LState) int { h.SetCursor(L.CheckInt(1)-1, L.OptInt(2, 1)-1); return 0 },
		"save_as":        func(L *lua.LState) int { return ret(L, h.SaveAs(L.CheckString(1))) },
		"save_as_dialog": func(L *lua.LState) int { h.SaveAsDialog(); return 0 },
		"page": func(L *lua.LState) int {
			p, n := h.Page()
			L.Push(lua.LNumber(p + 1))
			L.Push(lua.LNumber(n))
			return 2
		},
		"goto_page": func(L *lua.LState) int { h.GotoPage(L.CheckInt64(1) - 1); return 0 },
	}))

	L.SetGlobal("ui", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"alert":  func(L *lua.LState) int { h.Alert(L.CheckString(1)); return 0 },
		"notify": func(L *lua.LState) int { h.Notify(L.CheckString(1)); return 0 },
		"prompt": func(L *lua.LState) int {
			title, label, fn := L.CheckString(1), L.OptString(2, ""), L.CheckFunction(3)
			id, ctx := e.newTask("dialog", title)
			e.setTaskState(id, "waiting")
			h.Prompt(title, label, L.OptString(4, ""), func(v string, ok bool) {
				if ok {
					e.callback(id, ctx, fn, lua.LString(v))
				} else {
					e.callback(id, ctx, fn, lua.LNil)
				}
			})
			return 0
		},
		"confirm": func(L *lua.LState) int {
			title, msg, fn := L.CheckString(1), L.CheckString(2), L.CheckFunction(3)
			id, ctx := e.newTask("dialog", title)
			e.setTaskState(id, "waiting")
			h.Confirm(title, msg, func(ok bool) { e.callback(id, ctx, fn, lua.LBool(ok)) })
			return 0
		},
		"open":        func(L *lua.LState) int { return ret(L, h.OpenFile(config.ExpandHome(L.CheckString(1)))) },
		"open_dialog": func(L *lua.LState) int { h.OpenDialog(); return 0 },
		"new_tab":     func(L *lua.LState) int { h.NewTab(); return 0 },
		"close_tab":   func(L *lua.LState) int { h.CloseTab(); return 0 },
	}))

	L.SetGlobal("config", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"get": func(L *lua.LState) int {
			v, ok := h.GetVar(L.CheckString(1))
			return pushStrOrNil(L, v, ok)
		},
		"set": func(L *lua.LState) int { h.SetVar(L.CheckString(1), L.CheckString(2)); return 0 },
		"all": func(L *lua.LState) int {
			t := L.NewTable()
			for k, v := range h.Vars() {
				t.RawSetString(k, lua.LString(v))
			}
			L.Push(t)
			return 1
		},
	}))

	nup := L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"date": func(L *lua.LState) int {
			L.Push(lua.LString(time.Now().Format(L.OptString(1, "2006-01-02"))))
			return 1
		},
		"home": func(L *lua.LState) int {
			home, _ := os.UserHomeDir()
			L.Push(lua.LString(home))
			return 1
		},
		"expand": func(L *lua.LState) int { L.Push(lua.LString(config.ExpandHome(L.CheckString(1)))); return 1 },
		"join": func(L *lua.LState) int {
			var parts []string
			for i := 1; i <= L.GetTop(); i++ {
				parts = append(parts, L.CheckString(i))
			}
			L.Push(lua.LString(filepath.Join(parts...)))
			return 1
		},
		"mkdir": func(L *lua.LState) int {
			return ret(L, os.MkdirAll(config.ExpandHome(L.CheckString(1)), 0755))
		},
		"exists": func(L *lua.LState) int {
			_, err := os.Stat(config.ExpandHome(L.CheckString(1)))
			L.Push(lua.LBool(err == nil))
			return 1
		},
		"basename": func(L *lua.LState) int { L.Push(lua.LString(filepath.Base(L.CheckString(1)))); return 1 },
		"dirname":  func(L *lua.LState) int { L.Push(lua.LString(filepath.Dir(L.CheckString(1)))); return 1 },
		"ulen":     func(L *lua.LState) int { L.Push(lua.LNumber(utf8.RuneCountInString(L.CheckString(1)))); return 1 },
		"eval": func(L *lua.LState) int {
			name, start := L.OptString(2, "eval"), time.Now()
			e.log("script", fmt.Sprintf("Start eval %q (%d source bytes)", name, len(L.CheckString(1))))
			defer func() { e.log("script", fmt.Sprintf("Finish eval %q in %s", name, time.Since(start))) }()
			fn, err := L.Load(strings.NewReader(L.CheckString(1)), name)
			if err != nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			top := L.GetTop()
			L.Push(fn)
			if err := L.PCall(0, lua.MultRet, nil); err != nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			n := L.GetTop() - top
			if n <= 0 {
				L.Push(lua.LTrue)
				return 1
			}
			L.Insert(lua.LTrue, top+1)
			return n + 1
		},
	})
	L.SetField(nup, "version", lua.LString(config.Version))
	L.SetField(nup, "os", lua.LString(runtime.GOOS))
	L.SetGlobal("nup", nup)
	e.registerHTTP()
	e.registerWorkspace()
	e.registerTimers()
	e.registerExecution()
	e.registerInspection()

	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		var parts []string
		for i := 1; i <= L.GetTop(); i++ {
			parts = append(parts, L.ToStringMeta(L.Get(i)).String())
		}
		h.Notify(strings.Join(parts, " "))
		return 0
	}))
}
