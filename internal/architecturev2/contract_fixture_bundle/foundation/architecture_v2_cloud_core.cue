package foundation

import "list"

// Cloud Core has one shared service source. The explicit full graph and the
// no-PaaS graph are separate module identities, but they reuse these pinned
// core components instead of maintaining two copied service definitions.
_architectureV2CloudCoreBaseComponents: [
	{
		id: "router", role: "application", lifecycle: "daemon"
		image: {
			ref:    "ghcr.io/traefik/traefik:v3"
			digest: "sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8"
		}
		dependsOn: ["socket-proxy"], networkRefs: ["cloud-core", "cloud-control"]
		health: {kind: "http", path: "/ping", port: 8080}
		resources: {memoryLimit: "256m"}
	},
	{
		id: "socket-proxy", role: "application", lifecycle: "daemon"
		image: {
			ref:    "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2"
			digest: "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
		}
		dependsOn: [], networkRefs: ["cloud-control"]
		health: {kind: "image"}
		resources: {memoryLimit: "128m"}
	},
	{
		id: "pocketid", role: "application", lifecycle: "daemon"
		image: {
			ref:    "ghcr.io/pocket-id/pocket-id:v2.7.0"
			digest: "sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e"
		}
		dependsOn: [], networkRefs: ["cloud-core"]
		volumes: [{id: "pocketid-data", target: "/app/data", class: "persistent", backup: true}]
		health: {kind: "http", path: "/health", port: 1411}
		resources: {memoryLimit: "512m"}
	},
	{
		id: "tinyauth", role: "application", lifecycle: "daemon"
		image: {
			ref:    "ghcr.io/steveiliop56/tinyauth:v5.0.7"
			digest: "sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"
		}
		dependsOn: ["pocketid"], networkRefs: ["cloud-core"]
		volumes: [{id: "tinyauth-data", target: "/data", class: "persistent", backup: true}]
		health: {kind: "command", command: ["tinyauth", "healthcheck"]}
		resources: {memoryLimit: "256m"}
	},
]

_architectureV2CloudCorePlatformComponents: [
	{
		id: "coolify", role: "application", lifecycle: "daemon"
		image: {
			ref:    "ghcr.io/coollabsio/coolify:4.1.2"
			digest: "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"
		}
		dependsOn: ["coolify-postgres", "coolify-redis", "coolify-realtime"], networkRefs: ["cloud-core", "cloud-control"]
		volumes: [
			{id: "coolify-data", target: "/var/www/html/storage", class: "persistent", backup: true},
			{id: "coolify-ssh", target: "/var/www/html/storage/app/ssh", class: "persistent", backup: true},
			{id: "coolify-applications", target: "/var/www/html/storage/app/applications", class: "persistent", backup: true},
			{id: "coolify-databases", target: "/var/www/html/storage/app/databases", class: "persistent", backup: true},
			{id: "coolify-services", target: "/var/www/html/storage/app/services", class: "persistent", backup: true},
			{id: "coolify-backups", target: "/var/www/html/storage/app/backups", class: "persistent", backup: true},
		]
		health: {kind: "http", path: "/api/health", port: 8080}
		resources: {memoryLimit: "1g"}
	},
	{
		id: "coolify-postgres", role: "database", lifecycle: "daemon"
		image: {
			ref:    "docker.io/library/postgres:15-alpine"
			digest: "sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f"
		}
		dependsOn: [], networkRefs: ["cloud-control"]
		volumes: [{id: "coolify-postgres-data", target: "/var/lib/postgresql/data", class: "persistent", backup: true}]
		health: {kind: "command", command: ["pg_isready", "-U", "coolify"]}
		resources: {memoryLimit: "512m"}
	},
	{
		id: "coolify-redis", role: "cache", lifecycle: "daemon"
		image: {
			ref:    "docker.io/library/redis:7-alpine"
			digest: "sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99"
		}
		dependsOn: [], networkRefs: ["cloud-control"]
		volumes: [{id: "coolify-redis-data", target: "/data", class: "persistent", backup: true}]
		health: {kind: "command", command: ["redis-cli", "ping"]}
		resources: {memoryLimit: "256m"}
	},
	{
		id: "coolify-realtime", role: "application", lifecycle: "daemon"
		image: {
			ref:    "ghcr.io/coollabsio/coolify-realtime:1.0.16"
			digest: "sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629"
		}
		dependsOn: ["coolify-redis"], networkRefs: ["cloud-control"]
		health: {kind: "http", path: "/ready", port: 6001}
	},
]

_architectureV2CloudCoreHubComponent: {
	id: "hub", role: "application", lifecycle: "daemon"
	image: {
		ref:    "docker.io/library/nginx:alpine"
		digest: "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"
	}
	dependsOn: ["tinyauth"], networkRefs: ["cloud-core"]
	health: {kind: "http", path: "/healthz", port: 80}
	resources: {memoryLimit: "256m"}
}

_architectureV2CloudCoreStandaloneComponents: list.Concat([_architectureV2CloudCoreBaseComponents, [_architectureV2CloudCoreHubComponent, _architectureV2LocalKopiaComponent & {_networkRef: "cloud-backup"}]])
_architectureV2CloudCoreFullComponents: list.Concat([_architectureV2CloudCoreBaseComponents, _architectureV2CloudCorePlatformComponents, [_architectureV2CloudCoreHubComponent]])

// These contracts remain shared with the explicit full graph. Standalone
// removes only the PaaS-owned route, listener, controls and health source.
_architectureV2CloudCoreHealthContracts: [
	{id: "cloud-router-http", kind: "http", path: "/ping", port: 8080, expectedStatuses: [200]},
	{id: "cloud-pocketid-http", kind: "http", path: "/", port: 1411, expectedStatuses: [200, 302]},
	{id: "cloud-tinyauth-http", kind: "http", path: "/", port: 3000, expectedStatuses: [200, 302]},
	{id: "cloud-coolify-http", kind: "http", path: "/", port: 8080, expectedStatuses: [200, 302]},
	{id: "cloud-hub-http", kind: "http", path: "/healthz", port: 80, expectedStatuses: [200]},
]

_architectureV2CloudStandaloneServiceEndpoints: [
	for endpoint in _cloudCoreServiceEndpoints
	if endpoint.serviceRef != "coolify" {endpoint},
]

_architectureV2CloudStandaloneRuntimeListeners: [
	for listener in _cloudCoreVerificationRuntimeListeners
	if listener.componentRef != "coolify" {listener},
]

_architectureV2CloudStandaloneServiceControls: [
	for control in _cloudCoreServiceControls
	if control.serviceRef != "coolify" {
		key:            control.key
		serviceRef:     control.serviceRef
		adapter:        control.adapter
		runtimeRef:     "cloud-core-standalone"
		componentRefs:  control.componentRefs
		allowedActions: control.allowedActions
		critical:       control.critical
	},
]

_architectureV2CloudStandaloneHealthContracts: [
	for health in _architectureV2CloudCoreHealthContracts
	if health.id != "cloud-coolify-http" {health},
]
