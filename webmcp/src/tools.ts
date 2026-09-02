import type { PlannerSession } from "./session.js";
import { TOOL_INPUT_SCHEMAS_PUBLIC } from "./schema.js";
import type {
  AssessCapacityInput,
  GetModuleProfilesInput,
  ListCatalogInput,
  PrepareHandoffInput,
  ToolInputMap,
  ToolName,
  ToolResultMap,
} from "./types.js";

export interface WebMcpToolExecutionContext {
  signal?: AbortSignal;
}

export interface WebMcpToolDefinition<T extends ToolName = ToolName> {
  name: T;
  title: string;
  description: string;
  inputSchema: Record<string, unknown>;
  annotations: {
    readOnlyHint: true;
    untrustedContentHint: boolean;
  };
  execute: (input: ToolInputMap[T], context?: WebMcpToolExecutionContext) => Promise<ToolResultMap[T]>;
}

export function stackkitsListCatalog(
  session: PlannerSession,
  input: ListCatalogInput = {},
  signal?: AbortSignal,
): Promise<ToolResultMap["stackkits_list_catalog"]> {
  return session.invoke("stackkits_list_catalog", input, signal);
}

export function stackkitsGetModuleProfiles(
  session: PlannerSession,
  input: GetModuleProfilesInput,
  signal?: AbortSignal,
): Promise<ToolResultMap["stackkits_get_module_profiles"]> {
  return session.invoke("stackkits_get_module_profiles", input, signal);
}

export function stackkitsAssessCapacity(
  session: PlannerSession,
  input: AssessCapacityInput,
  signal?: AbortSignal,
): Promise<ToolResultMap["stackkits_assess_capacity"]> {
  return session.invoke("stackkits_assess_capacity", input, signal);
}

export function stackkitsPrepareHandoff(
  session: PlannerSession,
  input: PrepareHandoffInput,
  signal?: AbortSignal,
): Promise<ToolResultMap["stackkits_prepare_handoff"]> {
  return session.invoke("stackkits_prepare_handoff", input, signal);
}

export function createToolDefinitions(session: PlannerSession): {
  [T in ToolName]: WebMcpToolDefinition<T>;
}[ToolName][] {
  return [
    {
      name: "stackkits_list_catalog",
      title: "List StackKits",
      description: "List public StackKits and module-profile availability. This tool never recommends a configuration.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_list_catalog,
      annotations: readOnlyAnnotations(false),
      execute: (input, context) => stackkitsListCatalog(session, input, context?.signal),
    },
    {
      name: "stackkits_get_module_profiles",
      title: "Get module profiles",
      description: "Read one module and its use-case alternatives per page. Follow next_cursor with the same filters, or select module_id. No profile is selected.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_get_module_profiles,
      annotations: readOnlyAnnotations(false),
      execute: (input, context) => stackkitsGetModuleProfiles(session, input, context?.signal),
    },
    {
      name: "stackkits_assess_capacity",
      title: "Assess declared capacity",
      description: "Compare declared CPU, RAM, and storage with an explicit set of module-local profiles.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_assess_capacity,
      annotations: readOnlyAnnotations(true),
      execute: (input, context) => stackkitsAssessCapacity(session, input, context?.signal),
    },
    {
      name: "stackkits_prepare_handoff",
      title: "Prepare StackKit handoff",
      description: "Prepare a non-executing CLI handoff. Steps are [operation_id, argv, mutation, idempotent, owner_approval]. Apply is a non-executable follow-up. Requires explicit module profiles, use-case alternatives, capacity, and authoring input.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_prepare_handoff,
      annotations: readOnlyAnnotations(true),
      execute: (input, context) => stackkitsPrepareHandoff(session, input, context?.signal),
    },
  ];
}

export const TOOL_NAMES: readonly ToolName[] = [
  "stackkits_list_catalog",
  "stackkits_get_module_profiles",
  "stackkits_assess_capacity",
  "stackkits_prepare_handoff",
];

function readOnlyAnnotations(untrustedContentHint: boolean): WebMcpToolDefinition["annotations"] {
  return { readOnlyHint: true, untrustedContentHint };
}
