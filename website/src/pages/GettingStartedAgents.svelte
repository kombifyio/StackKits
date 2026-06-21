<script lang="ts">
  import { agentPrompts, type AgentPrompt } from '../content/agentPrompts'
  import CubeCluster from '../lib/CubeCluster.svelte'

  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()

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

  const copyPrompt = async (prompt: AgentPrompt, variant: 'short' | 'full') => {
    const text = variant === 'short' ? prompt.shortPrompt : prompt.fullPrompt
    await copyText(text)
    copiedKey = `${prompt.id}:${variant}`
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copiedKey = ''), 1800)
  }

  const openDetails = (prompt: AgentPrompt) => navigate(`/getting-started/agents/${prompt.id}`)

  const scopeColor = (scope: string) =>
    scope === 'read-only'
      ? 'bg-tertiary-container text-on-tertiary-container'
      : scope === 'remote'
        ? 'bg-secondary-container text-on-secondary-container'
        : 'bg-warning-container text-on-warning-container'

  const mcpConfig = `{
  "mcpServers": {
    "stackkit": {
      "command": "stackkit-mcp",
      "args": ["--mode", "docs,local,server"]
    }
  }
}`
</script>

<main class="pt-24 md:pt-32 pb-24 px-6 max-w-7xl mx-auto">
  <header class="grid gap-10 lg:grid-cols-[minmax(0,1fr)_380px] lg:items-center">
    <div>
      <p class="text-primary font-bold tracking-widest uppercase text-xs mb-4">Agent prompts</p>
      <h1 class="text-4xl md:text-6xl font-extrabold tracking-tighter text-on-surface mb-6 max-w-4xl leading-[1.05]">
        Short prompts. Full context. Real rollouts.
      </h1>
      <p class="max-w-2xl text-lg leading-relaxed text-on-surface-variant">
        Drop a one-liner into your agent. It reads <a href="/llms-full.txt" class="text-primary hover:underline">llms-full.txt</a>, the <a href="/api/openapi.v1.yaml" class="text-primary hover:underline">OpenAPI contract</a>, the <a href="/schemas/stackkit-agent-run-manifest.schema.json" class="text-primary hover:underline">run-manifest schema</a>, and uses one <code class="font-mono text-base bg-surface-container-high px-1.5 py-0.5 rounded">stackkit</code> MCP connection to drive a verified rollout. Copy the full prompt when you want the exhaustive playbook, or click into details when you want to tune it.
      </p>
    </div>
    <div class="relative min-h-72 overflow-hidden rounded-2xl bg-surface-container-low border border-outline-variant p-6">
      <div class="absolute -right-6 -top-6 opacity-60">
        <CubeCluster size={260} variant="cluster" />
      </div>
      <div class="relative mt-28">
        <p class="mb-3 text-xs font-bold uppercase tracking-widest text-primary">Prompt shape</p>
        <p class="text-xl md:text-2xl font-bold leading-tight text-on-surface">
          stackkit.cc → current workspace → fresh host → verified BaseKit rollout
        </p>
      </div>
    </div>
  </header>

  <section aria-label="Copy-ready StackKits prompts" class="mt-12 grid gap-5 md:grid-cols-2">
    {#each agentPrompts as prompt}
      <article class="rounded-2xl border border-outline-variant bg-surface-container-low p-6 md:p-7 hover:border-primary/40 transition-colors flex flex-col">
        <div class="mb-5 flex flex-wrap items-center gap-2">
          <span class="text-xs font-bold uppercase tracking-widest text-primary">{prompt.title}</span>
          {#each prompt.scopes as scope}
            <span class="rounded-md px-2 py-0.5 text-[10px] font-bold uppercase tracking-widest {scopeColor(scope)}">{scope}</span>
          {/each}
        </div>
        <blockquote class="select-text text-lg md:text-xl font-bold leading-snug text-on-surface mb-5">
          “{prompt.shortPrompt}”
        </blockquote>
        <p class="text-sm text-on-surface-variant leading-relaxed mb-6 flex-1">{prompt.summary}</p>
        <div class="flex flex-wrap items-center gap-2.5">
          <button
            type="button"
            class="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-bold text-on-primary hover:shadow-lg hover:shadow-primary/20 active:scale-95 transition-all cursor-pointer"
            onclick={() => copyPrompt(prompt, 'full')}
          >
            <span class="material-symbols-outlined text-base leading-none">{copiedKey === `${prompt.id}:full` ? 'check' : 'content_copy'}</span>
            {copiedKey === `${prompt.id}:full` ? 'Copied' : 'Copy full prompt'}
          </button>
          <button
            type="button"
            class="inline-flex items-center gap-2 rounded-lg bg-surface-container border border-outline-variant px-3.5 py-2 text-xs font-bold text-on-surface hover:border-primary/40 hover:bg-surface-container-high active:scale-95 transition-all cursor-pointer"
            onclick={() => copyPrompt(prompt, 'short')}
            title="Copy the short one-liner"
          >
            <span class="material-symbols-outlined text-sm leading-none">{copiedKey === `${prompt.id}:short` ? 'check' : 'short_text'}</span>
            {copiedKey === `${prompt.id}:short` ? 'Copied' : 'Copy short'}
          </button>
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-lg bg-transparent px-3 py-2 text-xs font-bold text-primary hover:bg-surface-container-high transition-colors cursor-pointer"
            onclick={() => openDetails(prompt)}
          >
            Details
            <span class="material-symbols-outlined text-sm leading-none">arrow_outward</span>
          </button>
          <a
            href={prompt.markdownPath}
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1.5 rounded-lg bg-transparent px-3 py-2 text-xs font-bold text-on-surface-variant hover:text-primary hover:bg-surface-container-high transition-colors no-underline"
            title="Open the static Markdown file"
          >
            .md
            <span class="material-symbols-outlined text-sm leading-none">open_in_new</span>
          </a>
        </div>
      </article>
    {/each}
  </section>

  <p class="mt-4 max-w-3xl text-xs leading-relaxed text-on-surface-variant">
    Every full prompt asks the agent to fetch <a href="/llms-full.txt" class="text-primary hover:underline">/llms-full.txt</a>, validate against the JSON schemas, and treat generated artifacts as outputs. Edit the details page when your target host, owner email, or scope differs from the default.
  </p>

  <section class="mt-16 grid gap-6 lg:grid-cols-[0.95fr_1fr]">
    <div class="relative overflow-hidden rounded-2xl border border-outline-variant bg-secondary-container p-8">
      <div class="absolute -bottom-8 -left-8 opacity-25">
        <CubeCluster size={200} variant="pyramid" />
      </div>
      <div class="relative">
        <div class="mb-3 text-xs font-bold uppercase tracking-widest text-on-secondary-container/80">Agent flow</div>
        <h2 class="mb-5 text-2xl font-bold text-on-secondary-container">Let the agent read the framework context.</h2>
        <ol class="space-y-3 text-sm leading-relaxed text-on-secondary-container/90">
          <li><span class="font-bold text-secondary">1.</span> Start at <a href="/" class="font-bold text-secondary hover:underline">stackkit.cc</a>.</li>
          <li><span class="font-bold text-secondary">2.</span> Read <a href="/llms.txt" class="font-bold text-secondary hover:underline">/llms.txt</a> + <a href="/llms-full.txt" class="font-bold text-secondary hover:underline">/llms-full.txt</a> for full agent-readable context.</li>
          <li><span class="font-bold text-secondary">3.</span> Use <a href="/api/openapi.v1.yaml" class="font-bold text-secondary hover:underline">/api/openapi.v1.yaml</a> + <a href="/schemas/stackkit-agent-run-manifest.schema.json" class="font-bold text-secondary hover:underline">run-manifest schema</a> for the node-local API contract.</li>
          <li><span class="font-bold text-secondary">4.</span> Drive the rollout via the <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded text-on-surface">stackkit</code> MCP connection or the CLI; do not hand-edit generated artifacts.</li>
        </ol>
      </div>
    </div>

    <div class="rounded-2xl border border-outline-variant bg-surface-container-low p-8">
      <div class="mb-3 text-xs font-bold uppercase tracking-widest text-primary">MCP setup</div>
      <h2 class="mb-5 text-2xl font-bold text-on-surface">Add one StackKits MCP connection.</h2>
      <pre class="overflow-x-auto rounded-xl border border-outline-variant bg-surface-container-lowest p-5 text-xs text-on-surface font-mono"><code>{mcpConfig}</code></pre>
      <p class="mt-5 text-sm leading-relaxed text-on-surface-variant">
        The connection is named <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit</code>. Locally it starts the <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit-mcp</code> adapter; after install the same connector can be exposed as protected <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit-server /mcp</code>. Get a ready-to-paste config with <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit agent mcp-config</code>. Write tools stay disabled unless you set <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">STACKKIT_MCP_ALLOW_WRITE=true</code>.
      </p>
    </div>
  </section>

  <section class="mt-16 grid gap-5 md:grid-cols-3">
    <article class="rounded-2xl border border-outline-variant bg-surface-container p-6">
      <div class="mb-3 text-xs font-bold uppercase tracking-widest text-primary">Plain text</div>
      <h2 class="mb-3 text-lg font-bold text-on-surface">Agent-readable site</h2>
      <p class="text-sm leading-relaxed text-on-surface-variant"><code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">/llms.txt</code>, <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">/llms-full.txt</code>, and <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">/llms-snippets.txt</code> describe modes, install paths, CLI verbs, and copy-paste snippets without DOM rendering.</p>
    </article>
    <article class="rounded-2xl border border-outline-variant bg-surface-container p-6">
      <div class="mb-3 text-xs font-bold uppercase tracking-widest text-primary">Contracts</div>
      <h2 class="mb-3 text-lg font-bold text-on-surface">OpenAPI &amp; JSON Schemas</h2>
      <p class="text-sm leading-relaxed text-on-surface-variant">The node-local <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit-server</code> API plus run-manifest and functional-result schemas — same shape the CLI uses internally.</p>
    </article>
    <article class="rounded-2xl border border-outline-variant bg-surface-container p-6">
      <div class="mb-3 text-xs font-bold uppercase tracking-widest text-primary">MCP</div>
      <h2 class="mb-3 text-lg font-bold text-on-surface">Local agent bridge</h2>
      <p class="text-sm leading-relaxed text-on-surface-variant">15 tools across docs, local, server, and opt-in actions modes. Stdio by default; HTTP binds to 127.0.0.1.</p>
    </article>
  </section>

  <section class="mt-16 flex flex-wrap gap-3">
    <button onclick={() => navigate('/getting-started/cli')} class="inline-flex items-center gap-2 rounded-lg bg-primary px-5 py-3 text-sm font-bold text-on-primary hover:shadow-lg hover:shadow-primary/20 active:scale-95 transition-all cursor-pointer">
      CLI walkthrough
      <span class="material-symbols-outlined text-base leading-none">arrow_forward</span>
    </button>
    <button onclick={() => navigate('/getting-started')} class="inline-flex items-center gap-2 rounded-lg bg-surface-container border border-outline-variant px-5 py-3 text-sm font-bold text-on-surface hover:border-primary/40 transition-all cursor-pointer">
      Back to hub
    </button>
  </section>
</main>
