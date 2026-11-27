# Device Discovery Service Plan

## Executive Summary

Gatesentry currently operates as a forwarding DNS server with ad-blocking/parental controls
and a simple internal A-record override system. This document describes a vision to transform
it into a **home network device inventory system** — automatically discovering every device on
the local network and making them resolvable by name, regardless of router capability.

---

## The Problem

Every home has a router with a DHCP server. When DHCP hands a device an IP address, the
router knows the device exists — but the DNS server doesn't. Most home users don't care
about DNS or domain names. They just want to say "hey, what's the IP of my printer?" or
"connect to the Mac Mini." Today, that works via mDNS/Bonjour on `.local` for Apple devices,
but fails for everything else.

### The spectrum of home routers

| Router Type | DHCP Server | DDNS Capability | Examples |
|-------------|-------------|-----------------|----------|
| ISP-provided box | Basic DHCP | ❌ None | Singtel, AT&T, BT Home Hub |
| Consumer gaming router | DHCP with some features | ⚠️ Vendor-specific | ASUS, Netgear, TP-Link |
| Prosumer/enterprise | Full DHCP + DDNS | ✅ RFC 2136 | pfSense, Ubiquiti, MikroTik |
| Linux-based (ISC/Kea) | Full DHCP + DDNS | ✅ RFC 2136 | Any Linux box running ISC dhcpd or Kea |

**Gatesentry must work with ALL of these**, not just the ones with DDNS support.

### The current limitation

Gatesentry's internal record system is IP-centric:

```go
// Current model — useless when DHCP changes the IP
type DNSCustomEntry struct {
    IP     string `json:"ip"`     // ← this changes every lease renewal!
    Domain string `json:"domain"` // ← this is what the user actually cares about
}

// Stored as: map[string]string  (domain → single IP, A records only)
internalRecords = make(map[string]string)
```

This means:
- **A records only** — no AAAA (IPv6), no PTR (reverse DNS)
- **Static IPs only** — if DHCP assigns a new IP, the manual entry is stale
- **No auto-discovery** — user must manually enter every device
- **No device concept** — just a domain-to-IP mapping with no identity

---

## The Vision: Automatic Device Discovery

Gatesentry sits as the DNS server for the home network. The router hands out Gatesentry's
IP as the DNS server to every device. This means **every device already talks to Gatesentry**
— it just doesn't know their names yet.

### Five discovery tiers

| Tier | Method | Router Requirement | Automatic? | What you learn |
|------|--------|--------------------|------------|----------------|
| **1** | **RFC 2136 DDNS** | pfSense, Kea, ISC dhcpd, Ubiquiti | ✅ Fully automatic | hostname, A, AAAA, PTR |
| **2** | **mDNS/Bonjour browser** | None (listens on the network) | ✅ Fully automatic | hostname, services, IPs |
| **3** | **Passive DNS query log** | None (Gatesentry already sees queries) | ✅ Fully automatic | client IP, query patterns, first/last seen |
| **4** | **Manual entries** | None (user enters via UI) | ❌ Manual | whatever the user types |

> **Why no DHCP lease file reader?** Gatesentry runs in a Docker container. The DHCP
> server runs on the router or a separate appliance. Reading local lease files from inside
> a container is the wrong model — the files don't exist there. Instead, **Tier 1 (DDNS)
> IS the DHCP integration**: the DHCP server sends RFC 2136 UPDATE messages to Gatesentry
> over the network. This is the standard, RFC-compliant way for DHCP and DNS to
> communicate, and it works regardless of whether they're on the same machine.

**Tier 4 already exists** — that's the `DNSCustomEntry` / `internalRecords` system.

**Tier 3 is basically free** — `handleDNSRequest` receives `w dns.ResponseWriter` which has
`RemoteAddr()`. Every DNS query reveals a device's IP address. The DNS server sees every
device on the network, every few seconds. ARP table lookup can get the MAC.

**Tier 2 requires `--net=host`** — mDNS uses multicast (224.0.0.251:5353) which doesn't
cross Docker's bridge network NAT. With `network_mode: host`, the container shares the
host's network stack and can see multicast traffic. The `bonjour.go` module already imports
`github.com/oleksandr/bonjour`. Adding `Browse()` calls discovers Apple devices, printers,
Chromecasts, and smart speakers automatically. **mDNS is an optional enrichment layer** —
passive discovery + DDNS are the reliable core.

**Tier 1 is the power-user feature** — RFC 2136 Dynamic DNS UPDATE support for users with
capable routers (pfSense, Kea, Ubiquiti, etc.). The DHCP server is configured to send
UPDATE messages to Gatesentry whenever it assigns a lease. **This IS the DHCP integration**
— no lease file parsing, no sidecar containers, just the standard RFC 2136 protocol over
the network.

### All tiers feed one unified store

Every discovery method populates the same device inventory. The DNS query handler answers
from it. The web UI displays it. The source tag tells the user how the device was discovered.

```
                    ┌───────────────────────────┐
                    │     Device Inventory       │
                    │     & Record Store         │
                    │                            │
                    │  device → identity + IPs   │
                    │  name → []DNS records      │
                    └──────┬────────────────────┘
                           │
          ┌────────────────┼────────────────────┐
          │                │                    │
    ┌─────▼──────┐  ┌─────▼──────┐  ┌─────────▼────────┐
    │ DNS Query  │  │   Web UI   │  │  API Endpoints   │
    │  Handler   │  │ "Devices"  │  │ GET /api/devices │
    │            │  │   page     │  │                  │
    └────────────┘  └────────────┘  └──────────────────┘

          Sources (all feed INTO the inventory):

    ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
    │ RFC 2136 │ │  mDNS    │ │ Passive  │ │  Manual  │
    │  DDNS    │ │ Browser  │ │ DNS Log  │ │  (UI)    │
    │          │ │          │ │          │ │          │
    │ Tier 1   │ │ Tier 2   │ │ Tier 3   │ │ Tier 4   │
    └──────────┘ └──────────┘ └──────────┘ └──────────┘
```

---

## The Device Model

### Fundamental shift: Name the device, not the IP

A "device" is a physical thing on the network. It has identities that persist and addresses
that come and go:

```go
type Device struct {
    ID          string    // UUID — stable primary key
    DisplayName string    // User-assigned: "Vivienne's iPad" (or auto-derived)
    DNSName     string    // Sanitized: "viviennes-ipad" (auto-generated from hostname)

    // Identity — how we recognize this device across IP changes
    Hostnames   []string  // DHCP Option 12 hostnames seen
    MDNSNames   []string  // Bonjour service names seen
    MACs        []string  // MAC addresses seen (may change with randomization)

    // Current addresses — DHCP gives these, we DON'T control them
    IPv4        string    // Current IPv4 address
    IPv6        string    // Current IPv6 address (link-local or GUA)

    // Metadata
    Source      string    // "ddns", "mdns", "passive", "manual"
    FirstSeen   time.Time
    LastSeen    time.Time
    Online      bool      // Seen within last N minutes

    // User categorization
    Owner       string    // "Vivienne", "Dad", etc.
    Category    string    // "family", "iot", "guest", etc.

    // Manual overrides
    ManualName  string    // User-assigned name (overrides auto-derived)
    Persistent  bool      // Manual entries survive restart; auto-discovered may not
}
```

### DNS records are DERIVED from the device inventory

When a device's IP changes (DHCP renewal), DNS records update automatically:

```
Device: "Mac Mini" at 192.0.2.100 and 2001:db8::24a

Auto-generated DNS records:
  A     macmini.local          → 192.0.2.100
  AAAA  macmini.local          → 2001:db8::24a
  PTR   100.2.0.192.in-addr.arpa → macmini.local
  PTR   a.4.2.0...ip6.arpa    → macmini.local
```

The enhanced record store replaces the current `map[string]string`:

```go
type InternalRecord struct {
    Name       string    // "macmini.local" or "macmini.jvj28.com"
    Type       uint16    // dns.TypeA, dns.TypeAAAA, dns.TypePTR
    Value      string    // IP address or PTR target
    Source     string    // "ddns", "mdns", "passive", "manual"
    TTL        uint32    // seconds
    DeviceID   string    // Links back to the Device
    LastSeen   time.Time // For expiry
}
```

---

## Handling Names and Dynamic MAC Addresses

### The MAC randomization problem

Modern operating systems increasingly use random MAC addresses:

| OS | Behavior | Impact |
|----|----------|--------|
| iOS 14+ | Random MAC per network by default | Different MAC per Wi-Fi network |
| Android 10+ | Random MAC per network by default | Persists per-network but differs between networks |
| Windows 10/11 | Optional, per-network | When enabled, changes MAC on reconnect |
| macOS | Random MAC in some modes | Sequoia+ has private Wi-Fi options |

This means **MAC address is not a reliable primary identifier** for devices.

### Better identifiers

| Signal | Stability | Coverage |
|--------|-----------|----------|
| DHCP hostname (Option 12) | ✅ Stable | Most devices send their name |
| mDNS/Bonjour name | ✅ Stable | Apple devices, printers, Chromecasts, IoT |
| DHCP client-id (Option 61) | ✅ Stable even with random MAC | Some devices |
| MAC address | ⚠️ May randomize | Universal but increasingly unreliable |
| Client IP + query pattern | ⚠️ Changes on lease renewal | Universal but ephemeral |
| User-assigned name | ✅ Permanent | Manual intervention required |

### Device matching strategy

**Hostname is the primary identifier, not MAC.** The matching priority:

1. **DDNS update arrives** with hostname "macmini" and IP 192.0.2.100 → Find or create
   device by hostname "macmini", update IP
2. **mDNS discovery** finds "Viviennes-iPad" at 192.0.2.42 → Find or create device by
   mDNS name, update IP
3. **Passive DNS** sees queries from 192.0.2.105 → Find device with that IP, or create
   unknown device
4. **MAC changes** — if hostname stays the same but MAC changes, we update the MAC on the
   existing device (hostname is primary key, not MAC)
5. **IP changes** — if hostname stays the same but IP changes, we update the IP and
   regenerate DNS records automatically

---

## Manual Entry Support

### Three modes for users

**Mode A — "Name this device I see" (90% case)**

The user sees an unknown device in the device inventory (discovered passively from its DNS
queries). They click it, type a name. Done. The system already knows its IP and keeps
tracking it. When the IP changes, the DNS records update automatically.

```
UI: Unknown device at 192.0.2.105 (MAC: 94:18:65:5d:b4:f9)
     [Name this device: ________________]  [Save]
```

**Mode B — "Match by hostname pattern"**

User types: Name = "Ring Doorbell", Match = DHCP hostname contains "Ring". Next time any
device with DHCP hostname "Ring-Doorbell-Pro" appears via any discovery method, it gets
auto-named. IP tracked automatically.

**Mode C — "Fixed entry" (legacy, current behavior)**

User types: Name = "nas.local", IP = "192.0.2.200". Static entry. This is what
`DNSCustomEntry` does today — still supported for servers with truly static IPs.

---

## UI: Devices Page

The web UI gets a new "Devices" page showing a network inventory:

| Status | Name | DNS Name | IPv4 | IPv6 | MAC | Via | Last Seen |
|--------|------|----------|------|------|-----|-----|-----------|
| 🟢 | Vivienne's iPad | viviennes-ipad | 192.0.2.42 | fd00::1a3 | c8:5e:... | mDNS + passive | 2 min ago |
| 🟢 | Mac Mini | macmini | 192.0.2.100 | fd00::24a | 3c:22:... | DDNS | 30 sec ago |
| 🟡 | *(click to name)* | — | 192.0.2.105 | — | 94:18:... | passive | 3 hrs ago |
| ⚫ | Dad's Printer | printer | — | — | e4:11:... | manual | 3 days ago |

Status indicators:
- 🟢 Online — seen within last 5 minutes
- 🟡 Unknown — seen but unnamed
- ⚫ Offline — not seen recently

Clicking any device opens a detail panel with full identity history, all IPs seen, all MACs
seen, all hostnames seen, and the option to name/rename/categorize.

---

## Existing Codebase Assessment

### What already exists

| Component | File(s) | Status |
|-----------|---------|--------|
| DNS server (UDP + TCP) | `dns/server/server.go` | ✅ Working (our bug fixes) |
| Internal record lookup | `dns/filter/internal-records.go` | ✅ Working (A records only) |
| Blocklist system | `dns/filter/domains.go` | ✅ Working |
| Exception domains | `dns/filter/exception-records.go` | ⚠️ Stub (commented out) |
| Periodic refresh | `dns/scheduler/scheduler.go` | ✅ Working |
| Block page HTTP server | `dns/http/` | ✅ Working |
| Settings persistence | `storage/` | ✅ Working (BuntDB-backed) |
| DNS custom entry type | `types/dns.go` | ✅ Working (but limited) |
| Bonjour advertising | `bonjour.go` | ✅ Working (advertise only) |
| Web UI DNS page | `ui/src/routes/dns/` | ✅ Working (manual entries) |
| `miekg/dns` library | `go.mod` | ✅ v1.1.43 (has TSIG, UPDATE, all record types) |
| `oleksandr/bonjour` | `go.mod` | ✅ Available (has Browse()) |

### What needs building

| Component | Location | Priority |
|-----------|----------|----------|
| Device data model | `dns/discovery/types.go` | Phase 1 ✅ |
| Enhanced record store | `dns/discovery/store.go` | Phase 1 ✅ |
| Query handler upgrade (AAAA, PTR) | `dns/server/server.go` | Phase 1 ✅ |
| Passive discovery (DNS query tracking) | `dns/server/server.go` | Phase 2 ✅ |
| mDNS/Bonjour browser | `dns/discovery/mdns.go` | Phase 3 ✅ |
| RFC 2136 UPDATE handler | `dns/server/ddns.go` | Phase 4 ✅ |
| TSIG authentication | `dns/server/ddns.go` | Phase 4 ✅ |
| Docker deployment | `Dockerfile`, `docker-compose.yml` | Phase 5 ✅ |
| UI Devices page | `ui/src/routes/devices/` | Phase 6 ✅ |
| API endpoints | `webserver/endpoints/handler_devices.go` | Phase 6 ✅ |

---

## DNS Request Flow — Before and After

### Current flow

```
DNS query arrives
  → Is it blocked? → NXDOMAIN + CNAME to blocked.local
  → Is it in internalRecords? → Return A record
  → Otherwise → Forward to external resolver (8.8.8.8)
```

### Enhanced flow

```
DNS query arrives
  → Record client IP for passive discovery (Tier 4)
  → Check opcode:
      → OpcodeUpdate? → TSIG verify → Apply to device inventory → NOERROR
      → OpcodeQuery?
          → Is it blocked? → NXDOMAIN + CNAME to blocked.local
          → Is it in device inventory? → Return A, AAAA, or PTR as appropriate
          → Is it in legacy internalRecords? → Return A record (backward compat)
          → Otherwise → Forward to external resolver
```

---

## Implementation Phases

### Phase 1: Foundation — Enhanced Record Store ✅

- `dns/discovery/types.go` — Device and InternalRecord types
- `dns/discovery/store.go` — Thread-safe device + record store with RWMutex
- Update `handleDNSRequest` to answer AAAA and PTR queries from the store
- Backward compatibility: existing `DNSCustomEntry` entries still work
- Persistence: device inventory saved to BuntDB

### Phase 2: Passive Discovery ✅

- Extract client IP from `w.RemoteAddr()` in `handleDNSRequest`
- ARP table lookup for MAC (`ip neigh` / `arp -a`)
- Create/update unknown devices on every query
- Track first seen / last seen / online status
- Zero configuration required — works with any router

### Phase 3: mDNS/Bonjour Browser ✅

- Add `Browse()` calls for common service types (_http._tcp, _airplay._tcp, etc.)
- Correlate mDNS names with existing devices (by IP or MAC)
- Run as background goroutine with configurable interval
- Auto-names Apple devices, printers, Chromecasts, smart speakers
- **Requires `--net=host` Docker networking** for multicast visibility

### Phase 4: RFC 2136 DDNS Handler ✅

- Add opcode dispatch in `handleDNSRequest`
- Implement UPDATE message processing (prerequisite + update sections)
- TSIG key configuration and verification (miekg/dns has built-in support)
- Accept A, AAAA, PTR updates from DHCP servers
- Correlate DDNS hostnames with existing devices in inventory
- **This IS the DHCP integration** — no lease file parsing needed

### ~~Phase 5: DHCP Lease File Reader~~ — DROPPED

> This phase was dropped during the Docker deployment architecture review. GateSentry
> runs in a Docker container; the DHCP server runs on the router or a separate appliance.
> Reading local lease files from inside a container is the wrong model. Phase 4 (RFC 2136
> DDNS) provides the correct, network-based integration: the DHCP server sends UPDATE
> messages to GateSentry over the wire, exactly as RFC 2136 intended.

### Phase 5: Docker Deployment ✅

- Runtime-only Dockerfile (Alpine + pre-built binary, ~30MB)
- `build.sh` handles full pipeline: Svelte UI → embed into Go → static binary
- `docker-compose.yml` with `network_mode: host`
- `.dockerignore` for minimal build context
- Deployment documentation (`DOCKER_DEPLOYMENT.md`)
- Environment variable configuration (TZ, debug logging, scan limits)

### Phase 6: UI — Devices Page ✅

- New Svelte route `/devices`
- DataTable with device inventory (Carbon Design System components)
- Online/offline status indicators
- Click-to-name for unknown devices
- Device detail panel (identity history, all IPs/MACs seen)
- API endpoints: `GET /api/devices`, `GET /api/devices/{id}`, `POST /api/devices/{id}/name`, `DELETE /api/devices/{id}`
- Side navigation menu entry
- Go handler: `webserver/endpoints/handler_devices.go`
- Svelte components: `devices.svelte`, `devicelist.svelte`, `devicedetail.svelte`

---

## Docker Deployment Architecture

### Why `--net=host`?

GateSentry is a network infrastructure service — it needs to be a first-class citizen on
the network, not hidden behind Docker's NAT. Like Pi-Hole, it uses host networking:

| Requirement | Bridge Mode | Host Mode |
|-------------|-------------|-----------|
| Bind to port 53 (DNS) | ⚠️ Works but hides client IPs | ✅ Sees real source IPs |
| mDNS multicast (224.0.0.251) | ❌ Multicast doesn't cross NAT | ✅ Full multicast visibility |
| RFC 2136 DDNS from router | ⚠️ Router must target Docker host IP | ✅ Router targets GateSentry directly |
| Passive discovery (client IP tracking) | ❌ All clients appear as 172.17.0.1 | ✅ Real client IPs visible |
| Port conflicts | ✅ Isolated | ⚠️ Must not conflict with host services |

**Host networking is not optional** for a DNS server that needs to know who is asking.
Without it, passive discovery (Tier 3) sees only Docker's gateway IP, and per-device
filtering policies become impossible.

### Container architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Host Network Stack                    │
│                                                         │
│  :53 (DNS)  :80 (Web UI)  :10413 (proxy)                   │
│      │          │           │               │           │
│  ┌───┴──────────┴───────────┴───────────────┴────────┐  │
│  │              GateSentry Container                  │  │
│  │                                                    │  │
│  │  /usr/local/gatesentry/gatesentry-bin  (binary)   │  │
│  │  /usr/local/gatesentry/gatesentry/     (data vol) │  │
│  │      ├── settings.db  (BuntDB)                    │  │
│  │      ├── devices.db   (device inventory)          │  │
│  │      ├── logs/                                    │  │
│  │      └── certs/       (MITM CA if enabled)        │  │
│  └────────────────────────────────────────────────────┘  │
│                                                         │
│  :5353 (mDNS multicast) ←── optional, auto-discovery   │
└─────────────────────────────────────────────────────────┘
                          │
              ┌───────────┴───────────┐
              │    Home Network       │
              │                       │
              │  Router/DHCP Server   │──── RFC 2136 DDNS UPDATEs ──→ :53
              │  Phones, Laptops      │──── DNS queries ──→ :53
              │  IoT, Printers        │──── mDNS announcements ──→ :5353
              └───────────────────────┘
```

### Build pipeline

The build happens entirely on the host — the Docker image is runtime-only:

1. **`build.sh`** builds the Svelte UI (`cd ui && npm run build`)
2. Copies `ui/dist/*` into `application/webserver/frontend/files/` (the `//go:embed` dir)
3. Builds the Go binary — all frontend assets are embedded at compile time
4. **Dockerfile** copies the single binary into Alpine (~30MB final image)

No Node toolchain, no Go toolchain, no build dependencies in the container.
The Go binary is fully self-contained — the Svelte UI, filter data, and block page
assets are all embedded at compile time. The only external state is the mounted data
volume for settings, device database, and logs.

### Deployment

```bash
# Build and start
docker compose up -d --build

# View logs
docker compose logs -f gatesentry

# Rebuild after code changes
docker compose up -d --build

# Stop
docker compose down
```

See `DOCKER_DEPLOYMENT.md` for complete deployment instructions including DHCP server
configuration for DDNS integration.

---

## DDNS Protocol Details (Tier 1)

### RFC 2136 Dynamic DNS UPDATE

The `miekg/dns` library v1.1.43 already provides all primitives:
- `dns.OpcodeUpdate` — opcode constant
- `dns.Msg` with `Ns` section for UPDATE resource records
- `dns.TsigSecret` map on the server for TSIG verification
- Full TSIG support (HMAC-MD5, HMAC-SHA256, etc.)

### What a DDNS UPDATE looks like

When KEA or ISC dhcpd assigns a lease, it sends:

```
;; HEADER: opcode=UPDATE, status=NOERROR
;; ZONE SECTION:
;; local.    IN    SOA

;; PREREQUISITE SECTION:
;; (empty or conditions)

;; UPDATE SECTION:
;; macmini.local.  300  IN  A     192.0.2.100
;; macmini.local.  300  IN  AAAA  2001:db8::24a
```

Gatesentry receives this, verifies the TSIG signature, and updates the device inventory.
The device "macmini" now resolves. When the lease renews with a new IP, another UPDATE
arrives and the records are refreshed.

### TSIG Configuration

```yaml
# gatesentry.yaml (or via UI settings)
ddns:
  enabled: true
  zone: "local"
  tsig_keys:
    - name: "dhcp-key"
      algorithm: "hmac-sha256"
      secret: "base64-encoded-secret"
```

---

## Domain/Zone Strategy

### Recommended defaults

| Zone | Purpose | Source |
|------|---------|--------|
| `.local` | mDNS-compatible local names | Auto-discovery |
| `<user-configured>.lan` | LAN-specific zone | DDNS / lease reader |
| User's domain (e.g., `jvj28.com`) | Split-horizon internal view | Manual / DDNS |

### Split-horizon DNS

Users like the author have a public domain (`jvj28.com`) hosted externally (e.g., CloudNS).
Gatesentry provides the **internal view** — devices on the LAN resolve to local IPs:

```
External (CloudNS):  jvj28.com → public IP (VPN, web, etc.)
Internal (Gatesentry): macmini.jvj28.com → 192.0.2.100

Query from LAN client → Gatesentry answers from device inventory
Query from internet → CloudNS answers from public zone
```

This is NOT the same as being an authoritative server for the internet. Gatesentry only
needs to be authoritative **for its local clients**.

---

## Security Considerations

### TSIG for DDNS

DDNS updates MUST be authenticated. Without TSIG, any device on the network could inject
DNS records — a trivial attack vector. The `miekg/dns` library provides robust TSIG support.

### Scope limitation

Gatesentry should only accept DDNS updates for its configured local zones. It must NOT
accept updates for external domains — that would make it an open DNS update relay.

### Passive discovery privacy

Passive DNS query logging reveals every website every device visits. This data should be
handled carefully:
- Device IP → name correlation: stored locally only
- Query content: already logged by the existing logger
- No external transmission of passive discovery data

---

## Compatibility with Existing Features

### Backward compatibility

The existing `DNSCustomEntry` system (`GET/POST /api/dns/custom_entries`) continues to work.
Manual entries are treated as Mode C devices (fixed name + fixed IP). They appear in the
device inventory with `source: "manual"`.

### Parental controls integration

Gatesentry's core purpose is parental controls — protecting children from inappropriate
content. The device discovery system is a critical enabler for **per-device filtering
policies**, but this branch intentionally does NOT implement the policy engine.

#### Current state: Global blocklists

Today, the blocklist system is global — a blocked domain is blocked for ALL devices. The
DNS handler has no concept of "who is asking" — it only sees the domain being queried.

#### Future state: Per-device/per-group filtering

With the device store in place, the DNS handler gains the ability to identify the
querying device:

```
DNS query arrives from 192.0.2.42
  → DeviceStore.FindDeviceByIP("192.0.2.42") → "Vivienne's iPad"
  → Device.Category = "kids"  (or Device.Groups = ["kids", "family"])
  → Apply "kids" filtering policy (stricter blocklists, time restrictions)
```

The existing `Rule` system (`types/rule.go`) already has:
- `Users []string` — maps to device Owner
- `TimeRestriction` — bedtime enforcement
- `RuleAction` — allow/block per domain

The missing piece today is: **query source IP → device → group → policy**.
The device store provides the first two links in that chain.

#### Design decisions for this branch

| Decision | Rationale |
|----------|----------|
| `Category string` not `Groups []string` | Simple for now. Can migrate to slice later; JSON deserialization handles both. |
| `Owner string` stays a plain string | Maps to Rule.Users. No need for a User type yet. |
| No `PolicyID` or `FilterProfile` on Device | Policy assignment is a separate concern. Don't couple it to the discovery model. |
| `FindDeviceByIP()` is a fast map lookup | This is the hot path — called on every DNS query once per-device filtering exists. |
| Store has no filtering logic | The store is pure data. Filtering decisions belong in the handler or a policy engine. |

#### Migration path (future branch)

When per-device filtering is implemented:
1. Add a `FilterPolicy` type (name, blocklists, time rules, allowed overrides)
2. Add a `deviceGroups` map in the settings store (group name → FilterPolicy ID)
3. In `handleDNSRequest`: after device lookup, resolve group → policy → check domain
4. `Category string` may evolve to `Groups []string` — backward-compatible via JSON
5. The global blocklist becomes the "default" policy for ungrouped devices

This is a separate feature branch. The device store is designed to support it without
modification.

### UI integration

The existing DNS page continues to work. The new Devices page is additive. The DNS "Custom
A Records" section could eventually link to the device inventory, showing that manual
entries are a subset of the larger system.

---

## Related Projects

- **[unbound-dhcp](https://github.com/jbarwick/unbound-dhcp)** — Python module for Unbound
  that reads DHCP lease files directly. Proved the lease-reading concept, but the approach
  doesn't apply to Docker-deployed GateSentry. The RFC 2136 DDNS approach (Phase 4) is the
  correct network-based alternative.
- **DDNS server prototype** (`/home/jbarwick/Development/DDNS`) — Python-based RFC 2136
  DDNS server. Proved the protocol handling concept. The Go implementation in Phase 4
  supersedes this prototype.
- **Gatesentry PR #135** — DNS server concurrency fixes (data races, TCP support, IPv6).
  This feature builds on top of those fixes.

---

## Open Questions

1. **Default zone name** — Should Gatesentry default to `.local` (mDNS-compatible) or
   `.lan` (avoids mDNS conflicts)?
2. **Device expiry** — How long before an offline device is removed from the inventory?
   Or never (keep history)?
3. **Hostname conflicts** — Two devices with the same DHCP hostname? Last-writer-wins?
   Append MAC suffix?
4. **IPv6 scope** — Track link-local addresses? Only GUA/ULA? Both?
5. **ARP access** — Passive discovery needs ARP table access. Works on Linux/FreeBSD,
   may need elevated privileges.
6. **mDNS port conflict** — If another mDNS responder runs on port 5353, Bonjour browsing
   may conflict. Need graceful handling.
