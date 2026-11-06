#!/usr/bin/env bash
# Builds linux/amd64, linux/arm64 and windows/amd64 release binaries inside Docker
# into ./output/. On a macOS host it additionally builds the macOS binaries
# natively (if Go is installed).
#
# Usage: scripts/build-docker.sh [--package] [--no-macos]
#   --package   also create release archives + SHA256SUMS.txt in ./dist/ (scripts/package.sh)
#   --no-macos  do not build macOS binaries even on a Mac
#
# Environment:
#   VERSION       version embedded in the binaries (default: git describe)
#   DOCKER_CACHE  set to "gha" to use the GitHub Actions cache (needs buildx)
set -euo pipefail
cd "$(dirname "$0")/.."

usage() { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; }

package=0
macos=1
for arg in "$@"; do
  case "$arg" in
    --package) package=1 ;;
    --no-macos) macos=0 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $arg" >&2; usage >&2; exit 1 ;;
  esac
done

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found: install Docker (https://docs.docker.com/get-docker/)" >&2
  exit 1
fi

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || true)}"
export VERSION
echo ">> version: ${VERSION:-<none>}"

rm -rf output
mkdir -p output

args=(--target export --output "type=local,dest=output" --build-arg "VERSION=$VERSION")
if [ "${DOCKER_CACHE:-}" = gha ]; then
  args+=(--cache-from "type=gha,scope=nitro-uber-pad"
         --cache-to "type=gha,mode=max,scope=nitro-uber-pad")
fi

if docker buildx version >/dev/null 2>&1; then
  docker buildx build "${args[@]}" .
else
  if [ "${DOCKER_CACHE:-}" = gha ]; then
    echo "DOCKER_CACHE=gha requires docker buildx" >&2
    exit 1
  fi
  DOCKER_BUILDKIT=1 docker build "${args[@]}" .
fi

if [ "$macos" = 1 ] && [ "$(uname -s)" = Darwin ]; then
  if command -v go >/dev/null 2>&1; then
    bash scripts/build.sh macos
  else
    echo "Go not found: skipping macOS build" >&2
  fi
fi

echo "Binaries in ./output:"
ls -l output

if [ "$package" = 1 ]; then
  bash scripts/package.sh
fi
