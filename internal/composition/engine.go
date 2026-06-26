// Package composition resolves module dependencies and determines deployment order.
package composition

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/placement"
	"github.com/kombifyio/stackkits/pkg/models"
)

// CompositionEngine resolves use cases and addons into a set of enabled modules
// with dependency-ordered deployment and propagated settings.
type CompositionEngine struct {
	contracts  map[string]*cueval.ModuleContract
	stackkit   *models.StackKit
	spec       *models.StackSpec
	modeMatrix *cueval.KitModeMatrix
}

// SetModeMatrix attaches the kit's mode-support matrix (#KitModeSupport).
// When set, Resolve grades the requested (placement, install, context) cell:
// unsupported cells hard-fail, scaffolding cells warn. Kits without a matrix
// (older exported caches) simply skip enforcement.
func (e *CompositionEngine) SetModeMatrix(m *cueval.KitModeMatrix) {
	e.modeMatrix = m
}

// CompositionResult is the output of the engine: which modules to enable, in what order,
// with what settings, and any warnings.
type CompositionResult struct {
	// EnabledModules in dependency order (deploy first → deploy last).
	EnabledModules []string
	// ModuleSettings maps module name → merged settings (perma + flexible + context override).
	ModuleSettings map[string]map[string]any
	// Warnings are non-fatal issues (e.g., addon requires higher tier).
	Warnings []string
	// Identity holds the resolved identity configuration.
	Identity *IdentityConfig
	// Placement holds the resolved S1 capability bindings (nil for S2/S3
	// placements, which StackKits-OSS does not realize — see Warnings).
	Placement *placement.Result
	// ControlPlaneHandoffs records enabled package/runtime profiles whose
	// realization is intentionally owned by Admin/TechStack rather than the
	// local OSS resolver.
	ControlPlaneHandoffs []ControlPlaneHandoff
}

type ControlPlaneHandoff struct {
	UseCase              string   `json:"useCase" yaml:"useCase"`
	Tool                 string   `json:"tool" yaml:"tool"`
	RuntimeProfile       string   `json:"runtimeProfile" yaml:"runtimeProfile"`
	Realization          string   `json:"realization,omitempty" yaml:"realization,omitempty"`
	Reason               string   `json:"reason" yaml:"reason"`
	ProductMCP           string   `json:"productMcp,omitempty" yaml:"productMcp,omitempty"`
	RequiresControlPlane bool     `json:"requiresControlPlane,omitempty" yaml:"requiresControlPlane,omitempty"`
	RequiresLocalBridge  bool     `json:"requiresLocalBridge,omitempty" yaml:"requiresLocalBridge,omitempty"`
	PlacementModes       []string `json:"placementModes,omitempty" yaml:"placementModes,omitempty"`
	Contexts             []string `json:"contexts,omitempty" yaml:"contexts,omitempty"`
}

// IdentityConfig holds the resolved identity stack configuration.
type IdentityConfig struct {
	// AdminEmail is the primary admin user email.
	AdminEmail string
	// AdminPassword is the generated admin password (plaintext, for initial setup only).
	AdminPassword string
	// PocketIDEnabled indicates whether PocketID OIDC provider is enabled.
	PocketIDEnabled bool
	// TinyAuthEnabled indicates whether TinyAuth ForwardAuth is enabled.
	TinyAuthEnabled bool
	// OIDCIssuerURL is the PocketID issuer URL (e.g., https://id.example.com).
	OIDCIssuerURL string
	// OIDCClientID is the generated OAuth2 client ID for TinyAuth→PocketID.
	OIDCClientID string
	// OIDCClientSecret is the generated OAuth2 client secret for TinyAuth→PocketID.
	OIDCClientSecret string
	// TinyAuthOAuthEnabled indicates TinyAuth should use PocketID as OAuth provider.
	TinyAuthOAuthEnabled bool
	// TinyAuthSessionSecret is a random secret for TinyAuth cookie signing.
	TinyAuthSessionSecret string
	// PocketIDAppURL is the external URL of the PocketID instance.
	PocketIDAppURL string
	// SecureCookie is resolved from context (local=false, cloud=true).
	SecureCookie bool
	// AuthMode is the TinyAuth authentication mode.
	AuthMode string
}

// NewCompositionEngine creates a new engine from loaded contracts, stackkit, and user spec.
func NewCompositionEngine(contracts []cueval.ModuleContract, stackkit *models.StackKit, spec *models.StackSpec) *CompositionEngine {
	cm := make(map[string]*cueval.ModuleContract, len(contracts))
	for i := range contracts {
		cm[contracts[i].Metadata.Name] = &contracts[i]
	}
	return &CompositionEngine{
		contracts: cm,
		stackkit:  stackkit,
		spec:      spec,
	}
}

// Resolve runs the composition pipeline:
//  1. Resolve use cases → required modules
//  2. Resolve addons → additional modules
//  3. Add platform defaults (identity, dashboard)
//  4. Expand transitive dependencies
//  5. Validate and topologically sort
//  6. Propagate settings with context overrides
//  7. Resolve identity configuration
//  8. Resolve S1 placement capability bindings
func (e *CompositionEngine) Resolve() (*CompositionResult, error) {
	result := &CompositionResult{
		ModuleSettings: make(map[string]map[string]any),
	}

	// Step 1: Collect explicitly enabled modules from use cases
	enabled := make(map[string]bool)
	e.resolveApplication(enabled, result)

	// Step 2: Collect addon modules
	e.resolveAddons(enabled, result)

	// Step 3: Add platform defaults (always-on modules)
	e.addPlatformDefaults(enabled)

	// Step 4: Apply per-service overrides from spec
	if err := e.applyServiceOverrides(enabled); err != nil {
		return nil, fmt.Errorf("service overrides: %w", err)
	}

	// Step 5: Expand transitive dependencies
	if err := e.expandDependencies(enabled, result); err != nil {
		return nil, fmt.Errorf("dependency resolution: %w", err)
	}

	// Step 6: Topological sort
	order, err := e.sortModules(enabled)
	if err != nil {
		return nil, fmt.Errorf("topological sort: %w", err)
	}
	result.EnabledModules = order

	// Step 7: Propagate settings with context overrides
	e.propagateSettings(result)

	// Step 8: Resolve identity configuration
	if err := e.resolveIdentity(result); err != nil {
		return nil, fmt.Errorf("identity resolution: %w", err)
	}

	// Step 9: Resolve S1 placement capability bindings (additive, non-breaking).
	// S2/S3 placements (managed-serverless/coupled) are not realized here; they
	// surface as a warning rather than failing the resolve.
	if pr, perr := placement.ResolveS1(e.spec); perr != nil {
		result.Warnings = append(result.Warnings, perr.Error())
	} else {
		result.Placement = pr
	}

	// Step 10: Validate module placement eligibility (#PlacementSupport).
	// A module whose declared eligibility excludes the resolved placement mode
	// is a hard composition error, not a silent acceptance.
	if err := e.validatePlacementEligibility(result); err != nil {
		return nil, fmt.Errorf("placement eligibility: %w", err)
	}

	// Step 11: Enforce the kit mode-support matrix (#KitModeSupport) when one
	// is attached: unsupported cells hard-fail, scaffolding cells warn.
	if err := e.enforceModeMatrix(result); err != nil {
		return nil, fmt.Errorf("mode matrix: %w", err)
	}

	return result, nil
}

// enforceModeMatrix grades the requested (placement, install, context) cell
// against the kit's declared matrix. The managed-serverless placement axis is
// graded control-plane by schema; its warning already comes from the S2/S3
// path in step 9, so control-plane here stays a warning, never an error.
func (e *CompositionEngine) enforceModeMatrix(result *CompositionResult) error {
	if e.modeMatrix == nil {
		return nil
	}
	placementMode := e.spec.EffectivePlacementMode()
	installMode := e.spec.EffectiveInstallMode()
	nodeContext := string(e.spec.Context)
	if nodeContext == "" {
		nodeContext = string(models.ContextLocal)
	}

	level, details := e.modeMatrix.CellVerdict(placementMode, installMode, nodeContext)
	switch level {
	case cueval.SupportUnsupported:
		return fmt.Errorf("kit %q does not support this mode cell: %s",
			e.modeMatrix.Kit, strings.Join(details, "; "))
	case cueval.SupportScaffolding, cueval.SupportControlPlane:
		for _, d := range details {
			result.Warnings = append(result.Warnings, "mode matrix: "+d)
		}
	}

	// PAAS axis (#PaasStatus): an explicitly selected PAAS that the kit grades
	// draft/experimental — or does not offer at all — surfaces as a warning.
	if paas := e.spec.PAAS; paas != "" && paas != models.PAASNone {
		status, declared := e.modeMatrix.Paas[paas]
		switch {
		case !declared:
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("mode matrix: paas %q is not offered by kit %q", paas, e.modeMatrix.Kit))
		case status == "draft", status == "experimental":
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("mode matrix: paas %q is %s in kit %q — not E2E-verified", paas, status, e.modeMatrix.Kit))
		}
	}
	return nil
}

// validatePlacementEligibility rejects enabled modules that declare themselves
// ineligible for the resolved placement mode. S2/S3 placements (Placement nil)
// are skipped — they already surfaced as a warning and are not realized here.
func (e *CompositionEngine) validatePlacementEligibility(result *CompositionResult) error {
	if result.Placement == nil {
		return nil
	}
	mode := result.Placement.Mode
	var rejected []string
	for _, name := range result.EnabledModules {
		contract, ok := e.contracts[name]
		if !ok {
			continue
		}
		if contract.EligibleForPlacement(mode) {
			continue
		}
		detail := fmt.Sprintf("module %q is not eligible for placement mode %q", name, mode)
		if contract.Placement != nil {
			if contract.Placement.RejectionReason != "" {
				detail += fmt.Sprintf(" (reason: %s)", contract.Placement.RejectionReason)
			}
			if len(contract.Placement.MissingAdapters) > 0 {
				detail += fmt.Sprintf(" (missing adapters: %s)", strings.Join(contract.Placement.MissingAdapters, ", "))
			}
		}
		rejected = append(rejected, detail)
	}
	if len(rejected) > 0 {
		return fmt.Errorf("%s", strings.Join(rejected, "; "))
	}
	return nil
}

// resolveApplication maps use case declarations to module enables.
func (e *CompositionEngine) resolveApplication(enabled map[string]bool, result *CompositionResult) {
	if e.stackkit == nil || e.stackkit.Application == nil {
		return
	}

	for ucName, ucDef := range e.stackkit.Application {
		// Check if this use case is enabled in the spec
		if !e.isApplicationEnabled(ucName) {
			continue
		}

		runtimeProfile, profile, profileKnown := e.applicationRuntimeProfile(ucName, ucDef, result)

		// Resolve the selected tool/provider before profile handling so local
		// module enablement and Control Plane handoff evidence agree.
		tool := ucDef.DefaultTool
		if tool == "" {
			continue
		}
		if alt := e.getApplicationTool(ucName); alt != "" {
			if containsString(ucDef.Alternatives, alt) {
				tool = alt
			} else {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("use case %q: alternative %q not available, using default %q", ucName, alt, tool))
			}
		}

		// Managed, hybrid, and external application packages are valid
		// selections, but their concrete realization is outside the local OSS
		// resolver. Do not silently fall back to a local Compose/PaaS app.
		if profileKnown && runtimeProfileNeedsHandoff(profile) {
			result.ControlPlaneHandoffs = append(result.ControlPlaneHandoffs, ControlPlaneHandoff{
				UseCase:              ucName,
				Tool:                 tool,
				RuntimeProfile:       runtimeProfile,
				Realization:          strings.ToLower(strings.TrimSpace(profile.Realization)),
				Reason:               runtimeProfileHandoffReason(profile),
				ProductMCP:           nativeProductMCPEndpoint(ucDef),
				RequiresControlPlane: profile.RequiresControlPlane,
				RequiresLocalBridge:  profile.RequiresLocalBridge,
				PlacementModes:       append([]string(nil), profile.PlacementModes...),
				Contexts:             append([]string(nil), profile.Contexts...),
			})
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("use case %q: runtime profile %q requires runtime handoff; local module deployment skipped", ucName, runtimeProfile))
			continue
		}

		if _, ok := e.contracts[tool]; ok {
			enabled[tool] = true
		} else {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("use case %q: module %q not found in contracts", ucName, tool))
		}
	}
}

// resolveAddons enables addon modules declared in the spec.
func (e *CompositionEngine) resolveAddons(enabled map[string]bool, result *CompositionResult) {
	for _, addon := range e.spec.Addons {
		mc, ok := e.contracts[addon]
		if !ok {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("addon %q: module not found in contracts", addon))
			continue
		}

		// Check resource tier compatibility
		if mc.Requires != nil && mc.Requires.Infrastructure.MinMemory != "" {
			tier := e.spec.Compute.Tier
			if tier == "" {
				tier = models.ComputeTierStandard
			}
			// Simple tier check: if module needs more than "low" and we're "low", warn
			if tier == models.ComputeTierLow && mc.Requires.Infrastructure.MinMemory != "" {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("addon %q: requires minMemory=%s, compute tier is %q",
						addon, mc.Requires.Infrastructure.MinMemory, tier))
			}
		}

		enabled[addon] = true
	}
}

// addPlatformDefaults adds always-enabled platform modules.
func (e *CompositionEngine) addPlatformDefaults(enabled map[string]bool) {
	// L1-foundation modules that are always needed.
	platformDefaults := []string{"traefik", "socket-proxy", "pocketid"}
	for _, name := range platformDefaults {
		if _, ok := e.contracts[name]; ok {
			enabled[name] = true
		}
	}

	// Identity modules: TinyAuth provides the default working gateway user.
	if mc, ok := e.contracts["tinyauth"]; ok {
		for _, svc := range mc.Services {
			if svc.Required {
				enabled["tinyauth"] = true
				break
			}
		}
	}

	// PocketID is the default passkey identity provider. A StackKit without an
	// identity provider cannot deliver the expected passwordless login path.
}

// applyServiceOverrides applies explicit enable/disable from spec.Services.
func (e *CompositionEngine) applyServiceOverrides(enabled map[string]bool) error {
	if e.spec.Services == nil {
		return nil
	}
	disabled := make(map[string]bool)

	for svcName, svcConfig := range e.spec.Services {
		svcMap, ok := svcConfig.(map[string]any)
		if !ok {
			continue
		}
		enabledVal, ok := svcMap["enabled"]
		if !ok {
			continue
		}
		v, ok := enabledVal.(bool)
		if !ok {
			continue
		}
		if _, exists := e.contracts[svcName]; exists {
			if v {
				enabled[svcName] = true
			} else {
				disabled[svcName] = true
			}
		}
	}

	for svcName := range disabled {
		if svcName == "pocketid" && !e.hasEnabledPocketBasePasskeyOIDCProvider(enabled) {
			return fmt.Errorf("pocketid cannot be disabled: PocketBase is not enabled as a passkey-capable OIDC identity provider")
		}
		delete(enabled, svcName)
	}

	return nil
}

// expandDependencies adds all transitive dependencies of enabled modules.
func (e *CompositionEngine) expandDependencies(enabled map[string]bool, result *CompositionResult) error {
	// Iteratively expand until stable
	for {
		added := false
		for name := range enabled {
			mc, ok := e.contracts[name]
			if !ok || mc.Requires == nil {
				continue
			}
			for dep, reqSvc := range mc.Requires.Services {
				if enabled[dep] {
					continue
				}
				if reqSvc.Optional {
					continue
				}
				if _, ok := e.contracts[dep]; !ok {
					return fmt.Errorf("module %q requires %q which does not exist", name, dep)
				}
				enabled[dep] = true
				added = true
			}
		}
		if !added {
			break
		}
	}

	return nil
}

// sortModules topologically sorts enabled modules.
func (e *CompositionEngine) sortModules(enabled map[string]bool) ([]string, error) {
	var contracts []cueval.ModuleContract
	for name := range enabled {
		if mc, ok := e.contracts[name]; ok {
			contracts = append(contracts, *mc)
		}
	}

	graph := BuildGraph(contracts)
	if errs := graph.Validate(); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, err := range errs {
			msgs[i] = err.Error()
		}
		return nil, fmt.Errorf("validation errors: %s", strings.Join(msgs, "; "))
	}

	return graph.TopologicalSort()
}

// propagateSettings merges perma + flexible settings with context overrides.
func (e *CompositionEngine) propagateSettings(result *CompositionResult) {
	ctx := models.NodeContext(e.spec.Context)

	for _, name := range result.EnabledModules {
		mc, ok := e.contracts[name]
		if !ok || mc.Settings == nil {
			continue
		}

		merged := make(map[string]any)

		// Start with perma settings
		for k, v := range mc.Settings.Perma {
			merged[k] = v
		}

		// Overlay flexible settings (user can change these)
		for k, v := range mc.Settings.Flexible {
			merged[k] = v
		}

		// Apply context override if available
		// Context overrides are stored in the module CUE as contexts: { local: {...} }
		// The ModuleContract doesn't currently parse these into explicit structs,
		// but we can apply known patterns here.
		switch name {
		case "tinyauth":
			switch ctx {
			case models.ContextLocal, models.ContextPi:
				merged["secureCookie"] = false
			case models.ContextCloud:
				merged["secureCookie"] = true
			}
		}

		result.ModuleSettings[name] = merged
	}
}

// resolveIdentity builds the identity configuration from enabled modules and spec.
func (e *CompositionEngine) resolveIdentity(result *CompositionResult) error {
	ic := &IdentityConfig{
		AuthMode: "passkeys_plus_legacy",
	}

	// Admin email: the human Owner is canonical. adminEmail/email are kept as
	// compatibility fallbacks and are normalized so downstream apps never get
	// a bare "admin" placeholder.
	ic.AdminEmail = models.ResolveAdminEmail(e.spec)

	// Generate an admin password that satisfies Coolify's root-user bootstrap
	// rules: lower/upper case letters, numbers, and a symbol.
	adminPwd, err := generateAdminPassword(24)
	if err != nil {
		return fmt.Errorf("generate admin password: %w", err)
	}
	ic.AdminPassword = adminPwd

	// Check which identity modules are enabled
	ic.TinyAuthEnabled = containsString(result.EnabledModules, "tinyauth")
	ic.PocketIDEnabled = containsString(result.EnabledModules, "pocketid")

	// Resolve OIDC issuer URL and credentials from the passkey identity
	// provider. PocketID is the default and currently deployed passkey
	// provider. PocketBase may replace it only if the PocketBase module itself
	// explicitly declares passkeys and OIDC provider semantics.
	passkeyProvider := ""
	if ic.PocketIDEnabled {
		passkeyProvider = "pocketid"
	} else if e.enabledPocketBaseIsPasskeyOIDCProvider(result.EnabledModules) {
		passkeyProvider = "pocketbase"
	}

	if passkeyProvider != "" {
		ic.OIDCIssuerURL = e.identityProviderIssuerURL(passkeyProvider)
		if passkeyProvider == "pocketid" {
			ic.PocketIDAppURL = ic.OIDCIssuerURL
		}

		// TinyAuth uses PKCE, so the default PocketID client is public and has
		// a stable, non-secret client ID. The PocketID client itself is created
		// during apply via the static admin API key.
		ic.OIDCClientID = "stackkit-tinyauth"
		ic.OIDCClientSecret = ""

		// PocketID's ENCRYPTION_KEY is provisioned by `stackkit generate` in
		// <homelab>/.stackkit/pocketid-encryption-key (file-based persistence
		// so destroy → re-apply round-trips reuse the same key — rotating it
		// would make existing data on the volume undecryptable). The CLI then
		// writes the value directly into terraform.tfvars.json before fragment
		// rendering. Composition is intentionally not the source of truth here.

		// When a passkey OIDC provider is enabled, TinyAuth should use it as
		// OAuth provider instead of falling back to username/password only.
		ic.TinyAuthOAuthEnabled = ic.TinyAuthEnabled
	}

	// Generate TinyAuth session secret regardless of PocketID
	if ic.TinyAuthEnabled {
		sessionSecret, err := generateRandomHex(32)
		if err != nil {
			return fmt.Errorf("generate TinyAuth session secret: %w", err)
		}
		ic.TinyAuthSessionSecret = sessionSecret
	}

	// Secure cookie from context
	ctx := models.NodeContext(e.spec.Context)
	switch ctx {
	case models.ContextCloud:
		ic.SecureCookie = true
	default:
		ic.SecureCookie = false
	}

	// AuthMode from settings or identity spec
	if settings, ok := result.ModuleSettings["tinyauth"]; ok {
		if mode, ok := settings["authMode"].(string); ok {
			ic.AuthMode = mode
		}
	}
	if e.spec.Identity != nil && e.spec.Identity.AuthMode != "" {
		ic.AuthMode = e.spec.Identity.AuthMode
	}

	result.Identity = ic
	return nil
}

func (e *CompositionEngine) hasEnabledPocketBasePasskeyOIDCProvider(enabled map[string]bool) bool {
	return enabled["pocketbase"] && e.moduleIsPasskeyOIDCProvider("pocketbase")
}

func (e *CompositionEngine) enabledPocketBaseIsPasskeyOIDCProvider(enabledModules []string) bool {
	return containsString(enabledModules, "pocketbase") && e.moduleIsPasskeyOIDCProvider("pocketbase")
}

func (e *CompositionEngine) moduleIsPasskeyOIDCProvider(name string) bool {
	mc, ok := e.contracts[name]
	if !ok || mc.Provides == nil {
		return false
	}
	caps := mc.Provides.Capabilities
	return caps["passkeys"] && caps["oidc-provider"] && caps["identity-provider"]
}

func (e *CompositionEngine) identityProviderIssuerURL(provider string) string {
	domain := e.spec.Domain
	if domain == "" {
		domain = models.DomainHomeLab
	}

	nested, flat := e.identityProviderSubdomains(provider)
	if e.spec.SubdomainPrefix != "" {
		if flat == "" {
			flat = nested
		}
		if flat == "" {
			flat = provider
		}
		return fmt.Sprintf("%s://%s-%s.%s", identityProviderURLScheme(domain), e.spec.SubdomainPrefix, flat, domain)
	}

	if nested == "" {
		nested = provider
	}
	return fmt.Sprintf("%s://%s.%s", identityProviderURLScheme(domain), nested, domain)
}

func identityProviderURLScheme(domain string) string {
	if models.IsLocalhostDomain(domain) {
		return "http"
	}
	return "https"
}

func (e *CompositionEngine) identityProviderSubdomains(provider string) (nested string, flat string) {
	if provider == "pocketid" {
		return "id", "id"
	}
	mc, ok := e.contracts[provider]
	if !ok {
		return "", ""
	}
	for _, svc := range mc.Services {
		if svc.SubdomainNested != "" || svc.SubdomainFlat != "" {
			return svc.SubdomainNested, svc.SubdomainFlat
		}
	}
	return "", ""
}

// isApplicationEnabled checks if a use case is enabled in the spec.
func (e *CompositionEngine) isApplicationEnabled(ucName string) bool {
	ucDef, ok := e.stackkit.Application[ucName]
	if !ok {
		return false
	}

	if appMap, ok := e.applicationConfig(ucName); ok {
		if enabled, ok := boolConfigValue(appMap, "enabled"); ok {
			return enabled
		}
	}

	if svcMap, ok := e.serviceConfig(ucName); ok {
		if enabled, ok := boolConfigValue(svcMap, "enabled"); ok {
			return enabled
		}
	}

	if ucDef.Role != models.RoleDefault {
		return false
	}

	// Default use cases are enabled for standard+ tiers.
	tier := e.spec.Compute.Tier
	if tier == "" {
		tier = models.ComputeTierStandard
	}
	return tier != models.ComputeTierLow
}

// getApplicationTool checks if the user specified an alternative tool.
func (e *CompositionEngine) getApplicationTool(ucName string) string {
	if appMap, ok := e.applicationConfig(ucName); ok {
		if tool, ok := stringConfigValue(appMap, "tool"); ok {
			return tool
		}
	}
	if svcMap, ok := e.serviceConfig(ucName); ok {
		if tool, ok := stringConfigValue(svcMap, "tool"); ok {
			return tool
		}
	}
	return ""
}

func (e *CompositionEngine) applicationRuntimeProfile(ucName string, ucDef models.ApplicationDef, result *CompositionResult) (string, models.ApplicationRuntimeProfileDef, bool) {
	profileName := ""
	if appMap, ok := e.applicationConfig(ucName); ok {
		if selected, ok := stringConfigValue(appMap, "runtimeProfile"); ok {
			profileName = selected
		} else if selected, ok := stringConfigValue(appMap, "runtime_profile"); ok {
			profileName = selected
		}
	}
	if profileName == "" {
		profileName = strings.TrimSpace(ucDef.DefaultRuntimeProfile)
	}
	if profileName == "" {
		return "", models.ApplicationRuntimeProfileDef{}, false
	}
	profile, ok := ucDef.RuntimeProfiles[profileName]
	if !ok {
		if len(ucDef.RuntimeProfiles) > 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("use case %q: runtime profile %q is not declared", ucName, profileName))
		}
		return profileName, models.ApplicationRuntimeProfileDef{}, false
	}
	return profileName, profile, true
}

func runtimeProfileNeedsHandoff(profile models.ApplicationRuntimeProfileDef) bool {
	realization := strings.ToLower(strings.TrimSpace(profile.Realization))
	return profile.RequiresControlPlane ||
		profile.RequiresLocalBridge ||
		realization == "control-plane" ||
		realization == "hybrid" ||
		realization == "external"
}

func runtimeProfileHandoffReason(profile models.ApplicationRuntimeProfileDef) string {
	realization := strings.ToLower(strings.TrimSpace(profile.Realization))
	switch {
	case profile.RequiresControlPlane || realization == "control-plane":
		return "runtime profile requires Kombify Control Plane realization"
	case realization == "hybrid":
		return "runtime profile requires hybrid local plus Control Plane realization"
	case realization == "external":
		return "runtime profile is external; existing service connection required"
	case profile.RequiresLocalBridge:
		return "runtime profile requires a local bridge managed outside local module deployment"
	default:
		return "runtime profile requires runtime handoff"
	}
}

func nativeProductMCPEndpoint(ucDef models.ApplicationDef) string {
	for _, connector := range ucDef.Connectors {
		if connector.NativeProduct || connector.Kind == "home-assistant-native" {
			return connector.Endpoint
		}
	}
	return ""
}

func (e *CompositionEngine) applicationConfig(ucName string) (map[string]any, bool) {
	if e.spec == nil {
		return nil, false
	}
	return configMapForKey(e.spec.Application, ucName)
}

func (e *CompositionEngine) serviceConfig(ucName string) (map[string]any, bool) {
	if e.spec == nil {
		return nil, false
	}
	return configMapForKey(e.spec.Services, ucName)
}

func configMapForKey(values map[string]any, key string) (map[string]any, bool) {
	if len(values) == 0 {
		return nil, false
	}
	normalizedKey := normalizeConfigKey(key)
	for candidate, raw := range values {
		if normalizeConfigKey(candidate) != normalizedKey {
			continue
		}
		switch value := raw.(type) {
		case map[string]any:
			return value, true
		case map[any]any:
			converted := make(map[string]any, len(value))
			for k, v := range value {
				if s, ok := k.(string); ok {
					converted[s] = v
				}
			}
			return converted, true
		}
	}
	return nil, false
}

func boolConfigValue(values map[string]any, key string) (bool, bool) {
	for candidate, raw := range values {
		if normalizeConfigKey(candidate) != normalizeConfigKey(key) {
			continue
		}
		value, ok := raw.(bool)
		return value, ok
	}
	return false, false
}

func stringConfigValue(values map[string]any, key string) (string, bool) {
	for candidate, raw := range values {
		if normalizeConfigKey(candidate) != normalizeConfigKey(key) {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return "", false
		}
		value = strings.TrimSpace(value)
		return value, value != ""
	}
	return "", false
}

func normalizeConfigKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// generateRandomHex returns a cryptographically random hex string of the given byte length.
// E.g., generateRandomHex(16) returns a 32-character hex string.
func generateRandomHex(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func generateAdminPassword(length int) (string, error) {
	if length < 12 {
		length = 12
	}
	classes := []string{
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"abcdefghijklmnopqrstuvwxyz",
		"0123456789",
		"!@#%^&*()-_=+",
	}
	all := strings.Join(classes, "")
	out := make([]byte, 0, length)
	for _, class := range classes {
		ch, err := randomChar(class)
		if err != nil {
			return "", err
		}
		out = append(out, ch)
	}
	for len(out) < length {
		ch, err := randomChar(all)
		if err != nil {
			return "", err
		}
		out = append(out, ch)
	}
	for i := len(out) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", fmt.Errorf("crypto/rand.Int failed: %w", err)
		}
		j := int(jBig.Int64())
		out[i], out[j] = out[j], out[i]
	}
	return string(out), nil
}

func randomChar(charset string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
	if err != nil {
		return 0, fmt.Errorf("crypto/rand.Int failed: %w", err)
	}
	return charset[n.Int64()], nil
}
