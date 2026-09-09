#!/usr/bin/env bash
set -euo pipefail

# Build software binaries. Does not package or deploy.
#   UI (if ui/node_modules exists) + Go → bin/gatesentrybin
#
# Next: ./release.sh
#
# Usage: ./build.sh

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

version_from_source() {
    grep -E 'GATESENTRY_VERSION[[:space:]]*=' main.go \
        | head -1 \
        | sed -n 's/.*"\([^"]*\)".*/\1/p'
}

VERSION="$(version_from_source)"
EMBED_DIR="application/webserver/frontend/files"

echo "Building GateSentry ${VERSION:-unknown}"
echo ""

if [[ -d ui/node_modules ]]; then
    echo "── UI ────────────────────────────────────────────────────────"
    (cd ui && npm run build)
    echo "Copying UI dist into Go embed directory..."
    find "${EMBED_DIR}" -mindepth 1 ! -name '.gitkeep' -delete
    cp -r ui/dist/* "$EMBED_DIR"/
else
    echo "Skipping UI build (ui/node_modules not found — run 'cd ui && npm install' first)"
    echo "Using existing frontend files in $EMBED_DIR"
fi

echo ""
echo "── Binary ────────────────────────────────────────────────────"
mkdir -p bin
find bin -maxdepth 1 -type f -delete
CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/ ./...

if [[ ! -x bin/gatesentrybin ]]; then
    echo "error: bin/gatesentrybin was not produced" >&2
    exit 1
fi

echo "Build successful: bin/gatesentrybin (${VERSION})"
