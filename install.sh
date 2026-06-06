#!/usr/bin/env sh
# Ties installer — builds the single static binary and puts it on your PATH.
#
# Usage (from a checkout of the repo):
#   ./install.sh                 # installs to the best writable dir on PATH
#   PREFIX=$HOME/.local ./install.sh   # install under ~/.local/bin
#
# Requires: Go 1.23+ and git.
set -eu

REPO_MODULE="github.com/defomok-max/Ties"
BINARY="ties"

# --- locate the source ----------------------------------------------------
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

if ! command -v go >/dev/null 2>&1; then
  echo "error: Go is not installed. Get it from https://go.dev/dl/ (need 1.23+)." >&2
  exit 1
fi

# --- version metadata -----------------------------------------------------
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo none)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w \
  -X ${REPO_MODULE}/internal/version.Version=${VERSION} \
  -X ${REPO_MODULE}/internal/version.Commit=${COMMIT} \
  -X ${REPO_MODULE}/internal/version.Date=${DATE}"

echo "Building ${BINARY} ${VERSION} ..."
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$BINARY" ./cmd/ties

# --- choose an install dir ------------------------------------------------
if [ -n "${PREFIX:-}" ]; then
  BINDIR="$PREFIX/bin"
elif [ -n "${GOBIN:-}" ]; then
  BINDIR="$GOBIN"
elif [ -w "/usr/local/bin" ]; then
  BINDIR="/usr/local/bin"
elif [ -d "$HOME/.local/bin" ] || mkdir -p "$HOME/.local/bin" 2>/dev/null; then
  BINDIR="$HOME/.local/bin"
else
  BINDIR="/usr/local/bin"
fi

mkdir -p "$BINDIR" 2>/dev/null || true

if [ -w "$BINDIR" ]; then
  install -m 0755 "$BINARY" "$BINDIR/$BINARY"
else
  echo "→ $BINDIR needs elevated permissions, using sudo"
  sudo install -m 0755 "$BINARY" "$BINDIR/$BINARY"
fi

echo "Installed: $BINDIR/$BINARY"

# --- PATH hint ------------------------------------------------------------
case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *) echo ""
     echo "Note: $BINDIR is not on your PATH. Add this to your shell profile:"
     echo "  export PATH=\"$BINDIR:\$PATH\"" ;;
esac

echo ""
echo "Done. Try:"
echo "  $BINARY auth login anthropic     # add a provider key"
echo "  $BINARY chat --tui               # full-screen chat"
