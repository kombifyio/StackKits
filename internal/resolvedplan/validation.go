package resolvedplan

import (
	"bytes"
	"fmt"
	"sort"
)

type profileView struct {
	slug                    string
	version                 string
	allowedSiteKinds        []string
	requiredSiteKinds       []string
	minSites                int
	maxSites                int
	allowedModes            []string
	allowedAuthorityKinds   []string
	controlMemberSiteScope  string
	requiredCapabilities    []string
	defaultCapabilities     []string
	optionalCapabilities    []string
	forbiddenCapabilities   []string
	bridgeRequired          bool
	bridgeEdgeKinds         []string
	deviceRequired          bool
	evidence                []string
	partitionPolicy         map[string]any
	dataAuthority           string
	cloudCopyRequiresPolicy bool
	generation              map[string]any
	network                 map[string]any
	reachability            reachabilityView
	availabilityPolicies    map[string]availabilityPolicyView
}

type reachabilityView struct {
	allowedAccessExposures []string
	lanStepDownAllowed     bool
	routes                 map[string]routeReachabilityRule
}

type routeReachabilityRule struct {
	allowed              bool
	requiredCapabilities []string
	allowedOriginKinds   []string
}

type siteView struct {
	id     string
	kind   string
	object map[string]any
}

type nodeView struct {
	id             string
	siteRef        string
	roles          []string
	enabled        bool
	object         map[string]any
	siteKind       string
	inventoryFacts map[string]any
	runtimeDaemons map[string]runtimeDaemonFact
}

type runtimeDaemonFact struct {
	instanceRef string
	engine      string
	socketPath  string
}

type specView struct {
	stackID             string
	fleetRef            string
	kitVersion          string
	sites               []siteView
	nodes               []nodeView
	siteByID            map[string]siteView
	nodeByID            map[string]nodeView
	siteKinds           map[string]struct{}
	controlPlane        map[string]any
	authoritySiteRef    string
	controlMode         string
	availabilityEnabled bool
	capabilities        map[string]any
	addons              map[string]any
	access              map[string]any
	bridge              map[string]any
	availability        map[string]any
	deviceEnrollment    map[string]any
	partitionPolicy     map[string]any
	workloads           map[string]any
	modules             map[string]any
	routes              map[string]any
	data                map[string]any
	originalSpec        map[string]any
	originalDefinition  map[string]any
	originalInventory   map[string]any
	source              map[string]any
	install             map[string]any
	generation          map[string]any
	system              map[string]any
	storage             map[string]any
	network             map[string]any
	container           map[string]any
}

func validateInputs(input Input) (*profileView, *specView, error) {
	definition := map[string]any(input.Definition)
	spec := map[string]any(input.Spec)
	inventory := map[string]any(input.Inventory)
	if len(definition) == 0 || len(spec) == 0 || len(inventory) == 0 {
		return nil, nil, fail(ErrInvalidInput, "input", "definition, spec, and inventory are required")
	}
	for _, document := range []struct {
		path  string
		value map[string]any
	}{{"definition", definition}, {"spec", spec}, {"inventory", inventory}} {
		if err := validateSecretReferences(document.value, document.path, ""); err != nil {
			return nil, nil, err
		}
	}

	if err := requireDiscriminator(definition, "definition", "stackkit/v2alpha1", "KitDefinition"); err != nil {
		return nil, nil, err
	}
	if err := requireDiscriminator(spec, "spec", "stackkit/v2alpha1", "StackSpec"); err != nil {
		return nil, nil, err
	}
	schemaVersion, err := stringField(inventory, "inventory", "schemaVersion")
	if err != nil {
		return nil, nil, err
	}
	if schemaVersion != "stackkit.inventory/v1" {
		return nil, nil, fail(ErrInvalidInput, "inventory.schemaVersion", "unsupported schema %q", schemaVersion)
	}

	profile, err := parseProfile(definition)
	if err != nil {
		return nil, nil, err
	}
	view, err := parseSpec(profile, definition, spec, inventory)
	if err != nil {
		return nil, nil, err
	}
	return profile, view, nil
}

func requireDiscriminator(object map[string]any, path, apiVersion, kind string) error {
	actualAPI, err := stringField(object, path, "apiVersion")
	if err != nil {
		return err
	}
	actualKind, err := stringField(object, path, "kind")
	if err != nil {
		return err
	}
	if actualAPI != apiVersion || actualKind != kind {
		return fail(ErrInvalidInput, path, "expected %s %s, got %s %s", apiVersion, kind, actualAPI, actualKind)
	}
	return nil
}

func parseProfile(definition map[string]any) (*profileView, error) {
	contracts, err := readProfileContracts(definition)
	if err != nil {
		return nil, err
	}
	profile := &profileView{}
	for _, populate := range []func() error{
		func() error { return populateProfileTopology(profile, contracts) },
		func() error { return populateProfileCapabilities(profile, contracts.capabilities) },
		func() error { return populateProfilePolicies(profile, definition, contracts) },
	} {
		if err := populate(); err != nil {
			return nil, err
		}
	}
	return profile, nil
}

type profileContracts struct {
	metadata     map[string]any
	topology     map[string]any
	controlPlane map[string]any
	capabilities map[string]any
	bridge       map[string]any
	device       map[string]any
	dataDefaults map[string]any
	reachability map[string]any
}

func readProfileContracts(definition map[string]any) (profileContracts, error) {
	var contracts profileContracts
	var err error
	if contracts.metadata, err = objectField(definition, "definition", "metadata"); err != nil {
		return contracts, err
	}
	if contracts.topology, err = objectField(definition, "definition", "topology"); err != nil {
		return contracts, err
	}
	if contracts.controlPlane, err = objectField(contracts.topology, "definition.topology", "controlPlane"); err != nil {
		return contracts, err
	}
	if contracts.capabilities, err = objectField(definition, "definition", "capabilities"); err != nil {
		return contracts, err
	}
	if contracts.bridge, err = objectField(definition, "definition", "bridge"); err != nil {
		return contracts, err
	}
	if contracts.device, err = objectField(definition, "definition", "deviceEnrollment"); err != nil {
		return contracts, err
	}
	if contracts.dataDefaults, err = objectField(definition, "definition", "dataDefaults"); err != nil {
		return contracts, err
	}
	if contracts.reachability, err = objectField(definition, "definition", "reachability"); err != nil {
		return contracts, err
	}
	return contracts, nil
}

func populateProfileTopology(profile *profileView, contracts profileContracts) error {
	var err error
	if profile.slug, err = stringField(contracts.metadata, "definition.metadata", "slug"); err != nil {
		return err
	}
	if profile.version, err = stringField(contracts.metadata, "definition.metadata", "version"); err != nil {
		return err
	}
	if profile.allowedSiteKinds, err = stringListField(contracts.topology, "definition.topology", "allowedSiteKinds", true); err != nil {
		return err
	}
	if profile.requiredSiteKinds, err = stringListField(contracts.topology, "definition.topology", "requiredSiteKinds", true); err != nil {
		return err
	}
	if profile.minSites, err = intField(contracts.topology, "definition.topology", "minSites"); err != nil {
		return err
	}
	if profile.maxSites, err = intField(contracts.topology, "definition.topology", "maxSites"); err != nil {
		return err
	}
	if profile.allowedModes, err = stringListField(contracts.controlPlane, "definition.topology.controlPlane", "allowedModes", true); err != nil {
		return err
	}
	if profile.allowedAuthorityKinds, err = stringListField(contracts.controlPlane, "definition.topology.controlPlane", "allowedAuthorityKinds", true); err != nil {
		return err
	}
	profile.controlMemberSiteScope, _, err = optionalStringField(contracts.controlPlane, "definition.topology.controlPlane", "memberSiteScope")
	if err != nil {
		return err
	}
	if profile.controlMemberSiteScope == "" {
		profile.controlMemberSiteScope = "any"
	}
	if profile.controlMemberSiteScope != "any" && profile.controlMemberSiteScope != "authority-site" {
		return fail(ErrInvalidInput, "definition.topology.controlPlane.memberSiteScope", "unsupported member site scope %q", profile.controlMemberSiteScope)
	}
	return nil
}

func populateProfileCapabilities(profile *profileView, capabilities map[string]any) error {
	var err error
	if profile.requiredCapabilities, err = stringListField(capabilities, "definition.capabilities", "required", true); err != nil {
		return err
	}
	if profile.defaultCapabilities, err = stringListField(capabilities, "definition.capabilities", "defaults", false); err != nil {
		return err
	}
	if profile.optionalCapabilities, err = stringListField(capabilities, "definition.capabilities", "optional", false); err != nil {
		return err
	}
	profile.forbiddenCapabilities, err = stringListField(capabilities, "definition.capabilities", "forbidden", false)
	return err
}

func populateProfilePolicies(profile *profileView, definition map[string]any, contracts profileContracts) error {
	var err error
	if profile.bridgeRequired, err = boolFieldDefault(contracts.bridge, "definition.bridge", "required", false); err != nil {
		return err
	}
	if profile.bridgeEdgeKinds, err = stringListField(contracts.bridge, "definition.bridge", "edgeKinds", false); err != nil {
		return err
	}
	if profile.deviceRequired, err = boolFieldDefault(contracts.device, "definition.deviceEnrollment", "required", false); err != nil {
		return err
	}
	if profile.evidence, err = stringListField(definition, "definition", "evidenceScenarios", true); err != nil {
		return err
	}
	if profile.partitionPolicy, err = objectField(definition, "definition", "partitionPolicy"); err != nil {
		return err
	}
	if profile.dataAuthority, err = stringField(contracts.dataDefaults, "definition.dataDefaults", "authority"); err != nil {
		return err
	}
	if profile.cloudCopyRequiresPolicy, err = requiredBoolField(contracts.dataDefaults, "definition.dataDefaults", "cloudCopyRequiresPolicy"); err != nil {
		return err
	}
	if profile.generation, err = objectField(definition, "definition", "generation"); err != nil {
		return err
	}
	if profile.network, err = objectField(definition, "definition", "network"); err != nil {
		return err
	}
	if profile.availabilityPolicies, err = parseAvailabilityPolicies(definition); err != nil {
		return err
	}
	profile.reachability, err = parseReachabilityContract(contracts.reachability)
	return err
}

func parseReachabilityContract(contract map[string]any) (reachabilityView, error) {
	var result reachabilityView
	accessPolicies, err := objectField(contract, "definition.reachability", "accessPolicies")
	if err != nil {
		return result, err
	}
	result.allowedAccessExposures, err = stringListField(accessPolicies, "definition.reachability.accessPolicies", "allowedExposures", true)
	if err != nil {
		return result, err
	}
	if len(result.allowedAccessExposures) == 0 {
		return result, fail(ErrInvalidInput, "definition.reachability.accessPolicies.allowedExposures", "at least one access exposure is required")
	}
	result.lanStepDownAllowed, err = requiredBoolField(accessPolicies, "definition.reachability.accessPolicies", "lanStepDownAllowed")
	if err != nil {
		return result, err
	}

	routes, err := objectField(contract, "definition.reachability", "routes")
	if err != nil {
		return result, err
	}
	expectedExposures := []string{"local", "public", "remote-private"}
	actualExposures := sortedStringMapKeys(routes)
	if !equalStrings(actualExposures, expectedExposures) {
		return result, fail(ErrInvalidInput, "definition.reachability.routes", "expected exactly route rules %v, got %v", expectedExposures, actualExposures)
	}
	result.routes = make(map[string]routeReachabilityRule, len(expectedExposures))
	for _, exposure := range expectedExposures {
		path := "definition.reachability.routes." + exposure
		rawRule, err := objectField(routes, "definition.reachability.routes", exposure)
		if err != nil {
			return result, err
		}
		rule := routeReachabilityRule{}
		if rule.allowed, err = requiredBoolField(rawRule, path, "allowed"); err != nil {
			return result, err
		}
		if rule.requiredCapabilities, err = stringListField(rawRule, path, "requiredCapabilities", true); err != nil {
			return result, err
		}
		if rule.allowedOriginKinds, err = stringListField(rawRule, path, "allowedOriginKinds", true); err != nil {
			return result, err
		}
		if rule.allowed && len(rule.allowedOriginKinds) == 0 {
			return result, fail(ErrInvalidInput, path+".allowedOriginKinds", "an allowed route exposure requires at least one origin kind")
		}
		if !rule.allowed && (len(rule.requiredCapabilities) != 0 || len(rule.allowedOriginKinds) != 0) {
			return result, fail(ErrInvalidInput, path, "a denied route exposure cannot declare capabilities or origin kinds")
		}
		result.routes[exposure] = rule
	}
	return result, nil
}

func requiredBoolField(object map[string]any, path, name string) (bool, error) {
	if _, exists := object[name]; !exists {
		return false, fail(ErrInvalidInput, joinPath(path, name), "required boolean is missing")
	}
	return boolFieldDefault(object, path, name, false)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func parseSpec(profile *profileView, definition, spec, inventory map[string]any) (*specView, error) {
	identity, err := parseSpecIdentity(profile, spec)
	if err != nil {
		return nil, err
	}
	siteObjects, err := objectListField(spec, "spec", "sites")
	if err != nil {
		return nil, err
	}
	if len(siteObjects) < profile.minSites || len(siteObjects) > profile.maxSites {
		return nil, fail(ErrProfileMismatch, "spec.sites", "profile requires %d..%d sites, got %d", profile.minSites, profile.maxSites, len(siteObjects))
	}
	view := &specView{
		stackID: identity.stackID, fleetRef: identity.fleetRef, kitVersion: identity.kitVersion,
		siteByID: make(map[string]siteView, len(siteObjects)),
		nodeByID: make(map[string]nodeView), siteKinds: make(map[string]struct{}),
		originalSpec: spec, originalDefinition: definition, originalInventory: inventory,
	}
	if err := populateSpecRuntime(profile, spec, view); err != nil {
		return nil, err
	}
	if err := populateSpecSites(profile, view, siteObjects); err != nil {
		return nil, err
	}

	nodeObjects, err := objectListField(spec, "spec", "nodes")
	if err != nil {
		return nil, err
	}
	if err := populateSpecNodes(view, nodeObjects); err != nil {
		return nil, err
	}
	if err := populateSpecContracts(profile, spec, view); err != nil {
		return nil, err
	}
	if err := validateInventory(view, inventory); err != nil {
		return nil, err
	}
	sort.Slice(view.sites, func(i, j int) bool { return view.sites[i].id < view.sites[j].id })
	sort.Slice(view.nodes, func(i, j int) bool { return view.nodes[i].id < view.nodes[j].id })
	return view, nil
}

type specIdentity struct {
	stackID    string
	fleetRef   string
	kitVersion string
}

func parseSpecIdentity(profile *profileView, spec map[string]any) (specIdentity, error) {
	var identity specIdentity
	kit, err := objectField(spec, "spec", "kit")
	if err != nil {
		return identity, err
	}
	slug, err := stringField(kit, "spec.kit", "slug")
	if err != nil {
		return identity, err
	}
	if slug != profile.slug {
		return identity, fail(ErrProfileMismatch, "spec.kit.slug", "spec selects %q but definition is %q", slug, profile.slug)
	}
	var hasVersion bool
	identity.kitVersion, hasVersion, err = optionalStringField(kit, "spec.kit", "version")
	if err != nil {
		return identity, err
	}
	if hasVersion && identity.kitVersion != profile.version {
		return identity, fail(ErrProfileMismatch, "spec.kit.version", "spec requests %q but definition is %q", identity.kitVersion, profile.version)
	}
	metadata, err := objectField(spec, "spec", "metadata")
	if err != nil {
		return identity, err
	}
	identity.stackID, hasVersion, err = optionalStringField(metadata, "spec.metadata", "stackId")
	if err != nil {
		return identity, err
	}
	if !hasVersion {
		identity.stackID, err = stringField(metadata, "spec.metadata", "name")
		if err != nil {
			return identity, err
		}
	}
	identity.fleetRef, _, err = optionalStringField(metadata, "spec.metadata", "fleetRef")
	return identity, err
}

func populateSpecRuntime(profile *profileView, spec map[string]any, view *specView) error {
	var err error
	if view.source, err = objectField(spec, "spec", "source"); err != nil {
		return err
	}
	if view.install, err = objectField(spec, "spec", "install"); err != nil {
		return err
	}
	if view.generation, err = objectField(spec, "spec", "generation"); err != nil {
		return err
	}
	if err := validateGenerationSelection(profile, view.generation); err != nil {
		return err
	}
	if view.system, err = objectField(spec, "spec", "system"); err != nil {
		return err
	}
	if view.storage, err = objectField(spec, "spec", "storage"); err != nil {
		return err
	}
	if view.network, err = objectField(spec, "spec", "network"); err != nil {
		return err
	}
	if err := validateNetworkSelection(profile, view.network); err != nil {
		return err
	}
	view.container, _, err = optionalObjectField(spec, "spec", "container")
	return err
}

func validateGenerationSelection(profile *profileView, generation map[string]any) error {
	strategy, err := stringField(generation, "spec.generation", "strategy")
	if err != nil {
		return err
	}
	target, err := stringField(generation, "spec.generation", "target")
	if err != nil {
		return err
	}
	allowedStrategies, err := stringListField(profile.generation, "definition.generation", "allowedStrategies", true)
	if err != nil {
		return err
	}
	allowedTargets, err := stringListField(profile.generation, "definition.generation", "allowedTargets", true)
	if err != nil {
		return err
	}
	if !contains(allowedStrategies, strategy) || !contains(allowedTargets, target) {
		return fail(ErrProfileMismatch, "spec.generation", "strategy %q and target %q are not allowed by %s", strategy, target, profile.slug)
	}
	return nil
}

func validateNetworkSelection(profile *profileView, network map[string]any) error {
	requestedMode, err := stringField(network, "spec.network", "mode")
	if err != nil {
		return err
	}
	profileMode, err := stringField(profile.network, "definition.network", "mode")
	if err != nil {
		return err
	}
	if requestedMode != profileMode {
		return fail(ErrProfileMismatch, "spec.network.mode", "network mode %q does not match %s mode %q", requestedMode, profile.slug, profileMode)
	}
	return nil
}

func populateSpecSites(profile *profileView, view *specView, siteObjects []map[string]any) error {
	for i, object := range siteObjects {
		path := fmt.Sprintf("spec.sites[%d]", i)
		id, err := stringField(object, path, "id")
		if err != nil {
			return err
		}
		kind, err := stringField(object, path, "kind")
		if err != nil {
			return err
		}
		if _, exists := view.siteByID[id]; exists {
			return fail(ErrInvalidInput, path+".id", "duplicate site %q", id)
		}
		if !contains(profile.allowedSiteKinds, kind) {
			return fail(ErrProfileMismatch, path+".kind", "site kind %q is not allowed by %s", kind, profile.slug)
		}
		site := siteView{id: id, kind: kind, object: object}
		view.sites = append(view.sites, site)
		view.siteByID[id] = site
		view.siteKinds[kind] = struct{}{}
	}
	for _, required := range profile.requiredSiteKinds {
		if _, exists := view.siteKinds[required]; !exists {
			return fail(ErrProfileMismatch, "spec.sites", "profile requires a %q site", required)
		}
	}
	return nil
}

func populateSpecNodes(view *specView, nodeObjects []map[string]any) error {
	for i, object := range nodeObjects {
		path := fmt.Sprintf("spec.nodes[%d]", i)
		id, err := stringField(object, path, "id")
		if err != nil {
			return err
		}
		if _, exists := view.nodeByID[id]; exists {
			return fail(ErrInvalidInput, path+".id", "duplicate node %q", id)
		}
		siteRef, err := stringField(object, path, "siteRef")
		if err != nil {
			return err
		}
		site, exists := view.siteByID[siteRef]
		if !exists {
			return fail(ErrProfileMismatch, path+".siteRef", "unknown site %q", siteRef)
		}
		roles, err := stringListField(object, path, "roles", true)
		if err != nil {
			return err
		}
		enabled, err := boolFieldDefault(object, path, "enabled", true)
		if err != nil {
			return err
		}
		node := nodeView{
			id: id, siteRef: siteRef, roles: roles, enabled: enabled, object: object, siteKind: site.kind,
			inventoryFacts: make(map[string]any),
			runtimeDaemons: make(map[string]runtimeDaemonFact),
		}
		view.nodes = append(view.nodes, node)
		view.nodeByID[id] = node
	}
	return nil
}

func populateSpecContracts(profile *profileView, spec map[string]any, view *specView) error {
	if err := parseControlPlane(profile, spec, view); err != nil {
		return err
	}
	if err := populateSpecAccessContracts(profile, spec, view); err != nil {
		return err
	}
	if err := populateSpecPolicyContracts(profile, spec, view); err != nil {
		return err
	}
	return populateSpecWorkloadContracts(profile, spec, view)
}

func populateSpecAccessContracts(profile *profileView, spec map[string]any, view *specView) error {
	var err error
	if view.capabilities, err = objectField(spec, "spec", "capabilities"); err != nil {
		return err
	}
	if view.addons, _, err = optionalObjectField(spec, "spec", "addons"); err != nil {
		return err
	}
	if view.access, _, err = optionalObjectField(spec, "spec", "access"); err != nil {
		return err
	}
	if err := validateAccessPolicies(profile, view.access); err != nil {
		return err
	}
	if view.bridge, _, err = optionalObjectField(spec, "spec", "bridge"); err != nil {
		return err
	}
	if profile.bridgeRequired && view.bridge == nil {
		return fail(ErrProfileMismatch, "spec.bridge", "%s requires an explicit bridge contract", profile.slug)
	}
	if view.availability, err = objectField(spec, "spec", "availability"); err != nil {
		return err
	}
	view.availabilityEnabled, err = boolFieldDefault(view.availability, "spec.availability", "enabled", false)
	return err
}

func validateAccessPolicies(profile *profileView, policies map[string]any) error {
	for _, policyID := range sortedStringMapKeys(policies) {
		path := "spec.access." + policyID
		policy, err := asObject(policies[policyID], path)
		if err != nil {
			return err
		}
		exposure, err := stringField(policy, path, "exposure")
		if err != nil {
			return err
		}
		if !contains(profile.reachability.allowedAccessExposures, exposure) {
			return fail(ErrProfileMismatch, path+".exposure", "access exposure %q is not allowed by the kit reachability contract", exposure)
		}
		lanStepDown, err := boolFieldDefault(policy, path, "lanStepDown", false)
		if err != nil {
			return err
		}
		if lanStepDown && !profile.reachability.lanStepDownAllowed {
			return fail(ErrProfileMismatch, path+".lanStepDown", "LAN step-down is not allowed by the kit reachability contract")
		}
	}
	return nil
}

func populateSpecPolicyContracts(profile *profileView, spec map[string]any, view *specView) error {
	var err error
	if view.deviceEnrollment, _, err = optionalObjectField(spec, "spec", "deviceEnrollment"); err != nil {
		return err
	}
	if profile.deviceRequired != (view.deviceEnrollment != nil) {
		return fail(ErrProfileMismatch, "spec.deviceEnrollment", "device enrollment presence does not match %s", profile.slug)
	}
	if view.partitionPolicy, err = objectField(spec, "spec", "partitionPolicy"); err != nil {
		return err
	}
	equal, err := jsonEqual(view.partitionPolicy, profile.partitionPolicy)
	if err != nil {
		return err
	}
	if !equal {
		return fail(ErrProfileMismatch, "spec.partitionPolicy", "partition policy must equal the selected kit definition")
	}
	return nil
}

func populateSpecWorkloadContracts(profile *profileView, spec map[string]any, view *specView) error {
	var err error
	if view.workloads, _, err = optionalObjectField(spec, "spec", "workloads"); err != nil {
		return err
	}
	if view.modules, _, err = optionalObjectField(spec, "spec", "modules"); err != nil {
		return err
	}
	if view.routes, _, err = optionalObjectField(spec, "spec", "routes"); err != nil {
		return err
	}
	if view.data, _, err = optionalObjectField(spec, "spec", "data"); err != nil {
		return err
	}
	if view.data == nil && profile.dataAuthority == "policy" {
		return fail(ErrProfileMismatch, "spec.data", "profile requires explicit per-workload data authority")
	}
	if view.data != nil {
		if err := validateDefinitionDataPolicy(profile, view); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocyclo // Data-policy validation is an explicit kit-specific invariant matrix and must report every invalid combination.
func validateDefinitionDataPolicy(profile *profileView, view *specView) error {
	defaultAuthority, err := stringField(view.data, "spec.data", "defaultAuthority")
	if err != nil {
		return err
	}
	wantAuthority := profile.dataAuthority
	if wantAuthority == "policy" {
		wantAuthority = "per-workload"
	}
	if defaultAuthority != wantAuthority {
		return fail(ErrProfileMismatch, "spec.data.defaultAuthority", "data authority %q does not match %s definition authority %q", defaultAuthority, profile.slug, wantAuthority)
	}
	// Cloud Kit's definition is itself the authority for native cloud data.
	// The typed copy policy below protects a Home-authoritative definition from
	// silently turning a cloud edge into a primary, replica, or future copy target.
	if !profile.cloudCopyRequiresPolicy || profile.dataAuthority != "home" {
		return nil
	}
	bindings, exists, err := optionalObjectField(view.data, "spec.data", "bindings")
	if err != nil || !exists {
		return err
	}
	for _, bindingID := range sortedStringMapKeys(bindings) {
		path := "spec.data.bindings." + bindingID
		binding, err := asObject(bindings[bindingID], path)
		if err != nil {
			return err
		}
		classes, err := stringListField(binding, path, "classes", true)
		if err != nil {
			return err
		}
		primarySiteRef, err := stringField(binding, path, "primarySiteRef")
		if err != nil {
			return err
		}
		primarySite, primaryExists := view.siteByID[primarySiteRef]
		if !primaryExists {
			return fail(ErrProfileMismatch, path+".primarySiteRef", "unknown site %q", primarySiteRef)
		}
		replicaSiteRefs, err := stringListField(binding, path, "replicaSiteRefs", false)
		if err != nil {
			return err
		}
		cloudReplica := false
		for index, replicaSiteRef := range replicaSiteRefs {
			replicaSite, replicaExists := view.siteByID[replicaSiteRef]
			if !replicaExists {
				return fail(ErrProfileMismatch, fmt.Sprintf("%s.replicaSiteRefs[%d]", path, index), "unknown site %q", replicaSiteRef)
			}
			cloudReplica = cloudReplica || replicaSite.kind == "cloud"
		}
		cloudPrimary := primarySite.kind == "cloud"
		cloudCopyAllowed, err := boolFieldDefault(binding, path, "cloudCopyAllowed", false)
		if err != nil {
			return err
		}
		if (cloudPrimary || cloudReplica) && !cloudCopyAllowed {
			return fail(ErrProfileMismatch, path+".cloudCopyAllowed", "cloud primary or replica placement requires an explicit cloud-copy opt-in")
		}
		if !cloudCopyAllowed && !cloudPrimary && !cloudReplica {
			continue
		}
		policy, policyExists, err := optionalObjectField(binding, path, "cloudCopyPolicy")
		if err != nil {
			return err
		}
		if !policyExists {
			return fail(ErrProfileMismatch, path+".cloudCopyPolicy", "cloud copy, primary, or replica placement requires a typed policy")
		}
		if _, err := stringField(policy, path+".cloudCopyPolicy", "policyRef"); err != nil {
			return err
		}
		allowedClasses, err := stringListField(policy, path+".cloudCopyPolicy", "allowedClasses", true)
		if err != nil {
			return err
		}
		for _, class := range classes {
			if !contains(allowedClasses, class) {
				return fail(ErrProfileMismatch, path+".cloudCopyPolicy.allowedClasses", "data class %q is not approved for cloud copy", class)
			}
		}
		if cloudPrimary {
			allowed, err := boolFieldDefault(policy, path+".cloudCopyPolicy", "allowPrimary", false)
			if err != nil {
				return err
			}
			if !allowed {
				return fail(ErrProfileMismatch, path+".cloudCopyPolicy.allowPrimary", "cloud primary placement is not approved")
			}
		}
		if cloudReplica {
			allowed, err := boolFieldDefault(policy, path+".cloudCopyPolicy", "allowReplicas", false)
			if err != nil {
				return err
			}
			if !allowed {
				return fail(ErrProfileMismatch, path+".cloudCopyPolicy.allowReplicas", "cloud replica placement is not approved")
			}
		}
	}
	return nil
}

func parseControlPlane(profile *profileView, spec map[string]any, view *specView) error {
	controlPlane, err := objectField(spec, "spec", "controlPlane")
	if err != nil {
		return err
	}
	mode, err := stringField(controlPlane, "spec.controlPlane", "mode")
	if err != nil {
		return err
	}
	if !contains(profile.allowedModes, mode) {
		return fail(ErrProfileMismatch, "spec.controlPlane.mode", "%q is not allowed by %s", mode, profile.slug)
	}
	authority, err := stringField(controlPlane, "spec.controlPlane", "authoritySiteRef")
	if err != nil {
		return err
	}
	authoritySite, exists := view.siteByID[authority]
	if !exists || !contains(profile.allowedAuthorityKinds, authoritySite.kind) {
		return fail(ErrProfileMismatch, "spec.controlPlane.authoritySiteRef", "site %q is not an allowed authority", authority)
	}
	members, err := stringListField(controlPlane, "spec.controlPlane", "members", true)
	if err != nil {
		return err
	}
	for i, member := range members {
		node, exists := view.nodeByID[member]
		if !exists || !node.enabled || !contains(node.roles, "controller") {
			return fail(ErrProfileMismatch, fmt.Sprintf("spec.controlPlane.members[%d]", i), "%q is not an enabled controller", member)
		}
		if profile.controlMemberSiteScope == "authority-site" && node.siteRef != authority {
			return fail(ErrProfileMismatch, fmt.Sprintf("spec.controlPlane.members[%d]", i), "%q belongs to site %q; %s requires every control-plane member at authority site %q", member, node.siteRef, profile.slug, authority)
		}
	}
	view.controlPlane = controlPlane
	view.authoritySiteRef = authority
	view.controlMode = mode
	return nil
}

func validateInventory(view *specView, inventory map[string]any) error {
	nodes, err := objectField(inventory, "inventory", "nodes")
	if err != nil {
		return err
	}
	for id, rawFacts := range nodes {
		node, exists := view.nodeByID[id]
		if !exists {
			return fail(ErrProfileMismatch, "inventory.nodes."+id, "inventory contains a node absent from the spec")
		}
		facts, err := asObject(rawFacts, "inventory.nodes."+id)
		if err != nil {
			return err
		}
		for key, value := range facts {
			node.inventoryFacts[key] = value
		}
		observed, ok, err := optionalStringField(facts, "inventory.nodes."+id, "observedSiteKind")
		if err != nil {
			return err
		}
		if ok && observed != node.siteKind {
			return fail(ErrProfileMismatch, "inventory.nodes."+id+".observedSiteKind", "observed %q but spec binds node to %q", observed, node.siteKind)
		}
		runtimeDaemons, exists, err := optionalObjectField(facts, "inventory.nodes."+id, "runtimeDaemons")
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		for daemonRef, rawDaemon := range runtimeDaemons {
			daemonPath := "inventory.nodes." + id + ".runtimeDaemons." + daemonRef
			daemon, err := asObject(rawDaemon, daemonPath)
			if err != nil {
				return err
			}
			instanceRef, err := stringField(daemon, daemonPath, "instanceRef")
			if err != nil {
				return err
			}
			engine, err := stringField(daemon, daemonPath, "engine")
			if err != nil {
				return err
			}
			socketPath, err := stringField(daemon, daemonPath, "socketPath")
			if err != nil {
				return err
			}
			if err := validateUnixSocketPath(socketPath, daemonPath+".socketPath", ErrInvalidInput); err != nil {
				return err
			}
			node.runtimeDaemons[daemonRef] = runtimeDaemonFact{instanceRef: instanceRef, engine: engine, socketPath: socketPath}
		}
	}
	return nil
}

func jsonEqual(left, right any) (bool, error) {
	leftJSON, err := canonicalJSON(left, false)
	if err != nil {
		return false, err
	}
	rightJSON, err := canonicalJSON(right, false)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}
