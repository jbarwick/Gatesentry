<script lang="ts">
  import { _ } from "svelte-i18n";
  import { onMount } from "svelte";
  import {
    Button,
    InlineNotification,
    TextArea,
    Tag,
  } from "carbon-components-svelte";
  import { Download } from "carbon-icons-svelte";
  import Toggle from "./toggle.svelte";
  import { store } from "../store/apistore";
  import { notificationstore } from "../store/notifications";
  import {
    createNotificationError,
    createNotificationSuccess,
  } from "../lib/utils";
  import { getBasePath } from "../lib/navigate";

  let enabled = "";
  let httpsPort = "";
  let serverCert = "";
  let serverKey = "";
  let caCert = "";
  let saving = "";

  const caDownload = getBasePath() + "/api/files/admin-https-ca";

  onMount(async () => {
    try {
      const about = await $store.api.doCall("/about");
      httpsPort = about.admin_https_port || "9877";
    } catch {
      httpsPort = "9877";
    }
    await loadPems();
  });

  async function loadPems() {
    const cert = await $store.api.getSetting("admin_https_certpem");
    const key = await $store.api.getSetting("admin_https_keypem");
    const ca = await $store.api.getSetting("admin_https_capem");
    serverCert = cert?.Value || "";
    serverKey = key?.Value || "";
    caCert = ca?.Value || "";
  }

  async function savePem(key: string, value: string, label: string) {
    saving = key;
    const response = await $store.api.setSetting(key, value);
    saving = "";
    if (response === false) {
      notificationstore.add(
        createNotificationError(
          { subtitle: $_("Unable to save") + " " + label },
          $_,
        ),
      );
      return;
    }
    notificationstore.add(
      createNotificationSuccess({ subtitle: label + " " + $_("saved") }, $_),
    );
    await loadPems();
  }

  function readFile(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result || ""));
      reader.onerror = () => reject(reader.error);
      reader.readAsText(file);
    });
  }

  async function onServerCertFile(e: Event) {
    const input = e.target as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    const text = await readFile(file);
    await savePem("admin_https_certpem", text, $_("Server certificate"));
  }

  async function onCAFile(e: Event) {
    const input = e.target as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    const text = await readFile(file);
    await savePem("admin_https_capem", text, $_("CA certificate"));
  }

  $: missingTLS =
    enabled === "true" && (!serverCert.trim() || !serverKey.trim());
</script>

<div class="admin-https">
  <p class="hint">
    {$_("HTTPS admin listens on port")}
    <strong>{httpsPort}</strong>
    ({$_("GS_ADMIN_PORT_SSL")}).
    {$_(
      "This CA is only for the admin console. It may be the same as the MITM CA, or a different one.",
    )}
  </p>

  <Toggle
    bind:settingValue={enabled}
    settingName="enable_admin_https"
    label={$_("Admin HTTPS Server")}
    labelA={$_("Disabled")}
    labelB={$_("Enabled")}
  />

  {#if missingTLS}
    <InlineNotification
      kind="warning"
      title={$_("Certificate required")}
      subtitle={$_(
        "Enable has no effect until a server certificate and private key are saved.",
      )}
      hideCloseButton
    />
  {/if}

  <div class="upload-block">
    <h6>{$_("Upload Server Certificate")}</h6>
    <p class="hint">
      {$_(
        "PEM file. If the file also contains the private key, it is stored automatically.",
      )}
    </p>
    <input
      type="file"
      accept=".pem,.crt,.cer,.key,.txt"
      on:change={onServerCertFile}
    />
    <TextArea
      labelText={$_("Server certificate (PEM)")}
      bind:value={serverCert}
      on:blur={() =>
        savePem("admin_https_certpem", serverCert, $_("Server certificate"))}
    />
    <TextArea
      labelText={$_("Server private key (PEM)")}
      bind:value={serverKey}
      on:blur={() =>
        savePem("admin_https_keypem", serverKey, $_("Server private key"))}
    />
  </div>

  <div class="upload-block">
    <h6>{$_("Upload CA Certificate")}</h6>
    <p class="hint">
      {$_(
        "The CA that signed the admin server certificate. Install this on browsers that open the HTTPS admin UI. Independent of MITM filtering.",
      )}
    </p>
    <div class="ca-row">
      <input type="file" accept=".pem,.crt,.cer,.txt" on:change={onCAFile} />
      {#if caCert}
        <a href={caDownload} target="_blank" class="dl">
          <Button size="small" kind="secondary" icon={Download}>
            {$_("Download CA Certificate")}
          </Button>
        </a>
        <Tag type="green" size="sm">{$_("Uploaded")}</Tag>
      {/if}
    </div>
    <TextArea
      labelText={$_("CA certificate (PEM)")}
      bind:value={caCert}
      on:blur={() => savePem("admin_https_capem", caCert, $_("CA certificate"))}
    />
  </div>

  {#if saving}
    <p class="hint">{$_("Saving…")}</p>
  {/if}
</div>

<style>
  .admin-https {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .hint {
    font-size: 0.875rem;
    color: #525252;
    margin: 0;
  }
  .upload-block h6 {
    margin: 0 0 0.35rem 0;
    font-size: 0.875rem;
    font-weight: 600;
  }
  .upload-block {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .ca-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .dl {
    text-decoration: none;
  }
</style>
