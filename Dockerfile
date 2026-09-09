# =============================================================================
# GateSentry Runtime Image
#
# This is a runtime-only container. The Go binary (with the Svelte UI embedded)
# is built on the host via build.sh and copied in. No Node, no Go toolchain,
# no build dependencies — just Alpine + the binary.
#
# Build workflow:
#   ./build.sh          # builds UI + Go binary → bin/gatesentrybin
#   docker compose up -d --build
# =============================================================================

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata iputils

WORKDIR /usr/local/gatesentry

# Copy the pre-built binary (built on the host by build.sh)
COPY bin/gatesentrybin ./gatesentry-bin

# Pre-create the data directory (volume mount point for persistent state)
RUN mkdir -p /usr/local/gatesentry/gatesentry

# Documented ports (actual binds come from env; host-network often uses 53/80):
#   10053  - DNS (bridged sample)
#   8080   - Admin UI (GS_ADMIN_PORT)
#   10413  - HTTP(S) proxy
#   10414  - Transparent proxy
#   5353   - mDNS
EXPOSE 10053/udp 10053/tcp 8080/tcp 10413/tcp 10414/tcp 5353/udp

ENTRYPOINT ["./gatesentry-bin"]
