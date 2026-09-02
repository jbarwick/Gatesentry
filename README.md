# Gatesentry

An open source proxy server (supports SSL filtering / MITM) + DNS Server with a nice frontend.

**This checkout is the v2 branch**, not v1 / 1.2. Development and image builds use **`2.0.0-beta.1`**. The last production container on monster-jj was `2.0.0-alpha.15` until this beta is published.

![Codecov](https://codecov.io/gh/fifthsegment/Gatesentry/branch/master/graph/badge.svg)


[Download the latest release](https://github.com/fifthsegment/Gatesentry/releases)

## This repo vs `../gatesentry-synology`

There are two directories next to each other. They are not two versions of the same app.

| Path | What it is | Role |
|------|------------|------|
| **`Gatesentry` (this repo, branch `v2`)** | Go + Svelte application, Dockerfile, tests, `docker-publish.sh` | **Source of truth.** Build images here. All v2 work happens here. |
| **`../gatesentry-synology`** | A single `docker-compose.yml` for the Synology NAS | **Deploy overlay only** (host networking, NAS volume path, port 53, admin port). It does not contain application code. |

**Use this project** for code, builds, and publishing. **Use a Synology compose file** only to run the published image on monster-jj.

`../gatesentry-synology/docker-compose.yml` is kept in sync with the NAS layout and currently pins **`gatesentry:2.0.0-beta.1`**. The **live** compose on the NAS is `/volume1/docker/Gatesentry/docker-compose.yml` (last running image was `2.0.0-alpha.15` until this beta is published). Typical env:

- `network_mode: host`
- volume `/volume1/docker/Gatesentry/gatesentry`
- `GS_ADMIN_PORT=9876`, `GS_BASE_PATH=/gatesentry`, `GATESENTRY_DNS_PORT=53`, `TZ=Asia/Singapore`

Do not deploy from this repo's root `docker-compose.yml` onto the NAS (it uses bridge ports and admin 8080). Do not start the stale `1.20.6.1` overlay. After a new v2 image is published, bump the image tag in the NAS compose (and sync `../gatesentry-synology` so it matches).

Production admin UI: **`http://monster-jj:9876/gatesentry/`** (metrics: `http://monster-jj:9876/metrics`). Do not use `http://monster-jj.jvj28.com` — that hostname is HTTPS-redirected by the NAS web server and never reaches GateSentry. When DNS is unhealthy, use `http://192.168.1.91:9876/gatesentry/` and bypass the proxy.

Usages:

- Privacy Protection: Users can use Gatesentry to prevent tracking by various online services by blocking tracking scripts and cookies.

- Parental Controls: Parents can configure Gatesentry to block inappropriate content or websites for younger users on the network.

- Bandwidth Management: By blocking unnecessary content like ads or heavy scripts, users can save on bandwidth, which is especially useful for limited data plans.

- Enhanced Security: Gatesentry can be used to block known malicious websites or phishing domains, adding an extra layer of security to the network.

- Access Control: In a corporate or institutional setting, Gatesentry can be used to restrict access to non-work-related sites during work hours.

- Logging and Monitoring: Track and monitor all the requests made in the network to keep an eye on suspicious activities or to analyze network usage patterns.

- Custom Redirects (via DNS): Redirect specific URLs to other addresses, useful for local development or for redirecting deprecated domains.

![gatesentry-repo](https://github.com/fifthsegment/Gatesentry/assets/5513549/5ab836ab-7362-4916-9f7c-655e67e4deab)

## Getting started

There are 2 ways to run Gatesentry, either using the docker image or using the single file binary directly.

### Method 1: Using Docker

1. Use the [docker-compose.yml](https://github.com/fifthsegment/Gatesentry/blob/master/docker-compose.yml) file from the root of this repo as a template, copy and paste it to any directory on your computer, then run the following command in a terminal `docker compose up`

### Method 2: Using the Gatesentry binary directly

1.  Downloading Gatesentry:

    Navigate to the 'Releases' section of this repository.
    Identify and download the appropriate file for your operating system, named either gatesentry-linux or gatesentry-mac.

2.  Installation:

    **For macOS and Linux:**

    Locate the downloaded Gatesentry binary file in your system.
    Open a terminal window and navigate to the directory containing the downloaded binary.
    Run the following command to grant execution permissions to the binary file:

        chmod +x gatesentry-{platform}

    Replace `{platform}` with your operating system (linux or mac).
    Proceed to execute the binary file to initiate the server.

    **Running as a Service (Optional)**

    If you want Gatesentry to keep running in the background on your machine, install it as :

    `./gatesentry-{platform} -service install`

    Next, on linux you can use your system service runner to start or stop it, for example for ubuntu:

    `service gatesentry start   #starts the service`

    `service gatesentry stop    #stops the service`

    **For Windows**

    The installer (GatesentrySetup.exe) contains instructions.

    **Running as a Service**

    The installer (GatesentrySetup.exe) should automatically install a service. You can look for it by searching for gatesentry in your Service manager (open it by running `services.msc`)

3.  Launching the Server:

    Execute the Gatesentry binary file to start the server.
    Upon successful launch, the server will begin listening for incoming connections on port 10413.

## Important information

### Ports

By default Gatesentry uses the following ports

| Port  | Purpose                                              |
| ----- | ---------------------------------------------------- |
| 10413 | For proxy (explicit mode)                            |
| 10414 | For proxy (transparent mode, optional)               |
| 80    | For the web based administration panel               |
| 53    | For the built-in DNS server                          |
| 80    | For the built-in webserver (showing DNS block pages) |

### Accessing the User Interface:

Open a modern web browser of your choice.
Enter the following URL in the address bar: http://localhost
The Gatesentry User Interface will load, providing access to various functionalities and settings.

### Default Login Credentials:

    Username: admin
    Password: admin

Use the above credentials to log in to the Gatesentry system for the first time. For security reasons, it is highly recommended to change the default password after the initial login.

Note: Ensure your system's firewall and security settings allow traffic on ports 53 (DNS), 8080 (admin UI), and 10413 (proxy) to ensure seamless operation and access to the Gatesentry server and user interface.

This guide now specifically refers to the Gatesentry software and uses the `gatesentry-{platform}` filename convention for clarity.

### DNS Information

Gatesentry ships with a built in DNS server which can be used to block domains.
The resolver used for forwarding requests can now be configured via the
application settings ("dns_resolver"). It defaults to Google DNS
(`8.8.8.8:53`).

## Transparent Proxy Mode (Linux only)

GateSentry automatically enables transparent proxy mode on Linux systems. This allows traffic interception without client configuration using Linux's `SO_ORIGINAL_DST` socket option and `IP_TRANSPARENT` socket support for TPROXY.

### Setup for Local Traffic (REDIRECT mode)

For traffic originating from the local machine:

```bash
iptables -t nat -A PREROUTING -p tcp --dport 80 -j REDIRECT --to-port 10414
iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port 10414
```

### Setup for Forwarded Traffic (TPROXY mode)

For traffic forwarded through the machine (e.g., Tailscale exit node, router):

```bash
# Mark traffic for routing
iptables -t mangle -A PREROUTING -p tcp --dport 80 -j TPROXY --tproxy-mark 0x1/0x1 --on-port 10414
iptables -t mangle -A PREROUTING -p tcp --dport 443 -j TPROXY --tproxy-mark 0x1/0x1 --on-port 10414

# Route marked traffic locally
ip rule add fwmark 1 lookup 100
ip route add local 0.0.0.0/0 dev lo table 100
```

### Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `GS_TRANSPARENT_PROXY_PORT` | Port for transparent proxy | `10414` |
| `GS_TRANSPARENT_PROXY` | Set to `false` to disable | `true` on Linux |

### Requirements

- Linux with `SO_ORIGINAL_DST` and `IP_TRANSPARENT` support
- Root or CAP_NET_ADMIN privileges
- CA certificate installed on clients for HTTPS interception

### Features

- Supports both REDIRECT (local) and TPROXY (forwarded) traffic
- Auto-starts on Linux with graceful fallback
- Protocol auto-detection (HTTP vs HTTPS)
- SSL Bump support for HTTPS filtering
- All existing filters work in transparent mode

## Local Development

`./setup.sh`

To run it:

`./run.sh`
