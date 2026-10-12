// Package opencloud -- OpenCloud files module.
//
// Transitional module contract for the BaseKit files use case.
package opencloud

import "github.com/kombifyio/stackkits/foundation"

_runtimeImages: foundation.ArchitectureV2ModuleImages["stackkits-opencloud-runtime"]

Contract: foundation.#ModuleContract & {
	metadata: {
		name:        "opencloud"
		displayName: "OpenCloud"
		version:     "8.1.0"
		layer:       "L3-application"
		description: "Native file management and sharing with OpenCloud"
		maturity:    "opt-in"
		testScenarios: ["SK-S1", "SK-S2", "SK-S3"]
	}

	delivery: {
		type:      "paas"
		managedBy: "selected-paas"
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy"]
			}
		}
		infrastructure: {
			docker:            true
			persistentStorage: true
			network:           "shared"
			minMemory:         "1g"
		}
	}

	provides: {
		capabilities: {
			"files":            true
			"document-storage": true
			"file-sharing":     true
		}
		endpoints: {
			ui: {
				url:         "https://files.{{.domain}}"
				description: "OpenCloud web UI"
			}
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: opencloud: foundation.#ServiceDefinition & {
		name:        "opencloud"
		displayName: "Files"
		description: "Files and sharing with native filesystem metadata; no SQL service"
		type:        "storage"
		image:       _runtimeImages["opencloud"].image
		tag:         _runtimeImages["opencloud"].tag
		upstream: {
			github: {repo: "opencloud-eu/opencloud"}
		}
		required: false
		status:   "implemented"
		needs: ["traefik"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: true
				rule:    "Host(`files.{{.domain}}`)"
				port:    9200
			}
			networks: ["base_net"]
		}

		accessPolicy: {
			outerAuth:      "tinyauth-pocketid"
			appAuth:        "self-auth"
			ownerBootstrap: "The guarded first-start initializer consumes the node-custodied bootstrap environment once; repeat start preserves the instance identity."
		}

		volumes: [{source: "opencloud-config", target: "/etc/opencloud", type: "volume", backup: true, description: "Initialized instance configuration and private signing state"}, {source: "opencloud-data", target: "/var/lib/opencloud", type: "volume", backup: true, description: "Native file content and filesystem metadata"}]

		healthCheck: {
			enabled: true
			http: {
				path:   "/"
				port:   9200
				scheme: "http"
			}
			interval: "30s"
			timeout:  "5s"
			retries:  5
		}

		config: {
			serviceGroup: "files"
			routeRole:    "files"
			replacementContract: {
				stableRouteKey:  "files"
				stableLocalSlug: "files"
				requiredCapabilities: ["files", "document-storage"]
				bootstrapRequirements: ["create-first-admin", "disable-demo-users"]
				note: "Any replacement files module must keep the files route role and document equivalent owner bootstrap steps."
			}
		}

		resources: {
			memory:    "1g"
			memoryMax: "2g"
			cpus:      1
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		labels: {
			"traefik.enable":                                           "true"
			"traefik.http.routers.opencloud.rule":                      "Host(`files.{{.domain}}`)"
			"traefik.http.routers.opencloud.entrypoints":               "web"
			"traefik.http.services.opencloud.loadbalancer.server.port": "9200"
			"stackkit.layer":                                           "3-application"
			"stackkit.managed-by":                                      "selected-paas"
		}

		subdomain: {key: "files", nested: "files", flat: "files"}
		dashboard: {icon: "&#128193;", order: 60, section: "Applications", badge: "L3 \u00b7 Files", enableVar: "enable_files", guideUrl: "https://docs.kombify.io/guides/stackkits/use-cases/files", vendor: {name: "OpenCloud", website: "https://opencloud.eu", docs: "https://docs.opencloud.eu", license: "Apache-2.0"}}

		output: {
			url:         "https://files.{{.domain}}"
			description: "OpenCloud file sharing"
		}
	}

}
