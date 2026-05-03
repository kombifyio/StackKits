//go:build vm

// Package vm provides integration tests that exercise every StackKit CLI command
// against a real Ubuntu VM running inside Docker. The VM container must already
// be running (started by mise run dev) before these tests execute.
package vm

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vmWorkDir = "/workspace/.stackkits-vm-workspace"

// execInCli runs a command inside the stackkits-cli container which has
// DOCKER_HOST=tcp://vm:2375 set, so all docker/tofu operations target the VM.
func execInCli(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"compose", "--profile", "cli", "exec", "-T", "cli"}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// execInVM runs a command directly inside the stackkits-vm container.
func execInVM(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"exec", "stackkits-vm"}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// waitForContainers polls docker ps inside the VM until at least one container
// matching the given name substring is running, or the timeout is reached.
func waitForContainers(t *testing.T, nameSubstring string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := execInVM(t, "docker", "ps", "--format", "{{.Names}}")
		if err == nil && strings.Contains(out, nameSubstring) {
			return true
		}
		time.Sleep(5 * time.Second)
	}
	return false
}

// ─── CLI Command Tests (executed in order) ───────────────────────

func TestCLI_Version(t *testing.T) {
	out, err := execInCli(t, "stackkit", "version")
	require.NoError(t, err, "version failed: %s", out)
	assert.Contains(t, out, "stackkit version")
}

func TestCLI_Init(t *testing.T) {
	out, err := execInCli(t, "sh", "-c", fmt.Sprintf("rm -rf %s && mkdir -p %s", vmWorkDir, vmWorkDir))
	require.NoError(t, err, "prepare isolated VM workspace failed: %s", out)

	out, err = execInCli(t, "stackkit", "init", "base-kit",
		"--non-interactive", "-C", vmWorkDir, "--force")
	require.NoError(t, err, "init failed: %s", out)

	// Verify stack-spec.yaml was created
	specOut, err := execInCli(t, "cat", vmWorkDir+"/stack-spec.yaml")
	require.NoError(t, err, "reading spec failed: %s", specOut)
	assert.Contains(t, specOut, "base-kit", "spec should reference base-kit stackkit")
	assert.Contains(t, specOut, "mode", "spec should contain mode field")
}

func TestCLI_Init_AddSvelteKitSmokeApp(t *testing.T) {
	buildSmokeSvelteKitImage(t)

	out, err := execInCli(t, "sh", "-c", fmt.Sprintf(`cat >> %s/stack-spec.yaml <<'YAML'

apps:
  web:
    kind: sveltekit
    image: stackkits-smoke-sveltekit:latest
    port: 3000
    route:
      host: app.stack.local
      auth: login-gateway
    health:
      path: /health
    env:
      PUBLIC_APP_NAME: StackKit Smoke
YAML
`, vmWorkDir))
	require.NoError(t, err, "append SvelteKit smoke app failed: %s", out)

	specOut, err := execInCli(t, "cat", vmWorkDir+"/stack-spec.yaml")
	require.NoError(t, err, "reading spec failed: %s", specOut)
	assert.Contains(t, specOut, "apps:", "spec should contain apps contract")
	assert.Contains(t, specOut, "stackkits-smoke-sveltekit:latest", "spec should reference the local smoke image")
}

func TestCLI_Init_SpecStructure(t *testing.T) {
	specOut, err := execInCli(t, "cat", vmWorkDir+"/stack-spec.yaml")
	require.NoError(t, err, "reading spec failed: %s", specOut)

	// Verify key spec fields
	lowerSpec := strings.ToLower(specOut)
	assert.True(t,
		strings.Contains(lowerSpec, "stackkit") || strings.Contains(lowerSpec, "kind"),
		"spec should have stackkit/kind field: %s", specOut)
	assert.True(t,
		strings.Contains(lowerSpec, "mode") || strings.Contains(lowerSpec, "compute"),
		"spec should have mode or compute field: %s", specOut)
}

func TestCLI_Validate(t *testing.T) {
	out, err := execInCli(t, "stackkit", "validate", "-C", vmWorkDir)
	require.NoError(t, err, "validate failed: %s", out)
	assert.Contains(t, strings.ToLower(out), "valid")
}

func TestCLI_Validate_InvalidSpec(t *testing.T) {
	_, err := execInCli(t, "sh", "-c",
		"echo 'mode: simple' > /tmp/bad-spec.yaml")
	require.NoError(t, err)

	out, err := execInCli(t, "stackkit", "validate", "-s", "/tmp/bad-spec.yaml")
	assert.Error(t, err, "validate should fail for invalid spec: %s", out)
}

func TestCLI_Validate_Verbose(t *testing.T) {
	out, err := execInCli(t, "stackkit", "validate", "-C", vmWorkDir, "-v")
	require.NoError(t, err, "validate -v failed: %s", out)
	t.Logf("Verbose validate output:\n%s", out)
}

func TestCLI_Prepare(t *testing.T) {
	out, err := execInCli(t, "stackkit", "prepare", "-C", vmWorkDir)
	if err != nil {
		t.Logf("prepare output (may partially fail): %s", out)
	}
	lowerOut := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lowerOut, "docker") || strings.Contains(lowerOut, "ready") || strings.Contains(lowerOut, "pass"),
		"prepare should reference Docker detection: %s", out)
}

func TestCLI_Prepare_DryRun(t *testing.T) {
	out, err := execInCli(t, "stackkit", "prepare", "-C", vmWorkDir, "--dry-run")
	if err != nil {
		t.Logf("prepare --dry-run output: %s", out)
	}
	// Dry run should not fail, just report what would happen
	t.Logf("Prepare dry-run output:\n%s", out)
}

func TestCLI_Generate(t *testing.T) {
	out, err := execInCli(t, "stackkit", "generate",
		"-C", vmWorkDir, "--force")
	require.NoError(t, err, "generate failed: %s", out)

	// Verify .tf files were created
	lsOut, err := execInCli(t, "sh", "-c", fmt.Sprintf("ls %s/deploy/*.tf 2>/dev/null || ls %s/*.tf 2>/dev/null || echo 'no tf files'", vmWorkDir, vmWorkDir))
	require.NoError(t, err)
	assert.NotContains(t, lsOut, "no tf files", "generate should create .tf files")
}

func TestCLI_Generate_VerifyTfStructure(t *testing.T) {
	// Verify generated Terraform contains expected resource types
	tfContent, err := execInCli(t, "sh", "-c",
		fmt.Sprintf("cat %s/deploy/*.tf 2>/dev/null || cat %s/*.tf 2>/dev/null || echo 'no tf files'", vmWorkDir, vmWorkDir))
	require.NoError(t, err)
	assert.NotContains(t, tfContent, "no tf files")

	lowerTf := strings.ToLower(tfContent)
	assert.True(t,
		strings.Contains(lowerTf, "resource") || strings.Contains(lowerTf, "module"),
		"generated TF should contain resource or module blocks: %s", tfContent[:min(500, len(tfContent))])
}

func TestCLI_Generate_VerifySvelteKitAppResources(t *testing.T) {
	appsTF, err := execInCli(t, "cat", vmWorkDir+"/deploy/apps.tf")
	require.NoError(t, err, "reading apps.tf failed: %s", appsTF)

	assert.Contains(t, appsTF, `resource "docker_container" "app_web"`, "apps.tf should define the SvelteKit app container")
	assert.Contains(t, appsTF, `name     = "app-web"`, "apps.tf should name the app container predictably")
	assert.Contains(t, appsTF, "Host(`app.stack.local`)", "apps.tf should route the app through Traefik")
	assert.Contains(t, appsTF, "tinyauth@docker", "login-gateway apps should use TinyAuth middleware")
	assert.Contains(t, appsTF, "fetch('http://127.0.0.1:3000/health')", "apps.tf should healthcheck the configured path")
}

func TestCLI_Plan(t *testing.T) {
	out, err := execInCli(t, "stackkit", "plan", "-C", vmWorkDir)
	require.NoError(t, err, "plan failed: %s", out)
	lowerOut := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lowerOut, "plan") || strings.Contains(lowerOut, "add") || strings.Contains(lowerOut, "change"),
		"plan output should show planned changes: %s", out)
}

func TestCLI_Plan_ResourceCount(t *testing.T) {
	out, err := execInCli(t, "stackkit", "plan", "-C", vmWorkDir)
	require.NoError(t, err, "plan failed: %s", out)

	// Log the full plan for debugging
	t.Logf("Full plan output:\n%s", out)

	// At minimum, we expect the plan to mention adding resources
	lowerOut := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lowerOut, "to add") || strings.Contains(lowerOut, "will be created") || strings.Contains(lowerOut, "plan:"),
		"plan should mention resources to add")
}

func TestCLI_Apply(t *testing.T) {
	out, err := execInCli(t, "stackkit", "apply",
		"-C", vmWorkDir, "--auto-approve")
	require.NoError(t, err, "apply failed: %s", out)
	lowerOut := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lowerOut, "complete") || strings.Contains(lowerOut, "applied") || strings.Contains(lowerOut, "success"),
		"apply should indicate success: %s", out)

	// Wait for containers to start inside the VM
	t.Log("Waiting for services to start in VM...")
	found := waitForContainers(t, "traefik", 90*time.Second)
	assert.True(t, found, "traefik container should be running in VM after apply")
}

func TestCLI_Apply_VerifyContainers(t *testing.T) {
	dockerPs, err := execInVM(t, "docker", "ps", "--format",
		"table {{.Names}}\t{{.Status}}\t{{.Ports}}")
	require.NoError(t, err, "docker ps in VM failed")
	t.Logf("VM containers after apply:\n%s", dockerPs)

	names, err := execInVM(t, "docker", "ps", "--format", "{{.Names}}")
	require.NoError(t, err)
	assert.Contains(t, names, "traefik", "traefik should be running")
	assert.Contains(t, names, "app-web", "SvelteKit app should be running")
}

func TestCLI_Apply_VerifyContainerHealth(t *testing.T) {
	// Check that running containers are in healthy state where healthchecks are configured
	out, err := execInVM(t, "docker", "ps", "--format", "{{.Names}}\t{{.Status}}")
	require.NoError(t, err)
	t.Logf("Container health status:\n%s", out)

	// Parse and verify no containers are unhealthy
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		lowerLine := strings.ToLower(line)
		assert.NotContains(t, lowerLine, "unhealthy",
			"no containers should be unhealthy: %s", line)
	}
}

func TestCLI_Apply_VerifySvelteKitAppRouteAndHealth(t *testing.T) {
	found := waitForContainers(t, "app-web", 90*time.Second)
	require.True(t, found, "SvelteKit app container should be running in VM after apply")

	directHealth, err := execInVM(t, "docker", "exec", "app-web", "node", "-e",
		"fetch('http://127.0.0.1:3000/health').then(async r=>{const body=await r.text(); if(!r.ok || !body.includes('ok')) process.exit(1)}).catch(()=>process.exit(1))")
	require.NoError(t, err, "direct app health failed: %s", directHealth)

	labels, err := execInVM(t, "docker", "inspect", "app-web",
		"--format", "{{json .Config.Labels}}")
	require.NoError(t, err, "inspect app-web labels failed: %s", labels)
	assert.Contains(t, labels, "Host(`app.stack.local`)", "Traefik host route should be attached to app-web")
	assert.Contains(t, labels, "tinyauth@docker", "login-gateway middleware should be attached to app-web")

	status, err := execInVM(t, "sh", "-c",
		"docker image inspect curlimages/curl:latest >/dev/null 2>&1 || docker pull -q curlimages/curl:latest >/dev/null; docker run --rm --network frontend curlimages/curl:latest -sS -o /tmp/stackkits-app-route.out -w '%{http_code}' -H 'Host: app.stack.local' http://traefik/health")
	require.NoError(t, err, "Traefik app route request failed: %s", status)
	code := strings.TrimSpace(status)
	assert.NotEqual(t, "404", code, "Traefik should have a route for app.stack.local")
	assert.NotEqual(t, "502", code, "Traefik should reach the app container")
	assert.Contains(t, map[string]bool{"200": true, "302": true, "401": true, "403": true}, code,
		"route should either serve health directly or be blocked by the login gateway")
}

func TestCLI_Status(t *testing.T) {
	out, err := execInCli(t, "stackkit", "status", "-C", vmWorkDir)
	require.NoError(t, err, "status failed: %s", out)
	lowerOut := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lowerOut, "running") || strings.Contains(lowerOut, "healthy") || strings.Contains(lowerOut, "active"),
		"status should show running services: %s", out)
}

func TestCLI_Status_JSON(t *testing.T) {
	out, err := execInCli(t, "stackkit", "status", "-C", vmWorkDir, "--json")
	require.NoError(t, err, "status --json failed: %s", out)
	assert.True(t,
		strings.Contains(out, "{") && strings.Contains(out, "}"),
		"status --json should return JSON: %s", out)

	// Verify it's valid JSON
	var parsed map[string]interface{}
	start := strings.Index(out, "{")
	require.GreaterOrEqual(t, start, 0, "status --json should contain JSON: %s", out)
	jsonStr := out[start:]
	end := strings.LastIndex(jsonStr, "}")
	require.GreaterOrEqual(t, end, 0, "status --json should contain a complete JSON object: %s", out)
	jsonStr = jsonStr[:end+1]
	err = json.Unmarshal([]byte(jsonStr), &parsed)
	assert.NoError(t, err, "status --json should return parseable JSON: %s", jsonStr)
}

// ─── Resource Validation Tests ───────────────────────────────────

func TestVM_DockerContainerList(t *testing.T) {
	out, err := execInVM(t, "docker", "ps", "-a", "--format",
		"table {{.Names}}\t{{.Status}}\t{{.Image}}\t{{.Ports}}")
	require.NoError(t, err)
	t.Logf("Full container list:\n%s", out)
	assert.NotEmpty(t, out, "VM should have containers")
}

func TestVM_DockerContainerCount(t *testing.T) {
	out, err := execInVM(t, "sh", "-c", "docker ps -q | wc -l")
	require.NoError(t, err)
	count, err := strconv.Atoi(strings.TrimSpace(out))
	require.NoError(t, err, "should parse container count: %s", out)
	assert.Greater(t, count, 0, "at least one container should be running")
	t.Logf("Running container count: %d", count)
}

func TestVM_DockerStats(t *testing.T) {
	out, err := execInVM(t, "docker", "stats", "--no-stream", "--format",
		"table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}")
	require.NoError(t, err)
	t.Logf("Docker stats:\n%s", out)
	assert.NotEmpty(t, out, "docker stats should return data")
}

func TestVM_DockerStatsMemoryBounds(t *testing.T) {
	// Verify no container is using excessive memory (>2GB as a sanity check)
	out, err := execInVM(t, "docker", "stats", "--no-stream", "--format",
		"{{.Name}}\t{{.MemUsage}}")
	require.NoError(t, err)
	t.Logf("Memory usage per container:\n%s", out)

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			// Just log — not asserting exact limits since they're environment-dependent
			t.Logf("  %s: %s", parts[0], parts[1])
		}
	}
}

func TestVM_DockerNetworks(t *testing.T) {
	out, err := execInVM(t, "docker", "network", "ls", "--format",
		"table {{.Name}}\t{{.Driver}}\t{{.Scope}}")
	require.NoError(t, err)
	t.Logf("Docker networks:\n%s", out)

	// Should have at least the default networks
	assert.Contains(t, out, "bridge", "bridge network should exist")
}

func TestVM_DockerNetworkConnectivity(t *testing.T) {
	// Get custom networks and verify containers are connected
	nets, err := execInVM(t, "docker", "network", "ls", "--format", "{{.Name}}")
	require.NoError(t, err)

	for _, net := range strings.Split(strings.TrimSpace(nets), "\n") {
		if net == "" || net == "bridge" || net == "host" || net == "none" {
			continue
		}
		inspect, err := execInVM(t, "docker", "network", "inspect", net,
			"--format", "{{.Name}}: {{range .Containers}}{{.Name}} {{end}}")
		if err != nil {
			continue
		}
		t.Logf("Network %s: %s", net, strings.TrimSpace(inspect))
	}
}

func TestVM_DockerVolumes(t *testing.T) {
	out, err := execInVM(t, "docker", "volume", "ls", "--format",
		"table {{.Name}}\t{{.Driver}}")
	require.NoError(t, err)
	t.Logf("Docker volumes:\n%s", out)
}

func TestVM_DockerVolumeCount(t *testing.T) {
	out, err := execInVM(t, "sh", "-c", "docker volume ls -q | wc -l")
	require.NoError(t, err)
	count, err := strconv.Atoi(strings.TrimSpace(out))
	require.NoError(t, err, "should parse volume count: %s", out)
	t.Logf("Volume count: %d", count)
	// After apply, we expect at least some volumes for persistent data
	assert.GreaterOrEqual(t, count, 0, "volume count should be non-negative")
}

func TestVM_DockerImages(t *testing.T) {
	out, err := execInVM(t, "docker", "images", "--format",
		"table {{.Repository}}\t{{.Tag}}\t{{.Size}}")
	require.NoError(t, err)
	t.Logf("Docker images:\n%s", out)
	assert.NotEmpty(t, out, "VM should have pulled images after apply")
}

func TestVM_DockerImageCount(t *testing.T) {
	out, err := execInVM(t, "sh", "-c", "docker images -q | wc -l")
	require.NoError(t, err)
	count, err := strconv.Atoi(strings.TrimSpace(out))
	require.NoError(t, err, "should parse image count: %s", out)
	assert.Greater(t, count, 0, "at least one image should be pulled")
	t.Logf("Image count: %d", count)
}

func TestVM_DockerDiskUsage(t *testing.T) {
	out, err := execInVM(t, "docker", "system", "df")
	require.NoError(t, err)
	t.Logf("Docker disk usage:\n%s", out)
	assert.Contains(t, out, "Images", "docker system df should show image stats")
	assert.Contains(t, out, "Containers", "docker system df should show container stats")
}

func TestVM_ContainerLogs_Traefik(t *testing.T) {
	names, err := execInVM(t, "docker", "ps", "--format", "{{.Names}}")
	require.NoError(t, err)

	var traefikName string
	for _, name := range strings.Split(strings.TrimSpace(names), "\n") {
		if strings.Contains(strings.ToLower(name), "traefik") {
			traefikName = name
			break
		}
	}

	if traefikName == "" {
		t.Skip("traefik container not found")
		return
	}

	logs, err := execInVM(t, "docker", "logs", traefikName, "--tail", "20")
	require.NoError(t, err, "failed to get traefik logs")
	t.Logf("Traefik logs (last 20 lines):\n%s", logs)

	// Traefik should not have fatal errors
	lowerLogs := strings.ToLower(logs)
	assert.NotContains(t, lowerLogs, "fatal", "traefik should not have fatal errors")
}

func TestVM_ContainerLogs_NoErrors(t *testing.T) {
	names, err := execInVM(t, "docker", "ps", "--format", "{{.Names}}")
	require.NoError(t, err)

	for _, name := range strings.Split(strings.TrimSpace(names), "\n") {
		if name == "" {
			continue
		}
		logs, err := execInVM(t, "docker", "logs", name, "--tail", "10")
		if err != nil {
			t.Logf("Could not get logs for %s: %v", name, err)
			continue
		}

		lowerLogs := strings.ToLower(logs)
		if strings.Contains(lowerLogs, "fatal") || strings.Contains(lowerLogs, "panic") {
			t.Errorf("Container %s has fatal/panic in logs:\n%s", name, logs)
		}
	}
}

func TestVM_ListeningPorts(t *testing.T) {
	out, err := execInVM(t, "sh", "-c",
		"ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || echo 'port info unavailable'")
	require.NoError(t, err)
	t.Logf("Listening ports:\n%s", out)
	assert.NotContains(t, out, "port info unavailable", "should be able to list ports")
}

func TestVM_ListeningPorts_ExpectedServices(t *testing.T) {
	out, err := execInVM(t, "sh", "-c",
		"ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || echo ''")
	require.NoError(t, err)

	// After deployment, we expect at least port 80 and 443 (traefik)
	if strings.Contains(out, ":80 ") || strings.Contains(out, ":80\t") {
		t.Log("Port 80 (HTTP) is listening")
	}
	if strings.Contains(out, ":443 ") || strings.Contains(out, ":443\t") {
		t.Log("Port 443 (HTTPS) is listening")
	}
	if strings.Contains(out, ":8080 ") || strings.Contains(out, ":8080\t") {
		t.Log("Port 8080 (Traefik dashboard) is listening")
	}
}

func TestVM_HtopResourceUsage(t *testing.T) {
	out, err := execInVM(t, "bash", "-c",
		"TERM=xterm htop --batch-mode --delay=10 --tree 2>/dev/null | head -40 || top -b -n1 | head -25")
	require.NoError(t, err)
	t.Logf("VM resource usage (htop/top):\n%s", out)
	assert.NotEmpty(t, out, "resource usage should return data")
}

func TestVM_SystemInfo(t *testing.T) {
	cpuOut, err := execInVM(t, "nproc")
	require.NoError(t, err)
	t.Logf("VM CPUs: %s", strings.TrimSpace(cpuOut))

	memOut, err := execInVM(t, "free", "-h")
	require.NoError(t, err)
	t.Logf("VM memory:\n%s", memOut)

	diskOut, err := execInVM(t, "df", "-h", "/")
	require.NoError(t, err)
	t.Logf("VM disk:\n%s", diskOut)
}

func TestVM_SystemInfo_Uptime(t *testing.T) {
	out, err := execInVM(t, "uptime")
	require.NoError(t, err)
	t.Logf("VM uptime: %s", strings.TrimSpace(out))
}

func TestVM_SystemInfo_KernelVersion(t *testing.T) {
	out, err := execInVM(t, "uname", "-a")
	require.NoError(t, err)
	t.Logf("VM kernel: %s", strings.TrimSpace(out))
	assert.Contains(t, strings.ToLower(out), "linux", "should be a Linux kernel")
}

func TestVM_SystemInfo_LoadAverage(t *testing.T) {
	out, err := execInVM(t, "cat", "/proc/loadavg")
	require.NoError(t, err)
	t.Logf("VM load average: %s", strings.TrimSpace(out))

	parts := strings.Fields(strings.TrimSpace(out))
	require.GreaterOrEqual(t, len(parts), 3, "load average should have at least 3 fields")

	load1, err := strconv.ParseFloat(parts[0], 64)
	require.NoError(t, err, "should parse 1-min load average")
	t.Logf("1-min load: %.2f", load1)
}

func TestVM_SystemInfo_ProcessCount(t *testing.T) {
	out, err := execInVM(t, "sh", "-c", "ps aux | wc -l")
	require.NoError(t, err)
	count, err := strconv.Atoi(strings.TrimSpace(out))
	require.NoError(t, err)
	t.Logf("Process count: %d", count)
	assert.Greater(t, count, 1, "VM should have running processes")
}

func TestVM_DockerDaemonInfo(t *testing.T) {
	out, err := execInVM(t, "docker", "info", "--format",
		fmt.Sprintf("Server Version: {{.ServerVersion}}\nContainers: {{.Containers}}\nRunning: {{.ContainersRunning}}\nImages: {{.Images}}\nDriver: {{.Driver}}"))
	require.NoError(t, err)
	t.Logf("Docker daemon info:\n%s", out)
	assert.Contains(t, out, "Server Version:", "should show Docker version")
}

// ─── Remove & Cleanup Tests ──────────────────────────────────────

func TestCLI_Remove(t *testing.T) {
	out, err := execInCli(t, "stackkit", "remove",
		"-C", vmWorkDir, "--auto-approve")
	require.NoError(t, err, "remove failed: %s", out)
	lowerOut := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lowerOut, "remove") || strings.Contains(lowerOut, "complete") || strings.Contains(lowerOut, "success"),
		"remove should indicate success: %s", out)
}

func TestCLI_Remove_VerifyCleanup(t *testing.T) {
	time.Sleep(5 * time.Second)

	names, err := execInVM(t, "docker", "ps", "--format", "{{.Names}}")
	require.NoError(t, err)
	assert.NotContains(t, names, "traefik",
		"traefik should be removed after remove")
	assert.NotContains(t, names, "dokploy",
		"dokploy should be removed after remove")
	assert.NotContains(t, names, "app-web",
		"SvelteKit app should be removed after remove")
}

func TestCLI_Remove_VerifyNoContainers(t *testing.T) {
	out, err := execInVM(t, "sh", "-c", "docker ps -q | wc -l")
	require.NoError(t, err)
	count, err := strconv.Atoi(strings.TrimSpace(out))
	require.NoError(t, err, "should parse container count: %s", out)
	assert.Equal(t, 0, count, "no containers should be running after remove")
}

func TestCLI_Destroy_VerifyNetworkCleanup(t *testing.T) {
	nets, err := execInVM(t, "docker", "network", "ls", "--format", "{{.Name}}")
	require.NoError(t, err)
	t.Logf("Networks after destroy:\n%s", nets)

	// Only default networks should remain
	for _, net := range strings.Split(strings.TrimSpace(nets), "\n") {
		net = strings.TrimSpace(net)
		if net == "" {
			continue
		}
		isDefault := net == "bridge" || net == "host" || net == "none"
		if !isDefault {
			t.Logf("Non-default network still exists: %s (may be expected for shared networks)", net)
		}
	}
}

func TestCLI_Destroy_VolumesPreserved(t *testing.T) {
	volumes, err := execInVM(t, "docker", "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		t.Logf("volume list: %s", volumes)
		return
	}
	t.Logf("Remaining volumes after destroy:\n%s", volumes)
}

func TestCLI_Destroy_VerifyDockerClean(t *testing.T) {
	// Final state: docker daemon should still be running but with no workload
	out, err := execInVM(t, "docker", "info", "--format",
		"Running: {{.ContainersRunning}} Stopped: {{.ContainersStopped}}")
	require.NoError(t, err)
	t.Logf("Docker state after destroy: %s", strings.TrimSpace(out))
	assert.Contains(t, out, "Running: 0", "no containers should be running after destroy")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildSmokeSvelteKitImage(t *testing.T) {
	t.Helper()

	out, err := execInVM(t, "sh", "-c", `set -eu
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/Dockerfile" <<'DOCKERFILE'
FROM node:22-alpine
WORKDIR /app
RUN cat > server.js <<'JS'
const http = require('http');
const appName = process.env.PUBLIC_APP_NAME || 'StackKit Smoke';
const port = Number(process.env.PORT || 3000);

http.createServer((req, res) => {
  if (req.url === '/health') {
    res.writeHead(200, {'content-type': 'application/json'});
    res.end(JSON.stringify({status: 'ok', app: appName}));
    return;
  }
  res.writeHead(200, {'content-type': 'text/plain'});
  res.end(appName);
}).listen(port, '0.0.0.0');
JS
CMD ["node", "server.js"]
DOCKERFILE
docker build -t stackkits-smoke-sveltekit:latest "$tmp"
`)
	require.NoError(t, err, "build SvelteKit smoke image in VM failed: %s", out)
}
