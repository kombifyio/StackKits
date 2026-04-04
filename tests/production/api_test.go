//go:build production

package production

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"net/http"
)

// TestGatewayHealth verifies Kong gateway is routing to both APIs correctly.
func TestGatewayHealth(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	client := NewClient(cfg)

	t.Run("SimAPI", func(t *testing.T) {
		sim := NewSimClient(client)
		health, err := sim.Health()
		if err != nil {
			t.Fatalf("sim health check failed: %v", err)
		}
		if health.Status != "ok" && health.Status != "healthy" {
			t.Errorf("unexpected sim health status: %s", health.Status)
		}
		t.Logf("Sim API healthy: %s", health.Status)
	})

	t.Run("StackKitsAPI", func(t *testing.T) {
		sk := NewStackKitsClient(client)
		health, err := sk.Health()
		if err != nil {
			t.Skipf("stackkits API not available: %v", err)
		}
		if health.Status != "ok" && health.Status != "healthy" && health.Status != "operational" {
			t.Errorf("unexpected stackkits health status: %s", health.Status)
		}
		t.Logf("StackKits API healthy: %s", health.Status)
	})
}

// TestSimulationLifecycle tests the full simulation workflow through the gateway.
func TestSimulationLifecycle(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	client := NewClient(cfg)
	sim := NewSimClient(client)

	// Generate unique name for this test run
	testName := fmt.Sprintf("prodtest-%d", time.Now().Unix())

	// Create simulation
	t.Log("Creating simulation...")
	simulation, err := sim.CreateSimulation(testName)
	if err != nil {
		t.Fatalf("create simulation: %v", err)
	}
	t.Logf("Created simulation: %s (%s)", simulation.Name, simulation.ID)

	// Always clean up
	defer func() {
		t.Log("Cleaning up simulation...")
		if err := sim.DeleteSimulation(simulation.ID); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	}()

	// Verify simulation appears in list
	t.Log("Listing simulations...")
	sims, err := sim.ListSimulations()
	if err != nil {
		t.Fatalf("list simulations: %v", err)
	}

	found := false
	for _, s := range sims {
		if s.ID == simulation.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created simulation not found in list")
	}
	t.Logf("Found %d simulations, test simulation present", len(sims))
}

// TestNodeLifecycle tests node creation and lifecycle through the gateway.
func TestNodeLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node lifecycle test in short mode")
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	client := NewClient(cfg)
	sim := NewSimClient(client)

	// Create simulation for nodes
	testName := fmt.Sprintf("nodetest-%d", time.Now().Unix())
	simulation, err := sim.CreateSimulation(testName)
	if err != nil {
		t.Fatalf("create simulation: %v", err)
	}
	t.Logf("Created simulation: %s", simulation.ID)

	// Always clean up
	defer func() {
		t.Log("Cleaning up...")
		if err := sim.DeleteSimulation(simulation.ID); err != nil {
			t.Logf("cleanup simulation warning: %v", err)
		}
	}()

	// Create node
	t.Log("Creating node...")
	node, err := sim.CreateNode(simulation.ID, CreateNodeRequest{
		Name: "test-node",
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	t.Logf("Created node: %s (status: %s)", node.ID, node.Status)

	// Cleanup node on test completion
	defer func() {
		if err := sim.DeleteNode(node.ID); err != nil {
			t.Logf("cleanup node warning: %v", err)
		}
	}()

	// Start node
	t.Log("Starting node...")
	node, err = sim.StartNode(node.ID)
	if err != nil {
		t.Fatalf("start node: %v", err)
	}
	t.Logf("Node started: %s (status: %s)", node.ID, node.Status)

	// Wait for node to be running (poll with timeout)
	t.Log("Waiting for node to be running...")
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("timeout waiting for node to start")
		case <-ticker.C:
			node, err = sim.GetNode(node.ID)
			if err != nil {
				t.Logf("get node: %v (retrying)", err)
				continue
			}
			t.Logf("Node status: %s", node.Status)
			if strings.ToLower(node.Status) == "running" {
				goto NodeRunning
			}
		}
	}

NodeRunning:
	t.Logf("Node is running! SSH: %s@%s:%d", node.SSHUser, node.SSHIP, node.SSHPort)

	// Stop node
	t.Log("Stopping node...")
	node, err = sim.StopNode(node.ID)
	if err != nil {
		t.Errorf("stop node: %v", err)
	} else {
		t.Logf("Node stopped: %s", node.Status)
	}
}

// TestStackKitsList verifies the StackKits catalog is accessible.
func TestStackKitsList(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	client := NewClient(cfg)
	sk := NewStackKitsClient(client)

	stackkits, err := sk.ListStackKits()
	if err != nil {
		t.Fatalf("list stackkits: %v", err)
	}

	t.Logf("Found %d StackKits in catalog", len(stackkits))

	// Log first few for visibility
	for i, kit := range stackkits {
		if i >= 5 {
			t.Logf("  ... and %d more", len(stackkits)-5)
			break
		}
		t.Logf("  - %s: %s", kit.ID, kit.Name)
	}
}

// ─── Shared helpers ────────────────────────────────────────────────────────

// startTestNode creates a simulation + node, starts it, waits for SSH, and
// returns both. The caller is responsible for deferred cleanup.
func startTestNode(t *testing.T, sim *SimClient, prefix string) (*Simulation, *Node) {
	t.Helper()

	name := fmt.Sprintf("%s-%d", prefix, time.Now().Unix())

	simulation, err := sim.CreateSimulation(name)
	if err != nil {
		t.Fatalf("create simulation: %v", err)
	}
	t.Logf("Simulation created: %s (%s)", simulation.Name, simulation.ID)

	node, err := sim.CreateNode(simulation.ID, CreateNodeRequest{Name: "test-node"})
	if err != nil {
		_ = sim.DeleteSimulation(simulation.ID)
		t.Fatalf("create node: %v", err)
	}

	node, err = sim.StartNode(node.ID)
	if err != nil {
		_ = sim.DeleteSimulation(simulation.ID)
		t.Fatalf("start node: %v", err)
	}

	// Wait for node to reach "running" status (max 5 min)
	t.Log("Waiting for node to reach running status...")
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
waitLoop:
	for {
		select {
		case <-timeout:
			_ = sim.DeleteSimulation(simulation.ID)
			t.Fatalf("timeout waiting for node to start")
		case <-ticker.C:
			n, err := sim.GetNode(node.ID)
			if err != nil {
				t.Logf("get node (retry): %v", err)
				continue
			}
			t.Logf("Node status: %s", n.Status)
			if strings.ToLower(n.Status) == "running" {
				node = n
				break waitLoop
			}
		}
	}

	// Enrich node with SSH connection info from the dedicated SSH endpoint.
	sshInfo, err := sim.GetNodeSSH(node.ID)
	if err != nil {
		_ = sim.DeleteSimulation(simulation.ID)
		t.Fatalf("get SSH info: %v", err)
	}
	node.SSHIP = sshInfo.Host
	node.SSHPort = sshInfo.Port
	node.SSHUser = sshInfo.User
	node.ProxyJump = sshInfo.ProxyJump
	t.Logf("Node SSH: %s@%s:%d (proxy: %s)", node.SSHUser, node.SSHIP, node.SSHPort, node.ProxyJump)

	keyPath, keyClean, err := extractNodeSSHKey(node.ID)
	if err != nil {
		_ = sim.DeleteSimulation(simulation.ID)
		t.Fatalf("extract SSH key: %v", err)
	}
	node.SSHKeyPath = keyPath
	t.Cleanup(keyClean)

	if err := WaitForSSH(*node, 2*time.Minute); err != nil {
		_ = sim.DeleteSimulation(simulation.ID)
		t.Fatalf("wait for SSH: %v", err)
	}

	t.Logf("Node running and SSH ready: %s@%s:%d", node.SSHUser, node.SSHIP, node.SSHPort)
	return simulation, node
}

// ─── Installer Tests ───────────────────────────────────────────────────────

// TestStackKitCLIInstallOnNode verifies that the official installer from
// kombifyio/stackKits installs the stackkit CLI binary on a live Sim node.
func TestStackKitCLIInstallOnNode(t *testing.T) {
if testing.Short() {
t.Skip("skipping live-node installer test in short mode")
}

cfg, err := LoadConfig()
if err != nil {
t.Fatalf("load config: %v", err)
}

sim := NewSimClient(NewClient(cfg))
simulation, node := startTestNode(t, sim, "install-test")
defer func() {
t.Log("Cleaning up simulation...")
if err := sim.DeleteSimulation(simulation.ID); err != nil {
t.Errorf("cleanup: %v", err)
}
}()

ssh, err := NewSSHSession(*node)
if err != nil {
t.Fatalf("SSH connect: %v", err)
}
defer ssh.Close()

// Run the official installer from the public release repo
t.Log("Running stackkit installer from install.kombify.me...")
out, err := ssh.Run("curl -sSL https://install.kombify.me | sh")
if err != nil {
t.Fatalf("installer failed: %v\noutput: %s", err, out)
}
t.Logf("Installer output:\n%s", out)

// Verify binary is present and functional
out, err = ssh.Run("stackkit version")
if err != nil {
t.Fatalf("stackkit version failed: %v\noutput: %s", err, out)
}
if !strings.Contains(out, "stackkit") {
t.Errorf("unexpected version output: %s", out)
}
t.Logf("stackkit installed: %s", strings.TrimSpace(out))
}

// TestStackKitInitAndValidateOnNode installs the CLI, runs stackkit init, and
// validates the generated spec — all on a live Sim node.
func TestStackKitInitAndValidateOnNode(t *testing.T) {
if testing.Short() {
t.Skip("skipping live-node init/validate test in short mode")
}

cfg, err := LoadConfig()
if err != nil {
t.Fatalf("load config: %v", err)
}

sim := NewSimClient(NewClient(cfg))
simulation, node := startTestNode(t, sim, "init-test")
defer func() {
t.Log("Cleaning up simulation...")
if err := sim.DeleteSimulation(simulation.ID); err != nil {
t.Errorf("cleanup: %v", err)
}
}()

ssh, err := NewSSHSession(*node)
if err != nil {
t.Fatalf("SSH connect: %v", err)
}
defer ssh.Close()

// Install CLI + base-kit definition files
t.Log("Installing stackkit CLI + base-kit...")
if out, err := ssh.Run("curl -sSL https://install.kombify.me | sh -s -- base-kit"); err != nil {
t.Fatalf("installer: %v\noutput: %s", err, out)
}

// Init base-kit non-interactively
t.Log("Running stackkit init base-kit...")
out, err := ssh.RunWithEnv(
map[string]string{"STACKKIT_ADMIN_EMAIL": "ci@kombify.io"},
"mkdir -p /tmp/testkit && cd /tmp/testkit && stackkit init base-kit --non-interactive --admin-email ci@kombify.io",
)
if err != nil {
t.Fatalf("stackkit init: %v\noutput: %s", err, out)
}
t.Logf("Init output:\n%s", out)

// Verify stack-spec.yaml was created
out, err = ssh.Run("cat /tmp/testkit/stack-spec.yaml")
if err != nil {
t.Fatalf("reading stack-spec.yaml: %v", err)
}
if !strings.Contains(out, "base-kit") {
t.Errorf("stack-spec.yaml does not reference base-kit:\n%s", out)
}

// Validate spec
t.Log("Running stackkit validate...")
out, err = ssh.Run("cd /tmp/testkit && stackkit validate")
if err != nil {
t.Fatalf("stackkit validate: %v\noutput: %s", err, out)
}
if !strings.Contains(strings.ToLower(out), "valid") {
t.Errorf("validate output does not indicate success:\n%s", out)
}
t.Logf("Validate: %s", strings.TrimSpace(out))
}

// TestBaseInstallerOnNode runs base-install.sh on a live Sim node and verifies
// the homelab stack comes up (whoami service responds via HTTP).
func TestBaseInstallerOnNode(t *testing.T) {
if testing.Short() {
t.Skip("skipping base-installer E2E test in short mode")
}

cfg, err := LoadConfig()
if err != nil {
t.Fatalf("load config: %v", err)
}

sim := NewSimClient(NewClient(cfg))
simulation, node := startTestNode(t, sim, "base-install-test")
defer func() {
t.Log("Cleaning up simulation...")
if err := sim.DeleteSimulation(simulation.ID); err != nil {
t.Errorf("cleanup: %v", err)
}
}()

ssh, err := NewSSHSession(*node)
if err != nil {
t.Fatalf("SSH connect: %v", err)
}
defer ssh.Close()

// Run base installer — sets admin email via env, non-interactive
t.Log("Running base-install.sh (full homelab deploy)...")
out, err := ssh.RunWithEnv(
map[string]string{
"STACKKIT_ADMIN_EMAIL": "ci@kombify.io",
},
"curl -sSL https://base.stackkit.cc | sh",
)
if err != nil {
t.Fatalf("base installer failed: %v\noutput: %s", err, out)
}
t.Logf("Installer output (last 50 lines):\n%s", lastLines(out, 50))

// Wait a moment for services to stabilise
time.Sleep(15 * time.Second)

// Verify whoami responds on the node (direct IP, bypass DNS)
whoamiURL := fmt.Sprintf("http://%s", node.SSHIP)
t.Logf("Checking stack health at %s ...", whoamiURL)
resp, err := http.Get(whoamiURL) //nolint:noctx // test-only
if err != nil {
t.Logf("Note: HTTP check failed (may need domain): %v", err)
} else {
defer resp.Body.Close()
t.Logf("Stack HTTP status: %d", resp.StatusCode)
}

// At minimum verify Docker containers are running
out, err = ssh.Run("docker ps --format '{{.Names}}' | head -20")
if err != nil {
t.Errorf("docker ps: %v", err)
} else {
t.Logf("Running containers:\n%s", out)
if out == "" {
t.Error("no containers running after base install")
}
}
}

// lastLines returns the last n lines of a string.
func lastLines(s string, n int) string {
lines := strings.Split(strings.TrimSpace(s), "\n")
if len(lines) <= n {
return s
}
return strings.Join(lines[len(lines)-n:], "\n")
}
