package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/pkg/models"
)

// ownerFlags is a small struct holding the raw CLI flag values for owner
// provisioning. Used by resolveOwnerSpec to validate and (in interactive
// mode) prompt for missing fields. Decoupling the resolver from the package
// globals keeps it unit-testable.
type ownerFlags struct {
	BootstrapMode       string
	Source              string
	Email               string
	Username            string
	DisplayName         string
	RecoveryHash        string
	RecoveryMaterialRef string

	// Cloud-source only.
	CloudIssuer    string
	CloudClientID  string
	CloudSecretRef string
	ForeignSubject string
}

// minRecoveryPassphraseLen is the minimum length we accept when the operator
// types a recovery passphrase interactively. Argon2id stretches anything we
// hand it, but a short passphrase still has weak entropy against a stolen
// bundle, so we require at least 12 chars.
const minRecoveryPassphraseLen = 12

// resolveOwnerBootstrapConfig normalizes the current lane-aware owner
// bootstrap flags into the stack-spec OwnerConfig. It keeps legacy
// --owner-source=local behavior for self-hosted custom owners while allowing
// orchestrator-managed SaaS specs to say bootstrapMode=auto without local
// owner e-mail/username prompts.
func resolveOwnerBootstrapConfig(f ownerFlags, p *prompter, nonInteractive bool) (models.OwnerConfig, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(f.BootstrapMode))
	source := strings.ToLower(strings.TrimSpace(f.Source))
	if mode == "" {
		switch source {
		case "":
			if nonInteractive {
				return models.OwnerConfig{}, false, nil
			}
			mode = models.OwnerBootstrapModeCustom
		case models.OwnerSourceLocal:
			mode = models.OwnerBootstrapModeCustom
		case models.OwnerSourceCloud:
			mode = models.OwnerBootstrapModeAuto
		default:
			return models.OwnerConfig{}, false, fmt.Errorf("invalid --owner-source %q (use 'local' or 'cloud')", f.Source)
		}
	}
	if !models.IsKnownOwnerBootstrapMode(mode) || mode == "" {
		return models.OwnerConfig{}, false, fmt.Errorf("invalid --owner-bootstrap-mode %q (use 'auto', 'custom', or 'none')", f.BootstrapMode)
	}

	switch mode {
	case models.OwnerBootstrapModeNone:
		if source != "" || strings.TrimSpace(f.Email) != "" || strings.TrimSpace(f.Username) != "" ||
			strings.TrimSpace(f.DisplayName) != "" || f.RecoveryHash != "" || f.RecoveryMaterialRef != "" {
			return models.OwnerConfig{}, false, fmt.Errorf("--owner-bootstrap-mode=none cannot be combined with owner identity or recovery fields")
		}
		return models.OwnerConfig{BootstrapMode: models.OwnerBootstrapModeNone}, true, nil

	case models.OwnerBootstrapModeAuto:
		if source == "" {
			source = models.OwnerSourceCloud
		}
		if source != models.OwnerSourceCloud {
			return models.OwnerConfig{}, false, fmt.Errorf("--owner-bootstrap-mode=auto requires --owner-source=cloud when source is provided")
		}
		if f.RecoveryHash == "" && strings.TrimSpace(f.RecoveryMaterialRef) == "" {
			return models.OwnerConfig{}, false, fmt.Errorf("--owner-bootstrap-mode=auto requires --recovery-material-ref or --recovery-passphrase-hash")
		}
		if f.RecoveryHash != "" {
			if !strings.HasPrefix(f.RecoveryHash, "$argon2id$") {
				return models.OwnerConfig{}, false, fmt.Errorf("--recovery-passphrase-hash must be an argon2id PHC string (got: %q)", f.RecoveryHash)
			}
		}
		return models.OwnerConfig{
			BootstrapMode:            models.OwnerBootstrapModeAuto,
			Source:                   models.OwnerSourceCloud,
			Email:                    strings.TrimSpace(f.Email),
			Username:                 strings.TrimSpace(f.Username),
			DisplayName:              strings.TrimSpace(f.DisplayName),
			RecoveryPassphraseHash:   f.RecoveryHash,
			RecoveryMaterialRef:      strings.TrimSpace(f.RecoveryMaterialRef),
			CloudOIDCIssuer:          strings.TrimSpace(f.CloudIssuer),
			CloudOIDCClientID:        strings.TrimSpace(f.CloudClientID),
			CloudOIDCClientSecretRef: strings.TrimSpace(f.CloudSecretRef),
			CloudOIDCForeignSubject:  strings.TrimSpace(f.ForeignSubject),
		}, true, nil

	case models.OwnerBootstrapModeCustom:
		if source == "" {
			source = models.OwnerSourceLocal
		}
		if source != models.OwnerSourceLocal {
			return models.OwnerConfig{}, false, fmt.Errorf("--owner-bootstrap-mode=custom requires --owner-source=local")
		}
		ownerSpec, hasOwner, err := resolveOwnerSpec(ownerFlags{
			Source:      source,
			Email:       f.Email,
			Username:    f.Username,
			DisplayName: f.DisplayName,
		}, p, nonInteractive)
		if err != nil || !hasOwner {
			return models.OwnerConfig{}, hasOwner, err
		}
		passphraseHash, _, err := resolveRecoveryPassphrase(f.RecoveryHash, p, nonInteractive)
		if err != nil {
			return models.OwnerConfig{}, false, fmt.Errorf("resolve recovery passphrase: %w", err)
		}
		return models.OwnerConfig{
			BootstrapMode:          models.OwnerBootstrapModeCustom,
			Source:                 ownerSpec.Source,
			Email:                  ownerSpec.Email,
			Username:               ownerSpec.Username,
			DisplayName:            ownerSpec.DisplayName,
			RecoveryPassphraseHash: passphraseHash,
		}, true, nil
	default:
		return models.OwnerConfig{}, false, fmt.Errorf("invalid --owner-bootstrap-mode %q (use 'auto', 'custom', or 'none')", f.BootstrapMode)
	}
}

// resolveOwnerSpec validates flag values, prompts for missing required
// fields in interactive mode, and returns a populated OwnerSpec.
//
// Return shape:
//
//   - (spec, true,  nil)  — owner data was provided or successfully prompted.
//   - (zero, false, nil)  — non-interactive caller did NOT pass --owner-source;
//     this is a valid "skip owner provisioning" outcome and the caller should
//     leave spec.Owner empty.
//   - (zero, false, err)  — actual misconfiguration: invalid source, missing
//     required fields in non-interactive mode despite --owner-source being set,
//     or a prompt failure.
//
// This helper resolves the local/custom Owner only. Cloud/auto bootstrap is
// handled by resolveOwnerBootstrapConfig before this helper is called.
func resolveOwnerSpec(f ownerFlags, p *prompter, nonInteractive bool) (identity.OwnerSpec, bool, error) {
	source := strings.ToLower(strings.TrimSpace(f.Source))

	// Source resolution. In interactive mode this local-only helper defaults to
	// "local"; non-interactive callers without --owner-source intentionally
	// skip owner provisioning (returning
	// hasOwner=false rather than erroring).
	if source == "" {
		if nonInteractive {
			return identity.OwnerSpec{}, false, nil
		}
		source = "local"
	}
	if source == "cloud" {
		return identity.OwnerSpec{}, false, fmt.Errorf("--owner-source=cloud requires --owner-bootstrap-mode=auto")
	}
	if source != "local" {
		return identity.OwnerSpec{}, false, fmt.Errorf("invalid --owner-source %q (use 'local' or 'cloud')", f.Source)
	}

	// Email
	email := strings.TrimSpace(f.Email)
	if email == "" {
		if nonInteractive {
			return identity.OwnerSpec{}, false, fmt.Errorf("--owner-email required for --owner-source=local")
		}
		v, err := p.inputString("Owner email", "")
		if err != nil {
			return identity.OwnerSpec{}, false, fmt.Errorf("prompt owner email: %w", err)
		}
		email = strings.TrimSpace(v)
	}
	if email == "" {
		return identity.OwnerSpec{}, false, fmt.Errorf("owner email cannot be empty")
	}

	// Username
	username := strings.TrimSpace(f.Username)
	if username == "" {
		if nonInteractive {
			return identity.OwnerSpec{}, false, fmt.Errorf("--owner-username required for --owner-source=local")
		}
		v, err := p.inputString("Owner username", "")
		if err != nil {
			return identity.OwnerSpec{}, false, fmt.Errorf("prompt owner username: %w", err)
		}
		username = strings.TrimSpace(v)
	}
	if username == "" {
		return identity.OwnerSpec{}, false, fmt.Errorf("owner username cannot be empty")
	}

	// Display name (optional, defaults to username).
	displayName := strings.TrimSpace(f.DisplayName)
	if displayName == "" && !nonInteractive && p != nil {
		// Optional prompt; user can press Enter to accept the username default.
		v, err := p.inputString(fmt.Sprintf("Display name (default: %s)", username), username)
		if err != nil {
			return identity.OwnerSpec{}, false, fmt.Errorf("prompt display name: %w", err)
		}
		displayName = strings.TrimSpace(v)
	}
	if displayName == "" {
		displayName = username
	}

	return identity.OwnerSpec{
		Source:      "local",
		Email:       email,
		Username:    username,
		DisplayName: displayName,
	}, true, nil
}

// resolveRecoveryPassphrase returns an argon2id-PHC-formatted hash of the
// recovery passphrase used to encrypt the break-glass bundle.
//
// Two paths:
//
//  1. flagHash != "": the operator pre-computed a PHC string (e.g. via a
//     wizard frontend). We pass it through verbatim. Plaintext is empty —
//     the caller must reprompt at bundle-encryption time, since an
//     argon2 hash alone cannot derive the symmetric key.
//
//  2. flagHash == "" and interactive: prompt for plaintext + confirmation,
//     enforce a minimum length, hash with argon2id, return both. Three
//     retries on confirmation mismatch / too-short input.
//
// Non-interactive without a flag is an error.
func resolveRecoveryPassphrase(flagHash string, p *prompter, nonInteractive bool) (hash string, plain string, err error) {
	if flagHash != "" {
		if !strings.HasPrefix(flagHash, "$argon2id$") {
			return "", "", fmt.Errorf("--recovery-passphrase-hash must be an argon2id PHC string (got: %q)", flagHash)
		}
		// Hash provided; plaintext stays empty. The apply path reprompts when
		// it actually needs to derive the encryption key.
		return flagHash, "", nil
	}

	if nonInteractive {
		return "", "", fmt.Errorf("--recovery-passphrase-hash required in non-interactive mode")
	}
	if p == nil {
		return "", "", fmt.Errorf("interactive prompter required to read recovery passphrase")
	}

	for attempt := 0; attempt < 3; attempt++ {
		first, err := p.inputPassword("Recovery passphrase (used to encrypt break-glass bundle)")
		if err != nil {
			return "", "", fmt.Errorf("read passphrase: %w", err)
		}
		if len(first) < minRecoveryPassphraseLen {
			fmt.Printf("  Passphrase must be at least %d characters. Try again.\n", minRecoveryPassphraseLen)
			continue
		}
		confirm, err := p.inputPassword("Confirm recovery passphrase")
		if err != nil {
			return "", "", fmt.Errorf("read passphrase confirmation: %w", err)
		}
		if first != confirm {
			fmt.Println("  Passphrases don't match. Try again.")
			continue
		}
		h, err := crypto.HashPassphrase(first)
		if err != nil {
			return "", "", fmt.Errorf("hash passphrase: %w", err)
		}
		return h, first, nil
	}
	return "", "", fmt.Errorf("recovery passphrase confirmation failed after 3 attempts")
}
