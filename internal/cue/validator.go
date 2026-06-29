// Package cue provides CUE schema validation for StackKits.
package cue

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
)

const (
	networkModePublic = "public"
	appKindSvelteKit  = "sveltekit"
	routeAuthLogin    = "login-gateway"
)

// Validator handles CUE schema validation
type Validator struct {
	ctx       *cue.Context
	baseDir   string
	schemaDir string
}

// NewValidator creates a new CUE validator
func NewValidator(baseDir string) *Validator {
	return &Validator{
		ctx:       cuecontext.New(),
		baseDir:   baseDir,
		schemaDir: filepath.Join(baseDir, "base"),
	}
}

// ValidateStackKit validates a StackKit against CUE schemas
func (v *Validator) ValidateStackKit(stackkitDir string) (*models.ValidationResult, error) {
	result := &models.ValidationResult{Valid: true}

	if _, err := os.Stat(stackkitDir); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    stackkitDir,
			Message: fmt.Sprintf("stackkit directory not found: %v", err),
			Code:    "STACKKIT_DIR_ERROR",
		})
		return result, nil
	}

	// Prefer an existing workspace/root cue.mod. Creating a nested cue.mod in a
	// repo checkout makes CUE treat basement-kit as its own module and breaks imports
	// such as github.com/kombifyio/stackkits/base.
	if _, ok := resolveCueModuleRoot(v.baseDir, stackkitDir); !ok {
		if err := ensureCueModule(stackkitDir); err != nil {
			result.Warnings = append(result.Warnings, models.ValidationError{
				Path:    stackkitDir,
				Message: fmt.Sprintf("could not set up CUE module: %v", err),
				Code:    "CUE_MODULE_WARNING",
			})
		}
	}

	cfg := cueLoadConfig(stackkitDir, v.baseDir)
	if cfg.ModuleRoot == "" {
		result.Warnings = append(result.Warnings, models.ValidationError{
			Path:    stackkitDir,
			Message: "no CUE module root found; imports may not resolve",
			Code:    "CUE_MODULE_WARNING",
		})
	}

	// Load CUE files from the stackkit directory
	instances := load.Instances([]string{"."}, cfg)
	if len(instances) == 0 {
		return nil, fmt.Errorf("no CUE files found in %s", stackkitDir)
	}

	inst := instances[0]
	if inst.Err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    stackkitDir,
			Message: fmt.Sprintf("failed to load CUE instance: %v", inst.Err),
			Code:    "LOAD_ERROR",
		})
		return result, nil
	}

	// Build the value
	value := v.ctx.BuildInstance(inst)
	if err := value.Err(); err != nil {
		result.Valid = false
		for _, e := range errors.Errors(err) {
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    fmt.Sprintf("%v", errors.Positions(e)),
				Message: e.Error(),
				Code:    "BUILD_ERROR",
			})
		}
		return result, nil
	}

	// Validate the value
	if err := value.Validate(cue.Concrete(true)); err != nil {
		result.Valid = false
		for _, e := range errors.Errors(err) {
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    fmt.Sprintf("%v", errors.Positions(e)),
				Message: e.Error(),
				Code:    "VALIDATION_ERROR",
			})
		}
	}

	return result, nil
}

// ValidateSpec validates a stack-spec against CUE schema
func (v *Validator) ValidateSpec(spec *models.StackSpec) (*models.ValidationResult, error) {
	result := &models.ValidationResult{Valid: true}

	// Basic validation rules
	if spec.Name == "" {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "name",
			Message: "name is required",
			Code:    "REQUIRED_FIELD",
		})
	}

	if !models.IsKnownInstallMode(spec.Mode) {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "mode",
			Message: fmt.Sprintf("invalid install mode %q (use bare, bootstrapped, or advanced)", spec.Mode),
			Code:    "INVALID_VALUE",
		})
	}

	if spec.StackKit == "" {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "stackkit",
			Message: "stackkit is required",
			Code:    "REQUIRED_FIELD",
		})
	}

	// Validate network mode
	validModes := map[string]bool{"local": true, networkModePublic: true, "hybrid": true}
	if spec.Network.Mode != "" && !validModes[spec.Network.Mode] {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "network.mode",
			Message: fmt.Sprintf("invalid network mode '%s', must be one of: local, public, hybrid", spec.Network.Mode),
			Code:    "INVALID_VALUE",
		})
	}

	// Validate context
	validContexts := map[string]bool{"local": true, "cloud": true, "pi": true}
	if spec.Context != "" && !validContexts[spec.Context] {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "context",
			Message: fmt.Sprintf("invalid context '%s', must be one of: local, cloud, pi", spec.Context),
			Code:    "INVALID_VALUE",
		})
	}

	// Validate compute tier
	validTiers := map[string]bool{"low": true, "standard": true, "high": true}
	if spec.Compute.Tier != "" && !validTiers[spec.Compute.Tier] {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "compute.tier",
			Message: fmt.Sprintf("invalid compute tier '%s', must be one of: low, standard, high", spec.Compute.Tier),
			Code:    "INVALID_VALUE",
		})
	}

	if requiresPublicOwnerEmail(spec) && !hasRealOwnerEmail(spec) {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "email",
			Message: "owner/admin email is required for public or managed StackKit configs",
			Code:    "REQUIRED_FIELD",
		})
	}

	validateOwnerConfig(spec.Owner, result)
	validateBreakGlassConfig(spec.BreakGlass, result)
	validateBootstrapConfig(spec.Bootstrap, result)
	validateSetupPolicyMaps(spec, result)
	validatePlatformFallback(spec, result)

	// Normal StackKits must deploy through a supported platform adapter.
	// Dockge is kept only as a constrained experimental compose manager, and
	// "none" would allow platform bypasses.
	if spec.PAAS != "" && !models.IsSupportedPAAS(spec.PAAS) && !platformFallbackEnabled(spec) {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "paas",
			Message: fmt.Sprintf("invalid paas '%s': normal StackKits must use coolify or komodo; dokploy is draft-only; remove paas to use the default coolify resolver", spec.PAAS),
			Code:    "INVALID_VALUE",
		})
	}

	// Validate nodes if present. BaseKit is a single homelab environment with
	// exactly one main-like node and optional worker/storage nodes.
	seenNodeNames := map[string]bool{}
	mainNodeCount := 0
	for i, node := range spec.Nodes {
		nodePath := fmt.Sprintf("nodes[%d]", i)
		if node.Name == "" {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    nodePath + ".name",
				Message: "node name is required",
				Code:    "REQUIRED_FIELD",
			})
		} else if seenNodeNames[node.Name] {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    nodePath + ".name",
				Message: fmt.Sprintf("duplicate node name '%s'", node.Name),
				Code:    "DUPLICATE_NODE_NAME",
			})
		} else {
			seenNodeNames[node.Name] = true
		}
		if node.IP == "" {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    nodePath + ".ip",
				Message: "node IP is required",
				Code:    "REQUIRED_FIELD",
			})
		}
		if !models.IsKnownNodeRole(node.Role) {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    nodePath + ".role",
				Message: fmt.Sprintf("invalid node role '%s', must be one of: main, worker, storage, control-plane, standalone", node.Role),
				Code:    "INVALID_VALUE",
			})
			continue
		}
		if len(spec.Nodes) > 1 && node.Role == "" {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    nodePath + ".role",
				Message: "node role is required for multi-node topologies",
				Code:    "REQUIRED_FIELD",
			})
			continue
		}
		if models.IsMainNodeRole(node.Role) {
			mainNodeCount++
		}
	}

	if len(spec.Nodes) > 1 && mainNodeCount != 1 {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "nodes",
			Message: fmt.Sprintf("multi-node topologies require exactly one main node, found %d", mainNodeCount),
			Code:    "INVALID_VALUE",
		})
	}

	v.validateApps(spec, result)

	// Check for warnings
	if spec.Domain == "" && spec.Network.Mode == networkModePublic {
		result.Warnings = append(result.Warnings, models.ValidationError{
			Path:    "domain",
			Message: "domain is recommended for public network mode",
			Code:    "RECOMMENDED_FIELD",
		})
	}

	if spec.Email == "" && spec.Network.Mode == networkModePublic {
		result.Warnings = append(result.Warnings, models.ValidationError{
			Path:    "email",
			Message: "email is recommended for Let's Encrypt certificates",
			Code:    "RECOMMENDED_FIELD",
		})
	}

	return result, nil
}

func requiresPublicOwnerEmail(spec *models.StackSpec) bool {
	domain := strings.TrimSpace(spec.Domain)
	if domain == "" {
		return false
	}
	if spec.Network.Mode != networkModePublic && spec.Context != string(models.ContextCloud) {
		return false
	}
	return models.IsKombifyMeDomain(domain) || !models.IsLocalDomain(domain)
}

func hasRealOwnerEmail(spec *models.StackSpec) bool {
	if spec.Owner.EffectiveBootstrapMode() == models.OwnerBootstrapModeAuto {
		return true
	}
	for _, candidate := range []string{spec.Owner.Email, spec.AdminEmail, spec.Email} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && strings.Contains(candidate, "@") && !models.NeedsSyntheticAdminEmail(candidate) {
			return true
		}
	}
	return false
}

func validateOwnerConfig(owner models.OwnerConfig, result *models.ValidationResult) {
	if owner.IsZero() {
		return
	}

	mode := owner.EffectiveBootstrapMode()
	if !models.IsKnownOwnerBootstrapMode(mode) {
		addOwnerError(result, "owner.bootstrapMode", fmt.Sprintf("invalid owner bootstrapMode %q (use auto, custom, or none)", owner.BootstrapMode), "INVALID_VALUE")
		return
	}
	if !models.IsKnownOwnerSource(owner.Source) {
		addOwnerError(result, "owner.source", fmt.Sprintf("invalid owner source %q (use local or cloud)", owner.Source), "INVALID_VALUE")
		return
	}

	switch mode {
	case models.OwnerBootstrapModeNone:
		if hasOwnerIdentityFields(owner) || owner.RecoveryPassphraseHash != "" || owner.RecoveryMaterialRef != "" || owner.Source != "" {
			addOwnerError(result, "owner", "owner bootstrapMode none must not include owner identity or recovery material fields", "INVALID_VALUE")
		}
	case models.OwnerBootstrapModeAuto:
		source := strings.ToLower(strings.TrimSpace(owner.Source))
		if source != "" && source != models.OwnerSourceCloud && source != models.OwnerSourceFirstRun {
			addOwnerError(result, "owner.source", "owner bootstrapMode auto must use source cloud or first-run when source is set", "INVALID_VALUE")
		}
		if owner.RecoveryPassphraseHash == "" && owner.RecoveryMaterialRef == "" {
			addOwnerError(result, "owner.recoveryMaterialRef", "owner bootstrapMode auto requires recoveryPassphraseHash or recoveryMaterialRef", "REQUIRED_FIELD")
		}
		if owner.RecoveryPassphraseHash != "" && !strings.HasPrefix(owner.RecoveryPassphraseHash, "$argon2id$") {
			addOwnerError(result, "owner.recoveryPassphraseHash", "owner recoveryPassphraseHash must be an argon2id PHC string", "INVALID_VALUE")
		}
		if owner.RecoveryMaterialRef != "" && !isRecoveryMaterialRef(owner.RecoveryMaterialRef) {
			addOwnerError(result, "owner.recoveryMaterialRef", "owner recoveryMaterialRef must be a secret or TechStack recovery reference", "INVALID_VALUE")
		}
	case models.OwnerBootstrapModeCustom:
		source := strings.ToLower(strings.TrimSpace(owner.Source))
		if source == "" {
			source = models.OwnerSourceLocal
		}
		if source != models.OwnerSourceLocal {
			addOwnerError(result, "owner.source", "owner bootstrapMode custom must use source local", "INVALID_VALUE")
		}
		if strings.TrimSpace(owner.Email) == "" {
			addOwnerError(result, "owner.email", "owner email is required for custom owner bootstrap", "REQUIRED_FIELD")
		}
		if strings.TrimSpace(owner.Username) == "" {
			addOwnerError(result, "owner.username", "owner username is required for custom owner bootstrap", "REQUIRED_FIELD")
		}
		if owner.RecoveryPassphraseHash == "" {
			addOwnerError(result, "owner.recoveryPassphraseHash", "owner recoveryPassphraseHash is required for custom owner bootstrap", "REQUIRED_FIELD")
		} else if !strings.HasPrefix(owner.RecoveryPassphraseHash, "$argon2id$") {
			addOwnerError(result, "owner.recoveryPassphraseHash", "owner recoveryPassphraseHash must be an argon2id PHC string", "INVALID_VALUE")
		}
	case "":
		if !owner.IsZero() {
			addOwnerError(result, "owner.bootstrapMode", "owner bootstrapMode is required when owner fields are set without source", "REQUIRED_FIELD")
		}
	}
}

func validateBreakGlassConfig(breakGlass models.BreakGlassConfig, result *models.ValidationResult) {
	if breakGlass.IsZero() {
		return
	}
	if strings.TrimSpace(breakGlass.Scope) != "" &&
		breakGlass.EffectiveScope() != models.BreakGlassScopeFullEmergencyAdmin {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "breakGlass.scope",
			Message: fmt.Sprintf("invalid breakGlass scope %q (use %q)", breakGlass.Scope, models.BreakGlassScopeFullEmergencyAdmin),
			Code:    "INVALID_VALUE",
		})
	}
}

func validateBootstrapConfig(bootstrap models.BootstrapSpec, result *models.ValidationResult) {
	if !models.IsKnownBootstrapSelector(bootstrap.Mode) {
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "bootstrap.mode",
			Message: fmt.Sprintf("invalid bootstrap mode %q (use full_auto, guided, minimal, or install mode bare, bootstrapped, advanced)", bootstrap.Mode),
			Code:    "INVALID_VALUE",
		})
	}
	if !models.IsKnownSetupPolicy(bootstrap.PlatformPolicy) {
		addSetupPolicyError(result, "bootstrap.platformPolicy", bootstrap.PlatformPolicy)
	}
	if !models.IsKnownSetupPolicy(bootstrap.ApplicationDefaultPolicy) {
		addSetupPolicyError(result, "bootstrap.applicationDefaultPolicy", bootstrap.ApplicationDefaultPolicy)
	}
}

func validateSetupPolicyMaps(spec *models.StackSpec, result *models.ValidationResult) {
	validateSetupPolicyMap("application", spec.Application, result)
	validateSetupPolicyMap("services", spec.Services, result)
}

func validateSetupPolicyMap(section string, values map[string]any, result *models.ValidationResult) {
	for name, raw := range values {
		if policy, ok := setupPolicyFromRaw(raw); ok && !models.IsKnownSetupPolicy(policy) {
			addSetupPolicyError(result, fmt.Sprintf("%s.%s.setup.policy", section, name), policy)
		}
	}
}

func setupPolicyFromRaw(raw any) (string, bool) {
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		return value, value != ""
	case map[string]any:
		return setupPolicyFromMap(value)
	case map[any]any:
		converted := make(map[string]any, len(value))
		for k, v := range value {
			if key, ok := k.(string); ok {
				converted[key] = v
			}
		}
		return setupPolicyFromMap(converted)
	default:
		return "", false
	}
}

func setupPolicyFromMap(values map[string]any) (string, bool) {
	for _, key := range []string{"policy", "setupPolicy"} {
		if policy, ok := values[key].(string); ok && strings.TrimSpace(policy) != "" {
			return policy, true
		}
	}
	if setup, ok := values["setup"]; ok {
		return setupPolicyFromRaw(setup)
	}
	return "", false
}

func addSetupPolicyError(result *models.ValidationResult, path, policy string) {
	result.Valid = false
	result.Errors = append(result.Errors, models.ValidationError{
		Path:    path,
		Message: fmt.Sprintf("setup policy must be 'manual', 'on_demand', or 'automatic' (got %q)", policy),
		Code:    "INVALID_VALUE",
	})
}

func validatePlatformFallback(spec *models.StackSpec, result *models.ValidationResult) {
	if spec == nil {
		return
	}
	mode := strings.TrimSpace(spec.PlatformFallback.Mode)
	if spec.PlatformFallback.Enabled {
		if mode == "" || mode == models.PlatformFallbackStandaloneCompose {
			return
		}
		result.Valid = false
		result.Errors = append(result.Errors, models.ValidationError{
			Path:    "platformFallback.mode",
			Message: fmt.Sprintf("platformFallback.enabled=true requires mode %q", models.PlatformFallbackStandaloneCompose),
			Code:    "INVALID_VALUE",
		})
		return
	}
	if mode == "" || mode == models.PlatformFallbackDisabled {
		return
	}
	result.Valid = false
	result.Errors = append(result.Errors, models.ValidationError{
		Path:    "platformFallback.mode",
		Message: fmt.Sprintf("platformFallback.enabled=false requires mode %q", models.PlatformFallbackDisabled),
		Code:    "INVALID_VALUE",
	})
}

func hasOwnerIdentityFields(owner models.OwnerConfig) bool {
	return strings.TrimSpace(owner.Email) != "" ||
		strings.TrimSpace(owner.Username) != "" ||
		strings.TrimSpace(owner.DisplayName) != "" ||
		strings.TrimSpace(owner.CloudOIDCIssuer) != "" ||
		strings.TrimSpace(owner.CloudOIDCClientID) != "" ||
		strings.TrimSpace(owner.CloudOIDCClientSecretRef) != "" ||
		strings.TrimSpace(owner.CloudOIDCForeignSubject) != ""
}

func addOwnerError(result *models.ValidationResult, path, message, code string) {
	result.Valid = false
	result.Errors = append(result.Errors, models.ValidationError{
		Path:    path,
		Message: message,
		Code:    code,
	})
}

func isRecoveryMaterialRef(value string) bool {
	for _, prefix := range []string{"techstack://", "secret://", "doppler://", "vault://", "env:", "file:"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

func (v *Validator) validateApps(spec *models.StackSpec, result *models.ValidationResult) {
	for name, app := range spec.Apps {
		path := "apps." + name
		if !isDNSLabel(name) {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path,
				Message: "app name must be a DNS-safe label",
				Code:    "INVALID_VALUE",
			})
		}

		kind := app.Kind
		if kind == "" {
			kind = appKindSvelteKit
		}
		if kind != appKindSvelteKit {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path + ".kind",
				Message: "only kind 'sveltekit' is supported for the BaseKit app contract",
				Code:    "INVALID_VALUE",
			})
		}

		if app.Image == "" {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path + ".image",
				Message: "image is required",
				Code:    "REQUIRED_FIELD",
			})
		}

		port := app.Port
		if port == 0 {
			port = 3000
		}
		if port < 1 || port > 65535 {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path + ".port",
				Message: "port must be between 1 and 65535",
				Code:    "INVALID_VALUE",
			})
		}

		healthPath := app.Health.Path
		if healthPath == "" {
			healthPath = "/health"
		}
		if !strings.HasPrefix(healthPath, "/") {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path + ".health.path",
				Message: "health path must start with '/'",
				Code:    "INVALID_VALUE",
			})
		}

		auth := app.Route.Auth
		if auth == "" {
			auth = routeAuthLogin
		}
		if auth != routeAuthLogin && auth != networkModePublic {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path + ".route.auth",
				Message: "route auth must be 'login-gateway' or 'public'",
				Code:    "INVALID_VALUE",
			})
		}
		if app.Route.Host != "" && !isRouteHost(app.Route.Host) {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path + ".route.host",
				Message: "route host must be a DNS hostname without scheme or path",
				Code:    "INVALID_VALUE",
			})
		}
		if app.Setup.Policy != "" && !isSetupPolicy(app.Setup.Policy) {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path + ".setup.policy",
				Message: "setup policy must be 'manual', 'on_demand', or 'automatic'",
				Code:    "INVALID_VALUE",
			})
		}

		for key := range app.Env {
			if !isEnvName(key) {
				result.Valid = false
				result.Errors = append(result.Errors, models.ValidationError{
					Path:    path + ".env." + key,
					Message: "environment variable names must match [A-Za-z_][A-Za-z0-9_]*",
					Code:    "INVALID_VALUE",
				})
			}
		}
		for key, ref := range app.Secrets {
			if !isEnvName(key) {
				result.Valid = false
				result.Errors = append(result.Errors, models.ValidationError{
					Path:    path + ".secrets." + key,
					Message: "secret environment variable names must match [A-Za-z_][A-Za-z0-9_]*",
					Code:    "INVALID_VALUE",
				})
			}
			if !isSecretRef(ref) {
				result.Valid = false
				result.Errors = append(result.Errors, models.ValidationError{
					Path:    path + ".secrets." + key,
					Message: "secret references must start with env:, doppler:, vault:, or file:",
					Code:    "INVALID_VALUE",
				})
			}
		}
	}
}

func isDNSLabel(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	if value[0] < 'a' || value[0] > 'z' {
		return false
	}
	last := value[len(value)-1]
	if last == '-' {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isEnvName(value string) bool {
	if value == "" {
		return false
	}
	first := value[0]
	if !((first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') || first == '_') {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isSecretRef(value string) bool {
	for _, prefix := range []string{"env:", "doppler:", "vault:", "file:"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

func isRouteHost(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/:\\") {
		return false
	}
	value = strings.TrimSuffix(value, ".")
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if !isDNSLabel(label) {
			return false
		}
	}
	return true
}

func isSetupPolicy(value string) bool {
	switch value {
	case platformdeploy.SetupPolicyManual, platformdeploy.SetupPolicyOnDemand, platformdeploy.SetupPolicyAutomatic:
		return true
	default:
		return false
	}
}

// ValidateCUEFile validates a single CUE file
func (v *Validator) ValidateCUEFile(path string) (*models.ValidationResult, error) {
	result := &models.ValidationResult{Valid: true}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read CUE file: %w", err)
	}

	value := v.ctx.CompileBytes(data)
	if err := value.Err(); err != nil {
		result.Valid = false
		for _, e := range errors.Errors(err) {
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path,
				Message: e.Error(),
				Code:    "COMPILE_ERROR",
			})
		}
		return result, nil
	}

	if err := value.Validate(); err != nil {
		result.Valid = false
		for _, e := range errors.Errors(err) {
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    path,
				Message: e.Error(),
				Code:    "VALIDATION_ERROR",
			})
		}
	}

	return result, nil
}

// ensureCueModule creates a cue.mod/module.cue in the given directory if one
// doesn't exist. This allows standalone kit directories (e.g. ~/.stackkits/basement-kit/)
// to resolve CUE imports without requiring manual setup.
func ensureCueModule(dir string) error {
	modDir := filepath.Join(dir, "cue.mod")
	modFile := filepath.Join(modDir, "module.cue")

	if _, err := os.Stat(modFile); err == nil {
		return nil // already exists
	}

	if err := os.MkdirAll(modDir, 0750); err != nil {
		return fmt.Errorf("create cue.mod: %w", err)
	}

	content := `module: "github.com/kombifyio/stackkits"
language: {
	version: "v0.9.0"
}
`
	return os.WriteFile(modFile, []byte(content), 0600)
}

func cueLoadConfig(dir, preferredRoot string) *load.Config {
	cfg := &load.Config{Dir: dir}
	if root, ok := resolveCueModuleRoot(preferredRoot, dir); ok {
		cfg.ModuleRoot = root
	}
	return cfg
}

func resolveCueModuleRoot(preferredRoot, dir string) (string, bool) {
	var candidates []string
	addCandidate := func(path string) {
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		for _, existing := range candidates {
			if existing == abs {
				return
			}
		}
		if hasCueModule(abs) {
			candidates = append(candidates, abs)
		}
	}

	addCandidate(preferredRoot)
	if absDir, err := filepath.Abs(dir); err == nil {
		for cur := filepath.Clean(absDir); ; cur = filepath.Dir(cur) {
			addCandidate(cur)
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
		}
	}

	for _, candidate := range candidates {
		if hasRepoBasePackage(candidate) {
			return candidate, true
		}
	}
	if len(candidates) > 0 {
		return candidates[0], true
	}
	return "", false
}

func hasCueModule(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "cue.mod", "module.cue"))
	return err == nil
}

func hasRepoBasePackage(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "base", "module.cue"))
	return err == nil
}

// GetSchemaDir returns the CUE schema directory
func (v *Validator) GetSchemaDir() string {
	return v.schemaDir
}
