import { WEBMCP_CATALOG_SCHEMA } from "./generated/catalog-schema.js";
import { validateSchema } from "./schema.js";
import type {
  CatalogKit,
  CatalogModule,
  ModuleAxisProfile,
  ModuleComputeProfile,
  OperationMetadata,
  WebMcpCatalog,
} from "./types.js";
import { SCHEMA_VERSION } from "./types.js";

export const CATALOG_PATH = "/data/stackkits-webmcp/v2alpha1/catalog.json" as const;
export const LEGACY_CATALOG_PATH = "/data/stackkits-webmcp/catalog.json" as const;
export const COMPUTE_PROFILE_ORDER = ["low", "standard", "high"] as const;
export const REQUIRED_OPERATION_IDS = [
  "stackkit.init",
  "stackkit.validate",
  "stackkit.resolve",
  "stackkit.generate",
  "stackkit.plan",
  "stackkit.apply",
] as const;

const PROFILE_IDS = new Set<string>(COMPUTE_PROFILE_ORDER);
const SENSITIVE_REFERENCE = /(?:\b(?:secret(?:s)?|credential(?:s)?|password|token|endpoint|socket|access[-_ ]?key|client[-_ ]?secret|private|internal|provider)[-_ ]?(?:ref(?:s|erence|erences)?|url|uri|id|name|address|host)\b|(?:https?|wss?|ssh|git|file|secret|doppler|vault|credential|provider):\/\/)/i;

type Schema = Record<string, unknown>;

export interface CatalogValidation {
  valid: boolean;
  catalog?: WebMcpCatalog;
  issues: string[];
}

export interface CatalogLoadOptions {
  /** Maximum end-to-end catalog load time. Values above the safety cap are clamped. */
  timeoutMs?: number;
}

export const CATALOG_LOAD_TIMEOUT_MS = 4_500 as const;

export class CatalogValidationError extends Error {
  readonly code = "AUTHORITY_INTEGRITY_FAILED" as const;

  constructor(message: string) {
    super(message);
    this.name = "CatalogValidationError";
  }
}

export class CatalogFetchError extends Error {
  readonly code = "CATALOG_FETCH_FAILED" as const;
  readonly timedOut: boolean;

  constructor(message: string, timedOut = false) {
    super(message);
    this.name = "CatalogFetchError";
    this.timedOut = timedOut;
  }
}

// Only fully verified, deeply frozen catalog objects enter this cache. This
// preserves the public validation boundary for mutable or externally-created
// values while avoiding repeated work for one immutable page catalog.
const verifiedCatalogs = new WeakMap<object, WebMcpCatalog>();

/** Validate the closed public JSON shape plus cross-reference invariants. */
export function validateCatalog(value: unknown): CatalogValidation {
  const cached = cachedVerifiedCatalog(value);
  if (cached) return { valid: true, catalog: cached, issues: [] };
  const schema = WEBMCP_CATALOG_SCHEMA as unknown as Schema;
  const definitions = isRecord(schema.$defs) ? schema.$defs as Record<string, Schema> : {};
  const shape = validateSchema(schema, value, "$", new Set(), definitions);
  const issues = shape.issues.map((issue) => `${issue.path}:${issue.keyword}`);
  if (!isRecord(value)) return { valid: false, issues };
  if (containsSensitiveReference(value)) issues.push("catalog contains a private or sensitive reference");
  if (value.schema_version !== SCHEMA_VERSION) issues.push("catalog schema version is not supported");

  if (Array.isArray(value.kits)) validateKits(value.kits, issues);
  if (Array.isArray(value.operations)) validateOperations(value.operations, issues);
  if (issues.length > 0) return { valid: false, issues: unique(issues) };

  return {
    valid: true,
    catalog: deepFreeze(clone(value) as unknown as WebMcpCatalog),
    issues: [],
  };
}

/** Validate the public shape and all content-addressed digests before use. */
export async function validateAndVerifyCatalog(value: unknown): Promise<CatalogValidation> {
  const cached = cachedVerifiedCatalog(value);
  if (cached) return { valid: true, catalog: cached, issues: [] };
  const validation = validateCatalog(value);
  if (!validation.valid || !validation.catalog) return validation;
  const issues: string[] = [];
  try {
    if (!(await verifyCatalogDigest(validation.catalog))) {
      issues.push("catalog digest does not match its content");
    }
    for (const kit of validation.catalog.kits) {
      for (const module of kit.modules) {
        for (const profile of module.compute_profiles) {
          if (!(await verifyProfileDigest(profile))) {
            issues.push(`profile digest does not match: ${kit.stackkit_id}/${module.module_id}/compute/${profile.id}`);
          }
        }
        for (const [axis, profiles] of [["storage", module.storage_profiles], ["accelerator", module.accelerator_profiles]] as const) {
          for (const profile of profiles) {
            if (!(await verifyProfileDigest(profile))) {
              issues.push(`profile digest does not match: ${kit.stackkit_id}/${module.module_id}/${axis}/${profile.id}`);
            }
          }
        }
      }
    }
  } catch {
    issues.push("catalog digest could not be verified");
  }
  if (issues.length > 0) return { valid: false, issues };
  verifiedCatalogs.set(validation.catalog, validation.catalog);
  return validation;
}

export async function loadCatalog(
  fetcher: typeof fetch = globalThis.fetch,
  signal?: AbortSignal,
  options: CatalogLoadOptions = {},
): Promise<WebMcpCatalog> {
  if (signal?.aborted) throw abortError();
  if (typeof fetcher !== "function") throw new CatalogFetchError("catalog fetch is unavailable");
  const requestedTimeout = options.timeoutMs ?? CATALOG_LOAD_TIMEOUT_MS;
  if (!Number.isFinite(requestedTimeout) || requestedTimeout <= 0) {
    throw new RangeError("catalog load timeout must be a positive finite number");
  }
  const timeoutMs = Math.min(requestedTimeout, CATALOG_LOAD_TIMEOUT_MS);
  const requestController = new AbortController();
  const forwardAbort = (): void => requestController.abort();
  signal?.addEventListener("abort", forwardAbort, { once: true });
  let timedOut = false;
  let timeoutHandle: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_resolve, reject) => {
    timeoutHandle = setTimeout(() => {
      timedOut = true;
      requestController.abort();
      reject(new CatalogFetchError("catalog fetch exceeded its time budget", true));
    }, timeoutMs);
  });
  const operation = (async (): Promise<WebMcpCatalog> => {
    let response: Response;
    try {
      response = await fetcher(CATALOG_PATH, { signal: requestController.signal });
    } catch {
      if (signal?.aborted) throw abortError();
      if (timedOut) throw new CatalogFetchError("catalog fetch exceeded its time budget", true);
      throw new CatalogFetchError("catalog request failed");
    }
    if (!response.ok) throw new CatalogFetchError(`catalog request failed with status ${response.status}`);
    let raw: unknown;
    try {
      raw = await response.json();
    } catch {
      if (signal?.aborted) throw abortError();
      throw new CatalogValidationError("catalog response is not valid JSON");
    }
    if (signal?.aborted) throw abortError();
    const validation = await validateAndVerifyCatalog(raw);
    if (signal?.aborted) throw abortError();
    if (!validation.valid || !validation.catalog) throw new CatalogValidationError(validation.issues.join("; "));
    return validation.catalog;
  })();
  try {
    return await Promise.race([operation, deadline]);
  } finally {
    if (timeoutHandle) clearTimeout(timeoutHandle);
    signal?.removeEventListener("abort", forwardAbort);
  }
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

export function profileDigestPayload<T extends ModuleComputeProfile | ModuleAxisProfile>(
  profile: T,
): Omit<T, "profile_sha256"> {
  const { profile_sha256: _digest, ...payload } = profile;
  return payload;
}

export async function verifyCatalogDigest(catalog: WebMcpCatalog): Promise<boolean> {
  return await digestJson(catalogDigestPayload(catalog)) === catalog.catalog_sha256;
}

export async function verifyProfileDigest(profile: ModuleComputeProfile | ModuleAxisProfile): Promise<boolean> {
  return await digestJson(profileDigestPayload(profile)) === profile.profile_sha256;
}

export function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map((entry) => canonicalJson(entry)).join(",")}]`;
  if (isRecord(value)) {
    const keys = Object.keys(value).filter((key) => value[key] !== undefined).sort();
    return `{${keys.map((key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(",")}}`;
  }
  const encoded = JSON.stringify(value);
  if (encoded === undefined) throw new TypeError("undefined is not canonical JSON");
  return encoded;
}

export async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const digest = await globalThis.crypto.subtle.digest("SHA-256", bytes as Uint8Array<ArrayBuffer>);
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function validateKits(rawKits: unknown[], issues: string[]): void {
  if (rawKits.length === 0) issues.push("catalog has no public kits");
  const kits = rawKits as CatalogKit[];
  uniqueSorted(kits.map((kit) => kit.stackkit_id), "kits", issues);
  for (const kit of kits) {
    if (kit.modules.length === 0) issues.push(`${kit.stackkit_id} has no modules`);
    uniqueSorted(kit.modules.map((module) => module.module_id), `${kit.stackkit_id}.modules`, issues);
    uniqueSorted(kit.use_cases.map((useCase) => useCase.use_case_id), `${kit.stackkit_id}.use_cases`, issues);
    const moduleIds = new Set(kit.modules.map((module) => module.module_id));
    const useCaseIds = new Set(kit.use_cases.map((useCase) => useCase.use_case_id));
    for (const module of kit.modules) validateModule(kit.stackkit_id, module, useCaseIds, issues);
    for (const useCase of kit.use_cases) {
      uniqueSorted(useCase.alternatives.map((alternative) => alternative.alternative_id), `${kit.stackkit_id}.${useCase.use_case_id}.alternatives`, issues);
      if (useCase.availability === "available" && useCase.alternatives.length === 0) {
        issues.push(`${kit.stackkit_id}.${useCase.use_case_id} is available without an alternative`);
      }
      if (useCase.availability === "blocked" && !useCase.reason_code) {
        issues.push(`${kit.stackkit_id}.${useCase.use_case_id} is blocked without a reason code`);
      }
      for (const alternative of useCase.alternatives) {
        if (!moduleIds.has(alternative.module_id)) {
          issues.push(`${kit.stackkit_id}.${useCase.use_case_id} references unknown module ${alternative.module_id}`);
        }
      }
      if (useCase.default_alternative_id && !useCase.alternatives.some((alternative) => alternative.alternative_id === useCase.default_alternative_id)) {
        issues.push(`${kit.stackkit_id}.${useCase.use_case_id} default alternative is not declared`);
      }
    }
    const legacyOrder = kit.legacy_compute_tier_mappings.map((mapping) => mapping.compute_tier);
    if (!sameJson(legacyOrder, COMPUTE_PROFILE_ORDER.filter((id) => legacyOrder.includes(id)))) {
      issues.push(`${kit.stackkit_id}.legacy_compute_tier_mappings is not in stable order`);
    }
  }
}

function validateModule(stackkitId: string, module: CatalogModule, useCaseIds: Set<string>, issues: string[]): void {
  uniqueSorted(module.compute_profiles.map((profile) => profile.id), `${stackkitId}.${module.module_id}.compute_profiles`, issues, COMPUTE_PROFILE_ORDER);
  uniqueSorted(module.storage_profiles.map((profile) => profile.id), `${stackkitId}.${module.module_id}.storage_profiles`, issues);
  uniqueSorted(module.accelerator_profiles.map((profile) => profile.id), `${stackkitId}.${module.module_id}.accelerator_profiles`, issues);
  for (const id of module.compute_profiles.map((profile) => profile.id)) {
    if (!PROFILE_IDS.has(id)) issues.push(`${stackkitId}.${module.module_id} has unsupported compute profile ${id}`);
  }
  if (module.default_compute_profile && !module.compute_profiles.some((profile) => profile.id === module.default_compute_profile)) {
    issues.push(`${stackkitId}.${module.module_id} default compute profile is not declared`);
  }
  if (module.default_storage_profile && !module.storage_profiles.some((profile) => profile.id === module.default_storage_profile)) {
    issues.push(`${stackkitId}.${module.module_id} default storage profile is not declared`);
  }
  if (module.default_accelerator_profile && !module.accelerator_profiles.some((profile) => profile.id === module.default_accelerator_profile)) {
    issues.push(`${stackkitId}.${module.module_id} default accelerator profile is not declared`);
  }
  for (const useCaseId of module.use_case_ids) {
    if (!useCaseIds.has(useCaseId)) issues.push(`${stackkitId}.${module.module_id} references unknown use case ${useCaseId}`);
  }
  for (const profile of module.compute_profiles) {
    const declaredAxes = new Set<string>([
      ...Object.keys(profile.host_floor ?? {}),
      ...Object.keys(profile.reservation ?? {}),
    ]);
    const declaration = declaredAxes.size === 0 ? "not_declared" : declaredAxes.size === 3 ? "declared" : "partial";
    if (profile.capacity_declaration !== declaration) {
      issues.push(`${stackkitId}.${module.module_id}.${profile.id} capacity declaration is inconsistent`);
    }
  }
  for (const [axis, profiles] of [["storage", module.storage_profiles], ["accelerator", module.accelerator_profiles]] as const) {
    for (const profile of profiles) {
      const declaration = profile.reservation ? "declared" : "not_declared";
      if (profile.capacity_declaration !== declaration) {
        issues.push(`${stackkitId}.${module.module_id}.${axis}.${profile.id} capacity declaration is inconsistent`);
      }
    }
  }
}

function validateOperations(rawOperations: unknown[], issues: string[]): void {
  const operations = rawOperations as OperationMetadata[];
  uniqueSorted(operations.map((operation) => operation.id), "operations", issues);
  const ids = new Set(operations.map((operation) => operation.id));
  for (const required of REQUIRED_OPERATION_IDS) {
    if (!ids.has(required)) issues.push(`required operation is missing: ${required}`);
  }
  const apply = operations.find((operation) => operation.id === "stackkit.apply");
  if (apply && (!apply.mutation || !apply.owner_approval)) {
    issues.push("stackkit.apply must remain an approval-gated mutation");
  }
}

function uniqueSorted(
  values: string[],
  path: string,
  issues: string[],
  order?: readonly string[],
): void {
  if (new Set(values).size !== values.length) issues.push(`${path} contains duplicates`);
  const expected = order
    ? [...values].sort((left, right) => order.indexOf(left) - order.indexOf(right))
    : [...values].sort((left, right) => left.localeCompare(right));
  if (!sameJson(values, expected)) issues.push(`${path} is not sorted`);
}

async function digestJson(value: unknown): Promise<string> {
  return sha256Hex(new TextEncoder().encode(canonicalJson(value)));
}

function containsSensitiveReference(value: unknown): boolean {
  if (typeof value === "string") return SENSITIVE_REFERENCE.test(value);
  if (Array.isArray(value)) return value.some(containsSensitiveReference);
  if (!isRecord(value)) return false;
  return Object.entries(value).some(([key, entry]) => SENSITIVE_REFERENCE.test(key) || containsSensitiveReference(entry));
}

function deepFreeze<T>(value: T): T {
  if (Array.isArray(value)) {
    for (const entry of value) deepFreeze(entry);
  } else if (isRecord(value)) {
    for (const entry of Object.values(value)) deepFreeze(entry);
  }
  return Object.freeze(value);
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function unique(values: string[]): string[] {
  return [...new Set(values)];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function cachedVerifiedCatalog(value: unknown): WebMcpCatalog | undefined {
  return value !== null && typeof value === "object" ? verifiedCatalogs.get(value) : undefined;
}

function sameJson(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function abortError(): Error {
  if (typeof DOMException === "function") return new DOMException("The operation was aborted", "AbortError");
  const error = new Error("The operation was aborted");
  error.name = "AbortError";
  return error;
}
