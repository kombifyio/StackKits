package servicecatalog

import (
	"testing"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/registry"
)

func TestDefaultCatalogUsesCanonicalKeysAndStableSlugs(t *testing.T) {
	catalog := Default()
	services := byKey(catalog)

	tests := []struct {
		key     string
		tool    string
		module  string
		local   string
		public  string
		aliases []string
	}{
		{key: "base", tool: "dashboard", module: "dashboard", local: "base", public: "base", aliases: []string{"dashboard", "dash"}},
		{key: "home", tool: "homepage", module: "homepage", local: "home", public: "home", aliases: []string{"homepage", "homelab-dashboard"}},
		{key: "auth", tool: "tinyauth", module: "tinyauth", local: "auth", public: "auth", aliases: []string{"tinyauth"}},
		{key: "id", tool: "pocketid", module: "pocketid", local: "id", public: "id", aliases: []string{"pocketid"}},
		{key: "vault", tool: "vaultwarden", module: "vaultwarden", local: "vault", public: "vault"},
		{key: "media", tool: "jellyfin", module: "jellyfin", local: "media", public: "media"},
		{key: "photos", tool: "immich", module: "immich", local: "photos", public: "photos"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			svc, ok := services[tt.key]
			if !ok {
				t.Fatalf("service %q missing", tt.key)
			}
			if svc.ToolName != tt.tool {
				t.Fatalf("ToolName = %q, want %q", svc.ToolName, tt.tool)
			}
			if svc.ModuleSlug != tt.module {
				t.Fatalf("ModuleSlug = %q, want %q", svc.ModuleSlug, tt.module)
			}
			if svc.LocalSlug != tt.local || svc.PublicSlug != tt.public {
				t.Fatalf("slugs = local:%q public:%q, want local:%q public:%q", svc.LocalSlug, svc.PublicSlug, tt.local, tt.public)
			}
			for _, alias := range tt.aliases {
				if !contains(svc.LegacyAliases, alias) {
					t.Fatalf("LegacyAliases = %v, want %q", svc.LegacyAliases, alias)
				}
			}
		})
	}
}

func TestDefaultCatalogDeclaresIdentityForDefaultServices(t *testing.T) {
	for _, svc := range Default() {
		if svc.Default && svc.IdentityPolicy == IdentityPolicyNone && svc.Key != "base" {
			t.Fatalf("%s must declare an identity policy unless it is the bootstrap-open Node Hub", svc.Key)
		}
		if svc.Default && svc.IdentityPolicy != IdentityPolicySelfAuth && svc.OwnerProvisioningPolicy != OwnerProvisioningRequired {
			if svc.Key != "base" {
				t.Fatalf("%s OwnerProvisioningPolicy = %q, want %q for %s", svc.Key, svc.OwnerProvisioningPolicy, OwnerProvisioningRequired, svc.IdentityPolicy)
			}
		}
		if svc.GuideURL == "" {
			t.Fatalf("%s GuideURL is empty", svc.Key)
		}
		if !hasPrefix(svc.GuideURL, "https://docs.kombify.io/") {
			t.Fatalf("%s GuideURL = %q, want public docs URL", svc.Key, svc.GuideURL)
		}
	}
}

func TestDefaultCatalogKeepsBaseHubBootstrapOpen(t *testing.T) {
	base := byKey(Default())["base"]
	if base.IdentityPolicy != IdentityPolicyNone {
		t.Fatalf("base IdentityPolicy = %q, want %q", base.IdentityPolicy, IdentityPolicyNone)
	}
	if base.OwnerProvisioningPolicy != OwnerProvisioningNone {
		t.Fatalf("base OwnerProvisioningPolicy = %q, want %q", base.OwnerProvisioningPolicy, OwnerProvisioningNone)
	}
	if base.Layer != "L2-platform" {
		t.Fatalf("base Layer = %q, want L2-platform", base.Layer)
	}
}

func TestDefaultCatalogKeepsPhotosSwappableButOwnerBootstrapped(t *testing.T) {
	photos := byKey(Default())["photos"]
	if photos.LocalSlug != "photos" || photos.PublicSlug != "photos" {
		t.Fatalf("photos route must stay stable for replacement modules: %#v", photos)
	}
	if photos.OwnerProvisioningPolicy != OwnerProvisioningRequired {
		t.Fatalf("photos OwnerProvisioningPolicy = %q, want %q", photos.OwnerProvisioningPolicy, OwnerProvisioningRequired)
	}
}

func TestDefaultCatalogKeepsPaaSSelectionOutOfAppDefaults(t *testing.T) {
	services := byKey(Default())

	defaults := map[string]bool{
		"base":    true,
		"home":    true,
		"auth":    true,
		"id":      true,
		"traefik": true,
		"dokploy": false,
		"kuma":    true,
		"whoami":  true,
		"vault":   true,
		"photos":  true,
		"media":   false,
		"coolify": false,
		"dockge":  false,
		"point":   false,
	}

	for key, want := range defaults {
		svc, ok := services[key]
		if !ok {
			t.Fatalf("default catalog missing %q", key)
		}
		if svc.Default != want {
			t.Fatalf("%s Default = %v, want %v", key, svc.Default, want)
		}
	}
}

func TestDefaultCatalogKeepsPlatformServicesOutOfL3Applications(t *testing.T) {
	services := byKey(Default())

	for _, key := range []string{"kuma", "whoami"} {
		svc, ok := services[key]
		if !ok {
			t.Fatalf("default catalog missing %q", key)
		}
		if svc.Section != "Platform" {
			t.Fatalf("%s Section = %q, want Platform", key, svc.Section)
		}
		if svc.Layer != "L2-platform" {
			t.Fatalf("%s Layer = %q, want L2-platform", key, svc.Layer)
		}
		if !hasPrefix(svc.Badge, "L2 ") {
			t.Fatalf("%s Badge = %q, want L2 badge", key, svc.Badge)
		}
		if svc.SetupPolicy != SetupPolicyAutomatic {
			t.Fatalf("%s SetupPolicy = %q, want %q", key, svc.SetupPolicy, SetupPolicyAutomatic)
		}
	}

	for _, key := range []string{"vault", "media", "photos"} {
		svc, ok := services[key]
		if !ok {
			t.Fatalf("default catalog missing %q", key)
		}
		if svc.Section != "Applications" {
			t.Fatalf("%s Section = %q, want Applications", key, svc.Section)
		}
		if svc.Layer != "L3-application" {
			t.Fatalf("%s Layer = %q, want L3-application", key, svc.Layer)
		}
	}
}

func TestDefaultCatalogProvidesToolLogosAndSetupMetadata(t *testing.T) {
	services := byKey(Default())

	for _, key := range []string{"base", "coolify", "kuma", "whoami", "vault", "photos"} {
		svc, ok := services[key]
		if !ok {
			t.Fatalf("default catalog missing %q", key)
		}
		if svc.LogoURL == "" {
			t.Fatalf("%s LogoURL is empty", key)
		}
		if svc.SetupPolicy == "" {
			t.Fatalf("%s SetupPolicy is empty", key)
		}
	}

	if services["photos"].SetupPolicy != SetupPolicyOnDemand {
		t.Fatalf("photos SetupPolicy = %q, want %q", services["photos"].SetupPolicy, SetupPolicyOnDemand)
	}
	if services["photos"].SetupActionLabel != "Do the setup for me" {
		t.Fatalf("photos SetupActionLabel = %q", services["photos"].SetupActionLabel)
	}
}

func TestFromCUEOverlaysDefaultServiceAndAddsUnknownService(t *testing.T) {
	services := FromCUE([]cueval.CatalogEntry{
		{
			Key:         "dashboard",
			DisplayName: "Operations Hub",
			Description: "Custom dashboard copy",
			Icon:        "hub",
			Badge:       "L3",
			Section:     "Platform",
			Order:       99,
			EnableVar:   "enable_dashboard_custom",
			GuideURL:    "https://docs.kombify.io/guides/stackkits/custom-node-hub",
		},
		{
			Key:         "custom-app",
			ToolName:    "custom-tool",
			ModuleSlug:  "custom-module",
			Nested:      "custom",
			Flat:        "custom-public",
			DisplayName: "Custom App",
			Description: "Added from CUE",
			Section:     "Applications",
			Order:       5,
			GuideURL:    "https://docs.kombify.io/guides/stackkits/services/custom-app",
		},
	})

	byKey := byKey(services)

	base := byKey["base"]
	if base.DisplayName != "Operations Hub" {
		t.Fatalf("base DisplayName = %q", base.DisplayName)
	}
	if base.Description != "Custom dashboard copy" {
		t.Fatalf("base Description = %q", base.Description)
	}
	if base.EnableVar != "enable_dashboard_custom" {
		t.Fatalf("base EnableVar = %q", base.EnableVar)
	}
	if base.GuideURL != "https://docs.kombify.io/guides/stackkits/custom-node-hub" {
		t.Fatalf("base GuideURL = %q", base.GuideURL)
	}
	if base.LocalSlug != "base" || base.PublicSlug != "base" {
		t.Fatalf("overlay must preserve canonical base slugs: %#v", base)
	}

	custom := byKey["custom-app"]
	if custom.ToolName != "custom-tool" || custom.ModuleSlug != "custom-module" {
		t.Fatalf("custom implementation fields = %#v", custom)
	}
	if custom.LocalSlug != "custom" || custom.PublicSlug != "custom-public" {
		t.Fatalf("custom slugs = %#v", custom)
	}
	if custom.IdentityPolicy != IdentityPolicyForwardAuth {
		t.Fatalf("custom identity policy = %q", custom.IdentityPolicy)
	}
	if custom.Layer != "L3-application" {
		t.Fatalf("custom Layer = %q, want L3-application", custom.Layer)
	}
	if custom.SetupPolicy != SetupPolicyManual {
		t.Fatalf("custom SetupPolicy = %q, want %q", custom.SetupPolicy, SetupPolicyManual)
	}
	if custom.Nested != custom.LocalSlug || custom.Flat != custom.PublicSlug {
		t.Fatalf("template compat slugs not normalized: %#v", custom)
	}
	if custom.GuideURL != "https://docs.kombify.io/guides/stackkits/services/custom-app" {
		t.Fatalf("custom GuideURL = %q", custom.GuideURL)
	}
}

func TestFromRegistryNormalizesAndSortsServices(t *testing.T) {
	services := FromRegistry([]registry.Service{
		{
			Key:                     "photos",
			DisplayName:             "Photos",
			ToolName:                "immich",
			Section:                 "Applications",
			Order:                   50,
			IdentityPolicy:          IdentityPolicyOIDC,
			OwnerProvisioningPolicy: OwnerProvisioningRequired,
		},
		{
			Key:                     "auth",
			DisplayName:             "Auth",
			ToolName:                "tinyauth",
			ModuleSlug:              "tinyauth",
			LocalSlug:               "auth",
			PublicSlug:              "auth",
			Section:                 "Platform",
			Order:                   20,
			IdentityPolicy:          IdentityPolicyOIDC,
			OwnerProvisioningPolicy: OwnerProvisioningRequired,
			LegacyAliases:           []string{"tinyauth"},
			GuideURL:                "https://docs.kombify.io/guides/stackkits/services/auth-custom",
			Layer:                   "L2-platform",
			LogoURL:                 "https://cdn.simpleicons.org/tinyauth/ffffff",
			SetupPolicy:             SetupPolicyAutomatic,
			Default:                 true,
		},
	})

	if len(services) != 2 {
		t.Fatalf("len(services) = %d, want 2", len(services))
	}
	if services[0].Key != "auth" {
		t.Fatalf("first service = %q, want Platform service first", services[0].Key)
	}
	photos := byKey(services)["photos"]
	if photos.ModuleSlug != "immich" {
		t.Fatalf("photos ModuleSlug = %q, want ToolName fallback", photos.ModuleSlug)
	}
	if photos.LocalSlug != "photos" || photos.PublicSlug != "photos" {
		t.Fatalf("photos slugs = %#v", photos)
	}
	if photos.Nested != "photos" || photos.Flat != "photos" {
		t.Fatalf("photos template compat fields = %#v", photos)
	}
	if photos.GuideURL != "" {
		t.Fatalf("photos GuideURL = %q, registry conversion should not invent defaults", photos.GuideURL)
	}
	if photos.Layer != "L3-application" {
		t.Fatalf("photos Layer = %q, want L3-application", photos.Layer)
	}
	if byKey(services)["auth"].GuideURL != "https://docs.kombify.io/guides/stackkits/services/auth-custom" {
		t.Fatalf("auth GuideURL not preserved from registry")
	}
	if byKey(services)["auth"].LogoURL != "https://cdn.simpleicons.org/tinyauth/ffffff" {
		t.Fatalf("auth LogoURL not preserved from registry")
	}
}

func TestWithDefaultFallbacksMergesGuideURLsAndMissingServices(t *testing.T) {
	services := WithDefaultFallbacks([]Service{
		{
			Key:                     "auth",
			DisplayName:             "Auth from registry",
			ToolName:                "tinyauth",
			Section:                 "Platform",
			Order:                   20,
			IdentityPolicy:          IdentityPolicyOIDC,
			OwnerProvisioningPolicy: OwnerProvisioningRequired,
		},
	})

	byKey := byKey(services)
	auth := byKey["auth"]
	if auth.DisplayName != "Auth from registry" {
		t.Fatalf("auth DisplayName = %q", auth.DisplayName)
	}
	if auth.GuideURL != "https://docs.kombify.io/guides/stackkits/services/tinyauth" {
		t.Fatalf("auth GuideURL = %q", auth.GuideURL)
	}
	if auth.LogoURL == "" {
		t.Fatalf("auth LogoURL should be filled from default catalog")
	}
	if auth.SetupPolicy != SetupPolicyAutomatic {
		t.Fatalf("auth SetupPolicy = %q, want %q", auth.SetupPolicy, SetupPolicyAutomatic)
	}
	if _, ok := byKey["base"]; !ok {
		t.Fatal("base default service was not appended")
	}
	if _, ok := byKey["home"]; !ok {
		t.Fatal("home default service was not appended")
	}
}

func TestWithDefaultFallbacksKeepsKnownDefaultFlagsFromLocalContract(t *testing.T) {
	services := WithDefaultFallbacks([]Service{
		{
			Key:                     "media",
			DisplayName:             "Media from registry",
			ToolName:                "jellyfin",
			IdentityPolicy:          IdentityPolicySelfAuth,
			OwnerProvisioningPolicy: OwnerProvisioningNone,
			Default:                 true,
		},
	})

	media := byKey(services)["media"]
	if media.Default {
		t.Fatalf("media Default = true from registry drift, want local BaseKit contract false")
	}
}

func TestWithDefaultFallbacksPinsBaseHubBootstrapOpenContract(t *testing.T) {
	services := WithDefaultFallbacks([]Service{
		{
			Key:                     "base",
			DisplayName:             "Base from stale registry",
			ToolName:                "dashboard",
			IdentityPolicy:          IdentityPolicyForwardAuth,
			OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Default:                 true,
		},
	})

	base := byKey(services)["base"]
	if base.IdentityPolicy != IdentityPolicyNone {
		t.Fatalf("base IdentityPolicy = %q, want %q", base.IdentityPolicy, IdentityPolicyNone)
	}
	if base.OwnerProvisioningPolicy != OwnerProvisioningNone {
		t.Fatalf("base OwnerProvisioningPolicy = %q, want %q", base.OwnerProvisioningPolicy, OwnerProvisioningNone)
	}
}

func TestFromRegistryEmptyReturnsNil(t *testing.T) {
	if got := FromRegistry(nil); got != nil {
		t.Fatalf("FromRegistry(nil) = %#v, want nil", got)
	}
}

func byKey(services []Service) map[string]Service {
	out := make(map[string]Service, len(services))
	for _, svc := range services {
		out[svc.Key] = svc
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
