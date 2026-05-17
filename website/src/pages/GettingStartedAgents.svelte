<script lang="ts">
  import { agentPrompts } from '../content/agentPrompts'

  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-5xl mx-auto">
    <nav class="text-xs text-on-surface-variant mb-6 flex gap-2">
      <button onclick={() => navigate('/getting-started')} class="hover:text-primary cursor-pointer">Getting Started</button>
      <span>/</span>
      <span class="text-on-surface">Agents</span>
    </nav>

    <header class="mb-10">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">For agents &amp; LLMs</span>
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-4">Autonomous rollout, by design.</h1>
      <p class="text-lg text-on-surface-variant max-w-3xl">StackKits is agent-ready through four surfaces that ship with every release: static public docs, deterministic CLI commands, a node-local HTTP API, and a local MCP connector installed beside the CLI.</p>
    </header>

    <section class="grid grid-cols-1 md:grid-cols-2 gap-5 mb-12">
      <a href="/llms.txt" target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-2xl p-6 hover:border-primary/40 no-underline transition-colors group">
        <div class="flex items-center gap-3 mb-3">
          <span class="material-symbols-outlined text-primary">description</span>
          <span class="font-bold text-on-surface">llms.txt + variants</span>
        </div>
        <p class="text-sm text-on-surface-variant mb-3">Three plain-text entrypoints: index, full context, and copy-paste snippets. Crawlable, cacheable, agent-friendly.</p>
        <ul class="text-xs text-on-surface-variant space-y-1 font-mono">
          <li>/llms.txt</li>
          <li>/llms-full.txt</li>
          <li>/llms-snippets.txt</li>
        </ul>
      </a>

      <a href="/api/openapi.v1.yaml" target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-2xl p-6 hover:border-primary/40 no-underline transition-colors">
        <div class="flex items-center gap-3 mb-3">
          <span class="material-symbols-outlined text-primary">api</span>
          <span class="font-bold text-on-surface">OpenAPI &amp; schemas</span>
        </div>
        <p class="text-sm text-on-surface-variant mb-3">Node-local <code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit-server</code> API plus JSON Schemas for run manifests and functional results.</p>
        <ul class="text-xs text-on-surface-variant space-y-1 font-mono">
          <li>/api/openapi.v1.yaml</li>
          <li>/schemas/stackkit-agent-run-manifest.schema.json</li>
          <li>/schemas/stackkit-agent-functional-result.schema.json</li>
        </ul>
      </a>

      <a href="/mcp/stackkit-mcp.md" target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-2xl p-6 hover:border-primary/40 no-underline transition-colors">
        <div class="flex items-center gap-3 mb-3">
          <span class="material-symbols-outlined text-primary">hub</span>
          <span class="font-bold text-on-surface">MCP connector</span>
        </div>
        <p class="text-sm text-on-surface-variant mb-3"><code class="font-mono text-xs bg-surface-container-high px-1.5 py-0.5 rounded">stackkit-mcp</code> ships beside the CLI. Get a ready-to-paste client config with one command:</p>
        <pre class="bg-surface-container-low border border-outline-variant rounded-lg px-3 py-2 text-xs font-mono text-on-surface overflow-x-auto"><code>stackkit agent mcp-config</code></pre>
      </a>

      <a href="/getting-started/agents.md" target="_blank" rel="noopener noreferrer" class="block bg-surface-container border border-outline-variant rounded-2xl p-6 hover:border-primary/40 no-underline transition-colors">
        <div class="flex items-center gap-3 mb-3">
          <span class="material-symbols-outlined text-primary">smart_toy</span>
          <span class="font-bold text-on-surface">Agent guide (Markdown)</span>
        </div>
        <p class="text-sm text-on-surface-variant">Same content as this page in agent-readable Markdown — wire it into your own loader, RAG, or context store.</p>
      </a>
    </section>

    <section class="mb-12">
      <h2 class="text-2xl font-bold text-on-surface mb-2">Published prompts</h2>
      <p class="text-on-surface-variant mb-6 max-w-3xl">Copy-ready prompts for the most common scenarios. Each is also available as Markdown for direct ingestion.</p>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        {#each agentPrompts as p}
          <button onclick={() => navigate('/getting-started/agents/' + p.id)} class="text-left bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 hover:bg-surface-container-high cursor-pointer transition-all">
            <h3 class="font-semibold text-on-surface mb-1.5">{p.title}</h3>
            <p class="text-sm text-on-surface-variant leading-relaxed mb-3">{p.summary}</p>
            <span class="inline-flex items-center gap-1.5 text-xs font-semibold text-primary">
              Open prompt
              <span class="material-symbols-outlined text-sm leading-none">arrow_forward</span>
            </span>
          </button>
        {/each}
      </div>
    </section>

    <section>
      <h2 class="text-2xl font-bold text-on-surface mb-2">Core autonomous flow</h2>
      <p class="text-on-surface-variant mb-4 max-w-3xl">Run this end-to-end on a fresh Ubuntu target. Every step before <code class="font-mono text-base bg-surface-container-high px-1.5 py-0.5 rounded">apply</code> is non-mutating.</p>
      <pre class="bg-surface-container-low border border-outline-variant rounded-xl px-5 py-4 overflow-x-auto font-mono text-sm leading-relaxed text-on-surface"><code>stackkit init base-kit --non-interactive --admin-email admin@example.com
stackkit prepare --dry-run
stackkit validate
stackkit generate --force
stackkit plan
stackkit apply
stackkit verify --http --json</code></pre>
      <p class="text-sm text-on-surface-variant mt-4">Do not hand-edit generated rollout artifacts. Change StackSpec, CUE, or Go source, then regenerate.</p>
    </section>
  </div>
</main>
