<script lang="ts">
  type Line =
    | { kind: 'cmd'; text: string; comment?: string }
    | { kind: 'output'; text: string }
    | { kind: 'comment'; text: string }
    | { kind: 'blank' }

  type Props = {
    lines: Line[]
    title?: string
  }
  const { lines, title }: Props = $props()
</script>

<div class="rounded-2xl overflow-hidden border border-outline-variant shadow-2xl shadow-black/40">
  <div class="bg-surface-container-high border-b border-outline-variant px-4 py-2.5 flex items-center gap-3">
    <div class="flex gap-1.5">
      <span class="w-3 h-3 rounded-full bg-red-500/70"></span>
      <span class="w-3 h-3 rounded-full bg-yellow-500/70"></span>
      <span class="w-3 h-3 rounded-full bg-green-500/70"></span>
    </div>
    <span class="text-xs font-mono text-on-surface-variant ml-2 truncate">{title ?? 'stackkit'}</span>
  </div>
  <pre class="code-block-gradient text-sm md:text-[0.92rem] font-mono leading-relaxed px-5 py-5 overflow-x-auto"><code>{#each lines as line}{#if line.kind === 'cmd'}<span class="terminal-prompt">$</span> <span class="text-on-surface">{line.text}</span>{#if line.comment}    <span class="terminal-comment"># {line.comment}</span>{/if}
{:else if line.kind === 'output'}<span class="text-on-surface-variant">{line.text}</span>
{:else if line.kind === 'comment'}<span class="terminal-comment"># {line.text}</span>
{/if}{/each}</code></pre>
</div>
