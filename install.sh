#!/usr/bin/env bash
# godx-arbiter installer — curl | bash flavor.
#
#   curl -sSL https://godx-arbiter.dev/install.sh | bash
#
# Honors PREFIX (default: $HOME/.local) and GODX_ARBITER_VERSION.
set -euo pipefail

REPO="godx-team/godx-arbiter"
VERSION="${GODX_ARBITER_VERSION:-latest}"
PREFIX="${PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"

require() { command -v "$1" >/dev/null 2>&1 || { echo >&2 "missing dependency: $1"; exit 1; }; }
require curl
require uname

case "$(uname -s)" in
  Linux*)   GOOS=linux ;;
  Darwin*)  GOOS=darwin ;;
  *)        echo "unsupported OS: $(uname -s)"; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *)             echo "unsupported arch: $(uname -m)"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"v\([^"]*\)".*/\1/p')
  if [ -z "$VERSION" ]; then
    echo "could not resolve latest version; set GODX_ARBITER_VERSION explicitly"
    exit 1
  fi
fi

ASSET="arbiter-${GOOS}-${GOARCH}"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${ASSET}"
SUM_URL="${URL}.sha256"

mkdir -p "$BIN_DIR"
TMP="$(mktemp)"
echo ">> downloading $URL"
curl -fsSL "$URL" -o "$TMP"

if curl -fsSL "$SUM_URL" -o "$TMP.sha256" 2>/dev/null; then
  expected=$(awk '{print $1}' "$TMP.sha256")
  got=$(shasum -a 256 "$TMP" | awk '{print $1}')
  if [ "$expected" != "$got" ]; then
    echo "checksum mismatch: got $got, want $expected" >&2
    exit 1
  fi
  echo ">> checksum ok"
else
  echo ">> warning: no checksum file at $SUM_URL — skipping verification" >&2
fi

install -m 0755 "$TMP" "$BIN_DIR/arbiter"
rm -f "$TMP" "$TMP.sha256"

echo ">> installed: $BIN_DIR/arbiter"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo ">> note: add $BIN_DIR to your PATH" ;;
esac
"$BIN_DIR/arbiter" --version
