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
    StructuredList,
    StructuredListHead,
    StructuredListRow,
    StructuredListCell,
    StructuredListBody,
  } from "carbon-components-svelte";
  import { createEventDispatcher } from "svelte";
  import { getBasePath } from "../../lib/navigate";

  const API_BASE = getBasePath() + "/api/devices";
  const DNS_RECENT_MS = 5 * 60 * 1000;
  const dispatch = createEventDispatcher();

  export let device: any;
  export let open = false;
  export let pingAvailable: boolean | null = null;

  let manualName = device?.manual_name || "";
  let owner = device?.owner || "";
  let category = device?.category || "";
  let saving = false;
  let pinging = false;
  let error = "";

  function getToken(): string {
    return localStorage.getItem("jwt") || "";
  }

  let formDeviceId = "";

  function syncForm(d: any) {
    if (!d) return;
    manualName = d.manual_name || "";
    owner = d.owner || "";
    category = d.category || "";
  }

  $: if (device?.id && device.id !== formDeviceId) {
    formDeviceId = device.id;
    syncForm(device);
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
        const errorText = await response.text();
        throw new Error(errorText);
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
        const errorText = await response.text();
        throw new Error(errorText);
      }
      const data = await response.json();
      if (data.device) {
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
    const diffDay = Math.floor(diffHr / 24);
    return `${diffDay}d ago`;
  }

  function pingStatus(): string {
    return device?.ping_status || (device?.online ? "online" : "unknown");
  }

  function pingLabel(): string {
    const s = pingStatus();
    if (s === "online") return "Online";
    if (s === "offline") return "Offline";
    return "Unknown";
  }

  function hasRecentDns(): boolean {
    const iso = device?.last_dns_query;
    if (!iso || iso.startsWith("0001-")) return false;
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return false;
    return Date.now() - date.getTime() < DNS_RECENT_MS;
  }

  function dnsActivityLabel(): string {
    const iso = device?.last_dns_query;
    if (!iso || iso.startsWith("0001-")) return "Never observed";
    if (hasRecentDns()) return "Active";
    return "Quiet";
  }

  $: pingOfflineButDns =
    pingStatus() !== "online" && !!device?.last_dns_query &&
    !String(device?.last_dns_query).startsWith("0001-");
  $: noIp = !device?.ipv4 && !device?.ipv6;
</script>

<ComposedModal {open} on:close={close} size="lg">
  <ModalHeader
    title="Device Details"
    label={device?.display_name || "Unknown Device"}
  />
  <ModalBody hasForm>
    {#if error}
      <div class="error-message">{error}</div>
    {/if}

    <FormGroup legendText="Assign a Name">
      <TextInput
        labelText="Display Name"
        placeholder="e.g., Vivienne's iPad"
        bind:value={manualName}
      />
      <TextInput
        labelText="Owner"
        placeholder="e.g., Vivienne, Dad"
        bind:value={owner}
        style="margin-top: 0.5rem;"
      />
      <TextInput
        labelText="Category"
        placeholder="e.g., kids, adults, iot"
        bind:value={category}
        style="margin-top: 0.5rem;"
      />
    </FormGroup>

    <div class="detail-heading">
      <h5>Reachability</h5>
      <Button
        kind="tertiary"
        size="small"
        disabled={pinging || noIp}
        on:click={pingNow}
      >
        {pinging ? "Pinging…" : "Ping now"}
      </Button>
    </div>
    <StructuredList condensed flush>
      <StructuredListHead>
        <StructuredListRow head>
          <StructuredListCell head>Property</StructuredListCell>
          <StructuredListCell head>Value</StructuredListCell>
        </StructuredListRow>
      </StructuredListHead>
      <StructuredListBody>
        <StructuredListRow>
          <StructuredListCell>Ping</StructuredListCell>
          <StructuredListCell>
            <span class="status-dot {pingStatus()}"></span>
            {pingLabel()}
            {#if pingStatus() === "online" && device?.ping_rtt_ms}
              <span class="muted">({device.ping_rtt_ms} ms)</span>
            {/if}
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>Last ping</StructuredListCell>
          <StructuredListCell>
            {formatDate(device?.last_ping)}
            {#if formatTimeAgo(device?.last_ping)}
              <span class="muted">({formatTimeAgo(device?.last_ping)})</span>
            {/if}
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>Address probed</StructuredListCell>
          <StructuredListCell
            >{device?.ipv4 || device?.ipv6 || "—"}</StructuredListCell
          >
        </StructuredListRow>
      </StructuredListBody>
    </StructuredList>
    {#if noIp}
      <p class="hint">No IP address is known for this device, so it cannot be pinged.</p>
    {:else if pingAvailable === false}
      <p class="hint">
        Ping is not available in this environment. Status stays unknown.
      </p>
    {:else if pingOfflineButDns}
      <p class="hint">
        This device did not respond to ping but has queried DNS — many phones and
        IoT devices ignore ICMP.
      </p>
    {:else if pingStatus() === "offline"}
      <p class="hint">Ping failed. The device may be off, on another network, or blocking ICMP.</p>
    {/if}

    <h5 class="section-title">DNS queries sent</h5>
    <StructuredList condensed flush>
      <StructuredListHead>
        <StructuredListRow head>
          <StructuredListCell head>Property</StructuredListCell>
          <StructuredListCell head>Value</StructuredListCell>
        </StructuredListRow>
      </StructuredListHead>
      <StructuredListBody>
        <StructuredListRow>
          <StructuredListCell>Status</StructuredListCell>
          <StructuredListCell>
            <Tag
              size="sm"
              type={dnsActivityLabel() === "Active"
                ? "teal"
                : dnsActivityLabel() === "Quiet"
                  ? "gray"
                  : "outline"}>{dnsActivityLabel()}</Tag
            >
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>Last DNS query</StructuredListCell>
          <StructuredListCell>
            {formatDate(device?.last_dns_query)}
            {#if formatTimeAgo(device?.last_dns_query)}
              <span class="muted">({formatTimeAgo(device?.last_dns_query)})</span>
            {/if}
          </StructuredListCell>
        </StructuredListRow>
      </StructuredListBody>
    </StructuredList>
    <p class="hint">
      This is when the device last asked GateSentry to resolve a name. Having an
      IP or a local DNS record does not count as a query. A quiet device can
      still be online.
    </p>

    <h5 class="section-title">Identity</h5>
    <StructuredList condensed flush>
      <StructuredListHead>
        <StructuredListRow head>
          <StructuredListCell head>Property</StructuredListCell>
          <StructuredListCell head>Value</StructuredListCell>
        </StructuredListRow>
      </StructuredListHead>
      <StructuredListBody>
        <StructuredListRow>
          <StructuredListCell>DNS Name</StructuredListCell>
          <StructuredListCell>{device?.dns_name || "—"}</StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>Hostnames</StructuredListCell>
          <StructuredListCell>
            {#if device?.hostnames?.length}
              {#each device.hostnames as h}
                <Tag size="sm" type="outline">{h}</Tag>
              {/each}
            {:else}
              —
            {/if}
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>mDNS Names</StructuredListCell>
          <StructuredListCell>
            {#if device?.mdns_names?.length}
              {#each device.mdns_names as m}
                <Tag size="sm" type="blue">{m}</Tag>
              {/each}
            {:else}
              —
            {/if}
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>IPv4</StructuredListCell>
          <StructuredListCell>{device?.ipv4 || "—"}</StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>IPv6</StructuredListCell>
          <StructuredListCell>
            <span class="ipv6-value">{device?.ipv6 || "—"}</span>
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>MAC Address(es)</StructuredListCell>
          <StructuredListCell>
            {#if device?.macs?.length}
              {#each device.macs as mac}
                <Tag size="sm" type="warm-gray">{mac}</Tag>
              {/each}
            {:else}
              —
            {/if}
          </StructuredListCell>
        </StructuredListRow>
      </StructuredListBody>
    </StructuredList>

    <h5 class="section-title">Discovery</h5>
    <StructuredList condensed flush>
      <StructuredListHead>
        <StructuredListRow head>
          <StructuredListCell head>Property</StructuredListCell>
          <StructuredListCell head>Value</StructuredListCell>
        </StructuredListRow>
      </StructuredListHead>
      <StructuredListBody>
        <StructuredListRow>
          <StructuredListCell>Primary Source</StructuredListCell>
          <StructuredListCell>
            <Tag
              size="sm"
              type={device?.source === "ddns"
                ? "green"
                : device?.source === "mdns"
                ? "blue"
                : device?.source === "passive"
                ? "warm-gray"
                : device?.source === "manual"
                ? "purple"
                : "gray"}>{device?.source || "—"}</Tag
            >
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>All Sources</StructuredListCell>
          <StructuredListCell>
            {#if device?.sources?.length}
              {#each device.sources as s}
                <Tag size="sm" type="outline">{s}</Tag>
              {/each}
            {:else}
              —
            {/if}
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>First Seen</StructuredListCell>
          <StructuredListCell
            >{formatDate(device?.first_seen)}</StructuredListCell
          >
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>Last Seen</StructuredListCell>
          <StructuredListCell>
            {formatDate(device?.last_seen)}
            {#if formatTimeAgo(device?.last_seen)}
              <span class="muted">({formatTimeAgo(device?.last_seen)})</span>
            {/if}
          </StructuredListCell>
        </StructuredListRow>
        <StructuredListRow>
          <StructuredListCell>Device ID</StructuredListCell>
          <StructuredListCell>
            <code style="font-size: 0.75rem;">{device?.id || "—"}</code>
          </StructuredListCell>
        </StructuredListRow>
      </StructuredListBody>
    </StructuredList>
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
  .status-dot {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    vertical-align: middle;
    margin-right: 0.25rem;
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
    margin-bottom: 1rem;
    font-size: 0.875rem;
  }
  .ipv6-value {
    word-break: break-all;
    font-size: 0.8125rem;
  }
  .section-title {
    margin-top: 1.5rem;
    margin-bottom: 0.5rem;
  }
  .detail-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-top: 1.5rem;
    margin-bottom: 0.5rem;
  }
  .detail-heading h5 {
    margin: 0;
  }
  .muted {
    color: #6f6f6f;
    font-size: 0.8125rem;
    margin-left: 0.35rem;
  }
  .hint {
    font-size: 0.8125rem;
    color: #525252;
    margin: 0.5rem 0 0 0;
  }
</style>
