// Package base -- Dynamic StackKits product use-case catalog.
//
// This is the single product-intent registry for documentation projections.
// It deliberately has no maturity or progress field: implementation progress is
// derived from Package CUE, Architecture v2 workloads/lifecycles, modules, and
// source-SHA-bound evidence by `stackkit docs`.
package base

#UseCaseCompletenessGate:
	"product-intent" |
	"use-case-package" |
	"runtime-workload" |
	"component-closure" |
	"delivery-adapter" |
	"setup-network-auth-data-backup" |
	"application-lifecycle" |
	"source-sha-tests" |
	"runtime-evidence" |
	"release-documentation"

#UseCaseCatalogComponent: {
	id:   =~"^[a-z][a-z0-9-]+$"
	name: string & =~"^.+$"
	role: "primary" | "alternative" | "supporting" | "connector" | "bridge"
	kind: "application" | "module" | "service" | "connector" | "bridge"
}

#UseCaseNotApplicable: {
	// An explicit, CUE-owned reason is required so a generator can never hide a
	// missing contract behind an unreviewed not-applicable result.
	reason: string & =~"^.+$"
}

#UseCaseCatalogEntry: {
	slug:        #UseCaseSlug
	displayName: string & =~"^.+$"
	description: string & =~"^.+$"
	owner:       "stackkits"
	components: [ComponentID=#UseCaseSlug]: #UseCaseCatalogComponent & {id: ComponentID}

	// Gates are derived, never manually marked complete. This map is the only
	// permitted exception and is validated to carry an explanatory reason.
	notApplicable?: [#UseCaseCompletenessGate]: #UseCaseNotApplicable

}

#UseCaseCatalog: {
	apiVersion: "stackkits-use-case-catalog/v1"
	entries: [Slug=#UseCaseSlug]: #UseCaseCatalogEntry & {slug: Slug}
}

// UseCaseCatalog is intentionally a map rather than a fixed list. Adding a
// typed entry changes all generated catalog projections without changing any
// generator code or a hand-maintained count.
UseCaseCatalog: #UseCaseCatalog & {
	entries: {
		"smart-home": {
			slug:        "smart-home"
			displayName: "Smart Home"
			description: "Home automation centered on Home Assistant, native product interfaces, and optional local device adjacency."
			owner:       "stackkits"
			components: {
				"home-assistant": {id: "home-assistant", name: "Home Assistant", role: "primary", kind: "application"}
				"kombify-home-bridge": {id: "kombify-home-bridge", name: "Kombify Home Bridge", role: "bridge", kind: "bridge"}
				mosquitto: {id: "mosquitto", name: "Mosquitto", role: "supporting", kind: "service"}
				zigbee2mqtt: {id: "zigbee2mqtt", name: "Zigbee2MQTT", role: "supporting", kind: "service"}
			}
		}
		photos: {
			slug:        "photos"
			displayName: "Photos and Memories"
			description: "Family photo and video vault with mobile backup, search, and shared memories."
			owner:       "stackkits"
			components: {
				immich: {id: "immich", name: "Immich", role: "primary", kind: "application"}
				"ente-photos": {id: "ente-photos", name: "Ente Photos", role: "alternative", kind: "application"}
			}
		}
		media: {
			slug:        "media"
			displayName: "Media Library"
			description: "Private media library and streaming intent for household media collections."
			owner:       "stackkits"
			components: jellyfin: {id: "jellyfin", name: "Jellyfin", role: "primary", kind: "application"}
		}
		vault: {
			slug:        "vault"
			displayName: "Password Vault"
			description: "Private password and secure-note vault with owner-controlled lifecycle and recovery."
			owner:       "stackkits"
			components: vaultwarden: {id: "vaultwarden", name: "Vaultwarden", role: "primary", kind: "application"}
		}
		files: {
			slug:        "files"
			displayName: "File Storage and Documents"
			description: "Private file storage, sharing, collaboration, and document-management workflows."
			owner:       "stackkits"
			components: {
				cloudreve: {id: "cloudreve", name: "Cloudreve", role: "primary", kind: "application"}
				nextcloud: {id: "nextcloud", name: "Nextcloud", role: "alternative", kind: "application"}
			}
		}
		ai: {
			slug:        "ai"
			displayName: "Private AI"
			description: "Private AI workloads, local model serving, and user-facing AI workbench intent."
			owner:       "stackkits"
			components: {
				ollama: {id: "ollama", name: "Ollama", role: "primary", kind: "application"}
				"open-webui": {id: "open-webui", name: "Open WebUI", role: "supporting", kind: "application"}
			}
		}
		dev: {
			slug:        "dev"
			displayName: "Developer Platform"
			description: "Private source control, developer collaboration, and delivery-platform intent."
			owner:       "stackkits"
			components: gitea: {id: "gitea", name: "Gitea", role: "primary", kind: "application"}
		}
		mail: {
			slug:        "mail"
			displayName: "Private Mail"
			description: "Private mail delivery, mailbox, and communication intent."
			owner:       "stackkits"
			components: stalwart: {id: "stalwart", name: "Stalwart Mail Server", role: "primary", kind: "application"}
		}
		game: {
			slug:        "game"
			displayName: "Game Server"
			description: "Self-hosted multiplayer game-server intent for a household or community."
			owner:       "stackkits"
			components: gameserver: {id: "gameserver", name: "Game Server Runtime", role: "primary", kind: "application"}
		}
		remote: {
			slug:        "remote"
			displayName: "Remote Desktop"
			description: "Private remote desktop and browser-accessible workspace intent."
			owner:       "stackkits"
			components: guacamole: {id: "guacamole", name: "Apache Guacamole", role: "primary", kind: "application"}
		}
	}
}
