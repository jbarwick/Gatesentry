#!/usr/bin/env bash
set -euo pipefail

# Install the released image on the NAS (monster-jj).
# Pulls the version from GATESENTRY_VERSION in main.go (same as ./release.sh).
#
# Prerequisite: ./release.sh has pushed that tag to Nexus.
#
# Usage:
#   ./deploy.sh
#
# Environment:
#   NAS_HOST      default monster-jj
#   NAS_USER      default root
#   NAS_COMPOSE   default /volume1/docker/Gatesentry/docker-compose.yml
#   NEXUS_SERVER  default https://monster-jj.jvj28.com:9092

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

NAS_HOST="${NAS_HOST:-monster-jj}"
NAS_USER="${NAS_USER:-root}"
NAS_COMPOSE="${NAS_COMPOSE:-/volume1/docker/Gatesentry/docker-compose.yml}"
IMAGE_NAME="gatesentry"
VERSION=""
ADMIN_URL="http://${NAS_HOST}:9876/gatesentry/api/about"
OVERLAY="${ROOT}/../gatesentry-synology/docker-compose.yml"

version_from_source() {
    grep -E 'GATESENTRY_VERSION[[:space:]]*=' main.go \
        | head -1 \
        | sed -n 's/.*"\([^"]*\)".*/\1/p'
}

usage() {
    sed -n '2,18p' "$0" | sed 's/^# \?//'
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    usage
    exit 0
fi
if [[ $# -gt 0 ]]; then
    echo "Unknown option: $1" >&2
    usage
    exit 1
fi

if [[ -z "$VERSION" ]]; then
    VERSION="$(version_from_source)"
fi
if [[ -z "$VERSION" ]]; then
    echo "error: could not read GATESENTRY_VERSION from main.go" >&2
    exit 1
fi

NEXUS_SERVER="${NEXUS_SERVER:-https://monster-jj.jvj28.com:9092}"
NEXUS_REGISTRY="${NEXUS_SERVER#https://}"
NEXUS_REGISTRY="${NEXUS_REGISTRY#http://}"
IMAGE="${NEXUS_REGISTRY}/${IMAGE_NAME}:${VERSION}"

ssh_nas() {
    ssh -o BatchMode=yes "${NAS_USER}@${NAS_HOST}" "export PATH=\$PATH:/usr/local/bin:/usr/bin; $*"
}

echo "Deploy ${VERSION}"
echo "  Host:   ${NAS_USER}@${NAS_HOST}"
echo "  Image:  ${IMAGE}"
echo "  File:   ${NAS_COMPOSE}"
echo ""

echo "── Updating NAS compose tag ──────────────────────────────────"
ssh_nas "grep -q 'image: .*/gatesentry:' ${NAS_COMPOSE}"
ssh_nas "sed -i -E 's|(image:[[:space:]]*[^[:space:]]+/gatesentry:)[^[:space:]]+|\\1${VERSION}|' ${NAS_COMPOSE}"
ssh_nas "grep -E '^[[:space:]]*image:' ${NAS_COMPOSE}"

if [[ -f "$OVERLAY" ]]; then
    sed -i -E "s|(image:[[:space:]]*[^[:space:]]+/gatesentry:)[^[:space:]]+|\\1${VERSION}|" "$OVERLAY"
    echo "synced overlay ${OVERLAY}"
fi

echo "── Recreating container ──────────────────────────────────────"
# Synology Container Manager ships docker-compose as a separate binary
# (not the `docker compose` plugin).
ssh_nas "cd $(dirname "${NAS_COMPOSE}") && docker-compose pull && docker-compose up -d"

echo "── Waiting for admin ─────────────────────────────────────────"
ok=0
for _ in $(seq 1 60); do
    if curl --noproxy '*' -fsS -o /dev/null --max-time 3 "$ADMIN_URL" 2>/dev/null; then
        ok=1
        break
    fi
    sleep 1
done
if [[ "$ok" -ne 1 ]]; then
    echo "error: admin did not respond at ${ADMIN_URL}" >&2
    ssh_nas "docker ps --filter name=gatesentry --format '{{.Names}} {{.Image}} {{.Status}}'; docker logs --tail 40 gatesentry" || true
    exit 1
fi

ssh_nas "docker ps --filter name=gatesentry --format '{{.Names}} {{.Image}} {{.Status}}'"
echo ""
echo "Deployed ${IMAGE}"
echo "  Admin: http://${NAS_HOST}:9876/gatesentry/"
