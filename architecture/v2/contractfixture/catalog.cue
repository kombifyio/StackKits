// Package contractfixture owns a deliberately non-product Architecture v2 contract
// fixture. It exercises the complete inventory -> compiler -> CUE authority ->
// renderer seam without adding an implementation module to any product kit.
// Product readiness must continue to be derived exclusively from
// ArchitectureV2Catalog.
package contractfixture

import (
	basement "github.com/kombifyio/stackkits/basement-kit:basement_kit"
	"github.com/kombifyio/stackkits/base"
	"list"
)

// ContractFixtureDefinition preserves Basement topology and policy semantics
// while carrying a distinct definition hash and evidence namespace. A plan
// resolved by this authority can therefore never masquerade as the product
// Basement definition or its SK-S1 evidence.
ContractFixtureDefinition: base.#KitDefinition & {
	apiVersion: basement.Definition.apiVersion
	kind:       basement.Definition.kind
	metadata: {
		slug:        basement.Definition.metadata.slug
		version:     "5.0.0-contract.fixture"
		displayName: "Basement Contract Fixture"
		description: "Non-product Basement semantics for the isolated Architecture v2 renderer contract proof"
	}
	topology:         basement.Definition.topology
	availability:     basement.Definition.availability
	capabilities:     basement.Definition.capabilities
	accessDefaults:   basement.Definition.accessDefaults
	reachability:     basement.Definition.reachability
	dataDefaults:     basement.Definition.dataDefaults
	failureDefaults:  basement.Definition.failureDefaults
	deviceEnrollment: basement.Definition.deviceEnrollment
	partitionPolicy:  basement.Definition.partitionPolicy
	generation:       basement.Definition.generation
	network:          basement.Definition.network
	bridge:           basement.Definition.bridge
	evidenceScenarios: ["contract-fixture-two-node"]
}

_architectureV2ContractFixtureRequiredCapabilities: list.Concat([base.#CommonCapabilityIDs, [
	"site-local",
	"provision-local",
	"local-hardware-preflight",
	"lan-discovery",
	"local-ingress",
	"lan-access-policy",
	"device-enrollment-home",
	"local-control-authority",
	"offline-autonomy",
	"local-backup-target",
]])

_architectureV2ContractFixtureConsumerCapabilities: [
	for capabilityRef in _architectureV2ContractFixtureRequiredCapabilities
	if capabilityRef != "topology-core" {capabilityRef},
]

_architectureV2ContractFixtureRendererRef: "stackkit-contract-fixture"

_architectureV2ContractFixtureModules: [
	{
		metadata: {
			id:          "fixture-socket-proxy"
			version:     "1.0.0"
			description: "Non-product renderer contract fixture for a reviewed node-local Docker API proxy."
		}
		providerRef: "fixture-basement-provider"
		provides: ["topology-core"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:           "socket-proxy"
			kind:         "compose"
			rendererRef:  _architectureV2ContractFixtureRendererRef
			templateRef:  "contract-fixture/socket-proxy"
			version:      "1.0.0"
			contractHash: "sha256:1111111111111111111111111111111111111111111111111111111111111111"
			publicInputRefs: []
			secretInputRefs: []
			outputs: ["compose/fixture-socket-proxy.yaml"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-daemon"
				daemonRef:   "docker-rootless"
			}
			providesInterfaces: [{
				id:       "docker-api-readonly"
				kind:     "docker-http-readonly-v1"
				protocol: "docker-http"
				version:  "v1"
				endpoint: {
					ref:        "docker-api"
					visibility: "node-local"
					transport:  "tcp"
					networkRef: "docker-api-fixture"
					address:    "fixture-socket-proxy"
					port:       2375
				}
				scopes: ["CONTAINERS", "EVENTS", "NETWORKS", "PING", "VERSION"]
				coLocation:    "same-node-and-network"
				daemonRef:     "docker-rootless"
				policyProfile: "docker-readonly-baseline"
			}]
			requiresInterfaces: [{
				id:       "docker-provider-backing"
				kind:     "docker-socket-direct-v1"
				protocol: "docker-engine"
				version:  "v1"
				endpoint: {
					visibility: "node-local"
					transport:  "unix-socket"
					pathSource: "daemon-binding"
				}
				scopes: ["docker-api:full"]
				coLocation:    "same-node"
				daemonRef:     "docker-rootless"
				policyProfile: "docker-provider-backing"
			}]
		}]
		realizationSupport: {
			contractVersion: "1.0.0"
			scope:           "concrete"
			level:           "generation-ready"
			compatibleRendererRefs: [_architectureV2ContractFixtureRendererRef]
			inputs: {contractComplete: true, requiredRefs: []}
			artifacts: {
				requiredRefs: ["fixture-socket-proxy-compose"]
				outputBindings: [{
					artifactRef: "fixture-socket-proxy-compose"
					unitRef:     "socket-proxy"
					outputRef:   "compose/fixture-socket-proxy.yaml"
				}]
				contracts: [{
					id:       "fixture-socket-proxy-compose"
					kind:     "compose"
					format:   "yaml"
					mode:     "0644"
					required: true
					compatibleTargets: ["compose"]
					unitRef:   "socket-proxy"
					outputRef: "compose/fixture-socket-proxy.yaml"
				}]
			}
			evidence: requiredRefs: []
		}
		health: [{id: "fixture-socket-proxy-contract", kind: "contract"}]
		evidence: ["fixture-socket-proxy-reviewed"]
	},
	{
		metadata: {
			id:          "fixture-http-consumer"
			version:     "1.0.0"
			description: "Non-product renderer contract fixture for an exact node-local Docker API consumer."
		}
		providerRef: "fixture-basement-provider"
		provides:    _architectureV2ContractFixtureConsumerCapabilities
		requires: ["fixture-socket-proxy"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:           "observer"
			kind:         "compose"
			rendererRef:  _architectureV2ContractFixtureRendererRef
			templateRef:  "contract-fixture/http-consumer"
			version:      "1.0.0"
			contractHash: "sha256:2222222222222222222222222222222222222222222222222222222222222222"
			publicInputRefs: []
			secretInputRefs: []
			outputs: ["compose/fixture-http-consumer.yaml"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
			requiresInterfaces: [{
				id:       "docker-api-observer"
				kind:     "docker-http-readonly-v1"
				protocol: "docker-http"
				version:  "v1"
				endpoint: {
					ref:        "docker-api"
					visibility: "node-local"
					transport:  "tcp"
					networkRef: "docker-api-fixture"
					address:    "fixture-socket-proxy"
					port:       2375
				}
				scopes: ["CONTAINERS", "EVENTS"]
				coLocation:    "same-node-and-network"
				daemonRef:     "docker-rootless"
				policyProfile: "docker-readonly-baseline"
			}]
		}]
		realizationSupport: {
			contractVersion: "1.0.0"
			scope:           "concrete"
			level:           "generation-ready"
			compatibleRendererRefs: [_architectureV2ContractFixtureRendererRef]
			inputs: {contractComplete: true, requiredRefs: []}
			artifacts: {
				requiredRefs: ["fixture-http-consumer-compose"]
				outputBindings: [{
					artifactRef: "fixture-http-consumer-compose"
					unitRef:     "observer"
					outputRef:   "compose/fixture-http-consumer.yaml"
				}]
				contracts: [{
					id:       "fixture-http-consumer-compose"
					kind:     "compose"
					format:   "yaml"
					mode:     "0644"
					required: true
					compatibleTargets: ["compose"]
					unitRef:   "observer"
					outputRef: "compose/fixture-http-consumer.yaml"
				}]
			}
			evidence: requiredRefs: []
		}
		health: [{id: "fixture-http-consumer-contract", kind: "contract"}]
		evidence: ["fixture-http-consumer-contract"]
	},
]

// ArchitectureV2ContractFixtureCatalog is bundled separately from the product
// catalog and is accepted only by the explicitly named contract-fixture
// service constructors. It cannot graduate a Basement, Cloud, or Modern
// profile into product readiness.
ArchitectureV2ContractFixtureCatalog: base.#ArchitectureV2CatalogContract & {
	capabilities: [
		for contract in base.ArchitectureV2Catalog.capabilities
		for requiredRef in _architectureV2ContractFixtureRequiredCapabilities
		if contract.metadata.id == requiredRef {base.#CapabilityContract & {
			metadata:           contract.metadata
			supportedSiteKinds: contract.supportedSiteKinds
			evidence: ["resolved-plan-contract"]
			if contract.requires != _|_ {requires: contract.requires}
			if contract.conflicts != _|_ {conflicts: contract.conflicts}
			if contract.privileges != _|_ {privileges: contract.privileges}
			if contract.networkFlows != _|_ {networkFlows: contract.networkFlows}
			if contract.secretInputs != _|_ {secretInputs: contract.secretInputs}
			if contract.dataClasses != _|_ {dataClasses: contract.dataClasses}
			if contract.health != _|_ {health: contract.health}
		}},
	]
	providers: [for contract in [{
		metadata: {id: "fixture-basement-provider", version: "1.0.0"}
		provides: _architectureV2ContractFixtureRequiredCapabilities
		supportedSiteKinds: ["home"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["fixture-socket-proxy", "fixture-http-consumer"]
				optional: []
			}
		}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "fixture-basement-provider-contract", kind: "contract"}]
		evidence: ["fixture-basement-provider-contract"]
	}] {base.#CapabilityProvider & contract}]
	addons: []
	modules: [for contract in _architectureV2ContractFixtureModules {base.#ModuleContractV2 & contract}]
	privilegedInterfaceApprovals: [for contract in [{
		id:            "approve-fixture-socket-proxy-backing"
		kind:          "docker-socket-direct-v1"
		moduleRef:     "fixture-socket-proxy"
		unitRef:       "socket-proxy"
		providerRef:   "fixture-basement-provider"
		daemonRef:     "docker-rootless"
		policyProfile: "docker-provider-backing"
		reasonCode:    "provider-backing"
		evidenceRef:   "fixture-socket-proxy-reviewed"
	}] {base.#PrivilegedInterfaceApprovalV2 & contract}]
	planArtifacts: base.ArchitectureV2Catalog.planArtifacts
}
