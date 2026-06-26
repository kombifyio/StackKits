// Package base - CUE constraint checks for runtime_handoff.cue.
// NOT named *_test.cue on purpose: CUE excludes *_test.cue files from
// `cue vet ./base/...`, so this file stays vet-enforced.
package base

_validRuntimeHandoffWithSupplementalNodes: #RuntimeActionNodeHandoff & {
	runtime_target: {
		host: "main.stack.home"
		user: "root"
		port: 22
	}
	platform_nodes: [
		{
			name: "worker-1"
			role: "worker"
			host: "worker-1.stack.home"
			services: ["vaultwarden"]
			platform: {
				server_id: "srv_worker_1"
			}
		},
		{
			name: "storage-1"
			role: "storage"
			host: "storage-1.stack.home"
			services: ["backup"]
			platform: {
				destination_uuid: "dest-storage-1"
			}
		},
	]
}

_validServicePlacementWithObservedPlatformID: #ServicePlacementNodeHandoff & {
	name: "worker-2"
	role: "worker"
	services: ["immich"]
	platform: {
		environment_id: "env-worker-2"
	}
}

_validServicePlacementWithKomodoBootstrap: #ServicePlacementNodeHandoff & {
	name: "komodo-worker"
	role: "worker"
	services: ["whoami"]
	bootstrap: {
		komodo_core_address:   "https://komodo.example.com"
		komodo_onboarding_key: "real-onboarding-key"
		ssh: {
			host:     "10.0.0.12"
			user:     "root"
			key_path: "/run/secrets/worker-key"
		}
	}
}

_validCapacityOnlySupplementalNode: #PlatformNodeHandoff & {
	name: "idle-worker"
	role: "worker"
	host: "idle-worker.stack.home"
}
