package architecturev2renderer

// nativeDockerPilotModules is the closed set of modules an operator may opt
// into native execution (STACKKIT_OPENTOFU_NATIVE_MODULES). Admission needs
// the module's full Compose document inside the renderer's translation and
// its own parity receipts; secret delivery alone does
// not admit a module. Admission is opt-in only: no default changes.
var nativeDockerPilotModules = map[string]struct{}{
	"stackkits-vaultwarden-runtime": {},
	// S2.1: Paperless-ngx, PostgreSQL and Valkey (health checks, depends_on,
	// command, NAME_FILE secrets).
	"stackkits-paperless-runtime": {},
	// Mosquitto: the LAN listener and the governed command's Compose secret
	// file (provider upload).
	"stackkits-mosquitto-runtime": {},
	// Stalwart: the published mail ports, an egress network and the governed
	// entrypoint's Compose secret file (provider upload).
	"stackkits-stalwart-runtime": {},
	// zigbee2mqtt: the USB device and the MQTT password reference file, a
	// Compose secret file (provider upload) on both executions.
	"stackkits-zigbee2mqtt-runtime": {},
	// Modules whose secrets the fixed-path readers cover.
	// SearXNG: the secret settings file.
	"stackkits-searxng-runtime": {},
	// Roundcube: the des_key file and the governed project files.
	"stackkits-roundcube-runtime": {},
	// Euro-Office: the JWT secret pre-seeded in the data volume.
	"stackkits-euro-office-runtime": {},
	// Wave 1 single-container modules without secrets.
	"stackkits-cloudreve-runtime":      {},
	"stackkits-navidrome-runtime":      {},
	"stackkits-audiobookshelf-runtime": {},
	"stackkits-esphome-runtime":        {},
	"stackkits-tika-runtime":           {},
	"stackkits-docling-runtime":        {},
	"stackkits-emby-runtime":           {},
}

// nativeDockerNoImageHealthcheck names, per module, the daemon services
// whose pinned image declares no HEALTHCHECK and whose Compose service sets
// none. Read from the image configuration at the pinned digest; a service
// missing here renders `wait`, and the provider then fails the apply instead
// of skipping the health wait.
var nativeDockerNoImageHealthcheck = map[string]map[string]bool{
	// eclipse-mosquitto 2.1.2-alpine: no HEALTHCHECK; health is the HTTP API.
	"stackkits-mosquitto-runtime": {"mosquitto": true},
	// zigbee2mqtt 2.14.1, searxng 2026.9.25-12f8b6515, roundcubemail
	// 1.6.19-apache, euro-office documentserver v9.3.4-hotfix.1: no
	// HEALTHCHECK; health is the catalog's HTTP probe.
	"stackkits-zigbee2mqtt-runtime": {"zigbee2mqtt": true},
	"stackkits-searxng-runtime":     {"searxng": true},
	"stackkits-roundcube-runtime":   {"roundcube": true},
	"stackkits-euro-office-runtime": {"euro-office": true},
	// cloudreve 4.18.0, navidrome 0.64.2, audiobookshelf 2.36.1, tika
	// 4.0.0-1, docling-serve-cpu v1.35.0,
	// embyserver 4.10.0.40: no HEALTHCHECK; health is the catalog's HTTP probe. esphome 2026.9.0
	// declares one (curl /version), so its container renders `wait`.
	"stackkits-cloudreve-runtime":      {"cloudreve": true},
	"stackkits-navidrome-runtime":      {"navidrome": true},
	"stackkits-audiobookshelf-runtime": {"audiobookshelf": true},
	"stackkits-tika-runtime":           {"tika": true},
	"stackkits-docling-runtime":        {"docling": true},
	"stackkits-emby-runtime":           {"emby": true},
}

// NativeDockerPilotModule reports whether moduleRef has a native renderer.
func NativeDockerPilotModule(moduleRef string) bool {
	_, ok := nativeDockerPilotModules[moduleRef]
	return ok
}
