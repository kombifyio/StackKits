<script lang="ts">
  import { agentPromptById } from '../content/agentPrompts'

  type Props = {
    promptID: string
    navigate: (path: string) => void
  }
  const { promptID, navigate }: Props = $props()

  const prompt = $derived(agentPromptById(promptID))
  let markdownBody = $state<string>('')
  let loading = $state(true)
  let error = $state<string | null>(null)

  $effect(() => {
    if (!prompt) {
      loading = false
      error = 'Prompt not found.'
      return
    }
    loading = true
    error = null
    fetch(prompt.mdPath)
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.text()
      })
      .then((text) => {
        markdownBody = text
        loading = false
      })
      .catch((e) => {
        error = String(e)
        loading = false
      })
  })

  const copyMarkdown = async () => {
    try {
      await navigator.clipboard.writeText(markdownBody)
    } catch {
      // user can copy from the rendered block
    }
  }
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
      <header class="mb-6 md:mb-8">
        <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Agent prompt</span>
        <h1 class="text-3xl md:text-4xl font-extrabold tracking-tight text-on-surface mt-2 mb-3">{prompt.title}</h1>
        <p class="text-lg text-on-surface-variant max-w-3xl">{prompt.summary}</p>
      </header>

      <div class="flex flex-wrap items-center gap-3 mb-6">
        <a href={prompt.mdPath} target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-2 bg-surface-container border border-outline-variant text-on-surface px-3 py-2 rounded-lg text-xs font-semibold hover:border-primary/40 no-underline transition-all">
          <span class="material-symbols-outlined text-sm leading-none">open_in_new</span>
          Open as Markdown
        </a>
        <button onclick={copyMarkdown} disabled={loading || !!error} class="inline-flex items-center gap-2 bg-primary text-on-primary px-3 py-2 rounded-lg text-xs font-semibold cursor-pointer disabled:opacity-50">
          <span class="material-symbols-outlined text-sm leading-none">content_copy</span>
          Copy full Markdown
        </button>
      </div>

      <div class="bg-surface-container-low border border-outline-variant rounded-2xl overflow-hidden">
        <div class="bg-surface-container-high border-b border-outline-variant px-4 py-2 flex items-center gap-2 text-xs text-on-surface-variant font-mono">
          <span class="material-symbols-outlined text-sm leading-none">description</span>
          {prompt.mdPath}
        </div>
        {#if loading}
          <div class="px-5 py-10 text-center text-on-surface-variant text-sm">Loading prompt…</div>
        {:else if error}
          <div class="px-5 py-10 text-center text-error text-sm">Could not load prompt: {error}</div>
        {:else}
          <pre class="px-5 py-5 overflow-x-auto font-mono text-sm leading-relaxed text-on-surface whitespace-pre-wrap"><code>{markdownBody}</code></pre>
        {/if}
      </div>
    {/if}
  </div>
</main>
