#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/bin"

cd "$ROOT_DIR"

VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --dirty --always 2>/dev/null || echo dev)}"
VERSION="${VERSION//[^a-zA-Z0-9._-]/}"  # strip shell metacharacters from version string
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUILD_MODE="${BUILD_MODE:-release}"

LDFLAGS="-X hali/internal/buildinfo.Version=$VERSION -X hali/internal/buildinfo.Commit=$COMMIT -X hali/internal/buildinfo.BuildMode=$BUILD_MODE"

mkdir -p "$OUT_DIR"
rm -rf "$OUT_DIR/hali-linux-amd64" "$OUT_DIR/halid-linux-amd64" "$OUT_DIR/hali-tray-linux-amd64"

if [[ -x "$HOME/.local/go/bin/go" ]]; then
  GO_BIN="$HOME/.local/go/bin/go"
elif command -v go >/dev/null 2>&1; then
  GO_BIN="$(command -v go)"
elif command -v go.exe >/dev/null 2>&1; then
  GO_BIN="$(command -v go.exe)"
elif [[ -x "/mnt/c/Program Files/Go/bin/go.exe" ]]; then
  GO_BIN="/mnt/c/Program Files/Go/bin/go.exe"
else
  echo "Missing Go toolchain: install go in Linux or expose go.exe through WSL PATH."
  exit 1
fi

echo "Using Go toolchain: $GO_BIN"

echo "Building hali linux/amd64"
GOOS=linux GOARCH=amd64 "$GO_BIN" build -ldflags "$LDFLAGS" -o "$OUT_DIR/hali-linux-amd64" ./

echo "Building halid linux/amd64"
GOOS=linux GOARCH=amd64 "$GO_BIN" build -ldflags "$LDFLAGS" -o "$OUT_DIR/halid-linux-amd64" ./cmd/service

if [[ -d "$ROOT_DIR/cmd/tray" ]]; then
  echo "Attempting hali-tray linux/amd64"
  if GOOS=linux GOARCH=amd64 "$GO_BIN" build -ldflags "$LDFLAGS" -o "$OUT_DIR/hali-tray-linux-amd64" ./cmd/tray; then
    echo "Built hali-tray-linux-amd64"
  else
    echo "Skipped hali-tray-linux-amd64 (linux tray dependencies unavailable in this environment)"
  fi
fi

echo "Packaging tar.gz"
"$ROOT_DIR/installer/linux/package-tar.sh"

if command -v dpkg-deb >/dev/null 2>&1; then
  echo "Packaging deb"
  VERSION="$VERSION" "$ROOT_DIR/installer/linux/package-deb.sh"
else
  echo "Skipping deb packaging: dpkg-deb not found"
fi

echo "Done. Artifacts are in $OUT_DIR"
