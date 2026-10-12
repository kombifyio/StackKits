<script lang="ts">
  import type { CatalogModule, CatalogUseCase, ModuleProfileSelection, ResourceVector } from "./types.js";

  export let useCase: CatalogUseCase;
  export let modules: CatalogModule[];
  export let selections: ModuleProfileSelection[];
  export let alternativeId: string;
  export let included = false;

  $: alternative = useCase.alternatives.find((entry) => entry.alternative_id === alternativeId);
  $: module = modules.find((entry) => entry.module_id === alternative?.module_id);
  $: profileId = included
    ? selections.find((entry) => entry.module_id === module?.module_id)?.compute_profile
    : module?.default_compute_profile;
  $: profile = module?.compute_profiles.find((entry) => entry.id === profileId);
  $: options = useCase.alternatives.flatMap((entry) => {
    const candidate = modules.find((item) => item.module_id === entry.module_id);
    return candidate ? [{ alternative: entry, module: candidate }] : [];
  });

  // These are individual CUE profile facts. Totals and fit verdicts belong to
  // the shared capacity engine; no arithmetic or admission happens in a tile.
  function vector(value?: ResourceVector): string {
    return `${value?.cpu_cores ?? "?"} CPU · ${value?.ram_gb ?? "?"} GiB RAM · ${value?.storage_gb ?? "?"} GiB storage`;
  }
</script>

<div class="capacity-facts">
  {#if profile}
    <p class="current-profile"><strong>{alternativeId} · {profile.id}</strong>{#if !included} · Default preview{/if}</p>
    <p><span>Host floor</span> {vector(profile.host_floor)}</p>
    <p><span>Reserved</span> {vector(profile.reservation)}</p>
  {:else}
    <p>Select an alternative and profile to see its resource needs.</p>
  {/if}
  {#if options.some((option) => option.module.compute_profiles.length > 0)}
    <details>
      <summary>Compare resource profiles</summary>
      <p class="note">Declared requirements for this application. Kit totals are checked below. ? means not declared.</p>
      {#each options as option (option.alternative.alternative_id)}
        <section aria-label={`${option.alternative.alternative_id} resource profiles`}>
          <h4>{option.alternative.alternative_id}</h4>
          {#each option.module.compute_profiles as candidate (candidate.id)}
            <div class="profile" class:current={option.alternative.alternative_id === alternativeId && candidate.id === profileId}>
              <strong>{candidate.id}</strong>
              {#if candidate.description}<p>{candidate.description}</p>{/if}
              <p><span>Host floor</span> {vector(candidate.host_floor)}</p>
              <p><span>Reserved</span> {vector(candidate.reservation)}</p>
              <p><span>Recommended</span> {vector(candidate.recommended)}</p>
              {#if candidate.capabilities.length}<p><span>Includes</span> {candidate.capabilities.join(" · ")}</p>{/if}
              {#if candidate.degradations.length}<p><span>Limitations</span> {candidate.degradations.join(" · ")}</p>{/if}
            </div>
          {/each}
        </section>
      {/each}
    </details>
  {/if}
</div>

<style>
  .capacity-facts { padding: .8rem 1rem; border-top: 1px solid var(--outline); color: var(--muted); font-size: .7rem; line-height: 1.5; overflow-wrap: anywhere; }
  p { margin: .25rem 0; }
  p span { color: var(--text); }
  .current-profile { color: var(--text); margin-bottom: .4rem; }
  details { margin-top: .6rem; }
  summary { cursor: pointer; color: var(--accent); }
  summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
  .note { margin: .6rem 0; }
  h4 { color: var(--text); font-size: .75rem; margin: .8rem 0 .4rem; }
  .profile { padding: .6rem; margin-top: .4rem; border: 1px solid var(--outline); border-radius: .4rem; }
  .profile strong { color: var(--text); }
  .profile.current { border-color: var(--accent); }
</style>
