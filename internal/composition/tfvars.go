package composition

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/placement"
	"github.com/kombifyio/stackkits/pkg/models"
	"golang.org/x/crypto/argon2"
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
	overlayPlacement(tfvars, cr.Placement)
	enforcePlatformFallback(tfvars)
	enforceInstallMode(tfvars, spec)

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
	if image := stackKitServerImageOverride(spec); image != "" {
		tfvars["stackkit_server_image"] = image
	}

	if moduleIsEnabled(cr.EnabledModules, "vaultwarden") {
		token, _ := tfvars["vaultwarden_admin_token"].(string)
		if token == "" {
			var err error
			token, err = GenerateRandomPassword(48)
			if err != nil {
				return fmt.Errorf("generate vaultwarden admin token: %w", err)
			}
			tfvars["vaultwarden_admin_token"] = token
		}
		if phc, _ := tfvars["vaultwarden_admin_token_phc"].(string); phc == "" {
			hash, err := Argon2IDPHCString(token)
			if err != nil {
				return fmt.Errorf("hash vaultwarden admin token: %w", err)
			}
			tfvars["vaultwarden_admin_token_phc"] = hash
			tfvars["vaultwarden_admin_token_phc_b64"] = base64.RawStdEncoding.EncodeToString([]byte(hash))
		} else if b64, _ := tfvars["vaultwarden_admin_token_phc_b64"].(string); b64 == "" {
			tfvars["vaultwarden_admin_token_phc_b64"] = base64.RawStdEncoding.EncodeToString([]byte(phc))
		}
	}

	return nil
}

func Argon2IDPHCString(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	const (
		memory     uint32 = 19456
		iterations uint32 = 2
		threads    uint8  = 1
		keyLength         = 32
		saltLength        = 16
	)
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate argon2 salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, iterations, memory, threads, keyLength)
	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedKey := base64.RawStdEncoding.EncodeToString(key)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", memory, iterations, threads, encodedSalt, encodedKey), nil
}

func stackKitServerImageOverride(spec *models.StackSpec) string {
	if spec != nil && spec.Environment != nil {
		if image := strings.TrimSpace(spec.Environment["STACKKIT_SERVER_IMAGE"]); image != "" {
			return image
		}
	}
	return strings.TrimSpace(os.Getenv("STACKKIT_SERVER_IMAGE"))
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
		"komodo":      "enable_komodo",
		"dashboard":   "enable_dashboard",
		"homepage":    "enable_homepage",
		"uptime-kuma": "enable_uptime_kuma",
		"whoami":      "enable_whoami",
		"vaultwarden": "enable_vaultwarden",
		"jellyfin":    "enable_jellyfin",
		"immich":      "enable_immich",
		"cloudreve":   "enable_cloudreve",
		"nextcloud":   "enable_nextcloud",
		"files":       "enable_files",
	}
	enabledSet := make(map[string]bool, len(enabledModules))
	for _, name := range enabledModules {
		enabledSet[name] = true
	}
	for module, tfvarKey := range enableMapping {
		if enabledSet[module] {
			if module == "traefik" {
				backend, _ := tfvars["reverse_proxy_backend"].(string)
				if backend != "" && backend != models.ReverseProxyStandalone && backend != models.ReverseProxyStackKit {
					continue
				}
			}
			tfvars[tfvarKey] = true
		}
	}
}

// overlayPlacement projects the resolved S1 placement block into tfvars so the
// generated plan is a pure function of the placement resolution (capability
// bindings like sqlite vs postgres) instead of implicit template defaults.
// S2/S3 placements arrive as nil (the engine already warned) and leave the
// template defaults untouched.
func overlayPlacement(tfvars map[string]any, p *placement.Result) {
	if p == nil {
		return
	}
	tfvars["placement_mode"] = p.Mode
	tfvars["placement_exposure"] = p.Exposure
	tfvars["placement_coupling"] = p.Coupling
	caps := make(map[string]map[string]string, len(p.Capabilities))
	for name, binding := range p.Capabilities {
		caps[name] = map[string]string{
			"provider": binding.Provider,
			"target":   binding.Target,
		}
	}
	tfvars["placement_capabilities"] = caps
}

func enforcePlatformFallback(tfvars map[string]any) {
	enabled, _ := tfvars["enable_platform_fallback"].(bool)
	if !enabled {
		return
	}
	tfvars["enable_traefik"] = true
	tfvars["enable_dokploy"] = false
	tfvars["enable_dokploy_apps"] = false
	tfvars["enable_dockge"] = false
	tfvars["enable_coolify"] = false
	tfvars["enable_komodo"] = false
	tfvars["reverse_proxy_backend"] = models.ReverseProxyStandalone
	tfvars["platform_fallback_mode"] = models.PlatformFallbackStandaloneCompose
}

func enforceInstallMode(tfvars map[string]any, spec *models.StackSpec) {
	if spec == nil || spec.EffectiveInstallMode() != models.InstallModeBare {
		return
	}
	tfvars["enable_dashboard"] = false
	tfvars["enable_homepage"] = false
	tfvars["demo_data_enabled"] = false
	tfvars["setup_policy_platform"] = models.SetupPolicyManual
	tfvars["setup_policy_application_default"] = models.SetupPolicyManual
	tfvars["setup_policy_kuma"] = models.SetupPolicyManual
	tfvars["setup_policy_whoami"] = models.SetupPolicyManual
	tfvars["setup_policy_vaultwarden"] = models.SetupPolicyManual
	tfvars["setup_policy_immich"] = models.SetupPolicyManual
	tfvars["setup_policy_files"] = models.SetupPolicyManual
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
