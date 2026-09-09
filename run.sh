#!/bin/bash

# DNS Server Configuration
# Set the listen address (default: 0.0.0.0 - all interfaces)
export GATESENTRY_DNS_ADDR="${GATESENTRY_DNS_ADDR:-0.0.0.0}"

# Set the DNS port (default: 10053 for local dev, avoids conflict with system DNS)
export GATESENTRY_DNS_PORT="${GATESENTRY_DNS_PORT:-10053}"

# Upstream resolver. Leave unset to keep the stored setting / 8.8.8.8 default.
# Override per site, e.g. GATESENTRY_DNS_RESOLVER=192.0.2.1:53
if [ -n "${GATESENTRY_DNS_RESOLVER:-}" ]; then
	export GATESENTRY_DNS_RESOLVER
fi

# Admin UI port — default 80 requires root; use 8080 for local dev
export GS_ADMIN_PORT="${GS_ADMIN_PORT:-8080}"
export GS_MAX_SCAN_SIZE_MB="${GS_MAX_SCAN_SIZE_MB:-2}"

# Unset proxy env vars — the GateSentry proxy server must not route its own
# outbound requests through itself (or any other proxy).
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY no_proxy NO_PROXY

# Kill any existing gatesentry processes so the new binary can bind ports
pkill -f gatesentryb 2>/dev/null
sleep 1

if [ "$1" == "--build" ]; then
    echo "Building GateSentry binary..."
    bash build.sh
    if [ $? -ne 0 ]; then
        echo "Build failed. Exiting."
        exit 1
    fi
fi

cd bin && ./gatesentrybin > ../log.txt 2>&1
