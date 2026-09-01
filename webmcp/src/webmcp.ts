/// <reference types="webmcp-types" />

import { loadCatalog, validateAndVerifyCatalog } from "./catalog.js";
import { getSharedPlannerSession, setSharedPlannerSession, type PlannerSession } from "./session.js";
import { createToolDefinitions } from "./tools.js";
import type { WebMcpCatalog } from "./types.js";

export type ModelContextTool = WebMCP.ModelContextTool;
export type ModelContextLike = Pick<WebMCP.ModelContext, "registerTool">;

export interface WebMcpRegistrationOptions {
  catalog?: WebMcpCatalog;
  session?: PlannerSession;
  sourceSha?: string;
  document?: Document;
  fetcher?: typeof fetch;
}

export interface WebMcpRegistration {
  available: boolean;
  registered: boolean;
  controller: AbortController;
  session?: PlannerSession;
  dispose: () => void;
}

/** Feature detection intentionally names only the standard page-side surface. */
export function hasWebMcp(documentLike: Document | undefined = typeof document === "undefined" ? undefined : document): documentLike is Document & { modelContext: ModelContextLike } {
  if (!documentLike) return false;
  const modelContext = (documentLike as Document & { modelContext?: ModelContextLike }).modelContext;
  return Boolean(modelContext && typeof modelContext.registerTool === "function");
}

/**
 * Registers the four StackKits tools once. No navigator fallback or origin
 * exposure is used; a missing browser API leaves the page fully usable.
 */
export async function registerStackKitsWebMcp(options: WebMcpRegistrationOptions = {}): Promise<WebMcpRegistration> {
  const controller = new AbortController();
  const pageDocument = options.document ?? (typeof document === "undefined" ? undefined : document);
  if (!hasWebMcp(pageDocument)) {
    return {
      available: false,
      registered: false,
      controller,
      dispose: () => controller.abort(),
    };
  }

  let session = options.session;
  try {
    if (!session) {
      const catalog = options.catalog ?? await loadCatalog(options.fetcher, controller.signal);
      if (options.catalog) {
        const integrity = await validateAndVerifyCatalog(catalog);
        if (!integrity.valid || !integrity.catalog) {
          return { available: true, registered: false, controller, dispose: () => controller.abort() };
        }
      }
      session = getSharedPlannerSession(catalog, { sourceSha: options.sourceSha });
    } else if (options.sourceSha && session.service.catalog?.source_sha !== options.sourceSha) {
      return {
        available: true,
        registered: false,
        controller,
        session,
        dispose: () => controller.abort(),
      };
    }
    if (!session.service.catalog) {
      return {
        available: true,
        registered: false,
        controller,
        session,
        dispose: () => controller.abort(),
      };
    }
    const integrity = await validateAndVerifyCatalog(session.service.catalog);
    if (!integrity.valid) {
      return {
        available: true,
        registered: false,
        controller,
        session,
        dispose: () => controller.abort(),
      };
    }
    setSharedPlannerSession(session);
    const definitions = createToolDefinitions(session);
    for (const definition of definitions) {
      if (controller.signal.aborted) break;
      await pageDocument.modelContext.registerTool(definition as unknown as ModelContextTool, { signal: controller.signal });
    }
    if (controller.signal.aborted) {
      return { available: true, registered: false, controller, session, dispose: () => controller.abort() };
    }
    return { available: true, registered: true, controller, session, dispose: () => controller.abort() };
  } catch {
    controller.abort();
    return {
      available: true,
      registered: false,
      controller,
      ...(session ? { session } : {}),
      dispose: () => controller.abort(),
    };
  }
}
