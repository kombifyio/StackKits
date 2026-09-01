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
  const outputPath = resolve(args.out ?? join(scriptRoot, "..", "data", "stackkits-webmcp", "catalog.json"));
  const sourceSha = await resolveSourceSha(args["source-sha"]);
  const catalog = await projectAuthorityBundle(authorityRoot, sourceSha, args["planner-path"] ?? "/planner");
  await mkdir(dirname(outputPath), { recursive: true });
  await writeFile(outputPath, `${JSON.stringify(catalog, null, 2)}\n`, "utf8");
  process.stdout.write(`${outputPath}\n`);
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
