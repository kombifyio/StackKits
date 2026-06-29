import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test from 'node:test';
import assert from 'node:assert/strict';

const execFileAsync = promisify(execFile);

async function writeModule(root, name, body) {
  const dir = path.join(root, 'modules', name);
  await mkdir(dir, { recursive: true });
  await writeFile(path.join(dir, 'module.cue'), body);
}

const validL3Module = `package photos

Contract: {
  metadata: {
    name: "photos"
    layer: "L3-application"
  }
  delivery: {
    type: "paas"
    managedBy: "selected-paas"
  }
  services: photos: {
    labels: {
      "stackkit.managed-by": "selected-paas"
      "stackkit.layer": "3-application"
    }
  }
}
`;

test('check-l3-paas-contract passes for default StackKit-owned L3 manifests', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-pass-'));
  await writeModule(root, 'photos', validL3Module);
  const generated = path.join(root, 'main.tf');
  await writeFile(
    generated,
    `variable "enable_platform_fallback" {
  default = false
}
variable "platform_fallback_mode" {
  default = "disabled"
}
locals {
  coolify_docker_host_name     = startswith(var.docker_host, "tcp://") ? split(":", trimprefix(var.docker_host, "tcp://"))[0] : ""
  coolify_local_endpoint       = "http://127.0.0.1:8000"
  coolify_api_endpoint         = local.coolify_local_endpoint
  coolify_bootstrap_api_endpoint = local.coolify_docker_host_name != "" ? "http://\${local.coolify_docker_host_name}:8000" : local.coolify_api_endpoint
  coolify_root_email           = var.admin_email
  platform_fallback_standalone = var.enable_platform_fallback && var.platform_fallback_mode == "standalone-compose"
  direct_compose_deploy        = false
  platform_hub_fallback        = false
  l3_platform_adapter          = local.platform_fallback_standalone ? "none" : var.paas
  traefik_http_entrypoint      = local.rp_coolify ? "http" : "web"
  traefik_https_entrypoint     = local.rp_coolify ? "https" : "websecure"
  entrypoint                   = var.enable_https ? local.traefik_https_entrypoint : local.traefik_http_entrypoint
  setup_immich_url             = local.is_host ? "http://127.0.0.1:\${local.host_ports.immich}" : (local.rp_coolify ? "http://immich-server:2283" : "http://immich:2283")
}
provider "docker" {
  host = var.docker_host != "" ? var.docker_host : "unix:///var/run/docker.sock"
}
resource "docker_container" "pocketid" {
  labels {
    label = "traefik.docker.network"
    value = local.traefik_network_name
  }
}
resource "local_file" "traefik_dynamic_stackkit" {
  content = "http: stackkit-coolify: rule: \"Host(\`\${local.domains.coolify}\`)\" service: stackkit-coolify url: \"http://coolify:8080\""
}
resource "local_file" "coolify_dynamic_stackkit" {
  filename = "/data/coolify/proxy/dynamic/stackkit.yml"
  content  = local_file.traefik_dynamic_stackkit[0].content
}
resource "null_resource" "coolify_platform_bootstrap" {
  provisioner "local-exec" {
    command = "stackkit_docker() { DOCKER_HOST=\"\${var.docker_host}\" docker \"$@\"; } stackkit_sync_coolify_dynamic_config() { cat > /data/coolify/proxy/dynamic/stackkit.yml; } stackkit_coolify_diagnostics() { echo Coolify readiness diagnostics (redacted):; } expected_endpoint=\"$(stackkit_coolify_proxy_docker_endpoint)\" current_endpoint=\"$(stackkit_docker inspect coolify-proxy)\" [ \"$current_endpoint\" != \"$expected_endpoint\" ] curl -fsS \${local.coolify_bootstrap_api_endpoint}/api/health curl -fsS \${local.coolify_bootstrap_api_endpoint}/health traefik.docker.network=\${local.routing_network} STACKKIT_COOLIFY_API_ENDPOINT=\"\${local.coolify_api_endpoint}\" STACKKIT_COOLIFY_PLATFORM_JSON=... STACKKIT_COOLIFY_SERVER_PUBLIC_KEY= authorized_keys server_settings set is_reachable = true, is_usable = true host.docker.internal --providers.docker.endpoint= --certificatesresolvers.letsencrypt.acme.httpchallenge=true --certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=http stackkit_docker compose -f \"$PROXY_COMPOSE\" up -d 'id' => 0 Hash::make($bootstrapPassword) show_boarding' => false is_api_enabled' => true is_registration_enabled' => false createToken($tokenName, ['root']) StartProxy::run($server, async: false, force: true) proxyContainer' => 'coolify-proxy' .stackkit/platform.json 'capability' => 'backups' 'status' => 'configured' stackkit.backup=required restore-drill endpoint=/api/v1/internal/runtime-actions/restore-drill"
  }
}
resource "null_resource" "coolify_install" {
  provisioner "local-exec" {
    command = "stackkit_preseed_coolify_image "postgres:15-alpine" "public.ecr.aws/docker/library/postgres:15-alpine" && stackkit_preseed_coolify_image "redis:7-alpine" "public.ecr.aws/docker/library/redis:7-alpine" && echo image already present locally for StackKit Coolify bootstrap && docker context create stackkit-host --docker "host=\${var.docker_host}" && docker context use default >/dev/null 2>&1 || true && echo Setting Docker CLI default context for Coolify runtime actions && docker context use stackkit-host"
  }
}
resource "local_file" "platform_l3_manifest" {
  content = jsonencode({
    apps = [{
      name = "photos"
      ownership = "stackkit"
      managedBy = local.l3_platform_adapter
    }]
  })
}
`,
  );

  const { stdout } = await execFileAsync(process.execPath, [
    'scripts/release/check-l3-paas-contract.mjs',
    '--repo-root',
    root,
    '--generated',
    generated,
  ]);

  assert.match(stdout, /Default StackKit-owned L3 PaaS contract check passed/);
});

test('check-l3-paas-contract rejects Coolify installer shim that drops docker_host', async () => {
  const repoRoot = process.cwd();
  const tempDir = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-coolify-shim-'));
  const generated = path.join(tempDir, 'main.tf');
  const source = await readFile(path.join(repoRoot, 'basement-kit', 'templates', 'simple', 'main.tf'), 'utf8');
  const requiredShim = `if [ -n "\${var.docker_host}" ]; then
  DOCKER_HOST="\${var.docker_host}" exec "\\$real" "\\$@"
fi
exec "\\$real" "\\$@"`;
  assert.match(source, /COOLIFY_SYSTEMCTL_SHIM\/docker/);
  assert.match(source, /DOCKER_HOST="\$\{var\.docker_host\}"\s+exec\s+"\\\$real"\s+"\\\$@"/);
  await writeFile(generated, source.replace(requiredShim, 'exec "\\$real" "\\$@"'));

  await assert.rejects(
    execFileAsync(process.execPath, [
      'scripts/release/check-l3-paas-contract.mjs',
      '--repo-root',
      repoRoot,
      '--generated',
      generated,
    ]),
    /Coolify installer Docker shim must force var\.docker_host/,
  );
});

test('check-l3-paas-contract allows unmanaged L3 modules without delivery metadata', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-unmanaged-module-'));
  await writeModule(
    root,
    'notes',
    `package notes

Contract: {
  metadata: {
    name: "notes"
    layer: "L3-application"
  }
}
`,
  );

  const { stdout } = await execFileAsync(process.execPath, ['scripts/release/check-l3-paas-contract.mjs', '--repo-root', root]);

  assert.match(stdout, /Default StackKit-owned L3 PaaS contract check passed/);
});

test('check-l3-paas-contract accepts v0.4 PaaS release posture', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-paas-posture-pass-'));
  await mkdir(path.join(root, 'basement-kit'), { recursive: true });
  await writeFile(
    path.join(root, 'basement-kit', 'mode_matrix.cue'),
    `package base_kit

modeMatrix: {
  paas: {
    coolify: "default"
    komodo:  "supported"
    dokploy: "draft"
    dockge:  "experimental"
  }
}
`,
  );
  await writeFile(
    path.join(root, 'basement-kit', 'stackkit.yaml'),
    `changelog:
  - version: "5.0.0"
    changes:
      - "CHANGED: Komodo is the beta-supported PaaS alternative; Dokploy remains draft"
`,
  );

  const { stdout } = await execFileAsync(process.execPath, ['scripts/release/check-l3-paas-contract.mjs', '--repo-root', root]);

  assert.match(stdout, /Default StackKit-owned L3 PaaS contract check passed/);
});

test('check-l3-paas-contract rejects Dokploy promotion without parity evidence', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-paas-posture-fail-'));
  await mkdir(path.join(root, 'basement-kit'), { recursive: true });
  await writeFile(
    path.join(root, 'basement-kit', 'mode_matrix.cue'),
    `package base_kit

modeMatrix: {
  paas: {
    coolify: "default"
    komodo:  "supported"
    dokploy: "supported"
  }
}
`,
  );
  await writeFile(
    path.join(root, 'basement-kit', 'stackkit.yaml'),
    `changelog:
  - version: "5.0.0"
    changes:
      - "CHANGED: Komodo is the beta-supported PaaS alternative; Dokploy remains draft"
`,
  );

  await assert.rejects(
    execFileAsync(process.execPath, ['scripts/release/check-l3-paas-contract.mjs', '--repo-root', root]),
    /Dokploy must not be promoted into the v0\.4 beta-supported PaaS set/,
  );
});

test('check-l3-paas-contract rejects Coolify generated services with standalone Traefik entrypoints', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-fail-coolify-entrypoint-'));
  await writeModule(root, 'photos', validL3Module);
  const generated = path.join(root, 'main.tf');
  await writeFile(
    generated,
    `variable "enable_platform_fallback" {
  default = false
}
variable "platform_fallback_mode" {
  default = "disabled"
}
locals {
  coolify_api_endpoint       = "http://127.0.0.1:8000"
  coolify_root_email         = var.admin_email
  platform_fallback_standalone = var.enable_platform_fallback && var.platform_fallback_mode == "standalone-compose"
  direct_compose_deploy        = false
  platform_hub_fallback        = false
  l3_platform_adapter          = local.platform_fallback_standalone ? "none" : var.paas
  entrypoint                   = var.enable_https ? "websecure" : "web"
}
provider "docker" {
  host = var.docker_host != "" ? var.docker_host : "unix:///var/run/docker.sock"
}
resource "docker_container" "pocketid" {
  labels {
    label = "traefik.docker.network"
    value = local.traefik_network_name
  }
}
resource "null_resource" "coolify_platform_bootstrap" {
  provisioner "local-exec" {
    command = "traefik.docker.network=\${local.routing_network} STACKKIT_COOLIFY_PLATFORM_JSON=... STACKKIT_COOLIFY_SERVER_PUBLIC_KEY= authorized_keys server_settings set is_reachable = true, is_usable = true host.docker.internal --providers.docker.endpoint= 'id' => 0 Hash::make($bootstrapPassword) show_boarding' => false is_api_enabled' => true is_registration_enabled' => false createToken($tokenName, ['root']) StartProxy::run($server, async: false, force: true) proxyContainer' => 'coolify-proxy' .stackkit/platform.json"
  }
}
resource "null_resource" "coolify_install" {
  provisioner "local-exec" {
    command = "docker context create stackkit-host --docker "host=\${var.docker_host}" && docker context use default >/dev/null 2>&1 || true && echo Setting Docker CLI default context for Coolify runtime actions && docker context use stackkit-host"
  }
}
resource "local_file" "platform_l3_manifest" {
  content = jsonencode({
    apps = [{
      name = "photos"
      ownership = "stackkit"
      managedBy = local.l3_platform_adapter
    }]
  })
}
`,
  );

  await assert.rejects(
    execFileAsync(process.execPath, [
      'scripts/release/check-l3-paas-contract.mjs',
      '--repo-root',
      root,
      '--generated',
      generated,
    ]),
    /must not hard-code standalone Traefik entrypoints/,
  );
});

test('check-l3-paas-contract rejects compose-managed StackKit-owned L3 modules', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-fail-module-'));
  await writeModule(
    root,
    'photos',
    validL3Module.replace('"stackkit.managed-by": "selected-paas"', '"stackkit.managed-by": "compose"'),
  );

  await assert.rejects(
    execFileAsync(process.execPath, ['scripts/release/check-l3-paas-contract.mjs', '--repo-root', root]),
    /stackkit\.managed-by="compose"/,
  );
});

test('check-l3-paas-contract rejects generated direct-compose default L3 starts', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-fail-generated-'));
  await writeModule(root, 'photos', validL3Module);
  const generated = path.join(root, 'main.tf');
  await writeFile(
    generated,
    `variable "enable_platform_fallback" {
  default = false
}
variable "platform_fallback_mode" {
  default = "disabled"
}
locals {
  coolify_api_endpoint       = "http://127.0.0.1:8000"
  coolify_root_email         = var.admin_email
  platform_fallback_standalone = var.enable_platform_fallback && var.platform_fallback_mode == "standalone-compose"
  direct_compose_deploy        = false
  platform_hub_fallback        = false
  l3_platform_adapter          = local.platform_fallback_standalone ? "none" : var.paas
}
provider "docker" {
  host = var.docker_host != "" ? var.docker_host : "unix:///var/run/docker.sock"
}
resource "null_resource" "coolify_platform_bootstrap" {
  provisioner "local-exec" {
    command = "STACKKIT_COOLIFY_PLATFORM_JSON=... STACKKIT_COOLIFY_SERVER_PUBLIC_KEY= authorized_keys server_settings set is_reachable = true, is_usable = true host.docker.internal --providers.docker.endpoint= 'id' => 0 Hash::make($bootstrapPassword) show_boarding' => false is_api_enabled' => true is_registration_enabled' => false createToken($tokenName, ['root']) StartProxy::run($server, async: false, force: true) proxyContainer' => 'coolify-proxy' .stackkit/platform.json"
  }
}
resource "null_resource" "coolify_install" {
  provisioner "local-exec" {
    command = "docker context create stackkit-host --docker "host=\${var.docker_host}" && docker context use default >/dev/null 2>&1 || true && echo Setting Docker CLI default context for Coolify runtime actions && docker context use stackkit-host"
  }
}
resource "local_file" "platform_l3_manifest" {
  content = jsonencode({
    apps = [{
      name = "photos"
      ownership = "stackkit"
    }]
  })
}
resource "null_resource" "deploy_photos" {
  provisioner "local-exec" {
    command = "DOCKER_HOST=\${var.docker_host} docker compose -f \${local_file.photos_compose[0].filename} -p stackkit-photos up -d"
  }
}
`,
  );

  await assert.rejects(
    execFileAsync(process.execPath, [
      'scripts/release/check-l3-paas-contract.mjs',
      '--repo-root',
      root,
      '--generated',
      generated,
    ]),
    /docker compose instead of the PaaS adapter/,
  );
});

test('check-l3-paas-contract rejects active Coolify routing fallback markers', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-fail-coolify-router-'));
  await writeModule(root, 'photos', validL3Module);
  const generated = path.join(root, 'main.tf');
  await writeFile(
    generated,
    `variable "enable_platform_fallback" {
  default = false
}
variable "platform_fallback_mode" {
  default = "disabled"
}
locals {
  coolify_api_endpoint     = "http://127.0.0.1:8000"
  coolify_root_email       = var.admin_email
  coolify_stackkit_router = local.rp_coolify
  direct_compose_deploy   = true
  platform_hub_fallback   = var.enable_dashboard
  l3_platform_adapter     = var.paas
}
provider "docker" {
  host = var.docker_host != "" ? var.docker_host : "unix:///var/run/docker.sock"
}
resource "null_resource" "coolify_platform_bootstrap" {
  provisioner "local-exec" {
    command = "STACKKIT_COOLIFY_PLATFORM_JSON=... STACKKIT_COOLIFY_SERVER_PUBLIC_KEY= authorized_keys server_settings set is_reachable = true, is_usable = true host.docker.internal --providers.docker.endpoint= 'id' => 0 Hash::make($bootstrapPassword) show_boarding' => false is_api_enabled' => true is_registration_enabled' => false createToken($tokenName, ['root']) StartProxy::run($server, async: false, force: true) proxyContainer' => 'coolify-proxy' .stackkit/platform.json"
  }
}
resource "null_resource" "coolify_install" {
  provisioner "local-exec" {
    command = "docker context create stackkit-host --docker "host=\${var.docker_host}" && docker context use default >/dev/null 2>&1 || true && echo Setting Docker CLI default context for Coolify runtime actions && docker context use stackkit-host"
  }
}
resource "local_file" "platform_l3_manifest" {
  content = jsonencode({
    apps = [{
      name = "photos"
      ownership = "stackkit"
    }]
  })
}
`,
  );

  await assert.rejects(
    execFileAsync(process.execPath, [
      'scripts/release/check-l3-paas-contract.mjs',
      '--repo-root',
      root,
      '--generated',
      generated,
    ]),
    /Coolify default path must not include the StackKit-owned Coolify routing fallback/,
  );
});

test('check-l3-paas-contract rejects generated Coolify path without API bootstrap', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-fail-coolify-bootstrap-'));
  await writeModule(root, 'photos', validL3Module);
  const generated = path.join(root, 'main.tf');
  await writeFile(
    generated,
    `variable "enable_platform_fallback" {
  default = false
}
variable "platform_fallback_mode" {
  default = "disabled"
}
locals {
  coolify_api_endpoint       = "http://127.0.0.1:8000"
  coolify_root_email         = var.admin_email
  platform_fallback_standalone = var.enable_platform_fallback && var.platform_fallback_mode == "standalone-compose"
  direct_compose_deploy        = false
  platform_hub_fallback        = false
  l3_platform_adapter          = local.platform_fallback_standalone ? "none" : var.paas
}
provider "docker" {
  host = var.docker_host != "" ? var.docker_host : "unix:///var/run/docker.sock"
}
resource "local_file" "platform_l3_manifest" {
  content = jsonencode({
    apps = [{
      name = "photos"
      ownership = "stackkit"
      managedBy = local.l3_platform_adapter
    }]
  })
}
`,
  );

  await assert.rejects(
    execFileAsync(process.execPath, [
      'scripts/release/check-l3-paas-contract.mjs',
      '--repo-root',
      root,
      '--generated',
      generated,
    ]),
    /must bootstrap and persist Coolify API config/,
  );
});

test('check-l3-paas-contract rejects incomplete Coolify first-run bootstrap', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-l3-fail-coolify-first-run-'));
  await writeModule(root, 'photos', validL3Module);
  const generated = path.join(root, 'main.tf');
  await writeFile(
    generated,
    `variable "enable_platform_fallback" {
  default = false
}
variable "platform_fallback_mode" {
  default = "disabled"
}
locals {
  coolify_api_endpoint       = "http://127.0.0.1:8000"
  coolify_root_email         = var.admin_email
  platform_fallback_standalone = var.enable_platform_fallback && var.platform_fallback_mode == "standalone-compose"
  direct_compose_deploy        = false
  platform_hub_fallback        = false
  l3_platform_adapter          = local.platform_fallback_standalone ? "none" : var.paas
  traefik_http_entrypoint      = local.rp_coolify ? "http" : "web"
  traefik_https_entrypoint     = local.rp_coolify ? "https" : "websecure"
  entrypoint                   = var.enable_https ? local.traefik_https_entrypoint : local.traefik_http_entrypoint
  setup_immich_url             = local.is_host ? "http://127.0.0.1:\${local.host_ports.immich}" : (local.rp_coolify ? "http://immich-server:2283" : "http://immich:2283")
}
provider "docker" {
  host = var.docker_host != "" ? var.docker_host : "unix:///var/run/docker.sock"
}
resource "docker_container" "pocketid" {
  labels {
    label = "traefik.docker.network"
    value = local.traefik_network_name
  }
}
resource "local_file" "traefik_dynamic_stackkit" {
  content = "http: stackkit-coolify: rule: \"Host(\`\${local.domains.coolify}\`)\" service: stackkit-coolify url: \"http://coolify:8080\""
}
resource "local_file" "coolify_dynamic_stackkit" {
  filename = "/data/coolify/proxy/dynamic/stackkit.yml"
  content  = local_file.traefik_dynamic_stackkit[0].content
}
resource "null_resource" "coolify_platform_bootstrap" {
  provisioner "local-exec" {
    command = "stackkit_docker() { DOCKER_HOST=\"\${var.docker_host}\" docker \"$@\"; } stackkit_coolify_diagnostics() { echo Coolify readiness diagnostics (redacted):; } curl -fsS \${local.coolify_api_endpoint}/api/health curl -fsS \${local.coolify_api_endpoint}/health traefik.docker.network=\${local.routing_network} STACKKIT_COOLIFY_PLATFORM_JSON=... STACKKIT_COOLIFY_SERVER_PUBLIC_KEY= authorized_keys server_settings set is_reachable = true, is_usable = true host.docker.internal --providers.docker.endpoint= 'id' => 0 Hash::make($bootstrapPassword) is_api_enabled' => true createToken($tokenName, ['root']) StartProxy::run($server, async: false, force: true) proxyContainer' => 'coolify-proxy' .stackkit/platform.json"
  }
}
resource "null_resource" "coolify_install" {
  provisioner "local-exec" {
    command = "stackkit_preseed_coolify_image "postgres:15-alpine" "public.ecr.aws/docker/library/postgres:15-alpine" && stackkit_preseed_coolify_image "redis:7-alpine" "public.ecr.aws/docker/library/redis:7-alpine" && echo image already present locally for StackKit Coolify bootstrap && docker context create stackkit-host --docker "host=\${var.docker_host}" && docker context use default >/dev/null 2>&1 || true && echo Setting Docker CLI default context for Coolify runtime actions && docker context use stackkit-host"
  }
}
resource "local_file" "platform_l3_manifest" {
  content = jsonencode({
    apps = [{
      name = "photos"
      ownership = "stackkit"
      managedBy = local.l3_platform_adapter
    }]
  })
}
`,
  );

  await assert.rejects(
    execFileAsync(process.execPath, [
      'scripts/release/check-l3-paas-contract.mjs',
      '--repo-root',
      root,
      '--generated',
      generated,
    ]),
    /Coolify bootstrap must disable public registration/,
  );
});
