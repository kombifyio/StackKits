import { createPlanner, type PlannerService } from "./planner.js";
import { abortedResult } from "./result.js";
import type {
  AssessCapacityInput,
  CapacityData,
  ModuleProfilesData,
  Notice,
  PartialDeclaredCapacity,
  Selection,
  ToolInputMap,
  ToolName,
  ToolResultMap,
} from "./types.js";

export interface PlannerState {
  selection?: Selection;
  declared_capacity?: PartialDeclaredCapacity;
  module_profiles?: ModuleProfilesData;
  capacity?: CapacityData;
  handoff?: ToolResultMap["stackkits_prepare_handoff"]["data"];
  last_result?: {
    tool: ToolName;
    outcome: ToolResultMap[ToolName]["outcome"];
    notices: Notice[];
  };
}

export type PlannerStateListener = (state: PlannerState) => void;

/** One shared state boundary for human controls and browser-agent calls. */
export class PlannerSession {
  readonly service: PlannerService;
  private current: PlannerState = {};
  private listeners = new Set<PlannerStateListener>();

  constructor(service: PlannerService) {
    this.service = service;
  }

  get state(): PlannerState {
    return clone(this.current);
  }

  subscribe(listener: PlannerStateListener): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  async invoke<T extends ToolName>(
    tool: T,
    input: ToolInputMap[T],
    signal?: AbortSignal,
  ): Promise<ToolResultMap[T]> {
    const result = await this.service.invoke(tool, input, { signal });
    if (signal?.aborted) {
      return abortedResult(tool, this.service.catalog) as ToolResultMap[T];
    }
    if (!await this.applyResult(tool, input, result, signal)) {
      return abortedResult(tool, this.service.catalog) as ToolResultMap[T];
    }
    return result;
  }

  setSelection(selection: Selection): void {
    const same = sameJson(this.current.selection, selection);
    this.current = {
      selection: clone(selection),
      ...(same && this.current.module_profiles ? { module_profiles: clone(this.current.module_profiles) } : {}),
      ...(same && this.current.declared_capacity ? { declared_capacity: clone(this.current.declared_capacity) } : {}),
      ...(same && this.current.capacity ? { capacity: clone(this.current.capacity) } : {}),
    };
    this.emit();
  }

  setCapacity(capacity: PartialDeclaredCapacity): void {
    this.current = {
      ...this.current,
      declared_capacity: clone(capacity),
      capacity: undefined,
      handoff: undefined,
    };
    this.emit();
  }

  private async applyResult<T extends ToolName>(
    tool: T,
    input: ToolInputMap[T],
    result: ToolResultMap[T],
    signal?: AbortSignal,
  ): Promise<boolean> {
    if (signal?.aborted) return false;
    const resultStackkitId = result.selection?.stackkit_id;
    const currentStackkitId = this.current.selection?.stackkit_id;
    const preserveExistingSelection = tool === "stackkits_get_module_profiles"
      && (!resultStackkitId || !currentStackkitId || resultStackkitId === currentStackkitId);
    const selectionChanged = result.selection && !sameJson(result.selection, this.current.selection);
    const next: PlannerState = {
      ...(selectionChanged && !preserveExistingSelection ? {} : this.current),
      last_result: { tool, outcome: result.outcome, notices: clone(result.notices) },
    };
    if (result.selection) {
      next.selection = preserveExistingSelection
        ? { ...(this.current.selection ?? {}), ...clone(result.selection) }
        : clone(result.selection);
    }
    if (tool === "stackkits_get_module_profiles" && result.outcome === "success") {
      next.module_profiles = clone(result.data as ToolResultMap["stackkits_get_module_profiles"]["data"]);
      next.handoff = undefined;
    }
    if (tool === "stackkits_assess_capacity" && result.outcome === "success") {
      const capacity = result.data as ToolResultMap["stackkits_assess_capacity"]["data"];
      next.selection = validatedInputSelection(input as AssessCapacityInput);
      next.capacity = clone(capacity);
      next.declared_capacity = clone(capacity.declared_capacity);
      next.handoff = undefined;
    }
    if (tool === "stackkits_prepare_handoff") {
      const handoff = result.data as ToolResultMap["stackkits_prepare_handoff"]["data"];
      if (isHandoffData(handoff)) next.handoff = clone(handoff);
      const handoffInput = input as ToolInputMap["stackkits_prepare_handoff"];
      const capacityResult = await this.service.assessCapacity({
        stackkit_id: handoffInput.stackkit_id,
        module_profiles: handoffInput.module_profiles,
        ...(handoffInput.use_cases ? { use_cases: handoffInput.use_cases } : {}),
        declared_capacity: handoffInput.declared_capacity,
      }, { signal });
      if (signal?.aborted) return false;
      if (capacityResult.outcome === "success") {
        next.selection = validatedInputSelection(handoffInput);
        next.capacity = clone(capacityResult.data);
        next.declared_capacity = clone(capacityResult.data.declared_capacity);
      }
    }
    if (signal?.aborted) return false;
    // Commit once, after all asynchronous validation. Aborting before this
    // boundary cannot undo edits made by another caller while we were waiting.
    this.current = next;
    this.emit();
    return true;
  }

  private emit(): void {
    const state = this.state;
    for (const listener of this.listeners) listener(state);
  }
}

let sharedSession: PlannerSession | undefined;

export function createPlannerSession(catalog: unknown, options: { sourceSha?: string } = {}): PlannerSession {
  return new PlannerSession(createPlanner(catalog, options));
}

export function getSharedPlannerSession(catalog?: unknown, options: { sourceSha?: string } = {}): PlannerSession {
  if (!sharedSession || (catalog !== undefined && sharedSession.service.catalog !== catalog)) {
    sharedSession = createPlannerSession(catalog, options);
  }
  return sharedSession;
}

export function setSharedPlannerSession(session: PlannerSession): PlannerSession {
  sharedSession = session;
  return session;
}

export function resetSharedPlannerSession(): void {
  sharedSession = undefined;
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function sameJson(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

/** Inputs reach this projection only after the service has validated their selection. */
function validatedInputSelection(input: AssessCapacityInput): Selection {
  return {
    stackkit_id: input.stackkit_id,
    module_profiles: clone(input.module_profiles).sort((left, right) => left.module_id.localeCompare(right.module_id)),
    ...(input.use_cases?.length ? {
      use_cases: clone(input.use_cases).sort((left, right) => left.use_case_id.localeCompare(right.use_case_id)),
    } : {}),
  };
}

function isHandoffData(value: unknown): value is ToolResultMap["stackkits_prepare_handoff"]["data"] {
  return value !== null && typeof value === "object" && "ready" in value && "steps" in value && "apply_follow_up" in value;
}
