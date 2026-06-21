<script lang="ts">
  type Props = {
    navigate: (path: string) => void
    currentPath: string
    children?: unknown
  }
  const { navigate, currentPath, children }: Props = $props()

  let docsMenuOpen = $state(false)
  let kitsMenuOpen = $state(false)
  let docsMenuTimer: ReturnType<typeof setTimeout> | null = null
  let kitsMenuTimer: ReturnType<typeof setTimeout> | null = null
  let mobileNavOpen = $state(false)

  const docsActive = $derived(currentPath.startsWith('/getting-started'))
  const kitsActive = $derived(currentPath.startsWith('/kits/'))

  const handleLink = (e: MouseEvent, path: string) => {
    e.preventDefault()
    docsMenuOpen = false
    kitsMenuOpen = false
    mobileNavOpen = false
    navigate(path)
  }

  const openDocsMenu = () => {
    if (docsMenuTimer) { clearTimeout(docsMenuTimer); docsMenuTimer = null }
    docsMenuOpen = true
  }
  const scheduleCloseDocsMenu = () => {
    if (docsMenuTimer) clearTimeout(docsMenuTimer)
    docsMenuTimer = setTimeout(() => { docsMenuOpen = false }, 140)
  }
  const openKitsMenu = () => {
    if (kitsMenuTimer) { clearTimeout(kitsMenuTimer); kitsMenuTimer = null }
    kitsMenuOpen = true
  }
  const scheduleCloseKitsMenu = () => {
    if (kitsMenuTimer) clearTimeout(kitsMenuTimer)
    kitsMenuTimer = setTimeout(() => { kitsMenuOpen = false }, 140)
  }

  const handleKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      docsMenuOpen = false
      kitsMenuOpen = false
      mobileNavOpen = false
    }
  }

  const navLinkClass = (active: boolean) =>
    `transition-colors no-underline text-xs md:text-sm ${
      active
        ? 'text-primary font-semibold border-b-2 border-primary pb-1'
        : 'text-on-surface-variant hover:text-on-surface'
    }`
</script>

<svelte:window onkeydown={handleKey} />

<nav class="fixed top-0 w-full z-50 bg-surface/85 backdrop-blur-xl font-sans antialiased text-sm tracking-tight border-b border-outline-variant/50">
  <div class="flex justify-between items-center max-w-7xl mx-auto px-4 md:px-6 h-16 relative">
    <div class="flex items-center gap-4 md:gap-8">
      <a href="/" onclick={(e) => handleLink(e, '/')} class="flex items-center gap-2.5 no-underline shrink-0" aria-label="kombify StackKits — home">
        <img src="/wordmark.png" alt="kombify StackKits" class="h-8 md:h-9 w-auto" />
      </a>

      <div class="hidden md:flex gap-3 md:gap-6 items-center">
        <a class={navLinkClass(currentPath === '/')} href="/" onclick={(e) => handleLink(e, '/')}>Home</a>

        <div
          class="relative"
          onmouseenter={openDocsMenu}
          onmouseleave={scheduleCloseDocsMenu}
          onfocusin={openDocsMenu}
          onfocusout={scheduleCloseDocsMenu}
          role="presentation"
        >
          <button
            type="button"
            aria-haspopup="menu"
            aria-expanded={docsMenuOpen}
            onclick={() => (docsMenuOpen = !docsMenuOpen)}
            class={`flex items-center gap-1 bg-transparent border-0 p-0 cursor-pointer ${navLinkClass(docsActive)}`}
          >
            Getting Started
            <span class="material-symbols-outlined text-base leading-none transition-transform duration-150 {docsMenuOpen ? 'rotate-180' : ''}">expand_more</span>
          </button>

          {#if docsMenuOpen}
            <div role="menu" class="absolute left-0 top-full mt-2 min-w-[280px] rounded-xl border border-outline-variant bg-surface-container shadow-xl py-2 z-50">
              <a href="/getting-started" onclick={(e) => handleLink(e, '/getting-started')} role="menuitem"
                 class="block px-4 py-2.5 no-underline text-sm transition-colors {currentPath === '/getting-started' ? 'bg-primary-container/40 text-on-primary-container font-semibold' : 'text-on-surface hover:bg-surface-container-high'}">
                <div class="font-medium">Overview</div>
                <div class="text-xs text-on-surface-variant mt-0.5">Choose your path</div>
              </a>
              <a href="/getting-started/cli" onclick={(e) => handleLink(e, '/getting-started/cli')} role="menuitem"
                 class="block px-4 py-2.5 no-underline text-sm transition-colors {currentPath === '/getting-started/cli' ? 'bg-primary-container/40 text-on-primary-container font-semibold' : 'text-on-surface hover:bg-surface-container-high'}">
                <div class="font-medium">CLI</div>
                <div class="text-xs text-on-surface-variant mt-0.5">Install, init, generate, apply</div>
              </a>
              <a href="/getting-started/agents" onclick={(e) => handleLink(e, '/getting-started/agents')} role="menuitem"
                 class="block px-4 py-2.5 no-underline text-sm transition-colors {currentPath === '/getting-started/agents' ? 'bg-primary-container/40 text-on-primary-container font-semibold' : 'text-on-surface hover:bg-surface-container-high'}">
                <div class="font-medium">Agents</div>
                <div class="text-xs text-on-surface-variant mt-0.5">Prompts, MCP, autonomous rollout</div>
              </a>
            </div>
          {/if}
        </div>

        <div
          class="relative"
          onmouseenter={openKitsMenu}
          onmouseleave={scheduleCloseKitsMenu}
          onfocusin={openKitsMenu}
          onfocusout={scheduleCloseKitsMenu}
          role="presentation"
        >
          <button
            type="button"
            aria-haspopup="menu"
            aria-expanded={kitsMenuOpen}
            onclick={() => (kitsMenuOpen = !kitsMenuOpen)}
            class={`flex items-center gap-1 bg-transparent border-0 p-0 cursor-pointer ${navLinkClass(kitsActive)}`}
          >
            Kits
            <span class="material-symbols-outlined text-base leading-none transition-transform duration-150 {kitsMenuOpen ? 'rotate-180' : ''}">expand_more</span>
          </button>

          {#if kitsMenuOpen}
            <div role="menu" class="absolute left-0 top-full mt-2 min-w-[280px] rounded-xl border border-outline-variant bg-surface-container shadow-xl py-2 z-50">
              <a href="/kits/base" onclick={(e) => handleLink(e, '/kits/base')} role="menuitem"
                 class="block px-4 py-2.5 no-underline text-sm transition-colors {currentPath === '/kits/base' ? 'bg-primary-container/40 text-on-primary-container font-semibold' : 'text-on-surface hover:bg-surface-container-high'}">
                <div class="font-medium flex items-center gap-2">BaseKit <span class="text-[10px] font-semibold uppercase tracking-widest px-1.5 py-0.5 rounded bg-success-container text-on-success-container">stable</span></div>
                <div class="text-xs text-on-surface-variant mt-0.5">Release-ready single-environment default</div>
              </a>
            </div>
          {/if}
        </div>

        <a class={navLinkClass(currentPath === '/cli')} href="/cli" onclick={(e) => handleLink(e, '/cli')}>CLI Reference</a>
        <a class={navLinkClass(currentPath === '/architecture')} href="/architecture" onclick={(e) => handleLink(e, '/architecture')}>Architecture</a>
      </div>
    </div>

    <div class="flex items-center gap-2 md:gap-3">
      <a href={__OSS_REPO__} target="_blank" rel="noopener noreferrer" title="View on GitHub" class="p-2 hover:bg-surface-container-high rounded-md transition-all active:scale-95 duration-150 no-underline">
        <span class="material-symbols-outlined text-on-surface-variant">code</span>
      </a>
      <a href="/getting-started/cli" onclick={(e) => handleLink(e, '/getting-started/cli')} class="hidden md:inline-flex items-center gap-2 bg-primary text-on-primary px-4 py-2 rounded-lg text-sm font-semibold hover:shadow-lg hover:shadow-primary/20 active:scale-95 duration-150 no-underline">
        <span class="material-symbols-outlined text-base leading-none">terminal</span>
        Install
      </a>
      <button type="button" aria-label="Open menu" onclick={() => (mobileNavOpen = !mobileNavOpen)} class="md:hidden p-2 hover:bg-surface-container-high rounded-md transition-all">
        <span class="material-symbols-outlined text-on-surface">{mobileNavOpen ? 'close' : 'menu'}</span>
      </button>
    </div>
  </div>

  {#if mobileNavOpen}
    <div class="md:hidden border-t border-outline-variant bg-surface-container">
      <div class="max-w-7xl mx-auto px-4 py-3 flex flex-col gap-1">
        {#each [
          { label: 'Home', href: '/' },
          { label: 'Getting Started', href: '/getting-started' },
          { label: 'CLI Reference', href: '/cli' },
          { label: 'Kits', href: '/kits/base' },
          { label: 'Architecture', href: '/architecture' },
          { label: 'Stack', href: '/stack' },
          { label: 'Changelog', href: '/changelog' },
        ] as item}
          <a href={item.href} onclick={(e) => handleLink(e, item.href)} class="px-3 py-2 rounded-md text-sm text-on-surface hover:bg-surface-container-high no-underline">{item.label}</a>
        {/each}
      </div>
    </div>
  {/if}
</nav>

{@render (children as any)?.()}

<footer class="w-full border-t border-outline-variant bg-surface-container-lowest mt-24">
  <div class="max-w-7xl mx-auto px-6 md:px-8 py-12">
    <div class="flex flex-col md:flex-row justify-between items-start md:items-center gap-8">
      <div class="flex items-center gap-3">
        <img src="/icon.png" alt="" class="w-9 h-9 opacity-90" />
        <div>
          <div class="text-sm font-bold text-on-surface">kombify StackKits</div>
          <div class="text-xs text-on-surface-variant mt-0.5">Declarative homelab infrastructure</div>
        </div>
      </div>
      <div class="flex gap-6 items-center flex-wrap">
        {#each [
          { label: 'GitHub', href: __OSS_REPO__, external: true },
          { label: 'Releases', href: __OSS_REPO_RELEASES__, external: true },
          { label: 'Changelog', href: '/changelog', external: false },
          { label: 'llms.txt', href: '/llms.txt', external: true },
          { label: 'License', href: __OSS_REPO__ + '/blob/main/LICENSE', external: true },
          { label: 'Impressum', href: '/impressum', external: false },
        ] as item}
          {#if item.external}
            <a href={item.href} target="_blank" rel="noopener noreferrer" class="text-xs font-medium uppercase tracking-widest text-on-surface-variant hover:text-primary transition-colors no-underline">{item.label}</a>
          {:else}
            <a href={item.href} onclick={(e) => handleLink(e, item.href)} class="text-xs font-medium uppercase tracking-widest text-on-surface-variant hover:text-primary transition-colors no-underline">{item.label}</a>
          {/if}
        {/each}
      </div>
    </div>
    <div class="mt-10 pt-6 border-t border-outline-variant/50 text-xs text-on-surface-variant text-center">
      © 2026 kombify · Apache-2.0 License · Open source. Built for homelabs and self-hosters.
    </div>
  </div>
</footer>
