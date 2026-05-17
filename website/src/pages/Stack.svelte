<script lang="ts">
  import { worksWithRail } from '../content/worksWith'

  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()

  const groups: Array<{ label: string; items: typeof worksWithRail }> = [
    {
      label: 'Contracts & generation',
      items: worksWithRail.filter((i) => ['CUE', 'OpenTofu'].includes(i.name)),
    },
    {
      label: 'Runtime & routing',
      items: worksWithRail.filter((i) => ['Docker', 'Traefik', 'Coolify'].includes(i.name)),
    },
    {
      label: 'Identity',
      items: worksWithRail.filter((i) => ['PocketID', 'TinyAuth'].includes(i.name)),
    },
    {
      label: 'Apps & operations',
      items: worksWithRail.filter((i) => ['Immich', 'Vaultwarden', 'Uptime Kuma', 'Homepage', 'Kopia'].includes(i.name)),
    },
  ]
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-5xl mx-auto">
    <header class="mb-12">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Stack components</span>
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-4">Best-in-class open source, wired together.</h1>
      <p class="text-lg text-on-surface-variant max-w-3xl">StackKits doesn't reinvent the wheel — it picks proven open-source pieces and provisions them with a deterministic CUE-driven pipeline.</p>
    </header>

    {#each groups as group}
      <section class="mb-10">
        <h2 class="text-xs font-bold uppercase tracking-widest text-on-surface-variant mb-4">{group.label}</h2>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          {#each group.items as item}
            <a href={item.href ?? '#'} target={item.href ? '_blank' : undefined} rel={item.href ? 'noopener noreferrer' : undefined} class="block bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 no-underline transition-colors">
              <div class="flex items-center gap-3">
                <span class="w-11 h-11 rounded-lg bg-surface-container-highest text-primary font-bold font-mono flex items-center justify-center">{item.mark ?? item.name.charAt(0)}</span>
                <div>
                  <div class="font-semibold text-on-surface">{item.name}</div>
                  <div class="text-xs text-on-surface-variant">{item.detail}</div>
                </div>
              </div>
            </a>
          {/each}
        </div>
      </section>
    {/each}

    <section class="mt-12 pt-8 border-t border-outline-variant">
      <button onclick={() => navigate('/architecture')} class="inline-flex items-center gap-2 text-primary hover:underline font-semibold text-sm cursor-pointer">
        Architecture overview
        <span class="material-symbols-outlined text-base leading-none">arrow_forward</span>
      </button>
    </section>
  </div>
</main>
