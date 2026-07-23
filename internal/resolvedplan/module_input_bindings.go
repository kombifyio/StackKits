package resolvedplan

import (
	"fmt"
	"net/netip"
	pathpkg "path"
	"reflect"
	"sort"
	"strings"
)

const (
	moduleInputSourceDeviceEnrollment = "identity.deviceEnrollment"
	moduleInputSourceHomeAuthority    = "identityTrust.homeDeviceAuthority"
	moduleInputSourceBasementVerify   = "identityTrust.basementVerification"
	moduleInputSourceCloudAuthority   = "identityTrust.cloudAuthority"
	moduleInputSourceModernHome       = "identityTrust.modernHomeAuthority"
	moduleInputSourceModernCloud      = "identityTrust.modernCloudVerification"
	moduleInputSourceHomeAccess       = "access.homeEnforcement"
	moduleInputSourceLocalAutonomy    = "localAutonomy.policy"
	moduleInputSourceNetworkRoutes    = "network.routes"
	moduleInputSourceCloudHostNetwork = "network.cloudHostSecurity"
	moduleInputSourceHostBootstrap    = "host.bootstrapRuntime"
	moduleInputSourceStorageHostRoots = "storage.hostRoots"
	moduleInputSourceStorageBackup    = "storage.backupRoot"
	moduleInputTypeDeviceEnrollment   = "device-enrollment-public-v1"
	moduleInputTypeHomeAuthority      = "home-device-authority-v1"
	moduleInputTypeBasementVerify     = "basement-identity-verification-v1"
	moduleInputTypeCloudAuthority     = "cloud-identity-authority-v1"
	moduleInputTypeModernHome         = "modern-home-identity-authority-v1"
	moduleInputTypeModernCloud        = "modern-cloud-identity-verification-v1"
	moduleInputTypeHomeAccess         = "home-access-enforcement-v1"
	moduleInputTypeLocalAutonomy      = "local-autonomy-policy-v1"
	moduleInputTypeNetworkRoutesV4    = "authority-bound-service-route-list-v4"
	moduleInputTypeCloudHostNetwork   = "cloud-host-security-network-v1"
	moduleInputTypeHostBootstrap      = "host-bootstrap-runtime-v1"
	moduleInputTypeStorageHostRoots   = "host-storage-roots-v1"
	moduleInputTypeStorageBackup      = "local-backup-root-v1"
	moduleInputTypeNetworkRoutes      = moduleInputTypeNetworkRoutesV4
)

type moduleRenderInputBinding struct {
	targetRef    string
	sourceRef    string
	valueType    string
	cardinality  string
	required     bool
	defaultValue any
	hasDefault   bool
	raw          map[string]any
}

type moduleRenderInputSource struct {
	stackID       string
	kit           map[string]any
	sites         []any
	identity      map[string]any
	identityTrust map[string]any
	controlPlane  map[string]any
	data          map[string]any
	failurePolicy map[string]any
	network       map[string]any
	gates         map[string]any
	install       map[string]any
	system        map[string]any
	storage       map[string]any
}

func moduleRenderInputBindings(unit map[string]any, unitPath string) ([]moduleRenderInputBinding, error) {
	rawBindings, err := objectListOptional(unit, "inputBindings")
	if err != nil {
		return nil, fail(ErrContractConflict, unitPath+".inputBindings", "%v", err)
	}
	publicRefs, err := stringListField(unit, unitPath, "publicInputRefs", false)
	if err != nil {
		return nil, err
	}
	secretRefs, err := stringListField(unit, unitPath, "secretInputRefs", false)
	if err != nil {
		return nil, err
	}
	planRefs, err := stringListField(unit, unitPath, "planInputRefs", false)
	if err != nil {
		return nil, err
	}
	publicSet, secretSet, planSet := moduleInputStringSet(publicRefs), moduleInputStringSet(secretRefs), moduleInputStringSet(planRefs)
	seen := make(map[string]struct{}, len(rawBindings))
	bindings := make([]moduleRenderInputBinding, 0, len(rawBindings))
	for index, raw := range rawBindings {
		path := fmt.Sprintf("%s.inputBindings[%d]", unitPath, index)
		binding, err := parseModuleRenderInputBinding(raw, path)
		if err != nil {
			return nil, err
		}
		if _, exists := publicSet[binding.targetRef]; !exists {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q is not a declared public input", binding.targetRef)
		}
		if _, exists := secretSet[binding.targetRef]; exists {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q aliases a secret input", binding.targetRef)
		}
		if _, exists := planSet[binding.targetRef]; exists {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q aliases a compiler plan input", binding.targetRef)
		}
		if _, duplicate := seen[binding.targetRef]; duplicate {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q is duplicated", binding.targetRef)
		}
		seen[binding.targetRef] = struct{}{}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].targetRef < bindings[j].targetRef })
	return bindings, nil
}

func parseModuleRenderInputBinding(raw map[string]any, path string) (moduleRenderInputBinding, error) {
	allowed := map[string]struct{}{
		"targetRef": {}, "sourceRef": {}, "valueType": {}, "cardinality": {}, "required": {}, "defaultValue": {},
	}
	for field := range raw {
		if _, ok := allowed[field]; !ok {
			return moduleRenderInputBinding{}, fail(ErrContractConflict, path+"."+field, "field is not part of the closed module input binding contract")
		}
	}
	targetRef, err := stringField(raw, path, "targetRef")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	sourceRef, err := stringField(raw, path, "sourceRef")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	valueType, err := stringField(raw, path, "valueType")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	cardinality, err := stringField(raw, path, "cardinality")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	requiredValue, exists := raw["required"]
	if !exists {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".required", "required boolean is missing")
	}
	required, ok := requiredValue.(bool)
	if !ok {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".required", "expected boolean")
	}
	defaultValue, hasDefault := raw["defaultValue"]
	if required && hasDefault {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".defaultValue", "required bindings cannot declare a default")
	}
	if !required && !hasDefault {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".defaultValue", "optional bindings require an exact typed default")
	}
	if err := validateModuleInputBindingShape(sourceRef, valueType, cardinality, defaultValue, hasDefault, path); err != nil {
		return moduleRenderInputBinding{}, err
	}
	clone, err := cloneObject(raw, true)
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	return moduleRenderInputBinding{
		targetRef: targetRef, sourceRef: sourceRef, valueType: valueType, cardinality: cardinality,
		required: required, defaultValue: defaultValue, hasDefault: hasDefault, raw: clone,
	}, nil
}

func validateModuleInputBindingShape(sourceRef, valueType, cardinality string, defaultValue any, hasDefault bool, path string) error {
	switch sourceRef {
	case moduleInputSourceDeviceEnrollment:
		if valueType != moduleInputTypeDeviceEnrollment || cardinality != "single" {
			return fail(ErrContractConflict, path, "identity.deviceEnrollment requires type %q and single cardinality", moduleInputTypeDeviceEnrollment)
		}
		if hasDefault {
			projected, err := projectPublicDeviceEnrollment(defaultValue, path+".defaultValue", true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "device enrollment default is not the exact public projection")
			}
		}
	case moduleInputSourceHomeAuthority:
		if valueType != moduleInputTypeHomeAuthority || cardinality != "single" {
			return fail(ErrContractConflict, path, "identityTrust.homeDeviceAuthority requires type %q and single cardinality", moduleInputTypeHomeAuthority)
		}
		if hasDefault {
			return fail(ErrContractConflict, path+".defaultValue", "Home device authority is compiler-owned and cannot declare a default")
		}
	case moduleInputSourceBasementVerify:
		if valueType != moduleInputTypeBasementVerify || cardinality != "single" {
			return fail(ErrContractConflict, path, "identityTrust.basementVerification requires type %q and single cardinality", moduleInputTypeBasementVerify)
		}
		if hasDefault {
			return fail(ErrContractConflict, path+".defaultValue", "Basement identity verification is compiler-owned and cannot declare a default")
		}
	case moduleInputSourceCloudAuthority:
		if valueType != moduleInputTypeCloudAuthority || cardinality != "single" {
			return fail(ErrContractConflict, path, "identityTrust.cloudAuthority requires type %q and single cardinality", moduleInputTypeCloudAuthority)
		}
		if hasDefault {
			return fail(ErrContractConflict, path+".defaultValue", "Cloud identity authority is compiler-owned and cannot declare a default")
		}
	case moduleInputSourceModernHome:
		if valueType != moduleInputTypeModernHome || cardinality != "single" {
			return fail(ErrContractConflict, path, "identityTrust.modernHomeAuthority requires type %q and single cardinality", moduleInputTypeModernHome)
		}
		if hasDefault {
			return fail(ErrContractConflict, path+".defaultValue", "Modern Home identity authority is compiler-owned and cannot declare a default")
		}
	case moduleInputSourceModernCloud:
		if valueType != moduleInputTypeModernCloud || cardinality != "single" {
			return fail(ErrContractConflict, path, "identityTrust.modernCloudVerification requires type %q and single cardinality", moduleInputTypeModernCloud)
		}
		if hasDefault {
			return fail(ErrContractConflict, path+".defaultValue", "Modern Cloud identity verification is compiler-owned and cannot declare a default")
		}
	case moduleInputSourceHomeAccess:
		if valueType != moduleInputTypeHomeAccess || cardinality != "single" {
			return fail(ErrContractConflict, path, "access.homeEnforcement requires type %q and single cardinality", moduleInputTypeHomeAccess)
		}
		if hasDefault {
			return fail(ErrContractConflict, path+".defaultValue", "Home access enforcement is compiler-owned and cannot declare a default")
		}
	case moduleInputSourceLocalAutonomy:
		if valueType != moduleInputTypeLocalAutonomy || cardinality != "single" {
			return fail(ErrContractConflict, path, "localAutonomy.policy requires type %q and single cardinality", moduleInputTypeLocalAutonomy)
		}
		if hasDefault {
			return fail(ErrContractConflict, path+".defaultValue", "local-autonomy policy is compiler-owned and cannot declare a default")
		}
	case moduleInputSourceNetworkRoutes:
		if valueType != moduleInputTypeNetworkRoutesV4 || cardinality != "list" {
			return fail(ErrContractConflict, path, "network.routes requires current type %q and list cardinality", moduleInputTypeNetworkRoutesV4)
		}
		if hasDefault {
			projected, err := projectPublicRouteList(defaultValue, path+".defaultValue", true, true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "route default is not the exact secret-safe public projection")
			}
		}
	case moduleInputSourceCloudHostNetwork:
		if valueType != moduleInputTypeCloudHostNetwork || cardinality != "single" {
			return fail(ErrContractConflict, path, "network.cloudHostSecurity requires type %q and single cardinality", moduleInputTypeCloudHostNetwork)
		}
		if hasDefault {
			projected, err := projectPublicCloudHostSecurityNetwork(defaultValue, nil, path+".defaultValue", true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "Cloud host-security default is not the exact public projection")
			}
		}
	case moduleInputSourceHostBootstrap:
		if valueType != moduleInputTypeHostBootstrap || cardinality != "single" {
			return fail(ErrContractConflict, path, "host.bootstrapRuntime requires type %q and single cardinality", moduleInputTypeHostBootstrap)
		}
		if hasDefault {
			projected, err := projectPublicHostBootstrapRuntime(defaultValue, path+".defaultValue", true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "host bootstrap default is not the exact public projection")
			}
		}
	case moduleInputSourceStorageHostRoots:
		if valueType != moduleInputTypeStorageHostRoots || cardinality != "single" {
			return fail(ErrContractConflict, path, "storage.hostRoots requires type %q and single cardinality", moduleInputTypeStorageHostRoots)
		}
		if hasDefault {
			projected, err := projectPublicHostStorageRoots(defaultValue, path+".defaultValue", true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "host storage default is not the exact public projection")
			}
		}
	case moduleInputSourceStorageBackup:
		if valueType != moduleInputTypeStorageBackup || cardinality != "single" {
			return fail(ErrContractConflict, path, "storage.backupRoot requires type %q and single cardinality", moduleInputTypeStorageBackup)
		}
		if hasDefault {
			projected, err := projectPublicLocalBackupRoot(defaultValue, path+".defaultValue", true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "backup-root default is not the exact public projection")
			}
		}
	default:
		return fail(ErrContractConflict, path+".sourceRef", "unsupported resolved-plan input source %q", sourceRef)
	}
	return nil
}

func moduleRenderInputBindingsAny(bindings []moduleRenderInputBinding) []any {
	result := make([]any, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, binding.raw)
	}
	return result
}

func bindResolvedModuleRenderInputs(modules []any, source moduleRenderInputSource) error {
	for moduleIndex, rawModule := range modules {
		module, err := asObject(rawModule, fmt.Sprintf("modules[%d]", moduleIndex))
		if err != nil {
			return err
		}
		moduleID, err := stringField(module, fmt.Sprintf("modules[%d]", moduleIndex), "id")
		if err != nil {
			return err
		}
		units, err := objectListField(module, "modules."+moduleID, "renderUnits")
		if err != nil {
			return err
		}
		for unitIndex, unit := range units {
			unitPath := fmt.Sprintf("modules.%s.renderUnits[%d]", moduleID, unitIndex)
			bindings, err := moduleRenderInputBindings(unit, unitPath)
			if err != nil {
				return err
			}
			values, err := objectField(unit, unitPath, "values")
			if err != nil {
				return err
			}
			for _, binding := range bindings {
				if _, exists := values[binding.targetRef]; exists {
					return fail(ErrContractConflict, unitPath+".values."+binding.targetRef, "compiler-bound public input was already populated")
				}
				value, available, err := source.resolve(binding)
				if err != nil {
					return fail(ErrContractConflict, unitPath+".inputBindings."+binding.targetRef, "%v", err)
				}
				if !available {
					if binding.required {
						return fail(ErrUnrealizedModule, unitPath+".inputBindings."+binding.targetRef, "required resolved-plan source %q is unavailable", binding.sourceRef)
					}
					value = binding.defaultValue
				}
				normalized, err := normalizeJSON(value, false, unitPath+".values."+binding.targetRef)
				if err != nil {
					return err
				}
				values[binding.targetRef] = normalized
			}
			unit["inputBindings"] = moduleRenderInputBindingsAny(bindings)
			unit["values"] = values
		}
	}
	return nil
}

func (source moduleRenderInputSource) resolve(binding moduleRenderInputBinding) (any, bool, error) {
	switch binding.sourceRef {
	case moduleInputSourceDeviceEnrollment:
		if source.identity == nil {
			return nil, false, nil
		}
		value, exists := source.identity["deviceEnrollment"]
		if !exists || value == nil {
			return nil, false, nil
		}
		projected, err := projectPublicDeviceEnrollment(value, "resolvedPlan.identity.deviceEnrollment", false)
		return projected, err == nil, err
	case moduleInputSourceHomeAuthority:
		if source.identityTrust == nil || source.kit == nil || source.sites == nil || source.stackID == "" {
			return nil, false, nil
		}
		projected, err := projectPublicHomeDeviceAuthority(
			source.identityTrust, source.kit, source.sites, source.stackID,
			"resolvedPlan.identityTrust.homeDeviceAuthority", false,
		)
		return projected, err == nil, err
	case moduleInputSourceBasementVerify:
		if source.identityTrust == nil || source.kit == nil || source.sites == nil || source.stackID == "" {
			return nil, false, nil
		}
		projected, err := projectPublicBasementIdentityVerification(
			source.identityTrust, source.kit, source.sites, source.stackID,
			"resolvedPlan.identityTrust.basementVerification", false,
		)
		return projected, err == nil, err
	case moduleInputSourceCloudAuthority:
		if source.identityTrust == nil || source.kit == nil || source.sites == nil || source.stackID == "" {
			return nil, false, nil
		}
		projected, err := projectPublicCloudIdentityAuthority(
			source.identityTrust, source.kit, source.sites, source.stackID,
			"resolvedPlan.identityTrust.cloudAuthority", false,
		)
		return projected, err == nil, err
	case moduleInputSourceModernHome:
		if source.identityTrust == nil || source.failurePolicy == nil || source.kit == nil || source.sites == nil || source.stackID == "" {
			return nil, false, nil
		}
		projected, err := projectPublicModernHomeIdentityAuthority(
			source.identityTrust, source.failurePolicy, source.kit, source.sites, source.stackID,
			"resolvedPlan.identityTrust.modernHomeAuthority", false,
		)
		return projected, err == nil, err
	case moduleInputSourceModernCloud:
		if source.identityTrust == nil || source.failurePolicy == nil || source.kit == nil || source.sites == nil || source.stackID == "" {
			return nil, false, nil
		}
		projected, err := projectPublicModernCloudIdentityVerification(
			source.identityTrust, source.failurePolicy, source.kit, source.sites, source.stackID,
			"resolvedPlan.identityTrust.modernCloudVerification", false,
		)
		return projected, err == nil, err
	case moduleInputSourceHomeAccess:
		if source.network == nil || source.sites == nil || source.stackID == "" {
			return nil, false, nil
		}
		projected, err := projectPublicHomeAccessEnforcement(
			source.stackID, source.network, source.sites, "resolvedPlan.access.homeEnforcement",
		)
		return projected, err == nil, err
	case moduleInputSourceLocalAutonomy:
		if source.stackID == "" || source.kit == nil || source.sites == nil || source.controlPlane == nil ||
			source.identity == nil || source.data == nil || source.failurePolicy == nil {
			return nil, false, nil
		}
		projected, err := projectPublicLocalAutonomyPolicy(
			source.stackID, source.kit, source.sites, source.controlPlane, source.identity, source.data, source.failurePolicy,
			"resolvedPlan.localAutonomy.policy",
		)
		return projected, err == nil, err
	case moduleInputSourceNetworkRoutes:
		if source.network == nil {
			return nil, false, nil
		}
		if _, exists := source.network["routes"]; !exists {
			return nil, false, nil
		}
		projected, err := projectPublicRouteListFromNetwork(source.network, source.gates, "resolvedPlan.network", true, true)
		return projected, err == nil, err
	case moduleInputSourceCloudHostNetwork:
		if source.network == nil || source.kit == nil {
			return nil, false, nil
		}
		projected, err := projectPublicCloudHostSecurityNetwork(source.network, source.kit, "resolvedPlan.network.cloudHostSecurity", false)
		return projected, err == nil, err
	case moduleInputSourceHostBootstrap:
		if source.install == nil || source.system == nil {
			return nil, false, nil
		}
		projected, err := projectPublicHostBootstrapRuntime(
			map[string]any{"install": source.install, "system": source.system},
			"resolvedPlan.host.bootstrapRuntime", false,
		)
		return projected, err == nil, err
	case moduleInputSourceStorageHostRoots:
		if source.storage == nil {
			return nil, false, nil
		}
		projected, err := projectPublicHostStorageRoots(source.storage, "resolvedPlan.storage.hostRoots", false)
		return projected, err == nil, err
	case moduleInputSourceStorageBackup:
		if source.storage == nil {
			return nil, false, nil
		}
		projected, err := projectPublicLocalBackupRoot(source.storage, "resolvedPlan.storage.backupRoot", false)
		return projected, err == nil, err
	default:
		return nil, false, fmt.Errorf("unsupported resolved-plan input source %q", binding.sourceRef)
	}
}

func projectPublicHomeDeviceAuthority(value any, kit map[string]any, sites []any, stackID, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	if alreadyPublic {
		return exactPublicHomeDeviceAuthority(input, kit, sites, stackID, path)
	}
	slug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil {
		return nil, err
	}
	if slug != "basement-kit" && slug != "modern-homelab" {
		return nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Home device authority is unavailable to kit %q", slug)
	}
	authorities, err := objectListField(input, path, "authorities")
	if err != nil {
		return nil, err
	}
	var authority map[string]any
	for index, candidate := range authorities {
		principal, err := stringField(candidate, fmt.Sprintf("%s.authorities[%d]", path, index), "principal")
		if err != nil {
			return nil, err
		}
		if principal == "device" {
			if authority != nil {
				return nil, fail(ErrContractConflict, path+".authorities", "Home authority projection requires exactly one device authority")
			}
			authority = candidate
		}
	}
	if authority == nil {
		return nil, fail(ErrContractConflict, path+".authorities", "Home authority projection requires exactly one device authority")
	}
	authorityID, err := stringField(authority, path+".authorities.device", "id")
	if err != nil || !identityTrustIDPattern.MatchString(authorityID) {
		return nil, fail(ErrContractConflict, path+".authorities.device.id", "requires a canonical device authority ID")
	}
	trustDomainRef, err := stringField(authority, path+".authorities.device", "trustDomainRef")
	if err != nil || !identityTrustIDPattern.MatchString(trustDomainRef) {
		return nil, fail(ErrContractConflict, path+".authorities.device.trustDomainRef", "requires a canonical trust-domain reference")
	}
	siteRef, err := requireHomeAuthorityPlacement(authority, sites, path+".authorities.device")
	if err != nil {
		return nil, err
	}
	if err := requireHomeAuthorityOwner(authority, path+".authorities.device"); err != nil {
		return nil, err
	}

	issuers, err := objectListField(input, path, "credentialIssuers")
	if err != nil {
		return nil, err
	}
	var issuer map[string]any
	for index, candidate := range issuers {
		principal, err := stringField(candidate, fmt.Sprintf("%s.credentialIssuers[%d]", path, index), "principal")
		if err != nil {
			return nil, err
		}
		if principal == "device" {
			if issuer != nil {
				return nil, fail(ErrContractConflict, path+".credentialIssuers", "Home authority projection requires exactly one device credential issuer")
			}
			issuer = candidate
		}
	}
	if issuer == nil {
		return nil, fail(ErrContractConflict, path+".credentialIssuers", "Home authority projection requires exactly one device credential issuer")
	}
	issuerSiteRef, err := requireHomeAuthorityPlacement(issuer, sites, path+".credentialIssuers.device")
	if err != nil {
		return nil, err
	}
	if issuerSiteRef != siteRef {
		return nil, fail(ErrContractConflict, path+".credentialIssuers.device.placement", "device authority and issuer must bind the same Home Site")
	}
	if err := requireHomeAuthorityOwner(issuer, path+".credentialIssuers.device"); err != nil {
		return nil, err
	}

	projected := map[string]any{
		"authority": map[string]any{"id": authorityID, "trustDomainRef": trustDomainRef, "siteRef": siteRef},
	}
	projectedIssuer, err := projectHomeDeviceIssuer(issuer, authorityID, stackID, path+".credentialIssuers.device")
	if err != nil {
		return nil, err
	}
	projected["issuer"] = projectedIssuer
	return exactPublicHomeDeviceAuthority(projected, kit, sites, stackID, path+".public")
}

func requireHomeAuthorityPlacement(value map[string]any, sites []any, path string) (string, error) {
	placement, err := objectField(value, path, "placement")
	if err != nil {
		return "", err
	}
	if len(placement) != 2 {
		return "", fail(ErrContractConflict, path+".placement", "Home authority placement must contain exactly kind and siteRefs")
	}
	kind, err := stringField(placement, path+".placement", "kind")
	if err != nil || kind != "sites" {
		return "", fail(ErrContractConflict, path+".placement.kind", "Home authority placement must be Site-owned")
	}
	refs, err := stringListField(placement, path+".placement", "siteRefs", true)
	if err != nil || len(refs) != 1 {
		return "", fail(ErrContractConflict, path+".placement.siteRefs", "Home authority placement requires exactly one Site")
	}
	matches := 0
	for index, rawSite := range sites {
		site, err := asObject(rawSite, fmt.Sprintf("resolvedPlan.sites[%d]", index))
		if err != nil {
			return "", err
		}
		id, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "id")
		if err != nil {
			return "", err
		}
		if id != refs[0] {
			continue
		}
		siteKind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil {
			return "", err
		}
		if siteKind != "home" {
			return "", fail(ErrContractConflict, path+".placement.siteRefs", "Home authority Site %q has kind %q", id, siteKind)
		}
		matches++
	}
	if matches != 1 {
		return "", fail(ErrContractConflict, path+".placement.siteRefs", "Home authority Site %q must exist exactly once", refs[0])
	}
	return refs[0], nil
}

func requireHomeAuthorityOwner(value map[string]any, path string) error {
	owner, err := objectField(value, path, "owner")
	if err != nil {
		return err
	}
	if len(owner) != 3 {
		return fail(ErrContractConflict, path+".owner", "Home authority owner must contain exactly kind, providerRef, and moduleRef")
	}
	for field, expected := range map[string]string{
		"kind": "catalog", "providerRef": "stackkits-home-device-authority", "moduleRef": "stackkits-home-device-authority-policy-manifest",
	} {
		actual, err := stringField(owner, path+".owner", field)
		if err != nil || actual != expected {
			return fail(ErrContractConflict, path+".owner."+field, "Home authority owner requires %q", expected)
		}
	}
	return nil
}

func projectHomeDeviceIssuer(issuer map[string]any, authorityID, stackID, path string) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"id", "authorityRef", "issuer", "verificationKeySetRef"} {
		value, err := stringField(issuer, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = value
	}
	if result["authorityRef"] != authorityID {
		return nil, fail(ErrContractConflict, path+".authorityRef", "device issuer must bind the projected authority")
	}
	if !identityTrustIDPattern.MatchString(result["id"].(string)) {
		return nil, fail(ErrContractConflict, path+".id", "requires a canonical issuer ID")
	}
	if err := requireResolvedIdentityURN(result["issuer"].(string), stackID, "issuer", path+".issuer"); err != nil {
		return nil, err
	}
	if err := requireResolvedIdentityURN(result["verificationKeySetRef"].(string), stackID, "keyset", path+".verificationKeySetRef"); err != nil {
		return nil, err
	}
	audiences, err := stringListField(issuer, path, "audiences", true)
	if err != nil || len(audiences) == 0 || !reflect.DeepEqual(audiences, sortStringsUnique(audiences)) {
		return nil, fail(ErrContractConflict, path+".audiences", "device issuer audiences must be non-empty, sorted, and unique")
	}
	for index, audience := range audiences {
		if err := requireResolvedIdentityURN(audience, stackID, "audience", fmt.Sprintf("%s.audiences[%d]", path, index)); err != nil {
			return nil, err
		}
	}
	result["audiences"] = stringSliceAny(audiences)
	for _, field := range []string{"credentialTTLSeconds", "sessionTTLSeconds", "revocationMaxStalenessSeconds"} {
		value, err := intField(issuer, path, field)
		if err != nil {
			return nil, err
		}
		targetField := field
		if field == "credentialTTLSeconds" {
			targetField = "lifetimeSeconds"
		}
		result[targetField] = value
	}
	credentialTTL := result["lifetimeSeconds"].(int)
	sessionTTL := result["sessionTTLSeconds"].(int)
	staleness := result["revocationMaxStalenessSeconds"].(int)
	if credentialTTL < 300 || credentialTTL > 86400 || sessionTTL < 60 || sessionTTL > 86400 || staleness < 0 || staleness > credentialTTL {
		return nil, fail(ErrContractConflict, path, "device issuer TTL and revocation policy are outside the closed bounds")
	}
	for _, field := range []string{"proofOfPossessionRequired", "revocationSupported", "issuanceWithinStackKit"} {
		value, exists := issuer[field]
		boolean, ok := value.(bool)
		if !exists || !ok || !boolean {
			return nil, fail(ErrContractConflict, path+"."+field, "Home device issuer requires true")
		}
		if field != "issuanceWithinStackKit" {
			result[field] = boolean
		}
	}
	enrollment, err := objectField(issuer, path, "enrollment")
	if err != nil || len(enrollment) != 2 {
		return nil, fail(ErrContractConflict, path+".enrollment", "Home device enrollment must contain exactly mode and exposure")
	}
	mode, err := stringField(enrollment, path+".enrollment", "mode")
	if err != nil || mode != "local-only" {
		return nil, fail(ErrContractConflict, path+".enrollment.mode", "Home device enrollment must be local-only")
	}
	exposure, err := stringField(enrollment, path+".enrollment", "exposure")
	if err != nil || exposure != "lan" {
		return nil, fail(ErrContractConflict, path+".enrollment.exposure", "Home device enrollment must remain LAN-only")
	}
	result["enrollment"] = map[string]any{"mode": mode, "exposure": exposure}
	return result, nil
}

func exactPublicHomeDeviceAuthority(input map[string]any, kit map[string]any, sites []any, stackID, path string) (map[string]any, error) {
	if len(input) != 2 {
		return nil, fail(ErrContractConflict, path, "Home device authority projection must contain exactly authority and issuer")
	}
	authority, err := objectField(input, path, "authority")
	if err != nil || len(authority) != 3 {
		return nil, fail(ErrContractConflict, path+".authority", "Home device authority must contain exactly id, trustDomainRef, and siteRef")
	}
	issuer, err := objectField(input, path, "issuer")
	if err != nil || len(issuer) != 11 {
		return nil, fail(ErrContractConflict, path+".issuer", "Home device issuer contains authority outside the closed projection")
	}
	authorityID, err := stringField(authority, path+".authority", "id")
	if err != nil || !identityTrustIDPattern.MatchString(authorityID) {
		return nil, fail(ErrContractConflict, path+".authority.id", "requires a canonical authority ID")
	}
	trustDomainRef, err := stringField(authority, path+".authority", "trustDomainRef")
	if err != nil || !identityTrustIDPattern.MatchString(trustDomainRef) {
		return nil, fail(ErrContractConflict, path+".authority.trustDomainRef", "requires a canonical trust-domain reference")
	}
	siteRef, err := stringField(authority, path+".authority", "siteRef")
	if err != nil || !identityTrustIDPattern.MatchString(siteRef) {
		return nil, fail(ErrContractConflict, path+".authority.siteRef", "requires a canonical Site reference")
	}
	if kit != nil && sites != nil {
		synthetic := map[string]any{"placement": map[string]any{"kind": "sites", "siteRefs": []any{siteRef}}}
		if _, err := requireHomeAuthorityPlacement(synthetic, sites, path+".authority"); err != nil {
			return nil, err
		}
		slug, err := stringField(kit, "resolvedPlan.kit", "slug")
		if err != nil || slug != "basement-kit" && slug != "modern-homelab" {
			return nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Home device authority is unavailable to this kit")
		}
	}
	projectedIssuer, err := projectHomeDeviceIssuerPublic(issuer, authorityID, stackID, path+".issuer")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"authority": map[string]any{"id": authorityID, "trustDomainRef": trustDomainRef, "siteRef": siteRef},
		"issuer":    projectedIssuer,
	}, nil
}

func projectHomeDeviceIssuerPublic(issuer map[string]any, authorityID, stackID, path string) (map[string]any, error) {
	source := make(map[string]any, len(issuer)+1)
	for key, value := range issuer {
		source[key] = value
	}
	lifetime, exists := source["lifetimeSeconds"]
	if !exists {
		return nil, fail(ErrInvalidInput, path+".lifetimeSeconds", "required integer is missing")
	}
	source["credentialTTLSeconds"] = lifetime
	delete(source, "lifetimeSeconds")
	source["issuanceWithinStackKit"] = true
	return projectHomeDeviceIssuer(source, authorityID, stackID, path)
}

func requireResolvedIdentityURN(value, stackID, kind, path string) error {
	prefix := "urn:stackkit:"
	if stackID != "" {
		prefix += stackID + ":" + kind + ":"
	} else {
		parts := strings.Split(value, ":")
		if len(parts) != 5 || parts[0] != "urn" || parts[1] != "stackkit" || parts[3] != kind || !identityTrustIDPattern.MatchString(parts[2]) {
			return fail(ErrContractConflict, path, "requires a canonical StackInstance %s URN", kind)
		}
		prefix = strings.Join(parts[:4], ":") + ":"
	}
	if !strings.HasPrefix(value, prefix) || !identityTrustIDPattern.MatchString(strings.TrimPrefix(value, prefix)) {
		return fail(ErrContractConflict, path, "requires a canonical StackInstance %s URN", kind)
	}
	return nil
}

func projectPublicCloudHostSecurityNetwork(value any, kit map[string]any, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	if !alreadyPublic {
		input, err = objectField(input, path, "configuration")
		if err != nil {
			return nil, err
		}
	}
	if alreadyPublic && len(input) != 4 {
		return nil, fail(ErrContractConflict, path, "Cloud host-security network must contain exactly four public fields")
	}
	modeField, subnetField, tlsField := "mode", "transport", "tls"
	if alreadyPublic {
		modeField, subnetField, tlsField = "networkMode", "transportSubnet", "tlsMinVersion"
	}
	mode, err := stringField(input, path, modeField)
	if err != nil {
		return nil, err
	}
	if mode != "public-capable" && mode != "hybrid" {
		return nil, fail(ErrContractConflict, path+"."+modeField, "Cloud host security permits only public-capable or hybrid network mode")
	}
	if kit != nil {
		slug, err := stringField(kit, "resolvedPlan.kit", "slug")
		if err != nil {
			return nil, err
		}
		expected := "public-capable"
		if slug == "modern-homelab" {
			expected = "hybrid"
		} else if slug != "cloud-kit" {
			return nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Cloud host-security input is unavailable to kit %q", slug)
		}
		if mode != expected {
			return nil, fail(ErrContractConflict, path+"."+modeField, "kit %s requires network mode %s", slug, expected)
		}
	}
	var subnet, minVersion string
	var ipv6 bool
	if alreadyPublic {
		subnet, err = stringField(input, path, subnetField)
		if err != nil {
			return nil, err
		}
		ipv6Value, exists := input["ipv6"]
		if !exists {
			return nil, fail(ErrContractConflict, path+".ipv6", "expected boolean")
		}
		ipv6, exists = ipv6Value.(bool)
		if !exists {
			return nil, fail(ErrContractConflict, path+".ipv6", "expected boolean")
		}
		minVersion, err = stringField(input, path, tlsField)
		if err != nil {
			return nil, err
		}
	} else {
		transport, err := objectField(input, path, subnetField)
		if err != nil {
			return nil, err
		}
		subnet, err = stringField(transport, path+".transport", "subnet")
		if err != nil {
			return nil, err
		}
		ipv6Value, exists := transport["ipv6"]
		if !exists {
			return nil, fail(ErrContractConflict, path+".transport.ipv6", "expected boolean")
		}
		ipv6, exists = ipv6Value.(bool)
		if !exists {
			return nil, fail(ErrContractConflict, path+".transport.ipv6", "expected boolean")
		}
		tls, err := objectField(input, path, tlsField)
		if err != nil {
			return nil, err
		}
		minVersion, err = stringField(tls, path+".tls", "minVersion")
		if err != nil {
			return nil, err
		}
	}
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil || prefix.String() != subnet {
		return nil, fail(ErrContractConflict, path+"."+subnetField, "requires a canonical transport CIDR")
	}
	if minVersion != "TLS1.2" && minVersion != "TLS1.3" {
		return nil, fail(ErrContractConflict, path+"."+tlsField, "requires TLS1.2 or TLS1.3")
	}
	return map[string]any{
		"networkMode": mode, "transportSubnet": subnet, "ipv6": ipv6, "tlsMinVersion": minVersion,
	}, nil
}

func projectPublicLocalBackupRoot(value any, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	if alreadyPublic && len(input) != 2 {
		return nil, fail(ErrContractConflict, path, "local backup root must contain exactly path and volumeDriver")
	}
	pathField := "backupRoot"
	if alreadyPublic {
		pathField = "path"
	}
	backupRoot, err := stringField(input, path, pathField)
	if err != nil || !pathpkg.IsAbs(backupRoot) || pathpkg.Clean(backupRoot) != backupRoot {
		return nil, fail(ErrContractConflict, path+"."+pathField, "requires a clean absolute backup root")
	}
	driver, err := stringField(input, path, "volumeDriver")
	if err != nil || driver != "local" {
		return nil, fail(ErrContractConflict, path+".volumeDriver", "Home backup target requires local storage")
	}
	return map[string]any{"path": backupRoot, "volumeDriver": driver}, nil
}

func projectPublicHostBootstrapRuntime(value any, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	if alreadyPublic {
		return exactHostBootstrapRuntime(input, path)
	}
	install, err := objectField(input, path, "install")
	if err != nil {
		return nil, err
	}
	system, err := objectField(input, path, "system")
	if err != nil {
		return nil, err
	}
	container, err := objectField(system, path+".system", "container")
	if err != nil {
		return nil, err
	}
	mode, err := stringField(install, path+".install", "mode")
	if err != nil {
		return nil, err
	}
	runtime, err := stringField(install, path+".install", "runtime")
	if err != nil {
		return nil, err
	}
	engine, err := stringField(container, path+".system.container", "engine")
	if err != nil {
		return nil, err
	}
	dataRoot, err := stringField(container, path+".system.container", "dataRoot")
	if err != nil {
		return nil, err
	}
	return exactHostBootstrapRuntime(map[string]any{
		"installMode": mode, "runtime": runtime, "engine": engine, "dataRoot": dataRoot,
	}, path)
}

func exactHostBootstrapRuntime(input map[string]any, path string) (map[string]any, error) {
	if len(input) != 4 {
		return nil, fail(ErrContractConflict, path, "host bootstrap runtime must contain exactly four public fields")
	}
	result := map[string]any{}
	for field, expected := range map[string]string{"installMode": "bootstrapped", "runtime": "docker", "engine": "docker"} {
		value, err := stringField(input, path, field)
		if err != nil {
			return nil, err
		}
		if value != expected {
			return nil, fail(ErrContractConflict, path+"."+field, "Core host bootstrap requires %q", expected)
		}
		result[field] = value
	}
	dataRoot, err := stringField(input, path, "dataRoot")
	if err != nil || !pathpkg.IsAbs(dataRoot) || pathpkg.Clean(dataRoot) != dataRoot {
		return nil, fail(ErrContractConflict, path+".dataRoot", "requires an absolute container data root")
	}
	result["dataRoot"] = dataRoot
	return result, nil
}

func projectPublicHostStorageRoots(value any, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{"dataRoot": {}, "backupRoot": {}, "stacksRoot": {}, "mediaRoot": {}, "volumeDriver": {}}
	if alreadyPublic {
		for field := range input {
			if _, ok := allowed[field]; !ok {
				return nil, fail(ErrContractConflict, path+"."+field, "field is outside the public host storage projection")
			}
		}
	}
	result := map[string]any{}
	for _, field := range []string{"dataRoot", "backupRoot", "stacksRoot"} {
		item, err := stringField(input, path, field)
		if err != nil || !pathpkg.IsAbs(item) || pathpkg.Clean(item) != item {
			return nil, fail(ErrContractConflict, path+"."+field, "requires an absolute host storage root")
		}
		result[field] = item
	}
	if item, exists := input["mediaRoot"]; exists {
		mediaRoot, ok := item.(string)
		if !ok || !pathpkg.IsAbs(mediaRoot) || pathpkg.Clean(mediaRoot) != mediaRoot {
			return nil, fail(ErrContractConflict, path+".mediaRoot", "requires an absolute host storage root")
		}
		result["mediaRoot"] = mediaRoot
	}
	driver, err := stringField(input, path, "volumeDriver")
	if err != nil || driver != "local" {
		return nil, fail(ErrContractConflict, path+".volumeDriver", "Core host bootstrap requires local storage")
	}
	result["volumeDriver"] = driver
	return result, nil
}

func projectPublicDeviceEnrollment(value any, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	stringsOut := map[string]string{}
	for _, field := range []string{"mode", "authoritySiteRef", "endpointExposure", "hardwareBackedKey"} {
		stringsOut[field], err = stringField(input, path, field)
		if err != nil {
			return nil, err
		}
	}
	result := map[string]any{}
	for key, item := range stringsOut {
		result[key] = item
	}
	for _, field := range []string{
		"remoteEnrollment", "requireOwnerStepUp", "requireLocalPairingProof", "requireDeviceGeneratedKey",
		"requirePossessionProof", "revocationSupported",
	} {
		item, exists := input[field]
		boolean, ok := item.(bool)
		if !exists || !ok {
			return nil, fail(ErrInvalidInput, path+"."+field, "expected boolean")
		}
		result[field] = boolean
	}
	lifetimeField := "credentialTTLSeconds"
	if alreadyPublic {
		lifetimeField = "lifetimeSeconds"
	}
	lifetime, err := intField(input, path, lifetimeField)
	if err != nil {
		return nil, err
	}
	result["lifetimeSeconds"] = lifetime
	return result, nil
}

func projectPublicRouteList(value any, path string, withProbe, withAuthority bool) ([]any, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, fail(ErrInvalidInput, path, "expected route list, got %T", value)
	}
	result := make([]any, 0, len(raw))
	for index, item := range raw {
		route, err := asObject(item, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return nil, err
		}
		pool, err := objectField(route, fmt.Sprintf("%s[%d]", path, index), "backendPool")
		if err != nil {
			return nil, err
		}
		var probe map[string]any
		if withProbe {
			rawProbe, err := objectField(route, fmt.Sprintf("%s[%d]", path, index), "healthProbe")
			if err != nil {
				return nil, err
			}
			probe, err = projectPublicRouteHealthProbe(rawProbe, route, pool, fmt.Sprintf("%s[%d].healthProbe", path, index), true)
			if err != nil {
				return nil, err
			}
		}
		projected, err := projectPublicRoute(route, pool, probe, fmt.Sprintf("%s[%d]", path, index), withAuthority)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["id"].(string) < result[j].(map[string]any)["id"].(string)
	})
	return result, nil
}

func projectPublicRouteListFromNetwork(network, gates map[string]any, path string, withProbe, withAuthority bool) ([]any, error) {
	routes, err := objectListField(network, path, "routes")
	if err != nil {
		return nil, err
	}
	pools, err := objectListField(network, path, "backendPools")
	if err != nil {
		return nil, err
	}
	poolsByID := make(map[string]map[string]any, len(pools))
	for index, pool := range pools {
		poolID, err := stringField(pool, fmt.Sprintf("%s.backendPools[%d]", path, index), "id")
		if err != nil {
			return nil, err
		}
		poolsByID[poolID] = pool
	}
	healthByID := map[string]map[string]any{}
	if withProbe {
		if gates == nil {
			return nil, fail(ErrInvalidInput, "resolvedPlan.gates", "route health gates are unavailable for v3 projection")
		}
		health, err := objectListField(gates, "resolvedPlan.gates", "health")
		if err != nil {
			return nil, err
		}
		for index, gate := range health {
			gateID, err := stringField(gate, fmt.Sprintf("resolvedPlan.gates.health[%d]", index), "id")
			if err != nil {
				return nil, err
			}
			healthByID[gateID] = gate
		}
	}
	result := make([]any, 0, len(routes))
	for index, route := range routes {
		routePath := fmt.Sprintf("%s.routes[%d]", path, index)
		poolRef, err := stringField(route, routePath, "backendPoolRef")
		if err != nil {
			return nil, err
		}
		pool, exists := poolsByID[poolRef]
		if !exists {
			return nil, fail(ErrContractConflict, routePath+".backendPoolRef", "backend pool %q does not exist", poolRef)
		}
		var probe map[string]any
		if withProbe {
			healthRef, err := stringField(route, routePath, "healthGateRef")
			if err != nil {
				return nil, err
			}
			healthGate, exists := healthByID[healthRef]
			if !exists {
				return nil, fail(ErrContractConflict, routePath+".healthGateRef", "health gate %q does not exist", healthRef)
			}
			probe, err = projectPublicRouteHealthProbe(healthGate, route, pool, routePath+".healthProbe", false)
			if err != nil {
				return nil, err
			}
		}
		projected, err := projectPublicRoute(route, pool, probe, routePath, withAuthority)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["id"].(string) < result[j].(map[string]any)["id"].(string)
	})
	return result, nil
}

func projectPublicRoute(route, pool, probe map[string]any, path string, withAuthority bool) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"id", "serviceRef", "moduleRef", "exposure", "protocol", "upstreamProtocol", "healthGateRef", "backendPoolRef"} {
		value, err := stringField(route, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = value
	}
	selector, hasSelector, err := optionalStringField(route, path, "originSelector")
	if err != nil {
		return nil, err
	}
	if !hasSelector {
		return nil, fail(ErrInvalidInput, path+".originSelector", "current route selector is missing")
	}
	if !contains([]string{"single-site", "control-authority-site", "multi-zone", "edge-pool"}, selector) {
		return nil, fail(ErrInvalidInput, path+".originSelector", "unsupported selector %q", selector)
	}
	result["originSelector"] = selector
	sites, err := stringListField(route, path, "originSiteRefs", true)
	if err != nil {
		return nil, err
	}
	result["originSiteRefs"] = stringSliceAny(sortStringsUnique(sites))
	originSiteRef, hasOriginSiteRef, err := optionalStringField(route, path, "originSiteRef")
	if err != nil {
		return nil, err
	}
	if selector == "single-site" || selector == "control-authority-site" {
		if !hasOriginSiteRef || len(sites) != 1 || originSiteRef != sites[0] {
			return nil, fail(ErrContractConflict, path+".originSiteRef", "single-Site selector requires one exact origin Site")
		}
		if _, exists := route["originSelection"]; exists {
			return nil, fail(ErrContractConflict, path+".originSelection", "single-Site selector forbids an origin selection policy")
		}
		result["originSiteRef"] = originSiteRef
	} else {
		if hasOriginSiteRef {
			return nil, fail(ErrContractConflict, path+".originSiteRef", "multi-Site selector forbids an invented primary Site")
		}
		selection, err := objectField(route, path, "originSelection")
		if err != nil {
			return nil, err
		}
		projectedSelection, err := projectPublicOriginSelection(selection, selector, sites, path+".originSelection")
		if err != nil {
			return nil, err
		}
		result["originSelection"] = projectedSelection
		if poolSelection, exists, err := optionalObjectField(pool, path+".backendPool", "originSelection"); err != nil {
			return nil, err
		} else if exists {
			equal, err := canonicalEqual(selection, poolSelection)
			if err != nil || !equal {
				return nil, fail(ErrContractConflict, path+".originSelection", "route and backend pool selection contracts differ")
			}
		}
	}
	for _, field := range []string{"port", "targetPort"} {
		value, err := intField(route, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = value
	}
	for _, field := range []string{"host", "path"} {
		if value, exists, err := optionalStringField(route, path, field); err != nil {
			return nil, err
		} else if exists {
			result[field] = value
		}
	}
	nodes, err := stringListField(route, path, "originNodeRefs", true)
	if err != nil {
		return nil, err
	}
	result["originNodeRefs"] = stringSliceAny(sortStringsUnique(nodes))
	publicPool, err := projectPublicBackendPool(pool, path+".backendPool")
	if err != nil {
		return nil, err
	}
	result["backendPool"] = publicPool
	if err := validatePublicRouteOriginSets(route, pool, path); err != nil {
		return nil, err
	}
	if probe != nil {
		result["healthProbe"] = probe
	}
	if withAuthority {
		authorities, err := projectPublicRouteCapabilityAuthorities(route, path)
		if err != nil {
			return nil, err
		}
		result["capabilityAuthorities"] = authorities
	}
	access, err := objectField(route, path, "access")
	if err != nil {
		return nil, err
	}
	result["access"], err = projectPublicRouteAccess(access, path+".access")
	if err != nil {
		return nil, err
	}
	tls, err := objectField(route, path, "tls")
	if err != nil {
		return nil, err
	}
	required, ok := tls["required"].(bool)
	if !ok {
		return nil, fail(ErrInvalidInput, path+".tls.required", "expected boolean")
	}
	mode, err := stringField(tls, path+".tls", "mode")
	if err != nil {
		return nil, err
	}
	publicTLS := map[string]any{"required": required, "mode": mode}
	if minVersion, exists, err := optionalStringField(tls, path+".tls", "minVersion"); err != nil {
		return nil, err
	} else if exists {
		publicTLS["minVersion"] = minVersion
	}
	for _, field := range []string{"profileRef", "issuerRef"} {
		if value, exists, err := optionalStringField(tls, path+".tls", field); err != nil {
			return nil, err
		} else if exists {
			publicTLS[field] = value
		}
	}
	if ownerCapabilityRef, exists, err := optionalStringField(tls, path+".tls", "ownerCapabilityRef"); err != nil {
		return nil, err
	} else if exists {
		publicTLS["ownerCapabilityRef"] = ownerCapabilityRef
	}
	result["tls"] = publicTLS
	return result, nil
}

func projectPublicRouteCapabilityAuthorities(route map[string]any, path string) ([]any, error) {
	requirements, err := routeCapabilityRequirementsFromProjection(route, path)
	if err != nil {
		return nil, err
	}
	seenRoles := make(map[string]struct{}, len(requirements))
	result := make([]any, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.role != "access" && requirement.role != "transport" && requirement.role != "edge" && requirement.role != "egress" {
			return nil, fail(ErrContractConflict, path+".capabilityRealizations", "unsupported route capability role %q", requirement.role)
		}
		if _, duplicate := seenRoles[requirement.role]; duplicate {
			return nil, fail(ErrContractConflict, path+".capabilityRealizations", "route capability role %q is duplicated", requirement.role)
		}
		seenRoles[requirement.role] = struct{}{}
		result = append(result, map[string]any{"capabilityRef": requirement.capabilityRef, "role": requirement.role})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["capabilityRef"].(string) < result[j].(map[string]any)["capabilityRef"].(string)
	})
	return result, nil
}

func projectPublicOriginSelection(selection map[string]any, selector string, originSiteRefs []string, path string) (map[string]any, error) {
	result := map[string]any{}
	thresholds := map[string]int{}
	for _, field := range []string{"minSites", "siteFailureDomainSpread", "nodeFailureDomainSpread"} {
		value, err := intField(selection, path, field)
		if err != nil || value < 1 {
			return nil, fail(ErrInvalidInput, path+"."+field, "expected positive integer")
		}
		result[field] = value
		thresholds[field] = value
	}
	stringLists := map[string][]string{}
	for _, field := range []string{"siteKinds", "requiredRoles", "siteFailureDomains"} {
		values, err := stringListField(selection, path, field, field != "requiredRoles")
		if err != nil {
			return nil, err
		}
		unique := sortStringsUnique(values)
		if len(unique) != len(values) {
			return nil, fail(ErrContractConflict, path+"."+field, "values must be unique")
		}
		stringLists[field] = unique
		result[field] = stringSliceAny(unique)
	}
	if len(stringLists["siteKinds"]) == 0 || len(originSiteRefs) < thresholds["minSites"] || len(stringLists["siteFailureDomains"]) < thresholds["siteFailureDomainSpread"] {
		return nil, fail(ErrContractConflict, path, "resolved Site selection is below its declared thresholds")
	}
	for _, kind := range stringLists["siteKinds"] {
		if kind != "home" && kind != "cloud" {
			return nil, fail(ErrContractConflict, path+".siteKinds", "unsupported Site kind %q", kind)
		}
	}
	if selector == "multi-zone" && (thresholds["nodeFailureDomainSpread"] < 2 || len(stringLists["requiredRoles"]) != 0) {
		return nil, fail(ErrContractConflict, path, "multi-zone requires node failure-domain spread >= 2 and no role filter")
	}
	if selector == "edge-pool" && !reflect.DeepEqual(stringLists["requiredRoles"], []string{"edge"}) {
		return nil, fail(ErrContractConflict, path+".requiredRoles", "edge-pool requires exactly the edge role")
	}
	nodeDomains, err := objectListField(selection, path, "nodeFailureDomains")
	if err != nil {
		return nil, err
	}
	projectedDomains := make([]any, 0, len(nodeDomains))
	seen := map[string]struct{}{}
	for index, domain := range nodeDomains {
		domainPath := fmt.Sprintf("%s.nodeFailureDomains[%d]", path, index)
		siteRef, err := stringField(domain, domainPath, "siteRef")
		if err != nil {
			return nil, err
		}
		failureDomain, err := stringField(domain, domainPath, "failureDomain")
		if err != nil {
			return nil, err
		}
		if !contains(originSiteRefs, siteRef) {
			return nil, fail(ErrContractConflict, domainPath+".siteRef", "node failure domain is outside the origin Site set")
		}
		key := siteRef + "\x00" + failureDomain
		if _, duplicate := seen[key]; duplicate {
			return nil, fail(ErrContractConflict, domainPath, "duplicate site-scoped node failure domain")
		}
		seen[key] = struct{}{}
		projectedDomains = append(projectedDomains, map[string]any{"siteRef": siteRef, "failureDomain": failureDomain})
	}
	if len(projectedDomains) < thresholds["nodeFailureDomainSpread"] {
		return nil, fail(ErrContractConflict, path+".nodeFailureDomains", "resolved node failure-domain evidence is below the declared spread")
	}
	sort.Slice(projectedDomains, func(i, j int) bool {
		left, right := projectedDomains[i].(map[string]any), projectedDomains[j].(map[string]any)
		if left["siteRef"] != right["siteRef"] {
			return left["siteRef"].(string) < right["siteRef"].(string)
		}
		return left["failureDomain"].(string) < right["failureDomain"].(string)
	})
	result["nodeFailureDomains"] = projectedDomains
	return result, nil
}

func validatePublicRouteOriginSets(route, pool map[string]any, path string) error {
	sites, err := stringListField(route, path, "originSiteRefs", true)
	if err != nil {
		return err
	}
	nodes, err := stringListField(route, path, "originNodeRefs", true)
	if err != nil {
		return err
	}
	members, err := objectListField(pool, path+".backendPool", "members")
	if err != nil {
		return err
	}
	memberSites, memberNodes, instances := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for index, member := range members {
		memberPath := fmt.Sprintf("%s.backendPool.members[%d]", path, index)
		siteRef, err := stringField(member, memberPath, "siteRef")
		if err != nil {
			return err
		}
		nodeRef, err := stringField(member, memberPath, "nodeRef")
		if err != nil {
			return err
		}
		instanceRef, err := stringField(member, memberPath, "instanceRef")
		if err != nil {
			return err
		}
		if _, duplicate := instances[instanceRef]; duplicate {
			return fail(ErrContractConflict, memberPath+".instanceRef", "backend instance is duplicated")
		}
		instances[instanceRef], memberSites[siteRef], memberNodes[nodeRef] = struct{}{}, struct{}{}, struct{}{}
	}
	if !reflect.DeepEqual(moduleInputStringSet(sites), memberSites) || !reflect.DeepEqual(moduleInputStringSet(nodes), memberNodes) {
		return fail(ErrContractConflict, path+".backendPool.members", "backend member Site and node sets must exactly equal the route origin sets")
	}
	return nil
}

func projectPublicRouteHealthProbe(input, route, pool map[string]any, path string, alreadyPublic bool) (map[string]any, error) {
	kind, err := stringField(input, path, "kind")
	if err != nil {
		return nil, err
	}
	protocol, err := stringField(input, path, "protocol")
	if err != nil {
		return nil, err
	}
	port, err := intField(input, path, "port")
	if err != nil {
		return nil, err
	}
	timeoutSeconds, err := intField(input, path, "timeoutSeconds")
	if err != nil {
		return nil, err
	}
	if timeoutSeconds < 1 || timeoutSeconds > 300 {
		return nil, fail(ErrContractConflict, path+".timeoutSeconds", "timeout must be between 1 and 300 seconds")
	}
	if !alreadyPublic {
		execution, err := stringField(input, path, "execution")
		if err != nil {
			return nil, err
		}
		if execution != "probe" {
			return nil, fail(ErrContractConflict, path+".execution", "v3 renderer input requires an executable probe descriptor")
		}
		routeID, err := stringField(route, path, "id")
		if err != nil {
			return nil, err
		}
		poolID, err := stringField(pool, path, "id")
		if err != nil {
			return nil, err
		}
		for field, expected := range map[string]string{"targetKind": "route", "targetRef": routeID, "routeRef": routeID, "backendPoolRef": poolID} {
			actual, err := stringField(input, path, field)
			if err != nil {
				return nil, err
			}
			if actual != expected {
				return nil, fail(ErrContractConflict, path+"."+field, "route health gate is not bound to the exact route and backend pool")
			}
		}
	}
	routeProtocol, err := stringField(route, path, "upstreamProtocol")
	if err != nil {
		return nil, err
	}
	poolProtocol, err := stringField(pool, path, "upstreamProtocol")
	if err != nil {
		return nil, err
	}
	routePort, err := intField(route, path, "targetPort")
	if err != nil {
		return nil, err
	}
	poolPort, err := intField(pool, path, "targetPort")
	if err != nil {
		return nil, err
	}
	if protocol != routeProtocol || protocol != poolProtocol || port != routePort || port != poolPort {
		return nil, fail(ErrContractConflict, path, "health probe protocol and port do not match the exact route backend pool")
	}
	result := map[string]any{"kind": kind, "protocol": protocol, "port": port, "timeoutSeconds": timeoutSeconds}
	switch kind {
	case "http":
		if protocol != "http" && protocol != "https" {
			return nil, fail(ErrContractConflict, path+".protocol", "http probe requires http or https")
		}
		method, err := stringField(input, path, "method")
		if err != nil || method != "GET" {
			return nil, fail(ErrContractConflict, path+".method", "http probe requires GET")
		}
		redirects, exists := input["followRedirects"]
		if !exists || redirects != false {
			return nil, fail(ErrContractConflict, path+".followRedirects", "http probe redirects must be disabled")
		}
		probePath, err := stringField(input, path, "path")
		if err != nil || !strings.HasPrefix(probePath, "/") {
			return nil, fail(ErrContractConflict, path+".path", "http probe path must be absolute")
		}
		statuses, exists := input["expectedStatuses"].([]any)
		if !exists || len(statuses) == 0 {
			return nil, fail(ErrContractConflict, path+".expectedStatuses", "http probe requires expected statuses")
		}
		seen := map[int]struct{}{}
		for index := range statuses {
			status, err := intField(map[string]any{"status": statuses[index]}, path+".expectedStatuses", "status")
			if err != nil || status < 100 || status > 599 {
				return nil, fail(ErrContractConflict, fmt.Sprintf("%s.expectedStatuses[%d]", path, index), "invalid HTTP status")
			}
			if _, duplicate := seen[status]; duplicate {
				return nil, fail(ErrContractConflict, path+".expectedStatuses", "statuses must be unique")
			}
			seen[status] = struct{}{}
		}
		result["method"], result["followRedirects"] = "GET", false
		result["path"], result["expectedStatuses"] = probePath, statuses
	case "tcp":
		if protocol != "tcp" {
			return nil, fail(ErrContractConflict, path+".protocol", "tcp probe requires tcp")
		}
	default:
		return nil, fail(ErrContractConflict, path+".kind", "v3 renderer input requires http or tcp probe")
	}
	return result, nil
}

func projectPublicBackendPool(pool map[string]any, path string) (map[string]any, error) {
	protocol, err := stringField(pool, path, "upstreamProtocol")
	if err != nil {
		return nil, err
	}
	port, err := intField(pool, path, "targetPort")
	if err != nil {
		return nil, err
	}
	members, err := objectListField(pool, path, "members")
	if err != nil {
		return nil, err
	}
	projectedMembers := make([]any, 0, len(members))
	for index, member := range members {
		memberPath := fmt.Sprintf("%s.members[%d]", path, index)
		projected := map[string]any{}
		for _, field := range []string{"siteRef", "nodeRef", "instanceRef"} {
			value, err := stringField(member, memberPath, field)
			if err != nil {
				return nil, err
			}
			projected[field] = value
		}
		projectedMembers = append(projectedMembers, projected)
	}
	return map[string]any{"upstreamProtocol": protocol, "targetPort": port, "members": projectedMembers}, nil
}

func projectPublicRouteAccess(access map[string]any, path string) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"exposure", "policyExposure", "authentication", "privilege", "policyRef"} {
		value, err := stringField(access, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = value
	}
	for _, field := range []string{"enrolledDeviceRequired", "ownerStepUpRequired", "lanStepDown", "defaultClosed"} {
		value, exists := access[field]
		boolean, ok := value.(bool)
		if !exists || !ok {
			return nil, fail(ErrInvalidInput, path+"."+field, "expected boolean")
		}
		result[field] = boolean
	}
	for _, field := range []string{"allowedSiteRefs", "allowedMethods"} {
		if _, exists := access[field]; !exists {
			continue
		}
		values, err := stringListField(access, path, field, false)
		if err != nil {
			return nil, err
		}
		result[field] = stringSliceAny(sortStringsUnique(values))
	}
	return result, nil
}

func moduleInputStringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
