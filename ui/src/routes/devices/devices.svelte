<script lang="ts">
  import DeviceList from "./devicelist.svelte";
  import { Devices as DevicesIcon } from "carbon-icons-svelte";
</script>

<div class="gs-page-title">
  <DevicesIcon size={24} />
  <h2>Network Devices</h2>
</div>

<p class="dev-desc">
  Devices discovered via DNS queries, mDNS, and DDNS. Online/offline is ping
  when you open this page. The query tag is the last DNS request the device
  sent to GateSentry — not whether it has a name or address record.
</p>

<DeviceList />

<style>
  .dev-desc {
    font-size: 0.875rem;
    color: #525252;
    margin: 0 0 0.75rem 0;
  }
</style>
