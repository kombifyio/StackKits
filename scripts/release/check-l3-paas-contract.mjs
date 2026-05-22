#!/usr/bin/env node
import { readdir, readFile, stat } from 'node:fs/promises';
import path from 'node:path';

function parseArgs(argv) {
  const opts = {
    repoRoot: '.',
    generated: [],
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = argv[i + 1];
    switch (arg) {
      case '--repo-root':
        if (!next || next.startsWith('--')) throw new Error('--repo-root requires a value');
        opts.repoRoot = next;
        i += 1;
        break;
      case '--generated':
        if (!next || next.startsWith('--')) throw new Error('--generated requires a value');
        opts.generated.push(next);
        i += 1;
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }
  return opts;
}

async function exists(filePath) {
  try {
    await stat(filePath);
    return true;
  } catch (err) {
    if (err.code === 'ENOENT') return false;
    throw err;
  }
}

async function walk(dir, predicate, out = []) {
  if (!(await exists(dir))) return out;
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === 'node_modules' || entry.name === '.git' || entry.name === 'dist') {
        continue;
      }
      await walk(full, predicate, out);
    } else if (predicate(full)) {
      out.push(full);
    }
  }
  return out;
}

function moduleNameFromPath(filePath) {
  return path.basename(path.dirname(filePath));
}

function tfName(moduleName) {
  return moduleName.replaceAll('-', '_');
}

async function collectL3Modules(repoRoot) {
  const modulesDir = path.join(repoRoot, 'modules');
  const files = await walk(modulesDir, (file) => path.basename(file) === 'module.cue');
  const l3 = [];
  for (const file of files) {
    const text = await readFile(file, 'utf8');
    if (!/layer:\s*"L3-application"/.test(text)) {
      continue;
    }
    l3.push({ name: moduleNameFromPath(file), file, text });
  }
  return l3.sort((a, b) => a.name.localeCompare(b.name));
}

function validateModuleContracts(modules, failures) {
  for (const mod of modules) {
    if (!/delivery\s*:/.test(mod.text)) {
      continue;
    }
    if (!/delivery:\s*{[\s\S]*type:\s*"paas"[\s\S]*managedBy:\s*"selected-paas"[\s\S]*}/m.test(mod.text)) {
      failures.push(`${mod.file}: L3 module delivery contract must declare type="paas" and managedBy="selected-paas"`);
    }
    if (/"stackkit\.managed-by":\s*"compose"/.test(mod.text)) {
      failures.push(`${mod.file}: StackKit-owned/default L3 module must not label services stackkit.managed-by="compose"`);
    }
    if (!/"stackkit\.managed-by":\s*"selected-paas"/.test(mod.text)) {
      failures.push(`${mod.file}: StackKit-owned/default L3 module services must label stackkit.managed-by="selected-paas"`);
    }
    if (!/"stackkit\.layer":\s*"3-application"/.test(mod.text)) {
      failures.push(`${mod.file}: StackKit-owned/default L3 module services must label stackkit.layer="3-application"`);
    }
  }
}

async function validateBaseKitServices(repoRoot, failures) {
  const servicesFile = path.join(repoRoot, 'base-kit', 'services.cue');
  if (!(await exists(servicesFile))) {
    return;
  }
  const text = await readFile(servicesFile, 'utf8');
  if (/"stackkit\.managed-by":\s*"dokploy"/.test(text)) {
    failures.push(`${servicesFile}: BaseKit default L3 service labels must use stackkit.managed-by="selected-paas", not hard-coded dokploy`);
  }
}

function collectStackOwnedGeneratedApps(text) {
  const names = new Set();
  const ownershipPattern = /ownership\s*=\s*"stackkit"/g;
  for (const match of text.matchAll(ownershipPattern)) {
    const before = text.slice(Math.max(0, match.index - 700), match.index);
    const nameMatches = [...before.matchAll(/name\s*=\s*"([^"]+)"/g)];
    if (nameMatches.length === 0) {
      continue;
    }
    names.add(nameMatches[nameMatches.length - 1][1]);
  }
  return [...names].sort((a, b) => a.localeCompare(b));
}

function stripAllowedInfrastructureComposeCommands(text) {
  return text.replace(
    /DOCKER_HOST="\$\{var\.docker_host\}"\s+docker compose\s+-f\s+"\$PROXY_COMPOSE"\s+up\s+-d/g,
    '',
  );
}

async function validateGeneratedFiles(generatedFiles, failures) {
  for (const file of generatedFiles) {
    if (!(await exists(file))) {
      failures.push(`${file}: generated file not found`);
      continue;
    }
    const text = await readFile(file, 'utf8');
    const stackOwnedApps = collectStackOwnedGeneratedApps(text);
    if (stackOwnedApps.length === 0) {
      failures.push(`${file}: generated default StackKit-owned L3 app manifests must include ownership="stackkit"`);
    }
    for (const appName of stackOwnedApps) {
      const name = tfName(appName);
      const directCompose = `docker compose -f \${local_file.${name}_compose[0].filename}`;
      if (text.includes(directCompose)) {
        failures.push(`${file}: StackKit-owned/default L3 app ${appName} is started with docker compose instead of the PaaS adapter`);
      }
    }
    const composeScanText = stripAllowedInfrastructureComposeCommands(text);
    if (/docker compose\b[\s\S]{0,160}\bup\b/.test(composeScanText)) {
      failures.push(`${file}: generated default path must not contain active docker compose up commands`);
    }
    if (/coolify_stackkit_router/.test(text) || /stackkit-coolify-route/.test(text)) {
      failures.push(`${file}: Coolify default path must not include the StackKit-owned Coolify routing fallback`);
    }
    if (/local-compose:/.test(text)) {
      failures.push(`${file}: strict generated defaults must not record local-compose deployment evidence`);
    }
    if (!/direct_compose_deploy\s*=\s*false/.test(text)) {
      failures.push(`${file}: strict Coolify default must set direct_compose_deploy=false`);
    }
    const hubFallback = text.match(/platform_hub_fallback\s*=\s*([^\n]+)/);
    if (!hubFallback || hubFallback[1].trim() !== 'false') {
      failures.push(`${file}: strict Coolify default must set platform_hub_fallback=false`);
    }
    if (!/variable\s+"enable_platform_fallback"[\s\S]*?default\s*=\s*false/.test(text)) {
      failures.push(`${file}: enable_platform_fallback must default to false`);
    }
    if (!/variable\s+"platform_fallback_mode"[\s\S]*?default\s*=\s*"disabled"/.test(text)) {
      failures.push(`${file}: platform_fallback_mode must default to "disabled"`);
    }
    if (!/provider\s+"docker"\s*\{[\s\S]*?host\s*=\s*var\.docker_host\s*!=\s*""\s*\?\s*var\.docker_host\s*:\s*"unix:\/\/\/var\/run\/docker\.sock"[\s\S]*?\}/.test(text)) {
      failures.push(`${file}: Docker provider must use var.docker_host so Fresh-VM Coolify installs use the same daemon endpoint as StackKit`);
    }
    if (!/l3_platform_adapter\s*=\s*local\.platform_fallback_standalone\s*\?\s*"none"\s*:\s*var\.paas/.test(text)) {
      failures.push(`${file}: generated default StackKit-owned L3 app manifests must resolve through var.paas unless explicit fallback is enabled`);
    }
    if (
      !/traefik_http_entrypoint\s*=\s*local\.rp_coolify\s*\?\s*"http"\s*:\s*"web"/.test(text) ||
      !/traefik_https_entrypoint\s*=\s*local\.rp_coolify\s*\?\s*"https"\s*:\s*"websecure"/.test(text) ||
      !/entrypoint\s*=\s*var\.enable_https\s*\?\s*local\.traefik_https_entrypoint\s*:\s*local\.traefik_http_entrypoint/.test(text)
    ) {
      failures.push(`${file}: Coolify-routed services must use Coolify Traefik entrypoints http/https, not StackKit standalone web/websecure`);
    }
    if (/entrypoint\s*=\s*var\.enable_https\s*\?\s*"websecure"\s*:\s*"web"/.test(text)) {
      failures.push(`${file}: generated Coolify default must not hard-code standalone Traefik entrypoints web/websecure`);
    }
    if (
      !/traefik\.docker\.network=\$\{local\.routing_network\}/.test(text) ||
      !/label\s*=\s*"traefik\.docker\.network"[\s\S]*?value\s*=\s*local\.traefik_network_name/.test(text)
    ) {
      failures.push(`${file}: Coolify-routed containers must set traefik.docker.network so Traefik selects the Coolify network for multi-network services`);
    }
    if (
      !/resource\s+"local_file"\s+"coolify_dynamic_stackkit"/.test(text) ||
      !/filename\s*=\s*"\/data\/coolify\/proxy\/dynamic\/stackkit\.yml"/.test(text) ||
      !/content\s*=\s*local_file\.traefik_dynamic_stackkit\[0\]\.content/.test(text)
    ) {
      failures.push(`${file}: Coolify-routed Base Hub dynamic middleware must be written into Coolify's proxy dynamic config directory`);
    }
    if (
      !/stackkit-coolify:/.test(text) ||
      !/rule:\s*"Host\(`\$\{local\.domains\.coolify\}`\)"/.test(text) ||
      !/service:\s*stackkit-coolify/.test(text) ||
      !/url:\s*"http:\/\/coolify:8080"/.test(text)
    ) {
      failures.push(`${file}: Coolify itself must be routed by Coolify's proxy dynamic config, not by a StackKit sidecar router`);
    }
    if (!/setup_immich_url\s*=\s*local\.is_host\s*\?\s*"http:\/\/127\.0\.0\.1:\$\{local\.host_ports\.immich\}"\s*:\s*\(local\.rp_coolify\s*\?\s*"http:\/\/immich-server:2283"\s*:\s*"http:\/\/immich:2283"\)/.test(text)) {
      failures.push(`${file}: stackkit-server setup actions must use Coolify's immich-server network alias in strict Coolify mode`);
    }
    if (!/coolify_api_endpoint\s*=\s*"http:\/\/127\.0\.0\.1:8000"/.test(text)) {
      failures.push(`${file}: strict Coolify default must define the node-local Coolify API endpoint for bootstrap`);
    }
    if (!/\$\{local\.coolify_api_endpoint\}\/api\/health/.test(text) || !/\$\{local\.coolify_api_endpoint\}\/health/.test(text)) {
      failures.push(`${file}: Coolify readiness must tolerate both Coolify health endpoints during upstream/runtime drift`);
    }
    if (!/stackkit_docker\(\)\s*\{[\s\S]*?DOCKER_HOST="\$\{var\.docker_host\}" docker "\$@"/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must route docker CLI calls through var.docker_host for Fresh-VM Docker-in-Docker rollouts`);
    }
    if (!/stackkit_coolify_diagnostics\(\)/.test(text) || !/Coolify readiness diagnostics \(redacted\):/.test(text)) {
      failures.push(`${file}: Coolify readiness timeout must emit redacted runtime diagnostics for release-gate failures`);
    }
    if (!/coolify_root_email\s*=\s*var\.admin_email/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must use the generated StackKit adminEmail as ROOT_USER_EMAIL`);
    }
    if (/stackkits-admin@kombify\.io|ci@kombify\.io|test@kombify\.io/.test(text)) {
      failures.push(`${file}: generated local bootstrap must not invent Kombify-owned test user emails`);
    }
    if (!/docker context create stackkit-host --docker "host=\$\{var\.docker_host\}"/.test(text) || !/docker context use stackkit-host/.test(text)) {
      failures.push(`${file}: Coolify install must set the root Docker CLI context from var.docker_host so Coolify SSH actions use the same Docker daemon as StackKit`);
    }
    if (!/--providers\.docker\.endpoint=/.test(text) || !/host\.docker\.internal/.test(text)) {
      failures.push(`${file}: Coolify proxy must point Traefik's Docker provider at the generated Docker host endpoint for Fresh-VM/Docker-in-Docker rollouts`);
    }
    if (!/STACKKIT_COOLIFY_SERVER_PUBLIC_KEY=/.test(text) || !/authorized_keys/.test(text) || !/server_settings set is_reachable = true, is_usable = true/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must authorize and mark the default server usable before strict app deployment can pass`);
    }
    if (!/docker context use default >\/dev\/null 2>&1 \|\| true/.test(text) || !/Setting Docker CLI default context for Coolify runtime actions/.test(text)) {
      failures.push(`${file}: Coolify install must keep Docker's default context during the installer helper run and switch to stackkit-host only after install`);
    }
    if (
      !/stackkit_preseed_coolify_image "postgres:15-alpine" "public\.ecr\.aws\/docker\/library\/postgres:15-alpine"/.test(text) ||
      !/stackkit_preseed_coolify_image "redis:7-alpine" "public\.ecr\.aws\/docker\/library\/redis:7-alpine"/.test(text) ||
      !/image already present locally for StackKit Coolify bootstrap/.test(text)
    ) {
      failures.push(`${file}: Coolify install must preseed Docker-Hub base images and avoid re-pulling them during the installer so live gates do not silently skip on rate limits`);
    }
    if (!/resource\s+"null_resource"\s+"coolify_platform_bootstrap"/.test(text)) {
      failures.push(`${file}: strict Coolify default must bootstrap and persist Coolify API config before app deployment`);
    }
    if (!/STACKKIT_COOLIFY_PLATFORM_JSON=/.test(text) || !/\.stackkit\/platform\.json/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must persist .stackkit/platform.json for stackkit apply`);
    }
    if (!/is_api_enabled'\s*=>\s*true/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must enable Coolify's API explicitly`);
    }
    if (!/is_registration_enabled'\s*=>\s*false/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must disable public registration after root bootstrap`);
    }
    if (!/show_boarding'\s*=>\s*false/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must clear Coolify onboarding before strict readiness can pass`);
    }
    if (!/createToken\(\$tokenName,\s*\['root'\]\)/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must create a root-scoped API token for StackKit-owned app registration`);
    }
    if (!/'id'\s*=>\s*0/.test(text) || !/Hash::make/.test(text) || !/bootstrapPassword/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must be able to create the root Coolify user when the upstream root-user seeder has not completed`);
    }
    if (!/StartProxy::run\(\$server,\s*async:\s*false,\s*force:\s*true\)/.test(text) || !/proxyContainer'\s*=>\s*'coolify-proxy'/.test(text)) {
      failures.push(`${file}: Coolify bootstrap must synchronously start Coolify's own proxy before strict routing readiness checks`);
    }
  }
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const repoRoot = path.resolve(opts.repoRoot);
  const modules = await collectL3Modules(repoRoot);
  const failures = [];

  validateModuleContracts(modules, failures);
  await validateBaseKitServices(repoRoot, failures);
  await validateGeneratedFiles(opts.generated.map((p) => path.resolve(p)), failures);

  if (failures.length > 0) {
    console.error('L3 PaaS contract check failed:');
    for (const failure of failures) {
      console.error(`- ${failure}`);
    }
    process.exit(1);
  }

  const moduleContracts = modules.filter((mod) => /delivery\s*:/.test(mod.text)).length;
  console.log(`Default StackKit-owned L3 PaaS contract check passed (${moduleContracts} module contract(s))`);
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
