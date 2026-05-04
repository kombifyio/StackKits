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
	// repo checkout makes CUE treat base-kit as its own module and breaks imports
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

	// Validate nodes if present
	for i, node := range spec.Nodes {
		if node.Name == "" {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    fmt.Sprintf("nodes[%d].name", i),
				Message: "node name is required",
				Code:    "REQUIRED_FIELD",
			})
		}
		if node.IP == "" {
			result.Valid = false
			result.Errors = append(result.Errors, models.ValidationError{
				Path:    fmt.Sprintf("nodes[%d].ip", i),
				Message: "node IP is required",
				Code:    "REQUIRED_FIELD",
			})
		}
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
					Message: "secret references must start with env:, secret:, vault:, or file:",
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
	for _, prefix := range []string{"env:", "secret:", "vault:", "file:"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
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
// doesn't exist. This allows standalone kit directories (e.g. ~/.stackkits/base-kit/)
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
