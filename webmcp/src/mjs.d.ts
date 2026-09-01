declare module "*.mjs" {
  export function projectAuthorityBundle(root: string, sourceSha: string, plannerPath?: string): Promise<unknown>;
}
