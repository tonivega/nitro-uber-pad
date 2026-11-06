#!/usr/bin/env bash
# Packages the binaries in ./output/ (or $OUT_DIR) into release archives in
# ./dist/ (or $DIST_DIR): .zip for Windows, .tar.gz otherwise, plus SHA256SUMS.txt.
# Requires tar and zip.
set -euo pipefail
cd "$(dirname "$0")/.."

APP=nitro-uber-pad
OUT="${OUT_DIR:-output}"
DIST="${DIST_DIR:-dist}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || true)}"
VERSION="${VERSION:-dev}"

shopt -s nullglob
files=("$OUT"/"$APP"-*)
if [ ${#files[@]} -eq 0 ]; then
  echo "no binaries found in $OUT/ (run scripts/build-docker.sh first)" >&2
  exit 1
fi

rm -rf "$DIST"
mkdir -p "$DIST"
dist_abs=$(cd "$DIST" && pwd)

for f in "${files[@]}"; do
  name=$(basename "$f")
  target="${name#"$APP"-}"
  target="${target%.exe}"
  pkg="$APP-$VERSION-$target"
  stage=$(mktemp -d)
  cp "$f" "$stage/$name"
  [ -f README.md ] && cp README.md "$stage/"
  case "$name" in
    *.exe)
      command -v zip >/dev/null 2>&1 || { echo "zip not found" >&2; rm -rf "$stage"; exit 1; }
      (cd "$stage" && zip -9 -q -r "$dist_abs/$pkg.zip" .)
      echo ">> $DIST/$pkg.zip"
      ;;
    *)
      chmod +x "$stage/$name"
      tar -C "$stage" -czf "$dist_abs/$pkg.tar.gz" .
      echo ">> $DIST/$pkg.tar.gz"
      ;;
  esac
  rm -rf "$stage"
done

cd "$DIST"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -- *.* > SHA256SUMS.txt
else
  shasum -a 256 -- *.* > SHA256SUMS.txt
fi
echo "Release packages in $DIST/:"
ls -l
