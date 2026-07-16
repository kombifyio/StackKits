package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"gopkg.in/yaml.v3"
)

type architectureV2ExecutionMode string

const (
	architectureV2Generate architectureV2ExecutionMode = "generate"
	architectureV2Plan     architectureV2ExecutionMode = "plan"
	architectureV2Apply    architectureV2ExecutionMode = "apply"
)

type architectureV2ExecutionCLIOptions struct {
	inventoryPath string
	planPath      string
	manifestPath  string
	receiptPath   string
}

type architectureV2ExecutionAuthority interface {
	Resolve(architecturev2.ResolveInput) (architecturev2.Result, error)
	VerifyCanonicalPlan([]byte) (generationartifact.VerifiedPlan, error)
	ReadCanonicalPlan(string) (generationartifact.VerifiedPlan, error)
}

type architectureV2ExecutionGate struct {
	newAuthority func() (architectureV2ExecutionAuthority, error)
	versions     generationartifact.ComponentVersions
}

func newArchitectureV2ExecutionGate() architectureV2ExecutionGate {
	componentVersion := architectureV2ComponentVersion(version)
	return architectureV2ExecutionGate{
		newAuthority: func() (architectureV2ExecutionAuthority, error) {
			return architecturev2.NewEmbeddedService(architecturev2.StackKitsV06Contract(version))
		},
		versions: generationartifact.ComponentVersions{
			CLI:       componentVersion,
			Generator: componentVersion,
			Runtime:   componentVersion,
		},
	}
}

// architectureV2ComponentVersion models a development build explicitly as a
// SemVer pre-release. It intentionally remains below the 0.6.0 release
// minimum; tests and release builds provide their actual component version.
func architectureV2ComponentVersion(buildVersion string) string {
	normalized := strings.TrimSpace(buildVersion)
	normalized = strings.TrimPrefix(normalized, "v")
	if normalized == "dev" || normalized == "" {
		return "0.6.0-dev"
	}
	return normalized
}

// preflight returns handled=false only for the existing StackSpec v1
// compatibility path. A document that explicitly claims a non-v1 apiVersion
// never falls through to the legacy loader, generator, or IaC executor.
func (g architectureV2ExecutionGate) preflight(wd, requestedSpecPath string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions) (bool, error) {
	rawSpec, isV2, err := classifyArchitectureV2ExecutionSpec(wd, requestedSpecPath)
	if err != nil || !isV2 {
		return isV2, err
	}
	return true, g.preflightV2(wd, rawSpec, mode, options)
}

func classifyArchitectureV2ExecutionSpec(wd, requestedSpecPath string) ([]byte, bool, error) {
	loader := config.NewLoader(wd)
	specPath, _, _, err := loader.ResolveStackSpecPathForRead(requestedSpecPath)
	if err != nil {
		return nil, false, nil // Preserve the legacy loader's existing diagnostic.
	}
	rawSpec, err := os.ReadFile(specPath)
	if err != nil {
		return nil, false, nil // Missing/default/fetched v1 paths remain unchanged.
	}

	document, readErr := stackspecmigration.Read(rawSpec)
	if readErr != nil {
		if claimsNonLegacyAPIVersion(rawSpec) {
			return nil, true, fmt.Errorf("architecture v2 execution classification: %w", readErr)
		}
		return nil, false, nil
	}
	if document.Version == stackspecmigration.SourceVersionV1 {
		return nil, false, nil
	}
	if document.Version != stackspecmigration.SourceVersionV2Alpha1 || document.V2 == nil {
		return nil, true, fmt.Errorf("architecture v2 execution classification returned no canonical v2 identity")
	}
	return rawSpec, true, nil
}

func (g architectureV2ExecutionGate) preflightV2(wd string, rawSpec []byte, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions) error {
	inventory, err := readArchitectureV2Inventory(wd, options.inventoryPath)
	if err != nil {
		return err
	}
	if g.newAuthority == nil {
		return fmt.Errorf("architecture v2 execution authority is not configured")
	}
	authority, err := g.newAuthority()
	if err != nil {
		return err
	}
	resolved, err := authority.Resolve(architecturev2.ResolveInput{StackSpec: rawSpec, Inventory: inventory})
	if err != nil {
		return err
	}
	current, err := authority.VerifyCanonicalPlan(resolved.CanonicalPlan)
	if err != nil {
		return err
	}
	defaultPlanPath, defaultManifestPath, defaultReceiptPath := current.MetadataPaths(wd)
	planPath := architectureV2MetadataPath(wd, options.planPath, defaultPlanPath)
	persisted, err := authority.ReadCanonicalPlan(planPath)
	if err != nil {
		return err
	}
	if err := persisted.VerifyCurrentResolution(resolved.CanonicalPlan); err != nil {
		return err
	}
	if err := persisted.VerifyCompatibility(g.versions); err != nil {
		return err
	}
	phase := architectureV2ReadinessPhase(mode)
	if err := persisted.RequireReady(phase); err != nil {
		return err
	}
	return g.continueV2Execution(wd, mode, options, persisted, resolved.CanonicalPlan, defaultManifestPath, defaultReceiptPath)
}

func architectureV2ReadinessPhase(mode architectureV2ExecutionMode) generationartifact.ExecutionPhase {
	if mode == architectureV2Apply {
		return generationartifact.ExecutionPhaseApply
	}
	return generationartifact.ExecutionPhaseGeneration
}

func (g architectureV2ExecutionGate) continueV2Execution(wd string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions, persisted generationartifact.VerifiedPlan, currentCanonical []byte, defaultManifestPath, defaultReceiptPath string) error {
	switch mode {
	case architectureV2Generate:
		return generationartifact.RendererNotImplemented(persisted.Binding().Renderer)
	case architectureV2Plan, architectureV2Apply:
		return g.verifyV2Generation(wd, mode, options, persisted, currentCanonical, defaultManifestPath, defaultReceiptPath)
	default:
		return fmt.Errorf("unsupported architecture v2 execution mode %q", mode)
	}
}

func (g architectureV2ExecutionGate) verifyV2Generation(wd string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions, persisted generationartifact.VerifiedPlan, currentCanonical []byte, defaultManifestPath, defaultReceiptPath string) error {
	manifestPath, err := architectureV2CanonicalMetadataPath(wd, options.manifestPath, defaultManifestPath, "artifact manifest")
	if err != nil {
		return err
	}
	receiptPath, err := architectureV2CanonicalMetadataPath(wd, options.receiptPath, defaultReceiptPath, "generation receipt")
	if err != nil {
		return err
	}
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return err
	}
	if err := generationartifact.VerifyExecution(generationartifact.ExecutionGateInput{
		CurrentCanonical: currentCanonical,
		Plan:             persisted,
		Phase:            architectureV2ReadinessPhase(mode),
		Versions:         g.versions,
		Root:             wd,
		Manifest:         manifest,
		Receipt:          receipt,
	}); err != nil {
		return err
	}
	return generationartifact.ExecutorNotImplemented(persisted.Binding().Renderer)
}

func architectureV2MetadataPath(wd, explicit, derived string) string {
	if strings.TrimSpace(explicit) == "" {
		return filepath.Clean(derived)
	}
	return resolvePathFromWorkDir(wd, explicit)
}

func architectureV2CanonicalMetadataPath(wd, explicit, derived, label string) (string, error) {
	canonical := filepath.Clean(derived)
	if strings.TrimSpace(explicit) == "" {
		return canonical, nil
	}
	requested := filepath.Clean(resolvePathFromWorkDir(wd, explicit))
	canonicalAbsolute, err := filepath.Abs(canonical)
	if err != nil {
		return "", fmt.Errorf("resolve canonical architecture v2 %s path %s: %w", label, canonical, err)
	}
	requestedAbsolute, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolve requested architecture v2 %s path %s: %w", label, requested, err)
	}
	pathsEqual := canonicalAbsolute == requestedAbsolute
	if runtime.GOOS == "windows" {
		pathsEqual = strings.EqualFold(canonicalAbsolute, requestedAbsolute)
	}
	if !pathsEqual {
		return "", fmt.Errorf("architecture v2 %s override must resolve to canonical governed path %s", label, canonical)
	}
	return canonical, nil
}

func readArchitectureV2Inventory(wd, explicit string) ([]byte, error) {
	if strings.TrimSpace(explicit) != "" {
		path := resolvePathFromWorkDir(wd, explicit)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read architecture v2 Inventory %s: %w", path, err)
		}
		return data, nil
	}

	candidates := []string{
		filepath.Join(wd, ".stackkit", "inventory.yaml"),
		filepath.Join(wd, ".stackkit", "inventory.json"),
		filepath.Join(wd, "inventory.yaml"),
		filepath.Join(wd, "inventory.json"),
	}
	var selected []string
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			selected = append(selected, candidate)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect architecture v2 Inventory candidate %s: %w", candidate, err)
		}
	}
	if len(selected) > 1 {
		return nil, fmt.Errorf("architecture v2 Inventory is ambiguous; choose exactly one with --inventory: %s", strings.Join(selected, ", "))
	}
	if len(selected) == 0 {
		return nil, nil
	}
	data, err := os.ReadFile(selected[0])
	if err != nil {
		return nil, fmt.Errorf("read architecture v2 Inventory %s: %w", selected[0], err)
	}
	return data, nil
}

func claimsNonLegacyAPIVersion(data []byte) bool {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return false
	}
	mapping := root.Content[0]
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != "apiVersion" || mapping.Content[index+1].Kind != yaml.ScalarNode {
			continue
		}
		value := strings.TrimSpace(mapping.Content[index+1].Value)
		if value != "" && value != stackspecmigration.APIVersionV1 {
			return true
		}
	}
	return false
}
