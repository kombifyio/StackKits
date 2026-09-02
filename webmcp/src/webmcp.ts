/// <reference types="webmcp-types" />

import {
  CatalogFetchError,
  CatalogValidationError,
  loadCatalog,
  validateAndVerifyCatalog,
  type CatalogLoadOptions,
} from "./catalog.js";
import { getSharedPlannerSession, setSharedPlannerSession, type PlannerSession } from "./session.js";
import { createToolDefinitions } from "./tools.js";
import type { WebMcpCatalog } from "./types.js";

export const WEBMCP_REGISTRATION_TIMEOUT_MS = 4_500 as const;

export type ModelContextTool = WebMCP.ModelContextTool;
export type ModelContextLike = Pick<WebMCP.ModelContext, "registerTool">;

export type WebMcpStatusCode =
  | "checking"
  | "ready"
  | "browser_api_unavailable"
  | "catalog_fetch_failed"
  | "catalog_fetch_timeout"
  | "catalog_integrity_failed"
  | "catalog_source_mismatch"
  | "registration_failed"
  | "registration_timeout";

export interface WebMcpStatus {
  code: WebMcpStatusCode;
  label: string;
  message: string;
  retryable: boolean;
}

export interface WebMcpRegistrationOptions {
  catalog?: WebMcpCatalog;
  session?: PlannerSession;
  sourceSha?: string;
  document?: Document;
  fetcher?: typeof fetch;
  catalogLoadOptions?: CatalogLoadOptions;
}

export interface WebMcpRegistration {
  available: boolean;
  registered: boolean;
  controller: AbortController;
  status: WebMcpStatus;
  session?: PlannerSession;
  dispose: () => void;
}

const WEBMCP_STATUS: Record<WebMcpStatusCode, Omit<WebMcpStatus, "code">> = {
  checking: {
    label: "Checking browser-agent tools",
    message: "Checking the browser WebMCP API and public catalog.",
    retryable: false,
  },
  ready: {
    label: "Browser-agent tools available",
    message: "The four read-only StackKits tools are registered.",
    retryable: false,
  },
  browser_api_unavailable: {
    label: "Browser-agent tools unavailable",
    message: "This browser does not expose document.modelContext.registerTool. Open the planner in a WebMCP-capable browser.",
    retryable: false,
  },
  catalog_fetch_failed: {
    label: "WebMCP catalog unavailable",
    message: "The public WebMCP catalog could not be loaded. Retry to fetch it again.",
    retryable: true,
  },
  catalog_fetch_timeout: {
    label: "WebMCP catalog timed out",
    message: "The public WebMCP catalog did not load within 4,500 ms. Retry to fetch it again.",
    retryable: true,
  },
  catalog_integrity_failed: {
    label: "WebMCP catalog rejected",
    message: "The public WebMCP catalog failed schema or digest verification. Reload or retry for a fresh exact-SHA catalog.",
    retryable: true,
  },
  catalog_source_mismatch: {
    label: "WebMCP build/catalog mismatch",
    message: "The planner build SHA does not match the public catalog. Reload after the matching build is published.",
    retryable: true,
  },
  registration_failed: {
    label: "WebMCP registration failed",
    message: "The browser WebMCP API rejected tool registration. Retry to register the tools again.",
    retryable: true,
  },
  registration_timeout: {
    label: "WebMCP registration timed out",
    message: "The browser WebMCP API did not finish registering tools within 4,500 ms. Retry to register them again.",
    retryable: true,
  },
};

export function createWebMcpStatus(code: WebMcpStatusCode): WebMcpStatus {
  return { code, ...WEBMCP_STATUS[code] };
}

export function webMcpStatusForCatalogError(error: unknown): WebMcpStatus {
  if (error instanceof CatalogFetchError) {
    return createWebMcpStatus(error.timedOut ? "catalog_fetch_timeout" : "catalog_fetch_failed");
  }
  if (error instanceof CatalogValidationError) return createWebMcpStatus("catalog_integrity_failed");
  return createWebMcpStatus("catalog_fetch_failed");
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
      status: createWebMcpStatus("browser_api_unavailable"),
      dispose: () => controller.abort(),
    };
  }

  let session = options.session;
  let phase: "catalog" | "registration" = "catalog";
  let registrationTimedOut = false;
  try {
    if (!session) {
      const catalog = options.catalog ?? await loadCatalog(options.fetcher, controller.signal, options.catalogLoadOptions);
      if (options.catalog) {
        const integrity = await validateAndVerifyCatalog(catalog);
        if (!integrity.valid || !integrity.catalog) {
          return {
            available: true,
            registered: false,
            controller,
            status: createWebMcpStatus("catalog_integrity_failed"),
            dispose: () => controller.abort(),
          };
        }
      }
      if (options.sourceSha && catalog.source_sha !== options.sourceSha) {
        return {
          available: true,
          registered: false,
          controller,
          status: createWebMcpStatus("catalog_source_mismatch"),
          dispose: () => controller.abort(),
        };
      }
      session = getSharedPlannerSession(catalog, { sourceSha: options.sourceSha });
    } else if (options.sourceSha && session.service.catalog?.source_sha !== options.sourceSha) {
      return {
        available: true,
        registered: false,
        controller,
        status: createWebMcpStatus("catalog_source_mismatch"),
        session,
        dispose: () => controller.abort(),
      };
    }
    if (!session.service.catalog) {
      return {
        available: true,
        registered: false,
        controller,
        status: createWebMcpStatus("catalog_integrity_failed"),
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
        status: createWebMcpStatus("catalog_integrity_failed"),
        session,
        dispose: () => controller.abort(),
      };
    }
    phase = "registration";
    setSharedPlannerSession(session);
    const definitions = createToolDefinitions(session);
    let registrationTimeoutHandle: ReturnType<typeof setTimeout> | undefined;
    const registration = (async (): Promise<void> => {
      for (const definition of definitions) {
        if (controller.signal.aborted) break;
        await pageDocument.modelContext.registerTool(definition as unknown as ModelContextTool, { signal: controller.signal });
      }
    })();
    const registrationDeadline = new Promise<never>((_resolve, reject) => {
      registrationTimeoutHandle = setTimeout(() => {
        registrationTimedOut = true;
        controller.abort();
        reject(new Error("WebMCP registration exceeded its time budget"));
      }, WEBMCP_REGISTRATION_TIMEOUT_MS);
    });
    try {
      await Promise.race([registration, registrationDeadline]);
    } finally {
      if (registrationTimeoutHandle) clearTimeout(registrationTimeoutHandle);
    }
    if (controller.signal.aborted) {
      return {
        available: true,
        registered: false,
        controller,
        status: createWebMcpStatus("registration_failed"),
        session,
        dispose: () => controller.abort(),
      };
    }
    return {
      available: true,
      registered: true,
      controller,
      status: createWebMcpStatus("ready"),
      session,
      dispose: () => controller.abort(),
    };
  } catch (error) {
    controller.abort();
    return {
      available: true,
      registered: false,
      controller,
      status: phase === "registration"
        ? createWebMcpStatus(registrationTimedOut ? "registration_timeout" : "registration_failed")
        : webMcpStatusForCatalogError(error),
      ...(session ? { session } : {}),
      dispose: () => controller.abort(),
    };
  }
}
