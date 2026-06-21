<script lang="ts">
  import { cliCommands, globalFlags, categoryLabels, type CliCommand } from '../content/cliCommands'
  import InstallBlock from '../lib/InstallBlock.svelte'

  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()

  const cliInstallOneLiner = __STACKKIT_CLI_INSTALL_ONELINER__

  const categories: CliCommand['category'][] = ['core', 'lifecycle', 'inspect', 'agent', 'release', 'utility']
  const grouped = categories.map((cat) => ({ cat, commands: cliCommands.filter((c) => c.category === cat) }))
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-5xl mx-auto">
    <header class="mb-10">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">CLI Reference</span>
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-4">stackkit</h1>
      <p class="text-lg text-on-surface-variant max-w-3xl">Cobra command definitions in <code class="font-mono text-base bg-surface-container-high px-1.5 py-0.5 rounded">cmd/stackkit/commands/</code> are the source of truth. This page tracks the implemented top-level surface; run <code class="font-mono text-base bg-surface-container-high px-1.5 py-0.5 rounded">stackkit help</code> for live details.</p>
    </header>

    <section class="mb-10">
      <h2 class="text-xl font-bold text-on-surface mb-3">Install</h2>
      <InstallBlock command={cliInstallOneLiner} note="Installs stackkit, stackkit-server, stackkit-mcp, packaged OpenTofu, and the public kit catalog into ~/.stackkits." />
    </section>

    <section class="mb-12">
      <h2 class="text-xl font-bold text-on-surface mb-4">Global flags</h2>
      <div class="bg-surface-container border border-outline-variant rounded-2xl overflow-hidden">
        <table class="w-full text-sm">
          <thead class="bg-surface-container-high text-on-surface-variant text-xs uppercase tracking-widest">
            <tr>
              <th class="text-left font-semibold px-4 py-3">Flag</th>
              <th class="text-left font-semibold px-4 py-3">Short</th>
              <th class="text-left font-semibold px-4 py-3">Default</th>
              <th class="text-left font-semibold px-4 py-3">Purpose</th>
            </tr>
          </thead>
          <tbody>
            {#each globalFlags as f}
              <tr class="border-t border-outline-variant">
                <td class="px-4 py-3 font-mono text-on-surface">{f.flag}</td>
                <td class="px-4 py-3 font-mono text-on-surface-variant">{f.short || '—'}</td>
                <td class="px-4 py-3 font-mono text-on-surface-variant">{f.def}</td>
                <td class="px-4 py-3 text-on-surface-variant">{f.purpose}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>

    <section class="mb-10">
      <h2 class="text-xl font-bold text-on-surface mb-4">Primary workflow</h2>
      <pre class="bg-surface-container-low border border-outline-variant rounded-xl px-5 py-4 overflow-x-auto font-mono text-sm leading-relaxed text-on-surface"><code>stackkit init base-kit
stackkit prepare
stackkit generate
stackkit plan
stackkit apply --verify
stackkit verify --http --json</code></pre>
    </section>

    {#each grouped as group}
      {#if group.commands.length}
        <section class="mb-10">
          <h2 class="text-xl font-bold text-on-surface mb-4">{categoryLabels[group.cat]}</h2>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            {#each group.commands as cmd}
              <div class="bg-surface-container border border-outline-variant rounded-xl p-5">
                <div class="flex items-baseline gap-2 mb-2">
                  <code class="font-mono font-bold text-on-surface">stackkit {cmd.name}</code>
                  {#if cmd.shortName}
                    <span class="text-xs text-on-surface-variant">alias <code class="font-mono">{cmd.shortName}</code></span>
                  {/if}
                </div>
                <p class="text-sm text-on-surface-variant leading-relaxed">{cmd.purpose}</p>
              </div>
            {/each}
          </div>
        </section>
      {/if}
    {/each}

    <section class="mt-12 pt-8 border-t border-outline-variant">
      <h2 class="text-xl font-bold text-on-surface mb-3">Need the manual walkthrough?</h2>
      <p class="text-on-surface-variant mb-4">The Getting Started CLI guide walks through each command with example flags and output.</p>
      <button onclick={() => navigate('/getting-started/cli')} class="inline-flex items-center gap-2 bg-primary text-on-primary px-4 py-2.5 rounded-lg text-sm font-semibold cursor-pointer">
        Open CLI walkthrough
        <span class="material-symbols-outlined text-base leading-none">arrow_forward</span>
      </button>
    </section>
  </div>
</main>
