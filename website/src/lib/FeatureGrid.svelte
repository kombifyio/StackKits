<script lang="ts">
  type Feature = {
    icon: string
    title: string
    body: string
  }

  type Props = {
    features: Feature[]
    columns?: 2 | 3
  }
  const { features, columns = 3 }: Props = $props()

  const gridClass = $derived(
    columns === 3
      ? 'grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 md:gap-5'
      : 'grid grid-cols-1 sm:grid-cols-2 gap-4 md:gap-5'
  )
</script>

<div class={gridClass}>
  {#each features as feature}
    <div class="bg-surface-container border border-outline-variant rounded-2xl p-5 md:p-6 hover:border-primary/40 transition-colors">
      <div class="w-10 h-10 rounded-lg bg-primary-container/40 text-primary flex items-center justify-center mb-4">
        <span class="material-symbols-outlined">{feature.icon}</span>
      </div>
      <h3 class="font-semibold text-on-surface mb-1.5">{feature.title}</h3>
      <p class="text-sm text-on-surface-variant leading-relaxed">{feature.body}</p>
    </div>
  {/each}
</div>
