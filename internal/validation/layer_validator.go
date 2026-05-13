// Package validation provides 3-layer architecture validation for StackKits.
package validation

import (
	"fmt"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

// Layer represents the validation layer (1, 2, or 3)
type Layer int

const (
	LayerFoundation   Layer = 1
	LayerPlatform     Layer = 2
	LayerApplications Layer = 3
)

// String returns the string representation of a layer
func (l Layer) String() string {
	switch l {
	case LayerFoundation:
		return "Foundation"
	case LayerPlatform:
		return "Platform"
	case LayerApplications:
		return "Applications"
	default:
		return fmt.Sprintf("Layer%d", l)
	}
}

// LayerError represents a layer validation error
type LayerError struct {
	Layer   Layer  `json:"layer"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Error implements the error interface
func (e *LayerError) Error() string {
	return fmt.Sprintf("ERROR: %s", e.Message)
}

// LayerValidationResult contains validation results for a single layer
type LayerValidationResult struct {
	Layer    Layer        `json:"layer"`
	Valid    bool         `json:"valid"`
	Errors   []LayerError `json:"errors,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
}

// StackKitValidationResult contains complete validation results
type StackKitValidationResult struct {
	Valid     bool                   `json:"valid"`
	StackKit  string                 `json:"stackkit"`
	Layer1    *LayerValidationResult `json:"layer1"`
	Layer2    *LayerValidationResult `json:"layer2"`
	Layer3    *LayerValidationResult `json:"layer3"`
	AllErrors []LayerError           `json:"allErrors,omitempty"`
}

// LayerValidator performs 3-layer architecture validation
type LayerValidator struct {
	ctx       *cue.Context
	baseDir   string
	schemaDir string
}

// NewLayerValidator creates a new layer validator
func NewLayerValidator(baseDir string) *LayerValidator {
	return &LayerValidator{
		ctx:       cuecontext.New(),
		baseDir:   baseDir,
		schemaDir: filepath.Join(baseDir, "base"),
	}
}

// ValidateStackKit performs complete 3-layer validation
func (v *LayerValidator) ValidateStackKit(stackkitDir string) (*StackKitValidationResult, error) {
	result := &StackKitValidationResult{
		StackKit: filepath.Base(stackkitDir),
		Valid:    true,
	}

	// Validate each layer
	result.Layer1 = v.validateLayer1(stackkitDir)
	result.Layer2 = v.validateLayer2(stackkitDir)
	result.Layer3 = v.validateLayer3(stackkitDir)

	// Combine all errors
	result.AllErrors = append(result.AllErrors, result.Layer1.Errors...)
	result.AllErrors = append(result.AllErrors, result.Layer2.Errors...)
	result.AllErrors = append(result.AllErrors, result.Layer3.Errors...)

	// Overall validity
	result.Valid = result.Layer1.Valid && result.Layer2.Valid && result.Layer3.Valid

	return result, nil
}

// validateLayer1 validates Layer 1 (Foundation) requirements
func (v *LayerValidator) validateLayer1(stackkitDir string) *LayerValidationResult {
	result := &LayerValidationResult{
		Layer: LayerFoundation,
		Valid: true,
	}

	// Load CUE files
	value, err := v.loadCUEValue(stackkitDir)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerFoundation,
			Code:    "L1_LOAD_ERROR",
			Message: fmt.Sprintf("Failed to load CUE: %v", err),
		})
		return result
	}

	// Check system configuration
	if system := value.LookupPath(cue.ParsePath("system")); !system.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerFoundation,
			Code:    "L1_MISSING_SYSTEM",
			Message: "Layer 1 foundation incomplete - missing required field: system",
			Field:   "system",
			Hint:    "Add system configuration block with timezone, locale, etc.",
		})
	}

	// Check packages
	if packages := value.LookupPath(cue.ParsePath("packages")); !packages.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerFoundation,
			Code:    "L1_MISSING_PACKAGES",
			Message: "Layer 1 foundation incomplete - missing required field: packages",
			Field:   "packages",
			Hint:    "Add packages block with core and extra packages",
		})
	}

	// Check SSH security
	if security := value.LookupPath(cue.ParsePath("security.ssh")); !security.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerFoundation,
			Code:    "L1_MISSING_SSH",
			Message: "Layer 1 foundation incomplete - missing required field: security.ssh",
			Field:   "security.ssh",
			Hint:    "Add security.ssh block with port, permitRootLogin, passwordAuth settings",
		})
	}

	// Check firewall
	if firewall := value.LookupPath(cue.ParsePath("security.firewall")); !firewall.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerFoundation,
			Code:    "L1_MISSING_FIREWALL",
			Message: "Layer 1 foundation incomplete - missing required field: security.firewall",
			Field:   "security.firewall",
			Hint:    "Add security.firewall block with enabled, backend, rules",
		})
	}

	return result
}

// validateLayer2 validates Layer 2 (Platform) requirements
func (v *LayerValidator) validateLayer2(stackkitDir string) *LayerValidationResult {
	result := &LayerValidationResult{
		Layer: LayerPlatform,
		Valid: true,
	}

	value, err := v.loadCUEValue(stackkitDir)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerPlatform,
			Code:    "L2_LOAD_ERROR",
			Message: fmt.Sprintf("Failed to load CUE: %v", err),
		})
		return result
	}

	// Check platform type is explicitly declared
	platform := value.LookupPath(cue.ParsePath("platform"))
	if !platform.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerPlatform,
			Code:    "L2_MISSING_PLATFORM",
			Message: "ERROR: Layer 2 platform not declared - must specify platform: 'docker' | 'docker-swarm' | 'bare-metal' in stackkit.yaml",
			Field:   "platform",
			Hint:    "Add platform field with value: docker | docker-swarm | bare-metal",
		})
	} else {
		// Validate platform type value
		platformStr, err := platform.String()
		if err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, LayerError{
				Layer:   LayerPlatform,
				Code:    "L2_INVALID_PLATFORM",
				Message: "Layer 2 platform has invalid type",
				Field:   "platform",
				Hint:    "Platform must be one of: docker, docker-swarm, bare-metal",
			})
		} else {
			validPlatforms := map[string]bool{
				"docker":       true,
				"docker-swarm": true,
				"bare-metal":   true,
			}
			if !validPlatforms[platformStr] {
				result.Valid = false
				result.Errors = append(result.Errors, LayerError{
					Layer:   LayerPlatform,
					Code:    "L2_INVALID_PLATFORM_VALUE",
					Message: fmt.Sprintf("Layer 2 platform '%s' is not valid", platformStr),
					Field:   "platform",
					Hint:    "Platform must be one of: docker, docker-swarm, bare-metal",
				})
			}
		}
	}

	// Check container runtime configuration
	if container := value.LookupPath(cue.ParsePath("container")); !container.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerPlatform,
			Code:    "L2_MISSING_CONTAINER",
			Message: "Layer 2 platform incomplete - missing container runtime configuration",
			Field:   "container",
			Hint:    "Add container block with runtime, version, storage settings",
		})
	}

	// Check network configuration
	if network := value.LookupPath(cue.ParsePath("network.defaults")); !network.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerPlatform,
			Code:    "L2_MISSING_NETWORK",
			Message: "Layer 2 platform incomplete - missing network base configuration",
			Field:   "network.defaults",
			Hint:    "Add network.defaults block with driver, subnet, gateway",
		})
	}

	return result
}

// validateLayer3 validates Layer 3 (Applications) requirements
func (v *LayerValidator) validateLayer3(stackkitDir string) *LayerValidationResult {
	result := &LayerValidationResult{
		Layer: LayerApplications,
		Valid: true,
	}

	value, err := v.loadCUEValue(stackkitDir)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerApplications,
			Code:    "L3_LOAD_ERROR",
			Message: fmt.Sprintf("Failed to load CUE: %v", err),
		})
		return result
	}

	// Check services exist
	services := value.LookupPath(cue.ParsePath("services"))
	if !services.Exists() {
		result.Valid = false
		result.Errors = append(result.Errors, LayerError{
			Layer:   LayerApplications,
			Code:    "L3_MISSING_SERVICES",
			Message: "Layer 3 applications missing - no services defined",
			Field:   "services",
			Hint:    "Add services block with at least one application service",
		})
		return result
	}

	// Check that Layer 3 does NOT contain PAAS/management services
	// PAAS services belong in Layer 2 (Platform), not Layer 3 (Applications)
	paasServices := []string{"dokploy", "coolify"}

	// Iterate through services to find misplaced PAAS services
	iter, err := services.Fields(cue.Concrete(false))
	if err == nil {
		for iter.Next() {
			svcName := iter.Selector().String()
			svcValue := iter.Value()

			isPAAS := false

			// Check if service has type field indicating PAAS
			typeField := svcValue.LookupPath(cue.ParsePath("type"))
			if typeField.Exists() {
				typeStr, _ := typeField.String()
				if typeStr == "paas" {
					isPAAS = true
				}
			}

			// Check if service has role field indicating PAAS
			roleField := svcValue.LookupPath(cue.ParsePath("role"))
			if roleField.Exists() {
				roleStr, _ := roleField.String()
				if roleStr == "paas" {
					isPAAS = true
				}
			}

			// Check if service name matches known PAAS services
			for _, paas := range paasServices {
				if strings.Contains(strings.ToLower(svcName), paas) {
					isPAAS = true
					break
				}
			}

			// Check layer field — services explicitly marked as layer 2 are misplaced
			layerField := svcValue.LookupPath(cue.ParsePath("layer"))
			if layerField.Exists() {
				layerStr, _ := layerField.String()
				if strings.HasPrefix(layerStr, "2") {
					isPAAS = true
				}
			}

			if isPAAS {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("Service '%s' appears to be a PAAS/platform service — it belongs in Layer 2 (Platform), not Layer 3 (Applications). Move it to the paas section.", svcName),
				)
			}
		}
	}

	return result
}

// loadCUEValue loads CUE files from the stackkit directory
func (v *LayerValidator) loadCUEValue(stackkitDir string) (cue.Value, error) {
	cfg := &load.Config{
		Dir: stackkitDir,
	}

	instances := load.Instances([]string{"."}, cfg)
	if len(instances) == 0 {
		return cue.Value{}, fmt.Errorf("no CUE files found")
	}

	inst := instances[0]
	if inst.Err != nil {
		return cue.Value{}, inst.Err
	}

	value := v.ctx.BuildInstance(inst)
	if err := value.Err(); err != nil {
		return cue.Value{}, err
	}

	return value, nil
}
