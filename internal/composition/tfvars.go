package composition

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/pkg/models"
	"golang.org/x/crypto/bcrypt"
)

// GenerateTFVarsJSON generates terraform.tfvars.json matching the template
// variables and overlays composition-owned identity credentials.
func GenerateTFVarsJSON(spec *models.StackSpec, cr *CompositionResult) ([]byte, error) {
	bridge := cueval.NewTerraformBridge(".")
	data, err := bridge.GenerateTFVarsBytesFromSpec(spec)
	if err != nil {
		return nil, err
	}

	if cr == nil {
		return data, nil
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tfvars for composition overlay: %w", err)
	}

	if cr.Identity != nil {
		if err := overlayIdentityConfig(tfvars, cr.Identity); err != nil {
			return nil, err
		}
	}
	if err := overlayRuntimeTemplateVars(tfvars, spec, cr); err != nil {
		return nil, err
	}
	overlayEnableFlags(tfvars, cr.EnabledModules)

	data, err = json.MarshalIndent(tfvars, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal tfvars: %w", err)
	}
	return append(data, '\n'), nil
}

func overlayRuntimeTemplateVars(tfvars map[string]any, spec *models.StackSpec, cr *CompositionResult) error {
	timezone := "UTC"
	if spec != nil && spec.System != nil && spec.System.Timezone != "" {
		timezone = spec.System.Timezone
	}
	tfvars["timezone"] = timezone

	if cr.Identity != nil {
		tfvars["tinyauth_secure_cookie"] = fmt.Sprintf("%t", cr.Identity.SecureCookie)
	}
	if _, ok := tfvars["tinyauth_session_expiry"]; !ok {
		tfvars["tinyauth_session_expiry"] = "86400"
	}

	if moduleIsEnabled(cr.EnabledModules, "vaultwarden") {
		if existing, ok := tfvars["vaultwarden_admin_token"].(string); !ok || existing == "" {
			token, err := GenerateRandomPassword(48)
			if err != nil {
				return fmt.Errorf("generate vaultwarden admin token: %w", err)
			}
			tfvars["vaultwarden_admin_token"] = token
		}
	}

	return nil
}

func overlayIdentityConfig(tfvars map[string]any, ic *IdentityConfig) error {
	if ic.AdminPassword != "" {
		tfvars["admin_password_plaintext"] = ic.AdminPassword
		hash, hashErr := BcryptHash(ic.AdminPassword)
		if hashErr != nil {
			return fmt.Errorf("failed to hash admin password: %w", hashErr)
		}
		email := ic.AdminEmail
		if email == "" {
			email = "admin"
		}
		tfvars["admin_email"] = email
		tfvars["tinyauth_users"] = fmt.Sprintf("%s:%s", email, hash)
	}

	if ic.PocketIDEnabled {
		tfvars["pocketid_app_url"] = ic.PocketIDAppURL
		// pocketid_encryption_key is provisioned by the CLI's file-based path
		// (internal/identity.EnsureEncryptionKey) and written into the tfvars
		// map directly by cmd/stackkit/commands/generate.go, so that destroy →
		// re-apply round-trips reuse the same key.
		tfvars["enable_pocketid"] = true
	} else {
		tfvars["enable_pocketid"] = false
	}

	if ic.TinyAuthOAuthEnabled {
		tfvars["tinyauth_oidc_enabled"] = true
		tfvars["tinyauth_oidc_issuer"] = ic.OIDCIssuerURL
		tfvars["tinyauth_oidc_client_id"] = ic.OIDCClientID
		tfvars["tinyauth_oidc_client_secret"] = ic.OIDCClientSecret
	}
	if ic.TinyAuthSessionSecret != "" {
		tfvars["tinyauth_session_secret"] = ic.TinyAuthSessionSecret
	}

	return nil
}

func overlayEnableFlags(tfvars map[string]any, enabledModules []string) {
	enableMapping := map[string]string{
		"traefik":     "enable_traefik",
		"tinyauth":    "enable_tinyauth",
		"pocketid":    "enable_pocketid",
		"dokploy":     "enable_dokploy",
		"dockge":      "enable_dockge",
		"coolify":     "enable_coolify",
		"dashboard":   "enable_dashboard",
		"homepage":    "enable_homepage",
		"uptime-kuma": "enable_uptime_kuma",
		"whoami":      "enable_whoami",
		"vaultwarden": "enable_vaultwarden",
		"jellyfin":    "enable_jellyfin",
		"immich":      "enable_immich",
	}
	enabledSet := make(map[string]bool, len(enabledModules))
	for _, name := range enabledModules {
		enabledSet[name] = true
	}
	for module, tfvarKey := range enableMapping {
		if enabledSet[module] {
			tfvars[tfvarKey] = true
		}
	}
}

func moduleIsEnabled(enabledModules []string, name string) bool {
	for _, module := range enabledModules {
		if module == name {
			return true
		}
	}
	return false
}

func GenerateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("generate random password: %w", err)
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}

func BcryptHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(hash), nil
}
