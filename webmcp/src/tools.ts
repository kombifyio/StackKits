import type { PlannerSession } from "./session.js";
import { TOOL_INPUT_SCHEMAS_PUBLIC } from "./schema.js";
import type {
  AssessCapacityInput,
  GetTierProfileInput,
  ListCatalogInput,
  PrepareHandoffInput,
  ToolInputMap,
  ToolName,
  ToolResultMap,
  WebMcpResult,
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

export function stackkitsListCatalog(session: PlannerSession, input: ListCatalogInput = {}, signal?: AbortSignal): Promise<ToolResultMap["stackkits_list_catalog"]> {
  return session.invoke("stackkits_list_catalog", input, signal);
}

export function stackkitsGetTierProfile(session: PlannerSession, input: GetTierProfileInput, signal?: AbortSignal): Promise<ToolResultMap["stackkits_get_tier_profile"]> {
  return session.invoke("stackkits_get_tier_profile", input, signal);
}

export function stackkitsAssessCapacity(session: PlannerSession, input: AssessCapacityInput, signal?: AbortSignal): Promise<ToolResultMap["stackkits_assess_capacity"]> {
  return session.invoke("stackkits_assess_capacity", input, signal);
}

export function stackkitsPrepareHandoff(session: PlannerSession, input: PrepareHandoffInput, signal?: AbortSignal): Promise<ToolResultMap["stackkits_prepare_handoff"]> {
  return session.invoke("stackkits_prepare_handoff", input, signal);
}

export function createToolDefinitions(session: PlannerSession): {
  [T in ToolName]: WebMcpToolDefinition<T>;
}[ToolName][] {
  return [
    {
      name: "stackkits_list_catalog",
      title: "List StackKits",
      description: "List public StackKits and declared compute tiers. Choose explicitly; this tool never recommends.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_list_catalog,
      annotations: readOnlyAnnotations(false),
      execute: (input, context) => stackkitsListCatalog(session, input, context?.signal),
    },
    {
      name: "stackkits_get_tier_profile",
      title: "Get tier profile",
      description: "Read a selected StackKit tier's requirements, graph deltas, modules, and use-case fits.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_get_tier_profile,
      annotations: readOnlyAnnotations(false),
      execute: (input, context) => stackkitsGetTierProfile(session, input, context?.signal),
    },
    {
      name: "stackkits_assess_capacity",
      title: "Assess declared capacity",
      description: "Compare declared CPU, RAM, and free storage with one selected tier's minimum and recommendation.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_assess_capacity,
      annotations: readOnlyAnnotations(true),
      execute: (input, context) => stackkitsAssessCapacity(session, input, context?.signal),
    },
    {
      name: "stackkits_prepare_handoff",
      title: "Prepare StackKit handoff",
      description: "Prepare a validated, non-executing CLI handoff for an explicit StackKit, tier, capacity, and authoring input.",
      inputSchema: TOOL_INPUT_SCHEMAS_PUBLIC.stackkits_prepare_handoff,
      annotations: readOnlyAnnotations(true),
      execute: (input, context) => stackkitsPrepareHandoff(session, input, context?.signal),
    },
  ];
}

export const TOOL_NAMES: readonly ToolName[] = [
  "stackkits_list_catalog",
  "stackkits_get_tier_profile",
  "stackkits_assess_capacity",
  "stackkits_prepare_handoff",
];

function readOnlyAnnotations(untrustedContentHint: boolean): WebMcpToolDefinition["annotations"] {
  return {
    readOnlyHint: true,
    untrustedContentHint,
  };
}
