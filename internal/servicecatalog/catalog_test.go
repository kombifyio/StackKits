package servicecatalog

import "testing"

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

func TestDefaultCatalogRequiresStrictSSOForDefaultServices(t *testing.T) {
	for _, svc := range Default() {
		if svc.Default && svc.OwnerProvisioningPolicy != OwnerProvisioningRequired {
			t.Fatalf("%s OwnerProvisioningPolicy = %q, want %q", svc.Key, svc.OwnerProvisioningPolicy, OwnerProvisioningRequired)
		}
		if svc.Default && svc.IdentityPolicy == IdentityPolicyNone {
			t.Fatalf("%s must declare a strict identity policy", svc.Key)
		}
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
