#!/usr/bin/env node

/*
 * Build-time authority projection. This file intentionally has no package
 * imports so a clean Node 24 checkout can run it before installing npm
 * dependencies. It reads only an OSS authority bundle and writes one
 * deterministic WebMCP catalog.
 */
import { createHash } from "node:crypto";
import { mkdir, readFile, readdir, stat, writeFile } from "node:fs/promises";
import { basename, dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const SCHEMA_VERSION = "stackkits-webmcp/v1";
const SCHEMA_VERSION_V2 = "stackkits-webmcp/v2alpha1";
const AUTHORITY_SCHEMA_VERSION = "stackkit.architecture-authority-bundle/v2";
const FIT_SCHEMA_VERSIONS = new Set([
  "stackkits-use-case-catalog/v1",
  "stackkits-use-case-compute-tier-fits/v1",
  "stackkits-compute-tier-fits/v1",
  "stackkits-webmcp-compute-tier-fits/v1",
]);
const OPERATIONS_SCHEMA_VERSIONS = new Set([
  "stackkits-standalone-operations/v1",
  "stackkits-operations/v1",
  "stackkits-webmcp-operations/v1",
]);
const TIERS = ["low", "standard", "high"];
const REQUIRED_OPERATIONS = ["stackkit.init", "stackkit.validate", "stackkit.resolve", "stackkit.generate", "stackkit.plan", "stackkit.apply"];
const SOURCE_SHA = /^[a-f0-9]{40}$/;
const ID = /^[a-z][a-z0-9-]{0,62}$/;
const OPERATION_ID = /^stackkit\.[a-z0-9.-]+$/;
const SAFE_ID = /^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$/;
const PRIVATE_KEY = /^(?:provider|secret|credential|password|token|endpoint|socket|private|internal|accesskey|clientsecret)(?:[-_ ]?(?:ref|reference|url|uri|id|name|address|host))?$/i;
const URL_VALUE = /(?:https?|ssh|git|file):\/\//i;
const SENSITIVE_REFERENCE = /(?:\b(?:secret(?:s)?|credential(?:s)?|password|token|endpoint|socket|access[-_ ]?key|client[-_ ]?secret|private|internal|provider)[-_ ]?(?:ref(?:s|erence|erences)?|url|uri|id|name|address|host)\b|(?:https?|wss?|ssh|git|file|secret|doppler|vault|credential|provider):\/\/)/i;
const HOST_REQUIREMENT_KEYS = new Set([
  "headroomFactor",
  "minCpuCores",
  "minRamGB",
  "minStorageGB",
  "recommendedCpuCores",
  "recommendedRamGB",
  "recommendedStorageGB",
  "architectures",
  "allowedArchitectures",
  "virtualization",
  "allowedVirtualization",
]);
const RUNTIME_REQUIREMENT_KEYS = new Set([
  "minCpuCores",
  "minRamGB",
  "minStorageGB",
  "recommendedCpuCores",
  "recommendedRamGB",
  "recommendedStorageGB",
]);

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const isMain = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isMain) await main();

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const authorityRoot = resolve(args["authority-bundle"] ?? join(scriptRoot, "..", "..", "internal", "architecturev2", "authority_bundle"));
  const schema = args.schema ?? "v2alpha1";
  if (schema !== "v1" && schema !== "v2alpha1") fail(`unknown catalog schema: ${schema}`);
  const outputPath = resolve(args.out ?? (schema === "v1"
    ? join(scriptRoot, "..", "data", "stackkits-catalog.json")
    : join(scriptRoot, "..", "data", "stackkits-webmcp", "v2alpha1", "catalog.json")));
  const sourceSha = await resolveSourceSha(args["source-sha"]);
  const catalog = schema === "v1"
    ? await projectAuthorityBundle(authorityRoot, sourceSha, args["planner-path"] ?? "/planner")
    : await projectAuthorityBundleV2(authorityRoot, sourceSha, args["planner-path"] ?? "/planner");
  await mkdir(dirname(outputPath), { recursive: true });
  await writeFile(outputPath, `${JSON.stringify(catalog, null, 2)}\n`, "utf8");
  process.stdout.write(`${outputPath}\n`);
}

/**
 * Project the native module-local profile contract. The v1 function above is
 * intentionally retained as a compatibility adapter for existing consumers;
 * v2 never reads a kit-wide host requirement as a module profile.
 */
export async function projectAuthorityBundleV2(authorityRootPath, exactSourceSha, plannerPath = "/planner") {
  const root = resolve(authorityRootPath);
  if (!SOURCE_SHA.test(exactSourceSha ?? "")) fail("source SHA must be a full lowercase 40-character commit SHA");
  if (!/^\/planner$/.test(plannerPath)) fail("planner path must be /planner");
  const files = await requiredAuthorityFiles(root);
  const manifest = await readJson(files.manifest);
  await validateManifest(manifest, files, root);
  const catalogSource = await readJson(files.catalog);
  const fitsSource = await readJson(files.computeTierFits);
  const operationsSource = await readJson(files.operations);
  validateDocument(fitsSource, FIT_SCHEMA_VERSIONS, "compute-tier-fits.json");
  validateDocument(operationsSource, OPERATIONS_SCHEMA_VERSIONS, "operations.json");
  verifyContentDigest(fitsSource, "compute-tier-fits.json");
  verifyContentDigest(operationsSource, "operations.json");

  const authorityBundleSha = await authorityBundleDigest(root);
  const modules = indexById(arrayValue(catalogSource.modules), "metadata.id");
  const workloads = indexById(arrayValue(catalogSource.workloads), "metadata.id");
  const useCases = parseNativeUseCases(fitsSource, workloads);
  const operations = parseOperations(operationsSource);
  const kits = [];
  for (const [profileId, profilePath] of Object.entries(manifest.profiles ?? {}).sort(([left], [right]) => left.localeCompare(right))) {
    if (!ID.test(profileId) || typeof profilePath !== "string") fail(`invalid profile identity: ${profileId}`);
    const definition = await readJson(safeBundlePath(root, profilePath));
    kits.push(await projectNativeKit(profileId, definition, useCases, modules, workloads, plannerPath));
  }
  kits.sort((left, right) => left.stackkit_id.localeCompare(right.stackkit_id));
  const payload = {
    schema_version: SCHEMA_VERSION_V2,
    source_sha: exactSourceSha,
    authority_bundle_sha256: authorityBundleSha,
    kits,
    operations: operations.sort((left, right) => left.id.localeCompare(right.id)),
  };
  return { ...payload, catalog_sha256: sha256Hex(Buffer.from(canonicalJson(payload), "utf8")) };
}

async function projectNativeKit(profileId, definition, useCases, modules, workloads, plannerPath) {
  if (definition.kind !== "KitDefinition") fail(`profile is not a KitDefinition: ${profileId}`);
  const metadata = objectValue(definition.metadata, `definition metadata for ${profileId}`);
  const stackkitId = stringValue(metadata.slug, `${profileId}.metadata.slug`);
  if (stackkitId !== profileId || !ID.test(stackkitId)) fail(`profile identity mismatch: ${profileId}`);
  const authoring = objectValue(definition.authoring, `${profileId}.authoring`);
  const graphTiers = objectValue(definition.computeTierGraphs, `${profileId}.computeTierGraphs`);
  const moduleIds = nativeModuleClosure(definition, workloads, modules);
  const kitUseCases = nativeUseCasesForKit(definition, useCases, workloads, moduleIds);
  const projectedModules = [];
  for (const moduleId of [...moduleIds].sort()) {
    const module = modules.get(moduleId);
    if (!module) fail(`${profileId} references unknown module ${moduleId}`);
    projectedModules.push(await projectNativeModule(module, moduleId, kitUseCases));
  }
  const legacyTiers = ["low", "standard", "high"].filter((tier) => Object.prototype.hasOwnProperty.call(graphTiers, tier));
  return {
    stackkit_id: stackkitId,
    display_name: publicString(metadata.displayName, `${profileId}.metadata.displayName`),
    version: publicString(metadata.version, `${profileId}.metadata.version`),
    description: publicString(metadata.description, `${profileId}.metadata.description`),
    status: publicString(authoring.initialSpecStatus ?? metadata.status, `${profileId}.status`),
    planner_link: `${plannerPath}?stackkit_id=${stackkitId}`,
    modules: projectedModules,
    use_cases: kitUseCases,
    legacy_compute_tier_mappings: legacyTiers.map((compute_tier) => ({
      compute_tier,
      status: "migration_only",
      reason_code: "LEGACY_GLOBAL_COMPUTE_TIER",
    })),
    required_authoring_inputs: stringArray(authoring.requiredOverrides ?? [], `${profileId}.authoring.requiredOverrides`, false).sort(),
  };
}

async function projectNativeModule(module, moduleId, useCases) {
  const role = module.role;
  if (!["foundation", "platform", "workload", "operations"].includes(role)) fail(`module ${moduleId} has an unsupported role`);
  const computeProfiles = await projectNativeProfileMap(module.computeProfiles ?? module.compute_profiles, `${moduleId}.computeProfiles`, true);
  const storageProfiles = await projectNativeProfileMap(module.storageProfiles ?? module.storage_profiles, `${moduleId}.storageProfiles`, false);
  const acceleratorProfiles = await projectNativeProfileMap(module.acceleratorProfiles ?? module.accelerator_profiles, `${moduleId}.acceleratorProfiles`, false);
  const capabilities = stringArray(module.provides ?? module.capabilities ?? [], `${moduleId}.provides`, false);
  const useCaseIds = useCases
    .filter((useCase) => useCase.alternatives.some((alternative) => alternative.module_id === moduleId))
    .map((useCase) => useCase.use_case_id)
    .sort();
  return {
    module_id: publicId(moduleId, `${moduleId}.id`),
    role,
    required: false,
    use_case_ids: useCaseIds,
    capabilities,
    compute_profiles: computeProfiles,
    storage_profiles: storageProfiles,
    accelerator_profiles: acceleratorProfiles,
  };
}

async function projectNativeProfileMap(raw, path, compute) {
  if (raw === undefined) return [];
  if (!isObject(raw)) fail(`${path} must be an object`);
  const result = [];
  for (const [id, value] of Object.entries(raw).sort(([left], [right]) => profileOrder(left, right))) {
    publicId(id, `${path}.${id}`);
    if (!isObject(value)) fail(`${path}.${id} must be an object`);
    result.push(compute ? await projectNativeComputeProfile(id, value, path) : await projectNativeAxisProfile(id, value, path));
  }
  return result;
}

function profileOrder(left, right) {
  const order = new Map([["low", 0], ["standard", 1], ["high", 2]]);
  const leftIndex = order.has(left) ? order.get(left) : 3;
  const rightIndex = order.has(right) ? order.get(right) : 3;
  return leftIndex - rightIndex || left.localeCompare(right);
}

async function projectNativeComputeProfile(id, source, path) {
  rejectUnknownKeys(source, `${path}.${id}`, new Set([
    "profileHash", "profile_sha256", "capacityDeclaration", "capacity_declaration", "maturity", "executable", "realization",
    "platformManagement", "platform_management", "hostFloor", "reservation", "recommended", "headroom", "architectures", "virtualization", "components", "capabilities", "degradations",
  ]));
  const hostFloor = isObject(source.hostFloor) ? source.hostFloor : undefined;
  const projected = {
    id: publicId(id, `${path}.${id}`),
    capacity_declaration: capacityDeclaration(source, true),
    maturity: profileMaturity(source, `${path}.${id}`),
    executable: requiredBoolean(source.executable, undefined, `${path}.${id}.executable`),
    realization: profileRealization(source, `${path}.${id}`),
    ...optionalResource("hostFloor", "host_floor", source, `${path}.${id}`),
    ...optionalResource("reservation", "reservation", source, `${path}.${id}`),
    ...optionalResource("headroom", "headroom", source, `${path}.${id}`),
    ...optionalResource("recommended", "recommended", source, `${path}.${id}`),
    architectures: publicStringArray(source.architectures ?? hostFloor?.allowedArchitectures ?? [], `${path}.${id}.architectures`, ["amd64", "arm64"]),
    virtualization: publicStringArray(source.virtualization ?? hostFloor?.allowedVirtualization ?? [], `${path}.${id}.virtualization`),
    components: publicIdArray(source.components ?? [], `${path}.${id}.components`),
    capabilities: publicIdArray(source.capabilities ?? [], `${path}.${id}.capabilities`),
    degradations: publicIdArray(source.degradations ?? [], `${path}.${id}.degradations`),
  };
  return { ...projected, profile_sha256: profileHash(source, projected) };
}

async function projectNativeAxisProfile(id, source, path) {
  rejectUnknownKeys(source, `${path}.${id}`, new Set([
    "profileHash", "profile_sha256", "capacityDeclaration", "capacity_declaration", "maturity", "realization", "reservation", "components", "capabilities",
  ]));
  const projected = {
    id: publicId(id, `${path}.${id}`),
    capacity_declaration: capacityDeclaration(source, false),
    maturity: profileMaturity(source, `${path}.${id}`),
    realization: profileRealization(source, `${path}.${id}`),
    ...optionalResource("reservation", "reservation", source, `${path}.${id}`),
    components: publicIdArray(source.components ?? [], `${path}.${id}.components`),
    capabilities: publicIdArray(source.capabilities ?? [], `${path}.${id}.capabilities`),
  };
  return { ...projected, profile_sha256: profileHash(source, projected) };
}

function profileHash(source, projected) {
  const declared = source.profileHash ?? source.profile_sha256;
  if (declared !== undefined) {
    if (typeof declared !== "string" || !/^sha256:[a-f0-9]{64}$/.test(declared)) fail("module profile hash must be sha256:<64 lowercase hex characters>");
    return declared.slice("sha256:".length);
  }
  return sha256Hex(Buffer.from(canonicalJson(projected), "utf8"));
}

function capacityDeclaration(source, compute) {
  const explicit = source.capacityDeclaration ?? source.capacity_declaration;
  if (explicit !== undefined) {
    if (!["declared", "partial", "not_declared"].includes(explicit)) fail(`unsupported capacity declaration: ${explicit}`);
    return explicit;
  }
  const axes = new Set();
  for (const key of ["hostFloor", "reservation"]) {
    const vector = source[key];
    if (!isObject(vector)) continue;
    const axisKeys = key === "hostFloor"
      ? [["minCpuCores", "cpuCores", "cpu_cores"], ["minRamGB", "ramGB", "ram_gb"], ["minStorageGB", "storageGB", "storage_gb"]]
      : [["cpuCores", "cpu_cores"], ["ramGB", "ram_gb"], ["storageGB", "storage_gb"]];
    for (const aliases of axisKeys) if (aliases.some((axis) => vector[axis] !== undefined)) axes.add(aliases[0]);
  }
  if (!compute && isObject(source.reservation)) return axes.size > 0 ? "declared" : "not_declared";
  return axes.size === 0 ? "not_declared" : axes.size === 3 ? "declared" : "partial";
}

function profileMaturity(source, path) {
  if (!["experimental", "beta", "supported", "deprecated"].includes(source.maturity)) fail(`${path}.maturity must be an explicit profile maturity`);
  return source.maturity;
}

function profileRealization(source, path) {
  if (!["contract-only", "generation-ready", "apply-ready"].includes(source.realization)) fail(`${path}.realization must be an explicit profile realization`);
  return source.realization;
}

function optionalResource(sourceKey, outputKey, source, path) {
  if (source[sourceKey] === undefined) return {};
  const value = source[sourceKey];
  if (!isObject(value)) fail(`${path}.${sourceKey} must be an object`);
  const result = {};
  const aliases = sourceKey === "hostFloor"
    ? [["minCpuCores", "cpu_cores"], ["minRamGB", "ram_gb"], ["minStorageGB", "storage_gb"]]
    : [["cpuCores", "cpu_cores"], ["ramGB", "ram_gb"], ["storageGB", "storage_gb"]];
  const allowedKeys = new Set(aliases.map(([key]) => key).concat([
    "cpu_cores", "ram_gb", "storage_gb",
    ...(sourceKey === "hostFloor" ? ["allowedArchitectures", "allowedVirtualization", "requireInventoryFacts"] : []),
  ]));
  rejectUnknownKeys(value, `${path}.${sourceKey}`, allowedKeys);
  for (const [input, output] of aliases) {
    const valueForAxis = value[input] ?? value[output];
    if (valueForAxis !== undefined) {
      if (typeof valueForAxis !== "number" || !Number.isFinite(valueForAxis) || valueForAxis <= 0) fail(`${path}.${sourceKey}.${input} must be positive`);
      result[output] = valueForAxis;
    }
  }
  if (Object.keys(result).length === 0) {
    if (sourceKey === "hostFloor") return {};
    fail(`${path}.${sourceKey} must declare at least one resource axis`);
  }
  return { [outputKey]: result };
}

function publicStringArray(value, path, allowed) {
  const values = stringArray(value, path, false);
  if (allowed && values.some((entry) => !allowed.includes(entry))) fail(`${path} contains an unsupported value`);
  return values;
}

function publicIdArray(value, path) {
  if (!Array.isArray(value) || value.some((entry) => typeof entry !== "string")) fail(`${path} must be a string array`);
  return [...new Set(value.map((entry) => publicId(entry, path)))].sort();
}

function nativeModuleClosure(definition, workloads, modules) {
  const ids = new Set();
  const workloadIds = nativeWorkloadIds(definition, true);
  for (const workloadId of workloadIds) {
    const workload = workloads.get(workloadId);
    if (!workload) continue;
    for (const alternative of arrayValue(workload.alternatives)) {
      if (typeof alternative.moduleRef === "string" && SAFE_ID.test(alternative.moduleRef)) ids.add(alternative.moduleRef);
    }
  }
  for (const graph of Object.values(definition.computeTierGraphs ?? {})) {
    if (!isObject(graph)) continue;
    for (const value of Object.values(graph.moduleSubstitutions ?? {})) if (typeof value === "string" && SAFE_ID.test(value)) ids.add(value);
    for (const key of Object.keys(graph.moduleSubstitutions ?? {})) if (SAFE_ID.test(key)) ids.add(key);
  }
  // Include typed module dependencies, including plan-only contracts without a
  // resource profile. Those remain visible facts and are never auto-selected.
  const pending = [...ids];
  while (pending.length > 0) {
    const id = pending.pop();
    const module = modules.get(id);
    for (const dependency of module?.requires ?? []) {
      if (typeof dependency === "string" && SAFE_ID.test(dependency) && !ids.has(dependency)) {
        ids.add(dependency);
        pending.push(dependency);
      }
    }
  }
  return ids;
}

function parseNativeUseCases(fitsSource, workloads) {
  const fits = parseUseCases(fitsSource);
  const result = new Map(fits.map((fit) => [fit.id, fit]));
  for (const [id, workload] of workloads) {
    if (!result.has(id)) result.set(id, { id, title: workload.metadata?.displayName ?? workload.metadata?.id ?? id, description: workload.metadata?.description, tiers: {} });
  }
  return result;
}

function nativeUseCasesForKit(definition, useCases, workloads, moduleIds) {
  const ids = nativeWorkloadIds(definition, true);
  const requiredIds = nativeWorkloadIds(definition, false);
  const rows = [];
  for (const id of [...ids].sort()) {
    const workload = workloads.get(id);
    const fit = useCases.get(id);
    if (!workload && !fit) continue;
    const alternatives = [];
    for (const alternative of arrayValue(workload?.alternatives)) {
      if (typeof alternative.moduleRef !== "string" || !moduleIds.has(alternative.moduleRef)) continue;
      const fitForAlternative = matchingFit(fit, alternative.id);
      const functions = fitForAlternative?.functions ?? workload.functionalCapabilities ?? [];
      const load = fitForAlternative?.load ?? { residency: "always-on", baseline: "idle-resident", burst: "interactive" };
      alternatives.push({ alternative_id: publicId(alternative.id, `${id}.alternative`), module_id: publicId(alternative.moduleRef, `${id}.moduleRef`), functions: stringArray(functions, `${id}.${alternative.id}.functions`), load });
    }
    // Native v2 alternatives are selected explicitly and are not gated by the
    // legacy kit-wide compute-tier fit table. The fit document remains useful
    // for functions/load metadata, but a required/default workload such as
    // basement-core has no tier fit and must still expose its CUE alternatives.
    const included = alternatives.length > 0;
    const blockedReason = !included
      ? (fit ? Object.values(fit.tiers).find((entry) => entry.reason)?.reason : undefined)
      : undefined;
    rows.push({
      use_case_id: publicId(id, `${id}.id`),
      title: publicString(fit?.title ?? workload?.metadata?.displayName ?? id, `${id}.title`),
      required: requiredIds.has(id),
      availability: included && alternatives.length > 0 ? "available" : "blocked",
      ...(included && alternatives.length > 0 ? {} : { reason_code: reasonCode(blockedReason) }),
      alternatives,
    });
  }
  return rows;
}

/**
 * Native v2 treats required workloads, policy defaults, and workloads already
 * present in authoring.initialSpec as selected workload intent. Optional policy
 * entries remain discoverable, but are not silently selected by this public
 * projection.
 */
function nativeWorkloadIds(definition, includeOptional) {
  const ids = new Set([
    ...stringArray(definition.workloads?.required ?? [], "definition.workloads.required", false),
    ...stringArray(definition.workloads?.defaults ?? [], "definition.workloads.defaults", false),
    ...nativeInitialWorkloadIds(definition),
  ]);
  if (includeOptional) {
    for (const id of stringArray(definition.workloads?.optional ?? [], "definition.workloads.optional", false)) ids.add(id);
  }
  return ids;
}

function nativeInitialWorkloadIds(definition) {
  const workloads = definition.authoring?.initialSpec?.workloads;
  if (workloads === undefined) return [];
  if (!isObject(workloads)) fail("definition.authoring.initialSpec.workloads must be an object");
  return Object.keys(workloads).map((id) => publicId(id, `definition.authoring.initialSpec.workloads.${id}`));
}

function matchingFit(fit, alternativeId) {
  if (!fit) return undefined;
  for (const tier of Object.values(fit.tiers)) {
    if (tier.included && (tier.alternative_id === alternativeId || tier.module_slug === alternativeId)) return tier;
  }
  return Object.values(fit.tiers).find((tier) => tier.included);
}

function reasonCode(reason) {
  const words = String(reason ?? "USE_CASE_NOT_AVAILABLE").toUpperCase().replace(/[^A-Z0-9]+/g, "_").replace(/^_|_$/g, "");
  return /^[A-Z][A-Z0-9_]{1,63}$/.test(words) ? words : "USE_CASE_NOT_AVAILABLE";
}

export async function projectAuthorityBundle(authorityRootPath, exactSourceSha, plannerPath = "/planner") {
  const root = resolve(authorityRootPath);
  if (!SOURCE_SHA.test(exactSourceSha ?? "")) fail("source SHA must be a full lowercase 40-character commit SHA");
  if (!/^\/planner$/.test(plannerPath)) fail("planner path must be /planner");
  const files = await requiredAuthorityFiles(root);
  const manifest = await readJson(files.manifest);
  await validateManifest(manifest, files, root);
  const catalogSource = await readJson(files.catalog);
  const fitsSource = await readJson(files.computeTierFits);
  const operationsSource = await readJson(files.operations);
  validateDocument(fitsSource, FIT_SCHEMA_VERSIONS, "compute-tier-fits.json");
  validateDocument(operationsSource, OPERATIONS_SCHEMA_VERSIONS, "operations.json");
  verifyContentDigest(fitsSource, "compute-tier-fits.json");
  verifyContentDigest(operationsSource, "operations.json");

  const authorityBundleSha = await authorityBundleDigest(root);
  const modules = indexById(arrayValue(catalogSource.modules), "metadata.id");
  const workloads = indexById(arrayValue(catalogSource.workloads), "metadata.id");
  const useCases = parseUseCases(fitsSource);
  const operations = parseOperations(operationsSource);
  const profiles = [];
  for (const [profileId, profilePath] of Object.entries(manifest.profiles ?? {}).sort(([left], [right]) => left.localeCompare(right))) {
    if (!ID.test(profileId) || typeof profilePath !== "string") fail(`invalid profile identity: ${profileId}`);
    const definition = await readJson(safeBundlePath(root, profilePath));
    profiles.push(projectKit(profileId, definition, useCases, modules, workloads, plannerPath));
  }
  profiles.sort((left, right) => left.stackkit_id.localeCompare(right.stackkit_id));
  const payload = {
    schema_version: SCHEMA_VERSION,
    source_sha: exactSourceSha,
    authority_bundle_sha256: authorityBundleSha,
    kits: profiles,
    operations: operations.sort((left, right) => left.id.localeCompare(right.id)),
  };
  const catalogSha = sha256Hex(Buffer.from(canonicalJson(payload), "utf8"));
  return { ...payload, catalog_sha256: catalogSha };
}

async function requiredAuthorityFiles(root) {
  const manifest = join(root, "manifest.json");
  const catalog = join(root, "catalog.json");
  const computeTierFits = join(root, "compute-tier-fits.json");
  const operations = join(root, "operations.json");
  for (const path of [manifest, catalog, computeTierFits, operations]) {
    try {
      const info = await stat(path);
      if (!info.isFile()) fail(`authority document is not a file: ${path}`);
    } catch {
      fail(`required authority document is missing: ${path}`);
    }
  }
  const definitions = join(root, "definitions");
  try {
    const info = await stat(definitions);
    if (!info.isDirectory()) fail("authority definitions directory is missing");
  } catch {
    fail("authority definitions directory is missing");
  }
  return { manifest, catalog, computeTierFits, operations };
}

async function validateManifest(manifest, files, root) {
  if (!isObject(manifest)) fail("authority manifest is not an object");
  const allowedManifestKeys = new Set(["documents", "documentHashes", "module", "profileScope", "profiles", "schemaVersion", "sourceHashes"]);
  for (const key of Object.keys(manifest)) if (!allowedManifestKeys.has(key)) fail(`authority manifest contains an unknown field: ${key}`);
  if (!isObject(manifest.documents)) fail("authority manifest documents are missing");
  for (const key of Object.keys(manifest.documents)) if (!["catalog", "computeTierFits", "operations"].includes(key)) fail(`authority manifest contains an unknown document: ${key}`);
  if (manifest.schemaVersion !== AUTHORITY_SCHEMA_VERSION) fail("unknown authority bundle schema version");
  if (manifest.module !== "github.com/kombifyio/stackkits") fail("authority bundle module is not the public StackKits module");
  if (manifest.profileScope !== "oss") fail("authority bundle is not an OSS profile");
  if (manifest.documents?.catalog !== basename(files.catalog)) fail("manifest catalog document does not match catalog.json");
  if (manifest.documents?.computeTierFits !== basename(files.computeTierFits)) fail("manifest compute-tier-fits document is missing or misnamed");
  if (manifest.documents?.operations !== basename(files.operations)) fail("manifest operations document is missing or misnamed");
  if (!manifest.documentHashes || typeof manifest.documentHashes !== "object") fail("manifest document hashes are missing");
  const expectedFiles = [["catalog", files.catalog], ["computeTierFits", files.computeTierFits], ["operations", files.operations]];
  for (const [key, path] of expectedFiles) {
    const expected = manifest.documentHashes[key] ?? manifest.documentHashes[basename(path)];
    if (typeof expected !== "string" || !/^sha256:[a-f0-9]{64}$/.test(expected)) fail(`manifest document hash is missing: ${key}`);
    const actual = `sha256:${sha256Hex(await readFile(path))}`;
    if (actual !== expected) fail(`manifest document hash does not match: ${key}`);
  }
  const profiles = manifest.profiles;
  if (!isObject(profiles) || Object.keys(profiles).length === 0) fail("manifest profiles are missing");
  for (const [profileId, profilePath] of Object.entries(profiles)) {
    if (!ID.test(profileId) || typeof profilePath !== "string") fail(`manifest profile path is invalid: ${profileId}`);
    const definitionPath = safeBundlePath(root, profilePath);
    const expected = manifest.documentHashes[profilePath] ?? manifest.documentHashes[basename(definitionPath)] ?? manifest.documentHashes[`definitions/${basename(definitionPath)}`];
    if (typeof expected !== "string" || !/^sha256:[a-f0-9]{64}$/.test(expected)) fail(`manifest document hash is missing: definitions/${profileId}`);
    const actual = `sha256:${sha256Hex(await readFile(definitionPath))}`;
    if (actual !== expected) fail(`manifest document hash does not match: definitions/${profileId}`);
  }
}

function projectKit(profileId, definition, useCases, modules, workloads, plannerPath) {
  if (definition.kind !== "KitDefinition") fail(`profile is not a KitDefinition: ${profileId}`);
  const metadata = objectValue(definition.metadata, `definition metadata for ${profileId}`);
  const stackkitId = stringValue(metadata.slug, `${profileId}.metadata.slug`);
  if (stackkitId !== profileId || !ID.test(stackkitId)) fail(`profile identity mismatch: ${profileId}`);
  const displayName = publicString(metadata.displayName, `${profileId}.metadata.displayName`);
  const version = publicString(metadata.version, `${profileId}.metadata.version`);
  const description = publicString(metadata.description, `${profileId}.metadata.description`);
  const authoring = objectValue(definition.authoring, `${profileId}.authoring`);
  const status = publicString(authoring.initialSpecStatus ?? metadata.status, `${profileId}.status`);
  const graphs = objectValue(definition.computeTierGraphs, `${profileId}.computeTierGraphs`);
  const computeTiers = TIERS.filter((tier) => Object.prototype.hasOwnProperty.call(graphs, tier));
  if (computeTiers.length === 0) fail(`profile declares no compute tiers: ${profileId}`);
  const tiers = {};
  for (const tier of computeTiers) {
    tiers[tier] = projectTier(profileId, tier, objectValue(graphs[tier], `${profileId}.${tier}`), useCases, modules, workloads, definition);
  }
  const requiredAuthoringInputs = stringArray(authoring.requiredOverrides ?? [], `${profileId}.authoring.requiredOverrides`, false).sort();
  return {
    stackkit_id: stackkitId,
    display_name: displayName,
    version,
    description,
    status,
    planner_link: `${plannerPath}?stackkit_id=${stackkitId}`,
    compute_tiers: computeTiers,
    tiers,
    required_authoring_inputs: requiredAuthoringInputs,
  };
}

function stringValue(value, path) {
  if (typeof value !== "string" || value.length === 0) fail(`${path} must be a non-empty string`);
  return value;
}

function projectTier(profileId, tier, graph, useCases, modules, workloads, definition) {
  const sourceRequirements = objectValue(graph.hostRequirements, `${profileId}.${tier}.hostRequirements`);
  const hostRequirements = projectRequirements(sourceRequirements, `${profileId}.${tier}.hostRequirements`);
  const moduleSubstitutions = projectStringMap(graph.moduleSubstitutions ?? {}, `${profileId}.${tier}.moduleSubstitutions`);
  const enableCapabilities = stringArray(graph.enableCapabilities ?? [], `${profileId}.${tier}.enableCapabilities`, false).sort();
  const platformManagement = publicString(graph.platformManagement, `${profileId}.${tier}.platformManagement`);
  const fits = useCases.map((useCase) => projectFit(useCase, tier));
  const moduleIds = moduleClosure(definition, tier, moduleSubstitutions, fits, workloads);
  const moduleRuntimeRequirements = [...moduleIds].sort().map((moduleId) => {
    const module = modules.get(moduleId);
    const rawRequirements = module?.runtimeRequirements;
    if (!rawRequirements || typeof rawRequirements !== "object") return { module_id: moduleId, declaration: "not_declared" };
    return { module_id: moduleId, declaration: "declared", runtime_requirements: projectRuntimeRequirements(rawRequirements, `${profileId}.${tier}.${moduleId}`) };
  });
  return {
    compute_tier: tier,
    host_requirements: hostRequirements,
    platform_management: platformManagement,
    enable_capabilities: enableCapabilities,
    module_substitutions: moduleSubstitutions,
    module_runtime_requirements: moduleRuntimeRequirements,
    use_case_fits: fits,
  };
}

function projectRequirements(source, path) {
  rejectUnknownKeys(source, path, HOST_REQUIREMENT_KEYS);
  const result = {};
  for (const [sourceKey, outputKey] of [["minCpuCores", "min_cpu_cores"], ["minRamGB", "min_ram_gb"], ["minStorageGB", "min_storage_gb"], ["recommendedCpuCores", "recommended_cpu_cores"], ["recommendedRamGB", "recommended_ram_gb"], ["recommendedStorageGB", "recommended_storage_gb"]]) {
    if (source[sourceKey] !== undefined) {
      if (typeof source[sourceKey] !== "number" || !Number.isFinite(source[sourceKey]) || source[sourceKey] <= 0) fail(`${path}.${sourceKey} is not a positive number`);
      result[outputKey] = source[sourceKey];
    }
  }
  if (source.headroomFactor !== undefined && (typeof source.headroomFactor !== "number" || !Number.isFinite(source.headroomFactor) || source.headroomFactor <= 0)) {
    fail(`${path}.headroomFactor is not a positive number`);
  }
  for (const [sourceKey, outputKey] of [["architectures", "architectures"], ["allowedArchitectures", "architectures"], ["virtualization", "virtualization"], ["allowedVirtualization", "virtualization"]]) {
    if (source[sourceKey] !== undefined) {
      const values = stringArray(Array.isArray(source[sourceKey]) ? source[sourceKey] : [source[sourceKey]], `${path}.${sourceKey}`);
      result[outputKey] = [...new Set([...(result[outputKey] ?? []), ...values])].sort();
    }
  }
  if (typeof result.min_cpu_cores !== "number" || typeof result.min_ram_gb !== "number" || typeof result.min_storage_gb !== "number") fail(`${path} omits a required capacity minimum`);
  return result;
}

function projectRuntimeRequirements(source, path) {
  rejectUnknownKeys(source, path, RUNTIME_REQUIREMENT_KEYS);
  const result = {};
  for (const [sourceKey, outputKey] of [["minCpuCores", "min_cpu_cores"], ["minRamGB", "min_ram_gb"], ["minStorageGB", "min_storage_gb"], ["recommendedCpuCores", "recommended_cpu_cores"], ["recommendedRamGB", "recommended_ram_gb"], ["recommendedStorageGB", "recommended_storage_gb"]]) {
    if (source[sourceKey] === undefined) continue;
    if (typeof source[sourceKey] !== "number" || !Number.isFinite(source[sourceKey]) || source[sourceKey] <= 0) fail(`${path}.${sourceKey} is not a positive number`);
    result[outputKey] = source[sourceKey];
  }
  return result;
}

function moduleClosure(definition, tier, substitutions, fits, workloads) {
  const ids = new Set(Object.values(substitutions));
  const declaredWorkloads = new Set(
    stringArray(definition.workloads?.required ?? [], "definition.workloads.required", false),
  );
  for (const fit of fits) {
    if (!fit.included) continue;
    declaredWorkloads.add(fit.use_case_id);
  }
  for (const workloadId of declaredWorkloads) {
    const workload = workloads.get(workloadId);
    if (!workload) continue;
    const tierFit = workload.computeTiers?.[tier];
    const alternativeId = tierFit?.alternativeID;
    const alternatives = arrayValue(workload.alternatives);
    const alternative = alternatives.find((candidate) => candidate.id === alternativeId) ?? alternatives[0];
    if (typeof alternative?.moduleRef === "string" && SAFE_ID.test(alternative.moduleRef)) {
      ids.add(substitutions[alternative.moduleRef] ?? alternative.moduleRef);
    }
  }
  return ids;
}

function parseUseCases(source) {
  const raw = source.useCases ?? source.catalog?.useCases ?? source.fits ?? source.entries;
  const rows = Array.isArray(raw) ? raw : isObject(raw) ? Object.entries(raw).map(([id, value]) => ({ id, ...(isObject(value) ? value : {}) })) : [];
  if (rows.length === 0) fail("compute-tier-fits.json contains no use cases");
  return rows.map((row, index) => {
    const id = row.id ?? row.useCaseId ?? row.use_case_id ?? row.metadata?.id;
    if (typeof id !== "string" || !ID.test(id)) fail(`compute-tier-fits use-case ${index} has an invalid id`);
    const title = typeof row.title === "string" ? publicString(row.title, `use-case ${id}.title`) : typeof row.metadata?.displayName === "string" ? publicString(row.metadata.displayName, `use-case ${id}.metadata.displayName`) : undefined;
    const description = typeof row.description === "string" ? publicString(row.description, `use-case ${id}.description`) : undefined;
    const tierSource = row.computeTiers ?? row.compute_tiers ?? row.tiers ?? {};
    const tiers = {};
    for (const tier of TIERS) {
      const fit = tierSource[tier];
      if (!fit || typeof fit !== "object") {
        tiers[tier] = { included: false, reason: "No declared fit for this compute tier." };
        continue;
      }
      tiers[tier] = projectUseCaseFit(fit, id, tier);
    }
    return { id, title, description, tiers };
  }).sort((left, right) => left.id.localeCompare(right.id));
}

function projectUseCaseFit(source, id, tier) {
  const included = source.included === true;
  if (!included) {
    return { included: false, ...(typeof source.reason === "string" ? { reason: publicString(source.reason, `use-case ${id}.${tier}.reason`) } : { reason: "The authority excludes this use case on this tier." }) };
  }
  const functions = stringArray(source.functions ?? [], `use-case ${id}.${tier}.functions`);
  if (functions.length === 0) fail(`included use-case ${id}.${tier} omits functions`);
  const loadSource = source.load;
  const load = loadSource && typeof loadSource === "object" ? {
    residency: publicString(loadSource.residency, `use-case ${id}.${tier}.load.residency`),
    baseline: publicString(loadSource.baseline, `use-case ${id}.${tier}.load.baseline`),
    burst: publicString(loadSource.burst, `use-case ${id}.${tier}.load.burst`),
  } : undefined;
  if (!load) fail(`included use-case ${id}.${tier} omits load`);
  return {
    included: true,
    functions,
    load,
    ...(typeof source.moduleSlug === "string" ? { module_slug: publicId(source.moduleSlug, `use-case ${id}.${tier}.moduleSlug`) } : {}),
    ...(typeof source.module_slug === "string" ? { module_slug: publicId(source.module_slug, `use-case ${id}.${tier}.module_slug`) } : {}),
    ...(typeof source.alternativeID === "string" ? { alternative_id: publicId(source.alternativeID, `use-case ${id}.${tier}.alternativeID`) } : {}),
    ...(typeof source.alternative_id === "string" ? { alternative_id: publicId(source.alternative_id, `use-case ${id}.${tier}.alternative_id`) } : {}),
    ...(Array.isArray(source.notes) ? { notes: stringArray(source.notes, `use-case ${id}.${tier}.notes`) } : {}),
  };
}

function projectFit(useCase, tier) {
  const fit = useCase.tiers[tier];
  return {
    use_case_id: useCase.id,
    ...(useCase.title ? { title: useCase.title } : {}),
    included: fit.included,
    ...(fit.functions ? { functions: [...fit.functions] } : {}),
    ...(fit.load ? { load: { ...fit.load } } : {}),
    ...(fit.module_slug ? { module_slug: fit.module_slug } : {}),
    ...(fit.alternative_id ? { alternative_id: fit.alternative_id } : {}),
    ...(fit.reason ? { reason: fit.reason } : {}),
    ...(fit.notes ? { notes: [...fit.notes] } : {}),
  };
}

function parseOperations(source) {
  const raw = source.operations ?? source.registry ?? source.entries ?? (Array.isArray(source) ? source : undefined);
  if (!Array.isArray(raw) || raw.length === 0) fail("operations.json contains no operation metadata");
  const ids = new Set();
  const result = raw.map((row, index) => {
    if (!isObject(row)) fail(`operations.json operation ${index} is not an object`);
    rejectUnknownKeys(row, `operations.${index}`, new Set(["command", "description", "destructive", "id", "idempotent", "mutation", "ownerApproval", "owner_approval", "title", "toolName", "tool_name"]));
    const id = row.id;
    const toolName = aliasValue(row.toolName, row.tool_name, `operations.${index}.toolName`);
    if (typeof id !== "string" || !OPERATION_ID.test(id)) fail(`operations.json operation ${index} has an invalid id`);
    if (ids.has(id)) fail(`operations.json contains duplicate operation id: ${id}`);
    ids.add(id);
    if (typeof toolName !== "string" || !/^stackkit_[a-z0-9_]+$/.test(toolName)) fail(`operations.json operation ${id} has an invalid tool name`);
    const command = row.command;
    if (!Array.isArray(command) || command.length === 0 || command.some((entry) => typeof entry !== "string" || entry.length === 0 || URL_VALUE.test(entry) || hasSensitiveReference(entry))) fail(`operations.json operation ${id} has an unsafe command`);
    const mutation = requiredBoolean(row.mutation, undefined, `operations.${id}.mutation`);
    const destructive = requiredBoolean(row.destructive, undefined, `operations.${id}.destructive`);
    const idempotent = requiredBoolean(row.idempotent, undefined, `operations.${id}.idempotent`);
    const ownerApproval = requiredBoolean(row.ownerApproval, row.owner_approval, `operations.${id}.ownerApproval`);
    return {
      id,
      tool_name: toolName,
      title: publicString(row.title, `operations.${id}.title`),
      description: publicString(row.description, `operations.${id}.description`),
      command: command.map((entry) => publicString(entry, `operations.${id}.command`)),
      mutation,
      destructive,
      idempotent,
      owner_approval: ownerApproval,
    };
  });
  for (const required of REQUIRED_OPERATIONS) if (!ids.has(required)) fail(`operations.json omits ${required}`);
  return result;
}

function verifyContentDigest(document, name) {
  if (typeof document.contentDigest !== "string") fail(`${name} omits contentDigest`);
  const expected = `sha256:${sha256Hex(Buffer.from(canonicalJson(withoutKey(document, "contentDigest")), "utf8"))}`;
  if (document.contentDigest !== expected) fail(`${name} contentDigest does not match its content`);
}

function validateDocument(document, acceptedVersions, name) {
  if (!isObject(document) || typeof document.schemaVersion !== "string" || !acceptedVersions.has(document.schemaVersion)) fail(`${name} has an unknown schema version`);
}

async function authorityBundleDigest(root) {
  const entries = [];
  await walk(root, root, entries);
  entries.sort((left, right) => left.path.localeCompare(right.path));
  const payload = entries.map((entry) => `${entry.path}\0${sha256Hex(entry.bytes)}\n`).join("");
  return sha256Hex(Buffer.from(payload, "utf8"));
}

async function walk(root, current, entries) {
  const children = await readdir(current, { withFileTypes: true });
  for (const child of children.sort((left, right) => left.name.localeCompare(right.name))) {
    const path = join(current, child.name);
    if (child.isSymbolicLink()) fail(`authority bundle must not contain symlinks: ${path}`);
    if (child.isDirectory()) await walk(root, path, entries);
    else if (child.isFile()) entries.push({ path: relative(root, path).split(sep).join("/"), bytes: await readFile(path) });
    else fail(`unsupported authority bundle entry: ${path}`);
  }
}

async function readJson(path) {
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch (error) {
    fail(`invalid authority JSON: ${path} (${error instanceof Error ? error.message : "unknown error"})`);
  }
}

function safeBundlePath(root, requested) {
  const candidate = resolve(root, requested);
  const prefix = `${root}${sep}`;
  if (candidate !== root && !candidate.startsWith(prefix)) fail(`authority path escapes the bundle: ${requested}`);
  if (basename(candidate) !== requested.split(/[\\/]/).pop()) fail(`authority profile path is invalid: ${requested}`);
  return candidate;
}

function indexById(rows, path) {
  const result = new Map();
  for (const row of rows) {
    const id = path.split(".").reduce((value, key) => value?.[key], row);
    if (typeof id === "string") result.set(id, row);
  }
  return result;
}

function objectValue(value, path) {
  if (!isObject(value)) fail(`${path} must be an object`);
  return value;
}

function arrayValue(value) {
  return Array.isArray(value) ? value : [];
}

function stringArray(value, path, requireValues = true) {
  if (!Array.isArray(value) || value.some((entry) => typeof entry !== "string")) fail(`${path} must be a string array`);
  const result = value.map((entry) => publicString(entry, path));
  if (requireValues && result.length === 0) fail(`${path} must not be empty`);
  return [...new Set(result)].sort();
}

function projectStringMap(value, path) {
  if (!isObject(value)) fail(`${path} must be an object`);
  const result = {};
  for (const [key, target] of Object.entries(value)) {
    publicId(key, `${path}.${key}`);
    result[key] = publicId(target, `${path}.${key}`);
  }
  return Object.fromEntries(Object.entries(result).sort(([left], [right]) => left.localeCompare(right)));
}

function publicId(value, path) {
  if (typeof value !== "string" || !SAFE_ID.test(value) || hasSensitiveReference(value) || URL_VALUE.test(value)) fail(`${path} is not a public identifier`);
  return value;
}

function publicString(value, path) {
  const field = path.split(".").at(-1) ?? path;
  if (typeof value !== "string" || value.length === 0 || value.length > 500 || hasSensitiveReference(value) || PRIVATE_KEY.test(field)) fail(`${path} is not a public string`);
  return value;
}

function hasSensitiveReference(value) {
  return typeof value === "string" && SENSITIVE_REFERENCE.test(value);
}

function rejectUnknownKeys(value, path, allowed) {
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) fail(`${path}.${key} is not supported by the public projection`);
  }
}

function aliasValue(primary, alias, path) {
  if (primary !== undefined && alias !== undefined && primary !== alias) fail(`${path} aliases disagree`);
  return primary ?? alias;
}

function requiredBoolean(primary, alias, path) {
  const value = aliasValue(primary, alias, path);
  if (typeof value !== "boolean") fail(`${path} must be an explicit boolean`);
  return value;
}

function withoutKey(value, key) {
  const clone = { ...value };
  delete clone[key];
  return clone;
}

function canonicalJson(value) {
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  if (isObject(value)) return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(",")}}`;
  return JSON.stringify(value);
}

function sha256Hex(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function parseArgs(values) {
  const result = {};
  for (let index = 0; index < values.length; index += 1) {
    const value = values[index];
    if (!value.startsWith("--")) fail(`unknown argument: ${value}`);
    const key = value.slice(2);
    const next = values[index + 1];
    if (!next || next.startsWith("--")) fail(`argument requires a value: ${value}`);
    result[key] = next;
    index += 1;
  }
  return result;
}

async function resolveSourceSha(explicit) {
  const candidate = explicit ?? process.env.STACKKITS_BUILD_SOURCE_SHA;
  if (candidate) {
    if (!SOURCE_SHA.test(candidate)) fail("source SHA must be a full lowercase 40-character commit SHA");
    return candidate;
  }
  try {
    const { stdout } = await execFileAsync("git", ["rev-parse", "HEAD"], { cwd: process.cwd() });
    const sha = stdout.trim();
    if (!SOURCE_SHA.test(sha)) fail("current Git commit is not a full lowercase SHA");
    return sha;
  } catch {
    fail("source SHA is required outside a Git checkout");
  }
}

function fail(message) {
  throw new Error(`StackKits WebMCP catalog generation failed: ${message}`);
}
