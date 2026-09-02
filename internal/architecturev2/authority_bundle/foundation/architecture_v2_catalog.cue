// Package foundation - governed Architecture v2 capability, implementation-adapter,
// and add-on contracts. The established CapabilityProvider/providerRef wire
// names describe StackKits adapters (PaaS, mesh, renderer, host-local modules),
// never server providers. Kit definitions declare what must exist; this catalog
// declares which immutable implementation contracts may realize it.
package foundation

import "list"

_architectureV2CoreCapabilities: [
	"topology-core",
	"host-bootstrap",
	"external-host-admission",
	"host-conformance",
	"security-baseline",
	"human-identity-core",
	"device-trust-core",
	"access-policy",
	"runtime-paas",
	"service-catalog",
	"secrets-recovery",
	"storage-data-policy",
	"backup-core",
	"observability-evidence",
	"lifecycle-update",
]

// Topology is declarative plan authority, not host runtime work. Keeping it
// outside the residual Core module prevents a generated executor handoff from
// falsely claiming that it creates sites or owns their lifecycle.
_architectureV2CoreTopologyCapabilities: ["topology-core"]

// The service catalog is resolved plan data consumed by runtime owners. It is
// neither a daemon nor a generated executor contract of its own.
_architectureV2ServiceCatalogCapabilities: ["service-catalog"]

// These are shared policy contracts consumed by kit-specific enforcement and
// runtime owners. Selection binds plan intent; it does not enforce access or
// mutate storage on its own.
_architectureV2AccessPolicyCapabilities: ["access-policy"]
_architectureV2StorageDataPolicyCapabilities: ["storage-data-policy"]

// runtime-paas is the historical capability ID for the shared workload
// delivery interface. Native v2 binds the concrete application-adapter
// separately, including the non-PaaS standalone Compose implementation.
// Basement Compose, Cloud runtime, and Modern federation remain distinct
// realizations and must not be inferred from this cross-kit contract.
_architectureV2WorkloadRuntimeContractCapabilities: ["runtime-paas"]

_architectureV2PhotosInfrastructure: #WorkloadInfrastructureV1 & {
	storageAllocation: {
		moduleRef: "stackkits-storage-allocation"
		allocations: [
			{
				componentRef: "immich-server", volumeRef: "library", target:                               "/data"
				class:        "persistent", backup:       true, dataClasses: ["personal"], dataBindingRef: "photos"
			},
			{
				componentRef: "immich-machine-learning", volumeRef: "model-cache", target: "/cache"
				class:        "cache", backup:                      false, dataClasses: []
			},
			{
				componentRef: "immich-postgres", volumeRef: "database", target:                              "/var/lib/postgresql/data"
				class:        "persistent", backup:         true, dataClasses: ["personal"], dataBindingRef: "photos"
			},
		]
	}
	dataBinding: {
		moduleRef:  "stackkits-workload-data-binding"
		bindingRef: "photos"
		classes: ["personal"]
		locality: "primary-site"
	}
	backupSource: {
		moduleRef: "stackkits-backup-source"
		allocations: [
			for allocation in storageAllocation.allocations
			if allocation.backup {
				componentRef: allocation.componentRef
				volumeRef:    allocation.volumeRef
				dataClasses:  allocation.dataClasses
			},
		]
	}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

_architectureV2FilesInfrastructure: #WorkloadInfrastructureV1 & {
	storageAllocation: {
		moduleRef: "stackkits-storage-allocation"
		allocations: [{
			componentRef: "cloudreve", volumeRef: "data", target:                                  "/cloudreve/data"
			class:        "persistent", backup:   true, dataClasses: ["personal"], dataBindingRef: "files"
		}]
	}
	dataBinding: {
		moduleRef:  "stackkits-workload-data-binding"
		bindingRef: "files"
		classes: ["personal"]
		locality: "primary-site"
	}
	backupSource: {
		moduleRef: "stackkits-backup-source"
		allocations: [
			for allocation in storageAllocation.allocations
			if allocation.backup {
				componentRef: allocation.componentRef
				volumeRef:    allocation.volumeRef
				dataClasses:  allocation.dataClasses
			},
		]
	}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

_architectureV2VaultInfrastructure: #WorkloadInfrastructureV1 & {
	storageAllocation: {
		moduleRef: "stackkits-storage-allocation"
		allocations: [{
			componentRef: "vaultwarden", volumeRef: "data", target:                                "/data"
			class:        "persistent", backup:     true, dataClasses: ["secret"], dataBindingRef: "vault"
		}]
	}
	dataBinding: {
		moduleRef:  "stackkits-workload-data-binding"
		bindingRef: "vault"
		classes: ["secret"]
		locality: "primary-site"
	}
	backupSource: {
		moduleRef: "stackkits-backup-source"
		allocations: [
			for allocation in storageAllocation.allocations
			if allocation.backup {
				componentRef: allocation.componentRef
				volumeRef:    allocation.volumeRef
				dataClasses:  allocation.dataClasses
			},
		]
	}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

_architectureV2MediaInfrastructure: #WorkloadInfrastructureV1 & {
	storageAllocation: {
		moduleRef: "stackkits-storage-allocation"
		allocations: [
			{
				componentRef: "jellyfin", volumeRef: "config", target:                                "/config"
				class:        "persistent", backup:  true, dataClasses: ["personal"], dataBindingRef: "media"
			},
			{
				componentRef: "jellyfin", volumeRef: "cache", target: "/cache"
				class:        "cache", backup:       false, dataClasses: []
			},
			{
				componentRef: "jellyfin", volumeRef: "library", target:                                "/media"
				class:        "persistent", backup:  false, dataClasses: ["personal"], dataBindingRef: "media"
			},
		]
	}
	dataBinding: {
		moduleRef:  "stackkits-workload-data-binding"
		bindingRef: "media"
		classes: ["personal"]
		locality: "primary-site"
	}
	backupSource: {
		moduleRef: "stackkits-backup-source"
		allocations: [
			for allocation in storageAllocation.allocations
			if allocation.backup {
				componentRef: allocation.componentRef
				volumeRef:    allocation.volumeRef
				dataClasses:  allocation.dataClasses
			},
		]
	}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

_architectureV2SmartHomeInfrastructure: #WorkloadInfrastructureV1 & {
	storageAllocation: {
		moduleRef: "stackkits-storage-allocation"
		allocations: [{
			componentRef: "home-assistant", volumeRef: "config", target:                                "/config"
			class:        "persistent", backup:        true, dataClasses: ["personal"], dataBindingRef: "smart-home"
		}]
	}
	dataBinding: {
		moduleRef:  "stackkits-workload-data-binding"
		bindingRef: "smart-home"
		classes: ["personal"]
		locality: "primary-site"
	}
	backupSource: {
		moduleRef: "stackkits-backup-source"
		allocations: [
			for allocation in storageAllocation.allocations
			if allocation.backup {
				componentRef: allocation.componentRef
				volumeRef:    allocation.volumeRef
				dataClasses:  allocation.dataClasses
			},
		]
	}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

_architectureV2HostAdmissionCapabilities: [
	"external-host-admission",
	"host-conformance",
]

_architectureV2IdentityCapabilities: [
	"human-identity-core",
	"device-trust-core",
]

// These shared Core capabilities are contracts consumed by kit-specific
// owners. None of them is permission for one generic host executor.
_architectureV2SecretsRecoveryCapabilities: ["secrets-recovery"]
_architectureV2BackupCoreCapabilities: ["backup-core"]
_architectureV2ObservabilityEvidenceCapabilities: ["observability-evidence"]
_architectureV2TelemetryCollectionCapabilities: ["telemetry-collection"]
_architectureV2LifecycleUpdateCapabilities: ["lifecycle-update"]
_architectureV2SecurityBaselineCapabilities: ["security-baseline"]
_architectureV2CoreHostBootstrapCapabilities: ["host-bootstrap"]

_architectureV2LocalCapabilities: [
	"site-local",
	"lan-discovery",
	"local-ingress",
	"lan-access-policy",
	"device-enrollment-home",
	"local-control-authority",
	"offline-autonomy",
	"local-backup-target",
	"local-backup-runtime",
	"lan-dns",
	"private-remote-access",
	"public-publish-egress",
	"encrypted-offsite-backup",
]

_architectureV2LocalAutonomyCapabilities: ["offline-autonomy"]

_architectureV2InternalPKICapabilities: ["internal-pki"]

_architectureV2HomeAccessCapabilities: ["local-ingress", "lan-access-policy"]

_architectureV2HomeLANDiscoveryCapabilities: ["lan-discovery"]

_architectureV2HomeIdentityAuthorityCapabilities: [
	"device-enrollment-home",
	"local-control-authority",
]

// A prepared Home backup target is an executable, node-bound concern. It is
// selected by Basement Kit because that kit requires local-backup-target; a
// Modern Homelab has a Home site but does not inherit this Basement default.
_architectureV2HomeBackupTargetCapabilities: ["local-backup-target"]
_architectureV2BasementCoreCapabilities: ["local-backup-runtime"]

_architectureV2LocalTopologyCapabilities: ["site-local"]

_architectureV2HomeLANDNSCapabilities: ["lan-dns"]
_architectureV2HomePrivateRemoteAccessCapabilities: ["private-remote-access"]
_architectureV2HomePublicPublishEgressCapabilities: ["public-publish-egress"]
_architectureV2HomeEncryptedOffsiteBackupCapabilities: ["encrypted-offsite-backup"]

// The first product Compose lowering lane is Basement-owned rather than a
// generic home-site behavior. Modern Homelab also has home sites, so placing
// this capability on stackkits-local would incorrectly select Basement
// rollout machinery into the hybrid kit.
_architectureV2BasementComposeCapabilities: ["basement-compose-runtime"]

_architectureV2CloudCapabilities: [
	"site-cloud",
	"cloud-core-runtime",
	"host-local-internet-firewall",
	"public-edge",
	"public-dns",
	"internet-host-hardening",
	"remote-owner-bootstrap",
	"offsite-object-backup",
	"cloud-control-authority",
	"private-admin-mesh",
	"failure-domain-placement",
]

_architectureV2CloudIdentityAuthorityCapabilities: [
	"remote-owner-bootstrap",
	"cloud-control-authority",
]

_architectureV2CloudTopologyCapabilities: ["site-cloud"]
_architectureV2CloudPlacementPolicyCapabilities: ["failure-domain-placement"]
_architectureV2CloudHostSecurityCapabilities: [
	"host-local-internet-firewall",
	"internet-host-hardening",
]
_architectureV2CloudPublicDNSCapabilities: ["public-dns"]
_architectureV2CloudPublicEdgeCapabilities: ["public-edge"]
_architectureV2CloudOffsiteBackupCapabilities: ["offsite-object-backup"]
_architectureV2CloudPrivateAdminMeshCapabilities: ["private-admin-mesh"]
_architectureV2CloudCoreCapabilities: ["cloud-core-runtime"]

_architectureV2PublicTLSCapabilities: ["public-tls"]

// Public edge, DNS, and TLS capabilities above are desired host/service
// behavior. DNS is declarative only; edge and TLS are separate generation
// handoffs. None authorize a server-provider or DNS-provider mutation; an
// external platform adapter owns any such realization.
// Application selection is owned exclusively by logical Workload contracts.
// It must never re-enter the architecture capability closure.
_architectureV2ApplicationCapabilityContracts: []

_architectureV2WorkloadContracts: [
	#WorkloadContractV2 & {
		metadata: {
			id:          "basement-core"
			version:     "1.0.0"
			description: "Required standalone Basement control plane with local routing, owner identity, internal PKI, container management, and operator hub."
		}
		kind: "service"
		functionalCapabilities: [
			"local-routing",
			"owner-identity",
			"forward-auth",
			"internal-ca",
			"container-platform",
			"operator-hub",
		]
		supportedSiteKinds: ["home"]
		dataClasses: []
		defaultAlternative: "standalone"
		alternatives: [{
			id:          "standalone"
			providerRef: "stackkits-basement-core"
			moduleRef:   "stackkits-basement-core-runtime"
			route: {serviceRef: "basement-hub", healthRef: "basement-hub-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["stackkit"]
				allowedAdapterRefs: []
			}
			setup: {
				mode:  "manual"
				owner: "operator"
				actionRefs: []
			}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {allowedRefs: [], requiredRefs: []}
			}
		}, {
			id:          "standalone-lite"
			providerRef: "stackkits-basement-core"
			moduleRef:   "stackkits-basement-core-lite-runtime"
			route: {serviceRef: "basement-hub", healthRef: "basement-hub-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["stackkit"]
				allowedAdapterRefs: []
			}
			setup: {
				mode:  "manual"
				owner: "operator"
				actionRefs: []
			}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {allowedRefs: [], requiredRefs: []}
			}
		}]
	},
	#WorkloadContractV2 & {
		metadata: {
			id:          "cloud-core"
			version:     "1.0.0"
			description: "Required provider-neutral Cloud control plane installed on an externally supplied host; provider and server lifecycle remain outside StackKits."
		}
		kind: "service"
		functionalCapabilities: [
			"public-routing",
			"owner-identity",
			"forward-auth",
			"container-platform",
			"operator-hub",
		]
		supportedSiteKinds: ["cloud"]
		dataClasses: []
		defaultAlternative: "standalone"
		alternatives: [{
			id:          "standalone"
			providerRef: "stackkits-cloud-core"
			moduleRef:   "stackkits-cloud-core-runtime"
			route: {serviceRef: "base", healthRef: "cloud-hub-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["stackkit"]
				allowedAdapterRefs: []
			}
			setup: {
				mode:  "manual"
				owner: "operator"
				actionRefs: []
			}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {allowedRefs: [], requiredRefs: []}
			}
		}]
	},
	#WorkloadContractV2 & {
		metadata: {
			id:          "photos"
			version:     "1.2.0"
			description: "Self-hosted photo management selected independently from kit architecture capabilities."
		}
		kind:       "application"
		useCaseRef: "photos"
		functionalCapabilities: ["photo-library", "mobile-photo-backup"]
		supportedSiteKinds: ["home", "cloud"]
		dataClasses: ["personal"]
		defaultAlternative: "immich"
		computeTiers: {
			low: {included: true, alternativeID: "immich-lite"}
			standard: {included: true, alternativeID: "immich"}
			high: {included: true, alternativeID: "immich"}
		}
		alternatives: [{
			id:          "immich"
			providerRef: "stackkits-immich"
			moduleRef:   "stackkits-immich-runtime"
			route: {serviceRef: "photos", healthRef: "immich-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["application-adapter"]
				allowedAdapterRefs: ["coolify", "komodo", "standalone-compose"]
				defaultAdapterRef: "coolify"
				defaultFallbackAdapterRefs: ["standalone-compose"]
				compatibility: [
					{adapterRef: "coolify", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "komodo", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "standalone-compose", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}},
				]
			}
			setup: {
				mode:  "on-demand"
				owner: "module"
				actionRefs: ["immich-owner-bootstrap"]
			}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {
					allowedRefs: ["database-password"]
					requiredRefs: ["database-password"]
				}
			}
			infrastructure: _architectureV2PhotosInfrastructure
		}, {
			id:          "immich-lite"
			providerRef: "stackkits-immich-lite"
			moduleRef:   "stackkits-immich-lite-runtime"
			route: {serviceRef: "photos", healthRef: "immich-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["application-adapter"]
				allowedAdapterRefs: ["standalone-compose"]
				defaultAdapterRef: "standalone-compose"
				defaultFallbackAdapterRefs: []
				compatibility: [
					{adapterRef: "standalone-compose", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}},
				]
			}
			setup: {
				mode:  "on-demand"
				owner: "module"
				actionRefs: ["immich-owner-bootstrap"]
			}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {
					allowedRefs: ["database-password"]
					requiredRefs: ["database-password"]
				}
			}
			infrastructure: _architectureV2PhotosLiteInfrastructure
		}]
	},
	#WorkloadContractV2 & {
		metadata: {
			id:          "files"
			version:     "1.2.0"
			description: "Self-hosted file management and sharing selected independently from kit architecture capabilities."
		}
		kind:       "application"
		useCaseRef: "files"
		functionalCapabilities: ["file-library", "file-sharing"]
		supportedSiteKinds: ["home", "cloud"]
		dataClasses: ["personal"]
		defaultAlternative: "cloudreve"
		computeTiers: {
			low: {included: true, alternativeID: "cloudreve"}
			standard: {included: true, alternativeID: "cloudreve"}
			high: {included: true, alternativeID: "cloudreve"}
		}
		alternatives: [{
			id:          "cloudreve"
			providerRef: "stackkits-cloudreve"
			moduleRef:   "stackkits-cloudreve-runtime"
			route: {serviceRef: "files", healthRef: "cloudreve-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["application-adapter"]
				allowedAdapterRefs: ["coolify", "komodo", "standalone-compose"]
				defaultAdapterRef: "coolify"
				defaultFallbackAdapterRefs: ["standalone-compose"]
				compatibility: [
					{adapterRef: "coolify", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "komodo", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "standalone-compose", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}},
				]
			}
			setup: {mode: "on-demand", owner: "module", actionRefs: ["cloudreve-owner-bootstrap"]}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {allowedRefs: [], requiredRefs: []}
			}
			infrastructure: _architectureV2FilesInfrastructure
		}]
	},
	#WorkloadContractV2 & {
		metadata: {
			id:          "vault"
			version:     "1.1.0"
			description: "Self-hosted password vault selected independently from kit architecture capabilities."
		}
		kind:       "application"
		useCaseRef: "vault"
		functionalCapabilities: ["password-vault", "secure-notes"]
		supportedSiteKinds: ["home", "cloud"]
		dataClasses: ["secret"]
		defaultAlternative: "vaultwarden"
		computeTiers: {
			low: {included: true, alternativeID: "vaultwarden"}
			standard: {included: true, alternativeID: "vaultwarden"}
			high: {included: true, alternativeID: "vaultwarden"}
		}
		alternatives: [{
			id:          "vaultwarden"
			providerRef: "stackkits-vaultwarden"
			moduleRef:   "stackkits-vaultwarden-runtime"
			route: {serviceRef: "vault", healthRef: "vaultwarden-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["application-adapter"]
				allowedAdapterRefs: ["coolify", "komodo", "standalone-compose"]
				defaultAdapterRef: "coolify"
				defaultFallbackAdapterRefs: ["standalone-compose"]
				compatibility: [
					{adapterRef: "coolify", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "komodo", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "standalone-compose", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}},
				]
			}
			setup: {mode: "on-demand", owner: "module", actionRefs: ["vault-owner-invite"]}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {
					allowedRefs: ["admin-token"]
					requiredRefs: ["admin-token"]
				}
			}
			infrastructure: _architectureV2VaultInfrastructure
		}]
	},
	#WorkloadContractV2 & {
		metadata: {
			id:          "media"
			version:     "1.0.0"
			description: "Self-hosted media library selected independently from kit architecture capabilities."
		}
		kind:       "application"
		useCaseRef: "media"
		functionalCapabilities: ["media-server", "video-stream"]
		supportedSiteKinds: ["home", "cloud"]
		dataClasses: ["personal"]
		defaultAlternative: "jellyfin"
		computeTiers: {
			low: {included: false, reason: "Jellyfin library and transcode are not on the Basement low graph."}
			standard: {included: true, alternativeID: "jellyfin"}
			high: {included: true, alternativeID: "jellyfin"}
		}
		alternatives: [{
			id:          "jellyfin"
			providerRef: "stackkits-jellyfin"
			moduleRef:   "stackkits-jellyfin-runtime"
			route: {serviceRef: "media", healthRef: "jellyfin-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["application-adapter"]
				allowedAdapterRefs: ["coolify", "komodo", "standalone-compose"]
				defaultAdapterRef: "coolify"
				defaultFallbackAdapterRefs: ["standalone-compose"]
				compatibility: [
					{adapterRef: "coolify", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "komodo", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "standalone-compose", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}},
				]
			}
			setup: {mode: "on-demand", owner: "module", actionRefs: ["jellyfin-owner-bootstrap"]}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {allowedRefs: [], requiredRefs: []}
			}
			infrastructure: _architectureV2MediaInfrastructure
		}]
	},
	#WorkloadContractV2 & {
		metadata: {
			id:          "smart-home"
			version:     "1.0.0"
			description: "Self-hosted Home Assistant container selected independently from kit architecture capabilities."
		}
		kind:       "application"
		useCaseRef: "smart-home"
		functionalCapabilities: ["smart-home-hub", "native-product-mcp", "assist-api", "automation"]
		supportedSiteKinds: ["home", "cloud"]
		dataClasses: ["personal"]
		defaultAlternative: "home-assistant"
		computeTiers: {
			low: {included: true, alternativeID: "home-assistant"}
			standard: {included: true, alternativeID: "home-assistant"}
			high: {included: true, alternativeID: "home-assistant"}
		}
		alternatives: [{
			id:          "home-assistant"
			providerRef: "stackkits-home-assistant"
			moduleRef:   "stackkits-home-assistant-runtime"
			route: {serviceRef: "smart-home", healthRef: "home-assistant-http"}
			runtime: {
				allowedKinds: ["container"]
				allowedDeliveries: ["application-adapter"]
				allowedAdapterRefs: ["coolify", "komodo", "standalone-compose"]
				defaultAdapterRef: "coolify"
				defaultFallbackAdapterRefs: ["standalone-compose"]
				compatibility: [
					{adapterRef: "coolify", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "komodo", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}},
					{adapterRef: "standalone-compose", maturity: "supported", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}},
				]
			}
			setup: {mode: "on-demand", owner: "module", actionRefs: ["home-assistant-owner-bootstrap"]}
			inputs: {
				settings: {allowedRefs: [], requiredRefs: []}
				secretInputs: {allowedRefs: [], requiredRefs: []}
			}
			infrastructure: _architectureV2SmartHomeInfrastructure
		}]
	},
]

_architectureV2ApplicationLifecycleContracts: [
	#ApplicationLifecycleContractV1 & {
		metadata: {
			id:          "photos"
			version:     "1.0.0"
			description: "Reusable owner-controlled lifecycle for the Photos Application Kit."
		}
		workloadRef: "photos"
		useCaseRef:  "photos"
		packageRef:  "photos"
		lifecycle: #StandardUseCaseLifecycle & {
			referenceVertical: true
			stages: setup: {}
		}
	},
	#ApplicationLifecycleContractV1 & {
		metadata: {
			id:          "files"
			version:     "1.0.0"
			description: "Reusable owner-controlled lifecycle for the Files Application Kit."
		}
		workloadRef: "files"
		useCaseRef:  "files"
		packageRef:  "files"
		lifecycle:   #StandardUseCaseLifecycle
	},
	#ApplicationLifecycleContractV1 & {
		metadata: {
			id:          "vault"
			version:     "1.0.0"
			description: "Reusable owner-controlled lifecycle for the Vault Application Kit."
		}
		workloadRef: "vault"
		useCaseRef:  "vault"
		packageRef:  "vault"
		lifecycle:   #StandardUseCaseLifecycle
	},
	#ApplicationLifecycleContractV1 & {
		metadata: {
			id:          "media"
			version:     "1.0.0"
			description: "Reusable owner-controlled lifecycle for the Media Application Kit."
		}
		workloadRef: "media"
		useCaseRef:  "media"
		packageRef:  "media"
		lifecycle:   #StandardUseCaseLifecycle
	},
	#ApplicationLifecycleContractV1 & {
		metadata: {
			id:          "smart-home"
			version:     "1.0.0"
			description: "Reusable owner-controlled lifecycle for the Smart Home Application Kit."
		}
		workloadRef: "smart-home"
		useCaseRef:  "smart-home"
		packageRef:  "smart-home"
		lifecycle: #StandardUseCaseLifecycle & {stages: setup: {}}
	},
]

_architectureV2TLSCapabilityContracts: [
	{
		metadata: {
			id:          "internal-pki"
			version:     "1.0.0"
			description: "Home-private certificate policy resolved through a dedicated internal CA adapter."
			layer:       "platform"
		}
		supportedSiteKinds: ["home"]
		evidence: ["internal-pki-contract"]
		tlsProfile: {
			id:   "stackkits-internal-pki-profile", capabilityRef: "internal-pki"
			mode: "internal", trustDomain:                         "private", minimumVersion: "TLS1.2"
			allowedIssuerKinds: ["internal-ca"]
		}
	},
	{
		metadata: {
			id:          "public-tls"
			version:     "1.0.0"
			description: "Public WebPKI certificate policy resolved through a dedicated ACME edge adapter."
			layer:       "platform"
		}
		supportedSiteKinds: ["cloud"]
		evidence: ["public-tls-contract"]
		tlsProfile: {
			id:   "stackkits-public-tls-profile", capabilityRef: "public-tls"
			mode: "terminate-at-edge", trustDomain:              "web-pki", minimumVersion: "TLS1.2"
			allowedIssuerKinds: ["acme"]
		}
	},
]

// Product-private catalog extensions override these defaults in a separate CUE
// source owned by the corresponding authority profile. A public projection can
// therefore omit the entire extension source without editing catalog text.
_architectureV2ProfileExtensionCapabilityContracts: [...#CapabilityContract] | *[]
_architectureV2ProfileExtensionProviders: [...#CapabilityProvider] | *[]
_architectureV2ProfileExtensionModules: [...#ModuleContractV2] | *[]
_architectureV2ProfileExtensionPrivilegedInterfaceApprovals: [...#PrivilegedInterfaceApprovalV2] | *[]

_architectureV2HACapabilities: ["availability-ha"]

// HA remains one add-on surface, while each kit and control-plane mode selects
// a distinct governed capability-adapter/module realization. The KitDefinition owns the
// policy envelope and references these immutable catalog IDs.
_architectureV2HARealizations: [
	{
		providerID: "stackkits-ha-basement-warm", moduleID: "stackkits-ha-basement-warm-runtime"
		supportedSiteKinds: ["home"], healthID: "ha-basement-warm-contract"
		authoritySelection:                     "any"
		evidenceRef:                            "ha-basement-warm-standby-failure-domain-proof"
	},
	{
		providerID: "stackkits-ha-basement-quorum", moduleID: "stackkits-ha-basement-quorum-runtime"
		supportedSiteKinds: ["home"], healthID: "ha-basement-quorum-contract"
		authoritySelection:                     "any"
		evidenceRef:                            "ha-basement-quorum-failure-domain-proof"
	},
	{
		providerID: "stackkits-ha-cloud-warm", moduleID: "stackkits-ha-cloud-warm-runtime"
		supportedSiteKinds: ["cloud"], healthID: "ha-cloud-warm-contract"
		authoritySelection:                      "any"
		evidenceRef:                             "ha-cloud-warm-standby-failure-domain-proof"
	},
	{
		providerID: "stackkits-ha-cloud-quorum", moduleID: "stackkits-ha-cloud-quorum-runtime"
		supportedSiteKinds: ["cloud"], healthID: "ha-cloud-quorum-contract"
		authoritySelection:                      "any"
		evidenceRef:                             "ha-cloud-quorum-failure-domain-majority-proof"
	},
	{
		providerID: "stackkits-ha-modern-warm", moduleID: "stackkits-ha-modern-warm-runtime"
		supportedSiteKinds: ["home", "cloud"], healthID: "ha-modern-warm-contract"
		authoritySelection:                              "control-authority-site"
		evidenceRef:                                     "ha-modern-warm-standby-partition-isolation-proof"
	},
	{
		providerID: "stackkits-ha-modern-quorum", moduleID: "stackkits-ha-modern-quorum-runtime"
		supportedSiteKinds: ["home", "cloud"], healthID: "ha-modern-quorum-contract"
		authoritySelection:                              "control-authority-site"
		evidenceRef:                                     "ha-modern-quorum-partition-majority-proof"
	},
]

// The canonical plan snapshot is the only artifact that exists independently
// of selected modules. Compose/OpenTofu outputs belong to concrete module
// contracts and must never be enumerated by Go compiler configuration.
_architectureV2PlanArtifacts: [#CatalogPlanArtifactV2 & {
	id:       "resolved-plan"
	kind:     "metadata"
	path:     ".stackkit/resolved-plan.json"
	format:   "json"
	mode:     "0600"
	required: true
	compatibleTargets: ["compose", "opentofu", "terramate"]
}]

// These capabilities are intentionally catalogued without a provider. A kit
// that forbids one can therefore explain the governed contract, while any
// attempt to enable it still fails closed because no realization is approved.
_architectureV2DeniedCapabilities: [
	"cloud-enrollment-authority",
	"broad-lan-route-advertisement",
]

_architectureV2Capabilities: list.Concat([
	[for capabilityID in _architectureV2CoreCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Shared StackKits Architecture v2 contract for \(capabilityID)."
			layer:       "foundation"
		}
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["resolved-plan-contract"]
	}],
	[for capabilityID in _architectureV2TelemetryCollectionCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Optional per-node OTLP collector-intent generation; runtime lifecycle, credentials, and telemetry backend ownership remain external."
			layer:       "platform"
		}
		requires: [{id: "observability-evidence"}]
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["monitoring-agent-intent-contract"]
	}],
	[for capabilityID in _architectureV2LocalCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Home-site Architecture v2 contract for \(capabilityID)."
			layer:       "platform"
		}
		supportedSiteKinds: ["home"]
		evidence: ["SK-S1"]
	}],
	[for capabilityID in _architectureV2BasementComposeCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Basement-owned Compose rollout contract; it is not inferred from a home site or legacy context."
			layer:       "platform"
		}
		requires: [{id: "site-local"}]
		supportedSiteKinds: ["home"]
		evidence: ["basement-compose-contract-governance"]
	}],
	[for capabilityID in _architectureV2CloudCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Cloud-site Architecture v2 contract for \(capabilityID)."
			layer:       "platform"
		}
		supportedSiteKinds: ["cloud"]
		evidence: ["SK-S2", "SK-S3"]
	}],
	_architectureV2ApplicationCapabilityContracts,
	_architectureV2TLSCapabilityContracts,
	_architectureV2ProfileExtensionCapabilityContracts,
	[for capabilityID in _architectureV2HACapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "High-availability add-on contract for \(capabilityID)."
			layer:       "operations"
		}
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["ha-failure-domain-proof"]
	}],
	[for capabilityID in _architectureV2DeniedCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Known unsafe or profile-incompatible contract for \(capabilityID)."
			layer:       "foundation"
		}
		supportedSiteKinds: ["home", "cloud"]
	}],
])

_architectureV2Providers: list.Concat([[
	{
		metadata: {id: "stackkits-core-topology", version: "1.0.0"}
		provides: _architectureV2CoreTopologyCapabilities
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "topology", topology: {siteKinds: ["home", "cloud"]}}
		selection: defaultForSiteKinds: ["home", "cloud"]
	},
	{
		metadata: {id: "stackkits-service-catalog", version: "1.0.0"}
		provides: _architectureV2ServiceCatalogCapabilities
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["home", "cloud"]
	},
	{
		metadata: {id: "stackkits-access-policy-contract", version: "1.0.0"}
		provides: _architectureV2AccessPolicyCapabilities
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["home", "cloud"]
	},
	{
		metadata: {id: "stackkits-storage-data-policy", version: "1.1.0"}
		provides: _architectureV2StorageDataPolicyCapabilities
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-storage-allocation", "stackkits-workload-data-binding"]
				optional: []
			}
		}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "storage-data-policy-contract", kind: "contract"}]
		evidence: ["storage-allocation-contract", "workload-data-binding-contract"]
	},
	{
		metadata: {id: "stackkits-workload-runtime-contract", version: "1.0.0"}
		provides: _architectureV2WorkloadRuntimeContractCapabilities
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["home", "cloud"]
	},
	{
		metadata: {id: "stackkits-secrets-recovery-contract", version: "1.0.0"}
		provides: _architectureV2SecretsRecoveryCapabilities
		requires: [{id: "storage-data-policy"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["home", "cloud"]
	},
	{
		metadata: {id: "stackkits-backup-core-contract", version: "1.1.0"}
		provides: _architectureV2BackupCoreCapabilities
		requires: [{id: "storage-data-policy"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: [
					"stackkits-backup-source",
					"stackkits-snapshot",
					"stackkits-restore",
					"stackkits-recovery",
				]
				optional: []
			}
		}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "backup-lifecycle-contract", kind: "contract"}]
		evidence: [
			"backup-source-contract",
			"snapshot-anchor",
			"restore-result",
			"recovery-evidence",
		]
	},
	{
		metadata: {id: "stackkits-observability-evidence-contract", version: "1.0.0"}
		provides: _architectureV2ObservabilityEvidenceCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["home", "cloud"]
	},
	{
		metadata: {id: "stackkits-monitoring-agent", version: "1.1.0"}
		provides: _architectureV2TelemetryCollectionCapabilities
		requires: [{id: "observability-evidence"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-monitoring-agent-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "monitoring-agent-intent-contract", kind: "contract"}]
		evidence: ["monitoring-agent-intent-contract"]
	},
	{
		metadata: {id: "stackkits-lifecycle-update-contract", version: "1.0.0"}
		provides: _architectureV2LifecycleUpdateCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["home", "cloud"]
	},
	{
		metadata: {id: "stackkits-security-baseline", version: "1.0.0"}
		provides: _architectureV2SecurityBaselineCapabilities
		requires: [{id: "topology-core"}, {id: "external-host-admission"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["security-baseline"], optional: []}}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "security-baseline-contract", kind: "contract"}]
		evidence: ["security-baseline-executor-contract"]
	},
	{
		metadata: {id: "stackkits-core-host-bootstrap", version: "1.0.0"}
		provides: _architectureV2CoreHostBootstrapCapabilities
		requires: [{id: "topology-core"}, {id: "external-host-admission"}, {id: "storage-data-policy"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-core-host-bootstrap"], optional: []}}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "core-host-bootstrap-contract", kind: "contract"}]
		evidence: ["core-host-bootstrap-executor-contract"]
	},
	{
		metadata: {id: "stackkits-host-admission", version: "1.0.0"}
		provides: _architectureV2HostAdmissionCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind:               "host"
			ownerRef:           "stackkits-host-admission"
			realizationSupport: _architectureV2HostAdmissionSupport
			inputBindings: {}
		}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "stackkits-host-admission-contract", kind: "contract"}]
		evidence: ["host-conformance-receipt-contract"]
	},
	{
		metadata: {id: "stackkits-home-device-authority", version: "1.0.0"}
		provides: _architectureV2HomeIdentityAuthorityCapabilities
		requires: [{id: "topology-core"}, {id: "device-trust-core"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-device-authority-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "home-device-authority-policy-contract", kind: "contract"}]
		evidence: ["home-device-authority-policy-contract"]
	},
	{
		metadata: {id: "stackkits-basement-identity-trust-policy", version: "1.0.0"}
		provides: _architectureV2IdentityCapabilities
		requires: [{id: "topology-core"}, {id: "device-enrollment-home"}, {id: "local-control-authority"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-basement-identity-trust-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "basement-identity-trust-policy-contract", kind: "contract"}]
		evidence: ["basement-identity-trust-policy-contract"]
	},
	{
		metadata: {id: "stackkits-cloud-identity-trust-policy", version: "1.0.0"}
		provides: list.Concat([_architectureV2IdentityCapabilities, _architectureV2CloudIdentityAuthorityCapabilities])
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-identity-trust-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "cloud-identity-trust-policy-contract", kind: "contract"}]
		evidence: ["cloud-identity-trust-policy-contract"]
	},
	{
		metadata: {id: "stackkits-local", version: "1.0.0"}
		provides: _architectureV2LocalTopologyCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "topology", topology: {siteKinds: ["home"]}}
		selection: defaultForSiteKinds: ["home"]
	},
	{
		metadata: {id: "stackkits-home-lan-dns-contract", version: "1.0.0"}
		provides: _architectureV2HomeLANDNSCapabilities
		requires: [{id: "site-local"}, {id: "service-catalog"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["home"]
	},
	{
		metadata: {id: "stackkits-home-private-remote-access", version: "1.0.0"}
		provides: _architectureV2HomePrivateRemoteAccessCapabilities
		requires: [{id: "site-local"}, {id: "lan-access-policy"}, {id: "device-trust-core"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-private-remote-access-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "home-private-remote-access-contract", kind: "contract"}]
		evidence: ["home-private-remote-access-contract"]
	},
	{
		metadata: {id: "stackkits-home-public-publish-egress", version: "1.0.0"}
		provides: _architectureV2HomePublicPublishEgressCapabilities
		requires: [{id: "site-local"}, {id: "access-policy"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-public-publish-egress-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "home-public-publish-egress-contract", kind: "contract"}]
		evidence: ["home-public-publish-egress-contract"]
	},
	{
		metadata: {id: "stackkits-home-encrypted-offsite-backup", version: "1.0.0"}
		provides: _architectureV2HomeEncryptedOffsiteBackupCapabilities
		requires: [{id: "site-local"}, {id: "backup-core"}, {id: "storage-data-policy"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-encrypted-offsite-backup-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "home-encrypted-offsite-backup-contract", kind: "contract"}]
		evidence: ["home-encrypted-offsite-backup-contract"]
	},
	{
		metadata: {id: "stackkits-home-backup-target", version: "1.0.0"}
		provides: _architectureV2HomeBackupTargetCapabilities
		requires: [{id: "host-bootstrap"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-backup-target"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "home-backup-target-contract", kind: "contract"}]
		evidence: ["home-backup-target-executor-contract"]
	},
	{
		metadata: {id: "stackkits-internal-pki", version: "1.0.0"}
		provides: _architectureV2InternalPKICapabilities
		requires: [{id: "site-local"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-internal-pki-contract"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "internal-pki-renewal-contract", kind: "contract"}]
		evidence: ["internal-pki-contract"]
		certificateIssuers: [{
			id: "stackkits-internal-ca", capabilityRef: "internal-pki", kind: "internal-ca", challenge: "none"
			supportedSiteKinds: ["home"], validitySeconds: 7776000
			owner: {providerRef: "stackkits-internal-pki", moduleRef: "stackkits-internal-pki-contract", materializationSupport: "contract-only"}
			requiredInputSlotIDs: []
			materialSlots: [
				{id: "root-certificate", purpose: "certificate-chain", sensitivity: "public"},
				{id: "root-private-key", purpose: "private-key", sensitivity: "secret"},
				{id: "trust-root", purpose: "trust-root", sensitivity: "public"},
			]
			renewal: {required: true, healthGateRef: "internal-pki-renewal-contract", renewBeforeSeconds: 2592000}
		}]
	},
	{
		metadata: {id: "stackkits-local-autonomy-policy", version: "1.0.0"}
		provides: _architectureV2LocalAutonomyCapabilities
		requires: [{id: "site-local"}, {id: "local-control-authority"}, {id: "device-enrollment-home"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-local-autonomy-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-local-autonomy-contract", kind: "contract"}]
		evidence: ["local-autonomy-policy-contract"]
	},
	{
		metadata: {id: "stackkits-home-access-policy", version: "1.0.0"}
		provides: _architectureV2HomeAccessCapabilities
		requires: [{id: "site-local"}, {id: "device-enrollment-home"}, {id: "device-trust-core"}, {id: "access-policy"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-access-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-home-access-policy-contract", kind: "contract"}]
		evidence: ["home-access-policy-contract"]
	},
	{
		metadata: {id: "stackkits-home-lan-discovery-policy", version: "1.0.0"}
		provides: _architectureV2HomeLANDiscoveryCapabilities
		requires: [{id: "site-local"}, {id: "local-ingress"}, {id: "lan-access-policy"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-lan-discovery-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-home-lan-discovery-policy-contract", kind: "contract"}]
		evidence: ["home-lan-discovery-policy-contract"]
	},
	{
		metadata: {id: "stackkits-cloud-topology", version: "1.0.0"}
		provides: _architectureV2CloudTopologyCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "topology", topology: {siteKinds: ["cloud"]}}
		selection: defaultForSiteKinds: ["cloud"]
	},
	{
		metadata: {id: "stackkits-cloud-placement-policy", version: "1.0.0"}
		provides: _architectureV2CloudPlacementPolicyCapabilities
		requires: [{id: "site-cloud"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["cloud"]
	},
	{
		metadata: {id: "stackkits-cloud-host-security", version: "1.1.0"}
		provides: _architectureV2CloudHostSecurityCapabilities
		requires: [{id: "site-cloud"}, {id: "host-bootstrap"}, {id: "security-baseline"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-host-security-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "cloud-host-security-contract", kind: "contract"}]
		evidence: ["cloud-host-security-contract"]
	},
	{
		metadata: {id: "stackkits-cloud-public-dns-contract", version: "1.0.0"}
		provides: _architectureV2CloudPublicDNSCapabilities
		requires: [{id: "site-cloud"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "contract"}
		selection: defaultForSiteKinds: ["cloud"]
	},
	{
		metadata: {id: "stackkits-cloud-public-edge", version: "1.0.0"}
		provides: _architectureV2CloudPublicEdgeCapabilities
		requires: [{id: "site-cloud"}, {id: "host-local-internet-firewall"}, {id: "internet-host-hardening"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-public-edge-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "cloud-public-edge-contract", kind: "contract"}]
		evidence: ["cloud-public-edge-contract"]
	},
	{
		metadata: {id: "stackkits-cloud-offsite-backup", version: "1.0.0"}
		provides: _architectureV2CloudOffsiteBackupCapabilities
		requires: [{id: "site-cloud"}, {id: "storage-data-policy"}, {id: "backup-core"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-offsite-backup-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "cloud-offsite-backup-contract", kind: "contract"}]
		evidence: ["cloud-offsite-backup-contract"]
	},
	{
		metadata: {id: "stackkits-cloud-private-admin-mesh", version: "1.0.0"}
		provides: _architectureV2CloudPrivateAdminMeshCapabilities
		requires: [{id: "site-cloud"}, {id: "host-local-internet-firewall"}, {id: "device-trust-core"}, {id: "human-identity-core"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-private-admin-mesh-runtime"], optional: []}}
		health: [{id: "cloud-private-admin-mesh-contract", kind: "contract"}]
		evidence: ["cloud-private-admin-mesh-contract"]
	},
	{
		metadata: {id: "stackkits-public-tls", version: "1.0.0"}
		provides: _architectureV2PublicTLSCapabilities
		requires: [{id: "site-cloud"}, {id: "public-edge"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-public-tls-contract"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "public-tls-renewal-contract", kind: "contract"}]
		evidence: ["public-tls-contract"]
		certificateIssuers: [{
			id: "stackkits-public-acme", capabilityRef: "public-tls", kind: "acme", challenge: "http-01"
			supportedSiteKinds: ["cloud"], validitySeconds: 7776000
			owner: {providerRef: "stackkits-public-tls", moduleRef: "stackkits-public-tls-contract", materializationSupport: "contract-only"}
			requiredInputSlotIDs: []
			materialSlots: [
				{id: "certificate", purpose: "certificate-chain", sensitivity: "public"},
				{id: "private-key", purpose: "private-key", sensitivity: "secret"},
				{id: "acme-account-key", purpose: "issuer-account-key", sensitivity: "secret"},
			]
			renewal: {required: true, healthGateRef: "public-tls-renewal-contract", renewBeforeSeconds: 2592000}
		}]
	},
	{
		metadata: {id: "stackkits-basement-compose", version: "1.0.0"}
		provides: _architectureV2BasementComposeCapabilities
		requires: [{id: "topology-core"}, {id: "site-local"}]
		supportedSiteKinds: ["home"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-basement-compose-runtime"]
				optional: ["socket-proxy"]
			}
		}
		health: [{id: "stackkits-basement-compose-contract", kind: "contract"}]
		evidence: ["basement-compose-contract-governance"]
	},
	{
		metadata: {id: "stackkits-basement-core", version: "1.0.0"}
		provides: _architectureV2BasementCoreCapabilities
		workloadRefs: ["basement-core"]
		requires: [
			{id: "backup-core"},
			{id: "host-bootstrap"},
			{id: "local-backup-target"},
			{id: "local-ingress"},
			{id: "service-catalog"},
		]
		supportedSiteKinds: ["home"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: []
				optional: ["stackkits-basement-core-runtime", "stackkits-basement-core-lite-runtime"]
			}
		}
		health: [{id: "basement-core-provider-contract", kind: "contract"}]
		evidence: ["basement-core-runtime-evidence"]
	},
	{
		metadata: {id: "stackkits-cloud-core", version: "1.0.0"}
		provides: _architectureV2CloudCoreCapabilities
		workloadRefs: ["cloud-core"]
		requires: [
			{id: "host-bootstrap"},
			{id: "public-edge"},
			{id: "public-tls"},
			{id: "service-catalog"},
		]
		supportedSiteKinds: ["cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-cloud-core-runtime"]
				optional: []
			}
		}
		health: [{id: "cloud-core-provider-contract", kind: "contract"}]
		evidence: ["cloud-core-runtime-evidence"]
	},
	{
		metadata: {id: "stackkits-immich", version: "1.1.0"}
		provides: []
		workloadRefs: ["photos"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
			{id: "storage-data-policy"},
			{id: "backup-core"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: []
				optional: ["stackkits-immich-runtime"]
			}
		}
		evidence: ["SK-S1", "SK-S2", "SK-S4"]
	},
	{
		metadata: {id: "stackkits-immich-lite", version: "1.0.0"}
		provides: []
		workloadRefs: ["photos"]
		requires: [
			{id: "service-catalog"},
			{id: "storage-data-policy"},
			{id: "backup-core"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: []
				optional: ["stackkits-immich-lite-runtime"]
			}
		}
		evidence: ["SK-S1", "SK-S2", "SK-S4"]
	},
	{
		metadata: {id: "stackkits-cloudreve", version: "1.1.0"}
		provides: []
		workloadRefs: ["files"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
			{id: "storage-data-policy"},
			{id: "backup-core"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: []
				optional: ["stackkits-cloudreve-runtime"]
			}
		}
		evidence: ["cloudreve-selected-paas-runtime-contract"]
	},
	{
		metadata: {id: "stackkits-vaultwarden", version: "1.0.0"}
		provides: []
		workloadRefs: ["vault"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
			{id: "storage-data-policy"},
			{id: "backup-core"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: []
				optional: ["stackkits-vaultwarden-runtime"]
			}
		}
		evidence: ["vaultwarden-selected-paas-runtime-contract"]
	},
	{
		metadata: {id: "stackkits-jellyfin", version: "1.0.0"}
		provides: []
		workloadRefs: ["media"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
			{id: "storage-data-policy"},
			{id: "backup-core"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: []
				optional: ["stackkits-jellyfin-runtime"]
			}
		}
		evidence: ["jellyfin-selected-paas-runtime-contract"]
	},
	{
		metadata: {id: "stackkits-home-assistant", version: "1.0.0"}
		provides: []
		workloadRefs: ["smart-home"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
			{id: "storage-data-policy"},
			{id: "backup-core"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: []
				optional: ["stackkits-home-assistant-runtime"]
			}
		}
		evidence: ["home-assistant-selected-paas-runtime-contract"]
	},
	{
		metadata: {id: "stackkits-coolify", version: "1.0.0"}
		provides: []
		runtimeAdapterRefs: ["coolify"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-coolify-runtime"]
				optional: []
			}
		}
		health: [{id: "coolify-runtime-contract", kind: "contract"}]
		evidence: ["coolify-adapter-contract"]
	},
	{
		metadata: {id: "stackkits-komodo", version: "1.0.0"}
		provides: []
		runtimeAdapterRefs: ["komodo"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-komodo-core-runtime", "stackkits-komodo-periphery-runtime"]
				optional: []
			}
		}
		health: [
			{id: "komodo-core-runtime-contract", kind: "contract"},
			{id: "komodo-periphery-runtime-contract", kind: "contract"},
		]
		evidence: ["komodo-core-adapter-contract", "komodo-periphery-agent-contract"]
	},
	{
		metadata: {id: "stackkits-standalone-compose", version: "1.0.0"}
		provides: []
		runtimeAdapterRefs: ["standalone-compose"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-standalone-compose-runtime"]
				optional: []
			}
		}
		health: [{id: "standalone-compose-runtime-contract", kind: "contract"}]
		evidence: ["standalone-compose-adapter-contract"]
	},
],
	_architectureV2ProfileExtensionProviders,
	[for haRealization in _architectureV2HARealizations {
		metadata: {id: haRealization.providerID, version: "1.0.0"}
		provides: _architectureV2HACapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: haRealization.supportedSiteKinds
		realization: {kind: "modules", moduleRefs: {required: [haRealization.moduleID], optional: []}}
		health: [{id: haRealization.healthID, kind: "contract"}]
		evidence: [haRealization.evidenceRef, "ha-availability-runtime-contract"]
	}],
])

_architectureV2UmbrellaSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "umbrella"
	level:           "contract-only"
	compatibleRendererRefs: []
	inputs: {
		contractComplete: false
		requiredRefs: []
	}
	artifacts: requiredRefs: []
	evidence: requiredRefs: []
}

_architectureV2HAAvailabilityRuntimeSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {contractComplete: true, requiredRefs: ["stackId", "kit", "sites", "controlPlane", "moduleTargets", "moduleCapabilities", "availability"]}
	artifacts: {}
	evidence: requiredRefs: ["ha-availability-runtime-contract"]
}

_architectureV2TLSContractSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "contract-only"
	compatibleRendererRefs: []
	inputs: {contractComplete: false, requiredRefs: []}
	planInputs: {contractComplete: false, requiredRefs: []}
	artifacts: {requiredRefs: [], outputBindings: [], contracts: []}
	evidence: requiredRefs: []
}

_architectureV2SharedInfrastructureContractSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "contract-only"
	compatibleRendererRefs: []
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {contractComplete: true, requiredRefs: []}
	artifacts: {requiredRefs: [], outputBindings: [], contracts: []}
	evidence: requiredRefs: []
}

_architectureV2InternalPKIGenerationSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {contractComplete: true, requiredRefs: ["internalPKI", "kit", "stackId"]}
	artifacts: {
		requiredRefs: ["internal-pki-executor-contract"]
		outputBindings: [{artifactRef: "internal-pki-executor-contract", unitRef: "executor-contract", outputRef: "home/tls/internal-pki-executor-contract.json"}]
		contracts: [{
			id: "internal-pki-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["opentofu", "compose"]
			unitRef: "executor-contract", outputRef: "home/tls/internal-pki-executor-contract.json"
		}]
	}
	evidence: requiredRefs: ["internal-pki-contract"]
}

_architectureV2PublicTLSGenerationSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {contractComplete: true, requiredRefs: ["kit", "moduleTargets", "publicTLS", "stackId"]}
	artifacts: {
		requiredRefs: ["public-tls-executor-contract"]
		outputBindings: [{artifactRef: "public-tls-executor-contract", unitRef: "executor-contract", outputRef: "cloud/tls/executor-contract.json"}]
		contracts: [{
			id: "public-tls-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["opentofu", "compose"]
			unitRef: "executor-contract", outputRef: "cloud/tls/executor-contract.json"
		}]
	}
	evidence: requiredRefs: ["public-tls-contract"]
}

_architectureV2CoreHostBootstrapSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["host-runtime", "storage-roots"]}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "moduleTargets", "moduleCapabilities"]
	}
	artifacts: {
		requiredRefs: ["core-host-bootstrap-policy"]
		outputBindings: [{
			artifactRef: "core-host-bootstrap-policy", unitRef: "host-policy", outputRef: "foundation/host-bootstrap/policy.json"
		}]
		contracts: [{
			id: "core-host-bootstrap-policy", kind: "native-config", format: "json", mode: "0600", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "host-policy", outputRef: "foundation/host-bootstrap/policy.json"
		}]
	}
	evidence: requiredRefs: ["core-host-bootstrap-executor-contract"]
}

_architectureV2HomeBackupTargetSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["backup-root"]}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "moduleTargets", "moduleCapabilities"]
	}
	artifacts: {
		requiredRefs: ["home-backup-target-policy"]
		outputBindings: [{
			artifactRef: "home-backup-target-policy", unitRef: "backup-policy", outputRef: "home/backup/target-policy.json"
		}]
		contracts: [{
			id: "home-backup-target-policy", kind: "native-config", format: "json", mode: "0600", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "backup-policy", outputRef: "home/backup/target-policy.json"
		}]
	}
	evidence: requiredRefs: ["home-backup-target-executor-contract"]
}

_architectureV2HomeExtensionRuntimeArtifacts: {
	privateRemoteAccess: {
		id:                    "home-private-remote-access-executor-contract"
		outputRef:             "home/remote-access/executor-contract.json"
		requiresAccessBinding: true
	}
	publicPublishEgress: {
		id:                    "home-public-publish-egress-executor-contract"
		outputRef:             "home/publication/executor-contract.json"
		requiresAccessBinding: true
	}
	encryptedOffsiteBackup: {
		id:                    "home-encrypted-offsite-backup-executor-contract"
		outputRef:             "home/backup/offsite-executor-contract.json"
		requiresAccessBinding: false
		requiresBackupBinding: true
	}
	privateRemoteAccess: requiresBackupBinding: false
	publicPublishEgress: requiresBackupBinding: false
}

_architectureV2HomeExtensionRuntimeSupports: {
	for runtimeName, artifact in _architectureV2HomeExtensionRuntimeArtifacts {
		"\(runtimeName)": #ModuleRealizationSupportV2 & {
			contractVersion: "1.0.0"
			scope:           "concrete"
			if runtimeName == "privateRemoteAccess" {
				level: "apply-ready"
			}
			if runtimeName != "privateRemoteAccess" {
				level: "generation-ready"
			}
			compatibleRendererRefs: ["stackkit"]
			inputs: {contractComplete: true, requiredRefs: []}
			planInputs: {
				contractComplete: true
				requiredRefs: list.Concat([
					["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"],
					[for ref in ["homeAccessHandoff"] if artifact.requiresAccessBinding {ref}],
					[for ref in ["homeOffsiteBackup"] if artifact.requiresBackupBinding {ref}],
				])
			}
			artifacts: {
				requiredRefs: [artifact.id]
				outputBindings: [{artifactRef: artifact.id, unitRef: "executor-contract", outputRef: artifact.outputRef}]
				contracts: [{
					id: artifact.id, kind: "native-config", format: "json", mode: "0640", required: true
					compatibleTargets: ["compose", "opentofu"]
					unitRef: "executor-contract", outputRef: artifact.outputRef
				}]
			}
			evidence: requiredRefs: [for ref in ["home-private-remote-access-evidence"] if runtimeName == "privateRemoteAccess" {ref}]
		}
	}
}

_architectureV2BasementComposeExecutorContractSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
	}
	artifacts: {
		requiredRefs: ["basement-compose-runtime-executor-contract"]
		outputBindings: [{
			artifactRef: "basement-compose-runtime-executor-contract", unitRef: "executor-contract", outputRef: "basement/runtime/executor-contract.json"
		}]
		contracts: [{
			id: "basement-compose-runtime-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "executor-contract", outputRef: "basement/runtime/executor-contract.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2CloudHostSecuritySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["host-security-network"]}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
	}
	artifacts: {
		requiredRefs: ["cloud-host-security-executor-contract"]
		outputBindings: [{artifactRef: "cloud-host-security-executor-contract", unitRef: "executor-contract", outputRef: "cloud/host-security/executor-contract.json"}]
		contracts: [{
			id: "cloud-host-security-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "executor-contract", outputRef: "cloud/host-security/executor-contract.json"
		}]
	}
	evidence: requiredRefs: ["cloud-host-security-evidence"]
}

_architectureV2CloudPublicEdgeSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "publicEdge"]
	}
	artifacts: {
		requiredRefs: ["cloud-public-edge-executor-contract"]
		outputBindings: [{artifactRef: "cloud-public-edge-executor-contract", unitRef: "executor-contract", outputRef: "cloud/public-edge/executor-contract.json"}]
		contracts: [{
			id: "cloud-public-edge-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "executor-contract", outputRef: "cloud/public-edge/executor-contract.json"
		}]
	}
	evidence: requiredRefs: ["cloud-public-edge-evidence"]
}

_architectureV2CloudOffsiteBackupSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "cloudOffsiteBackup"]
	}
	artifacts: {
		requiredRefs: ["cloud-offsite-backup-executor-contract"]
		outputBindings: [{artifactRef: "cloud-offsite-backup-executor-contract", unitRef: "executor-contract", outputRef: "cloud/backup/executor-contract.json"}]
		contracts: [{
			id: "cloud-offsite-backup-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "executor-contract", outputRef: "cloud/backup/executor-contract.json"
		}]
	}
	evidence: requiredRefs: ["cloud-offsite-backup-evidence"]
}

_architectureV2CloudPrivateAdminMeshSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "cloudAdminMesh"]
	}
	artifacts: {
		requiredRefs: ["cloud-private-admin-mesh-executor-contract"]
		outputBindings: [{artifactRef: "cloud-private-admin-mesh-executor-contract", unitRef: "executor-contract", outputRef: "cloud/admin-mesh/executor-contract.json"}]
		contracts: [{
			id: "cloud-private-admin-mesh-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "executor-contract", outputRef: "cloud/admin-mesh/executor-contract.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2MonitoringAgentSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["observability", "sites"]
	}
	artifacts: {
		requiredRefs: ["monitoring-agent-collector-intent"]
		outputBindings: [{
			artifactRef: "monitoring-agent-collector-intent"
			unitRef:     "collector-intent"
			outputRef:   "observability/monitoring-agent/collector-intent.json"
		}]
		contracts: [{
			id:       "monitoring-agent-collector-intent"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "collector-intent"
			outputRef: "observability/monitoring-agent/collector-intent.json"
		}]
	}
	evidence: requiredRefs: ["monitoring-agent-runtime-evidence"]
}

// Host admission and conformance are pre-generation runtime gates, not
// generated files. Their concrete implementation is the binding/receipt
// producer and admission path; modeling them as a host owner keeps them out of
// the residual Core module without fabricating a render unit or artifact.
_architectureV2HostAdmissionSupport: #NonRenderingProviderOwnerRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: []
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: requiredRefs: []
	evidence: requiredRefs: ["host-conformance-receipt-contract"]
}

_architectureV2SecurityBaselineSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: ["security-baseline-host-policy"]
		outputBindings: [{
			artifactRef: "security-baseline-host-policy"
			unitRef:     "host-policy"
			outputRef:   "foundation/security-baseline/apply.sh"
		}]
		contracts: [{
			id:       "security-baseline-host-policy"
			kind:     "script"
			format:   "shell"
			mode:     "0700"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "host-policy"
			outputRef: "foundation/security-baseline/apply.sh"
		}]
	}
	evidence: requiredRefs: ["security-baseline-executor-contract"]
}

_architectureV2LocalAutonomySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: ["local-autonomy-policy"]
	}
	planInputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: ["local-autonomy-policy"]
		outputBindings: [{
			artifactRef: "local-autonomy-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/autonomy/policy.json"
		}]
		contracts: [{
			id:       "local-autonomy-policy"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "policy-bundle"
			outputRef: "local/autonomy/policy.json"
		}]
	}
	evidence: requiredRefs: ["local-autonomy-enforcement"]
}

_architectureV2HomeAccessSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: ["home-access-policy"]
	}
	planInputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: ["home-access-policy"]
		outputBindings: [{
			artifactRef: "home-access-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/network/access-policy.json"
		}]
		contracts: [{
			id:       "home-access-policy"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "policy-bundle"
			outputRef: "local/network/access-policy.json"
		}]
	}
	evidence: requiredRefs: ["home-access-enforcement"]
}

_architectureV2HomeLANDiscoverySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "homeLANDiscovery"]
	}
	artifacts: {
		requiredRefs: ["home-lan-discovery-policy"]
		outputBindings: [{
			artifactRef: "home-lan-discovery-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/network/discovery-policy.json"
		}]
		contracts: [{
			id:       "home-lan-discovery-policy"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "policy-bundle"
			outputRef: "local/network/discovery-policy.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2HomeDeviceAuthoritySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["home-device-authority"]}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit"]
	}
	artifacts: {
		requiredRefs: ["home-device-authority-policy"]
		outputBindings: [{
			artifactRef: "home-device-authority-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/identity/device-authority-policy.json"
		}]
		contracts: [{
			id: "home-device-authority-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "local/identity/device-authority-policy.json"
		}]
	}
	evidence: requiredRefs: ["home-device-authority-enforcement"]
}

_architectureV2BasementIdentityTrustSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["basement-verification-policy"]}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit"]
	}
	artifacts: {
		requiredRefs: ["basement-identity-trust-policy"]
		outputBindings: [{
			artifactRef: "basement-identity-trust-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/identity/trust-policy.json"
		}]
		contracts: [{
			id: "basement-identity-trust-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "local/identity/trust-policy.json"
		}]
	}
	evidence: requiredRefs: ["basement-identity-trust-enforcement"]
}

_architectureV2CloudIdentityTrustSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["cloud-identity-authority"]}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit"]
	}
	artifacts: {
		requiredRefs: ["cloud-identity-trust-policy"]
		outputBindings: [{
			artifactRef: "cloud-identity-trust-policy"
			unitRef:     "policy-bundle"
			outputRef:   "cloud/identity/trust-policy.json"
		}]
		contracts: [{
			id: "cloud-identity-trust-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "cloud/identity/trust-policy.json"
		}]
	}
	evidence: requiredRefs: ["cloud-identity-trust-enforcement"]
}

_architectureV2SocketProxySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: ["socket-proxy-compose"]
		outputBindings: [{
			artifactRef: "socket-proxy-compose"
			unitRef:     "compose"
			outputRef:   "foundation/socket-proxy/compose.yaml"
		}]
		contracts: [{
			id:       "socket-proxy-compose"
			kind:     "compose"
			format:   "yaml"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose"]
			unitRef:   "compose"
			outputRef: "foundation/socket-proxy/compose.yaml"
		}]
	}
	evidence: requiredRefs: []
}

// Immich owns one complete provider-neutral, target-bound selected-PaaS bundle.
// Apply is admitted only through the exact selected-PaaS runtime registration
// and an authenticated execution channel; provider/PaaS lifecycle and
// credentials remain in the external operations owner's custody.
_architectureV2ImmichSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: ["database-password"]
	}
	artifacts: {
		requiredRefs: ["immich-workload-bundle"]
		outputBindings: [{
			artifactRef: "immich-workload-bundle"
			unitRef:     "immich-server"
			outputRef:   "workloads/immich/bundle.json"
		}]
		contracts: [{
			id:       "immich-workload-bundle"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "immich-server"
			outputRef: "workloads/immich/bundle.json"
		}]
	}
	evidence: requiredRefs: ["immich-selected-paas-runtime-contract"]
}

_architectureV2ImmichLiteSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: ["database-password"]
	}
	artifacts: {
		requiredRefs: ["immich-lite-workload-bundle"]
		outputBindings: [{
			artifactRef: "immich-lite-workload-bundle"
			unitRef:     "immich-server"
			outputRef:   "workloads/immich-lite/bundle.json"
		}]
		contracts: [{
			id:       "immich-lite-workload-bundle"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "immich-server"
			outputRef: "workloads/immich-lite/bundle.json"
		}]
	}
	evidence: requiredRefs: ["immich-selected-paas-runtime-contract"]
}

// Cloudreve is the second Application Kit vertical on the reusable lifecycle.
// Its artifact carries a complete pinned single-container runtime while the
// selected PaaS retains endpoint, credential, and provider lifecycle custody.
_architectureV2CloudreveSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {
		requiredRefs: ["cloudreve-workload-bundle"]
		outputBindings: [{
			artifactRef: "cloudreve-workload-bundle"
			unitRef:     "cloudreve"
			outputRef:   "workloads/cloudreve/bundle.json"
		}]
		contracts: [{
			id:       "cloudreve-workload-bundle"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "cloudreve"
			outputRef: "workloads/cloudreve/bundle.json"
		}]
	}
	evidence: requiredRefs: ["cloudreve-selected-paas-runtime-contract"]
}

// Vaultwarden is the third Application Kit vertical on the reusable
// lifecycle. Its bundle contains only an opaque admin-token reference; the
// selected PaaS retains credential material, endpoint, and provider custody.
_architectureV2VaultwardenSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["admin-token"]}
	artifacts: {
		requiredRefs: ["vaultwarden-workload-bundle"]
		outputBindings: [{
			artifactRef: "vaultwarden-workload-bundle"
			unitRef:     "vaultwarden"
			outputRef:   "workloads/vaultwarden/bundle.json"
		}]
		contracts: [{
			id:       "vaultwarden-workload-bundle"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "vaultwarden"
			outputRef: "workloads/vaultwarden/bundle.json"
		}]
	}
	evidence: requiredRefs: ["vaultwarden-selected-paas-runtime-contract"]
}

// Jellyfin is the Media Library vertical. Config is a StackKits backup source;
// the library volume is owner-custodied and excluded from backup.
_architectureV2JellyfinSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {
		requiredRefs: ["jellyfin-workload-bundle"]
		outputBindings: [{
			artifactRef: "jellyfin-workload-bundle"
			unitRef:     "jellyfin"
			outputRef:   "workloads/jellyfin/bundle.json"
		}]
		contracts: [{
			id:       "jellyfin-workload-bundle"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "jellyfin"
			outputRef: "workloads/jellyfin/bundle.json"
		}]
	}
	evidence: requiredRefs: ["jellyfin-selected-paas-runtime-contract"]
}

_architectureV2HomeAssistantSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {
		requiredRefs: ["home-assistant-workload-bundle"]
		outputBindings: [{
			artifactRef: "home-assistant-workload-bundle"
			unitRef:     "home-assistant"
			outputRef:   "workloads/home-assistant/bundle.json"
		}]
		contracts: [{
			id:       "home-assistant-workload-bundle"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "home-assistant"
			outputRef: "workloads/home-assistant/bundle.json"
		}]
	}
	evidence: requiredRefs: ["home-assistant-selected-paas-runtime-contract"]
}

// Coolify owns a deterministic provider-free adapter handoff. Generation does
// not claim that StackKits can install Coolify, hold its credentials, discover
// an endpoint, or mutate its provider lifecycle.
_architectureV2CoolifyAdapterSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {
		requiredRefs: ["coolify-runtime-adapter"]
		outputBindings: [{
			artifactRef: "coolify-runtime-adapter"
			unitRef:     "coolify-adapter"
			outputRef:   "platform/coolify/runtime-adapter.json"
		}]
		contracts: [{
			id:       "coolify-runtime-adapter"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "coolify-adapter"
			outputRef: "platform/coolify/runtime-adapter.json"
		}]
	}
	evidence: requiredRefs: []
}

// Standalone Compose is a StackKits-owned execution adapter over an already
// admitted Docker host. It consumes the same closed workload and route
// authority as PaaS adapters and owns neither host nor server-provider
// lifecycle.
_architectureV2StandaloneComposeAdapterSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {
		requiredRefs: ["standalone-compose-runtime-adapter"]
		outputBindings: [{
			artifactRef: "standalone-compose-runtime-adapter"
			unitRef:     "standalone-compose-adapter"
			outputRef:   "platform/standalone-compose/runtime-adapter.json"
		}]
		contracts: [{
			id:       "standalone-compose-runtime-adapter"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "standalone-compose-adapter"
			outputRef: "platform/standalone-compose/runtime-adapter.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2KomodoCoreAdapterSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {
		requiredRefs: ["komodo-core-runtime-adapter"]
		outputBindings: [{
			artifactRef: "komodo-core-runtime-adapter"
			unitRef:     "komodo-core-adapter"
			outputRef:   "platform/komodo/core-runtime-adapter.json"
		}]
		contracts: [{
			id:       "komodo-core-runtime-adapter"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "komodo-core-adapter"
			outputRef: "platform/komodo/core-runtime-adapter.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2KomodoPeripheryAgentSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {
		requiredRefs: ["komodo-periphery-runtime-agent"]
		outputBindings: [{
			artifactRef: "komodo-periphery-runtime-agent"
			unitRef:     "komodo-periphery-agent"
			outputRef:   "platform/komodo/periphery-agent.json"
		}]
		contracts: [{
			id:       "komodo-periphery-runtime-agent"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "komodo-periphery-agent"
			outputRef: "platform/komodo/periphery-agent.json"
		}]
	}
	evidence: requiredRefs: []
}

_basementCoreLiteServiceEndpoints: [
	for endpoint in _basementCoreServiceEndpoints
	if endpoint.serviceRef != "coolify" {endpoint},
]
_basementCoreLiteRuntimeListeners: [
	for listener in _basementCoreRuntimeListeners
	if listener.componentRef != "coolify" {listener},
]
_basementCoreLiteServiceControls: [
	for control in _basementCoreServiceControls
	if control.serviceRef != "coolify" {control},
]
_architectureV2PhotosLiteInfrastructure: #WorkloadInfrastructureV1 & {
	storageAllocation: {
		moduleRef: "stackkits-storage-allocation"
		allocations: [
			for allocation in _architectureV2PhotosInfrastructure.storageAllocation.allocations
			if allocation.componentRef != "immich-machine-learning" {allocation},
		]
	}
	dataBinding: _architectureV2PhotosInfrastructure.dataBinding
	backupSource: {
		moduleRef: "stackkits-backup-source"
		allocations: [
			for allocation in storageAllocation.allocations
			if allocation.backup {
				componentRef: allocation.componentRef
				volumeRef:    allocation.volumeRef
				dataClasses:  allocation.dataClasses
			},
		]
	}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

_basementCoreServiceEndpoints: [
	{
		serviceRef: "basement-hub", upstreamProtocol: "http", targetPort: 80
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["local", "remote-private"]
		originSelector: "control-authority-site"
		healthRef:      "basement-hub-http"
	},
	{
		serviceRef: "id", upstreamProtocol: "http", targetPort: 1411
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["local", "remote-private"]
		originSelector: "control-authority-site"
		healthRef:      "pocketid-http"
	},
	{
		serviceRef: "auth", upstreamProtocol: "http", targetPort: 3000
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["local", "remote-private"]
		originSelector: "control-authority-site"
		healthRef:      "tinyauth-http"
	},
	{
		serviceRef: "coolify", upstreamProtocol: "http", targetPort: 8080
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["local", "remote-private"]
		originSelector: "control-authority-site"
		healthRef:      "coolify-http"
	},
]

_cloudCoreServiceEndpoints: [
	{
		serviceRef: "base", upstreamProtocol: "http", targetPort: 80
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["remote-private", "public"]
		originSelector: "control-authority-site"
		healthRef:      "cloud-hub-http"
	},
	{
		serviceRef: "id", upstreamProtocol: "http", targetPort: 1411
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["remote-private", "public"]
		originSelector: "control-authority-site"
		healthRef:      "cloud-pocketid-http"
	},
	{
		serviceRef: "auth", upstreamProtocol: "http", targetPort: 3000
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["remote-private", "public"]
		originSelector: "control-authority-site"
		healthRef:      "cloud-tinyauth-http"
	},
	{
		serviceRef: "coolify", upstreamProtocol: "http", targetPort: 8080
		allowedIngressProtocols: ["http", "https"]
		allowedExposures: ["remote-private", "public"]
		originSelector: "control-authority-site"
		healthRef:      "cloud-coolify-http"
	},
]

_cloudCoreRuntimeListeners: [
	{id: "web-http", componentRef: "router", transport: "tcp", bindAddress: "0.0.0.0", port: 80, targetPort: 80, sharing: "virtual-host", listenerGroupRef: "public-web", exposure: "public", sourceServiceRefs: []},
	{id: "web-https", componentRef: "router", transport: "tcp", bindAddress: "0.0.0.0", port: 443, targetPort: 443, sharing: "virtual-host", listenerGroupRef: "public-web", exposure: "public", sourceServiceRefs: []},
	{id: "router-admin", componentRef: "router", transport: "tcp", bindAddress: "127.0.0.1", port: 8080, targetPort: 8080, sharing: "exclusive", exposure: "remote-private", sourceServiceRefs: []},
	{id: "pocketid-direct", componentRef: "pocketid", transport: "tcp", bindAddress: "0.0.0.0", port: 1411, targetPort: 1411, sharing: "exclusive", exposure: "remote-private", sourceServiceRefs: []},
	{id: "tinyauth-direct", componentRef: "tinyauth", transport: "tcp", bindAddress: "0.0.0.0", port: 4000, targetPort: 3000, sharing: "exclusive", exposure: "remote-private", sourceServiceRefs: []},
	{id: "coolify-direct", componentRef: "coolify", transport: "tcp", bindAddress: "0.0.0.0", port: 8000, targetPort: 8080, sharing: "exclusive", exposure: "remote-private", sourceServiceRefs: []},
]

_cloudCoreVerificationRuntimeListeners: list.Concat([_cloudCoreRuntimeListeners, [
	{id: "hub-health", componentRef: "hub", transport: "tcp", bindAddress: "127.0.0.1", port: 8081, targetPort: 80, sharing: "exclusive", exposure: "remote-private", sourceServiceRefs: []},
]])

_basementCoreRuntimeListeners: list.Concat([_cloudCoreRuntimeListeners, [
	{id: "step-ca-direct", componentRef: "step-ca", transport: "tcp", bindAddress: "0.0.0.0", port: 9000, targetPort: 9000, sharing: "exclusive", exposure: "remote-private", sourceServiceRefs: []},
]])

_sharedCoreServiceControls: [
	{key: "auth", serviceRef: "auth", adapter: "compose", runtimeRef: "cloud-core", componentRefs: ["tinyauth"], allowedActions: ["start", "restart", "logs"], critical: true},
	{key: "coolify", serviceRef: "coolify", adapter: "compose", runtimeRef: "cloud-core", componentRefs: ["coolify", "coolify-postgres", "coolify-redis", "coolify-realtime"], allowedActions: ["start", "stop", "restart", "logs"], critical: false},
	{key: "id", serviceRef: "id", adapter: "compose", runtimeRef: "cloud-core", componentRefs: ["pocketid"], allowedActions: ["start", "restart", "logs"], critical: true},
]

_cloudCoreServiceControls: list.Concat([_sharedCoreServiceControls, [
	{key: "base", serviceRef: "base", adapter: "compose", runtimeRef: "cloud-core", componentRefs: ["router", "socket-proxy", "hub"], allowedActions: ["start", "restart", "logs"], critical: true},
]])

_basementCoreServiceControls: list.Concat([_sharedCoreServiceControls, [
	{key: "base", serviceRef: "basement-hub", adapter: "compose", runtimeRef: "cloud-core", componentRefs: ["router", "socket-proxy", "step-ca", "kopia-agent", "hub"], allowedActions: ["start", "restart", "logs"], critical: true},
]])

_architectureV2LocalKopiaSourceRenderUnit: {
	_outputRef:   string | *"home/backup/kopia-source-policy.json"
	id:           "source-policy"
	kind:         "native-config"
	rendererRef:  "stackkit"
	applyMode:    "artifact-only"
	templateRef:  "builtin://home/backup/kopia-source/v1.json"
	version:      "1.0.0"
	contractHash: "sha256:7edfa9c808cedb14d3f58a7101f7a31f597fa09ea1488002587225a4ee43bee3"
	compatibleTargets: ["compose", "opentofu", "terramate"]
	publicInputRefs: ["backup-source"], secretInputRefs: []
	planInputRefs: ["stackId", "kit", "sites", "moduleTargets", "moduleCapabilities", "backupPolicy"]
	inputBindings: [{
		targetRef:   "backup-source"
		sourceRef:   "backup.localKopiaSource"
		valueType:   "local-kopia-backup-source-v1"
		cardinality: "single"
		required:    true
	}]
	outputs: [_outputRef]
	placement: {scope: "node-local", cardinality: "one-per-node"}
}

_architectureV2Modules: list.Concat([[
	{
		metadata: {
			id:          "stackkits-storage-allocation"
			version:     "1.0.0"
			description: "Reusable resolved-plan storage allocation authority shared by Application Kits."
		}
		role:        "foundation"
		providerRef: "stackkits-storage-data-policy"
		planOnly:    true
		provides:    _architectureV2StorageDataPolicyCapabilities
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
			requiredRoles: ["worker"]
		}
		storageAllocationContract: {
			apiVersion:            "stackkit.storage-allocation/v1"
			kind:                  "StorageAllocationContract"
			authority:             "resolved-plan"
			rootSourceRef:         "storage.dataRoot"
			volumeDriverSourceRef: "storage.volumeDriver"
			allocationIdentity:    "workload/component/volume"
			supportedClasses: ["persistent", "cache"]
			providerLifecycleOwned:        false
			credentialCustodyOwned:        false
			multiServerOrchestrationOwned: false
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		realizationSupport: _architectureV2SharedInfrastructureContractSupport
		health: [{id: "storage-allocation-contract", kind: "contract"}]
		evidence: ["storage-allocation-contract"]
	},
	{
		metadata: {
			id:          "stackkits-workload-data-binding"
			version:     "1.0.0"
			description: "Reusable workload-to-data-placement binding authority shared by Application Kits."
		}
		role:        "foundation"
		providerRef: "stackkits-storage-data-policy"
		planOnly:    true
		provides:    _architectureV2StorageDataPolicyCapabilities
		requires: ["stackkits-storage-allocation"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
			requiredRoles: ["worker"]
		}
		dataBindingContract: {
			apiVersion:                    "stackkit.workload-data-binding/v1"
			kind:                          "WorkloadDataBindingContract"
			authority:                     "resolved-plan"
			sourceRef:                     "data.bindings"
			bindingIdentity:               "workload/service"
			classValidation:               "workload-subset"
			placementValidation:           "primary-or-declared-replica"
			storageAllocationModuleRef:    "stackkits-storage-allocation"
			providerLifecycleOwned:        false
			credentialCustodyOwned:        false
			multiServerOrchestrationOwned: false
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		realizationSupport: _architectureV2SharedInfrastructureContractSupport
		health: [{id: "workload-data-binding-contract", kind: "contract"}]
		evidence: ["workload-data-binding-contract"]
	},
	{
		metadata: {
			id:          "stackkits-backup-source"
			version:     "1.0.0"
			description: "Reusable ResolvedPlan backup-source authority derived from workload storage allocations."
		}
		role:        "foundation"
		providerRef: "stackkits-backup-core-contract"
		planOnly:    true
		provides:    _architectureV2BackupCoreCapabilities
		requires: ["stackkits-storage-allocation", "stackkits-workload-data-binding"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
			requiredRoles: ["worker"]
		}
		backupSourceContract: {
			apiVersion:                    "stackkit.backup-source/v1"
			kind:                          "BackupSourceContract"
			authority:                     "resolved-plan"
			sourceRef:                     "workloads[].alternative.infrastructure.backupSource"
			storageAllocationModuleRef:    "stackkits-storage-allocation"
			workloadDataBindingModuleRef:  "stackkits-workload-data-binding"
			eligibleStorageClass:          "persistent"
			backupIntentRequired:          true
			providerLifecycleOwned:        false
			credentialCustodyOwned:        false
			targetLifecycleOwned:          false
			multiServerOrchestrationOwned: false
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		realizationSupport: _architectureV2SharedInfrastructureContractSupport
		health: [{id: "backup-source-contract", kind: "contract"}]
		evidence: ["backup-source-contract"]
	},
	{
		metadata: {
			id:          "stackkits-snapshot"
			version:     "1.0.0"
			description: "Reusable snapshot stage contract bound to one Application Kit backup source and lifecycle evidence."
		}
		role:        "operations"
		providerRef: "stackkits-backup-core-contract"
		planOnly:    true
		provides:    _architectureV2BackupCoreCapabilities
		requires: ["stackkits-backup-source"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
			requiredRoles: ["worker"]
		}
		snapshotContract: {
			apiVersion: "stackkit.snapshot/v1"
			kind:       "SnapshotContract"
			authority:  "resolved-plan"
			stageRef:   "applicationLifecycles[].lifecycle.stages.backup"
			operation:  "stackkit.backup"
			phases: ["snapshot", "verify"]
			evidence: ["snapshot-anchor"]
			backupSourceModuleRef:         "stackkits-backup-source"
			ownerApprovalRequired:         true
			providerLifecycleOwned:        false
			credentialCustodyOwned:        false
			targetLifecycleOwned:          false
			multiServerOrchestrationOwned: false
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		realizationSupport: _architectureV2SharedInfrastructureContractSupport
		health: [{id: "snapshot-contract", kind: "contract"}]
		evidence: ["snapshot-anchor"]
	},
	{
		metadata: {
			id:          "stackkits-restore"
			version:     "1.0.0"
			description: "Reusable staged restore contract with a mandatory safety snapshot and verification."
		}
		role:        "operations"
		providerRef: "stackkits-backup-core-contract"
		planOnly:    true
		provides:    _architectureV2BackupCoreCapabilities
		requires: ["stackkits-snapshot"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
			requiredRoles: ["worker"]
		}
		restoreContract: {
			apiVersion: "stackkit.restore/v1"
			kind:       "RestoreContract"
			authority:  "resolved-plan"
			stageRef:   "applicationLifecycles[].lifecycle.stages.restore"
			operations: ["stackkit.restore", "stackkit.verify"]
			phases: ["stage", "safety-snapshot", "activate", "verify", "recover"]
			evidence: ["owner-observation", "restore-result", "snapshot-anchor"]
			snapshotModuleRef:             "stackkits-snapshot"
			stagedActivation:              true
			safetySnapshotRequired:        true
			ownerApprovalRequired:         true
			providerLifecycleOwned:        false
			credentialCustodyOwned:        false
			targetLifecycleOwned:          false
			multiServerOrchestrationOwned: false
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		realizationSupport: _architectureV2SharedInfrastructureContractSupport
		health: [{id: "restore-contract", kind: "contract"}]
		evidence: ["restore-result"]
	},
	{
		metadata: {
			id:          "stackkits-recovery"
			version:     "1.0.0"
			description: "Reusable durable recovery contract over the shared Application Lifecycle state and evidence chain."
		}
		role:        "operations"
		providerRef: "stackkits-backup-core-contract"
		planOnly:    true
		provides:    _architectureV2BackupCoreCapabilities
		requires: ["stackkits-restore"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
			requiredRoles: ["worker"]
		}
		recoveryContract: {
			apiVersion:                    "stackkit.recovery/v1"
			kind:                          "RecoveryContract"
			authority:                     "resolved-plan"
			stageRef:                      "applicationLifecycles[].lifecycle.stages.restore"
			phase:                         "recover"
			restoreModuleRef:              "stackkits-restore"
			stateSourceRef:                "application-lifecycle"
			evidenceSourceRef:             "application-lifecycle"
			resumeRequiresOwnerApproval:   true
			recoveryEvidenceRequired:      true
			providerLifecycleOwned:        false
			credentialCustodyOwned:        false
			targetLifecycleOwned:          false
			multiServerOrchestrationOwned: false
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		realizationSupport: _architectureV2SharedInfrastructureContractSupport
		health: [{id: "recovery-contract", kind: "contract"}]
		evidence: ["recovery-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-monitoring-agent-runtime"
			version:     "1.1.0"
			description: "Node-local OTLP collector intent consumed only by the separately authenticated monitoring-agent runtime owner."
		}
		role:        "platform"
		providerRef: "stackkits-monitoring-agent"
		provides:    _architectureV2TelemetryCollectionCapabilities
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
		}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status:   "bound"
			ownerRef: "stackkits-monitoring-agent-executor"
			policyArtifactRefs: ["monitoring-agent-collector-intent"]
			targetScope: "selected-nodes"
			operations: [
				"apply-monitoring-agent-intent",
				"reconcile-monitoring-agent-intent",
				"verify-monitoring-agent-runtime",
				"commit-monitoring-agent-evidence",
			]
			requiredHealthRef:   "monitoring-agent-runtime-health"
			requiredEvidenceRef: "monitoring-agent-runtime-evidence"
		}
		renderUnits: [{
			id:           "collector-intent"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://observability/monitoring-agent/collector-intent/v1.json"
			version:      "1.1.0"
			contractHash: "sha256:c5f0045e9115ca6e39475c6ee001ce6e498cf4a8b375d72b6a90ddc2056d998b"
			publicInputRefs: []
			secretInputRefs: []
			planInputRefs: ["observability", "sites"]
			outputs: ["observability/monitoring-agent/collector-intent.json"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
		}]
		realizationSupport: _architectureV2MonitoringAgentSupport
		health: [{id: "monitoring-agent-runtime-health", kind: "contract", scope: "each-node"}]
		evidence: ["monitoring-agent-runtime-evidence"]
	},
	{
		metadata: {
			id:          "security-baseline"
			version:     "1.0.0"
			description: "Deterministic target-neutral OS-hardening policy generated once for every managed StackKit node; access and network controls remain delegated."
		}
		role:        "platform"
		providerRef: "stackkits-security-baseline"
		provides: ["security-baseline"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
		}
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:           "host-policy"
			kind:         "host"
			rendererRef:  "stackkit"
			templateRef:  "builtin://foundation/security-baseline/apply.sh"
			version:      "1.0.0"
			contractHash: "sha256:a8f629c2e7df8fb9f38b2f57c1b10d6bc76c67d8be615c116310c68e48a6d6e4"
			publicInputRefs: []
			secretInputRefs: []
			outputs: ["foundation/security-baseline/apply.sh"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
		}]
		realizationSupport: _architectureV2SecurityBaselineSupport
		health: [{id: "security-baseline-contract", kind: "contract", scope: "each-node"}]
		evidence: ["resolved-plan-contract", "security-baseline-executor-contract"]
	},
	{
		metadata: {
			id:          "stackkits-core-host-bootstrap"
			version:     "1.0.0"
			description: "Node-local, provider-free Core host preparation owner; it creates only declared StackKit storage roots and observes the required pre-existing runtime."
		}
		role:        "foundation"
		providerRef: "stackkits-core-host-bootstrap"
		provides: ["host-bootstrap"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
		}
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:           "host-policy"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://foundation/host-bootstrap/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:871d10265613851dc4ad928b4b8e280a874eb10d99b10dfa289bac1f56cc0e35"
			publicInputRefs: ["host-runtime", "storage-roots"], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "moduleTargets", "moduleCapabilities"]
			inputBindings: [
				{
					targetRef:   "host-runtime"
					sourceRef:   "host.bootstrapRuntime"
					valueType:   "host-bootstrap-runtime-v1"
					cardinality: "single"
					required:    true
				},
				{
					targetRef:   "storage-roots"
					sourceRef:   "storage.hostRoots"
					valueType:   "host-storage-roots-v1"
					cardinality: "single"
					required:    true
				},
			]
			outputs: ["foundation/host-bootstrap/policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2CoreHostBootstrapSupport
		health: [{id: "core-host-bootstrap-contract", kind: "contract", scope: "each-node"}]
		evidence: ["core-host-bootstrap-executor-contract"]
	},
	{
		metadata: {
			id:          "stackkits-home-backup-target"
			version:     "1.0.0"
			description: "Node-local Home backup-target verifier; it observes the CUE-declared backup root on Home control-plane nodes after Core host bootstrap and owns no provider, network, discovery, or backup-job lifecycle."
		}
		role:        "foundation"
		providerRef: "stackkits-home-backup-target"
		provides:    _architectureV2HomeBackupTargetCapabilities
		requires: ["stackkits-core-host-bootstrap"]
		supportedSiteKinds: ["home"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "only"
		}
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:           "backup-policy"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://home/backup-target/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:7add0b8b8e643141ca2adb61b03a3aa229daf2c6a08b65ba169870aceb2abe83"
			publicInputRefs: ["backup-root"], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "moduleTargets", "moduleCapabilities"]
			inputBindings: [{
				targetRef:   "backup-root"
				sourceRef:   "storage.backupRoot"
				valueType:   "local-backup-root-v1"
				cardinality: "single"
				required:    true
			}]
			outputs: ["home/backup/target-policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2HomeBackupTargetSupport
		health: [{id: "home-backup-target-contract", kind: "contract", scope: "each-node"}]
		evidence: ["home-backup-target-executor-contract"]
	},
	{
		metadata: {
			id:          "stackkits-home-private-remote-access-runtime"
			version:     "1.0.0"
			description: "Provider-neutral Home private-access binding; transport, endpoints, credentials, provider lifecycle, discovery, and general LAN reachability remain external."
		}
		role: "platform", providerRef: "stackkits-home-private-remote-access", provides: _architectureV2HomePrivateRemoteAccessCapabilities
		requires: ["stackkits-core-host-bootstrap", "stackkits-home-access-policy-manifest"]
		supportedSiteKinds: ["home"]
		runtime: {execution: "executable", kind: "host", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-home-private-remote-access-executor"
			policyArtifactRefs: ["home-private-remote-access-executor-contract"]
			targetScope: "home-sites", operations: ["bind-private-remote-access", "remove-private-remote-access", "verify-private-remote-access"]
			requiredHealthRef: "home-private-remote-access-health", requiredEvidenceRef: "home-private-remote-access-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                         "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://home/remote-access/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:d534d74860c5453ec0fb4f377306371e9a388b9ecf7725c495bff4b092219978"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "homeAccessHandoff"]
			inputBindings: [], outputs: ["home/remote-access/executor-contract.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2HomeExtensionRuntimeSupports.privateRemoteAccess
		health: [{id: "home-private-remote-access-health", kind: "contract", scope: "each-node"}]
		evidence: ["home-private-remote-access-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-home-public-publish-egress-runtime"
			version:     "1.0.0"
			description: "Outbound-only Home publication boundary; public DNS, TLS issuance, credentials, inbound tunnels, and provider lifecycle remain external."
		}
		role: "platform", providerRef: "stackkits-home-public-publish-egress", provides: _architectureV2HomePublicPublishEgressCapabilities
		requires: ["stackkits-core-host-bootstrap", "stackkits-home-access-policy-manifest"]
		supportedSiteKinds: ["home"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status:         "unbound", ownerRef: "stackkits-home-public-publish-egress-executor"
			capabilityRefs: _architectureV2HomePublicPublishEgressCapabilities
			targetScope:    "home-sites", operations: ["bind-public-publish-egress", "remove-public-publish-egress", "verify-public-publish-egress"]
			requiredHealthRef: "home-public-publish-egress-health", requiredEvidenceRef: "home-public-publish-egress-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                       "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://home/publication/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:16f928871e35f06c1f5960f90acfa8d88cebd23fb9aefe8630c0f0017d65a387"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "homeAccessHandoff"]
			inputBindings: [], outputs: ["home/publication/executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}]
		realizationSupport: _architectureV2HomeExtensionRuntimeSupports.publicPublishEgress
		health: [{id: "home-public-publish-egress-contract", kind: "contract"}]
		evidence: ["home-public-publish-egress-contract"]
	},
	{
		metadata: {
			id:          "stackkits-home-encrypted-offsite-backup-runtime"
			version:     "1.0.0"
			description: "Provider-neutral encrypted Home offsite-backup binding; repositories, endpoints, credentials, retention execution, restore execution, and provider lifecycle remain external."
		}
		role: "foundation", providerRef: "stackkits-home-encrypted-offsite-backup", provides: _architectureV2HomeEncryptedOffsiteBackupCapabilities
		requires: ["stackkits-core-host-bootstrap"]
		supportedSiteKinds: ["home"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status:         "unbound", ownerRef: "stackkits-home-encrypted-offsite-backup-executor"
			capabilityRefs: _architectureV2HomeEncryptedOffsiteBackupCapabilities
			targetScope:    "home-sites", operations: ["bind-encrypted-offsite-backup", "remove-encrypted-offsite-backup", "verify-encrypted-offsite-backup"]
			requiredHealthRef: "home-encrypted-offsite-backup-health", requiredEvidenceRef: "home-encrypted-offsite-backup-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                          "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://home/backup/offsite-executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:c6ba1e9050b63a30fc9436a5325f86801b2adf08e3857708aac91c6a93cab05b"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "homeOffsiteBackup"]
			inputBindings: [], outputs: ["home/backup/offsite-executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}]
		realizationSupport: _architectureV2HomeExtensionRuntimeSupports.encryptedOffsiteBackup
		health: [{id: "home-encrypted-offsite-backup-contract", kind: "contract"}]
		evidence: ["home-encrypted-offsite-backup-contract"]
	},
	{
		metadata: {
			id:          "stackkits-internal-pki-contract"
			version:     "1.0.0"
			description: "Provider-free authenticated Home PKI owner on one explicit authority node. Root/leaf custody remains owner-held while exact public trust targets and compiler-derived leaf identities are closed in the generated policy."
		}
		role:        "platform"
		providerRef: "stackkits-internal-pki"
		provides:    _architectureV2InternalPKICapabilities
		requires: ["stackkits-core-host-bootstrap"]
		supportedSiteKinds: ["home"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "only"
			requiredRoles: ["controller"]
		}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "executor-contract", kind:                                            "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://home/tls/internal-pki-executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:af28e0a1d23129fcfa1a2e91e8510c0ce5e57ff3d032a29dd03a3a345531a056"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "internalPKI"]
			outputs: ["home/tls/internal-pki-executor-contract.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2InternalPKIGenerationSupport
		health: [{id: "internal-pki-renewal-contract", kind: "contract"}]
		evidence: ["internal-pki-contract"]
	},
	{
		metadata: {
			id:          "stackkits-home-device-authority-policy-manifest"
			version:     "1.0.0"
			description: "Node-local Home enrollment and credential-authority policy enforced by an explicit runtime owner; credential material, endpoints, and provider lifecycle are excluded."
		}
		role:        "platform"
		providerRef: "stackkits-home-device-authority"
		provides:    _architectureV2HomeIdentityAuthorityCapabilities
		supportedSiteKinds: ["home"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "only"
			requiredRoles: ["controller"]
		}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-home-device-authority-enforcer"
			policyArtifactRefs: ["home-device-authority-policy"]
			targetScope: "home-control-authority"
			operations: ["configure-device-enrollment", "configure-device-credential-issuer", "configure-device-credential-revocation"]
			requiredHealthRef:   "home-device-authority-enforcement"
			requiredEvidenceRef: "home-device-authority-enforcement"
		}
		renderUnits: [{
			id:           "policy-bundle", kind:                                     "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://home/device-authority-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:6645794f097ca1b778fa3833075c1ffdc1dea7aff3cfbbc5e3ac7f0801466ffa"
			publicInputRefs: ["home-device-authority"], secretInputRefs: []
			inputBindings: [{
				targetRef: "home-device-authority", sourceRef:      "identityTrust.homeDeviceAuthority"
				valueType: "home-device-authority-v1", cardinality: "single", required: true
			}]
			planInputRefs: ["stackId", "kit"]
			outputs: ["local/identity/device-authority-policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2HomeDeviceAuthoritySupport
		health: [{id: "home-device-authority-enforcement", kind: "contract", scope: "each-node"}]
		evidence: ["home-device-authority-enforcement"]
	},
	{
		metadata: {
			id:          "stackkits-basement-identity-trust-policy-manifest"
			version:     "1.0.0"
			description: "Node-local Basement identity verifier and trust policy enforced on the Home control authority; credential material and provider lifecycle are excluded."
		}
		role:        "platform"
		providerRef: "stackkits-basement-identity-trust-policy"
		provides:    _architectureV2IdentityCapabilities
		requires: ["stackkits-home-device-authority-policy-manifest"]
		supportedSiteKinds: ["home"]
		nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["controller"]}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-basement-identity-trust-enforcer"
			policyArtifactRefs: ["basement-identity-trust-policy"]
			targetScope: "home-control-authority"
			operations: ["verify-device-session", "verify-human-session", "verify-workload-identity"]
			requiredHealthRef:   "basement-identity-trust-enforcement"
			requiredEvidenceRef: "basement-identity-trust-enforcement"
		}
		renderUnits: [{
			id:           "policy-bundle", kind:                                       "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://basement/identity-trust-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:57e0d7eaba4751adc07c689bb78ae99df3e331def33694af68fad6bc770f6c7f"
			publicInputRefs: ["basement-verification-policy"], secretInputRefs: []
			inputBindings: [{
				targetRef: "basement-verification-policy", sourceRef:        "identityTrust.basementVerification"
				valueType: "basement-identity-verification-v1", cardinality: "single", required: true
			}]
			planInputRefs: ["stackId", "kit"]
			outputs: ["local/identity/trust-policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2BasementIdentityTrustSupport
		health: [{id: "basement-identity-trust-enforcement", kind: "contract", scope: "each-node"}]
		evidence: ["basement-identity-trust-enforcement"]
	},
	{
		metadata: {
			id:          "stackkits-local-autonomy-policy-manifest"
			version:     "1.0.0"
			description: "Node-local Home control-authority offline-autonomy policy; air-gapped installation remains a separate claim."
		}
		role:        "platform"
		providerRef: "stackkits-local-autonomy-policy"
		provides:    _architectureV2LocalAutonomyCapabilities
		supportedSiteKinds: ["home"]
		nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["controller"]}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-local-autonomy-enforcer"
			policyArtifactRefs: ["local-autonomy-policy"]
			targetScope: "home-control-authority"
			operations: ["deny-forbidden-cross-site-session", "enforce-link-loss-policy", "preserve-local-control"]
			requiredHealthRef:   "local-autonomy-enforcement"
			requiredEvidenceRef: "local-autonomy-enforcement"
		}
		renderUnits: [{
			id:           "policy-bundle"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://home/local-autonomy/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:423ed27c579f6e7232c069535203c07dd2993183758525fcde6303795f20094c"
			publicInputRefs: ["local-autonomy-policy"]
			secretInputRefs: []
			planInputRefs: []
			inputBindings: [{
				targetRef: "local-autonomy-policy", sourceRef:      "localAutonomy.policy"
				valueType: "local-autonomy-policy-v1", cardinality: "single", required: true
			}]
			outputs: ["local/autonomy/policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2LocalAutonomySupport
		health: [{id: "local-autonomy-enforcement", kind: "contract", scope: "each-node"}]
		evidence: ["local-autonomy-enforcement"]
	},
	{
		metadata: {
			id:          "stackkits-home-access-policy-manifest"
			version:     "1.0.0"
			description: "Node-local Home local-ingress and LAN access enforcement policy; discovery remains a separate optional claim."
		}
		role:        "platform"
		providerRef: "stackkits-home-access-policy"
		provides:    _architectureV2HomeAccessCapabilities
		supportedSiteKinds: ["home"]
		nodeSelection: {authority: "any", controlPlaneMembers: "any"}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-home-access-enforcer"
			policyArtifactRefs: ["home-access-policy"]
			targetScope: "home-sites"
			operations: ["enforce-lan-access", "enforce-local-ingress", "enforce-privileged-step-up"]
			requiredHealthRef:   "home-access-enforcement"
			requiredEvidenceRef: "home-access-enforcement"
		}
		renderUnits: [{
			id:           "policy-bundle"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://home/access/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:22afca6d222abf93e1454ce97ce4b1dc74326a1b6f120796da5b8ea2fbd80f76"
			publicInputRefs: ["home-access-policy"]
			secretInputRefs: []
			planInputRefs: []
			inputBindings: [{
				targetRef: "home-access-policy", sourceRef:           "access.homeEnforcement"
				valueType: "home-access-enforcement-v1", cardinality: "single", required: true
			}]
			outputs: ["local/network/access-policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2HomeAccessSupport
		health: [{id: "home-access-enforcement", kind: "contract", scope: "each-node"}]
		evidence: ["home-access-enforcement"]
	},
	{
		metadata: {
			id:          "stackkits-home-lan-discovery-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only explicit Home LAN discovery policy; empty intent emits no advertisements and runtime publication remains separately verified."
		}
		role:        "platform"
		providerRef: "stackkits-home-lan-discovery-policy"
		provides:    _architectureV2HomeLANDiscoveryCapabilities
		requires: ["stackkits-home-access-policy-manifest"]
		supportedSiteKinds: ["home"]
		runtime: {execution: "contract-handoff", kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://home/lan-discovery/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:48eb8152174d0a9de62e57b7eb3d93dad25ee36894611caf5005d05f452ec68d"
			publicInputRefs: []
			secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "homeLANDiscovery"]
			outputs: ["local/network/discovery-policy.json"]
			placement: {
				scope:       "module"
				cardinality: "single"
			}
		}]
		realizationSupport: _architectureV2HomeLANDiscoverySupport
		health: [{id: "home-lan-discovery-policy-contract", kind: "contract"}]
		evidence: ["home-lan-discovery-policy-contract"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-host-security-runtime"
			version:     "1.1.0"
			description: "Cloud-only node-local firewall and Internet-host hardening boundary; it owns no public edge, DNS, backup, mesh, or server-provider lifecycle."
		}
		role:        "platform"
		providerRef: "stackkits-cloud-host-security"
		provides:    _architectureV2CloudHostSecurityCapabilities
		requires: ["stackkits-core-host-bootstrap", "security-baseline"]
		supportedSiteKinds: ["cloud"]
		runtime: {execution: "executable", kind: "host", delivery: "stackkit"}
		enforcementRequirement: {
			status:      "bound", ownerRef: "stackkits-cloud-host-security-executor"
			targetScope: "cloud-sites"
			operations: ["apply-cloud-host-firewall", "reconcile-cloud-host-firewall", "apply-cloud-host-hardening", "verify-cloud-host-security", "commit-cloud-host-security-evidence"]
			policyArtifactRefs: ["cloud-host-security-executor-contract"]
			requiredHealthRef:   "cloud-host-security-health"
			requiredEvidenceRef: "cloud-host-security-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                          "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/host-security/executor-contract/v2.json", version: "1.1.0"
			contractHash: "sha256:577d15449a15753e08b2c39af519ebba03e4159b7f772a19ae47b9974195f2a8"
			publicInputRefs: ["host-security-network"], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
			inputBindings: [{
				targetRef: "host-security-network", sourceRef:           "network.cloudHostSecurity"
				valueType: "cloud-host-security-policy-v2", cardinality: "single", required: true
			}]
			outputs: ["cloud/host-security/executor-contract.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:4d6339d1b9ae81c6b62edd28e3738f100f4d12959b54867a611497cca4e5056c"
				unitRefs: ["executor-contract"]
				artifactRefs: ["cloud-host-security-executor-contract"]
				publicInputRefs: ["host-security-network"], secretInputRefs: []
				planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:4764ee33223ee9b43f03c4880b23168f807503f3ed120016c7a5c4d7f9873ace"
				unitRefs: ["executor-contract"]
				artifactRefs: ["cloud-host-security-executor-contract"]
				publicInputRefs: ["host-security-network"], secretInputRefs: []
				planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
			},
		]
		realizationSupport: _architectureV2CloudHostSecuritySupport
		health: [{id: "cloud-host-security-health", kind: "contract", scope: "each-node"}]
		evidence: ["cloud-host-security-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-public-edge-runtime"
			version:     "1.1.0"
			description: "Cloud-only public edge boundary; DNS provider mutation, TLS issuance, host hardening, backup, mesh, and server lifecycle remain separate."
		}
		role:        "platform"
		providerRef: "stackkits-cloud-public-edge"
		provides:    _architectureV2CloudPublicEdgeCapabilities
		requires: ["stackkits-cloud-host-security-runtime"]
		supportedSiteKinds: ["cloud"]
		runtime: {execution: "executable", kind: "host", delivery: "stackkit"}
		enforcementRequirement: {
			status:      "bound", ownerRef: "stackkits-cloud-public-edge-executor"
			targetScope: "cloud-sites"
			operations: ["apply-public-edge", "remove-obsolete-public-edge", "verify-public-edge", "commit-cloud-public-edge-evidence"]
			policyArtifactRefs: ["cloud-public-edge-executor-contract"]
			requiredHealthRef:   "cloud-public-edge-health"
			requiredEvidenceRef: "cloud-public-edge-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                        "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/public-edge/executor-contract/v2.json", version: "1.1.0"
			contractHash: "sha256:a452f3a9f9651f4bb96ff5abdb32328eab08be7e896397e2de72fae5029a8b0d"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "publicEdge"]
			outputs: ["cloud/public-edge/executor-contract.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2CloudPublicEdgeSupport
		health: [{id: "cloud-public-edge-health", kind: "contract", scope: "each-node"}]
		evidence: ["cloud-public-edge-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-offsite-backup-runtime"
			version:     "1.1.0"
			description: "Provider-neutral node-local Cloud offsite-backup binding and verification; object-storage provider lifecycle, buckets, endpoints, credentials, and target custody remain external."
		}
		role:        "foundation"
		providerRef: "stackkits-cloud-offsite-backup"
		provides:    _architectureV2CloudOffsiteBackupCapabilities
		supportedSiteKinds: ["cloud"]
		runtime: {execution: "executable", kind: "host", delivery: "stackkit"}
		enforcementRequirement: {
			status:      "bound", ownerRef: "stackkits-cloud-offsite-backup-executor"
			targetScope: "cloud-sites"
			operations: ["bind-offsite-backup-target", "remove-obsolete-offsite-backup-binding", "verify-offsite-backup-target", "commit-cloud-offsite-backup-evidence"]
			policyArtifactRefs: ["cloud-offsite-backup-executor-contract"]
			requiredHealthRef:   "cloud-offsite-backup-health"
			requiredEvidenceRef: "cloud-offsite-backup-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                   "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/backup/executor-contract/v2.json", version: "1.1.0"
			contractHash: "sha256:f6a804316a73cbb9f424f1fdd68de1e8555bde39298a46a6c65de2eccf9b17b9"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "cloudOffsiteBackup"]
			outputs: ["cloud/backup/executor-contract.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2CloudOffsiteBackupSupport
		health: [{id: "cloud-offsite-backup-health", kind: "contract", scope: "each-node"}]
		evidence: ["cloud-offsite-backup-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-private-admin-mesh-runtime"
			version:     "1.0.0"
			description: "Optional provider-neutral private admin-mesh policy handoff; transport, endpoints, credentials, identity issuance, provider lifecycle, federation, and LAN reachability remain separate."
		}
		role:        "platform"
		providerRef: "stackkits-cloud-private-admin-mesh"
		provides:    _architectureV2CloudPrivateAdminMeshCapabilities
		requires: ["stackkits-cloud-host-security-runtime", "stackkits-cloud-identity-trust-policy-manifest"]
		supportedSiteKinds: ["cloud"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status:         "unbound", ownerRef: "stackkits-cloud-private-admin-mesh-executor"
			capabilityRefs: _architectureV2CloudPrivateAdminMeshCapabilities
			targetScope:    "cloud-sites"
			operations: ["bind-private-admin-mesh", "remove-private-admin-mesh-binding", "verify-private-admin-mesh"]
			requiredHealthRef:   "cloud-private-admin-mesh-health"
			requiredEvidenceRef: "cloud-private-admin-mesh-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                       "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/admin-mesh/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:009e99ba8c84136dccaad45e99a1166cbdda3be9bf08b615d769538d5b290f1a"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "cloudAdminMesh"]
			inputBindings: []
			outputs: ["cloud/admin-mesh/executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}]
		realizationSupport: _architectureV2CloudPrivateAdminMeshSupport
		health: [{id: "cloud-private-admin-mesh-contract", kind: "contract"}]
		evidence: ["cloud-private-admin-mesh-contract"]
	},
	{
		metadata: {
			id:          "stackkits-public-tls-contract"
			version:     "1.0.0"
			description: "Exact public TLS termination and renewal handoff; certificate material and ACME credentials remain owned by an authenticated external operations implementation."
		}
		role:        "platform"
		providerRef: "stackkits-public-tls"
		provides:    _architectureV2PublicTLSCapabilities
		requires: ["stackkits-cloud-public-edge-runtime"]
		supportedSiteKinds: ["cloud"]
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-public-tls-enforcer"
			policyArtifactRefs: ["public-tls-executor-contract"]
			targetScope: "cloud-sites"
			operations: ["materialize-public-tls", "renew-public-tls", "verify-public-tls"]
			requiredHealthRef:   "public-tls-renewal-contract"
			requiredEvidenceRef: "public-tls-contract"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/tls/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:7779966dc102170d25a75c0a508427d5cb7462b2b7108b311036b2b3b02f97c8"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["kit", "moduleTargets", "publicTLS", "stackId"]
			outputs: ["cloud/tls/executor-contract.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2PublicTLSGenerationSupport
		health: [{id: "public-tls-renewal-contract", kind: "contract", scope: "each-node"}]
		evidence: ["public-tls-contract"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-identity-trust-policy-manifest"
			version:     "1.0.0"
			description: "Node-local Cloud identity trust policy with external device authority and Cloud-local enforcement; no device issuance or enrollment is owned."
		}
		role:        "platform"
		providerRef: "stackkits-cloud-identity-trust-policy"
		provides: list.Concat([_architectureV2IdentityCapabilities, _architectureV2CloudIdentityAuthorityCapabilities])
		supportedSiteKinds: ["cloud"]
		nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["controller"]}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-cloud-identity-trust-enforcer"
			policyArtifactRefs: ["cloud-identity-trust-policy"]
			targetScope: "cloud-sites"
			operations: ["configure-human-credential-issuer", "configure-workload-credential-issuer", "verify-device-session", "verify-human-session", "verify-workload-identity"]
			requiredHealthRef:   "cloud-identity-trust-enforcement"
			requiredEvidenceRef: "cloud-identity-trust-enforcement"
		}
		renderUnits: [{
			id:           "policy-bundle", kind:                                    "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/identity-trust-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:0522a70d6833d2c6d9b1a85971fa90e549b7a105a3042843929b9ab14c442362"
			publicInputRefs: ["cloud-identity-authority"], secretInputRefs: []
			inputBindings: [{
				targetRef: "cloud-identity-authority", sourceRef:      "identityTrust.cloudAuthority"
				valueType: "cloud-identity-authority-v1", cardinality: "single", required: true
			}]
			planInputRefs: ["stackId", "kit"]
			outputs: ["cloud/identity/trust-policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2CloudIdentityTrustSupport
		health: [{id: "cloud-identity-trust-enforcement", kind: "contract", scope: "each-node"}]
		evidence: ["cloud-identity-trust-enforcement"]
	},
	{
		metadata: {
			id:          "stackkits-basement-compose-runtime"
			version:     "1.0.0"
			description: "Residual Basement-only Compose rollout owner; concrete foundation helpers lower independently beneath it."
		}
		role:        "platform"
		providerRef: "stackkits-basement-compose"
		provides:    _architectureV2BasementComposeCapabilities
		requires: ["stackkits-core-host-bootstrap"]
		supportedSiteKinds: ["home"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status:         "unbound", ownerRef: "stackkits-basement-compose-executor"
			capabilityRefs: _architectureV2BasementComposeCapabilities
			targetScope:    "home-sites"
			operations: ["apply-compose-project", "remove-compose-project", "verify-compose-project"]
			requiredHealthRef:   "basement-compose-runtime-health"
			requiredEvidenceRef: "basement-compose-runtime-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                       "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://basement/runtime/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:3e9795cc29f1d063184a5be004b796341f1c408d40bd7c4e41b814f979c795ad"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
			outputs: ["basement/runtime/executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:caa13cedcc306755ce50179a0e6ba5f6eecefa8c58f8e472770c04c8cde92212"
				unitRefs: ["executor-contract"]
				artifactRefs: ["basement-compose-runtime-executor-contract"]
				publicInputRefs: [], secretInputRefs: []
				planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:d4b39bdc9ca285e10c899bbf0619035cbe66c596365bace374d69def776d69d4"
				unitRefs: ["executor-contract"]
				artifactRefs: ["basement-compose-runtime-executor-contract"]
				publicInputRefs: [], secretInputRefs: []
				planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane"]
			},
		]
		realizationSupport: _architectureV2BasementComposeExecutorContractSupport
		health: [{id: "stackkits-basement-compose-contract", kind: "contract"}]
		evidence: ["basement-compose-contract-governance"]
	},
	{
		metadata: {
			id:          "socket-proxy"
			version:     "1.0.0"
			description: "Basement node-local Docker API isolation proxy backed by one explicitly approved daemon socket."
		}
		role:        "platform"
		providerRef: "stackkits-basement-compose"
		// This helper provides an implementation interface, not a product
		// capability. The Basement Compose runtime owner above carries the
		// capability selection and readiness responsibility.
		provides: []
		requires: ["stackkits-basement-compose-runtime"]
		supportedSiteKinds: ["home"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
		}
		runtime: {
			kind:     "container"
			delivery: "stackkit"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2"
				digest: "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
			}
		}
		renderUnits: [{
			id:           "compose"
			kind:         "compose"
			rendererRef:  "stackkit"
			templateRef:  "builtin://foundation/socket-proxy/compose.yaml"
			version:      "1.0.0"
			contractHash: "sha256:7f7beb9fdefe9f6c4f4acfdae8d0f71a754356f9dd29112992927f884438a47c"
			publicInputRefs: []
			secretInputRefs: []
			outputs: ["foundation/socket-proxy/compose.yaml"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-daemon"
				daemonRef:   "docker-default"
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
					networkRef: "docker-api-readonly"
					address:    "socket-proxy"
					port:       2375
				}
				scopes: ["CONTAINERS", "EVENTS", "NETWORKS", "PING", "VERSION"]
				coLocation:    "same-node-and-network"
				daemonRef:     "docker-default"
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
				daemonRef:     "docker-default"
				policyProfile: "docker-provider-backing"
			}]
		}]
		renderVariants: [{
			id:           "compose", target: "compose", rendererRef: "stackkit"
			contractHash: "sha256:bf2cf9138330226055c10bf0de998ff20b4c96d61de2a183aca3b4a1997c3cdd"
			unitRefs: ["compose"]
			artifactRefs: ["socket-proxy-compose"]
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
		}]
		realizationSupport: _architectureV2SocketProxySupport
		health: [{id: "socket-proxy-contract", kind: "contract"}]
		evidence: ["socket-proxy-provider-backing-governance"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-core-runtime"
			version:     "1.0.0"
			description: "Provider-neutral Cloud core runtime for routing, local identity, access control, Coolify, and the operator hub on an externally supplied host."
		}
		role:        "workload"
		providerRef: "stackkits-cloud-core"
		provides:    _architectureV2CloudCoreCapabilities
		requires: ["stackkits-public-tls-contract"]
		supportedSiteKinds: ["cloud"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "only"
			requiredRoles: ["controller", "worker"]
		}
		runtimeRequirements: {
			minCpuCores:  2
			minRamGB:     4
			minStorageGB: 20
		}
		computeProfiles: _architectureV2CloudCoreComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "stackkit"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/coollabsio/coolify:4.1.2"
				digest: "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"
			}
			entryComponentRef: "coolify"
			components: [
				{
					id: "router", role: "application", lifecycle: "daemon"
					image: {ref: "ghcr.io/traefik/traefik:v3", digest: "sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8"}
					dependsOn: ["socket-proxy"], networkRefs: ["cloud-core", "cloud-control"]
					health: {kind: "http", path: "/ping", port: 8080}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "socket-proxy", role: "application", lifecycle: "daemon"
					image: {ref: "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2", digest: "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"}
					dependsOn: [], networkRefs: ["cloud-control"]
					health: {kind: "image"}
					resources: {memoryLimit: "128m"}
				},
				{
					id: "pocketid", role: "application", lifecycle: "daemon"
					image: {ref: "ghcr.io/pocket-id/pocket-id:v2.7.0", digest: "sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e"}
					dependsOn: [], networkRefs: ["cloud-core"]
					volumes: [{id: "pocketid-data", target: "/app/data", class: "persistent", backup: true}]
					health: {kind: "http", path: "/health", port: 1411}
					resources: {memoryLimit: "512m"}
				},
				{
					id: "tinyauth", role: "application", lifecycle: "daemon"
					image: {ref: "ghcr.io/steveiliop56/tinyauth:v5.0.7", digest: "sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"}
					dependsOn: ["pocketid"], networkRefs: ["cloud-core"]
					volumes: [{id: "tinyauth-data", target: "/data", class: "persistent", backup: true}]
					health: {kind: "command", command: ["tinyauth", "healthcheck"]}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "coolify", role: "application", lifecycle: "daemon"
					image: {ref: "ghcr.io/coollabsio/coolify:4.1.2", digest: "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"}
					dependsOn: ["coolify-postgres", "coolify-redis", "coolify-realtime"], networkRefs: ["cloud-core", "cloud-control"]
					volumes: [
						{id: "coolify-data", target: "/var/www/html/storage", class: "persistent", backup: true},
						{id: "coolify-ssh", target: "/var/www/html/storage/app/ssh", class: "persistent", backup: true},
						{id: "coolify-applications", target: "/var/www/html/storage/app/applications", class: "persistent", backup: true},
						{id: "coolify-databases", target: "/var/www/html/storage/app/databases", class: "persistent", backup: true},
						{id: "coolify-services", target: "/var/www/html/storage/app/services", class: "persistent", backup: true},
						{id: "coolify-backups", target: "/var/www/html/storage/app/backups", class: "persistent", backup: true},
					]
					health: {kind: "http", path: "/api/health", port: 8080}
					resources: {memoryLimit: "1g"}
				},
				{
					id: "coolify-postgres", role: "database", lifecycle: "daemon"
					image: {ref: "docker.io/library/postgres:15-alpine", digest: "sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f"}
					dependsOn: [], networkRefs: ["cloud-control"]
					volumes: [{id: "coolify-postgres-data", target: "/var/lib/postgresql/data", class: "persistent", backup: true}]
					health: {kind: "command", command: ["pg_isready", "-U", "coolify"]}
					resources: {memoryLimit: "512m"}
				},
				{
					id: "coolify-redis", role: "cache", lifecycle: "daemon"
					image: {ref: "docker.io/library/redis:7-alpine", digest: "sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99"}
					dependsOn: [], networkRefs: ["cloud-control"]
					volumes: [{id: "coolify-redis-data", target: "/data", class: "persistent", backup: true}]
					health: {kind: "command", command: ["redis-cli", "ping"]}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "coolify-realtime", role: "application", lifecycle: "daemon"
					image: {ref: "ghcr.io/coollabsio/coolify-realtime:1.0.16", digest: "sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629"}
					dependsOn: ["coolify-redis"], networkRefs: ["cloud-control"]
					health: {kind: "http", path: "/ready", port: 6001}
				},
				{
					id: "hub", role: "application", lifecycle: "daemon"
					image: {ref: "docker.io/library/nginx:alpine", digest: "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"}
					dependsOn: ["tinyauth"], networkRefs: ["cloud-core"]
					health: {kind: "http", path: "/healthz", port: 80}
					resources: {memoryLimit: "256m"}
				},
			]
		}
		serviceControls: _cloudCoreServiceControls
		renderUnits: [{
			id:           "compose", kind:                                 "compose", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/core/compose/v1.yaml", version: "1.0.0"
			contractHash: "sha256:2362c3e483760093eeebc6f1f75162cef5cd4e4292e257536651560a7c028609"
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			outputs: ["platform/cloud-core/compose.yaml"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
			serviceEndpoints: _cloudCoreServiceEndpoints
			runtimeListeners: _cloudCoreVerificationRuntimeListeners
		}]
		renderVariants: [{
			id:           "compose", target: "compose", rendererRef: "stackkit"
			contractHash: "sha256:2362c3e483760093eeebc6f1f75162cef5cd4e4292e257536651560a7c028609"
			unitRefs: ["compose"], artifactRefs: ["cloud-core-compose"]
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
		}]
		realizationSupport: {
			contractVersion: "1.0.0", scope: "concrete", level: "apply-ready"
			compatibleRendererRefs: ["stackkit"]
			inputs: {contractComplete: true, requiredRefs: []}
			planInputs: {contractComplete: true, requiredRefs: []}
			artifacts: {
				requiredRefs: ["cloud-core-compose"]
				outputBindings: [{artifactRef: "cloud-core-compose", unitRef: "compose", outputRef: "platform/cloud-core/compose.yaml"}]
				contracts: [{
					id: "cloud-core-compose", kind: "compose", format: "yaml", mode: "0640", required: true
					compatibleTargets: ["compose"], unitRef: "compose", outputRef: "platform/cloud-core/compose.yaml"
				}]
			}
			evidence: requiredRefs: ["cloud-core-runtime-evidence"]
		}
		health: [
			{id: "cloud-router-http", kind: "http", path: "/ping", port: 8080, expectedStatuses: [200]},
			{id: "cloud-pocketid-http", kind: "http", path: "/", port: 1411, expectedStatuses: [200, 302]},
			{id: "cloud-tinyauth-http", kind: "http", path: "/", port: 3000, expectedStatuses: [200, 302]},
			{id: "cloud-coolify-http", kind: "http", path: "/", port: 8080, expectedStatuses: [200, 302]},
			{id: "cloud-hub-http", kind: "http", path: "/healthz", port: 80, expectedStatuses: [200]},
		]
		evidence: ["cloud-core-runtime-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-basement-core-runtime"
			version:     "1.0.0"
			description: "Required standalone Basement core runtime with exact Compose and OpenTofu realizations for routing, identity, internal PKI, Coolify, and the operator hub."
		}
		role:        "workload"
		providerRef: "stackkits-basement-core"
		provides:    _architectureV2BasementCoreCapabilities
		requires: ["stackkits-home-backup-target"]
		supportedSiteKinds: ["home"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "only"
			requiredRoles: ["controller", "worker"]
		}
		runtimeRequirements: {
			minCpuCores:  2
			minRamGB:     4
			minStorageGB: 20
		}
		computeProfiles: _architectureV2BasementCoreComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "stackkit"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/coollabsio/coolify:4.1.2"
				digest: "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"
			}
			entryComponentRef: "coolify"
			components: [
				{
					id: "router", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/traefik/traefik:v3"
						digest: "sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8"
					}
					dependsOn: ["socket-proxy"], networkRefs: ["basement-core", "basement-control"]
					health: {kind: "http", path: "/ping", port: 8080}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "socket-proxy", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2"
						digest: "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
					}
					dependsOn: [], networkRefs: ["basement-control"]
					health: {kind: "image"}
					resources: {memoryLimit: "128m"}
				},
				{
					id: "pocketid", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/pocket-id/pocket-id:v2.7.0"
						digest: "sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e"
					}
					dependsOn: [], networkRefs: ["basement-core"]
					volumes: [{id: "pocketid-data", target: "/app/data", class: "persistent", backup: true}]
					health: {kind: "http", path: "/health", port: 1411}
					resources: {memoryLimit: "512m"}
				},
				{
					id: "tinyauth", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/steveiliop56/tinyauth:v5.0.7"
						digest: "sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"
					}
					dependsOn: ["pocketid"], networkRefs: ["basement-core"]
					volumes: [{id: "tinyauth-data", target: "/data", class: "persistent", backup: true}]
					health: {kind: "command", command: ["tinyauth", "healthcheck"]}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "step-ca", role: "application", lifecycle: "daemon"
					image: {
						ref:    "smallstep/step-ca:0.30.2"
						digest: "sha256:a2b17872915c193259b75a5474c398326f41bd199f0842093e52cf4182bc8270"
					}
					dependsOn: [], networkRefs: ["basement-core"]
					volumes: [{id: "step-ca-db", target: "/home/step/db", class: "persistent", backup: true}]
					health: {kind: "http", path: "/health", port: 9000}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "coolify", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/coollabsio/coolify:4.1.2"
						digest: "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"
					}
					dependsOn: ["coolify-postgres", "coolify-redis", "coolify-realtime"], networkRefs: ["basement-core", "basement-control"]
					environment: {
						AUTOUPDATE:         "false"
						CDN_URL:            "http://hub/.stackkit/offline/coolify/cdn"
						VERSIONS_URL:       "http://hub/.stackkit/offline/coolify/versions.json"
						UPGRADE_SCRIPT_URL: "http://hub/.stackkit/offline/coolify/upgrade.sh"
						RELEASES_URL:       "http://hub/.stackkit/offline/coolify/releases.json"
					}
					volumes: [
						{id: "coolify-data", target: "/var/www/html/storage", class: "persistent", backup: true},
						{id: "coolify-ssh", target: "/var/www/html/storage/app/ssh", class: "persistent", backup: true},
						{id: "coolify-applications", target: "/var/www/html/storage/app/applications", class: "persistent", backup: true},
						{id: "coolify-databases", target: "/var/www/html/storage/app/databases", class: "persistent", backup: true},
						{id: "coolify-services", target: "/var/www/html/storage/app/services", class: "persistent", backup: true},
						{id: "coolify-backups", target: "/var/www/html/storage/app/backups", class: "persistent", backup: true},
					]
					health: {kind: "http", path: "/api/health", port: 8080}
					resources: {memoryLimit: "1g"}
				},
				{
					id: "coolify-postgres", role: "database", lifecycle: "daemon"
					image: {
						ref:    "docker.io/library/postgres:15-alpine"
						digest: "sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f"
					}
					dependsOn: [], networkRefs: ["basement-control"]
					volumes: [{id: "coolify-postgres-data", target: "/var/lib/postgresql/data", class: "persistent", backup: true}]
					health: {kind: "command", command: ["pg_isready", "-U", "coolify"]}
					resources: {memoryLimit: "512m"}
				},
				{
					id: "coolify-redis", role: "cache", lifecycle: "daemon"
					image: {
						ref:    "docker.io/library/redis:7-alpine"
						digest: "sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99"
					}
					dependsOn: [], networkRefs: ["basement-control"]
					volumes: [{id: "coolify-redis-data", target: "/data", class: "persistent", backup: true}]
					health: {kind: "command", command: ["redis-cli", "ping"]}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "coolify-realtime", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/coollabsio/coolify-realtime:1.0.16"
						digest: "sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629"
					}
					dependsOn: ["coolify-redis"], networkRefs: ["basement-control"]
					health: {kind: "http", path: "/ready", port: 6001}
				},
				{
					id: "kopia-agent", role: "application", lifecycle: "daemon"
					image: {
						ref:    "docker.io/kopia/kopia:0.18.2"
						digest: "sha256:b6cb1f09a5fa832a320ee06d7803e82cdd7f69ac6f61d76a0d55fbbf1495c043"
					}
					dependsOn: [], networkRefs: ["basement-backup"]
					volumes: [
						{id: "kopia-repository", target: "/app/repository", class: "persistent", backup: false},
						{id: "kopia-config", target: "/app/config", class: "persistent", backup: false},
						{id: "kopia-cache", target: "/app/cache", class: "cache", backup: false},
						{id: "kopia-restore-staging", target: "/restore-staging", class: "persistent", backup: false},
					]
					health: {kind: "command", command: ["kopia", "--version"]}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "hub", role: "application", lifecycle: "daemon"
					image: {
						ref:    "docker.io/library/nginx:alpine"
						digest: "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"
					}
					dependsOn: ["tinyauth"], networkRefs: ["basement-core"]
					health: {kind: "http", path: "/healthz", port: 80}
					resources: {memoryLimit: "256m"}
				},
			]
		}
		serviceControls: _basementCoreServiceControls
		renderUnits: [
			{
				id:           "compose", kind:                                    "compose", rendererRef: "stackkit"
				templateRef:  "builtin://basement/core/compose/v1.yaml", version: "1.0.0"
				contractHash: "sha256:519e5585e2d04245781c8967733819428917e11588e2391687fe5b886e71aae4"
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
				outputs: ["platform/basement-core/compose.yaml"]
				placement: {scope: "node-local", cardinality: "one-per-node"}
				serviceEndpoints: _basementCoreServiceEndpoints
				runtimeListeners: _basementCoreRuntimeListeners
			},
			{
				id:           "opentofu", kind:                                  "opentofu", rendererRef: "stackkit"
				templateRef:  "builtin://basement/core/opentofu/v1.tf", version: "1.0.0"
				contractHash: "sha256:60547d2c38a46af404cf1372e28aa8230613f2884b338b6b7793308f0f71d1a3"
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
				outputs: ["platform/basement-core/main.tf"]
				placement: {scope: "node-local", cardinality: "one-per-node"}
				serviceEndpoints: _basementCoreServiceEndpoints
				runtimeListeners: _basementCoreRuntimeListeners
			},
			{
				id:           "terramate", kind:                               "terramate", rendererRef: "stackkit"
				templateRef:  "builtin://basement/core/terramate/v1", version: "1.0.0"
				contractHash: "sha256:6cac9e11984dca63a715d9eb886afbf96cd4bb2d210cce737468e7f2205c16d7"
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
				outputs: [
					"platform/basement-core/main.tf",
					"platform/basement-core/terramate.tm.hcl",
					"platform/basement-core/stack.tm.hcl",
				]
				placement: {scope: "node-local", cardinality: "one-per-node"}
				serviceEndpoints: _basementCoreServiceEndpoints
				runtimeListeners: _basementCoreRuntimeListeners
			},
			_architectureV2LocalKopiaSourceRenderUnit,
		]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:4db83db58296db815c26fdd95b16e1f1099c6c18648be77cf60efa74da9a2e53"
				unitRefs: ["compose", "source-policy"], artifactRefs: ["basement-core-compose", "local-kopia-backup-source-policy"]
				publicInputRefs: ["backup-source"], secretInputRefs: []
				planInputRefs: _architectureV2LocalKopiaSourceRenderUnit.planInputRefs
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:c7628f0520224fa57f4934e20711c55f15a50f2ed00721f67e662941a616037c"
				unitRefs: ["opentofu", "source-policy"], artifactRefs: ["basement-core-opentofu", "local-kopia-backup-source-policy"]
				publicInputRefs: ["backup-source"], secretInputRefs: []
				planInputRefs: _architectureV2LocalKopiaSourceRenderUnit.planInputRefs
			},
			{
				id:           "terramate", target: "terramate", rendererRef: "stackkit"
				contractHash: "sha256:b5b8c38c50d99d14cc822faeae79c76ab10c77fa7c01a4fc83578aa223ca8ff5"
				unitRefs: ["terramate", "source-policy"]
				artifactRefs: [
					"basement-core-terramate-opentofu",
					"basement-core-terramate-root",
					"basement-core-terramate-stack",
					"local-kopia-backup-source-policy",
				]
				publicInputRefs: ["backup-source"], secretInputRefs: []
				planInputRefs: _architectureV2LocalKopiaSourceRenderUnit.planInputRefs
			},
		]
		realizationSupport: {
			contractVersion: "1.0.0"
			scope:           "concrete"
			level:           "apply-ready"
			compatibleRendererRefs: ["stackkit"]
			inputs: {contractComplete: true, requiredRefs: ["backup-source"]}
			planInputs: {
				contractComplete: true
				requiredRefs:     _architectureV2LocalKopiaSourceRenderUnit.planInputRefs
			}
			artifacts: {
				requiredRefs: [
					"basement-core-compose",
					"basement-core-opentofu",
					"basement-core-terramate-opentofu",
					"basement-core-terramate-root",
					"basement-core-terramate-stack",
					"local-kopia-backup-source-policy",
				]
				outputBindings: [
					{
						artifactRef: "basement-core-compose", unitRef: "compose"
						outputRef:   "platform/basement-core/compose.yaml"
					},
					{
						artifactRef: "basement-core-opentofu", unitRef: "opentofu"
						outputRef:   "platform/basement-core/main.tf"
					},
					{
						artifactRef: "basement-core-terramate-opentofu", unitRef: "terramate"
						outputRef:   "platform/basement-core/main.tf"
					},
					{
						artifactRef: "basement-core-terramate-root", unitRef: "terramate"
						outputRef:   "platform/basement-core/terramate.tm.hcl"
					},
					{
						artifactRef: "basement-core-terramate-stack", unitRef: "terramate"
						outputRef:   "platform/basement-core/stack.tm.hcl"
					},
					{
						artifactRef: "local-kopia-backup-source-policy", unitRef: "source-policy"
						outputRef:   "home/backup/kopia-source-policy.json"
					},
				]
				contracts: [
					{
						id: "basement-core-compose", kind: "compose", format: "yaml", mode: "0640", required: true
						compatibleTargets: ["compose"], unitRef: "compose"
						outputRef:                               "platform/basement-core/compose.yaml"
					},
					{
						id: "basement-core-opentofu", kind: "opentofu", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["opentofu"], unitRef: "opentofu"
						outputRef:                                "platform/basement-core/main.tf"
					},
					{
						id: "basement-core-terramate-opentofu", kind: "terramate", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["terramate"], unitRef: "terramate"
						outputRef:                                 "platform/basement-core/main.tf"
					},
					{
						id: "basement-core-terramate-root", kind: "terramate", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["terramate"], unitRef: "terramate"
						outputRef:                                 "platform/basement-core/terramate.tm.hcl"
					},
					{
						id: "basement-core-terramate-stack", kind: "terramate", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["terramate"], unitRef: "terramate"
						outputRef:                                 "platform/basement-core/stack.tm.hcl"
					},
					{
						id: "local-kopia-backup-source-policy", kind: "native-config", format: "json", mode: "0600", required: true
						compatibleTargets: ["compose", "opentofu", "terramate"], unitRef: "source-policy"
						outputRef:                                                        "home/backup/kopia-source-policy.json"
					},
				]
			}
			evidence: requiredRefs: ["basement-core-runtime-evidence"]
		}
		health: [
			{id: "basement-router-http", kind: "http", path: "/ping", port: 8080, expectedStatuses: [200]},
			{id: "pocketid-http", kind: "http", path: "/", port: 1411, expectedStatuses: [200, 302]},
			{id: "tinyauth-http", kind: "http", path: "/", port: 4000, expectedStatuses: [200, 302]},
			{id: "step-ca-tcp", kind: "tcp", port: 9000},
			{id: "coolify-http", kind: "http", path: "/", port: 8000, expectedStatuses: [200, 302]},
			{id: "local-kopia-runtime-container", kind: "container", scope: "each-node"},
			{id: "basement-hub-http", kind: "http", path: "/healthz", port: 80, expectedStatuses: [200]},
		]
		evidence: ["basement-core-runtime-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-basement-core-lite-runtime"
			version:     "1.0.0"
			description: "Basement core runtime without Coolify PaaS: routing, identity, internal PKI, and the operator hub."
		}
		role:        "workload"
		providerRef: "stackkits-basement-core"
		provides:    _architectureV2BasementCoreCapabilities
		requires: ["stackkits-home-backup-target"]
		supportedSiteKinds: ["home"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "only"
			requiredRoles: ["controller", "worker"]
		}
		runtimeRequirements: {
			minCpuCores:  2
			minRamGB:     2
			minStorageGB: 10
		}
		computeProfiles: _architectureV2BasementCoreLiteComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "stackkit"
			engine:   "docker"
			image: {
				ref:    "docker.io/library/nginx:alpine"
				digest: "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"
			}
			entryComponentRef: "hub"
			components: [
				{
					id: "router", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/traefik/traefik:v3"
						digest: "sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8"
					}
					dependsOn: ["socket-proxy"], networkRefs: ["basement-core", "basement-control"]
					health: {kind: "http", path: "/ping", port: 8080}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "socket-proxy", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2"
						digest: "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
					}
					dependsOn: [], networkRefs: ["basement-control"]
					health: {kind: "image"}
					resources: {memoryLimit: "128m"}
				},
				{
					id: "pocketid", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/pocket-id/pocket-id:v2.7.0"
						digest: "sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e"
					}
					dependsOn: [], networkRefs: ["basement-core"]
					volumes: [{id: "pocketid-data", target: "/app/data", class: "persistent", backup: true}]
					health: {kind: "http", path: "/health", port: 1411}
					resources: {memoryLimit: "512m"}
				},
				{
					id: "tinyauth", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/steveiliop56/tinyauth:v5.0.7"
						digest: "sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"
					}
					dependsOn: ["pocketid"], networkRefs: ["basement-core"]
					volumes: [{id: "tinyauth-data", target: "/data", class: "persistent", backup: true}]
					health: {kind: "command", command: ["tinyauth", "healthcheck"]}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "step-ca", role: "application", lifecycle: "daemon"
					image: {
						ref:    "smallstep/step-ca:0.30.2"
						digest: "sha256:a2b17872915c193259b75a5474c398326f41bd199f0842093e52cf4182bc8270"
					}
					dependsOn: [], networkRefs: ["basement-core"]
					volumes: [{id: "step-ca-db", target: "/home/step/db", class: "persistent", backup: true}]
					health: {kind: "http", path: "/health", port: 9000}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "kopia-agent", role: "application", lifecycle: "daemon"
					image: {
						ref:    "docker.io/kopia/kopia:0.18.2"
						digest: "sha256:b6cb1f09a5fa832a320ee06d7803e82cdd7f69ac6f61d76a0d55fbbf1495c043"
					}
					dependsOn: [], networkRefs: ["basement-backup"]
					volumes: [
						{id: "kopia-repository", target: "/app/repository", class: "persistent", backup: false},
						{id: "kopia-config", target: "/app/config", class: "persistent", backup: false},
						{id: "kopia-cache", target: "/app/cache", class: "cache", backup: false},
						{id: "kopia-restore-staging", target: "/restore-staging", class: "persistent", backup: false},
					]
					health: {kind: "command", command: ["kopia", "--version"]}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "hub", role: "application", lifecycle: "daemon"
					image: {
						ref:    "docker.io/library/nginx:alpine"
						digest: "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"
					}
					dependsOn: ["tinyauth"], networkRefs: ["basement-core"]
					health: {kind: "http", path: "/healthz", port: 80}
					resources: {memoryLimit: "256m"}
				},
			]
		}
		serviceControls: _basementCoreLiteServiceControls
		renderUnits: [
			{
				id:           "compose", kind:                                         "compose", rendererRef: "stackkit"
				templateRef:  "builtin://basement/core-lite/compose/v1.yaml", version: "1.0.0"
				contractHash: "sha256:ad87a08fc59a84686db9160045e2e6a158b4107ff17bc9cb769e89afc00c356e"
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
				outputs: ["platform/basement-core-lite/compose.yaml"]
				placement: {scope: "node-local", cardinality: "one-per-node"}
				serviceEndpoints: _basementCoreLiteServiceEndpoints
				runtimeListeners: _basementCoreLiteRuntimeListeners
			},
			{
				id:           "opentofu", kind:                                       "opentofu", rendererRef: "stackkit"
				templateRef:  "builtin://basement/core-lite/opentofu/v1.tf", version: "1.0.0"
				contractHash: "sha256:b7e11c232677a50c30569345a4ded35ea52cc3cdf60c9874f0fc513ed31b6d36"
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
				outputs: ["platform/basement-core-lite/main.tf"]
				placement: {scope: "node-local", cardinality: "one-per-node"}
				serviceEndpoints: _basementCoreLiteServiceEndpoints
				runtimeListeners: _basementCoreLiteRuntimeListeners
			},
			{
				id:           "terramate", kind:                                    "terramate", rendererRef: "stackkit"
				templateRef:  "builtin://basement/core-lite/terramate/v1", version: "1.0.0"
				contractHash: "sha256:6282b30a8232c76a74c562d6d7eae310e131226f8c74c846c138f3c48a2f3fc2"
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
				outputs: [
					"platform/basement-core-lite/main.tf",
					"platform/basement-core-lite/terramate.tm.hcl",
					"platform/basement-core-lite/stack.tm.hcl",
				]
				placement: {scope: "node-local", cardinality: "one-per-node"}
				serviceEndpoints: _basementCoreLiteServiceEndpoints
				runtimeListeners: _basementCoreLiteRuntimeListeners
			},
			_architectureV2LocalKopiaSourceRenderUnit & {_outputRef: "home/backup/kopia-source-policy-lite.json"},
		]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:ad87a08fc59a84686db9160045e2e6a158b4107ff17bc9cb769e89afc00c356e"
				unitRefs: ["compose", "source-policy"], artifactRefs: ["basement-core-lite-compose", "local-kopia-backup-source-policy-lite"]
				publicInputRefs: _architectureV2LocalKopiaSourceRenderUnit.publicInputRefs, secretInputRefs: []
				planInputRefs: _architectureV2LocalKopiaSourceRenderUnit.planInputRefs
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:b7e11c232677a50c30569345a4ded35ea52cc3cdf60c9874f0fc513ed31b6d36"
				unitRefs: ["opentofu", "source-policy"], artifactRefs: ["basement-core-lite-opentofu", "local-kopia-backup-source-policy-lite"]
				publicInputRefs: _architectureV2LocalKopiaSourceRenderUnit.publicInputRefs, secretInputRefs: []
				planInputRefs: _architectureV2LocalKopiaSourceRenderUnit.planInputRefs
			},
			{
				id:           "terramate", target: "terramate", rendererRef: "stackkit"
				contractHash: "sha256:6282b30a8232c76a74c562d6d7eae310e131226f8c74c846c138f3c48a2f3fc2"
				unitRefs: ["terramate", "source-policy"]
				artifactRefs: [
					"basement-core-lite-terramate-opentofu",
					"basement-core-lite-terramate-root",
					"basement-core-lite-terramate-stack",
					"local-kopia-backup-source-policy-lite",
				]
				publicInputRefs: _architectureV2LocalKopiaSourceRenderUnit.publicInputRefs, secretInputRefs: []
				planInputRefs: _architectureV2LocalKopiaSourceRenderUnit.planInputRefs
			},
		]
		realizationSupport: {
			contractVersion: "1.0.0"
			scope:           "concrete"
			level:           "apply-ready"
			compatibleRendererRefs: ["stackkit"]
			inputs: {contractComplete: true, requiredRefs: _architectureV2LocalKopiaSourceRenderUnit.publicInputRefs}
			planInputs: {contractComplete: true, requiredRefs: _architectureV2LocalKopiaSourceRenderUnit.planInputRefs}
			artifacts: {
				requiredRefs: [
					"basement-core-lite-compose",
					"basement-core-lite-opentofu",
					"basement-core-lite-terramate-opentofu",
					"basement-core-lite-terramate-root",
					"basement-core-lite-terramate-stack",
					"local-kopia-backup-source-policy-lite",
				]
				outputBindings: [
					{
						artifactRef: "basement-core-lite-compose", unitRef: "compose"
						outputRef:   "platform/basement-core-lite/compose.yaml"
					},
					{
						artifactRef: "basement-core-lite-opentofu", unitRef: "opentofu"
						outputRef:   "platform/basement-core-lite/main.tf"
					},
					{
						artifactRef: "basement-core-lite-terramate-opentofu", unitRef: "terramate"
						outputRef:   "platform/basement-core-lite/main.tf"
					},
					{
						artifactRef: "basement-core-lite-terramate-root", unitRef: "terramate"
						outputRef:   "platform/basement-core-lite/terramate.tm.hcl"
					},
					{
						artifactRef: "basement-core-lite-terramate-stack", unitRef: "terramate"
						outputRef:   "platform/basement-core-lite/stack.tm.hcl"
					},
					{
						artifactRef: "local-kopia-backup-source-policy-lite", unitRef: "source-policy"
						outputRef:   "home/backup/kopia-source-policy-lite.json"
					},
				]
				contracts: [
					{
						id: "basement-core-lite-compose", kind: "compose", format: "yaml", mode: "0640", required: true
						compatibleTargets: ["compose"], unitRef: "compose"
						outputRef:                               "platform/basement-core-lite/compose.yaml"
					},
					{
						id: "basement-core-lite-opentofu", kind: "opentofu", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["opentofu"], unitRef: "opentofu"
						outputRef:                                "platform/basement-core-lite/main.tf"
					},
					{
						id: "basement-core-lite-terramate-opentofu", kind: "terramate", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["terramate"], unitRef: "terramate"
						outputRef:                                 "platform/basement-core-lite/main.tf"
					},
					{
						id: "basement-core-lite-terramate-root", kind: "terramate", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["terramate"], unitRef: "terramate"
						outputRef:                                 "platform/basement-core-lite/terramate.tm.hcl"
					},
					{
						id: "basement-core-lite-terramate-stack", kind: "terramate", format: "hcl", mode: "0640", required: true
						compatibleTargets: ["terramate"], unitRef: "terramate"
						outputRef:                                 "platform/basement-core-lite/stack.tm.hcl"
					},
					{
						id: "local-kopia-backup-source-policy-lite", kind: "native-config", format: "json", mode: "0600", required: true
						compatibleTargets: ["compose", "opentofu", "terramate"], unitRef: "source-policy"
						outputRef:                                                        "home/backup/kopia-source-policy-lite.json"
					},
				]
			}
			evidence: requiredRefs: ["basement-core-runtime-evidence"]
		}
		health: [
			{id: "basement-router-http", kind: "http", path: "/ping", port: 8080, expectedStatuses: [200]},
			{id: "pocketid-http", kind: "http", path: "/", port: 1411, expectedStatuses: [200, 302]},
			{id: "tinyauth-http", kind: "http", path: "/", port: 4000, expectedStatuses: [200, 302]},
			{id: "step-ca-tcp", kind: "tcp", port: 9000},
			{id: "local-kopia-runtime-container", kind: "container", scope: "each-node"},
			{id: "basement-hub-http", kind: "http", path: "/healthz", port: 80, expectedStatuses: [200]},
		]
		evidence: ["basement-core-runtime-evidence"]
	},
	{
		metadata: {
			id:          "stackkits-immich-runtime"
			version:     "1.0.1"
			description: "Immich photo service contract bound to the control-authority site and its personal-data primary."
		}
		role:        "workload"
		providerRef: "stackkits-immich"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		computeProfiles: _architectureV2ImmichComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "application-adapter"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/immich-app/immich-server:v2.7.0"
				digest: "sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1"
			}
			entryComponentRef: "immich-server"
			components: [
				{
					id: "immich-server", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/immich-app/immich-server:v2.7.0"
						digest: "sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1"
					}
					dependsOn: ["immich-machine-learning", "immich-postgres-init", "immich-valkey"]
					networkRefs: ["immich-internal"]
					environment: {
						DB_HOSTNAME:    "immich-postgres", DB_PORT:  "5432", DB_USERNAME:                 "immich", DB_DATABASE_NAME: "immich"
						REDIS_HOSTNAME: "immich-valkey", REDIS_PORT: "6379", IMMICH_MACHINE_LEARNING_URL: "http://immich-machine-learning:3003"
					}
					secretEnvironment: DB_PASSWORD: "database-password"
					volumes: [for allocation in _architectureV2PhotosInfrastructure.storageAllocation.allocations if allocation.componentRef == "immich-server" {
						id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
					}]
					health: {kind: "http", path: "/api/server/ping", port: 2283}
					resources: {memoryLimit: "3g", memoryReservation: "512m"}
				},
				{
					id: "immich-machine-learning", role: "machine-learning", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/immich-app/immich-machine-learning:v2.7.0"
						digest: "sha256:aff861526d690bb720130a46bd48ee2827c44d2f601a194e61f31e979a591952"
					}
					dependsOn: [], networkRefs: ["immich-internal"]
					volumes: [for allocation in _architectureV2PhotosInfrastructure.storageAllocation.allocations if allocation.componentRef == "immich-machine-learning" {
						id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
					}]
					// The image's own healthcheck inherits Docker's defaults, which
					// give this model server no start period and mark it unhealthy
					// before it finishes loading. Running the same script as a
					// declared command health gets the generous renderer timings.
					health: {kind: "command", command: ["python3", "healthcheck.py"]}
					resources: {memoryLimit: "3g", memoryReservation: "512m"}
				},
				{
					id: "immich-postgres", role: "database", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0"
						digest: "sha256:bcf63357191b76a916ae5eb93464d65c07511da41e3bf7a8416db519b40b1c23"
					}
					dependsOn: [], networkRefs: ["immich-internal"]
					environment: {POSTGRES_USER: "immich", POSTGRES_DB: "immich", POSTGRES_INITDB_ARGS: "--data-checksums"}
					secretEnvironment: POSTGRES_PASSWORD: "database-password"
					volumes: [for allocation in _architectureV2PhotosInfrastructure.storageAllocation.allocations if allocation.componentRef == "immich-postgres" {
						id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
					}]
					health: {kind: "command", command: ["pg_isready", "-U", "immich", "-d", "postgres"]}
					resources: {memoryLimit: "2g", memoryReservation: "256m"}
				},
				{
					id: "immich-postgres-init", role: "database-init", lifecycle: "one-shot"
					image: {
						ref:    "ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0"
						digest: "sha256:bcf63357191b76a916ae5eb93464d65c07511da41e3bf7a8416db519b40b1c23"
					}
					dependsOn: ["immich-postgres"], networkRefs: ["immich-internal"]
					command: ["sh", "-c", "until pg_isready -h immich-postgres -U immich -d postgres; do sleep 1; done; psql -h immich-postgres -U immich -d postgres -tAc \"SELECT 1 FROM pg_database WHERE datname = 'immich'\" | grep -q 1 || createdb -h immich-postgres -U immich immich"]
					environment: {PGUSER: "immich"}
					secretEnvironment: PGPASSWORD: "database-password"
					health: {kind: "completion"}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "immich-valkey", role: "cache", lifecycle: "daemon"
					image: {
						ref:    "docker.io/valkey/valkey:9"
						digest: "sha256:3b55fbaa0cd93cf0d9d961f405e4dfcc70efe325e2d84da207a0a8e6d8fde4f9"
					}
					dependsOn: [], networkRefs: ["immich-internal"]
					command: ["valkey-server"]
					health: {kind: "command", command: ["redis-cli", "ping"]}
					resources: {memoryLimit: "512m", memoryReservation: "64m"}
				},
			]
		}
		renderUnits: [{
			id:          "immich-server"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://workloads/immich/bundle/v2.json"
			version:      "3.0.0"
			contractHash: "sha256:a84c2f253497052d30677aaecc3b0d73793a5b48ea36c380b01c3c385aa9064e"
			publicInputRefs: ["delivery-route"]
			inputBindings: [{
				targetRef: "delivery-route", sourceRef:                    "network.moduleRoute"
				valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null
			}]
			secretInputRefs: ["database-password"]
			outputs: ["workloads/immich/bundle.json"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
			serviceEndpoints: [{
				serviceRef:       "photos"
				upstreamProtocol: "http"
				targetPort:       2283
				allowedIngressProtocols: ["http", "https"]
				allowedExposures: ["local", "remote-private", "public"]
				originSelector: "control-authority-site"
				healthRef:      "immich-http"
				data: {
					bindingRef:      _architectureV2PhotosInfrastructure.dataBinding.bindingRef
					requiredClasses: _architectureV2PhotosInfrastructure.dataBinding.classes
					locality:        _architectureV2PhotosInfrastructure.dataBinding.locality
				}
			}]
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:383c8a53811d3c7abb2049a188af77152367ea56b7c47716dc9a7144b7cda99e"
				unitRefs: ["immich-server"], artifactRefs: ["immich-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: ["database-password"], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:c8cafbfb8ada7743566432b94ad908dfaef53b62db343f80e38fad5c34ab9d33"
				unitRefs: ["immich-server"], artifactRefs: ["immich-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: ["database-password"], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2ImmichSupport
		health: [{
			id:             "immich-http"
			phase:          "continuous"
			kind:           "http"
			path:           "/api/server/ping"
			port:           2283
			timeoutSeconds: 10
			expectedStatuses: [200]
		}]
		evidence: ["SK-S1", "SK-S2", "SK-S4", "immich-selected-paas-runtime-contract"]
		rilActionPrimitives: [{
			id:      "inspect-immich-runtime-health", version: "1.0.0", title:     "Inspect governed Immich runtime health", category: "verify"
			support: "contract-only", mutation:                false, destructive: false, risk:                                        "read-only"
			owner: {authority: "stackkits", operationClass: "immich-health-readback"}
			extensionAuthority: {
				kind: "module", moduleRef: "stackkits-immich-runtime", providerRef: "stackkits-immich"
			}
			approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "owner-step-up", receiptRequired: true}
			grant: {required: true, audience: "stackkits", scopes: ["stackkit-immich-health-read"], connectorBindingRequired: true}
			target: {scope: "module-instance", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: false, requiresRuntimeInstanceRef: false}
			inputs: []
			verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["readback"]}
			recovery: {kind: "none", requiredOnFailure: false}
		}]
	},
	{
		metadata: {
			id:          "stackkits-immich-lite-runtime"
			version:     "1.0.0"
			description: "Immich photo service without machine-learning, bound to the control-authority site and its personal-data primary."
		}
		role:        "workload"
		providerRef: "stackkits-immich-lite"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		computeProfiles: _architectureV2ImmichLiteComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "application-adapter"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/immich-app/immich-server:v2.7.0"
				digest: "sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1"
			}
			entryComponentRef: "immich-server"
			components: [
				{
					id: "immich-server", role: "application", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/immich-app/immich-server:v2.7.0"
						digest: "sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1"
					}
					dependsOn: ["immich-postgres-init", "immich-valkey"]
					networkRefs: ["immich-internal"]
					environment: {
						DB_HOSTNAME:    "immich-postgres", DB_PORT:  "5432", DB_USERNAME: "immich", DB_DATABASE_NAME: "immich"
						REDIS_HOSTNAME: "immich-valkey", REDIS_PORT: "6379"
					}
					secretEnvironment: DB_PASSWORD: "database-password"
					volumes: [for allocation in _architectureV2PhotosLiteInfrastructure.storageAllocation.allocations if allocation.componentRef == "immich-server" {
						id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
					}]
					health: {kind: "http", path: "/api/server/ping", port: 2283}
					resources: {memoryLimit: "3g", memoryReservation: "512m"}
				},
				{
					id: "immich-postgres", role: "database", lifecycle: "daemon"
					image: {
						ref:    "ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0"
						digest: "sha256:bcf63357191b76a916ae5eb93464d65c07511da41e3bf7a8416db519b40b1c23"
					}
					dependsOn: [], networkRefs: ["immich-internal"]
					environment: {POSTGRES_USER: "immich", POSTGRES_DB: "immich", POSTGRES_INITDB_ARGS: "--data-checksums"}
					secretEnvironment: POSTGRES_PASSWORD: "database-password"
					volumes: [for allocation in _architectureV2PhotosLiteInfrastructure.storageAllocation.allocations if allocation.componentRef == "immich-postgres" {
						id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
					}]
					health: {kind: "command", command: ["pg_isready", "-U", "immich", "-d", "postgres"]}
					resources: {memoryLimit: "2g", memoryReservation: "256m"}
				},
				{
					id: "immich-postgres-init", role: "database-init", lifecycle: "one-shot"
					image: {
						ref:    "ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0"
						digest: "sha256:bcf63357191b76a916ae5eb93464d65c07511da41e3bf7a8416db519b40b1c23"
					}
					dependsOn: ["immich-postgres"], networkRefs: ["immich-internal"]
					command: ["sh", "-c", "until pg_isready -h immich-postgres -U immich -d postgres; do sleep 1; done; psql -h immich-postgres -U immich -d postgres -tAc \"SELECT 1 FROM pg_database WHERE datname = 'immich'\" | grep -q 1 || createdb -h immich-postgres -U immich immich"]
					environment: {PGUSER: "immich"}
					secretEnvironment: PGPASSWORD: "database-password"
					health: {kind: "completion"}
					resources: {memoryLimit: "256m"}
				},
				{
					id: "immich-valkey", role: "cache", lifecycle: "daemon"
					image: {
						ref:    "docker.io/valkey/valkey:9"
						digest: "sha256:3b55fbaa0cd93cf0d9d961f405e4dfcc70efe325e2d84da207a0a8e6d8fde4f9"
					}
					dependsOn: [], networkRefs: ["immich-internal"]
					command: ["valkey-server"]
					health: {kind: "command", command: ["redis-cli", "ping"]}
					resources: {memoryLimit: "512m", memoryReservation: "64m"}
				},
			]
		}
		renderUnits: [{
			id:          "immich-server"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://workloads/immich-lite/bundle/v2.json"
			version:      "3.0.0"
			contractHash: "sha256:9e63b69bfdbcc6df895134afe9261b21300faf20776458a84a75222fb5d88df6"
			publicInputRefs: ["delivery-route"]
			inputBindings: [{
				targetRef: "delivery-route", sourceRef:                    "network.moduleRoute"
				valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null
			}]
			secretInputRefs: ["database-password"]
			outputs: ["workloads/immich-lite/bundle.json"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
			serviceEndpoints: [{
				serviceRef:       "photos"
				upstreamProtocol: "http"
				targetPort:       2283
				allowedIngressProtocols: ["http", "https"]
				allowedExposures: ["local", "remote-private", "public"]
				originSelector: "control-authority-site"
				healthRef:      "immich-http"
				data: {
					bindingRef:      _architectureV2PhotosLiteInfrastructure.dataBinding.bindingRef
					requiredClasses: _architectureV2PhotosLiteInfrastructure.dataBinding.classes
					locality:        _architectureV2PhotosLiteInfrastructure.dataBinding.locality
				}
			}]
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:9e63b69bfdbcc6df895134afe9261b21300faf20776458a84a75222fb5d88df6"
				unitRefs: ["immich-server"], artifactRefs: ["immich-lite-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: ["database-password"], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:9e63b69bfdbcc6df895134afe9261b21300faf20776458a84a75222fb5d88df6"
				unitRefs: ["immich-server"], artifactRefs: ["immich-lite-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: ["database-password"], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2ImmichLiteSupport
		health: [{
			id:             "immich-http"
			phase:          "continuous"
			kind:           "http"
			path:           "/api/server/ping"
			port:           2283
			timeoutSeconds: 10
			expectedStatuses: [200]
		}]
		evidence: ["SK-S1", "SK-S2", "SK-S4", "immich-selected-paas-runtime-contract"]
	},
	{
		metadata: {
			id:          "stackkits-cloudreve-runtime"
			version:     "1.0.0"
			description: "Cloudreve file service contract bound to one selected site and its personal-data primary."
		}
		role:        "workload"
		providerRef: "stackkits-cloudreve"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		computeProfiles: _architectureV2CloudreveComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "application-adapter"
			engine:   "docker"
			image: {
				ref:    "docker.io/cloudreve/cloudreve:4.18.0"
				digest: "sha256:f7a464100bf6325e9ba58cb2b0ee60f9a24c58fc2eb90647720bc4b8f3cddd9a"
			}
			entryComponentRef: "cloudreve"
			components: [{
				id: "cloudreve", role: "application", lifecycle: "daemon"
				image: {
					ref:    "docker.io/cloudreve/cloudreve:4.18.0"
					digest: "sha256:f7a464100bf6325e9ba58cb2b0ee60f9a24c58fc2eb90647720bc4b8f3cddd9a"
				}
				dependsOn: []
				networkRefs: ["cloudreve-internal"]
				volumes: [for allocation in _architectureV2FilesInfrastructure.storageAllocation.allocations if allocation.componentRef == "cloudreve" {
					id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
				}]
				health: {kind: "http", path: "/", port: 5212}
				resources: {memoryLimit: "1g", memoryReservation: "128m"}
			}]
		}
		renderUnits: [{
			id:          "cloudreve"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://workloads/cloudreve/bundle/v2.json"
			version:      "2.0.0"
			contractHash: "sha256:a6343c858576f1dee488e363779962fa9c4f7feda0f2592ced538fa86e934ae0"
			publicInputRefs: ["delivery-route"]
			inputBindings: [{
				targetRef: "delivery-route", sourceRef:                    "network.moduleRoute"
				valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null
			}]
			secretInputRefs: []
			outputs: ["workloads/cloudreve/bundle.json"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
			serviceEndpoints: [{
				serviceRef:       "files"
				upstreamProtocol: "http"
				targetPort:       5212
				allowedIngressProtocols: ["http", "https"]
				allowedExposures: ["local", "remote-private", "public"]
				originSelector: "control-authority-site"
				healthRef:      "cloudreve-http"
				data: {
					bindingRef:      _architectureV2FilesInfrastructure.dataBinding.bindingRef
					requiredClasses: _architectureV2FilesInfrastructure.dataBinding.classes
					locality:        _architectureV2FilesInfrastructure.dataBinding.locality
				}
			}]
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:351ef1abec597588906c6abedfeb95c85fe8fed5be63937c16a0c178c1210130"
				unitRefs: ["cloudreve"], artifactRefs: ["cloudreve-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:58609d10adde5f149301184cd8f24c375da1f8eadd6f2ce8e83c58c3dca34520"
				unitRefs: ["cloudreve"], artifactRefs: ["cloudreve-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2CloudreveSupport
		health: [{
			id:             "cloudreve-http"
			phase:          "continuous"
			kind:           "http"
			path:           "/"
			port:           5212
			timeoutSeconds: 10
			expectedStatuses: [200, 302]
		}]
		evidence: ["cloudreve-selected-paas-runtime-contract"]
	},
	{
		metadata: {
			id:          "stackkits-vaultwarden-runtime"
			version:     "1.0.0"
			description: "Vaultwarden password-vault contract bound to one selected site and its secret-data primary."
		}
		role:        "workload"
		providerRef: "stackkits-vaultwarden"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		computeProfiles: _architectureV2VaultwardenComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "application-adapter"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/dani-garcia/vaultwarden:1.35.4"
				digest: "sha256:43498a94b22f9563f2a94b53760ab3e710eefc0d0cac2efda4b12b9eb8690664"
			}
			entryComponentRef: "vaultwarden"
			components: [{
				id: "vaultwarden", role: "application", lifecycle: "daemon"
				image: {
					ref:    "ghcr.io/dani-garcia/vaultwarden:1.35.4"
					digest: "sha256:43498a94b22f9563f2a94b53760ab3e710eefc0d0cac2efda4b12b9eb8690664"
				}
				dependsOn: []
				networkRefs: ["vaultwarden-internal"]
				environment: SIGNUPS_ALLOWED:   "false"
				secretEnvironment: ADMIN_TOKEN: "admin-token"
				volumes: [for allocation in _architectureV2VaultInfrastructure.storageAllocation.allocations if allocation.componentRef == "vaultwarden" {
					id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
				}]
				health: {kind: "http", path: "/alive", port: 80}
				resources: {memoryLimit: "512m", memoryReservation: "64m"}
			}]
		}
		renderUnits: [{
			id:          "vaultwarden"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://workloads/vaultwarden/bundle/v2.json"
			version:      "2.0.0"
			contractHash: "sha256:fa23863a99c114b5662986eb6b814504c8b1dd1dbfe4aa0c03b504ba451a58ce"
			publicInputRefs: ["delivery-route"]
			inputBindings: [{
				targetRef: "delivery-route", sourceRef:                    "network.moduleRoute"
				valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null
			}]
			secretInputRefs: ["admin-token"]
			outputs: ["workloads/vaultwarden/bundle.json"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
			serviceEndpoints: [{
				serviceRef:        "vault"
				upstreamProtocol:  "http"
				targetPort:        80
				requiredPrivilege: "vault"
				allowedIngressProtocols: ["http", "https"]
				allowedExposures: ["local", "remote-private", "public"]
				originSelector: "control-authority-site"
				healthRef:      "vaultwarden-http"
				data: {
					bindingRef:      _architectureV2VaultInfrastructure.dataBinding.bindingRef
					requiredClasses: _architectureV2VaultInfrastructure.dataBinding.classes
					locality:        _architectureV2VaultInfrastructure.dataBinding.locality
				}
			}]
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:efac52c8e5f859db1840d54bf3b18d1f1f9b58fe14a52c01d7a63476b6481a56"
				unitRefs: ["vaultwarden"], artifactRefs: ["vaultwarden-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: ["admin-token"], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:b5726cb7e278a8a0d3b083e83c41f107a8c08b200b7989c02ebc52da8bebb430"
				unitRefs: ["vaultwarden"], artifactRefs: ["vaultwarden-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: ["admin-token"], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2VaultwardenSupport
		health: [{
			id:             "vaultwarden-http"
			phase:          "continuous"
			kind:           "http"
			path:           "/alive"
			port:           80
			timeoutSeconds: 10
			expectedStatuses: [200]
		}]
		evidence: ["vaultwarden-selected-paas-runtime-contract"]
	},
	{
		metadata: {
			id:          "stackkits-jellyfin-runtime"
			version:     "1.0.0"
			description: "Jellyfin media-library contract bound to one selected site; the media library volume is owner-custodied and not a StackKits backup source."
		}
		role:        "workload"
		providerRef: "stackkits-jellyfin"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		computeProfiles: _architectureV2JellyfinComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "application-adapter"
			engine:   "docker"
			image: {
				ref:    "docker.io/jellyfin/jellyfin:10.10.7"
				digest: "sha256:7ae36aab93ef9b6aaff02b37f8bb23df84bb2d7a3f6054ec8fc466072a648ce2"
			}
			entryComponentRef: "jellyfin"
			components: [{
				id: "jellyfin", role: "application", lifecycle: "daemon"
				image: {
					ref:    "docker.io/jellyfin/jellyfin:10.10.7"
					digest: "sha256:7ae36aab93ef9b6aaff02b37f8bb23df84bb2d7a3f6054ec8fc466072a648ce2"
				}
				dependsOn: []
				networkRefs: ["jellyfin-internal"]
				volumes: [for allocation in _architectureV2MediaInfrastructure.storageAllocation.allocations if allocation.componentRef == "jellyfin" {
					id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
				}]
				health: {kind: "http", path: "/health", port: 8096}
				resources: {memoryLimit: "2g", memoryReservation: "512m"}
			}]
		}
		renderUnits: [{
			id:          "jellyfin"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://workloads/jellyfin/bundle/v2.json"
			version:      "2.0.0"
			contractHash: "sha256:8e48d778e46c4d9ffa0b5bec4921c4775438b74e7119d2dc530a3c27f572905f"
			publicInputRefs: ["delivery-route"]
			inputBindings: [{
				targetRef: "delivery-route", sourceRef:                    "network.moduleRoute"
				valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null
			}]
			secretInputRefs: []
			outputs: ["workloads/jellyfin/bundle.json"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
			serviceEndpoints: [{
				serviceRef:       "media"
				upstreamProtocol: "http"
				targetPort:       8096
				allowedIngressProtocols: ["http", "https"]
				allowedExposures: ["local", "remote-private", "public"]
				originSelector: "control-authority-site"
				healthRef:      "jellyfin-http"
				data: {
					bindingRef:      _architectureV2MediaInfrastructure.dataBinding.bindingRef
					requiredClasses: _architectureV2MediaInfrastructure.dataBinding.classes
					locality:        _architectureV2MediaInfrastructure.dataBinding.locality
				}
			}]
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:f4cc3429148975e7741e8a55171d5a9d0138d67ec7b4ef42732ede51d7b53af8"
				unitRefs: ["jellyfin"], artifactRefs: ["jellyfin-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:47ffd0559451c9da935a2e115b3a7a139aef279e271c8389c8fc276674e6c9b9"
				unitRefs: ["jellyfin"], artifactRefs: ["jellyfin-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2JellyfinSupport
		health: [{
			id:             "jellyfin-http"
			phase:          "continuous"
			kind:           "http"
			path:           "/health"
			port:           8096
			timeoutSeconds: 10
			expectedStatuses: [200]
		}]
		evidence: ["jellyfin-selected-paas-runtime-contract"]
	},
	{
		metadata: {
			id:          "stackkits-home-assistant-runtime"
			version:     "1.0.0"
			description: "Home Assistant self-hosted container contract bound to one selected site; config is a StackKits backup source. Native product MCP remains /api/mcp on the same listener."
		}
		role:        "workload"
		providerRef: "stackkits-home-assistant"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		computeProfiles: _architectureV2HomeAssistantComputeProfiles
		runtime: {
			kind:     "container"
			delivery: "application-adapter"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/home-assistant/home-assistant:2026.7.2"
				digest: "sha256:1476924357b46e80735c13e94232ba5c853cac052e9df4bb28d50fa56348097b"
			}
			entryComponentRef: "home-assistant"
			components: [{
				id: "home-assistant", role: "application", lifecycle: "daemon"
				image: {
					ref:    "ghcr.io/home-assistant/home-assistant:2026.7.2"
					digest: "sha256:1476924357b46e80735c13e94232ba5c853cac052e9df4bb28d50fa56348097b"
				}
				dependsOn: []
				networkRefs: ["home-assistant-internal"]
				volumes: [for allocation in _architectureV2SmartHomeInfrastructure.storageAllocation.allocations if allocation.componentRef == "home-assistant" {
					id: allocation.volumeRef, target: allocation.target, class: allocation.class, backup: allocation.backup
				}]
				health: {kind: "http", path: "/", port: 8123}
				resources: {memoryLimit: "2g", memoryReservation: "512m"}
			}]
		}
		renderUnits: [{
			id:          "home-assistant"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://workloads/home-assistant/bundle/v2.json"
			version:      "2.0.0"
			contractHash: "sha256:8ace48ca66779adec03b5362b1f0cf33eb576e00cb7cd0b036d6c2e7512cbd7f"
			publicInputRefs: ["delivery-route"]
			inputBindings: [{
				targetRef: "delivery-route", sourceRef:                    "network.moduleRoute"
				valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null
			}]
			secretInputRefs: []
			outputs: ["workloads/home-assistant/bundle.json"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
			serviceEndpoints: [{
				serviceRef:       "smart-home"
				upstreamProtocol: "http"
				targetPort:       8123
				allowedIngressProtocols: ["http", "https"]
				allowedExposures: ["local", "remote-private", "public"]
				originSelector: "control-authority-site"
				healthRef:      "home-assistant-http"
				data: {
					bindingRef:      _architectureV2SmartHomeInfrastructure.dataBinding.bindingRef
					requiredClasses: _architectureV2SmartHomeInfrastructure.dataBinding.classes
					locality:        _architectureV2SmartHomeInfrastructure.dataBinding.locality
				}
			}]
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:158d52e1794a9dddf5a1b6ca134c51f115a6d721b1afea22f8b89add28108695"
				unitRefs: ["home-assistant"], artifactRefs: ["home-assistant-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:d15f978c9b540c10de3c1cd311a97aed52f5bf4eef99c75ecda2c5cc0e718fb5"
				unitRefs: ["home-assistant"], artifactRefs: ["home-assistant-workload-bundle"]
				publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2HomeAssistantSupport
		health: [{
			id:             "home-assistant-http"
			phase:          "continuous"
			kind:           "http"
			path:           "/"
			port:           8123
			timeoutSeconds: 10
			expectedStatuses: [200]
		}]
		evidence: ["home-assistant-selected-paas-runtime-contract"]
	},
	{
		metadata: {
			id:          "stackkits-coolify-runtime"
			version:     "1.0.0"
			description: "Workload-scoped Coolify adapter contract; provider lifecycle, endpoints, leases, and credential material remain outside StackKits."
		}
		role:        "platform"
		providerRef: "stackkits-coolify"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		runtimeAdapter: {
			id: "coolify"
			supportedKinds: ["container"]
			supportedDeliveries: ["application-adapter"]
			operations: ["apply", "observe"]
			credentialCustody: "external-owner"
			providerLifecycle: "not-owned"
			evidenceRequired:  true
		}
		runtime: {execution: "contract-handoff", kind: "control-plane", delivery: "external-control-plane"}
		renderUnits: [{
			id:          "coolify-adapter"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://platform/coolify/runtime-adapter/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:0115f3bdc1a7806823330a1a271085c9721549f9b69dbb0e9fd2da25bada97ab"
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			outputs: ["platform/coolify/runtime-adapter.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:3860bf406244e848711a32034420330018147993188eeaa43fa6b7f9428cff86"
				unitRefs: ["coolify-adapter"], artifactRefs: ["coolify-runtime-adapter"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:b9331740d5a3ab65809133c45bb771fa671ad9c2c74c8b504c862c542e267d09"
				unitRefs: ["coolify-adapter"], artifactRefs: ["coolify-runtime-adapter"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2CoolifyAdapterSupport
		health: [{id: "coolify-runtime-contract", kind: "contract"}]
		evidence: ["coolify-adapter-contract"]
	},
	{
		metadata: {
			id:          "stackkits-standalone-compose-runtime"
			version:     "1.0.0"
			description: "StackKits-owned no-PaaS workload adapter over an admitted Docker host; host and server-provider lifecycle remain outside this contract."
		}
		role:        "platform"
		providerRef: "stackkits-standalone-compose"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		runtimeAdapter: {
			id: "standalone-compose"
			supportedKinds: ["container"]
			supportedDeliveries: ["application-adapter"]
			operations: ["apply", "observe"]
			credentialCustody: "local-owner"
			providerLifecycle: "not-owned"
			evidenceRequired:  true
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:          "standalone-compose-adapter"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://platform/standalone-compose/runtime-adapter/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:552389b0d9c11503754d264e39654aebece5251fa92cdd38d6f59b9790734e05"
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			outputs: ["platform/standalone-compose/runtime-adapter.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:75a6431c0ad93a09a27d454c7f29712b910287b5b03668a5c1f5f089ef0daba5"
				unitRefs: ["standalone-compose-adapter"], artifactRefs: ["standalone-compose-runtime-adapter"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:b98d542d03eaf368279943dbffbd6641ddc49df854e13f988b59289ac62f067d"
				unitRefs: ["standalone-compose-adapter"], artifactRefs: ["standalone-compose-runtime-adapter"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2StandaloneComposeAdapterSupport
		health: [{id: "standalone-compose-runtime-contract", kind: "contract"}]
		evidence: ["standalone-compose-adapter-contract"]
	},
	{
		metadata: {
			id:          "stackkits-komodo-core-runtime"
			version:     "1.0.0"
			description: "Workload-scoped Komodo Core API adapter contract; installation, endpoints, credentials, leases, and provider lifecycle remain outside StackKits."
		}
		role:        "platform"
		providerRef: "stackkits-komodo"
		provides: []
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "only"
			requiredRoles: ["worker"]
		}
		runtimeAdapter: {
			id: "komodo"
			supportedKinds: ["container"]
			supportedDeliveries: ["application-adapter"]
			operations: ["apply", "observe"]
			agentRefs: ["komodo-periphery"]
			credentialCustody: "external-owner"
			providerLifecycle: "not-owned"
			evidenceRequired:  true
		}
		runtime: {execution: "contract-handoff", kind: "control-plane", delivery: "external-control-plane"}
		renderUnits: [{
			id:          "komodo-core-adapter"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://platform/komodo/core-runtime-adapter/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:c9f14e4bd228574a3a4c26465141fcc0f57f1eddf8d24aa5d418f28df8095c34"
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			outputs: ["platform/komodo/core-runtime-adapter.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:2c3b41071fea2cc762fe0051473e2b8dca24c7469964b875cd1bc8b5e3697274"
				unitRefs: ["komodo-core-adapter"], artifactRefs: ["komodo-core-runtime-adapter"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:3a03864406d17b324d61c923ce55c12616364e9cdb8a844c699b81da8badaaff"
				unitRefs: ["komodo-core-adapter"], artifactRefs: ["komodo-core-runtime-adapter"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2KomodoCoreAdapterSupport
		health: [{id: "komodo-core-runtime-contract", kind: "contract", scope: "each-node"}]
		evidence: ["komodo-core-adapter-contract"]
	},
	{
		metadata: {
			id:          "stackkits-komodo-periphery-runtime"
			version:     "1.0.0"
			description: "Komodo Periphery node-agent handoff for control-authority-site workers; host execution and all trust material remain executor-mediated."
		}
		role:        "platform"
		providerRef: "stackkits-komodo"
		provides: []
		requires: ["stackkits-komodo-core-runtime"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "control-authority-site"
			controlPlaneMembers: "any"
			requiredRoles: ["worker"]
		}
		runtimeAdapterAgent: {
			id:          "komodo-periphery"
			adapterRef:  "komodo"
			role:        "node-agent"
			targetScope: "control-authority-site-workers"
			connection: {
				direction:         "outbound-to-control-plane"
				transport:         "tls"
				minimumTLSVersion: "TLS1.3"
				authentication:    "mutual-key"
			}
			credentialCustody: "external-owner"
			hostExecution:     "executor-mediated"
			providerLifecycle: "not-owned"
			evidenceRequired:  true
		}
		runtime: {execution: "contract-handoff", kind: "host", delivery: "external-control-plane"}
		renderUnits: [{
			id:          "komodo-periphery-agent"
			kind:        "native-config"
			rendererRef: "stackkit"
			compatibleTargets: ["compose", "opentofu"]
			templateRef:  "builtin://platform/komodo/periphery-agent/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:73f2410689ff511901d9e8a255022f4a499816662fb5f387a3f2a16f9e0e6b95"
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			outputs: ["platform/komodo/periphery-agent.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		renderVariants: [
			{
				id:           "compose", target: "compose", rendererRef: "stackkit"
				contractHash: "sha256:6ad3cfd8d11856955479bce39d0d06219cf4df410f3d5395c0b24d42c698c58a"
				unitRefs: ["komodo-periphery-agent"], artifactRefs: ["komodo-periphery-runtime-agent"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
			{
				id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
				contractHash: "sha256:08fd20fcd5a4d80d0c6b3c3a8ec41dc872aa3effc1b8d33435c1d665eb280aa3"
				unitRefs: ["komodo-periphery-agent"], artifactRefs: ["komodo-periphery-runtime-agent"]
				publicInputRefs: [], secretInputRefs: [], planInputRefs: []
			},
		]
		realizationSupport: _architectureV2KomodoPeripheryAgentSupport
		health: [{id: "komodo-periphery-runtime-contract", kind: "contract", scope: "each-node"}]
		evidence: ["komodo-periphery-agent-contract"]
	},
],
	_architectureV2ProfileExtensionModules,
	[for haRealization in _architectureV2HARealizations {
		metadata: {
			id:          haRealization.moduleID
			version:     "1.0.0"
			description: "Kit and mode specific high-availability realization bound exclusively to explicit control-plane members."
		}
		role:               "operations"
		providerRef:        haRealization.providerID
		provides:           _architectureV2HACapabilities
		supportedSiteKinds: haRealization.supportedSiteKinds
		nodeSelection: {
			authority:           haRealization.authoritySelection
			controlPlaneMembers: "only"
		}
		runtime: {execution: "executable", kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:           "executor-contract", kind:                                      "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://availability/ha/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:0a7aa7b06915bb94c8846f653c8e8fad37849f35a29642f5b11bc60ec0127784"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "controlPlane", "moduleTargets", "moduleCapabilities", "availability"]
			outputs: ["availability/ha/\(haRealization.moduleID)/executor-contract.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2HAAvailabilityRuntimeSupport & {
			artifacts: {
				requiredRefs: ["\(haRealization.moduleID)-executor-contract"]
				outputBindings: [{artifactRef: "\(haRealization.moduleID)-executor-contract", unitRef: "executor-contract", outputRef: "availability/ha/\(haRealization.moduleID)/executor-contract.json"}]
				contracts: [{
					id: "\(haRealization.moduleID)-executor-contract", kind: "native-config", format: "json", mode: "0640", required: true
					compatibleTargets: ["opentofu", "compose"]
					unitRef: "executor-contract", outputRef: "availability/ha/\(haRealization.moduleID)/executor-contract.json"
				}]
			}
		}
		health: [{id: haRealization.healthID, kind: "contract", scope: "each-node"}]
		evidence: [haRealization.evidenceRef]
	}],
])

_architectureV2AddOns: [{
	metadata: {
		id:          "ha"
		version:     "1.0.0"
		description: "High availability policy and topology overlay; never a standalone kit."
	}
	supportedKits: [for profile in ArchitectureV2AuthorityProfiles {profile.slug}]
	provides: ["availability-ha"]
	requires: [{id: "topology-core"}]
	availability: {
		policyAuthority: "kit-definition"
		supportedModes: ["warm-standby", "quorum"]
		selector: "control-plane-members"
	}
}]

_architectureV2PrivilegedInterfaceApprovals: list.Concat([[
	{
		id:            "approve-socket-proxy-backing"
		kind:          "docker-socket-direct-v1"
		moduleRef:     "socket-proxy"
		unitRef:       "compose"
		providerRef:   "stackkits-basement-compose"
		daemonRef:     "docker-default"
		policyProfile: "docker-provider-backing"
		reasonCode:    "provider-backing"
		evidenceRef:   "socket-proxy-provider-backing-governance"
	},
], _architectureV2ProfileExtensionPrivilegedInterfaceApprovals])

_architectureV2RILActionPrimitives: [
	{
		id:      "plan-drift-repair", version: "1.0.0", title:     "Plan a governed drift repair", category: "plan"
		support: "contract-only", mutation:    false, destructive: false, risk:                              "read-only"
		owner: {authority: "stackkits", operationClass: "plan-inspection"}
		approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "owner-step-up", receiptRequired: true}
		grant: {required: true, audience: "stackkits", scopes: ["stackkit-plan"], connectorBindingRequired: true}
		target: {scope: "stack", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: false, requiresRuntimeInstanceRef: false}
		inputs: []
		verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["readback"]}
		recovery: {kind: "none", requiredOnFailure: false}
	},
	{
		id:      "apply-stackkit-change", version: "1.0.0", title:    "Apply an approved StackKit change", category: "apply"
		support: "contract-only", mutation:        true, destructive: false, risk:                                   "high"
		owner: {authority: "stackkits", operationClass: "product-apply"}
		approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "owner-step-up", receiptRequired: true}
		grant: {required: true, audience: "stackkits", scopes: ["stackkit-apply"], connectorBindingRequired: true}
		target: {scope: "stack", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: false, requiresRuntimeInstanceRef: false}
		inputs: [{id: "approved-change-ref", type: "opaque-reference", required: true, source: "approved-action-card", opaqueReferenceOnly: true, inlineMaterial: false}]
		verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["preflight", "post-action", "readback"]}
		recovery: {kind: "primitive", requiredOnFailure: true, primitiveRef: "rollback-stackkit-change"}
	},
	{
		id:      "verify-stackkit-state", version: "1.0.0", title:                                   "Verify governed StackKit state", category: "verify"
		support: "executor-bound", executorRef:    "stackkits-governed-state-verifier-v1", mutation: false, destructive:                         false, risk: "read-only"
		owner: {authority: "stackkits", operationClass: "product-verify"}
		approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "owner-step-up", receiptRequired: true}
		grant: {required: true, audience: "stackkits", scopes: ["stackkit-verify"], connectorBindingRequired: true}
		target: {scope: "stack", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: false, requiresRuntimeInstanceRef: false}
		inputs: []
		verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["readback"]}
		recovery: {kind: "none", requiredOnFailure: false}
	},
	{
		id:      "rollback-stackkit-change", version: "1.0.0", title:    "Roll back an approved StackKit change", category: "rollback"
		support: "contract-only", mutation:           true, destructive: true, risk:                                        "critical"
		owner: {authority: "stackkits", operationClass: "product-rollback"}
		approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "break-glass", receiptRequired: true}
		grant: {required: true, audience: "stackkits", scopes: ["stackkit-rollback"], connectorBindingRequired: true}
		target: {scope: "stack", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: false, requiresRuntimeInstanceRef: false}
		inputs: [{id: "checkpoint-ref", type: "opaque-reference", required: true, source: "approved-action-card", opaqueReferenceOnly: true, inlineMaterial: false}]
		verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["preflight", "post-action", "readback"]}
		recovery: {kind: "manual", requiredOnFailure: true}
	},
	{
		id:      "restart-service", version: "1.0.0", title:    "Restart one governed runtime service", category: "service"
		support: "contract-only", mutation:  true, destructive: false, risk:                                      "high"
		owner: {authority: "stackkits", operationClass: "runtime-service-action"}
		approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "owner-step-up", receiptRequired: true}
		grant: {required: true, audience: "stackkits", scopes: ["stackkit-service-restart"], connectorBindingRequired: true}
		target: {scope: "runtime-instance", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: true, requiresRuntimeInstanceRef: true}
		inputs: [{id: "runtime-instance-ref", type: "opaque-reference", required: true, source: "approved-action-card", opaqueReferenceOnly: true, inlineMaterial: false}]
		verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["preflight", "post-action", "readback"]}
		recovery: {kind: "manual", requiredOnFailure: true}
	},
	{
		id:      "rotate-certificate", version: "1.0.0", title:    "Rotate one governed certificate binding", category: "certificate"
		support: "contract-only", mutation:     true, destructive: false, risk:                                         "high"
		owner: {authority: "stackkits", operationClass: "certificate-action"}
		approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "owner-step-up", receiptRequired: true}
		grant: {required: true, audience: "stackkits", scopes: ["stackkit-certificate-rotate"], connectorBindingRequired: true}
		target: {scope: "module-instance", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: true, requiresRuntimeInstanceRef: false}
		inputs: [{id: "certificate-binding-ref", type: "opaque-reference", required: true, source: "approved-action-card", opaqueReferenceOnly: true, inlineMaterial: false}]
		verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["preflight", "post-action", "readback"]}
		recovery: {kind: "manual", requiredOnFailure: true}
	},
	{
		id:      "check-backup", version:   "1.0.0", title:     "Check governed backup and restore evidence", category: "backup"
		support: "contract-only", mutation: false, destructive: false, risk:                                            "read-only"
		owner: {authority: "stackkits", operationClass: "backup-evidence"}
		approval: {required: true, authority: "techstack", policyAuthority: "gateway", class: "owner-step-up", receiptRequired: true}
		grant: {required: true, audience: "stackkits", scopes: ["stackkit-backup-check"], connectorBindingRequired: true}
		target: {scope: "module-instance", requiresStackID: true, requiresResolvedPlanHash: true, requiresNodeRef: false, requiresRuntimeInstanceRef: false}
		inputs: [{id: "backup-contract-ref", type: "opaque-reference", required: true, source: "approved-action-card", opaqueReferenceOnly: true, inlineMaterial: false}]
		verification: {required: true, evidenceSchema: "stackkit.ril-action-evidence/v1", phases: ["readback"]}
		recovery: {kind: "none", requiredOnFailure: false}
	},
]

_architectureV2RILActionExecutors: [{
	schemaVersion: "stackkit.ril-action-executor/v1"
	ref:           "stackkits-governed-state-verifier-v1"
	version:       "1.0.0"
	owner: authority: "stackkits"
	operationClasses: ["product-verify"]
	prohibitions: {
		providerLifecycle:    true
		providerInputs:       true
		leaseAuthority:       true
		credentialResolution: true
		transport:            true
		callerCommands:       true
		arbitraryPaths:       true
	}
}]

ArchitectureV2Catalog: #ArchitectureV2CatalogContract & {
	capabilities: [for contract in _architectureV2Capabilities {#CapabilityContract & contract}]
	providers: [for contract in _architectureV2Providers {#CapabilityProvider & contract}]
	addons: [for contract in _architectureV2AddOns {#AddOnContract & contract}]
	modules: [for contract in _architectureV2Modules {#ModuleContractV2 & contract}]
	workloads:             _architectureV2WorkloadContracts
	applicationLifecycles: _architectureV2ApplicationLifecycleContracts
	privilegedInterfaceApprovals: [for contract in _architectureV2PrivilegedInterfaceApprovals {#PrivilegedInterfaceApprovalV2 & contract}]
	rilActionExecutors: [for contract in _architectureV2RILActionExecutors {#RILActionExecutorContractV1 & contract}]
	rilActionPrimitives: list.Concat([
		[for contract in _architectureV2RILActionPrimitives {
			#RILActionPrimitiveContractV1 & contract & {extensionAuthority?: _|_}
		}],
		[for module in modules for contract in module.rilActionPrimitives {contract}],
	])
	planArtifacts: _architectureV2PlanArtifacts

	_capabilityIDsUnique: list.UniqueItems([for contract in capabilities {contract.metadata.id}]) & true
	_providerIDsUnique: list.UniqueItems([for contract in providers {contract.metadata.id}]) & true
	_addOnIDsUnique: list.UniqueItems([for contract in addons {contract.metadata.id}]) & true
}
