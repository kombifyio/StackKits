<script lang="ts">
  import { onMount } from "svelte";
  import CapacityInput from "./CapacityInput.svelte";
  import type { PlannerState, PlannerStateListener } from "./session.js";
  import type {
    Authoring,
    AuthoringInput,
    CapacityAxis,
    CapacityStatus,
    CatalogKit,
    CatalogModule,
    ModuleAxisProfile,
    ModuleComputeProfile,
    ModuleProfileSelection,
    PartialDeclaredCapacity,
    PrepareHandoffInput,
    Selection,
    ToolInputMap,
    ToolName,
    ToolResultMap,
    CatalogUseCase,
    UseCaseSelection,
    WebMcpCatalog,
    ResourceVector,
  } from "./types.js";

  interface PlannerSessionContract {
    readonly service: { readonly catalog?: WebMcpCatalog };
    readonly state: PlannerState;
    subscribe(listener: PlannerStateListener): () => void;
    setSelection(selection: Selection): void;
    setCapacity(capacity: PartialDeclaredCapacity): void;
    invoke<T extends ToolName>(tool: T, input: ToolInputMap[T], signal?: AbortSignal): Promise<ToolResultMap[T]>;
  }

  export let session: PlannerSessionContract;
  export let webMcpAvailable = false;
  export let preselectedStackkitId = "";

  let catalog: WebMcpCatalog | undefined;
  let kits: CatalogKit[] = [];
  let state: PlannerState = {};
  let stackkitId = "";
  let useCaseSelections: UseCaseSelection[] = [];
  let moduleProfileSelections: ModuleProfileSelection[] = [];
  let cpuCores: number | undefined;
  let ramGb: number | undefined;
  let storageGb: number | undefined;
  let domainBase = "";
  let metadataName = "";
  let additionalAuthoring: Record<string, string> = {};
  let busy = false;
  let kit: CatalogKit | undefined;
  let activeModules: CatalogModule[] = [];
  let requiredUseCases: CatalogUseCase[] = [];
  let selectedUseCaseIds: string[] = [];
  let selectedAlternativeByUseCase: Record<string, string> = {};
  let requiredUseCasesComplete = false;
  let activeProfilesComplete = false;
  let provenance: Pick<WebMcpCatalog, "source_sha" | "authority_bundle_sha256" | "catalog_sha256"> | undefined;

  $: catalog = session.service.catalog;
  $: kits = catalog?.kits ?? [];
  $: kit = kits.find((candidate) => candidate.stackkit_id === stackkitId);
  $: requiredUseCases = kit?.use_cases.filter((useCase) => useCase.required) ?? [];
  $: selectedUseCaseIds = useCaseSelections.map((selection) => selection.use_case_id);
  $: selectedAlternativeByUseCase = Object.fromEntries(useCaseSelections.map((selection) => [selection.use_case_id, selection.alternative_id]));
  $: activeModules = kit
    ? kit.modules.filter((module) => {
      if (module.required) return true;
      if (moduleProfileSelections.some((selection) => selection.module_id === module.module_id)) return true;
      return useCaseSelections.some((selection) => {
        const useCase = kit?.use_cases.find((candidate) => candidate.use_case_id === selection.use_case_id);
        return useCase?.alternatives.some((alternative) => alternative.alternative_id === selection.alternative_id && alternative.module_id === module.module_id) ?? false;
      });
    })
    : [];
  // Pass every source collection explicitly: legacy Svelte reactivity does not
  // inspect dependencies captured inside a helper called from markup.
  $: requiredUseCasesComplete = hasAllRequiredUseCases(requiredUseCases, useCaseSelections);
  $: activeProfilesComplete = hasAllActiveProfiles(activeModules, moduleProfileSelections);
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
      useCaseSelections = selection?.use_cases ? [...selection.use_cases] : [];
      moduleProfileSelections = selection?.module_profiles ? [...selection.module_profiles] : [];
      cpuCores = next.declared_capacity?.cpu_cores;
      ramGb = next.declared_capacity?.ram_gb;
      storageGb = next.declared_capacity?.storage_gb;
    });
    if (!stackkitId && preselectedStackkitId && kits.some((candidate) => candidate.stackkit_id === preselectedStackkitId)) {
      stackkitId = preselectedStackkitId;
      session.setSelection(currentSelection());
    }
    return unsubscribe;
  });

  function currentSelection(): Selection {
    return {
      ...(stackkitId ? { stackkit_id: stackkitId } : {}),
      ...(moduleProfileSelections.length > 0 ? { module_profiles: [...moduleProfileSelections].sort((left, right) => left.module_id.localeCompare(right.module_id)) } : {}),
      ...(useCaseSelections.length > 0 ? { use_cases: [...useCaseSelections].sort((left, right) => left.use_case_id.localeCompare(right.use_case_id)) } : {}),
    };
  }

  function capacity(): PartialDeclaredCapacity {
    return {
      ...(typeof cpuCores === "number" ? { cpu_cores: cpuCores } : {}),
      ...(typeof ramGb === "number" ? { ram_gb: ramGb } : {}),
      ...(typeof storageGb === "number" ? { storage_gb: storageGb } : {}),
    };
  }

  function chooseKit(value: string): void {
    stackkitId = value;
    useCaseSelections = [];
    moduleProfileSelections = [];
    domainBase = "";
    metadataName = "";
    additionalAuthoring = {};
    session.setSelection(currentSelection());
  }

  function chooseUseCase(useCaseId: string, alternativeId: string): void {
    useCaseSelections = alternativeId
      ? [...useCaseSelections.filter((selection) => selection.use_case_id !== useCaseId), { use_case_id: useCaseId, alternative_id: alternativeId }]
          .sort((left, right) => left.use_case_id.localeCompare(right.use_case_id))
      : useCaseSelections.filter((selection) => selection.use_case_id !== useCaseId);
    const activeIds = activeModuleIds(useCaseSelections);
    moduleProfileSelections = moduleProfileSelections.filter((selection) => activeIds.has(selection.module_id));
    session.setSelection(currentSelection());
  }

  function activeModuleIds(selections: UseCaseSelection[]): Set<string> {
    const ids = new Set(kit?.modules.filter((module) => module.required).map((module) => module.module_id) ?? []);
    for (const selection of selections) {
      const useCase = kit?.use_cases.find((candidate) => candidate.use_case_id === selection.use_case_id);
      const alternative = useCase?.alternatives.find((candidate) => candidate.alternative_id === selection.alternative_id);
      if (alternative) ids.add(alternative.module_id);
    }
    return ids;
  }

  function chooseModuleProfile(moduleId: string, axis: "compute_profile" | "storage_profile" | "accelerator_profile", value: string): void {
    const existing = moduleProfileSelections.find((selection) => selection.module_id === moduleId);
    if (axis === "compute_profile" && !value) {
      moduleProfileSelections = moduleProfileSelections.filter((selection) => selection.module_id !== moduleId);
    } else if (!existing) {
      if (axis !== "compute_profile" || !value) return;
      moduleProfileSelections = [...moduleProfileSelections, { module_id: moduleId, compute_profile: value }];
    } else {
      const next = { ...existing } as ModuleProfileSelection;
      if (value) next[axis] = value;
      else delete next[axis];
      moduleProfileSelections = moduleProfileSelections.map((selection) => selection.module_id === moduleId ? next : selection);
    }
    session.setSelection(currentSelection());
  }

  function updateCapacityField(axis: CapacityAxis, raw: string): void {
    const numeric = raw.trim() === "" ? undefined : Number(raw);
    const parsed = numeric !== undefined && Number.isFinite(numeric) ? numeric : undefined;
    if (axis === "cpu_cores") cpuCores = parsed;
    if (axis === "ram_gb") ramGb = parsed;
    if (axis === "storage_gb") storageGb = parsed;
    session.setCapacity(capacity());
  }

  async function runProfiles(): Promise<void> {
    if (!stackkitId) return;
    busy = true;
    try {
      await session.invoke("stackkits_get_module_profiles", {
        stackkit_id: stackkitId,
        ...(selectedUseCaseIds.length > 0 ? { use_case_ids: [...selectedUseCaseIds] } : {}),
      });
    } finally {
      busy = false;
    }
  }

  async function runAssessment(): Promise<void> {
    if (!stackkitId || moduleProfileSelections.length === 0) return;
    busy = true;
    try {
      await session.invoke("stackkits_assess_capacity", {
        stackkit_id: stackkitId,
        module_profiles: [...moduleProfileSelections],
        ...(useCaseSelections.length > 0 ? { use_cases: [...useCaseSelections] } : {}),
        declared_capacity: capacity(),
      });
    } finally {
      busy = false;
    }
  }

  async function runHandoff(): Promise<void> {
    if (!stackkitId || moduleProfileSelections.length === 0) return;
    const authoring: Authoring = {
      ...(domainBase ? { domain_base: domainBase } : {}),
      ...(metadataName ? { name: metadataName } : {}),
    };
    const authoringInputs: AuthoringInput[] = Object.entries(additionalAuthoring)
      .filter(([, value]) => value.length > 0)
      .map(([path, value]) => ({ path, value }));
    const input: PrepareHandoffInput = {
      stackkit_id: stackkitId,
      module_profiles: [...moduleProfileSelections],
      ...(useCaseSelections.length > 0 ? { use_cases: [...useCaseSelections] } : {}),
      declared_capacity: capacity(),
      ...(Object.keys(authoring).length > 0 ? { authoring } : {}),
      ...(authoringInputs.length > 0 ? { authoring_inputs: authoringInputs } : {}),
    };
    busy = true;
    try {
      await session.invoke("stackkits_prepare_handoff", input);
    } finally {
      busy = false;
    }
  }

  function moduleSelection(moduleId: string): ModuleProfileSelection | undefined {
    return moduleProfileSelections.find((selection) => selection.module_id === moduleId);
  }

  function computeProfile(module: CatalogModule, selection = moduleSelection(module.module_id)): ModuleComputeProfile | undefined {
    return selection?.compute_profile ? module.compute_profiles.find((profile) => profile.id === selection.compute_profile) : undefined;
  }

  function axisSelection(module: CatalogModule, axis: "storage_profile" | "accelerator_profile", selection = moduleSelection(module.module_id)): ModuleAxisProfile | undefined {
    const id = selection?.[axis];
    const profiles = axis === "storage_profile" ? module.storage_profiles : module.accelerator_profiles;
    return id ? profiles.find((profile) => profile.id === id) : undefined;
  }

  function formatVector(vector?: ResourceVector): string {
    if (!vector) return "not declared";
    return `${formatValue(vector.cpu_cores)} CPU · ${formatValue(vector.ram_gb)} GiB RAM · ${formatValue(vector.storage_gb)} GiB storage`;
  }

  function formatValue(value?: number): string {
    return typeof value === "number" ? String(value) : "not declared";
  }

  function profileSummary(profile?: ModuleComputeProfile | ModuleAxisProfile): string {
    if (!profile) return "No profile selected";
    return `${profile.id} · ${profile.capacity_declaration} · ${profile.realization}`;
  }

  function axisLabel(axis: string): string {
    return axis.replace("_", " ");
  }

  function readValue(event: Event): string {
    const target = event.currentTarget;
    return target instanceof HTMLInputElement || target instanceof HTMLSelectElement ? target.value : "";
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

  function statusLabel(status: CapacityStatus): string {
    return status === "unverified" ? "unverified" : status;
  }

  function hasAllRequiredUseCases(useCases: CatalogUseCase[], selections: UseCaseSelection[]): boolean {
    const selectedUseCaseIds = new Set(selections.map((selection) => selection.use_case_id));
    return useCases.every((useCase) => selectedUseCaseIds.has(useCase.use_case_id));
  }

  function hasAllActiveProfiles(modules: CatalogModule[], selections: ModuleProfileSelection[]): boolean {
    const selectionByModule = new Map(selections.map((selection) => [selection.module_id, selection]));
    return modules.every((module) => {
      const selection = selectionByModule.get(module.module_id);
      return (module.compute_profiles.length === 0 || Boolean(selection?.compute_profile))
        && (module.storage_profiles.length === 0 || Boolean(selection?.storage_profile))
        && (module.accelerator_profiles.length === 0 || Boolean(selection?.accelerator_profile));
    });
  }
</script>

<section class="planner-shell" data-testid="stackkits-planner">
  <header class="planner-hero">
    <div>
      <p class="eyebrow">StackKits WebMCP v2alpha1</p>
      <h1>Module profile planner</h1>
      <p class="lede">Select each use-case alternative and every module-local profile explicitly. Compare your declared capacity with CUE facts, then prepare a reviewable CLI handoff. Nothing is installed from this page.</p>
    </div>
    <div class:available={webMcpAvailable} class="agent-status" data-testid="webmcp-status">
      <span aria-hidden="true"></span>
      {webMcpAvailable ? "Browser-agent tools available" : "Planner available · agent tools unavailable"}
    </div>
  </header>

  {#if !catalog}
    <div class="integrity-error" role="alert" data-testid="authority-error">
      The public CUE authority catalog could not be verified. WebMCP tools remain disabled while the planner stays available for diagnostics.
    </div>
  {:else}
    <div class="planner-grid">
      <article class="panel selection-panel">
        <div class="step-heading"><span>1</span><div><h2>Choose the product graph</h2><p>Use-case alternatives and module profiles are explicit; no global compute tier is inferred.</p></div></div>
        <label>
          <span>StackKit</span>
          <select value={stackkitId} onchange={(event) => chooseKit(readValue(event))} data-testid="stackkit-select">
            <option value="">Select a StackKit</option>
            {#each kits as candidate}
              <option value={candidate.stackkit_id}>{candidate.display_name} · {candidate.version}</option>
            {/each}
          </select>
        </label>

        {#if kit}
          <div class="use-cases" data-testid="use-case-list">
            <div class="section-label"><span>Use-case alternatives</span><small>Required selections are marked</small></div>
            {#each kit.use_cases as useCase (useCase.use_case_id)}
              <div class="use-case-row" data-use-case-id={useCase.use_case_id} data-required={useCase.required}>
                <div class="use-case-copy">
                  <strong>{useCase.title}</strong>
                  <small>{useCase.required ? "Required workload" : "Optional workload"} · {useCase.availability}</small>
                </div>
                {#if useCase.availability === "available" && useCase.alternatives.length > 0}
                  {#key selectedAlternativeByUseCase[useCase.use_case_id] ?? ""}
                    <select value={selectedAlternativeByUseCase[useCase.use_case_id] ?? ""} onchange={(event) => chooseUseCase(useCase.use_case_id, readValue(event))} data-testid={`use-case-${useCase.use_case_id}`}>
                      <option value="">{useCase.required ? "Select an alternative" : "Not selected"}</option>
                      {#each useCase.alternatives as alternative}
                        <option value={alternative.alternative_id}>{alternative.alternative_id} · {alternative.module_id}</option>
                      {/each}
                    </select>
                  {/key}
                {:else}
                  <span class="fit-pill excluded">{useCase.reason_code ?? "blocked"}</span>
                {/if}
              </div>
            {/each}
          </div>

          <div class="selection-actions">
            <button class="secondary-action" onclick={runProfiles} disabled={busy} data-testid="profile-button">{busy ? "Loading…" : "Show module profiles"}</button>
            {#if state.module_profiles}
              <small class="agent-sync">Agent/UI state synced · {state.module_profiles.modules.length} module profile records</small>
            {/if}
          </div>
        {/if}
      </article>

      <article class="panel modules-panel">
        <div class="step-heading"><span>2</span><div><h2>Select module-local profiles</h2><p>Compute, storage, and accelerator dimensions are independent. Plan-only contracts remain visible without artificial profiles.</p></div></div>
        {#if !kit}
          <p class="empty-state">Select a StackKit to inspect its CUE-declared modules.</p>
        {:else if activeModules.length === 0}
          <p class="empty-state">Select the required use-case alternative to reveal its active modules.</p>
        {:else}
          <div class="module-list" data-testid="module-profile-list">
            {#each activeModules as module (module.module_id)}
              {@const selection = moduleSelection(module.module_id)}
              {@const selectedCompute = computeProfile(module, selection)}
              {@const selectedStorage = axisSelection(module, "storage_profile", selection)}
              {@const selectedAccelerator = axisSelection(module, "accelerator_profile", selection)}
              <section class="module-card" data-module-id={module.module_id} data-has-compute={module.compute_profiles.length > 0}>
                <div class="module-head">
                  <div><strong>{module.module_id}</strong><small>{module.role} · {module.required ? "required module" : "selected workload module"}</small></div>
                  {#if module.compute_profiles.length === 0}<span class="fit-pill neutral">plan-only / no compute profile</span>{/if}
                </div>
                {#if module.compute_profiles.length > 0}
                  <label>
                    <span>Compute profile <em>required</em></span>
                    <select value={selection?.compute_profile ?? ""} onchange={(event) => chooseModuleProfile(module.module_id, "compute_profile", readValue(event))} data-testid={`compute-profile-${module.module_id}`}>
                      <option value="">Select a compute profile</option>
                      {#each module.compute_profiles as profile}
                        <option value={profile.id}>{profile.id} · {profile.capacity_declaration} · {profile.realization}</option>
                      {/each}
                    </select>
                  </label>
                {/if}
                <div class="axis-selectors">
                  <label>
                    <span>Storage profile <em>{module.storage_profiles.length > 0 ? "required" : "optional"}</em></span>
                    {#if module.storage_profiles.length > 0}
                      <select value={selection?.storage_profile ?? ""} onchange={(event) => chooseModuleProfile(module.module_id, "storage_profile", readValue(event))} data-testid={`storage-profile-${module.module_id}`}>
                        <option value="">No storage profile selected</option>
                        {#each module.storage_profiles as profile}<option value={profile.id}>{profile.id} · {profile.capacity_declaration}</option>{/each}
                      </select>
                    {:else}<small class="not-declared">No storage profile declared</small>{/if}
                  </label>
                  <label>
                    <span>Accelerator profile <em>{module.accelerator_profiles.length > 0 ? "required" : "optional"}</em></span>
                    {#if module.accelerator_profiles.length > 0}
                      <select value={selection?.accelerator_profile ?? ""} onchange={(event) => chooseModuleProfile(module.module_id, "accelerator_profile", readValue(event))} data-testid={`accelerator-profile-${module.module_id}`}>
                        <option value="">No accelerator profile selected</option>
                        {#each module.accelerator_profiles as profile}<option value={profile.id}>{profile.id} · {profile.capacity_declaration}</option>{/each}
                      </select>
                    {:else}<small class="not-declared">No accelerator profile declared</small>{/if}
                  </label>
                </div>
                {#if selectedCompute || selectedStorage || selectedAccelerator}
                  <div class="facts" data-testid={`module-facts-${module.module_id}`}>
                    {#if selectedCompute}
                      <div class="fact-group">
                        <span class="summary-label">Compute facts · {selectedCompute.id}</span>
                        <p><b>Host floor</b> {formatVector(selectedCompute.host_floor)}</p>
                        <p><b>Reservation</b> {formatVector(selectedCompute.reservation)}</p>
                        <p><b>CUE reference</b> {formatVector(selectedCompute.recommended)}</p>
                        <p><b>Status</b> {selectedCompute.capacity_declaration} · {selectedCompute.maturity} · {selectedCompute.executable ? "executable" : "contract-only"}</p>
                        <p><b>Realization</b> {selectedCompute.realization}</p>
                        {#if selectedCompute.degradations.length > 0}<p><b>Degradations</b> {selectedCompute.degradations.join(" · ")}</p>{/if}
                      </div>
                    {/if}
                    {#if selectedStorage}
                      <div class="fact-group"><span class="summary-label">Storage facts · {selectedStorage.id}</span><p><b>Reservation</b> {formatVector(selectedStorage.reservation)}</p><p><b>Status</b> {profileSummary(selectedStorage)} </p></div>
                    {/if}
                    {#if selectedAccelerator}
                      <div class="fact-group"><span class="summary-label">Accelerator facts · {selectedAccelerator.id}</span><p><b>Reservation</b> {formatVector(selectedAccelerator.reservation)}</p><p><b>Status</b> {profileSummary(selectedAccelerator)} </p></div>
                    {/if}
                  </div>
                {:else if module.compute_profiles.length === 0}
                  <p class="not-declared">This active contract has no resource profile declaration. It is carried as a graph fact only.</p>
                {:else}
                  <p class="not-declared">Choose a compute profile to reveal its declared floor, reservation, and reference values.</p>
                {/if}
              </section>
            {/each}
          </div>
          {#if !requiredUseCasesComplete}<p class="inline-warning">Choose an alternative for every required use case before assessment or handoff.</p>{/if}
          {#if !activeProfilesComplete}<p class="inline-warning">Every declared active module dimension needs an explicit local profile.</p>{/if}
        {/if}
      </article>

      <article class="panel capacity-panel">
        <div class="step-heading"><span>3</span><div><h2>Declare available capacity</h2><p>Enter values explicitly. Sliders mirror the numeric inputs and never supply hidden capacity.</p></div></div>
        <div class="capacity-inputs">
          <CapacityInput id="capacity-cpu" label="CPU cores" unit="CPU cores" value={cpuCores} minimum={1} maximum={1024} wholeNumber onValue={(raw) => updateCapacityField("cpu_cores", raw)} />
          <CapacityInput id="capacity-ram" label="RAM GiB" unit="GiB RAM" value={ramGb} minimum={0.0625} maximum={16384} onValue={(raw) => updateCapacityField("ram_gb", raw)} />
          <CapacityInput id="capacity-storage" label="Free storage GiB" unit="GiB storage" value={storageGb} minimum={0.0625} maximum={1048576} onValue={(raw) => updateCapacityField("storage_gb", raw)} />
        </div>
        <p class="input-note">Blank means undeclared and produces an unverified result.</p>
        <button class="primary-action" onclick={runAssessment} disabled={!kit || moduleProfileSelections.length === 0 || busy} data-testid="assess-button">{busy ? "Assessing…" : "Assess declared capacity"}</button>
        {#if state.capacity}
          <div class="assessment" data-status={state.capacity.overall} data-testid="capacity-result">
            <div class="assessment-head"><span>Overall capacity status</span><strong data-status={state.capacity.overall}>{statusLabel(state.capacity.overall)}</strong></div>
            {#each state.capacity.checks as check}
              <div class="axis-row" data-status={check.status}>
                <span>{axisLabel(check.axis)}</span>
                <code>{check.observed ?? "not declared"} / minimum {check.minimum ?? "not declared"} / reference {check.recommended ?? "not declared"}</code>
                <strong data-status={check.status}>{statusLabel(check.status)}</strong>
              </div>
            {/each}
            {#if state.capacity.unverified_modules.length > 0}<p class="not-declared">Unverified module facts: {state.capacity.unverified_modules.join(" · ")}</p>{/if}
          </div>
        {/if}
      </article>

      <article class="panel handoff-panel">
        <div class="step-heading"><span>4</span><div><h2>Prepare the CLI handoff</h2><p>The browser only prepares init → validate → resolve → generate → plan. CLI steps that write local files are marked below. Apply remains an approval-gated follow-up.</p></div></div>
        {#if requiredUseCases.length > 0 && !requiredUseCasesComplete}<p class="inline-warning">Required use-case alternatives are still missing.</p>{/if}
        {#if kit && kit.required_authoring_inputs.length > 0}
          <div class="authoring-fields">
            {#each kit.required_authoring_inputs as path}
              <label><span>{path}</span><input value={authoringValue(path)} oninput={(event) => setAuthoringValue(path, readValue(event))} placeholder={path === "network.domain.base" ? "example.net" : "Required contract value"} data-authoring-path={path} /></label>
            {/each}
          </div>
        {/if}
        <p class="input-note">Incomplete or partial CUE resource facts remain unverified; the handoff stays blocked until every selected resource axis is declared.</p>
        <button class="primary-action" onclick={runHandoff} disabled={!kit || moduleProfileSelections.length === 0 || busy} data-testid="handoff-button">{busy ? "Preparing…" : "Prepare handoff"}</button>

        {#if state.last_result?.notices.length}
          <div class="notices" aria-live="polite">
            {#each state.last_result.notices as notice}
              <p data-code={notice.code} class:error={notice.severity === "error"}>{notice.code}{notice.field ? ` · ${notice.field}` : ""}: {notice.message}</p>
            {/each}
          </div>
        {/if}

        {#if state.handoff}
          <div class="handoff-output" data-ready={state.handoff.ready} data-testid="handoff-result">
            <div class="assessment-head"><span>Handoff</span><strong data-status={state.handoff.ready ? "pass" : "fail"}>{state.handoff.ready ? "ready" : "blocked"}</strong></div>
            {#if state.handoff.blocked_reasons?.length}<p class="not-declared">Blocked reasons: {state.handoff.blocked_reasons.join(" · ")}</p>{/if}
            {#each state.handoff.steps as [id, argv, mutation, idempotent, ownerApproval], index}
              <div class="command-step">
                <span>{index + 1}</span>
                <div><strong>{id}</strong><code>{JSON.stringify(argv)}</code></div>
                <small>{mutation ? "local mutation" : "read only"} · {idempotent ? "idempotent" : "non-idempotent"} · {ownerApproval ? "owner approval" : "no approval"}</small>
              </div>
            {/each}
            <div class="apply-boundary"><strong>{state.handoff.apply_follow_up.id}</strong><span>Not executable · explicit owner approval required</span></div>
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
  .planner-shell { --accent: var(--color-primary, #f97316); --surface: var(--color-surface-container-low, #0f0f0f); --surface-high: var(--color-surface-container-high, #1c1c1c); --text: var(--color-on-surface, #e5e5e5); --muted: var(--color-on-surface-variant, #a3a3a3); --outline: var(--color-outline-variant, #2b2b2b); max-width: 1240px; margin: 0 auto; padding: 8rem 1.5rem 5rem; color: var(--text); }
  .planner-hero { display: flex; align-items: flex-start; justify-content: space-between; gap: 2rem; margin-bottom: 2rem; }
  .eyebrow, .summary-label, .section-label { color: var(--accent); font: 700 .72rem/1.2 ui-monospace, monospace; letter-spacing: .12em; text-transform: uppercase; }
  h1 { margin: .55rem 0 .9rem; font-size: clamp(2.6rem, 7vw, 5.2rem); line-height: .95; letter-spacing: -.055em; }
  .lede { max-width: 760px; margin: 0; color: var(--muted); font-size: 1.08rem; line-height: 1.65; }
  .agent-status { display: flex; align-items: center; gap: .55rem; flex: 0 0 auto; padding: .65rem .8rem; border: 1px solid var(--outline); border-radius: 999px; color: var(--muted); font-size: .78rem; background: color-mix(in srgb, var(--surface) 88%, transparent); }
  .agent-status span { width: .5rem; height: .5rem; border-radius: 50%; background: #737373; }
  .agent-status.available span { background: #4ade80; box-shadow: 0 0 0 4px rgb(74 222 128 / .1); }
  .planner-grid { display: grid; grid-template-columns: repeat(12, 1fr); gap: 1rem; }
  .panel { border: 1px solid var(--outline); border-radius: 1rem; padding: 1.35rem; background: linear-gradient(145deg, color-mix(in srgb, var(--surface-high) 75%, transparent), var(--surface)); box-shadow: 0 24px 80px rgb(0 0 0 / .16); }
  .selection-panel { grid-column: span 5; }
  .modules-panel { grid-column: span 7; }
  .capacity-panel { grid-column: 1 / -1; }
  .handoff-panel { grid-column: 1 / -1; }
  .step-heading { display: flex; gap: .8rem; margin-bottom: 1.25rem; }
  .step-heading > span { display: grid; place-items: center; width: 1.8rem; height: 1.8rem; flex: 0 0 auto; border-radius: .5rem; background: var(--accent); color: #0a0a0a; font-weight: 800; }
  h2 { margin: .05rem 0 .25rem; font-size: 1.08rem; }
  .step-heading p { margin: 0; color: var(--muted); font-size: .84rem; line-height: 1.45; }
  label > span { display: block; margin-bottom: .38rem; color: var(--muted); font-size: .76rem; font-weight: 650; }
  label em { color: var(--accent); font-size: .65rem; font-style: normal; font-weight: 500; }
  input, select { width: 100%; min-height: 2.7rem; border: 1px solid var(--outline); border-radius: .55rem; padding: .65rem .75rem; background: #090909; color: var(--text); font: inherit; outline: none; }
  input:focus, select:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgb(249 115 22 / .12); }
  input:disabled, select:disabled { opacity: .45; }
  button { min-height: 2.55rem; margin-top: .8rem; border: 0; border-radius: .55rem; padding: .6rem 1rem; cursor: pointer; font: 750 .82rem/1 inherit; }
  button:disabled { cursor: not-allowed; opacity: .42; }
  .primary-action { background: var(--accent); color: #0a0a0a; }
  .secondary-action { border: 1px solid var(--outline); background: transparent; color: var(--text); }
  .use-cases, .module-list { display: flex; flex-direction: column; gap: .55rem; margin-top: 1rem; }
  .section-label { display: flex; justify-content: space-between; align-items: center; margin-bottom: .2rem; }
  .section-label small { color: var(--muted); letter-spacing: 0; text-transform: none; }
  .use-case-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(11rem, 15rem); align-items: center; gap: .7rem; padding: .7rem; border: 1px solid var(--outline); border-radius: .62rem; background: rgb(0 0 0 / .14); }
  .use-case-copy strong, .use-case-copy small { display: block; }
  .use-case-copy strong { font-size: .8rem; }
  .use-case-copy small { margin-top: .15rem; color: var(--muted); font-size: .7rem; line-height: 1.4; }
  .fit-pill { display: inline-flex; align-items: center; justify-content: center; margin: 0; padding: .25rem .42rem; border-radius: 999px; background: rgb(74 222 128 / .1); color: #86efac; font: 700 .62rem/1.2 ui-monospace, monospace; }
  .fit-pill.excluded, .fit-pill.neutral { background: rgb(163 163 163 / .1); color: var(--muted); }
  .selection-actions { display: flex; align-items: center; gap: .7rem; flex-wrap: wrap; }
  .agent-sync, .input-note { color: var(--muted); font-size: .68rem; }
  .module-card { border: 1px solid var(--outline); border-radius: .7rem; padding: .8rem; background: rgb(0 0 0 / .16); }
  .module-head { display: flex; justify-content: space-between; align-items: flex-start; gap: .7rem; margin-bottom: .65rem; }
  .module-head strong, .module-head small { display: block; }
  .module-head strong { font: 700 .78rem/1.4 ui-monospace, monospace; }
  .module-head small { margin-top: .15rem; color: var(--muted); font-size: .68rem; }
  .axis-selectors { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .65rem; margin-top: .65rem; }
  .facts { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .55rem; margin-top: .75rem; }
  .fact-group { border-top: 1px solid var(--outline); padding-top: .55rem; }
  .fact-group p { margin: .3rem 0 0; color: var(--muted); font-size: .68rem; line-height: 1.45; }
  .fact-group b { color: var(--text); font-weight: 650; }
  .not-declared, .empty-state, .inline-warning { margin: .65rem 0 0; color: var(--muted); font-size: .72rem; line-height: 1.45; }
  .inline-warning { color: #fde68a; }
  .capacity-inputs { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .75rem; }
  .assessment, .handoff-output { border: 1px solid var(--outline); border-radius: .7rem; padding: .8rem; margin-top: 1rem; background: rgb(0 0 0 / .18); }
  .assessment-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: .65rem; }
  .assessment-head span { color: var(--muted); font-size: .75rem; }
  [data-status="pass"] > .assessment-head strong, strong[data-status="pass"] { color: #4ade80; }
  [data-status="warning"] > .assessment-head strong, strong[data-status="warning"] { color: #facc15; }
  [data-status="fail"] > .assessment-head strong, strong[data-status="fail"] { color: #f87171; }
  [data-status="unverified"] > .assessment-head strong, strong[data-status="unverified"] { color: #a3a3a3; }
  .axis-row { display: grid; grid-template-columns: 1fr minmax(18rem, auto) auto; gap: .65rem; align-items: center; padding: .45rem 0; border-top: 1px solid var(--outline); font-size: .72rem; }
  .authoring-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; margin-bottom: .15rem; }
  .notices { margin-top: .8rem; }
  .notices p { margin: .35rem 0; padding: .6rem .7rem; border-left: 2px solid #facc15; background: rgb(250 204 21 / .06); color: #fde68a; font: .7rem/1.45 ui-monospace, monospace; }
  .notices p.error { border-color: #f87171; background: rgb(248 113 113 / .06); color: #fecaca; }
  .command-step { display: grid; grid-template-columns: auto 1fr auto; gap: .75rem; align-items: center; padding: .65rem 0; border-top: 1px solid var(--outline); }
  .command-step > span { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border: 1px solid var(--outline); border-radius: 50%; color: var(--muted); font-size: .68rem; }
  .command-step strong, .command-step code { display: block; }
  .command-step small { max-width: 220px; color: var(--muted); font-size: .66rem; text-align: right; }
  code { font: .72rem/1.45 ui-monospace, SFMono-Regular, monospace; color: #d4d4d4; overflow-wrap: anywhere; }
  .apply-boundary { display: flex; justify-content: space-between; gap: 1rem; margin-top: .65rem; padding: .7rem; border: 1px dashed #f87171; border-radius: .55rem; color: #fecaca; font-size: .72rem; }
  .integrity-error { border: 1px solid #f87171; border-radius: .7rem; padding: 1rem; background: rgb(248 113 113 / .06); color: #fecaca; }
  .provenance { display: grid; grid-template-columns: auto 1fr; gap: .35rem .8rem; margin-top: 1rem; padding: 1rem; border-top: 1px solid var(--outline); color: var(--muted); overflow: hidden; }
  .provenance span { grid-row: 1 / 4; color: var(--accent); font-size: .72rem; font-weight: 800; }
  .provenance code { color: var(--muted); font-size: .62rem; }
  @media (max-width: 980px) { .selection-panel, .modules-panel { grid-column: 1 / -1; } .facts { grid-template-columns: 1fr; } }
  @media (max-width: 680px) { .planner-shell { padding-top: 6.5rem; } .planner-hero { flex-direction: column; } .use-case-row, .axis-selectors, .capacity-inputs, .authoring-fields { grid-template-columns: 1fr; } .axis-row { grid-template-columns: 1fr auto; } .axis-row code { grid-column: 1 / -1; } .command-step { grid-template-columns: auto 1fr; } .command-step small { grid-column: 2; text-align: left; } .apply-boundary, .provenance { display: flex; flex-direction: column; } }
</style>
