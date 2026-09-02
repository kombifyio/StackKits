import {
  COMPUTE_PROFILE_ORDER,
  REQUIRED_OPERATION_IDS,
  validateCatalog,
  type CatalogValidation,
} from "./catalog.js";
import { abortedResult, authorityFailure, makeNotice, makeResult } from "./result.js";
import { validateToolInput } from "./schema.js";
import type {
  AssessCapacityInput,
  CapacityAxis,
  CapacityCheck,
  CapacityData,
  CapacityStatus,
  CatalogKit,
  CatalogModule,
  CatalogUseCase,
  DeclaredCapacity,
  GetModuleProfilesInput,
  HandoffData,
  HandoffFollowUp,
  HandoffStep,
  ListCatalogData,
  ListCatalogInput,
  ModuleAxisProfile,
  ModuleComputeProfile,
  ModuleProfileSelection,
  ModuleProfilesData,
  OperationMetadata,
  PartialDeclaredCapacity,
  PrepareHandoffInput,
  ResourceVector,
  Selection,
  ToolInputMap,
  ToolName,
  ToolResultMap,
  UseCaseSelection,
  WebMcpCatalog,
  WebMcpResult,
} from "./types.js";

const CAPACITY_AXES = ["cpu_cores", "ram_gb", "storage_gb"] as const;
const AUTHORING_DOMAIN = /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;
const AUTHORING_NAME = /^[a-z][a-z0-9-]{0,62}$/;

export interface PlannerOptions {
  sourceSha?: string;
}

export interface PlannerInvocationOptions {
  signal?: AbortSignal;
}

export type PlannerResult<T extends ToolName> = ToolResultMap[T];

interface ResolvedSelection {
  selections: ModuleProfileSelection[];
  useCases: UseCaseSelection[];
  modules: Array<{
    module: CatalogModule;
    selection: ModuleProfileSelection;
    compute: ModuleComputeProfile;
    storage?: ModuleAxisProfile;
    accelerator?: ModuleAxisProfile;
  }>;
  notices: ReturnType<typeof makeNotice>[];
}

export class PlannerService {
  readonly catalogValidation: CatalogValidation;
  readonly catalog?: WebMcpCatalog;
  readonly sourceSha?: string;

  constructor(catalog: unknown, options: PlannerOptions = {}) {
    let validation = validateCatalog(catalog);
    let validatedCatalog = validation.catalog;
    this.sourceSha = options.sourceSha;
    if (validatedCatalog && options.sourceSha && options.sourceSha !== validatedCatalog.source_sha) {
      validation = { valid: false, issues: ["configured build source SHA does not match the catalog"] };
      validatedCatalog = undefined;
    }
    this.catalogValidation = validation;
    this.catalog = validatedCatalog;
  }

  async invoke<T extends ToolName>(
    tool: T,
    input: ToolInputMap[T],
    options: PlannerInvocationOptions = {},
  ): Promise<PlannerResult<T>> {
    if (options.signal?.aborted) return abortedResult(tool, this.catalog) as PlannerResult<T>;
    switch (tool) {
      case "stackkits_list_catalog":
        return this.listCatalog(input as ListCatalogInput, options) as unknown as PlannerResult<T>;
      case "stackkits_get_module_profiles":
        return this.getModuleProfiles(input as GetModuleProfilesInput, options) as unknown as PlannerResult<T>;
      case "stackkits_assess_capacity":
        return this.assessCapacity(input as AssessCapacityInput, options) as unknown as PlannerResult<T>;
      case "stackkits_prepare_handoff":
        return this.prepareHandoff(input as PrepareHandoffInput, options) as unknown as PlannerResult<T>;
    }
  }

  async listCatalog(
    input: ListCatalogInput = {},
    options: PlannerInvocationOptions = {},
  ): Promise<PlannerResult<"stackkits_list_catalog">> {
    const tool = "stackkits_list_catalog" as const;
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    if (!this.catalog) return authorityFailure(tool, this.catalogValidation);
    const data: ListCatalogData = {
      kits: this.catalog.kits.map((kit) => ({
        stackkit_id: kit.stackkit_id,
        display_name: kit.display_name,
        version: kit.version,
        status: kit.status,
        planner_link: kit.planner_link,
        module_count: kit.modules.length,
        use_case_ids: kit.use_cases.map((useCase) => useCase.use_case_id),
      })),
      module_selection_required: true,
    };
    return makeResult(tool, "success", data, this.catalog);
  }

  async getModuleProfiles(
    input: GetModuleProfilesInput,
    options: PlannerInvocationOptions = {},
  ): Promise<PlannerResult<"stackkits_get_module_profiles">> {
    const tool = "stackkits_get_module_profiles" as const;
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    const kitResult = this.requireKit(tool, input.stackkit_id);
    if (kitResult.result) return kitResult.result as PlannerResult<typeof tool>;
    const kit = kitResult.kit as CatalogKit;
    const requestedUseCases = input.use_case_ids ?? [];
    const unknown = requestedUseCases.find((id) => !kit.use_cases.some((useCase) => useCase.use_case_id === id));
    if (unknown) {
      return makeResult(tool, "invalid_input", {}, this.catalog, [
        makeNotice("UNKNOWN_USE_CASE", "error", "The use case is not declared by this StackKit.", "use_case_ids"),
      ], { stackkit_id: kit.stackkit_id }) as PlannerResult<typeof tool>;
    }
    const filter = new Set(requestedUseCases);
    if (input.module_id && !kit.modules.some((module) => module.module_id === input.module_id)) {
      return makeResult(tool, "invalid_input", {}, this.catalog, [
        makeNotice("UNKNOWN_MODULE", "error", "The module is not declared by this StackKit.", "module_id"),
      ], { stackkit_id: kit.stackkit_id }) as PlannerResult<typeof tool>;
    }
    const alternatives = kit.use_cases
      .filter((useCase) => filter.size === 0 || filter.has(useCase.use_case_id))
      .flatMap((useCase) => useCase.alternatives.map((alternative) => [
        useCase.use_case_id, alternative.alternative_id, alternative.module_id,
      ] as [string, string, string]));
    const modules = kit.modules.filter((module) =>
      (!input.module_id || module.module_id === input.module_id)
      && (filter.size === 0 || alternatives.some((alternative) => alternative[2] === module.module_id)));
    const cursor = input.cursor ?? 0;
    if (cursor > 0 && cursor >= modules.length) return this.invalidInput(tool, "cursor");
    const page = modules.slice(cursor, cursor + 1);
    const data: ModuleProfilesData = {
      modules: page.map((module) => ({
        module_id: module.module_id,
        required: module.required,
        profiles: module.compute_profiles.map(compactProfile),
        storage_profiles: module.storage_profiles.map(compactAxisProfile),
        accelerator_profiles: module.accelerator_profiles.map(compactAxisProfile),
      })),
      use_case_alternatives: alternatives.filter((alternative) => page.some((module) => module.module_id === alternative[2])),
      legacy_global_tiers: kit.legacy_compute_tier_mappings.map((mapping) => mapping.compute_tier),
      ...(cursor + page.length < modules.length ? { next_cursor: cursor + page.length } : {}),
    };
    return makeResult(tool, "success", data, this.catalog, [], { stackkit_id: kit.stackkit_id });
  }

  async assessCapacity(
    input: AssessCapacityInput,
    options: PlannerInvocationOptions = {},
  ): Promise<PlannerResult<"stackkits_assess_capacity">> {
    const tool = "stackkits_assess_capacity" as const;
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    const kitResult = this.requireKit(tool, input.stackkit_id);
    if (kitResult.result) return kitResult.result as PlannerResult<typeof tool>;
    const resolved = this.resolveSelection(kitResult.kit as CatalogKit, input.module_profiles, input.use_cases ?? []);
    const selection = selectionEnvelope(input.stackkit_id, resolved.selections, resolved.useCases);
    if (resolved.notices.some((notice) => notice.severity === "error")) {
      return makeResult(tool, "invalid_input", {}, this.catalog, resolved.notices, selection) as PlannerResult<typeof tool>;
    }
    const capacity = evaluateCapacity(resolved.modules, input.declared_capacity);
    return makeResult(tool, "success", capacity, this.catalog, [
      ...resolved.notices,
      ...capacityNotices(capacity, false),
    ], { stackkit_id: input.stackkit_id });
  }

  async prepareHandoff(
    input: PrepareHandoffInput,
    options: PlannerInvocationOptions = {},
  ): Promise<PlannerResult<"stackkits_prepare_handoff">> {
    const tool = "stackkits_prepare_handoff" as const;
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    const kitResult = this.requireKit(tool, input.stackkit_id);
    if (kitResult.result) return kitResult.result as PlannerResult<typeof tool>;
    const kit = kitResult.kit as CatalogKit;
    const resolved = this.resolveSelection(kit, input.module_profiles, input.use_cases ?? []);
    const selection = selectionEnvelope(input.stackkit_id, resolved.selections, resolved.useCases);
    if (resolved.notices.some((notice) => notice.severity === "error")) {
      return makeResult(tool, "invalid_input", this.emptyHandoff("unverified", [resolved.notices[0]?.code ?? "INVALID_INPUT"]), this.catalog, resolved.notices, selection);
    }

    const capacity = evaluateCapacity(resolved.modules, input.declared_capacity);
    const notices = [...resolved.notices, ...capacityNotices(capacity, true)];
    const blockedReasons: string[] = [];
    if (capacity.overall === "fail") blockedReasons.push("DECLARED_CAPACITY_BELOW_MINIMUM");
    if (capacity.overall === "unverified") {
      blockedReasons.push(capacity.unverified_modules.length > 0
        ? "MODULE_PROFILE_CAPACITY_UNDECLARED"
        : "DECLARED_CAPACITY_INCOMPLETE");
    }
    for (const entry of resolved.modules) {
      const hasNonExecutableDimension = !entry.compute.executable || entry.compute.realization !== "apply-ready";
      const hasNonReadyAxis = [entry.storage, entry.accelerator]
        .some((profile) => profile !== undefined && profile.realization !== "apply-ready");
      if (hasNonExecutableDimension || hasNonReadyAxis) {
        blockedReasons.push("MODULE_PROFILE_NOT_EXECUTABLE");
        notices.push(makeNotice(
          "MODULE_PROFILE_NOT_EXECUTABLE",
          "error",
          "Every selected module profile dimension must be apply-ready before handoff.",
          `module_profiles.${entry.module.module_id}`,
        ));
      }
    }

    const authoring = collectAuthoringInputs(input);
    for (const path of authoring.invalidPaths) {
      notices.push(makeNotice("INVALID_INPUT", "error", "The authoring value does not match the supported public contract.", path));
      blockedReasons.push("INVALID_INPUT");
    }
    const missing = kit.required_authoring_inputs.filter((path) => !authoring.values.has(path));
    const unsupported = kit.required_authoring_inputs.filter((path) => authoring.values.has(path) && !isSupportedAuthoringPath(path));
    for (const path of [...missing, ...unsupported]) {
      notices.push(makeNotice("REQUIRED_AUTHORING_INPUT_MISSING", "error", "An explicit supported authoring value is required.", path));
      blockedReasons.push("REQUIRED_AUTHORING_INPUT_MISSING");
    }
    if (blockedReasons.length > 0) {
      return makeResult(
        tool,
        blockedReasons.includes("INVALID_INPUT") ? "invalid_input" : "blocked",
        this.emptyHandoff(capacity.overall, blockedReasons),
        this.catalog,
        notices,
        selection,
      );
    }
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);

    const operations = this.catalog?.operations ?? [];
    const steps = REQUIRED_OPERATION_IDS.slice(0, 5).map((id) => {
      const operation = operations.find((candidate) => candidate.id === id) as OperationMetadata;
      return operationStep(operation, input, authoring.values, resolved.selections, resolved.useCases);
    });
    const apply = operations.find((candidate) => candidate.id === "stackkit.apply") as OperationMetadata;
    const data: HandoffData = {
      ready: true,
      capacity_status: capacity.overall,
      steps,
      apply_follow_up: applyFollowUp(apply),
    };
    notices.push(makeNotice("TARGET_COMPAT_REQUIRED", "info", "Run stackkit compat on the target before apply."));
    // The validated init argv already carries the full selection. Avoid reflecting it twice.
    return makeResult(tool, "success", data, this.catalog, notices, { stackkit_id: kit.stackkit_id });
  }

  private resolveSelection(
    kit: CatalogKit,
    rawSelections: ModuleProfileSelection[],
    rawUseCases: UseCaseSelection[],
  ): ResolvedSelection {
    const notices: ReturnType<typeof makeNotice>[] = [];
    const selections = [...rawSelections].sort((left, right) => left.module_id.localeCompare(right.module_id));
    const useCases = [...rawUseCases].sort((left, right) => left.use_case_id.localeCompare(right.use_case_id));
    const selectedModules = new Map<string, ModuleProfileSelection>();
    for (const selection of selections) {
      if (selectedModules.has(selection.module_id)) {
        notices.push(makeNotice("DUPLICATE_MODULE_PROFILE", "error", "Each module may be selected only once.", "module_profiles"));
      } else {
        selectedModules.set(selection.module_id, selection);
      }
    }
    const selectedUseCases = new Set<string>();
    const activeModules = new Set(kit.modules.filter((module) => module.required).map((module) => module.module_id));
    const requiredUseCases = kit.use_cases.filter((useCase) => useCase.required);
    for (const selection of useCases) {
      if (selectedUseCases.has(selection.use_case_id)) {
        notices.push(makeNotice("DUPLICATE_USE_CASE", "error", "Each use case may be selected only once.", "use_cases"));
        continue;
      }
      selectedUseCases.add(selection.use_case_id);
      const useCase = kit.use_cases.find((candidate) => candidate.use_case_id === selection.use_case_id);
      if (!useCase) {
        notices.push(makeNotice("UNKNOWN_USE_CASE", "error", "The use case is not declared by this StackKit.", "use_cases"));
        continue;
      }
      if (useCase.availability !== "available") {
        notices.push(makeNotice(useCase.reason_code ?? "USE_CASE_NOT_AVAILABLE", "error", "The use case is blocked by the CUE authority.", "use_cases"));
        continue;
      }
      const alternative = useCase.alternatives.find((candidate) => candidate.alternative_id === selection.alternative_id);
      if (!alternative) {
        notices.push(makeNotice("UNKNOWN_USE_CASE_ALTERNATIVE", "error", "The use-case alternative is not declared.", "use_cases"));
        continue;
      }
      activeModules.add(alternative.module_id);
    }
    for (const useCase of requiredUseCases) {
      if (!selectedUseCases.has(useCase.use_case_id)) {
        notices.push(makeNotice(
          "REQUIRED_USE_CASE_SELECTION_MISSING",
          "error",
          "Choose one declared alternative for this required use case before assessing or handing off.",
          `use_cases.${useCase.use_case_id}`,
        ));
      }
    }
    const rejectedWorkloadModules = new Set<string>();
    for (const moduleId of selectedModules.keys()) {
      const module = kit.modules.find((candidate) => candidate.module_id === moduleId);
      if (module?.role === "workload" && !activeModules.has(moduleId)) {
        notices.push(makeNotice(
          "MODULE_PROFILE_UNSELECTED_WORKLOAD",
          "error",
          "Workload module profiles may be selected only through the chosen use-case alternative.",
          `module_profiles.${moduleId}`,
        ));
        rejectedWorkloadModules.add(moduleId);
        continue;
      }
      if (module) activeModules.add(moduleId);
    }
    for (const moduleId of activeModules) {
      const module = kit.modules.find((candidate) => candidate.module_id === moduleId);
      const selection = selectedModules.get(moduleId);
      if ((module?.compute_profiles?.length ?? 0) > 0 && !selection) {
        notices.push(makeNotice(
          "MODULE_PROFILE_SELECTION_INCOMPLETE",
          "error",
          "Every active module with a compute dimension needs an explicit local profile.",
          `module_profiles.${moduleId}.compute_profile`,
        ));
      }
      if (module?.storage_profiles.length && !selection?.storage_profile) {
        notices.push(makeNotice(
          "MODULE_PROFILE_SELECTION_INCOMPLETE",
          "error",
          "Every declared storage dimension needs an explicit local profile.",
          `module_profiles.${moduleId}.storage_profile`,
        ));
      }
      if (module?.accelerator_profiles.length && !selection?.accelerator_profile) {
        notices.push(makeNotice(
          "MODULE_PROFILE_SELECTION_INCOMPLETE",
          "error",
          "Every declared accelerator dimension needs an explicit local profile.",
          `module_profiles.${moduleId}.accelerator_profile`,
        ));
      }
    }

    const modules: ResolvedSelection["modules"] = [];
    for (const [moduleId, selection] of selectedModules) {
      if (rejectedWorkloadModules.has(moduleId)) continue;
      const module = kit.modules.find((candidate) => candidate.module_id === moduleId);
      if (!module) {
        notices.push(makeNotice("UNKNOWN_MODULE", "error", "The module is not declared by this StackKit.", "module_profiles"));
        continue;
      }
      if (module.compute_profiles.length === 0) {
        notices.push(makeNotice("MODULE_PROFILE_NOT_DECLARED", "error", "This module has no declared compute-profile dimension.", `module_profiles.${moduleId}`));
        continue;
      }
      const compute = module.compute_profiles.find((profile) => profile.id === selection.compute_profile);
      if (!compute) {
        notices.push(makeNotice("UNDECLARED_MODULE_COMPUTE_PROFILE", "error", "The compute profile is not declared for this module.", `module_profiles.${moduleId}.compute_profile`));
        continue;
      }
      const storage = selection.storage_profile
        ? module.storage_profiles.find((profile) => profile.id === selection.storage_profile)
        : undefined;
      if (selection.storage_profile && !storage) {
        notices.push(makeNotice("UNDECLARED_MODULE_STORAGE_PROFILE", "error", "The storage profile is not declared for this module.", `module_profiles.${moduleId}.storage_profile`));
        continue;
      }
      const accelerator = selection.accelerator_profile
        ? module.accelerator_profiles.find((profile) => profile.id === selection.accelerator_profile)
        : undefined;
      if (selection.accelerator_profile && !accelerator) {
        notices.push(makeNotice("UNDECLARED_MODULE_ACCELERATOR_PROFILE", "error", "The accelerator profile is not declared for this module.", `module_profiles.${moduleId}.accelerator_profile`));
        continue;
      }
      if (module.storage_profiles.length > 0 && !storage) continue;
      if (module.accelerator_profiles.length > 0 && !accelerator) continue;
      modules.push({ module, selection, compute, ...(storage ? { storage } : {}), ...(accelerator ? { accelerator } : {}) });
    }
    modules.sort((left, right) => left.module.module_id.localeCompare(right.module.module_id));
    return { selections, useCases, modules, notices };
  }

  private emptyHandoff(status: CapacityStatus, reasons: string[]): HandoffData {
    const apply = this.catalog?.operations.find((operation) => operation.id === "stackkit.apply");
    return {
      ready: false,
      capacity_status: status,
      steps: [],
      apply_follow_up: applyFollowUp(apply),
      blocked_reasons: [...new Set(reasons)].sort(),
    };
  }

  private requireKit<T extends ToolName>(
    tool: T,
    stackkitId: string,
  ): { kit?: CatalogKit; result?: WebMcpResult<unknown> } {
    if (!this.catalog) return { result: authorityFailure(tool, this.catalogValidation) };
    const kit = this.catalog.kits.find((candidate) => candidate.stackkit_id === stackkitId);
    if (!kit) {
      return { result: makeResult(tool, "invalid_input", {}, this.catalog, [
        makeNotice("UNKNOWN_STACKKIT", "error", "The StackKit is not declared by the authority.", "stackkit_id"),
      ]) };
    }
    return { kit };
  }

  private invalidInput<T extends ToolName>(tool: T, field?: string): PlannerResult<T> {
    return makeResult(tool, "invalid_input", {}, this.catalog, [
      makeNotice("INVALID_INPUT", "error", "Input does not match the closed tool schema.", field),
    ]) as PlannerResult<T>;
  }
}

export function createPlanner(catalog: unknown, options: PlannerOptions = {}): PlannerService {
  return new PlannerService(catalog, options);
}

export function overallCapacityStatus(statuses: CapacityStatus[]): CapacityStatus {
  if (statuses.includes("fail")) return "fail";
  if (statuses.includes("unverified")) return "unverified";
  if (statuses.includes("warning")) return "warning";
  return "pass";
}

function evaluateCapacity(
  modules: ResolvedSelection["modules"],
  declared: PartialDeclaredCapacity,
): CapacityData {
  const unverifiedModules = modules
    .filter((entry) => CAPACITY_AXES.some((axis) => !moduleAxisComplete(entry, axis)))
    .map((entry) => entry.module.module_id)
    .sort();
  const floor = maxVectors(modules.map((entry) => entry.compute.host_floor));
  const reservation = sumVectors(modules.flatMap((entry) => [
    entry.compute.reservation,
    entry.storage?.reservation,
    entry.accelerator?.reservation,
  ]));
  const recommendation = sumVectors(modules.flatMap((entry) => [
    maxVectors([entry.compute.recommended, entry.compute.reservation]),
    entry.compute.headroom,
    entry.storage?.reservation,
    entry.accelerator?.reservation,
  ]));

  const checks: CapacityCheck[] = CAPACITY_AXES.map((axis) => {
    const observed = declared[axis];
    const minimum = maxNumber(floor[axis], reservation[axis]);
    const recommended = maxNumber(minimum, recommendation[axis]);
    if (typeof observed !== "number") {
      return compactCheck(axis, undefined, minimum, recommended, "unverified", "DECLARED_CAPACITY_INCOMPLETE");
    }
    if (typeof minimum === "number" && observed < minimum) {
      return compactCheck(axis, observed, minimum, recommended, "fail", "DECLARED_CAPACITY_BELOW_MINIMUM");
    }
    if (!moduleAxisCompleteForAll(modules, axis) || typeof minimum !== "number") {
      return compactCheck(axis, observed, minimum, recommended, "unverified", "MODULE_PROFILE_CAPACITY_UNDECLARED");
    }
    if (typeof recommended === "number" && observed < recommended) {
      return compactCheck(axis, observed, minimum, recommended, "warning", "DECLARED_CAPACITY_BELOW_RECOMMENDATION");
    }
    return compactCheck(axis, observed, minimum, recommended, "pass", "DECLARED_CAPACITY_PASS");
  });
  return {
    declared_capacity: clone(declared),
    checks,
    overall: overallCapacityStatus(checks.map((check) => check.status)),
    origin: "user_declared",
    unverified_modules: unverifiedModules,
  };
}

function moduleAxisCompleteForAll(
  modules: ResolvedSelection["modules"],
  axis: CapacityAxis,
): boolean {
  return modules.every((entry) => moduleAxisComplete(entry, axis));
}

/**
 * Capacity authority is evaluated per axis. A partial workload reservation can
 * establish RAM while leaving CPU and storage unverified; an observed value
 * below a known floor still fails before that incompleteness is reported.
 */
function moduleAxisComplete(
  entry: ResolvedSelection["modules"][number],
  axis: CapacityAxis,
): boolean {
  const computeComplete = entry.compute.capacity_declaration === "declared"
    || (entry.compute.capacity_declaration === "partial"
      && hasAxis(axis, entry.compute.host_floor, entry.compute.reservation));
  const selectedAxes = [entry.storage, entry.accelerator].filter((profile): profile is ModuleAxisProfile => Boolean(profile));
  const axesComplete = selectedAxes.every((profile) => hasAxis(axis, profile.reservation));
  return computeComplete && axesComplete;
}

function hasAxis(axis: CapacityAxis, ...vectors: Array<ResourceVector | undefined>): boolean {
  return vectors.some((vector) => typeof vector?.[axis] === "number");
}

function compactCheck(
  axis: CapacityAxis,
  observed: number | undefined,
  minimum: number | undefined,
  recommended: number | undefined,
  status: CapacityStatus,
  reason: CapacityCheck["reason_code"],
): CapacityCheck {
  return {
    axis,
    ...(typeof observed === "number" ? { observed } : {}),
    ...(typeof minimum === "number" ? { minimum } : {}),
    ...(typeof recommended === "number" ? { recommended } : {}),
    status,
    reason_code: reason,
  };
}

function capacityNotices(capacity: CapacityData, handoff: boolean): ReturnType<typeof makeNotice>[] {
  const grouped = new Map<CapacityCheck["reason_code"], CapacityAxis[]>();
  for (const check of capacity.checks) {
    if (check.status === "pass") continue;
    grouped.set(check.reason_code, [...(grouped.get(check.reason_code) ?? []), check.axis]);
  }
  return [...grouped].map(([reason, axes]) => {
    const message = reason === "DECLARED_CAPACITY_BELOW_MINIMUM" ? "Below the declared minimum."
      : reason === "DECLARED_CAPACITY_BELOW_RECOMMENDATION" ? "Below the declared recommendation."
      : reason === "MODULE_PROFILE_CAPACITY_UNDECLARED" ? "Module capacity is not fully declared."
      : "Available capacity is not declared.";
    const severity = reason === "DECLARED_CAPACITY_BELOW_MINIMUM" ? "error"
      : reason === "DECLARED_CAPACITY_BELOW_RECOMMENDATION" ? "warning"
      : handoff ? "error" : "warning";
    return makeNotice(reason, severity,
      axes.length > 1 ? `${message} Axes: ${axes.join(", ")}.` : message,
      axes.length === 1 ? axes[0] : undefined);
  });
}

function compactProfile(profile: ModuleComputeProfile): ModuleProfilesData["modules"][number]["profiles"][number] {
  return [
    profile.id,
    profile.capacity_declaration,
    capacityTuple(profile.host_floor),
    capacityTuple(profile.reservation),
    capacityTuple(profile.recommended),
    profile.realization,
    profile.executable,
    profile.profile_sha256,
  ];
}

function compactAxisProfile(profile: ModuleAxisProfile): ModuleProfilesData["modules"][number]["storage_profiles"][number] {
  return [
    profile.id,
    profile.capacity_declaration,
    capacityTuple(profile.reservation),
    profile.realization,
    profile.profile_sha256,
  ];
}

function capacityTuple(vector?: ResourceVector): [number | null, number | null, number | null] {
  return [vector?.cpu_cores ?? null, vector?.ram_gb ?? null, vector?.storage_gb ?? null];
}

function maxVectors(vectors: Array<ResourceVector | undefined>): ResourceVector {
  const result: ResourceVector = {};
  for (const axis of CAPACITY_AXES) {
    const values = vectors.map((vector) => vector?.[axis]).filter((value): value is number => typeof value === "number");
    if (values.length > 0) result[axis] = Math.max(...values);
  }
  return result;
}

function sumVectors(vectors: Array<ResourceVector | undefined>): ResourceVector {
  const result: ResourceVector = {};
  for (const axis of CAPACITY_AXES) {
    const values = vectors.map((vector) => vector?.[axis]).filter((value): value is number => typeof value === "number");
    if (values.length > 0) result[axis] = values.reduce((sum, value) => sum + value, 0);
  }
  return result;
}

function maxNumber(...values: Array<number | undefined>): number | undefined {
  const declared = values.filter((value): value is number => typeof value === "number");
  return declared.length > 0 ? Math.max(...declared) : undefined;
}

function operationStep(
  operation: OperationMetadata,
  input: PrepareHandoffInput,
  authoring: Map<string, string>,
  profiles: ModuleProfileSelection[],
  useCases: UseCaseSelection[],
): HandoffStep {
  return [
    operation.id,
    operation.id === "stackkit.init"
      ? initArgv(input.stackkit_id, authoring, profiles, useCases)
      : ["stackkit", ...operation.command],
    operation.mutation,
    operation.idempotent,
    operation.owner_approval,
  ];
}

function initArgv(
  stackkitId: string,
  authoring: Map<string, string>,
  profiles: ModuleProfileSelection[],
  useCases: UseCaseSelection[],
): string[] {
  const argv = ["stackkit", "init", stackkitId, "--api-version", "stackkit/v2alpha2", "--non-interactive", "--owner-source=local"];
  const domain = authoring.get("network.domain.base");
  const name = authoring.get("metadata.name");
  if (domain) argv.push("--domain", domain);
  if (name) argv.push("--name", name);
  for (const profile of profiles) {
    argv.push("--module-compute-profile", `${profile.module_id}=${profile.compute_profile}`);
    if (profile.storage_profile) argv.push("--module-storage-profile", `${profile.module_id}=${profile.storage_profile}`);
    if (profile.accelerator_profile) argv.push("--module-accelerator-profile", `${profile.module_id}=${profile.accelerator_profile}`);
  }
  for (const useCase of useCases) {
    argv.push("--use-case", useCase.use_case_id, "--use-case-alternative", `${useCase.use_case_id}=${useCase.alternative_id}`);
  }
  return argv;
}

function collectAuthoringInputs(input: PrepareHandoffInput): { values: Map<string, string>; invalidPaths: string[] } {
  const values = new Map<string, string>();
  if (input.authoring?.domain_base) values.set("network.domain.base", input.authoring.domain_base);
  if (input.authoring?.name) values.set("metadata.name", input.authoring.name);
  const invalidPaths: string[] = [];
  for (const entry of input.authoring_inputs ?? []) {
    if (values.has(entry.path) || !isValidAuthoringValue(entry.path, entry.value)) invalidPaths.push(entry.path);
    else values.set(entry.path, entry.value);
  }
  return { values, invalidPaths: [...new Set(invalidPaths)].sort() };
}

function isSupportedAuthoringPath(path: string): boolean {
  return path === "network.domain.base" || path === "metadata.name";
}

function isValidAuthoringValue(path: string, value: string): boolean {
  if (path === "network.domain.base") return AUTHORING_DOMAIN.test(value);
  if (path === "metadata.name") return AUTHORING_NAME.test(value);
  return false;
}

function applyFollowUp(operation?: OperationMetadata): HandoffFollowUp {
  return {
    id: "stackkit.apply",
    mutation: true,
    idempotent: operation?.idempotent ?? false,
    owner_approval: true,
    executable: false,
  };
}

function selectionEnvelope(
  stackkitId: string,
  profiles: ModuleProfileSelection[],
  useCases: UseCaseSelection[],
): Selection {
  return {
    stackkit_id: stackkitId,
    module_profiles: clone(profiles),
    ...(useCases.length > 0 ? { use_cases: clone(useCases) } : {}),
  };
}

function dedupeNotices(notices: ReturnType<typeof makeNotice>[]): ReturnType<typeof makeNotice>[] {
  const seen = new Set<string>();
  return notices.filter((notice) => {
    const key = `${notice.code}:${notice.field ?? ""}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
