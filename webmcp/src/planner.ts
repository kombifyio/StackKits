import {
  REQUIRED_OPERATION_IDS,
  validateCatalog,
  type CatalogValidation,
} from "./catalog.js";
import { validateToolInput } from "./schema.js";
import { abortedResult, authorityFailure, makeNotice, makeResult } from "./result.js";
import type {
  AssessCapacityInput,
  CapacityAxis,
  CapacityCheck,
  CapacityData,
  CapacityStatus,
  CatalogKit,
  CompactIncludedUseCase,
  ComputeTier,
  DeclaredCapacity,
  GetTierProfileInput,
  HandoffData,
  HandoffFollowUp,
  HandoffStep,
  ListCatalogData,
  ListCatalogInput,
  OperationMetadata,
  PrepareHandoffInput,
  RuntimeRequirementTuple,
  Selection,
  TierProfile,
  TierProfileData,
  ToolInputMap,
  ToolName,
  ToolResultMap,
  UseCaseFit,
  WebMcpCatalog,
  WebMcpResult,
} from "./types.js";

const CAPACITY_AXES: readonly CapacityAxis[] = ["cpu_cores", "ram_gb", "storage_gb"];
const CAPACITY_FIELDS: Record<CapacityAxis, keyof DeclaredCapacity> = {
  cpu_cores: "cpu_cores",
  ram_gb: "ram_gb",
  storage_gb: "storage_gb",
};
const MINIMUM_FIELD: Record<CapacityAxis, keyof TierProfile["host_requirements"]> = {
  cpu_cores: "min_cpu_cores",
  ram_gb: "min_ram_gb",
  storage_gb: "min_storage_gb",
};
const RECOMMENDED_FIELD: Record<CapacityAxis, keyof TierProfile["host_requirements"]> = {
  cpu_cores: "recommended_cpu_cores",
  ram_gb: "recommended_ram_gb",
  storage_gb: "recommended_storage_gb",
};
const AUTHORING_DOMAIN = /^(?=.{1,253}$)(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(?:\.(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?))+$/;
const AUTHORING_NAME = /^[a-zA-Z][a-zA-Z0-9_-]{0,63}$/;

export interface PlannerOptions {
  sourceSha?: string;
}

export interface PlannerInvocationOptions {
  signal?: AbortSignal;
}

export type PlannerResult<T extends ToolName> = ToolResultMap[T];

/** Framework-independent implementation shared by the UI and WebMCP tools. */
export class PlannerService {
  readonly catalog?: WebMcpCatalog;
  readonly catalogValidation: CatalogValidation;
  readonly sourceSha?: string;

  constructor(catalog: unknown, options: PlannerOptions = {}) {
    this.catalogValidation = validateCatalog(catalog);
    this.catalog = this.catalogValidation.catalog;
    this.sourceSha = options.sourceSha;
    if (this.catalog && options.sourceSha && options.sourceSha !== this.catalog.source_sha) {
      this.catalogValidation = {
        valid: false,
        issues: ["configured build source SHA does not match the catalog"],
      };
      this.catalog = undefined;
    }
  }

  async invoke<T extends ToolName>(tool: T, input: ToolInputMap[T], options: PlannerInvocationOptions = {}): Promise<PlannerResult<T>> {
    if (options.signal?.aborted) return abortedResult(tool, this.catalog) as PlannerResult<T>;
    switch (tool) {
      case "stackkits_list_catalog": return this.listCatalog(input as ListCatalogInput, options) as unknown as PlannerResult<T>;
      case "stackkits_get_tier_profile": return this.getTierProfile(input as GetTierProfileInput, options) as unknown as PlannerResult<T>;
      case "stackkits_assess_capacity": return this.assessCapacity(input as AssessCapacityInput, options) as unknown as PlannerResult<T>;
      case "stackkits_prepare_handoff": return this.prepareHandoff(input as PrepareHandoffInput, options) as unknown as PlannerResult<T>;
    }
  }

  async listCatalog(input: ListCatalogInput = {}, options: PlannerInvocationOptions = {}): Promise<PlannerResult<"stackkits_list_catalog">> {
    const tool: ToolName = "stackkits_list_catalog";
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    if (!this.catalog) return authorityFailure(tool, this.catalogValidation);
    const data: ListCatalogData = {
      kits: this.catalog.kits.map(({ stackkit_id, display_name, version, status, planner_link, compute_tiers }) => ({
        stackkit_id,
        display_name,
        version,
        status,
        planner_link,
        compute_tiers: [...compute_tiers],
      })),
      selection_required: true,
    };
    return makeResult(tool, "success", data, this.catalog) as PlannerResult<"stackkits_list_catalog">;
  }

  async getTierProfile(input: GetTierProfileInput, options: PlannerInvocationOptions = {}): Promise<PlannerResult<"stackkits_get_tier_profile">> {
    const tool: ToolName = "stackkits_get_tier_profile";
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    const kitResult = this.requireKit(tool, input.stackkit_id);
    if (kitResult.result) return kitResult.result as PlannerResult<"stackkits_get_tier_profile">;
    const tierResult = this.requireTier(tool, kitResult.kit, input.compute_tier);
    if (tierResult.result) return tierResult.result as PlannerResult<"stackkits_get_tier_profile">;
    const selection: Selection = { stackkit_id: input.stackkit_id, compute_tier: input.compute_tier };
    const data: TierProfileData = compactTierProfile(tierResult.profile as TierProfile);
    return makeResult(tool, "success", data, this.catalog, [], selection) as PlannerResult<"stackkits_get_tier_profile">;
  }

  async assessCapacity(input: AssessCapacityInput, options: PlannerInvocationOptions = {}): Promise<PlannerResult<"stackkits_assess_capacity">> {
    const tool: ToolName = "stackkits_assess_capacity";
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    const kitResult = this.requireKit(tool, input.stackkit_id);
    if (kitResult.result) return kitResult.result as PlannerResult<"stackkits_assess_capacity">;
    const tierResult = this.requireTier(tool, kitResult.kit, input.compute_tier);
    if (tierResult.result) return tierResult.result as PlannerResult<"stackkits_assess_capacity">;
    const capacity = this.evaluateCapacity(tierResult.profile as TierProfile, input.declared_capacity);
    const selection: Selection = { stackkit_id: input.stackkit_id, compute_tier: input.compute_tier };
    const notices = capacity.checks
      .filter((check) => check.status !== "pass")
      .map((check) => check.status === "fail"
        ? makeNotice("DECLARED_CAPACITY_BELOW_MINIMUM", "error", "The declared value is below the tier minimum.", check.axis)
        : check.status === "warning"
          ? makeNotice("DECLARED_CAPACITY_BELOW_RECOMMENDATION", "warning", "The declared value is below the tier recommendation.", check.axis)
          : makeNotice("DECLARED_CAPACITY_INCOMPLETE", "warning", "This capacity axis was not declared.", check.axis));
    return makeResult(tool, "success", capacity, this.catalog, notices, selection) as PlannerResult<"stackkits_assess_capacity">;
  }

  async prepareHandoff(input: PrepareHandoffInput, options: PlannerInvocationOptions = {}): Promise<PlannerResult<"stackkits_prepare_handoff">> {
    const tool: ToolName = "stackkits_prepare_handoff";
    const invalid = validateToolInput(tool, input);
    if (!invalid.valid) return this.invalidInput(tool, invalid.issues[0]?.path);
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);
    const kitResult = this.requireKit(tool, input.stackkit_id);
    if (kitResult.result) return kitResult.result as PlannerResult<"stackkits_prepare_handoff">;
    const tierResult = this.requireTier(tool, kitResult.kit, input.compute_tier);
    if (tierResult.result) return tierResult.result as PlannerResult<"stackkits_prepare_handoff">;
    const selection: Selection = {
      stackkit_id: input.stackkit_id,
      compute_tier: input.compute_tier,
      ...(input.use_case_ids && input.use_case_ids.length > 0 ? { use_case_ids: [...input.use_case_ids].sort() } : {}),
    };
    const capacity = this.evaluateCapacity(tierResult.profile as TierProfile, input.declared_capacity);
    const capacityNotices = capacity.checks
      .filter((check) => check.status !== "pass")
      .map((check) => check.status === "fail"
        ? makeNotice("DECLARED_CAPACITY_BELOW_MINIMUM", "error", "The declared value is below the tier minimum.", check.axis)
        : check.status === "warning"
          ? makeNotice("DECLARED_CAPACITY_BELOW_RECOMMENDATION", "warning", "The declared value is below the tier recommendation.", check.axis)
          : makeNotice("DECLARED_CAPACITY_INCOMPLETE", "error", "All capacity axes are required before a handoff can be prepared.", check.axis));
    if (capacity.overall === "fail" || capacity.overall === "unverified") {
      const blockedReasons = capacity.checks.filter((check) => check.status === "fail" || check.status === "unverified").map((check) => check.reason_code);
      const data = this.emptyHandoff(capacity, blockedReasons);
      return makeResult(tool, "blocked", data, this.catalog, capacityNotices, selection) as PlannerResult<"stackkits_prepare_handoff">;
    }

    const useCases = this.validateUseCases(tierResult.profile as TierProfile, input.use_case_ids ?? [], tool, selection);
    if (useCases.result) {
      const code = useCases.result.notices[0]?.code ?? "USE_CASE_NOT_AVAILABLE_FOR_TIER";
      const data = this.emptyHandoff(capacity, [code]);
      return makeResult(tool, useCases.result.outcome, data, this.catalog, [...capacityNotices, ...useCases.result.notices], selection) as PlannerResult<"stackkits_prepare_handoff">;
    }
    const authoringCollection = this.collectAuthoringInputs(input);
    const authoring = authoringCollection.values;
    if (authoringCollection.invalidPaths.length > 0) {
      const notices = authoringCollection.invalidPaths.map((path) => makeNotice("INVALID_INPUT", "error", "The authoring value does not match the supported public contract.", path));
      const data = this.emptyHandoff(capacity, ["INVALID_INPUT"]);
      return makeResult(tool, "invalid_input", data, this.catalog, [...capacityNotices, ...notices], selection) as PlannerResult<"stackkits_prepare_handoff">;
    }
    const selectedKit = kitResult.kit as CatalogKit;
    const missing = selectedKit.required_authoring_inputs.filter((path) => !authoring.has(path));
    const unsupported = selectedKit.required_authoring_inputs.filter((path) => authoring.has(path) && !isSupportedAuthoringPath(path));
    if (missing.length > 0 || unsupported.length > 0) {
      const paths = [...missing, ...unsupported];
      const notices = paths.map((path) => makeNotice("REQUIRED_AUTHORING_INPUT_MISSING", "error", "An explicit authoring-contract value is required.", path));
      const data = this.emptyHandoff(capacity, ["REQUIRED_AUTHORING_INPUT_MISSING"]);
      return makeResult(tool, "blocked", data, this.catalog, [...capacityNotices, ...notices], selection) as PlannerResult<"stackkits_prepare_handoff">;
    }
    if (options.signal?.aborted) return abortedResult(tool, this.catalog);

    const operations = this.catalog?.operations ?? [];
    const steps = REQUIRED_OPERATION_IDS.slice(0, 5).map((operationId) => {
      const operation = operations.find((candidate) => candidate.id === operationId) as OperationMetadata;
      return operationStep(operation, input, authoring, useCases.useCaseIds);
    });
    const apply = operations.find((candidate) => candidate.id === "stackkit.apply") as OperationMetadata;
    const applyFollowUp: HandoffFollowUp = {
      id: "stackkit.apply",
      mutation: true,
      idempotent: apply.idempotent,
      owner_approval: true,
      executable: false,
    };
    const data: HandoffData = {
      ready: true,
      capacity_status: capacity.overall,
      steps,
      apply_follow_up: applyFollowUp,
    };
    const notices = [
      ...capacityNotices,
      makeNotice("TARGET_COMPAT_REQUIRED", "info", "Target stackkit compat is required before apply."),
    ];
    return makeResult(tool, "success", data, this.catalog, notices, selection) as PlannerResult<"stackkits_prepare_handoff">;
  }

  private evaluateCapacity(profile: TierProfile, declared: Partial<DeclaredCapacity>): CapacityData {
    const checks: CapacityCheck[] = CAPACITY_AXES.map((axis) => {
      const field = CAPACITY_FIELDS[axis];
      const observed = declared[field];
      const minimum = profile.host_requirements[MINIMUM_FIELD[axis]];
      const recommended = profile.host_requirements[RECOMMENDED_FIELD[axis]];
      if (typeof observed !== "number") {
        return {
          axis,
          ...(typeof minimum === "number" ? { minimum } : {}),
          ...(typeof recommended === "number" ? { recommended } : {}),
          status: "unverified",
          reason_code: "DECLARED_CAPACITY_INCOMPLETE",
        };
      }
      if (typeof minimum !== "number") {
        return {
          axis,
          observed,
          ...(typeof recommended === "number" ? { recommended } : {}),
          status: "unverified",
          reason_code: "DECLARED_REQUIREMENT_NOT_DECLARED",
        };
      }
      if (observed < minimum) {
        return {
          axis,
          observed,
          minimum,
          ...(typeof recommended === "number" ? { recommended } : {}),
          status: "fail",
          reason_code: "DECLARED_CAPACITY_BELOW_MINIMUM",
        };
      }
      if (typeof recommended === "number" && observed < recommended) {
        return {
          axis,
          observed,
          minimum,
          recommended,
          status: "warning",
          reason_code: "DECLARED_CAPACITY_BELOW_RECOMMENDATION",
        };
      }
      return {
        axis,
        observed,
        minimum,
        ...(typeof recommended === "number" ? { recommended } : {}),
        status: "pass",
        reason_code: "DECLARED_CAPACITY_PASS",
      };
    });
    const overall = overallCapacityStatus(checks.map((check) => check.status));
    const declared_capacity: Partial<DeclaredCapacity> = {};
    for (const axis of CAPACITY_AXES) {
      const value = declared[CAPACITY_FIELDS[axis]];
      if (typeof value === "number") declared_capacity[CAPACITY_FIELDS[axis]] = value;
    }
    return { declared_capacity, checks, overall, origin: "user_declared" };
  }

  private validateUseCases(profile: TierProfile, ids: string[], tool: ToolName, selection: Selection): { useCaseIds: string[]; result?: WebMcpResult<unknown> } {
    const fits = new Map(profile.use_case_fits.map((fit) => [fit.use_case_id, fit]));
    const unknown = ids.find((id) => !fits.has(id));
    if (unknown) {
      return {
        useCaseIds: [],
        result: makeResult(tool, "invalid_input", {}, this.catalog, [makeNotice("UNKNOWN_USE_CASE", "error", "The use case is not declared by the authority.", "use_case_ids")], selection),
      };
    }
    const unavailable = ids.find((id) => !fits.get(id)?.included);
    if (unavailable) {
      return {
        useCaseIds: [],
        result: makeResult(tool, "blocked", {}, this.catalog, [makeNotice("USE_CASE_NOT_AVAILABLE_FOR_TIER", "error", "The selected use case is excluded on this compute tier.", "use_case_ids")], selection),
      };
    }
    return { useCaseIds: [...ids].sort() };
  }

  private collectAuthoringInputs(input: PrepareHandoffInput): { values: Map<string, string>; invalidPaths: string[] } {
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

  private emptyHandoff(capacity: CapacityData, blockedReasons: string[]): HandoffData {
    const operations = this.catalog?.operations ?? [];
    const apply = operations.find((candidate) => candidate.id === "stackkit.apply");
    const followUp: HandoffFollowUp = {
      id: "stackkit.apply",
      mutation: true,
      idempotent: apply?.idempotent ?? false,
      owner_approval: true,
      executable: false,
    };
    return {
      ready: false,
      capacity_status: capacity.overall,
      steps: [],
      apply_follow_up: followUp,
      blocked_reasons: [...new Set(blockedReasons)],
    };
  }

  private requireKit<T extends ToolName>(tool: T, stackkitId: string): { kit?: CatalogKit; result?: WebMcpResult<unknown> } {
    if (!this.catalog) return { result: authorityFailure(tool, this.catalogValidation) };
    const kit = this.catalog.kits.find((candidate) => candidate.stackkit_id === stackkitId);
    if (!kit) return { result: makeResult(tool, "invalid_input", {}, this.catalog, [makeNotice("UNKNOWN_STACKKIT", "error", "The StackKit is not declared by the authority.", "stackkit_id")] ) };
    return { kit };
  }

  private requireTier<T extends ToolName>(tool: T, kit: CatalogKit | undefined, computeTier: ComputeTier): { profile?: TierProfile; result?: WebMcpResult<unknown> } {
    if (!kit) return { result: authorityFailure(tool, this.catalogValidation) };
    if (!kit.compute_tiers.includes(computeTier) || !kit.tiers[computeTier]) {
      return { result: makeResult(tool, "invalid_input", {}, this.catalog, [makeNotice("UNDECLARED_COMPUTE_TIER", "error", "The compute tier is not declared for this StackKit.", "compute_tier")]) };
    }
    return { profile: kit.tiers[computeTier] };
  }

  private invalidInput<T extends ToolName>(tool: T, field?: string): PlannerResult<T> {
    return makeResult(tool, "invalid_input", {}, this.catalog, [makeNotice("INVALID_INPUT", "error", "Input does not match the closed tool schema.", field)]) as PlannerResult<T>;
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

function operationStep(operation: OperationMetadata, input: PrepareHandoffInput, authoring: Map<string, string>, useCaseIds: string[]): HandoffStep {
  const argv = operation.id === "stackkit.init"
    ? initArgv(input, authoring, useCaseIds)
    : argvForOperation(operation);
  return {
    id: operation.id,
    argv,
    mutation: operation.mutation,
    idempotent: operation.idempotent,
    owner_approval: operation.owner_approval,
  };
}

function argvForOperation(operation: OperationMetadata): string[] {
  return ["stackkit", ...operation.command];
}

function initArgv(input: PrepareHandoffInput, authoring: Map<string, string>, useCaseIds: string[]): string[] {
  const argv = ["stackkit", "init", input.stackkit_id, "--compute-tier", input.compute_tier, "--non-interactive", "--owner-source=local"];
  const domain = authoring.get("network.domain.base");
  const name = authoring.get("metadata.name");
  if (domain) argv.push("--domain", domain);
  if (name) argv.push("--name", name);
  for (const useCaseId of useCaseIds) argv.push("--use-case", useCaseId);
  return argv;
}

function isSupportedAuthoringPath(path: string): boolean {
  return path === "network.domain.base" || path === "metadata.name";
}

function isValidAuthoringValue(path: string, value: string): boolean {
  if (path === "network.domain.base") return AUTHORING_DOMAIN.test(value);
  if (path === "metadata.name") return AUTHORING_NAME.test(value);
  return false;
}

function compactTierProfile(profile: TierProfile): TierProfileData {
  const host = profile.host_requirements;
  const declared = profile.module_runtime_requirements
    .filter((entry) => entry.declaration === "declared")
    .map((entry): [string, RuntimeRequirementTuple] => {
      const requirements = entry.runtime_requirements;
      return [
        entry.module_id,
        [
          numberOrNull(requirements?.min_cpu_cores),
          numberOrNull(requirements?.min_ram_gb),
          numberOrNull(requirements?.min_storage_gb),
          numberOrNull(requirements?.recommended_cpu_cores),
          numberOrNull(requirements?.recommended_ram_gb),
          numberOrNull(requirements?.recommended_storage_gb),
        ],
      ];
    });
  const notDeclared = profile.module_runtime_requirements
    .filter((entry) => entry.declaration === "not_declared")
    .map((entry) => entry.module_id)
    .sort();
  const included: CompactIncludedUseCase[] = profile.use_case_fits
    .filter((fit) => fit.included)
    .map((fit) => [
      fit.use_case_id,
      fit.module_slug ?? fit.alternative_id ?? null,
      [...(fit.functions ?? [])],
      fit.load ? [fit.load.residency, fit.load.baseline, fit.load.burst] : null,
    ]);
  const excluded = profile.use_case_fits
    .filter((fit) => !fit.included)
    .map((fit) => fit.use_case_id)
    .sort();
  return {
    capacity: {
      min: [host.min_cpu_cores, host.min_ram_gb, host.min_storage_gb],
      recommended: [
        numberOrNull(host.recommended_cpu_cores),
        numberOrNull(host.recommended_ram_gb),
        numberOrNull(host.recommended_storage_gb),
      ],
    },
    graph: {
      substitutions: Object.entries(profile.module_substitutions)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([from, to]) => [from, to]),
      ...(profile.enable_capabilities.length > 0 ? { capabilities: [...profile.enable_capabilities].sort() } : {}),
    },
    modules: { declared, undeclared: notDeclared },
    use_cases: {
      included,
      excluded,
    },
  };
}

function numberOrNull(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
