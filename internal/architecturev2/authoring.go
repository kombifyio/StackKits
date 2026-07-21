package architecturev2

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

const (
	initialAuthoringContractVersion  = "1.0.0"
	initialOverrideMetadataName      = "metadata.name"
	initialOverrideNetworkDomainBase = "network.domain.base"
)

// AuthoringOverrides is the deliberately narrow authoring surface for
// a kit's governed initial StackSpec. Adding another field here requires a
// matching Definition.authoring.requiredOverrides contract and an explicit
// materializer implementation; arbitrary paths are never accepted.
type AuthoringOverrides struct {
	Name       string
	DomainBase string
}

// InitialStackSpecAuthoring exposes only the workflow metadata a CLI or UI
// needs before materialization. The initial spec itself stays encapsulated and
// can only leave this service after CUE revalidation.
type InitialStackSpecAuthoring struct {
	ContractVersion   string
	Status            string
	RequiredOverrides []string
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
	return materializeInitialStackSpec(profile, definition, overrides, s.ValidateStackSpec)
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
	return InitialStackSpecAuthoring{ContractVersion: contractVersion, Status: status, RequiredOverrides: append([]string(nil), required...)}, nil
}

func materializeInitialStackSpec(
	profile stackspecmigration.KitProfile,
	definition resolvedplan.KitDefinition,
	overrides AuthoringOverrides,
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

	if strings.TrimSpace(overrides.Name) != "" {
		if err := setNestedString(spec, overrides.Name, "metadata", "name"); err != nil {
			return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "apply metadata.name override: "+err.Error(), err)
		}
	}
	if strings.TrimSpace(overrides.DomainBase) != "" {
		if err := setNestedString(spec, overrides.DomainBase, "network", "domain", "base"); err != nil {
			return StackSpecValidation{}, resolveError(ErrAuthorityLoad, "apply network.domain.base override: "+err.Error(), err)
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
	switch profile {
	case stackspecmigration.KitProfileBasement, stackspecmigration.KitProfileCloud, stackspecmigration.KitProfileModern:
		return true
	default:
		return false
	}
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
