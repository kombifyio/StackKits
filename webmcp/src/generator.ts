import type { WebMcpCatalog } from "./types.js";

export interface AuthorityProjectionOptions {
  plannerPath?: "/planner";
}

type AuthorityProjectionModule = {
  projectAuthorityBundle: (root: string, sourceSha: string, plannerPath?: string) => Promise<WebMcpCatalog>;
};

/** Node-only facade over the dependency-free public projection script. */
export async function projectAuthorityBundle(root: string, sourceSha: string, options: AuthorityProjectionOptions = {}): Promise<WebMcpCatalog> {
  const module = await import("../scripts/generate-catalog.mjs") as unknown as AuthorityProjectionModule;
  return module.projectAuthorityBundle(root, sourceSha, options.plannerPath ?? "/planner");
}

export async function generateCatalog(root: string, sourceSha: string, options: AuthorityProjectionOptions = {}): Promise<WebMcpCatalog> {
  return projectAuthorityBundle(root, sourceSha, options);
}
