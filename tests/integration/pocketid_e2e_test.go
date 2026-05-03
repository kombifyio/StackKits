//go:build integration

// End-to-end PocketID v2 integration tests for Phase 1 / Task 14.
//
// Two tests are defined here, both gated by the `integration` build tag so they
// stay out of the default `go test ./...` invocation (the rest of the suite
// must remain hermetic):
//
//	TestE2E_PocketIDClient verifies the wire format of every pocketid.Client
//	    method we depend on, against a real ghcr.io/pocket-id/pocket-id:v2
//	    container started for the test. The CRITICAL assertion is on
//	    CreateOneTimeAccessToken — Task 6 chose
//	    POST /api/users/:id/one-time-access-token with body {expiresAt} and
//	    response {token} based on PocketID conventions but did NOT verify it
//	    empirically. This test is that empirical check.
//
//	TestE2E_FullApplyRun spins up the same container, then drives the entire
//	    apply.Run orchestrator (owner provisioning + break-glass + TinyAuth
//	    static + recovery bundle write/encrypt). It is the closest we get in
//	    Phase 1 to a "real customer install" without involving terraform.
//
// Run with:
//
//	go test -tags=integration ./tests/integration/... -timeout 10m -v -run TestE2E
//
// Tests skip cleanly with t.Skip if `docker` is not on PATH; they fail with
// captured container logs if the container starts but does not become healthy.
package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/internal/apply"
	"github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const (
	// pocketIDImage is the PocketID v2 container we test against. Must match
	// what the StackKit's container module renders into compose/terraform.
	pocketIDImage = "ghcr.io/pocket-id/pocket-id:v2"

	// healthTimeout is how long we wait for /healthz to return 2xx after
	// `docker run`. Cold-start image pulls + sqlite init typically finish in
	// under 30s, but slow CI runners may need more headroom.
	healthTimeout = 90 * time.Second

	// pullTimeout is the upper bound on `docker pull`. Separate from
	// healthTimeout because pulls can be much slower on first run.
	pullTimeout = 5 * time.Minute

	// pocketIDInternalPort is the port PocketID v2 listens on inside the
	// container (verified via `docker inspect ghcr.io/pocket-id/pocket-id:v2`,
	// upstream defaults).
	pocketIDInternalPort = 1411
)

// startPocketID spins up a fresh PocketID container and returns its public
// base URL plus the STATIC_API_KEY that's been provisioned on it. The caller
// can use the returned values immediately — /healthz has been polled until
// success before this returns.
//
// The container is registered for cleanup via t.Cleanup. The function calls
// t.Skip if `docker` is not available on PATH (so the suite stays runnable on
// machines without docker), and t.Fatal on any other failure (including pull
// failures and health timeouts).
func startPocketID(t *testing.T) (baseURL, apiKey string) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available on PATH; skipping PocketID e2e test")
	}

	// Pull the image first so a slow pull doesn't eat into the health-check
	// budget. Use a generous timeout — first-time pulls of multi-arch images
	// can take a while on slow links.
	pullCtx, pullCancel := context.WithTimeout(t.Context(), pullTimeout)
	defer pullCancel()
	pullCmd := exec.CommandContext(pullCtx, "docker", "pull", pocketIDImage)
	if out, err := pullCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker pull %s failed: %v\n%s", pocketIDImage, err, out)
	}

	// Generate a STATIC_API_KEY for this container instance. Each test gets a
	// fresh key so cross-test bleed is impossible.
	var err error
	apiKey, err = crypto.RandomPassword(32)
	if err != nil {
		t.Fatalf("generate STATIC_API_KEY: %v", err)
	}

	// PocketID v2 also requires ENCRYPTION_KEY (>=16 bytes) for symmetric
	// encryption of stored secrets. The test generates a fresh one alongside
	// STATIC_API_KEY — neither value crosses tests.
	encryptionKey, err := crypto.RandomPassword(24)
	if err != nil {
		t.Fatalf("generate ENCRYPTION_KEY: %v", err)
	}

	port, err := freePort()
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	publicURL := fmt.Sprintf("http://localhost:%d", port)

	// Container name is unique per invocation so parallel test runs (and
	// abandoned containers from previous failures) don't collide.
	name := fmt.Sprintf("pocketid-e2e-%d", time.Now().UnixNano())

	runArgs := []string{
		"run", "--rm", "-d",
		"--name", name,
		"-p", fmt.Sprintf("%d:%d", port, pocketIDInternalPort),
		"-e", "PUBLIC_APP_URL=" + publicURL,
		"-e", "TRUST_PROXY=false",
		"-e", "STATIC_API_KEY=" + apiKey,
		"-e", "ENCRYPTION_KEY=" + encryptionKey,
		pocketIDImage,
	}
	out, err := exec.Command("docker", runArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run failed: %v\n%s", err, out)
	}

	// Cleanup runs even on test failure — `docker rm -f` is idempotent.
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	// Wait for /healthz. On failure capture container logs so the report
	// contains enough diagnostics to figure out why the container is sick.
	healthCtx, healthCancel := context.WithTimeout(t.Context(), healthTimeout)
	defer healthCancel()
	if err := waitHealthy(healthCtx, publicURL); err != nil {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		t.Fatalf("PocketID never became healthy at %s: %v\n--- container logs ---\n%s",
			publicURL, err, logs)
	}

	return publicURL, apiKey
}

// waitHealthy polls baseURL+/healthz every 2s until it returns 2xx or the
// context's deadline fires.
func waitHealthy(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline, _ := ctx.Deadline()
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("not healthy by deadline")
}

// freePort asks the OS for an available TCP port. The listener is closed
// immediately; the port may be reused by another process before docker binds
// it, but in practice the race is fine for test hosts.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// TestE2E_PocketIDClient exercises every pocketid.Client method we depend on
// against a real PocketID v2 container. Provides empirical confirmation of the
// wire formats Task 6 chose (most importantly CreateOneTimeAccessToken).
func TestE2E_PocketIDClient(t *testing.T) {
	baseURL, apiKey := startPocketID(t)
	client := pocketid.NewClient(baseURL, apiKey)
	ctx := t.Context()

	// 1. WaitHealthy — already exercised inside startPocketID, but re-running
	//    here confirms the client method (not just our test polling) talks to
	//    the same endpoint.
	if err := client.WaitHealthy(ctx, 10*time.Second); err != nil {
		t.Fatalf("client.WaitHealthy: %v", err)
	}

	// 2. BootstrapInitialAdmin — with STATIC_API_KEY pre-configured this
	//    should either succeed silently or return ErrAlreadyBootstrapped. Any
	//    other error means the static-admin path is broken.
	if _, err := client.BootstrapInitialAdmin(ctx, "boot@local", "boot", ""); err != nil &&
		!errors.Is(err, pocketid.ErrAlreadyBootstrapped) {
		t.Fatalf("BootstrapInitialAdmin: %v", err)
	}

	// 3. CreateUser — verifies the POST /api/users wire format.
	user, err := client.CreateUser(ctx, pocketid.CreateUserRequest{
		Username:  "testuser",
		Email:     "testuser@local.invalid",
		FirstName: "Test User",
		IsAdmin:   true,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID == "" {
		t.Errorf("CreateUser: returned user has empty ID")
	}
	if user.Username != "testuser" {
		t.Errorf("CreateUser: username mismatch: got %q want %q", user.Username, "testuser")
	}

	// 4. GetGroupIDByName — exercises GET /api/user-groups?search=. PocketID
	//    v2 ships without an "owners" group, so we just sanity-check that a
	//    bogus name returns "" (no error, no group). The createOwnersGroup
	//    helper in TestE2E_FullApplyRun verifies the create-side of the API.
	gID, err := client.GetGroupIDByName(ctx, "nonexistent-group-12345")
	if err != nil {
		t.Errorf("GetGroupIDByName(bogus): %v (expected nil err with empty result)", err)
	}
	if gID != "" {
		t.Errorf("GetGroupIDByName(bogus): expected empty ID, got %q", gID)
	}

	// 5. CreateOneTimeAccessToken — THE wire-format check. Task 6 chose:
	//      POST /api/users/:id/one-time-access-token
	//      body: {"expiresAt": "<RFC3339>"}
	//      response: {"token": "..."}
	//    based on PocketID source/convention but never verified empirically.
	//    This is the test that validates that choice.
	token, err := client.CreateOneTimeAccessToken(ctx, user.ID, 24*time.Hour)
	if err != nil {
		// Loud failure with the full error so the caller can see exactly what
		// the wire-format mismatch looks like (HTTP status + response body).
		t.Fatalf("CreateOneTimeAccessToken WIRE-FORMAT CHECK FAILED: %v\n"+
			"This is the empirical verification of the choice made in Task 6 "+
			"(POST /api/users/:id/one-time-access-token, body {expiresAt}, "+
			"response {token}). The HTTP details above tell us what to fix in "+
			"internal/pocketid/client.go.", err)
	}
	if token == "" {
		t.Error("CreateOneTimeAccessToken: empty token (response shape mismatch?)")
	}
	// PocketID v2.6.x emits short, single-use tokens (~6 chars) by default —
	// they're meant to be typed if needed. The wire-format test only needs to
	// confirm a non-empty string came back; sanity-check anything shorter than
	// 4 bytes since that would suggest a serialization bug.
	if len(token) < 4 {
		t.Errorf("CreateOneTimeAccessToken: token suspiciously short (%d bytes): %q",
			len(token), token)
	}
	t.Logf("OTAT WIRE FORMAT VERIFIED. Token length: %d bytes.", len(token))
}

// TestE2E_FullApplyRun drives the full apply.Run orchestrator against a real
// PocketID container. apply.Run is responsible for creating the "owners"
// group during step 2.5 of the bootstrap sequence (PocketID v2 ships without
// it); this test asserts that path works end-to-end against the real API
// rather than a mock — and is the empirical proof that the wire format
// (POST /api/user-groups with {name, friendlyName}) matches the upstream
// container.
func TestE2E_FullApplyRun(t *testing.T) {
	baseURL, apiKey := startPocketID(t)

	// A real argon2id-PHC hash + matching plaintext, so apply.Run's third-
	// factor verification passes without prompting the operator.
	passphrase := "test-passphrase-for-integration-12345"
	hash, err := crypto.HashPassphrase(passphrase)
	if err != nil {
		t.Fatalf("HashPassphrase: %v", err)
	}

	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "recovery")
	tinyEnvPath := filepath.Join(tmpDir, "tinyauth-users.env")

	result, err := apply.Run(t.Context(), apply.OwnerBootstrapInput{
		NodeName:             "e2e-test-node",
		Hostname:             "e2e-test-node.local",
		PocketIDURL:          baseURL,
		PocketIDStaticAPIKey: apiKey,
		Owner: identity.OwnerSpec{
			Source:      "local",
			Email:       "owner@e2e.local",
			Username:    "e2eowner",
			DisplayName: "E2E Owner",
		},
		RecoveryPassphraseHash:  hash,
		RecoveryPassphrasePlain: passphrase,
		BundleDir:               bundleDir,
		TinyAuthEnvPath:         tinyEnvPath,
	})
	if err != nil {
		t.Fatalf("apply.Run: %v", err)
	}

	// --- Result-shape sanity ---

	if result.OwnerUserID == "" {
		t.Error("OwnerUserID is empty")
	}
	if !strings.Contains(result.OwnerSetupURL, baseURL+"/setup-account?token=") {
		t.Errorf("OwnerSetupURL malformed: %q", result.OwnerSetupURL)
	}
	if result.BreakGlass == nil {
		t.Fatal("BreakGlass is nil")
	}
	if !strings.HasPrefix(result.BreakGlass.Username, "bg-e2e-test-node") {
		t.Errorf("BreakGlass.Username = %q (expected prefix bg-e2e-test-node)", result.BreakGlass.Username)
	}
	if result.BreakGlass.SetupToken == "" {
		t.Error("BreakGlass.SetupToken is empty")
	}
	if !strings.Contains(result.BreakGlass.SetupURL, "/setup-account?token=") {
		t.Errorf("BreakGlass.SetupURL malformed: %q", result.BreakGlass.SetupURL)
	}
	if result.TinyAuthStatic == nil {
		t.Fatal("TinyAuthStatic is nil")
	}
	if result.BundlePaths == nil {
		t.Fatal("BundlePaths is nil")
	}

	// --- Bundle file verification ---

	if _, err := os.Stat(result.BundlePaths.EncryptedPath); err != nil {
		t.Errorf("encrypted bundle missing: %v", err)
	}
	if _, err := os.Stat(result.BundlePaths.PlaintextPath); err != nil {
		t.Errorf("plaintext bundle missing: %v", err)
	}

	encBytes, err := os.ReadFile(result.BundlePaths.EncryptedPath)
	if err != nil {
		t.Fatalf("read encrypted bundle: %v", err)
	}

	plain, err := crypto.DecryptWithPassphrase(encBytes, passphrase)
	if err != nil {
		t.Fatalf("decrypt bundle with correct passphrase: %v", err)
	}
	if !strings.Contains(string(plain), "bg-e2e-test-node@local") {
		t.Errorf("decrypted bundle missing break-glass username; payload:\n%s", string(plain))
	}
	if !strings.Contains(string(plain), result.BreakGlass.SetupToken) {
		t.Error("decrypted bundle missing break-glass setup token")
	}

	// Wrong passphrase MUST fail — proves the bundle is genuinely encrypted.
	if _, err := crypto.DecryptWithPassphrase(encBytes, "wrong-passphrase-xyz"); err == nil {
		t.Error("decrypt with wrong passphrase succeeded (bundle not actually encrypted?)")
	}

	// --- TinyAuth env file verification ---

	envBytes, err := os.ReadFile(tinyEnvPath)
	if err != nil {
		t.Fatalf("read tinyauth env file: %v", err)
	}
	envStr := string(envBytes)
	if !strings.HasPrefix(envStr, "USERS=bg-e2e-test-node-static:") {
		t.Errorf("tinyauth env malformed: %q", envStr)
	}
	if !strings.Contains(envStr, "$2a$") && !strings.Contains(envStr, "$2b$") {
		// bcrypt may use either $2a$ (legacy) or $2b$ depending on version;
		// golang.org/x/crypto/bcrypt produces $2a$.
		t.Errorf("tinyauth env missing bcrypt hash prefix; got %q", envStr)
	}

	t.Logf("Full apply.Run end-to-end SUCCESS.")
	t.Logf("  Owner setup URL:      %s", result.OwnerSetupURL)
	t.Logf("  Break-glass setup URL: %s", result.BreakGlass.SetupURL)
	t.Logf("  Encrypted bundle:     %s", result.BundlePaths.EncryptedPath)
}
