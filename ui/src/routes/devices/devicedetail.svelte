<script lang="ts">
  import {
    ComposedModal,
    ModalHeader,
    ModalBody,
    ModalFooter,
    TextInput,
    FormGroup,
    Tag,
    Button,
  } from "carbon-components-svelte";
  import { createEventDispatcher } from "svelte";
  import { getBasePath } from "../../lib/navigate";

  const API_BASE = getBasePath() + "/api/devices";
  const DNS_RECENT_MS = 5 * 60 * 1000;
  const dispatch = createEventDispatcher();

  export let device: any;
  export let open = false;
  export let pingAvailable: boolean | null = null;

  let manualName = "";
  let owner = "";
  let category = "";
  let saving = false;
  let pinging = false;
  let error = "";
  let detailTab = "reach";
  let formDeviceId = "";

  function getToken(): string {
    return localStorage.getItem("jwt") || "";
  }

  function syncForm(d: any) {
    if (!d) return;
    manualName = d.manual_name || "";
    owner = d.owner || "";
    category = d.category || "";
  }

  $: if (device?.id && device.id !== formDeviceId) {
    formDeviceId = device.id;
    detailTab = "reach";
    syncForm(device);
  }

  $: pingStatus =
    device?.ping_status || (device?.online ? "online" : "unknown");
  $: pingLabel =
    pingStatus === "online"
      ? "Online"
      : pingStatus === "offline"
        ? "Offline"
        : "Unknown";
  $: noIp = !device?.ipv4 && !device?.ipv6;
  $: pingOfflineButDns =
    pingStatus !== "online" &&
    !!device?.last_dns_query &&
    !String(device.last_dns_query).startsWith("0001-");

  function dnsActivityLabel(): string {
    const iso = device?.last_dns_query;
    if (!iso || iso.startsWith("0001-")) return "Never observed";
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return "Never observed";
    if (Date.now() - date.getTime() < DNS_RECENT_MS) return "Active";
    return "Quiet";
  }

  function sourceTagType(s: string): string {
    if (s === "ddns") return "green";
    if (s === "mdns") return "blue";
    if (s === "passive") return "warm-gray";
    if (s === "manual") return "purple";
    return "gray";
  }

  async function save() {
    saving = true;
    error = "";
    try {
      const token = getToken();
      const response = await fetch(`${API_BASE}/${device.id}/name`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          name: manualName,
          owner: owner,
          category: category,
        }),
      });
      if (!response.ok) {
        throw new Error(await response.text());
      }
      dispatch("saved");
    } catch (err) {
      error = err.message;
    } finally {
      saving = false;
    }
  }

  async function pingNow() {
    pinging = true;
    error = "";
    try {
      const token = getToken();
      const response = await fetch(`${API_BASE}/${device.id}/probe`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!response.ok) {
        throw new Error(await response.text());
      }
      const data = await response.json();
      if (data.device) {
        device = { ...device, ...data.device };
        dispatch("probed", data.device);
      }
    } catch (err) {
      error = err.message;
    } finally {
      pinging = false;
    }
  }

  function close() {
    dispatch("close");
  }

  function formatDate(isoDate: string): string {
    if (!isoDate || isoDate.startsWith("0001-")) return "—";
    const date = new Date(isoDate);
    if (Number.isNaN(date.getTime())) return "—";
    return date.toLocaleString();
  }

  function formatTimeAgo(isoDate: string): string {
    if (!isoDate || isoDate.startsWith("0001-")) return "";
    const date = new Date(isoDate);
    if (Number.isNaN(date.getTime())) return "";
    const diffMs = Date.now() - date.getTime();
    const diffSec = Math.floor(diffMs / 1000);
    if (diffSec < 60) return "just now";
    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m ago`;
    const diffHr = Math.floor(diffMin / 60);
    if (diffHr < 24) return `${diffHr}h ago`;
    return `${Math.floor(diffHr / 24)}d ago`;
  }

  function when(iso: string): { abs: string; rel: string } {
    return { abs: formatDate(iso), rel: formatTimeAgo(iso) };
  }
</script>

<ComposedModal {open} on:close={close} size="md">
  <ModalHeader
    title="Device Details"
    label={device?.display_name || "Unknown Device"}
  />
  <ModalBody hasForm>
    {#if error}
      <div class="error-message">{error}</div>
    {/if}

    <FormGroup legendText="Assign a Name">
      <div class="name-grid">
        <TextInput
          labelText="Display Name"
          placeholder="e.g., Vivienne's iPad"
          bind:value={manualName}
          size="sm"
        />
        <TextInput
          labelText="Owner"
          placeholder="e.g., Vivienne, Dad"
          bind:value={owner}
          size="sm"
        />
        <TextInput
          labelText="Category"
          placeholder="e.g., kids, adults, iot"
          bind:value={category}
          size="sm"
        />
      </div>
    </FormGroup>

    <div class="gs-tabs dd-tabs">
      <button
        type="button"
        class="gs-tab"
        class:gs-tab--active={detailTab === "reach"}
        on:click={() => (detailTab = "reach")}>Reachability</button
      >
      <button
        type="button"
        class="gs-tab"
        class:gs-tab--active={detailTab === "dns"}
        on:click={() => (detailTab = "dns")}>DNS queries</button
      >
      <button
        type="button"
        class="gs-tab"
        class:gs-tab--active={detailTab === "id"}
        on:click={() => (detailTab = "id")}>Identity</button
      >
      <button
        type="button"
        class="gs-tab"
        class:gs-tab--active={detailTab === "disc"}
        on:click={() => (detailTab = "disc")}>Discovery</button
      >
    </div>

    <div class="dd-panels">
      <div class="dd-panel" class:active={detailTab === "reach"}>
        <div class="tab-toolbar">
          <Button
            kind="tertiary"
            size="small"
            disabled={pinging || noIp}
            on:click={pingNow}
          >
            {pinging ? "Pinging…" : "Ping now"}
          </Button>
        </div>
        <div class="dd-list">
          <div class="dd-row">
            <div class="dd-label">Online / offline</div>
            <div class="dd-value">
              <span class="status-dot {pingStatus}"></span>
              {pingLabel}
              {#if pingStatus === "online" && device?.ping_rtt_ms}
                <span class="muted">{device.ping_rtt_ms} ms</span>
              {/if}
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">Last ping</div>
            <div class="dd-value">
              {when(device?.last_ping).abs}
              {#if when(device?.last_ping).rel}
                <span class="muted">{when(device?.last_ping).rel}</span>
              {/if}
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">Address probed</div>
            <div class="dd-value mono">{device?.ipv4 || device?.ipv6 || "—"}</div>
          </div>
        </div>
        {#if noIp}
          <p class="hint">No IP address is known, so this device cannot be pinged.</p>
        {:else if pingAvailable === false}
          <p class="hint">Ping is not available in this environment. Status stays unknown.</p>
        {:else if pingOfflineButDns}
          <p class="hint">
            Ping failed, but this device has queried DNS — many phones and IoT
            devices ignore ICMP.
          </p>
        {:else if pingStatus === "offline"}
          <p class="hint">
            Ping failed. The device may be off, on another network, or blocking ICMP.
          </p>
        {/if}
      </div>

      <div class="dd-panel" class:active={detailTab === "dns"}>
        <div class="dd-list">
          <div class="dd-row">
            <div class="dd-label">Status</div>
            <div class="dd-value">
              <Tag
                size="sm"
                type={dnsActivityLabel() === "Active"
                  ? "teal"
                  : dnsActivityLabel() === "Quiet"
                    ? "gray"
                    : "outline"}>{dnsActivityLabel()}</Tag
              >
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">Last DNS query</div>
            <div class="dd-value">
              {when(device?.last_dns_query).abs}
              {#if when(device?.last_dns_query).rel}
                <span class="muted">{when(device?.last_dns_query).rel}</span>
              {/if}
            </div>
          </div>
        </div>
        <p class="hint">
          Last time this device asked GateSentry to resolve a name. An IP or local
          DNS record is not a query. A quiet device can still be online.
        </p>
      </div>

      <div class="dd-panel" class:active={detailTab === "id"}>
        <div class="dd-list">
          <div class="dd-row">
            <div class="dd-label">DNS name</div>
            <div class="dd-value">{device?.dns_name || "—"}</div>
          </div>
          <div class="dd-row">
            <div class="dd-label">Hostnames</div>
            <div class="dd-value tags">
              {#if device?.hostnames?.length}
                {#each device.hostnames as h}
                  <Tag size="sm" type="outline">{h}</Tag>
                {/each}
              {:else}
                —
              {/if}
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">mDNS names</div>
            <div class="dd-value tags">
              {#if device?.mdns_names?.length}
                {#each device.mdns_names as m}
                  <Tag size="sm" type="blue">{m}</Tag>
                {/each}
              {:else}
                —
              {/if}
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">IPv4</div>
            <div class="dd-value mono">{device?.ipv4 || "—"}</div>
          </div>
          <div class="dd-row">
            <div class="dd-label">IPv6</div>
            <div class="dd-value mono wrap">{device?.ipv6 || "—"}</div>
          </div>
          <div class="dd-row">
            <div class="dd-label">MAC address</div>
            <div class="dd-value tags">
              {#if device?.macs?.length}
                {#each device.macs as mac}
                  <Tag size="sm" type="warm-gray">{mac}</Tag>
                {/each}
              {:else}
                —
              {/if}
            </div>
          </div>
        </div>
      </div>

      <div class="dd-panel" class:active={detailTab === "disc"}>
        <div class="dd-list">
          <div class="dd-row">
            <div class="dd-label">Primary source</div>
            <div class="dd-value">
              <Tag size="sm" type={sourceTagType(device?.source)}
                >{device?.source || "—"}</Tag
              >
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">All sources</div>
            <div class="dd-value tags">
              {#if device?.sources?.length}
                {#each device.sources as s}
                  <Tag size="sm" type="outline">{s}</Tag>
                {/each}
              {:else}
                —
              {/if}
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">First seen</div>
            <div class="dd-value">{when(device?.first_seen).abs}</div>
          </div>
          <div class="dd-row">
            <div class="dd-label">Last seen</div>
            <div class="dd-value">
              {when(device?.last_seen).abs}
              {#if when(device?.last_seen).rel}
                <span class="muted">{when(device?.last_seen).rel}</span>
              {/if}
            </div>
          </div>
          <div class="dd-row">
            <div class="dd-label">Device ID</div>
            <div class="dd-value mono wrap">{device?.id || "—"}</div>
          </div>
        </div>
      </div>
    </div>
  </ModalBody>
  <ModalFooter
    primaryButtonText={saving ? "Saving..." : "Save"}
    primaryButtonDisabled={saving}
    secondaryButtonText="Cancel"
    on:click:button--primary={save}
    on:click:button--secondary={close}
  />
</ComposedModal>

<style>
  .name-grid {
    display: grid;
    grid-template-columns: 1fr;
    gap: 0.5rem;
  }
  .dd-tabs {
    margin: 0.5rem 0 0.75rem 0;
  }
  .dd-panels {
    display: grid;
  }
  .dd-panel {
    grid-area: 1 / 1;
    visibility: hidden;
    pointer-events: none;
  }
  .dd-panel.active {
    visibility: visible;
    pointer-events: auto;
  }
  .tab-toolbar {
    display: flex;
    justify-content: flex-end;
    margin-bottom: 0.5rem;
    min-height: 2rem;
  }
  .dd-list {
    display: flex;
    flex-direction: column;
    border-top: 1px solid #e0e0e0;
  }
  .dd-row {
    display: grid;
    grid-template-columns: 8.5rem 1fr;
    gap: 0.75rem;
    align-items: start;
    padding: 0.45rem 0;
    border-bottom: 1px solid #e0e0e0;
    min-height: 1.75rem;
  }
  .dd-label {
    font-size: 0.75rem;
    font-weight: 600;
    color: #525252;
    letter-spacing: 0.01em;
    line-height: 1.4;
    padding-top: 0.1rem;
  }
  .dd-value {
    font-size: 0.8125rem;
    color: #161616;
    line-height: 1.4;
    min-width: 0;
  }
  .dd-value.mono {
    font-family: "IBM Plex Mono", monospace;
    font-size: 0.75rem;
  }
  .dd-value.wrap {
    word-break: break-all;
  }
  .dd-value.tags {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }
  .status-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    vertical-align: middle;
    margin-right: 0.35rem;
    box-sizing: border-box;
  }
  .status-dot.online {
    background-color: #24a148;
  }
  .status-dot.offline {
    background-color: #8d8d8d;
  }
  .status-dot.unknown {
    background-color: transparent;
    border: 2px solid #c6c6c6;
  }
  .error-message {
    color: #da1e28;
    margin-bottom: 0.75rem;
    font-size: 0.8125rem;
  }
  .muted {
    color: #6f6f6f;
    font-size: 0.75rem;
    margin-left: 0.35rem;
  }
  .hint {
    font-size: 0.75rem;
    color: #525252;
    margin: 0.6rem 0 0 0;
    line-height: 1.4;
  }

  @media (min-width: 672px) {
    .name-grid {
      grid-template-columns: 1fr 1fr 1fr;
    }
  }
</style>
