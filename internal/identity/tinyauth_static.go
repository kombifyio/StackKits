package identity

// TinyAuth static-credential generator for Layer-2 break-glass.
//
// TinyAuth supports a static `USERS` env var of the form
// "username:bcrypt-hash" entries. This is independent of PocketID and gives
// the owner a way to reach recovery UIs even when PocketID is corrupt or
// down. Each node gets its own TinyAuth static user; the bcrypt hash is
// rendered into the TinyAuth container env, while the plaintext password is
// included in the recovery bundle (and shown to the owner once at install
// time) so they always have something to type if everything else is broken.

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/crypto"
	"golang.org/x/crypto/bcrypt"
)

// defaultBcryptCost is the cost factor used when BcryptCost is zero.
// Cost 12 is the OWASP-recommended minimum for 2025-class hardware and is
// what TinyAuth itself defaults to.
const defaultBcryptCost = 12

// TinyAuthStaticCredential is the materialized result of a TinyAuth static
// user provisioning. PasswordPlain is shown to the owner exactly once and
// embedded in the sealed recovery bundle; PasswordBcrypt is what gets
// rendered into the TinyAuth container `USERS` env var.
type TinyAuthStaticCredential struct {
	// Username is the synthetic local handle of the form
	// "bg-<nodename>-static". Distinct from the PocketID break-glass
	// username so logs/audit can tell the two layers apart.
	Username string

	// PasswordPlain is the human-typeable password (base64 of 32 random
	// bytes). Goes into the recovery bundle; never logged.
	PasswordPlain string

	// PasswordBcrypt is the bcrypt hash of PasswordPlain. Goes into the
	// TinyAuth container env via ToEnvValue.
	PasswordBcrypt string
}

// ToEnvValue returns the "username:bcrypt-hash" entry suitable for direct
// inclusion in TinyAuth's USERS env var. Multiple users are joined with
// commas; this method returns a single entry.
func (c *TinyAuthStaticCredential) ToEnvValue() string {
	return fmt.Sprintf("%s:%s", c.Username, c.PasswordBcrypt)
}

// TinyAuthStaticGenerator creates a per-node TinyAuth static credential.
type TinyAuthStaticGenerator struct {
	// NodeName identifies the homelab firstnode this credential belongs
	// to. Required.
	NodeName string

	// BcryptCost is the bcrypt cost factor. Defaults to 12 when zero.
	// Tests may pass a lower value to keep the suite fast.
	BcryptCost int
}

// Generate produces a fresh TinyAuth static credential. The plaintext
// password is 32 random bytes encoded as base64 (~43 characters); the
// bcrypt hash is computed at the configured cost.
func (g *TinyAuthStaticGenerator) Generate() (*TinyAuthStaticCredential, error) {
	if g.NodeName == "" {
		return nil, fmt.Errorf("tinyauth-static: NodeName required")
	}
	cost := g.BcryptCost
	if cost == 0 {
		cost = defaultBcryptCost
	}

	pwd, err := crypto.RandomPassword(32)
	if err != nil {
		return nil, fmt.Errorf("tinyauth-static: random password: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), cost)
	if err != nil {
		return nil, fmt.Errorf("tinyauth-static: bcrypt: %w", err)
	}

	return &TinyAuthStaticCredential{
		Username:       fmt.Sprintf("bg-%s-static", g.NodeName),
		PasswordPlain:  pwd,
		PasswordBcrypt: string(hash),
	}, nil
}
