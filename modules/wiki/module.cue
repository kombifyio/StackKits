package wiki

import "github.com/kombifyio/stackkits/foundation"

Contract: foundation.#ModuleContract & {
	metadata: {name: "wiki", displayName: "kombify Wiki", version: "0.1.0", layer: "L3-application", description: "Personal IT source of truth: one SQLite Wiki and a small binary with a reader and native MCP.", core: false, maturity: "draft"}
	requires: infrastructure: {docker: true, persistentStorage: true, minMemory: "512m"}
	placementSupport: {local_only: true, standard: true, managed_serverless: false}
	provides: capabilities: {"wiki-read": true, "wiki-search": true, "wiki-graph": true}
}
