package wiki

import "github.com/kombifyio/stackkits/foundation"

Contract: foundation.#ModuleContract & {
	metadata: {name: "wiki", displayName: "kombify Wiki", version: "0.1.0", layer: "L3-application", description: "Personal IT source of truth: one SQLite Wiki and a small binary with a reader and native MCP.", core: false, maturity: "draft"}
	requires: infrastructure: {docker: true, persistentStorage: true, minMemory: "512m"}
	placementSupport: {local_only: true, standard: true, managed_serverless: false}
	provides: capabilities: {"wiki-read": true, "wiki-search": true, "wiki-graph": true}
}

// AppModuleV3 is the #AppModuleV3 declaration of wiki (ADR-0055 Phase 2A).
AppModuleV3: foundation.#AppModuleV3 & {
	let _images = foundation.AppModuleV3CatalogImages["stackkits-wiki-runtime"]
	apiVersion:     "stackkit.app-module/v3"
	moduleRef:      "stackkits-wiki-runtime"
	workloadRef:    "wiki"
	alternativeRef: "wiki"
	release:        "git-092c18ccacfaa36acfa7cefa424c1136c0092481"
	entryComponent: "wiki"
	delivery: {contract: "opentofu-stage1", resourcePrefix: "workload"}
	lifecycle: {}
	route: {
		serviceRef:        "wiki"
		targetPort:        8484
		upstreamProtocol:  "http"
		ingressAuth:       "forward-auth"
		healthRef:         "wiki-http"
		requiredPrivilege: "user"
		allowedExposures: ["local", "remote-private", "public"]
	}
	secrets: {
		"read-bearer": purpose: "Custody secret slot read-bearer for wiki"
		"write-bearer": purpose: "Custody secret slot write-bearer for wiki"
	}
	backup: {
		volumes: [
			{componentRef: "wiki", volumeRef: "state"},
		]
		hookRef:  "hook/quiesce-stop"
		probeRef: "probe/http-status"
	}
	networks: {
		"wiki-internal": {}
	}
	components: [
		{
			id:        "wiki"
			role:      "application"
			lifecycle: "daemon"
			image:     _images["wiki"]
			entrypoint: ["/app/wiki"]
			command: ["serve", "-initialize"]
			environment: {
				WIKI_FILE: "/var/lib/wiki/wiki.db"
				WIKI_LISTEN: "0.0.0.0:8484"
			}
			secretEnvironment: {
				WIKI_READ_BEARER: "read-bearer"
				WIKI_WRITE_BEARER: "write-bearer"
			}
			volumes: [
				{id: "state", target: "/var/lib/wiki", class: "persistent", backup: true},
			]
			networks: ["wiki-internal"]
			egress: true
			health: {kind: "http", path: "/health", port: 8484}
			resources: {memoryLimit: "512m", memoryReservation: "128m"}
			labels: {}
			traits: {}
		},
	]
}
