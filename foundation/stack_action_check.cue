// Package foundation - vet-enforced checks for the canonical StackAction contract.
package foundation

_validStackActionWithSupplementalNodes: #StackActionRequest & {
	action:    "stackkit_rollout"
	stack_id:  "stack-1"
	mode:      "bootstrapped"
	tenant_id: "tenant-1"
	owner_id:  "owner-1"
	runtime_target: {
		host: "main.stack.home"
		user: "root"
		port: 22
		access_profile_ref: {
			ref:     "access-profile/main"
			version: "v1"
			scopes: ["runtime:ssh"]
			expires_at: "2099-01-01T00:00:00Z"
		}
	}
	platform_nodes: [
		{
			name: "worker-1"
			role: "worker"
			host: "worker-1.stack.home"
			services: ["vaultwarden"]
			platform: {server_id: "srv_worker_1"}
		},
		{
			name: "storage-1"
			role: "storage"
			host: "storage-1.stack.home"
			services: ["backup"]
			platform: {destination_uuid: "dest-storage-1"}
		},
	]
	techstack_enrollment: {
		tenant_id:        "tenant-1"
		owner_id:         "owner-1"
		stack_id:         "stack-1"
		lease_id:         "lease-1"
		server_url:       "https://techstack.kombify.io"
		server_id:        "server-1"
		runtime_agent_id: "runtime-1"
		enrollment_access_ref: {
			ref:     "enrollment/lease-1"
			version: "v1"
			scopes: ["runtime:enroll"]
			expires_at: "2099-01-01T00:00:00Z"
		}
		inventory_url: "https://techstack.kombify.io/api/v1/workers/runtime-1/inventory"
		control_urls: ["wss://techstack.kombify.io/api/v1/workers/runtime-1/control/ws"]
	}
}

_validServicePlacementWithObservedPlatformID: #ServicePlacementNodeHandoff & {
	name: "worker-2"
	role: "worker"
	services: ["immich"]
	platform: {environment_id: "env-worker-2"}
}

_validServicePlacementWithKomodoBootstrap: #ServicePlacementNodeHandoff & {
	name: "komodo-worker"
	role: "worker"
	services: ["whoami"]
	bootstrap: {
		komodo_core_address: "https://komodo.example.com"
		onboarding_ref: {
			ref:     "platform-onboarding/komodo-worker"
			version: "v1"
			scopes: ["platform:node-onboard"]
			expires_at: "2099-01-01T00:00:00Z"
		}
		ssh: {
			host: "10.0.0.12"
			user: "root"
			access_profile_ref: {
				ref:     "access-profile/komodo-worker"
				version: "v1"
				scopes: ["runtime:ssh"]
				expires_at: "2099-01-01T00:00:00Z"
			}
		}
	}
}
