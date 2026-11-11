package architecturev2renderer

import "encoding/json"

const workbenchHostWorkloadModuleID = "stackkits-workbench-host-runtime"

// The private Desktop release read credential, read only by the root launcher.
var workbenchHostReleaseTokenFile = selectedPaaSSecretFile{Slot: "release-token", Target: "/run/secrets/release-token", PathEnvironment: "STACKKIT_RELEASE_TOKEN_FILE", UID: 0, GID: 0}

func init() {
	governedCustodyNodeRights[workbenchHostWorkloadModuleID] = governedCommandSecretFile("workbench-host", workbenchHostReleaseTokenFile)
}

func workbenchHostWorkload() aiAddOnWorkload {
	var command []string
	if err := json.Unmarshal([]byte(workbenchHostCommandJSON), &command); err != nil {
		panic(err)
	}
	return aiAddOnWorkload{
		kind: "WorkbenchHostWorkloadBundle", name: "Workbench host", moduleID: workbenchHostWorkloadModuleID, unitID: "workbench-host",
		workloadRef: "dev-workbench", alternativeRef: "workbench-host", release: workbenchHostRelease,
		imageRef: workbenchHostImageRef, imageDigest: workbenchHostImageDigest, port: 3773,
		secretRefs: []string{"release-token"}, dataBinding: "dev-workbench",
		schemaBoundary: "|archive:workbench-host-cloud-v0.3.4:sha256:1737877513d211db6175c0c1e0537888467d371875e433ea6ada0ee3d9db27df|rights:workspace-only|peer:none", requiredPrivilege: "admin", ingressAuth: "forward-auth",
		component: selectedPaaSRuntimeComponent{
			ID: "workbench-host", Role: "application", Lifecycle: "daemon", Egress: true, Init: true,
			Image:     selectedPaaSRuntimeImage{Ref: workbenchHostImageRef, Digest: workbenchHostImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"workbench-host-internal"},
			Command:     command,
			SecretFiles: []selectedPaaSSecretFile{workbenchHostReleaseTokenFile},
			Volumes:     []selectedPaaSRuntimeVolume{{ID: "state", Target: "/var/lib/workbench-host", Class: "persistent", Backup: true}, {ID: "archive-cache", Target: "/var/cache/workbench-host", Class: "cache", Backup: false}},
			Health:      selectedPaaSRuntimeHealth{Kind: "http", Path: "/.well-known/kombify-workbench-host", Port: 3773},
			Resources:   &selectedPaaSRuntimeLimits{MemoryLimit: "4g", MemoryReservation: "512m"},
		},
	}
}
func WorkbenchHostWorkloadBundleRendererContract() RendererContract {
	return workbenchHostWorkload().contract()
}
func ParseWorkbenchHostWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return workbenchHostWorkload().parse(data)
}
