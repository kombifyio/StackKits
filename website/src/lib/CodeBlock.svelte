<script lang="ts">
  type Props = {
    code: string
    language?: string
    filename?: string
  }
  const { code, language = 'bash', filename }: Props = $props()
</script>

<div class="rounded-xl overflow-hidden border border-outline-variant">
  {#if filename}
    <div class="bg-surface-container-high border-b border-outline-variant px-4 py-2 flex items-center gap-2 text-xs text-on-surface-variant font-mono">
      <span class="material-symbols-outlined text-sm leading-none">description</span>
      {filename}
      <span class="ml-auto text-[10px] uppercase tracking-widest">{language}</span>
    </div>
  {/if}
  <pre class="code-block-gradient px-5 py-4 overflow-x-auto font-mono text-sm leading-relaxed text-on-surface"><code>{code}</code></pre>
</div>
