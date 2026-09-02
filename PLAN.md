# GateSentry 2.0.0-beta.1 — Availability Fix Plan

Tracking for the monster-jj hang (upstream DNS down → admin unusable) and related control-plane bugs. Version numbering moved from `2.0.0-alpha.15` to **`2.0.0-beta.1`**.

Legend: `[ ]` not started · `[x]` done

## Phase 0 — Recover monster-jj (ops)

- [x] Leave `dns_resolver` at `192.168.1.1:53` (upstream is back; do not set `GATESENTRY_DNS_RESOLVER`)
- [x] Rotate `log.db` aside (`log.db.bak-20260902-131403`, 129MB)
- [x] Start container (`docker-compose -f /volume1/docker/Gatesentry/docker-compose.yml up -d`)
- [x] Verify admin: `http://monster-jj:9876/` → 302 `/gatesentry/` → 200
- [x] After 2.0.0-beta.1 image is published, bump the NAS compose tag and start

## Phase 1 — Control plane stays up when upstream DNS is down

### 1.1 Storage / settings

- [x] `MapStore.Update`: `defer Unlock()`; never return while holding the mutex
- [x] Lock `Get` / `GetInt` / `SetDefault`; keep an in-memory map (no full JSON unmarshal per Get)
- [x] `Reload()` in place — `Init()` must not replace the `MapStore` pointer
- [x] Skip blocking `lsof`/`netstat` on every `Init`/reload
- [x] Unit tests: corrupt-JSON Update does not deadlock; concurrent Get/Update

### 1.2 DNS upstream failure handling

- [x] Negative-cache SERVFAIL/timeouts (`PutWithTTL` / `PutFailure`, ~15s)
- [x] Semaphore on in-flight upstream forwards (cap 32)
- [x] Circuit breaker: after consecutive failures, fail-fast for ~10s
- [x] Store `externalResolver` in `atomic.Value`
- [x] Safe QNAME handling (`TrimSuffix`, ignore empty names)
- [x] Unit tests: failure cache TTL, circuit open/close

### 1.3 Hot-path load

- [x] Remove per-query `log.Println` in `handleDNSRequest`
- [x] Bound `LogDNS`/`LogProxy`: single worker + drop-on-full queue; `json.Marshal`
- [x] Cache `dns_domain_lists` / whitelist IDs; refresh on settings POST

### 1.4 Admin remains reachable

- [x] Host allowlist: `GS_ADMIN_HOSTS`, `hostname.<dns_local_zone>`, hostname/IP/WPAD host
- [x] Appliance A records for those names (do not forward admin hostname to upstream)
- [x] Admin `http.Server` timeouts (no `WriteTimeout` so SSE lives)
- [x] Auth on `GET /api/logs/{id}`

### 1.5 Shutdown, proxy, defaults

- [x] Real `application.Stop()`: DNS (incl. cache recorder), admin `Shutdown`, close BuntDB
- [x] `http.Transport.Proxy = nil` on in-process clients
- [x] `InitProxy` must not overwrite `GS_MAX_SCAN_SIZE_MB`
- [x] DDNS default **off** for new installs
- [x] `log.Fatal` in settings POST → log + return
- [x] Cache-history scan only `cache:stats:` keys

## Phase 2 — Capacity

- [x] Cap BuntDB stats walks (`maxLogScan`); default stats window 24h
- [x] Domain-list index: match parent domains, skip 1-label TLDs
- [x] EventBus `enabled` as `atomic.Bool`
- [x] Bump `GATESENTRY_VERSION` to `2.0.0-beta.1`
- [x] Sync `../gatesentry-synology/docker-compose.yml` to NAS layout + resolver override + beta tag

## Phase 3 — Verify and ship

- [x] Unit tests: storage, dns cache, dns server, domainlist (passing)
- [x] Build `2.0.0-beta.1` UI+binary (`./build.sh`) and publish to Nexus
- [x] Deploy on monster-jj (`docker-compose -f /volume1/docker/Gatesentry/docker-compose.yml up -d`)

## Phase 4 — Follow-ups (2.0.0-beta.2)

- [x] Fallback blocked image when `blocked.jpg` is missing
- [x] Bonjour RegisterProxy with LAN IP
- [x] Timezone from `TZ` env; drop per-request timezone log
- [x] Dual-stack DNS listen; AAAA for self/WPAD/block
- [x] IPv6 upstream setting `dns_resolver_ipv6` (default `fd00:1234:5678::1`); AAAA uses it
- [x] Quiet IPv6 no-route proxy logs
- [x] Build/publish/deploy `2.0.0-beta.2`
- [x] Advertise all five NICs (91–95) on appliance A/AAAA records
