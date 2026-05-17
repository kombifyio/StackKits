<script lang="ts">
  import type { WorksWithItem } from '../content/worksWith'

  type Props = { items: WorksWithItem[] }
  const { items }: Props = $props()

  const track = $derived([...items, ...items])
</script>

<div class="works-with-marquee">
  <div class="works-with-marquee__track flex gap-3 md:gap-4">
    {#each track as item, i}
      <a
        href={item.href ?? '#'}
        target={item.href ? '_blank' : undefined}
        rel={item.href ? 'noopener noreferrer' : undefined}
        class="shrink-0 flex items-center gap-3 bg-surface-container border border-outline-variant rounded-xl px-4 py-3 hover:border-primary/40 hover:bg-surface-container-high transition-colors no-underline group"
        aria-hidden={i >= items.length ? 'true' : undefined}
      >
        <span class="w-9 h-9 rounded-md bg-surface-container-highest text-primary font-bold font-mono flex items-center justify-center text-sm">
          {item.mark ?? item.name.charAt(0)}
        </span>
        <span class="flex flex-col">
          <span class="text-sm font-semibold text-on-surface leading-tight">{item.name}</span>
          <span class="text-[11px] text-on-surface-variant leading-tight">{item.detail}</span>
        </span>
      </a>
    {/each}
  </div>
</div>
