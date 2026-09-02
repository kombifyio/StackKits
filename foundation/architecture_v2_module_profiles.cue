package foundation

// Native module profiles project only facts already declared by StackKits.
// Core host floors and recommendations come from the existing kit contracts.
// Application RAM reservations are the sum of the pinned runtime component
// reservations expressed exactly in GiB. A reservation is not a
// host minimum or a recommendation. CPU, storage and workload-size requirements
// remain absent where the catalog has not declared them; consumers must report
// those axes as unverified. Equal legacy high/standard profiles do not imply an
// additional capability or a measured performance benefit.
_architectureV2CoreComputeProfile: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	platformManagement: "selected-provider"
	hostFloor: {minCpuCores: 2, minRamGB: 4, minStorageGB: 20}
	recommended: {cpuCores: 4, ramGB: 4, storageGB: 20}
}

_architectureV2CloudCoreComputeProfile: _architectureV2CoreComputeProfile & {
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "coolify", "coolify-postgres", "coolify-redis", "coolify-realtime", "hub"]
}
_architectureV2CloudCoreComputeProfiles: {
	standard: _architectureV2CloudCoreComputeProfile
	high:     _architectureV2CloudCoreComputeProfile
}

_architectureV2BasementCoreComputeProfile: _architectureV2CoreComputeProfile & {
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "step-ca", "coolify", "coolify-postgres", "coolify-redis", "coolify-realtime", "kopia-agent", "hub"]
}
_architectureV2BasementCoreComputeProfiles: {
	standard: _architectureV2BasementCoreComputeProfile
	high:     _architectureV2BasementCoreComputeProfile
}

_architectureV2BasementCoreLiteComputeProfiles: low: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	platformManagement: "standalone"
	hostFloor: {minCpuCores: 2, minRamGB: 2, minStorageGB: 10}
	recommended: {cpuCores: 2, ramGB: 2, storageGB: 10}
	components: ["router", "socket-proxy", "pocketid", "tinyauth", "step-ca", "kopia-agent", "hub"]
	degradations: ["paas-management-omitted"]
}

_architectureV2ImmichComputeProfile: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	// 512 + 512 + 256 + 64 MiB = 1344 MiB; one-shot init has no reservation.
	reservation: ramGB: 1.3125
	components: ["immich-server", "immich-machine-learning", "immich-postgres", "immich-postgres-init", "immich-valkey"]
}
_architectureV2ImmichComputeProfiles: {
	standard: _architectureV2ImmichComputeProfile
	high:     _architectureV2ImmichComputeProfile
}
_architectureV2ImmichLiteComputeProfiles: low: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	// 512 + 256 + 64 MiB = 832 MiB; no machine-learning worker is selected.
	reservation: ramGB: 0.8125
	components: ["immich-server", "immich-postgres", "immich-postgres-init", "immich-valkey"]
	degradations: ["machine-learning-disabled"]
}

_architectureV2CloudreveComputeProfile: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.125 // Existing 128 MiB component reservation.
	components: ["cloudreve"]
}
_architectureV2CloudreveComputeProfiles: {
	low:      _architectureV2CloudreveComputeProfile
	standard: _architectureV2CloudreveComputeProfile
	high:     _architectureV2CloudreveComputeProfile
}

_architectureV2VaultwardenComputeProfile: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.0625 // Existing 64 MiB component reservation.
	components: ["vaultwarden"]
}
_architectureV2VaultwardenComputeProfiles: {
	low:      _architectureV2VaultwardenComputeProfile
	standard: _architectureV2VaultwardenComputeProfile
	high:     _architectureV2VaultwardenComputeProfile
}

_architectureV2JellyfinComputeProfile: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.5 // Existing 512 MiB component reservation.
	components: ["jellyfin"]
}
_architectureV2JellyfinComputeProfiles: {
	standard: _architectureV2JellyfinComputeProfile
	high:     _architectureV2JellyfinComputeProfile
}

_architectureV2HomeAssistantComputeProfile: #ModuleComputeProfileV2 & {
	maturity: "supported", executable: true, realization: "apply-ready"
	reservation: ramGB: 0.5 // Existing 512 MiB component reservation.
	components: ["home-assistant"]
}
_architectureV2HomeAssistantComputeProfiles: {
	low:      _architectureV2HomeAssistantComputeProfile
	standard: _architectureV2HomeAssistantComputeProfile
	high:     _architectureV2HomeAssistantComputeProfile
}
