package basement_kit

_assertDefinitionSlug:        "basement-kit" & Definition.metadata.slug
_assertHomeOnly:              "home" & Definition.topology.allowedSiteKinds[0]
_assertLocalAuthority:        "home" & Definition.topology.controlPlane.allowedAuthorityKinds[0]
_assertBridgeOptional:        false & Definition.bridge.required
_assertLANStepDown:           true & Definition.reachability.accessPolicies.lanStepDownAllowed
_assertRemoteRouteCapability: "private-remote-access" & Definition.reachability.routes["remote-private"].requiredCapabilities[0]
_assertPublicRouteCapability: "public-publish-egress" & Definition.reachability.routes.public.requiredCapabilities[0]
_assertPhotosOptional: [for capability in Definition.capabilities.optional if capability == "photos" {capability}] & ["photos"]
_assertReachabilityMatrix: Definition.reachability & {
	accessPolicies: {allowedExposures: ["private", "lan", "public"], lanStepDownAllowed: true}
	routes: {
		local: {allowed: true, requiredCapabilities: [], allowedOriginKinds: ["home"]}
		"remote-private": {allowed: true, requiredCapabilities: ["private-remote-access"], allowedOriginKinds: ["home"]}
		public: {allowed: true, requiredCapabilities: ["public-publish-egress"], allowedOriginKinds: ["home"]}
	}
}

_validBasementV2: #BasementKitStackV2 & {
	spec: {
		apiVersion: "stackkit/v2alpha1"
		kind:       "StackSpec"
		metadata: name: "family-home"
		kit: slug:      "basement-kit"
		install: {platform: {setupPolicy: {}}}
		generation: {strategy: "kit-template", target: "opentofu"}
		network: {
			mode: "private"
			domain: {base: "home.localhost"}
			transport: {}
			dns: {}
			tls: {defaultMode: "internal"}
		}
		sites: [{id: "home", kind: "home", failureDomain: "house-a"}]
		nodes: [{
			id:      "main"
			siteRef: "home"
			roles: ["controller", "worker"]
			hardware: {}
			failureDomain: "host-main"
		}]
		controlPlane: {authoritySiteRef: "home", members: ["main"]}
		capabilities: enable: ["private-remote-access", "public-publish-egress"]
		access: {
			"lan-device": {
				exposure:               "lan"
				authentication:         "device"
				enrolledDeviceRequired: true
				lanStepDown:            true
			}
			"remote-owner": {
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
			"dashboard-local": {
				serviceRef:      "dashboard"
				exposure:        "local"
				protocol:        "tcp"
				port:            8080
				accessPolicyRef: "lan-device"
			}
			"dashboard-remote": {
				serviceRef:      "dashboard"
				exposure:        "remote-private"
				protocol:        "tcp"
				port:            8080
				accessPolicyRef: "remote-owner"
			}
			"dashboard-public": {
				serviceRef:      "dashboard"
				exposure:        "public"
				protocol:        "https"
				port:            443
				host:            "dashboard.example.net"
				path:            "/"
				accessPolicyRef: "public-owner"
			}
		}
		deviceEnrollment: {
			mode:                      "local-only"
			authoritySiteRef:          "home"
			endpointExposure:          "lan"
			remoteEnrollment:          false
			requireOwnerStepUp:        true
			requireLocalPairingProof:  true
			requireDeviceGeneratedKey: true
			requirePossessionProof:    true
			hardwareBackedKey:         "preferred"
			revocationSupported:       true
			credentialTTLSeconds:      3600
		}
	}
}
