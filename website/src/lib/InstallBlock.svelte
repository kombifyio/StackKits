<script lang="ts">
  type Props = {
    command: string
    note?: string
    label?: string
    variant?: 'hero' | 'inline'
  }
  let { command, note = '', label = 'Copy', variant = 'hero' }: Props = $props()

  let copied = $state(false)
  let timer: ReturnType<typeof setTimeout> | null = null

  const copy = async () => {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(command)
      } else {
        const ta = document.createElement('textarea')
        ta.value = command
        ta.style.position = 'fixed'
        ta.style.opacity = '0'
        document.body.appendChild(ta)
        ta.select()
        document.execCommand('copy')
        document.body.removeChild(ta)
      }
      copied = true
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => (copied = false), 1600)
    } catch {
      // swallow — user can still copy by hand
    }
  }
</script>

{#if variant === 'hero'}
  <div class="group relative">
    <div class="absolute -inset-px rounded-2xl bg-linear-to-r from-primary/40 via-primary-fixed-dim/30 to-primary/40 opacity-60 group-hover:opacity-100 blur-md transition-opacity"></div>
    <div class="relative bg-surface-container-low border border-outline-variant rounded-2xl px-5 py-4 flex items-center justify-between gap-4">
      <code class="font-mono text-base md:text-lg text-on-surface whitespace-nowrap overflow-x-auto scrollbar-thin">
        <span class="text-primary mr-2">$</span>{command}
      </code>
      <button onclick={copy} type="button" class="shrink-0 inline-flex items-center gap-1.5 bg-primary text-on-primary px-3 py-2 rounded-lg text-xs font-bold uppercase tracking-widest cursor-pointer hover:shadow-lg hover:shadow-primary/20 active:scale-95 transition-all">
        <span class="material-symbols-outlined text-sm leading-none">{copied ? 'check' : 'content_copy'}</span>
        {copied ? 'Copied' : label}
      </button>
    </div>
  </div>
  {#if note}
    <p class="text-xs text-on-surface-variant text-center mt-3">{note}</p>
  {/if}
{:else}
  <div class="bg-surface-container-low border border-outline-variant rounded-xl px-4 py-3 flex items-center justify-between gap-3">
    <code class="font-mono text-sm text-on-surface whitespace-nowrap overflow-x-auto">
      <span class="text-primary mr-1">$</span>{command}
    </code>
    <button onclick={copy} type="button" class="shrink-0 inline-flex items-center gap-1 text-on-surface-variant hover:text-primary text-xs font-medium cursor-pointer transition-colors">
      <span class="material-symbols-outlined text-sm leading-none">{copied ? 'check' : 'content_copy'}</span>
      {copied ? 'Copied' : 'Copy'}
    </button>
  </div>
{/if}
