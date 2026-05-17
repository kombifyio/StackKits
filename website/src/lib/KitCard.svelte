<script lang="ts">
  import type { Kit } from '../content/kits'

  type Props = {
    kit: Kit
    navigate: (path: string) => void
  }
  const { kit, navigate }: Props = $props()

  const statusBadgeClass = $derived(
    kit.status === 'stable'
      ? 'bg-success-container text-on-success-container'
      : 'bg-warning-container text-on-warning-container'
  )

  const handleClick = (e: MouseEvent) => {
    e.preventDefault()
    navigate(`/kits/${kit.id}`)
  }
</script>

<a href={`/kits/${kit.id}`} onclick={handleClick} class="group block bg-surface-container border border-outline-variant rounded-2xl p-6 hover:border-primary/60 hover:bg-surface-container-high transition-all no-underline">
  <div class="flex items-center justify-between mb-3">
    <h3 class="text-lg font-bold text-on-surface">{kit.name}</h3>
    <span class="text-[10px] font-bold uppercase tracking-widest px-2 py-1 rounded-md {statusBadgeClass}">{kit.statusLabel}</span>
  </div>
  <p class="text-sm text-on-surface-variant mb-4 leading-relaxed">{kit.tagline}</p>
  <div class="bg-surface-container-low border border-outline-variant rounded-lg px-3 py-2 font-mono text-xs text-on-surface overflow-x-auto">
    <span class="terminal-prompt">$</span> {kit.oneLiner}
  </div>
  <div class="mt-4 inline-flex items-center gap-1.5 text-xs font-semibold text-primary group-hover:gap-3 transition-all">
    Read details
    <span class="material-symbols-outlined text-sm leading-none">arrow_forward</span>
  </div>
</a>
