// Package apply orchestrates post-deployment bootstrap of identity resources.
//
// The single entry point Run wires together the building blocks created by
// Tasks 5–10 (PocketID client, owner provisioner, break-glass generator,
// TinyAuth static-cred generator, recovery-bundle builder) into the
// post-terraform sequence the CLI calls once the homelab containers are
// healthy.
//
// Idempotency: PocketID's STATIC_API_KEY-based bootstrap is itself a no-op
// when the static admin user already exists, so re-running the orchestrator
// against an already-provisioned PocketID surfaces ErrAlreadyBootstrapped
// (treated as success). Owner / break-glass / TinyAuth-static creation are
// not idempotent at the API level — re-running on a node that already has an
// owner will error in CreateUser. Phase 1 keeps that strict so a partial
// bootstrap is loud rather than silently double-provisioning. Per-step error
// wrapping makes it possible to identify exactly where a re-run would need
// to resume.
package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

// healthWaitTimeout is the maximum time Run blocks waiting for PocketID to
// become healthy after terraform-apply. Two minutes covers cold-start image
// pulls on slow links without making a stuck install hang forever.
const healthWaitTimeout = 2 * time.Minute

// passphraseAttempts is how many times Run prompts for the recovery
// passphrase before giving up. Three attempts matches the wizard's
// confirm-loop ergonomics and limits brute-force surface if the prompt is
// somehow exposed.
const passphraseAttempts = 3

// defaultBundleDir is the on-host directory recovery bundles are written to
// when OwnerBootstrapInput.BundleDir is empty. Mirrors
// identity.defaultBundleDir but is duplicated here so the orchestrator can
// log the resolved path without reaching into the identity package.
const defaultBundleDir = "/var/lib/stackkit/recovery"

// defaultTinyAuthEnvPath is where TinyAuth's static users env line lives on
// the homelab node. Templated by the TinyAuth container module to be loaded
// via env_file. Only used when OwnerBootstrapInput.TinyAuthEnvPath is empty.
const defaultTinyAuthEnvPath = "/etc/tinyauth/users.env"

// PassphrasePrompter gates the third-factor passphrase confirmation.
// Production uses ttyPassphrasePrompter (reads /dev/tty with echo off);
// tests inject a fake.
type PassphrasePrompter interface {
	// PromptAndVerify reads the recovery passphrase from the operator,
	// compares it against expectedHash via crypto.VerifyPassphrase, and
	// returns the raw plaintext on success. It must give the operator at
	// most attempts tries before erroring out — empty plaintext on success
	// is not allowed.
	PromptAndVerify(expectedHash string, attempts int) (plaintext string, err error)
}

// pocketIDClientForBootstrap is the subset of *pocketid.Client used by the
// orchestrator for non-provisioner calls. Defined as an interface so tests
// can stub WaitHealthy / BootstrapInitialAdmin / CreateUserGroup without going
// through HTTP. The provisioner / generator paths use identity.PocketIDClient
// directly.
type pocketIDClientForBootstrap interface {
	identity.PocketIDClient
	WaitHealthy(ctx context.Context, timeout time.Duration) error
	BootstrapInitialAdmin(ctx context.Context, email, username, password string) (string, error)
	CreateUserGroup(ctx context.Context, req pocketid.CreateUserGroupRequest) (*pocketid.UserGroup, error)
}

// Compile-time check that the real pocketid.Client satisfies our interface.
var _ pocketIDClientForBootstrap = (*pocketid.Client)(nil)

// OwnerBootstrapInput is everything Run needs to provision the owner and
// break-glass artifacts. Validation rules: NodeName, PocketIDURL,
// PocketIDStaticAPIKey, and RecoveryPassphraseHash are required. Owner is
// validated by the underlying provisioner.
type OwnerBootstrapInput struct {
	// NodeName is the homelab firstnode identifier (used for synthetic
	// break-glass usernames and bundle filenames). Required.
	NodeName string

	// Hostname is the node's network hostname. Documentation-only field
	// embedded into the bundle so the recovery operator can match the
	// artifact to physical hardware. Defaults to NodeName when empty.
	Hostname string

	// PocketIDURL is the public origin of the PocketID instance, e.g.
	// "https://id.example.com". No trailing slash. Required.
	PocketIDURL string

	// PocketIDStaticAPIKey is the value rendered into the PocketID
	// container as STATIC_API_KEY (Task 12 will provision this via tfvars;
	// for now Run reads it from the caller). Required.
	PocketIDStaticAPIKey string

	// Owner is the daily-admin owner spec. Required.
	Owner identity.OwnerSpec

	// RecoveryPassphraseHash is the argon2id-PHC hash the operator chose
	// during init. Required. Run uses it to verify the operator types the
	// matching plaintext at bundle-encryption time (third factor).
	RecoveryPassphraseHash string

	// RecoveryPassphrasePlain is the plaintext passphrase, optional. When
	// set, Run skips the terminal prompt and verifies the value against
	// the hash directly. Empty value triggers the prompt.
	RecoveryPassphrasePlain string

	// BundleDir is where the .age and .txt bundle files get written.
	// Defaults to defaultBundleDir when empty.
	BundleDir string

	// TinyAuthEnvPath is the file the TinyAuth users env line is written
	// to. Defaults to defaultTinyAuthEnvPath when empty.
	TinyAuthEnvPath string

	// ClusterRole is mirrored into the bundle so the recovery operator
	// knows which role this node played. Defaults to "main" when empty.
	ClusterRole string

	// PocketIDClient is the optional injection point for tests. When nil,
	// Run constructs a real *pocketid.Client from PocketIDURL +
	// PocketIDStaticAPIKey.
	PocketIDClient pocketIDClientForBootstrap

	// TerminalPrompter overrides the third-factor passphrase prompt.
	// Production sets this to nil (uses ttyPassphrasePrompter); tests inject
	// a fake.
	TerminalPrompter PassphrasePrompter

	// Now is injectable for deterministic tests. Defaults to time.Now in
	// the bundle builder.
	Now func() time.Time
}

// OwnerBootstrapResult is what Run returns on success — the persisted IDs
// the controller wants to push back to TechStack later (Task 13+) plus the
// on-disk paths the CLI prints to the operator.
type OwnerBootstrapResult struct {
	// OwnerUserID is the PocketID-assigned UUID for the owner record.
	OwnerUserID string

	// OwnerSetupURL is the one-time-access link the owner clicks to enroll
	// a WebAuthn credential. Single-use; expires per identity.setupTokenTTL.
	OwnerSetupURL string

	// BreakGlass is the materialized Layer-1 recovery credential.
	BreakGlass *identity.BreakGlassCredential

	// TinyAuthStatic is the materialized Layer-2 recovery credential.
	TinyAuthStatic *identity.TinyAuthStaticCredential

	// BundlePaths are the on-disk locations of the encrypted (.age) and
	// plaintext (.txt) recovery files.
	BundlePaths *identity.BundlePaths
}

// ownersGroupName is the PocketID user-group all owner records (and the
// per-node break-glass record) are added to. It must exist before
// OwnerProvisioner.Provision runs; PocketID v2 ships with no preset groups,
// so the orchestrator creates it idempotently in step 2.5.
const ownersGroupName = "owners"

// ownersGroupFriendlyName is the human label for the owners group. Shown in
// PocketID's UI when an admin lists groups; not load-bearing for the
// machine-name lookup.
const ownersGroupFriendlyName = "Owners"

// adminsGroupName is the group SSO-facing tools can use to authorize
// administrative access from PocketID without maintaining their own allowlist.
const adminsGroupName = "admins"

const adminsGroupFriendlyName = "Admins"

// Run executes the full owner-and-break-glass bootstrap sequence:
//
//  1. Wait for PocketID health (HTTP /healthz returns 2xx).
//  2. BootstrapInitialAdmin — verifies STATIC_API_KEY is accepted. Treated
//     as success when the instance is already bootstrapped.
//  3. Ensure the "owners" and "admins" user-groups exist.
//  4. Provision the daily-admin owner (CreateUser + group + setup URL).
//  5. Generate the per-node break-glass admin (CreateUser + group + token).
//  6. Generate the per-node TinyAuth static credential (random pwd + bcrypt).
//  7. Write TINYAUTH_AUTH_USERS=username:bcrypt-hash into TinyAuthEnvPath.
//  8. Confirm the recovery passphrase at the terminal (third factor).
//  9. Build the recovery bundle and write the .age + .txt files.
//
// The function is deliberately verbose with error wrapping — each step's
// error names which operation failed, so a partial-completion state is
// diagnosable from a single log line.
func Run(ctx context.Context, in OwnerBootstrapInput) (*OwnerBootstrapResult, error) {
	if err := validateInput(&in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	pidClient := in.PocketIDClient
	if pidClient == nil {
		pidClient = pocketid.NewClient(in.PocketIDURL, in.PocketIDStaticAPIKey)
	}

	// Step 1: Wait for PocketID
	if err := pidClient.WaitHealthy(ctx, healthWaitTimeout); err != nil {
		return nil, fmt.Errorf("pocketid not healthy: %w", err)
	}

	// Step 2: Bootstrap. STATIC_API_KEY-based bootstrap is idempotent —
	// once the static admin exists, BootstrapInitialAdmin verifies the
	// token works and returns ErrAlreadyBootstrapped. Anything else is a
	// real failure.
	if _, err := pidClient.BootstrapInitialAdmin(ctx, in.Owner.Email, in.Owner.Username, ""); err != nil &&
		!errors.Is(err, pocketid.ErrAlreadyBootstrapped) {
		return nil, fmt.Errorf("pocketid bootstrap: %w", err)
	}

	// Step 3: Ensure the groups used by strict SSO readiness exist.
	if err := ensureBootstrapGroups(ctx, pidClient); err != nil {
		return nil, fmt.Errorf("ensure bootstrap groups: %w", err)
	}

	// Step 4: Owner provisioning.
	ownerProv := &identity.OwnerProvisioner{
		Client:      pidClient,
		PocketIDURL: in.PocketIDURL,
	}
	ownerResult, err := ownerProv.Provision(ctx, in.Owner)
	if err != nil {
		return nil, fmt.Errorf("provision owner: %w", err)
	}
	if err := addUserToGroupByName(ctx, pidClient, ownerResult.UserID, adminsGroupName); err != nil {
		return nil, fmt.Errorf("add owner to %s group: %w", adminsGroupName, err)
	}

	// Step 5: Break-glass admin (per node).
	bgGen := &identity.BreakGlassGenerator{
		Client:      pidClient,
		NodeName:    in.NodeName,
		PocketIDURL: in.PocketIDURL,
	}
	bgCred, err := bgGen.Generate(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate break-glass: %w", err)
	}
	if err := addUserToGroupByName(ctx, pidClient, bgCred.UserID, adminsGroupName); err != nil {
		return nil, fmt.Errorf("add break-glass to %s group: %w", adminsGroupName, err)
	}

	// Step 6: TinyAuth static cred (per node).
	taGen := &identity.TinyAuthStaticGenerator{NodeName: in.NodeName}
	taCred, err := taGen.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate tinyauth-static: %w", err)
	}

	// Step 7: Write the TinyAuth users env-file. The container reads it via
	// env_file, so the orchestrator's job is to land the file at a stable
	// path with restrictive perms.
	if err := writeTinyAuthEnv(in.TinyAuthEnvPath, taCred); err != nil {
		return nil, fmt.Errorf("write tinyauth env: %w", err)
	}

	// Step 8: Recovery-passphrase confirmation. The hash alone cannot
	// derive the symmetric encryption key — we need the plaintext. If the
	// caller already has it (interactive init flow), use it directly;
	// otherwise prompt the operator to type it at the terminal.
	plaintext := in.RecoveryPassphrasePlain
	if plaintext == "" {
		prompter := in.TerminalPrompter
		if prompter == nil {
			prompter = &ttyPassphrasePrompter{}
		}
		plaintext, err = prompter.PromptAndVerify(in.RecoveryPassphraseHash, passphraseAttempts)
		if err != nil {
			return nil, fmt.Errorf("passphrase confirm: %w", err)
		}
	} else if !crypto.VerifyPassphrase(plaintext, in.RecoveryPassphraseHash) {
		// Defensive: the caller passed both fields and they disagree. Fail
		// loud rather than silently use the plaintext to encrypt — that
		// would produce a bundle that no recovered hash matches.
		return nil, fmt.Errorf("passphrase confirm: plaintext does not match provided hash")
	}

	// Step 9: Build & save bundle (encrypted .age + plaintext .txt).
	clusterRole := in.ClusterRole
	if clusterRole == "" {
		clusterRole = "main"
	}
	hostname := in.Hostname
	if hostname == "" {
		hostname = in.NodeName
	}
	builder := &identity.BundleBuilder{
		NodeName:       in.NodeName,
		Hostname:       hostname,
		ClusterRole:    clusterRole,
		PocketIDURL:    in.PocketIDURL,
		PocketIDAdmin:  bgCred,
		TinyAuthStatic: taCred,
		BundleDir:      in.BundleDir,
	}
	if in.Now != nil {
		builder.Now = in.Now
	}
	paths, err := builder.BuildAndSave(plaintext)
	if err != nil {
		return nil, fmt.Errorf("save bundle: %w", err)
	}

	return &OwnerBootstrapResult{
		OwnerUserID:    ownerResult.UserID,
		OwnerSetupURL:  ownerResult.SetupURL,
		BreakGlass:     bgCred,
		TinyAuthStatic: taCred,
		BundlePaths:    paths,
	}, nil
}

// validateInput enforces the required-fields contract documented on
// OwnerBootstrapInput and applies defaults for the optional path fields.
// Mutates in to install defaults so the rest of Run sees a populated struct.
func validateInput(in *OwnerBootstrapInput) error {
	if in.NodeName == "" {
		return fmt.Errorf("NodeName required")
	}
	if in.PocketIDURL == "" {
		return fmt.Errorf("PocketIDURL required")
	}
	if in.PocketIDStaticAPIKey == "" {
		return fmt.Errorf("PocketIDStaticAPIKey required")
	}
	if in.RecoveryPassphraseHash == "" {
		return fmt.Errorf("RecoveryPassphraseHash required")
	}
	if in.BundleDir == "" {
		in.BundleDir = defaultBundleDir
	}
	if in.TinyAuthEnvPath == "" {
		in.TinyAuthEnvPath = defaultTinyAuthEnvPath
	}
	return nil
}

// ensureBootstrapGroups makes sure the groups used by StackKit SSO exist in
// PocketID before owner/break-glass provisioning starts.
func ensureBootstrapGroups(ctx context.Context, client pocketIDClientForBootstrap) error {
	groups := []struct {
		name     string
		friendly string
	}{
		{name: ownersGroupName, friendly: ownersGroupFriendlyName},
		{name: adminsGroupName, friendly: adminsGroupFriendlyName},
	}
	for _, group := range groups {
		if err := ensureUserGroup(ctx, client, group.name, group.friendly); err != nil {
			return err
		}
	}
	return nil
}

// ensureUserGroup makes sure a named user-group exists in PocketID.
// Strategy:
//
//  1. Look up by name. If found, the group already exists; nothing to do.
//  2. Otherwise, create it. ErrAlreadyExists from CreateUserGroup is treated
//     as success — covers the rare race where another concurrent run created
//     it between our lookup and our create.
//
// Other errors (HTTP failures, marshal errors) propagate so the caller can
// abort the bootstrap rather than continuing to OwnerProvisioner.Provision
// where the missing group would surface as a confusing
// "no group by that name exists" error.
func ensureUserGroup(ctx context.Context, client pocketIDClientForBootstrap, name, friendlyName string) error {
	id, err := client.GetGroupIDByName(ctx, name)
	if err != nil {
		return fmt.Errorf("lookup %s group: %w", name, err)
	}
	if id != "" {
		return nil
	}
	_, err = client.CreateUserGroup(ctx, pocketid.CreateUserGroupRequest{
		Name:         name,
		FriendlyName: friendlyName,
	})
	if err != nil && !errors.Is(err, pocketid.ErrAlreadyExists) {
		return fmt.Errorf("create %s group: %w", name, err)
	}
	return nil
}

func addUserToGroupByName(ctx context.Context, client pocketIDClientForBootstrap, userID, groupName string) error {
	groupID, err := client.GetGroupIDByName(ctx, groupName)
	if err != nil {
		return fmt.Errorf("lookup %s group: %w", groupName, err)
	}
	if groupID == "" {
		return fmt.Errorf("lookup %s group: no group by that name exists", groupName)
	}
	if err := client.AddUserToGroup(ctx, userID, groupID); err != nil {
		return fmt.Errorf("add user to %s: %w", groupName, err)
	}
	return nil
}

// writeTinyAuthEnv lands the USERS env line at path with mode 0640. The
// containing directory is created with mode 0755 (mkdir -p semantics) so
// fresh installs don't fail when /etc/tinyauth/ doesn't yet exist.
//
// File mode 0640 keeps the bcrypt hash readable to root and the tinyauth
// service group, but nothing else — bcrypt is not a secret you want any
// non-root local user to harvest for offline cracking.
func writeTinyAuthEnv(path string, cred *identity.TinyAuthStaticCredential) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	line := "TINYAUTH_AUTH_USERS=" + cred.ToEnvValue() + "\n"
	if err := os.WriteFile(path, []byte(line), 0o640); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
