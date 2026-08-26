package architecturev2

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

const (
	initialAuthoringContractVersion  = "1.0.0"
	initialOverrideMetadataName      = "metadata.name"
	initialOverrideNetworkDomainBase = "network.domain.base"
	installComputeTierStandard       = "standard"
	installComputeTierLow            = "low"
	installComputeTierHigh           = "high"
)

// AuthoringOverrides is the deliberately narrow authoring surface for
// a kit's governed initial StackSpec. Adding another field here requires a
// matching Definition.authoring.requiredOverrides contract and an explicit
// materializer implementation; arbitrary paths are never accepted.
type AuthoringOverrides struct {
	Name       string
	DomainBase string
	// Platform selects the workload runtime adapter (e.g. coolify, komodo,
	// standalone-compose) for every workload chosen via UseCases. It is
	// validated against each selected alternative's runtime.allowedAdapterRefs
	// and requires at least one use case.
	Platform string
	// EnableCapabilities is appended to capabilities.enable (optional
	// capability IDs declared by the kit, e.g. lan-dns). The resolver
	// rejects capabilities the kit does not declare.
	EnableCapabilities []string
	// UseCases selects optional kit workloads by ID (e.g. photos, files,
	// vault). Each ID must be declared by the kit's workload policy; the
	// catalog computeTiers entry for ComputeTier supplies the alternative.
	UseCases []string
	// ComputeTier selects a declared kit module graph (low|standard|high).
	// Empty means the CUE default standard. Missing or undeclared graphs fail
	// closed. This is not install.mode, hardware.profile, or legacy context.
	ComputeTier string
	// HardwareProfile writes nodes[0].hardware.profile (standard|pi|gpu|storage).
	// pi is a constrained homelab device class, not Raspberry-only. Empty leaves
	// the CUE default. This is never auto-detected from inventory.
	HardwareProfile string
}

// InitialStackSpecAuthoring exposes only the workflow metadata a CLI or UI
// needs before materialization. The initial spec itself stays encapsulated and
// can only leave this service after CUE revalidation.
type InitialStackSpecAuthoring struct {
	ContractVersion   string
	Status            string
	RequiredOverrides []string
	StandaloneOwner   *StandaloneOwnerAuthoring
}

// StandaloneOwnerAuthoring is the CUE-owned local owner/bootstrap projection
// for a kit that supports account-free standalone initialization.
type StandaloneOwnerAuthoring struct {
	Source               string
	SiteRef              string
	NodeRef              string
	ExecutionChannelRef  string
	IdentityProvider     string
	CertificateAuthority string
	HumanAuthorityRef    string
	HumanIssuerRef       string
	TrustDomainRef       string
}

type stackSpecValidationFunc func([]byte) (StackSpecValidation, error)

// MaterializeInitialStackSpec selects one canonical product Definition,
// clones its authoring.initialSpec, applies only the approved init overrides,
// and revalidates the result through this service's CUE-bound authority.
func (s *Service) MaterializeInitialStackSpec(profile stackspecmigration.KitProfile, overrides AuthoringOverrides) (StackSpecValidation, error) {
	if s == nil || s.authority == nil {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	if !isCanonicalProductKitProfile(profile) {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("kit profile %q is not a canonical product kit", profile), nil)
	}
	definition, exists := s.authority.definitions[profile]
	if !exists {
		return StackSpecValidation{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("no governed Definition exists for %q", profile), nil)
	}
	workloadSelections, err := resolveUseCaseWorkloadSelections(profile, definition, s.authority.catalog, overrides.UseCases, overrides.Platform, overrides.ComputeTier)
	if err != nil {
		return StackSpecValidation{}, err
	}
	return materializeInitialStackSpec(profile, definition, overrides, workloadSelections, s.ValidateStackSpec)
}

// resolveUseCaseWorkloadSelections maps requested use-case IDs onto the kit's
// declared workloads and the catalog alternative for the selected computeTier.
// Omitted fits and unknown IDs fail closed.
func resolveUseCaseWorkloadSelections(
	profile stackspecmigration.KitProfile,
	definition resolvedplan.KitDefinition,
	catalog resolvedplan.Catalog,
	useCases []string,
	platform string,
	computeTier string,
) (map[string]useCaseWorkloadSelection, error) {
	platform = strings.TrimSpace(platform)
	computeTier = strings.TrimSpace(computeTier)
	if computeTier == "" {
		computeTier = installComputeTierStandard
	}
	if len(useCases) == 0 {
		if platform != "" {
			return nil, resolveError(ErrInvalidStackSpec, "--platform selects the runtime adapter for chosen use cases; combine it with --use-case (e.g. --use-case photos --platform komodo)", nil)
		}
		return nil, nil
	}
	declared := map[string]bool{}
	if policy, ok := definition["workloads"].(map[string]any); ok {
		for _, field := range []string{"required", "defaults", "optional"} {
			if list, ok := policy[field].([]any); ok {
				for _, entry := range list {
					if id, ok := entry.(string); ok {
						declared[id] = true
					}
				}
			}
		}
	}
	declaredIDs := make([]string, 0, len(declared))
	for id := range declared {
		declaredIDs = append(declaredIDs, id)
	}
	sort.Strings(declaredIDs)

	selections := make(map[string]useCaseWorkloadSelection, len(useCases))
	for _, useCase := range useCases {
		useCase = strings.TrimSpace(useCase)
		if useCase == "" {
			continue
		}
		if !declared[useCase] {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q is not declared by %s; declared workloads: %s", useCase, profile, strings.Join(declaredIDs, ", ")), nil)
		}
		var contract map[string]any
		for _, candidate := range catalog.Workloads {
			object := map[string]any(candidate)
			metadata, _ := object["metadata"].(map[string]any)
			if metadata == nil {
				continue
			}
			if id, _ := metadata["id"].(string); id == useCase {
				contract = object
				break
			}
		}
		alternativeID := ""
		if contract != nil {
			fit := resolvedplan.CatalogWorkloadComputeTierFit(contract, computeTier)
			if fit.Declared && !fit.Included {
				reason := fit.Reason
				if reason == "" {
					reason = "catalog omits this use case on the selected graph"
				}
				return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q is not included on computeTier %q: %s", useCase, computeTier, reason), nil)
			}
			if fit.Declared && fit.Included && fit.AlternativeID != "" {
				alternativeID = fit.AlternativeID
			} else {
				alternativeID, _ = contract["defaultAlternative"].(string)
			}
		}
		if strings.TrimSpace(alternativeID) == "" {
			return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("workload %q has no governed catalog alternative for computeTier %q", useCase, computeTier), nil)
		}
		if platform != "" {
			allowed := workloadAlternativeAllowedAdapterRefs(contract, alternativeID)
			if !containsStringValue(allowed, platform) {
				return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("platform %q is not an allowed runtime adapter for use case %q (alternative %q); allowed: %s", platform, useCase, alternativeID, strings.Join(allowed, ", ")), nil)
			}
		}
		selections[useCase] = useCaseWorkloadSelection{
			Alternative:        alternativeID,
			RuntimeAdapterRef:  platform,
			RequiredSecretRefs: workloadAlternativeRequiredSecretRefs(contract, alternativeID),
		}
	}
	return selections, nil
}

type useCaseWorkloadSelection struct {
	Alternative        string
	RuntimeAdapterRef  string
	RequiredSecretRefs []string
}

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// workloadAlternativeAllowedAdapterRefs returns runtime.allowedAdapterRefs of
// the selected workload alternative.
func workloadAlternativeAllowedAdapterRefs(contract map[string]any, alternativeID string) []string {
	alternatives, _ := contract["alternatives"].([]any)
	for _, raw := range alternatives {
		alternative, _ := raw.(map[string]any)
		if alternative == nil {
			continue
		}
		if id, _ := alternative["id"].(string); id != alternativeID {
			continue
		}
		runtime, _ := alternative["runtime"].(map[string]any)
		if runtime == nil {
			return nil
		}
		rawRefs, _ := runtime["allowedAdapterRefs"].([]any)
		refs := make([]string, 0, len(rawRefs))
		for _, entry := range rawRefs {
			if ref, ok := entry.(string); ok {
				refs = append(refs, ref)
			}
		}
		return refs
	}
	return nil
}

// workloadAlternativeRequiredSecretRefs returns the secret input refs the
// selected alternative declares as required (alternative.inputs.secretInputs.
// requiredRefs). The initial spec satisfies them with the canonical opaque
// convention secret://workloads/<workload>/<ref>.
func workloadAlternativeRequiredSecretRefs(contract map[string]any, alternativeID string) []string {
	alternatives, _ := contract["alternatives"].([]any)
	for _, raw := range alternatives {
		alternative, _ := raw.(map[string]any)
		if alternative == nil {
			continue
		}
		if id, _ := alternative["id"].(string); id != alternativeID {
			continue
		}
		inputs, _ := alternative["inputs"].(map[string]any)
		if inputs == nil {
			return nil
		}
		secretInputs, _ := inputs["secretInputs"].(map[string]any)
		if secretInputs == nil {
			return nil
		}
		rawRefs, _ := secretInputs["requiredRefs"].([]any)
		refs := make([]string, 0, len(rawRefs))
		for _, entry := range rawRefs {
			if ref, ok := entry.(string); ok && strings.TrimSpace(ref) != "" {
				refs = append(refs, ref)
			}
		}
		return refs
	}
	return nil
}

// InitialStackSpecAuthoringContract returns the CUE-owned authoring status and
// required override paths for one canonical product kit.
func (s *Service) InitialStackSpecAuthoringContract(profile stackspecmigration.KitProfile) (InitialStackSpecAuthoring, error) {
	if s == nil || s.authority == nil {
		return InitialStackSpecAuthoring{}, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	if !isCanonicalProductKitProfile(profile) {
		return InitialStackSpecAuthoring{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("kit profile %q is not a canonical product kit", profile), nil)
	}
	definition, exists := s.authority.definitions[profile]
	if !exists {
		return InitialStackSpecAuthoring{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("no governed Definition exists for %q", profile), nil)
	}
	if err := validateDefinitionProfile(definition, profile); err != nil {
		return InitialStackSpecAuthoring{}, err
	}
	authoring, ok := definition["authoring"].(map[string]any)
	if !ok || authoring == nil {
		return InitialStackSpecAuthoring{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition has no authoring object", profile), nil)
	}
	contractVersion, err := validateInitialAuthoringContractVersion(authoring, profile)
	if err != nil {
		return InitialStackSpecAuthoring{}, err
	}
	status, ok := authoring["initialSpecStatus"].(string)
	if !ok || (status != "supported" && status != "preview") {
		return InitialStackSpecAuthoring{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.initialSpecStatus is %v", profile, authoring["initialSpecStatus"]), nil)
	}
	if initial, ok := authoring["initialSpec"].(map[string]any); !ok || initial == nil {
		return InitialStackSpecAuthoring{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.initialSpec is not an object", profile), nil)
	}
	required, err := decodeRequiredInitialOverrides(authoring, profile)
	if err != nil {
		return InitialStackSpecAuthoring{}, err
	}
	standaloneOwner, err := decodeStandaloneOwnerAuthoring(authoring, profile)
	if err != nil {
		return InitialStackSpecAuthoring{}, err
	}
	return InitialStackSpecAuthoring{
		ContractVersion: contractVersion, Status: status,
		RequiredOverrides: append([]string(nil), required...),
		StandaloneOwner:   standaloneOwner,
	}, nil
}

func decodeStandaloneOwnerAuthoring(authoring map[string]any, profile stackspecmigration.KitProfile) (*StandaloneOwnerAuthoring, error) {
	raw, exists := authoring["standaloneOwner"]
	if !exists {
		return nil, nil
	}
	document, ok := raw.(map[string]any)
	if !ok || document == nil {
		return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.standaloneOwner is not an object", profile), nil)
	}
	field := func(name string) (string, error) {
		value, ok := document[name].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return "", resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.standaloneOwner.%s is not a non-empty string", profile, name), nil)
		}
		return value, nil
	}
	values := make([]string, 9)
	names := []string{
		"source", "siteRef", "nodeRef", "executionChannelRef", "identityProvider",
		"certificateAuthority", "humanAuthorityRef", "humanIssuerRef", "trustDomainRef",
	}
	for index, name := range names {
		value, err := field(name)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	if values[0] != "local" || values[4] != "pocketid" || values[5] != "step-ca" {
		return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition has an unsupported standalone owner implementation", profile), nil)
	}
	return &StandaloneOwnerAuthoring{
		Source: values[0], SiteRef: values[1], NodeRef: values[2], ExecutionChannelRef: values[3],
		IdentityProvider: values[4], CertificateAuthority: values[5],
		HumanAuthorityRef: values[6], HumanIssuerRef: values[7], TrustDomainRef: values[8],
	}, nil
}

func materializeInitialStackSpec(
	profile stackspecmigration.KitProfile,
	definition resolvedplan.KitDefinition,
	overrides AuthoringOverrides,
	workloadSelections map[string]useCaseWorkloadSelection,
	validate stackSpecValidationFunc,
) (StackSpecValidation, error) {
	if !isCanonicalProductKitProfile(profile) {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("kit profile %q is not a canonical product kit", profile), nil)
	}
	if validate == nil {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, "StackSpec validator is not initialized", nil)
	}
	if err := validateDefinitionProfile(definition, profile); err != nil {
		return StackSpecValidation{}, err
	}

	authoring, ok := definition["authoring"].(map[string]any)
	if !ok || authoring == nil {
		return StackSpecValidation{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition has no authoring object", profile), nil)
	}
	if _, err := validateInitialAuthoringContractVersion(authoring, profile); err != nil {
		return StackSpecValidation{}, err
	}
	status, ok := authoring["initialSpecStatus"].(string)
	if !ok || (status != "supported" && status != "preview") {
		return StackSpecValidation{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.initialSpecStatus is %v", profile, authoring["initialSpecStatus"]), nil)
	}
	initial, ok := authoring["initialSpec"].(map[string]any)
	if !ok || initial == nil {
		return StackSpecValidation{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.initialSpec is not an object", profile), nil)
	}
	required, err := decodeRequiredInitialOverrides(authoring, profile)
	if err != nil {
		return StackSpecValidation{}, err
	}
	if err := enforceRequiredInitialOverrides(required, overrides); err != nil {
		return StackSpecValidation{}, err
	}

	canonicalInitial, err := resolvedplan.CanonicalJSON(initial)
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "canonicalize Definition authoring.initialSpec: "+err.Error(), err)
	}
	spec, err := resolvedplan.DecodeDocument[map[string]any](canonicalInitial)
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "clone Definition authoring.initialSpec: "+err.Error(), err)
	}
	initialProfile, err := nestedString(spec, "kit", "slug")
	if err != nil || initialProfile != string(profile) {
		return StackSpecValidation{}, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.initialSpec kit.slug is %q", profile, initialProfile), err)
	}

	if err := applyInstallComputeTier(spec, definition, overrides.ComputeTier); err != nil {
		return StackSpecValidation{}, err
	}
	if err := applyHardwareProfile(spec, overrides.HardwareProfile); err != nil {
		return StackSpecValidation{}, err
	}
	if strings.TrimSpace(overrides.Name) != "" {
		if err := setNestedString(spec, overrides.Name, "metadata", "name"); err != nil {
			return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "apply metadata.name override: "+err.Error(), err)
		}
	}
	if strings.TrimSpace(overrides.DomainBase) != "" {
		if err := setNestedString(spec, overrides.DomainBase, "network", "domain", "base"); err != nil {
			return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "apply network.domain.base override: "+err.Error(), err)
		}
		if profile == stackspecmigration.KitProfileCloud {
			if _, declared := spec["routes"]; declared {
				if err := projectCloudInitialPublicRouteHosts(spec, overrides.DomainBase); err != nil {
					return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "project Cloud initial public route hosts: "+err.Error(), err)
				}
			}
		}
		if profile == stackspecmigration.KitProfileModern {
			if err := projectModernInitialPublicationHost(spec, overrides.DomainBase); err != nil {
				return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "project Modern initial publication host: "+err.Error(), err)
			}
		}
	}
	if len(overrides.EnableCapabilities) > 0 {
		if err := appendNestedStringList(spec, overrides.EnableCapabilities, "capabilities", "enable"); err != nil {
			return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "apply capabilities.enable override: "+err.Error(), err)
		}
	}
	if len(workloadSelections) > 0 {
		workloads, _ := spec["workloads"].(map[string]any)
		if workloads == nil {
			workloads = map[string]any{}
			spec["workloads"] = workloads
		}
		for _, id := range sortedSelectionKeys(workloadSelections) {
			if _, exists := workloads[id]; exists {
				continue
			}
			selection := workloadSelections[id]
			siteRef, err := initialWorkloadSiteRef(spec, id)
			if err != nil {
				return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "derive initial workload placement: "+err.Error(), err)
			}
			// Initial authoring must be concrete enough for resolution, not only
			// CUE admission. This matters for multi-site kits: an empty site list
			// can let a home-owned application select a cloud edge whose runtime
			// provider cannot host it.
			entry := map[string]any{
				"alternative": selection.Alternative,
				"placement": map[string]any{
					"siteRefs":      []any{siteRef},
					"nodeRefs":      []any{},
					"requiresRoles": []any{},
				},
			}
			if strings.TrimSpace(selection.RuntimeAdapterRef) != "" {
				entry["runtimeAdapterRef"] = selection.RuntimeAdapterRef
			}
			if len(selection.RequiredSecretRefs) > 0 {
				secretRefs := map[string]any{}
				for _, ref := range selection.RequiredSecretRefs {
					secretRefs[ref] = fmt.Sprintf("secret://workloads/%s/%s", id, ref)
				}
				entry["secretRefs"] = secretRefs
			}
			workloads[id] = entry
		}
	}

	candidate, err := resolvedplan.CanonicalJSON(spec)
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, "marshal initial StackSpec candidate: "+err.Error(), err)
	}
	validation, err := validate(candidate)
	if err != nil {
		return StackSpecValidation{}, err
	}
	if validation.KitProfile != profile {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, fmt.Sprintf("StackSpec validator selected %q, want %q", validation.KitProfile, profile), nil)
	}
	if len(validation.CanonicalStackSpec) == 0 || strings.TrimSpace(validation.SpecHash) == "" {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, "StackSpec validator returned incomplete canonical evidence", nil)
	}
	return validation, nil
}

func initialWorkloadSiteRef(spec map[string]any, workloadID string) (string, error) {
	data, _ := spec["data"].(map[string]any)
	if bindings, ok := data["bindings"].(map[string]any); ok {
		if binding, ok := bindings[workloadID].(map[string]any); ok {
			if siteRef, _ := binding["primarySiteRef"].(string); strings.TrimSpace(siteRef) != "" {
				return requireInitialSite(spec, siteRef)
			}
		}
	}
	defaultAuthority, _ := data["defaultAuthority"].(string)
	if strings.TrimSpace(defaultAuthority) == "" {
		return "", fmt.Errorf("workload %q has neither data binding nor default data authority", workloadID)
	}
	return requireInitialSite(spec, defaultAuthority)
}

func requireInitialSite(spec map[string]any, siteRef string) (string, error) {
	siteRef = strings.TrimSpace(siteRef)
	sites, _ := spec["sites"].([]any)
	for _, raw := range sites {
		site, _ := raw.(map[string]any)
		if id, _ := site["id"].(string); id == siteRef {
			return siteRef, nil
		}
	}
	return "", fmt.Errorf("initial data authority references unknown site %q", siteRef)
}

// projectCloudInitialPublicRouteHosts keeps the CUE-owned default service
// routes bound to the operator-selected domain. Initial authoring starts from
// a concrete Definition projection, so CUE cannot re-evaluate a route host
// after the narrow domain override is applied here.
func projectCloudInitialPublicRouteHosts(spec map[string]any, domainBase string) error {
	routes, ok := spec["routes"].(map[string]any)
	if !ok || len(routes) == 0 {
		return fmt.Errorf("routes are not an object with a declared Cloud service route")
	}
	for routeID, rawRoute := range routes {
		route, ok := rawRoute.(map[string]any)
		if !ok || route == nil {
			return fmt.Errorf("routes.%s is not an object", routeID)
		}
		exposure, _ := route["exposure"].(string)
		if exposure != "public" {
			continue
		}
		serviceRef, ok := route["serviceRef"].(string)
		if !ok || strings.TrimSpace(serviceRef) == "" {
			return fmt.Errorf("routes.%s has no serviceRef", routeID)
		}
		route["host"] = serviceRef + "." + strings.TrimSpace(domainBase)
	}
	return nil
}

func projectModernInitialPublicationHost(spec map[string]any, domainBase string) error {
	bridge, ok := spec["bridge"].(map[string]any)
	if !ok || bridge == nil {
		return fmt.Errorf("bridge is not an object")
	}
	publications, ok := bridge["publications"].([]any)
	if !ok || len(publications) != 1 {
		return fmt.Errorf("bridge.publications does not contain the one compiler-owned default")
	}
	publication, ok := publications[0].(map[string]any)
	if !ok || publication == nil || publication["serviceRef"] != "photos" {
		return fmt.Errorf("bridge.publications[0] is not the compiler-owned photos publication")
	}
	publication["host"] = "photos." + strings.TrimSpace(domainBase)
	return nil
}

func validateInitialAuthoringContractVersion(authoring map[string]any, profile stackspecmigration.KitProfile) (string, error) {
	contractVersion, ok := authoring["contractVersion"].(string)
	if !ok || contractVersion != initialAuthoringContractVersion {
		return "", resolveError(
			ErrAuthorityLoad,
			fmt.Sprintf("%s Definition authoring.contractVersion is %v, want exact supported version %s", profile, authoring["contractVersion"], initialAuthoringContractVersion),
			nil,
		)
	}
	return contractVersion, nil
}

func isCanonicalProductKitProfile(profile stackspecmigration.KitProfile) bool {
	return productkits.IsActive(string(profile))
}

func validateDefinitionProfile(definition resolvedplan.KitDefinition, profile stackspecmigration.KitProfile) error {
	if definition == nil {
		return resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition is nil", profile), nil)
	}
	metadata, ok := definition["metadata"].(map[string]any)
	if !ok || metadata == nil {
		return resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition has no metadata object", profile), nil)
	}
	slug, ok := metadata["slug"].(string)
	if !ok || slug != string(profile) {
		return resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition metadata.slug is %v", profile, metadata["slug"]), nil)
	}
	return nil
}

func decodeRequiredInitialOverrides(authoring map[string]any, profile stackspecmigration.KitProfile) ([]string, error) {
	raw, exists := authoring["requiredOverrides"]
	if !exists {
		return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.requiredOverrides is missing", profile), nil)
	}
	var values []string
	switch typed := raw.(type) {
	case []any:
		values = make([]string, 0, len(typed))
		for index, value := range typed {
			path, ok := value.(string)
			if !ok || strings.TrimSpace(path) == "" {
				return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.requiredOverrides[%d] is not a non-empty string", profile, index), nil)
			}
			values = append(values, path)
		}
	case []string:
		values = append([]string(nil), typed...)
	default:
		return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition authoring.requiredOverrides is not a list", profile), nil)
	}

	seen := make(map[string]struct{}, len(values))
	for _, path := range values {
		if path != initialOverrideMetadataName && path != initialOverrideNetworkDomainBase {
			return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition requires unsupported init override %q", profile, path), nil)
		}
		if _, duplicate := seen[path]; duplicate {
			return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("%s Definition repeats init override %q", profile, path), nil)
		}
		seen[path] = struct{}{}
	}
	return values, nil
}

func applyInstallComputeTier(spec map[string]any, definition resolvedplan.KitDefinition, computeTier string) error {
	computeTier = strings.TrimSpace(computeTier)
	if computeTier == "" {
		return nil
	}
	switch computeTier {
	case installComputeTierStandard, installComputeTierLow, installComputeTierHigh:
	default:
		return resolveError(ErrInvalidStackSpec, fmt.Sprintf("install.computeTier %q is not a declared product graph", computeTier), nil)
	}
	graph, declared := kitComputeTierGraph(definition, computeTier)
	if !declared {
		if computeTier == installComputeTierStandard {
			if err := setNestedString(spec, computeTier, "install", "computeTier"); err != nil {
				return resolveError(ErrAuthorityLoad, "apply install.computeTier override: "+err.Error(), err)
			}
			return nil
		}
		return resolveError(ErrInvalidStackSpec, fmt.Sprintf("install.computeTier %q has no declared kit graph", computeTier), nil)
	}
	if err := setNestedString(spec, computeTier, "install", "computeTier"); err != nil {
		return resolveError(ErrAuthorityLoad, "apply install.computeTier override: "+err.Error(), err)
	}
	if graph.platformManagement == "standalone" {
		if err := setNestedString(spec, "standalone", "install", "platform", "management"); err != nil {
			return resolveError(ErrAuthorityLoad, "apply install.platform.management override: "+err.Error(), err)
		}
		if err := setNestedBool(spec, true, "install", "platform", "fallbackAllowed"); err != nil {
			return resolveError(ErrAuthorityLoad, "apply install.platform.fallbackAllowed override: "+err.Error(), err)
		}
	}
	if len(graph.enableCapabilities) > 0 {
		if err := appendNestedStringList(spec, graph.enableCapabilities, "capabilities", "enable"); err != nil {
			return resolveError(ErrAuthorityLoad, "apply computeTier enableCapabilities: "+err.Error(), err)
		}
	}
	return nil
}

type authoringComputeTierGraph struct {
	platformManagement string
	enableCapabilities []string
}

func kitComputeTierGraph(definition resolvedplan.KitDefinition, tier string) (authoringComputeTierGraph, bool) {
	graphs, _ := definition["computeTierGraphs"].(map[string]any)
	if graphs == nil {
		return authoringComputeTierGraph{}, false
	}
	raw, _ := graphs[tier].(map[string]any)
	if raw == nil {
		return authoringComputeTierGraph{}, false
	}
	graph := authoringComputeTierGraph{platformManagement: strings.TrimSpace(fmt.Sprint(raw["platformManagement"]))}
	switch enable := raw["enableCapabilities"].(type) {
	case []any:
		for _, value := range enable {
			if id, ok := value.(string); ok && strings.TrimSpace(id) != "" {
				graph.enableCapabilities = append(graph.enableCapabilities, strings.TrimSpace(id))
			}
		}
	case []string:
		for _, id := range enable {
			if strings.TrimSpace(id) != "" {
				graph.enableCapabilities = append(graph.enableCapabilities, strings.TrimSpace(id))
			}
		}
	}
	return graph, graph.platformManagement != ""
}

func applyHardwareProfile(spec map[string]any, profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return nil
	}
	switch profile {
	case "standard", "pi", "gpu", "storage":
	default:
		return resolveError(ErrInvalidStackSpec, fmt.Sprintf("nodes[0].hardware.profile %q is not a declared device class", profile), nil)
	}
	nodes, ok := spec["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		return resolveError(ErrAuthorityLoad, "Definition authoring.initialSpec has no nodes to receive hardware.profile", nil)
	}
	node, ok := nodes[0].(map[string]any)
	if !ok || node == nil {
		return resolveError(ErrAuthorityLoad, "Definition authoring.initialSpec nodes[0] is not an object", nil)
	}
	hardware, _ := node["hardware"].(map[string]any)
	if hardware == nil {
		hardware = map[string]any{}
		node["hardware"] = hardware
	}
	hardware["profile"] = profile
	return nil
}

func setNestedBool(document map[string]any, value bool, path ...string) error {
	if len(path) == 0 {
		return fmt.Errorf("path is empty")
	}
	current := document
	for index, segment := range path[:len(path)-1] {
		nextValue, exists := current[segment]
		if !exists {
			next := make(map[string]any)
			current[segment] = next
			current = next
			continue
		}
		next, ok := nextValue.(map[string]any)
		if !ok || next == nil {
			return fmt.Errorf("%s is not an object", strings.Join(path[:index+1], "."))
		}
		current = next
	}
	current[path[len(path)-1]] = value
	return nil
}

func enforceRequiredInitialOverrides(required []string, overrides AuthoringOverrides) error {
	for _, path := range required {
		var value string
		switch path {
		case initialOverrideMetadataName:
			value = overrides.Name
		case initialOverrideNetworkDomainBase:
			value = overrides.DomainBase
		default:
			return resolveError(ErrAuthorityLoad, fmt.Sprintf("unsupported required init override %q", path), nil)
		}
		if strings.TrimSpace(value) == "" {
			return resolveError(ErrInvalidStackSpec, fmt.Sprintf("required init override %s is missing", path), nil)
		}
	}
	return nil
}

func nestedString(document map[string]any, path ...string) (string, error) {
	current := document
	for index, segment := range path {
		value, exists := current[segment]
		if !exists {
			return "", fmt.Errorf("%s is missing", strings.Join(path[:index+1], "."))
		}
		if index == len(path)-1 {
			text, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
			}
			return text, nil
		}
		next, ok := value.(map[string]any)
		if !ok || next == nil {
			return "", fmt.Errorf("%s is not an object", strings.Join(path[:index+1], "."))
		}
		current = next
	}
	return "", fmt.Errorf("path is empty")
}

func sortedSelectionKeys(values map[string]useCaseWorkloadSelection) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// appendNestedStringList appends values to a list of strings at path,
// deduplicating while preserving order (existing entries first).
func appendNestedStringList(document map[string]any, values []string, path ...string) error {
	if len(path) == 0 {
		return fmt.Errorf("path is empty")
	}
	current := document
	for index, segment := range path[:len(path)-1] {
		nextValue, exists := current[segment]
		if !exists {
			next := make(map[string]any)
			current[segment] = next
			current = next
			continue
		}
		next, ok := nextValue.(map[string]any)
		if !ok || next == nil {
			return fmt.Errorf("%s is not an object", strings.Join(path[:index+1], "."))
		}
		current = next
	}
	leaf := path[len(path)-1]
	existing := []any{}
	if raw, exists := current[leaf]; exists {
		list, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s is not a list", strings.Join(path, "."))
		}
		existing = list
	}
	seen := make(map[string]bool, len(existing)+len(values))
	for _, entry := range existing {
		if s, ok := entry.(string); ok {
			seen[s] = true
		}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		existing = append(existing, value)
		seen[value] = true
	}
	current[leaf] = existing
	return nil
}

func setNestedString(document map[string]any, value string, path ...string) error {
	if len(path) == 0 {
		return fmt.Errorf("path is empty")
	}
	current := document
	for index, segment := range path[:len(path)-1] {
		nextValue, exists := current[segment]
		if !exists {
			next := make(map[string]any)
			current[segment] = next
			current = next
			continue
		}
		next, ok := nextValue.(map[string]any)
		if !ok || next == nil {
			return fmt.Errorf("%s is not an object", strings.Join(path[:index+1], "."))
		}
		current = next
	}
	current[path[len(path)-1]] = value
	return nil
}
