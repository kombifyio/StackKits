package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/kombifyio/stackkits/internal/composition"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// executeCommand runs the root command with the given args and captures
// cobra-buffered output. Commands that write directly to os.Stdout (e.g.
// version, completion) won't appear in the returned string; use
// executeCommandCaptureStdout for those.
func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	// Close deploy logger to release file handles (PostRun skips on error)
	if deployLog != nil {
		deployLog.Close()
		deployLog = nil
	}
	return buf.String(), err
}

// executeCommandCaptureStdout redirects os.Stdout so that commands using
// fmt.Printf / os.Stdout writes are captured. A goroutine drains the pipe
// concurrently to avoid blocking on Windows when output is large.
func executeCommandCaptureStdout(args ...string) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()

	orig := os.Stdout
	os.Stdout = w

	rootCmd.SetArgs(args)
	execErr := rootCmd.Execute()
	// Close deploy logger to release file handles (PostRun skips on error)
	if deployLog != nil {
		deployLog.Close()
		deployLog = nil
	}

	_ = w.Close()
	os.Stdout = orig
	<-done
	_ = r.Close()

	return buf.String(), execErr
}

func TestRootCommand_SubcommandsRegistered(t *testing.T) {
	expected := []string{
		"init", "prepare", "generate", "validate", "app",
		"plan", "apply", "remove", "status",
		"verify", "version", "completion",
		"doctor",
		"agent",
	}

	registered := make(map[string]bool)
	for _, cmd := range rootCmd.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range expected {
		assert.True(t, registered[name], "subcommand %q should be registered", name)
	}
}

func TestDoctorCommand_RegisteredAndDocumentsUpdateCheck(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"doctor"})
	require.NoError(t, err, "rootCmd.Find should locate the documented doctor subcommand")
	require.Equal(t, "doctor", cmd.Name())
	require.NotNil(t, cmd.Flag("check-updates"), "doctor must expose the documented --check-updates flag")
}

func TestRootCommand_GlobalFlags(t *testing.T) {
	tests := []struct {
		flag      string
		shorthand string
	}{
		{"verbose", "v"},
		{"quiet", "q"},
		{"chdir", "C"},
		{"spec", "s"},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			f := rootCmd.PersistentFlags().Lookup(tt.flag)
			require.NotNil(t, f, "flag --%s should exist", tt.flag)
			assert.Equal(t, tt.shorthand, f.Shorthand, "shorthand for --%s", tt.flag)
		})
	}
}

func TestVersionCommand(t *testing.T) {
	out, err := executeCommandCaptureStdout("version")
	require.NoError(t, err)
	assert.Contains(t, out, "stackkit version")
	assert.Contains(t, out, "Git commit:")
	assert.Contains(t, out, "Build date:")
	assert.Contains(t, out, "Go version:")
	assert.Contains(t, out, "OS/Arch:")
}

func TestInitCommand_NonInteractive_MissingName(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("init", "--non-interactive", "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-interactive")
}

func TestValidateCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	// validate returns an error when the spec file cannot be loaded (the
	// loader wraps the underlying error so os.IsNotExist does not match).
	_, err := executeCommand("validate", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestPlanCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("plan", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load spec")
}

func TestApplyCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("apply", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	// apply now attempts auto-init when spec is missing
	assert.True(t,
		strings.Contains(err.Error(), "failed to load spec") || strings.Contains(err.Error(), "no spec file"),
		"unexpected error: %s", err.Error())
}

func TestRemoveCommand_NoDeployDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal spec so the loader doesn't fail before the deploy-dir check.
	specPath := filepath.Join(tmpDir, "stack-spec.yaml")
	specContent := `name: test
stackkit: test-kit
mode: simple
`
	require.NoError(t, os.WriteFile(specPath, []byte(specContent), 0600))

	// Remove should succeed even without a deploy dir — falls back to Docker cleanup
	_, err := executeCommand("remove", "--auto-approve", "--spec", specPath, "--chdir", tmpDir)
	assert.NoError(t, err)
}

func TestCompletionCommand_RequiresShellArg(t *testing.T) {
	_, err := executeCommand("completion")
	require.Error(t, err)
}

func TestCompletionCommand_ValidShells(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			// Completion writes directly to os.Stdout. Drain the pipe
			// in a goroutine to prevent blocking on Windows when the
			// output exceeds the OS pipe buffer.
			r, w, err := os.Pipe()
			require.NoError(t, err)

			var buf bytes.Buffer
			done := make(chan struct{})
			go func() {
				_, _ = buf.ReadFrom(r)
				close(done)
			}()

			orig := os.Stdout
			os.Stdout = w

			rootCmd.SetArgs([]string{"completion", shell})
			execErr := rootCmd.Execute()

			_ = w.Close()
			os.Stdout = orig
			<-done
			_ = r.Close()

			assert.NoError(t, execErr)
			assert.Greater(t, buf.Len(), 0, "completion output should not be empty")
		})
	}
}

func TestStatusCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("status", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load spec")
}

func TestGenerateRandomPassword(t *testing.T) {
	pw, err := generateRandomPassword(16)
	require.NoError(t, err)
	assert.Len(t, pw, 16)

	// Should be alphanumeric only
	for _, c := range pw {
		assert.True(t, (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'),
			"unexpected character %q in password", c)
	}

	// Two passwords should differ (probabilistic but virtually certain for 16 chars)
	pw2, err := generateRandomPassword(16)
	require.NoError(t, err)
	assert.NotEqual(t, pw, pw2)
}

func TestResolveModulesDirPrefersWorkspaceModules(t *testing.T) {
	root := t.TempDir()
	stackkitDir := filepath.Join(root, "base-kit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	workspaceModules := filepath.Join(root, "modules")
	require.NoError(t, os.MkdirAll(workspaceModules, 0750))

	assert.Equal(t, workspaceModules, resolveModulesDir(stackkitDir, root))
}

func TestGenerateCommand_BaseKitDefaultSpecGoldenPath(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	outputRel := filepath.ToSlash(filepath.Join("build", "generate-command-"+strings.ReplaceAll(t.Name(), "/", "-")))
	outputAbs := filepath.Join(repoRoot, filepath.FromSlash(outputRel))
	require.NoError(t, os.RemoveAll(outputAbs))
	t.Cleanup(func() { _ = os.RemoveAll(outputAbs) })

	_, err = executeCommandCaptureStdout(
		"--no-log",
		"--spec", "base-kit/default-spec.yaml",
		"--chdir", repoRoot,
		"generate",
		"--output", outputRel,
		"--force",
	)
	require.NoError(t, err)

	for _, rel := range []string{
		"main.tf",
		"terraform.tfvars.json",
	} {
		path := filepath.Join(outputAbs, rel)
		info, statErr := os.Stat(path)
		require.NoError(t, statErr, "expected generated file %s", rel)
		assert.Greater(t, info.Size(), int64(0), "%s should not be empty", rel)
	}

	mainTFData, err := os.ReadFile(filepath.Join(outputAbs, "main.tf"))
	require.NoError(t, err)
	parser := hclparse.NewParser()
	_, diags := parser.ParseHCLFile(filepath.Join(outputAbs, "main.tf"))
	require.False(t, diags.HasErrors(), "generated main.tf should parse as HCL: %s", diags.Error())

	mainTF := string(mainTFData)
	assert.Contains(t, mainTF, `variable "tinyauth_users"`)
	assert.Contains(t, mainTF, `variable "tinyauth_oidc_enabled"`)
	assert.Contains(t, mainTF, `variable "enable_platform_fallback"`)
	assert.Contains(t, mainTF, `variable "platform_fallback_mode"`)
	assert.Contains(t, mainTF, `output "vaultwarden_admin_token"`)
	assert.Contains(t, mainTF, `platform_fallback_standalone = var.enable_platform_fallback && var.platform_fallback_mode == "standalone-compose"`)
	assert.Contains(t, mainTF, `platform_fallback_contract_valid = (`)
	assert.Contains(t, mainTF, `stackkit_traefik_enabled     = (var.enable_traefik && (local.rp_standalone || local.rp_stackkit)) || local.platform_fallback_standalone`)
	assert.Contains(t, mainTF, `dokploy_traefik_enabled      = var.enable_dokploy && local.rp_dokploy`)
	assert.Contains(t, mainTF, `direct_compose_deploy        = false`)
	assert.Contains(t, mainTF, `platform_adapter             = local.platform_fallback_standalone ? "none" : var.paas`)
	assert.Contains(t, mainTF, `l3_platform_adapter          = local.platform_fallback_standalone ? "none" : var.paas`)
	assert.Contains(t, mainTF, `traefik_http_entrypoint  = local.rp_coolify ? "http" : "web"`)
	assert.Contains(t, mainTF, `traefik_https_entrypoint = local.rp_coolify ? "https" : "websecure"`)
	assert.Contains(t, mainTF, `entrypoint               = var.enable_https ? local.traefik_https_entrypoint : local.traefik_http_entrypoint`)
	assert.Contains(t, mainTF, `traefik.docker.network=${local.routing_network}`)
	assert.Contains(t, mainTF, `label = "traefik.docker.network"`)
	assert.Contains(t, mainTF, `value = local.traefik_network_name`)
	assert.Contains(t, mainTF, `platform_hub_managed     = var.enable_dashboard`)
	assert.Contains(t, mainTF, `platform_hub_fallback    = false`)
	assert.Contains(t, mainTF, `resource "null_resource" "platform_fallback_contract"`)
	assert.Contains(t, mainTF, `enable_platform_fallback=false requires platform_fallback_mode=\"disabled\"`)
	assert.Contains(t, mainTF, `host = var.docker_host != "" ? var.docker_host : "unix:///var/run/docker.sock"`)
	assert.NotContains(t, mainTF, `coolify_stackkit_router`)
	assert.Contains(t, mainTF, `auth_middleware          = var.enable_tinyauth ? "tinyauth@docker" : ""`)
	assert.Contains(t, mainTF, `coolify_auth_middleware  = var.enable_tinyauth ? "tinyauth@file" : ""`)
	assert.Contains(t, mainTF, `service_auth_middleware  = local.rp_coolify ? local.coolify_auth_middleware : local.auth_middleware`)
	assert.Contains(t, mainTF, `tinyauth_forwardauth_address = local.is_host ? "http://127.0.0.1:${local.host_ports.tinyauth}/api/auth/traefik" : "http://tinyauth:3000/api/auth/traefik"`)
	assert.Contains(t, mainTF, `base_hub_bootstrap_open  = var.enable_dashboard && (local.is_localhost_domain || local.is_local_dns_domain) && !var.protect_base_hub`)
	assert.Contains(t, mainTF, `base_hub_uses_dynamic_protection = var.enable_tinyauth && (local.is_localhost_domain || local.is_local_dns_domain)`)
	assert.Contains(t, mainTF, `base_hub_auth_middleware = var.enable_tinyauth ? (local.base_hub_uses_dynamic_protection ? "base-hub-auth@file" : "tinyauth@docker") : ""`)
	assert.Contains(t, mainTF, `base_hub_unprotected     = var.enable_dashboard && (local.base_hub_bootstrap_open || local.base_hub_auth_middleware == "")`)
	assert.Contains(t, mainTF, `coolify_proxy_dynamic_enabled = local.rp_coolify && var.enable_coolify`)
	assert.Contains(t, mainTF, `stackkit_dynamic_middleware_enabled = local.stackkit_traefik_enabled || local.base_hub_uses_dynamic_protection || local.coolify_proxy_dynamic_enabled`)
	assert.Contains(t, mainTF, `local.rp_dokploy ? docker_network.dokploy_network[0].name`)
	assert.Contains(t, mainTF, `resource "docker_network" "dokploy_network"`)
	assert.Contains(t, mainTF, `count = (!local.is_host && (var.enable_dokploy || local.rp_dokploy)) ? 1 : 0`)
	assert.Contains(t, mainTF, `count      = (!local.is_host && local.rp_coolify && !local.platform_fallback_standalone) ? 1 : 0`)
	assert.Contains(t, mainTF, `count = local.stackkit_traefik_enabled ? 1 : 0`)
	assert.NotContains(t, mainTF, `resource "docker_container" "coolify_dashboard_route"`)
	assert.NotContains(t, mainTF, `stackkit-coolify-route`)
	assert.Contains(t, mainTF, `coolify_root_email                = var.admin_email`)
	assert.Contains(t, mainTF, `setup_immich_url = local.is_host ? "http://127.0.0.1:${local.host_ports.immich}" : (local.rp_coolify ? "http://immich-server:2283" : "http://immich:2283")`)
	assert.Contains(t, mainTF, `ROOT_USER_EMAIL="${local.coolify_root_email}"`)
	assert.Contains(t, mainTF, `DOCKER_HOST="${var.docker_host}"`)
	assert.Contains(t, mainTF, `DOCKER_ADDRESS_POOL_BASE="172.30.0.0/16"`)
	assert.Contains(t, mainTF, `DOCKER_POOL_FORCE_OVERRIDE=true`)
	assert.Contains(t, mainTF, `COOLIFY_SYSTEMCTL_SHIM="$(mktemp -d)"`)
	assert.Contains(t, mainTF, `COOLIFY_REAL_DOCKER="$(command -v docker)"`)
	assert.Contains(t, mainTF, `cat > "$COOLIFY_SYSTEMCTL_SHIM/service" <<'EOS'`)
	assert.Contains(t, mainTF, `cat > "$COOLIFY_SYSTEMCTL_SHIM/rc-update" <<'EOS'`)
	assert.Contains(t, mainTF, `cat > "$COOLIFY_SYSTEMCTL_SHIM/sshd" <<'EOS'`)
	assert.Contains(t, mainTF, `echo "permitrootlogin yes"`)
	assert.Contains(t, mainTF, `stackkit_preseed_coolify_image "postgres:15-alpine" "public.ecr.aws/docker/library/postgres:15-alpine"`)
	assert.Contains(t, mainTF, `stackkit_preseed_coolify_image "redis:7-alpine" "public.ecr.aws/docker/library/redis:7-alpine"`)
	assert.Contains(t, mainTF, `image already present locally for StackKit Coolify bootstrap`)
	assert.Contains(t, mainTF, `Coolify installer exited without creating the coolify container`)
	assert.Contains(t, mainTF, `PATH="$COOLIFY_INSTALL_PATH"`)
	assert.Contains(t, mainTF, `docker context create stackkit-host --docker "host=${var.docker_host}"`)
	assert.Contains(t, mainTF, `docker context use default >/dev/null 2>&1 || true`)
	assert.Contains(t, mainTF, `Setting Docker CLI default context for Coolify runtime actions`)
	assert.Contains(t, mainTF, `docker context use stackkit-host`)
	assert.Contains(t, mainTF, `coolify_docker_host_name          = startswith(var.docker_host, "tcp://") ? split(":", trimprefix(var.docker_host, "tcp://"))[0] : ""`)
	assert.Contains(t, mainTF, `coolify_api_endpoint              = local.coolify_local_endpoint`)
	assert.Contains(t, mainTF, `coolify_bootstrap_api_endpoint    = local.coolify_docker_host_name != "" ? "http://${local.coolify_docker_host_name}:8000" : local.coolify_api_endpoint`)
	assert.Contains(t, mainTF, `"${local.coolify_bootstrap_api_endpoint}/api/health"`)
	assert.Contains(t, mainTF, `"${local.coolify_bootstrap_api_endpoint}/health"`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_API_ENDPOINT="${local.coolify_api_endpoint}"`)
	assert.Contains(t, mainTF, `Coolify runtime container is missing after installer phase`)
	assert.Contains(t, mainTF, `after 5 minutes`)
	assert.NotContains(t, mainTF, `after 30 minutes`)
	assert.Contains(t, mainTF, `stackkit_coolify_diagnostics()`)
	assert.Contains(t, mainTF, `Coolify readiness diagnostics (redacted):`)
	assert.Contains(t, mainTF, `resource "local_file" "coolify_platform_bootstrap_php"`)
	assert.Contains(t, mainTF, `resource "null_resource" "coolify_platform_bootstrap"`)
	assert.Contains(t, mainTF, `<?php`)
	assert.Contains(t, mainTF, `'id' => 0,`)
	assert.Contains(t, mainTF, `'password' => \Illuminate\Support\Facades\Hash::make($bootstrapPassword),`)
	assert.Contains(t, mainTF, `$team->forceFill(['show_boarding' => false])->save();`)
	assert.Contains(t, mainTF, `'is_api_enabled' => true,`)
	assert.Contains(t, mainTF, `'is_registration_enabled' => false,`)
	assert.Contains(t, mainTF, `$token = $user->createToken($tokenName, ['root']);`)
	assert.Contains(t, mainTF, `\App\Actions\Proxy\StartProxy::run($server, async: false, force: true);`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_ALLOW_PROXY_FALLBACK="true"`)
	assert.Contains(t, mainTF, `WARN: StackKit will start a Docker-level Coolify proxy fallback`)
	assert.Contains(t, mainTF, `'proxyContainer' => 'coolify-proxy',`)
	assert.Contains(t, mainTF, `'projectUuid' => $project->uuid,`)
	assert.Contains(t, mainTF, `'environmentUuid' => $environment->uuid,`)
	assert.Contains(t, mainTF, `'serverId' => $server->uuid,`)
	assert.Contains(t, mainTF, `'destinationUuid' => $destination->uuid,`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_PLATFORM_JSON=`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_ROOT_EMAIL="${local.coolify_root_email}"`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_SERVER_USER="root"`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_SERVER_IP="host.docker.internal"`)
	assert.Contains(t, mainTF, `stackkit_docker exec coolify getent hosts "$STACKKIT_COOLIFY_SERVER_IP"`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_SERVER_IP="$(stackkit_docker network inspect coolify --format '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null || true)"`)
	assert.Contains(t, mainTF, `Using Coolify server SSH target $STACKKIT_COOLIFY_SERVER_IP`)
	assert.Contains(t, mainTF, `stackkit_docker exec -u 0 coolify chmod 0644 /tmp/stackkit-coolify-platform-bootstrap.php`)
	assert.Contains(t, mainTF, `php artisan tinker --execute="require \"/tmp/stackkit-coolify-platform-bootstrap.php\";"`)
	assert.Contains(t, mainTF, `stackkit_write_coolify_proxy_fallback()`)
	assert.Contains(t, mainTF, `stackkit_coolify_proxy_needs_reconcile()`)
	assert.Contains(t, mainTF, `stackkit_sync_coolify_dynamic_config()`)
	assert.Contains(t, mainTF, `cat > /data/coolify/proxy/dynamic/stackkit.yml`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_DYNAMIC_TARGET="/data/coolify/proxy/dynamic/stackkit.yml"`)
	assert.Contains(t, mainTF, `if [ "$STACKKIT_COOLIFY_DYNAMIC_SOURCE_REAL" != "$STACKKIT_COOLIFY_DYNAMIC_TARGET_REAL" ]; then`)
	assert.Contains(t, mainTF, `Coolify proxy compose file missing at $PROXY_COMPOSE; creating StackKit-managed proxy fallback`)
	assert.Contains(t, mainTF, `Coolify proxy compose is missing StackKit routing/TLS settings; replacing it with StackKit-managed proxy fallback`)
	assert.Contains(t, mainTF, `image: ghcr.io/traefik/traefik:v3`)
	assert.Contains(t, mainTF, `container_name: coolify-proxy`)
	assert.Contains(t, mainTF, `--certificatesresolvers.letsencrypt.acme.storage=/traefik/acme/acme.json`)
	assert.Contains(t, mainTF, `--entrypoints.https.http.tls=true`)
	assert.Contains(t, mainTF, `--entrypoints.https.http.tls.certresolver=letsencrypt`)
	assert.Contains(t, mainTF, `--certificatesresolvers.letsencrypt.acme.httpchallenge=true`)
	assert.Contains(t, mainTF, `--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=http`)
	assert.Contains(t, mainTF, `DOCKER_API_VERSION=1.44`)
	assert.Contains(t, mainTF, `CF_API_KEY=${var.dns_api_token}`)
	assert.Contains(t, mainTF, `CF_DNS_API_TOKEN=${var.dns_api_token}`)
	assert.Contains(t, mainTF, `--providers.file.directory=/traefik/dynamic`)
	assert.Contains(t, mainTF, `/data/coolify/proxy/dynamic:/traefik/dynamic:ro`)
	assert.Contains(t, mainTF, `/data/coolify/proxy/acme:/traefik/acme`)
	assert.Contains(t, mainTF, `touch /data/coolify/proxy/acme/acme.json`)
	assert.Contains(t, mainTF, `Coolify proxy is running with StackKit routing/TLS settings`)
	assert.Contains(t, mainTF, `grep -q -- "--providers.docker.endpoint="`)
	assert.Contains(t, mainTF, `grep -q -- "--entrypoints.https.http.tls.certresolver=letsencrypt" "$PROXY_COMPOSE" || return 0`)
	assert.Contains(t, mainTF, `stackkit_docker compose -f "$PROXY_COMPOSE" up -d`)
	assert.Contains(t, mainTF, `Coolify proxy compose failed; replacing with StackKit-managed proxy fallback`)
	assert.Contains(t, mainTF, `local_file.coolify_dynamic_stackkit`)
	assert.Contains(t, mainTF, `STACKKIT_COOLIFY_SERVER_PUBLIC_KEY=`)
	assert.Contains(t, mainTF, `/root/.ssh/authorized_keys`)
	assert.Contains(t, mainTF, `-v /root/.ssh:/target-ssh`)
	assert.Contains(t, mainTF, `server_settings set is_reachable = true, is_usable = true`)
	assert.Contains(t, mainTF, `PLATFORM_CONFIG_PATH="${path.module}/.stackkit/platform.json"`)
	assert.Contains(t, mainTF, `chmod 600 "$PLATFORM_CONFIG_PATH"`)
	assert.Contains(t, mainTF, `depends_on = [
    null_resource.coolify_install,
    local_file.coolify_dynamic_stackkit,
    local_file.coolify_platform_bootstrap_php,
  ]`)
	assert.Contains(t, mainTF, `output "coolify_admin_email"`)
	assert.Contains(t, mainTF, `resource "local_file" "stackkit_hub_compose"`)
	assert.Contains(t, mainTF, `resource "local_file" "stackkit_server_compose"`)
	assert.Contains(t, mainTF, `resource "local_file" "traefik_dynamic_stackkit"`)
	assert.Contains(t, mainTF, `count           = local.stackkit_dynamic_middleware_enabled ? 1 : 0`)
	assert.Contains(t, mainTF, `resource "docker_container" "dokploy_traefik"`)
	assert.Contains(t, mainTF, `name       = "dokploy-traefik"`)
	assert.Contains(t, mainTF, `--providers.docker.network=${local.traefik_network_name}`)
	assert.Contains(t, mainTF, `docker ps --filter "name=dokploy-traefik" --filter "status=running" -q`)
	assert.Contains(t, mainTF, `stackkit-coolify:`)
	assert.Contains(t, mainTF, "rule: \"Host(`${local.domains.coolify}`)\"")
	assert.Contains(t, mainTF, `- "${local.coolify_auth_middleware}"`)
	assert.Contains(t, mainTF, `tinyauth:`)
	assert.Contains(t, mainTF, `address: "${local.tinyauth_forwardauth_address}"`)
	assert.Contains(t, mainTF, `service: stackkit-coolify`)
	assert.Contains(t, mainTF, `- url: "http://coolify:8080"`)
	assert.Contains(t, mainTF, `resource "local_file" "coolify_dynamic_stackkit"`)
	assert.Contains(t, mainTF, `count           = local.coolify_proxy_dynamic_enabled ? 1 : 0`)
	assert.Contains(t, mainTF, `filename        = "/data/coolify/proxy/dynamic/stackkit.yml"`)
	assert.Contains(t, mainTF, `content         = local_file.traefik_dynamic_stackkit[0].content`)
	assert.Contains(t, mainTF, `--providers.file.directory=/etc/traefik/dynamic`)
	assert.Contains(t, mainTF, `resource "docker_container" "dashboard"`)
	assert.Contains(t, mainTF, `count = var.enable_dashboard && local.platform_hub_fallback ? 1 : 0`)
	assert.Contains(t, mainTF, `traefik.http.routers.dashboard.middlewares=${local.base_hub_auth_middleware}`)
	assert.Contains(t, mainTF, `traefik.http.routers.stackkit-server.middlewares=${local.base_hub_auth_middleware}`)
	assert.Contains(t, mainTF, `traefik.http.routers.vaultwarden.middlewares=${local.service_auth_middleware}`)
	assert.Contains(t, mainTF, `traefik.http.routers.immich.middlewares=${local.service_auth_middleware}`)
	assert.Contains(t, mainTF, `coolify.traefik.middlewares=${local.service_auth_middleware}`)
	assert.Contains(t, mainTF, `entrypoint = ["stackkit-server"]`)
	assert.NotContains(t, mainTF, `"stackkit-server",`+"\n"+`    "--port"`)
	assert.Contains(t, mainTF, `version    = "stackkit.platform-apps/v2"`)
	assert.Contains(t, mainTF, `platform   = local.l3_platform_adapter`)
	assert.Contains(t, mainTF, `managedBy   = local.platform_adapter`)
	assert.Contains(t, mainTF, `ownership   = "stackkit"`)
	assert.Contains(t, mainTF, `managedBy   = local.l3_platform_adapter`)
	assert.Contains(t, mainTF, `systemApps = concat(`)
	assert.Contains(t, mainTF, `fallback   = {`)
	assert.Contains(t, mainTF, `enabled = local.platform_fallback_standalone`)
	assert.Contains(t, mainTF, `mode    = var.platform_fallback_mode`)
	assert.Contains(t, mainTF, `workspace_root = abspath("${path.module}/..")`)
	assert.Contains(t, mainTF, `name        = "stackkit-hub"`)
	assert.Contains(t, mainTF, `role        = "node-hub"`)
	assert.Contains(t, mainTF, `name        = "stackkit-server"`)
	assert.Contains(t, mainTF, `role        = "node-api"`)
	assert.Contains(t, mainTF, `Diese Seite ist aktuell ungeschützt.`)
	assert.Contains(t, mainTF, `Base Hub schützen`)
	assert.NotContains(t, mainTF, `Nach dem Owner-Setup <code>protect_base_hub=true</code>`)
	assert.Contains(t, mainTF, `source: ${local.workspace_root}`)
	assert.Contains(t, mainTF, `source = local.workspace_root`)
	assert.Contains(t, mainTF, `name: ${local.routing_network}`)
	assert.Contains(t, mainTF, `resource "docker_container" "homepage"`)
	assert.Contains(t, mainTF, `resource "docker_container" "homepage_socket_proxy"`)
	assert.Contains(t, mainTF, `resource "local_file" "homepage_services"`)
	assert.Contains(t, mainTF, `homepage_services_yaml = yamlencode(local.homepage_services)`)
	assert.Contains(t, mainTF, `homepage_container_names = {`)
	assert.Contains(t, mainTF, `base    = "dashboard"`)
	assert.Contains(t, mainTF, `kuma    = "kuma"`)
	assert.Contains(t, mainTF, `server      = "stackkit"`)
	assert.Contains(t, mainTF, `container   = lookup(local.homepage_container_names, "base", "dashboard")`)
	assert.Contains(t, mainTF, `container   = lookup(local.homepage_container_names, "photos", "immich")`)
	assert.NotContains(t, mainTF, `ping        = "${local.proto}://${local.domains`)
	assert.Contains(t, mainTF, `HOMEPAGE_ALLOWED_HOSTS=${local.domains.home},localhost,127.0.0.1`)
	assert.Contains(t, mainTF, `TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTID`)
	assert.Contains(t, mainTF, `TINYAUTH_OAUTH_AUTOREDIRECT=pocketid`)
	assert.Contains(t, mainTF, `variable "pocketid_app_url"`)
	assert.Contains(t, mainTF, `variable "docker_host"`)
	assert.Contains(t, mainTF, `variable "enable_whoami"`)
	assert.Contains(t, mainTF, `variable "vaultwarden_image"`)
	assert.Contains(t, mainTF, `ghcr.io/dani-garcia/vaultwarden:latest`)
	assert.Contains(t, mainTF, `variable "immich_postgres_image"`)
	assert.Contains(t, mainTF, `ghcr.io/immich-app/postgres:16-vectorchord0.3.0-pgvectors0.3.0`)
	assert.Contains(t, mainTF, `variable "immich_redis_image"`)
	assert.Contains(t, mainTF, `ghcr.io/valkey-io/valkey:9`)
	assert.NotContains(t, mainTF, `init-immich:`)
	assert.NotContains(t, mainTF, `IMMICH_USER: "${var.admin_email}"`)
	assert.NotContains(t, mainTF, `IMMICH_PASS: "${var.admin_password_plaintext}"`)
	assert.NotContains(t, mainTF, `/api/auth/admin-sign-up`)
	assert.NotContains(t, mainTF, `/api/users/me/onboarding`)
	assert.NotContains(t, mainTF, `/api/system-metadata/admin-onboarding`)
	assert.Contains(t, mainTF, `setupPolicy = "on_demand"`)
	assert.Contains(t, mainTF, `name        = "immich-owner-bootstrap"`)
	assert.Contains(t, mainTF, `runner      = "stackkit-script"`)
	assert.Contains(t, mainTF, `depends_on = [
    docker_container.traefik,`)
	assert.Contains(t, mainTF, `null_resource.coolify_traefik_ready`)
	assert.Contains(t, mainTF, `null_resource.coolify_platform_bootstrap`)
	assert.NotContains(t, mainTF, `null_resource.dokploy_traefik_ready`)
	assert.Contains(t, mainTF, `Coolify Traefik is ready`)
	assert.Contains(t, mainTF, `ERROR: Coolify routing backend not detected after 5 minutes`)
	assert.NotContains(t, mainTF, `StackKit Traefik ready for Coolify-backed routing`)
	assert.NotContains(t, mainTF, `Coolify app is ready for StackKit routing`)
	assert.Contains(t, mainTF, `command     = "echo StackKit platform adapter will deploy ${local_file.kuma_compose[0].filename}"`)
	assert.Contains(t, mainTF, `command     = "echo StackKit platform adapter will deploy ${local_file.whoami_compose[0].filename}"`)
	assert.NotContains(t, mainTF, `docker compose -f ${local_file.kuma_compose[0].filename} -p stackkit-uptime-kuma up -d`)
	assert.NotContains(t, mainTF, `docker compose -f ${local_file.whoami_compose[0].filename} -p stackkit-whoami up -d`)
	assert.Contains(t, mainTF, `command     = "echo StackKit platform adapter will deploy ${local_file.vaultwarden_compose[0].filename}"`)
	assert.Contains(t, mainTF, `command     = "echo StackKit platform adapter will deploy ${local_file.jellyfin_compose[0].filename}"`)
	assert.Contains(t, mainTF, `command     = "echo StackKit platform adapter will deploy ${local_file.immich_compose[0].filename}"`)
	assert.NotContains(t, mainTF, `docker compose -f ${local_file.vaultwarden_compose[0].filename} -p stackkit-vaultwarden up -d`)
	assert.NotContains(t, mainTF, `docker compose -f ${local_file.jellyfin_compose[0].filename} -p stackkit-jellyfin up -d`)
	assert.NotContains(t, mainTF, `docker compose -f ${local_file.immich_compose[0].filename} -p stackkit-immich up -d`)
	assert.Contains(t, mainTF, `ERROR: reverse proxy (${var.reverse_proxy_backend}) not detected after 3 minutes`)
	assert.Contains(t, mainTF, `exit 1`)
	assert.Contains(t, mainTF, `value       = var.enable_uptime_kuma ? "${local.proto}://${local.domains.kuma}" : null`)
	assert.Contains(t, mainTF, `value       = var.enable_uptime_kuma ? try(random_password.kuma_admin[0].result, "") : null`)
	assert.Contains(t, mainTF, `var.enable_uptime_kuma ? { kuma = local.domains.kuma } : {}`)
	assert.Contains(t, mainTF, `var.enable_whoami ? { whoami = local.domains.whoami } : {}`)
	assert.Contains(t, mainTF, `var.enable_homepage ? { home = local.domains.home } : {}`)
	assert.Contains(t, mainTF, `var.enable_dokploy ? { dokploy = local.domains.dokploy } : {}`)
	assert.Contains(t, mainTF, `KUMA_PASS: "${var.enable_uptime_kuma ? try(random_password.kuma_admin[0].result, "") : ""}"`)
	assert.Contains(t, mainTF, `DB_PASSWORD=${try(random_password.immich_db[0].result, "")}`)
	assert.Contains(t, mainTF, `api.set_settings(`)
	assert.Contains(t, mainTF, `password=pw,`)
	assert.Contains(t, mainTF, `disableAuth=True,`)
	assert.Contains(t, mainTF, `trustProxy=True,`)
	assert.Contains(t, mainTF, `resource "local_file" "whoami_compose"`)
	assert.Contains(t, mainTF, `count = var.enable_whoami ? 1 : 0`)
	assert.Contains(t, mainTF, `${var.enable_uptime_kuma ? "<a href=\"${local.proto}://${local.domains.kuma}\" target=\"_blank\" rel=\"noreferrer\">Status</a>" : ""}`)
	assert.Contains(t, mainTF, `${var.enable_uptime_kuma ? "<li><span class=\"num\">4</span><span>Check <a href=\"${local.proto}://${local.domains.kuma}\"`)
	assert.Contains(t, mainTF, `${var.enable_uptime_kuma ? "<div class=\"service-row\"><a class=\"service-main\" href=\"${local.proto}://${local.domains.kuma}\"`)
	assert.Contains(t, mainTF, `${var.enable_whoami ? "<div class=\"service-row\"><a class=\"service-main\" href=\"${local.proto}://${local.domains.whoami}\"`)
	assert.Contains(t, mainTF, `https://docs.kombify.io/guides/stackkits/services/uptime-kuma`)
	assert.Contains(t, mainTF, `https://docs.kombify.io/guides/stackkits/services/whoami`)
	assert.NotContains(t, mainTF, `${var.enable_uptime_kuma ? "✓" : "✗"} Kuma`)
	assert.NotContains(t, mainTF, `${var.enable_whoami ? "✓" : "✗"} Whoami`)
	assert.Contains(t, mainTF, `${var.enable_dokploy ? format("║    ✓ Dokploy`)
	assert.Contains(t, mainTF, `${var.enable_coolify ? format("║    ✓ Coolify`)
	assert.Contains(t, mainTF, `${var.enable_dockge ? format("║    ✓ Dockge`)
	assert.Contains(t, mainTF, `${var.enable_uptime_kuma ? format("║    ✓ Kuma`)
	assert.Contains(t, mainTF, `${var.enable_whoami ? format("║    ✓ Whoami`)
	assert.Contains(t, mainTF, `${(var.enable_dokploy || var.enable_komodo || var.enable_coolify) ? format("║  3. PaaS:`)

	_, statErr := os.Stat(filepath.Join(outputAbs, "immich.tf"))
	assert.True(t, os.IsNotExist(statErr), "default production generation should use the stable Base Kit template, not experimental fragments")
	_, statErr = os.Stat(filepath.Join(outputAbs, "pocketid.tf"))
	assert.True(t, os.IsNotExist(statErr), "stable Base Kit generation should keep PocketID in main.tf instead of experimental fragments")

	moduleFiles, err := os.ReadDir(outputAbs)
	require.NoError(t, err)
	for _, entry := range moduleFiles {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".tf" {
			continue
		}
		if entry.Name() == "main.tf" {
			// The stable template legitimately embeds Docker CLI Go templates
			// such as docker ps --format '{{.Names}}' inside shell snippets.
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(outputAbs, entry.Name()))
		require.NoError(t, readErr)
		assert.NotContains(t, string(data), "{{.", "%s should not contain unresolved template placeholders", entry.Name())
	}

	tfvarsData, err := os.ReadFile(filepath.Join(outputAbs, "terraform.tfvars.json"))
	require.NoError(t, err)
	var tfvars map[string]any
	require.NoError(t, json.Unmarshal(tfvarsData, &tfvars))
	assert.NotEmpty(t, tfvars["tinyauth_users"])
	assert.NotEmpty(t, tfvars["vaultwarden_admin_token"])
	assert.Equal(t, "UTC", tfvars["timezone"])
	assert.Equal(t, true, tfvars["enable_pocketid"])
	assert.Equal(t, true, tfvars["enable_homepage"])
	assert.True(t, boolVar(t, tfvars, "enable_uptime_kuma"))
	assert.True(t, boolVar(t, tfvars, "enable_whoami"))
	assert.Equal(t, false, tfvars["enable_platform_fallback"])
	assert.Equal(t, "disabled", tfvars["platform_fallback_mode"])
	assert.Equal(t, "http://id.home.localhost", tfvars["pocketid_app_url"])
	assert.Equal(t, true, tfvars["tinyauth_oidc_enabled"])
	assert.Equal(t, "http://id.home.localhost", tfvars["tinyauth_oidc_issuer"])
	assert.NotEmpty(t, tfvars["tinyauth_oidc_client_id"])
}

func TestGenerateCommandRejectsPocketIDDisableWithoutPasskeyProvider(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	workDir := filepath.Join(repoRoot, ".tmp-generate-invalid-identity-"+strings.ReplaceAll(t.Name(), "/", "-"))
	require.NoError(t, os.MkdirAll(workDir, 0750))
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	specPath := filepath.Join(workDir, "stack-spec.yaml")
	spec := `name: invalid-identity
stackkit: base-kit
mode: simple
domain: home.localhost
services:
  pocketid:
    enabled: false
`
	require.NoError(t, os.WriteFile(specPath, []byte(spec), 0600))

	_, err = executeCommandCaptureStdout(
		"--no-log",
		"--spec", specPath,
		"--chdir", repoRoot,
		"generate",
		"--output", filepath.ToSlash(filepath.Join("build", "invalid-identity-"+strings.ReplaceAll(t.Name(), "/", "-"))),
		"--force",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pocketid cannot be disabled")
}

func TestPocketIDSetupLinksUseSupportedRoute(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	for _, rel := range []string{
		"base-install.sh",
		filepath.Join("base-kit", "templates", "simple", "main.tf"),
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		require.NoError(t, err, rel)
		content := string(data)
		assert.NotContains(t, content, "/login/setup", "%s must not point at PocketID's removed route", rel)
		assert.Contains(t, content, "/setup", "%s should point at PocketID's supported initial setup route", rel)
	}
}

func TestTinyAuthPocketIDUsesInternalServerSideOIDCEndpoints(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "templates", "simple", "main.tf"))
	require.NoError(t, err)
	mainTF := string(data)

	assert.Contains(t, mainTF, `TINYAUTH_OAUTH_PROVIDERS_POCKETID_AUTHURL=${var.tinyauth_oidc_issuer}/authorize`)
	assert.Contains(t, mainTF, `TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL=${var.tinyauth_app_url}/api/oauth/callback/pocketid`)
	assert.Contains(t, mainTF, `pocketid_internal_oidc_origin`)
	assert.Contains(t, mainTF, `"http://pocketid:1411"`)
	assert.Contains(t, mainTF, `"http://127.0.0.1:${local.host_ports.pocketid}"`)
	assert.Contains(t, mainTF, `TINYAUTH_OAUTH_PROVIDERS_POCKETID_TOKENURL=${local.pocketid_internal_oidc_origin}/api/oidc/token`)
	assert.Contains(t, mainTF, `TINYAUTH_OAUTH_PROVIDERS_POCKETID_USERINFOURL=${local.pocketid_internal_oidc_origin}/api/oidc/userinfo`)
}

func TestTinyAuthPocketIDDoesNotRestrictOAuthToAdminEmailByDefault(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "templates", "simple", "main.tf"))
	require.NoError(t, err)
	mainTF := string(data)

	assert.Contains(t, mainTF, `variable "tinyauth_oauth_whitelist"`)
	assert.Contains(t, mainTF, `TINYAUTH_OAUTH_WHITELIST=${var.tinyauth_oauth_whitelist}`)
	assert.NotContains(t, mainTF, `TINYAUTH_OAUTH_WHITELIST=${var.admin_email}`)
}

func TestKomodoBootstrapUsesLoopbackAndRedactedJSONParsing(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "templates", "simple", "main.tf"))
	require.NoError(t, err)
	mainTF := string(data)

	assert.Contains(t, mainTF, `ip       = "127.0.0.1"`)
	assert.Contains(t, mainTF, `KOMODO_INIT_ADMIN_USERNAME=stackkits-admin`)
	assert.Contains(t, mainTF, `KOMODO_DISABLE_USER_REGISTRATION=true`)
	assert.Contains(t, mainTF, `KOMODO_ENABLE_NEW_USERS=false`)
	assert.Contains(t, mainTF, `user = "0:0"`)
	assert.Contains(t, mainTF, `container_docker_host`)
	assert.Contains(t, mainTF, `DOCKER_HOST=${local.container_docker_host}`)
	assert.Contains(t, mainTF, `host = "host.docker.internal"`)
	assert.Contains(t, mainTF, `ip   = "host-gateway"`)
	assert.Contains(t, mainTF, `KOMODO_ADMIN_PASSWORD = var.admin_password_plaintext`)
	assert.Contains(t, mainTF, `/auth/login`)
	assert.Contains(t, mainTF, `json_field data.jwt`)
	assert.Contains(t, mainTF, `post_komodo_json auth/manage CreateApiKey`)
	assert.Contains(t, mainTF, `json_field()`)
	assert.Contains(t, mainTF, `python3 - "$field"`)
	assert.Contains(t, mainTF, `Komodo login response redacted`)
	assert.Contains(t, mainTF, `Komodo API key response redacted`)
	assert.NotContains(t, mainTF, `LOGIN_PAYLOAD='${jsonencode({ username = "stackkits-admin", password = var.admin_password_plaintext })}'`)
	assert.NotContains(t, mainTF, `sed -n 's/.*"jwt"`)
	assert.NotContains(t, mainTF, `printf '%s\n' "$LOGIN_JSON" >&2`)
	assert.NotContains(t, mainTF, `printf '%s\n' "$KEY_JSON" >&2`)
}

func TestDokployBootstrapPersistsHeadlessAPIKeyConfig(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "templates", "simple", "main.tf"))
	require.NoError(t, err)
	mainTF := string(data)

	assert.Contains(t, mainTF, `resource "random_password" "dokploy_better_auth_secret"`)
	assert.Contains(t, mainTF, `BETTER_AUTH_SECRET=${try(random_password.dokploy_better_auth_secret[0].result, "")}`)
	assert.Contains(t, mainTF, `BETTER_AUTH_URL=${local.dokploy_api_endpoint}`)
	assert.Contains(t, mainTF, `resource "null_resource" "dokploy_platform_config_dir"`)
	assert.Contains(t, mainTF, `resource "docker_image" "dokploy_bootstrap"`)
	assert.Contains(t, mainTF, `name  = "public.ecr.aws/docker/library/node:22-alpine"`)
	assert.Contains(t, mainTF, `resource "local_sensitive_file" "dokploy_bootstrap_env"`)
	assert.Contains(t, mainTF, `resource "null_resource" "dokploy_platform_bootstrap"`)
	assert.Contains(t, mainTF, `DOKPLOY_AUTH_ORIGIN=${local.dokploy_api_endpoint}`)
	assert.Contains(t, mainTF, `DOKPLOY_PLATFORM_CONFIG_PATH=/stackkit-out/platform.json`)
	assert.Contains(t, mainTF, `"Origin": authOrigin`)
	assert.Contains(t, mainTF, `"Referer": authOrigin + "/"`)
	assert.Contains(t, mainTF, `function statusLabel(response)`)
	assert.Contains(t, mainTF, `trap 'rm -f "$bootstrap_env"' EXIT`)
	assert.Contains(t, mainTF, `docker run --name init-dokploy --rm -i`)
	assert.Contains(t, mainTF, `--env-file "$bootstrap_env"`)
	assert.Contains(t, mainTF, `${docker_image.dokploy_bootstrap[0].name} node <<'JS'`)
	assert.NotContains(t, mainTF, `resource "docker_container" "init_dokploy"`)
	assert.NotContains(t, mainTF, `resource "null_resource" "dokploy_platform_config_ready"`)
	assert.NotContains(t, mainTF, `must_run = false`)
	assert.NotContains(t, mainTF, `DOKPLOY_ADMIN_PASSWORD       = var.admin_password_plaintext`)
	assert.Contains(t, mainTF, `/api/auth/sign-up/email`)
	assert.Contains(t, mainTF, `/api/trpc/auth.createAdmin?batch=1`)
	assert.Contains(t, mainTF, `/api/auth.login`)
	assert.Contains(t, mainTF, `/api/trpc/auth.login?batch=1`)
	assert.Contains(t, mainTF, `/api/auth/sign-in/email`)
	assert.Contains(t, mainTF, `/api/user.createApiKey`)
	assert.Contains(t, mainTF, `/api/trpc/user.createApiKey`)
	assert.Contains(t, mainTF, `/api/trpc/user.createApiKey?batch=1`)
	assert.Contains(t, mainTF, `rateLimitEnabled: false`)
	assert.Contains(t, mainTF, `/api/project.all`)
	assert.Contains(t, mainTF, `/api/project.create`)
	assert.Contains(t, mainTF, `ensureStackKitEnvironment(cookie)`)
	assert.Contains(t, mainTF, `projectId`)
	assert.Contains(t, mainTF, `environmentId`)
	assert.Contains(t, mainTF, `token: apiKey`)
	assert.Contains(t, mainTF, `apiKey`)
	assert.Contains(t, mainTF, `ERROR: " + error.message`)
	assert.NotContains(t, mainTF, `curlimages/curl`)
	assert.NotContains(t, mainTF, `console.error(response.text`)
	assert.NotContains(t, mainTF, `console.error(cookie`)
}

func TestFormatApplyErrorUsesActionableRegistryAndConfigMessages(t *testing.T) {
	assert.Contains(t,
		formatApplyError("error from registry: You have reached your unauthenticated pull rate limit"),
		"Docker Hub rate limit reached",
	)
	assert.Contains(t,
		formatApplyError("Error: Reference to undeclared resource"),
		"Generated OpenTofu configuration references a missing resource",
	)
}

func TestGenerateCommand_KombinationAliasFromChildWorkDir(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	workDir := filepath.Join(repoRoot, ".tmp-generate-alias-"+strings.ReplaceAll(t.Name(), "/", "-"))
	require.NoError(t, os.MkdirAll(workDir, 0750))
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	spec := `name: alias-generate
stackkit: base-kit
mode: simple
domain: home.localhost
network:
  mode: local
compute:
  tier: standard
ssh:
  user: root
  port: 22
`
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "kombination.yaml"), []byte(spec), 0600))

	_, err = executeCommandCaptureStdout(
		"--no-log",
		"--spec", "stack-spec.yaml",
		"--chdir", workDir,
		"generate",
		"--output", "deploy",
		"--force",
	)
	require.NoError(t, err)

	generatedPath := filepath.Join(workDir, "deploy", "terraform.tfvars.json")
	info, statErr := os.Stat(generatedPath)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0), "terraform.tfvars.json should not be empty")
}

func TestBcryptHash(t *testing.T) {
	password := "testpassword123"
	hash, err := bcryptHash(password)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$2a$"), "hash should be bcrypt format")

	// Verify the hash matches the password
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	assert.NoError(t, err)

	// Wrong password should not match
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong"))
	assert.Error(t, err)
}

func TestGenerateTfvarsJSON_AdminEmail(t *testing.T) {
	spec := &models.StackSpec{
		Name:       "test-homelab",
		AdminEmail: "test@example.com",
		Domain:     "home.example.com",
	}

	// Composition engine provides identity credentials
	cr := &composition.CompositionResult{
		EnabledModules: []string{"socket-proxy", "traefik", "tinyauth", "pocketid"},
		ModuleSettings: map[string]map[string]any{},
		Identity: &composition.IdentityConfig{
			AdminEmail:            "test@example.com",
			AdminPassword:         "abcdef0123456789abcdef0123456789",
			TinyAuthEnabled:       true,
			PocketIDEnabled:       true,
			TinyAuthSessionSecret: "session_secret_hex",
			OIDCClientID:          "client_id_hex",
			OIDCClientSecret:      "client_secret_hex",
			OIDCIssuerURL:         "https://id.home.example.com",
			PocketIDAppURL:        "https://id.home.example.com",
			TinyAuthOAuthEnabled:  true,
		},
	}

	data, err := generateTfvarsJSON(spec, cr)
	require.NoError(t, err)

	var vars map[string]interface{}
	err = json.Unmarshal(data, &vars)
	require.NoError(t, err)

	assert.Equal(t, "test@example.com", vars["admin_email"])
	assert.Equal(t, true, vars["enable_dashboard"])
	assert.Equal(t, "86400", vars["tinyauth_session_expiry"])
	assert.Equal(t, "false", vars["tinyauth_secure_cookie"])

	// tinyauth_users should be email:bcrypt_hash
	users, ok := vars["tinyauth_users"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(users, "test@example.com:$2a$"),
		"tinyauth_users should be email:bcrypt, got: %s", users)

	// admin_password_plaintext should be present (32 hex chars from composition engine)
	pw, ok := vars["admin_password_plaintext"].(string)
	require.True(t, ok)
	assert.Len(t, pw, 32)
}

func TestGenerateTfvarsJSON_FallbackAdmin(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name: "test-homelab",
	}

	// Without composition result, bridge generates structural defaults only (no credentials)
	data, err := generateTfvarsJSON(spec, nil)
	require.NoError(t, err)

	var vars map[string]interface{}
	err = json.Unmarshal(data, &vars)
	require.NoError(t, err)

	// Without composition result, admin_email is still a syntactically valid email.
	assert.Equal(t, "admin@example.com", vars["admin_email"])

	// Without composition result, no credentials are generated
	assert.Empty(t, vars["admin_password_plaintext"], "no password without composition result")
	assert.Empty(t, vars["tinyauth_users"], "no users without composition result")
}

func TestGenerateTfvarsJSON_OwnerEmailBecomesAdminEmail(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:       "test-homelab",
		AdminEmail: "legacy-admin@example.com",
		Domain:     "home.example.com",
		Owner: models.OwnerConfig{
			Email: "owner@example.com",
		},
	}

	data, err := generateTfvarsJSON(spec, nil)
	require.NoError(t, err)

	var vars map[string]interface{}
	err = json.Unmarshal(data, &vars)
	require.NoError(t, err)

	assert.Equal(t, "owner@example.com", vars["admin_email"])
}

func TestGenerateTfvarsJSON_SyntheticKombifyMeAdmin(t *testing.T) {
	spec := &models.StackSpec{
		Name:            "test-homelab",
		Domain:          models.DomainKombifyMe,
		SubdomainPrefix: "sh-my-homelab-cfe020",
	}

	adminEmail := models.NormalizeAdminEmail("", spec.Domain, spec.SubdomainPrefix)
	cr := &composition.CompositionResult{
		EnabledModules: []string{"socket-proxy", "traefik", "tinyauth", "pocketid"},
		ModuleSettings: map[string]map[string]any{},
		Identity: &composition.IdentityConfig{
			AdminEmail:            adminEmail,
			AdminPassword:         "abcdef0123456789abcdef0123456789",
			TinyAuthEnabled:       true,
			PocketIDEnabled:       true,
			TinyAuthSessionSecret: "session_secret_hex",
			OIDCClientID:          "stackkit-tinyauth",
			OIDCIssuerURL:         "https://sh-my-homelab-cfe020-id.kombify.me",
			PocketIDAppURL:        "https://sh-my-homelab-cfe020-id.kombify.me",
			TinyAuthOAuthEnabled:  true,
		},
	}

	data, err := generateTfvarsJSON(spec, cr)
	require.NoError(t, err)

	var vars map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &vars))

	assert.Equal(t, "admin@sh-my-homelab-cfe020.kombify.me", vars["admin_email"])
	assert.True(t, strings.HasPrefix(vars["tinyauth_users"].(string), "admin@sh-my-homelab-cfe020.kombify.me:$2a$"))
}

func TestInitCommand_AdminEmailFlag(t *testing.T) {
	f := initCmd.Flags().Lookup("admin-email")
	require.NotNil(t, f, "--admin-email flag should exist")
	assert.Equal(t, "", f.DefValue)
}

func TestInitCommand_DomainFlag(t *testing.T) {
	f := initCmd.Flags().Lookup("domain")
	require.NotNil(t, f, "--domain flag should exist")
	assert.Equal(t, "", f.DefValue)
}

func TestInitCommand_ServiceProfileFlag(t *testing.T) {
	f := initCmd.Flags().Lookup("service-profile")
	require.NotNil(t, f, "--service-profile flag should exist")
	assert.Equal(t, "", f.DefValue)
}

func TestResolveInitDefaults(t *testing.T) {
	resetInitGlobals := func(t *testing.T) {
		t.Helper()
		prevComputeTier := initComputeTier
		prevDomain := initDomain
		prevMode := initMode
		prevForce := initForce
		prevNonInteractive := initNonInteractive
		prevAdminEmail := initAdminEmail
		prevLocalDNS := initLocalDNS
		prevLocalName := initLocalName
		prevServiceProfile := initServiceProfile
		prevContextFlag := contextFlag

		initComputeTier = ""
		initDomain = ""
		initMode = ""
		initForce = false
		initNonInteractive = false
		initAdminEmail = ""
		initLocalDNS = false
		initLocalName = ""
		initServiceProfile = ""
		contextFlag = ""

		t.Cleanup(func() {
			initComputeTier = prevComputeTier
			initDomain = prevDomain
			initMode = prevMode
			initForce = prevForce
			initNonInteractive = prevNonInteractive
			initAdminEmail = prevAdminEmail
			initLocalDNS = prevLocalDNS
			initLocalName = prevLocalName
			initServiceProfile = prevServiceProfile
			contextFlag = prevContextFlag
		})
	}

	t.Run("cloud defaults to kombify me public and detected standard tier", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextCloud, defaults.Context)
		assert.Equal(t, models.ComputeTierStandard, defaults.ComputeTier)
		assert.Equal(t, models.DomainKombifyMe, defaults.Domain)
		assert.Equal(t, "public", defaults.NetworkMode)
	})

	t.Run("local defaults to home lab local network and detected high tier", func(t *testing.T) {
		resetInitGlobals(t)

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		capsDir := filepath.Join(home, ".stackkits")
		require.NoError(t, os.MkdirAll(capsDir, 0750))

		caps := models.DockerCapabilities{
			ResolvedContext:  models.ContextLocal,
			BridgeNetworking: true,
			StorageDriver:    models.StorageOverlay2,
			CPUCores:         8,
			MemoryGB:         16,
		}
		data, err := json.Marshal(caps)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(capsDir, "capabilities.json"), data, 0600))

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextLocal, defaults.Context)
		assert.Equal(t, models.ComputeTierHigh, defaults.ComputeTier)
		assert.Equal(t, models.DomainHomeLab, defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})

	t.Run("local default domain honors stackkit local domain environment override", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextLocal)
		t.Setenv("STACKKIT_LOCAL_DOMAIN", models.DomainHomeLocalhost)

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextLocal, defaults.Context)
		assert.Equal(t, models.DomainHomeLocalhost, defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})

	t.Run("custom domain is preserved for cloud", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)

		defaults := resolveInitDefaults("apps.example.com")

		assert.Equal(t, models.ContextCloud, defaults.Context)
		assert.Equal(t, "apps.example.com", defaults.Domain)
		assert.Equal(t, "public", defaults.NetworkMode)
	})

	t.Run("explicit flags win over detected defaults", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)
		initComputeTier = models.ComputeTierLow
		contextFlag = string(models.ContextLocal)

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextLocal, defaults.Context)
		assert.Equal(t, models.ComputeTierLow, defaults.ComputeTier)
		assert.Equal(t, models.DomainHomeLab, defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})

	t.Run("local dns defaults to stack home and local network", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)
		initLocalDNS = true

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextCloud, defaults.Context)
		assert.Equal(t, models.DomainStackHome, defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})

	t.Run("local dns short name expands to nested home zone", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)
		initLocalDNS = true
		initLocalName = "family"

		defaults := resolveInitDefaults("")

		assert.Equal(t, "family.home", defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})
}

func TestInitCommand_NonInteractive_DomainOverrideCreatesSpec(t *testing.T) {
	prevComputeTier := initComputeTier
	prevDomain := initDomain
	prevMode := initMode
	prevForce := initForce
	prevNonInteractive := initNonInteractive
	prevAdminEmail := initAdminEmail
	prevLocalDNS := initLocalDNS
	prevLocalName := initLocalName
	prevServiceProfile := initServiceProfile
	prevContextFlag := contextFlag
	prevSpecFile := specFile
	t.Cleanup(func() {
		initComputeTier = prevComputeTier
		initDomain = prevDomain
		initMode = prevMode
		initForce = prevForce
		initNonInteractive = prevNonInteractive
		initAdminEmail = prevAdminEmail
		initLocalDNS = prevLocalDNS
		initLocalName = prevLocalName
		initServiceProfile = prevServiceProfile
		contextFlag = prevContextFlag
		specFile = prevSpecFile
	})

	specFile = "stack-spec.yaml"

	cwd, err := os.Getwd()
	require.NoError(t, err)

	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	tmpDir, err := os.MkdirTemp(repoRoot, "init-domain-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	_, err = executeCommand(
		"init", "base-kit",
		"--non-interactive",
		"--force",
		"--context", "cloud",
		"--compute-tier", "standard",
		"--domain", "apps.example.com",
		"--admin-email", "test@example.com",
		"--chdir", tmpDir,
	)
	require.NoError(t, err)

	specData, err := os.ReadFile(filepath.Join(tmpDir, "stack-spec.yaml"))
	require.NoError(t, err)

	var spec models.StackSpec
	require.NoError(t, yaml.Unmarshal(specData, &spec))
	assert.Equal(t, "apps.example.com", spec.Domain)
	assert.Equal(t, string(models.ContextCloud), spec.Context)
	assert.Equal(t, "public", spec.Network.Mode)
	assert.Equal(t, "test@example.com", spec.AdminEmail)
}

func TestInitCommand_BaseKitAdminOnlyServiceProfile(t *testing.T) {
	prevComputeTier := initComputeTier
	prevDomain := initDomain
	prevMode := initMode
	prevForce := initForce
	prevNonInteractive := initNonInteractive
	prevAdminEmail := initAdminEmail
	prevLocalDNS := initLocalDNS
	prevLocalName := initLocalName
	prevServiceProfile := initServiceProfile
	prevContextFlag := contextFlag
	prevSpecFile := specFile
	prevGenOutputDir := genOutputDir
	prevGenForce := genForce
	t.Cleanup(func() {
		initComputeTier = prevComputeTier
		initDomain = prevDomain
		initMode = prevMode
		initForce = prevForce
		initNonInteractive = prevNonInteractive
		initAdminEmail = prevAdminEmail
		initLocalDNS = prevLocalDNS
		initLocalName = prevLocalName
		initServiceProfile = prevServiceProfile
		contextFlag = prevContextFlag
		specFile = prevSpecFile
		genOutputDir = prevGenOutputDir
		genForce = prevGenForce
	})

	setCapabilitiesHome(t, models.ContextCloud)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	tmpDir, err := os.MkdirTemp(repoRoot, "init-admin-only-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	specFile = "stack-spec.yaml"
	_, err = executeCommand(
		"init", "base-kit",
		"--non-interactive",
		"--force",
		"--context", "cloud",
		"--compute-tier", "standard",
		"--admin-email", "tester@kombify.pro",
		"--service-profile", "admin-only",
		"--chdir", tmpDir,
	)
	require.NoError(t, err)

	specData, err := os.ReadFile(filepath.Join(tmpDir, "stack-spec.yaml"))
	require.NoError(t, err)

	var spec models.StackSpec
	require.NoError(t, yaml.Unmarshal(specData, &spec))
	assert.Equal(t, models.DomainKombifyMe, spec.Domain)
	assert.Equal(t, string(models.ContextCloud), spec.Context)
	assert.Equal(t, "public", spec.Network.Mode)
	assert.Equal(t, "tester@kombify.pro", spec.AdminEmail)
	require.Contains(t, spec.Services, "vault")
	require.Contains(t, spec.Services, "photos")
	assertServiceEnabled := func(name string, want bool) {
		t.Helper()
		service, ok := spec.Services[name].(map[string]any)
		require.True(t, ok, "services.%s should be a map", name)
		assert.Equal(t, want, service["enabled"], "services.%s.enabled", name)
	}
	assertServiceEnabled("uptime-kuma", true)
	assertServiceEnabled("whoami", true)
	assertServiceEnabled("vault", false)
	assertServiceEnabled("vaultwarden", false)
	assertServiceEnabled("photos", false)
	assertServiceEnabled("immich", false)

	spec.SubdomainPrefix = "sh-admin-only-test"
	specBytes, err := yaml.Marshal(&spec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "stack-spec.yaml"), specBytes, 0600))

	_, err = executeCommand(
		"--chdir", tmpDir,
		"--context", "cloud",
		"generate",
		"--force",
	)
	require.NoError(t, err)
	vars := readTFVarsFile(t, filepath.Join(tmpDir, "deploy", "terraform.tfvars.json"))
	assert.Equal(t, models.PAASCoolify, vars["paas"])
	assert.Equal(t, true, vars["enable_coolify"])
	assert.Equal(t, false, vars["enable_dokploy"])
	assert.Equal(t, true, vars["enable_uptime_kuma"])
	assert.Equal(t, true, vars["enable_whoami"])
	assert.Equal(t, false, vars["enable_vaultwarden"])
	assert.Equal(t, false, vars["enable_jellyfin"])
	assert.Equal(t, false, vars["enable_immich"])
}

func TestInitCommand_BaseKitLocalReferenceUsesSafeSSHDefaults(t *testing.T) {
	prevComputeTier := initComputeTier
	prevDomain := initDomain
	prevMode := initMode
	prevForce := initForce
	prevNonInteractive := initNonInteractive
	prevAdminEmail := initAdminEmail
	prevLocalDNS := initLocalDNS
	prevLocalName := initLocalName
	prevServiceProfile := initServiceProfile
	prevContextFlag := contextFlag
	prevSpecFile := specFile
	t.Cleanup(func() {
		initComputeTier = prevComputeTier
		initDomain = prevDomain
		initMode = prevMode
		initForce = prevForce
		initNonInteractive = prevNonInteractive
		initAdminEmail = prevAdminEmail
		initLocalDNS = prevLocalDNS
		initLocalName = prevLocalName
		initServiceProfile = prevServiceProfile
		contextFlag = prevContextFlag
		specFile = prevSpecFile
	})

	specFile = "stack-spec.yaml"

	cwd, err := os.Getwd()
	require.NoError(t, err)

	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	tmpDir, err := os.MkdirTemp(repoRoot, "init-local-ssh-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	_, err = executeCommand(
		"init", "base-kit",
		"--non-interactive",
		"--force",
		"--context", "local",
		"--admin-email", "test@example.com",
		"--chdir", tmpDir,
	)
	require.NoError(t, err)

	specData, err := os.ReadFile(filepath.Join(tmpDir, "stack-spec.yaml"))
	require.NoError(t, err)

	var spec models.StackSpec
	require.NoError(t, yaml.Unmarshal(specData, &spec))
	assert.Equal(t, models.DomainHomeLab, spec.Domain)
	assert.Equal(t, string(models.ContextLocal), spec.Context)
	assert.Equal(t, "admin", spec.SSH.User)
	assert.Equal(t, "~/.ssh/id_ed25519", spec.SSH.KeyPath)
	assert.Empty(t, spec.PAAS, "init should leave PaaS resolution to the canonical resolver")

	assertServiceEnabled := func(name string, want bool) {
		t.Helper()
		service, ok := spec.Services[name].(map[string]any)
		require.True(t, ok, "service %q should be present in init spec", name)
		assert.Equal(t, want, service["enabled"], "services.%s.enabled", name)
	}
	assertServiceEnabled("homepage", true)
	assertServiceEnabled("uptime-kuma", true)
	assertServiceEnabled("whoami", true)
	assertServiceEnabled("vaultwarden", true)
	assertServiceEnabled("jellyfin", false)
	assertServiceEnabled("immich", true)
	for _, platformService := range []string{"dokploy", "coolify", "dockge", "traefik"} {
		assert.NotContains(t, spec.Services, platformService, "init must leave %s selection to the resolver", platformService)
	}
}

func TestBaseKitDefaultSpecDoesNotPinPlatformSelection(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "default-spec.yaml"))
	require.NoError(t, err)

	var spec models.StackSpec
	require.NoError(t, yaml.Unmarshal(data, &spec))

	assert.Empty(t, spec.PAAS, "default-spec must leave PaaS selection to the resolver")
	for _, platformService := range []string{"dokploy", "coolify", "dockge", "traefik"} {
		assert.NotContains(t, spec.Services, platformService, "default-spec must not pin %s", platformService)
	}

	setCapabilitiesHome(t, models.ContextCloud)
	spec.Context = string(models.ContextCloud)
	spec.Domain = "example.com"
	spec.Network.Mode = "public"

	vars := decodeTFVars(t, &spec)
	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.Equal(t, models.ReverseProxyCoolify, stringVar(t, vars, "reverse_proxy_backend"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.False(t, boolVar(t, vars, "enable_dokploy"))
	assert.False(t, boolVar(t, vars, "enable_traefik"))
}

func TestBaseKitTemplateCriticalDefaultsMatchGeneratedGolden(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	templateData, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "templates", "simple", "main.tf"))
	require.NoError(t, err)
	template := string(templateData)
	golden := readTFVarsFile(t, filepath.Join(repoRoot, "cmd", "stackkit", "commands", "testdata", "golden", "local-standard.json"))

	for variable, tfvarKey := range map[string]string{
		"enable_coolify":        "enable_coolify",
		"enable_dokploy":        "enable_dokploy",
		"enable_dokploy_apps":   "enable_dokploy_apps",
		"enable_whoami":         "enable_whoami",
		"enable_jellyfin":       "enable_jellyfin",
		"paas":                  "paas",
		"reverse_proxy_backend": "reverse_proxy_backend",
	} {
		expected, ok := golden[tfvarKey]
		require.True(t, ok, "golden tfvars missing %s", tfvarKey)
		assertTerraformVariableDefault(t, template, variable, expected)
	}
}

func TestInitCommand_BaseKitLocalDefaultMatchesDefaultSpecTFVars(t *testing.T) {
	prevComputeTier := initComputeTier
	prevDomain := initDomain
	prevMode := initMode
	prevForce := initForce
	prevNonInteractive := initNonInteractive
	prevAdminEmail := initAdminEmail
	prevLocalDNS := initLocalDNS
	prevLocalName := initLocalName
	prevServiceProfile := initServiceProfile
	prevContextFlag := contextFlag
	prevSpecFile := specFile
	prevGenOutputDir := genOutputDir
	prevGenForce := genForce
	t.Cleanup(func() {
		initComputeTier = prevComputeTier
		initDomain = prevDomain
		initMode = prevMode
		initForce = prevForce
		initNonInteractive = prevNonInteractive
		initAdminEmail = prevAdminEmail
		initLocalDNS = prevLocalDNS
		initLocalName = prevLocalName
		initServiceProfile = prevServiceProfile
		contextFlag = prevContextFlag
		specFile = prevSpecFile
		genOutputDir = prevGenOutputDir
		genForce = prevGenForce
	})

	setCapabilitiesHome(t, models.ContextLocal)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	initDir, err := os.MkdirTemp(repoRoot, "init-parity-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(initDir) })

	specFile = "stack-spec.yaml"
	_, err = executeCommand(
		"init", "base-kit",
		"--non-interactive",
		"--force",
		"--context", "local",
		"--admin-email", "admin@example.com",
		"--chdir", initDir,
	)
	require.NoError(t, err)

	_, err = executeCommand(
		"--chdir", initDir,
		"--context", "local",
		"generate",
		"--force",
	)
	require.NoError(t, err)
	initVars := readTFVarsFile(t, filepath.Join(initDir, "deploy", "terraform.tfvars.json"))

	defaultDir, err := os.MkdirTemp(repoRoot, "default-spec-parity-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(defaultDir) })

	defaultSpec := filepath.Join("..", "base-kit", "default-spec.yaml")
	_, err = executeCommand(
		"--chdir", defaultDir,
		"--context", "local",
		"--spec", defaultSpec,
		"generate",
		"--force",
	)
	require.NoError(t, err)
	defaultVars := readTFVarsFile(t, filepath.Join(defaultDir, "deploy", "terraform.tfvars.json"))

	for _, key := range []string{
		"domain",
		"paas",
		"reverse_proxy_backend",
		"enable_https",
		"step_ca_enabled",
		"enable_kombify_point",
		"enable_dashboard",
		"enable_homepage",
		"enable_tinyauth",
		"enable_pocketid",
		"enable_dokploy",
		"enable_dokploy_apps",
		"enable_uptime_kuma",
		"enable_whoami",
		"enable_vaultwarden",
		"enable_jellyfin",
		"enable_immich",
	} {
		assert.Equalf(t, defaultVars[key], initVars[key], "init/default-spec drift for %s", key)
	}
}

func readTFVarsFile(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var vars map[string]any
	require.NoError(t, json.Unmarshal(data, &vars))
	return vars
}

func assertTerraformVariableDefault(t *testing.T, template, variable string, expected any) {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)variable\s+"` + regexp.QuoteMeta(variable) + `"\s*\{.*?default\s*=\s*` + regexp.QuoteMeta(terraformDefaultLiteral(t, expected)) + `(?:\s|$)`)
	assert.Regexp(t, pattern, template, "variable %s default should match generated local-standard golden", variable)
}

func terraformDefaultLiteral(t *testing.T, value any) string {
	t.Helper()
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return `"` + v + `"`
	default:
		t.Fatalf("unsupported Terraform default literal type %T", value)
		return ""
	}
}

func TestParseDfOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		wantGB float64
		wantOK bool
	}{
		{
			name: "normal df output",
			output: `     Avail      Size Target
 744488960 5267922944 /`,
			wantGB: 0.69, // ~694MB
			wantOK: true,
		},
		{
			name: "larger disk",
			output: `        Avail         Size Target
 21474836480  42949672960 /`,
			wantGB: 20.0,
			wantOK: true,
		},
		{
			name:   "empty output",
			output: "",
			wantOK: false,
		},
		{
			name:   "header only",
			output: "     Avail      Size Target",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			availGB, _, _ := parseDfOutput(tt.output)
			if tt.wantOK {
				assert.InDelta(t, tt.wantGB, availGB, 0.1, "available GB")
			} else {
				assert.Equal(t, float64(0), availGB)
			}
		})
	}
}

func TestIsNoSpaceError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"no space left on device", true},
		{"write /var/lib/containerd/...: no space left on device", true},
		{"Error response from daemon: mkdir /var/lib/containerd/...: no space left on device", true},
		{"connection refused", false},
		{"timeout", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			assert.Equal(t, tt.want, isNoSpaceError(tt.errMsg))
		})
	}
}

func TestResourceSpec_DiskFromYAML(t *testing.T) {
	yamlData := `
minimum:
  cpu: 2
  memory: 4
  disk: 50
recommended:
  cpu: 4
  memory: 8
  disk: 100
`
	var reqs models.Requirements
	err := yaml.Unmarshal([]byte(yamlData), &reqs)
	require.NoError(t, err)

	assert.Equal(t, 2, reqs.Minimum.CPU)
	assert.Equal(t, 4, reqs.Minimum.RAM)
	assert.Equal(t, 50, reqs.Minimum.Disk)
	assert.Equal(t, 4, reqs.Recommended.CPU)
	assert.Equal(t, 8, reqs.Recommended.RAM)
	assert.Equal(t, 100, reqs.Recommended.Disk)
}

func TestDockerCapabilities_DiskFields(t *testing.T) {
	caps := &models.DockerCapabilities{
		DiskTotalGB: 20.0,
		DiskAvailGB: 15.5,
		DiskMount:   "/",
		LVMDetected: true,
		LVMExtended: false,
	}

	data, err := json.Marshal(caps)
	require.NoError(t, err)

	var decoded models.DockerCapabilities
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.InDelta(t, 20.0, decoded.DiskTotalGB, 0.01)
	assert.InDelta(t, 15.5, decoded.DiskAvailGB, 0.01)
	assert.Equal(t, "/", decoded.DiskMount)
	assert.True(t, decoded.LVMDetected)
	assert.False(t, decoded.LVMExtended)
}

func TestRemoveCommand_PurgeFlag(t *testing.T) {
	f := removeCmd.Flags().Lookup("purge")
	require.NotNil(t, f, "--purge flag should exist")
	assert.Equal(t, "false", f.DefValue)
}

func TestCleanupFiles_Purge(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directories that should be removed
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "deploy", ".terraform", "providers"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "deploy", "main.tf"), []byte("# test"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".stackkit", "state.yaml"), []byte("status: running"), 0600))

	cleanupFiles(tmpDir, true)

	// All directories should be gone
	_, err := os.Stat(filepath.Join(tmpDir, "deploy"))
	assert.True(t, os.IsNotExist(err), "deploy/ should be removed after purge")

	_, err = os.Stat(filepath.Join(tmpDir, ".stackkit"))
	assert.True(t, os.IsNotExist(err), ".stackkit/ should be removed after purge")
}

func TestAutoDetectComputeTier(t *testing.T) {
	tests := []struct {
		name     string
		cpu      int
		memory   float64
		expected string
	}{
		{"high tier", 8, 16.0, "high"},
		{"high tier large", 16, 64.0, "high"},
		{"standard tier", 4, 8.0, "standard"},
		{"standard tier 6 cpu", 6, 12.0, "standard"},
		{"low tier few cpu", 2, 4.0, "low"},
		{"low tier low ram", 4, 4.0, "low"},
		{"low tier minimal", 1, 1.0, "low"},
		{"boundary high cpu low ram", 8, 8.0, "standard"},
		{"boundary low cpu high ram", 2, 16.0, "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := autoDetectComputeTier(tt.cpu, tt.memory)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateTfvarsJSON_TierDriven(t *testing.T) {
	tests := []struct {
		name         string
		tier         string
		wantDokploy  bool
		wantCoolify  bool
		wantDockge   bool
		wantKuma     bool
		wantTraefik  bool
		wantTinyauth bool
		wantPocketid bool
	}{
		{
			name:         "standard tier",
			tier:         "standard",
			wantDokploy:  false,
			wantCoolify:  true,
			wantDockge:   false,
			wantKuma:     true,
			wantTraefik:  false,
			wantTinyauth: true,
			wantPocketid: true,
		},
		{
			name:         "high tier",
			tier:         "high",
			wantDokploy:  false,
			wantCoolify:  true,
			wantDockge:   false,
			wantKuma:     true,
			wantTraefik:  false,
			wantTinyauth: true,
			wantPocketid: true,
		},
		{
			name:         "low tier",
			tier:         "low",
			wantDokploy:  false,
			wantCoolify:  true,
			wantDockge:   false,
			wantKuma:     true,
			wantTraefik:  false,
			wantTinyauth: true,
			wantPocketid: true,
		},
		{
			name:         "empty tier defaults to standard",
			tier:         "",
			wantDokploy:  false,
			wantCoolify:  true,
			wantDockge:   false,
			wantKuma:     true,
			wantTraefik:  false,
			wantTinyauth: true,
			wantPocketid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCapabilitiesHome(t, models.ContextLocal)

			spec := &models.StackSpec{
				Name:   "test",
				Domain: "test.local",
				Compute: models.ComputeSpec{
					Tier: tt.tier,
				},
			}

			data, err := generateTfvarsJSON(spec, nil)
			require.NoError(t, err)

			var vars map[string]interface{}
			err = json.Unmarshal(data, &vars)
			require.NoError(t, err)

			// L1/L2 core — always enabled
			assert.Equal(t, tt.wantTraefik, vars["enable_traefik"], "enable_traefik")
			assert.Equal(t, tt.wantTinyauth, vars["enable_tinyauth"], "enable_tinyauth")
			assert.Equal(t, tt.wantPocketid, vars["enable_pocketid"], "enable_pocketid")

			// PAAS — tier-dependent
			assert.Equal(t, tt.wantDokploy, vars["enable_dokploy"], "enable_dokploy")
			assert.Equal(t, tt.wantCoolify, vars["enable_coolify"], "enable_coolify")
			assert.Equal(t, tt.wantDockge, vars["enable_dockge"], "enable_dockge")

			// Monitoring — tier-dependent
			assert.Equal(t, tt.wantKuma, vars["enable_uptime_kuma"], "enable_uptime_kuma")

			// Dashboard — always
			assert.Equal(t, true, vars["enable_dashboard"], "enable_dashboard")
		})
	}
}

func TestCleanupFiles_NoPurge(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directories
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "deploy", ".terraform", "providers"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "deploy", "traefik.tf"), []byte("# test"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "deploy", "terraform.tfvars.json"), []byte("{}"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".stackkit", "state.yaml"), []byte("status: running"), 0600))

	cleanupFiles(tmpDir, false)

	// .terraform should be removed but deploy/ and .stackkit/ should remain
	_, err := os.Stat(filepath.Join(tmpDir, "deploy", ".terraform"))
	assert.True(t, os.IsNotExist(err), ".terraform/ should be removed")

	_, err = os.Stat(filepath.Join(tmpDir, "deploy", "traefik.tf"))
	assert.NoError(t, err, "generated deploy/*.tf files should still exist")

	_, err = os.Stat(filepath.Join(tmpDir, "deploy", "terraform.tfvars.json"))
	assert.NoError(t, err, "generated terraform.tfvars.json should still exist")

	_, err = os.Stat(filepath.Join(tmpDir, ".stackkit", "state.yaml"))
	assert.NoError(t, err, ".stackkit/state.yaml should still exist")
}
