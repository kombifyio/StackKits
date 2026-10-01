package foundation

// Neutral cache metadata is never an Owner, host-conformance or Apply receipt.
#NeutralImageDigest: string & =~"^[a-f0-9]{64}$"
#NeutralImageProfile: close({
	id: string
	kit: "basement-kit" | "cloud-kit"
	module: string
	defaultOS: "ubuntu-26.04"
	compatibleOS: ["ubuntu-26.04", "ubuntu-24.04"]
	architectures: ["amd64", "arm64"]
	runtime: "compose"
	managedTarget: "terramate"
	cacheScope: "core-container-images"
	networkRequired: true
	images: [...close({component: string, ref: string, digest: string & =~"^sha256:[a-f0-9]{64}$"})]
})
#NeutralImageManifest: close({
	schemaVersion: "stackkit.neutral-image/v1"
	profile: #NeutralImageProfile
	platform: close({os: "linux", arch: "amd64" | "arm64", distribution: "ubuntu", version: "26.04" | "24.04"})
	release: close({kit: string, version: string, archiveSha256: #NeutralImageDigest, indexSha256: #NeutralImageDigest})
	binaries: close({stackkit: #NeutralImageDigest, "stackkit-server": #NeutralImageDigest, "stackkit-mcp": #NeutralImageDigest, tofu: #NeutralImageDigest, terramate: #NeutralImageDigest})
	providerFiles: {[string]: #NeutralImageDigest}
})

// Profiles select only existing catalog-owned core modules. No owner-shaped
// StackSpec is compiled to obtain these cache pins.
NeutralImageProfiles: {
	for selection in [
		{id: "cloud-core-compose", kit: "cloud-kit", module: "stackkits-cloud-core-standalone-runtime"},
		{id: "basement-core-compose", kit: "basement-kit", module: "stackkits-basement-core-runtime"},
		{id: "basement-core-lite-compose", kit: "basement-kit", module: "stackkits-basement-core-lite-runtime"},
	] {
		(selection.id): #NeutralImageProfile & {
			id: selection.id
			kit: selection.kit
			module: selection.module
			defaultOS: "ubuntu-26.04"
			compatibleOS: ["ubuntu-26.04", "ubuntu-24.04"]
			architectures: ["amd64", "arm64"]
			runtime: "compose"
			managedTarget: "terramate"
			cacheScope: "core-container-images"
			networkRequired: true
			images: [for catalogModule in ArchitectureV2Catalog.modules if catalogModule.metadata.id == selection.module for runtimeComponent in catalogModule.runtime.components {
				component: runtimeComponent.id
				ref: runtimeComponent.image.ref
				digest: runtimeComponent.image.digest
			}]
		}
	}
}
