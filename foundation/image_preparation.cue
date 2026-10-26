package foundation

// Neutral cache metadata is never an Owner, host-conformance or Apply receipt.
#NeutralImageDigest: string & =~"^[a-f0-9]{64}$"
#NeutralImageProfile: {
	id: string
	kit: "basement-kit" | "cloud-kit"
	module: string
	defaultOS: "ubuntu-26.04"
	compatibleOS: ["ubuntu-26.04", "ubuntu-24.04"]
	architectures: ["amd64", "arm64"]
	runtime: "compose"
	managedTarget: "terramate"
	cacheScope: "core-container-images" | "full-local-artifacts"
	networkRequired: bool
	dataProfile?: "full-local"
	modules?: [...string]
	modelPacks?: [...close({component: "ollama-model-coding", size: "small"})]
	if cacheScope == "core-container-images" {
		networkRequired: true
		dataProfile?: _|_
		modules?: _|_
		modelPacks?: _|_
	}
	if cacheScope == "full-local-artifacts" {
		networkRequired: false
		dataProfile: "full-local"
		kit: "basement-kit"
		module: "stackkits-basement-core-lite-runtime"
		modules: ["stackkits-basement-core-lite-runtime", "stackkits-private-ai-runtime"]
		modelPacks: [{component: "ollama-model-coding", size: "small"}]
	}
	images: [...close({component: string, ref: string, digest: string & =~"^sha256:[a-f0-9]{64}$"})]
}
#NeutralImageManifest: close({
	schemaVersion: "stackkit.neutral-image/v1"
	profile: #NeutralImageProfile
	platform: close({os: "linux", arch: "amd64" | "arm64", distribution: "ubuntu", version: "26.04" | "24.04"})
	// Historical public manifests omit sourceKind. Go admission additionally
	// requires exactly one source identity and never treats a candidate as a release.
	sourceKind?: "public-release" | "preview-candidate"
	release?: close({kit: string, version: string, archiveSha256: #NeutralImageDigest, indexSha256: #NeutralImageDigest})
	candidate?: close({
		kit: "basement-kit"
		version: "0.54.8"
		platform: close({os: "linux", arch: "amd64" | "arm64"})
		cliSha256: #NeutralImageDigest
		identity: close({
			source: close({repository: string, commit: string & =~"^[a-f0-9]{40}$", tree: string & =~"^[a-f0-9]{40}$", exportTree: string & =~"^[a-f0-9]{40}$"})
			producer: close({workflow: string, workflowRef: string, workflowSha: string & =~"^[a-f0-9]{40}$", runId: string, runAttempt: string, event: "workflow_dispatch"})
			indexSha256: #NeutralImageDigest
			archiveSha256: #NeutralImageDigest
			indexAttestationSha256: #NeutralImageDigest
			payloadAttestationSha256: #NeutralImageDigest
			trustedRootSha256: #NeutralImageDigest
			sbomSha256: #NeutralImageDigest
			buildProfile: "full-local"
			buildTags: ["kombify_full_local"]
		})
	})
	binaries: close({
		stackkit: #NeutralImageDigest
		"stackkit-server": #NeutralImageDigest
		"stackkit-mcp": #NeutralImageDigest
		tofu: #NeutralImageDigest
		terramate: #NeutralImageDigest
		if sourceKind != _|_ {
			if sourceKind == "preview-candidate" {
				"stackkit-content-bridge": #NeutralImageDigest
			}
		}
	})
	providerFiles: {[string]: #NeutralImageDigest}
})

// Profiles select only existing catalog-owned core modules. No owner-shaped
// StackSpec is compiled to obtain these cache pins.
NeutralImageProfiles: {
	// This is a data-profile closure of existing Basement and Private AI
	// modules. It is not a fourth kit and contains no Owner or tenant state.
	"basement-full-local-compose": #NeutralImageProfile & {
		id: "basement-full-local-compose"
		kit: "basement-kit"
		module: "stackkits-basement-core-lite-runtime"
		defaultOS: "ubuntu-26.04"
		compatibleOS: ["ubuntu-26.04", "ubuntu-24.04"]
		architectures: ["amd64", "arm64"]
		runtime: "compose"
		managedTarget: "terramate"
		cacheScope: "full-local-artifacts"
		networkRequired: false
		dataProfile: "full-local"
		modules: ["stackkits-basement-core-lite-runtime", "stackkits-private-ai-runtime"]
		modelPacks: [{component: "ollama-model-coding", size: "small"}]
		images: [for catalogModule in ArchitectureV2Catalog.modules if catalogModule.metadata.id == "stackkits-basement-core-lite-runtime" || catalogModule.metadata.id == "stackkits-private-ai-runtime" for runtimeComponent in catalogModule.runtime.components if catalogModule.metadata.id == "stackkits-basement-core-lite-runtime" || runtimeComponent.id == "open-webui" || runtimeComponent.id == "ollama" {
			component: runtimeComponent.id
			ref: runtimeComponent.image.ref
			digest: runtimeComponent.image.digest
		}]
	}
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
