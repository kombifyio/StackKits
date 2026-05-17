<script lang="ts">
  type Props = { navigate: (path: string) => void }
  const { navigate }: Props = $props()

  const paths = [
    {
      id: 'one-liner',
      eyebrow: 'Fastest',
      title: 'One-liner install',
      description: 'Run BaseKit on a fresh Ubuntu VM or macOS host. Best when you just want a working homelab.',
      icon: 'rocket_launch',
      cta: 'Install BaseKit',
      command: __STACKKIT_INSTALL_ONELINER__,
      next: '/getting-started/cli',
    },
    {
      id: 'cli',
      eyebrow: 'Most control',
      title: 'CLI walkthrough',
      description: 'Install the CLI, run the wizard, inspect generated artifacts, plan and apply in steps.',
      icon: 'terminal',
      cta: 'Walk through the CLI',
      command: '',
      next: '/getting-started/cli',
    },
    {
      id: 'agents',
      eyebrow: 'For coding agents',
      title: 'Agent rollout',
      description: 'Copy-ready prompts, MCP connector, JSON schemas, and OpenAPI for autonomous rollout.',
      icon: 'smart_toy',
      cta: 'Open agent guide',
      command: '',
      next: '/getting-started/agents',
    },
  ]
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-5xl mx-auto">
    <header class="mb-12 md:mb-16">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Getting Started</span>
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-4">Pick the path that fits you.</h1>
      <p class="text-lg text-on-surface-variant max-w-3xl">StackKits is shipped as a single CLI plus a public kit catalog. You can take the one-command path, walk through the CLI by hand, or hand the whole rollout to a coding agent — same artifacts, same result.</p>
    </header>

    <div class="grid grid-cols-1 md:grid-cols-3 gap-5">
      {#each paths as p}
        <button onclick={() => navigate(p.next)} class="text-left bg-surface-container border border-outline-variant rounded-2xl p-6 cursor-pointer hover:border-primary/60 hover:bg-surface-container-high transition-all">
          <div class="w-10 h-10 rounded-lg bg-primary-container/40 text-primary flex items-center justify-center mb-4">
            <span class="material-symbols-outlined">{p.icon}</span>
          </div>
          <span class="text-[10px] font-bold tracking-widest uppercase text-on-surface-variant">{p.eyebrow}</span>
          <h3 class="text-lg font-bold text-on-surface mt-1 mb-2">{p.title}</h3>
          <p class="text-sm text-on-surface-variant leading-relaxed mb-5">{p.description}</p>
          <span class="inline-flex items-center gap-1.5 text-sm font-semibold text-primary">
            {p.cta}
            <span class="material-symbols-outlined text-base leading-none">arrow_forward</span>
          </span>
        </button>
      {/each}
    </div>

    <section class="mt-16 md:mt-20">
      <h2 class="text-2xl font-bold text-on-surface mb-3">Before you start</h2>
      <ul class="text-on-surface-variant space-y-2 max-w-3xl">
        <li class="flex gap-3"><span class="material-symbols-outlined text-primary text-base leading-relaxed">check</span> Linux host (Ubuntu recommended) or macOS with root/sudo access.</li>
        <li class="flex gap-3"><span class="material-symbols-outlined text-primary text-base leading-relaxed">check</span> Docker available, or installable — StackKits will check and prepare it.</li>
        <li class="flex gap-3"><span class="material-symbols-outlined text-primary text-base leading-relaxed">check</span> Roughly 6 GB RAM and 30 GB free disk for the BaseKit default.</li>
        <li class="flex gap-3"><span class="material-symbols-outlined text-primary text-base leading-relaxed">check</span> Outbound HTTPS for fetching container images and packaged OpenTofu.</li>
      </ul>
    </section>
  </div>
</main>
