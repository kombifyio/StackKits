<script lang="ts">
  import InstallBlock from '../lib/InstallBlock.svelte'
  import TerminalBlock from '../lib/TerminalBlock.svelte'
  import KitCard from '../lib/KitCard.svelte'
  import FeatureGrid from '../lib/FeatureGrid.svelte'
  import WorksWithMarquee from '../lib/WorksWithMarquee.svelte'
  import CubeCluster from '../lib/CubeCluster.svelte'
  import { kits } from '../content/kits'
  import { worksWithRail } from '../content/worksWith'

  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()

  const releaseNotes = __STACKKIT_LATEST_RELEASE_NOTES__
  const displayVersion = __STACKKIT_DISPLAY_VERSION__
  const installOneLiner = __STACKKIT_INSTALL_ONELINER__

  const heroFeatures = [
    { icon: 'integration_instructions', title: 'CUE is the contract', body: 'Schemas, defaults, and shape live in CUE. Rollout artifacts are deterministic outputs.' },
    { icon: 'rocket_launch', title: 'OpenTofu under the hood', body: 'Packaged OpenTofu does the heavy lifting. You never touch a tfvars file.' },
    { icon: 'language', title: '*.home.localhost', body: 'Browser-native local DNS. No hosts edits, no certs, no port suffixes.' },
    { icon: 'smart_toy', title: 'Agent-first', body: 'llms.txt, OpenAPI, JSON schemas, MCP connector, prompt Markdown — all in the release.' },
    { icon: 'verified_user', title: 'Identity baked in', body: 'PocketID, TinyAuth, and a routed dashboard from the first apply.' },
    { icon: 'open_in_new', title: 'Apache-2.0 OSS', body: 'CLI defaults: no telemetry, no lock-in. Run it on your hardware, on your terms.' },
  ]

  const heroLines = [
    { kind: 'comment' as const, text: 'Bootstrap a fresh Ubuntu VM into a routed homelab' },
    { kind: 'cmd' as const, text: installOneLiner },
    { kind: 'output' as const, text: '==> Installing stackkit, stackkit-server, packaged OpenTofu...' },
    { kind: 'output' as const, text: '==> Bootstrapping BaseKit defaults...' },
    { kind: 'output' as const, text: '==> apply: 18 services routed, 2 platforms configured' },
    { kind: 'blank' as const },
    { kind: 'comment' as const, text: 'Hub is live at the browser-native local link' },
    { kind: 'cmd' as const, text: 'open http://base.home.localhost' },
  ]
</script>

<main class="pt-24 md:pt-32 pb-12">
  <section class="relative max-w-7xl mx-auto px-6 mb-20 md:mb-28">
    <div class="absolute inset-0 hero-glow pointer-events-none -z-10"></div>
    <div class="absolute -right-12 top-8 opacity-15 pointer-events-none -z-10 hidden lg:block">
      <CubeCluster size={420} variant="cluster" />
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-12 gap-10 md:gap-12 items-center">
      <div class="lg:col-span-7">
        <div class="inline-flex items-center gap-2 px-3 py-1 bg-primary-container/40 border border-primary/30 rounded-full mb-6">
          <span class="w-1.5 h-1.5 rounded-full bg-primary animate-pulse"></span>
          <span class="text-[11px] font-bold tracking-widest uppercase text-on-primary-container">{displayVersion} · BaseKit verified beta</span>
        </div>

        <h1 class="text-4xl md:text-6xl lg:text-7xl font-extrabold tracking-tighter text-on-surface leading-[1.05] mb-6">
          Your homelab.<br />
          <span class="text-primary">One command.</span>
        </h1>

        <p class="text-lg md:text-xl text-on-surface-variant max-w-2xl leading-relaxed mb-8">
          <strong class="text-on-surface">kombify StackKits</strong> is a declarative infrastructure blueprint system. CUE is the source of truth, OpenTofu does the provisioning, and a single shell command brings up a routed homelab with identity, dashboard, photos, and backups.
        </p>

        <div class="mb-4">
          <InstallBlock command={installOneLiner} note="Verified beta BaseKit one-liner — Linux or macOS host, root/sudo access." />
        </div>

        <div class="flex flex-wrap items-center gap-3 mt-6">
          <button onclick={() => navigate('/getting-started')} class="inline-flex items-center gap-2 bg-surface-container border border-outline-variant text-on-surface px-5 py-2.5 rounded-lg text-sm font-semibold hover:border-primary/40 hover:bg-surface-container-high cursor-pointer transition-all">
            <span class="material-symbols-outlined text-base leading-none">menu_book</span>
            Getting Started
          </button>
          <a href={__OSS_REPO__} target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-2 text-on-surface-variant hover:text-on-surface px-3 py-2.5 text-sm font-semibold no-underline transition-colors">
            <span class="material-symbols-outlined text-base leading-none">code</span>
            View on GitHub
          </a>
        </div>
      </div>

      <div class="lg:col-span-5">
        <TerminalBlock title="base.stackkit.cc | sh" lines={heroLines} />
      </div>
    </div>
  </section>

  <section class="max-w-7xl mx-auto px-6 mb-20 md:mb-24">
    <div class="text-center mb-10">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Works with</span>
      <h2 class="text-2xl md:text-3xl font-bold text-on-surface mt-2">Open standards, open source, open homelab.</h2>
    </div>
    <WorksWithMarquee items={worksWithRail} />
  </section>

  {#if releaseNotes && releaseNotes.length}
    <section class="max-w-5xl mx-auto px-6 mb-20 md:mb-24">
      <div class="bg-surface-container border border-outline-variant rounded-2xl p-6 md:p-8">
        <div class="flex items-center justify-between mb-5">
          <div>
            <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Latest release</span>
            <h2 class="text-xl md:text-2xl font-bold text-on-surface mt-1">What's new</h2>
          </div>
          <a href="/changelog" onclick={(e) => { e.preventDefault(); navigate('/changelog') }} class="text-sm font-semibold text-primary hover:underline no-underline">
            Full changelog →
          </a>
        </div>
        <ul class="grid grid-cols-1 md:grid-cols-2 gap-4">
          {#each releaseNotes as note}
            <li class="border-l-2 border-primary pl-4">
              <h3 class="text-sm font-semibold text-on-surface mb-1">{note.title}</h3>
              <p class="text-sm text-on-surface-variant leading-relaxed">{note.body}</p>
            </li>
          {/each}
        </ul>
      </div>
    </section>
  {/if}

  <section class="max-w-7xl mx-auto px-6 mb-20 md:mb-24">
    <div class="text-center mb-10 md:mb-12">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Choose your StackKit</span>
      <h2 class="text-3xl md:text-4xl font-bold text-on-surface mt-2 mb-3">Three compositions, one CLI.</h2>
      <p class="text-on-surface-variant max-w-2xl mx-auto">BaseKit is the verified one-click path for this release. Modern and HA Kit ship as alpha definitions for preview while their rollout matrices graduate.</p>
    </div>
    <div class="grid grid-cols-1 md:grid-cols-3 gap-5">
      {#each kits as kit}
        <KitCard {kit} {navigate} />
      {/each}
    </div>
  </section>

  <section class="max-w-7xl mx-auto px-6 mb-20 md:mb-24">
    <div class="text-center mb-10 md:mb-12">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Why StackKits</span>
      <h2 class="text-3xl md:text-4xl font-bold text-on-surface mt-2 mb-3">Built for engineers who self-host.</h2>
      <p class="text-on-surface-variant max-w-2xl mx-auto">A CLI that treats your homelab the way good infra tools treat production: deterministic, declarative, and resumable.</p>
    </div>
    <FeatureGrid features={heroFeatures} columns={3} />
  </section>

  <section class="max-w-7xl mx-auto px-6 mb-20 md:mb-24">
    <div class="bg-surface-container-low border border-outline-variant rounded-3xl overflow-hidden">
      <div class="grid grid-cols-1 lg:grid-cols-12">
        <div class="lg:col-span-5 p-8 md:p-10 flex flex-col justify-center">
          <span class="text-[11px] font-bold tracking-widest uppercase text-primary mb-3">CLI overview</span>
          <h2 class="text-2xl md:text-3xl font-bold text-on-surface mb-3">A short, predictable surface.</h2>
          <p class="text-on-surface-variant mb-6 leading-relaxed">
            <code class="font-mono text-sm text-on-surface bg-surface-container-high px-2 py-0.5 rounded">init</code>, <code class="font-mono text-sm text-on-surface bg-surface-container-high px-2 py-0.5 rounded">prepare</code>, <code class="font-mono text-sm text-on-surface bg-surface-container-high px-2 py-0.5 rounded">generate</code>, <code class="font-mono text-sm text-on-surface bg-surface-container-high px-2 py-0.5 rounded">plan</code>, <code class="font-mono text-sm text-on-surface bg-surface-container-high px-2 py-0.5 rounded">apply</code>, <code class="font-mono text-sm text-on-surface bg-surface-container-high px-2 py-0.5 rounded">verify</code>. Every command is non-destructive until <code class="font-mono text-sm text-on-surface bg-surface-container-high px-2 py-0.5 rounded">apply</code>.
          </p>
          <div class="flex flex-wrap gap-3">
            <button onclick={() => navigate('/cli')} class="inline-flex items-center gap-2 bg-primary text-on-primary px-4 py-2.5 rounded-lg text-sm font-semibold cursor-pointer hover:shadow-lg hover:shadow-primary/20 transition-all">
              <span class="material-symbols-outlined text-base leading-none">terminal</span>
              CLI Reference
            </button>
            <button onclick={() => navigate('/getting-started/cli')} class="inline-flex items-center gap-2 text-on-surface-variant hover:text-on-surface text-sm font-semibold cursor-pointer transition-colors">
              Manual install walkthrough →
            </button>
          </div>
        </div>
        <div class="lg:col-span-7 p-6 md:p-8 bg-surface-container">
          <TerminalBlock
            title="stackkit"
            lines={[
              { kind: 'comment', text: 'Core workflow' },
              { kind: 'cmd', text: 'stackkit init base-kit', comment: 'create stack-spec.yaml' },
              { kind: 'cmd', text: 'stackkit prepare', comment: 'Docker + packaged OpenTofu' },
              { kind: 'cmd', text: 'stackkit generate', comment: 'CUE → rollout artifacts' },
              { kind: 'cmd', text: 'stackkit plan', comment: 'preview changes' },
              { kind: 'cmd', text: 'stackkit apply --verify', comment: 'deploy + post-checks' },
              { kind: 'cmd', text: 'stackkit status', comment: 'service health' },
              { kind: 'blank' },
              { kind: 'comment', text: 'Agent surfaces' },
              { kind: 'cmd', text: 'stackkit agent prompt basekit-autonomous-rollout' },
              { kind: 'cmd', text: 'stackkit agent mcp-config' },
            ]}
          />
        </div>
      </div>
    </div>
  </section>

  <section class="max-w-5xl mx-auto px-6 mb-20 md:mb-24">
    <div class="relative overflow-hidden bg-linear-to-br from-primary-container/50 to-surface-container border border-primary/30 rounded-3xl p-8 md:p-12 text-center">
      <div class="absolute -left-8 -top-8 opacity-25 pointer-events-none">
        <CubeCluster size={200} variant="pyramid" />
      </div>
      <div class="absolute -right-8 -bottom-12 opacity-20 pointer-events-none">
        <CubeCluster size={220} variant="cluster" />
      </div>
      <div class="relative">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">For agents &amp; LLMs</span>
      <h2 class="text-3xl md:text-4xl font-bold text-on-surface mt-2 mb-3">First-class autonomous rollout.</h2>
      <p class="text-on-surface-variant max-w-2xl mx-auto mb-7 leading-relaxed">
        Every release ships <code class="font-mono text-sm bg-surface-container px-2 py-0.5 rounded">llms.txt</code>, prompt Markdown, OpenAPI, JSON schemas, and one <code class="font-mono text-sm bg-surface-container px-2 py-0.5 rounded">stackkit</code> MCP connection through the local adapter. Agents get a deterministic, idempotent path from zero to a verified rollout.
      </p>
      <div class="flex flex-wrap items-center justify-center gap-3">
        <button onclick={() => navigate('/getting-started/agents')} class="inline-flex items-center gap-2 bg-primary text-on-primary px-5 py-3 rounded-lg text-sm font-semibold cursor-pointer hover:shadow-lg hover:shadow-primary/20 transition-all">
          <span class="material-symbols-outlined text-base leading-none">smart_toy</span>
          Agent Getting Started
        </button>
        <a href="/llms.txt" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-2 text-on-surface-variant hover:text-primary text-sm font-semibold no-underline transition-colors">
          <span class="material-symbols-outlined text-base leading-none">description</span>
          Open llms.txt
        </a>
      </div>
      </div>
    </div>
  </section>
</main>
