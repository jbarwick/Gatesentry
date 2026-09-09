# GateSentry

An open-source DNS filter, HTTPS-inspecting proxy, and parental-control appliance with a web admin UI.

**This is the v2 branch** (`2.0.0-beta.5`), a fork of [fifthsegment/Gatesentry](https://github.com/fifthsegment/Gatesentry). Filtering is rule-based (Allow or Block, per user) rather than a pile of global lists. Pre-release: APIs and settings may still change.

[Release notes](https://github.com/jbarwick/Gatesentry/releases/tag/v2.0.0-beta.3) · [RFC to upstream](https://github.com/fifthsegment/Gatesentry/pull/141)

![gatesentry-repo](https://github.com/fifthsegment/Gatesentry/assets/5513549/5ab836ab-7362-4916-9f7c-655e67e4deab)

Typical uses: ad / tracker blocking, parental controls, bandwidth saving, phishing-domain blocking, work-hours access control, request logging, and local DNS redirects.

---

## Requirements

| What | Version | Notes |
|------|---------|--------|
| **Go** | 1.24+ | Matches `go.mod` |
| **Node.js** | 18+ | Builds the Svelte admin UI |
| **Docker Engine + Compose v2** | optional | Recommended way to run it |
| **Linux** | — | Primary target (transparent proxy and TPROXY are Linux-only) |

The Docker image is **runtime-only**: it copies a binary you already built with `./build.sh`. There is no pre-built `2.0.0-beta.5` image on Docker Hub yet (`jbarwick/gatesentry:latest` is still `2.0.0-alpha.15`).

---

## Install from source (Docker)

This is the path for running GateSentry on your own server.

```bash
git clone https://github.com/jbarwick/Gatesentry.git
cd Gatesentry
git checkout v2.0.0-beta.3   # or: git checkout v2

# First time only — UI toolchain
cd ui && npm install && cd ..

# Build Svelte UI, embed it, compile a static Go binary → bin/gatesentrybin
./build.sh

# Start the container (bridged ports, no root DNS port)
docker compose up -d --build
# older engines / Synology: docker-compose up -d --build
```

Admin UI: **http://localhost:8080/**  
Default login: **`admin` / `admin`** — change this immediately.

DNS on this first-run compose is **UDP/TCP 10053** (so it does not fight systemd-resolved on 53). Point a test client at it with:

```bash
dig @127.0.0.1 -p 10053 example.com
```

Persistent data lives in `./data/` on the host (settings, logs, CA cert, device inventory). Back that directory up.

Stop:

```bash
docker compose down
```

---

## Production: Linux host network

For a LAN DNS/proxy appliance you want **host networking**: real client IPs, mDNS, and DNS on port 53. Use the sample in [`docker-compose.host.yml`](docker-compose.host.yml).

1. Free port 53 if `systemd-resolved` owns it (see below).
2. Edit the file: set `TZ`, and optionally `GS_ADMIN_PORT` / `GS_BASE_PATH`.
3. **Do not** set `GATESENTRY_DNS_RESOLVER` unless you intend to overwrite the stored resolver on every start.

```bash
./build.sh
docker compose -f docker-compose.host.yml up -d --build
```

Then:

| URL / check | |
|-------------|-|
| Admin UI | http://\<server-ip\>:8080/ |
| DNS | `dig @<server-ip> -p 53 example.com` |
| Proxy | `\<server-ip\>:10413` |

Point the router’s DHCP **DNS server** at this host. Devices pick it up on the next lease renew (or after reconnecting Wi-Fi).

### Freeing port 53 (`systemd-resolved`)

```bash
sudo mkdir -p /etc/systemd/resolved.conf.d
echo -e '[Resolve]\nDNSStubListener=no' | sudo tee /etc/systemd/resolved.conf.d/gatesentry.conf
sudo systemctl restart systemd-resolved
```

Keep a recursive resolver on the LAN (router, Unbound, etc.) and set that as GateSentry’s upstream in **DNS settings** after login — not as `GATESENTRY_DNS_RESOLVER` in compose, unless you want the env var to win every boot.

---

## Run the binary without Docker

Same build as above, then start from `bin/` (working directory matters; data is `bin/gatesentry/`):

```bash
./build.sh
cd bin
../run.sh
```

Or:

```bash
cd bin
GS_ADMIN_PORT=8080 GATESENTRY_DNS_PORT=10053 ./gatesentrybin
```

Optional Linux service (from the `bin/` directory):

```bash
./gatesentrybin -service install
sudo service gatesentry start   # name depends on the OS service manager
```

`run.sh` / `restart.sh` default `GATESENTRY_DNS_RESOLVER` to `192.0.2.1:53` for this developer’s LAN. On your server, either unset it or point it at **your** recursive DNS. A non-empty value **overwrites** the stored `dns_resolver` setting on startup.

---

## Ports

Code defaults vs the samples in this repo:

| Service | Code default | Bridged sample (`docker-compose.yml`) | Host sample (`docker-compose.host.yml`) | Env |
|---------|--------------|----------------------------------------|------------------------------------------|-----|
| Admin UI | **80** | **8080** | **8080** | `GS_ADMIN_PORT` |
| Admin URL prefix | `/gatesentry` | `/` (root) | `/` (root) | `GS_BASE_PATH` |
| DNS | **53** | **10053** | **53** | `GATESENTRY_DNS_PORT` |
| Explicit proxy | 10413 | 10413 | 10413 | (compiled default) |
| Transparent proxy | 10414 | 10414 | 10414 | `GS_TRANSPARENT_PROXY_PORT` |
| mDNS / Bonjour | 5353/udp | 5353/udp | host stack | — |

With `GS_BASE_PATH=/` the UI is `http://host:port/`. With the code default `/gatesentry` it is `http://host:port/gatesentry/`. Metrics are on the **same** HTTP listener: `http://host:port/metrics` (or `…/gatesentry/metrics` if you keep the prefix).

Open the firewall for DNS (53 or 10053), admin, and 10413.

---

## Environment variables

| Variable | Default | Meaning |
|----------|---------|---------|
| `GS_ADMIN_PORT` | `80` | Admin HTTP port. Samples use `8080` so you do not need a privileged port. |
| `GS_BASE_PATH` | `/gatesentry` | URL prefix. Set `/` for `http://host:port/`. |
| `GATESENTRY_DNS_PORT` | `53` | DNS UDP+TCP port. |
| `GATESENTRY_DNS_ADDR` | `0.0.0.0` | Bind address. Comma-separated for dual-stack, e.g. `0.0.0.0,::`. **Quote it in YAML** (`"GATESENTRY_DNS_ADDR=0.0.0.0,::"`) or `::` is parsed as a nested mapping. If you only bind IPv4, the server also starts an extra `::` listener. |
| `GATESENTRY_DNS_RESOLVER` | unset | If **set**, overwrites stored `dns_resolver` on every start. Leave unset and configure the IPv4 upstream in the UI (default **`8.8.8.8:53`**). |
| `GATESENTRY_DNS_RESOLVER_IPV6` | unset | Same overwrite behaviour for `dns_resolver_ipv6`. AAAA / HTTPS / ip6.arpa use this upstream. Set it in the UI to your recursive IPv6 DNS (for example `[2001:4860:4860::8888]:53`). |
| `TZ` | `UTC` | IANA timezone for time-based rules (e.g. `Asia/Singapore`, `America/New_York`). |
| `GS_MAX_SCAN_SIZE_MB` | `2` | Max response body scanned for keywords (MB). |
| `GS_TRANSPARENT_PROXY` | `true` on Linux | Set `false` to disable the transparent listener. |
| `GS_TRANSPARENT_PROXY_PORT` | `10414` | Transparent proxy port. |
| `GS_DEBUG_LOGGING` | unset | `true` enables verbose proxy logs. |

---

## Sample Compose files

- [`docker-compose.yml`](docker-compose.yml) — first run, bridged ports (8080 / 10053 / 10413).
- [`docker-compose.host.yml`](docker-compose.host.yml) — Linux production, `network_mode: host`, DNS on 53.

Both expect `./build.sh` to have produced `bin/gatesentrybin` before `docker compose up --build`.

Copy either file to the server, change `TZ` and ports, and keep the `./data` (or a named volume) mount. The NAS-specific overlay used in this project’s lab is **not** what you should copy; these two files are the public samples.

---

## After it is running

1. Log in, change `admin` / `admin`.
2. **DNS → resolver** — IPv4 and IPv6 upstreams that actually exist on your network.
3. Point DHCP DNS at GateSentry (production) or test with `dig @…`.
4. Optional: create Domain Lists and rules (Allow/Block, per user). HTTPS URL/keyword/content-type matching needs MITM enabled on the rule (install the generated CA on clients).
5. Optional: set the router’s HTTP proxy / WPAD to port 10413.

---

## Transparent proxy (Linux only)

Enabled automatically on Linux (`SO_ORIGINAL_DST` / `IP_TRANSPARENT`). Disable with `GS_TRANSPARENT_PROXY=false`.

Local REDIRECT:

```bash
iptables -t nat -A PREROUTING -p tcp --dport 80 -j REDIRECT --to-port 10414
iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port 10414
```

Forwarded TPROXY (router / Tailscale exit node):

```bash
iptables -t mangle -A PREROUTING -p tcp --dport 80 -j TPROXY --tproxy-mark 0x1/0x1 --on-port 10414
iptables -t mangle -A PREROUTING -p tcp --dport 443 -j TPROXY --tproxy-mark 0x1/0x1 --on-port 10414
ip rule add fwmark 1 lookup 100
ip route add local 0.0.0.0/0 dev lo table 100
```

Needs root or `CAP_NET_ADMIN`, and the GateSentry CA on clients for HTTPS inspection.

---

## Local development (no Docker)

```bash
cd ui && npm install && cd ..
./build.sh
./run.sh            # starts bin/gatesentrybin; logs → log.txt
./restart.sh        # restart without rebuilding
./run.sh --build    # rebuild then start
```

Admin: http://localhost:8080/ (via `run.sh`’s `GS_ADMIN_PORT=8080`). Tests: `make tests`. Lint: `make lint`.

---

## v2 in brief

- **Rules, not global lists** — each rule matches users / domains / URL / content-type / keywords, then Allow or Block. First match wins (lower priority number wins).
- **Domain Lists** — shared between DNS and proxy; URL-sourced lists (StevenBlack, Hagezi, …) plus local lists.
- **Device discovery** — passive DNS, mDNS/Bonjour, RFC 2136 DDNS.
- **WPAD / PAC**, dual-stack DNS, per-rule MITM.

More detail: [v2.0.0-beta.3 release](https://github.com/jbarwick/Gatesentry/releases/tag/v2.0.0-beta.3) and [DOCKER_DEPLOYMENT.md](DOCKER_DEPLOYMENT.md) (router DHCP / reverse-proxy notes; some port numbers there still describe an older “admin on :80” layout — prefer this README and the compose files).
