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
#   NEXUS_TOKEN       docker login username for some Nexus IP endpoints
#   NEXUS_USERNAME    docker login username (hostname registries)
#   NEXUS_PASSWORD    docker login password for the account

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

nexus_host_is_ip() {
    local host="${NEXUS_REGISTRY%%:*}"
    [[ "$host" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] && return 0
    [[ "$host" == \[* ]] && return 0
    return 1
}

nexus_docker_login() {
    local user="$1" pass="$2" how="$3"
    echo "  docker login (${how})"
    printf '%s\n' "$pass" | docker login "${NEXUS_REGISTRY}" -u "$user" --password-stdin
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

HAS_USER=false
HAS_TOKEN=false
[[ -n "${NEXUS_USERNAME:-}" && -n "${NEXUS_PASSWORD:-}" ]] && HAS_USER=true
[[ -n "${NEXUS_TOKEN:-}" ]] && HAS_TOKEN=true

if [[ "$HAS_USER" != true && "$HAS_TOKEN" != true ]]; then
    echo "error: set NEXUS_TOKEN, or NEXUS_USERNAME and NEXUS_PASSWORD" >&2
    exit 1
fi

# Hostname registries typically want the account. Nexus reached by IP often
# wants NEXUS_TOKEN as docker -u, not NEXUS_USERNAME.
FIRST_AUTH="username"
if nexus_host_is_ip && [[ "$HAS_TOKEN" == true ]]; then
    FIRST_AUTH="token"
elif [[ "$HAS_USER" != true ]]; then
    FIRST_AUTH="token"
fi

echo "Release ${VERSION}"
echo "  Local:  ${IMAGE_NAME}:${VERSION}"
echo "  Remote: ${REPO}:${VERSION}"
echo ""

echo "── Packaging image ───────────────────────────────────────────"
docker build -t "${IMAGE_NAME}:${VERSION}" -t "${REPO}:${VERSION}" -t "${REPO}:latest" .

echo "── Publishing to Nexus (${NEXUS_REGISTRY}) ───────────────────"
logged_in=false
if [[ "$FIRST_AUTH" == "token" ]]; then
    if nexus_docker_login "$NEXUS_TOKEN" "$NEXUS_TOKEN" "token"; then
        logged_in=true
    elif [[ "$HAS_USER" == true ]] && nexus_docker_login "$NEXUS_USERNAME" "$NEXUS_PASSWORD" "username"; then
        logged_in=true
    fi
else
    if nexus_docker_login "$NEXUS_USERNAME" "$NEXUS_PASSWORD" "username"; then
        logged_in=true
    elif [[ "$HAS_TOKEN" == true ]] && nexus_docker_login "$NEXUS_TOKEN" "$NEXUS_TOKEN" "token"; then
        logged_in=true
    fi
fi
if [[ "$logged_in" != true ]]; then
    echo "error: docker login to ${NEXUS_REGISTRY} failed" >&2
    exit 1
fi

docker push "${REPO}:${VERSION}"
docker push "${REPO}:latest"

echo ""
echo "Published ${REPO}:${VERSION}"
echo "          ${REPO}:latest"
echo "Install on NAS with: ./deploy.sh"
