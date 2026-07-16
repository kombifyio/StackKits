// Package base - Runtime Action node handoff schemas.
package base

import "list"

#NonEmptyString: string & =~"^.+$"

// #RuntimeActionNodeHandoff mirrors the TechStack -> StackKits runtime-action
// JSON boundary for multi-node Base Kit rollouts. The runtime target is the
// primary/foundation node. platform_nodes contains only supplemental
// worker/storage nodes that expand capacity or service placement.
#RuntimeActionNodeHandoff: {
	mode?:                string
	tenant_id?:           string
	owner_id?:            string
	runtime_target?: #RuntimeTarget
	platform_nodes:       [...#PlatformNodeHandoff] | *[]
	techstack_enrollment?: #TechStackEnrollment
}

// #TechStackEnrollment is the TechStack-managed runtime control-plane handoff
// passed through Runtime Actions and `stackkit prepare` env. It identifies the
// real managed runtime server and callback channels; StackKits must fail closed
// when a managed handoff is requested but the required fields are partial.
#TechStackEnrollment: {
	tenant_id?:         string
	owner_id?:          string
	stack_id?:          string
	lease_id:           #NonEmptyString
	server_url?:        string
	server_id:          #NonEmptyString
	runtime_agent_id:   #NonEmptyString
	agent_token:        #NonEmptyString
	heartbeat_url?:     string
	inventory_url?:     string
	control_urls?:      [...#NonEmptyString]
	channel_bootstrap?: {...}
}

// #RuntimeTarget is the primary rollout host. Supplemental nodes must not
// create another hidden main server.
#RuntimeTarget: {
	host?:               string
	public_ip?:          string
	private_ip?:         string
	user?:               string
	port?:               int & >=1 & <=65535
	// A managed Docker transport is only allowed over SSH. Runtime Actions also
	// verify that its user, host, and port match this runtime target.
	docker_host?:        string & =~"^ssh://[^/?#]+$"
	key_path?:           string
	private_key?:        string
	client_private_key?: string
	password?:           string
}

// #SupplementalNodeRole is intentionally narrower than #ClusterNodeRole:
// runtime handoff is for nodes that join an existing primary node.
#SupplementalNodeRole: "worker" | "storage"

// #PlatformNodeHandoff accepts either an already observed platform target, a
// Komodo Periphery bootstrap path, or a no-service capacity note that the
// runtime may skip. Nodes with requested services should use
// #ServicePlacementNodeHandoff.
#PlatformNodeHandoff: (#PlatformNodeBase & {
	platform:   #ExistingPlatformTarget
	bootstrap?: #NodeBootstrap
}) | (#PlatformNodeBase & {
	bootstrap: #KomodoPeripheryBootstrap
	platform?: #NodePlatformTarget
}) | (#PlatformNodeBase & {
	services?: []
	platform?:  #NodePlatformTarget
	bootstrap?: #NodeBootstrap
})

// #ServicePlacementNodeHandoff is the stronger contract for actual service
// placement: a node with requested services must have a real platform identity
// or a bootstrap path that can produce one before app deployment.
#ServicePlacementNodeHandoff: (#PlatformNodeBase & {
	services: [...#NonEmptyString] & list.MinItems(1)
	platform: #ExistingPlatformTarget
}) | (#PlatformNodeBase & {
	services: [...#NonEmptyString] & list.MinItems(1)
	bootstrap: #KomodoPeripheryBootstrap
})

#PlatformNodeBase: {
	name:  #NonEmptyString
	role:  #SupplementalNodeRole | *"worker"
	ip?:   string
	host?: string
	services?: [...#NonEmptyString]
	...
}

// #ExistingPlatformTarget is the non-synthetic placement boundary used by
// Coolify, Dokploy, and Komodo adapters. It cannot prove the identifier exists
// remotely; Runtime Actions must still observe/use the real platform.
#ExistingPlatformTarget: {
	server_id: #NonEmptyString
	...
} | {
	destination_uuid: #NonEmptyString
	...
} | {
	environment_id: #NonEmptyString
	...
} | {
	environment_uuid: #NonEmptyString
	...
}

#NodePlatformTarget: {
	server_id?:        string
	destination_uuid?: string
	environment_id?:   string
	project_uuid?:     string
	environment_uuid?: string
}

#NodeBootstrap: {
	komodo_core_address?:   string
	komodo_onboarding_key?: string
	ssh?:                   #SSHBootstrap
}

// #KomodoPeripheryBootstrap captures the official Periphery onboarding path:
// Core address + onboarding key + SSH access to the supplemental host.
#KomodoPeripheryBootstrap: #NodeBootstrap & {
	komodo_core_address:   #NonEmptyString
	komodo_onboarding_key: #NonEmptyString
	ssh: #SSHBootstrap & {
		host: #NonEmptyString
	}
}

#SSHBootstrap: {
	host?:               string
	user?:               string
	port?:               int & >=1 & <=65535
	key_path?:           string
	key_pem?:            string
	private_key?:        string
	client_private_key?: string
	proxy_jump?:         string
}
