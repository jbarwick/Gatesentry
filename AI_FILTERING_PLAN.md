# GateSentry AI Image Filtering — Design Plan (Alpha)

## Status: OPEN FOR DECISION (v2 constraints locked — see §3.4, §10, §11, §13)

The admin page at `/gatesentry/ai` now stores a **provider mode** (`disabled` / `grok` / `chatgpt`) and **API keys**. This document is the design debate for *how* those providers should run in the proxy. It is not yet an implementation spec for the scanner itself.

---

## 1. Problem

A family-LAN MITM proxy can see image bytes (when HTTPS inspection is on). We want a vision model (Grok or ChatGPT) to classify NSFW images and block them.

Vision APIs are **slow and expensive** relative to a proxy hop:

| Path | Typical latency | What the user sees |
|------|-----------------|--------------------|
| Current image path (peek 4KB, stream rest) | milliseconds | Page images appear immediately |
| Local NSFW classifier HTTP POST (legacy) | tens–hundreds of ms, same host | Noticeable but sometimes tolerable |
| Grok / OpenAI vision, full image upload | **~0.5–5+ seconds** per image | Every `<img>` stalls; pages feel broken |

A single news homepage can request **50–150 images**. Real-time blocking at API latency is not a product.

Three product issues, in order of impact (full debate in §11):

**(a)** First retrieval: deliver to the browser, queue AI, block later via the URL list.  
**(b)** First retrieval: stall the response until AI finishes.  
**(3)** Almost all images are appropriate — how do we **not** scan every GET to the internet?

(a) vs (b) is latency. **(3) is cost and availability.** Scanning every image is not a product, even with (a).

That verdict store is **not** an HTTP content cache. GateSentry does not store origin bodies today (verified). `Cache-Control: no-cache` / `no-store` must not disable classification. See §2.5 and §3.

---

## 2. What the proxy actually does today

### 2.1 Three response pipelines (`gatesentryproxy/proxy.go`)

| Path | Content-Type | Behaviour |
|------|----------------|-----------|
| **A stream** | JS, CSS, fonts, JSON, downloads | Zero-copy stream. No body scan. |
| **B peek** | `image/*`, `video/*`, `audio/*` | Read **first 4KB** (`PeekSize`), run `ContentHandler`, then stream the rest. |
| **C buffer** | `text/html`, empty type | Buffer up to `GS_MAX_SCAN_SIZE_MB` (default 2MB), then `ScanMedia` / `ScanText`. |

Images take **Path B**. The handler receives **4KB**, not the full file.

### 2.2 Legacy AI scanner (`application/filters/filter-images-ai.go`)

`FilterImagesAI` POSTs a multipart file to `ai_scanner_url` (a local NudeNet-style classifier: classes like `FEMALE_BREAST_EXPOSED`). It **skips bodies smaller than 6000 bytes**.

Consequence: on Path B, `len(content) == 4096 < 6000`, so **the legacy scanner never classifies typical images**. It can only fire if an image was mislabeled and fell into Path C with a full body.

`ScanMedia` (Path C) *does* pass the full buffer, then either writes a placeholder JPEG or copies the original.

### 2.3 What Grok/ChatGPT would need

Both APIs want a **full image** (URL or base64) plus a text prompt, and return text. That is incompatible with Path B’s 4KB peek:

- 4KB of a JPEG is not a valid picture for a vision model.
- Uploading the full image **before** writing response headers means buffering every image (defeating Path B).
- Holding the client until `api.x.ai` / `api.openai.com` returns couples browsing to a third-party SLA, rate limit, and network path.

### 2.4 Settings after this Alpha UI change

| Key | Values | Role |
|-----|--------|------|
| `ai_image_filtering_mode` | `disabled`, `grok`, `chatgpt`, `local` | Radio on `/ai`. Syncs `enable_ai_image_filtering` to `true`/`false`. |
| `ai_grok_api_key` | secret | xAI key. Server-side only. |
| `ai_openai_api_key` | secret | OpenAI key. Server-side only. |
| `ai_local_llm_url` | URL | Ollama (or compatible) HTTP base URL. Used when mode is `local`. |
| `ai_scanner_url` | URL | Legacy local classifier. Used only when mode is unset and the old toggle is on. |

**Grok and ChatGPT are not called on the request path yet.** Enabling them persists intent and keys. In-line scanning remains the legacy local URL only.

### 2.5 HTTP content cache — verified: there is none

GateSentry is a **filtering forward proxy**, not a caching proxy (not Squid). `http.Transport` has no response cache. `copyResponseHeader` copies origin `Cache-Control` / `Expires` / `ETag` to the client and adds `Via`. It does **not** store the body, does **not** honour `max-age` for a second client, and does **not** add `Age`.

Verified in `gatesentryproxy/http_cache_test.go`: two identical GETs of `Cache-Control: public, max-age=3600` HTML produce **two origin fetches**. That is why tests through the proxy almost always show a cache miss.

| What exists today | Kind | HTTP Cache-Control? |
|-------------------|------|---------------------|
| DNS response cache | Answers by `(qname, qtype)` + TTL | n/a (DNS) |
| MITM certificate cache | Generated leaf certs | n/a |
| Auth user cache | Basic-auth tuple, 5 min | n/a |
| **HTTP response body cache** | **Does not exist** | Origin headers are **forwarded only** |
| Block pages | Synthetic 403 | Forced `no-store, no-cache` |

What testers actually see:

- **`curl --proxy`** has no client cache. Every curl is a miss at the proxy, and the proxy always goes upstream.
- **Browser disk cache** is the only HTTP cache in the path. If DevTools “Disable cache” is on, or the origin sent `no-store` / `no-cache` / `max-age=0`, every navigation is a miss. Many HTML sites do exactly that.
- **Path C** (HTML) decompresses, maybe re-gzips, and rewrites `Content-Length`. Origin `Cache-Control` is still copied. We do not interpret it.
- **304 Not Modified** can still happen: the *browser* sends `If-None-Match`, we forward it, origin returns 304. That is origin revalidation, not a proxy cache hit. The proxy has no body on a 304.

CVE-style tests (`§15.28` cache poison) assume we **must not** become a shared HTTP cache. Adding one later is a product decision with Vary / `Authorization` / cookie poisoning risk. It is **not** required for AI.

---

## 3. Verdict cache vs HTTP cache (do not mix them)

The goal of AI is to decide whether content is **appropriate** (pass through) or **inappropriate** (filter). That decision is a property of **bytes** (and maybe URL). It is **not** a property of whether we are allowed to reuse the origin response as a substitute for fetching it again.

RFC 9111 `Cache-Control` answers a different question: “may a shared cache store and replay this **representation**?”

| | HTTP content cache (RFC 9111) | AI **verdict** cache |
|--|-------------------------------|----------------------|
| Stores | Response bodies (+ headers) | `allow` / `block` (+ confidence, time) |
| Key | Method + URL + `Vary` | **Content hash** (source of truth); URL as a hint only |
| Honours `no-store` / `no-cache` / `private` | **Must**, if we ever store bodies | **Must not.** Those flags mean “do not replay the page,” not “forget that this picture was porn.” |
| Purpose | Save bandwidth / latency | Do not pay a vision API twice for the same bytes |
| On miss | Fetch origin | Classify (async or wait); default **fail-open** |
| Shared across users | Dangerous (personalized HTML, cookies) | Safe **if keyed by content hash** (same bytes → same verdict) |

**`no-cache` does not mean “we have no verdict.”** A bank page with `Cache-Control: no-store` can still be classified. We look at the bytes as they pass, record allow/block, and **drop the body**. The next time those bytes appear (same URL or not), we apply the verdict without storing the page.

**Do not build an HTTP body cache in order to run AI.** That would:

- Skip almost all interesting HTML (`no-cache`, `private`, `max-age=0` are normal).
- Skip anything with `no-store`.
- Invite cache poisoning (authenticated content served to the wrong client).
- Fight Path B (images are streamed, not stored).

### 3.1 What we *should* do with Cache-Control (header rewrite, not storage)

The browser **is** an HTTP cache. That is the real cache in this system. If we deliver an unclassified NSFW image with the origin’s `Cache-Control: public, max-age=31536000`, the browser keeps it on disk. An async verdict that later says “block” **never runs in that browser**, because the browser never comes back.

So AI must be smart about **client** caching, not about storing pages in the proxy:

| Verdict state | What we send the client | Why |
|---------------|-------------------------|-----|
| **Unknown / pending** | Replace origin freshness with `Cache-Control: private, max-age=0, must-revalidate`. Keep `ETag`/`Last-Modified` if present. | Next navigation revalidates through the proxy so a later block can take effect. Not `no-store` — that kills bfcache and forces a full re-download every time. |
| **Allow** | Forward origin `Cache-Control` unchanged | Harmless content may be cached by the browser like today. |
| **Block** | Placeholder image/page with its own short `private, max-age=…` | Browser caches the placeholder, not the original. |

On the follow-up request:

- If the browser sends `If-None-Match` and origin returns **304**, we have **no body**. Apply the **URL-hint** verdict if we have one. Do not try to content-hash an empty 304.
- If origin returns **200**, content-hash the body (Path C already has it; Path B can hash while streaming) and look up the verdict cache.

### 3.2 Cache keys

1. **Primary:** `SHA-256` of decoded body (after gzip). Same image on two CDN URLs → one API call.
2. **Secondary hint:** normalized URL (strip tracking query params we know, keep the rest). Used for 304s and for “block before we have peeked 4KB.”
3. **Never** key a shared verdict by URL alone for `text/html` that is `private` / `Set-Cookie` / `Authorization`. Two users, one URL, different bytes. Hash only. URL hint for HTML is optional and must be per-user or omitted.

TTL: allow ~24h; block ~7d; pending short (seconds). API 429/5xx: negative-cache the *failure*, not a fake allow, so we retry without hammering.

### 3.3 Path through the proxy (no body store)

```
request
  → URL-hint verdict == block?  serve placeholder (no origin fetch needed, optional)
  → else fetch origin as today (always, unless we later add a real HTTP cache)
  → 304: apply URL-hint verdict; rewrite Cache-Control per table above
  → 200: hash body (stream or buffer)
       → hash verdict == block? placeholder
       → hash verdict == allow? forward, origin Cache-Control
       → miss: deliver now (fail-open), enqueue scan, send pending Cache-Control
background worker: API classify → store verdict only (not the bytes)
```

Memory on the NAS: LRU of hashes and URLs, not JPEGs. A few tens of bytes per entry.

### 3.4 v2 vs v3 — locked for this branch

| Branch | HTTP body cache (Squid-style) | What we keep |
|--------|-------------------------------|--------------|
| **v2 (this branch)** | **No.** Forwarding-only, as today. | Bytes only while a worker is classifying them, then drop. Verdicts and (if we adopt §10) blocked URLs. |
| **v3 (future)** | May add an RFC 9111 content cache for bandwidth. | Separate project. Poison tests, `Vary`, `Authorization`. Not a prerequisite for AI. |

v2 will not grow a response store “so AI has something to scan later.” If the bytes are gone after the first response, we either classified them in-flight / in the short analysis queue, or we wait for the next origin fetch (browser revalidation, §3.1). That is enough.

---

## 4. The two designs (and a hybrid)

### Option A — Real-time (hold the response)

```
client GET image
  → proxy fetches upstream
  → buffer full body
  → POST bytes to Grok/OpenAI
  → if NSFW: placeholder JPEG
  → else: send original bytes
```

**Pros**

- First view is blocked. No “one free look.”
- Simple mental model: the filter either ran or it did not.

**Cons**

- Adds **seconds** to every uncached image. Browsers request images in parallel; 20 in-flight vision calls will melt the NAS, the API quota, and the page.
- Must cap concurrency (semaphore) or the process becomes the DNS-outage class of hang: admin UI shares the process.
- Fail-closed (timeout = block) looks like a broken internet. Fail-open (timeout = allow) is Option B with worse latency.
- Full-body buffer of large JPEGs/PNGs undoes Path B.

**When it could work**

- Tiny images only (e.g. `< 32KB`) with a **hard 300–400ms deadline**, then fail-open.
- Not as the default for every `image/*` response.

### Option B — Verdict cache (scan after deliver)

This is the **verdict** cache in §3, not an HTTP body cache. Origin is still fetched every time (today’s proxy). We remember allow/block.

```
client GET image
  → lookup verdict by URL hint, then (if we have bytes) by content hash
  → HIT block: placeholder immediately
  → HIT allow: stream immediately (Path B), origin Cache-Control
  → MISS: stream immediately, Cache-Control rewritten to revalidate,
           enqueue bytes or a copy for background scan
background worker:
  → call Grok/ChatGPT (bounded concurrency)
  → store verdict with TTL (drop the bytes)
next request: HIT (browser revalidated because we stripped long max-age)
```

**Pros**

- Page load stays on Path B. Alpha can ship without making the LAN feel dead.
- Natural dedup: the same avatar/CDN URL is classified once.
- API cost and rate limits are tunable (queue depth, per-host caps, “scan at most N unique images/minute”).
- A dead API degrades to “never blocks new URLs,” not “every image hangs.”

**Cons**

- **First-seen leak.** The first load of a new NSFW image is delivered. Blocking starts on the next request (reload, next page, thumbnail reused).
- If we only hash the URL, CDNs with unique query strings (`?token=`) miss. Content hash is stable but needs bytes (hash-while-stream on Path B, or the Path C buffer).
- The user may never “reload” a one-shot image (chat attachment). Those stay leaked unless we rewrite Cache-Control to force revalidation (§3.1) or wait on the first response (Option A / hybrid).
- If we **forward** origin `max-age=1y` on a miss, the browser never revalidates and the leak is **permanent in that browser**. Header rewrite is mandatory for Option B.

**Queue**

- Bounded channel (e.g. 64). Drop oldest miss rather than blocking Path B.
- Semaphore of 1–2 in-flight API calls on the NAS.
- Never `log.Println` per image unless `GS_DEBUG_LOGGING`.

### Option C — Hybrid (recommended default)

1. **Always** Path B for delivery unless the **URL list / verdict** says **block**. Never stall for Grok (issue (a), not (b)).
2. Background scan **only if the eligibility funnel says so** (issue 3).
3. Optional “strict” checkbox later: tiny images, 300–400 ms deadline, then fail-open. Off by default.
4. Domain lists / rules remain the **first line**. AI is for URLs that already passed those rules. Once AI says block, that URL joins the first line (§10).

This matches how GateSentry already thinks: cheap, local, deterministic filters first; expensive content inspection last.

---

## 5. Recommendation for Alpha

**v2 default: (a) + issue (3).** Deliver the image, queue a *rare* analysis, enforce later via the URL list. Do not stall. Do not scan every GET.

Rationale:

1. Path B exists specifically so images do not wait on filters. Real-time vision (b) fights that design.
2. A 134MB `log.db` and a previous DNS-timeout hang already proved that slow work in the request path takes down admin. Remote HTTP to `api.x.ai` is that class of I/O.
3. First-seen leak in (a) is honest for Alpha **only if** we rewrite Cache-Control on unclassified responses so the next page load can block. Otherwise the browser caches the leak.
4. Optional strict/small wait (b) can come later, off by default, with a hard 300–400 ms deadline then fail-open.
5. We will **not** add an RFC 9111 HTTP body cache on **v2**. Squid-style caching is a **v3** idea.
6. **Enforcement of a “block”** is an AI-managed URL list (§10).
7. **Issue (3):** eligibility funnel + allow LRU + rate/queue caps so almost no requests reach a model (§11.4). Without that, (a) still scans the internet.

**Do not call the APIs at all until the verdict store + queue exist.** Enabling Grok on the radio today only stores the key. That is deliberate.

---

## 6. Other constraints (not optional)

- **MITM.** No TLS bump → no image bytes → AI cannot run. The UI must keep saying that.
- **Privacy.** Image bytes leave the house to xAI or OpenAI. Keys stay in `GSSettings`. Do not log prompts, keys, or bodies.
- **Availability.** Timeouts, semaphore, negative cache on 429/5xx. Fail-open on errors. Admin must stay up if the API is down.
- **Rules.** AI should eventually be a **rule action or rule flag**, not a global always-on pipe, consistent with “all filtering is scoped to rules.” Alpha can stay global on `/ai` while we learn the cost.
- **HTML vs images.** Keyword scanning already buffers HTML (Path C). Do not fold vision into that path. Do not parse HTML to rewrite `<img src>` in Alpha (racy with the browser’s parallel fetches).
- **Video.** Out of scope. Path B peeks 4KB of video; vision APIs want frames we do not extract.

---

## 7. Working classify prompt (in `application/filters/ai_vision.go`)

Keep the model on a **short, structured** answer so we do not parse essays on the NAS. This is the prompt that passed live tests on a clearly pornographic JPEG (woman’s backside, genitals visible) against Grok, ChatGPT, and Ollama/MLX (2026-09-10).

**Prompt** (`visionClassifyPrompt`):

```
Classify this image for a household content filter.
Reply with one JSON object only:
{"nsfw": true|false, "confidence": 0-100, "reason": "short"}
nsfw=true for pornography, sexual nudity, explicit sexual activity, or clearly sexualized poses meant as adult content.
nsfw=false for non-sexual clothing, fashion, or ordinary photos of people.
```

**Why this wording:** an earlier draft said `nsfw=true only for sexual nudity or explicit sexual activity`. `gemma4:e4b-mlx` then returned `nsfw=false` / “clothed, suggestive” on that same JPEG. Naming **pornography** and **sexualized adult content** made all three providers agree `nsfw=true`.

**Parse:** extract a JSON object that contains `"nsfw"` (tolerate markdown fences and extra thinking text). `ShouldBlock(minConfidence)` is `nsfw && confidence >= threshold` (tests used 50; product start **80**). On parse failure, do not write the whitelist; fail-open.

**Wire format**

| Provider | HTTP | Body |
|----------|------|------|
| Grok | `POST https://api.x.ai/v1/chat/completions` Bearer key | OpenAI-style `messages` + `image_url` data URI. Do **not** send `response_format` (400 on some Grok models). |
| ChatGPT | `POST https://api.openai.com/v1/chat/completions` Bearer key | Same, **with** `response_format: json_object`. |
| Ollama | `POST {base}/api/chat` | Native `{ model, stream:false, format:"json", messages:[{ content, images:[raw base64] }] }`. OpenAI `/v1/chat/completions` is not required. |

**Models that worked in live tests** (admin-editable on `/gatesentry/ai`; empty field uses the code default):

| Setting | Env seed | Default if empty | Notes |
|---------|----------|------------------|--------|
| `ai_grok_model` | `GS_AI_GROK_MODEL` | `grok-4.5` | `grok-2-vision-1212` is gone (`Model not found`). |
| `ai_openai_model` | `GS_AI_OPENAI_MODEL` | `gpt-4o-mini` | |
| `ai_local_llm_model` | `GS_AI_OLLAMA_MODEL` | `llava` | On Apple Silicon MLX, `gemma4:e4b-mlx` works **if** Ollama ≥ 0.33.3 and `/api/show` lists `vision`. An older pull of the same tag is text-only (`capabilities` without `vision` → HTTP 400). |

**Latency** (same 64 KB JPEG, 3 sequential calls, this LAN, 2026-09-10):

| Provider | Cold | Warm | Avg |
|----------|------|------|-----|
| ChatGPT `gpt-4o-mini` | 1.6 s | 1.0–2.5 s | ~1.7 s |
| Ollama `gemma4:e4b-mlx` | 3.8 s | 3.2–3.9 s | ~3.6 s |
| Grok `grok-4.5` | 5.7 s | 1.3–4.0 s | ~3.7 s |

Outbound probes and classify calls **must not** use `HTTP_PROXY` or GateSentry DNS (see passthrough for `api.x.ai` / `api.openai.com`).

Live tests: `go test ./application/filters -count=1 -timeout 3m -run TestLive` with `.env` (`GS_AI_*`). Placeholders `CHANGE_ME` skip that provider.

---

## 8. Key decisions (to confirm)

| # | Decision | Proposed | Alternative |
|---|----------|----------|-------------|
| D1 | First retrieval (a vs b) | **(a) deliver + async queue** | (b) stall until scanned |
| D2 | First-seen leak | **Accepted on v2**, closed on the *next* request via Cache-Control rewrite | (b) / strict small-image wait |
| D3 | Verdict identity | **Content hash** primary; URL hint for 304 / first peek | URL only |
| D4 | Scope | Global `/ai` toggle for Alpha | Per-rule flag from day one |
| D5 | On API error | Fail-open, negative-cache 429 | Fail-closed |
| D6 | Local scanner | Keep as advanced leftover | Delete |
| D7 | HTTP body cache | **v2: do not add. v3: maybe Squid.** Always fetch origin on v2 | Squid on v2 (rejected) |
| D8 | Unclassified Cache-Control | **Rewrite to `private, max-age=0, must-revalidate`** | Forward origin (browser may cache the leak forever) |
| D9 | `no-store` / `no-cache` from origin | **Still classify**; do not skip AI; do not store the body | Treat as “cannot scan” |
| D10 | How a “block” is enforced | **AI-managed URL list** consumed by existing URL/rule matching (§10) | Parallel “AI proxy action” that never hits rules |
| D11 | Body retention | **Only in the analysis queue**, dropped after verdict | Persist images / HTML on disk |
| D12 | Allow / inspected index | **Durable hash whitelist with TTL** (§13), not a Domain List and not an HTTP body cache | Memory-only LRU forever |
| D13 | Who gets scanned (issue 3) | **Eligibility funnel** (§11.4): type/size, rule, inspection index, pending, budget, queue cap. Default skip. | Scan every image when AI is enabled |
| D14 | Whitelist key | **SHA-256 of decoded body**; URL is a hint only | URL-only whitelist (rejected: content can change) |

---

## 9. PR Plan

### PR 1 — Admin UI + settings (this change)

- `/ai`: Alpha tag, provider radio, Grok/OpenAI password fields, legacy URL.
- Settings whitelist: `ai_image_filtering_mode`, `ai_grok_api_key`, `ai_openai_api_key`.
- Proxy still does not call remote APIs.

### PR 2 — Analysis queue + URL-list enforcement (no API yet)

- Queue holds **a copy of the body only until classified**, then drops it. Cap queue RAM (e.g. N images × size cap).
- **Eligibility gate before enqueue** (§11.4). Most images never copy into the queue.
- Household rate limit + semaphore (1–2 in-flight). Queue drop = skip, not stall.
- Inspection index (§13): hash → allow/block/pending with TTL. Memory + small on-disk file. **No image bytes.**
- In-memory pending set so we do not re-enqueue.
- **Block** writes a normalized URL into an AI-managed URL list (§10), not into DNS Domain Lists.
- Request path: existing URL/rule match first. If the URL is already on the AI list → block **before** origin fetch (same as today’s URL filter).
- On miss that *is* eligible: stream as today **(a)**; rewrite `Cache-Control` to `private, max-age=0, must-revalidate`.
- Tests: two GETs still hit origin when not blocked; after a synthetic “AI block” the third GET is blocked with **no** second origin body; `no-store` still classifies; documentation IPs/URLs only.
- Metrics: verdict hits/misses, queue depth, drops, AI-list size. Do not reuse DNS cache metrics.

### PR 3 — Grok/OpenAI worker

- Worker reads provider + key from settings.
- Bounded concurrency, timeouts, no request-path I/O.
- JSON classifier prompt. Do not log image bytes. Drop bytes after the verdict is stored.

### PR 4 — Optional strict mode

- Size cap + wait budget for small images.
- Default off.

### PR 5 — Rule integration (after Alpha)

- `ai_action` or similar on a rule: default / enable / disable, same shape as `mitm_action`.

---

## 10. Option D — AI as a URL-filter feeder (v2 proposal)

**Concept (from product):** v2 stays a forwarding proxy. We already block URLs. AI does not need to become a content cache. It only needs to **decide** if a URL’s content is inappropriate, **keep the bytes only while that decision runs**, then **add the URL to a filter** so every later request is blocked by the machinery we already have. A Squid-style cache is v3.

This is the most GateSentry-native shape. Critique it honestly before adopting it.

### 10.1 What we already have (reuse, do not fork)

Request evaluation is already an 8-step rule pipeline. URL/domain blocking already happens **before** the body is fetched when the match is domain-level:

| Mechanism | Granularity | When it runs | Persists? |
|-----------|-------------|--------------|-----------|
| DNS Domain Lists | Hostname | Resolution | Yes |
| Rule `Domain` / `DomainPatterns` / `DomainLists` | Hostname | Proxy, step 3 | Yes |
| Rule `URLRegexPatterns` | Path (needs MITM on HTTPS) | Proxy, step 5 | Yes |
| Legacy `blockedsites.json` | Host/URL | `UrlAccessHandler` | Yes |
| Keyword / content-type | Body | After MITM | n/a |

A “block” verdict that becomes **another URL on a list** is then enforced on the cheap path: no vision API, no body, no Squid.

### 10.2 Why this is a good fit for v2

- **Forwarding-only.** Origin is still fetched on unclassified URLs. No shared body store, no `Age`, no poison surface.
- **Bytes are ephemeral.** Queue: `{url, content-type, body, enqueued_at}`. Worker classifies, writes a verdict, **deletes the body**. RAM cap + TTL so a stuck API cannot pin JPEGs forever.
- **Enforcement is boring.** Second request hits the URL list the way ads/malware lists already work. Path B stays a stream.
- **Visible.** An admin can open the AI list, see why `cdn.example/foo.jpg` is blocked, delete a false positive. A hash LRU in RAM cannot be audited.
- **Restart-safe for blocks.** Domain Lists and rules already persist. An in-memory-only block forgets after `deploy.sh`.
- **Rules stay in charge.** A rule can attach the AI list, scope it to users/hours, or disable it. That matches “all filtering is scoped to rules.”
- **DNS is optional, not automatic.** The same list *could* be assigned on the DNS page later. It must not be auto-assigned (see 10.3).

### 10.3 Critiques (must design around these)

**1. URL is not content.**
The same `https://cdn.example/img.jpg` can be a logo today and NSFW tomorrow. Two URLs can be the same bytes (`?token=`). A URL list is the right **enforcement** key for “do not fetch this again.” It is a weak **identity** for “we already know this picture.” Keep a **content-hash LRU** for *allows* and for de-duping the API. Do not pretend the URL list replaces hashing.

**2. Do not write into Domain Lists as they exist today.**
`DomainList` entries are **hostnames**, O(1) by domain. If AI blocks `https://media.discordapp.net/attachments/…/pic.jpg` by inserting `media.discordapp.net`, we break chat, avatars, and harmless CDNs. Wikipedia Commons, Google user content, S3, CloudFront — one dirty object must not sink the host.

v2 AI list must be **exact normalized URLs** (scheme + host + path, strip fragment, consider stripping known cache-buster query keys). That is a new collection, or a new `source=ai` list type that the **URL** matcher consumes — not `Index.IsDomainInAnyList`.

**3. Do not auto-feed DNS.**
DNS can only block hostnames. Auto-promoting AI image hits into `dns_domain_lists` would NXDOMAIN entire CDNs. DNS assignment stays a human action, and only for hosts that are *themselves* the adult site (`pornhost.example`), not `cdn.cloudfront.net`.

**4. A block list cannot store allows.**
If we only append “bad URLs,” every Google logo, every favicon, every CSS sprite is a new URL and a new API call. That will empty the xAI/OpenAI wallet. Need:

| Store | Holds | Durable? |
|-------|--------|----------|
| AI **block URL** list | Inappropriate URLs | Yes (filter file / domain-list-like JSON) |
| **Allow/pending LRU** | Hash + URL → allow/pending | Memory is enough for Alpha; optional file later |
| Analysis **queue** | Body bytes | Seconds, then gone |

**5. First-seen leak is unchanged.**
Adding to a URL list helps request *n+1*, not request 1. Still need fail-open + Cache-Control rewrite (§3.1), or a later strict wait. The URL-filter idea does not solve the Alpha product question by itself.

**6. HTTPS path visibility.**
Exact-URL blocking of `https://site/path/img.jpg` needs the proxy to see the path. That is MITM (or the client using HTTP). Without MITM we only have the hostname from CONNECT — back to critique 2. The `/ai` page must keep saying MITM is required.

**7. Overblock vs underblock on HTML.**
If the model says a **page** is inappropriate, adding the page URL blocks the next navigation. If it says an **embedded image** is inappropriate, add the **image** URL only. Do not add the page host because one thumbnail failed. Chat attachments and unique signed URLs may never repeat — URL list does nothing for those; that is acceptable on v2.

**8. Concurrency.**
Twenty `<img>` tags to the same URL must enqueue **one** analysis. Pending state on the URL hint is required or we stampede the API.

**9. False positives and poison.**
A bad classification is now a durable filter entry. Require: confidence threshold, `source=ai` tagging, admin purge, optional auto-expire (e.g. 7 days unless re-seen). Do not let the AI list be imported as a public DNS blocklist.

**10. Rule-pipeline placement.**
Best: a **built-in list** checked like today’s URL block, *and* exposable as `DomainLists`/`URL` match criteria so an admin can write “kids: block AI list; adults: ignore.” If we only inject into the global `UrlAccessHandler`, we skip per-user rules. If we only add regexes onto a random existing rule, we fight the operator. Prefer a dedicated list + a default rule that references it.

### 10.4 Recommended v2 shape (after critique)

```
unclassified GET
  → existing rules / AI URL list: already blocked?  stop (no origin)
  → allow LRU hit?  forward as today
  → fetch origin (forwarding proxy, always)
  → deliver body (fail-open); rewrite Cache-Control if pending
  → if not allow/block: copy body into analysis queue (size cap), return

worker
  → Grok/ChatGPT on the copy
  → drop the copy
  → if inappropriate + high confidence: append normalized URL to AI URL list
  → if appropriate: remember hash+URL in allow LRU
  → if error: remember failure briefly; do not write the URL list
```

**v3 Squid** would sit *under* this: a later optional “replay this allowed body.” It does not change the AI decision or the URL list. Do not couple the two roadmaps.

### 10.5 Verdict on the concept

**Adopt it as the v2 enforcement path for blocks**, with these non-negotiables:

1. No HTTP content cache on v2.
2. Bodies live only in the analysis queue.
3. AI writes **URLs**, not hostnames, and **not** into DNS by default.
4. Allows stay in a small LRU (hash + URL), not in a Domain List.
5. The list is visible, tagged, purgeable, and preferably attached through a rule.
6. First-seen leak + Cache-Control rewrite remain; the URL list does not replace them.

Reject: “AI = Domain List of hosts,” “AI = skip scan when `no-cache`,” “AI = we must be Squid first.”

---

## 11. Three issues: stall vs leak vs “do not scan the internet”

Vision is slow (often **0.5–5+ seconds** per image) and costs money. A homepage can request **50–150** images. Almost none of them are inappropriate. The design has to answer three questions together; picking (a) or (b) without (3) still melts the API budget.

### 11.1 (a) Pass through, analyse later

```
browser ← image bytes immediately (Path B)
queue   ← copy of bytes (RAM, short TTL)
worker  ← Grok/ChatGPT
          drop bytes
          if bad: append URL to AI block list
next GET of that URL → existing URL filter blocks (no origin / placeholder)
```

**Holds:** first view can be inappropriate (the “one free look”). Closed on the *next* request if we rewrite `Cache-Control` on unclassified responses (§3.1). Unique signed URLs (chat attachments) may never have a next request.

**Does not hold:** page load, NAS CPU, admin UI. Queue drop is fail-open, not a hang.

**Fits v2:** forwarding proxy, URL-list enforcement (§10), body only while analysing.

### 11.2 (b) Stall until scanned

```
browser waits
proxy buffers full image
API returns
  bad  → placeholder
  good → original bytes
```

**Holds:** no first-seen leak for that image.

**Does not hold:** browsing. Parallel `<img>` tags each wait on a remote model. A 3 s API × 40 images is not “a bit slow”; the tab looks dead. Fail-closed on timeout looks like a broken internet. Fail-open on timeout **is (a) with extra latency**.

**When (b) is defensible:** optional later “strict” mode, **small** images only (e.g. `< 32 KB`), **hard deadline** 300–400 ms, then fail-open. Never the default for every `image/*`.

### 11.3 Critique and recommendation: pick (a)

| | (a) async | (b) stall |
|--|-----------|-----------|
| First NSFW image | Delivered once | Blocked |
| News homepage | Normal | Unusable |
| API down / slow | Pages still load | Every image waits |
| Unique one-shot URL | Leak (no second GET) | Blocked if the wait finished |
| Matches Path B | Yes | No (must buffer) |
| Admin process hang risk | Queue + semaphore | Request-path I/O (same class as dead DNS) |

**v2 default is (a).** Stall is not a better moral choice if it makes the filter something nobody will leave on. Strict/small wait can be a later checkbox (PR 4), off by default.

(a) only works as a *filter* if unclassified responses are not cached for a year by the browser (§3.1). Without that rewrite, (a) is “leak forever in this browser,” not “leak until next load.”

### 11.4 Issue (3): do not scan every request

(a) without an **eligibility gate** still sends every JPEG on the LAN to Grok. That is slower than (b) for the wallet and still slow for the queue. **Most GETs must never enter the analysis queue.**

The cheap path already exists: DNS lists, rule domain match, URL block list, allow LRU. AI is only for what those miss. Think of a funnel, not a default scan.

```
every response
  1. Not an image we care about?          skip   (js/css/font/svg/ico/video; Path A)
  2. Already on AI block URL list?        block  (no API, maybe no origin)
  3. Allow LRU hit (URL or content hash)? skip   (already classified good)
  4. Pending in queue?                    skip   (one analysis per URL)
  5. Rule does not enable AI?             skip   (kids vs adults; hours)
  6. Exception / allow-listed host?       skip
  7. Too small / too large / 1×1 pixel?   skip   (favicons, tracking pixels, 20 MB “image”)
  8. Household scan budget exhausted?     skip   (N/min, N/day — fail-open)
  9. Queue full?                          skip   (drop; never block Path B)
 10. Else                                 enqueue copy, deliver (a)
```

**What “skip” means:** forward as today. Not “allow forever” unless step 3 said so. A skipped URL can be scanned later if it shows up again and budget exists.

#### Cheap local skips (no model)

| Skip | Why almost everything is this |
|------|-------------------------------|
| Not `image/jpeg`, `image/png`, `image/webp`, `image/gif` | HTML, JS, CSS, fonts, JSON, video |
| Body `< ~6 KB` or looks like ICO/SVG | Favicons, sprites, UI chrome |
| Declared or decoded size tiny (e.g. both dimensions `< 64`) | Tracking pixels; optional JPEG header peek without full decode |
| Body `> MaxContentScanSize` (default 2 MB) | Not a thumbnail; do not pin RAM in the queue |
| Path / host looks like site chrome | `/favicon`, `/sprite`, `/logo`, well-known static paths — heuristic, must be conservative |
| MITM off | No bytes; cannot scan HTTPS images anyway |

v2 does **not** vision-scan HTML. Keywords already run on Path C. Sending every article to ChatGPT is a different product.

#### Do not re-scan what we already know

| Hit | Action |
|-----|--------|
| AI **block** URL list | Enforce; never API |
| **Allow LRU** (hash and/or URL) | Skip; TTL (e.g. 24 h) so a replaced CDN object can be seen again |
| **Pending** URL | Skip second enqueue |
| Content hash already allow/block | Skip API even if the URL is new (`?token=`) |

This is the real answer to (3) for repeat traffic: the internet is huge, a household is not. Logos and sprites repeat constantly. Hash + URL LRU is what keeps the API quiet after the first hour.

#### Budget so a new site cannot dump the queue

- **In-flight:** 1–2 vision calls on the NAS (semaphore).
- **Queue depth:** small (e.g. 32–64). Drop oldest, fail-open, metric `ai_queue_drops`.
- **Rate:** e.g. 10 scans/minute household-wide, configurable. Burst of 100 images → ~10 scanned, rest skip.
- **Optional daily cap** (token/cost). At cap: skip, do not stall.

When the budget is tight, **prefer scanning unknown, large-enough photos** over icons. A simple priority: bigger dimension / bigger file first. Do not random-sample if it means we scan the logo and skip the only photo.

#### Optional later: “likely user content” hint

Not required for Alpha. If budget is still too hot: prefer paths like `/uploads/`, `/attachments/`, `/media/`, known UGC hosts; deprioritize first-party `static.` / `cdn.` chrome. This is a heuristic — it will miss NSFW on `img.wp.com/i/foo.jpg`. Treat as a **priority**, never as a hard allow.

#### What we will not do on v2

- Scan every request “because AI is on.”
- Stall (b) as the way to reduce volume (it increases pain, not selectivity).
- Skip scanning because origin sent `no-cache` (§3).
- Insert skipped hosts into an allow Domain List (would hide future bad objects on that CDN).

### 11.5 How (a), (b), and (3) fit together

```
(3) eligibility gate     →  almost no requests reach AI
(a) deliver + queue      →  the few that do never stall the tab
URL list + allow LRU     →  the second hit is free
(b) optional strict      →  only tiny images, hard deadline, off by default
```

**Recommendation for v2:** **(a) + (3).** Do not ship (b) as default. Do not ship (a) without the eligibility funnel — that would still scan the internet.

---

## 12. Open questions

1. Confirm **(a)** as v2 default vs **(b)** stall? Proposed: **(a)**. Strict (b) later, off, small images only.
2. Confirm we **do not scan every image** — eligibility funnel + budget (§11.4)? Proposed: **yes**. Rule-scoped AI (kids only) as well as type/size/LRU/rate caps.
3. Content hash while streaming (extra CPU) vs copy-to-worker buffer (RAM)? Worker must not re-fetch origin unless we have to (auth, one-shot URLs).
4. Persist the verdict store across restarts (`devices.json`-style file) or memory-only for Alpha?
5. For HTML with `Set-Cookie` / `Authorization`, skip the URL hint entirely (hash only)? Proposed: **yes**.
6. Do we ever want a real HTTP body cache for bandwidth, separate from AI? **v2: no. v3: maybe.** Unrelated to shipping AI.
7. Block **exact URL** vs **hostname** vs **URL prefix**? Proposed for v2: **exact normalized URL** only. Hostname promotion is opt-in later and must not auto-feed DNS.
8. Should the AI URL list be visible and editable on Domain Lists / Rules UI, or a hidden system list? Proposed: **visible**, named, purgeable, tagged `source=ai`, so false positives are fixable.
9. One built-in catch-all rule that consumes the AI list, or must the admin attach the list to a rule? Proposed: **built-in default-on rule** (all users, block, MITM default) that the admin can disable or scope — otherwise Alpha AI does nothing until someone builds a rule.
10. Inspection-index TTL for **allow** vs **block**? Proposed: allow **24h**, block **7d**, plus invalidate when model/prompt/threshold changes (§13).
11. Cap on whitelist rows? Proposed: **50k** LRU evict oldest-allow first; never evict a block until TTL.

---

## 13. Inspection whitelist (do not scan the same bytes twice)

**Goal:** after an image has been classified, remember that fact with a **TTL** so the next time those **same bytes** appear we do not pay Grok/ChatGPT/Ollama again. This is the main answer to issue (3) once the first scan has happened.

Call it a **whitelist** in the product sense: “already inspected, safe to skip the model.” It is **not** an HTTP cache and **not** a Domain List.

| Store | Holds | Skips the model? |
|-------|--------|------------------|
| AI **block URL** list (§10) | Inappropriate **URLs** | Yes (and can skip origin) |
| **Inspection whitelist** (this section) | Hash of **bytes** → allow or block | Yes |
| Browser / RFC 9111 cache | Response bodies | Unrelated |
| Analysis queue | Bytes **during** the one scan | n/a |

Blocks still **also** go on the URL list so the *next URL* is cheap. The whitelist is how we skip the API when the **same JPEG** shows up on a new CDN URL (`?token=`, `img.jpg` vs `img.webp` is a *different* hash).

### 13.1 Feasibility

Measured on the 64 KB test JPEG (household LAN, 2026-09-10): ChatGPT ~1.0–2.5 s, Ollama/MLX ~3.2–3.9 s, Grok ~1.3–5.7 s. Re-scanning every logo on every page is not a product.

A family LAN does not see the whole internet. Unique images per day are hundreds to low thousands, not millions. SHA-256 over a streamed body is cheap next to a vision round-trip. **Feasible on the NAS** if we:

- Store **hashes and metadata only** (tens of bytes per row), never pixels.
- Cap the table (LRU).
- Look up **before** enqueue, on the Path B stream (hash while copying, or hash the Path C buffer).
- Do not put this on the request path as a remote call.

Not feasible / not wanted:

- Whitelist by **URL only**. CDNs reuse URLs; a “safe” avatar URL can become porn on the next deploy. That is a security bug, not a cache miss.
- Whitelist that stores **bodies**. That is Squid (v3). v2 stays forwarding-only.
- Unbounded append-only log (we already have a 134 MB `log.db` problem).

### 13.2 Database model

Do **not** put this in `log.db`, `GSSettings`, or Domain Lists.

Proposed record (`ai_inspect` / `ai_verdicts`):

| Field | Type | Why |
|-------|------|-----|
| `sha256` | 32 bytes, **primary key** | Identity of the bytes after gzip decode |
| `verdict` | `allow` \| `block` | Skip model either way |
| `confidence` | 0–100 | Optional; do not re-scan just to refresh a number |
| `provider` | `grok` \| `chatgpt` \| `local` | Which model said so |
| `model` | string | Invalidate when the admin changes model id |
| `prompt_ver` | small int / hash | Invalidate when the classify prompt changes |
| `url_norm` | optional string | Hint for 304s / metrics only, **not** the lookup key |
| `first_seen` | unix time | |
| `last_seen` | unix time | LRU / TTL refresh on hit? **No** for allow (see 13.3) |
| `expires` | unix time | Absolute TTL |

Secondary index: `url_norm` → sha256 for “we blocked this URL” correlation. Never treat URL as proof of content.

**Engine (proposed):** a small dedicated file, not buntdb `log.db`.

| Option | Pros | Cons |
|--------|------|------|
| In-memory map + JSON snapshot (`devices.json` style) | Simple, restart-safe enough | Full rewrite; cap must stay small |
| Separate BuntDB `ai_verdicts.db` | Already a dependency; keyed get | Another file to backup; don’t mix with logs |
| SQLite | TTL queries easy | New dependency, overkill for Alpha |

**Alpha:** memory map + atomic JSON/msgpack snapshot every N writes or every 60 s. Cap 50k rows. **v2 later:** dedicated `ai_verdicts.db` if snapshot size or crash-loss hurts.

Lookup on the hot path is `map[sha256]row` under a mutex (or `sync.Map`). Do not fsync per image.

### 13.3 TTL

Different verdicts have different risk.

| Verdict | Proposed TTL | Rationale |
|---------|--------------|-----------|
| **allow** | **24 hours** | Object at a URL can be replaced. Hash is of *these* bytes; if the file changes, hash misses and we scan again. TTL still bounds a wrong “allow” if the model was too timid. |
| **block** | **7 days** | False positive is visible (admin can purge). Re-paying the API for the same porn JPEG is waste. URL list already enforces. |
| **pending** | **30–60 s** | In-flight only. Do not persist pending. |
| **error / 429** | **1–5 min** | Negative cache the *failure*, not an allow. |

**Do not sliding-expire allows on hit.** If we refresh TTL every time a logo is seen, a wrong allow never dies. Absolute `expires = now + TTL` at insert time.

**Invalidate the whole allow set (not blocks) when:**

- `ai_grok_model` / `ai_openai_model` / `ai_local_llm_model` changes
- classify prompt version changes
- confidence threshold changes

Store `prompt_ver` on the row; lookup misses if the current prompt_ver differs. Cheaper than a global flush, and blocks can stay (a block from the old prompt is still a block we are willing to keep until TTL or admin purge).

### 13.4 Lookup / write path

```
stream or buffer body
  → sha256(decoded bytes)
  → whitelist hit allow (unexpired, prompt_ver match) → do not enqueue
  → whitelist hit block → placeholder / URL list (already)
  → pending → do not enqueue
  → else if eligible → enqueue copy, mark pending, deliver (a)
worker
  → classify
  → drop bytes
  → insert whitelist row (allow or block) with TTL
  → if block: also append URL to AI URL list
```

Path B: hash **while** copying to the client so we do not buffer the whole image just for the whitelist. If the hash hits **block** after we already started streaming, we cannot unsend those bytes (first-seen leak on *this* connection). The *next* request is blocked. Acceptable under D2. Optional later: hold the first 4KB until hash of the full body if we ever buffer.

### 13.5 Security considerations

**1. URL is not content.** A whitelist keyed only by URL is poisonable: scan a safe placeholder, then the origin swaps in porn at the same URL. **Primary key is SHA-256 of the body.** URL is telemetry.

**2. Do not store pixels.** A disk full of family photos and/or porn is a liability and a NAS-capacity bug. Hash + verdict only. Drop the analysis-queue copy as soon as the row is written.

**3. Do not log reasons that describe the image** in the default log. `reason` from the model can be explicit. Keep it off the hot log; optional debug only, behind `GS_DEBUG_LOGGING`, still no raw bytes.

**4. Shared household, not per-user.** The same bytes are the same bytes. A hash allow for user A is valid for user B. Do **not** key the whitelist on username. Per-user *policy* stays on the rule (who is subject to AI). If adults disable AI, they never consult this index; if kids enable it, they do.

**5. DoS / table stuffing.** An attacker (or a very image-heavy site) can present unlimited unique hashes. Cap 50k, evict oldest **allow** first. Do not evict **block** early (that would re-allow porn to save RAM). If the table is full of allows, drop new allows (fail-open, may re-scan) rather than dropping blocks.

**6. Hashing what we actually classified.** Hash **after** gzip decode, same bytes the model saw. Hashing the on-wire gzip would miss the same image with different compression.

**7. Do not trust the client.** Never accept `X-Content-SHA256` from the browser. We hash what we received from origin.

**8. Model disagreement / prompt drift.** Gemma was conservative (“clothed”) until the prompt named pornography. A whitelist row must carry `provider` + `model` + `prompt_ver` so changing Grok vs Ollama does not silently reuse a timid allow.

**9. Not a DNS list, not public.** Do not export this index as a Domain List or share it off-box. Hashes of rare images can be identifying.

**10. Admin control.** `/gatesentry/ai` (or a small table) should show count, allow vs block, oldest expiry, and a **Purge whitelist** button. False allow: purge and the next GET can scan again. False block: remove from URL list **and** whitelist or it will keep matching the hash.

**11. Fail-open on store errors.** If the snapshot file is corrupt, start empty, log once, do not take down the proxy.

### 13.6 Recommendation

Adopt the inspection whitelist for v2:

1. Key = SHA-256(decoded body).
2. Values = allow/block + TTL + model/prompt version.
3. Persist a capped snapshot; no image bytes.
4. Allow TTL 24h, block TTL 7d, no sliding allow expiry.
5. URL list remains the **enforcement** path for blocks; whitelist is the **do-not-rescan** path for both verdicts.

Reject: URL-only whitelist, storing bodies, mixing this into `log.db`, auto-export to DNS.
