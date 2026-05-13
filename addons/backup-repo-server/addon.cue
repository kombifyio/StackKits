// Package backup_repo_server - Kopia Repository Server Add-On
//
// Deploys Kopia's built-in Repository Server as a Layer-2 platform service.
// Acts as the central fan-in for many Backup-Agents (one per host) in the
// kombify-Backup-SaaS path: each tenant gets a per-user ACL into the same
// physical storage backend, so cross-tenant deduplication keeps storage
// cost low while access remains isolated.
//
// Self-hosted users do NOT need this addon. The local kopia-agent + Kopia
// Web UI in addons/backup are sufficient for a single host. This addon
// only matters when:
//   - the kombify-Backup-Controller (internal/backup-controller) drives the
//     fleet, or
//   - a power user wants to fan multiple of their own hosts into one repo.
//
// License: Apache-2.0 (Kopia upstream).
//
// Storage backend: any backend Kopia supports. We default to S3 / B2 /
// Hetzner-Storagebox (SFTP) — same options the per-host addon offers.
//
// Authentication model: Kopia Repository Server users (one per fleet/host
// pair, provisioned by the controller). The server is also fronted by
// Traefik + login-gateway (TinyAuth + PocketID forward-auth) for any
// browser-driven status views, but agent-to-server traffic uses Kopia's
// own user/password authentication on a separate route.
//
// Usage:
//   addons: backup-repo-server: backup_repo_server.#Config & {
//       storage: provider: "b2"
//       storage: b2: bucket: "kombify-backups"
//   }

package backup_repo_server

// #Config defines the repository-server addon configuration.
//
// Most knobs the user might expect (per-tenant ACLs, schedules, retention)
// are NOT here — those are owned by the kombify-Backup-Controller, which
// provisions users and policies on this server via the Kopia management
// API. The addon's job is to stand up the server with a healthy storage
// backend and the right auth.
#Config: {
	_addon: {
		name:        "backup-repo-server"
		displayName: "Backup Repository Server"
		version:     "1.0.0"
		layer:       "PLATFORM"
		description: "Kopia Repository Server for multi-host / multi-tenant fan-in"
	}

	_compatibility: {
		stackkits: ["base-kit", "modern-homelab", "ha-kit"]
		contexts:  ["cloud", "local"]
		// Logical dependency only — the surrounding stack must have a domain
		// and Traefik so we can expose the server.
		requires: ["traefik"]
		conflicts: []
	}

	enabled: bool | *true

	// Storage backend the repository writes to. Same provider set as
	// addons/backup, by design — a future Phase-4 host can be onboarded
	// against either a local kopia-agent or this server without changing
	// what physical storage holds the data.
	storage: #StorageBackend

	// Repository password (root key for the storage layer). Per-user
	// passwords are layered on top by the controller and are NOT this
	// value.
	repositoryPassword: =~"^secret://"

	// Network exposure.
	exposure: #ExposureConfig

	// Server-side cache size hint (bytes). Defaults to 8 GiB; bump for
	// big fleets. The cache lives on a named volume so a container
	// restart does not warm-start from cold.
	cacheBytes: int | *(8 * 1024 * 1024 * 1024)
}

#StorageBackend: {
	provider: *"b2" | "hetzner-storagebox" | "s3"

	if provider == "b2" {
		b2: {
			bucket:     string
			accountId:  =~"^secret://"
			accountKey: =~"^secret://"
		}
	}

	if provider == "hetzner-storagebox" {
		hetzner: {
			host:     string
			user:     string
			password: =~"^secret://"
			path:     string | *"/repo"
		}
	}

	if provider == "s3" {
		s3: {
			endpoint:  string
			bucket:    string
			accessKey: =~"^secret://"
			secretKey: =~"^secret://"
			region:    string | *"us-east-1"

			// Object-Lock is recommended for the SaaS Business tier.
			// The controller decides per-tenant policy; this knob just
			// turns the bucket-level capability on.
			objectLockEnabled: bool | *true
		}
	}
}

#ExposureConfig: {
	// Subdomain hosting the Kopia management/UI.
	uiSubdomain: string | *"backup-repo"

	// Subdomain hosting the agent endpoint (HTTPS). Separate from the UI
	// so we can apply different middlewares — agents authenticate with
	// per-user credentials on a basic-auth-like flow, not the
	// login-gateway forward-auth.
	agentSubdomain: string | *"backup-repo-agent"

	// UI is always behind the platform's login-gateway (TinyAuth +
	// PocketID). Hard-wired; not a knob.
	uiAuthRequired: true
}

// =============================================================================
// SERVICE DEFINITIONS
// =============================================================================

#KopiaRepoServerService: {
	name:        "kopia-repo-server"
	displayName: "Kopia Repository Server"
	image:       "kopia/kopia:0.18"
	category:    "backup"

	placement: {
		// Single-instance, on a host with reliable bandwidth to the
		// storage backend. The orchestrator places it on the cloud
		// node when one exists; otherwise on `main`.
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 51515, host: 51515, protocol: "tcp", name: "ui"},
		{container: 51516, host: 51516, protocol: "tcp", name: "agent"},
	]

	volumes: [
		{name: "repo-server-config", path: "/app/config", type: "volume"},
		{name: "repo-server-cache", path: "/app/cache", type: "volume"},
	]

	environment: {
		KOPIA_PASSWORD: =~"^secret://"
	}

	traefik: {
		enabled: true
		// Two routes, two middleware stacks. The UI route inherits the
		// platform login-gateway; the agent route relies on Kopia's own
		// per-user auth (no forward-auth on top).
		routes: [
			{
				name: "ui"
				rule: "Host(`{{.repoServer.exposure.uiSubdomain}}.{{.domain}}`)"
				port: 51515
				// login-gateway middleware appended automatically because
				// #ExposureConfig.uiAuthRequired is true.
			},
			{
				name: "agent"
				rule: "Host(`{{.repoServer.exposure.agentSubdomain}}.{{.domain}}`)"
				port: 51516
				// No forward-auth: agents present Kopia user credentials.
			},
		]
	}
}

// #Outputs is what this add-on exports to the deployment summary.
#Outputs: {
	uiUrl:    string | *"https://{{.repoServer.exposure.uiSubdomain}}.{{.domain}}"
	agentUrl: string | *"https://{{.repoServer.exposure.agentSubdomain}}.{{.domain}}"
	provider: string
}
