// Package wizard — Shared 4-question wizard schema.
//
// This file is the Single Source of Truth for the user-facing install wizard.
// It is consumed by:
//   - TechStack web wizard  (Phase 1, Q2/2026): web/src/lib/wizard/ reads this CUE
//     definition (via cue-go server-side or a generated JSON schema) and renders
//     the form.
//   - StackKits CLI wizard  (Phase 4, Q1/2027): cmd/stackkit/init.go loads this
//     file directly and drives a bubbletea TUI.
//   - kombify-Cloud StackKits Finder (2026-07): the dashboard finder captures
//     answers against this schema and reports them via the Admin API
//     (SkWizardAnswer) on completion.
//
// The 4 questions map 1:1 to what the user needs to decide before `stackkit apply`
// can run. Everything else is inferred from hardware (Context, Compute Tier) or
// defaulted by the StackKit choice.
//
// Changes to this schema require coordination between StackKits and TechStack;
// see V6 ROADMAP Phase 1 (TechStack) and Phase 4 (StackKits).
//
// STATUS: Scaffolded 2026-04-17 for V6. Field set is stable; validation rules
// will tighten in Phase 4. v1.1.0 (2026-07-29) adds the optional operator
// capability profile — the platform-canonical technical-affinity scale shared
// with kombify-Techstack (pkg/core/types.go OperatorCapabilityProfile).
package wizard

// #Answer is the canonical shape of a completed 4-question wizard run.
// Both TechStack and StackKits produce a value satisfying this schema.
#Answer: {
	// Schema version. Bump when the shape of #Answer changes.
	version: "1.1.0"

	// Q1: What do you want to do with this homelab?
	// Maps to the V5 Use Case catalog (the 10 use cases). Multi-select.
	// BaseKit V6 default set is pre-checked: photos, media, vault, smart-home, files, ai.
	goals: [...#Goal] & [_, ...]

	// Q2: How do you want to reach your services?
	// Drives the Context detection hint and the domain/TLS strategy.
	access: #Access

	// Q3: Who will use this? How many users?
	// Drives LLDAP seeding, admin-bootstrap settings, and MFA policy.
	users: #Users

	// Q4: How should people log in?
	// Drives login-gateway settings: passkeys, passwords, OIDC providers.
	login: #Login

	// Q5 (optional, opt-in): free-text "anything else?" — reserved for Tier-2
	// provisioning (ADR-0009). The inter-repo contract with kombify-AI does
	// not exist yet, so this field is captured but not routed in V6. Empty =
	// Tier-1 only; non-empty is stored alongside the answer for later routing
	// once the kombify-AI intent contract lands.
	intentFreeText?: string

	// Q0 (optional): declared technical affinity of the operator.
	// Drives adaptive question depth in the wizard UI and the StackKit
	// profile/guidance resolution downstream. Absent = consumers infer a
	// conservative default; present = the user's declaration wins over
	// behavioral inference (declared source, high confidence).
	capability?: #OperatorCapability
}

// #OperatorCapability is the platform-canonical technical-affinity scale.
// Field names mirror kombify-Techstack pkg/core/types.go
// OperatorCapabilityProfile so values round-trip through DecisionContext
// without translation. Score 1..10 maps deterministically to five bands.
#OperatorCapability: {
	// Affinity score on the canonical 1..10 scale.
	score: int & >=1 & <=10

	// Interaction band derived from the score:
	//   1-2  oneclick      maximum defaults, low jargon, one primary action
	//   3-4  guided        few material choices
	//   5-6  curious-admin recommended defaults plus concise alternatives
	//   7-8  techie        explicit StackKit/service/placement/network controls
	//   9-10 expert        validated overrides and full trace visibility
	band: "oneclick" | "guided" | "curious-admin" | "techie" | "expert"

	// Score↔band consistency is part of the contract, not a convention.
	if score <= 2 {band: "oneclick"}
	if score >= 3 && score <= 4 {band: "guided"}
	if score >= 5 && score <= 6 {band: "curious-admin"}
	if score >= 7 && score <= 8 {band: "techie"}
	if score >= 9 {band: "expert"}

	// How the score was obtained. "declared" = explicit self-assessment in a
	// wizard UI (wins over inference); "inferred" = derived from behavioral
	// signals (channel, advanced interactions).
	source: *"declared" | "inferred"

	// Confidence in the score, 0..1. Declared self-assessment is high
	// confidence by definition; inferred scores carry their model confidence.
	confidence?: number & >=0 & <=1

	// Why this score was chosen. Behavioral signals are recorded here even
	// when source is "declared" — they never silently override the score.
	evidence?: [...#CapabilityEvidence]
}

// #CapabilityEvidence records one signal behind a capability score.
// Mirrors kombify-Techstack OperatorCapabilityEvidence.
#CapabilityEvidence: {
	signal:  string
	weight?: int
	source?: string
}

// #Goal maps to V5 §2 "The 10 Use Cases" catalog.
#Goal:
	"platform" |  // Always implicit; listed for explicitness.
	"photos" |    // Immich
	"media" |     // Jellyfin + *arr
	"vault" |     // Vaultwarden
	"smart-home" |// Home Assistant + Mosquitto + Zigbee2MQTT
	"files" |     // Cloudreve
	"ai" |        // Ollama + Open WebUI
	"dev" |       // Gitea + Woodpecker
	"mail" |      // Stalwart
	"game" |      // Various
	"remote"      // Guacamole

// #Access expresses reachability and TLS strategy.
#Access: {
	// Who can reach the services.
	audience: "local-only" | "vpn-mesh" | "public-internet"

	// The domain under which services will be reachable, e.g. "mylab.example.com".
	// For `local-only`: optional (defaults to `stack.local`).
	// For `public-internet`: required.
	domain?: =~"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$"

	// TLS strategy. Derived in most cases but user-overridable.
	tls: *"auto" | "letsencrypt" | "step-ca" | "custom-cert"

	// For vpn-mesh audience: which mesh to use. Default Tailscale.
	vpnMesh?: *"tailscale" | "headscale" | "wireguard-raw"
}

// #Users describes the user-base shape.
#Users: {
	// Approximate number of human users. Drives LLDAP storage + memory sizing.
	count: int & >=1 & <=10000

	// Whether accounts are managed centrally (admin creates all) or self-served
	// (users can register themselves). BaseKit V6 defaults to "admin-managed".
	model: *"admin-managed" | "self-service"

	// Whether MFA should be required for every user (not just admins).
	requireMfaAll: *false | bool
}

// #Login describes the login experience.
#Login: {
	// Primary method. Passkeys recommended; passwords + OIDC are supported fallbacks.
	primaryMethod: *"passkeys" | "passwords" | "oidc-upstream"

	// Upstream OIDC providers to federate with (empty = local PocketID only).
	// Each entry is the name of a configured provider (e.g., "google", "github",
	// "auth0"). Configuration of the upstream provider itself is out of scope
	// for the wizard; the user supplies client credentials post-install.
	upstreamProviders: *[] | [...string]

	// Session expiry in seconds (feed through to login-gateway).
	sessionExpirySeconds: *86400 | int & >=300 & <=604800
}

// #ValidationRules express cross-field invariants that apply to a completed #Answer.
// Enforced at wizard-submit time by TechStack + StackKits.
#ValidationRules: {
	answer: #Answer

	// Public audience requires a real domain.
	if answer.access.audience == "public-internet" {
		answer: access: domain: !=""
	}

	// VPN-mesh audience: pick a mesh.
	if answer.access.audience == "vpn-mesh" {
		answer: access: vpnMesh: string
	}

	// Let's Encrypt requires public audience OR a domain that resolves publicly.
	if answer.access.tls == "letsencrypt" {
		answer: access: audience: "public-internet"
	}
}
