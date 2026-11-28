// Package foundation — #AppModuleV3, the typed application module contract of
// the StackKits target architecture (ADR-0054, ADR-0055).
//
// #AppModuleV3 coexists with #ModuleContract. A module that migrates declares
// both: its legacy `Contract` keeps describing the pre-migration delivery
// until Phase 2 deletes the legacy bundle renderers, and its `AppModuleV3`
// value is the complete container declaration internal/engine renders into
// the workload Compose model (internal/engine/workload_compose_model.go). The
// schema types every container and security field the 41 legacy workload
// bundles and the standalone Compose owner can emit, plus the eight governed
// trait families, so a migrated module never needs a module-specific Go
// renderer.
//
// Rendering rules the schema relies on (internal/engine/app_module_v3.go):
//   - A daemon gets restart "unless-stopped", bounded json-file logging and
//     the role's OOM score bias unless the component overrides them.
//   - A dependency on a one-shot or init-once component waits for
//     service_completed_successfully; every other dependency waits for
//     service_started unless dependsOnConditions overrides it.
//   - Route, host-fact and owner-enabled traits are resolved at render time
//     from the plan; nothing in this declaration carries secret material.
package foundation

import "list"

// #AppModuleV3Slug is a DNS-safe identifier for components, networks,
// volumes and workload references.
#AppModuleV3Slug: =~"^[a-z0-9][a-z0-9-]{0,62}$"

// #AppModuleV3EnvName is a container environment variable name.
#AppModuleV3EnvName: =~"^[A-Za-z_][A-Za-z0-9_]*$"

// #AppModuleV3Duration is a Compose duration ("10s", "1m30s").
#AppModuleV3Duration: =~"^([0-9]+(ns|us|ms|s|m|h))+$"

// #AppModuleV3ByteSize is a Compose byte size ("512m", "3g").
#AppModuleV3ByteSize: =~"^[0-9]+(b|k|kb|m|mb|g|gb)?$"

// #AppModuleV3AbsolutePath is an absolute container or host path.
#AppModuleV3AbsolutePath: =~"^/[^\\x00]*$"

// #AppModuleV3Image pins one OCI image by tag and digest.
#AppModuleV3Image: {
	ref:    =~"^[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9._-]+)?$"
	digest: =~"^sha256:[a-f0-9]{64}$"
}

// #AppModuleV3Delivery is the delivery contract. "opentofu-stage1" wraps the
// rendered Compose payload in the ADR-0045 Stage 1 OpenTofu root
// (internal/engine/compose_payload_opentofu.go); "compose" hands the same
// payload to the standalone Compose owner directly.
#AppModuleV3Delivery: {
	contract:  "opentofu-stage1" | "compose"
	managedBy: contract
	// resourcePrefix names the Stage 1 OpenTofu resources.
	resourcePrefix: =~"^[a-z][a-z0-9_]{0,62}$"
	// noWait drops `--wait`; #AppModuleV3 derives it from any component
	// whose health failure is "degraded", as the native executor does.
	noWait: bool
	// adoptNoRecreate renders the adoption apply of an existing Compose
	// install with `up --no-recreate`.
	adoptNoRecreate: bool | *false
}

// #AppModuleV3AdapterRef names an entry of the engine's closed adapter
// registry: a module-owned setup action, a backup hook or a probe.
#AppModuleV3SetupRef: =~"^setup/[a-z0-9][a-z0-9-]*$"
#AppModuleV3HookRef:  =~"^hook/[a-z0-9][a-z0-9-]*$"
#AppModuleV3ProbeRef: =~"^probe/[a-z0-9][a-z0-9-]*$"

// #AppModuleV3ConfigRendererRef names a pure, non-I/O config renderer of the
// engine's closed ConfigRenderer registry.
#AppModuleV3ConfigRendererRef: =~"^config/[a-z0-9][a-z0-9-]*$"

// #AppModuleV3Lifecycle is the two-tier lifecycle. requiredSafetyInit names
// the one-shot components every apply must complete before the entry starts
// (schema or database creation); optionalAppsetupRef is the owner-triggered
// application setup that never blocks an apply.
#AppModuleV3Lifecycle: {
	requiredSafetyInit: [...#AppModuleV3Slug] | *[]
	optionalAppsetupRef?: #AppModuleV3SetupRef
}

// #AppModuleV3Route is the governed service endpoint of the entry component.
// The concrete route (host, exposure, TLS, core owner) comes from the plan.
#AppModuleV3Route: {
	serviceRef:       #AppModuleV3Slug
	targetPort:       int & >0 & <65536
	upstreamProtocol: "http" | "https"
	ingressAuth:      "native" | "forward-auth"
	healthRef:        #AppModuleV3Slug
	requiredPrivilege: "user" | "admin" | "owner"
	allowedExposures: [...("local" | "remote-private" | "public")]
	companionSubdomains?: [...#AppModuleV3Slug]
}

// #AppModuleV3Backup declares which volumes a backup covers and the hooks
// and probes the backup owner runs around a snapshot.
#AppModuleV3Backup: {
	volumes: [...{componentRef: #AppModuleV3Slug, volumeRef: #AppModuleV3Slug}]
	hookRef?:  #AppModuleV3HookRef
	probeRef?: #AppModuleV3ProbeRef
}

// #AppModuleV3ConfigFile is one generated, read-only configuration file
// mounted into the component volume that owns its target path.
#AppModuleV3ConfigFile: {
	componentRef:      #AppModuleV3Slug
	target:            #AppModuleV3AbsolutePath
	configRendererRef: #AppModuleV3ConfigRendererRef
}

// #AppModuleV3SecretSlot is one opaque custody secret slot. The declaration
// never carries material; the node adapter resolves the slot at Apply.
#AppModuleV3SecretSlot: {
	purpose: string & !=""
}

// #AppModuleV3Volume is one container mount.
#AppModuleV3Volume: {
	id:     #AppModuleV3Slug
	target: #AppModuleV3AbsolutePath
	class:  "persistent" | "cache" | "ephemeral"
	backup: bool | *false
	readOnly: bool | *false
	if class != "persistent" {
		backup: false
	}
}

// #AppModuleV3Health is the component health contract. "http" is probed by
// the node adapter through the loopback health port; "command" renders a
// Compose health check; "completion" is a one-shot's exit status.
#AppModuleV3Health: {
	kind: "http" | "command" | "completion"
	if kind == "http" {
		path: =~"^/"
		port: int & >0 & <65536
	}
	if kind == "command" {
		command: [string, ...string]
		interval:    #AppModuleV3Duration | *"10s"
		timeout:     #AppModuleV3Duration | *"5s"
		retries:     int & >0 | *12
		startPeriod: #AppModuleV3Duration | *"10s"
		// startInterval pins the image's own HEALTHCHECK start interval,
		// which Docker otherwise merges into the Compose health check.
		startInterval?: #AppModuleV3Duration
	}
}

// #AppModuleV3Resources is the measured per-container ceiling
// (deploy.resources).
#AppModuleV3Resources: {
	memoryLimit?:       #AppModuleV3ByteSize
	memoryReservation?: #AppModuleV3ByteSize
	cpus?:              number & >0
}

// #AppModuleV3Ulimit is one Compose ulimit.
#AppModuleV3Ulimit: int | {soft: int, hard: int}

// #AppModuleV3Security types every retained container hardening field.
#AppModuleV3Security: {
	capDrop?: [...string]
	capAdd?: [...string]
	securityOpt?: [...string]
	sysctls?: [string]: string
	tmpfs?: [...#AppModuleV3AbsolutePath]
	user?:         =~"^[0-9a-z_][0-9a-z_-]*(:[0-9a-z_][0-9a-z_-]*)?$"
	readOnly?:     bool
	shmSize?:      #AppModuleV3ByteSize
	ulimits?: [string]: #AppModuleV3Ulimit
	pidsLimit?:    int & >0
	memLimit?:     #AppModuleV3ByteSize
	memswapLimit?: #AppModuleV3ByteSize
	cpus?:         number & >0
	// oomScoreAdj overrides the role-derived OOM bias.
	oomScoreAdj?: int & >=-1000 & <=1000
}

// #AppModuleV3Logging overrides the bounded default log policy.
#AppModuleV3Logging: {
	driver: "json-file" | "local" | "journald" | "none"
	options: [string]: string
}

// #AppModuleV3SecretFile delivers a custody secret as a 0400 file.
#AppModuleV3SecretFile: {
	slot:            #AppModuleV3Slug
	target:          #AppModuleV3AbsolutePath
	pathEnvironment: #AppModuleV3EnvName
	uid:             int & >=0
	gid:             int & >=0
}

// #AppModuleV3Traits are the eight governed trait families. Each is admitted
// per module by the engine; a trait outside its admitted module fails the
// render closed.
#AppModuleV3Traits: {
	// hostNetwork runs the component in the host network namespace (the
	// governed LAN announcer); lanListeners are owner-enabled non-HTTP LAN
	// ports.
	hostNetwork?: bool
	lanListeners?: [...{
		port:       int & >0 & <65536
		protocol:   "tcp" | "udp"
		settingRef: #AppModuleV3Slug
	}]
	// publishedPorts are host ports published on every host address (the
	// ADR-0046 mail node).
	publishedPorts?: [...{
		hostPort:      int & >0 & <65536
		containerPort: int & >0 & <65536
		protocol:      "tcp" | "udp"
		hostIP?:       string
	}]
	// devicePassthrough are owner-chosen host devices.
	devicePassthrough?: [...{
		hostPath:   #AppModuleV3AbsolutePath
		target:     #AppModuleV3AbsolutePath
		settingRef: #AppModuleV3Slug
	}]
	// accelerator is the GPU grant of a selected accelerator profile.
	accelerator?: {
		access:     "cdi" | "rocm-device-nodes"
		profileRef: #AppModuleV3Slug
	}
	// sandboxRuntime runs the component under a registered OCI runtime;
	// companionEnv is the governed wiring to its companion workload.
	sandboxRuntime?: {
		runtime: "runsc"
		companionEnv?: [#AppModuleV3EnvName]: string
	}
	// peerNetworks join internal networks of other workloads on the node.
	peerNetworks?: [...{workloadRef: #AppModuleV3Slug, networkRef: #AppModuleV3Slug}]
	// bundledPlugin installs a pinned, governed plugin. Its files reach the
	// container through the node adapter, which owns the host facts
	// (identity issuer, owner binding) the plugin needs.
	bundledPlugin?: {
		kind:    "hass-oidc" | "jellyfin-sso"
		version: string & !=""
		sha256:  =~"^sha256:[a-f0-9]{64}$"
		// Extra pinned artifacts (patch, vendored dependencies) by name.
		artifacts?: [string]: =~"^(sha256:[a-f0-9]{64}|[a-z0-9.-]+)$"
	}
	// dockerLifecycleOwner grants the Docker socket (ADR-0043 game node);
	// ownHostSSH is the governed host SSH right of an own application.
	dockerLifecycleOwner?: bool
	ownHostSSH?:           bool

	// Host-fact traits: the node adapter binds them from workspace custody
	// at Apply. They are declared here so the contract is complete; the
	// engine reports them as pending host facts instead of rendering them.
	homeIdentityAccess?: {
		caBundleTarget: #AppModuleV3AbsolutePath
		caBundleEnvironment: [...#AppModuleV3EnvName]
	}
	pocketIDClient?: {
		publicClient: bool | *false
		callbackPath?: =~"^/"
		callbackURLs?: [...string]
		environment: [#AppModuleV3EnvName]: string
		configuration?: {
			target: #AppModuleV3AbsolutePath
			body:   string
		}
	}
	homeAssistantNotify?: {
		settingRef: #AppModuleV3Slug
		target:     #AppModuleV3AbsolutePath
	}
}

// #AppModuleV3Component is one container.
#AppModuleV3Component: {
	id:   #AppModuleV3Slug
	role: "application" | "database" | "database-init" | "cache" | "machine-learning" | "worker" | "proxy" | "agent" | "bootstrap"
	// lifecycle: "init-once" is a one-shot that the node adapter runs only
	// on the first apply of an instance.
	lifecycle:     "daemon" | "one-shot" | "init-once"
	healthFailure: *"blocking" | "degraded"
	image:         #AppModuleV3Image
	pullPolicy?:   "always" | "missing" | "never"
	// restart overrides the lifecycle-derived restart policy.
	restart?: "no" | "always" | "on-failure" | "unless-stopped"
	runtime?: string & !=""
	command?: [...string]
	entrypoint?: [...string]
	init?:       bool
	stopSignal?: "SIGTERM" | "SIGINT" | "SIGQUIT" | "SIGHUP" | "SIGUSR1"
	dependsOn: [...#AppModuleV3Slug] | *[]
	dependsOnConditions?: [#AppModuleV3Slug]: "service_started" | "service_healthy" | "service_completed_successfully"
	environment: [#AppModuleV3EnvName]: string
	// secretEnvironment maps a variable to a declared secret slot.
	secretEnvironment: [#AppModuleV3EnvName]: #AppModuleV3Slug
	// environmentFromRoute binds a variable to a value derived from the
	// plan's delivery route of the entry component.
	environmentFromRoute?: [#AppModuleV3EnvName]: "https-root-url" | "route-host"
	secretFiles: [...#AppModuleV3SecretFile] | *[]
	volumes: [...#AppModuleV3Volume] | *[]
	// networks are the project networks; only a host-network announcer
	// (traits.hostNetwork) joins none (enforced by the engine validator).
	networks: [...#AppModuleV3Slug]
	// egress joins the outbound-only workload bridge.
	egress:     bool | *false
	health:     #AppModuleV3Health
	resources?: #AppModuleV3Resources
	security?:  #AppModuleV3Security
	logging?:   #AppModuleV3Logging
	dns?: [...string]
	extraHosts?: [...string]
	macAddress?: =~"^([0-9a-f]{2}:){5}[0-9a-f]{2}$"
	labels: [string]: string
	traits: #AppModuleV3Traits

	if lifecycle != "daemon" {
		health: kind: "completion"
	}
}

// #AppModuleV3Network is one project network.
#AppModuleV3Network: {
	internal: bool | *true
}

// #AppModuleV3 is one application module.
#AppModuleV3: {
	apiVersion:     "stackkit.app-module/v3"
	moduleRef:      =~"^stackkits-[a-z0-9-]+-runtime$"
	workloadRef:    #AppModuleV3Slug
	alternativeRef: #AppModuleV3Slug
	release:        string & !=""
	entryComponent: #AppModuleV3Slug
	delivery:       #AppModuleV3Delivery
	lifecycle:      #AppModuleV3Lifecycle
	route?:         #AppModuleV3Route
	secrets: [#AppModuleV3Slug]: #AppModuleV3SecretSlot
	configFiles: [...#AppModuleV3ConfigFile] | *[]
	backup?: #AppModuleV3Backup
	networks: [#AppModuleV3Slug]: #AppModuleV3Network
	components: [#AppModuleV3Component, ...#AppModuleV3Component]

	delivery: noWait: len([for c in components if c.healthFailure == "degraded" {c.id}]) > 0

	_componentIDs: [for c in components {c.id}]
	_componentIDsUnique: list.UniqueItems(_componentIDs) & true
	_entryDeclared: list.Contains(_componentIDs, entryComponent) & true
	_safetyInitDeclared: [for ref in lifecycle.requiredSafetyInit {list.Contains(_componentIDs, ref) & true}]
	_dependenciesDeclared: [for c in components for dep in c.dependsOn {list.Contains(_componentIDs, dep) & true}]
	_networksDeclared: [for c in components for n in c.networks {networks[n] != _|_ & true}]
	_secretSlotsDeclared: [for c in components for _, slot in c.secretEnvironment {secrets[slot] != _|_ & true}]
	_configOwnersDeclared: [for f in configFiles {list.Contains(_componentIDs, f.componentRef) & true}]
}

// AppModuleV3CatalogImages projects the immutable runtime image authority of
// ArchitectureV2Catalog into #AppModuleV3Image values, so a migrated module
// pins its images by reference instead of owning a second set of versions.
AppModuleV3CatalogImages: {
	for module in ArchitectureV2Catalog.modules if module.runtime.components != _|_ {
		(module.metadata.id): {
			for component in module.runtime.components if component.image.digest != _|_ {
				(component.id): #AppModuleV3Image & {ref: component.image.ref, digest: component.image.digest}
			}
		}
	}
}
