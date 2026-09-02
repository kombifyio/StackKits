import type { WebMcpCatalog } from "./types.js";

export interface AuthorityProjectionOptions {
  plannerPath?: "/planner";
}

type AuthorityProjectionModule = {
  projectAuthorityBundleV2: (root: string, sourceSha: string, plannerPath?: string) => Promise<WebMcpCatalog>;
  projectAuthorityBundle: (root: string, sourceSha: string, plannerPath?: string) => Promise<unknown>;
};

/** Node-only facade over the dependency-free native v2 public projection. */
export async function projectAuthorityBundle(root: string, sourceSha: string, options: AuthorityProjectionOptions = {}): Promise<WebMcpCatalog> {
  const module = await import("../scripts/generate-catalog.mjs") as unknown as AuthorityProjectionModule;
  return module.projectAuthorityBundleV2(root, sourceSha, options.plannerPath ?? "/planner");
}

export async function generateCatalog(root: string, sourceSha: string, options: AuthorityProjectionOptions = {}): Promise<WebMcpCatalog> {
  return projectAuthorityBundle(root, sourceSha, options);
}

/** Explicit compatibility adapter for the pre-v2 global-tier catalog. */
export async function projectLegacyCatalog(root: string, sourceSha: string, options: AuthorityProjectionOptions = {}): Promise<unknown> {
  const module = await import("../scripts/generate-catalog.mjs") as unknown as AuthorityProjectionModule;
  return module.projectAuthorityBundle(root, sourceSha, options.plannerPath ?? "/planner");
}
