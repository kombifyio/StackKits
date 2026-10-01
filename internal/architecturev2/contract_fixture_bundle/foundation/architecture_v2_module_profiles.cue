package foundation

// Native module profiles declare the StackKits hosting envelope. Core host
// floors come from the existing kit contracts and are reused by application
// profiles as an explicit Kombify support policy, not upstream measurements.
// Application RAM reservations are the sum of the pinned runtime component
// reservations expressed exactly in GiB. A reservation is not a
// host minimum or a recommendation. These absolute host floors aggregate by
// maximum; application data capacity is separately governed by DataBinding.
// Equal high/standard profiles do not imply a measured performance benefit.
//
// Every declared resource block carries #ResourceProvenanceV1. Floors, core
// recommendations and GPU envelopes are labelled policy, except the Immich
// CPU/RAM floor, which cites the upstream requirement. Application
// reservations and recommendations marked measured are bound from Depot
// lifecycle receipts (docs/data/resource-evidence, rule in
// scripts/compat/bind-resource-evidence.mjs). Container footprints do not
// depend on the guest size, but each profile binds the evidence
// of the cell that ran it; high reuses the standard evidence.
_architectureV2PolicyFloorProvenance: #ResourceProvenanceV1 & {source: "policy", ref: "Kombify host floor support policy"}
_architectureV2PolicyRecommendedProvenance: #ResourceProvenanceV1 & {source: "policy", ref: "Kombify recommended sizing policy"}
_architectureV2ComponentReservationProvenance: #ResourceProvenanceV1 & {source: "policy", ref: "Sum of pinned Compose component memoryReservation limits in foundation/architecture_v2_catalog.cue"}
_architectureV2PolicyReservationProvenance: #ResourceProvenanceV1 & {source: "policy", ref: "Kombify reservation policy; declared, not a sum of component limits"}
_architectureV2AcceleratorEnvelopeProvenance: #ResourceProvenanceV1 & {source: "policy", ref: "Kombify GPU runtime support envelope"}
_architectureV2ImmichUpstreamFloorProvenance: #ResourceProvenanceV1 & {source: "upstream", ref: "Immich v2.7.0 docs/install/requirements.md (CPU/RAM); storage is the Kombify platform floor"}

_architectureV2CoreComputeProfile: #ModuleComputeProfileV2 & {
	maturity:           "supported", executable: true, realization: "apply-ready"
	platformManagement: "selected-provider"
	hostFloor: {minCpuCores: 2, minRamGB: 4, minStorageGB: 20}
	recommended: {cpuCores: 4, ramGB: 4, storageGB: 20}
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		recommended: _architectureV2PolicyRecommendedProvenance
	}
}

// Application profiles that reuse a core floor reuse its provenance with it.
_architectureV2CoreHostFloor: {
	hostFloor: _architectureV2CoreComputeProfile.hostFloor
	provenance: hostFloor: _architectureV2CoreComputeProfile.provenance.hostFloor
}

_architectureV2CloudCoreComputeProfile: _architectureV2CoreComputeProfile & {
	description: "Cloud routing, owner identity, application management, the hub and the StackKits server with its MCP endpoint. Standard and high have the same declared components and resource envelope; high does not claim additional capacity or availability."
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "coolify", "coolify-postgres", "coolify-redis", "coolify-realtime", "hub", "stackkit-server"]
}
_architectureV2CloudCoreComputeProfiles: {
	standard: _architectureV2CloudCoreComputeProfile
	high:     _architectureV2CloudCoreComputeProfile
}

_architectureV2CloudStandaloneCoreComputeProfile: #ModuleComputeProfileV2 & {
	description:        "Cloud routing, owner identity, public edge, the hub and the StackKits server with its MCP endpoint using standalone Compose. Coolify and Komodo are omitted; public TLS, offsite backup and provider lifecycle remain separately owned contracts."
	maturity:           "supported", executable: true, realization: "apply-ready"
	platformManagement: "standalone"
	hostFloor:          _architectureV2CoreComputeProfile.hostFloor
	recommended:        _architectureV2CoreComputeProfile.recommended
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "hub", "stackkit-server", "kopia-agent"]
	degradations: ["paas-management-omitted"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		recommended: _architectureV2PolicyRecommendedProvenance
	}
}
_architectureV2CloudStandaloneCoreComputeProfiles: {
	standard: _architectureV2CloudStandaloneCoreComputeProfile
	high:     _architectureV2CloudStandaloneCoreComputeProfile
}

_architectureV2BasementCoreComputeProfile: _architectureV2CoreComputeProfile & {
	description: "Local routing, owner identity, internal certificates, the site resolver, application management, backup agent, the hub and the StackKits server with its MCP endpoint. Standard and high have the same declared components and resource envelope; high does not claim additional capacity or availability."
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "step-ca", "lan-dns", "coolify", "coolify-postgres", "coolify-redis", "coolify-realtime", "kopia-agent", "hub", "stackkit-server"]
}
_architectureV2BasementCoreComputeProfiles: {
	standard: _architectureV2BasementCoreComputeProfile
	high:     _architectureV2BasementCoreComputeProfile
}

_architectureV2BasementStandaloneCoreComputeProfile: #ModuleComputeProfileV2 & {
	description:        "Local routing, owner identity, internal certificates, the site resolver, backup agent, the hub and the StackKits server with its MCP endpoint using standalone Compose. PaaS management is omitted. Photos, Media and other applications keep their own explicitly selected profiles."
	maturity:           "supported", executable: true, realization: "apply-ready"
	platformManagement: "standalone"
	hostFloor: {minCpuCores: 2, minRamGB: 2, minStorageGB: 10}
	recommended: {cpuCores: 2, ramGB: 2, storageGB: 10}
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "step-ca", "lan-dns", "kopia-agent", "hub", "stackkit-server"]
	degradations: ["paas-management-omitted"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		recommended: _architectureV2PolicyRecommendedProvenance
	}
}

// All profiles retain the same complete standalone service graph. The module
// identity is preserved for existing Core Lite installations; resource profile
// names never turn a platform manager on or select a different application.
_architectureV2BasementCoreLiteComputeProfiles: {
	low:      _architectureV2BasementStandaloneCoreComputeProfile
	standard: _architectureV2BasementStandaloneCoreComputeProfile
	high:     _architectureV2BasementStandaloneCoreComputeProfile
}
_architectureV2CoreLiteHostFloor: {
	hostFloor: _architectureV2BasementCoreLiteComputeProfiles.low.hostFloor
	provenance: hostFloor: _architectureV2BasementCoreLiteComputeProfiles.low.provenance.hostFloor
}

_architectureV2ImmichStorageFilesystemRequirement: #StorageFilesystemRequirementV2 & {
	sourceRef:     "system.container.dataRoot"
	requiredClass: "local-posix"
	allowedFilesystemTypes: ["ext2", "ext3", "ext4", "xfs", "btrfs", "zfs"]
	requireOwnership: true
}

_architectureV2ImmichComputeProfile: #ModuleComputeProfileV2 & {
	description: "Photo library and mobile backup with the machine-learning service, PostgreSQL and Valkey. Standard and high have the same declared resources and features; neither guarantees a user count or ingest rate. Photo-library growth needs a separate data budget."
	maturity:    "supported", executable: true, realization: "apply-ready"
	// CPU/RAM: Immich v2.7.0 docs/install/requirements.md. Disk is the
	// Kombify platform floor; it is not space reserved for the photo library.
	hostFloor: {
		minCpuCores:                    2
		minRamGB:                       6
		minStorageGB:                   _architectureV2CoreComputeProfile.hostFloor.minStorageGB
		minAMD64MicroarchitectureLevel: 2
		storageFilesystem:              _architectureV2ImmichStorageFilesystemRequirement
	}
	// Measured idle p95 after apply and after the photo seed; recommended
	// covers the observed peak during the seed with 25% margin.
	reservation: {ramGB: 2.375, cpuCores: 0.1}
	recommended: {ramGB: 3.625, cpuCores: 1}
	components: ["immich-server", "immich-machine-learning", "immich-postgres", "immich-postgres-init", "immich-valkey"]
	provenance: {
		hostFloor: _architectureV2ImmichUpstreamFloorProvenance
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-immich-runtime/standard.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-immich-runtime/standard.json"}
	}
}
_architectureV2ImmichComputeProfiles: {
	standard: _architectureV2ImmichComputeProfile
	high:     _architectureV2ImmichComputeProfile
}
_architectureV2ImmichLiteComputeProfiles: low: #ModuleComputeProfileV2 & {
	description: "Photo library and mobile backup with PostgreSQL and Valkey. The machine-learning service and ML search are omitted to reduce resident memory. Photo-library growth needs a separate data budget."
	maturity:    "supported", executable: true, realization: "apply-ready"
	// The upstream 4 GiB path requires the declared omission of ML.
	hostFloor: {
		minCpuCores:       2
		minRamGB:          4
		minStorageGB:      _architectureV2BasementCoreLiteComputeProfiles.low.hostFloor.minStorageGB
		storageFilesystem: _architectureV2ImmichStorageFilesystemRequirement
	}
	// Measured without the machine-learning worker, including the photo seed.
	reservation: {ramGB: 1.125, cpuCores: 0.05}
	recommended: {ramGB: 1.375, cpuCores: 0.25}
	components: ["immich-server", "immich-postgres", "immich-postgres-init", "immich-valkey"]
	degradations: ["machine-learning-disabled"]
	provenance: {
		hostFloor: _architectureV2PolicyFloorProvenance
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-immich-lite-runtime/low.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-immich-lite-runtime/low.json"}
	}
}

_architectureV2CloudreveComputeProfile: #ModuleComputeProfileV2 & {
	description: "File storage and sharing through Cloudreve. All profiles run the same application; low declares a smaller host floor and binds its own measured footprint. Standard and high are equivalent. Nextcloud, collaboration and OCR are not added by selecting high. File growth needs a separate data budget."
	maturity:    "supported", executable: true, realization: "apply-ready"
	components: ["cloudreve"]
}
_architectureV2CloudreveLowMeasured: {
	reservation: {ramGB: 0.1875, cpuCores: 0.05}
	recommended: {ramGB: 0.25, cpuCores: 0.25}
	provenance: {
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-cloudreve-runtime/low.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-cloudreve-runtime/low.json"}
	}
}
_architectureV2CloudreveStandardMeasured: {
	reservation: {ramGB: 0.125, cpuCores: 0.05}
	recommended: {ramGB: 0.25, cpuCores: 0.25}
	provenance: {
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-cloudreve-runtime/standard.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-cloudreve-runtime/standard.json"}
	}
}
_architectureV2CloudreveComputeProfiles: {
	low:      _architectureV2CloudreveComputeProfile & _architectureV2CoreLiteHostFloor & _architectureV2CloudreveLowMeasured
	standard: _architectureV2CloudreveComputeProfile & _architectureV2CoreHostFloor & _architectureV2CloudreveStandardMeasured
	high:     _architectureV2CloudreveComputeProfile & _architectureV2CoreHostFloor & _architectureV2CloudreveStandardMeasured
}

_architectureV2NextcloudComputeProfile: #ModuleComputeProfileV2 & {
	description: "Files, sharing and collaboration through Nextcloud Server with PostgreSQL and Valkey. Office editing is a separate add-on. Standard and high are equivalent; storage growth needs a separate data budget."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.75
	components: ["nextcloud", "nextcloud-postgres", "nextcloud-valkey"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2PolicyReservationProvenance
	}
}
_architectureV2NextcloudComputeProfiles: {standard: _architectureV2NextcloudComputeProfile, high: _architectureV2NextcloudComputeProfile}

_architectureV2VaultwardenComputeProfile: #ModuleComputeProfileV2 & {
	description: "Password vault and secure notes through Vaultwarden. All profiles run the same application; low declares a smaller host floor and binds its own measured footprint. Standard and high are equivalent. The owner creates the encrypted account and retains the master password."
	maturity:    "supported", executable: true, realization: "apply-ready"
	components: ["vaultwarden"]
}
_architectureV2VaultwardenLowMeasured: {
	reservation: {ramGB: 0.0625, cpuCores: 0.05}
	recommended: {ramGB: 0.125, cpuCores: 0.25}
	provenance: {
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-vaultwarden-runtime/low.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-vaultwarden-runtime/low.json"}
	}
}
_architectureV2VaultwardenStandardMeasured: {
	reservation: {ramGB: 0.0625, cpuCores: 0.05}
	recommended: {ramGB: 0.125, cpuCores: 0.25}
	provenance: {
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-vaultwarden-runtime/standard.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-vaultwarden-runtime/standard.json"}
	}
}
_architectureV2VaultwardenComputeProfiles: {
	low:      _architectureV2VaultwardenComputeProfile & _architectureV2CoreLiteHostFloor & _architectureV2VaultwardenLowMeasured
	standard: _architectureV2VaultwardenComputeProfile & _architectureV2CoreHostFloor & _architectureV2VaultwardenStandardMeasured
	high:     _architectureV2VaultwardenComputeProfile & _architectureV2CoreHostFloor & _architectureV2VaultwardenStandardMeasured
}

_architectureV2PassboltComputeProfile: #ModuleComputeProfileV2 & {
	description: "Team password manager through Passbolt Community Edition with MariaDB. The owner registers with the Passbolt browser extension, which creates the owner's OpenPGP key on the device. Standard and high are equivalent."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.5
	components: ["passbolt", "passbolt-mariadb"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2PassboltComputeProfiles: {standard: _architectureV2PassboltComputeProfile, high: _architectureV2PassboltComputeProfile}

_architectureV2JellyfinComputeProfile: #ModuleComputeProfileV2 & {
	description: "Media library and playback through Jellyfin. Standard and high have the same declared application and resources; no transcoding concurrency or GPU acceleration is promised. The owner supplies the media library and its storage."
	maturity:    "supported", executable: true, realization: "apply-ready"
	// A hosting baseline, not a guarantee of any transcoding concurrency.
	hostFloor: _architectureV2CoreComputeProfile.hostFloor
	reservation: {ramGB: 0.3125, cpuCores: 0.05}
	recommended: {ramGB: 0.375, cpuCores: 0.25}
	components: ["jellyfin"]
	provenance: {
		hostFloor: _architectureV2PolicyFloorProvenance
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-jellyfin-runtime/standard.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-jellyfin-runtime/standard.json"}
	}
}
_architectureV2JellyfinComputeProfiles: {
	standard: _architectureV2JellyfinComputeProfile
	high:     _architectureV2JellyfinComputeProfile
}

_architectureV2ImmichPowerToolsComputeProfile: #ModuleComputeProfileV2 & {
	description: "Library maintenance through Immich Power Tools behind the gateway login. It reaches Immich and its database on the node's internal photos network with an Immich-issued API key and the photos database credential."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.125
	components: ["immich-power-tools"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2ImmichPowerToolsComputeProfiles: {standard: _architectureV2ImmichPowerToolsComputeProfile, high: _architectureV2ImmichPowerToolsComputeProfile}

_architectureV2ImmichKioskComputeProfile: #ModuleComputeProfileV2 & {
	description: "Photo-frame slideshow through Immich Kiosk behind the gateway login. It reaches Immich on the node's internal photos network with an API key the Immich owner issues through stackkit setup."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.0625
	components: ["immich-kiosk"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2ImmichKioskComputeProfiles: {standard: _architectureV2ImmichKioskComputeProfile, high: _architectureV2ImmichKioskComputeProfile}

_architectureV2ImmichPublicProxyComputeProfile: #ModuleComputeProfileV2 & {
	description: "Public share links through Immich Public Proxy. It reaches Immich on the node's internal photos network, holds no Immich credentials and exposes no Immich login or API; the route is public by design."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.0625
	components: ["immich-public-proxy"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2ImmichPublicProxyComputeProfiles: {standard: _architectureV2ImmichPublicProxyComputeProfile, high: _architectureV2ImmichPublicProxyComputeProfile}

_architectureV2Zigbee2mqttComputeProfile: #ModuleComputeProfileV2 & {
	description: "Zigbee bridge through Zigbee2MQTT. The owner chooses the Zigbee USB adapter and the MQTT broker address; the frontend stays behind the gateway login."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.125
	components: ["zigbee2mqtt"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2Zigbee2mqttComputeProfiles: {standard: _architectureV2Zigbee2mqttComputeProfile, high: _architectureV2Zigbee2mqttComputeProfile}

_architectureV2MosquittoComputeProfile: #ModuleComputeProfileV2 & {
	description: "MQTT broker through Eclipse Mosquitto with password authentication. The MQTT listener on port 1883 is published on the LAN only when the owner enables it; the broker statistics API stays behind the gateway login."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.03125
	components: ["mosquitto"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2MosquittoComputeProfiles: {standard: _architectureV2MosquittoComputeProfile, high: _architectureV2MosquittoComputeProfile}

_architectureV2EuroofficeComputeProfile: #ModuleComputeProfileV2 & {
	description: "Browser office editing through Euro-Office Document Server (ONLYOFFICE fork). Every request carries a JWT signed with the custodied secret that the Nextcloud connector app also uses; the server has no user login of its own."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 2
	components: ["euro-office"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2EuroofficeComputeProfiles: {standard: _architectureV2EuroofficeComputeProfile, high: _architectureV2EuroofficeComputeProfile}

_architectureV2ESPHomeComputeProfile: #ModuleComputeProfileV2 & {
	description: "ESPHome dashboard for building and updating ESP32/ESP8266 firmware over the network. Firmware builds are CPU-bound bursts; the dashboard itself has no user store and stays behind the gateway login."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.25
	components: ["esphome"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2ESPHomeComputeProfiles: {standard: _architectureV2ESPHomeComputeProfile, high: _architectureV2ESPHomeComputeProfile}

_architectureV2AudiobookshelfComputeProfile: #ModuleComputeProfileV2 & {
	description: "Audiobooks and podcasts through Audiobookshelf with progress sync. The owner supplies the library; the administrator is created on first visit behind the gateway login, and Pocket ID OIDC can be enabled in the app."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.125
	components: ["audiobookshelf"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2AudiobookshelfComputeProfiles: {standard: _architectureV2AudiobookshelfComputeProfile, high: _architectureV2AudiobookshelfComputeProfile}

_architectureV2NavidromeComputeProfile: #ModuleComputeProfileV2 & {
	description: "Music streaming through Navidrome for Subsonic-compatible apps. The owner supplies the music library; the administrator is created on first visit behind the gateway login."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.125
	components: ["navidrome"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2NavidromeComputeProfiles: {standard: _architectureV2NavidromeComputeProfile, high: _architectureV2NavidromeComputeProfile}

_architectureV2EmbyComputeProfile: #ModuleComputeProfileV2 & {
	description: "Media library and playback through Emby Server. Some client features need an Emby Premiere licence; no transcoding concurrency or GPU acceleration is promised. The owner supplies the media library and its storage."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.5
	components: ["emby"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2EmbyComputeProfiles: {
	standard: _architectureV2EmbyComputeProfile
	high:     _architectureV2EmbyComputeProfile
}

_architectureV2HomeAssistantComputeProfile: #ModuleComputeProfileV2 & {
	description: "Home Assistant Container for automation and its native product interfaces. All profiles run the same application; low declares a smaller host floor and binds its own measured footprint. Standard and high are equivalent. Home Assistant OS, Supervisor, MQTT and radio-device provisioning are not included."
	maturity:    "supported", executable: true, realization: "apply-ready"
	components: ["home-assistant"]
}
_architectureV2HomeAssistantLowMeasured: {
	reservation: {ramGB: 0.4375, cpuCores: 0.05}
	recommended: {ramGB: 0.625, cpuCores: 0.25}
	provenance: {
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-home-assistant-runtime/low.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-home-assistant-runtime/low.json"}
	}
}
_architectureV2HomeAssistantStandardMeasured: {
	reservation: {ramGB: 0.4375, cpuCores: 0.05}
	recommended: {ramGB: 0.625, cpuCores: 0.25}
	provenance: {
		reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-home-assistant-runtime/standard.json"}
		recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-home-assistant-runtime/standard.json"}
	}
}
_architectureV2HomeAssistantComputeProfiles: {
	low:      _architectureV2HomeAssistantComputeProfile & _architectureV2CoreLiteHostFloor & _architectureV2HomeAssistantLowMeasured
	standard: _architectureV2HomeAssistantComputeProfile & _architectureV2CoreHostFloor & _architectureV2HomeAssistantStandardMeasured
	high:     _architectureV2HomeAssistantComputeProfile & _architectureV2CoreHostFloor & _architectureV2HomeAssistantStandardMeasured
}

_architectureV2PrivateAIComputeProfiles: {
	standard: #ModuleComputeProfileV2 & {
		description: "CPU inference for an explicitly selected small model. No model is downloaded at install. Model size/context determine additional RAM and disk; GPU acceleration is not enabled by this profile."
		maturity:    "beta", executable: true, realization: "apply-ready"
		hostFloor: {minCpuCores: 4, minRamGB: 12, minStorageGB: 40}
		recommended: {cpuCores: 8, ramGB: 16, storageGB: 80}
		// The optional kombify AI connector declares a memory limit only; it
		// adds no reservation while it is off.
		reservation: ramGB: 1.5
		components: ["open-webui", "ollama", "kombify-ai-connector"]
		provenance: {
			hostFloor:   _architectureV2PolicyFloorProvenance
			recommended: _architectureV2PolicyRecommendedProvenance
			reservation: _architectureV2ComponentReservationProvenance
		}
	}
	high: standard
}

// AnythingLLM chat alternative of the ai workload: the same Ollama runtime
// with AnythingLLM instead of Open WebUI; the same host floor applies.
_architectureV2AnythingLLMComputeProfiles: {
	standard: #ModuleComputeProfileV2 & {
		description: "CPU inference for an explicitly selected small model with AnythingLLM as the chat module. No model is downloaded at install. Model size/context determine additional RAM and disk; GPU acceleration is not enabled by this profile."
		maturity:    "beta", executable: true, realization: "apply-ready"
		hostFloor: {minCpuCores: 4, minRamGB: 12, minStorageGB: 40}
		recommended: {cpuCores: 8, ramGB: 16, storageGB: 80}
		reservation: ramGB: 1.5
		components: ["anythingllm", "ollama"]
		provenance: {
			hostFloor:   _architectureV2PolicyFloorProvenance
			recommended: _architectureV2PolicyRecommendedProvenance
			reservation: _architectureV2ComponentReservationProvenance
		}
	}
	high: standard
}

// GPU inference for Ollama (docs/use-case-expansion/ai-agents.md, item 2).
// Selecting no accelerator profile keeps the CPU runtime above unchanged. The
// reservation is the declared support envelope for the GPU runtime libraries
// on the host (and, for ROCm, the larger image), not a measurement. Model
// weights still need their own RAM/VRAM and disk.
_architectureV2PrivateAIAcceleratorProfiles: {
	nvidia: #ModuleAxisProfileV2 & {
		description: "Ollama inference on NVIDIA GPUs through the Container Device Interface (all GPUs of the node). Needs the NVIDIA driver 550 or newer, the NVIDIA Container Toolkit and a generated CDI spec; StackKits never installs GPU drivers. Open WebUI stays on the CPU."
		maturity:    "experimental"
		realization: "apply-ready"
		reservation: {cpuCores: 1, ramGB: 1, storageGB: 1}
		components: ["ollama"]
		accelerator: {vendor: "nvidia", access: "cdi", minDriverMajor: 550}
		provenance: reservation: _architectureV2AcceleratorEnvelopeProvenance
	}
	amd: #ModuleAxisProfileV2 & {
		description: "Ollama inference on AMD GPUs with ROCm through /dev/kfd and /dev/dri, using the Ollama ROCm image (amd64 only). Needs a ROCm-capable card with the amdgpu kernel driver. Open WebUI stays on the CPU."
		maturity:    "experimental"
		realization: "apply-ready"
		reservation: {cpuCores: 1, ramGB: 1, storageGB: 6}
		components: ["ollama"]
		accelerator: {
			vendor: "amd", access: "rocm-device-nodes"
			images: ollama: {ref: "docker.io/ollama/ollama:0.34.0-rocm", digest: "sha256:36a99c0aaa4d28d0fc84969124bcf2f3d1ba8ab89181e2bd33bbc12f6ac2c4ce"}
		}
		provenance: reservation: _architectureV2AcceleratorEnvelopeProvenance
	}
}

// The same GPU profiles for the AnythingLLM alternative; only Ollama
// receives the device.
_architectureV2AnythingLLMAcceleratorProfiles: {
	nvidia: #ModuleAxisProfileV2 & {
		description: "Ollama inference on NVIDIA GPUs through the Container Device Interface (all GPUs of the node). Needs the NVIDIA driver 550 or newer, the NVIDIA Container Toolkit and a generated CDI spec; StackKits never installs GPU drivers. AnythingLLM stays on the CPU."
		maturity:    "experimental"
		realization: "apply-ready"
		reservation: {cpuCores: 1, ramGB: 1, storageGB: 1}
		components: ["ollama"]
		accelerator: {vendor: "nvidia", access: "cdi", minDriverMajor: 550}
		provenance: reservation: _architectureV2AcceleratorEnvelopeProvenance
	}
	amd: #ModuleAxisProfileV2 & {
		description: "Ollama inference on AMD GPUs with ROCm through /dev/kfd and /dev/dri, using the Ollama ROCm image (amd64 only). Needs a ROCm-capable card with the amdgpu kernel driver. AnythingLLM stays on the CPU."
		maturity:    "experimental"
		realization: "apply-ready"
		reservation: {cpuCores: 1, ramGB: 1, storageGB: 6}
		components: ["ollama"]
		accelerator: {
			vendor: "amd", access: "rocm-device-nodes"
			images: ollama: {ref: "docker.io/ollama/ollama:0.34.0-rocm", digest: "sha256:36a99c0aaa4d28d0fc84969124bcf2f3d1ba8ab89181e2bd33bbc12f6ac2c4ce"}
		}
		provenance: reservation: _architectureV2AcceleratorEnvelopeProvenance
	}
}

// Private AI add-ons. Each serves Open WebUI only and holds no owner data.
_architectureV2SearxngComputeProfile: #ModuleComputeProfileV2 & {
	description: "Private SearXNG meta search for Open WebUI web search. It queries public search engines on the owner's behalf and keeps no search history; there is no route of its own."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.125
	components: ["searxng"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2SearxngComputeProfiles: {standard: _architectureV2SearxngComputeProfile, high: _architectureV2SearxngComputeProfile}

_architectureV2TikaComputeProfile: #ModuleComputeProfileV2 & {
	description: "Apache Tika text extraction for Open WebUI documents. The minimal image has no OCR, so scanned pages yield no text; Docling is the alternative for them."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.5
	components: ["tika"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2TikaComputeProfiles: {standard: _architectureV2TikaComputeProfile, high: _architectureV2TikaComputeProfile}

_architectureV2DoclingComputeProfile: #ModuleComputeProfileV2 & {
	description: "Docling document conversion with layout, table and OCR models on the CPU for Open WebUI documents. Large scanned documents take minutes on a CPU; GPU acceleration is not enabled by this profile."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor: {minCpuCores: 4, minRamGB: 8, minStorageGB: 20}
	reservation: ramGB: 1
	components: ["docling"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2DoclingComputeProfiles: {standard: _architectureV2DoclingComputeProfile, high: _architectureV2DoclingComputeProfile}

// Private AI image and video (ComfyUI), a core AI component (owner decision
// 2026-09-27). Without an accelerator profile it runs on the CPU: upscaling
// works on the floor below, text-to-image needs about 32 GB of RAM because
// the FLUX.1 schnell checkpoint (17 GB) is held in memory, and video stays
// GPU-only. The model presets enforce these limits at download time.
_architectureV2ComfyUIComputeProfile: #ModuleComputeProfileV2 & {
	description: "ComfyUI image and video generation with owner-downloaded models, on the CPU (slow) or on a selected GPU. Text-to-image on the CPU needs about 32 GB of RAM; video needs a GPU with 16 GB of VRAM. No model is downloaded at install."
	maturity:    "experimental", executable: true, realization: "apply-ready"
	hostFloor: {minCpuCores: 4, minRamGB: 8, minStorageGB: 40}
	recommended: {cpuCores: 8, ramGB: 32, storageGB: 150}
	reservation: ramGB: 2
	components: ["comfyui"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		recommended: _architectureV2PolicyRecommendedProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2ComfyUIComputeProfiles: {standard: _architectureV2ComfyUIComputeProfile, high: _architectureV2ComfyUIComputeProfile}

// Private AI agent harness (OpenHands). Inference happens in Ollama; this
// container holds the agent server, the automation backend, the editor and
// whatever the agent builds in its workspace. The gVisor runtime adds memory
// for its sentry, and repositories and package installs need their own disk.
_architectureV2OpenHandsComputeProfile: #ModuleComputeProfileV2 & {
	description: "OpenHands agent harness in one gVisor-isolated container with its own workspace: agent server, automations and editor. Models run in Ollama; the workspace grows with the owner's projects."
	maturity:    "experimental", executable: true, realization: "apply-ready"
	hostFloor: {minCpuCores: 4, minRamGB: 8, minStorageGB: 40}
	recommended: {cpuCores: 8, ramGB: 16, storageGB: 100}
	reservation: ramGB: 1
	components: ["openhands"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		recommended: _architectureV2PolicyRecommendedProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2OpenHandsComputeProfiles: {standard: _architectureV2OpenHandsComputeProfile, high: _architectureV2OpenHandsComputeProfile}

// Private AI agent control plane (Paperclip). Inference happens in Ollama or
// the installed Hermes; this bundle holds the Node.js server with the UI, its
// PostgreSQL and the agent CLIs Paperclip starts as child processes. The
// gVisor runtime adds memory for its sentry.
_architectureV2PaperclipComputeProfile: #ModuleComputeProfileV2 & {
	description: "Paperclip agent control plane in one gVisor-isolated bundle: Node.js server and UI, PostgreSQL and the agent processes it starts. Models run in Ollama or the installed Hermes."
	maturity:    "experimental", executable: true, realization: "apply-ready"
	hostFloor: {minCpuCores: 4, minRamGB: 8, minStorageGB: 20}
	recommended: {cpuCores: 8, ramGB: 16, storageGB: 60}
	reservation: ramGB: 1
	components: ["paperclip", "paperclip-postgres"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		recommended: _architectureV2PolicyRecommendedProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2PaperclipComputeProfiles: {standard: _architectureV2PaperclipComputeProfile, high: _architectureV2PaperclipComputeProfile}

// The image templates need 8 GiB of VRAM; the Wan 2.2 video template needs
// 16 GiB, which the model download refuses to fetch on a smaller GPU.
_architectureV2ComfyUIAcceleratorProfiles: {
	nvidia: #ModuleAxisProfileV2 & {
		description: "ComfyUI on NVIDIA GPUs through the Container Device Interface (all GPUs of the node), with at least 8 GiB of VRAM (Turing or newer). Needs the NVIDIA driver 580 or newer for the CUDA 13.0 image, the NVIDIA Container Toolkit and a generated CDI spec; StackKits never installs GPU drivers."
		maturity:    "experimental"
		realization: "apply-ready"
		reservation: {cpuCores: 1, ramGB: 2, storageGB: 1}
		components: ["comfyui"]
		accelerator: {vendor: "nvidia", access: "cdi", minDriverMajor: 580, minVramGiB: 8}
		provenance: reservation: _architectureV2AcceleratorEnvelopeProvenance
	}
}

// Private AI personal assistant (Hermes Agent). The agent itself is light:
// the model runs in the node's Ollama, whose profile carries the model
// memory. The image bundles Python, Node and a headless browser runtime.
_architectureV2HermesComputeProfile: #ModuleComputeProfileV2 & {
	description: "Hermes Agent personal assistant (gateway, scheduler and dashboard) on the node's local model. The model's memory belongs to the inference module; the assistant needs about 512 MB and up to 3 GB with its dashboard chat."
	maturity:    "experimental", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.5
	components: ["hermes"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2HermesComputeProfiles: {standard: _architectureV2HermesComputeProfile, high: _architectureV2HermesComputeProfile}

// Private AI speech (kombify SpeechKit with local providers). The server is
// light; the whisper.cpp small model (CPU) and the Kokoro-82M CPU runtime
// carry the memory. Assist uses the node's Ollama, whose profile holds the
// model memory.
_architectureV2SpeechKitComputeProfile: #ModuleComputeProfileV2 & {
	description: "kombify SpeechKit server with a whisper.cpp small-model sidecar (about 1 GB, up to 3 GB while transcribing) and a Kokoro-FastAPI CPU sidecar (about 512 MB, up to 2 GB). Assist runs on the inference module's Ollama."
	maturity:    "experimental", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 2
	components: ["speechkit", "speechkit-whisper", "speechkit-tts"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2PolicyReservationProvenance
	}
}
_architectureV2SpeechKitComputeProfiles: {standard: _architectureV2SpeechKitComputeProfile, high: _architectureV2SpeechKitComputeProfile}

_architectureV2GiteaComputeProfile: #ModuleComputeProfileV2 & {
	description: "Private Git hosting with SQLite and persistent repositories, LFS objects and configuration. CI runners and SSH are not included. Repository growth needs a separate data budget."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.25
	components: ["gitea"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2GiteaComputeProfiles: {standard: _architectureV2GiteaComputeProfile, high: _architectureV2GiteaComputeProfile}

_architectureV2ForgejoComputeProfile: #ModuleComputeProfileV2 & {
	description: "Private Git hosting through Forgejo with SQLite and persistent repositories, LFS objects and configuration. Actions runners and SSH are not included. Repository growth needs a separate data budget."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.25
	components: ["forgejo"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2ForgejoComputeProfiles: {standard: _architectureV2ForgejoComputeProfile, high: _architectureV2ForgejoComputeProfile}

_architectureV2PaperlessComputeProfile: #ModuleComputeProfileV2 & {
	description: "Document ingestion, OCR, indexing and search through Paperless-ngx with PostgreSQL and Valkey. StackKits configures and operates the upstream services; document features remain owned by Paperless-ngx. Document growth needs a separate data budget."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 1.25
	components: ["paperless", "paperless-postgres", "paperless-valkey"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2ComponentReservationProvenance
	}
}
_architectureV2PaperlessComputeProfiles: {standard: _architectureV2PaperlessComputeProfile, high: _architectureV2PaperlessComputeProfile}

_architectureV2PterodactylComputeProfile: #ModuleComputeProfileV2 & {
	description: "Pterodactyl Panel with MariaDB and Valkey plus the Wings node daemon. Game servers run in their own containers with the memory each curated profile declares; a Minecraft Java world typically needs 2 GB and Bedrock 1.5 GB on top of this platform reservation."
	maturity:    "beta", executable: true, realization: "apply-ready"
	hostFloor: {minCpuCores: 2, minRamGB: 4, minStorageGB: _architectureV2CoreComputeProfile.hostFloor.minStorageGB}
	reservation: ramGB: 1.25
	components: ["panel", "panel-database", "panel-cache", "panel-bootstrap", "wings"]
	provenance: {
		hostFloor:   _architectureV2PolicyFloorProvenance
		reservation: _architectureV2PolicyReservationProvenance
	}
}
_architectureV2PterodactylComputeProfiles: {standard: _architectureV2PterodactylComputeProfile, high: _architectureV2PterodactylComputeProfile}

_architectureV2RoundcubeComputeProfile: #ModuleComputeProfileV2 & {
	description: "Roundcube Webmail with SQLite as a client for an existing external IMAP/SMTP mailbox. All profiles retain the same application and memory reservation; low declares a smaller host floor. Standard and high are equivalent. No mail server, spam filter or DNS is included; mail storage stays with the owner's provider."
	maturity:    "beta", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.125 // 128 MiB component reservation.
	components: ["roundcube"]
	provenance: reservation: _architectureV2ComponentReservationProvenance
}
_architectureV2RoundcubeComputeProfiles: {
	low:      _architectureV2RoundcubeComputeProfile & _architectureV2CoreLiteHostFloor
	standard: _architectureV2RoundcubeComputeProfile & _architectureV2CoreHostFloor
	high:     _architectureV2RoundcubeComputeProfile & _architectureV2CoreHostFloor
}

// ADR-0046: Stalwart is light; the reservation covers RocksDB caches, the
// spam classifier and a handful of mailboxes.
_architectureV2StalwartComputeProfile: #ModuleComputeProfileV2 & {
	description: "Stalwart Mail Server with its embedded RocksDB store for one owner domain and a few mailboxes. All profiles retain the same application and memory reservation; low declares a smaller host floor. Standard and high are equivalent."
	maturity:    "experimental", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.5 // 512 MiB component reservation.
	components: ["stalwart"]
	provenance: reservation: _architectureV2ComponentReservationProvenance
}
_architectureV2StalwartComputeProfiles: {
	low:      _architectureV2StalwartComputeProfile & _architectureV2CoreLiteHostFloor
	standard: _architectureV2StalwartComputeProfile & _architectureV2CoreHostFloor
	high:     _architectureV2StalwartComputeProfile & _architectureV2CoreHostFloor
}
