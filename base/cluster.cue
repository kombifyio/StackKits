// Package base - Cluster mode schemas.
package base

#ClusterMode: "first" | "join"

// #ClusterNodeRole is the canonical BaseKit multi-node role vocabulary.
// "control-plane" and "standalone" remain compatibility aliases at import
// boundaries; normalized cluster state uses "main", "worker", or "storage".
#ClusterNodeRole: "main" | "worker" | "storage"

// #JoinToken is the only supported path for turning a separately prepared node
// into a member of an existing homelab. Two BaseKit installs are separate
// homelabs until a worker/storage node joins a main node with this token.
#JoinToken: {
	version:   *"stackkit.cluster/v1" | string
	homelabId: string & =~"^[a-z][a-z0-9-]*$"
	mainNode: {
		id:       string & =~"^[a-z][a-z0-9-]*$"
		name:     string
		endpoint: string
	}
	token:      string & =~"^skj_[A-Za-z0-9_-]{32,}$"
	expiresAt?: string
	allowedRoles: [...("worker" | "storage")] | *["worker", "storage"]
}

// #JoinConfig is rendered by `stackkit init --cluster-mode=join` and consumed
// by the worker-side registration flow.
#JoinConfig: {
	mode:         "join"
	token:        string & =~"^skj_[A-Za-z0-9_-]{32,}$"
	mainEndpoint: string
	node: {
		id:    string & =~"^[a-z][a-z0-9-]*$"
		name:  string
		role:  "worker" | "storage"
		host?: string
		ip?:   string
	}
	statePath: string | *".stackkit/state.yaml"
}

// #ClusterState records the source-of-truth shape mirrored into kombify
// TechStack while keeping the node-local .stackkit/state.yaml as the recovery
// anchor.
#ClusterState: {
	homelabId:   string & =~"^[a-z][a-z0-9-]*$"
	trustDomain: string
	mainNodeId:  string
	nodes: [...{
		id:        string & =~"^[a-z][a-z0-9-]*$"
		name:      string
		role:      #ClusterNodeRole
		endpoint?: string
		statePath: string | *".stackkit/state.yaml"
	}]
}
