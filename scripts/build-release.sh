#!/usr/bin/env sh
# Cross-compile release binaries for all supported platforms into ./dist.
# Usage: VERSION=v0.1.0 sh scripts/build-release.sh
set -eu

REPO_MODULE="github.com/defomok-max/Ties"
OUT="${OUT:-dist}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w \
  -X ${REPO_MODULE}/internal/version.Version=${VERSION} \
  -X ${REPO_MODULE}/internal/version.Commit=${COMMIT} \
  -X ${REPO_MODULE}/internal/version.Date=${DATE}"

mkdir -p "$OUT"
rm -f "$OUT"/ties-* "$OUT"/SHA256SUMS.txt

build() {
  os="$1"; arch="$2"; ext="${3:-}"
  out="$OUT/ties-${os}-${arch}${ext}"
  echo "→ $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/ties
}

build windows amd64 .exe
build windows arm64 .exe
build linux   amd64
build linux   arm64
build darwin  amd64
build darwin  arm64

( cd "$OUT" && sha256sum ties-* > SHA256SUMS.txt )
echo ""
echo "Built ${VERSION} into $OUT/:"
ls -1 "$OUT"
echo ""
echo "Publish with:"
echo "  gh release create ${VERSION} $OUT/ties-* $OUT/SHA256SUMS.txt --title 'Ties ${VERSION}' --notes '...'"
