// Package foundation -- Dynamic StackKits product use-case catalog.
//
// This is the single product-intent registry for documentation projections.
// It deliberately has no maturity or progress field: implementation progress is
// derived from Package CUE, Architecture v2 workloads/lifecycles, modules, and
// source-SHA-bound evidence by `stackkit docs`.
package foundation

import "list"

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
	// Overrides the setting's realization for this option, so a recorded
	// setting can still say that its default choice is what installs.
	realization?: "install" | "recorded"
	// The server memory in GB this option needs to run well. A consumer that
	// knows the target server recommends the largest option that fits.
	minMemoryGb?: int & >0
	// The option needs a GPU or other accelerator to be usable in practice.
	needsAccelerator?: bool
	// The option widens exposure; it can never be the setting's default.
	widens?: #UseCaseWidening
}

// How a setting or option widens a use case beyond the safe default
// (docs/USE_CASE_STANDARD.md D4): reachable without an account, open account
// creation, or user data sent to an outside provider.
#UseCaseWidening: "public-access" | "self-signup" | "external-data"

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
		// D4: the default option never widens exposure.
		_safeDefault: [for option in options if option.id == default && option.widens != _|_ {option.id}] & []
	}
	if kind == "toggle" {
		default: bool
	}

	// D4: a toggle that widens exposure is off by default.
	widens?: #UseCaseWidening
	if widens != _|_ {
		kind:    "toggle"
		default: false
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

	// The Architecture v2 application workload this toggle selects in the
	// StackSpec when it is on (an add-on such as a music server, or another
	// use case such as the document archive). Such a toggle installs.
	workloadRef?: =~"^[a-z][a-z0-9-]+$"
	if workloadRef != _|_ {
		kind:        "toggle"
		realization: "install"
	}

	// The Architecture v2 module whose accelerator profile this choice
	// selects: every option except the default is the ID of one of the
	// module's acceleratorProfiles, applied with `stackkit init
	// --module-accelerator-profile <module>=<option>`. The default option
	// selects no profile (CPU). The catalog generator checks the options.
	acceleratorProfileOf?: =~"^[a-z][a-z0-9-]+$"
	if acceleratorProfileOf != _|_ {
		kind:        "choice"
		realization: "install"
	}
}

// One tool that can realize a capability module.
#UseCaseCapabilityOption: {
	id:    =~"^[a-z][a-z0-9-]+$" & !="off"
	name:  string & =~"^.+$"
	note?: string & =~"^.+$"

	// install: the pinned release installs the option through the named
	// Architecture v2 application workload alternative. recorded: the option is
	// planned; authoring refuses it until its workload lands, never a silent
	// no-op. Several options may share one workload alternative (a bundle).
	realization: "install" | "recorded"
	// pending names what a recorded option waits for; authoring repeats it in
	// the refusal so the owner learns the exact blocker, not only "planned".
	pending?: string & =~"^.+$"
	if pending != _|_ {
		realization: "recorded"
	}
	workloadRef?:    =~"^[a-z][a-z0-9-]+$"
	alternativeRef?: =~"^[a-z][a-z0-9-]+$"
	if realization == "install" {
		workloadRef:    =~"^[a-z][a-z0-9-]+$"
		alternativeRef: =~"^[a-z][a-z0-9-]+$"
	}
	if workloadRef != _|_ || alternativeRef != _|_ {
		realization: "install"
	}

	// alternativeRefs lists every alternative of workloadRef that installs
	// this tool (a bundle shared by several alternatives, such as Ollama in
	// both chat compositions). alternativeRef is the one resolved when no
	// other module of the use case decides the workload; another module may
	// pick any listed alternative without a conflict.
	alternativeRefs?: [...=~"^[a-z][a-z0-9-]+$"] & list.MinItems(1)
	if alternativeRefs != _|_ {
		realization:                    "install"
		_alternativeRefsIncludePrimary: list.Contains(alternativeRefs, alternativeRef) & true
		_alternativeRefsUnique:         list.UniqueItems(alternativeRefs) & true
	}
}

// A capability module is an independently selectable part of one use case
// (for example speech or image generation inside Private AI). Its ID is a
// stable authoring identity: `stackkit init --use-case-capability
// <use-case>.<capability>=<option|off>`. Each has one default tool and at
// most one alternative. Selections are closed: unknown IDs, unmet
// dependencies, recorded options and contradictory workload choices are
// refused. Selecting the use case without a capability choice resolves the
// enabledByDefault composition, which must equal the use case's previous
// selection so existing authoring keeps its exact StackSpec.
#UseCaseCapabilityModule: {
	id:    =~"^[a-z][a-z0-9-]+$"
	name:  string & =~"^.+$"
	help?: string & =~"^.+$"
	// A required capability is part of every selection; it cannot be off.
	required:         bool | *false
	enabledByDefault: bool | *false
	if required {
		enabledByDefault: true
	}

	// Capabilities that must be enabled together with this one.
	requires: [...=~"^[a-z][a-z0-9-]+$"] | *[]
	// The realizing module must run with an explicit accelerator profile.
	requiresAccelerator: bool | *false
	default:             #UseCaseCapabilityOption
	alternative?: #UseCaseCapabilityOption & {id: !=default.id}
	_notSelfRequired: [for ref in requires if ref == id {ref}] & []
}

// A bundle groups capability modules of one use case under one customer-facing
// choice (owner request 2026-10-13: Private AI offers five bundles instead of
// ten modules). It is catalog grouping only: turning a bundle on selects its
// defaults through the same `--use-case-capability` authoring, so StackSpec,
// resolver and workloads never see a bundle. A use case that declares bundles
// places every capability module in exactly one of them.
#UseCaseBundle: {
	id:    =~"^[a-z][a-z0-9-]+$"
	name:  string & =~"^.+$"
	help?: string & =~"^.+$"
	// Concrete things a person does with the bundle, in customer language.
	examples?: [...string & =~"^.+$"] & list.MinItems(1)
	// The capability modules this bundle groups, in display order. A module
	// several bundles need may appear in each of them; it installs once.
	capabilities: [...=~"^[a-z][a-z0-9-]+$"] & list.MinItems(1)
	// The modules turning the bundle on selects; the others stay individual
	// choices inside it. A bundle that is on by default selects exactly its
	// enabledByDefault modules, so the default composition never changes.
	defaults: [...=~"^[a-z][a-z0-9-]+$"] & list.MinItems(1)
	// Derived facts, checked by the catalog generator: a bundle is required
	// when it holds a required module and on by default when it holds a module
	// that is on by default.
	required:         bool | *false
	enabledByDefault: bool | *false
	if required {
		enabledByDefault: true
	}

	// For experienced owners only; consumers never preselect it.
	advanced: bool | *false
	if advanced {
		enabledByDefault: false
	}

	// Consumers recommend the bundle when the Node has a GPU; it still runs
	// without one.
	recommendsAccelerator: bool | *false
	// Its installing modules run in a gVisor sandbox; the host needs the runsc
	// runtime (`stackkit host remediate --apply gvisor-runsc`).
	requiresSandbox:     bool | *false
	_capabilitiesUnique: list.UniqueItems(capabilities) & true
	_defaultsUnique:     list.UniqueItems(defaults) & true
	_defaultsInBundle: [for ref in defaults {list.Contains(capabilities, ref) & true}]
}

// The owner-accepted main use cases, including Marketplace (2026-10-10),
// keyed by id. Several catalog slugs share one main use case
// until their compatibility migrations merge them (files + documents, dev +
// remote); network has no catalog entry yet.
#MainUseCaseTitles: {
	photos:            "Photos"
	"documents-files": "Documents & Files"
	vault:             "Vault"
	media:             "Media"
	"smart-home":      "Smart Home"
	dev:               "Dev"
	mail:              "Mail"
	game:              "Game"
	network:           "Network"
	automation:        "Automation"
	ai:                "AI"
	marketplace:       "Marketplace"
}

#MainUseCase: {
	id: or([for id, _ in #MainUseCaseTitles {id}])
	title: #MainUseCaseTitles[id]
}

#UseCaseCatalogEntry: {
	slug:        #UseCaseSlug
	displayName: string & =~"^.+$"
	description: string & =~"^.+$"
	owner:       "stackkits"
	components: [ComponentID=#UseCaseSlug]: #UseCaseCatalogComponent & {id: ComponentID}

	// The main use case this entry is grouped under on consumer surfaces. Set
	// only the id; the title is derived from #MainUseCaseTitles.
	mainUseCase: #MainUseCase

	// Gates are derived, never manually marked complete. This map is the only
	// permitted exception and is validated to carry an explanatory reason.
	notApplicable?: [#UseCaseCompletenessGate]: #UseCaseNotApplicable

	// Decisions an operator makes about this use case; see #UseCaseSetting.
	// The backend alternative and the compute profile are NOT listed here:
	// consumers derive those from `components` (role alternative) and the
	// package's computeTiers, so they never drift from the workload graph.
	settings?: [...#UseCaseSetting]

	// Independently selectable capability modules; see
	// #UseCaseCapabilityModule. Only a use case composed of such modules
	// declares them.
	capabilities?: [...#UseCaseCapabilityModule]
	if capabilities != _|_ {
		_capabilityIDsUnique: list.UniqueItems([for capability in capabilities {capability.id}]) & true
		_capabilityRequiresKnown: [for capability in capabilities for ref in capability.requires {
			matches: [for candidate in capabilities if candidate.id == ref {ref}] & list.MinItems(1)
		}]
	}

	// Customer-facing groups of the capability modules; see #UseCaseBundle.
	bundles?: [...#UseCaseBundle] & list.MinItems(2)
	if bundles != _|_ {
		_bundleIDsUnique: list.UniqueItems([for bundle in bundles {bundle.id}]) & true
		_bundledCapabilities: [for bundle in bundles for ref in bundle.capabilities {ref}]
		// A module in no bundle stays in the catalog but setup does not offer
		// it; the generator checks that every required or default module is
		// in a bundle.
		_bundledCapabilitiesKnown: [for ref in _bundledCapabilities {list.Contains([for capability in capabilities {capability.id}], ref) & true}]
	}

	// Path of this use case's guide on docs.kombify.io. Entries sharing a main
	// use case share its guide.
	docs: =~"^/[a-z0-9/-]+$"
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
			mainUseCase: id: "smart-home"
			docs: "/guides/stackkits/use-cases/smart-home"
			components: {
				"home-assistant": {id: "home-assistant", name: "Home Assistant", role: "primary", kind: "application"}
				mosquitto: {id: "mosquitto", name: "Eclipse Mosquitto", role: "supporting", kind: "service"}
				zigbee2mqtt: {id: "zigbee2mqtt", name: "Zigbee2MQTT", role: "supporting", kind: "application"}
				esphome: {id: "esphome", name: "ESPHome", role: "supporting", kind: "application"}
			}
			settings: [
				{
					id:    "operating-form"
					name:  "Home Assistant installation"
					kind:  "choice"
					group: "profile"
					depth: "summary"
					help:  "Choose Container or a dedicated Home Assistant OS appliance. OS requires a separately admitted appliance target."
					options: [
						{id: "container", name: "Container", note: "Home Assistant Core with operator-managed host services; no Supervisor or Apps."},
						{id: "haos", name: "Home Assistant OS", note: "Dedicated appliance with Supervisor, Apps and native system maintenance."},
					]
					default:     "container"
					realization: "install"
				},
				{
					id:    "instance-origin"
					name:  "Existing installation"
					kind:  "choice"
					group: "profile"
					depth: "summary"
					help:  "An existing instance is observed without changing accounts, integrations or configuration. Migration is a separate backup-protected operation."
					options: [
						{id: "new", name: "Create new"},
						{id: "existing", name: "Connect existing", note: "Preserve the original configuration; management requires a separate grant."},
					]
					default:     "new"
					realization: "install"
				},
				{
					id:          "device-passthrough"
					name:        "USB devices"
					kind:        "toggle"
					group:       "hardware"
					depth:       "summary"
					help:        "Let Home Assistant use a Zigbee, Z-Wave or other USB stick plugged into this Node."
					default:     false
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
				{
					id:          "mqtt-broker"
					name:        "MQTT broker"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Add Eclipse Mosquitto so MQTT devices, Zigbee2MQTT and ESPHome can talk to Home Assistant."
					default:     false
					realization: "install"
					workloadRef: "smart-home-mqtt"
				},
				{
					id:          "zigbee-bridge"
					name:        "Zigbee devices"
					kind:        "toggle"
					group:       "features"
					depth:       "summary"
					help:        "Add Zigbee2MQTT to use Zigbee lights and sensors without vendor hubs. You choose the Zigbee USB adapter and the MQTT broker address."
					default:     false
					realization: "install"
					workloadRef: "smart-home-zigbee"
				},
				{
					id:          "esphome"
					name:        "ESPHome devices"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Add ESPHome to build and update firmware for your own ESP32 sensors and switches."
					default:     false
					realization: "install"
					workloadRef: "smart-home-esphome"
				},
			]
		}
		photos: {
			slug:        "photos"
			displayName: "Photos and Memories"
			description: "Family photo and video vault with mobile backup, search, and shared memories."
			owner:       "stackkits"
			mainUseCase: id: "photos"
			docs: "/guides/stackkits/use-cases/photos"
			components: {
				immich: {id: "immich", name: "Immich", role: "primary", kind: "application"}
				"immich-lite": {id: "immich-lite", name: "Immich Lite (no smart search)", role: "alternative", kind: "application"}
				"ente-photos": {id: "ente-photos", name: "Ente Photos", role: "alternative", kind: "application"}
				"immich-public-proxy": {id: "immich-public-proxy", name: "Immich Public Proxy", role: "supporting", kind: "application"}
				"immich-kiosk": {id: "immich-kiosk", name: "Immich Kiosk", role: "supporting", kind: "application"}
				"immich-power-tools": {id: "immich-power-tools", name: "Immich Power Tools", role: "supporting", kind: "application"}
			}
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
				{
					id:          "public-sharing"
					name:        "Public share links"
					kind:        "toggle"
					group:       "access"
					depth:       "summary"
					help:        "Add Immich Public Proxy so people without an account can open the albums you share, while Immich itself stays private."
					widens:      "public-access"
					default:     false
					realization: "install"
					workloadRef: "photos-share"
				},
				{
					id:          "kiosk"
					name:        "Photo frame and TV slideshow"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Add Immich Kiosk to show albums or people as a slideshow on a photo frame, TV or tablet."
					default:     false
					realization: "install"
					workloadRef: "photos-kiosk"
				},
				{
					id:          "power-tools"
					name:        "Library power tools"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Add Immich Power Tools for bulk work: merge people, fix missing locations and tidy albums."
					default:     false
					realization: "install"
					workloadRef: "photos-tools"
				},
			]
		}
		media: {
			slug:        "media"
			displayName: "Media Library"
			description: "Private media library and streaming intent for household media collections."
			owner:       "stackkits"
			mainUseCase: id: "media"
			docs: "/guides/stackkits/use-cases/media"
			components: {
				jellyfin: {id: "jellyfin", name: "Jellyfin", role: "primary", kind: "application"}
				emby: {id: "emby", name: "Emby", role: "alternative", kind: "application"}
				navidrome: {id: "navidrome", name: "Navidrome", role: "supporting", kind: "application"}
				audiobookshelf: {id: "audiobookshelf", name: "Audiobookshelf", role: "supporting", kind: "application"}
			}
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
				{
					id:          "music-server"
					name:        "Music streaming"
					kind:        "toggle"
					group:       "features"
					depth:       "summary"
					help:        "Add Navidrome to stream your music library to Subsonic-compatible apps."
					default:     false
					realization: "install"
					workloadRef: "media-music"
				},
				{
					id:          "audiobooks"
					name:        "Audiobooks and podcasts"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Add Audiobookshelf for audiobooks and podcasts with progress sync on your phone."
					default:     false
					realization: "install"
					workloadRef: "media-audiobooks"
				},
			]
		}
		vault: {
			slug:        "vault"
			displayName: "Password Vault"
			description: "Private password and secure-note vault with owner-controlled lifecycle and recovery."
			owner:       "stackkits"
			mainUseCase: id: "vault"
			docs: "/guides/stackkits/use-cases/vault"
			components: {
				vaultwarden: {id: "vaultwarden", name: "Vaultwarden", role: "primary", kind: "application"}
				passbolt: {id: "passbolt", name: "Passbolt CE", role: "alternative", kind: "application"}
			}
			settings: [
				{
					id:          "open-signups"
					name:        "Open sign-ups"
					kind:        "toggle"
					group:       "access"
					depth:       "advanced"
					help:        "Let people create their own vault account. Off means the owner invites them."
					widens:      "self-signup"
					default:     false
					realization: "recorded"
				},
			]
		}
		files: {
			slug:        "files"
			displayName: "File Storage and Documents"
			description: "Private file storage and sharing: Cloudreve by default, Nextcloud or kombify Paperwork as alternatives, with optional office editing and a document archive."
			owner:       "stackkits"
			mainUseCase: id: "documents-files"
			docs: "/guides/stackkits/use-cases/documents-and-files"
			// Owner direction 2026-09-25: Nextcloud and Paperwork are Documents &
			// Files alternatives; Euro-Office adds browser office editing and
			// Paperless-ngx the document archive.
			components: {
				cloudreve: {id: "cloudreve", name: "Cloudreve", role: "primary", kind: "application"}
				nextcloud: {id: "nextcloud", name: "Nextcloud", role: "alternative", kind: "application"}
				opencloud: {id: "opencloud", name: "OpenCloud", role: "alternative", kind: "application"}
				// kombify Paperwork has no distributable self-hosted edition; the
				// alternative binds the owner's hosted Paperwork instance.
				paperwork: {id: "paperwork", name: "kombify Paperwork", role: "alternative", kind: "connector"}
				"euro-office": {id: "euro-office", name: "Euro-Office", role: "supporting", kind: "application"}
				"paperless-ngx": {id: "paperless-ngx", name: "Paperless-ngx", role: "supporting", kind: "application"}
			}
			settings: [
				{
					id:          "paperless"
					name:        "Document archive"
					kind:        "toggle"
					group:       "features"
					depth:       "summary"
					help:        "Add Paperless-ngx to scan, read (OCR) and search your paper documents next to your files."
					default:     false
					realization: "install"
					workloadRef: "documents"
				},
				{
					id:          "office-editing"
					name:        "Office editing"
					kind:        "toggle"
					group:       "features"
					depth:       "summary"
					help:        "Add Euro-Office to edit documents, spreadsheets and presentations in the browser. It connects to Nextcloud through the Euro-Office app."
					default:     false
					realization: "install"
					workloadRef: "files-office"
				},
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
			mainUseCase: id: "ai"
			docs: "/guides/stackkits/use-cases/personal-ai"
			components: {
				ollama: {id: "ollama", name: "Ollama", role: "primary", kind: "application"}
				"open-webui": {id: "open-webui", name: "Open WebUI", role: "supporting", kind: "application"}
				anythingllm: {id: "anythingllm", name: "AnythingLLM", role: "alternative", kind: "application"}
				searxng: {id: "searxng", name: "SearXNG", role: "supporting", kind: "application"}
				tika: {id: "tika", name: "Apache Tika", role: "supporting", kind: "application"}
				docling: {id: "docling", name: "Docling", role: "supporting", kind: "application"}
				comfyui: {id: "comfyui", name: "ComfyUI", role: "supporting", kind: "application"}
				hermes: {id: "hermes", name: "Hermes Agent", role: "supporting", kind: "application"}
				speechkit: {id: "speechkit", name: "kombify SpeechKit", role: "supporting", kind: "application"}
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
						{id: "nvidia", name: "NVIDIA GPU", note: "Needs the NVIDIA driver 550 or newer; StackKits sets up the NVIDIA Container Toolkit"},
						{id: "amd", name: "AMD GPU", note: "ROCm-capable card with the amdgpu driver; amd64 only"},
					]
					default:              "cpu"
					realization:          "install"
					acceleratorProfileOf: "stackkits-private-ai-runtime"
				},
				{
					id:    "model-size"
					name:  "Model size"
					kind:  "choice"
					group: "features"
					depth: "advanced"
					help:  "Larger models answer better and need more memory."
					options: [
						// minMemoryGb is the whole server, not the model alone:
						// the model plus the system and the other modules.
						{id: "small", name: "Small", note: "Around 4 GB of memory, e.g. Qwen 3.5 4B", minMemoryGb: 8},
						{id: "medium", name: "Medium", note: "Around 8 GB of memory, e.g. Qwen 3.5 9B", minMemoryGb: 16},
						{id: "large", name: "Large", note: "16 GB of memory or more, e.g. Qwen 3.5 27B", minMemoryGb: 32, needsAccelerator: true},
					]
					default: "small"
					// The ai workload setting of the same id: the size of every
					// selected model pack (reviewed tags in the Private AI renderer).
					realization: "install"
				},
				{
					// Owner request 2026-10-10: offer "only my own interface" on top
					// of a provider the operator already pays for. Recorded until an
					// external-inference composition (chat without Ollama) lands.
					id:    "inference-location"
					name:  "Where the models run"
					kind:  "choice"
					group: "profile"
					depth: "summary"
					help:  "Run models on your own hardware, or use only your own interface with a provider you already pay for."
					options: [
						{id: "local", name: "On my own hardware", note: "Models run on this Node; private, no per-message cost", realization: "install"},
						{id: "external", name: "Only my own interface", widens: "external-data", note: "Your own chat on top of OpenAI, Anthropic or another provider; no models on your hardware"},
					]
					default:     "local"
					realization: "recorded"
				},
				{
					// Owner request 2026-10-10: choose which models are pulled during
					// setup. Each pack toggle is the ai workload setting of the same id
					// (private-ai chat alternative): Apply pulls the selected packs into
					// Ollama and verifies them; a failed pull fails the Apply.
					id:          "model-pack-general"
					name:        "General chat model"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Download an everyday chat model during setup so the first chat works offline."
					default:     true
					realization: "install"
				},
				{
					id:          "model-pack-coding"
					name:        "Coding model"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Download a code model for the agent harness and your editor."
					default:     false
					realization: "install"
				},
				{
					// Owner decision 2026-10-14: ComfyUI must not depend on a GPU.
					// Without one it can generate through external providers (the
					// ComfyUI API nodes, signed in inside ComfyUI with the owner's own
					// Comfy.org account). The ai-image-video workload setting of the
					// same id; StackKits stores no credential.
					id:          "comfyui-api-nodes"
					name:        "External image and video providers"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Let ComfyUI generate through external providers, so it works without a GPU. Off by default: nothing leaves the Node. When on, you sign in to your own Comfy.org account inside ComfyUI, prompts and images are sent to those providers, and their use may cost money on your provider account."
					widens:      "external-data"
					default:     false
					realization: "install"
				},
				{
					id:          "model-pack-vision"
					name:        "Vision model"
					kind:        "toggle"
					group:       "features"
					depth:       "advanced"
					help:        "Download a model that understands images you drop into the chat."
					default:     false
					realization: "install"
				},
			]
			// docs/use-case-expansion/ai-agents.md "Plan (owner request
			// 2026-09-27)". Inference and chat keep the existing private-ai
			// composition by default; chat=anythingllm selects the second
			// alternative of the same ai workload (Ollama plus AnythingLLM),
			// which is why the Ollama option lists both alternatives. Web
			// search and document parsing install as separate add-on
			// workloads wired into the chat module; authoring refuses an
			// add-on the selected chat module cannot use. Every other module
			// is recorded until its workload lands. Speech is wired (workload
			// ai-speech, alternative speechkit, chat wiring) but stays recorded:
			// no SpeechKit release contains the OpenAI-compatible audio routes
			// of SpeechKit #638 yet and ghcr.io/kombifyio/speechkit-server is
			// not publicly pullable. The flip is documented in
			// docs/use-case-expansion/ai-agents.md.
			// Owner decision 2026-10-14 (AI use-case review section 4): four
			// bundles, each a goal a person has for AI at home. A module two
			// bundles need (web search) is installed once. Speech, agent
			// harness and agent control plane sit in no bundle: they stay in
			// the catalog but setup no longer offers them. Creative studio has
			// no GPU condition: ComfyUI runs on the CPU, on an NVIDIA GPU or,
			// opted in, through external providers.
			bundles: [
				{
					id:   "local-ai"
					name: "My local AI"
					help: "Own and run your AI models on your hardware, offline, with no subscription and no outside provider."
					examples: ["Load models from a reviewed list and compare them", "Keep one model for coding and one that understands images", "Use your own AI from your phone, editor or Home Assistant with a token"]
					capabilities: ["inference", "observability"]
					defaults: ["inference"]
					required: true
				},
				{
					id:   "private-chat"
					name: "My private Chat"
					help: "Write, ask and think with an AI without your data leaving the house."
					examples: ["Draft an email, translate or summarise", "Ask questions with web search", "Ask questions about your own PDFs, with one account per family member"]
					capabilities: ["chat", "web-search", "document-parsing"]
					defaults: ["chat"]
					enabledByDefault: true
				},
				{
					id:   "creative-studio"
					name: "Creative studio"
					help: "Create images and improve old photos, on the CPU, a GPU or an optional external provider."
					examples: ["Design an invitation card or an avatar", "Upscale an old photo", "Create an image from the chat or a short clip from an image"]
					capabilities: ["image-video"]
					defaults: ["image-video"]
				},
				{
					id:   "personal-assistant"
					name: "My personal assistant"
					help: "Takes small tasks off your hands and remembers things; it drafts first and every further permission is yours to grant."
					examples: ["Summarise appointments and weather each morning", "Remind you and prepare research", "Draft replies and reach you through a messenger"]
					capabilities: ["assistant", "web-search"]
					defaults: ["assistant"]
					advanced: true
				},
			]
			capabilities: [
				{
					id:       "inference"
					name:     "Inference"
					help:     "Serves the models you download; every other AI module uses this local endpoint."
					required: true
					default: {id: "ollama", name: "Ollama", realization: "install", workloadRef: "ai", alternativeRef: "private-ai", alternativeRefs: ["private-ai", "anythingllm"]}
					alternative: {id: "llama-cpp", name: "llama.cpp server", note: "For small or non-NVIDIA nodes", realization: "recorded"}
				},
				{
					id:               "chat"
					name:             "Chat and knowledge"
					help:             "Chat with your local models and your own documents."
					enabledByDefault: true
					requires: ["inference"]
					default: {id: "open-webui", name: "Open WebUI", realization: "install", workloadRef: "ai", alternativeRef: "private-ai"}
					alternative: {id: "anythingllm", name: "AnythingLLM", note: "Workspaces with built-in RAG and agents, multi-user mode; parses documents itself, so Tika and Docling are not selectable with it", realization: "install", workloadRef: "ai", alternativeRef: "anythingllm"}
				},
				{
					id:   "speech"
					name: "Speech"
					help: "Speak to your assistant and hear answers; recognition runs on your device where it can."
					requires: ["inference"]
					default: {id: "speechkit", name: "kombify SpeechKit", note: "Local speech providers only", realization: "recorded", pending: "waiting for a SpeechKit release with OpenAI audio endpoints and a public image"}
				},
				{
					id:   "web-search"
					name: "Web search"
					help: "Private meta search for chat and the assistant."
					requires: ["inference"]
					default: {id: "searxng", name: "SearXNG", realization: "install", workloadRef: "ai-search", alternativeRef: "searxng"}
				},
				{
					id:   "document-parsing"
					name: "Document parsing"
					help: "Reads PDFs and office files so chat can answer from them."
					requires: ["inference", "chat"]
					default: {id: "tika", name: "Apache Tika", note: "Text documents; no OCR", realization: "install", workloadRef: "ai-documents", alternativeRef: "tika"}
					alternative: {id: "docling", name: "Docling", note: "For scanned and complex layouts; needs more memory", realization: "install", workloadRef: "ai-documents", alternativeRef: "docling"}
				},
				{
					id:   "image-video"
					name: "Image and video"
					help: "Create and edit images and short videos from reviewed workflow templates."
					requires: ["inference"]
					default: {id: "comfyui", name: "ComfyUI", note: "No GPU needed: runs on the CPU (slow; upscaling, and text-to-image with 32 GB RAM), on an NVIDIA GPU with 8 GB for fast images (video needs 16 GB of VRAM), or through optional external providers with the comfyui-api-nodes setting", realization: "install", workloadRef: "ai-image-video", alternativeRef: "comfyui"}
					alternative: {id: "invokeai", name: "InvokeAI", realization: "recorded"}
				},
				{
					id:   "assistant"
					name: "Personal assistant"
					help: "An assistant with memory that drafts and inspects first; every further permission is a separate grant."
					requires: ["inference"]
					default: {id: "hermes", name: "Hermes Agent", note: "Experimental; one owner, inspect and draft by default", realization: "install", workloadRef: "ai-assistant", alternativeRef: "hermes"}
					// OpenBot needs a CopilotKit Intelligence server and API key and has
					// no degraded mode; self-hosting Intelligence is a paid CopilotKit
					// plan, so it stays recorded.
					alternative: {id: "openbot", name: "OpenBot", note: "Supervised AI coworker with its own workspace; needs a paid CopilotKit Intelligence plan to self-host", realization: "recorded"}
				},
				{
					id:   "agent-harness"
					name: "Agent harness"
					help: "Task and coding agents that run only inside isolated workspaces."
					requires: ["inference"]
					// OpenHands installs as the add-on workload ai-harness in a
					// gVisor-isolated container; the host needs the runsc runtime
					// (stackkit host remediate --apply gvisor-runsc). Goose stays
					// recorded: it ships a desktop app, a CLI and the goosed
					// companion server, but no self-hostable multi-user web
					// surface or official container image for a node workload.
					default: {id: "openhands", name: "OpenHands", note: "Runs in a gVisor sandbox; the host needs the runsc runtime", realization: "install", workloadRef: "ai-harness", alternativeRef: "openhands"}
					alternative: {id: "goose", name: "Goose", note: "Desktop app and CLI; no self-hostable web surface yet", realization: "recorded"}
				},
				{
					id:   "agent-control-plane"
					name: "Agent control plane"
					help: "Organizes agents, their budgets and approvals."
					// Paperclip installs as the add-on workload ai-control-plane
					// with its own PostgreSQL in a gVisor-isolated container (the
					// host needs the runsc runtime). It drives the installed Hermes
					// through its hermes_gateway adapter; Paperclip ships no
					// OpenHands adapter, so the harness is not a dependency.
					// Langflow stays recorded: its custom Python components run
					// in-process before any sandbox and it needs its own module.
					requires: ["inference"]
					default: {id: "paperclip", name: "Paperclip", note: "Runs in a gVisor sandbox; the host needs the runsc runtime", realization: "install", workloadRef: "ai-control-plane", alternativeRef: "paperclip"}
					alternative: {id: "langflow", name: "Langflow", note: "Visual agent and flow builder; runs flow code in-process", realization: "recorded"}
				},
				{
					id:   "observability"
					name: "Model router and API access"
					help: "One OpenAI-compatible endpoint with a token for your apps and devices."
					requires: ["inference"]
					// LiteLLM installs as the add-on workload ai-gateway in front
					// of the node's Ollama; apps and devices authenticate with
					// its master key. Langfuse stays recorded: tracing needs its
					// own PostgreSQL and ClickHouse stack.
					default: {id: "litellm", name: "LiteLLM", note: "Apps and devices use the master key from owner custody", realization: "install", workloadRef: "ai-gateway", alternativeRef: "litellm"}
					alternative: {id: "langfuse", name: "Langfuse", note: "Traces and budgets", realization: "recorded", pending: "needs its own PostgreSQL and ClickHouse stack"}
				},
			]
		}
		dev: {
			slug:        "dev"
			displayName: "Developer Platform"
			description: "Private source control, developer collaboration, and delivery-platform intent."
			owner:       "stackkits"
			mainUseCase: id: "dev"
			docs: "/guides/stackkits/use-cases/development"
			components: {
				gitea: {id: "gitea", name: "Gitea", role: "primary", kind: "application"}
				forgejo: {id: "forgejo", name: "Forgejo", role: "alternative", kind: "application"}
			}
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
		documents: {
			slug:        "documents"
			displayName: "Documents"
			description: "Private document ingestion, OCR, indexing and search through Paperless-ngx."
			owner:       "stackkits"
			mainUseCase: id: "documents-files"
			docs: "/guides/stackkits/use-cases/documents-and-files"
			components: "paperless-ngx": {id: "paperless-ngx", name: "Paperless-ngx", role: "primary", kind: "application"}
		}
		mail: {
			slug:        "mail"
			displayName: "Private Mail"
			description: "Private webmail through Roundcube for a mailbox you already have at an external provider. No mail server, DNS or MX record is created."
			owner:       "stackkits"
			mainUseCase: id: "mail"
			docs: "/guides/stackkits/use-cases/mail"
			components: {
				roundcube: {id: "roundcube", name: "Roundcube Webmail", role: "primary", kind: "application"}
				// Owner direction 2026-09-25: the hosted Paperwork mail client is a
				// Mail alternative; it deploys nothing on the Node.
				paperwork: {id: "paperwork", name: "kombify Paperwork", role: "alternative", kind: "connector"}
			}
			settings: [
				{
					id:    "hosting"
					name:  "Mail hosting"
					kind:  "choice"
					group: "backend"
					depth: "summary"
					help:  "Keep your mailbox at your current provider, or run your own mail server on a dedicated Cloud node."
					options: [
						{id: "existing", name: "Existing provider", note: "No mail server, DNS or MX change"},
						{id: "stalwart", name: "Stalwart", note: "Own mail server: SMTP, IMAP and JMAP in one service"},
						{id: "mailcow", name: "mailcow", note: "Own mail suite with SOGo webmail, calendar and contacts"},
					]
					default:     "existing"
					realization: "recorded"
				},
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
		// ADR-0046: the own mail server is a second, separately selectable
		// part of the Mail main use case next to the mail client.
		"mail-server": {
			slug:        "mail-server"
			displayName: "Own Mail Server"
			description: "Your own mail server on a dedicated Cloud node through Stalwart: mailboxes for your domain with SMTP, IMAP and submission. StackKits prints the DNS records you publish; it never changes DNS."
			owner:       "stackkits"
			mainUseCase: id: "mail"
			docs: "/guides/stackkits/use-cases/mail"
			components: {
				stalwart: {id: "stalwart", name: "Stalwart Mail Server", role: "primary", kind: "application"}
				mailcow: {id: "mailcow", name: "mailcow", role: "alternative", kind: "application"}
			}
		}
		game: {
			slug:        "game"
			displayName: "Game Server"
			description: "Self-hosted game servers for friends and family through Calagopus (or Pelican or Pterodactyl), with curated Minecraft Java, Paper and Bedrock, Terraria and Valheim profiles."
			owner:       "stackkits"
			mainUseCase: id: "game"
			docs: "/guides/stackkits/use-cases/game"
			components: {
				calagopus: {id: "calagopus", name: "Calagopus Panel", role: "primary", kind: "application"}
				pelican: {id: "pelican", name: "Pelican Panel", role: "alternative", kind: "application"}
				pterodactyl: {id: "pterodactyl", name: "Pterodactyl", role: "alternative", kind: "application"}
			}
			settings: [
				{
					id:    "edition"
					name:  "Minecraft edition"
					kind:  "choice"
					group: "profile"
					depth: "summary"
					help:  "Java and Bedrock players cannot join each other's worlds; choose the edition your players use."
					options: [
						{id: "java", name: "Minecraft Java", note: "PC and Mac players"},
						{id: "bedrock", name: "Minecraft Bedrock", note: "Console, phone, tablet and Windows players"},
					]
					default:     "java"
					realization: "recorded"
				},
			]
		}
		network: {
			slug: "network", displayName: "Network", description: "Filter DNS for selected clients while preserving the existing local resolver and device DNS choices.", owner: "stackkits", mainUseCase: id: "network", docs: "/guides/stackkits/use-cases/network"
			components: "adguard-home": {id: "adguard-home", name: "AdGuard Home", role: "primary", kind: "application"}
		}
		remote: {
			slug:        "remote"
			displayName: "Remote Desktop"
			description: "Private remote desktop and browser-accessible workspace intent."
			owner:       "stackkits"
			mainUseCase: id: "dev"
			docs: "/guides/stackkits/use-cases/development"
			components: guacamole: {id: "guacamole", name: "Apache Guacamole", role: "primary", kind: "application"}
			// Stateless by design: GUACAMOLE_HOME is tmpfs, connections come from the
			// StackSpec and destination credentials are prompted, never stored.
			notApplicable: "setup-network-auth-data-backup": reason: "No persistent user data: Guacamole runs from tmpfs with connections rendered from the StackSpec and prompted destination credentials; the owner password is node custody; setup is the declared owner-login check."
		}
	}
}
