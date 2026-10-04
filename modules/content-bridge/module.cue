// Package content_bridge describes the node side of the kombify Content
// Bridge (HOMELAB-CONTENT-BRIDGE-STANDARD, docs/ADR/ADR-0047).
//
// The bridge answers consented, read-only counts of the self-hosted apps of
// one server (Photos from Immich first) to kombify Cloud and the Companion.
// It is a separate component: not kombify Guard, never in Guard's channel,
// never behind the StackKit identity gateway. It is opt-in and off by
// default; nothing is read before the owner turns a use case on in Cloud or
// the Companion.
//
// Like the Core stackkit-server (ADR-0044) it runs the release's own binary,
// `stackkit-content-bridge`, which Apply stages beside compose.yaml and
// mounts read-only into a digest-pinned minimal base image. The binary links
// only the standard library.
package content_bridge

import "github.com/kombifyio/stackkits/foundation"

Contract: foundation.#ModuleContract & {
	metadata: {
		name:        "content-bridge"
		displayName: "kombify Content Bridge"
		version:     "0.1.0"
		layer:       "L3-application"
		description: "Read-only, consent-gated homelab content counts (Photos from Immich) for kombify Cloud and the Companion"
		core:        false
		maturity:    "opt-in"
	}

	requires: {
		services: {
			// The first adapter reads Immich over its internal network. The
			// bridge is installable only beside a Photos workload.
			immich: {
				provides: ["photos"]
				optional: false
			}
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy"]
			}
		}
		infrastructure: {
			docker:            true
			dockerSocket:      false
			persistentStorage: false
			minMemory:         "32m"
		}
	}

	provides: {
		capabilities: {
			"homelab-content": true
		}
		middleware: {
			"content-bridge-ratelimit": {
				type:    "ratelimit"
				average: 5
				burst:   20
			}
		}
		endpoints: {
			summary: {
				url:         "https://base.{{.domain}}/content/v1/summary"
				description: "Consent-tiered homelab content summary (homelab-content/v1); answers only the tier the Gateway grants"
			}
		}
	}

	placementSupport: {
		local_only:         true
		standard:           true
		managed_serverless: false
	}

	services: "content-bridge": foundation.#ServiceDefinition & {
		name:        "content-bridge"
		displayName: "kombify Content Bridge"
		type:        "api"
		// Runtime base only. The executable is the stackkit-content-bridge of
		// the release that applied it, staged by Apply (ADR-0044 pattern).
		image:    "docker.io/library/alpine"
		tag:      "3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6"
		required: false
		// The module contract is complete; Apply does not render it yet.
		status: "planned"
		needs: ["traefik"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			// The router reaches the bridge over the bridge's own internal
			// network. No port is published on the host.
			traefik: {
				enabled: true
				// A path prefix of the base host: there is no new hostname.
				// Only /content/v1 is routed; /healthz stays unrouted.
				rule: "Host(`base.{{.domain}}`) && PathPrefix(`/content/v1`)"
				port: 8083
				middlewares: ["content-bridge-ratelimit"]
			}
			// The Immich app network (to read) and a network of its own
			// (for the router). Both are `internal: true`, so the bridge has
			// no route to the internet; see config.egress.
			networks: ["immich-internal", "content-bridge-internal"]
		}

		// No TinyAuth forward-auth: kombify's Gateway is the only caller and
		// cannot complete a browser login. The credential is the signed
		// installation envelope (verification is not implemented yet), and
		// until it exists the bridge answers a tier only to a caller on its own
		// loopback.
		accessPolicy: {
			outerAuth: "none-explicit"
			appAuth:   "none"
			reason:    "Called only by the kombify Gateway, which cannot complete a browser login. The route is rate limited, GET-only and answers counts only at the consent tier granted in the Gateway's signed installation envelope; without a grant it returns 403 and reads nothing."
		}

		environment: {
			CONTENT_BRIDGE_LISTEN:     ":8083"
			CONTENT_BRIDGE_IMMICH_URL: "http://immich-server:2283"
			// The identifier of the linked installation, supplied by Apply.
			CONTENT_BRIDGE_HOMELAB_ID: "{{.homelab_id}}"
			// The credential arrives as a file, never as an environment value that
			// container inspection would expose (see config.secretFiles).
			CONTENT_BRIDGE_IMMICH_API_KEY_FILE: "/run/secrets/immich-api-key"
		}

		healthCheck: {
			enabled: true
			test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8083/healthz"]
			interval: "10s"
			timeout:  "3s"
			retries:  6
		}

		resources: {
			memory:    "32m"
			memoryMax: "64m"
		}

		// House hardening block (`stackkit module lint`): a read-only root
		// file system, every capability dropped, no privilege escalation.
		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			readOnly: true
			tmpfs: ["/tmp"]
		}
		securityContext: {
			runAsNonRoot:           true
			runAsUser:              10083
			runAsGroup:             10083
			readOnlyRootFilesystem: true
			capabilitiesDrop: ["ALL"]
			noNewPrivileges: true
		}

		config: {
			// Issued by Immich on the node into owner-signed local custody
			// (localevidence.StoreLocalIssuedSecret) and delivered as a read-only
			// file. The key carries only asset.statistics, album.statistics and
			// memory.statistics (Immich 2.7), so it can count and nothing else.
			secretFiles: [{
				slot:            "immich-api-key"
				ref:             "secret://workloads/photos-content-bridge/immich-api-key"
				target:          "/run/secrets/immich-api-key"
				pathEnvironment: "CONTENT_BRIDGE_IMMICH_API_KEY_FILE"
				permissions: ["asset.statistics", "album.statistics", "memory.statistics"]
			}]
			// Verified by Verify: the bridge has no route out and its logs
			// carry no payload fields.
			egress: "none"
			internalNetworks: ["content-bridge-internal"]
			http: {methods: ["GET"]}
			// The only upstream requests the bridge can make; enforced in the
			// HTTP client's transport, not by convention.
			upstream: {
				app: "immich"
				methods: ["GET"]
				paths: [
					"/api/assets/statistics",
					"/api/albums/statistics",
					"/api/memories/statistics",
				]
			}
			cache: {maxSeconds: 60, storesPayload: false}
			logging: {fields: ["use_case", "status", "tier", "latency_ms"], payload: false}
		}

		labels: {
			"stackkit.layer":      "3-application"
			"stackkit.managed-by": "stackkit"
		}

		output: description: "kombify Content Bridge: consented, read-only homelab content counts"
	}
}
