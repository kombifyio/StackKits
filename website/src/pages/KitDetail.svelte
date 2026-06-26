<script lang="ts">
  import { kitById } from '../content/kits'
  import InstallBlock from '../lib/InstallBlock.svelte'
  import PostInstallGuide from '../lib/PostInstallGuide.svelte'
  import FeatureGrid from '../lib/FeatureGrid.svelte'

  type Props = {
    kit: 'base'
    navigate: (path: string) => void
  }
  const { kit: kitId, navigate }: Props = $props()
  const kit = $derived(kitById(kitId))

  const statusBadge = $derived(
    kit?.status === 'stable'
      ? 'bg-success-container text-on-success-container'
      : 'bg-warning-container text-on-warning-container'
  )
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-5xl mx-auto">
    {#if !kit}
      <h1 class="text-3xl font-bold text-on-surface">Kit not found.</h1>
    {:else}
      <nav class="text-xs text-on-surface-variant mb-6 flex gap-2">
        <button onclick={() => navigate('/')} class="hover:text-primary cursor-pointer">Home</button>
        <span>/</span>
        <span class="text-on-surface-variant">Kits</span>
        <span>/</span>
        <span class="text-on-surface">{kit.name}</span>
      </nav>

      <header class="mb-10">
        <div class="flex items-center gap-3 mb-4">
          <span class="text-[10px] font-bold uppercase tracking-widest px-2 py-1 rounded-md {statusBadge}">{kit.statusLabel}</span>
        </div>
        <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mb-3">{kit.name}</h1>
        <p class="text-xl text-on-surface-variant max-w-3xl">{kit.tagline}</p>
      </header>

      <section class="mb-10">
        <p class="text-on-surface-variant text-lg leading-relaxed max-w-3xl mb-6">{kit.description}</p>
        <InstallBlock command={kit.oneLiner} note={kit.status === 'alpha' ? 'Definition only — alpha rollout, not production.' : 'Run on the target server. Default *.home.localhost links are target-local.'} />
      </section>

      <section class="mb-12">
        <PostInstallGuide />
      </section>

      <section class="mb-12">
        <h2 class="text-2xl font-bold text-on-surface mb-5">Highlights</h2>
        <FeatureGrid features={kit.features} columns={3} />
      </section>

      <section class="grid grid-cols-1 md:grid-cols-2 gap-6 mb-12">
        <div class="bg-surface-container border border-outline-variant rounded-2xl p-6">
          <h3 class="font-bold text-on-surface mb-3 flex items-center gap-2">
            <span class="material-symbols-outlined text-primary">checklist</span>
            Services
          </h3>
          <ul class="space-y-2 text-sm text-on-surface-variant">
            {#each kit.services as svc}
              <li class="flex gap-2"><span class="text-primary">›</span> {svc}</li>
            {/each}
          </ul>
        </div>
        <div class="bg-surface-container border border-outline-variant rounded-2xl p-6">
          <h3 class="font-bold text-on-surface mb-3 flex items-center gap-2">
            <span class="material-symbols-outlined text-warning">warning</span>
            Not for this
          </h3>
          <ul class="space-y-2 text-sm text-on-surface-variant">
            {#each kit.notSuitableFor as item}
              <li class="flex gap-2"><span class="text-warning">›</span> {item}</li>
            {/each}
          </ul>
        </div>
      </section>

      <section class="bg-surface-container-low border border-outline-variant rounded-2xl p-6 md:p-8">
        <h2 class="text-xl font-bold text-on-surface mb-4">Initialize this kit</h2>
        <p class="text-sm text-on-surface-variant mb-4">If you already have the CLI installed, scaffold a deployment with:</p>
        <pre class="bg-surface-container border border-outline-variant rounded-xl px-4 py-3 overflow-x-auto font-mono text-sm text-on-surface"><code>{kit.initCommand}</code></pre>
        <div class="flex flex-wrap gap-3 mt-6">
          <button onclick={() => navigate('/getting-started/cli')} class="inline-flex items-center gap-2 bg-primary text-on-primary px-4 py-2.5 rounded-lg text-sm font-semibold cursor-pointer">
            <span class="material-symbols-outlined text-base leading-none">menu_book</span>
            CLI walkthrough
          </button>
          <button onclick={() => navigate('/cli')} class="inline-flex items-center gap-2 bg-surface-container border border-outline-variant text-on-surface px-4 py-2.5 rounded-lg text-sm font-semibold cursor-pointer hover:border-primary/40">
            Full CLI Reference
          </button>
        </div>
      </section>
    {/if}
  </div>
</main>
