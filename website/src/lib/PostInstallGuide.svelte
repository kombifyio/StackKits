<script lang="ts">
  type ServiceLink = {
    name: string
    url: string
    purpose: string
  }

  const serviceLinks: ServiceLink[] = [
    { name: 'Base Hub', url: 'http://base.home.localhost', purpose: 'Main dashboard and first setup' },
    { name: 'Auth', url: 'http://auth.home.localhost', purpose: 'TinyAuth login gateway' },
    { name: 'Identity', url: 'http://id.home.localhost/setup', purpose: 'PocketID passkey setup' },
    { name: 'Status', url: 'http://kuma.home.localhost', purpose: 'Uptime Kuma monitoring' },
  ]
</script>

<section class="py-2">
  <div class="flex flex-col md:flex-row md:items-start md:justify-between gap-4 mb-6">
    <div>
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">After install</span>
      <h2 class="text-xl md:text-2xl font-bold text-on-surface mt-1">The installer prints your Hub and service links.</h2>
    </div>
    <div class="text-sm text-on-surface-variant md:text-right md:max-w-sm">
      Default <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">*.home.localhost</code>
      links are target-local. Open them from the server context, or choose an explicit domain/LAN-DNS path for other devices.
    </div>
  </div>

  <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mb-5">
    <div class="bg-surface-container border border-outline-variant rounded-lg p-4">
      <div class="text-xs font-bold uppercase tracking-widest text-on-surface-variant mb-2">Primary Hub</div>
      <code class="font-mono text-sm md:text-base text-on-surface break-all">http://base.home.localhost</code>
      <p class="text-sm text-on-surface-variant mt-2">This is the first URL shown in the final success message.</p>
    </div>
    <div class="bg-surface-container border border-outline-variant rounded-lg p-4">
      <div class="text-xs font-bold uppercase tracking-widest text-on-surface-variant mb-2">Machine-readable summary</div>
      <code class="font-mono text-sm md:text-base text-on-surface break-all">~/my-homelab/.stackkit/access.json</code>
      <p class="text-sm text-on-surface-variant mt-2">Contains the Hub URL, service URLs, domain, mode, and setup evidence.</p>
    </div>
  </div>

  <div class="grid grid-cols-1 md:grid-cols-2 gap-3 mb-5">
    {#each serviceLinks as link}
      <div class="flex gap-3 bg-surface-container border border-outline-variant rounded-lg p-4">
        <span class="material-symbols-outlined text-primary text-lg leading-6">open_in_new</span>
        <div class="min-w-0">
          <div class="font-semibold text-on-surface">{link.name}</div>
          <code class="block font-mono text-xs text-on-surface-variant break-all mt-0.5">{link.url}</code>
          <p class="text-xs text-on-surface-variant mt-1">{link.purpose}</p>
        </div>
      </div>
    {/each}
  </div>

  <div class="bg-surface-container border border-outline-variant rounded-lg p-4">
    <div class="text-xs font-bold uppercase tracking-widest text-on-surface-variant mb-2">Checks after the message</div>
    <pre class="font-mono text-sm text-on-surface whitespace-pre-wrap"><code>cd ~/my-homelab
stackkit status
stackkit verify --http</code></pre>
    <p class="text-sm text-on-surface-variant mt-3">
      After PocketID owner setup, open the Base Hub and use <span class="font-semibold text-on-surface">Protect Base Hub</span>
      so the Hub and node-local API move behind TinyAuth.
    </p>
  </div>
</section>
