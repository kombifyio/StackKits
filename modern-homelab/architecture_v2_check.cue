package modern_homelab

_assertDefinitionSlug:         "modern-homelab" & Definition.metadata.slug
_assertHomeRequired:           "home" & Definition.topology.requiredSiteKinds[0]
_assertCloudRequired:          "cloud" & Definition.topology.requiredSiteKinds[1]
_assertLocalAuthority:         "home" & Definition.topology.controlPlane.allowedAuthorityKinds[0]
_assertBridgeRequired:         true & Definition.bridge.required
_assertLANStepDown:            true & Definition.reachability.accessPolicies.lanStepDownAllowed
_assertRemoteAccessCapability: "private-remote-access" & Definition.reachability.routes["remote-private"].requiredRealizations[0].capabilityRef
_assertRemoteAccessRole:       "access" & Definition.reachability.routes["remote-private"].requiredRealizations[0].role
_assertPublicEdgeCapability:   "public-edge" & Definition.reachability.routes.public.requiredRealizations[0].capabilityRef
_assertPublicEdgeRole:         "edge" & Definition.reachability.routes.public.requiredRealizations[0].role
_assertPhotosOptional: [for workload in Definition.workloads.optional if workload == "photos" {workload}] & ["photos"]
_assertReachabilityMatrix: Definition.reachability & {
	accessPolicies: {allowedExposures: ["private", "lan", "public"], lanStepDownAllowed: true}
	routes: {
		local: {allowed: true, requiredRealizations: [], allowedOriginKinds: ["home"]}
		"remote-private": {
			allowed: true, requiredRealizations: [{capabilityRef: "private-remote-access", role: "access"}], allowedOriginKinds: ["home"]
		}
		public: {
			allowed: true, requiredRealizations: [{capabilityRef: "public-edge", role: "edge"}], allowedOriginKinds: ["cloud"]
		}
	}
}

_validModernV2: #ModernHomelabStackV2 & {
	spec: {
		apiVersion: "stackkit/v2alpha1"
		kind:       "StackSpec"
		metadata: name: "family-hybrid"
		kit: slug:      "modern-homelab"
		install: {platform: {setupPolicy: {}}}
		generation: {strategy: "kit-template", target: "opentofu"}
		network: {
			mode: "hybrid"
			domain: {base: "example.net"}
			transport: {}
			dns: {}
			tls: {defaultMode: "public"}
		}
		sites: [
			{id: "home", kind: "home", failureDomain: "house-a"},
			{id: "edge", kind: "cloud", failureDomain: "eu-central-1a"},
		]
		nodes: [
			{
				id:      "home-main"
				siteRef: "home"
				roles: ["controller", "worker", "storage"]
				hardware: {}
				failureDomain: "home-host-a"
			},
			{
				id:      "cloud-edge"
				siteRef: "edge"
				roles: ["edge", "worker"]
				hardware: {}
				failureDomain: "cloud-zone-a"
			},
		]
		controlPlane: {authoritySiteRef: "home", members: ["home-main"]}
		capabilities: enable: []
		workloads: photos: {
			alternative: "immich"
			placement: {siteRefs: ["home"], nodeRefs: ["home-main"]}
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
			hardwareBackedKey:         "required"
			revocationSupported:       true
			credentialTTLSeconds:      3600
		}
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
			"owner-device": {
				exposure:               "public"
				privilege:              "user"
				authentication:         "human+device"
				enrolledDeviceRequired: true
				ownerStepUpRequired:    false
				lanStepDown:            false
				allowedMethods: ["GET", "HEAD"]
			}
		}
		routes: {
			"photos-local": {
				moduleRef:       "stackkits-immich-runtime"
				serviceRef:      "photos"
				exposure:        "local"
				protocol:        "http"
				port:            8080
				targetPort:      2283
				host:            "photos.home.test"
				path:            "/"
				accessPolicyRef: "lan-device"
			}
			"photos-remote": {
				moduleRef:       "stackkits-immich-runtime"
				serviceRef:      "photos"
				exposure:        "remote-private"
				protocol:        "https"
				port:            443
				targetPort:      2283
				host:            "photos.remote.example.net"
				path:            "/"
				accessPolicyRef: "remote-owner"
			}
			"photos-public": {
				moduleRef:       "stackkits-immich-runtime"
				serviceRef:      "photos"
				exposure:        "public"
				protocol:        "https"
				port:            443
				targetPort:      2283
				host:            "photos.example.net"
				path:            "/"
				accessPolicyRef: "owner-device"
			}
		}
		bridge: {
			overlay: {
				contractRef: "outbound-private-mesh"
				trafficMode: "policy-scoped"
				peerSiteRefs: ["home", "edge"]
			}
			publications: [{
				serviceRef:    "photos"
				sourceSiteRef: "home"
				edgeSiteRef:   "edge"
				host:          "photos.example.net"
				protocol:      "https"
				port:          443
				path:          "/"
				defaultClosed: true
				tls: {required: true, mode: "terminate-at-edge", minVersion: "TLS1.3"}
				auth: {required: true, policyRef: "owner-device"}
				origin: {identityRef: "photos-origin", mtlsRequired: true}
				rateLimit: {enabled: true, requests: 120, windowSeconds: 60}
			}]
			policy: {
				defaultDeny: true
				allowedFlows: [{
					fromSiteRef: "edge"
					toSiteRef:   "home"
					serviceRef:  "photos"
					protocol:    "http"
					ports: [2283]
					methods: ["GET", "HEAD"]
					dataClasses: ["personal"]
					serviceIdentityRequired: true
				}]
				cloudMayEnrollDevices:          false
				cloudMayIssueDeviceCredentials: false
			}
			controlAgent: {
				enabled: true
				actionAllowlist: ["plan", "apply", "verify"]
			}
		}
		data: {
			defaultAuthority: "home"
			bindings: photos: {
				classes: ["personal"]
				primarySiteRef: "home"
				replicaSiteRefs: []
				cloudCopyAllowed: false
			}
		}
	}
}
