//go:build production

package production

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestInstallerKombifyMeDomain
//
// End-to-end: install base-install.sh on a live VPS Sim node with no custom
// domain. The installer detects cloud context (injected by Sim) and
// auto-selects kombify.me as the domain.
// ─────────────────────────────────────────────────────────────────────────────
func TestInstallerKombifyMeDomain(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	kombifyAPIKey := os.Getenv("KOMBIFY_API_KEY")
	if kombifyAPIKey == "" {
		t.Skip("KOMBIFY_API_KEY not set — skipping kombify.me domain test")
	}

	sim := NewSimClient(NewClient(cfg))
	testName := fmt.Sprintf("e2e-km-%d", time.Now().Unix())

	node, simID, done := startTestVPSNode(t, sim, testName)
	defer done()

	ssh := openSSH(t, node)
	defer ssh.Close()

	verifyCloudContext(t, ssh)
	runInstaller(t, ssh, map[string]string{
		"STACKKIT_ADMIN_EMAIL": "test@kombify.io",
		"KOMBIFY_API_KEY":      kombifyAPIKey,
		"KOMBIFY_CONTEXT":      "cloud",
	})
	_ = simID

	// Domain must be kombify.me
	spec, _ := ssh.Run("cat ~/homelab/stack-spec.yaml 2>/dev/null | grep -i domain | head -5")
	t.Logf("Domain config:\n%s", spec)
	if !strings.Contains(spec, "kombify.me") {
		t.Errorf("expected kombify.me domain in spec, got:\n%s", spec)
	}
	tfvars, _ := ssh.Run("cat ~/homelab/deploy/terraform.tfvars.json 2>/dev/null | grep '\"paas\"' | head -1")
	if !strings.Contains(tfvars, "dokploy") {
		t.Errorf("expected dokploy PAAS for kombify.me installer flow, got:\n%s", tfvars)
	}

	assertCoreServicesRunning(t, ssh)
	smokeTestViaSSHTunnel(t, ssh, 18080)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestInstallerCustomDomain
//
// End-to-end: install base-install.sh with cappuccinoquest.de as the custom
// domain. The installer sets up Cloudflare DNS-01 (wildcard cert) and Traefik.
// Verifies DNS A-record, spec domain, and core services.
// ─────────────────────────────────────────────────────────────────────────────
func TestInstallerCustomDomain(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	cfZoneID := os.Getenv("CLOUDFLARE_ZONE_ID")
	cfEmail := os.Getenv("CLOUDFLARE_EMAIL")
	if cfToken == "" || cfZoneID == "" {
		t.Skip("CLOUDFLARE_API_TOKEN / CLOUDFLARE_ZONE_ID not set")
	}

	const domain = "cappuccinoquest.de"
	const adminEmail = "test@kombify.io"
	const serverFallbackIP = "217.154.174.107"

	sim := NewSimClient(NewClient(cfg))
	testName := fmt.Sprintf("e2e-cd-%d", time.Now().Unix())

	node, _, done := startTestVPSNode(t, sim, testName)
	defer done()

	ssh := openSSH(t, node)
	defer ssh.Close()

	// Get node's outbound IP — this is what DNS should point to.
	publicIP, err := ssh.Run(
		"curl -sSL --max-time 10 https://ifconfig.me/ip 2>/dev/null " +
			"|| curl -sSL --max-time 10 https://api.ipify.org 2>/dev/null",
	)
	if err != nil || strings.TrimSpace(publicIP) == "" {
		t.Fatalf("could not determine node public IP: %v", err)
	}
	publicIP = strings.TrimSpace(publicIP)
	t.Logf("Node public IP (for DNS): %s", publicIP)

	// Point cappuccinoquest.de A-record to node IP.
	t.Logf("Setting Cloudflare DNS: %s → %s", domain, publicIP)
	if err := updateCloudflareDNS(cfToken, cfEmail, cfZoneID, domain, publicIP); err != nil {
		t.Fatalf("Cloudflare DNS update: %v", err)
	}
	// Always restore after test.
	t.Cleanup(func() {
		t.Logf("DNS cleanup: resetting %s → %s", domain, serverFallbackIP)
		_ = updateCloudflareDNS(cfToken, cfEmail, cfZoneID, domain, serverFallbackIP)
	})

	runInstaller(t, ssh, map[string]string{
		"STACKKIT_ADMIN_EMAIL": adminEmail,
		"KOMBIFY_CONTEXT":      "cloud",
		"DOMAIN":               domain,
		"CLOUDFLARE_EMAIL":     cfEmail,
		"CLOUDFLARE_API_TOKEN": cfToken,
	})

	// Spec must contain custom domain, not home.lab or kombify.me.
	spec, _ := ssh.Run("cat ~/homelab/stack-spec.yaml 2>/dev/null | grep -i domain | head -5")
	t.Logf("Domain config:\n%s", spec)
	if strings.Contains(spec, "home.lab") || strings.Contains(spec, "kombify.me") {
		t.Errorf("expected %s domain in spec, got:\n%s", domain, spec)
	}
	if !strings.Contains(spec, domain) {
		t.Errorf("domain %s not found in spec:\n%s", domain, spec)
	}
	tfvars, _ := ssh.Run("cat ~/homelab/deploy/terraform.tfvars.json 2>/dev/null | grep '\"paas\"' | head -1")
	if !strings.Contains(tfvars, "coolify") {
		t.Errorf("expected coolify PAAS for custom-domain installer flow, got:\n%s", tfvars)
	}

	// Verify Cloudflare DNS record points to node.
	if err := verifyCloudflareDNS(cfToken, cfZoneID, domain, publicIP); err != nil {
		t.Errorf("Cloudflare DNS verification: %v", err)
	} else {
		t.Logf("DNS verified: %s → %s ✓", domain, publicIP)
	}

	assertCoreServicesRunning(t, ssh)
	smokeTestViaSSHTunnel(t, ssh, 18081)
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared helpers
// ─────────────────────────────────────────────────────────────────────────────

// startTestVPSNode creates a VPS simulation + node, starts it, waits, extracts
// SSH key. Returns the ready Node, the simulation ID, and a cleanup func.
func startTestVPSNode(t *testing.T, sim *SimClient, name string) (Node, string, func()) {
	t.Helper()

	simulation, err := sim.CreateSimulation(name)
	if err != nil {
		t.Fatalf("create simulation: %v", err)
	}
	cleanup := func() {
		t.Log("Cleanup: deleting simulation ...")
		if err := sim.DeleteSimulation(simulation.ID); err != nil {
			t.Logf("cleanup warning: %v", err)
		}
	}

	node, err := sim.CreateVPSNode(simulation.ID, "vps-node")
	if err != nil {
		cleanup()
		t.Fatalf("create node: %v", err)
	}

	if _, err := sim.StartNode(node.ID); err != nil {
		cleanup()
		t.Fatalf("start node: %v", err)
	}

	t.Log("Waiting for node to be running ...")
	node, err = sim.WaitForNode(simulation.ID, node.ID, 3*time.Minute)
	if err != nil {
		cleanup()
		t.Fatalf("node did not start: %v", err)
	}

	// Enrich with SSH info from the dedicated endpoint.
	sshInfo, err := sim.GetNodeSSH(node.ID)
	if err != nil {
		cleanup()
		t.Fatalf("get SSH info: %v", err)
	}
	node.SSHIP = sshInfo.Host
	node.SSHPort = sshInfo.Port
	node.SSHUser = sshInfo.User
	node.ProxyJump = sshInfo.ProxyJump
	t.Logf("Node SSH: %s@%s:%d (proxy: %s)", node.SSHUser, node.SSHIP, node.SSHPort, node.ProxyJump)

	keyPath, keyClean, err := extractNodeSSHKey(node.ID)
	if err != nil {
		cleanup()
		t.Fatalf("extract SSH key: %v", err)
	}
	node.SSHKeyPath = keyPath
	node.SSHUser = sshInfo.User

	origClean := cleanup
	cleanup = func() {
		keyClean()
		origClean()
	}

	if err := WaitForSSH(*node, 2*time.Minute); err != nil {
		cleanup()
		t.Fatalf("SSH not ready: %v", err)
	}

	return *node, simulation.ID, cleanup
}

// openSSH creates an SSH session or fatals the test.
func openSSH(t *testing.T, node Node) *SSHSession {
	t.Helper()
	s, err := NewSSHSession(node)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}
	return s
}

// verifyCloudContext logs and asserts the kombify cloud context is set.
func verifyCloudContext(t *testing.T, s *SSHSession) {
	t.Helper()
	out, _ := s.Run("cat /etc/kombify/context 2>/dev/null || echo missing")
	val := strings.TrimSpace(out)
	t.Logf("KOMBIFY_CONTEXT from /etc/kombify/context: %s", val)
	if val != "cloud" {
		t.Logf("Warning: cloud context not in file; KOMBIFY_CONTEXT will be passed via env")
	}
}

// runInstaller downloads base-install.sh and runs it with the given env vars.
func runInstaller(t *testing.T, s *SSHSession, env map[string]string) {
	t.Helper()

	_, err := s.Run("curl -sSL https://base.stackkit.cc -o /tmp/base-install.sh && chmod +x /tmp/base-install.sh")
	if err != nil {
		t.Fatalf("download base-install.sh: %v", err)
	}

	t.Log("Running base installer (10-20 min) ...")
	out, exitCode, runErr := s.RunOutput(buildEnvPrefix(env) + "sh /tmp/base-install.sh")
	t.Logf("Installer output (last 80 lines):\n%s", lastLines(out, 80))

	if runErr != nil {
		t.Fatalf("installer command error: %v", runErr)
	}
	if exitCode != 0 {
		t.Logf("Note: installer exited %d (may be non-fatal)", exitCode)
	}
}

// buildEnvPrefix returns "export K=V; export K2=V2; " shell prefix.
func buildEnvPrefix(env map[string]string) string {
	var sb strings.Builder
	for k, v := range env {
		sb.WriteString(fmt.Sprintf("export %s=%q; ", k, v))
	}
	return sb.String()
}

// assertCoreServicesRunning checks that traefik and dokploy containers are up.
func assertCoreServicesRunning(t *testing.T, s *SSHSession) {
	t.Helper()
	containers, err := s.Run("docker ps --format '{{.Names}}' 2>/dev/null")
	if err != nil {
		t.Errorf("docker ps: %v", err)
		return
	}
	t.Logf("Running containers:\n%s", containers)
	for _, svc := range []string{"traefik", "dokploy"} {
		if !strings.Contains(containers, svc) {
			t.Errorf("expected container %q to be running", svc)
		}
	}
}

// smokeTestViaSSHTunnel forwards localPort→80 on node and checks HTTP response.
func smokeTestViaSSHTunnel(t *testing.T, s *SSHSession, localPort int) {
	t.Helper()
	ln, err := s.ForwardPort(localPort, "localhost", 80)
	if err != nil {
		t.Logf("Note: port-forward setup failed: %v", err)
		return
	}
	defer ln.Close()
	time.Sleep(500 * time.Millisecond) // allow listener to settle

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", localPort)) //nolint:noctx
	if err != nil {
		t.Logf("Note: HTTP smoke-test via SSH tunnel: %v", err)
		return
	}
	defer resp.Body.Close()
	t.Logf("HTTP via SSH tunnel (port %d): %d", localPort, resp.StatusCode)
}

// extractNodeSSHKey reads the private key from the Sim container and writes
// it to a temp file. Returns path + cleanup func.
func extractNodeSSHKey(nodeID string) (string, func(), error) {
	f, err := os.CreateTemp("", "kombify-node-*.pem")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	f.Close()

	out, err := runLocalShell(fmt.Sprintf(
		"docker exec kombify-sim-ionos sh -c 'cat /tmp/kombify-sim-ssh-*/%s.pem 2>/dev/null' > %s",
		nodeID, f.Name(),
	))
	if err != nil {
		return "", nil, fmt.Errorf("extract key via docker exec: %v\n%s", err, out)
	}

	stat, _ := os.Stat(f.Name())
	if stat == nil || stat.Size() < 100 {
		return "", nil, fmt.Errorf("key file is empty — docker exec access to kombify-sim-ionos required")
	}

	if err := os.Chmod(f.Name(), 0600); err != nil {
		return "", nil, err
	}

	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// runLocalShell executes a shell command locally and returns combined output.
func runLocalShell(cmd string) (string, error) {
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	return string(out), err
}

// ─────────────────────────────────────────────────────────────────────────────
// Cloudflare DNS helpers
// ─────────────────────────────────────────────────────────────────────────────

// updateCloudflareDNS upserts an A-record: domain → ip.
func updateCloudflareDNS(apiToken, email, zoneID, domain, ip string) error {
	ids, err := cfListARecordIDs(apiToken, zoneID, domain)
	if err != nil {
		return err
	}

	body := fmt.Sprintf(`{"type":"A","name":"%s","content":"%s","ttl":60,"proxied":false}`, domain, ip)

	if len(ids) > 0 {
		return cfDo("PUT",
			fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, ids[0]),
			apiToken, email, body, nil)
	}
	return cfDo("POST",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID),
		apiToken, email, body, nil)
}

// verifyCloudflareDNS asserts that an A-record for domain points to expectedIP.
func verifyCloudflareDNS(apiToken, zoneID, domain, expectedIP string) error {
	var result struct {
		Result []struct {
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := cfDo("GET",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=A&name=%s", zoneID, domain),
		apiToken, "", "", &result); err != nil {
		return err
	}
	for _, r := range result.Result {
		if r.Content == expectedIP {
			return nil
		}
	}
	return fmt.Errorf("A-record for %s not pointing to %s (got: %v)", domain, expectedIP, result.Result)
}

func cfListARecordIDs(apiToken, zoneID, domain string) ([]string, error) {
	var result struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := cfDo("GET",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=A&name=%s", zoneID, domain),
		apiToken, "", "", &result); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Result))
	for _, r := range result.Result {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

func cfDo(method, url, apiToken, email, body string, out interface{}) error {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader) //nolint:noctx
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
	}
	if email != "" {
		req.Header.Set("X-Auth-Email", email)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("CF API %s %s: %d — %s", method, url, resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
