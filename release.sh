#!/usr/bin/env bash
set -euo pipefail

# Package binaries from ./build.sh into a versioned image and publish it
# to the Nexus registry so the NAS can pull it. Does not deploy.
#
# Version comes from GATESENTRY_VERSION in main.go.
# Tags: gatesentry:<ver>, <nexus>/gatesentry:<ver>, <nexus>/gatesentry:latest
#
# Prerequisite: Docker daemon on this machine, bin/gatesentrybin
#
# Next: ./deploy.sh
#
# Usage:
#   ./release.sh
#
# Environment:
#   NEXUS_SERVER      default https://monster-jj.jvj28.com:9092
#   NEXUS_TOKEN       preferred docker login username (required for some
#                     Nexus IP endpoints)
#   NEXUS_USERNAME    docker login username when NEXUS_TOKEN is unset
#   NEXUS_PASSWORD    docker login password (token passcode, or user password)

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

IMAGE_NAME="gatesentry"

version_from_source() {
    grep -E 'GATESENTRY_VERSION[[:space:]]*=' main.go \
        | head -1 \
        | sed -n 's/.*"\([^"]*\)".*/\1/p'
}

usage() {
    sed -n '2,24p' "$0" | sed 's/^# \?//'
}

require_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        echo "error: docker is required on this machine" >&2
        echo "  Start Docker Desktop and retry." >&2
        exit 1
    fi
    if ! docker info >/dev/null 2>&1; then
        echo "error: Docker daemon is not running" >&2
        echo "  Start Docker Desktop and retry." >&2
        exit 1
    fi
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

VERSION="$(version_from_source)"
if [[ -z "$VERSION" ]]; then
    echo "error: could not read GATESENTRY_VERSION from main.go" >&2
    exit 1
fi

NEXUS_SERVER="${NEXUS_SERVER:-https://monster-jj.jvj28.com:9092}"
NEXUS_REGISTRY="${NEXUS_SERVER#https://}"
NEXUS_REGISTRY="${NEXUS_REGISTRY#http://}"
REPO="${NEXUS_REGISTRY}/${IMAGE_NAME}"

require_docker

if [[ ! -x bin/gatesentrybin ]]; then
    echo "error: bin/gatesentrybin missing — run ./build.sh first" >&2
    exit 1
fi

# Some Nexus listeners (especially by IP) want the user token as -u, not the
# account name. Prefer NEXUS_TOKEN; otherwise NEXUS_USERNAME + NEXUS_PASSWORD.
if [[ -n "${NEXUS_TOKEN:-}" ]]; then
    NEXUS_LOGIN_USER="$NEXUS_TOKEN"
    NEXUS_LOGIN_PASS="${NEXUS_PASSWORD:-$NEXUS_TOKEN}"
    NEXUS_LOGIN_HOW="token"
elif [[ -n "${NEXUS_USERNAME:-}" && -n "${NEXUS_PASSWORD:-}" ]]; then
    NEXUS_LOGIN_USER="$NEXUS_USERNAME"
    NEXUS_LOGIN_PASS="$NEXUS_PASSWORD"
    NEXUS_LOGIN_HOW="username"
else
    echo "error: set NEXUS_TOKEN, or NEXUS_USERNAME and NEXUS_PASSWORD" >&2
    exit 1
fi

echo "Release ${VERSION}"
echo "  Local:  ${IMAGE_NAME}:${VERSION}"
echo "  Remote: ${REPO}:${VERSION}"
echo "  Auth:   ${NEXUS_LOGIN_HOW}"
echo ""

echo "── Packaging image ───────────────────────────────────────────"
docker build -t "${IMAGE_NAME}:${VERSION}" -t "${REPO}:${VERSION}" -t "${REPO}:latest" .

echo "── Publishing to Nexus (${NEXUS_REGISTRY}) ───────────────────"
printf '%s\n' "$NEXUS_LOGIN_PASS" | docker login "${NEXUS_REGISTRY}" -u "${NEXUS_LOGIN_USER}" --password-stdin
docker push "${REPO}:${VERSION}"
docker push "${REPO}:latest"

echo ""
echo "Published ${REPO}:${VERSION}"
echo "          ${REPO}:latest"
echo "Install on NAS with: ./deploy.sh"
