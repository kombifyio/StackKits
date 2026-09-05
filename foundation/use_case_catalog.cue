// Package foundation -- Dynamic StackKits product use-case catalog.
//
// This is the single product-intent registry for documentation projections.
// It deliberately has no maturity or progress field: implementation progress is
// derived from Package CUE, Architecture v2 workloads/lifecycles, modules, and
// source-SHA-bound evidence by `stackkit docs`.
package foundation

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

// A decision an operator can make about a use case before it is installed.
//
// This is the product's answer to "what can I set and decide?" per use case.
// Until it existed, every consumer surface (the Techstack creation wizard
// first) could only show the same three facts for every use case, because the
// catalog carried nothing else that differs between them (owner finding
// 2026-09-05). Keep each list SHORT: a setting earns its place by being a
// decision a homelab owner actually makes, not by existing in the product.
#UseCaseSettingOption: {
	id:   =~"^[a-z][a-z0-9-]+$"
	name: string & =~"^.+$"
	// One-line consequence of choosing it, customer-facing.
	note?: string & =~"^.+$"
}

#UseCaseSetting: {
	id:   =~"^[a-z][a-z0-9-]+$"
	name: string & =~"^.+$"
	kind: "choice" | "toggle" | "text"
	// Which drawer group it belongs to. Fixed vocabulary so consumers can order
	// and label groups consistently across use cases.
	group: "backend" | "profile" | "storage" | "hardware" | "access" | "features"
	// Whether the summary line of the large card carries it, or only the
	// advanced drawer does. Level two vs level three of the depth model.
	depth: "summary" | "advanced"
	// One sentence, customer-facing, said once next to the control.
	help?: string & =~"^.+$"
	if kind == "choice" {
		options: [...#UseCaseSettingOption] & [_, _, ...]
		default: string
	}
	if kind == "toggle" {
		default: bool
	}
	if kind == "text" {
		default:      string
		placeholder?: string
	}
	// Whether the pinned release applies this decision at install time, or
	// only records it against the homelab for a later release to honour. A
	// consumer shows a recorded setting as such; it never hides it, because
	// the creation surface shows the target state (owner decision 2026-09-05).
	realization: "install" | "recorded"
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

	// Decisions an operator makes about this use case; see #UseCaseSetting.
	// The backend alternative and the compute profile are NOT listed here:
	// consumers derive those from `components` (role alternative) and the
	// package's computeTiers, so they never drift from the workload graph.
	settings?: [...#UseCaseSetting]

	// Path of this use case's guide on docs.kombify.io, when one exists. A
	// consumer with no path falls back to the use-case overview; it never
	// invents a URL.
	docs?: =~"^/[a-z0-9/-]+$"
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
			}
			settings: [
				{
					id:          "device-passthrough"
					name:        "USB devices"
					kind:        "toggle"
					group:       "hardware"
					depth:       "summary"
					help:        "Let Home Assistant use a Zigbee, Z-Wave or other USB stick plugged into this Node."
					default:     true
					realization: "recorded"
				},
				{
					id:          "network-discovery"
					name:        "Find devices on your network"
					kind:        "toggle"
					group:       "access"
					depth:       "advanced"
					help:        "Discover lights, speakers and hubs on the home network automatically."
					default:     true
					realization: "recorded"
				},
			]
		}
		photos: {
			slug:        "photos"
			displayName: "Photos and Memories"
			description: "Family photo and video vault with mobile backup, search, and shared memories."
			owner:       "stackkits"
			components: {
				immich: {id: "immich", name: "Immich", role: "primary", kind: "application"}
			}
			docs: "/guides/stackkits/use-cases/family-photo-vault"
			settings: [
				{
					id:          "machine-learning"
					name:        "Smart search and faces"
					kind:        "toggle"
					group:       "features"
					depth:       "summary"
					help:        "Recognise faces and search photos by what is in them. Needs more memory on the Node."
					default:     true
					realization: "recorded"
				},
				{
					id:    "library-volume"
					name:  "Where photos are stored"
					kind:  "choice"
					group: "storage"
					depth: "advanced"
					help:  "A dedicated disk keeps a growing library off the system disk."
					options: [
						{id: "kit-storage", name: "Kit storage", note: "Shared data volume of this StackKit"},
						{id: "dedicated-disk", name: "Dedicated disk", note: "An extra disk on the Node, just for photos"},
					]
					default:     "kit-storage"
					realization: "recorded"
				},
			]
		}
		media: {
			slug:        "media"
			displayName: "Media Library"
			description: "Private media library and streaming intent for household media collections."
			owner:       "stackkits"
			components: jellyfin: {id: "jellyfin", name: "Jellyfin", role: "primary", kind: "application"}
			settings: [
				{
					id:    "hardware-transcoding"
					name:  "Hardware transcoding"
					kind:  "choice"
					group: "hardware"
					depth: "summary"
					help:  "Use the Node's GPU to convert video for phones and TVs."
					options: [
						{id: "auto", name: "Automatic", note: "Use a GPU when the Node has one"},
						{id: "off", name: "Off", note: "Software only; fine for one stream at a time"},
						{id: "on", name: "Always", note: "Requires a supported GPU"},
					]
					default:     "auto"
					realization: "recorded"
				},
				{
					id:    "library-volume"
					name:  "Where media is stored"
					kind:  "choice"
					group: "storage"
					depth: "advanced"
					help:  "A dedicated disk keeps a large library off the system disk."
					options: [
						{id: "kit-storage", name: "Kit storage", note: "Shared data volume of this StackKit"},
						{id: "dedicated-disk", name: "Dedicated disk", note: "An extra disk on the Node, just for media"},
					]
					default:     "kit-storage"
					realization: "recorded"
				},
			]
		}
		vault: {
			slug:        "vault"
			displayName: "Password Vault"
			description: "Private password and secure-note vault with owner-controlled lifecycle and recovery."
			owner:       "stackkits"
			components: vaultwarden: {id: "vaultwarden", name: "Vaultwarden", role: "primary", kind: "application"}
			settings: [
				{
					id:          "open-signups"
					name:        "Open sign-ups"
					kind:        "toggle"
					group:       "access"
					depth:       "advanced"
					help:        "Let people create their own vault account. Off means the owner invites them."
					default:     false
					realization: "recorded"
				},
			]
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
			docs: "/guides/stackkits/use-cases/private-file-library"
			settings: [
				{
					id:    "library-volume"
					name:  "Where files are stored"
					kind:  "choice"
					group: "storage"
					depth: "advanced"
					help:  "A dedicated disk keeps your files off the system disk."
					options: [
						{id: "kit-storage", name: "Kit storage", note: "Shared data volume of this StackKit"},
						{id: "dedicated-disk", name: "Dedicated disk", note: "An extra disk on the Node, just for files"},
					]
					default:     "kit-storage"
					realization: "recorded"
				},
			]
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
			settings: [
				{
					id:    "accelerator"
					name:  "Accelerator"
					kind:  "choice"
					group: "hardware"
					depth: "summary"
					help:  "Local models run far faster on a GPU."
					options: [
						{id: "cpu", name: "CPU only", note: "Works everywhere; small models only"},
						{id: "nvidia", name: "NVIDIA GPU", note: "Needs the NVIDIA container runtime on the Node"},
						{id: "amd", name: "AMD GPU", note: "ROCm-capable card required"},
					]
					default:     "cpu"
					realization: "recorded"
				},
				{
					id:    "model-size"
					name:  "Model size"
					kind:  "choice"
					group: "features"
					depth: "advanced"
					help:  "Larger models answer better and need more memory."
					options: [
						{id: "small", name: "Small", note: "Around 4 GB of memory"},
						{id: "medium", name: "Medium", note: "Around 8 GB of memory"},
						{id: "large", name: "Large", note: "16 GB of memory or more"},
					]
					default:     "small"
					realization: "recorded"
				},
			]
		}
		dev: {
			slug:        "dev"
			displayName: "Developer Platform"
			description: "Private source control, developer collaboration, and delivery-platform intent."
			owner:       "stackkits"
			components: gitea: {id: "gitea", name: "Gitea", role: "primary", kind: "application"}
			settings: [
				{
					id:          "ci-runners"
					name:        "CI runners"
					kind:        "toggle"
					group:       "features"
					depth:       "summary"
					help:        "Run build and test pipelines on this Node."
					default:     false
					realization: "recorded"
				},
			]
		}
		mail: {
			slug:        "mail"
			displayName: "Private Mail"
			description: "Private mail delivery, mailbox, and communication intent."
			owner:       "stackkits"
			components: stalwart: {id: "stalwart", name: "Stalwart Mail Server", role: "primary", kind: "application"}
			settings: [
				{
					id:          "mail-domain"
					name:        "Mail domain"
					kind:        "text"
					group:       "access"
					depth:       "summary"
					help:        "The domain your addresses end in, e.g. example.com. You need to own it."
					default:     ""
					placeholder: "example.com"
					realization: "recorded"
				},
			]
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
