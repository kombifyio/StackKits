package foundation

// Native module profiles declare the StackKits hosting envelope. Core host
// floors come from the existing kit contracts and are reused by application
// profiles as an explicit Kombify support policy, not upstream measurements.
// Application RAM reservations are the sum of the pinned runtime component
// reservations expressed exactly in GiB. A reservation is not a
// host minimum or a recommendation. These absolute host floors aggregate by
// maximum; application data capacity is separately governed by DataBinding.
// Equal high/standard profiles do not imply a measured performance benefit.
_architectureV2CoreComputeProfile: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	platformManagement: "selected-provider"
	hostFloor: {minCpuCores: 2, minRamGB: 4, minStorageGB: 20}
	recommended: {cpuCores: 4, ramGB: 4, storageGB: 20}
}

_architectureV2CloudCoreComputeProfile: _architectureV2CoreComputeProfile & {
	description: "Cloud routing, owner identity, application management and the hub. Standard and high have the same declared components and resource envelope; high does not claim additional capacity or availability."
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "coolify", "coolify-postgres", "coolify-redis", "coolify-realtime", "hub"]
}
_architectureV2CloudCoreComputeProfiles: {
	standard: _architectureV2CloudCoreComputeProfile
	high:     _architectureV2CloudCoreComputeProfile
}

_architectureV2BasementCoreComputeProfile: _architectureV2CoreComputeProfile & {
	description: "Local routing, owner identity, internal certificates, application management, backup agent and the hub. Standard and high have the same declared components and resource envelope; high does not claim additional capacity or availability."
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "step-ca", "coolify", "coolify-postgres", "coolify-redis", "coolify-realtime", "kopia-agent", "hub"]
}
_architectureV2BasementCoreComputeProfiles: {
	standard: _architectureV2BasementCoreComputeProfile
	high:     _architectureV2BasementCoreComputeProfile
}

_architectureV2BasementCoreLiteComputeProfiles: low: #ModuleComputeProfileV2 & {
	description: "Local routing, owner identity, internal certificates, backup agent and the hub using standalone Compose. PaaS management is omitted. Photos, Media and other applications keep their own explicitly selected profiles."
	maturity: "supported", executable: true, realization: "apply-ready"
	platformManagement: "standalone"
	hostFloor: {minCpuCores: 2, minRamGB: 2, minStorageGB: 10}
	recommended: {cpuCores: 2, ramGB: 2, storageGB: 10}
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "step-ca", "kopia-agent", "hub"]
	degradations: ["paas-management-omitted"]
}

_architectureV2ImmichStorageFilesystemRequirement: #StorageFilesystemRequirementV2 & {
	sourceRef:               "system.container.dataRoot"
	requiredClass:           "local-posix"
	allowedFilesystemTypes:  ["ext2", "ext3", "ext4", "xfs", "btrfs", "zfs"]
	requireOwnership:        true
}

_architectureV2ImmichComputeProfile: #ModuleComputeProfileV2 & {
	description: "Photo library and mobile backup with the machine-learning service, PostgreSQL and Valkey. Standard and high have the same declared resources and features; neither guarantees a user count or ingest rate. Photo-library growth needs a separate data budget."
	maturity: "supported", executable: true, realization: "apply-ready"
	// CPU/RAM: Immich v2.7.0 docs/install/requirements.md. Disk is the
	// Kombify platform floor; it is not space reserved for the photo library.
	hostFloor: {
		minCpuCores: 2
		minRamGB: 6
		minStorageGB: _architectureV2CoreComputeProfile.hostFloor.minStorageGB
		minAMD64MicroarchitectureLevel: 2
		storageFilesystem: _architectureV2ImmichStorageFilesystemRequirement
	}
	// 512 + 512 + 256 + 64 MiB = 1344 MiB; one-shot init has no reservation.
	reservation: ramGB: 1.3125
	components: ["immich-server", "immich-machine-learning", "immich-postgres", "immich-postgres-init", "immich-valkey"]
}
_architectureV2ImmichComputeProfiles: {
	standard: _architectureV2ImmichComputeProfile
	high:     _architectureV2ImmichComputeProfile
}
_architectureV2ImmichLiteComputeProfiles: low: #ModuleComputeProfileV2 & {
	description: "Photo library and mobile backup with PostgreSQL and Valkey. The machine-learning service and ML search are omitted to reduce resident memory. Photo-library growth needs a separate data budget."
	maturity: "supported", executable: true, realization: "apply-ready"
	// The upstream 4 GiB path requires the declared omission of ML.
	hostFloor: {
		minCpuCores: 2
		minRamGB: 4
		minStorageGB: _architectureV2BasementCoreLiteComputeProfiles.low.hostFloor.minStorageGB
		storageFilesystem: _architectureV2ImmichStorageFilesystemRequirement
	}
	// 512 + 256 + 64 MiB = 832 MiB; no machine-learning worker is selected.
	reservation: ramGB: 0.8125
	components: ["immich-server", "immich-postgres", "immich-postgres-init", "immich-valkey"]
	degradations: ["machine-learning-disabled"]
}

_architectureV2CloudreveComputeProfile: #ModuleComputeProfileV2 & {
	description: "File storage and sharing through Cloudreve. All profiles retain the same application and memory reservation; low declares a smaller host floor. Standard and high are equivalent. Nextcloud, collaboration and OCR are not added by selecting high. File growth needs a separate data budget."
	maturity: "supported", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.125 // Existing 128 MiB component reservation.
	components: ["cloudreve"]
}
_architectureV2CloudreveComputeProfiles: {
	low:      _architectureV2CloudreveComputeProfile & {hostFloor: _architectureV2BasementCoreLiteComputeProfiles.low.hostFloor}
	standard: _architectureV2CloudreveComputeProfile & {hostFloor: _architectureV2CoreComputeProfile.hostFloor}
	high:     _architectureV2CloudreveComputeProfile & {hostFloor: _architectureV2CoreComputeProfile.hostFloor}
}

_architectureV2VaultwardenComputeProfile: #ModuleComputeProfileV2 & {
	description: "Password vault and secure notes through Vaultwarden. All profiles retain the same application and memory reservation; low declares a smaller host floor. Standard and high are equivalent. The owner creates the encrypted account and retains the master password."
	maturity: "supported", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.0625 // Existing 64 MiB component reservation.
	components: ["vaultwarden"]
}
_architectureV2VaultwardenComputeProfiles: {
	low:      _architectureV2VaultwardenComputeProfile & {hostFloor: _architectureV2BasementCoreLiteComputeProfiles.low.hostFloor}
	standard: _architectureV2VaultwardenComputeProfile & {hostFloor: _architectureV2CoreComputeProfile.hostFloor}
	high:     _architectureV2VaultwardenComputeProfile & {hostFloor: _architectureV2CoreComputeProfile.hostFloor}
}

_architectureV2JellyfinComputeProfile: #ModuleComputeProfileV2 & {
	description: "Media library and playback through Jellyfin. Standard and high have the same declared application and resources; no transcoding concurrency or GPU acceleration is promised. The owner supplies the media library and its storage."
	maturity: "supported", executable: true, realization: "apply-ready"
	// A hosting baseline, not a guarantee of any transcoding concurrency.
	hostFloor: _architectureV2CoreComputeProfile.hostFloor
	reservation: ramGB: 0.5 // Existing 512 MiB component reservation.
	components: ["jellyfin"]
}
_architectureV2JellyfinComputeProfiles: {
	standard: _architectureV2JellyfinComputeProfile
	high:     _architectureV2JellyfinComputeProfile
}

_architectureV2HomeAssistantComputeProfile: #ModuleComputeProfileV2 & {
	description: "Home Assistant Container for automation and its native product interfaces. All profiles retain the same application and memory reservation; low declares a smaller host floor. Standard and high are equivalent. Home Assistant OS, Supervisor, MQTT and radio-device provisioning are not included."
	maturity: "supported", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.5 // Existing 512 MiB component reservation.
	components: ["home-assistant"]
}
_architectureV2HomeAssistantComputeProfiles: {
	low:      _architectureV2HomeAssistantComputeProfile & {hostFloor: _architectureV2BasementCoreLiteComputeProfiles.low.hostFloor}
	standard: _architectureV2HomeAssistantComputeProfile & {hostFloor: _architectureV2CoreComputeProfile.hostFloor}
	high:     _architectureV2HomeAssistantComputeProfile & {hostFloor: _architectureV2CoreComputeProfile.hostFloor}
}
