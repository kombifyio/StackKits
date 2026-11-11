// The host binary is the exact Desktop Delivery archive; Python only verifies and launches it.
package foundation

import "encoding/json"

_architectureV2WorkbenchHostImage: {ref: "docker.io/library/python:3.14.10-trixie", digest: "sha256:8e4ff723e809a8e3bbd61bdceb92e9031689ad11edccff3306c0b3ebe60704ff"}
_architectureV2WorkbenchHostStack: _architectureV2WorkloadTerramateStack & {_slug: "workbench-host", _placement: {scope: "node-local", cardinality: "one-per-node"}}
_architectureV2WorkbenchHostProviders: [{
	metadata: {id: "stackkits-workbench-host", version: "1.0.0"}
	provides: [], workloadRefs: ["dev-workbench"]
	requires: [{id: "runtime-paas"}, {id: "service-catalog"}, {id: "storage-data-policy"}, {id: "backup-core"}]
	supportedSiteKinds: ["home", "cloud"]
	realization: {kind: "modules", moduleRefs: {required: [], optional: ["stackkits-workbench-host-runtime"]}}
	evidence: ["workbench-host-selected-paas-runtime-contract"]
}]
_architectureV2WorkbenchHostWorkloads: [#WorkloadContractV2 & {
	metadata: {id: "dev-workbench", version: "1.0.0", description: "Optional pinned Workbench Cloud Linux host; Connect enrollment and provider configuration are explicit owner setup. Resource floors are policy, runtime acceptance is pending M13."}
	kind: "application", useCaseRef: "dev", functionalCapabilities: ["user-host-agent-execution"], supportedSiteKinds: ["home", "cloud"], dataClasses: ["personal"]
	defaultAlternative: "workbench-host"
	computeTiers: {low: {included: false, reason: "No independently measured low variant."}, standard: {included: true, alternativeID: "workbench-host"}, high: {included: false, reason: "No named measured high capability; standard is not copied as high."}}
	alternatives: [{
		id: "workbench-host", providerRef: "stackkits-workbench-host", moduleRef: "stackkits-workbench-host-runtime"
		route: {serviceRef: "dev-workbench", healthRef: "workbench-host-http"}
		runtime: {allowedKinds: ["container"], allowedDeliveries: ["application-adapter"], allowedAdapterRefs: ["standalone-compose"], defaultAdapterRef: "standalone-compose", defaultFallbackAdapterRefs: [], compatibility: [{adapterRef: "standalone-compose", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}}]}
		setup: {mode: "manual", owner: "operator", actionRefs: []}
		infrastructure: _architectureV2WorkbenchHostInfrastructure
		inputs: {settings: {allowedRefs: [], requiredRefs: []}, secretInputs: {allowedRefs: ["release-token"], requiredRefs: ["release-token"]}}
	}]
}]
_architectureV2WorkbenchHostLifecycles: [#ApplicationLifecycleContractV1 & {
	metadata: {id: "dev-workbench", version: "1.0.0", description: "Optional pinned Workbench Cloud Linux host; Connect enrollment and provider configuration are explicit owner setup. Resource floors are policy, runtime acceptance is pending M13."}
	workloadRef: "dev-workbench", useCaseRef: "dev", packageRef: "dev", lifecycle: #StandardUseCaseLifecycle & {stages: setup: {}}
}]
_architectureV2WorkbenchHostSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0", scope: "concrete", level: "apply-ready", compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["release-token"]}
	artifacts: {requiredRefs: ["workbench-host-workload-bundle", _architectureV2WorkbenchHostStack.contract.id], outputBindings: [{artifactRef: "workbench-host-workload-bundle", unitRef: "workbench-host", outputRef: "workloads/workbench-host/bundle.json"}, _architectureV2WorkbenchHostStack.binding], contracts: [{id: "workbench-host-workload-bundle", kind: "native-config", format: "json", mode: "0640", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "workbench-host", outputRef: "workloads/workbench-host/bundle.json"}, _architectureV2WorkbenchHostStack.contract]}
	evidence: requiredRefs: ["workbench-host-selected-paas-runtime-contract"]
}
_architectureV2WorkbenchHostModules: [{
	metadata: {id: "stackkits-workbench-host-runtime", version: "1.0.0", description: "Optional pinned Workbench Cloud Linux host; Connect enrollment and provider configuration are explicit owner setup. Resource floors are policy, runtime acceptance is pending M13."}
	role: "workload", providerRef: "stackkits-workbench-host", provides: [], supportedSiteKinds: ["home", "cloud"], nodeSelection: {authority: "control-authority-site", requiredRoles: ["worker"]}
	computeProfiles: standard: #ModuleComputeProfileV2 & {description: "Optional pinned Workbench Cloud Linux host; Connect enrollment and provider configuration are explicit owner setup. Resource floors are policy, runtime acceptance is pending M13.", maturity: "experimental", executable: true, realization: "apply-ready", hostFloor: _architectureV2CoreComputeProfile.hostFloor & {allowedArchitectures: ["amd64"]}, reservation: ramGB: 0.5, components: ["workbench-host"], provenance: {hostFloor: _architectureV2PolicyFloorProvenance, reservation: _architectureV2PolicyReservationProvenance}}
	defaultComputeProfile: "standard"
	runtimeRequirements: allowedArchitectures: ["amd64"]
	runtime: {kind: "container", delivery: "application-adapter", engine: "docker", image: _architectureV2WorkbenchHostImage, entryComponentRef: "workbench-host", components: [{
		id: "workbench-host", role: "application", lifecycle: "daemon", image: _architectureV2WorkbenchHostImage, dependsOn: [], networkRefs: ["workbench-host-internal"]
		egress: true, init: true
		command: ["python3", "-c", "exec(\(json.Marshal(_architectureV2WorkbenchHostStart)))"]
		secretFiles: [{slot: "release-token", target: "/run/secrets/release-token", pathEnvironment: "STACKKIT_RELEASE_TOKEN_FILE", uid: 0, gid: 0}]
		volumes: [{id: "state", target: "/var/lib/workbench-host", class: "persistent", backup: true}, {id: "archive-cache", target: "/var/cache/workbench-host", class: "cache", backup: false}]
		health: {kind: "http", path: "/.well-known/kombify-workbench-host", port: 3773}, resources: {memoryLimit: "4g", memoryReservation: "512m"}
	}]}
	renderUnits: [{
		id: "workbench-host", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"], templateRef: "builtin://workloads/workbench-host/bundle/v2.json", version: "2.0.0", contractHash: "sha256:efffc38244321c333dc49dba0965fc9053430827fd5471113cde0169aed3d244"
		publicInputRefs: ["delivery-route"], inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}], secretInputRefs: ["release-token"], outputs: ["workloads/workbench-host/bundle.json"], placement: {scope: "node-local", cardinality: "one-per-node"}
		serviceEndpoints: [{serviceRef: "dev-workbench", upstreamProtocol: "http", targetPort: 3773, requiredPrivilege: "admin", ingressAuth: "forward-auth", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private"], originSelector: "control-authority-site", healthRef: "workbench-host-http", data: {bindingRef: "dev-workbench", requiredClasses: ["personal"], locality: "primary-site"}}]
	}, _architectureV2WorkbenchHostStack.unit]
	renderVariants: [
		{id: "compose", target: "compose", rendererRef: "stackkit", contractHash: "sha256:66aa1152e7d87b3292a2eefbcca398746546e6499c60b70000a0a7cb34704f02", unitRefs: ["workbench-host"], artifactRefs: ["workbench-host-workload-bundle"], publicInputRefs: ["delivery-route"], secretInputRefs: ["release-token"], planInputRefs: []},
		{id: "opentofu", target: "opentofu", rendererRef: "stackkit", contractHash: "sha256:52e50871398d3f1fc5bca11be237abce135bdb39157fa33bc23b6b3d18bd0e41", unitRefs: ["workbench-host"], artifactRefs: ["workbench-host-workload-bundle"], publicInputRefs: ["delivery-route"], secretInputRefs: ["release-token"], planInputRefs: []},
		{id: "terramate", target: "terramate", rendererRef: "stackkit", contractHash: _architectureV2TerramateStackHashes.workload, unitRefs: ["workbench-host", "terramate-stack"], artifactRefs: ["workbench-host-workload-bundle", _architectureV2WorkbenchHostStack.contract.id], publicInputRefs: ["delivery-route"], secretInputRefs: ["release-token"], planInputRefs: []},
	]
	realizationSupport: _architectureV2WorkbenchHostSupport
	health: [{id: "workbench-host-http", phase: "continuous", kind: "http", path: "/.well-known/kombify-workbench-host", port: 3773, timeoutSeconds: 10, expectedStatuses: [200]}]
	evidence: ["workbench-host-selected-paas-runtime-contract"]
}]

_architectureV2WorkbenchHostInfrastructure: #WorkloadInfrastructureV1 & {
	dataBinding: {moduleRef: "stackkits-workload-data-binding", bindingRef: "dev-workbench", classes: ["personal"], locality: "primary-site"}
	storageAllocation: {moduleRef: "stackkits-storage-allocation", allocations: [{componentRef: "workbench-host", volumeRef: "state", target: "/var/lib/workbench-host", class: "persistent", backup: true, dataClasses: ["personal"], dataBindingRef: "dev-workbench"}, {componentRef: "workbench-host", volumeRef: "archive-cache", target: "/var/cache/workbench-host", class: "cache", backup: false, dataClasses: []}]}
	backupSource: {moduleRef: "stackkits-backup-source", allocations: [for a in storageAllocation.allocations if a.backup {componentRef: a.componentRef, volumeRef: a.volumeRef, dataClasses: a.dataClasses, backupDataClass: "secret-material"}]}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

// The launcher holds the private-release credential only before dropping privilege.
_architectureV2WorkbenchHostStart: """
	import hashlib, os, pathlib, platform, tarfile, tempfile, urllib.request
	if platform.machine() != "x86_64":
	    raise SystemExit("Workbench host archive requires linux-x64")
	class Redirect(urllib.request.HTTPRedirectHandler):
	    def redirect_request(self, req, fp, code, msg, headers, newurl):
	        if not newurl.startswith("https://"):
	            raise RuntimeError("Release redirect must use HTTPS")
	        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
	        redirected.remove_header("Authorization")
	        return redirected
	cache = pathlib.Path("/var/cache/workbench-host")
	if cache.is_symlink():
	    raise RuntimeError("Archive cache must not be a symlink")
	cache.mkdir(exist_ok=True, mode=0o700)
	os.chmod(cache, 0o700)
	digest_pin = "1737877513d211db6175c0c1e0537888467d371875e433ea6ada0ee3d9db27df"
	archive_path = cache / (digest_pin + ".tar.gz")
	if archive_path.is_symlink():
	    raise RuntimeError("Archive cache entry must not be a symlink")
	token_path = os.environ.pop("STACKKIT_RELEASE_TOKEN_FILE")
	def verified(path):
	    if not path.is_file() or path.stat().st_size > 1073741824:
	        return False
	    with path.open("rb") as cached:
	        return hashlib.file_digest(cached, "sha256").hexdigest() == digest_pin
	if not verified(archive_path):
	    for stale in cache.glob("tmp*"):
	        if stale.is_file() and not stale.is_symlink():
	            stale.unlink()
	    opener = urllib.request.build_opener(Redirect())
	    req = urllib.request.Request("https://api.github.com/repositories/1347582990/releases/assets/730476591", headers={"Accept": "application/octet-stream", "Authorization": "Bearer " + pathlib.Path(token_path).read_text().strip(), "User-Agent": "stackkits-workbench-host"})
	    download = tempfile.NamedTemporaryFile(dir=cache, delete=False)
	    try:
	        with download, opener.open(req, timeout=60) as response:
	            size = 0
	            while chunk := response.read(1048576):
	                size += len(chunk)
	                if size > 1073741824:
	                    raise RuntimeError("Archive exceeds 1 GiB")
	                download.write(chunk)
	        candidate = pathlib.Path(download.name)
	        if not verified(candidate):
	            raise RuntimeError("Published Workbench archive digest mismatch")
	        os.replace(candidate, archive_path)
	    finally:
	        pathlib.Path(download.name).unlink(missing_ok=True)
	with archive_path.open("rb") as archive:
	    if hashlib.file_digest(archive, "sha256").hexdigest() != digest_pin:
	        raise RuntimeError("Published Workbench archive digest mismatch")
	    archive.seek(0)
	    with tarfile.open(fileobj=archive, mode="r:gz") as payload:
	        payload.extractall("/opt/workbench-host", filter="data")
	root = pathlib.Path("/opt/workbench-host/kombify-workbench-host-cloud-0.3.4-linux-x64")
	state = pathlib.Path("/var/lib/workbench-host")
	for name in ("home", "workspace", "bin"):
	    directory = state / name
	    if directory.is_symlink():
	        raise RuntimeError("Workbench state directory must not be a symlink")
	    directory.mkdir(exist_ok=True, mode=0o700)
	    os.chmod(directory, 0o700)
	    os.chown(directory, 10000, 10000)
	os.setgroups([])
	os.setgid(10000)
	os.setuid(10000)
	os.environ.update(HOME=str(state / "home"), PATH=str(state / "bin") + ":" + str(root) + ":" + os.environ["PATH"])
	os.chdir(state / "workspace")
	os.execv(str(root / "t3"), [str(root / "t3"), "serve", "--no-browser", "--host", "0.0.0.0", "--port", "3773"])
	"""
