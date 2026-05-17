<script lang="ts">
  import Layout from './Layout.svelte'
  import Home from './pages/Home.svelte'
  import GettingStarted from './pages/GettingStarted.svelte'
  import GettingStartedCli from './pages/GettingStartedCli.svelte'
  import GettingStartedAgents from './pages/GettingStartedAgents.svelte'
  import GettingStartedAgentPrompt from './pages/GettingStartedAgentPrompt.svelte'
  import KitDetail from './pages/KitDetail.svelte'
  import CliReference from './pages/CliReference.svelte'
  import Architecture from './pages/Architecture.svelte'
  import Stack from './pages/Stack.svelte'
  import Changelog from './pages/Changelog.svelte'
  import Impressum from './pages/Impressum.svelte'

  let path = $state(window.location.pathname)

  const resetScroll = () => {
    if (typeof window !== 'undefined') {
      window.scrollTo({ top: 0, left: 0, behavior: 'instant' as ScrollBehavior })
    }
  }

  window.addEventListener('popstate', () => {
    path = window.location.pathname
    resetScroll()
  })

  const navigate = (newPath: string) => {
    const samePath = newPath === window.location.pathname
    window.history.pushState({}, '', newPath)
    path = newPath
    if (!samePath) resetScroll()
  }
</script>

<Layout {navigate} currentPath={path}>
  {#if path === '/'}
    <Home {navigate} />
  {:else if path === '/getting-started' || path === '/getting-started/'}
    <GettingStarted {navigate} />
  {:else if path === '/getting-started/cli' || path === '/getting-started/cli/'}
    <GettingStartedCli {navigate} />
  {:else if path === '/getting-started/agents' || path === '/getting-started/agents/'}
    <GettingStartedAgents {navigate} />
  {:else if path.startsWith('/getting-started/agents/') && !path.endsWith('.md')}
    <GettingStartedAgentPrompt promptID={path.split('/').filter(Boolean).pop() ?? ''} {navigate} />
  {:else if path === '/kits/base' || path === '/kits/base/'}
    <KitDetail kit="base" {navigate} />
  {:else if path === '/kits/modern' || path === '/kits/modern/'}
    <KitDetail kit="modern" {navigate} />
  {:else if path === '/kits/ha' || path === '/kits/ha/'}
    <KitDetail kit="ha" {navigate} />
  {:else if path === '/cli' || path === '/cli/'}
    <CliReference {navigate} />
  {:else if path === '/architecture' || path === '/architecture/'}
    <Architecture {navigate} />
  {:else if path === '/stack' || path === '/stack/'}
    <Stack {navigate} />
  {:else if path === '/changelog' || path === '/changelog/'}
    <Changelog />
  {:else if path === '/impressum' || path === '/impressum/'}
    <Impressum />
  {:else}
    <div class="pt-32 pb-24 text-center max-w-7xl mx-auto min-h-[60vh] px-6">
      <h1 class="text-4xl font-bold mb-4 text-on-surface">404 — Page Not Found</h1>
      <p class="text-on-surface-variant mb-8">The page you are looking for does not exist.</p>
      <button onclick={() => navigate('/')} class="bg-primary text-on-primary px-6 py-2.5 rounded-md font-semibold cursor-pointer">
        Return Home
      </button>
    </div>
  {/if}
</Layout>
