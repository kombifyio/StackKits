package cloud_kit

_assertDefinitionSlug:        "cloud-kit" & Definition.metadata.slug
_assertCloudOnly:             "cloud" & Definition.topology.allowedSiteKinds[0]
_assertCloudAuthority:        "cloud" & Definition.topology.controlPlane.allowedAuthorityKinds[0]
_assertRoutesClosed:          true & Definition.accessDefaults.publicRoutesDefaultClosed
_assertLANStepDownDenied:     false & Definition.reachability.accessPolicies.lanStepDownAllowed
_assertRemoteRouteCapability: "private-admin-mesh" & Definition.reachability.routes["remote-private"].requiredRealizations[0].capabilityRef
_assertRemoteRouteRole:       "access" & Definition.reachability.routes["remote-private"].requiredRealizations[0].role
_assertPublicRouteCapability: "public-edge" & Definition.reachability.routes.public.requiredRealizations[0].capabilityRef
_assertPublicRouteRole:       "edge" & Definition.reachability.routes.public.requiredRealizations[0].role
_assertPhotosOptional: [for workload in Definition.workloads.optional if workload == "photos" {workload}] & ["photos"]
_assertReachabilityMatrix: Definition.reachability & {
	accessPolicies: {allowedExposures: ["private", "public"], lanStepDownAllowed: false}
	routes: {
		local: {allowed: true, requiredRealizations: [], allowedOriginKinds: ["cloud"]}
		"remote-private": {allowed: true, requiredRealizations: [{capabilityRef: "private-admin-mesh", role: "access"}], allowedOriginKinds: ["cloud"]}
		public: {allowed: true, requiredRealizations: [{capabilityRef: "public-edge", role: "edge"}], allowedOriginKinds: ["cloud"]}
	}
}

_validCloudV2: #CloudKitStackV2 & {
	spec: {
		apiVersion: "stackkit/v2alpha1"
		kind:       "StackSpec"
		metadata: name: "public-edge"
		kit: slug:      "cloud-kit"
		install: {platform: {setupPolicy: {}}}
		generation: {strategy: "kit-template", target: "opentofu"}
		network: {
			mode: "public-capable"
			domain: {base: "example.net"}
			transport: {}
			dns: {}
			tls: {defaultMode: "public"}
		}
		sites: [{id: "cloud-eu", kind: "cloud", failureDomain: "eu-central-1a"}]
		nodes: [
			{
				id:      "main"
				siteRef: "cloud-eu"
				roles: ["controller", "worker", "edge"]
				hardware: {}
				failureDomain: "vps-a"
			},
			{
				id:      "worker"
				siteRef: "cloud-eu"
				roles: ["worker"]
				hardware: {}
				failureDomain: "vps-b"
			},
		]
		controlPlane: {authoritySiteRef: "cloud-eu", members: ["main"]}
		capabilities: enable: ["private-admin-mesh"]
		access: {
			"private-owner": {
				exposure:               "private"
				authentication:         "human+device"
				enrolledDeviceRequired: true
			}
			"public-owner": {
				exposure:               "public"
				authentication:         "human+device"
				enrolledDeviceRequired: true
			}
		}
		routes: {
			"console-local": {
				serviceRef:      "console"
				exposure:        "local"
				protocol:        "tcp"
				port:            8080
				accessPolicyRef: "private-owner"
			}
			"console-remote": {
				serviceRef:      "console"
				exposure:        "remote-private"
				protocol:        "tcp"
				port:            8080
				accessPolicyRef: "private-owner"
			}
			"console-public": {
				serviceRef:      "console"
				exposure:        "public"
				protocol:        "https"
				port:            443
				host:            "console.example.net"
				path:            "/"
				accessPolicyRef: "public-owner"
			}
		}
	}
}
