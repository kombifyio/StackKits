<script lang="ts">
  import { onMount } from "svelte";
  import type { PlannerState, PlannerStateListener } from "./session.js";
  import type { CatalogKit, ComputeTier, DeclaredCapacity, ModuleRuntimeRequirement, PrepareHandoffInput, Selection, TierProfile, ToolInputMap, ToolName, ToolResultMap, UseCaseFit, WebMcpCatalog } from "./types.js";

  interface PlannerSessionContract {
    readonly service: { readonly catalog?: WebMcpCatalog };
    readonly state: PlannerState;
    subscribe(listener: PlannerStateListener): () => void;
    setSelection(selection: Selection): void;
    setCapacity(capacity: Partial<DeclaredCapacity>): void;
    invoke<T extends ToolName>(tool: T, input: ToolInputMap[T], signal?: AbortSignal): Promise<ToolResultMap[T]>;
  }

  export let session: PlannerSessionContract;
  export let webMcpAvailable = false;
  export let preselectedStackkitId = "";

  let catalog: WebMcpCatalog | undefined;
  let kits: CatalogKit[] = [];
  let state: PlannerState = {};
  let stackkitId = "";
  let computeTier: ComputeTier | "" = "";
  let useCaseIds: string[] = [];
  let cpuCores: number | undefined;
  let ramGb: number | undefined;
  let storageGb: number | undefined;
  let domainBase = "";
  let metadataName = "";
  let additionalAuthoring: Record<string, string> = {};
  let busy = false;
  let kit: CatalogKit | undefined;
  let tierProfile: TierProfile | undefined;
  let includedUseCases: UseCaseFit[] = [];
  let requiredAuthoringInputs: string[] = [];
  let provenance: Pick<WebMcpCatalog, "source_sha" | "authority_bundle_sha256" | "catalog_sha256"> | undefined;

  $: catalog = session.service.catalog;
  $: kits = catalog?.kits ?? [];
  $: kit = kits.find((candidate) => candidate.stackkit_id === stackkitId);
  $: tierProfile = kit && computeTier ? kit.tiers[computeTier] : undefined;
  $: includedUseCases = tierProfile?.use_case_fits.filter((fit) => fit.included) ?? [];
  $: requiredAuthoringInputs = kit?.required_authoring_inputs ?? [];
  $: provenance = catalog ? {
    source_sha: catalog.source_sha,
    authority_bundle_sha256: catalog.authority_bundle_sha256,
    catalog_sha256: catalog.catalog_sha256,
  } : undefined;

  onMount(() => {
    const unsubscribe = session.subscribe((next) => {
      state = next;
      const selection = next.selection;
      stackkitId = selection?.stackkit_id ?? "";
      computeTier = selection?.compute_tier ?? "";
      useCaseIds = selection?.use_case_ids ? [...selection.use_case_ids] : [];
      cpuCores = next.declared_capacity?.cpu_cores;
      ramGb = next.declared_capacity?.ram_gb;
      storageGb = next.declared_capacity?.storage_gb;
    });
    if (!stackkitId && preselectedStackkitId && kits.some((candidate) => candidate.stackkit_id === preselectedStackkitId)) {
      stackkitId = preselectedStackkitId;
      session.setSelection({ stackkit_id: preselectedStackkitId });
    }
    return unsubscribe;
  });

  function currentSelection(): Selection {
    return {
      ...(stackkitId ? { stackkit_id: stackkitId } : {}),
      ...(computeTier ? { compute_tier: computeTier } : {}),
      ...(useCaseIds.length > 0 ? { use_case_ids: [...useCaseIds].sort() } : {}),
    };
  }

  function capacity(): Partial<DeclaredCapacity> {
    return {
      ...(typeof cpuCores === "number" ? { cpu_cores: cpuCores } : {}),
      ...(typeof ramGb === "number" ? { ram_gb: ramGb } : {}),
      ...(typeof storageGb === "number" ? { storage_gb: storageGb } : {}),
    };
  }

  function chooseKit(): void {
    computeTier = "";
    useCaseIds = [];
    domainBase = "";
    metadataName = "";
    additionalAuthoring = {};
    session.setSelection(currentSelection());
  }

  function chooseTier(): void {
    useCaseIds = [];
    session.setSelection(currentSelection());
  }

  function toggleUseCase(useCaseId: string, selected: boolean): void {
    useCaseIds = selected
      ? [...new Set([...useCaseIds, useCaseId])].sort()
      : useCaseIds.filter((candidate) => candidate !== useCaseId);
    session.setSelection(currentSelection());
  }

  function updateCapacity(): void {
    session.setCapacity(capacity());
  }

  async function runProfile(): Promise<void> {
    if (!stackkitId || !computeTier) return;
    busy = true;
    try {
      await session.invoke("stackkits_get_tier_profile", { stackkit_id: stackkitId, compute_tier: computeTier });
    } finally {
      busy = false;
    }
  }

  async function runAssessment(): Promise<void> {
    if (!stackkitId || !computeTier) return;
    busy = true;
    try {
      await session.invoke("stackkits_assess_capacity", {
        stackkit_id: stackkitId,
        compute_tier: computeTier,
        declared_capacity: capacity(),
      });
    } finally {
      busy = false;
    }
  }

  async function runHandoff(): Promise<void> {
    if (!stackkitId || !computeTier) return;
    const input: PrepareHandoffInput = {
      stackkit_id: stackkitId,
      compute_tier: computeTier,
      declared_capacity: capacity(),
      ...(useCaseIds.length > 0 ? { use_case_ids: [...useCaseIds] } : {}),
      ...((domainBase || metadataName) ? { authoring: {
        ...(domainBase ? { domain_base: domainBase } : {}),
        ...(metadataName ? { name: metadataName } : {}),
      } } : {}),
      ...(Object.keys(additionalAuthoring).length > 0 ? {
        authoring_inputs: Object.entries(additionalAuthoring)
          .filter(([, value]) => value.length > 0)
          .map(([path, value]) => ({ path, value })),
      } : {}),
    };
    busy = true;
    try {
      await session.invoke("stackkits_prepare_handoff", input);
    } finally {
      busy = false;
    }
  }

  function useCaseModule(fit: UseCaseFit): string {
    return fit.module_slug ?? fit.alternative_id ?? "CUE graph";
  }

  function runtimeRequirementSummary(module: ModuleRuntimeRequirement): string {
    if (module.declaration === "not_declared" || !module.runtime_requirements) return "Runtime requirements not declared";
    const requirements = module.runtime_requirements;
    const minimum = `${requirements.min_cpu_cores ?? "—"} CPU / ${requirements.min_ram_gb ?? "—"} GiB RAM / ${requirements.min_storage_gb ?? "—"} GiB storage`;
    const recommended = `${requirements.recommended_cpu_cores ?? "—"} CPU / ${requirements.recommended_ram_gb ?? "—"} GiB RAM / ${requirements.recommended_storage_gb ?? "—"} GiB storage`;
    return `minimum ${minimum} · recommended ${recommended}`;
  }

  function authoringValue(path: string): string {
    if (path === "network.domain.base") return domainBase;
    if (path === "metadata.name") return metadataName;
    return additionalAuthoring[path] ?? "";
  }

  function setAuthoringValue(path: string, value: string): void {
    if (path === "network.domain.base") domainBase = value;
    else if (path === "metadata.name") metadataName = value;
    else additionalAuthoring = { ...additionalAuthoring, [path]: value };
  }
</script>

<section class="planner-shell" data-testid="stackkits-planner">
  <header class="planner-hero">
    <div>
      <p class="eyebrow">StackKits WebMCP v1</p>
      <h1>Compute-tier planner</h1>
      <p class="lede">Choose a StackKit and compute tier explicitly, compare your declared capacity, and prepare a reviewable CLI handoff. Nothing is installed from this page.</p>
    </div>
    <div class:available={webMcpAvailable} class="agent-status" data-testid="webmcp-status">
      <span aria-hidden="true"></span>
      {webMcpAvailable ? "Browser-agent tools available" : "Planner available · agent tools unavailable"}
    </div>
  </header>

  {#if !catalog}
    <div class="integrity-error" role="alert" data-testid="authority-error">
      The public authority catalog could not be verified. WebMCP tools remain disabled while the website stays available.
    </div>
  {:else}
    <div class="planner-grid">
      <article class="panel selection-panel">
        <div class="step-heading"><span>1</span><div><h2>Choose the product graph</h2><p>No tier is inferred from inventory or host capacity.</p></div></div>
        <div class="field-grid">
          <label>
            <span>StackKit</span>
            <select bind:value={stackkitId} onchange={chooseKit} data-testid="stackkit-select">
              <option value="">Select a StackKit</option>
              {#each kits as candidate}
                <option value={candidate.stackkit_id}>{candidate.display_name}</option>
              {/each}
            </select>
          </label>
          <label>
            <span>Compute tier</span>
            <select bind:value={computeTier} onchange={chooseTier} disabled={!kit} data-testid="compute-tier-select">
              <option value="">Select a compute tier</option>
              {#each kit?.compute_tiers ?? [] as tier}
                <option value={tier}>{tier}</option>
              {/each}
            </select>
          </label>
        </div>
        <button class="secondary-action" onclick={runProfile} disabled={!stackkitId || !computeTier || busy} data-testid="profile-button">Show tier profile</button>

        {#if tierProfile}
          <div class="profile-summary" data-testid="tier-profile">
            <div>
              <span class="summary-label">Declared minimum</span>
              <strong>{tierProfile.host_requirements.min_cpu_cores} CPU · {tierProfile.host_requirements.min_ram_gb} GiB RAM · {tierProfile.host_requirements.min_storage_gb} GiB free</strong>
            </div>
            <div>
              <span class="summary-label">Recommended</span>
              <strong>{tierProfile.host_requirements.recommended_cpu_cores ?? "—"} CPU · {tierProfile.host_requirements.recommended_ram_gb ?? "—"} GiB RAM · {tierProfile.host_requirements.recommended_storage_gb ?? "—"} GiB free</strong>
            </div>
          </div>
          {#if Object.keys(tierProfile.module_substitutions).length > 0}
            <div class="substitutions">
              <span class="summary-label">CUE graph substitutions</span>
              {#each Object.entries(tierProfile.module_substitutions) as [from, to]}
                <code>{from} → {to}</code>
              {/each}
            </div>
          {/if}
          {#if tierProfile.enable_capabilities.length > 0}
            <div class="capabilities">
              <span class="summary-label">Capability activations</span>
              <div>{tierProfile.enable_capabilities.join(" · ")}</div>
            </div>
          {/if}
          <div class="module-requirements" data-testid="module-requirements">
            <span class="summary-label">Active module runtime declarations</span>
            {#each tierProfile.module_runtime_requirements as module}
              <div class="module-requirement" data-declaration={module.declaration}>
                <strong>{module.module_id}</strong>
                <small>{runtimeRequirementSummary(module)}</small>
              </div>
            {/each}
          </div>
          <div class="use-cases" data-testid="use-case-list">
            <div class="section-label"><span>Tier-dependent use cases</span><small>{includedUseCases.length} available</small></div>
            {#each tierProfile.use_case_fits as fit}
              <label class:excluded={!fit.included} class="use-case-row">
                <input
                  type="checkbox"
                  checked={useCaseIds.includes(fit.use_case_id)}
                  disabled={!fit.included}
                  onchange={(event) => toggleUseCase(fit.use_case_id, event.currentTarget.checked)}
                  data-use-case-id={fit.use_case_id}
                />
                <span class="use-case-copy">
                  <strong>{fit.title ?? fit.use_case_id}</strong>
                  {#if fit.included}
                    <small>{useCaseModule(fit)} · {fit.functions?.join(" · ")}</small>
                  {:else}
                    <small>{fit.reason ?? "Not declared for this tier."}</small>
                  {/if}
                </span>
                <span class:excluded={!fit.included} class="fit-pill">{fit.included ? "included" : "excluded"}</span>
              </label>
            {/each}
          </div>
        {/if}
      </article>

      <article class="panel capacity-panel">
        <div class="step-heading"><span>2</span><div><h2>Declare available capacity</h2><p>This is a declared-capacity comparison, not host compatibility.</p></div></div>
        <div class="capacity-inputs">
          <label><span>CPU cores</span><input type="number" min="1" bind:value={cpuCores} oninput={updateCapacity} data-testid="capacity-cpu" /></label>
          <label><span>RAM GiB</span><input type="number" min="0.1" step="0.1" bind:value={ramGb} oninput={updateCapacity} data-testid="capacity-ram" /></label>
          <label><span>Free storage GiB</span><input type="number" min="0.1" step="0.1" bind:value={storageGb} oninput={updateCapacity} data-testid="capacity-storage" /></label>
        </div>
        <button class="primary-action" onclick={runAssessment} disabled={!stackkitId || !computeTier || busy} data-testid="assess-button">Assess declared capacity</button>
        {#if state.capacity}
          <div class="assessment" data-status={state.capacity.overall} data-testid="capacity-result">
            <div class="assessment-head"><span>Overall</span><strong>{state.capacity.overall}</strong></div>
            {#each state.capacity.checks as check}
              <div class="axis-row">
                <span>{check.axis.replace("_", " ")}</span>
                <code>{check.observed ?? "—"} / min {check.minimum ?? "—"} / rec {check.recommended ?? "—"}</code>
                <strong data-status={check.status}>{check.status}</strong>
              </div>
            {/each}
          </div>
        {/if}
      </article>

      <article class="panel handoff-panel">
        <div class="step-heading"><span>3</span><div><h2>Prepare the CLI handoff</h2><p>Only init → validate → resolve → generate → plan. Apply is never executable here.</p></div></div>
        {#if requiredAuthoringInputs.length > 0}
          <div class="authoring-fields">
            {#each requiredAuthoringInputs as path}
              <label>
                <span>{path}</span>
                <input
                  value={authoringValue(path)}
                  oninput={(event) => setAuthoringValue(path, event.currentTarget.value)}
                  placeholder={path === "network.domain.base" ? "example.net" : "Required contract value"}
                  data-authoring-path={path}
                />
              </label>
            {/each}
          </div>
        {/if}
        <button class="primary-action" onclick={runHandoff} disabled={!stackkitId || !computeTier || busy} data-testid="handoff-button">Prepare handoff</button>

        {#if state.last_result?.notices.length}
          <div class="notices" aria-live="polite">
            {#each state.last_result.notices as notice}
              <p data-code={notice.code} class:error={notice.severity === "error"}>{notice.code}{notice.field ? ` · ${notice.field}` : ""}: {notice.message}</p>
            {/each}
          </div>
        {/if}

        {#if state.handoff}
          <div class="handoff-output" data-ready={state.handoff.ready} data-testid="handoff-result">
            <div class="assessment-head"><span>Handoff</span><strong>{state.handoff.ready ? "ready" : "blocked"}</strong></div>
            {#each state.handoff.steps as step, index}
              <div class="command-step">
                <span>{index + 1}</span>
                <div><strong>{step.id}</strong><code>{JSON.stringify(step.argv)}</code></div>
                <small>{step.mutation ? "local mutation" : "read only"} · {step.idempotent ? "idempotent" : "non-idempotent"} · {step.owner_approval ? "owner approval" : "no approval"}</small>
              </div>
            {/each}
            <div class="apply-boundary">
              <strong>{state.handoff.apply_follow_up.id}</strong>
              <span>Not executable · explicit owner approval required</span>
            </div>
          </div>
        {/if}
      </article>
    </div>

    {#if provenance}
      <footer class="provenance" data-testid="planner-provenance" data-source-sha={provenance.source_sha} data-catalog-sha={provenance.catalog_sha256} data-authority-sha={provenance.authority_bundle_sha256}>
        <span>CUE authority</span>
        <code>source {provenance.source_sha}</code>
        <code>authority {provenance.authority_bundle_sha256}</code>
        <code>catalog {provenance.catalog_sha256}</code>
      </footer>
    {/if}
  {/if}
</section>

<style>
  :global(*) { box-sizing: border-box; }
  .planner-shell { --accent: var(--color-primary, #f97316); --surface: var(--color-surface-container-low, #0f0f0f); --surface-high: var(--color-surface-container-high, #1c1c1c); --text: var(--color-on-surface, #e5e5e5); --muted: var(--color-on-surface-variant, #a3a3a3); --outline: var(--color-outline-variant, #2b2b2b); max-width: 1180px; margin: 0 auto; padding: 8rem 1.5rem 5rem; color: var(--text); }
  .planner-hero { display: flex; align-items: flex-start; justify-content: space-between; gap: 2rem; margin-bottom: 2rem; }
  .eyebrow, .summary-label, .section-label { color: var(--accent); font: 700 .72rem/1.2 ui-monospace, monospace; letter-spacing: .12em; text-transform: uppercase; }
  h1 { margin: .55rem 0 .9rem; font-size: clamp(2.6rem, 7vw, 5.2rem); line-height: .95; letter-spacing: -.055em; }
  .lede { max-width: 720px; margin: 0; color: var(--muted); font-size: 1.08rem; line-height: 1.65; }
  .agent-status { display: flex; align-items: center; gap: .55rem; flex: 0 0 auto; padding: .65rem .8rem; border: 1px solid var(--outline); border-radius: 999px; color: var(--muted); font-size: .78rem; background: color-mix(in srgb, var(--surface) 88%, transparent); }
  .agent-status span { width: .5rem; height: .5rem; border-radius: 50%; background: #737373; }
  .agent-status.available span { background: #4ade80; box-shadow: 0 0 0 4px rgb(74 222 128 / .1); }
  .planner-grid { display: grid; grid-template-columns: repeat(12, 1fr); gap: 1rem; }
  .panel { border: 1px solid var(--outline); border-radius: 1rem; padding: 1.35rem; background: linear-gradient(145deg, color-mix(in srgb, var(--surface-high) 75%, transparent), var(--surface)); box-shadow: 0 24px 80px rgb(0 0 0 / .16); }
  .selection-panel { grid-column: span 7; }
  .capacity-panel { grid-column: span 5; }
  .handoff-panel { grid-column: 1 / -1; }
  .step-heading { display: flex; gap: .8rem; margin-bottom: 1.25rem; }
  .step-heading > span { display: grid; place-items: center; width: 1.8rem; height: 1.8rem; flex: 0 0 auto; border-radius: .5rem; background: var(--accent); color: #0a0a0a; font-weight: 800; }
  h2 { margin: .05rem 0 .25rem; font-size: 1.08rem; }
  .step-heading p { margin: 0; color: var(--muted); font-size: .84rem; line-height: 1.45; }
  .field-grid, .capacity-inputs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; }
  .capacity-inputs { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  label > span { display: block; margin-bottom: .38rem; color: var(--muted); font-size: .76rem; font-weight: 650; }
  input, select { width: 100%; min-height: 2.7rem; border: 1px solid var(--outline); border-radius: .55rem; padding: .65rem .75rem; background: #090909; color: var(--text); font: inherit; outline: none; }
  input:focus, select:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgb(249 115 22 / .12); }
  input:disabled, select:disabled { opacity: .45; }
  button { min-height: 2.55rem; margin-top: .8rem; border: 0; border-radius: .55rem; padding: .6rem 1rem; cursor: pointer; font: 750 .82rem/1 inherit; }
  button:disabled { cursor: not-allowed; opacity: .42; }
  .primary-action { background: var(--accent); color: #0a0a0a; }
  .secondary-action { border: 1px solid var(--outline); background: transparent; color: var(--text); }
  .profile-summary { display: grid; grid-template-columns: 1fr 1fr; gap: .75rem; margin-top: 1rem; }
  .profile-summary > div, .substitutions, .capabilities, .module-requirements, .assessment, .handoff-output { border: 1px solid var(--outline); border-radius: .7rem; padding: .8rem; background: rgb(0 0 0 / .18); }
  .profile-summary strong { display: block; margin-top: .35rem; font-size: .78rem; }
  .substitutions { display: flex; flex-direction: column; gap: .35rem; margin-top: .75rem; overflow: hidden; }
  .capabilities, .module-requirements { margin-top: .75rem; }
  .capabilities div { margin-top: .35rem; color: var(--muted); font-size: .72rem; }
  .module-requirement { display: grid; grid-template-columns: minmax(10rem, auto) 1fr; gap: .7rem; padding-top: .45rem; margin-top: .45rem; border-top: 1px solid var(--outline); }
  .module-requirement strong { font: 700 .7rem/1.4 ui-monospace, monospace; }
  .module-requirement small { color: var(--muted); font-size: .68rem; line-height: 1.45; }
  code { font: .72rem/1.45 ui-monospace, SFMono-Regular, monospace; color: #d4d4d4; overflow-wrap: anywhere; }
  .use-cases { display: flex; flex-direction: column; gap: .42rem; margin-top: 1rem; }
  .section-label { display: flex; justify-content: space-between; align-items: center; margin-bottom: .2rem; }
  .section-label small { color: var(--muted); letter-spacing: 0; text-transform: none; }
  .use-case-row { display: grid; grid-template-columns: auto 1fr auto; align-items: center; gap: .65rem; margin: 0; padding: .62rem; border: 1px solid var(--outline); border-radius: .62rem; background: rgb(0 0 0 / .14); }
  .use-case-row.excluded { opacity: .64; }
  .use-case-row input { width: 1rem; min-height: 1rem; padding: 0; accent-color: var(--accent); }
  .use-case-copy strong, .use-case-copy small { display: block; }
  .use-case-copy strong { font-size: .8rem; }
  .use-case-copy small { margin-top: .15rem; color: var(--muted); font-size: .7rem; line-height: 1.4; }
  .fit-pill { margin: 0; padding: .25rem .42rem; border-radius: 999px; background: rgb(74 222 128 / .1); color: #86efac; font: 700 .62rem/1 ui-monospace, monospace; }
  .fit-pill.excluded { background: rgb(163 163 163 / .1); color: var(--muted); }
  .assessment { margin-top: 1rem; }
  .assessment-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: .65rem; }
  .assessment-head span { color: var(--muted); font-size: .75rem; }
  [data-status="pass"] > .assessment-head strong, strong[data-status="pass"] { color: #4ade80; }
  [data-status="warning"] > .assessment-head strong, strong[data-status="warning"] { color: #facc15; }
  [data-status="fail"] > .assessment-head strong, strong[data-status="fail"] { color: #f87171; }
  [data-status="unverified"] > .assessment-head strong, strong[data-status="unverified"] { color: #a3a3a3; }
  .axis-row { display: grid; grid-template-columns: 1fr auto auto; gap: .65rem; align-items: center; padding: .45rem 0; border-top: 1px solid var(--outline); font-size: .72rem; }
  .authoring-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; margin-bottom: .15rem; }
  .notices { margin-top: .8rem; }
  .notices p { margin: .35rem 0; padding: .6rem .7rem; border-left: 2px solid #facc15; background: rgb(250 204 21 / .06); color: #fde68a; font: .7rem/1.45 ui-monospace, monospace; }
  .notices p.error { border-color: #f87171; background: rgb(248 113 113 / .06); color: #fecaca; }
  .handoff-output { margin-top: 1rem; }
  .command-step { display: grid; grid-template-columns: auto 1fr auto; gap: .75rem; align-items: center; padding: .65rem 0; border-top: 1px solid var(--outline); }
  .command-step > span { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border: 1px solid var(--outline); border-radius: 50%; color: var(--muted); font-size: .68rem; }
  .command-step strong, .command-step code { display: block; }
  .command-step small { max-width: 220px; color: var(--muted); font-size: .66rem; text-align: right; }
  .apply-boundary { display: flex; justify-content: space-between; gap: 1rem; margin-top: .65rem; padding: .7rem; border: 1px dashed #f87171; border-radius: .55rem; color: #fecaca; font-size: .72rem; }
  .integrity-error { border: 1px solid #f87171; border-radius: .7rem; padding: 1rem; background: rgb(248 113 113 / .06); color: #fecaca; }
  .provenance { display: grid; grid-template-columns: auto 1fr; gap: .35rem .8rem; margin-top: 1rem; padding: 1rem; border-top: 1px solid var(--outline); color: var(--muted); overflow: hidden; }
  .provenance span { grid-row: 1 / 4; color: var(--accent); font-size: .72rem; font-weight: 800; }
  .provenance code { color: var(--muted); font-size: .62rem; }
  @media (max-width: 820px) { .planner-shell { padding-top: 6.5rem; } .planner-hero { flex-direction: column; } .selection-panel, .capacity-panel { grid-column: 1 / -1; } .capacity-inputs { grid-template-columns: 1fr; } .command-step { grid-template-columns: auto 1fr; } .command-step small { grid-column: 2; text-align: left; } }
  @media (max-width: 560px) { .field-grid, .profile-summary, .authoring-fields, .module-requirement { grid-template-columns: 1fr; } .module-requirement { gap: .2rem; } .axis-row { grid-template-columns: 1fr auto; } .axis-row code { grid-column: 1 / -1; } .apply-boundary, .provenance { display: flex; flex-direction: column; } }
</style>
