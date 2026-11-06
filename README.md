# Nitro UberPad

### A text editor that thinks in pages, speaks Lua, and lets you touch the bytes.

**Go · Fyne · Lua 5.1 · Windows / macOS / Linux**

Most editors ask you to adapt your workflow to their UI. Nitro UberPad explores the opposite:
**what if your workflow could create the UI—and your file didn't have to fit in one giant text buffer?**

Open a large log. Inspect an unfamiliar emoji. Patch half a byte in a binary page.
Turn a Lua script into a menu, a toolbar button and a shortcut. Schedule it for later.
Then look at the activity journal to see what actually happened.

## Not another ordinary text buffer

| Design choice | Why it matters |
| --- | --- |
| **Seek/read paging with read-ahead and read-behind** | Navigation loads pages rather than eagerly placing the entire file in a GUI text buffer. |
| **Deferred page replacements, with spill-to-disk edits** | Changes are retained separately and streamed into the output on save; large pending replacements need not all stay in memory. |
| **Horizontal windows for unusually long lines** | A single enormous line gets a byte window instead of forcing the widget to render it all at once. |
| **Automatic text / hex switching per page** | Non-representable bytes are not silently converted to replacement characters. Click either hex digit and overwrite that nibble directly—no row-edit dialog. |
| **A character inspector, not just a character counter** | See Unicode names, code points, UTF-8 bytes, escapes and common emoji/combining sequences. Invisible characters become easier to investigate. |
| **Lua-materialized controls** | A running script can add, update or remove its own menus, buttons and shortcuts without replacing configured controls. |
| **Path-addressable document tabs** | Automation can find the intended file instead of accidentally editing whichever tab happens to be active. |
| **An observable scripting workspace** | Script durations, callback errors, HTTP connections and file lifecycle events have a dedicated activity journal. |

These are engineering choices, not benchmark claims. Paging avoids a whole-file text buffer;
line indexing still scans the file in the background, saves stream the output, and some page-edit
operations materialize a complete page. Available disk space and platform limits still apply.

## A toolbox that builds itself

Create an action in **Settings → Actions & Shortcuts**, paste this Lua, and run it:

```lua
local ok, err = ui.action{
  id = "my_timestamp",
  label = "Timestamp",
  menu = "My toolbox",
  toolbar = true,
  shortcut = "Mod+Alt+T", -- Ctrl on Windows/Linux; Cmd on macOS
  script = 'editor.insert(nup.date("2006-01-02 15:04:05"))',
}
if not ok then ui.alert(err) end

timer.after(3, function()
  ui.notify("Your toolbox is ready. Click this notification for its history.")
end)
```

One registration, three entry points. Register the same ID again to change it;
`ui.remove_action("my_timestamp")` removes it. **Settings → Scripted UI** shows the
materialized runtime controls separately from the persisted actions. Existing controls stay
in place, and conflicting runtime IDs or shortcuts are rejected. Runtime controls and timers
last for the current session; keep their setup code in a configured action to recreate them.

### File-aware automation, without file pickers

```lua
local path = nup.join(nup.home(), "notes.txt")
local ok, err = ui.select_tab(path)
if not ok then ok, err = editor.open(path) end
if not ok then ui.alert(err) return end

editor.insert("\nReviewed " .. nup.date("2006-01-02"))
local saved, why = editor.save()
if not saved then ui.notify(tostring(why)) end

for _, tab in ipairs(ui.tabs()) do
  print(tab.index, tab.path or "(unsaved)", tab.modified)
end
```

`ui.select_tab` accepts a path or a 1-based index. `editor.path(index)` retrieves another
tab's path; `editor.path()` retrieves the active one. `editor.save_as(path)` writes directly
and changes the document's path, including for a new document. It can overwrite the target:
check `nup.exists(path)` and ask with `ui.confirm` when appropriate.

Most text APIs operate on the **current page**, not the entire file. A scheduled callback
should capture and select its intended file path before making edits: the active tab can change.

### From macros to small integrations

- `http.get`, `http.post`, `http.request`: response body, status, error and headers; configurable timeout.
- `nup.eval(code)`: evaluate generated code, settings-held macros or trusted remote toolboxes.
- `timer.after(seconds, callback)`: one-shot work.
- `timer.every(seconds, callback)`: fixed-delay repetition; cancel with `timer.cancel(id)`.
- `timer.at("2030-01-01T09:00:00Z", callback)`: wall-clock scheduling; Unix timestamps also work.
- `config.get` / `config.set`: persistent variables for your tools.

Callbacks can be Lua functions or source strings. Execution is serialized; synchronous HTTP
and busy scripts delay timers. Repeating callbacks never overlap or accumulate a backlog and
stop on error. All timers are canceled when the application quits.

**Help → Scripting Help & Snippets** includes the API reference and copyable examples.

> **Trust boundary:** Lua is automation, not a sandbox. Scripts can use filesystem and network
> facilities. Downloaded code runs with those capabilities too. Only execute code you trust;
> prefer HTTPS. HTTP calls are synchronous and can pause the editor until completion or timeout.

## Run documents, programs and background tasks

Use **Run Lua** on the toolbar, **Tools → Run Selection / Document as Lua**, or
**Mod+Enter** (Ctrl/Cmd+Enter, unless already assigned in your settings). Selected
text takes priority; without a selection the **complete edited document**, not
just its visible page, is snapshotted and executed. Execution is limited to 16 MiB.
These runs are queued so a busy Lua loop can be canceled from **View → Task Runner**.
Configured actions remain synchronous; use `task.run` for long-running Lua work.

```lua
local result, err = process.run{
  program = "python3",
  args = {"-c", "import os; print(os.environ['GREETING'])"},
  env = {GREETING = "hello from Lua"},
  timeout = 5,
}
assert(result, err)
print(result.state, result.exit_code, result.output)

local id = process.run{
  program = "python3", args = {"worker.py", "one argument with spaces"},
  background = true, timeout = 0, -- nil/omitted/0: no timeout
}
local lua_id = task.run('assert(task.sleep(1)); print("later")', "example", 10)
-- task.cancel(id); task.cancel(lua_id)
```

Programs receive literal argument arrays and inherited environment variables with
supplied overrides; no shell is implicitly used. Foreground calls return a result
table; background calls return a task ID. Combined stdout/stderr is drained and
capped at 1 MiB per task. Inspect `state`, `error` and `exit_code` for failures.
Cancellation/timeout terminates the **direct child only**, not an entire process
tree. Do not shell-detach children if you need the runner to manage their lifetime.
Lua's `os.execute` is routed through the tracked process runner but still uses a shell.

The **Task Runner** provides refresh, cancel selected/all, output/error inspection,
copy details, and clear finished. It tracks foreground/background scripts and
processes, scheduled/repeating timers, and pending Lua dialog callbacks. Task IDs
are distinct from timer IDs. History retains 200 finished records plus live tasks.
Lua executes serially in a shared interpreter: background means queued, not parallel
Lua. Process jobs can run concurrently. Timeout includes queue time. Canceling a
pending dialog disables its callback, without dismissing the dialog. Lua loops,
HTTP, sleeps and child processes support cancellation; filesystem calls must finish
before returning. Internal document indexing/watchers remain document-owned and
stop on document close; they are not executable script tasks.

Read/write variables without opening tabs or file pickers:

```lua
local bytes, err = fs.read(nup.join(nup.home(), "input.bin"))
assert(bytes, err)
assert(fs.write(nup.join(nup.home(), "copy.bin"), bytes))
```

These APIs preserve binary strings and expand `~`. Reads materialize the complete
file; writes directly overwrite the target, without updating open tabs. Create
parents with `nup.mkdir` and avoid overwriting an open modified file accidentally.

**Tools → Load Kitchen Sink Script** loads an embedded editable demo without running
it. It tests binary file roundtrips, environment/arguments, foreground/background
processes, timeout/cancel, queued Lua, timers (including an intentional failure),
scripted controls, document tabs/saves, and dialog callbacks. It writes a uniquely
named demo directory under your home. Network testing is opt-in via the
`kitchen_http_url` settings variable. Inspect Task Runner and Activity Logs afterward.

## See what happened—not just the last toast

- **Briefly blinking, clickable status notifications:** open the full message and browse earlier
  notifications instead of losing an error behind the next message.
- **View → Notification History:** the latest 200 session notifications, newest first.
- **View → Activity Logs:** the latest 2,000 session events, with filtering, refresh, expandable
  details and copying. Includes script execution/callback timing, HTTP connection attempts,
  response status and byte counts, file opens/saves/closes/reloads, and detected disk changes.
- **External-change protection:** a banner and `!` tab marker warn when an open file changes on
  disk; saving asks before overwriting. Disk monitoring polls every two seconds, so it is not
  an exhaustive filesystem audit. Reload explicitly discards pending edits.
- **Per-file change history:** saved page replacements are recorded in `<file>.nup.log`, separate
  from the session activity journal.

Activity logs do not intentionally record script source, HTTP headers or bodies; request URLs
omit credentials and query strings. Paths, URL paths, script errors and explicit notifications
can still contain sensitive information—review before sharing. Session journals are bounded
and are not persisted across restarts.

## Find, replace and inspect

- **Ctrl+F** opens Find; **Ctrl+R** opens Replace. Choose literal text or
  **Go/RE2 regular expressions**, and current page, whole file, or the text
  selection captured when the dialog opens. Search is case-sensitive; regex
  replacements support `$1`, `${name}` and `$$`. Literal replacements do not
  expand dollar signs. Find next wraps; Replace next and confirmed Replace all
  create pending edits, not immediate disk writes. Whole-file matches can span
  pages and include unsaved edits.
- Save named search/replacement templates in the dialog. **View → Search /
  Replace Usage** shows and clears the latest 100 persisted usages. Templates
  and history contain the actual patterns/replacements: avoid storing secrets.
  Search snapshots and replacement output are limited to 64 MiB and 100,000
  matches per operation; larger files can still be searched page by page.
  A selection across hidden portions of a long-line window is intentionally
  rejected; select within one displayed line instead.
- **Ctrl+G**, the **Go to cursor** toolbar button, and the per-page Cursor
  control navigate to `page[:line[:column]]` in text mode (all 1-based, Unicode
  character columns within the page), or an absolute byte `offset` in hex mode
  (0-based, decimal or `0x` hexadecimal). Offsets refer to the concatenated
  edited buffer and load the appropriate page. Hover the control for help;
  Enter in the modal moves immediately.
- **View → Toggle text / hex** permits inspecting text bytes as hex. Returning
  to text requires valid representable UTF-8; binary bytes are never coerced
  into text. The hex preference follows page navigation; invalid pages always
  remain hex even when the text preference is selected.
- **File → Open URL** (also `editor.open(url)` and command-line URLs) downloads
  HTTP(S) data into an unsaved disk-backed tab, without executing it. Downloads
  are bounded to 64 MiB and use the configured HTTP timeout. Save As creates a
  permanent local file; otherwise closing removes the temporary download.

Lua includes its standard binary-safe `string` library and an additional `str`
module for literal split/replace, trimming, case conversion and Go regex search
and replacement. **Help → Scripting Help & Snippets** documents the signatures.

**Tools → Load and Run Lua File on Current Document…** and **Lua Textbox for
Current Document…** run external or pasted trusted code against the captured tab,
in either text or hex mode. They queue cancellable source snapshots (16 MiB maximum)
and reject a target closed before execution. They do not replace the document with
the script source.

`editor.apply_template("mp3")` applies the embedded offline MP3 inspector to the
current document. The Kitchen Sink registers a menu item and toolbar button for
this, independently of its optional download demo. It calculates Layer III frame
lengths and follows boundaries across editor pages, inspecting up to 400 frames
with at most 8 MiB of resynchronisation scanning. Free-format headers are unsupported;
this is not an audio decoder.

Annotations open a split panel with matching colors, bit ranges, live raw values,
interpretations and possible values. `editor.mark_bits` accepts an optional fifth
argument `{meaning="...", possible="..."}`; `editor.marks()` exposes the registered
fields and metadata to Lua. Raw numerical display is limited to 53 bits. Fields
outside the current page are not misrepresented as complete numerical values.
`editor.read_range(offset, count)` reads concatenated edited bytes across pages;
`editor.offset_info(offset)` maps an edited offset to page/local/original coordinates.
`editor.mark_text(start, length, color, label)` colors UTF-8 **byte** ranges in a
separate text preview, keeping the editable text intact. Reapply annotations after
content changes; interpreted template labels are calculated when the template runs.

Binary templates can use `editor.page_info(page)`,
`editor.read_bytes(page, offset, count)`, `editor.set_view("hex")`,
`editor.mark_bits(start, length, "#RRGGBB", label)` and
`editor.clear_marks()`. Marks are session/tab-local, MSB-first bit ranges in
original file-offset coordinates (matching hex row offsets). Hex displays
colored offsets, nibbles, individual bits and range labels; later marks win
color overlaps. Marks do not modify file bytes. Reapply a template after
length-changing edits; search replacements and `editor.set_text` clear marks.

The Kitchen Sink includes an **opt-in MP3 visual inspector**. Set
`kitchen_mp3_url` to
`https://www.soundhelix.com/examples/mp3/SoundHelix-Song-1.mp3`, load the Kitchen
Sink and run it. It downloads that sample as data, identifies ID3 fields and
up to 100 MPEG Layer III frame headers, and labels/colors fields down to single
bits across page boundaries. It is an inspector, not an audio decoder; reserved
and free-format headers are skipped. Network access is never required to run
the rest of the demo. Only download material you are authorized to use.

## Under the hood

A compact codebase with distinct responsibilities:

- [`internal/document`](internal/document): cached seek/read access, deterministic page ranges,
  background line indexing, memory-budgeted edit storage and streaming saves.
- [`internal/script`](internal/script): embedded Lua, host API, HTTP integration and cancellable timers.
- [`internal/ui`](internal/ui): document tabs, virtualized hex rows, long-line windows, Unicode
  inspection, configurable controls and activity views.
- [`internal/config`](internal/config): JSON configuration and default scripted actions.

For engineers exploring the project, the interesting part is where these meet: keeping original
byte coordinates while edits change length, translating clicks into nibble positions, and making
a live scripting environment inspectable rather than opaque.

Configuration lives in `~/.nitro.uberpad.rc`. Recent files retain the latest 100 entries.
The default **Save** action writes existing files in place; new files ask for a prefix and become
`<prefix>-YYYYMMDD.txt` under the `save_dir` setting. Prefer your own convention? Edit the Lua.

## Try it

Build locally below, or use the release assets when available. A useful first tour:

1. Open a text file and inspect a Unicode character at the cursor.
2. Open a binary file, click a hex digit and type `0–9` or `A–F` to overwrite one nibble.
3. Run the toolbox example, then inspect **Settings → Scripted UI**.
4. Open **View → Activity Logs** and inspect the recorded execution.

### Tests

```sh
go test ./internal/script ./internal/document ./internal/config
go test ./internal/ui
```

The UI tests use Fyne's test application; building the UI package still requires the platform
build dependencies described below.

## Build

Fyne uses CGO, OpenGL and (on Linux) X11, so a C compiler and the system development
headers are required. If `go run .` fails with `Package gl was not found` or
`X11/Xlib.h: No such file or directory`, install them first:

```
./scripts/install-deps.sh      # apt / dnf / pacman / zypper / apk / macOS
```

(Debian/Ubuntu equivalent: `sudo apt-get install gcc pkg-config libgl1-mesa-dev xorg-dev`.)
On Windows install a MinGW-w64 gcc (e.g. via MSYS2 or TDM-GCC) and add it to `PATH`.

Then:

```
go mod tidy
go run .
go build -o nitro-uber-pad .
```

### Release builds into ./output/

Release builds are made with Docker, so only Docker is needed on the host:

```
./scripts/build-docker.sh            # linux amd64 + arm64, windows x64 (Docker); + macOS when run on a Mac
./scripts/build-docker.sh --package  # same, plus .tar.gz/.zip archives and SHA256SUMS.txt in ./dist/
VERSION=v0.2 ./scripts/build-docker.sh   # override the embedded version (default: git describe)
./scripts/package.sh                 # package whatever is in ./output into ./dist
```

Native builds without Docker (need the C toolchains/headers above):

```
./scripts/build.sh             # native build for the current host
./scripts/build.sh cross       # linux-amd64 linux-arm64 windows-amd64 (needs cross toolchains)
./scripts/build.sh macos       # darwin-arm64 darwin-amd64 darwin-universal (macOS + Xcode CLT)
```

macOS binaries cannot be cross-compiled in Docker (Apple SDK required); they are built on a
macOS host or by GitHub Actions. They are unsigned, so on first launch use right click > Open.

### GitHub Actions

`.github/workflows/release.yml` builds Linux and Windows executables with the same
`scripts/build-docker.sh` (Docker Buildx, layers cached in the GitHub Actions cache) and the macOS
executables natively on a macOS runner, then packages everything with `scripts/package.sh`.
On every `v*` tag (e.g. `git tag v0.1 && git push --tags`) the packages (`.tar.gz` / `.zip` +
`SHA256SUMS.txt`) are published as release assets, with the tag embedded as the version.
Pull requests and manual runs (workflow_dispatch) only produce downloadable build artifacts.

Requests, comments or suggestions: https://www.nitrobutton.com - nfn@nitrobutton.com
