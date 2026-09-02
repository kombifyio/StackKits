<script lang="ts">
  import type { CatalogUseCase } from "./types.js";

  export let useCase: CatalogUseCase;
  export let selected = "";
  export let onSelect: (alternativeId: string) => void;
  let choosing = false;

  $: available = useCase.availability === "available" && useCase.alternatives.length > 0;
  $: title = useCase.title === useCase.use_case_id ? words(useCase.title) : useCase.title;
  $: description = useCase.description || useCase.alternatives[0]?.functions.map(words).join(" · ") || "Inspect the declared workload options.";
  $: defaultId = useCase.default_alternative_id ?? (useCase.alternatives.length === 1 ? useCase.alternatives[0].alternative_id : "");

  function words(value: string): string {
    return value.replace(/[-_]/g, " ").replace(/^./, (letter) => letter.toUpperCase());
  }

  function toggle(): void {
    if (selected) {
      if (!useCase.required) { choosing = false; onSelect(""); }
    } else if (defaultId) onSelect(defaultId);
    else choosing = !choosing;
  }

  function icon(id: string): string {
    switch (id) {
      case "photos": return "M4 4h16v16H4z M4 15l5-5 7 10 M13 15l3-3 4 4 M15 8h.01";
      case "files": return "M3 7h7l2 2h9v11H3z M3 7V4h7l2 3h7v2";
      case "media": return "M3 4h18v16H3z M10 8l6 4-6 4z M7 4v16 M17 4v16";
      case "smart-home": return "M3 11l9-8 9 8 M5 10v11h14V10 M9 21v-7h6v7 M8 8a6 6 0 0 1 8 0";
      case "vault": return "M4 3h16v18H4z M8 10a4 4 0 1 0 8 0 4 4 0 0 0-8 0 M12 8v4 M10 10h4 M20 7h2 M20 17h2";
      case "cloud-core": return "M6 17a5 5 0 0 1-1-10 7 7 0 0 1 13-1 5 5 0 0 1 0 10 M8 20v-6h8v6 M12 14V9";
      default: return "M4 3h16v7H4z M4 14h16v7H4z M7 6h.01 M7 17h.01 M11 6h6 M11 17h6 M12 10v4";
    }
  }
</script>

<section class="use-case-tile" class:selected={Boolean(selected)} class:unavailable={!available} data-use-case-id={useCase.use_case_id} data-required={useCase.required}>
  <button type="button" class="tile-toggle" aria-pressed={Boolean(selected)} aria-label={`${title}${useCase.required ? " (required)" : ""}`} disabled={!available || (useCase.required && Boolean(selected))} onclick={toggle} data-testid={`use-case-${useCase.use_case_id}`}>
    <span class="tile-top">
      <span class="illustration" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d={icon(useCase.use_case_id)} /></svg></span>
      <span class="state-mark" aria-hidden="true">{selected ? "✓" : "+"}</span>
    </span>
    <span class="tile-title">{title}</span>
    <span class="tile-description">{description}</span>
    <span class="tile-status">{!available ? "Unavailable" : useCase.required ? (selected ? "Required · included" : "Required · choose an alternative") : selected ? "Included" : "Add to your kit"}</span>
  </button>
  {#if !available}
    <p class="blocked-reason">{words(useCase.reason_code ?? "USE_CASE_NOT_AVAILABLE")}</p>
  {:else if selected || choosing}
    <div class="alternatives" role="group" aria-label={`${title} alternatives`}>
      <span class="options-label">{useCase.alternatives.length > 1 ? "Choose an alternative" : "Included alternative"}</span>
      {#each useCase.alternatives as alternative (alternative.alternative_id)}
        <button type="button" class="alternative" class:active={selected === alternative.alternative_id} aria-pressed={selected === alternative.alternative_id} onclick={() => onSelect(alternative.alternative_id)} data-testid={`alternative-${useCase.use_case_id}-${alternative.alternative_id}`}>
          <span>{words(alternative.alternative_id)}</span>
          {#if alternative.alternative_id === defaultId}<small>Default</small>{/if}
        </button>
      {/each}
    </div>
  {/if}
</section>

<style>
  .use-case-tile { min-width: 0; border: 1px solid var(--outline); border-radius: .8rem; background: var(--surface); transition: border-color 150ms ease, background 150ms ease; }
  .use-case-tile.selected { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 5%, var(--surface)); }
  .use-case-tile:not(.unavailable):hover { border-color: color-mix(in srgb, var(--accent) 65%, var(--outline)); }
  button { font: inherit; cursor: pointer; }
  button:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
  .tile-toggle { display: flex; width: 100%; flex-direction: column; align-items: flex-start; padding: 1.05rem; gap: .55rem; border: 0; border-radius: .8rem; color: var(--text); background: transparent; text-align: left; }
  .tile-toggle:disabled { cursor: default; }
  .tile-top { display: flex; align-items: center; justify-content: space-between; width: 100%; }
  .illustration { display: grid; place-items: center; width: 2.8rem; height: 2.8rem; border-radius: .65rem; color: var(--accent); background: color-mix(in srgb, var(--accent) 9%, var(--surface)); }
  svg { width: 1.9rem; height: 1.9rem; }
  .state-mark { display: grid; place-items: center; width: 1.45rem; height: 1.45rem; border: 1px solid var(--outline); border-radius: 50%; color: var(--muted); font-size: 1rem; }
  .selected .state-mark { color: var(--surface); background: var(--accent); border-color: var(--accent); }
  .tile-title { font-weight: 650; font-size: 1rem; }
  .tile-description { color: var(--muted); font-size: .77rem; line-height: 1.5; text-wrap: pretty; }
  .tile-status { font-size: .7rem; color: var(--muted); }
  .selected .tile-status { color: var(--accent); }
  .alternatives { display: flex; flex-wrap: wrap; gap: .4rem; padding: .8rem 1rem 1rem; border-top: 1px solid var(--outline); }
  .options-label { width: 100%; margin-bottom: .2rem; color: var(--muted); font-size: .68rem; }
  .alternative { display: flex; align-items: center; gap: .5rem; min-height: 2.2rem; padding: .4rem .6rem; border: 1px solid var(--outline); border-radius: .4rem; color: var(--text); background: transparent; font-size: .73rem; }
  .alternative.active { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 9%, var(--surface)); }
  .alternative small { color: var(--muted); font-size: .62rem; }
  .unavailable { opacity: .65; }
  .blocked-reason { margin: 0 1rem 1rem; font-size: .7rem; color: var(--muted); }
  @media (prefers-reduced-motion: reduce) { .use-case-tile { transition: none; } }
</style>
