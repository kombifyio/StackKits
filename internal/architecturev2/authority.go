package architecturev2

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

type authorityProfile struct {
	Slug    stackspecmigration.KitProfile `json:"slug"`
	Package string                        `json:"package"`
}

type cueAuthority struct {
	moduleRoot      string
	contractSources map[string][]byte
	definitions     map[stackspecmigration.KitProfile]resolvedplan.KitDefinition
	catalog         resolvedplan.Catalog
	planAuthority   resolvedplan.PlanAuthority
}

func loadCUEAuthority(moduleRoot string) (*cueAuthority, error) {
	absoluteRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve module root: %w", err)
	}
	authority := &cueAuthority{
		moduleRoot:    filepath.Clean(absoluteRoot),
		planAuthority: resolvedplan.DevelopmentPlanAuthority(),
	}
	profiles, err := authority.loadProfiles()
	if err != nil {
		return nil, fmt.Errorf("load base.ArchitectureV2AuthorityProfiles: %w", err)
	}
	authority.definitions = make(map[stackspecmigration.KitProfile]resolvedplan.KitDefinition, len(profiles))

	catalogDocument, err := authority.loadObject("base", "ArchitectureV2Catalog")
	if err != nil {
		return nil, fmt.Errorf("load base.ArchitectureV2Catalog: %w", err)
	}
	authority.catalog, err = decodeCatalog(catalogDocument)
	if err != nil {
		return nil, fmt.Errorf("decode base.ArchitectureV2Catalog: %w", err)
	}
	for _, profile := range profiles {
		document, err := authority.loadObject(profile.Package, "Definition")
		if err != nil {
			return nil, fmt.Errorf("load %s.Definition: %w", profile.Slug, err)
		}
		definition := resolvedplan.KitDefinition(document)
		metadata, ok := document["metadata"].(map[string]any)
		slug, slugOK := metadata["slug"].(string)
		if !ok || !slugOK || slug != string(profile.Slug) {
			return nil, fmt.Errorf("%s.Definition exports metadata.slug %q", profile.Slug, metadata["slug"])
		}
		authority.definitions[profile.Slug] = definition
	}
	return authority, nil
}

// loadCUEContractFixtureAuthority loads only the non-product fixture package,
// its distinct Definition, and its catalog. Product service startup never
// parses this package and therefore cannot be broken by fixture drift.
func loadCUEContractFixtureAuthority(moduleRoot string) (*cueAuthority, error) {
	absoluteRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve module root: %w", err)
	}
	authority := &cueAuthority{
		moduleRoot:    filepath.Clean(absoluteRoot),
		definitions:   make(map[stackspecmigration.KitProfile]resolvedplan.KitDefinition, 1),
		planAuthority: resolvedplan.ContractFixturePlanAuthority(),
	}
	definitionDocument, err := authority.loadObject("architecture/v2/contractfixture", "ContractFixtureDefinition")
	if err != nil {
		return nil, fmt.Errorf("load contractfixture.ContractFixtureDefinition: %w", err)
	}
	if err := validateContractFixtureDefinition(definitionDocument); err != nil {
		return nil, err
	}
	authority.definitions[stackspecmigration.KitProfileBasement] = resolvedplan.KitDefinition(definitionDocument)
	catalogDocument, err := authority.loadObject("architecture/v2/contractfixture", "ArchitectureV2ContractFixtureCatalog")
	if err != nil {
		return nil, fmt.Errorf("load contractfixture.ArchitectureV2ContractFixtureCatalog: %w", err)
	}
	authority.catalog, err = decodeCatalog(catalogDocument)
	if err != nil {
		return nil, fmt.Errorf("decode contractfixture.ArchitectureV2ContractFixtureCatalog: %w", err)
	}
	return authority, nil
}

func validateContractFixtureDefinition(document map[string]any) error {
	metadata, ok := document["metadata"].(map[string]any)
	if !ok {
		return fmt.Errorf("contractfixture.ContractFixtureDefinition has no metadata object")
	}
	slug, ok := metadata["slug"].(string)
	if !ok || slug != string(stackspecmigration.KitProfileBasement) {
		return fmt.Errorf("contractfixture.ContractFixtureDefinition exports metadata.slug %q, want %q", metadata["slug"], stackspecmigration.KitProfileBasement)
	}
	return nil
}

func (a *cueAuthority) loadProfiles() ([]authorityProfile, error) {
	data, err := a.loadJSON("base", "ArchitectureV2AuthorityProfiles")
	if err != nil {
		return nil, err
	}
	var profiles []authorityProfile
	decoder := json.NewDecoder(bytesReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profiles); err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("authority profile set is empty")
	}
	seen := make(map[stackspecmigration.KitProfile]struct{}, len(profiles))
	for index, profile := range profiles {
		if strings.TrimSpace(string(profile.Slug)) == "" || strings.TrimSpace(profile.Package) == "" {
			return nil, fmt.Errorf("authority profile %d requires slug and package", index)
		}
		if filepath.Base(profile.Package) != profile.Package || profile.Package == "." || profile.Package == ".." {
			return nil, fmt.Errorf("authority profile %s has unsafe package directory %q", profile.Slug, profile.Package)
		}
		if _, duplicate := seen[profile.Slug]; duplicate {
			return nil, fmt.Errorf("authority profile %s is duplicated", profile.Slug)
		}
		seen[profile.Slug] = struct{}{}
	}
	return profiles, nil
}

func (a *cueAuthority) loadObject(directory, expression string) (map[string]any, error) {
	data, err := a.loadJSON(directory, expression)
	if err != nil {
		return nil, err
	}
	return resolvedplan.DecodeDocument[map[string]any](data)
}

func (a *cueAuthority) loadJSON(directory, expression string) ([]byte, error) {
	instances := load.Instances([]string{"./" + filepath.ToSlash(directory)}, &load.Config{
		Dir:        a.moduleRoot,
		ModuleRoot: a.moduleRoot,
	})
	if len(instances) != 1 {
		return nil, fmt.Errorf("CUE loader returned %d instances, want 1", len(instances))
	}
	if instances[0].Err != nil {
		return nil, instances[0].Err
	}
	root := cuecontext.New().BuildInstance(instances[0])
	if err := root.Err(); err != nil {
		return nil, err
	}
	value := root.LookupPath(cueapi.ParsePath(expression))
	if err := value.Validate(cueapi.Concrete(true)); err != nil {
		return nil, fmt.Errorf("%s is not a concrete contract: %w", expression, err)
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("export %s: %w", expression, err)
	}
	return data, nil
}

func decodeCatalog(document map[string]any) (resolvedplan.Catalog, error) {
	data, err := json.Marshal(document)
	if err != nil {
		return resolvedplan.Catalog{}, err
	}
	decoder := json.NewDecoder(bytesReader(data))
	decoder.UseNumber()
	var wire struct {
		Capabilities                 []resolvedplan.CapabilityContract          `json:"capabilities"`
		Providers                    []resolvedplan.CapabilityProvider          `json:"providers"`
		AddOns                       []resolvedplan.AddOnContract               `json:"addons"`
		Modules                      []resolvedplan.ModuleContract              `json:"modules"`
		Workloads                    []resolvedplan.WorkloadContract            `json:"workloads"`
		PrivilegedInterfaceApprovals []resolvedplan.PrivilegedInterfaceApproval `json:"privilegedInterfaceApprovals"`
		RILActionPrimitives          []resolvedplan.RILActionPrimitiveContract  `json:"rilActionPrimitives"`
		PlanArtifacts                []resolvedplan.PlanArtifactContract        `json:"planArtifacts"`
	}
	if err := decoder.Decode(&wire); err != nil {
		return resolvedplan.Catalog{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return resolvedplan.Catalog{}, fmt.Errorf("catalog contains multiple JSON values")
		}
		return resolvedplan.Catalog{}, fmt.Errorf("invalid trailing catalog data: %w", err)
	}
	return resolvedplan.Catalog{
		Capabilities:                 wire.Capabilities,
		Providers:                    wire.Providers,
		AddOns:                       wire.AddOns,
		Modules:                      wire.Modules,
		Workloads:                    wire.Workloads,
		PrivilegedInterfaceApprovals: wire.PrivilegedInterfaceApprovals,
		RILActionPrimitives:          wire.RILActionPrimitives,
		PlanArtifacts:                wire.PlanArtifacts,
	}, nil
}
