package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/identity"
)

// ownerFlags is a small struct holding the raw CLI flag values for owner
// provisioning. Used by resolveOwnerSpec to validate and (in interactive
// mode) prompt for missing fields. Decoupling the resolver from the package
// globals keeps it unit-testable.
type ownerFlags struct {
	Source      string
	Email       string
	Username    string
	DisplayName string

	// Cloud-source only (Phase 2; Phase 1 errors out before these matter).
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
// Phase 1: --owner-source=cloud is rejected with a clear Phase-2 error. The
// cloud-related fields on ownerFlags are accepted for shape but unused.
func resolveOwnerSpec(f ownerFlags, p *prompter, nonInteractive bool) (identity.OwnerSpec, bool, error) {
	source := strings.ToLower(strings.TrimSpace(f.Source))

	// Source resolution. In interactive mode we default to "local" since
	// that's the only Phase-1 option anyway; non-interactive callers without
	// --owner-source intentionally skip owner provisioning (returning
	// hasOwner=false rather than erroring).
	if source == "" {
		if nonInteractive {
			return identity.OwnerSpec{}, false, nil
		}
		source = "local"
	}
	if source == "cloud" {
		return identity.OwnerSpec{}, false, fmt.Errorf("--owner-source=cloud is Phase 2, not yet supported")
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
		// Hash provided; plaintext stays empty. Task 11's apply path
		// reprompts when it actually needs to derive the encryption key.
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
