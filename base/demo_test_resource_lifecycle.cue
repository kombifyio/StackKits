// Package base - demo and test resource lifecycle and authority contracts.
//
// The contract intentionally contains only logical references. Concrete host
// addresses and provider credentials belong in the private operations registry
// and secret stores, never in CUE, generated plans, or public evidence.
package base

import "list"

#DemoTestResourceRoleV1: "local-demo-anchor" |
	"cloud-demo-anchor" |
	"cloud-demo-session" |
	"provider-release-smoke" |
	"compatibility-target" |
	"unknown"

#DemoTestResourceLifecycleV1: "protected-persistent" |
	"session-ephemeral" |
	"test-ephemeral" |
	"unknown"

#LocalDemoNodeClassV1: "virtual-machine" | "bare-metal"

#DemoTestAuthorityClassV1: "demo-local-read" |
	"demo-cloud-read" |
	"demo-session" |
	"release-smoke" |
	"compatibility-lab" |
	"inventory-read"

#DemoTestReadOnlyAuthorityV1: {
	class:       #DemoTestAuthorityClassV1
	readRef:     #ContractID
	mutateRef?:  _|_
	cleanupRef?: _|_
}

#DemoTestLeaseAuthorityV1: {
	class:      #DemoTestAuthorityClassV1
	readRef:    #ContractID
	mutateRef:  #ContractID
	cleanupRef: #ContractID
	_refConflicts: [
		if readRef == mutateRef {"read-mutate"},
		if readRef == cleanupRef {"read-cleanup"},
		if mutateRef == cleanupRef {"mutate-cleanup"},
	] & list.MaxItems(0)
}

#DemoSessionLeaseV1: {
	sessionRef:  #ContractID
	ownerRef:    #ContractID
	resourceRef: #ContractID
	ttlSeconds:  int & >=60 & <=14400
}

#DemoTestCandidateLeaseV1: {
	runRef:       #ContractID
	ownerRef:     #ContractID
	resourceRef:  #ContractID
	candidateRef: #ContractID
	ttlSeconds:   int & >=60 & <=14400
}

#DemoTestManagedResourceCommonV1: {
	apiVersion:    #ArchitectureAPIVersion
	kind:          "DemoTestManagedResource"
	logicalID:     #ContractID
	role:          #DemoTestResourceRoleV1
	lifecycle:     #DemoTestResourceLifecycleV1
	locality:      #SiteKind
	protected:     bool
	allowMutation: bool
	allowCleanup:  bool

	// providerRef identifies a provider contract, not a hostname or address.
	providerRef?: #ContractID
	adapterRef?:  #ContractID
	boundaryRef?: #ContractID
	inventoryRef?: #ContractID
	nodeClass?:    #LocalDemoNodeClassV1
	substrateRef?: #ContractID
	session?:     #DemoSessionLeaseV1
	candidate?:   #DemoTestCandidateLeaseV1
	mutability:   #DemoTestReadOnlyAuthorityV1 | #DemoTestLeaseAuthorityV1
}

#LocalProtectedDemoAnchorV1: #DemoTestManagedResourceCommonV1 & {
	role:          "local-demo-anchor"
	lifecycle:     "protected-persistent"
	locality:      "home"
	protected:     true
	allowMutation: false
	allowCleanup:  false
	providerRef?:  _|_
	adapterRef?:   _|_
	boundaryRef:   #ContractID
	inventoryRef:  #ContractID
	nodeClass:     #LocalDemoNodeClassV1
	session?:      _|_
	candidate?:    _|_
	if nodeClass == "virtual-machine" {
		substrateRef: #ContractID
	}
	if nodeClass == "bare-metal" {
		substrateRef?: _|_
	}
	mutability: #DemoTestReadOnlyAuthorityV1 & {
		class: "demo-local-read"
	}
}

#CloudProtectedDemoAnchorV1: #DemoTestManagedResourceCommonV1 & {
	role:          "cloud-demo-anchor"
	lifecycle:     "protected-persistent"
	locality:      "cloud"
	protected:     true
	allowMutation: false
	allowCleanup:  false
	providerRef:   #ContractID
	adapterRef?:   _|_
	boundaryRef:   #ContractID
	session?:      _|_
	candidate?:    _|_
	mutability: #DemoTestReadOnlyAuthorityV1 & {
		class: "demo-cloud-read"
	}
}

#CloudSessionDemoResourceV1: #DemoTestManagedResourceCommonV1 & {
	role:          "cloud-demo-session"
	lifecycle:     "session-ephemeral"
	locality:      "cloud"
	protected:     false
	allowMutation: true
	allowCleanup:  true
	providerRef:   #ContractID
	adapterRef?:   _|_
	boundaryRef:   #ContractID
	session:       #DemoSessionLeaseV1
	candidate?:    _|_
	mutability: #DemoTestLeaseAuthorityV1 & {
		class: "demo-session"
	}
}

#ProviderReleaseSmokeResourceV1: #DemoTestManagedResourceCommonV1 & {
	role:          "provider-release-smoke"
	lifecycle:     "test-ephemeral"
	locality:      "cloud"
	protected:     false
	allowMutation: true
	allowCleanup:  true
	providerRef:   #ContractID
	adapterRef?:   _|_
	boundaryRef:   #ContractID
	session?:      _|_
	candidate:     #DemoTestCandidateLeaseV1
	mutability: #DemoTestLeaseAuthorityV1 & {
		class: "release-smoke"
	}
}

#CompatibilityTargetResourceV1: #DemoTestManagedResourceCommonV1 & {
	role:          "compatibility-target"
	lifecycle:     "test-ephemeral"
	locality:      "home"
	protected:     false
	allowMutation: true
	allowCleanup:  true
	providerRef?:  _|_
	adapterRef:    #ContractID
	boundaryRef:   #ContractID
	session?:      _|_
	candidate:     #DemoTestCandidateLeaseV1
	mutability: #DemoTestLeaseAuthorityV1 & {
		class: "compatibility-lab"
	}
}

#UnknownDemoTestResourceV1: #DemoTestManagedResourceCommonV1 & {
	role:          "unknown"
	lifecycle:     "unknown"
	protected:     true
	allowMutation: false
	allowCleanup:  false
	adapterRef?:   _|_
	boundaryRef?:  _|_
	session?:      _|_
	candidate?:    _|_
	mutability: #DemoTestReadOnlyAuthorityV1 & {
		class: "inventory-read"
	}
}

#DemoTestManagedResourceV1: #LocalProtectedDemoAnchorV1 |
	#CloudProtectedDemoAnchorV1 |
	#CloudSessionDemoResourceV1 |
	#ProviderReleaseSmokeResourceV1 |
	#CompatibilityTargetResourceV1 |
	#UnknownDemoTestResourceV1

// #DemoTestResourceTopologyV1 is the fail-closed resource-set boundary used by
// demo orchestration, provider release smoke, and the local compatibility lab.
// It enforces two protected anchors, permits at most one second-provider demo
// session, and prevents any authority class from sharing an administrative
// boundary with another class.
#DemoTestResourceTopologyV1: {
	apiVersion: #ArchitectureAPIVersion
	kind:       "DemoTestResourceTopology"
	resources: [...#DemoTestManagedResourceV1] & list.MinItems(2)

	_logicalIDsUnique: list.UniqueItems([for resource in resources {resource.logicalID}]) & true
	_localAnchors: [for resource in resources if resource.role == "local-demo-anchor" {resource.logicalID}] & list.MinItems(1) & list.MaxItems(1)
	_cloudAnchors: [for resource in resources if resource.role == "cloud-demo-anchor" {resource.logicalID}] & list.MinItems(1) & list.MaxItems(1)
	_cloudSessions: [for resource in resources if resource.role == "cloud-demo-session" {resource.logicalID}] & list.MaxItems(1)
	_leaseResourceRefs: [
		for resource in resources
		if resource.session != _|_ {resource.session.resourceRef},
		for resource in resources
		if resource.candidate != _|_ {resource.candidate.resourceRef},
	]
	_leaseResourceRefsUnique: list.UniqueItems(_leaseResourceRefs) & true

	// The optional SDK demo resource is always at a provider other than the
	// protected Cloud anchor. A second permanent Cloud anchor is not valid.
	_sessionProviderConflicts: [
		for session in resources
		if session.role == "cloud-demo-session"
		for anchor in resources
		if anchor.role == "cloud-demo-anchor" && session.providerRef == anchor.providerRef {
			sessionRef: session.logicalID
			anchorRef:  anchor.logicalID
		},
	] & list.MaxItems(0)

	// Reuse inside one authority class is allowed (for example several VMs in
	// one compatibility pool). Sharing a boundary across authority classes is
	// rejected, which isolates anchors, SDK sessions, release smoke, and labs.
	_authorityBoundaries: [
		for resource in resources
		if resource.boundaryRef != _|_ {
			resourceRef:    resource.logicalID
			boundaryRef:    resource.boundaryRef
			authorityClass: resource.mutability.class
		},
	]
	_authorityBoundaryConflicts: [
		for left in _authorityBoundaries
		for right in _authorityBoundaries
		if left.boundaryRef == right.boundaryRef && left.authorityClass != right.authorityClass {
			leftRef:        left.resourceRef
			rightRef:       right.resourceRef
			boundaryRef:    left.boundaryRef
			leftAuthority:  left.authorityClass
			rightAuthority: right.authorityClass
		},
	] & list.MaxItems(0)

	// An authority reference belongs to exactly one authority class. This keeps
	// anchor readers out of every mutation/cleanup principal and prevents an
	// SDK, release, or compatibility principal from crossing class boundaries.
	_authorityRefs: [
		for resource in resources {
			resourceRef:    resource.logicalID
			authorityRef:   resource.mutability.readRef
			authorityClass: resource.mutability.class
		},
		for resource in resources
		if resource.mutability.mutateRef != _|_ {
			resourceRef:    resource.logicalID
			authorityRef:   resource.mutability.mutateRef
			authorityClass: resource.mutability.class
		},
		for resource in resources
		if resource.mutability.cleanupRef != _|_ {
			resourceRef:    resource.logicalID
			authorityRef:   resource.mutability.cleanupRef
			authorityClass: resource.mutability.class
		},
	]
	_authorityRefConflicts: [
		for left in _authorityRefs
		for right in _authorityRefs
		if left.authorityRef == right.authorityRef && left.authorityClass != right.authorityClass {
			leftRef:        left.resourceRef
			rightRef:       right.resourceRef
			authorityRef:   left.authorityRef
			leftAuthority:  left.authorityClass
			rightAuthority: right.authorityClass
		},
	] & list.MaxItems(0)
}

// Concrete, address-free probes keep both the steady-state and active-session
// branches under `cue vet`; they are standards examples, not live inventory.
DemoTestResourceTopologyContractExamplesV1: {
	steadyState: #DemoTestResourceTopologyV1 & {
		apiVersion: #ArchitectureAPIVersion
		kind:       "DemoTestResourceTopology"
		resources: [
			{
				apiVersion:    #ArchitectureAPIVersion
				kind:          "DemoTestManagedResource"
				logicalID:     "demo-local-anchor"
				role:          "local-demo-anchor"
				lifecycle:     "protected-persistent"
				locality:      "home"
				protected:     true
				allowMutation: false
				allowCleanup:  false
				boundaryRef:   "demo-local-boundary"
				inventoryRef:  "demo-local-node-current"
				nodeClass:     "virtual-machine"
				substrateRef:  "demo-local-virtualization-host"
				mutability: {
					class:   "demo-local-read"
					readRef: "demo-local-reader"
				}
			},
			{
				apiVersion:    #ArchitectureAPIVersion
				kind:          "DemoTestManagedResource"
				logicalID:     "demo-cloud-anchor"
				role:          "cloud-demo-anchor"
				lifecycle:     "protected-persistent"
				locality:      "cloud"
				protected:     true
				allowMutation: false
				allowCleanup:  false
				providerRef:   "cloud-provider-a"
				boundaryRef:   "demo-cloud-boundary"
				mutability: {
					class:   "demo-cloud-read"
					readRef: "demo-cloud-reader"
				}
			},
		]
	}

	// Target cutover replaces the one VM-backed anchor; it never adds a second
	// local anchor or treats the protected virtualization host as a StackKit node.
	bareMetalTargetState: #DemoTestResourceTopologyV1 & {
		apiVersion: #ArchitectureAPIVersion
		kind:       "DemoTestResourceTopology"
		resources: [
			{
				apiVersion:    #ArchitectureAPIVersion
				kind:          "DemoTestManagedResource"
				logicalID:     "demo-local-anchor"
				role:          "local-demo-anchor"
				lifecycle:     "protected-persistent"
				locality:      "home"
				protected:     true
				allowMutation: false
				allowCleanup:  false
				boundaryRef:   "demo-local-boundary"
				inventoryRef:  "demo-local-node-bare-metal-target"
				nodeClass:     "bare-metal"
				mutability: {
					class:   "demo-local-read"
					readRef: "demo-local-reader"
				}
			},
			steadyState.resources[1],
		]
	}

	activeSessionAndTests: #DemoTestResourceTopologyV1 & {
		apiVersion: #ArchitectureAPIVersion
		kind:       "DemoTestResourceTopology"
		resources: [
			steadyState.resources[0],
			steadyState.resources[1],
			{
				apiVersion:    #ArchitectureAPIVersion
				kind:          "DemoTestManagedResource"
				logicalID:     "demo-cloud-session"
				role:          "cloud-demo-session"
				lifecycle:     "session-ephemeral"
				locality:      "cloud"
				protected:     false
				allowMutation: true
				allowCleanup:  true
				providerRef:   "cloud-provider-b"
				boundaryRef:   "demo-session-boundary"
				session: {
					sessionRef:  "demo-session"
					ownerRef:    "sdk-demo-broker"
					resourceRef: "provider-resource-session"
					ttlSeconds:  3600
				}
				mutability: {
					class:      "demo-session"
					readRef:    "sdk-demo-reader"
					mutateRef:  "sdk-demo-provisioner"
					cleanupRef: "sdk-demo-cleaner"
				}
			},
			{
				apiVersion:    #ArchitectureAPIVersion
				kind:          "DemoTestManagedResource"
				logicalID:     "provider-release-target"
				role:          "provider-release-smoke"
				lifecycle:     "test-ephemeral"
				locality:      "cloud"
				protected:     false
				allowMutation: true
				allowCleanup:  true
				providerRef:   "cloud-provider-a"
				boundaryRef:   "release-smoke-boundary"
				candidate: {
					runRef:       "release-run"
					ownerRef:     "release-ci"
					resourceRef:  "provider-resource-release"
					candidateRef: "release-candidate"
					ttlSeconds:   3600
				}
				mutability: {
					class:      "release-smoke"
					readRef:    "release-reader"
					mutateRef:  "release-provisioner"
					cleanupRef: "release-cleaner"
				}
			},
			{
				apiVersion:    #ArchitectureAPIVersion
				kind:          "DemoTestManagedResource"
				logicalID:     "compatibility-target"
				role:          "compatibility-target"
				lifecycle:     "test-ephemeral"
				locality:      "home"
				protected:     false
				allowMutation: true
				allowCleanup:  true
				adapterRef:    "local-lab-adapter"
				boundaryRef:   "compatibility-lab-boundary"
				candidate: {
					runRef:       "compatibility-run"
					ownerRef:     "compatibility-runner"
					resourceRef:  "lab-resource-target"
					candidateRef: "compatibility-candidate"
					ttlSeconds:   3600
				}
				mutability: {
					class:      "compatibility-lab"
					readRef:    "compatibility-reader"
					mutateRef:  "compatibility-provisioner"
					cleanupRef: "compatibility-cleaner"
				}
			},
		]
	}
}
