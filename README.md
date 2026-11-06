# Nitro UberPad v0.1

A multiplatform (Windows, macOS, Linux) scriptable text editor written in Go with Fyne.

- Opens files of any size: pages are read with seeks/reads and a read-ahead/behind cache.
- Edits are kept per page (memory, spilled to temp files when large) and applied on save.
- Line numbers (absolute file lines) in the text view; character inspector showing emoji /
  unicode code points, names, UTF-8 bytes and `\u`/`\U`/`\x`/`&#..;` escapes (Edit > Decode Unicode Escapes).
- Pages with non-representable bytes switch automatically to a hex view with offsets on the left;
  type hex digits to overwrite individual nibbles in place (double-click a row to replace its bytes).
- Files changed on disk by other programs are flagged (banner, `!` in the tab); saving asks before
  overwriting and a "Reload from disk" button is shown.
- Lua scripting with HTTP GET/POST (`http.get`, `http.post`, `http.request`, returning body and HTTP
  code, default timeout selectable in Settings), cursor positioning (`editor.set_cursor`) and
  `nup.eval(code)` to run Lua code from a variable (like PHP `eval`), useful for dynamic
  integrations from outside the app, e.g. a toolbar button running code from an HTTP server.
  See Help > Scripting Help & Snippets.
- Very long lines are read through a horizontal window (seek/read) that loads more when scrolling.
- Tabs, paginated recent files (last 100), change history saved to `<file>.nup.log`.
- Configurable menu entries, toolbar buttons and keyboard shortcuts backed by Lua scripts.
- Settings key/values usable from scripts (`config.get`).
- Configuration stored as JSON in `~/.nitro.uberpad.rc`.

The default **Save** action saves existing files in place; new files ask for a prefix and are
saved as `<prefix>-YYYYMMDD.txt` into the `save_dir` variable directory.

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
