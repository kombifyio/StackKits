package architecturev2renderer

const wikiWorkloadModuleID = "stackkits-wiki-runtime"

// WikiRegistrySecretRef is the fixed recipient of sealed Wallet registry grants.
const WikiRegistrySecretRef = "secret://stackkits/wiki/registry-auth"

var wikiConnectionFile = selectedPaaSSecretFile{
	Slot: "connection", Target: "/run/secrets/wiki-connection",
	PathEnvironment: "WIKI_CONNECTION_FILE", UID: 65532, GID: 65532,
}

func init() {
	governedCustodyNodeRights[wikiWorkloadModuleID] = governedCommandSecretFile("wiki", wikiConnectionFile)
}

func wikiWorkload() aiAddOnWorkload {
	var pin WorkloadRelease
	if releases := AdmittedWorkloadReleases(wikiWorkloadModuleID); len(releases) > 0 {
		pin = releases[0]
	}
	return aiAddOnWorkload{
		kind: "WikiWorkloadBundle", name: "Wiki", moduleID: wikiWorkloadModuleID, unitID: "wiki",
		workloadRef: "wiki", alternativeRef: "wiki", release: pin.Release,
		imageRef: pin.Ref, imageDigest: pin.Digest, port: 8484,
		secretRefs: []string{"read-bearer", "write-bearer", "connection", "registry-auth"}, dataBinding: "wiki",
		schemaBoundary: "|format:kombify.wiki.v1|initialize:missing-local-writer-only|connection:node-custody|registry:node-custody-oci:secret://stackkits/wiki/registry-auth|peer:none", ingressAuth: "native",
		component: selectedPaaSRuntimeComponent{
			ID: "wiki", Role: "application", Lifecycle: "daemon", Egress: true,
			Image:     selectedPaaSRuntimeImage{Ref: pin.Ref, Digest: pin.Digest},
			DependsOn: []string{}, NetworkRefs: []string{"wiki-internal"},
			Entrypoint: []string{"/app/wiki"}, Command: []string{"serve", "-initialize"},
			Environment:       map[string]string{"WIKI_FILE": "/var/lib/wiki/wiki.db", "WIKI_LISTEN": "0.0.0.0:8484"},
			SecretEnvironment: map[string]string{"WIKI_READ_BEARER": "read-bearer", "WIKI_WRITE_BEARER": "write-bearer"},
			SecretFiles:       []selectedPaaSSecretFile{wikiConnectionFile},
			Volumes:           []selectedPaaSRuntimeVolume{{ID: "state", Target: "/var/lib/wiki", Class: "persistent", Backup: true}},
			Health:            selectedPaaSRuntimeHealth{Kind: "http", Path: "/health", Port: 8484},
			Resources:         &selectedPaaSRuntimeLimits{MemoryLimit: "512m", MemoryReservation: "128m"},
		},
	}
}

func WikiWorkloadBundleRendererContract() RendererContract { return wikiWorkload().contract() }
func ParseWikiWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return wikiWorkload().parse(data)
}
