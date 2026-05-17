import { readFile, readdir, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const mode = process.argv[2] || 'source';
const websiteDir = path.join(repoRoot, 'website');

// Source-mode static surfaces live under website/public/; build-mode static
// surfaces are flattened to website/dist/. Svelte/Vite source lives under
// website/src/ and is bundled into website/dist/assets/.
const staticRoot = mode === 'build'
  ? path.join(websiteDir, 'dist')
  : path.join(websiteDir, 'public');

const sourceOnly = [
  'package.json',
  'vite.config.ts',
  'svelte.config.js',
  'tsconfig.json',
  'index.html',
  'src/main.ts',
  'src/App.svelte',
  'src/Layout.svelte',
  'src/app.css',
  'src/pages/Home.svelte',
  'src/pages/CliReference.svelte',
  'src/content/kits.ts',
  'scripts/prebuild.mjs',
];

const commonStaticFiles = [
  'CNAME',
  'favicon.svg',
  'icon.png',
  '_headers',
  '_worker.js',
  'install',
  'base',
  'modern',
  'ha',
  'llms.txt',
  'llms-full.txt',
  'llms-snippets.txt',
  'getting-started/agents.md',
  'getting-started/agents/basekit-autonomous-rollout.md',
  'getting-started/agents/inspect-existing-rollout.md',
  'getting-started/agents/diagnose-failed-rollout.md',
  'getting-started/agents/enable-monitoring-addon.md',
  'getting-started/agents/ssh-rollout.md',
  'mcp/stackkit-mcp.md',
  'api/openapi.v1.yaml',
  'schemas/stackkit-agent-run-manifest.schema.json',
  'schemas/stackkit-agent-functional-result.schema.json',
  'sitemap.xml',
];

const buildExtra = ['changelog.json', 'index.html'];

for (const rel of commonStaticFiles) {
  await requireFile(staticRoot, rel);
}

if (mode === 'source') {
  for (const rel of sourceOnly) {
    await requireFile(websiteDir, rel);
  }
}

if (mode === 'build') {
  for (const rel of buildExtra) {
    await requireFile(staticRoot, rel);
  }
}

const install = await readStatic('install');
const base = await readStatic('base');
const modern = await readStatic('modern');
const ha = await readStatic('ha');
const headers = await readStatic('_headers');
const worker = await readStatic('_worker.js');
const llms = await readStatic('llms.txt');
const llmsFull = await readStatic('llms-full.txt');
const snippets = await readStatic('llms-snippets.txt');
const agents = await readStatic('getting-started/agents.md');
const mcp = await readStatic('mcp/stackkit-mcp.md');
const siteOpenAPI = await readStatic('api/openapi.v1.yaml');
const canonicalOpenAPI = await readRepo('api/openapi/stackkits-v1.yaml');
const runManifestSchema = JSON.parse(await readStatic('schemas/stackkit-agent-run-manifest.schema.json'));
const functionalResultSchema = JSON.parse(await readStatic('schemas/stackkit-agent-functional-result.schema.json'));
const sitemap = await readStatic('sitemap.xml');

for (const [name, content] of Object.entries({ install, base, modern, ha })) {
  assert(content.startsWith('#!/bin/sh'), `${name} must start with #!/bin/sh`);
  assert(!content.trimStart().startsWith('<!DOCTYPE html'), `${name} must not be HTML`);
}

assert(install.includes('StackKits CLI installer'), 'install endpoint must identify the CLI installer');
assert(install.includes('install.sh'), 'install endpoint must delegate to the root release installer');
assert(base.includes('StackKits Base Installer'), 'base endpoint must identify the Base installer');
assert(modern.includes('alpha/scaffolding'), 'modern endpoint must warn that it is alpha/scaffolding');
assert(ha.includes('alpha/scaffolding'), 'ha endpoint must warn that it is alpha/scaffolding');

for (const route of ['/install', '/base', '/modern', '/ha']) {
  assert(headers.includes(route), `_headers must include ${route}`);
}
assert(headers.includes('text/x-shellscript'), '_headers must force shell content type');
for (const route of ['/llms.txt', '/llms-full.txt', '/llms-snippets.txt', '/getting-started/agents.md', '/mcp/stackkit-mcp.md', '/api/openapi.v1.yaml', '/schemas/*']) {
  assert(headers.includes(route), `_headers must include ${route}`);
}
assert(headers.includes('Content-Signal: search=yes, ai-input=yes'), '_headers must allow search and AI input for public agent docs');
assert(!headers.includes('ai-train=yes'), '_headers must not grant AI training rights');

for (const host of ['install.stackkit.cc', 'base.stackkit.cc', 'modern.stackkit.cc', 'ha.stackkit.cc']) {
  assert(worker.includes(host), `_worker.js must route ${host}`);
}
assert(worker.includes('env.ASSETS.fetch'), '_worker.js must serve static assets through ASSETS');
assert(worker.includes('text/x-shellscript'), '_worker.js must set shell content type');

assert(llms.includes('/llms-full.txt'), 'llms.txt must point to full context');
assert(llms.includes('/api/openapi.v1.yaml'), 'llms.txt must point to OpenAPI mirror');
assert(llmsFull.includes('BaseKit'), 'llms-full must mention BaseKit');
assert(llmsFull.includes('Modern Homelab and HA Kit are alpha/scaffolding'), 'llms-full must state alpha stance for Modern and HA Kit');
assert(snippets.includes('stackkit verify --http --json'), 'snippets must include verify JSON command');
assert(agents.includes('stackkit agent prompt'), 'agent guide must document CLI prompt helper');
assert(mcp.includes('STACKKIT_MCP_ALLOW_WRITE=true'), 'MCP guide must document write gate');
assert(siteOpenAPI === canonicalOpenAPI, 'website OpenAPI mirror must match api/openapi/stackkits-v1.yaml');
assert(runManifestSchema.properties.checkedViaAgent.const === true, 'run manifest schema must require checkedViaAgent true');
assert(runManifestSchema.properties.noHandEditedGeneratedArtifacts.const === true, 'run manifest schema must forbid hand-edited generated artifacts');
assert(functionalResultSchema.properties.status.enum.includes('pass'), 'functional result schema must require pass status');
assert(functionalResultSchema.properties.checkedViaAgent.const === true, 'functional result schema must require checkedViaAgent true');
for (const route of ['/llms.txt', '/llms-full.txt', '/llms-snippets.txt', '/getting-started/agents.md', '/mcp/stackkit-mcp.md', '/api/openapi.v1.yaml']) {
  assert(sitemap.includes(`https://stackkit.cc${route}`), `sitemap must include ${route}`);
}

if (mode === 'source') {
  const indexHtml = await readFile(path.join(websiteDir, 'index.html'), 'utf8');
  assert(/kombify StackKits/i.test(indexHtml), 'index.html must brand as kombify StackKits');
  assert(indexHtml.includes('rel="alternate"'), 'index.html must expose <link rel="alternate"> agent surfaces');
  assert(indexHtml.includes('href="/llms.txt"'), 'index.html must alternate-link llms.txt');
  assert(indexHtml.includes('href="/api/openapi.v1.yaml"'), 'index.html must alternate-link the OpenAPI mirror');
  assert(indexHtml.includes('href="/getting-started/agents.md"'), 'index.html must alternate-link the agent guide');

  const kitsTs = await readFile(path.join(websiteDir, 'src/content/kits.ts'), 'utf8');
  assert(kitsTs.includes("id: 'base'"), 'kits.ts must define BaseKit');
  assert(kitsTs.includes("id: 'modern'"), 'kits.ts must define Modern Home Lab');
  assert(kitsTs.includes("id: 'ha'"), 'kits.ts must define HA Kit');
  assert(/status:\s*'alpha'/.test(kitsTs), 'kits.ts must keep alpha status for Modern/HA');

  const cliCommandsTs = await readFile(path.join(websiteDir, 'src/content/cliCommands.ts'), 'utf8');
  for (const cmd of ['init', 'prepare', 'generate', 'plan', 'apply', 'verify', 'agent']) {
    assert(cliCommandsTs.includes(`name: '${cmd}'`), `cliCommands.ts must document the ${cmd} command`);
  }
}

if (mode === 'build') {
  const distIndex = await readStatic('index.html');
  assert(/kombify StackKits/i.test(distIndex), 'built index must brand as kombify StackKits');
  assert(distIndex.includes('href="/llms.txt"'), 'built index must alternate-link llms.txt');
  assert(distIndex.includes('href="/icon.png"'), 'built index must reference the kombifyKits icon');

  const changelog = JSON.parse(await readStatic('changelog.json'));
  assert(typeof changelog.version === 'string' && changelog.version.length > 0, 'built changelog.json must include a non-empty version');
  assert(Array.isArray(changelog.notes), 'built changelog.json must include a notes array');

  const assetsDir = path.join(staticRoot, 'assets');
  const assetFiles = await readdir(assetsDir);
  const jsFiles = assetFiles.filter((f) => f.endsWith('.js'));
  assert(jsFiles.length > 0, 'built dist/assets must contain at least one JS bundle');
  const bundle = await readFile(path.join(assetsDir, jsFiles[0]), 'utf8');
  for (const needle of ['stackkit init base-kit', 'stackkit apply', 'BaseKit', 'Modern Home Lab', 'High Availability']) {
    assert(bundle.includes(needle), `JS bundle must include "${needle}"`);
  }
}

async function requireFile(root, rel) {
  const full = path.join(root, rel);
  try {
    const info = await stat(full);
    assert(info.isFile() || info.isDirectory(), `expected ${rel} to exist`);
  } catch {
    throw new Error(`expected ${rel} to exist under ${path.relative(repoRoot, root) || '.'}`);
  }
}

async function readStatic(rel) {
  return readFile(path.join(staticRoot, rel), 'utf8');
}

async function readRepo(rel) {
  return readFile(path.join(repoRoot, rel), 'utf8');
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}
