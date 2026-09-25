package generationartifact

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	// ApplyExecutionRoleAuthority is the Foundation Node of a StackInstance
	// whose certified member hosts execute their own runtime targets.
	ApplyExecutionRoleAuthority = "authority"
	// ApplyExecutionRoleMember is a joined member host executing only its own
	// Site/node/execution-channel tuple.
	ApplyExecutionRoleMember = "member"
)

// ApplyExecutionScopeBinding is one exact Site/node/execution-channel tuple.
type ApplyExecutionScopeBinding struct {
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	ExecutionChannelRef string `json:"executionChannelRef"`
}

// ApplyExecutionScope selects the runtime targets one host executes from a
// multi-host plan. A target whose owner is in DispatchedOwnerRefs runs only
// through the authority's execution channel dispatch (remote-only process
// owners); every other target runs on the host whose tuple it names. The
// authority lists the certified Members whose targets it leaves to them, so a
// target that belongs to no host fails closed instead of silently vanishing.
type ApplyExecutionScope struct {
	Role                string                       `json:"role"`
	StackID             string                       `json:"stackId"`
	Local               ApplyExecutionScopeBinding   `json:"local"`
	DispatchedOwnerRefs []string                     `json:"dispatchedOwnerRefs"`
	Members             []ApplyExecutionScopeBinding `json:"members,omitempty"`
}

func (s *ApplyExecutionScope) clone() *ApplyExecutionScope {
	if s == nil {
		return nil
	}
	result := *s
	result.DispatchedOwnerRefs = append([]string(nil), s.DispatchedOwnerRefs...)
	result.Members = append([]ApplyExecutionScopeBinding(nil), s.Members...)
	return &result
}

// Includes reports whether this scope executes one runtime requirement.
func (s ApplyExecutionScope) Includes(requirement ApplyRuntimeRequirement) bool {
	if len(requirement.SiteRefs) != 1 || len(requirement.NodeRefs) != 1 {
		return false
	}
	if containsSortedString(s.DispatchedOwnerRefs, requirement.OwnerRef) {
		return s.Role == ApplyExecutionRoleAuthority
	}
	return requirement.SiteRefs[0] == s.Local.SiteRef && requirement.NodeRefs[0] == s.Local.NodeRef
}

// Validate checks the closed scope shape. Plan-dependent rules are checked by
// VerifiedPlan.WithExecutionScope.
func (s ApplyExecutionScope) Validate() error {
	if s.Role != ApplyExecutionRoleAuthority && s.Role != ApplyExecutionRoleMember {
		return fail(ErrInvalidContract, "applyExecutionScope.role", "must be %q or %q", ApplyExecutionRoleAuthority, ApplyExecutionRoleMember)
	}
	if !canonicalScopeToken(s.StackID) {
		return fail(ErrInvalidContract, "applyExecutionScope.stackId", "is required")
	}
	if err := s.Local.validate("applyExecutionScope.local"); err != nil {
		return err
	}
	if !sort.StringsAreSorted(s.DispatchedOwnerRefs) {
		return fail(ErrInvalidContract, "applyExecutionScope.dispatchedOwnerRefs", "must be sorted")
	}
	for index, ref := range s.DispatchedOwnerRefs {
		if !canonicalScopeToken(ref) || (index > 0 && s.DispatchedOwnerRefs[index-1] == ref) {
			return fail(ErrInvalidContract, "applyExecutionScope.dispatchedOwnerRefs", "must be unique canonical owner refs")
		}
	}
	if s.Role == ApplyExecutionRoleMember && len(s.Members) != 0 {
		return fail(ErrInvalidContract, "applyExecutionScope.members", "a member scope names no other members")
	}
	if s.Role == ApplyExecutionRoleAuthority && len(s.Members) == 0 {
		return fail(ErrInvalidContract, "applyExecutionScope.members", "an authority scope requires at least one certified member")
	}
	seen := map[[2]string]struct{}{{s.Local.SiteRef, s.Local.NodeRef}: {}}
	for index, member := range s.Members {
		if err := member.validate("applyExecutionScope.members"); err != nil {
			return err
		}
		key := [2]string{member.SiteRef, member.NodeRef}
		if _, duplicate := seen[key]; duplicate {
			return fail(ErrInvalidContract, "applyExecutionScope.members", "tuple %s/%s is named more than once", member.SiteRef, member.NodeRef)
		}
		seen[key] = struct{}{}
		if index > 0 && !scopeBindingLess(s.Members[index-1], member) {
			return fail(ErrInvalidContract, "applyExecutionScope.members", "must be sorted by Site and node")
		}
	}
	return nil
}

// ExecutionScope returns the scope of a host-scoped plan, or nil.
func (p VerifiedPlan) ExecutionScope() *ApplyExecutionScope {
	return p.applyRequirements.ExecutionScope.clone()
}

// WithExecutionScope returns the host-scoped projection of this verified plan.
// The plan bytes, binding, and generated artifact set are unchanged; runtime
// targets, their health gates, local host facts, and the secrets and workloads
// they own are narrowed to the targets this host executes. Evidence, execution,
// recovery, and result verification all derive from the projection, so a
// member signs host evidence only for itself.
//
//nolint:gocyclo // One closed projection rule set is easier to audit in one place.
func (p VerifiedPlan) WithExecutionScope(scope ApplyExecutionScope) (VerifiedPlan, error) {
	if p.applyRequirements.ExecutionScope != nil {
		return VerifiedPlan{}, fail(ErrInvalidContract, "applyExecutionScope", "the plan is already host-scoped")
	}
	if err := scope.Validate(); err != nil {
		return VerifiedPlan{}, err
	}
	var identity struct {
		StackID string `json:"stackId"`
	}
	if err := json.Unmarshal(p.canonical, &identity); err != nil || identity.StackID != scope.StackID {
		return VerifiedPlan{}, fail(ErrBindingMismatch, "applyExecutionScope.stackId", "does not name this StackInstance")
	}
	source := p.applyRequirements
	localHost := false
	for _, host := range source.Hosts {
		if host.NodeRef == scope.Local.NodeRef && host.SiteRef == scope.Local.SiteRef {
			localHost = true
		}
	}
	if !localHost {
		return VerifiedPlan{}, fail(ErrBindingMismatch, "applyExecutionScope.local", "%s/%s is not a host of this plan", scope.Local.SiteRef, scope.Local.NodeRef)
	}
	members := make(map[[2]string]struct{}, len(scope.Members))
	for _, member := range scope.Members {
		members[[2]string{member.SiteRef, member.NodeRef}] = struct{}{}
	}

	scoped := cloneApplyRequirements(source)
	scoped.RuntimeInstances = scoped.RuntimeInstances[:0]
	kept := map[string]ApplyRuntimeRequirement{}
	workloads := map[string]struct{}{}
	providerNodes := map[string][][2]string{}
	for _, requirement := range cloneApplyRequirements(source).RuntimeInstances {
		if len(requirement.SiteRefs) != 1 || len(requirement.NodeRefs) != 1 {
			return VerifiedPlan{}, fail(ErrInvalidContract, "applyExecutionScope.runtimeInstances", "runtime target %q does not name exactly one Site/node", requirement.ID)
		}
		if !scope.Includes(requirement) {
			if scope.Role == ApplyExecutionRoleAuthority {
				if _, member := members[[2]string{requirement.SiteRefs[0], requirement.NodeRefs[0]}]; !member {
					return VerifiedPlan{}, fail(ErrBindingMismatch, "applyExecutionScope.members",
						"runtime target %q on %s/%s belongs to no certified member", requirement.ID, requirement.SiteRefs[0], requirement.NodeRefs[0])
				}
			}
			continue
		}
		scoped.RuntimeInstances = append(scoped.RuntimeInstances, requirement)
		kept[requirement.ID] = requirement
		if requirement.WorkloadRef != "" {
			workloads[requirement.WorkloadRef] = struct{}{}
		}
		if requirement.OwnerKind == "provider-owner" {
			providerNodes[requirement.OwnerRef] = append(providerNodes[requirement.OwnerRef], [2]string{requirement.SiteRefs[0], requirement.NodeRefs[0]})
		}
	}
	if len(kept) == 0 {
		return VerifiedPlan{}, fail(ErrExecutorMissing, "applyExecutionScope", "no runtime target of this plan runs on %s/%s", scope.Local.SiteRef, scope.Local.NodeRef)
	}

	scoped.HealthRequirements = scoped.HealthRequirements[:0]
	keptHealth := map[string]struct{}{}
	for _, health := range cloneApplyRequirements(source).HealthRequirements {
		for _, requirement := range kept {
			if scopedHealthTargetsRuntime(health, requirement) {
				scoped.HealthRequirements = append(scoped.HealthRequirements, health)
				keptHealth[health.ID] = struct{}{}
				break
			}
		}
	}

	scoped.Hosts = scoped.Hosts[:0]
	for _, host := range source.Hosts {
		if host.NodeRef == scope.Local.NodeRef {
			scoped.Hosts = append(scoped.Hosts, host)
		}
	}

	scoped.Workloads = scoped.Workloads[:0]
	for _, workload := range cloneApplyRequirements(source).Workloads {
		if _, ok := workloads[workload.ID]; ok {
			scoped.Workloads = append(scoped.Workloads, workload)
		}
	}

	scoped.Secrets = scoped.Secrets[:0]
	type secretSource struct{ kind, ref, input string }
	boundSources := map[secretSource]bool{}
	keptSources := map[secretSource]bool{}
	for _, secret := range source.Secrets {
		if len(secret.NodeRefs) == 0 {
			continue
		}
		key := secretSource{secret.SourceKind, secret.SourceRef, secret.SourceInputRef}
		boundSources[key] = true
		if containsString(secret.NodeRefs, scope.Local.NodeRef) {
			keptSources[key] = true
		}
	}
	for _, secret := range cloneApplyRequirements(source).Secrets {
		key := secretSource{secret.SourceKind, secret.SourceRef, secret.SourceInputRef}
		switch {
		case len(secret.NodeRefs) != 0 && containsString(secret.NodeRefs, scope.Local.NodeRef):
			scoped.Secrets = append(scoped.Secrets, secret)
		case len(secret.NodeRefs) == 0 && (keptSources[key] || (!boundSources[key] && scope.Role == ApplyExecutionRoleAuthority)):
			scoped.Secrets = append(scoped.Secrets, secret)
		}
	}

	scoped.ProviderOwners = scoped.ProviderOwners[:0]
	for _, owner := range cloneApplyRequirements(source).ProviderOwners {
		placements := providerNodes[owner.Ref]
		if len(placements) == 0 {
			continue
		}
		sites, nodes := map[string]struct{}{}, map[string]struct{}{}
		for _, placement := range placements {
			sites[placement[0]] = struct{}{}
			nodes[placement[1]] = struct{}{}
		}
		owner.SiteRefs = sortedScopeKeys(sites)
		owner.NodeRefs = sortedScopeKeys(nodes)
		gates := owner.HealthGateRefs[:0]
		for _, ref := range owner.HealthGateRefs {
			if _, ok := keptHealth[ref]; ok {
				gates = append(gates, ref)
			}
		}
		owner.HealthGateRefs = gates
		scoped.ProviderOwners = append(scoped.ProviderOwners, owner)
	}

	scoped.AccessBindings = scoped.AccessBindings[:0]
	for _, binding := range cloneApplyRequirements(source).AccessBindings {
		if _, ok := kept[binding.RuntimeRequirementID]; ok {
			scoped.AccessBindings = append(scoped.AccessBindings, binding)
		}
	}
	scoped.BackupTargetBindings = scoped.BackupTargetBindings[:0]
	for _, binding := range cloneApplyRequirements(source).BackupTargetBindings {
		if _, ok := kept[binding.RuntimeRequirementID]; ok {
			scoped.BackupTargetBindings = append(scoped.BackupTargetBindings, binding)
		}
	}
	scoped.ExecutionScope = scope.clone()
	if err := validateApplyRequirementIDs(scoped); err != nil {
		return VerifiedPlan{}, err
	}
	result := p
	result.applyRequirements = scoped
	return result, nil
}

// scopedHealthTargetsRuntime is the same exact owner rule the product runtime
// registry applies when it assigns a health target to one runtime owner.
func scopedHealthTargetsRuntime(health ApplyHealthRequirement, target ApplyRuntimeRequirement) bool {
	if len(health.SiteRefs) != 1 || len(health.NodeRefs) != 1 || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		health.SiteRefs[0] != target.SiteRefs[0] || health.NodeRefs[0] != target.NodeRefs[0] {
		return false
	}
	if health.RuntimeRequirementID != "" {
		return health.RuntimeRequirementID == target.ID
	}
	return health.TargetKind == "module" && health.TargetRef == target.ModuleRef ||
		health.TargetKind == "provider" && health.TargetRef == target.ProviderRef ||
		health.TargetKind == "runtime" && health.TargetRef == target.InstanceRef
}

func (b ApplyExecutionScopeBinding) validate(path string) error {
	if !canonicalScopeToken(b.SiteRef) || !canonicalScopeToken(b.NodeRef) || !canonicalScopeToken(b.ExecutionChannelRef) {
		return fail(ErrInvalidContract, path, "requires an exact Site, node, and execution channel")
	}
	return nil
}

func scopeBindingLess(left, right ApplyExecutionScopeBinding) bool {
	if left.SiteRef != right.SiteRef {
		return left.SiteRef < right.SiteRef
	}
	return left.NodeRef < right.NodeRef
}

func canonicalScopeToken(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

func containsSortedString(values []string, candidate string) bool {
	index := sort.SearchStrings(values, candidate)
	return index < len(values) && values[index] == candidate
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func sortedScopeKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
