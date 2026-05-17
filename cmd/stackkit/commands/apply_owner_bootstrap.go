package commands

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/apply"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

// runOwnerBootstrap performs the post-deployment owner + break-glass
// provisioning sequence. It is gated by spec.Owner — the field populated by
// `stackkit init` or by an orchestration handoff when owner bootstrap is
// requested.
//
// Empty owner data, bootstrapMode=none, and bootstrapMode=auto are no-ops for
// local apply. Only bootstrapMode=custom provisions the local PocketID Owner.
// The --cluster-mode flag still gates which nodes provision the daily-admin
// record: only "first" runs the bootstrap; "join" nodes are no-ops.
//
// Inputs:
//   - cmd is the cobra command driving the run; we use it only for context
//     propagation.
//   - spec is the loaded StackSpec. spec.Owner provides the owner identity
//     persisted at init-time; spec.Domain is used to compose the PocketID
//     origin (id.<domain>).
//
// Side effects on success:
//   - The owner record exists in PocketID and an enrollment URL is printed
//     for the operator to visit in their browser.
//   - A per-node break-glass admin record exists in PocketID.
//   - The TinyAuth USERS env-file is written.
//   - The recovery bundle (encrypted .age + plaintext .txt) is written to
//     the configured bundle directory.
func runOwnerBootstrap(cmd *cobra.Command, spec *models.StackSpec) error {
	if spec == nil {
		return nil
	}
	switch spec.Owner.EffectiveBootstrapMode() {
	case "", models.OwnerBootstrapModeNone:
		return nil
	case models.OwnerBootstrapModeAuto:
		printInfo("Skipping local owner bootstrap (owner bootstrap is orchestrator-managed)")
		return nil
	}
	if initClusterMode != "" && initClusterMode != "first" {
		printInfo("Skipping owner bootstrap (cluster-mode=%q)", initClusterMode)
		return nil
	}

	if spec.Owner.RecoveryPassphraseHash == "" {
		return fmt.Errorf("spec.owner.source is set but recoveryPassphraseHash is missing — re-run 'stackkit init'")
	}
	if spec.Owner.Email == "" || spec.Owner.Username == "" {
		return fmt.Errorf("spec.owner is incomplete (email or username missing) — re-run 'stackkit init'")
	}

	// Read the per-homelab STATIC_API_KEY persisted by `stackkit init` into
	// <wd>/.stackkit/pocketid-static-api-key. The same value was rendered
	// into the PocketID container's env block via terraform.tfvars.json, so
	// the bootstrap call below talks to the running container with the
	// matching credential.
	staticAPIKey, err := identity.ReadStaticAPIKey(getWorkDir())
	if err != nil {
		return fmt.Errorf("load pocketid static api key: %w", err)
	}

	displayName := spec.Owner.DisplayName
	if displayName == "" {
		displayName = spec.Owner.Username
	}

	ownerSpec := identity.OwnerSpec{
		Source:           spec.Owner.Source,
		Email:            spec.Owner.Email,
		Username:         spec.Owner.Username,
		DisplayName:      displayName,
		ForeignSubjectID: spec.Owner.CloudOIDCForeignSubject,
	}

	pocketIDURL := pocketIDURLForSpec(spec)
	if pocketIDURL == "" {
		return fmt.Errorf("could not compose PocketID URL: spec.Domain is empty")
	}

	nodeName := nodeNameForBootstrap()

	deployLog.Event("owner_bootstrap.start",
		slog.String("node", nodeName),
		slog.String("pocketid_url", pocketIDURL),
		slog.String("owner_source", spec.Owner.Source),
		slog.String("cluster_mode", initClusterMode),
	)

	printInfo("Bootstrapping owner and break-glass...")

	// Recovery passphrase plaintext is empty here — apply.Run prompts at the
	// terminal for the third-factor (only the hash is persisted in the spec).
	result, err := apply.Run(cmd.Context(), apply.OwnerBootstrapInput{
		NodeName:               nodeName,
		Hostname:               nodeName,
		PocketIDURL:            pocketIDURL,
		PocketIDStaticAPIKey:   staticAPIKey,
		Owner:                  ownerSpec,
		RecoveryPassphraseHash: spec.Owner.RecoveryPassphraseHash,
	})
	if err != nil {
		deployLog.Error("owner_bootstrap.failed",
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("owner bootstrap: %w", err)
	}

	deployLog.Event("owner_bootstrap.success",
		slog.String("owner_user_id", result.OwnerUserID),
		slog.String("break_glass_user_id", result.BreakGlass.UserID),
		slog.String("bundle_path", result.BundlePaths.EncryptedPath),
	)

	printOwnerBootstrapSummary(cmd, ownerSpec, result)
	return nil
}

// pocketIDURLForSpec returns the public origin of the homelab's PocketID
// instance. Phase 1 deployments expose PocketID at id.<spec.Domain>. Returns
// "" when the spec is missing or has no domain — the caller surfaces that
// as a user-facing error.
func pocketIDURLForSpec(spec *models.StackSpec) string {
	if spec == nil {
		return ""
	}
	domain := strings.TrimSpace(spec.Domain)
	if domain == "" {
		return ""
	}
	proto := "https"
	if models.IsLocalhostDomain(domain) {
		proto = "http"
	}
	return proto + "://id." + domain
}

// nodeNameForBootstrap returns a stable identifier for the firstnode the
// orchestrator is running on. Falls back to "node1" when os.Hostname is
// unavailable (rare but it can happen in container builds without a
// configured hostname). The value is used for synthetic break-glass
// usernames, the bundle filename, and the TinyAuth-static username — so it
// must be filename-safe. We trust the host's hostname and fail loud later
// if it produces something the downstream packages reject.
func nodeNameForBootstrap() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "node1"
	}
	// Strip a trailing dot (some resolvers include one in FQDNs).
	return strings.TrimSuffix(host, ".")
}

// printOwnerBootstrapSummary formats the success message for the operator.
// The setup URL is load-bearing — without visiting it once they can't
// enroll a passkey, and the account stays unusable. The bundle paths drive
// their off-site-backup workflow.
//
// The setup URL and bundle paths are written to stderr so a `stackkit apply
// | tee log` or CI log capture doesn't accidentally land them in shared
// files. Terminals show stderr by default, so the user still sees them
// inline; only deliberate redirection (>file, >&1) hides them.
func printOwnerBootstrapSummary(cmd *cobra.Command, ownerSpec identity.OwnerSpec, result *apply.OwnerBootstrapResult) {
	out := cmd.ErrOrStderr()

	fmt.Println()
	printSuccess("Owner created: %s", ownerSpec.Email)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  WARNING: This URL contains a single-use token. Do not paste it into chat or commit it.")
	fmt.Fprintln(out, "  Owner setup URL (open this in your browser to enroll WebAuthn):")
	fmt.Fprintln(out, "    "+result.OwnerSetupURL)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Recovery bundle:")
	fmt.Fprintln(out, "    Encrypted (back this up off-site): "+result.BundlePaths.EncryptedPath)
	fmt.Fprintln(out, "    Plaintext (root-only convenience): "+result.BundlePaths.PlaintextPath)
	fmt.Fprintln(out)
	printInfo("Save the encrypted bundle and your recovery passphrase to a password manager.")
	printInfo("Together with physical access to this node they form the three-factor recovery.")
}
