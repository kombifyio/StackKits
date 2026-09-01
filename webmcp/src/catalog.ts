import type {
  CatalogKit,
  ComputeTier,
  HostRequirements,
  ModuleRuntimeRequirement,
  OperationMetadata,
  TierProfile,
  UseCaseFit,
  WebMcpCatalog,
} from "./types.js";
import { SCHEMA_VERSION } from "./types.js";

export const CATALOG_PATH = "/data/stackkits-webmcp/catalog.json" as const;
export const TIER_ORDER: readonly ComputeTier[] = ["low", "standard", "high"];
export const REQUIRED_OPERATION_IDS = ["stackkit.init", "stackkit.validate", "stackkit.resolve", "stackkit.generate", "stackkit.plan", "stackkit.apply"] as const;

const SHA40 = /^[a-f0-9]{40}$/;
const SHA64 = /^[a-f0-9]{64}$/;
const STACKKIT_ID = /^[a-z][a-z0-9-]{0,62}$/;
const OPERATION_ID = /^stackkit\.[a-z0-9.-]+$/;
const USE_CASE_ID = /^[a-z][a-z0-9-]{0,62}$/;
const MODULE_ID = /^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$/;
const TOOL_NAME = /^stackkit_[a-z0-9_]+$/;
const SENSITIVE_REFERENCE = /(?:\b(?:secret(?:s)?|credential(?:s)?|password|token|endpoint|socket|access[-_ ]?key|client[-_ ]?secret|private|internal|provider)[-_ ]?(?:ref(?:s|erence|erences)?|url|uri|id|name|address|host)\b|(?:https?|wss?|ssh|git|file|secret|doppler|vault|credential|provider):\/\/)/i;

const CATALOG_KEYS = new Set(["schema_version", "source_sha", "authority_bundle_sha256", "catalog_sha256", "kits", "operations"]);
const KIT_KEYS = new Set(["stackkit_id", "display_name", "version", "description", "status", "planner_link", "compute_tiers", "tiers", "required_authoring_inputs"]);
const TIER_KEYS = new Set(["compute_tier", "host_requirements", "platform_management", "enable_capabilities", "module_substitutions", "module_runtime_requirements", "use_case_fits"]);
const HOST_REQUIREMENT_KEYS = new Set(["min_cpu_cores", "min_ram_gb", "min_storage_gb", "recommended_cpu_cores", "recommended_ram_gb", "recommended_storage_gb", "architectures", "virtualization"]);
const RUNTIME_REQUIREMENT_KEYS = new Set(["min_cpu_cores", "min_ram_gb", "min_storage_gb", "recommended_cpu_cores", "recommended_ram_gb", "recommended_storage_gb"]);
const MODULE_REQUIREMENT_KEYS = new Set(["module_id", "declaration", "runtime_requirements"]);
const USE_CASE_LOAD_KEYS = new Set(["residency", "baseline", "burst"]);
const USE_CASE_FIT_KEYS = new Set(["use_case_id", "title", "included", "functions", "load", "module_slug", "alternative_id", "reason", "notes"]);
const OPERATION_KEYS = new Set(["id", "tool_name", "title", "description", "command", "mutation", "destructive", "idempotent", "owner_approval"]);

export interface CatalogValidation {
  valid: boolean;
  catalog?: WebMcpCatalog;
  issues: string[];
}

export class CatalogValidationError extends Error {
  readonly code = "AUTHORITY_INTEGRITY_FAILED" as const;

  constructor(message: string) {
    super(message);
    this.name = "CatalogValidationError";
  }
}

export function validateCatalog(value: unknown): CatalogValidation {
  const issues: string[] = [];
  if (!isRecord(value)) return { valid: false, issues: ["catalog must be an object"] };
  rejectUnknownFields(value, "$", CATALOG_KEYS, issues);
  if (value.schema_version !== SCHEMA_VERSION) issues.push("catalog schema version is not supported");
  if (typeof value.source_sha !== "string" || !SHA40.test(value.source_sha)) issues.push("catalog source_sha is invalid");
  if (typeof value.authority_bundle_sha256 !== "string" || !SHA64.test(value.authority_bundle_sha256)) issues.push("catalog authority digest is invalid");
  if (typeof value.catalog_sha256 !== "string" || !SHA64.test(value.catalog_sha256)) issues.push("catalog digest is invalid");
  if (!Array.isArray(value.kits) || value.kits.length === 0) issues.push("catalog has no public kits");
  if (!Array.isArray(value.operations)) issues.push("catalog operations are missing");

  const kits: CatalogKit[] = [];
  if (Array.isArray(value.kits)) {
    const ids = new Set<string>();
    for (const [index, raw] of value.kits.entries()) {
      const kit = validateKit(raw, `kits[${index}]`, issues);
      if (kit) {
        if (ids.has(kit.stackkit_id)) issues.push(`duplicate kit: ${kit.stackkit_id}`);
        ids.add(kit.stackkit_id);
        kits.push(kit);
      }
    }
    if (!isSortedBy(kits, (kit) => kit.stackkit_id)) issues.push("kits are not sorted by stackkit_id");
  }

  const operations: OperationMetadata[] = [];
  if (Array.isArray(value.operations)) {
    const ids = new Set<string>();
    for (const [index, raw] of value.operations.entries()) {
      const operation = validateOperation(raw, `operations[${index}]`, issues);
      if (operation) {
        if (ids.has(operation.id)) issues.push(`duplicate operation: ${operation.id}`);
        ids.add(operation.id);
        operations.push(operation);
      }
    }
    if (!isSortedBy(operations, (operation) => operation.id)) issues.push("operations are not sorted by id");
    for (const required of REQUIRED_OPERATION_IDS) if (!ids.has(required)) issues.push(`required operation is missing: ${required}`);
  }

  if (issues.length > 0) return { valid: false, issues };
  return {
    valid: true,
    catalog: freezeCatalog({
      schema_version: SCHEMA_VERSION,
      source_sha: value.source_sha as string,
      authority_bundle_sha256: value.authority_bundle_sha256 as string,
      catalog_sha256: value.catalog_sha256 as string,
      kits,
      operations,
    }),
    issues: [],
  };
}

export async function verifyCatalogDigest(catalog: WebMcpCatalog): Promise<boolean> {
  const payload = catalogDigestPayload(catalog);
  const digest = await sha256Hex(new TextEncoder().encode(canonicalJson(payload)));
  return digest === catalog.catalog_sha256;
}

/** Validate the public shape and verify its content digest before consumption. */
export async function validateAndVerifyCatalog(value: unknown): Promise<CatalogValidation> {
  const validation = validateCatalog(value);
  if (!validation.valid || !validation.catalog) return validation;
  try {
    if (!(await verifyCatalogDigest(validation.catalog))) {
      return { valid: false, issues: ["catalog digest does not match its content"] };
    }
  } catch {
    return { valid: false, issues: ["catalog digest could not be verified"] };
  }
  return validation;
}

export async function loadCatalog(
  fetcher: typeof fetch = globalThis.fetch,
  signal?: AbortSignal,
): Promise<WebMcpCatalog> {
  if (signal?.aborted) throw abortError();
  if (typeof fetcher !== "function") throw new CatalogValidationError("fetch is unavailable");
  const response = await fetcher(CATALOG_PATH, { signal });
  if (!response.ok) throw new CatalogValidationError(`catalog request failed with status ${response.status}`);
  const raw: unknown = await response.json();
  if (signal?.aborted) throw abortError();
  const validation = await validateAndVerifyCatalog(raw);
  if (!validation.valid || !validation.catalog) throw new CatalogValidationError(validation.issues.join("; "));
  return validation.catalog;
}

export function catalogDigestPayload(catalog: WebMcpCatalog): Omit<WebMcpCatalog, "catalog_sha256"> {
  return {
    schema_version: catalog.schema_version,
    source_sha: catalog.source_sha,
    authority_bundle_sha256: catalog.authority_bundle_sha256,
    kits: catalog.kits,
    operations: catalog.operations,
  };
}

export function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map((entry) => canonicalJson(entry)).join(",")}]`;
  if (isRecord(value)) {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

export async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const digest = await globalThis.crypto.subtle.digest("SHA-256", bytes as Uint8Array<ArrayBuffer>);
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function validateKit(value: unknown, path: string, issues: string[]): CatalogKit | undefined {
  if (!isRecord(value)) {
    issues.push(`${path} must be an object`);
    return undefined;
  }
  rejectUnknownFields(value, path, KIT_KEYS, issues);
  const required = ["stackkit_id", "display_name", "version", "description", "status", "planner_link", "compute_tiers", "tiers", "required_authoring_inputs"];
  for (const key of required) if (!(key in value)) issues.push(`${path}.${key} is required`);
  if (typeof value.stackkit_id !== "string" || !STACKKIT_ID.test(value.stackkit_id)) issues.push(`${path}.stackkit_id is invalid`);
  validateText(value.display_name, `${path}.display_name`, issues, { minLength: 1, maxLength: 160 });
  validateText(value.version, `${path}.version`, issues, { minLength: 1, maxLength: 80 });
  validateText(value.description, `${path}.description`, issues, { maxLength: 500 });
  validateText(value.status, `${path}.status`, issues, { minLength: 1, maxLength: 80 });
  validateText(value.planner_link, `${path}.planner_link`, issues, { minLength: 1 });
  if (typeof value.planner_link === "string" && !/^\/planner(?:\?[a-z_]+=[a-z0-9-]+)?$/.test(value.planner_link)) issues.push(`${path}.planner_link is invalid`);
  if (!Array.isArray(value.compute_tiers)) issues.push(`${path}.compute_tiers must be an array`);
  const tiers: ComputeTier[] = [];
  if (Array.isArray(value.compute_tiers)) {
    for (const tier of value.compute_tiers) {
      if (!isTier(tier)) issues.push(`${path}.compute_tiers contains an unknown tier`);
      else tiers.push(tier);
    }
    if (new Set(tiers).size !== tiers.length) issues.push(`${path}.compute_tiers contains duplicates`);
    if (!isTierOrder(tiers)) issues.push(`${path}.compute_tiers is not in stable order`);
  }
  const tierMap: Record<string, TierProfile> = {};
  if (!isRecord(value.tiers)) issues.push(`${path}.tiers must be an object`);
  else {
    for (const key of Object.keys(value.tiers)) {
      if (!isTier(key)) {
        issues.push(`${path}.tiers contains an unknown tier`);
        continue;
      }
      const profile = validateTier(value.tiers[key], `${path}.tiers.${key}`, issues);
      if (profile) {
        if (profile.compute_tier !== key) issues.push(`${path}.tiers.${key}.compute_tier does not match its tier key`);
        tierMap[key] = profile;
      }
    }
    for (const tier of tiers) if (!(tier in tierMap)) issues.push(`${path}.tiers is missing ${tier}`);
    for (const key of Object.keys(tierMap)) if (!tiers.includes(key as ComputeTier)) issues.push(`${path}.tiers contains undeclared ${key}`);
  }
  if (!Array.isArray(value.required_authoring_inputs) || value.required_authoring_inputs.some((entry) => typeof entry !== "string" || entry.length === 0 || entry.length > 160 || hasSensitiveReference(entry))) issues.push(`${path}.required_authoring_inputs is invalid`);
  if (typeof value.stackkit_id !== "string" || typeof value.display_name !== "string" || typeof value.version !== "string" || typeof value.description !== "string" || typeof value.status !== "string" || typeof value.planner_link !== "string" || !Array.isArray(value.required_authoring_inputs)) return undefined;
  return {
    stackkit_id: value.stackkit_id,
    display_name: value.display_name,
    version: value.version,
    description: value.description,
    status: value.status,
    planner_link: value.planner_link,
    compute_tiers: tiers,
    tiers: tierMap,
    required_authoring_inputs: value.required_authoring_inputs as string[],
  };
}

function validateTier(value: unknown, path: string, issues: string[]): TierProfile | undefined {
  if (!isRecord(value)) {
    issues.push(`${path} must be an object`);
    return undefined;
  }
  rejectUnknownFields(value, path, TIER_KEYS, issues);
  const computeTier = value.compute_tier;
  if (!isTier(computeTier)) issues.push(`${path}.compute_tier is invalid`);
  const requirements = validateRequirements(value.host_requirements, `${path}.host_requirements`, issues);
  validateText(value.platform_management, `${path}.platform_management`, issues, { maxLength: 80 });
  const capabilities = stringArray(value.enable_capabilities, `${path}.enable_capabilities`, issues, 128);
  const substitutions = stringMap(value.module_substitutions, `${path}.module_substitutions`, issues, 128);
  const modules = Array.isArray(value.module_runtime_requirements) ? value.module_runtime_requirements.map((entry, index) => validateModule(entry, `${path}.module_runtime_requirements[${index}]`, issues)).filter((entry): entry is ModuleRuntimeRequirement => Boolean(entry)) : [];
  if (!Array.isArray(value.module_runtime_requirements)) issues.push(`${path}.module_runtime_requirements must be an array`);
  const fits = Array.isArray(value.use_case_fits) ? value.use_case_fits.map((entry, index) => validateFit(entry, `${path}.use_case_fits[${index}]`, issues)).filter((entry): entry is UseCaseFit => Boolean(entry)) : [];
  if (!Array.isArray(value.use_case_fits)) issues.push(`${path}.use_case_fits must be an array`);
  if (!requirements || !isTier(computeTier) || typeof value.platform_management !== "string") return undefined;
  return {
    compute_tier: computeTier,
    host_requirements: requirements,
    platform_management: value.platform_management,
    enable_capabilities: capabilities,
    module_substitutions: substitutions,
    module_runtime_requirements: modules,
    use_case_fits: fits,
  };
}

function validateRequirements(value: unknown, path: string, issues: string[]): HostRequirements | undefined {
  if (!isRecord(value)) {
    issues.push(`${path} must be an object`);
    return undefined;
  }
  rejectUnknownFields(value, path, HOST_REQUIREMENT_KEYS, issues);
  const required = ["min_cpu_cores", "min_ram_gb", "min_storage_gb"] as const;
  for (const key of required) if (typeof value[key] !== "number" || !Number.isFinite(value[key]) || value[key] <= 0) issues.push(`${path}.${key} is invalid`);
  const optional = ["recommended_cpu_cores", "recommended_ram_gb", "recommended_storage_gb"] as const;
  for (const key of optional) if (key in value && (typeof value[key] !== "number" || !Number.isFinite(value[key]) || value[key] <= 0)) issues.push(`${path}.${key} is invalid`);
  const architectures = value.architectures === undefined ? undefined : stringArray(value.architectures, `${path}.architectures`, issues, 64, true);
  const virtualization = value.virtualization === undefined ? undefined : stringArray(value.virtualization, `${path}.virtualization`, issues, 64, true);
  if (typeof value.min_cpu_cores !== "number" || typeof value.min_ram_gb !== "number" || typeof value.min_storage_gb !== "number") return undefined;
  return {
    min_cpu_cores: value.min_cpu_cores,
    min_ram_gb: value.min_ram_gb,
    min_storage_gb: value.min_storage_gb,
    ...(typeof value.recommended_cpu_cores === "number" ? { recommended_cpu_cores: value.recommended_cpu_cores } : {}),
    ...(typeof value.recommended_ram_gb === "number" ? { recommended_ram_gb: value.recommended_ram_gb } : {}),
    ...(typeof value.recommended_storage_gb === "number" ? { recommended_storage_gb: value.recommended_storage_gb } : {}),
    ...(architectures ? { architectures } : {}),
    ...(virtualization ? { virtualization } : {}),
  };
}

function validateModule(value: unknown, path: string, issues: string[]): ModuleRuntimeRequirement | undefined {
  if (!isRecord(value)) {
    issues.push(`${path} must be an object`);
    return undefined;
  }
  rejectUnknownFields(value, path, MODULE_REQUIREMENT_KEYS, issues);
  if (typeof value.module_id !== "string" || !MODULE_ID.test(value.module_id) || hasSensitiveReference(value.module_id)) issues.push(`${path}.module_id is invalid`);
  if (value.declaration !== "declared" && value.declaration !== "not_declared") issues.push(`${path}.declaration is invalid`);
  const runtimeRequirements = value.runtime_requirements === undefined ? undefined : validateRuntimeRequirements(value.runtime_requirements, `${path}.runtime_requirements`, issues);
  if (typeof value.module_id !== "string" || !MODULE_ID.test(value.module_id) || (value.declaration !== "declared" && value.declaration !== "not_declared")) return undefined;
  return {
    module_id: value.module_id,
    declaration: value.declaration,
    ...(value.runtime_requirements === undefined ? {} : { runtime_requirements: runtimeRequirements ?? null }),
  };
}

function validateRuntimeRequirements(
  value: unknown,
  path: string,
  issues: string[],
): Record<string, number> | null | undefined {
  if (value === null) return null;
  if (!isRecord(value)) {
    issues.push(`${path} is invalid`);
    return undefined;
  }
  rejectUnknownFields(value, path, RUNTIME_REQUIREMENT_KEYS, issues);
  const result: Record<string, number> = {};
  for (const key of RUNTIME_REQUIREMENT_KEYS) {
    if (value[key] === undefined) continue;
    if (typeof value[key] !== "number" || !Number.isFinite(value[key]) || value[key] < 0) issues.push(`${path}.${key} is invalid`);
    else result[key] = value[key];
  }
  if (containsSensitiveReference(value)) issues.push(`${path} contains a sensitive reference`);
  return result;
}

function validateFit(value: unknown, path: string, issues: string[]): UseCaseFit | undefined {
  if (!isRecord(value)) {
    issues.push(`${path} must be an object`);
    return undefined;
  }
  rejectUnknownFields(value, path, USE_CASE_FIT_KEYS, issues);
  if (typeof value.use_case_id !== "string" || !USE_CASE_ID.test(value.use_case_id) || hasSensitiveReference(value.use_case_id)) issues.push(`${path}.use_case_id is invalid`);
  if (typeof value.included !== "boolean") issues.push(`${path}.included is invalid`);
  const title = value.title === undefined ? undefined : validateText(value.title, `${path}.title`, issues, { minLength: 1, maxLength: 160 });
  const functions = value.functions === undefined ? undefined : stringArray(value.functions, `${path}.functions`, issues, 80);
  const load = value.load === undefined ? undefined : validateLoad(value.load, `${path}.load`, issues);
  const moduleSlug = value.module_slug === undefined ? undefined : validateText(value.module_slug, `${path}.module_slug`, issues, { maxLength: 128 });
  const alternativeId = value.alternative_id === undefined ? undefined : validateText(value.alternative_id, `${path}.alternative_id`, issues, { maxLength: 128 });
  const reason = value.reason === undefined ? undefined : validateText(value.reason, `${path}.reason`, issues, { minLength: 1, maxLength: 500 });
  const notes = value.notes === undefined ? undefined : stringArray(value.notes, `${path}.notes`, issues, 300);
  if (typeof value.use_case_id !== "string" || !USE_CASE_ID.test(value.use_case_id) || typeof value.included !== "boolean") return undefined;
  return {
    use_case_id: value.use_case_id,
    ...(title !== undefined ? { title } : {}),
    included: value.included,
    ...(functions !== undefined ? { functions } : {}),
    ...(value.load === undefined ? {} : { load: load ?? null }),
    ...(moduleSlug !== undefined ? { module_slug: moduleSlug } : {}),
    ...(alternativeId !== undefined ? { alternative_id: alternativeId } : {}),
    ...(reason !== undefined ? { reason } : {}),
    ...(notes !== undefined ? { notes } : {}),
  };
}

function validateLoad(value: unknown, path: string, issues: string[]): UseCaseFit["load"] {
  if (value === null) return null;
  if (!isRecord(value)) {
    issues.push(`${path} is invalid`);
    return undefined;
  }
  rejectUnknownFields(value, path, USE_CASE_LOAD_KEYS, issues);
  const residency = value.residency;
  const baseline = value.baseline;
  const burst = value.burst;
  if (residency !== "always-on" && residency !== "on-demand" && residency !== "scheduled") issues.push(`${path}.residency is invalid`);
  if (baseline !== "none" && baseline !== "idle-resident" && baseline !== "active-resident") issues.push(`${path}.baseline is invalid`);
  if (burst !== "none" && burst !== "interactive" && burst !== "ingest" && burst !== "batch") issues.push(`${path}.burst is invalid`);
  if (residency !== "always-on" && residency !== "on-demand" && residency !== "scheduled") return undefined;
  if (baseline !== "none" && baseline !== "idle-resident" && baseline !== "active-resident") return undefined;
  if (burst !== "none" && burst !== "interactive" && burst !== "ingest" && burst !== "batch") return undefined;
  return { residency, baseline, burst };
}

function validateOperation(value: unknown, path: string, issues: string[]): OperationMetadata | undefined {
  if (!isRecord(value)) {
    issues.push(`${path} must be an object`);
    return undefined;
  }
  rejectUnknownFields(value, path, OPERATION_KEYS, issues);
  for (const key of ["id", "tool_name"] as const) if (typeof value[key] !== "string" || value[key].length === 0) issues.push(`${path}.${key} is invalid`);
  const title = validateText(value.title, `${path}.title`, issues, { minLength: 1, maxLength: 160 });
  const description = validateText(value.description, `${path}.description`, issues, { minLength: 1, maxLength: 500 });
  if (typeof value.id === "string" && !OPERATION_ID.test(value.id)) issues.push(`${path}.id is invalid`);
  if (typeof value.tool_name !== "string" || !TOOL_NAME.test(value.tool_name)) issues.push(`${path}.tool_name is invalid`);
  if (!Array.isArray(value.command) || value.command.length === 0 || value.command.some((entry) => typeof entry !== "string" || entry.length === 0 || entry.length > 160 || hasSensitiveReference(entry))) issues.push(`${path}.command is invalid`);
  for (const key of ["mutation", "destructive", "idempotent", "owner_approval"] as const) if (typeof value[key] !== "boolean") issues.push(`${path}.${key} is invalid`);
  if (typeof value.id !== "string" || !OPERATION_ID.test(value.id) || typeof value.tool_name !== "string" || !TOOL_NAME.test(value.tool_name) || title === undefined || description === undefined || !Array.isArray(value.command) || value.command.some((entry) => typeof entry !== "string" || entry.length === 0 || entry.length > 160 || hasSensitiveReference(entry)) || ["mutation", "destructive", "idempotent", "owner_approval"].some((key) => typeof value[key] !== "boolean")) return undefined;
  return {
    id: value.id,
    tool_name: value.tool_name,
    title,
    description,
    command: value.command as string[],
    mutation: value.mutation as boolean,
    destructive: value.destructive as boolean,
    idempotent: value.idempotent as boolean,
    owner_approval: value.owner_approval as boolean,
  };
}

function stringArray(value: unknown, path: string, issues: string[], maxLength = Number.POSITIVE_INFINITY, unique = false): string[] {
  if (!Array.isArray(value) || value.some((entry) => typeof entry !== "string" || entry.length > maxLength || hasSensitiveReference(entry))) {
    issues.push(`${path} must be a string array`);
    return [];
  }
  if (unique && new Set(value).size !== value.length) issues.push(`${path} must contain unique values`);
  return [...value].sort();
}

function stringMap(value: unknown, path: string, issues: string[], maxLength = Number.POSITIVE_INFINITY): Record<string, string> {
  if (!isRecord(value) || Object.entries(value).some(([key, entry]) => key.length > maxLength || hasSensitiveReference(key) || typeof entry !== "string" || entry.length > maxLength || hasSensitiveReference(entry))) {
    issues.push(`${path} must be a string map`);
    return {};
  }
  return Object.fromEntries(Object.entries(value).sort(([left], [right]) => left.localeCompare(right)).map(([key, entry]) => [key, entry as string]));
}

function validateText(
  value: unknown,
  path: string,
  issues: string[],
  options: { minLength?: number; maxLength?: number } = {},
): string | undefined {
  const minLength = options.minLength ?? 0;
  const maxLength = options.maxLength ?? Number.POSITIVE_INFINITY;
  const valid = typeof value === "string" && value.length >= minLength && value.length <= maxLength && !hasSensitiveReference(value);
  if (!valid) issues.push(`${path} is invalid`);
  return valid ? value : undefined;
}

function containsSensitiveReference(value: Record<string, unknown>): boolean {
  for (const [key, entry] of Object.entries(value)) {
    if (hasSensitiveReference(key)) return true;
    if (typeof entry === "string" && hasSensitiveReference(entry)) return true;
    if (isRecord(entry) && containsSensitiveReference(entry)) return true;
    if (Array.isArray(entry) && entry.some((item) => typeof item === "string" ? hasSensitiveReference(item) : isRecord(item) && containsSensitiveReference(item))) return true;
  }
  return false;
}

function rejectUnknownFields(value: Record<string, unknown>, path: string, allowed: Set<string>, issues: string[]): void {
  for (const key of Object.keys(value)) if (!allowed.has(key)) issues.push(`${path}.${key} is not supported by the catalog schema`);
}

function hasSensitiveReference(value: string): boolean {
  return SENSITIVE_REFERENCE.test(value);
}

function freezeCatalog(catalog: WebMcpCatalog): WebMcpCatalog {
  return deepFreeze(catalog);
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const child of Object.values(value)) deepFreeze(child);
    Object.freeze(value);
  }
  return value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isTier(value: unknown): value is ComputeTier {
  return value === "low" || value === "standard" || value === "high";
}

function isTierOrder(values: ComputeTier[]): boolean {
  let previous = -1;
  for (const value of values) {
    const current = TIER_ORDER.indexOf(value);
    if (current <= previous) return false;
    previous = current;
  }
  return true;
}

function isSortedBy<T>(values: T[], key: (value: T) => string): boolean {
  return values.every((value, index) => index === 0 || key(values[index - 1] as T) <= key(value));
}

function abortError(): DOMException {
  return new DOMException("The operation was aborted", "AbortError");
}
