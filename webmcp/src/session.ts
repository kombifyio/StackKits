import { createPlanner, type PlannerService } from "./planner.js";
import { abortedResult } from "./result.js";
import type {
  CapacityData,
  DeclaredCapacity,
  Selection,
  TierProfileData,
  ToolInputMap,
  ToolName,
  ToolResultMap,
  WebMcpCatalog,
  Notice,
} from "./types.js";

export interface PlannerState {
  selection?: Selection;
  declared_capacity?: Partial<DeclaredCapacity>;
  tier_profile?: TierProfileData;
  capacity?: CapacityData;
  handoff?: ToolResultMap["stackkits_prepare_handoff"]["data"];
  last_result?: {
    tool: ToolName;
    outcome: ToolResultMap[ToolName]["outcome"];
    notices: Notice[];
  };
}

export type PlannerStateListener = (state: PlannerState) => void;

/** A shared state boundary for human controls and page-side agent calls. */
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

  async invoke<T extends ToolName>(tool: T, input: ToolInputMap[T], signal?: AbortSignal): Promise<ToolResultMap[T]> {
    const result = await this.service.invoke(tool, input, { signal });
    if (signal?.aborted) return abortedResult(tool, this.service.catalog) as ToolResultMap[T];
    await this.applyResult(tool, input, result, signal);
    if (signal?.aborted) return abortedResult(tool, this.service.catalog) as ToolResultMap[T];
    return result;
  }

  setSelection(selection: Selection): void {
    const sameProfile = this.current.selection?.stackkit_id === selection.stackkit_id
      && this.current.selection?.compute_tier === selection.compute_tier;
    this.current = {
      selection: clone(selection),
      ...(sameProfile && this.current.tier_profile ? { tier_profile: clone(this.current.tier_profile) } : {}),
      ...(sameProfile && this.current.declared_capacity ? { declared_capacity: clone(this.current.declared_capacity) } : {}),
      ...(sameProfile && this.current.capacity ? { capacity: clone(this.current.capacity) } : {}),
    };
    this.emit();
  }

  setCapacity(capacity: Partial<DeclaredCapacity>): void {
    this.current = { ...this.current, declared_capacity: clone(capacity), capacity: undefined, handoff: undefined };
    this.emit();
  }

  private async applyResult<T extends ToolName>(tool: T, input: ToolInputMap[T], result: ToolResultMap[T], signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) return;
    const selectionChanged = result.selection
      && (result.selection.stackkit_id !== this.current.selection?.stackkit_id
        || result.selection.compute_tier !== this.current.selection?.compute_tier);
    const next: PlannerState = {
      ...(selectionChanged ? {} : this.current),
      last_result: { tool, outcome: result.outcome, notices: clone(result.notices) },
    };
    if (result.selection) next.selection = clone(result.selection);
    if (tool === "stackkits_get_tier_profile" && result.outcome === "success") {
      next.tier_profile = clone(result.data as ToolResultMap["stackkits_get_tier_profile"]["data"]);
      next.handoff = undefined;
    }
    if (tool === "stackkits_assess_capacity" && (result.outcome === "success" || result.outcome === "blocked")) {
      next.capacity = clone(result.data as ToolResultMap["stackkits_assess_capacity"]["data"]);
      next.declared_capacity = clone((result.data as ToolResultMap["stackkits_assess_capacity"]["data"]).declared_capacity);
    }
    if (tool === "stackkits_assess_capacity") next.handoff = undefined;
    if (tool === "stackkits_prepare_handoff") {
      next.handoff = undefined;
      const handoff = result.data as ToolResultMap["stackkits_prepare_handoff"]["data"];
      if (!isHandoffData(handoff)) {
        if (!signal?.aborted) {
          this.current = next;
          this.emit();
        }
        return;
      }
      next.handoff = clone(handoff);
      const handoffInput = input as ToolInputMap["stackkits_prepare_handoff"];
      const capacityResult = await this.service.assessCapacity({
        stackkit_id: handoffInput.stackkit_id,
        compute_tier: handoffInput.compute_tier,
        declared_capacity: handoffInput.declared_capacity,
      }, { signal });
      if (signal?.aborted) return;
      if (capacityResult.outcome === "success") {
        next.capacity = clone(capacityResult.data);
        next.declared_capacity = clone(capacityResult.data.declared_capacity);
      }
    }
    if (signal?.aborted) return;
    this.current = next;
    this.emit();
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
  if (!sharedSession || catalog !== undefined && sharedSession.service.catalog !== catalog) {
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

function isHandoffData(value: unknown): value is ToolResultMap["stackkits_prepare_handoff"]["data"] {
  return value !== null && typeof value === "object" && "ready" in value && "steps" in value && "apply_follow_up" in value;
}
