package usecasecatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

func DiscoverRepoRoot(start string) (string, error) {
	dir := start
	for i := 0; i < 8; i++ {
		if fileExists(filepath.Join(dir, "foundation", "use_case.cue")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("stackkits use-case contracts are not in %s", start)
}

func loadSource(root string, release ReleaseIdentity) (sourceCatalog, error) {
	var registry struct {
		Entries map[string]struct {
			Slug          string               `json:"slug"`
			DisplayName   string               `json:"displayName"`
			Description   string               `json:"description"`
			Components    map[string]Component `json:"components"`
			NotApplicable map[string]struct {
				Reason string `json:"reason"`
			} `json:"notApplicable"`
			Settings     []Setting    `json:"settings"`
			Capabilities []Capability `json:"capabilities"`
			Bundles      []Bundle     `json:"bundles"`
			Docs         string       `json:"docs"`
			MainUseCase  MainUseCase  `json:"mainUseCase"`
		} `json:"entries"`
	}
	if err := loadCUE(root, "foundation", "UseCaseCatalog", &registry); err != nil {
		return sourceCatalog{}, err
	}
	var architecture struct {
		Workloads             []map[string]any `json:"workloads"`
		ApplicationLifecycles []map[string]any `json:"applicationLifecycles"`
		Modules               []map[string]any `json:"modules"`
	}
	if err := loadCUE(root, "foundation", "ArchitectureV2Catalog", &architecture); err != nil {
		return sourceCatalog{}, err
	}
	result := sourceCatalog{Packages: map[string]map[string]any{}, Workloads: map[string]map[string]any{}, AddOnWorkloads: map[string][]map[string]any{}, Lifecycles: map[string]map[string]any{}, Modules: map[string]bool{}, NotApplicable: map[string]map[string]string{}, RuntimeEvidence: map[string]string{}}
	for key, entry := range registry.Entries {
		if key != entry.Slug {
			return sourceCatalog{}, fmt.Errorf("catalog key %q differs from slug %q", key, entry.Slug)
		}
		components := make([]Component, 0, len(entry.Components))
		for componentKey, component := range entry.Components {
			if componentKey != component.ID {
				return sourceCatalog{}, fmt.Errorf("use case %s component key %q differs from id %q", key, componentKey, component.ID)
			}
			components = append(components, component)
		}
		sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
		settings := append([]Setting(nil), entry.Settings...)
		seenSettings := map[string]bool{}
		for _, setting := range settings {
			if seenSettings[setting.ID] {
				return sourceCatalog{}, fmt.Errorf("use case %s declares setting %q twice", key, setting.ID)
			}
			seenSettings[setting.ID] = true
		}
		if err := validateBundles(key, entry.Capabilities, entry.Bundles); err != nil {
			return sourceCatalog{}, err
		}
		result.UseCases = append(result.UseCases, UseCase{ID: key, Title: entry.DisplayName, Description: entry.Description, Components: components, MainUseCase: entry.MainUseCase, Settings: settings, Capabilities: entry.Capabilities, Bundles: entry.Bundles, Docs: entry.Docs})
		if len(entry.NotApplicable) > 0 {
			result.NotApplicable[key] = map[string]string{}
			for gateID, exception := range entry.NotApplicable {
				if strings.TrimSpace(exception.Reason) == "" {
					return sourceCatalog{}, fmt.Errorf("use case %s notApplicable %s has no reason", key, gateID)
				}
				result.NotApplicable[key][gateID] = exception.Reason
			}
		}
	}
	sort.Slice(result.UseCases, func(i, j int) bool { return result.UseCases[i].ID < result.UseCases[j].ID })
	result.catalogWorkloads = architecture.Workloads
	result.catalogModules = architecture.Modules
	for _, workload := range architecture.Workloads {
		if stringField(workload, "kind") != "application" {
			continue
		}
		ref := stringField(workload, "useCaseRef")
		id := metadataID(workload)
		if _, ok := registry.Entries[ref]; !ok {
			return sourceCatalog{}, fmt.Errorf("workload %q references unknown use case %q", id, ref)
		}
		// The workload named after its use case is the primary; any other
		// application workload of that use case is an add-on the owner
		// selects in addition (for example a music server next to media).
		if id != ref {
			result.AddOnWorkloads[ref] = append(result.AddOnWorkloads[ref], workload)
			continue
		}
		if _, duplicate := result.Workloads[ref]; duplicate {
			return sourceCatalog{}, fmt.Errorf("multiple application workloads reference use case %q", ref)
		}
		result.Workloads[ref] = workload
	}
	for _, lifecycle := range architecture.ApplicationLifecycles {
		ref := stringField(lifecycle, "useCaseRef")
		if _, ok := registry.Entries[ref]; !ok {
			return sourceCatalog{}, fmt.Errorf("lifecycle references unknown use case %q", ref)
		}
		workloadRef := stringField(lifecycle, "workloadRef")
		if workloadRef != ref {
			// Add-on lifecycles are closed over their workload by the CUE
			// catalog check; the use-case gate follows the primary workload.
			continue
		}
		workload := result.Workloads[ref]
		if workload == nil || metadataID(workload) != workloadRef {
			return sourceCatalog{}, fmt.Errorf("lifecycle %q does not match use case workload", ref)
		}
		if _, duplicate := result.Lifecycles[ref]; duplicate {
			return sourceCatalog{}, fmt.Errorf("multiple application lifecycles reference use case %q", ref)
		}
		result.Lifecycles[ref] = lifecycle
	}
	useCasesRoot := filepath.Join(root, "use-cases")
	if directories, err := os.ReadDir(useCasesRoot); err == nil {
		for _, directory := range directories {
			if !directory.IsDir() {
				continue
			}
			var pkg map[string]any
			if err := loadCUE(root, filepath.ToSlash(filepath.Join("use-cases", directory.Name())), "Package", &pkg); err != nil {
				return sourceCatalog{}, err
			}
			metadata, _ := pkg["metadata"].(map[string]any)
			ref := stringField(metadata, "useCaseRef")
			if _, ok := registry.Entries[ref]; !ok {
				return sourceCatalog{}, fmt.Errorf("package %q references unknown use case %q", directory.Name(), ref)
			}
			if ref != stringField(metadata, "category") {
				return sourceCatalog{}, fmt.Errorf("package %q category/useCaseRef drift", directory.Name())
			}
			if _, duplicate := result.Packages[ref]; duplicate {
				return sourceCatalog{}, fmt.Errorf("multiple packages reference use case %q", ref)
			}
			result.Packages[ref] = pkg
		}
	}
	if err := attachPackageComputeTiers(&result); err != nil {
		return sourceCatalog{}, err
	}
	modulesRoot := filepath.Join(root, "modules")
	if directories, err := os.ReadDir(modulesRoot); err == nil {
		for _, directory := range directories {
			if !directory.IsDir() || !fileExists(filepath.Join(modulesRoot, directory.Name(), "module.cue")) {
				continue
			}
			// Module directories are the stable module slug namespace; their
			// module.cue contracts are validated by the affected CUE gate.
			result.Modules[directory.Name()] = true
		}
	}
	if err := loadOS(filepath.Join(root, "docs", "data", "os-compat", "latest.json"), release, &result.OS); err != nil {
		return sourceCatalog{}, err
	}
	if err := loadRuntimeEvidence(filepath.Join(root, "docs", "data", "use-case-runtime-evidence", "latest.json"), release.SourceSHA, &result); err != nil {
		return sourceCatalog{}, err
	}
	return result, nil
}

func loadRuntimeEvidence(path, sourceSHA string, target *sourceCatalog) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var receipt RuntimeEvidenceReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return fmt.Errorf("decode use-case runtime evidence: %w", err)
	}
	if receipt.SchemaVersion != "stackkits-use-case-runtime-evidence/v1" {
		return fmt.Errorf("unknown use-case runtime evidence schema %q", receipt.SchemaVersion)
	}
	target.RuntimeEvidencePresent = true
	if receipt.SourceSHA != sourceSHA {
		target.RuntimeEvidenceSourceMismatch = true
		return nil
	}
	known := map[string]bool{}
	for _, useCase := range target.UseCases {
		known[useCase.ID] = true
	}
	previous := ""
	for _, row := range receipt.UseCases {
		if row.UseCaseRef <= previous {
			return fmt.Errorf("runtime evidence use cases must be sorted and unique")
		}
		previous = row.UseCaseRef
		if !known[row.UseCaseRef] {
			return fmt.Errorf("runtime evidence references unknown use case %q", row.UseCaseRef)
		}
		if row.Status != "passed" || !strings.HasPrefix(row.EvidenceRef, "https://") {
			return fmt.Errorf("runtime evidence for %s is not passed with public evidence", row.UseCaseRef)
		}
		target.RuntimeEvidence[row.UseCaseRef] = row.EvidenceRef
	}
	return nil
}

func loadCUE(root, directory, expression string, target any) error {
	instances := load.Instances([]string{"./" + filepath.ToSlash(directory)}, &load.Config{Dir: root, ModuleRoot: root})
	if len(instances) != 1 || instances[0].Err != nil {
		if len(instances) == 1 {
			return fmt.Errorf("load %s.%s: %w", directory, expression, instances[0].Err)
		}
		return fmt.Errorf("load %s.%s: got %d instances", directory, expression, len(instances))
	}
	value := cuecontext.New().BuildInstance(instances[0]).LookupPath(cueapi.ParsePath(expression))
	if err := value.Validate(cueapi.Concrete(true)); err != nil {
		return fmt.Errorf("%s.%s is not concrete: %w", directory, expression, err)
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s.%s: %w", directory, expression, err)
	}
	return nil
}

func loadOS(path string, release ReleaseIdentity, target *[]OSCompatibility) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var source struct {
		SchemaVersion    int    `json:"schemaVersion"`
		StackKitsVersion string `json:"stackkitsVersion"`
		Results          []struct {
			OS            struct{ Family, Distribution, Version string } `json:"os"`
			Architectures []string                                       `json:"architectures"`
			Grade         string                                         `json:"grade"`
			ReasonCodes   []string                                       `json:"reasonCodes"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return err
	}
	// Receipts for a release are projected only after that release is
	// published, so a manifest emitted from the tag's own tree normally sees
	// the previous release's evidence. Positive grades count only when the
	// projection is bound to this exact release; otherwise they are pending.
	boundToRelease := source.SchemaVersion >= 3 && source.StackKitsVersion == release.Tag
	for _, row := range source.Results {
		status := row.Grade
		reasons := row.ReasonCodes
		id := row.OS.Distribution + "-" + row.OS.Version
		evidenceRef := ""
		switch status {
		case "supported", "preview":
			if boundToRelease {
				evidenceRef = "https://stackkit.cc/compatibility"
			} else {
				status, reasons = "unverified", []string{"current-release-receipt-pending"}
			}
		case "unverified":
		case "unsupported":
			if len(reasons) == 0 {
				return fmt.Errorf("OS %s is unsupported without a policy reason", id)
			}
		default:
			return fmt.Errorf("OS %s has invalid status %q", id, status)
		}
		architecture := strings.Join(row.Architectures, "/")
		if architecture == "" {
			architecture = "untested"
		}
		*target = append(*target, OSCompatibility{ID: id, Name: title(row.OS.Distribution), Version: row.OS.Version, Architecture: architecture, Status: status, Reason: strings.Join(reasons, ", "), EvidenceRef: evidenceRef})
	}
	sort.Slice(*target, func(i, j int) bool { return (*target)[i].ID < (*target)[j].ID })
	return nil
}

func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }

func title(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
