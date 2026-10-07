package architecturev2renderer

const (
	calagopusWorkloadModuleID = "stackkits-calagopus-runtime"
	calagopusPanelPort        = 8000
)

var calagopusSecretSlots = []string{"database-password", "encryption-key", "owner-password", "application-api-key", "client-api-key"}

// Custody files of the governed Calagopus entrypoints and bootstrap steps.
var (
	calagopusPanelSecretFiles = []selectedPaaSSecretFile{
		custodyFile("database-password", "STACKKIT_DB_PASSWORD_FILE"),
		custodyFile("encryption-key", "STACKKIT_ENCRYPTION_KEY_FILE"),
	}
	calagopusOwnerSecretFiles = append(append([]selectedPaaSSecretFile{}, calagopusPanelSecretFiles...),
		custodyFile("owner-password", "STACKKIT_OWNER_PASSWORD_FILE"))
	calagopusKeysSecretFiles = []selectedPaaSSecretFile{
		custodyFile("database-password", "STACKKIT_DB_PASSWORD_FILE"),
		custodyFile("application-api-key", "STACKKIT_APPLICATION_API_KEY_FILE"),
		custodyFile("client-api-key", "STACKKIT_CLIENT_API_KEY_FILE"),
	}
	calagopusNodeSecretFiles = []selectedPaaSSecretFile{custodyFile("application-api-key", "STACKKIT_APPLICATION_API_KEY_FILE")}
)

const calagopusWorkloadRendererSchema = `stackkit.workload-bundle/v2|CalagopusWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:panel,panel-database,panel-cache,panel-bootstrap,panel-keys,wings-bootstrap,wings|wings:docker-socket-direct-v1/lifecycle-owner,firewall-disabled,panel-updates-ignored|game-data:self-path|panel:wings-proxy|eggs:` + GameVanillaMinecraftEggSHA256 + `,` + GamePaperEggSHA256 + `,` + PterodactylBedrockEggSHA256 + `,` + PterodactylTerrariaEggSHA256 + `,` + PterodactylValheimEggSHA256 + `|release:` + calagopusPanelRelease + `|secret-material:not-included`

var calagopusPlatform = gamePlatform{
	name: "Calagopus", alternative: "calagopus", moduleID: calagopusWorkloadModuleID, unitID: "calagopus",
	providerRef: "stackkits-calagopus", kind: "CalagopusWorkloadBundle",
	templateRef: "builtin://workloads/calagopus/bundle/v1.json", outputRef: "workloads/calagopus/bundle.json",
	healthRef: "calagopus-panel-http", approvalID: "approve-calagopus-wings-lifecycle-owner",
	approvalEvidence: "calagopus-wings-lifecycle-owner-governance",
	release:          calagopusPanelRelease, schema: calagopusWorkloadRendererSchema, routePort: calagopusPanelPort,
	entryImage:  selectedPaaSRuntimeImage{Ref: calagopusPanelImageRef, Digest: calagopusPanelImageDigest},
	secretSlots: calagopusSecretSlots, components: calagopusComponents, configFiles: calagopusConfigFiles,
}

// CalagopusWorkloadBundleRendererContract is the registered renderer identity.
func CalagopusWorkloadBundleRendererContract() RendererContract {
	return calagopusPlatform.rendererContract()
}

// ParseCalagopusWorkloadBundle validates the closed Calagopus artifact.
func ParseCalagopusWorkloadBundle(data []byte) (GamePlatformWorkloadBundleDescriptor, error) {
	return calagopusPlatform.parse(data)
}

var calagopusPanelEnvironment = map[string]string{
	"TZ": "UTC", "PORT": "8000", "REDIS_URL": "redis://panel-cache", "DATABASE_MIGRATE": "true",
	"APP_PRIMARY": "true", "APP_LOG_DIRECTORY": "/var/log/calagopus",
	// The Panel forwards the console WebSocket and file transfers to Wings
	// on the internal network, so Wings publishes no API port (ADR-0048).
	"APP_ENABLE_WINGS_PROXY": "true", "APP_USE_DECRYPTION_CACHE": "false", "APP_USE_INTERNAL_CACHE": "true",
}

// calagopusWingsEnvironment holds the governed Wings settings at every start,
// whatever the configuration file says: no host-network firewall helper, no
// Panel-pushed configuration or binary, a bridge name within 15 characters.
var calagopusWingsEnvironment = map[string]string{
	"TZ": "UTC", "WINGS_UID": "988", "WINGS_GID": "988", "WINGS_USERNAME": "calagopus",
	"CALAGOPUS_DOCKER_FIREWALL_BACKEND":              "disabled",
	"CALAGOPUS_SYSTEM_PASSWD_ENABLED":                "true",
	"CALAGOPUS_IGNORE_PANEL_CONFIG_UPDATES":          "true",
	"CALAGOPUS_IGNORE_PANEL_WINGS_UPGRADES":          "true",
	"CALAGOPUS_DOCKER_NETWORK_NAME":                  "sk_game_nw",
	"CALAGOPUS_DOCKER_NETWORK_MODE":                  "sk_game_nw",
	"CALAGOPUS_DOCKER_NETWORK_INTERFACE":             "10.213.0.1",
	"CALAGOPUS_DOCKER_NETWORK_INTERFACES_V4_SUBNET":  "10.213.0.0/24",
	"CALAGOPUS_DOCKER_NETWORK_INTERFACES_V4_GATEWAY": "10.213.0.1",
}

func calagopusComponents(origin string) []selectedPaaSRuntimeComponent {
	panelImage := selectedPaaSRuntimeImage{Ref: calagopusPanelImageRef, Digest: calagopusPanelImageDigest}
	databaseImage := selectedPaaSRuntimeImage{Ref: calagopusDatabaseImageRef, Digest: calagopusDatabaseImageDigest}
	wingsImage := selectedPaaSRuntimeImage{Ref: calagopusWingsImageRef, Digest: calagopusWingsImageDigest}
	network := []string{gamePlatformNetwork}
	nodeEnvironment, wingsEnvironment := map[string]string{}, calagopusWingsEnvironment
	if origin != "" {
		// Wings admits the console WebSocket only from the Panel origin.
		nodeEnvironment["STACKKIT_PANEL_ORIGIN"] = gameOrigin(origin)
		wingsEnvironment = withGameEnvironment(calagopusWingsEnvironment, map[string]string{"CALAGOPUS_ALLOWED_ORIGINS": `["` + gameOrigin(origin) + `"]`})
	}
	return []selectedPaaSRuntimeComponent{
		{
			ID: "panel", Role: "application", Lifecycle: "daemon", Image: panelImage,
			DependsOn: []string{"panel-cache", "panel-database"}, NetworkRefs: network,
			Entrypoint: []string{"/bin/sh", "/stackkit/panel-entrypoint.sh"}, Environment: calagopusPanelEnvironment,
			SecretFiles: calagopusPanelSecretFiles,
			Volumes: []selectedPaaSRuntimeVolume{
				gameVolume("data", "/var/lib/calagopus", true), gameVolume("logs", "/var/log/calagopus", false), gameVolume("stackkit", "/stackkit", false),
			},
			Health:    selectedPaaSRuntimeHealth{Kind: "http", Path: "/", Port: calagopusPanelPort},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "512m", MemoryReservation: "128m"},
		},
		{
			ID: "panel-database", Role: "database", Lifecycle: "daemon", Image: databaseImage, NetworkRefs: network,
			Environment:       map[string]string{"POSTGRES_DB": "panel", "POSTGRES_USER": "panel"},
			SecretEnvironment: map[string]string{"POSTGRES_PASSWORD": "database-password"},
			Volumes:           []selectedPaaSRuntimeVolume{gameVolume("database", "/var/lib/postgresql", true)},
			Health:            selectedPaaSRuntimeHealth{Kind: "command", Command: []string{"pg_isready", "-U", "panel", "-d", "panel"}},
			Resources:         &selectedPaaSRuntimeLimits{MemoryLimit: "512m", MemoryReservation: "128m"},
		},
		{
			ID: "panel-cache", Role: "cache", Lifecycle: "daemon",
			Image:       selectedPaaSRuntimeImage{Ref: calagopusCacheImageRef, Digest: calagopusCacheImageDigest},
			NetworkRefs: network, Command: []string{"valkey-server"},
			Volumes:   []selectedPaaSRuntimeVolume{gameVolume("cache", "/data", false)},
			Health:    selectedPaaSRuntimeHealth{Kind: "command", Command: []string{"valkey-cli", "ping"}},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "256m", MemoryReservation: "64m"},
		},
		{
			ID: "panel-bootstrap", Role: "database-init", Lifecycle: "one-shot", Image: panelImage,
			DependsOn: []string{"panel"}, NetworkRefs: network,
			Entrypoint: []string{"/bin/sh"}, Command: []string{"/stackkit/bootstrap-owner.sh"}, Environment: calagopusPanelEnvironment,
			OwnerEnvironment: map[string]string{"STACKKIT_OWNER_EMAIL": "email"}, SecretFiles: calagopusOwnerSecretFiles,
			Volumes: []selectedPaaSRuntimeVolume{gameVolume("stackkit", "/stackkit", false)},
			Health:  selectedPaaSRuntimeHealth{Kind: "completion"},
		},
		{
			ID: "panel-keys", Role: "database-init", Lifecycle: "one-shot", Image: databaseImage,
			DependsOn: []string{"panel-bootstrap"}, NetworkRefs: network,
			Entrypoint: []string{"/bin/sh"}, Command: []string{"/stackkit/keys.sh"},
			Environment:      map[string]string{"PGHOST": "panel-database", "PGUSER": "panel", "PGDATABASE": "panel"},
			OwnerEnvironment: map[string]string{"STACKKIT_OWNER_EMAIL": "email"}, SecretFiles: calagopusKeysSecretFiles,
			Volumes: []selectedPaaSRuntimeVolume{gameVolume("stackkit", "/stackkit", false)},
			Health:  selectedPaaSRuntimeHealth{Kind: "completion"},
		},
		{
			ID: "wings-bootstrap", Role: "database-init", Lifecycle: "one-shot", Image: wingsImage,
			DependsOn: []string{"panel-keys"}, NetworkRefs: network,
			Entrypoint: []string{"/bin/sh"}, Command: []string{"/stackkit/node.sh"}, Environment: nodeEnvironment,
			SecretFiles: calagopusNodeSecretFiles,
			Volumes: []selectedPaaSRuntimeVolume{
				gameVolume("stackkit", "/stackkit", false),
				{ID: "data", Target: GameDataTarget, Class: "persistent", Backup: true, SharedFrom: &selectedPaaSVolumeSource{ComponentRef: "wings", VolumeRef: "data"}},
			},
			Health: selectedPaaSRuntimeHealth{Kind: "completion"},
		},
		{
			ID: "wings", Role: "application", Lifecycle: "daemon", Image: wingsImage,
			DependsOn: []string{"wings-bootstrap"}, NetworkRefs: network,
			// Wings hands its data path to the Docker daemon, so its root is
			// the host path the executor exports; it changes per install.
			Entrypoint: []string{"/bin/sh", "-c", calagopusWingsEntrypoint, "calagopus-wings"},
			Command:    []string{"--config", GameDataTarget + "/config.yml"}, Environment: wingsEnvironment,
			DockerLifecycleOwner: &selectedPaaSDockerLifecycleOwner{DaemonRef: gamePlatformDaemonRef, PolicyProfile: gamePlatformPolicyProfile},
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "data", Target: GameDataTarget, Class: "persistent", Backup: true, SelfPath: true},
				gameVolume("logs", "/var/log/calagopus-wings", false),
			},
			Health:    selectedPaaSRuntimeHealth{Kind: "image"},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "512m", MemoryReservation: "128m"},
		},
	}
}

func calagopusConfigFiles() []selectedPaaSConfigFile {
	return append([]selectedPaaSConfigFile{
		{Path: "/stackkit/credentials.sh", Body: calagopusCredentialsShell},
		{Path: "/stackkit/panel-entrypoint.sh", Body: calagopusPanelEntrypoint},
		{Path: "/stackkit/bootstrap-owner.sh", Body: calagopusBootstrapOwnerShell},
		{Path: "/stackkit/keys.sh", Body: calagopusKeysShell},
		{Path: "/stackkit/keys.sql", Body: calagopusKeysSQL},
		{Path: "/stackkit/node.sh", Body: calagopusNodeShell},
	}, gameEggFiles()...)
}

// calagopusCredentialsShell exports the Panel's database URL and encryption
// key from their custody files; neither is in the container configuration.
const calagopusCredentialsShell = `password="$(sed -e 's/%/%25/g' -e 's/+/%2B/g' -e 's#/#%2F#g' -e 's/=/%3D/g' "$STACKKIT_DB_PASSWORD_FILE")"
export DATABASE_URL="postgresql://panel:${password}@panel-database/panel"
export APP_ENCRYPTION_KEY="$(cat "$STACKKIT_ENCRYPTION_KEY_FILE")"
unset password
`

const calagopusPanelEntrypoint = `#!/bin/sh
set -eu
. /stackkit/credentials.sh
exec /usr/bin/panel-rs "$@"
`

// The owner is created once; a later run finds it and only re-marks the
// first-run wizard as finished. The Panel migrates while this step waits.
const calagopusBootstrapOwnerShell = `#!/bin/sh
set -eu
. /stackkit/credentials.sh
owner_password="$(cat "$STACKKIT_OWNER_PASSWORD_FILE")"
deadline=$(( $(date +%s) + 900 ))
while :; do
  if output="$(calagopus-panel users create --username owner --email "$STACKKIT_OWNER_EMAIL" \
      --name-first Home --name-last Owner --password "$owner_password" --admin true --json 2>&1)"; then
    break
  fi
  case "$output" in *"duplicate key"*) break ;; esac
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "stackkit: the Calagopus owner could not be created" >&2
    exit 3
  fi
  sleep 3
done
unset owner_password output
calagopus-panel oobe finish >/dev/null
echo "Calagopus owner converged"
`

// Calagopus stores an API key as the SHA-256 digest of the whole key and its
// first 16 characters, so the custody material becomes the key itself.
const calagopusKeysShell = `#!/bin/sh
set -eu
export PGPASSWORD="$(cat "$STACKKIT_DB_PASSWORD_FILE")"
until pg_isready -q; do sleep 2; done
psql -v ON_ERROR_STOP=1 -q -v email="$STACKKIT_OWNER_EMAIL" -f /stackkit/keys.sql
echo "Calagopus custody keys converged"
`

// The setup key carries only what the setup action and the node bootstrap
// need; the owner's client key only server read, power, console and files.
const calagopusKeysSQL = `\set setup_raw ` + "`" + `cat "$STACKKIT_APPLICATION_API_KEY_FILE"` + "`" + `
\set client_raw ` + "`" + `cat "$STACKKIT_CLIENT_API_KEY_FILE"` + "`" + `
SELECT 1 / count(*) FROM users WHERE lower(email) = lower(:'email');
INSERT INTO user_api_keys (user_uuid, name, key_start, key, user_permissions, admin_permissions, server_permissions)
SELECT u.uuid, k.name, substr(k.key, 1, 16), encode(sha256(convert_to(k.key, 'UTF8')), 'hex'), k.user_permissions, k.admin_permissions, k.server_permissions
FROM users u, (VALUES
  ('stackkit-setup', 'c7sp_' || :'setup_raw', ARRAY['servers.read']::varchar[],
   ARRAY['settings.read', 'settings.update', 'users.read', 'locations.create', 'locations.read', 'nodes.create', 'nodes.read',
         'nodes.update', 'nodes.reset-token', 'nodes.allocations', 'nests.create', 'nests.read', 'eggs.create', 'eggs.read',
         'eggs.update', 'servers.create', 'servers.read', 'servers.update', 'servers.variables']::varchar[],
   ARRAY['control.read-console', 'control.console', 'control.start', 'control.stop', 'control.restart', 'files.create',
         'files.read', 'files.read-content', 'files.update', 'startup.read', 'startup.update', 'settings.install']::varchar[]),
  ('stackkit-owner-client', 'c7sp_' || :'client_raw', ARRAY['servers.read']::varchar[], ARRAY[]::varchar[],
   ARRAY['control.read-console', 'control.console', 'control.start', 'control.stop', 'control.restart', 'files.create',
         'files.read', 'files.read-content', 'files.update', 'startup.read']::varchar[])
) AS k(name, key, user_permissions, admin_permissions, server_permissions)
WHERE lower(u.email) = lower(:'email')
ON CONFLICT (user_uuid, name) DO UPDATE SET key_start = EXCLUDED.key_start, key = EXCLUDED.key,
  user_permissions = EXCLUDED.user_permissions, admin_permissions = EXCLUDED.admin_permissions,
  server_permissions = EXCLUDED.server_permissions, enabled = true, expires = NULL;
`

// calagopusWingsEntrypoint points Wings' root and passwd directories at the
// game data's own host path before Wings starts.
const calagopusWingsEntrypoint = `export CALAGOPUS_SYSTEM_ROOT_DIRECTORY="$` + GameDataHostPathEnv + `" CALAGOPUS_SYSTEM_PASSWD_DIRECTORY="$` + GameDataHostPathEnv + `/passwd"; exec /usr/bin/calagopus-wings "$@"`

// The node bootstrap converges the Panel URL, location, node, allocations
// and curated Eggs through the Admin API with the setup key, then enrolls
// Wings once. Wings publishes no port: the Panel proxies the console.
const calagopusNodeShell = `#!/bin/sh
set -eu
key="c7sp_$(cat "$STACKKIT_APPLICATION_API_KEY_FILE")"
api=http://panel:8000
data=/stackkit/game-data
origin="${STACKKIT_PANEL_ORIGIN%/}"

call() {
  method="$1"; path="$2"; shift 2
  curl -fsS --retry 5 --retry-all-errors --retry-delay 3 -X "$method" -H "Authorization: Bearer $key" \
    -H "Content-Type: application/json" -H "Accept: application/json" "$@" "$api$path"
}
first_uuid() { grep -o '"uuid":"[0-9a-f-]\{36\}"' | head -n 1 | cut -d '"' -f 4; }

deadline=$(( $(date +%s) + 900 ))
until curl -fsS -o /dev/null -H "Authorization: Bearer $key" "$api/api/admin/locations"; do
  [ "$(date +%s)" -lt "$deadline" ] || { echo "stackkit: the Calagopus Panel did not answer" >&2; exit 3; }
  sleep 3
done

call PUT /api/admin/settings -d "{\"app\":{\"url\":\"$origin\"}}" >/dev/null

# The created identities live beside Wings' data, so a restored game node
# keeps the Panel rows its database snapshot holds.
state="$data/stackkit-panel.env"
location=""; node=""; nest_Minecraft=""; nest_Terraria=""; nest_Valheim=""
if [ -f "$state" ]; then . "$state"; fi
save_state() {
  printf 'location=%s\nnode=%s\nnest_Minecraft=%s\nnest_Terraria=%s\nnest_Valheim=%s\n' \
    "$location" "$node" "$nest_Minecraft" "$nest_Terraria" "$nest_Valheim" > "$state"
}
exists() { [ -n "$2" ] && curl -fsS -o /dev/null -H "Authorization: Bearer $key" "$api/api/admin/$1/$2"; }
if ! exists locations "$location"; then
  location="$(call POST /api/admin/locations -d '{"name":"stackkit-home","description":"StackKits game node"}' | first_uuid)"
  node=""
fi

memory=$(awk '/^MemTotal:/ { m = int($2 / 1024) - 3072; if (m < 2048) m = 2048; print m }' /proc/meminfo)
disk=$(df -Pm "$data" | awk 'NR == 2 { d = int($2 * 0.8); if (d < 10240) d = 10240; print d }')
if ! exists nodes "$node"; then
  node="$(call POST /api/admin/nodes -d "{\"location_uuid\":\"$location\",\"name\":\"stackkit-node\",\"description\":\"StackKits game node\",\"deployment_enabled\":true,\"maintenance_enabled\":false,\"url\":\"http://wings:8080\",\"sftp_port\":2022,\"memory\":$memory,\"disk\":$disk}" | first_uuid)"
fi
save_state
call PATCH "/api/admin/nodes/$node" -d "{\"public_url\":\"$origin/wings-proxy/$node\",\"memory\":$memory,\"disk\":$disk}" >/dev/null

# Minecraft Bedrock and Java, Terraria, and Valheim (game plus query port).
allocations="$(call GET "/api/admin/nodes/$node/allocations?per_page=100")"
missing=""
for port in 19132 25565 25566 25567 25568 25569 25570 7777 2456 2457; do
  case "$allocations" in *"\"port\":$port,"*) ;; *) missing="$missing${missing:+,}$port" ;; esac
done
if [ -n "$missing" ]; then
  call POST "/api/admin/nodes/$node/allocations" -d "{\"ip\":\"0.0.0.0\",\"ports\":[$missing]}" >/dev/null
fi

for egg in /stackkit/eggs/*.json; do
  case "$(basename "$egg")" in
    minecraft-*) nest_name=Minecraft ;;
    terraria-*) nest_name=Terraria ;;
    valheim-*) nest_name=Valheim ;;
    *) echo "stackkit: Egg $egg has no curated nest" >&2; exit 1 ;;
  esac
  eval "nest=\${nest_$nest_name}"
  if ! exists nests "$nest"; then
    nest="$(call POST /api/admin/nests -d "{\"author\":\"stackkits@kombify.io\",\"name\":\"$nest_name\",\"description\":\"Curated by StackKits\"}" | first_uuid)"
    eval "nest_$nest_name=\$nest"
    save_state
  fi
  egg_name="$(grep -o '"name": *"[^"]*"' "$egg" | head -n 1 | sed 's/^"name": *"\(.*\)"$/\1/')"
  case "$(call GET "/api/admin/nests/$nest/eggs?per_page=100")" in
    *"\"name\":\"$egg_name\""*) ;;
    *) call POST "/api/admin/nests/$nest/eggs/import" --data-binary "@$egg" >/dev/null ;;
  esac
done

config="$data/config.yml"
if ! grep -q "^uuid: $node\$" "$config" 2>/dev/null; then
  umask 077
  code="$(call POST "/api/admin/nodes/$node/enrollment" -d '{"remote":"http://panel:8000"}' | sed -n 's/.*"code":"\([^"]*\)".*/\1/p')"
  calagopus-wings --config "$config" configure --enroll "$code" --panel-url "$api" --override >/dev/null
fi
chmod 600 "$config"
echo "Calagopus node converged"
`

// gameOrigin is a route origin without its trailing slash; browsers send
// Origin without one and Wings compares exactly.
func gameOrigin(origin string) string {
	for len(origin) > 0 && origin[len(origin)-1] == '/' {
		origin = origin[:len(origin)-1]
	}
	return origin
}
