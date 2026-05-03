package composition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/pkg/models"
)

// identityContracts returns the minimal set of contracts for the identity stack.
func identityContracts() []cueval.ModuleContract {
	return []cueval.ModuleContract{
		{
			Metadata: cueval.ModuleMetadata{Name: "socket-proxy", Layer: "L0-foundation"},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"docker-socket": true},
			},
			Services: map[string]cueval.ServiceDef{
				"socket-proxy": {Required: true},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "traefik", Layer: "L1-network-edge"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"socket-proxy": {Provides: []string{"docker-socket"}},
				},
			},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"reverse-proxy": true, "tls": true},
			},
			Services: map[string]cueval.ServiceDef{
				"traefik": {Required: true},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "tinyauth", Layer: "L2-platform-identity"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"traefik": {Provides: []string{"reverse-proxy"}},
				},
			},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"forward-auth": true},
			},
			Services: map[string]cueval.ServiceDef{
				"tinyauth": {Required: true},
			},
			Settings: &cueval.SettingsSpec{
				Perma:    map[string]any{"secureCookie": false},
				Flexible: map[string]any{"authMode": "passkeys_plus_legacy"},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "pocketid", Layer: "L2-platform-identity"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"traefik": {Provides: []string{"reverse-proxy"}},
				},
			},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"oidc": true, "identity-provider": true},
			},
			Services: map[string]cueval.ServiceDef{
				"pocketid": {Required: false},
			},
		},
	}
}

func TestResolve_IdentityChain_PocketIDDefaultOIDCClient(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "homelab.example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	ic := result.Identity
	if ic == nil {
		t.Fatal("Identity is nil")
	}

	if !ic.PocketIDEnabled {
		t.Error("PocketIDEnabled should be true")
	}
	if !ic.TinyAuthEnabled {
		t.Error("TinyAuthEnabled should be true")
	}
	if !ic.TinyAuthOAuthEnabled {
		t.Error("TinyAuthOAuthEnabled should be true when both are enabled")
	}

	// TinyAuth uses a stable public PKCE client. The value is not secret and
	// must not churn between generate/apply runs.
	if ic.OIDCClientID != "stackkit-tinyauth" {
		t.Errorf("OIDCClientID = %q, want stackkit-tinyauth", ic.OIDCClientID)
	}
	if ic.OIDCClientSecret != "" {
		t.Error("OIDCClientSecret should be empty for the public PKCE client")
	}
	// PocketID's ENCRYPTION_KEY is provisioned file-based by the CLI
	// (internal/identity.EnsureEncryptionKey + cmd/stackkit/commands/generate.go),
	// not by the composition engine. Asserted in
	// internal/identity/static_api_key_test.go and the integration smoke.

	// Session secret
	if ic.TinyAuthSessionSecret == "" {
		t.Error("TinyAuthSessionSecret should be generated")
	}

	// URLs
	if ic.OIDCIssuerURL != "https://id.homelab.example.com" {
		t.Errorf("OIDCIssuerURL = %q, want https://id.homelab.example.com", ic.OIDCIssuerURL)
	}
	if ic.PocketIDAppURL != "https://id.homelab.example.com" {
		t.Errorf("PocketIDAppURL = %q, want https://id.homelab.example.com", ic.PocketIDAppURL)
	}

	// Cloud context → secure cookie
	if !ic.SecureCookie {
		t.Error("SecureCookie should be true for cloud context")
	}
}

func TestResolve_IdentityChain_LocalContext(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail: "user@test.local",
		Domain:     "stack.local",
		Context:    "local",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"pocketid": map[string]any{"enabled": true},
		},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	ic := result.Identity
	if ic.OIDCIssuerURL != "https://id.stack.local" {
		t.Errorf("OIDCIssuerURL = %q, want https://id.stack.local", ic.OIDCIssuerURL)
	}
	if ic.SecureCookie {
		t.Error("SecureCookie should be false for local context")
	}
}

func TestResolve_IdentityChain_SubdomainPrefix(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail:      "admin@example.com",
		Domain:          "example.com",
		SubdomainPrefix: "lab",
		Context:         "cloud",
		Compute:         models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"pocketid": map[string]any{"enabled": true},
		},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	ic := result.Identity
	if ic.OIDCIssuerURL != "https://lab-id.example.com" {
		t.Errorf("OIDCIssuerURL = %q, want https://lab-id.example.com", ic.OIDCIssuerURL)
	}
	if ic.PocketIDAppURL != "https://lab-id.example.com" {
		t.Errorf("PocketIDAppURL = %q, want https://lab-id.example.com", ic.PocketIDAppURL)
	}
}

func TestResolve_IdentityChain_PocketIDCannotBeDisabledWithoutAlternative(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"pocketid": map[string]any{"enabled": false},
		},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	_, err := engine.Resolve()
	if err == nil {
		t.Fatal("Resolve() should reject pocketid=false without another passkey-capable identity provider")
	}
	if !strings.Contains(err.Error(), "pocketid cannot be disabled") {
		t.Fatalf("Resolve() error = %v, want pocketid policy error", err)
	}
}

func TestResolve_IdentityChain_PocketIDCannotBeDisabledWithGenericPasskeyOIDCProvider(t *testing.T) {
	contracts := append(identityContracts(), cueval.ModuleContract{
		Metadata: cueval.ModuleMetadata{Name: "generic-idp", Layer: "L2-platform-identity"},
		Provides: &cueval.ProvidesSpec{
			Capabilities: map[string]bool{
				"identity-provider": true,
				"oidc-provider":     true,
				"passkeys":          true,
			},
		},
		Services: map[string]cueval.ServiceDef{
			"generic-idp": {
				Required:        false,
				SubdomainNested: "id",
				SubdomainFlat:   "id",
			},
		},
	})
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"generic-idp": map[string]any{"enabled": true},
			"pocketid":    map[string]any{"enabled": false},
		},
	}

	_, err := NewCompositionEngine(contracts, nil, spec).Resolve()
	if err == nil {
		t.Fatal("Resolve() should reject pocketid=false unless PocketBase itself owns passkey login")
	}
	if !strings.Contains(err.Error(), "pocketid cannot be disabled") {
		t.Fatalf("Resolve() error = %v, want pocketid policy error", err)
	}
}

func TestResolve_IdentityChain_PocketIDCanBeDisabledWithPasskeyCapablePocketBase(t *testing.T) {
	contracts := append(identityContracts(), cueval.ModuleContract{
		Metadata: cueval.ModuleMetadata{Name: "pocketbase", Layer: "L2-platform-identity"},
		Requires: &cueval.RequiresSpec{
			Services: map[string]cueval.RequiredService{
				"traefik": {Provides: []string{"reverse-proxy"}},
			},
		},
		Provides: &cueval.ProvidesSpec{
			Capabilities: map[string]bool{
				"identity-provider": true,
				"oidc-provider":     true,
				"passkeys":          true,
			},
		},
		Services: map[string]cueval.ServiceDef{
			"pocketbase": {
				Required:        false,
				SubdomainNested: "id",
				SubdomainFlat:   "id",
			},
		},
	})
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"pocketbase": map[string]any{"enabled": true},
			"pocketid":   map[string]any{"enabled": false},
		},
	}

	result, err := NewCompositionEngine(contracts, nil, spec).Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	if containsString(result.EnabledModules, "pocketid") {
		t.Fatalf("pocketid should be disabled when a passkey-capable OIDC provider is active, got %v", result.EnabledModules)
	}
	if !containsString(result.EnabledModules, "pocketbase") {
		t.Fatalf("pocketbase should be enabled as the passkey-capable provider, got %v", result.EnabledModules)
	}
	if result.Identity.PocketIDEnabled {
		t.Fatal("PocketIDEnabled should be false when the alternative provider owns passkey login")
	}
	if !result.Identity.TinyAuthOAuthEnabled {
		t.Fatal("TinyAuthOAuthEnabled should stay true for the alternative passkey OIDC provider")
	}
	if result.Identity.OIDCIssuerURL != "https://id.example.com" {
		t.Fatalf("OIDCIssuerURL = %q, want https://id.example.com", result.Identity.OIDCIssuerURL)
	}
}

func TestResolve_IdentityChain_OIDCClientStable(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"pocketid": map[string]any{"enabled": true},
		},
	}

	engine1 := NewCompositionEngine(identityContracts(), nil, spec)
	result1, _ := engine1.Resolve()

	engine2 := NewCompositionEngine(identityContracts(), nil, spec)
	result2, _ := engine2.Resolve()

	if result1.Identity.OIDCClientID != result2.Identity.OIDCClientID {
		t.Error("OIDCClientID should be stable across runs")
	}
	if result1.Identity.OIDCClientSecret != "" || result2.Identity.OIDCClientSecret != "" {
		t.Error("OIDCClientSecret should stay empty for the public PKCE client")
	}
	// PocketID encryption key is intentionally NOT regenerated per Resolve()
	// run — it must persist across invocations (file-based, see the CLI's
	// EnsureEncryptionKey path) so destroy → re-apply round-trips reuse the
	// same key. Uniqueness is enforced at file-creation time.
}

func TestResolve_EnabledModules_IdentityStack(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	// Default identity stack includes PocketID so passkey login is available
	// without an explicit opt-in.
	expected := []string{"socket-proxy", "traefik", "pocketid", "tinyauth"}
	for _, name := range expected {
		if !containsString(result.EnabledModules, name) {
			t.Errorf("expected %q in EnabledModules, got %v", name, result.EnabledModules)
		}
	}
	// Verify deployment order: socket-proxy before traefik, traefik before tinyauth
	indexOf := func(name string) int {
		for i, n := range result.EnabledModules {
			if n == name {
				return i
			}
		}
		return -1
	}

	if indexOf("socket-proxy") > indexOf("traefik") {
		t.Errorf("socket-proxy should deploy before traefik, order: %v", result.EnabledModules)
	}
	if indexOf("traefik") > indexOf("tinyauth") {
		t.Errorf("traefik should deploy before tinyauth, order: %v", result.EnabledModules)
	}
}

func TestResolve_EnabledModules_PocketIDOptIn(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"pocketid": map[string]any{"enabled": true},
		},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	if !containsString(result.EnabledModules, "pocketid") {
		t.Errorf("expected pocketid in EnabledModules when explicitly enabled, got %v", result.EnabledModules)
	}
	if !result.Identity.PocketIDEnabled || !result.Identity.TinyAuthOAuthEnabled {
		t.Errorf("expected PocketID/OIDC identity fields when explicitly enabled, got %+v", result.Identity)
	}
}

func TestResolve_AdminEmail_Fallback(t *testing.T) {
	spec := &models.StackSpec{
		Email:   "fallback@example.com",
		Domain:  "example.com",
		Context: "cloud",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	if result.Identity.AdminEmail != "fallback@example.com" {
		t.Errorf("AdminEmail = %q, want fallback@example.com", result.Identity.AdminEmail)
	}

	// AdminPassword must always be generated
	if result.Identity.AdminPassword == "" {
		t.Error("AdminPassword should be generated")
	}
	if len(result.Identity.AdminPassword) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("AdminPassword should be 32 hex chars, got %d", len(result.Identity.AdminPassword))
	}
}

func TestResolve_AdminEmail_NoneProvided(t *testing.T) {
	spec := &models.StackSpec{
		Domain:  "mylab.com",
		Context: "cloud",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	// Should generate admin@domain, never bare "admin"
	if result.Identity.AdminEmail != "admin@mylab.com" {
		t.Errorf("AdminEmail = %q, want admin@mylab.com", result.Identity.AdminEmail)
	}
}

func TestResolve_AuthMode_FromSpec(t *testing.T) {
	spec := &models.StackSpec{
		AdminEmail: "admin@example.com",
		Domain:     "example.com",
		Context:    "cloud",
		Compute:    models.ComputeSpec{Tier: models.ComputeTierStandard},
		Identity:   &models.IdentitySpec{AuthMode: "oidc_only"},
	}

	engine := NewCompositionEngine(identityContracts(), nil, spec)
	result, err := engine.Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	if result.Identity.AuthMode != "oidc_only" {
		t.Errorf("AuthMode = %q, want oidc_only", result.Identity.AuthMode)
	}
}

func TestResolve_UseCases_DefaultOnlyUnlessExplicit(t *testing.T) {
	contracts := append(identityContracts(),
		cueval.ModuleContract{
			Metadata: cueval.ModuleMetadata{Name: "immich"},
		},
		cueval.ModuleContract{
			Metadata: cueval.ModuleMetadata{Name: "home-assistant"},
		},
	)
	stackkit := &models.StackKit{
		Application: map[string]models.ApplicationDef{
			"photos": {
				Role:        models.RoleDefault,
				DefaultTool: "immich",
			},
			"smart-home": {
				Role:        models.RoleOptional,
				DefaultTool: "home-assistant",
			},
		},
	}
	spec := &models.StackSpec{
		Domain:  "home.lab",
		Context: "local",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
	}

	result, err := NewCompositionEngine(contracts, stackkit, spec).Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	if !containsString(result.EnabledModules, "immich") {
		t.Errorf("default use case module immich should be enabled, got %v", result.EnabledModules)
	}
	if containsString(result.EnabledModules, "home-assistant") {
		t.Errorf("optional use case module home-assistant should not be enabled by default, got %v", result.EnabledModules)
	}
}

func TestResolve_UseCases_OptionalCanBeExplicitlyEnabled(t *testing.T) {
	contracts := append(identityContracts(),
		cueval.ModuleContract{
			Metadata: cueval.ModuleMetadata{Name: "home-assistant"},
		},
	)
	stackkit := &models.StackKit{
		Application: map[string]models.ApplicationDef{
			"smart-home": {
				Role:        models.RoleOptional,
				DefaultTool: "home-assistant",
			},
		},
	}
	spec := &models.StackSpec{
		Domain:  "home.lab",
		Context: "local",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"smart-home": map[string]any{"enabled": true},
		},
	}

	result, err := NewCompositionEngine(contracts, stackkit, spec).Resolve()
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	if !containsString(result.EnabledModules, "home-assistant") {
		t.Errorf("explicitly enabled optional use case should be enabled, got %v", result.EnabledModules)
	}
}

func TestRealModuleDependencyCapabilities(t *testing.T) {
	modulesDir := filepath.Join("..", "..", "modules")
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		t.Skipf("modules directory not found: %s", modulesDir)
	}

	reader := cueval.NewModuleReader()
	contracts, err := reader.ReadAllModules(modulesDir)
	if err != nil {
		t.Fatalf("ReadAllModules failed: %v", err)
	}

	graph := BuildGraph(contracts)
	for _, err := range graph.Validate() {
		t.Errorf("module dependency contract violation: %v", err)
	}
}
