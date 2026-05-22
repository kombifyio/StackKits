<script lang="ts">
  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()

  const layers = [
    {
      title: 'CUE contracts',
      subtitle: 'Source of truth',
      icon: 'integration_instructions',
      body: 'Module schemas, defaults, constraints, and deployment shape live in CUE. When the generated output needs to change, fix CUE — never the artifact.',
    },
    {
      title: 'Composition engine',
      subtitle: 'Deterministic merge',
      icon: 'merge',
      body: 'The composition engine resolves StackSpec + CUE contracts + registry mirror into a fully evaluated graph. Same inputs always produce the same artifacts.',
    },
    {
      title: 'Generated artifacts',
      subtitle: 'Disposable outputs',
      icon: 'output',
      body: 'OpenTofu, Docker Compose, tfvars, and rollout scripts are deterministic outputs. Hand-edits are forbidden and would be overwritten on the next generate.',
    },
    {
      title: 'Packaged OpenTofu',
      subtitle: 'Vendor-locked provisioner',
      icon: 'rocket_launch',
      body: 'A pinned OpenTofu binary ships with the CLI. No global tofu install required, and no version drift across nodes.',
    },
    {
      title: 'Runtime layer',
      subtitle: 'Docker + Traefik + Coolify',
      icon: 'lan',
      body: 'BaseKit runs containers through Docker. Coolify is the target platform baseline; the current verified local path still uses a StackKit-owned routing fallback until Coolify-managed application-layer rollout is complete.',
    },
    {
      title: 'Node-local API',
      subtitle: 'stackkit-server',
      icon: 'api',
      body: 'A read-mostly HTTP API runs on every node, exposing manifest visibility and setup-action endpoints. The OpenAPI contract lives at /api/openapi.v1.yaml.',
    },
    {
      title: 'Agent surfaces',
      subtitle: 'stackkit-mcp, llms.txt, schemas',
      icon: 'smart_toy',
      body: 'A local MCP connector plus public llms.txt and JSON Schemas give coding agents a deterministic, machine-readable rollout path.',
    },
  ]
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-5xl mx-auto">
    <header class="mb-12">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Architecture</span>
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-4">CUE in, routed homelab out.</h1>
      <p class="text-lg text-on-surface-variant max-w-3xl">StackKits compiles declarative CUE contracts into deterministic OpenTofu plans, applies them through Docker, and exposes node-local APIs plus agent-readable surfaces. Every layer is replaceable; none of them keep secrets you can't see.</p>
    </header>

    <section class="space-y-4 mb-14">
      {#each layers as layer, i}
        <div class="flex gap-4 md:gap-6 items-start bg-surface-container border border-outline-variant rounded-2xl p-5 md:p-6 hover:border-primary/40 transition-colors">
          <div class="shrink-0 w-12 h-12 rounded-xl bg-primary-container/40 text-primary flex items-center justify-center font-bold font-mono">
            {String(i + 1).padStart(2, '0')}
          </div>
          <div class="flex-1">
            <div class="flex items-center gap-3 mb-1">
              <span class="material-symbols-outlined text-primary">{layer.icon}</span>
              <h3 class="font-bold text-on-surface">{layer.title}</h3>
              <span class="text-xs text-on-surface-variant ml-auto">{layer.subtitle}</span>
            </div>
            <p class="text-sm text-on-surface-variant leading-relaxed">{layer.body}</p>
          </div>
        </div>
      {/each}
    </section>

    <section class="bg-surface-container-low border border-outline-variant rounded-2xl p-6 md:p-8 mb-12">
      <h2 class="text-xl font-bold text-on-surface mb-4">Repository layout</h2>
      <pre class="font-mono text-sm text-on-surface bg-surface-container border border-outline-variant rounded-xl px-5 py-4 overflow-x-auto leading-relaxed"><code>base/                 shared CUE schemas and foundation contracts
base-kit/             verified single-environment StackKit
modern-homelab/       hybrid local + cloud workstream
ha-kit/               high-availability workstream
modules/              atomic service module contracts
addons/               optional composable capabilities
cmd/                  stackkit, stackkit-server, stackkit-mcp, backup binaries
internal/             Go implementation packages
api/openapi/          OpenAPI contract source for stackkit-server
docs/                 Tier-3 repo documentation
tests/                integration, VM, production-style, scenario tests
website/              static stackkit.cc source</code></pre>
    </section>

    <section>
      <h2 class="text-xl font-bold text-on-surface mb-3">Read more</h2>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <button onclick={() => navigate('/stack')} class="text-left bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 cursor-pointer transition-colors">
          <h3 class="font-semibold text-on-surface mb-1">Stack components</h3>
          <p class="text-sm text-on-surface-variant">What runs inside a BaseKit deployment.</p>
        </button>
        <button onclick={() => navigate('/kits/base')} class="text-left bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 cursor-pointer transition-colors">
          <h3 class="font-semibold text-on-surface mb-1">BaseKit details</h3>
          <p class="text-sm text-on-surface-variant">Services, links, and scope of the release.</p>
        </button>
        <a href="/api/openapi.v1.yaml" target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 no-underline transition-colors">
          <h3 class="font-semibold text-on-surface mb-1">OpenAPI contract</h3>
          <p class="text-sm text-on-surface-variant">Raw YAML for the node-local API.</p>
        </a>
      </div>
    </section>
  </div>
</main>
