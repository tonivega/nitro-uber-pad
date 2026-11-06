# syntax=docker/dockerfile:1
#
# Cross-compiles Nitro UberPad for linux/amd64, linux/arm64 and windows/amd64
# and exports the binaries (nothing else) into the --output directory.
#
# Usage:
#   ./scripts/build-docker.sh                     (recommended)
#   docker buildx build --target export --build-arg VERSION=v0.1 \
#       --output type=local,dest=output .
#
# macOS binaries cannot be built here (they need the Apple SDK); use
# ./scripts/build.sh macos on a Mac or the GitHub Actions workflow.

FROM --platform=linux/amd64 golang:1.22-bookworm AS build

RUN dpkg --add-architecture arm64 \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      pkg-config gcc libc6-dev \
      libgl1-mesa-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev \
      gcc-aarch64-linux-gnu libc6-dev-arm64-cross \
      libgl1-mesa-dev:arm64 libx11-dev:arm64 libxcursor-dev:arm64 libxrandr-dev:arm64 \
      libxinerama-dev:arm64 libxi-dev:arm64 libxxf86vm-dev:arm64 \
      gcc-mingw-w64-x86-64 \
 && rm -rf /var/lib/apt/lists/*

ENV GOFLAGS=-buildvcs=false
WORKDIR /src

# Module download is its own layer so it is reused (also via the GitHub Actions
# layer cache) as long as go.mod / go.sum do not change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=""
RUN --mount=type=cache,target=/root/.cache/go-build \
    go mod tidy \
 && VERSION="$VERSION" OUT_DIR=/out bash scripts/build.sh cross

FROM scratch AS export
COPY --from=build /out/ /
