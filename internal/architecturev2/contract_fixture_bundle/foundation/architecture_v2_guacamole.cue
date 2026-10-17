// Remote uses Apache Guacamole's explicit static XML authentication profile.
// Application features stay upstream-owned. Destination credentials are prompted
// by Guacamole; only the break-glass owner password belongs to node custody.
package foundation

_architectureV2GuacamoleImage: {ref: "docker.io/guacamole/guacamole:1.6.0", digest: "sha256:f344085e618bb05e22b964b0208dbd06d3468275bac70206f93805245e067b40"}
_architectureV2GuacdImage: {ref: "docker.io/guacamole/guacd:1.6.0", digest: "sha256:8974eaa9ba32f713daf311e7cc8cd7e4cdfba1edea39eed75524e78ef4b08f4f"}

// The startup bytes are also embedded by the renderer and included in its
// contract hash. Secret material is read from the governed raw custody file.
_architectureV2GuacamoleStartup: "set +x; set -eu; umask 077; [ \"$GUACAMOLE_HOME\" = /dev/shm/stackkits-guacamole ]; [ \"$(stat -f -c %T /dev/shm)\" = tmpfs ]; [ \"$STACKKITS_OWNER_PASSWORD_FILE\" = /run/secrets/owner-password ]; [ -s \"$STACKKITS_OWNER_PASSWORD_FILE\" ]; [ -n \"$STACKKITS_REMOTE_CONNECTION_XML\" ]; [ ! -L \"$GUACAMOLE_HOME\" ]; mkdir -p \"$GUACAMOLE_HOME\"; chmod 700 \"$GUACAMOLE_HOME\"; sum=$(sha256sum < \"$STACKKITS_OWNER_PASSWORD_FILE\"); hash=${sum%% *}; [ \"${#hash}\" = 64 ]; case \"$hash\" in *[!0-9a-f]*) exit 1 ;; esac; mapping=$(mktemp \"$GUACAMOLE_HOME/.user-mapping.XXXXXX\"); trap 'rm -f \"$mapping\"' EXIT HUP INT TERM; printf '<?xml version=\"1.0\" encoding=\"UTF-8\"?><user-mapping><authorize username=\"owner\" password=\"%s\" encoding=\"sha256\">%s</authorize></user-mapping>\\n' \"$hash\" \"$STACKKITS_REMOTE_CONNECTION_XML\" > \"$mapping\"; chmod 600 \"$mapping\"; mv -f \"$mapping\" \"$GUACAMOLE_HOME/user-mapping.xml\"; trap - EXIT HUP INT TERM; unset hash sum mapping STACKKITS_REMOTE_CONNECTION_XML STACKKITS_OWNER_PASSWORD_FILE; exec /opt/guacamole/bin/entrypoint.sh"
_architectureV2GuacamoleTerramateStack: _architectureV2WorkloadTerramateStack & {_slug: "guacamole", _placement: {scope: "node-local", cardinality: "one-per-node"}}

_architectureV2GuacamoleComputeProfile: {
	description: "Guacamole and guacd with the same normal RDP/input/reconnect and transient XML owner authentication. Module demand is observed from both resident daemons; allocation and physical/high-benefit qualification remain separate."
	maturity:    "experimental", executable: true, realization: "apply-ready"
	hostFloor:   _architectureV2CoreComputeProfile.hostFloor
	// Keep the conservative measured Standard RAM reservation. Low/High CPU
	// now includes the observed container-cold phase through the common C1 binder.
	reservation: {ramGB: 0.3125}
	components: ["guacd", "guacamole"]
	provenance: {hostFloor: _architectureV2PolicyFloorProvenance}
}
// One module envelope is shared equally by both resident components.
// The profile label never changes authentication, desktop protocols or user features.
_architectureV2GuacamoleComputeProfiles: {
	low: #ModuleComputeProfileV2 & _architectureV2GuacamoleComputeProfile & {reservation: cpuCores: 0.1, headroom: {ramGB: 0.1875, cpuCores: 0.4}, recommended: {ramGB: 0.25, cpuCores: 0.5}, provenance: {reservation: {source: "measured", ref: "docs/plans/2026-10-11-stream-a/a6-remote-cold-latency-observation.json"}, headroom: _architectureV2PolicyReservationProvenance, recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-guacamole-runtime/low.json"}}}
	standard: #ModuleComputeProfileV2 & _architectureV2GuacamoleComputeProfile & {reservation: cpuCores: 0.05, headroom: {ramGB: 0.4375, cpuCores: 0.95}, recommended: {ramGB: 0.375, cpuCores: 0.5}, provenance: {reservation: {source: "measured", ref: "docs/data/resource-evidence/stackkits-guacamole-runtime/standard.json"}, headroom: _architectureV2PolicyReservationProvenance, recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-guacamole-runtime/standard.json"}}}
	high: #ModuleComputeProfileV2 & _architectureV2GuacamoleComputeProfile & {reservation: cpuCores: 0.2, headroom: {ramGB: 1.1875, cpuCores: 1.8}, recommended: {ramGB: 0.25, cpuCores: 1.25}, provenance: {reservation: {source: "measured", ref: "docs/plans/2026-10-11-stream-a/a6-remote-cold-latency-observation.json"}, headroom: _architectureV2PolicyReservationProvenance, recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-guacamole-runtime/high.json"}}}
}

_architectureV2GuacamoleProviders: [{
	metadata: {id: "stackkits-guacamole", version: "1.0.0"}
	provides: []
	workloadRefs: ["remote"]
	requires: [{id: "runtime-paas"}, {id: "service-catalog"}]
	supportedSiteKinds: ["home", "cloud"]
	realization: {kind: "modules", moduleRefs: {required: [], optional: ["stackkits-guacamole-runtime"]}}
	evidence: ["guacamole-selected-paas-runtime-contract"]
}]

// Data contract (backup and restore): nothing to carry. GUACAMOLE_HOME is
// tmpfs, the connection list is rendered from the `remote-connection` setting,
// the owner password stays in node custody and destination credentials are
// prompted per session. The workload therefore declares no storage allocation
// and its delivery row keeps `backupRestore: false`; a node restore skips it
// because it holds nothing to restore, and its next start renders the restored
// plan again. A persistent backup-enabled volume must claim the capability in
// the same change: the catalog refuses the combination otherwise.
_architectureV2GuacamoleWorkloads: [#WorkloadContractV2 & {
	metadata: {id: "remote", version: "1.0.0", description: "Browser access to one explicitly selected SSH, RDP or VNC destination through Apache Guacamole. Static XML authentication is the declared initial profile; SSO and household clients require separate admitted configuration."}
	kind: "application", useCaseRef: "remote"
	functionalCapabilities: ["browser-remote-access", "remote-session"]
	supportedSiteKinds: ["home", "cloud"]
	dataClasses: ["personal"]
	defaultAlternative: "guacamole"
	computeTiers: {
		low: {included: true, alternativeID: "guacamole"}
		standard: {included: true, alternativeID: "guacamole"}
		high: {included: true, alternativeID: "guacamole"}
	}
	alternatives: [{
		id: "guacamole", providerRef: "stackkits-guacamole", moduleRef: "stackkits-guacamole-runtime"
		route: {serviceRef: "remote", healthRef: "guacamole-http"}
		runtime: {
			allowedKinds: ["container"], allowedDeliveries: ["application-adapter"]
			allowedAdapterRefs: ["standalone-compose"], defaultAdapterRef: "standalone-compose", defaultFallbackAdapterRefs: []
			compatibility: [{adapterRef: "standalone-compose", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}}]
		}
		setup: {mode: "on-demand", owner: "module", actionRefs: ["guacamole-owner-login"]}
		inputs: {
			settings: {allowedRefs: ["remote-connection"], requiredRefs: ["remote-connection"]}
			secretInputs: {allowedRefs: ["owner-password"], requiredRefs: ["owner-password"]}
		}
	}]
}]

_architectureV2GuacamoleLifecycles: [#ApplicationLifecycleContractV1 & {
	metadata: {id: "remote", version: "1.0.0", description: "Remote application lifecycle using shared standalone operations and the plan-bound owner login setup action."}
	workloadRef: "remote", useCaseRef: "remote", packageRef: "remote"
	lifecycle: #StandardUseCaseLifecycle & {stages: setup: {}}
}]

_architectureV2GuacamoleSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0", scope: "concrete", level: "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["remote-connection", "owner-password", "compute-profile"]}
	artifacts: {
		requiredRefs: ["guacamole-workload-bundle", _architectureV2GuacamoleTerramateStack.contract.id]
		outputBindings: [{artifactRef: "guacamole-workload-bundle", unitRef: "guacamole", outputRef: "workloads/guacamole/bundle.json"}, _architectureV2GuacamoleTerramateStack.binding]
		contracts: [{id: "guacamole-workload-bundle", kind: "native-config", format: "json", mode: "0640", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "guacamole", outputRef: "workloads/guacamole/bundle.json"}, _architectureV2GuacamoleTerramateStack.contract]
	}
	evidence: requiredRefs: ["guacamole-selected-paas-runtime-contract"]
}

_architectureV2GuacamoleModules: [{
	metadata: {id: "stackkits-guacamole-runtime", version: "1.0.0", description: "Digest-pinned Guacamole and internal guacd on one selected node. Connection intent is required; destination credentials stay with the upstream prompt and owner authentication is generated in private tmpfs."}
	role: "workload", providerRef: "stackkits-guacamole"
	provides: [], supportedSiteKinds: ["home", "cloud"]
	nodeSelection: {authority: "control-authority-site", requiredRoles: ["worker"]}
	computeProfiles: _architectureV2GuacamoleComputeProfiles, defaultComputeProfile: "standard"
	runtime: {
		kind:  "container", delivery:                            "application-adapter", engine: "docker"
		image: _architectureV2GuacamoleImage, entryComponentRef: "guacamole"
		components: [{
			id: "guacd", role: "application", lifecycle: "daemon", image: _architectureV2GuacdImage
			dependsOn: [], networkRefs: ["guacamole-internal"], egress: true
			health: {kind: "command", command: ["nc", "-z", "127.0.0.1", "4822"]}
		}, {
			id: "guacamole", role: "application", lifecycle: "daemon", image: _architectureV2GuacamoleImage
			dependsOn: ["guacd"], networkRefs: ["guacamole-internal"]
			environment: {GUACD_HOSTNAME: "guacd", GUACD_PORT: "4822", WEBAPP_CONTEXT: "ROOT", GUACAMOLE_HOME: "/dev/shm/stackkits-guacamole"}
			command: ["/bin/sh", "-ec", _architectureV2GuacamoleStartup]
			secretFiles: [{slot: "owner-password", target: "/run/secrets/owner-password", pathEnvironment: "STACKKITS_OWNER_PASSWORD_FILE", uid: 1001, gid: 1001}]
			health: {kind: "http", path: "/", port: 8080}
		}]
	}
	renderUnits: [{
		id: "guacamole", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"]
		templateRef: "builtin://workloads/guacamole/bundle/v2.json", version: "2.0.0", contractHash: "sha256:9acbeb92d5afecfc7532c9c0bceb92c035214c85d1f85e0bdc9f20999415e586"
		publicInputRefs: ["delivery-route", "remote-connection", "compute-profile"]
		inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}, {targetRef: "compute-profile", sourceRef: "modules.computeProfileBinding", valueType: "module-compute-profile-v1", cardinality: "single", required: true}]
		secretInputRefs: ["owner-password"], outputs: ["workloads/guacamole/bundle.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
		serviceEndpoints: [{serviceRef: "remote", upstreamProtocol: "http", targetPort: 8080, requiredPrivilege: "user", ingressAuth: "forward-auth", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private", "public"], originSelector: "control-authority-site", healthRef: "guacamole-http", data: {bindingRef: "remote", requiredClasses: ["personal"], locality: "primary-site"}}]
	}, _architectureV2GuacamoleTerramateStack.unit]
	renderVariants: [{
		id: "compose", target: "compose", rendererRef: "stackkit", contractHash: "sha256:c29cb073dde154d4aa38789b506874de7f2d2364fe8deb2dbb181a507fcc346a"
		unitRefs: ["guacamole"], artifactRefs: ["guacamole-workload-bundle"], publicInputRefs: ["delivery-route", "remote-connection", "compute-profile"], secretInputRefs: ["owner-password"], planInputRefs: []
	}, {
		id: "opentofu", target: "opentofu", rendererRef: "stackkit", contractHash: "sha256:c13901023f2cb0271580070c4e115940afa77510e42b9aad549bfd8981ed5690"
		unitRefs: ["guacamole"], artifactRefs: ["guacamole-workload-bundle"], publicInputRefs: ["delivery-route", "remote-connection", "compute-profile"], secretInputRefs: ["owner-password"], planInputRefs: []
	}, {
		id: "terramate", target: "terramate", rendererRef: "stackkit", contractHash: _architectureV2TerramateStackHashes.workload
		unitRefs: ["guacamole", "terramate-stack"], artifactRefs: ["guacamole-workload-bundle", _architectureV2GuacamoleTerramateStack.contract.id], publicInputRefs: ["delivery-route", "remote-connection", "compute-profile"], secretInputRefs: ["owner-password"], planInputRefs: []
	}]
	realizationSupport: _architectureV2GuacamoleSupport
	health: [{id: "guacamole-http", phase: "continuous", kind: "http", path: "/", port: 8080, timeoutSeconds: 10, expectedStatuses: [200]}]
	evidence: ["guacamole-selected-paas-runtime-contract"]
}]
