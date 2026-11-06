#!/usr/bin/env bash
# Build Nitro UberPad executables into ./output/ (or $OUT_DIR).
#
# Usage: scripts/build.sh [target...]
#   (no args)  build for the current host
#   cross      linux-amd64 linux-arm64 windows-amd64 (needs cross compilers, see Dockerfile)
#   macos      darwin-arm64 darwin-amd64 darwin-universal (macOS host with Xcode CLT)
#   targets:   linux-amd64 linux-arm64 windows-amd64 darwin-amd64 darwin-arm64 darwin-universal
#
# Environment: VERSION (embedded in the binary, default: git describe), OUT_DIR.
# For release builds prefer scripts/build-docker.sh (reproducible toolchain).
set -euo pipefail
cd "$(dirname "$0")/.."

APP=nitro-uber-pad
OUT="${OUT_DIR:-output}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || true)}"
LDFLAGS="-s -w"
if [ -n "$VERSION" ]; then
  LDFLAGS="$LDFLAGS -X nitrobutton.com/nitro-uber-pad/internal/config.Version=$VERSION"
fi
mkdir -p "$OUT"

host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}"

linux_triple() {
  case "$1" in
    amd64) echo x86_64-linux-gnu ;;
    arm64) echo aarch64-linux-gnu ;;
    *) echo "unsupported linux arch: $1" >&2; return 1 ;;
  esac
}

build() {
  local goos=$1 goarch=$2 ext="" ld="$LDFLAGS" cc="" pkgdir=""
  case "$goos" in
    windows)
      ext=".exe"
      ld="$ld -H windowsgui"
      if [ "$host_os" = windows ]; then cc=gcc; else cc=x86_64-w64-mingw32-gcc; fi
      ;;
    linux)
      if [ "$host_os" = linux ] && [ "$host_arch" = "$goarch" ]; then
        cc=gcc
      else
        local triple
        triple=$(linux_triple "$goarch")
        cc="${triple}-gcc"
        pkgdir="/usr/lib/${triple}/pkgconfig:/usr/share/pkgconfig"
      fi
      ;;
    darwin)
      if [ "$host_os" != darwin ]; then
        echo "!! skipping $goos-$goarch: macOS builds require a macOS host" >&2
        return 1
      fi
      cc=clang
      ;;
    *) echo "unsupported OS: $goos" >&2; return 1 ;;
  esac
  if ! command -v "$cc" >/dev/null 2>&1; then
    echo "!! C compiler '$cc' not found for $goos-$goarch (use scripts/build-docker.sh or scripts/install-deps.sh)" >&2
    return 1
  fi
  local out="$OUT/$APP-$goos-$goarch$ext"
  echo ">> building $out (CC=$cc)"
  local envs=(CGO_ENABLED=1 "GOOS=$goos" "GOARCH=$goarch" "CC=$cc")
  if [ -n "$pkgdir" ]; then
    envs+=("PKG_CONFIG_LIBDIR=$pkgdir")
  fi
  env "${envs[@]}" go build -trimpath -ldflags "$ld" -o "$out" .
}

universal() {
  local a="$OUT/$APP-darwin-arm64" b="$OUT/$APP-darwin-amd64"
  [ -f "$a" ] || build darwin arm64
  [ -f "$b" ] || build darwin amd64
  echo ">> building $OUT/$APP-darwin-universal"
  lipo -create -output "$OUT/$APP-darwin-universal" "$a" "$b"
}

targets=("$@")
if [ ${#targets[@]} -eq 0 ]; then
  targets=("$host_os-$host_arch")
fi

expanded=()
for t in "${targets[@]}"; do
  case "$t" in
    cross) expanded+=(linux-amd64 linux-arm64 windows-amd64) ;;
    macos) expanded+=(darwin-arm64 darwin-amd64 darwin-universal) ;;
    *) expanded+=("$t") ;;
  esac
done

for t in "${expanded[@]}"; do
  case "$t" in
    darwin-universal) universal ;;
    *-*) build "${t%%-*}" "${t#*-}" ;;
    *) echo "unknown target: $t" >&2; exit 1 ;;
  esac
done

echo "Done. Binaries in $OUT/:"
ls -l "$OUT"
