<script lang="ts">
  type Note = { title: string; body: string }
  type ChangelogData = { version: string; notes: Note[] }

  let data = $state<ChangelogData | null>(null)
  let loading = $state(true)
  let error = $state<string | null>(null)

  $effect(() => {
    fetch('/changelog.json')
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.json()
      })
      .then((j) => {
        data = j
        loading = false
      })
      .catch((e) => {
        error = String(e)
        loading = false
      })
  })

  const repoChangelog = __OSS_REPO__ + '/blob/main/CHANGELOG.md'
  const repoReleases = __OSS_REPO_RELEASES__
</script>

<main class="pt-24 md:pt-32 pb-20 px-6">
  <div class="max-w-3xl mx-auto">
    <header class="mb-10">
      <span class="text-[11px] font-bold tracking-widest uppercase text-primary">Changelog</span>
      <h1 class="text-4xl md:text-5xl font-extrabold tracking-tight text-on-surface mt-2 mb-3">What's new</h1>
      <p class="text-on-surface-variant">Latest highlights pulled from <a href={repoChangelog} target="_blank" rel="noopener noreferrer" class="text-primary hover:underline">CHANGELOG.md</a>. Full release notes live on the <a href={repoReleases} target="_blank" rel="noopener noreferrer" class="text-primary hover:underline">GitHub releases page</a>.</p>
    </header>

    {#if loading}
      <div class="text-on-surface-variant text-sm">Loading…</div>
    {:else if error}
      <div class="text-error text-sm">Could not load changelog.json: {error}</div>
    {:else if data}
      <div class="bg-surface-container border border-outline-variant rounded-2xl p-6 md:p-8 mb-6">
        <div class="flex items-baseline gap-3 mb-5">
          <h2 class="text-xl font-bold text-on-surface">Release {data.version}</h2>
          <span class="text-xs font-bold tracking-widest uppercase text-primary">latest</span>
        </div>
        <ul class="space-y-4">
          {#each data.notes as note}
            <li class="border-l-2 border-primary pl-4">
              <h3 class="font-semibold text-on-surface mb-1">{note.title}</h3>
              <p class="text-sm text-on-surface-variant leading-relaxed">{note.body}</p>
            </li>
          {/each}
        </ul>
      </div>
      <a href={repoChangelog} target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-2 text-primary hover:underline text-sm font-semibold no-underline">
        Full changelog on GitHub
        <span class="material-symbols-outlined text-base leading-none">open_in_new</span>
      </a>
    {/if}
  </div>
</main>
