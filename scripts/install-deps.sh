#!/usr/bin/env bash
# Installs the C compiler, OpenGL and X11 development packages that Fyne needs
# to build natively (fixes "Package gl was not found" and "X11/Xlib.h: No such file").
set -euo pipefail

SUDO=""
if [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
  SUDO=sudo
fi

if [ "$(uname -s)" = Darwin ]; then
  xcode-select -p >/dev/null 2>&1 || xcode-select --install
  echo "Xcode command line tools are installed (or installing)."
  exit 0
fi

if command -v apt-get >/dev/null 2>&1; then
  $SUDO apt-get update
  $SUDO apt-get install -y gcc pkg-config libgl1-mesa-dev libx11-dev libxcursor-dev \
    libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev
elif command -v dnf >/dev/null 2>&1; then
  $SUDO dnf install -y gcc pkgconf-pkg-config mesa-libGL-devel libX11-devel libXcursor-devel \
    libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel
elif command -v pacman >/dev/null 2>&1; then
  $SUDO pacman -S --needed --noconfirm gcc pkgconf mesa libx11 libxcursor libxrandr \
    libxinerama libxi libxxf86vm
elif command -v zypper >/dev/null 2>&1; then
  $SUDO zypper install -y gcc pkg-config Mesa-libGL-devel libX11-devel libXcursor-devel \
    libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel
elif command -v apk >/dev/null 2>&1; then
  $SUDO apk add gcc musl-dev pkgconf mesa-dev libx11-dev libxcursor-dev libxrandr-dev \
    libxinerama-dev libxi-dev libxxf86vm-dev
else
  echo "Unknown package manager. Install: gcc, pkg-config, OpenGL (gl.pc) and X11 dev headers" >&2
  echo "(Xlib, Xcursor, Xrandr, Xinerama, Xi, Xxf86vm)." >&2
  exit 1
fi

echo "Dependencies installed. You can now run: go run ."
