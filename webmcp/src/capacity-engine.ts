import type { CapacityStatus } from "./types.js";

export interface CapacityVector {
  cpuCores?: number;
  ramGB?: number;
  storageGB?: number;
}

export interface CapacityEngineInput {
  modules: Array<{
    id: string;
    capacityDeclaration: string;
    nodeRefs: string[];
    compute: { hostFloor: CapacityVector; reservation: CapacityVector; recommended: CapacityVector; headroom: CapacityVector };
    orthogonalReservations: CapacityVector[];
  }>;
  capacities: Array<{ nodeRef: string; origin: "declared"; values: CapacityVector }>;
}

export interface CapacityEngineResult {
  schemaVersion: "stackkit.capacity-assessment/v1";
  overall: CapacityStatus;
  nodes: Array<{
    nodeRef: string;
    origin: string;
    overall: CapacityStatus;
    demand: { unverifiedModuleRefs: string[] };
    checks: Array<{
      axis: "cpuCores" | "ramGB" | "storageGB";
      observed?: number;
      minimum?: number;
      recommended?: number;
      status: CapacityStatus;
      reasonCode: string;
    }>;
  }>;
}

interface CapacityBridge {
  assess(input: string): string;
  overall(input: string): string;
}
interface GoRuntime {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}
type CapacityGlobal = typeof globalThis & { Go?: new () => GoRuntime; __stackkitsCapacityFit?: CapacityBridge };
const engines = new Map<string, Promise<CapacityBridge>>();
let activeBridge: CapacityBridge | undefined;

function assetURLs(baseURL?: string | URL): { wasm: URL; runtime: URL } {
  if (baseURL) {
    const base = new URL(baseURL, typeof document === "undefined" ? import.meta.url : document.baseURI);
    if (!base.pathname.endsWith("/")) base.pathname += "/";
    return { wasm: new URL("capacityfit.wasm", base), runtime: new URL("wasm_exec.js", base) };
  }
  return { wasm: new URL("../assets/capacityfit.wasm", import.meta.url), runtime: new URL("../assets/wasm_exec.js", import.meta.url) };
}

// Assets are loaded once from the host/package, never from a capacity API or CDN.
// The optional base URL lets library consumers keep their own static base path.
export function loadCapacityEngine(assetBaseURL?: string | URL): Promise<CapacityBridge> {
  const assets = assetURLs(assetBaseURL);
  const existing = engines.get(assets.wasm.href);
  if (existing) return existing;
  const loading = (async () => {
    if ([assets.runtime, assets.wasm].some((url) => url.username !== "" || url.password !== "") || (typeof location !== "undefined" && [assets.runtime, assets.wasm].some((url) => url.origin !== location.origin))) {
      throw new Error("capacity assets must be same-origin");
    }
    await import(/* @vite-ignore */ assets.runtime.href);
    const global = globalThis as CapacityGlobal;
    if (!global.Go) throw new Error("the matching Go browser runtime is unavailable");
    let bytes: Uint8Array<ArrayBuffer>;
    if (assets.wasm.protocol === "file:" && typeof document === "undefined") {
      // Local Node consumers and probes read their packaged static bytes.
      const moduleName = "node:fs/promises";
      const fs = await import(/* @vite-ignore */ moduleName) as { readFile(url: URL): Promise<Uint8Array> };
      bytes = new Uint8Array(await fs.readFile(assets.wasm));
    } else {
      const response = await fetch(assets.wasm);
      if (!response.ok) throw new Error("the static capacity engine is unavailable");
      bytes = new Uint8Array(await response.arrayBuffer());
    }
    const runtime = new global.Go();
    const module = await WebAssembly.compile(bytes);
    const instance = await WebAssembly.instantiate(module, runtime.importObject);
    void runtime.run(instance).catch(() => { activeBridge = undefined; });
    const bridge = global.__stackkitsCapacityFit;
    if (!bridge) throw new Error("the shared capacity engine did not initialize");
    activeBridge = bridge;
    return bridge;
  })();
  engines.set(assets.wasm.href, loading);
  // Registration/discovery can still work if static assets are unavailable.
  void loading.catch(() => {});
  return loading;
}

export async function assessWithCapacityEngine(input: CapacityEngineInput, assetBaseURL?: string | URL): Promise<CapacityEngineResult> {
  const bridge = await loadCapacityEngine(assetBaseURL);
  const result = JSON.parse(bridge.assess(JSON.stringify(input))) as CapacityEngineResult & { error?: string };
  if (result.error || result.schemaVersion !== "stackkit.capacity-assessment/v1") throw new Error("the shared capacity engine rejected the assessment");
  return result;
}

// Compatibility export delegates verdict precedence to Go once initialized.
export function capacityOverall(statuses: CapacityStatus[]): CapacityStatus {
  if (!activeBridge) throw new Error("the shared capacity engine is not initialized");
  const result = JSON.parse(activeBridge.overall(JSON.stringify(statuses))) as { overall?: CapacityStatus; error?: string };
  if (!result.overall || result.error) throw new Error("the shared capacity engine rejected the verdicts");
  return result.overall;
}
