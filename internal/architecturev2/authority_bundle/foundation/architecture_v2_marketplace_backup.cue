package foundation

import "list"

// Explicit Owner-selected upstream filesystem data, not a compiler workload.
#MarketplaceBackupServiceV1: close({
	siteRef: #SiteID
	nodeRef: #NodeID
	installationDigest: #ContentHash
	serverUUID: string & =~"^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$"
	serviceUUID: string & =~"^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$"
	componentUUID: string & =~"^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$"
	componentName: string & =~"^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$"
	volumeName: string & =~"^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$"
	containerTarget: string & =~"^/[^\\x00]+$"
	backupDataClass: "config" | "user-content"
	expectedGeneration: close({
		daemonID: string & =~"^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$"
		containerID: string & =~"^[a-f0-9]{64}$"
		imageID: #ContentHash
		volumeCreatedAt: string
		volumeMountpoint: string & =~"^/[^\\x00]+$"
		volumeMetadataDigest: #ContentHash
	})
})

#BackupPolicyV1: marketplaceServices?: [...#MarketplaceBackupServiceV1] & list.MaxItems(1)
#ModuleBackupPolicyProjectionV1: marketplaceServices?: [...#MarketplaceBackupServiceV1] & list.MaxItems(1)
#ModulePublicLocalKopiaBackupSourceV1: marketplaceServices?: [...#MarketplaceBackupServiceV1] & list.MaxItems(1)
