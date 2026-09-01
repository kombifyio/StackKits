import { SCHEMA_VERSION } from "./types.js";
import type { CatalogValidation } from "./catalog.js";
import type { Effects, Notice, Outcome, Provenance, Selection, ToolName, WebMcpCatalog, WebMcpResult } from "./types.js";
import { validateToolResult } from "./schema.js";

export const ZERO_SOURCE_SHA = "0000000000000000000000000000000000000000";
export const ZERO_DIGEST = "0000000000000000000000000000000000000000000000000000000000000000";

export const NO_EFFECTS: Effects = Object.freeze({
  executed: false,
  target_mutation: false,
  provider_action: false,
});

export function provenanceFor(catalog?: WebMcpCatalog): Provenance {
  return {
    authority: "cue",
    source_sha: catalog?.source_sha ?? ZERO_SOURCE_SHA,
    authority_bundle_sha256: catalog?.authority_bundle_sha256 ?? ZERO_DIGEST,
    catalog_sha256: catalog?.catalog_sha256 ?? ZERO_DIGEST,
  };
}

export function makeResult<TData>(
  tool: ToolName,
  outcome: Outcome,
  data: TData,
  catalog?: WebMcpCatalog,
  notices: Notice[] = [],
  selection?: Selection,
): WebMcpResult<TData> {
  const result: WebMcpResult<TData> = {
    schema_version: SCHEMA_VERSION,
    tool,
    outcome,
    provenance: provenanceFor(catalog),
    ...(selection ? { selection } : {}),
    data,
    notices,
    effects: { ...NO_EFFECTS },
  };
  const validation = validateToolResult(tool, result as WebMcpResult<unknown>);
  if (!validation.valid) throw new Error(`StackKits WebMCP result violates its public schema at ${validation.issues[0]?.path ?? "$"}`);
  return result;
}

export function makeNotice(code: string, severity: Notice["severity"], message: string, field?: string): Notice {
  return {
    code: /^[A-Z][A-Z0-9_]{1,63}$/.test(code) ? code : "INTERNAL_CONTRACT_ERROR",
    severity,
    ...(field ? { field: field.slice(0, 150) } : {}),
    message: message.slice(0, 500),
  };
}

export function authorityFailure<TData>(tool: ToolName, validation?: CatalogValidation): WebMcpResult<TData> {
  void validation;
  return makeResult(tool, "blocked", {} as TData, undefined, [makeNotice("AUTHORITY_INTEGRITY_FAILED", "error", "The public authority catalog failed integrity validation.")]);
}

export function abortedResult<TData>(tool: ToolName, catalog?: WebMcpCatalog): WebMcpResult<TData> {
  return makeResult(tool, "blocked", {} as TData, catalog, [makeNotice("ABORTED", "info", "The tool execution was cancelled.")]);
}
