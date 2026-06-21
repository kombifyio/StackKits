// =============================================================================
// FROZEN ARTIFACT — hand-maintained until WS-2 (truth-consolidation)
// =============================================================================
// Originally generated 2026-05-27 from the legacy kombify-admin seed
// (AdminProfile table). The legacy generator is retired (WS-1,
// docs/plans/2026-06-10-stackkit-lifecycle-truth-consolidation.md); setup
// profiles move to kombify-DB sk_stackkit_spec_profile with
// kombify-StackKits-1if (closes in WS-2), after which this file is emitted
// from the registry snapshot. Until then: edit by hand, keep
// cmd/stackkit/commands/canonical_scenario_test.go parity green.
// =============================================================================

package base

// =============================================================================
// ADMIN PROFILE DEFINITIONS
// =============================================================================

#AdminProfileID: "local-no-mail" | "kombify-me-cloud-owner" | "custom-domain-explicit-mail" | "no-owner-byos"

#AdminMailMode: "none" | "cloud-owner" | "explicit"

#AdminOwnerMode: "none" | "auto" | "custom"

#AdminSetupProfile: {
	profileKey:                   #AdminProfileID
	scenarioId:                   string
	displayName:                  string
	description?:                 string
	stackkit:                     "base-kit"
	context:                      "local" | "cloud"
	domain:                       string
	networkMode:                  "local" | "public"
	mailMode:                     #AdminMailMode
	ownerMode:                    #AdminOwnerMode
	ownerSource?:                 "first-run" | "cloud" | "local"
	paas?:                        "coolify" | "komodo"
	bootstrapMode:                "full_auto" | "bootstrapped" | "guided" | "minimal"
	demoDataEnabled:              bool
	expectedAdminEmail?:          string
	expectedReverseProxyBackend?: string
	expectedFailureMessage?:      string
	setupActions:                 [...string]
	seededContent:                [...string]
	healthChecks:                 [...string]
}

// =============================================================================
// BASEKIT V0.4 STANDARD ADMIN PROFILES
// =============================================================================

#AdminProfiles: {
	"custom-domain-explicit-mail": #AdminSetupProfile & {
		profileKey:                   "custom-domain-explicit-mail"
		scenarioId:                   "SK-S3"
		displayName:                  "Bare custom-domain explicit mail BaseKit"
		description:                  "Bare custom-domain profile with explicit owner/admin mail, Cloudflare DNS challenge, Coolify, and no Base Hub setup automation."
		stackkit:                     "base-kit"
		context:                      "cloud"
		domain:                       "kombify.pro"
		networkMode:                  "public"
		mailMode:                     "explicit"
		ownerMode:                    "custom"
		ownerSource:                  "local"
		paas:                         "coolify"
		bootstrapMode:                "minimal"
		demoDataEnabled:              false
		expectedAdminEmail:           "owner@kombify.pro"
		expectedReverseProxyBackend:  "coolify"
		setupActions:                 []
		seededContent:                []
		healthChecks:                 ["coolify-route", "auth-route", "id-route", "vault-route", "photos-route", "files-route"]
	}
	"kombify-me-cloud-owner": #AdminSetupProfile & {
		profileKey:                   "kombify-me-cloud-owner"
		scenarioId:                   "SK-S2"
		displayName:                  "Advanced kombify.me cloud-owner BaseKit"
		description:                  "Managed kombify.me advanced profile with cloud Owner handoff, Komodo as the PaaS alternative, and passive L3 one-click setup actions."
		stackkit:                     "base-kit"
		context:                      "cloud"
		domain:                       "kombify.me"
		networkMode:                  "public"
		mailMode:                     "cloud-owner"
		ownerMode:                    "auto"
		ownerSource:                  "cloud"
		paas:                         "komodo"
		bootstrapMode:                "guided"
		demoDataEnabled:              true
		expectedAdminEmail:           "tester@kombify.pro"
		expectedReverseProxyBackend:  "stackkit"
		setupActions:                 ["kuma-platform-bootstrap", "cloudreve-owner-bootstrap", "vaultwarden-admin-handoff", "immich-owner-bootstrap"]
		seededContent:                []
		healthChecks:                 ["base-route", "komodo-route", "auth-route", "id-route", "vault-protected-route", "photos-protected-route", "files-protected-route"]
	}
	"local-no-mail": #AdminSetupProfile & {
		profileKey:                   "local-no-mail"
		scenarioId:                   "SK-S1"
		displayName:                  "Local no-mail BaseKit"
		description:                  "Local home.localhost beta profile with synthetic technical admin identity and first-run Owner activation."
		stackkit:                     "base-kit"
		context:                      "local"
		domain:                       "home.localhost"
		networkMode:                  "local"
		mailMode:                     "none"
		ownerMode:                    "none"
		ownerSource:                  "first-run"
		paas:                         "coolify"
		bootstrapMode:                "bootstrapped"
		demoDataEnabled:              false
		expectedAdminEmail:           "admin@example.com"
		expectedReverseProxyBackend:  "coolify"
		setupActions:                 ["kuma-platform-bootstrap", "cloudreve-owner-bootstrap", "vaultwarden-admin-handoff", "immich-owner-bootstrap"]
		seededContent:                []
		healthChecks:                 ["base-route", "auth-route", "id-route", "vault-protected-route", "photos-protected-route", "files-protected-route"]
	}
	"no-owner-byos": #AdminSetupProfile & {
		profileKey:              "no-owner-byos"
		scenarioId:              "SK-S5"
		displayName:             "No-owner BYOS negative guardrail"
		description:             "BYOS/no-owner profile used to prove public non-interactive configs without mail fail before generate/apply."
		stackkit:                "base-kit"
		context:                 "cloud"
		domain:                  "kombify.pro"
		networkMode:             "public"
		mailMode:                "none"
		ownerMode:               "none"
		bootstrapMode:           "full_auto"
		demoDataEnabled:         true
		expectedFailureMessage:  "owner/admin email is required"
		setupActions:            []
		seededContent:           []
		healthChecks:            []
	}
}
