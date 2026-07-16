package base

import "strings"

let _architectureV2MaxSocketSegment = strings.Repeat("a", 101)

_architectureV2UnixSocketMaximumBytesCheck: #UnixSocketPath & "/\(_architectureV2MaxSocketSegment).sock"

// Concrete positive checks keep the node-local implementation seam executable
// as the surrounding architecture contract evolves.
_architectureV2RuntimeDaemonFactCheck: #RuntimeDaemonFactV1 & {
	instanceRef: "docker-rootless-a"
	engine:      "docker"
	socketPath:  "/run/user/1000/docker.sock"
}

_architectureV2RuntimeDaemonBindingCheck: #ResolvedRuntimeDaemonBindingV1 & {
	siteRef:     "home"
	nodeRef:     "home-node-a"
	daemonRef:   "docker-rootless"
	instanceRef: "docker-rootless-a"
	engine:      "docker"
	socketPath:  "/run/user/1000/docker.sock"
}

_architectureV2DockerHTTPProviderCheck: #ImplementationInterfaceProviderV2 & {
	id:       "docker-api-readonly"
	kind:     "docker-http-readonly-v1"
	protocol: "docker-http"
	version:  "v1"
	endpoint: {
		ref:        "docker-api"
		visibility: "node-local"
		transport:  "tcp"
		networkRef: "docker-api-net"
		address:    "docker-socket-proxy"
		port:       2375
	}
	scopes: ["CONTAINERS", "EVENTS", "NETWORKS", "PING", "VERSION"]
	coLocation:    "same-node-and-network"
	daemonRef:     "docker-default"
	policyProfile: "docker-readonly-baseline"
}

_architectureV2DockerHTTPRequirementCheck: #ResolvedImplementationInterfaceRequirementV2 & {
	id:       "docker-api-consumer"
	kind:     "docker-http-readonly-v1"
	protocol: "docker-http"
	version:  "v1"
	endpoint: _architectureV2DockerHTTPProviderCheck.endpoint
	scopes: ["CONTAINERS", "EVENTS"]
	coLocation:    "same-node-and-network"
	daemonRef:     "docker-default"
	policyProfile: "docker-readonly-baseline"
	providerBindings: [
		{
			interfaceRef:        "docker-api-readonly"
			moduleRef:           "socket-proxy"
			unitRef:             "socket-proxy"
			providerInstanceRef: "socket-proxy-node-home-node-a-daemon-docker-home-node-a"
			consumerInstanceRef: "monitoring-agent-node-home-node-a"
			siteRef:             "home"
			nodeRef:             "home-node-a"
			daemonRef:           "docker-default"
			daemonInstanceRef:   "docker-home-node-a"
			networkRef:          "docker-api-net"
			networkInstanceRef:  "socket-proxy-node-home-node-a-daemon-docker-home-node-a-network-docker-api-net-interface-docker-api-readonly"
			policyProfile:       "docker-readonly-baseline"
			endpointRef:         "docker-api"
		},
		{
			interfaceRef:        "docker-api-readonly"
			moduleRef:           "socket-proxy"
			unitRef:             "socket-proxy"
			providerInstanceRef: "socket-proxy-node-home-node-b-daemon-docker-home-node-b"
			consumerInstanceRef: "monitoring-agent-node-home-node-b"
			siteRef:             "home"
			nodeRef:             "home-node-b"
			daemonRef:           "docker-default"
			daemonInstanceRef:   "docker-home-node-b"
			networkRef:          "docker-api-net"
			networkInstanceRef:  "socket-proxy-node-home-node-b-daemon-docker-home-node-b-network-docker-api-net-interface-docker-api-readonly"
			policyProfile:       "docker-readonly-baseline"
			endpointRef:         "docker-api"
		},
	]
}

_architectureV2DirectDockerRequirementCheck: #ImplementationInterfaceRequirementV2 & {
	id:       "docker-lifecycle-owner"
	kind:     "docker-socket-direct-v1"
	protocol: "docker-engine"
	version:  "v1"
	endpoint: {visibility: "node-local", transport: "unix-socket", pathSource: "daemon-binding"}
	scopes: ["docker-api:full"]
	coLocation:    "same-node"
	daemonRef:     "docker-rootless"
	policyProfile: "docker-lifecycle-owner"
}

_architectureV2FixedDirectDockerRequirementCheck: #ImplementationInterfaceRequirementV2 & {
	id:       "docker-lifecycle-owner-fixed"
	kind:     "docker-socket-direct-v1"
	protocol: "docker-engine"
	version:  "v1"
	endpoint: {visibility: "node-local", transport: "unix-socket", path: "/run/user/1000/docker.sock"}
	scopes: ["docker-api:full"]
	coLocation:    "same-node"
	daemonRef:     "docker-rootless"
	policyProfile: "docker-lifecycle-owner-fixed"
}
