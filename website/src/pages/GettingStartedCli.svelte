<script lang="ts">
  import InstallBlock from '../lib/InstallBlock.svelte'
  import TerminalBlock from '../lib/TerminalBlock.svelte'

  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()

  const installOneLiner = __STACKKIT_INSTALL_ONELINER__
  const cliInstallOneLiner = __STACKKIT_CLI_INSTALL_ONELINER__
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-4xl mx-auto">
    <nav class="text-xs text-on-surface-variant mb-6 flex gap-2">
      <button onclick={() => navigate('/getting-started')} class="hover:text-primary cursor-pointer">Getting Started</button>
      <span>/</span>
      <span class="text-on-surface">CLI</span>
    </nav>

    <header class="mb-10">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">CLI walkthrough</span>
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-4">Install, init, plan, apply.</h1>
      <p class="text-lg text-on-surface-variant max-w-3xl">The CLI ships as a single Go binary plus a packaged OpenTofu and the public kit catalog. Every command up to <code class="font-mono text-base bg-surface-container-high px-1.5 py-0.5 rounded">apply</code> is read-only or filesystem-local.</p>
    </header>

    <section class="prose-stackkit">
      <h2>1 · Install the CLI</h2>
      <p>The shared installer downloads <code>stackkit</code>, <code>stackkit-server</code>, the <code>stackkit-mcp</code> local adapter for the single <code>stackkit</code> MCP connection, packaged OpenTofu, and the public kit catalog into <code>~/.stackkits</code>. Works on Linux and macOS (amd64 + arm64).</p>
    </section>

    <div class="my-6">
      <InstallBlock command={cliInstallOneLiner} note="Use this when you want the full CLI without immediately running BaseKit." />
    </div>

    <section class="prose-stackkit">
      <h3>Verify the install</h3>
      <pre><code>stackkit version
stackkit doctor</code></pre>

      <h2>2 · Initialize a deployment</h2>
      <p>The wizard writes <code>stack-spec.yaml</code> with sensible defaults. You can re-run <code>init</code> or edit the spec by hand at any time.</p>
      <pre><code>mkdir my-homelab && cd my-homelab
stackkit init base-kit</code></pre>

      <p>Or skip the wizard entirely:</p>
      <pre><code>stackkit init base-kit \
  --non-interactive \
  --admin-email you@example.com \
  --owner-username admin</code></pre>

      <h2>3 · Prepare the host</h2>
      <p>Idempotent host preparation: checks prerequisites, installs Docker if missing, places packaged OpenTofu, validates the spec, and inspects hardware.</p>
      <pre><code>stackkit prepare</code></pre>

      <h2>4 · Generate rollout artifacts</h2>
      <p>CUE contracts are compiled into deterministic OpenTofu, Docker Compose, and tfvars files. Generated output is disposable — if you need a change, fix CUE or the spec and regenerate.</p>
      <pre><code>stackkit generate
stackkit validate</code></pre>

      <h2>5 · Plan &amp; apply</h2>
      <p>Plan is a dry-run preview; apply is the only mutating step. <code>--verify</code> runs read-only post-checks immediately afterward.</p>
      <pre><code>stackkit plan
stackkit apply --verify</code></pre>

      <h2>6 · Inspect &amp; verify</h2>
      <pre><code>stackkit status
stackkit verify --http --json
stackkit logs</code></pre>

      <h2>Or: the one-liner</h2>
      <p>If you just want BaseKit on a fresh Ubuntu VM with sane defaults, skip the manual flow and run:</p>
    </section>

    <div class="my-6">
      <InstallBlock command={installOneLiner} note="The verified beta BaseKit one-liner. Runs prepare → init → generate → apply for you." />
    </div>

    <section class="mt-10 pt-8 border-t border-outline-variant">
      <h2 class="text-xl font-bold text-on-surface mb-4">Next steps</h2>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <button onclick={() => navigate('/cli')} class="text-left bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 cursor-pointer transition-colors">
          <h3 class="font-semibold text-on-surface mb-1">CLI Reference</h3>
          <p class="text-sm text-on-surface-variant">Every top-level command and flag.</p>
        </button>
        <button onclick={() => navigate('/kits/base')} class="text-left bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 cursor-pointer transition-colors">
          <h3 class="font-semibold text-on-surface mb-1">BaseKit details</h3>
          <p class="text-sm text-on-surface-variant">What the verified BaseKit path deploys.</p>
        </button>
        <button onclick={() => navigate('/architecture')} class="text-left bg-surface-container border border-outline-variant rounded-xl p-5 hover:border-primary/40 cursor-pointer transition-colors">
          <h3 class="font-semibold text-on-surface mb-1">Architecture</h3>
          <p class="text-sm text-on-surface-variant">CUE → OpenTofu → Docker pipeline.</p>
        </button>
      </div>
    </section>
  </div>
</main>
