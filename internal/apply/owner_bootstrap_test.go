package apply

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/pocketid"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// fakePassphrasePrompter returns a fixed plaintext if it matches expectedHash;
// otherwise errors after the configured attempts. Records call count so tests
// can assert the prompter was actually invoked.
type fakePassphrasePrompter struct {
	plaintext string
	calls     int32
}

func (p *fakePassphrasePrompter) PromptAndVerify(expectedHash string, _ int) (string, error) {
	atomic.AddInt32(&p.calls, 1)
	if crypto.VerifyPassphrase(p.plaintext, expectedHash) {
		return p.plaintext, nil
	}
	return "", errors.New("fake prompter: plaintext mismatch")
}

// mockPocketIDState is the in-memory state the mock server tracks across
// requests. Tests inspect it after Run completes to verify the orchestrator
// hit the expected endpoints and didn't smuggle anything weird through.
type mockPocketIDState struct {
	healthzCalls       int32
	bootstrapVerifyHit int32 // GET /api/users (the BootstrapInitialAdmin probe)
	createdUsers       []pocketid.CreateUserRequest
	addedToGroups      map[string][]string // groupID -> userIDs
	tokensIssued       map[string]string   // userID -> token

	// ownersGroupExists toggles the GET /api/user-groups response between
	// "owners group is preset" and "owners group is missing — must be
	// created". Defaults true (existing behavior). Tests that exercise the
	// cold-start branch flip it to false.
	ownersGroupExists      bool
	adminsGroupExists      bool
	createUserGroupCalls   int32
	createdUserGroupBodies []pocketid.CreateUserGroupRequest
}

// newMockPocketID returns an httptest.Server that responds to all the
// endpoints the apply orchestrator touches. The server uses a shared state
// map so tests can read back what was sent. Closes via t.Cleanup.
func newMockPocketID(t *testing.T) (*httptest.Server, *mockPocketIDState) {
	t.Helper()
	state := &mockPocketIDState{
		addedToGroups:     map[string][]string{},
		tokensIssued:      map[string]string{},
		ownersGroupExists: true,
		adminsGroupExists: true,
	}

	mux := http.NewServeMux()

	// /healthz: 204 No Content (PocketID v2 contract).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&state.healthzCalls, 1)
		w.WriteHeader(http.StatusNoContent)
	})

	// /api/users: POST creates a user; GET (with pagination[limit]=1) is
	// the BootstrapInitialAdmin verification probe.
	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "static-key-test" {
			t.Errorf("X-API-Key: got %q, want static-key-test", got)
		}
		switch r.Method {
		case http.MethodGet:
			atomic.AddInt32(&state.bootstrapVerifyHit, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"data":[],"pagination":{"totalItems":0}}`)
		case http.MethodPost:
			var req pocketid.CreateUserRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode CreateUserRequest: %v", err)
			}
			state.createdUsers = append(state.createdUsers, req)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{
				"id":"uid-`+req.Username+`",
				"username":"`+req.Username+`",
				"email":"`+req.Email+`",
				"firstName":"`+req.FirstName+`",
				"displayName":"",
				"isAdmin":true
			}`)
		default:
			t.Errorf("/api/users: unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// /api/user-groups: GET (search) returns matching groups when they
	// exist; POST creates them (cold-start path).
	mux.HandleFunc("/api/user-groups", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			search := r.URL.Query().Get("search")
			w.WriteHeader(http.StatusOK)
			switch {
			case search == "owners" && state.ownersGroupExists:
				_, _ = io.WriteString(w, `{"data":[{"id":"grp-owners-uuid","name":"owners"}]}`)
			case search == "admins" && state.adminsGroupExists:
				_, _ = io.WriteString(w, `{"data":[{"id":"grp-admins-uuid","name":"admins"}]}`)
			default:
				_, _ = io.WriteString(w, `{"data":[]}`)
			}
		case http.MethodPost:
			atomic.AddInt32(&state.createUserGroupCalls, 1)
			var body pocketid.CreateUserGroupRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode CreateUserGroupRequest: %v", err)
			}
			state.createdUserGroupBodies = append(state.createdUserGroupBodies, body)
			// Materialize the group so a subsequent GET (e.g. from
			// OwnerProvisioner.Provision) returns it.
			switch body.Name {
			case "owners":
				state.ownersGroupExists = true
			case "admins":
				state.adminsGroupExists = true
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"grp-`+body.Name+`-uuid","name":"`+body.Name+`","friendlyName":"`+body.FriendlyName+`"}`)
		default:
			t.Errorf("/api/user-groups: unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// /api/users/{id}/user-groups (PUT) and
	// /api/users/{id}/one-time-access-token (POST).
	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
		// Strip the prefix and inspect the suffix to dispatch.
		rest := strings.TrimPrefix(r.URL.Path, "/api/users/")
		// Format: <userID>/<endpoint>
		idx := strings.Index(rest, "/")
		if idx < 0 {
			t.Errorf("unexpected /api/users/ path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		userID, endpoint := rest[:idx], rest[idx+1:]

		switch endpoint {
		case "user-groups":
			if r.Method != http.MethodPut {
				t.Errorf("user-groups: unexpected method %s", r.Method)
			}
			var body struct {
				UserGroupIDs []string `json:"userGroupIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode AddUserToGroup: %v", err)
			}
			for _, gid := range body.UserGroupIDs {
				state.addedToGroups[gid] = append(state.addedToGroups[gid], userID)
			}
			w.WriteHeader(http.StatusOK)
		case "one-time-access-token":
			if r.Method != http.MethodPost {
				t.Errorf("one-time-access-token: unexpected method %s", r.Method)
			}
			tok := "tok-" + userID
			state.tokensIssued[userID] = tok
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"token":"`+tok+`"}`)
		default:
			t.Errorf("unknown /api/users/{id}/%s", endpoint)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

// makeHash hashes a plaintext passphrase with the production argon2id
// parameters so tests exercise the same code path as the install flow.
func makeHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := crypto.HashPassphrase(plain)
	if err != nil {
		t.Fatalf("HashPassphrase: %v", err)
	}
	return h
}

// TestRun_HappyPath drives the orchestrator end-to-end against the mock
// PocketID. It verifies every step of the sequence:
//   - PocketID health was probed
//   - BootstrapInitialAdmin verified the static API key
//   - Owner + break-glass users were created with the right shapes
//   - Both users were added to the owners and admins groups
//   - One-time-access tokens were issued for both
//   - TinyAuth env file landed at the expected path with bcrypt content
//   - Bundle .age and .txt files exist and round-trip via the passphrase
//   - The result struct is fully populated
func TestRun_HappyPath(t *testing.T) {
	srv, state := newMockPocketID(t)

	tmpRoot := t.TempDir()
	bundleDir := filepath.Join(tmpRoot, "recovery")
	tinyAuthEnv := filepath.Join(tmpRoot, "tinyauth", "users.env")

	const passphrase = "correct horse battery staple"
	hash := makeHash(t, passphrase)

	in := OwnerBootstrapInput{
		NodeName:             "node1",
		Hostname:             "node1.test.local",
		PocketIDURL:          srv.URL,
		PocketIDStaticAPIKey: "static-key-test",
		Owner: identity.OwnerSpec{
			Source:      "local",
			Email:       "owner@test.local",
			Username:    "owner",
			DisplayName: "Test Owner",
		},
		RecoveryPassphraseHash: hash,
		BundleDir:              bundleDir,
		TinyAuthEnvPath:        tinyAuthEnv,
		TerminalPrompter:       &fakePassphrasePrompter{plaintext: passphrase},
		Now: func() time.Time {
			return time.Date(2026, 4, 28, 14, 32, 11, 0, time.UTC)
		},
	}

	result, err := Run(t.Context(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// PocketID was probed at least once.
	if atomic.LoadInt32(&state.healthzCalls) == 0 {
		t.Error("WaitHealthy didn't probe /healthz")
	}
	// BootstrapInitialAdmin verified the token.
	if atomic.LoadInt32(&state.bootstrapVerifyHit) == 0 {
		t.Error("BootstrapInitialAdmin didn't probe /api/users")
	}

	// Two users created: owner + break-glass.
	if len(state.createdUsers) != 2 {
		t.Fatalf("createdUsers: got %d, want 2 (owner + break-glass): %+v",
			len(state.createdUsers), state.createdUsers)
	}
	// Owner first.
	if state.createdUsers[0].Username != "owner" {
		t.Errorf("first created user should be owner, got %q", state.createdUsers[0].Username)
	}
	if !state.createdUsers[0].IsAdmin {
		t.Error("owner must be admin")
	}
	// Break-glass second.
	if state.createdUsers[1].Username != "bg-node1@local" {
		t.Errorf("break-glass username: got %q, want bg-node1@local", state.createdUsers[1].Username)
	}
	if !state.createdUsers[1].IsAdmin {
		t.Error("break-glass must be admin")
	}

	// Both added to the owners group.
	owners := state.addedToGroups["grp-owners-uuid"]
	if len(owners) != 2 {
		t.Errorf("expected 2 owners-group members, got %v", owners)
	}
	admins := state.addedToGroups["grp-admins-uuid"]
	if len(admins) != 2 {
		t.Errorf("expected 2 admins-group members, got %v", admins)
	}

	// Both got setup tokens.
	if state.tokensIssued["uid-owner"] == "" {
		t.Error("owner missing setup token")
	}
	if state.tokensIssued["uid-bg-node1@local"] == "" {
		t.Error("break-glass missing setup token")
	}

	// Result is well-formed.
	if result.OwnerUserID != "uid-owner" {
		t.Errorf("OwnerUserID: got %q, want uid-owner", result.OwnerUserID)
	}
	if !strings.HasPrefix(result.OwnerSetupURL, srv.URL+"/setup-account?token=") {
		t.Errorf("OwnerSetupURL malformed: %q", result.OwnerSetupURL)
	}
	if !strings.HasSuffix(result.OwnerSetupURL, "tok-uid-owner") {
		t.Errorf("OwnerSetupURL doesn't include owner's token: %q", result.OwnerSetupURL)
	}
	if result.BreakGlass == nil || result.BreakGlass.Username != "bg-node1@local" {
		t.Errorf("BreakGlass: got %+v", result.BreakGlass)
	}
	if result.TinyAuthStatic == nil || !strings.HasPrefix(result.TinyAuthStatic.Username, "bg-node1-static") {
		t.Errorf("TinyAuthStatic: got %+v", result.TinyAuthStatic)
	}

	// TinyAuth env file landed.
	envContent, err := os.ReadFile(tinyAuthEnv)
	if err != nil {
		t.Fatalf("read tinyauth env: %v", err)
	}
	envStr := string(envContent)
	if !strings.HasPrefix(envStr, "USERS=bg-node1-static:$2") {
		t.Errorf("tinyauth env content unexpected: %q", envStr)
	}
	if !strings.HasSuffix(envStr, "\n") {
		t.Error("tinyauth env should end with newline")
	}
	// Bcrypt hash in the file must verify against the plaintext in the
	// returned credential — that's the contract that lets recovery work.
	parts := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(envStr, "USERS=")), ":", 2)
	if len(parts) != 2 {
		t.Fatalf("USERS line malformed: %q", envStr)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(parts[1]), []byte(result.TinyAuthStatic.PasswordPlain)); err != nil {
		t.Errorf("env-file bcrypt does not verify against returned plaintext: %v", err)
	}

	// File mode 0640 on Unix; Windows fs maps mode bits loosely.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(tinyAuthEnv)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Errorf("tinyauth env mode = %o, want 0640", info.Mode().Perm())
		}
	}

	// Bundle files exist.
	if _, err := os.Stat(result.BundlePaths.EncryptedPath); err != nil {
		t.Errorf("encrypted bundle missing: %v", err)
	}
	if _, err := os.Stat(result.BundlePaths.PlaintextPath); err != nil {
		t.Errorf("plaintext bundle missing: %v", err)
	}
	// Filenames encode the node.
	if filepath.Base(result.BundlePaths.EncryptedPath) != "break-glass-node1.age" {
		t.Errorf("encrypted bundle path: %s", result.BundlePaths.EncryptedPath)
	}

	// Bundle decrypts with the same passphrase and contains the break-glass
	// data — proves the orchestrator's third-factor verification produced
	// the correct symmetric key.
	enc, err := os.ReadFile(result.BundlePaths.EncryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptWithPassphrase(enc, passphrase)
	if err != nil {
		t.Fatalf("decrypt bundle: %v", err)
	}
	var payload identity.BundlePayload
	if err := yaml.Unmarshal(plain, &payload); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if payload.BreakGlass.PocketIDAdmin.Username != "bg-node1@local" {
		t.Errorf("bundle pocketidAdmin.username: got %q", payload.BreakGlass.PocketIDAdmin.Username)
	}
	if payload.BreakGlass.TinyAuthStatic.PasswordPlain != result.TinyAuthStatic.PasswordPlain {
		t.Error("bundle tinyauthStatic.passwordPlain doesn't match returned credential")
	}
	if payload.Node.ClusterRole != "main" {
		t.Errorf("bundle node.clusterRole: got %q, want main (default)", payload.Node.ClusterRole)
	}
	if payload.Node.Hostname != "node1.test.local" {
		t.Errorf("bundle node.hostname: got %q", payload.Node.Hostname)
	}
}

// TestRun_PassphrasePlainMatchesHash verifies that when the caller passes
// a matching plaintext directly, the orchestrator skips the prompt entirely.
func TestRun_PassphrasePlainMatchesHash(t *testing.T) {
	srv, _ := newMockPocketID(t)

	const passphrase = "another solid passphrase"
	hash := makeHash(t, passphrase)

	prompter := &fakePassphrasePrompter{plaintext: "would-fail-anyway"}
	in := OwnerBootstrapInput{
		NodeName:                "node1",
		PocketIDURL:             srv.URL,
		PocketIDStaticAPIKey:    "static-key-test",
		Owner:                   identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
		RecoveryPassphraseHash:  hash,
		RecoveryPassphrasePlain: passphrase,
		BundleDir:               t.TempDir(),
		TinyAuthEnvPath:         filepath.Join(t.TempDir(), "users.env"),
		TerminalPrompter:        prompter,
	}
	if _, err := Run(t.Context(), in); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if atomic.LoadInt32(&prompter.calls) != 0 {
		t.Errorf("prompter should not have been called when plaintext was supplied")
	}
}

// TestRun_PassphrasePlainMismatch verifies that a caller-supplied plaintext
// that doesn't match the hash is rejected before any bundle is written —
// otherwise we'd produce an artifact whose passphrase nobody knows.
func TestRun_PassphrasePlainMismatch(t *testing.T) {
	srv, _ := newMockPocketID(t)

	hash := makeHash(t, "the real one")
	bundleDir := t.TempDir()
	in := OwnerBootstrapInput{
		NodeName:                "node1",
		PocketIDURL:             srv.URL,
		PocketIDStaticAPIKey:    "static-key-test",
		Owner:                   identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
		RecoveryPassphraseHash:  hash,
		RecoveryPassphrasePlain: "the wrong one",
		BundleDir:               bundleDir,
		TinyAuthEnvPath:         filepath.Join(t.TempDir(), "users.env"),
	}
	_, err := Run(t.Context(), in)
	if err == nil {
		t.Fatal("expected error for plaintext/hash mismatch")
	}
	if !strings.Contains(err.Error(), "passphrase confirm") {
		t.Errorf("error should mention passphrase confirm: %v", err)
	}
	// No bundle on disk.
	entries, _ := os.ReadDir(bundleDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".age") || strings.HasSuffix(e.Name(), ".txt") {
			t.Errorf("bundle artifact left behind despite passphrase failure: %s", e.Name())
		}
	}
}

// TestRun_PassphraseRejectedThreeTimes verifies the prompter loop drops out
// after three mismatching attempts and Run errors clearly.
func TestRun_PassphraseRejectedThreeTimes(t *testing.T) {
	srv, _ := newMockPocketID(t)

	hash := makeHash(t, "the real one")
	prompter := &fakePassphrasePrompter{plaintext: "wrong forever"}
	bundleDir := t.TempDir()

	in := OwnerBootstrapInput{
		NodeName:               "node1",
		PocketIDURL:            srv.URL,
		PocketIDStaticAPIKey:   "static-key-test",
		Owner:                  identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
		RecoveryPassphraseHash: hash,
		BundleDir:              bundleDir,
		TinyAuthEnvPath:        filepath.Join(t.TempDir(), "users.env"),
		TerminalPrompter:       prompter,
	}
	_, err := Run(t.Context(), in)
	if err == nil {
		t.Fatal("expected error after passphrase rejection")
	}
	if !strings.Contains(err.Error(), "passphrase confirm") {
		t.Errorf("error should mention passphrase confirm: %v", err)
	}
}

// TestRun_PocketIDUnreachable points at a closed server so WaitHealthy
// times out. The error must be wrapped with the "pocketid not healthy"
// label so log scrapers can spot the failure mode.
func TestRun_PocketIDUnreachable(t *testing.T) {
	// Spin up and immediately close to get a guaranteed-dead URL with a
	// realistic shape (not just "http://localhost:1").
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	in := OwnerBootstrapInput{
		NodeName:               "node1",
		PocketIDURL:            url,
		PocketIDStaticAPIKey:   "static-key-test",
		Owner:                  identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
		RecoveryPassphraseHash: makeHash(t, "doesnt-matter"),
		BundleDir:              t.TempDir(),
		TinyAuthEnvPath:        filepath.Join(t.TempDir(), "users.env"),
		TerminalPrompter:       &fakePassphrasePrompter{},
	}

	// Use a short context so WaitHealthy's 2-minute internal timeout
	// doesn't dominate test runtime.
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, in)
	if err == nil {
		t.Fatal("expected error when pocketid is unreachable")
	}
	if !strings.Contains(err.Error(), "pocketid not healthy") {
		t.Errorf("error should mention 'pocketid not healthy': %v", err)
	}
}

// TestRun_ValidateInput exercises the required-field guards. Each must fail
// before any HTTP traffic occurs.
func TestRun_ValidateInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*OwnerBootstrapInput)
		want string
	}{
		{
			"empty NodeName",
			func(in *OwnerBootstrapInput) { in.NodeName = "" },
			"NodeName required",
		},
		{
			"empty PocketIDURL",
			func(in *OwnerBootstrapInput) { in.PocketIDURL = "" },
			"PocketIDURL required",
		},
		{
			"empty PocketIDStaticAPIKey",
			func(in *OwnerBootstrapInput) { in.PocketIDStaticAPIKey = "" },
			"PocketIDStaticAPIKey required",
		},
		{
			"empty RecoveryPassphraseHash",
			func(in *OwnerBootstrapInput) { in.RecoveryPassphraseHash = "" },
			"RecoveryPassphraseHash required",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := OwnerBootstrapInput{
				NodeName:               "node1",
				PocketIDURL:            "https://id.test.local",
				PocketIDStaticAPIKey:   "static-key-test",
				Owner:                  identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
				RecoveryPassphraseHash: makeHash(t, "p"),
			}
			c.mut(&in)
			_, err := Run(t.Context(), in)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error: got %v, want substring %q", err, c.want)
			}
		})
	}
}

// TestRun_ValidateAppliesDefaults verifies that BundleDir / TinyAuthEnvPath
// defaults are filled in (not just used internally — they must mutate the
// input so the caller can later inspect the resolved paths if desired).
func TestRun_ValidateAppliesDefaults(t *testing.T) {
	in := OwnerBootstrapInput{
		NodeName:               "n",
		PocketIDURL:            "https://x",
		PocketIDStaticAPIKey:   "k",
		RecoveryPassphraseHash: makeHash(t, "p"),
	}
	if err := validateInput(&in); err != nil {
		t.Fatal(err)
	}
	if in.BundleDir != defaultBundleDir {
		t.Errorf("BundleDir default: got %q, want %q", in.BundleDir, defaultBundleDir)
	}
	if in.TinyAuthEnvPath != defaultTinyAuthEnvPath {
		t.Errorf("TinyAuthEnvPath default: got %q, want %q", in.TinyAuthEnvPath, defaultTinyAuthEnvPath)
	}
}

// TestRun_CreatesOwnersGroupWhenMissing covers the cold-start path: a fresh
// PocketID v2 instance ships without an "owners" group, so apply.Run must
// create it before OwnerProvisioner.Provision tries to add the daily-admin to
// the group. Without this step Phase-1 deploys fail at step 3 with
// "no group by that name exists" — the regression this fix targets.
func TestRun_CreatesOwnersGroupWhenMissing(t *testing.T) {
	srv, state := newMockPocketID(t)
	state.ownersGroupExists = false // <-- simulate a fresh PocketID

	const passphrase = "horse battery staple correct"
	hash := makeHash(t, passphrase)
	bundleDir := t.TempDir()

	in := OwnerBootstrapInput{
		NodeName:                "node1",
		PocketIDURL:             srv.URL,
		PocketIDStaticAPIKey:    "static-key-test",
		Owner:                   identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
		RecoveryPassphraseHash:  hash,
		RecoveryPassphrasePlain: passphrase,
		BundleDir:               bundleDir,
		TinyAuthEnvPath:         filepath.Join(t.TempDir(), "users.env"),
	}
	if _, err := Run(t.Context(), in); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// CreateUserGroup must have been called exactly once.
	if got := atomic.LoadInt32(&state.createUserGroupCalls); got != 1 {
		t.Errorf("CreateUserGroup calls: got %d, want 1", got)
	}
	if len(state.createdUserGroupBodies) != 1 {
		t.Fatalf("createdUserGroupBodies len: got %d, want 1", len(state.createdUserGroupBodies))
	}
	body := state.createdUserGroupBodies[0]
	if body.Name != "owners" {
		t.Errorf("CreateUserGroup name: got %q, want owners", body.Name)
	}
	if body.FriendlyName != "Owners" {
		t.Errorf("CreateUserGroup friendlyName: got %q, want Owners", body.FriendlyName)
	}
}

// TestRun_DoesNotCreateOwnersGroupWhenPresent verifies the happy-path
// idempotency: when the owners group is already there, Run does NOT issue a
// redundant CreateUserGroup call. Re-runs of the bootstrap pipeline should be
// cheap and side-effect-free at the API layer.
func TestRun_DoesNotCreateOwnersGroupWhenPresent(t *testing.T) {
	srv, state := newMockPocketID(t)
	// state.ownersGroupExists defaults to true.

	const passphrase = "another-strong-passphrase"
	hash := makeHash(t, passphrase)

	in := OwnerBootstrapInput{
		NodeName:                "node1",
		PocketIDURL:             srv.URL,
		PocketIDStaticAPIKey:    "static-key-test",
		Owner:                   identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
		RecoveryPassphraseHash:  hash,
		RecoveryPassphrasePlain: passphrase,
		BundleDir:               t.TempDir(),
		TinyAuthEnvPath:         filepath.Join(t.TempDir(), "users.env"),
	}
	if _, err := Run(t.Context(), in); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&state.createUserGroupCalls); got != 0 {
		t.Errorf("CreateUserGroup should not have been called when group exists; got %d calls", got)
	}
}

// TestRun_HostnameDefaultsToNodeName confirms the bundle records something
// useful even when the operator didn't pass a hostname explicitly.
func TestRun_HostnameDefaultsToNodeName(t *testing.T) {
	srv, _ := newMockPocketID(t)
	const passphrase = "horse battery staple correct"
	hash := makeHash(t, passphrase)
	bundleDir := t.TempDir()

	in := OwnerBootstrapInput{
		NodeName:                "lone-node",
		Hostname:                "", // <- intentionally blank
		PocketIDURL:             srv.URL,
		PocketIDStaticAPIKey:    "static-key-test",
		Owner:                   identity.OwnerSpec{Source: "local", Email: "x@y.com", Username: "owner"},
		RecoveryPassphraseHash:  hash,
		RecoveryPassphrasePlain: passphrase,
		BundleDir:               bundleDir,
		TinyAuthEnvPath:         filepath.Join(t.TempDir(), "users.env"),
	}
	result, err := Run(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := os.ReadFile(result.BundlePaths.EncryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptWithPassphrase(enc, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	var payload identity.BundlePayload
	if err := yaml.Unmarshal(plain, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Node.Hostname != "lone-node" {
		t.Errorf("Hostname default: got %q, want lone-node (==NodeName)", payload.Node.Hostname)
	}
}
