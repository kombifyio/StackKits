package usecasecatalog

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

func attachPackageComputeTiers(source *sourceCatalog) error {
	for index, useCase := range source.UseCases {
		pkg := source.Packages[useCase.ID]
		if pkg == nil {
			continue
		}
		fits, err := decodePackageComputeTiers(pkg, useCase.ID)
		if err != nil {
			return err
		}
		source.UseCases[index].ComputeTiers = fits
	}
	return nil
}

func attachAuthoringVocabulary(source *sourceCatalog, workloads, modules []map[string]any) error {
	profiles := map[string][]string{}
	projected := map[string]AuthoringModule{}
	for _, module := range modules {
		id := metadataID(module)
		if id == "" {
			continue
		}
		keys := computeProfileIDs(module)
		if len(keys) == 0 {
			continue
		}
		profiles[id] = keys
		authoringModule, err := authoringModuleFromCatalog(id, keys, module)
		if err != nil {
			return err
		}
		projected[id] = authoringModule
	}
	source.moduleProfiles = profiles
	for index, useCase := range source.UseCases {
		workload := source.Workloads[useCase.ID]
		if workload == nil {
			annotateAlternativeRealization(&source.UseCases[index], AuthoringWorkload{})
			continue
		}
		authoring, err := authoringWorkloadFromCatalog(workload, projected)
		if err != nil {
			return err
		}
		annotateAlternativeRealization(&source.UseCases[index], authoring)
		source.UseCases[index].WorkloadRef = authoring.ID
		source.UseCases[index].DefaultAlternative = authoring.DefaultAlternative
		source.UseCases[index].Alternatives = authoring.Alternatives
	}
	for index, useCase := range source.UseCases {
		addOns := make([]AuthoringWorkload, 0, len(source.AddOnWorkloads[useCase.ID]))
		for _, workload := range source.AddOnWorkloads[useCase.ID] {
			authoring, err := authoringWorkloadFromCatalog(workload, projected)
			if err != nil {
				return err
			}
			addOns = append(addOns, authoring)
		}
		sort.Slice(addOns, func(i, j int) bool { return addOns[i].ID < addOns[j].ID })
		source.UseCases[index].AddOns = addOns
		if err := validateSettingWorkloads(source.UseCases[index], workloads); err != nil {
			return err
		}
		if err := validateSettingAcceleratorProfiles(source.UseCases[index], modules); err != nil {
			return err
		}
		if err := validateCapabilityWorkloads(source.UseCases[index], workloads); err != nil {
			return err
		}
	}
	var kitCores []AuthoringWorkload
	for _, workload := range workloads {
		if stringField(workload, "kind") != "service" {
			continue
		}
		authoring, err := authoringWorkloadFromCatalog(workload, projected)
		if err != nil {
			return err
		}
		kitCores = append(kitCores, authoring)
	}
	sort.Slice(kitCores, func(i, j int) bool { return kitCores[i].ID < kitCores[j].ID })
	source.KitCores = kitCores
	return nil
}

// annotateAlternativeRealization marks each alternative component with what
// the pinned release does with it. An alternative the Architecture v2 workload
// admits installs; any other alternative is a recorded choice (owner direction
// 2026-09-25: every real alternative is published, and consumers show what the
// release installs today instead of hiding the choice).
func annotateAlternativeRealization(useCase *UseCase, authoring AuthoringWorkload) {
	admitted := make(map[string]bool, len(authoring.Alternatives))
	for _, alternative := range authoring.Alternatives {
		admitted[alternative.ID] = true
	}
	for index, component := range useCase.Components {
		if component.Role != "alternative" {
			continue
		}
		useCase.Components[index].Realization = "recorded"
		if admitted[component.ID] {
			useCase.Components[index].Realization = "install"
		}
	}
}

func authoringWorkloadFromCatalog(workload map[string]any, modules map[string]AuthoringModule) (AuthoringWorkload, error) {
	id := metadataID(workload)
	defaultAlternative := stringField(workload, "defaultAlternative")
	rawAlternatives, _ := workload["alternatives"].([]any)
	if defaultAlternative == "" || len(rawAlternatives) == 0 {
		return AuthoringWorkload{}, fmt.Errorf("workload %s omits v2alpha2 alternatives", id)
	}
	seen := map[string]bool{}
	authoring := AuthoringWorkload{ID: id, DefaultAlternative: defaultAlternative}
	for _, raw := range rawAlternatives {
		alternative, _ := raw.(map[string]any)
		alternativeID := stringField(alternative, "id")
		moduleRef := stringField(alternative, "moduleRef")
		if alternativeID == "" || moduleRef == "" {
			return AuthoringWorkload{}, fmt.Errorf("workload %s has an alternative without id and moduleRef", id)
		}
		if seen[alternativeID] {
			return AuthoringWorkload{}, fmt.Errorf("workload %s declares alternative %q twice", id, alternativeID)
		}
		seen[alternativeID] = true
		module, ok := modules[moduleRef]
		if !ok || len(module.ComputeProfiles) == 0 {
			return AuthoringWorkload{}, fmt.Errorf("workload %s alternative %s references module %s without compute profiles", id, alternativeID, moduleRef)
		}
		module.ComputeProfiles = append([]string(nil), module.ComputeProfiles...)
		authoring.Alternatives = append(authoring.Alternatives, AuthoringAlternative{
			ID: alternativeID, Name: alternativeID,
			Configuration:             configurationInputsFromAlternative(alternative),
			Modules:                   []AuthoringModule{module},
			ProvidesRuntimeAdapterRef: stringField(alternative, "providesRuntimeAdapterRef"),
		})
	}
	if !seen[defaultAlternative] {
		return AuthoringWorkload{}, fmt.Errorf("workload %s default alternative %s is not in alternatives", id, defaultAlternative)
	}
	sort.Slice(authoring.Alternatives, func(i, j int) bool { return authoring.Alternatives[i].ID < authoring.Alternatives[j].ID })
	return authoring, nil
}

// authoringModuleFromCatalog projects a module's declared compute and
// accelerator profile facts. It copies declared values only; an axis the
// module does not declare stays absent.
func authoringModuleFromCatalog(id string, keys []string, module map[string]any) (AuthoringModule, error) {
	projected := AuthoringModule{ID: id, ComputeProfiles: keys, StorageProfiles: configurationProfileIDs(module, "storageProfiles"), AcceleratorProfiles: configurationProfileIDs(module, "acceleratorProfiles")}
	var declared struct {
		ComputeProfiles     map[string]ComputeProfileFacts `json:"computeProfiles"`
		AcceleratorProfiles map[string]struct {
			Maturity    string          `json:"maturity"`
			Reservation *ResourceBudget `json:"reservation"`
			Accelerator struct {
				Vendor         string   `json:"vendor"`
				Access         string   `json:"access"`
				MinVramGiB     *float64 `json:"minVramGiB"`
				MinDriverMajor *float64 `json:"minDriverMajor"`
			} `json:"accelerator"`
		} `json:"acceleratorProfiles"`
	}
	raw, err := json.Marshal(map[string]any{"computeProfiles": module["computeProfiles"], "acceleratorProfiles": module["acceleratorProfiles"]})
	if err != nil {
		return AuthoringModule{}, err
	}
	if err := json.Unmarshal(raw, &declared); err != nil {
		return AuthoringModule{}, fmt.Errorf("module %s profile facts: %w", id, err)
	}
	projected.ComputeProfileFacts = map[string]ComputeProfileFacts{}
	for _, key := range keys {
		facts := declared.ComputeProfiles[key]
		if floor := facts.HostFloor; floor != nil && floor.MinCPUCores == nil && floor.MinRAMGB == nil && floor.MinStorageGB == nil && floor.MinAMD64MicroarchitectureLevel == nil && len(floor.AllowedArchitectures) == 0 {
			facts.HostFloor = nil
		}
		projected.ComputeProfileFacts[key] = facts
	}
	for key, profile := range declared.AcceleratorProfiles {
		if projected.AcceleratorProfileFacts == nil {
			projected.AcceleratorProfileFacts = map[string]AcceleratorProfileFacts{}
		}
		projected.AcceleratorProfileFacts[key] = AcceleratorProfileFacts{
			Vendor: profile.Accelerator.Vendor, Access: profile.Accelerator.Access,
			MinVramGiB: profile.Accelerator.MinVramGiB, MinDriverMajor: profile.Accelerator.MinDriverMajor,
			Maturity: profile.Maturity, Reservation: profile.Reservation,
		}
	}
	return projected, nil
}

func computeProfileIDs(module map[string]any) []string {
	raw, _ := module["computeProfiles"].(map[string]any)
	if len(raw) == 0 {
		return nil
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func decodePackageComputeTiers(pkg map[string]any, useCaseID string) (map[string]UseCaseComputeTierFit, error) {
	raw, _ := pkg["computeTiers"].(map[string]any)
	if raw == nil {
		return nil, fmt.Errorf("use case %s package omits computeTiers", useCaseID)
	}
	fits := map[string]UseCaseComputeTierFit{}
	for _, tier := range []string{"low", "standard", "high"} {
		entry, _ := raw[tier].(map[string]any)
		if entry == nil {
			return nil, fmt.Errorf("use case %s omits computeTiers.%s", useCaseID, tier)
		}
		fit := UseCaseComputeTierFit{Included: boolField(entry, "included"), ModuleSlug: stringField(entry, "moduleSlug"), Reason: stringField(entry, "reason")}
		if functions, ok := entry["functions"].([]any); ok {
			for _, value := range functions {
				if token, ok := value.(string); ok && strings.TrimSpace(token) != "" {
					fit.Functions = append(fit.Functions, token)
				}
			}
		}
		if notes, ok := entry["notes"].([]any); ok {
			for _, value := range notes {
				if note, ok := value.(string); ok && strings.TrimSpace(note) != "" {
					fit.Notes = append(fit.Notes, note)
				}
			}
		}
		if load, ok := entry["load"].(map[string]any); ok {
			fit.Load = &UseCaseLoad{
				Residency: stringField(load, "residency"),
				Baseline:  stringField(load, "baseline"),
				Burst:     stringField(load, "burst"),
			}
		}
		fits[tier] = fit
	}
	return fits, nil
}

// validateSettingWorkloads keeps the install promise of a setting honest: a
// setting that names a workload must be an installing toggle, and the named
// workload must be an application workload of the compiled catalog.
func validateSettingWorkloads(useCase UseCase, workloads []map[string]any) error {
	applications := map[string]bool{}
	for _, workload := range workloads {
		if stringField(workload, "kind") == "application" {
			applications[metadataID(workload)] = true
		}
	}
	for _, setting := range useCase.Settings {
		if setting.WorkloadRef == "" {
			continue
		}
		if setting.Kind != "toggle" || setting.Realization != "install" {
			return fmt.Errorf("use case %s setting %s selects workload %s but is not an installing toggle", useCase.ID, setting.ID, setting.WorkloadRef)
		}
		if !applications[setting.WorkloadRef] {
			return fmt.Errorf("use case %s setting %s selects unknown application workload %s", useCase.ID, setting.ID, setting.WorkloadRef)
		}
	}
	return nil
}

// validateSettingAcceleratorProfiles keeps an accelerator choice installable:
// every non-default option must be an accelerator profile of the named module,
// and the default option must not be one, because it selects no profile.
func validateSettingAcceleratorProfiles(useCase UseCase, modules []map[string]any) error {
	for _, setting := range useCase.Settings {
		if setting.AcceleratorProfileOf == "" {
			continue
		}
		var profiles map[string]any
		for _, module := range modules {
			if metadataID(module) == setting.AcceleratorProfileOf {
				profiles, _ = module["acceleratorProfiles"].(map[string]any)
			}
		}
		if len(profiles) == 0 {
			return fmt.Errorf("use case %s setting %s selects accelerator profiles of %s, which declares none", useCase.ID, setting.ID, setting.AcceleratorProfileOf)
		}
		for _, option := range setting.Options {
			_, declared := profiles[option.ID]
			if isDefault := option.ID == setting.Default; isDefault == declared {
				return fmt.Errorf("use case %s setting %s option %s must name an accelerator profile of %s unless it is the default", useCase.ID, setting.ID, option.ID, setting.AcceleratorProfileOf)
			}
		}
	}
	return nil
}

// validateCapabilityWorkloads closes installing capability options over the
// Architecture v2 catalog: each names an application workload of the same use
// case and one of its alternatives. The enabledByDefault composition must
// resolve to the use case's own workload with its default alternative, so a
// selection that names no capability keeps its exact StackSpec.
func validateCapabilityWorkloads(useCase UseCase, workloads []map[string]any) error {
	if len(useCase.Capabilities) == 0 {
		return nil
	}
	byID := map[string]map[string]any{}
	for _, workload := range workloads {
		if stringField(workload, "kind") == "application" {
			byID[metadataID(workload)] = workload
		}
	}
	for _, capability := range useCase.Capabilities {
		for _, option := range capability.Options() {
			if option.Realization != "install" {
				continue
			}
			workload := byID[option.WorkloadRef]
			if workload == nil || stringField(workload, "useCaseRef") != useCase.ID {
				return fmt.Errorf("use case %s capability %s option %s names %s, which is not an application workload of this use case", useCase.ID, capability.ID, option.ID, option.WorkloadRef)
			}
			declared := map[string]bool{}
			alternatives, _ := workload["alternatives"].([]any)
			for _, raw := range alternatives {
				alternative, _ := raw.(map[string]any)
				declared[stringField(alternative, "id")] = true
			}
			for _, alternativeRef := range append([]string{option.AlternativeRef}, option.AlternativeRefs...) {
				if !declared[alternativeRef] {
					return fmt.Errorf("use case %s capability %s option %s names alternative %s, which workload %s does not declare", useCase.ID, capability.ID, option.ID, alternativeRef, option.WorkloadRef)
				}
			}
		}
		if !capability.EnabledByDefault {
			continue
		}
		primary := byID[useCase.ID]
		if capability.Default.Realization != "install" || primary == nil || capability.Default.WorkloadRef != useCase.ID || capability.Default.AlternativeRef != stringField(primary, "defaultAlternative") {
			return fmt.Errorf("use case %s capability %s is enabled by default but its default does not install the use case's default workload alternative", useCase.ID, capability.ID)
		}
	}
	return nil
}

// validateBundles checks the derived bundle facts CUE cannot express without
// cross-list joins: a bundle is required exactly when it holds a required
// module and on by default exactly when it holds a module that is on by
// default, and a default bundle selects exactly those modules, so turning the
// default bundles on reproduces the previous default composition.
func validateBundles(useCase string, capabilities []Capability, bundles []Bundle) error {
	byID := make(map[string]Capability, len(capabilities))
	for _, capability := range capabilities {
		byID[capability.ID] = capability
	}
	placed := map[string]string{}
	for _, bundle := range bundles {
		var required, enabled bool
		var enabledMembers []string
		for _, ref := range bundle.Capabilities {
			capability, ok := byID[ref]
			if !ok {
				return fmt.Errorf("use case %s bundle %s names unknown capability %s", useCase, bundle.ID, ref)
			}
			placed[ref] = bundle.ID
			required = required || capability.Required
			if capability.EnabledByDefault {
				enabled = true
				enabledMembers = append(enabledMembers, ref)
			}
		}
		for _, ref := range bundle.Defaults {
			if !slices.Contains(bundle.Capabilities, ref) {
				return fmt.Errorf("use case %s bundle %s default %s is not one of its capabilities", useCase, bundle.ID, ref)
			}
		}
		if bundle.Required != required {
			return fmt.Errorf("use case %s bundle %s required=%t, but its modules make it required=%t", useCase, bundle.ID, bundle.Required, required)
		}
		if bundle.EnabledByDefault != enabled {
			return fmt.Errorf("use case %s bundle %s enabledByDefault=%t, but its modules make it %t", useCase, bundle.ID, bundle.EnabledByDefault, enabled)
		}
		if enabled {
			defaults := slices.Sorted(slices.Values(bundle.Defaults))
			slices.Sort(enabledMembers)
			if !slices.Equal(defaults, enabledMembers) {
				return fmt.Errorf("use case %s default bundle %s selects %v, but its modules on by default are %v", useCase, bundle.ID, defaults, enabledMembers)
			}
		}
	}
	// A module in no bundle is not offered by setup, so the required and
	// default modules must each sit in a bundle.
	if len(bundles) > 0 {
		for _, capability := range capabilities {
			if _, ok := placed[capability.ID]; !ok && (capability.Required || capability.EnabledByDefault) {
				return fmt.Errorf("use case %s capability %s is required or on by default but in no bundle", useCase, capability.ID)
			}
		}
	}
	return nil
}
