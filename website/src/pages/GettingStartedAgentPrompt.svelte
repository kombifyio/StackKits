<script lang="ts">
  import { agentPromptById } from '../content/agentPrompts'
  import CubeCluster from '../lib/CubeCluster.svelte'

  type Props = {
    promptID: string
    navigate: (path: string) => void
  }
  const { promptID, navigate }: Props = $props()

  const prompt = $derived(agentPromptById(promptID))

  let copiedKey = $state('')
  let copyTimer: ReturnType<typeof setTimeout> | null = null

  const copyText = async (text: string) => {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return
    }
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', 'true')
    ta.style.position = 'fixed'
    ta.style.left = '-9999px'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    ta.remove()
  }

  const copy = async (key: string, text: string) => {
    await copyText(text)
    copiedKey = key
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copiedKey = ''), 1800)
  }

  const scopeColor = (scope: string) =>
    scope === 'read-only'
      ? 'bg-tertiary-container text-on-tertiary-container'
      : scope === 'remote'
        ? 'bg-secondary-container text-on-secondary-container'
        : 'bg-warning-container text-on-warning-container'
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-4xl mx-auto">
    <nav class="text-xs text-on-surface-variant mb-6 flex gap-2 flex-wrap">
      <button onclick={() => navigate('/getting-started')} class="hover:text-primary cursor-pointer">Getting Started</button>
      <span>/</span>
      <button onclick={() => navigate('/getting-started/agents')} class="hover:text-primary cursor-pointer">Agents</button>
      <span>/</span>
      <span class="text-on-surface">{prompt?.title ?? promptID}</span>
    </nav>

    {#if !prompt}
      <h1 class="text-3xl font-bold text-on-surface mb-2">Prompt not found</h1>
      <p class="text-on-surface-variant mb-6">No prompt with id <code class="font-mono bg-surface-container-high px-2 py-0.5 rounded">{promptID}</code>.</p>
      <button onclick={() => navigate('/getting-started/agents')} class="bg-primary text-on-primary px-4 py-2 rounded-lg text-sm font-semibold cursor-pointer">Back to agents</button>
    {:else}
      <header class="relative mb-10">
        <div class="absolute -right-4 -top-6 opacity-30 hidden md:block">
          <CubeCluster size={180} variant="cluster" />
        </div>
        <div class="relative">
          <div class="mb-4 flex flex-wrap items-center gap-2">
            <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Agent prompt</span>
            {#each prompt.scopes as scope}
              <span class="rounded-md px-2 py-0.5 text-[10px] font-bold uppercase tracking-widest {scopeColor(scope)}">{scope}</span>
            {/each}
          </div>
          <h1 class="text-3xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-4 max-w-2xl">{prompt.title}</h1>
          <p class="text-lg text-on-surface-variant max-w-3xl">{prompt.summary}</p>
        </div>
      </header>

      <section class="mb-10">
        <div class="flex items-center justify-between mb-3">
          <div class="flex items-center gap-2">
            <span class="material-symbols-outlined text-primary text-base leading-none">short_text</span>
            <h2 class="text-sm font-bold uppercase tracking-widest text-primary">Short prompt</h2>
          </div>
          <button onclick={() => copy('short', prompt.shortPrompt)} class="inline-flex items-center gap-1.5 text-xs font-semibold text-on-surface-variant hover:text-primary cursor-pointer transition-colors">
            <span class="material-symbols-outlined text-sm leading-none">{copiedKey === 'short' ? 'check' : 'content_copy'}</span>
            {copiedKey === 'short' ? 'Copied' : 'Copy'}
          </button>
        </div>
        <blockquote class="select-text text-2xl md:text-3xl font-bold leading-snug text-on-surface bg-surface-container-low border-l-4 border-primary rounded-r-xl rounded-bl-xl px-6 py-5">
          “{prompt.shortPrompt}”
        </blockquote>
        <p class="mt-3 text-sm text-on-surface-variant leading-relaxed">
          Paste this into your agent. It treats <a href="/" class="text-primary hover:underline">stackkit.cc</a> as the framework context, fetches <a href="/llms-full.txt" class="text-primary hover:underline">/llms-full.txt</a>, and uses the local CLI plus <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit-mcp</code> to complete the task.
        </p>
      </section>

      <section class="mb-10">
        <div class="flex items-center justify-between mb-3">
          <div class="flex items-center gap-2">
            <span class="material-symbols-outlined text-on-surface-variant text-base leading-none">description</span>
            <h2 class="text-sm font-bold uppercase tracking-widest text-on-surface-variant">Full prompt</h2>
          </div>
          <button onclick={() => copy('full', prompt.fullPrompt)} class="inline-flex items-center gap-1.5 text-xs font-semibold text-on-surface hover:text-primary cursor-pointer transition-colors bg-primary/10 hover:bg-primary/20 px-3 py-1.5 rounded-md">
            <span class="material-symbols-outlined text-sm leading-none">{copiedKey === 'full' ? 'check' : 'content_copy'}</span>
            {copiedKey === 'full' ? 'Copied' : 'Copy full prompt'}
          </button>
        </div>
        <div class="rounded-xl border border-outline-variant bg-surface-container-low overflow-hidden">
          <div class="bg-surface-container-high border-b border-outline-variant px-4 py-2 flex items-center gap-2 text-xs text-on-surface-variant font-mono">
            <span class="material-symbols-outlined text-sm leading-none">description</span>
            {prompt.markdownPath}
          </div>
          <pre class="px-5 py-5 overflow-x-auto font-mono text-sm leading-relaxed text-on-surface whitespace-pre-wrap">{prompt.fullPrompt}</pre>
        </div>
      </section>

      <section class="grid grid-cols-1 md:grid-cols-3 gap-4 mb-10">
        <a href={prompt.markdownPath} target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 no-underline transition-colors">
          <div class="text-xs font-bold uppercase tracking-widest text-primary mb-2">Static Markdown</div>
          <p class="text-sm text-on-surface-variant leading-relaxed">Open the raw <code class="font-mono text-xs">.md</code> for direct ingestion into your own loader.</p>
        </a>
        <a href="/llms-full.txt" target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 no-underline transition-colors">
          <div class="text-xs font-bold uppercase tracking-widest text-primary mb-2">Site context</div>
          <p class="text-sm text-on-surface-variant leading-relaxed">Full agent-readable framework context at <code class="font-mono text-xs">/llms-full.txt</code>.</p>
        </a>
        <a href="/api/openapi.v1.yaml" target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 no-underline transition-colors">
          <div class="text-xs font-bold uppercase tracking-widest text-primary mb-2">OpenAPI</div>
          <p class="text-sm text-on-surface-variant leading-relaxed">Node-local API contract for <code class="font-mono text-xs">stackkit-server</code> — status, verify, doctor, logs.</p>
        </a>
      </section>

      <section class="flex flex-wrap gap-3">
        <button onclick={() => navigate('/getting-started/agents')} class="inline-flex items-center gap-2 bg-surface-container border border-outline-variant text-on-surface px-4 py-2.5 rounded-lg text-sm font-semibold cursor-pointer hover:border-primary/40 transition-all">
          <span class="material-symbols-outlined text-base leading-none">arrow_back</span>
          All prompts
        </button>
        <button onclick={() => navigate('/cli')} class="inline-flex items-center gap-2 text-on-surface-variant hover:text-primary text-sm font-semibold cursor-pointer transition-colors px-3 py-2.5">
          CLI Reference
          <span class="material-symbols-outlined text-base leading-none">arrow_forward</span>
        </button>
      </section>
    {/if}
  </div>
</main>
