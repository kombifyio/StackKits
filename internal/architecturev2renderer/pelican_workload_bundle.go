package architecturev2renderer

const (
	pelicanWorkloadModuleID = "stackkits-pelican-runtime"
	pelicanPanelPort        = 80
)

var pelicanSecretSlots = []string{"app-key", "owner-password", "application-api-key", "client-api-key"}

// The Pelican Panel image runs as www-data (uid 82); its bootstrap runs as
// root because it writes Wings' configuration (ADR-0048).
var (
	pelicanPanelSecretFiles = []selectedPaaSSecretFile{
		{Slot: "app-key", Target: "/run/secrets/app-key", PathEnvironment: "STACKKIT_APP_KEY_FILE", UID: 82, GID: 82},
	}
	pelicanBootstrapSecretFiles = []selectedPaaSSecretFile{
		custodyFile("app-key", "STACKKIT_APP_KEY_FILE"),
		custodyFile("owner-password", "STACKKIT_OWNER_PASSWORD_FILE"),
		custodyFile("application-api-key", "STACKKIT_APPLICATION_API_KEY_FILE"),
		custodyFile("client-api-key", "STACKKIT_CLIENT_API_KEY_FILE"),
	}
)

const pelicanWorkloadRendererSchema = `stackkit.workload-bundle/v2|PelicanWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:panel,panel-bootstrap,wings|database:sqlite|wings:docker-socket-direct-v1/lifecycle-owner|game-data:self-path|panel:route-host-loopback,node-tls-local,caddy-node-proxy|bootstrap:root|eggs:` + GameVanillaMinecraftEggSHA256 + `,` + GamePaperEggSHA256 + `,` + PterodactylBedrockEggSHA256 + `,` + PterodactylTerrariaEggSHA256 + `,` + PterodactylValheimEggSHA256 + `|release:` + pelicanPanelRelease + `+wings-` + pelicanWingsRelease + `|secret-material:not-included`

var pelicanPlatform = gamePlatform{
	name: "Pelican", alternative: "pelican", moduleID: pelicanWorkloadModuleID, unitID: "pelican",
	providerRef: "stackkits-pelican", kind: "PelicanWorkloadBundle",
	templateRef: "builtin://workloads/pelican/bundle/v1.json", outputRef: "workloads/pelican/bundle.json",
	healthRef: "pelican-panel-http", approvalID: "approve-pelican-wings-lifecycle-owner",
	approvalEvidence: "pelican-wings-lifecycle-owner-governance",
	release:          pelicanPanelRelease, schema: pelicanWorkloadRendererSchema, routePort: pelicanPanelPort,
	entryImage:  selectedPaaSRuntimeImage{Ref: pelicanPanelImageRef, Digest: pelicanPanelImageDigest},
	secretSlots: pelicanSecretSlots, components: pelicanComponents, configFiles: pelicanConfigFiles,
}

// PelicanWorkloadBundleRendererContract is the registered renderer identity.
func PelicanWorkloadBundleRendererContract() RendererContract {
	return pelicanPlatform.rendererContract()
}

// ParsePelicanWorkloadBundle validates the closed Pelican artifact.
func ParsePelicanWorkloadBundle(data []byte) (GamePlatformWorkloadBundleDescriptor, error) {
	return pelicanPlatform.parse(data)
}

// APP_INSTALLED skips the web installer; the Panel keeps its SQLite database
// in its data volume. PHP_INI_SCAN_DIR adds the governed CA bundle with which
// the Panel trusts its own node certificate.
var pelicanPanelEnvironment = map[string]string{
	"APP_ENV": "production", "APP_DEBUG": "false", "APP_INSTALLED": "true", "APP_TIMEZONE": "UTC",
	"DB_CONNECTION": "sqlite", "CACHE_STORE": "file", "SESSION_DRIVER": "file", "QUEUE_CONNECTION": "database",
	"MAIL_MAILER": "log", "BEHIND_PROXY": "true", "TRUSTED_PROXIES": "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16",
	"PHP_INI_SCAN_DIR": ":/tmp/stackkit/php",
}

func pelicanComponents(origin string) []selectedPaaSRuntimeComponent {
	panelImage := selectedPaaSRuntimeImage{Ref: pelicanPanelImageRef, Digest: pelicanPanelImageDigest}
	network := []string{gamePlatformNetwork}
	panelEnvironment, bootstrapEnvironment := pelicanPanelEnvironment, pelicanPanelEnvironment
	if origin != "" {
		origin = gameOrigin(origin)
		route := map[string]string{"APP_URL": origin, "STACKKIT_ROUTE_HOST": gameRouteHost(origin)}
		panelEnvironment = withGameEnvironment(pelicanPanelEnvironment, route)
		bootstrapEnvironment = withGameEnvironment(panelEnvironment, map[string]string{"STACKKIT_PANEL_ORIGIN": origin})
	}
	return []selectedPaaSRuntimeComponent{
		{
			ID: "panel", Role: "application", Lifecycle: "daemon", Image: panelImage, NetworkRefs: network,
			Entrypoint: []string{"/bin/ash", "/stackkit/panel-entrypoint.sh"},
			Command:    []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, Environment: panelEnvironment,
			SecretFiles: pelicanPanelSecretFiles,
			// The Panel reaches its node under its own route host on
			// loopback, so console and API calls never cross the router.
			RouteHostLoopback: true,
			Volumes: []selectedPaaSRuntimeVolume{
				gameVolume("data", "/pelican-data", true), gameVolume("caddy", "/etc/caddy", false), gameVolume("stackkit", "/stackkit", false),
			},
			Health:    selectedPaaSRuntimeHealth{Kind: "http", Path: "/up", Port: pelicanPanelPort},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "1g", MemoryReservation: "256m"},
		},
		{
			ID: "panel-bootstrap", Role: "database-init", Lifecycle: "one-shot", Image: panelImage,
			DependsOn: []string{"panel"}, NetworkRefs: network,
			Entrypoint: []string{"/bin/ash"}, Command: []string{"/stackkit/bootstrap.sh"}, Environment: bootstrapEnvironment,
			OwnerEnvironment: map[string]string{"STACKKIT_OWNER_EMAIL": "email"}, SecretFiles: pelicanBootstrapSecretFiles,
			Volumes: []selectedPaaSRuntimeVolume{
				gameVolume("stackkit", "/stackkit", false),
				{ID: "panel-data", Target: "/pelican-data", Class: "persistent", Backup: true, SharedFrom: &selectedPaaSVolumeSource{ComponentRef: "panel", VolumeRef: "data"}},
				{ID: "data", Target: GameDataTarget, Class: "persistent", Backup: true, SharedFrom: &selectedPaaSVolumeSource{ComponentRef: "wings", VolumeRef: "data"}},
			},
			Health: selectedPaaSRuntimeHealth{Kind: "completion"},
		},
		{
			ID: "wings", Role: "application", Lifecycle: "daemon",
			Image:     selectedPaaSRuntimeImage{Ref: pelicanWingsImageRef, Digest: pelicanWingsImageDigest},
			DependsOn: []string{"panel-bootstrap"}, NetworkRefs: network,
			Command:              []string{"/usr/bin/wings", "--config", GameDataTarget + "/config.yml"},
			Environment:          map[string]string{"TZ": "UTC", "WINGS_UID": "988", "WINGS_GID": "988", "WINGS_USERNAME": "pelican"},
			DockerLifecycleOwner: &selectedPaaSDockerLifecycleOwner{DaemonRef: gamePlatformDaemonRef, PolicyProfile: gamePlatformPolicyProfile},
			Volumes: []selectedPaaSRuntimeVolume{
				{ID: "data", Target: GameDataTarget, Class: "persistent", Backup: true, SelfPath: true},
				gameVolume("logs", "/var/log/pelican", false),
			},
			Health:    selectedPaaSRuntimeHealth{Kind: "image"},
			Resources: &selectedPaaSRuntimeLimits{MemoryLimit: "512m", MemoryReservation: "128m"},
		},
	}
}

func pelicanConfigFiles() []selectedPaaSConfigFile {
	return append([]selectedPaaSConfigFile{
		{Path: "/stackkit/credentials.sh", Body: pelicanCredentialsShell},
		{Path: "/stackkit/panel-entrypoint.sh", Body: pelicanPanelEntrypoint},
		{Path: "/etc/caddy/Caddyfile", Body: pelicanCaddyfile},
		{Path: "/stackkit/bootstrap.sh", Body: pelicanBootstrapShell},
		{Path: "/stackkit/bootstrap.php", Body: pelicanBootstrapPHP},
	}, gameEggFiles()...)
}

const pelicanCredentialsShell = `export APP_KEY="base64:$(cat "$STACKKIT_APP_KEY_FILE")="
`

// The Panel keeps its key out of the persistent .env: with an existing .env
// the upstream entrypoint never writes it there, and an earlier copy is
// removed. The Panel trusts a node certificate that exists only inside it.
const pelicanPanelEntrypoint = `#!/bin/ash
set -eu
. /stackkit/credentials.sh
touch /pelican-data/.env
sed -i -e '/^APP_KEY=/d' -e '/^APP_INSTALLED=/d' /pelican-data/.env
mkdir -p /tmp/stackkit/tls /tmp/stackkit/php
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=${STACKKIT_ROUTE_HOST}" \
  -addext "subjectAltName=DNS:${STACKKIT_ROUTE_HOST}" \
  -keyout /tmp/stackkit/tls/node.key -out /tmp/stackkit/tls/node.crt >/dev/null 2>&1
cat /etc/ssl/certs/ca-certificates.crt /tmp/stackkit/tls/node.crt > /tmp/stackkit/ca-bundle.pem
printf 'curl.cainfo=/tmp/stackkit/ca-bundle.pem\nopenssl.cafile=/tmp/stackkit/ca-bundle.pem\n' > /tmp/stackkit/php/zz-stackkit.ini
exec /bin/ash /entrypoint.sh "$@"
`

// The Panel's own Caddy serves the router on 80 and its node origin on 443;
// both forward the Wings node paths to Wings on the internal network.
const pelicanCaddyfile = `{
	admin off
	auto_https off
	servers {
		trusted_proxies static private_ranges
	}
}

(panel) {
	root * /var/www/html/public
	encode gzip
	@node path /api/servers /api/servers/* /api/system /api/system/* /api/update /api/update/* /api/transfers /api/transfers/* /download /download/* /upload /upload/*
	handle @node {
		reverse_proxy wings:8080 {
			flush_interval -1
		}
	}
	handle {
		file_server
		php_fastcgi 127.0.0.1:9000
	}
}

http://:80 {
	import panel
}

https://:443 {
	tls /tmp/stackkit/tls/node.crt /tmp/stackkit/tls/node.key
	import panel
}
`

// The bootstrap runs as root; the SQLite files stay owned by the Panel user.
const pelicanBootstrapShell = `#!/bin/ash
set -eu
cd /var/www/html
. /stackkit/credentials.sh
export STACKKIT_OWNER_PASSWORD="$(cat "$STACKKIT_OWNER_PASSWORD_FILE")"
export STACKKIT_APPLICATION_API_KEY="$(cat "$STACKKIT_APPLICATION_API_KEY_FILE")"
export STACKKIT_CLIENT_API_KEY="$(cat "$STACKKIT_CLIENT_API_KEY_FILE")"
status=0
php /stackkit/bootstrap.php || status=$?
chown -R www-data:www-data /pelican-data
if [ "$status" -eq 0 ] && [ ! -s /stackkit/game-data/config.yml ]; then
  echo "stackkit: the Pelican bootstrap wrote no Wings configuration" >&2
  status=1
fi
exit "$status"
`

// The bootstrap is idempotent: every run converges the owner, node, API
// keys, allocations, curated Eggs and the Wings configuration.
const pelicanBootstrapPHP = `<?php
declare(strict_types=1);

require '/var/www/html/vendor/autoload.php';
$app = require '/var/www/html/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();

use App\Enums\EggFormat;
use App\Models\Allocation;
use App\Models\ApiKey;
use App\Models\Egg;
use App\Models\Node;
use App\Models\User;
use Symfony\Component\Yaml\Yaml;

function envOrFail(string $name): string {
    $value = getenv($name);
    if ($value === false || $value === '') {
        fwrite(STDERR, "missing required environment {$name}\n");
        exit(2);
    }
    return $value;
}

// The Panel migrates at its own start; converge only once no migration is
// pending, so the bootstrap never writes into a half-migrated schema.
$deadline = time() + 900;
while (true) {
    try {
        $migrator = app('migrator');
        if ($migrator->repositoryExists()) {
            $paths = array_merge([database_path('migrations')], $migrator->paths());
            $pending = array_diff(array_keys($migrator->getMigrationFiles($paths)), $migrator->getRepository()->getRan());
            if ($pending === []) {
                break;
            }
        }
    } catch (Throwable $e) {
    }
    if (time() > $deadline) {
        fwrite(STDERR, "Panel database was not migrated in time\n");
        exit(3);
    }
    sleep(3);
}

try {

$email = envOrFail('STACKKIT_OWNER_EMAIL');
$routeHost = envOrFail('STACKKIT_ROUTE_HOST');
$origin = envOrFail('STACKKIT_PANEL_ORIGIN');
$dataHost = rtrim(envOrFail('STACKKIT_GAME_DATA_HOST_PATH'), '/');

$user = User::query()->where('email', $email)->first();
if ($user === null) {
    $user = app(App\Services\Users\UserCreationService::class)->handle([
        'email' => $email, 'username' => 'owner', 'password' => envOrFail('STACKKIT_OWNER_PASSWORD'), 'root_admin' => true,
    ]);
}

$memoryMb = 4096;
foreach (file('/proc/meminfo') ?: [] as $line) {
    if (preg_match('/^MemTotal:\s+(\d+)\s+kB/', $line, $m)) {
        // The platform (Panel, Wings) and the Basement core keep 3 GB.
        $memoryMb = max(2048, intdiv((int) $m[1], 1024) - 3072);
    }
}
$diskMb = max(10240, (int) floor(((float) disk_total_space('/stackkit/game-data')) / 1048576 * 0.8));

// The browser and the Panel both reach the node on the Panel origin; the
// Panel's own Caddy forwards the node paths to Wings (ADR-0043).
$nodeAttributes = [
    'name' => 'stackkit-node', 'description' => 'StackKits game node', 'public' => false,
    'fqdn' => $routeHost, 'scheme' => 'https', 'behind_proxy' => true,
    'memory' => $memoryMb, 'memory_overallocate' => 0, 'disk' => $diskMb, 'disk_overallocate' => 0,
    'cpu' => 0, 'cpu_overallocate' => 0, 'upload_size' => 100,
    'daemon_listen' => 8080, 'daemon_connect' => 443, 'daemon_sftp' => 2022,
    'daemon_base' => $dataHost . '/volumes', 'maintenance_mode' => false,
];
$node = Node::query()->where('name', 'stackkit-node')->first();
if ($node === null) {
    $node = Node::query()->create($nodeAttributes);
} else {
    $node->forceFill($nodeAttributes)->save();
}

$ensureKey = function (string $material, int $type, array $permissions) use ($user): void {
    if (strlen($material) < 43) {
        fwrite(STDERR, "API key material is too short\n");
        exit(4);
    }
    $identifier = ApiKey::getPrefixForType($type) . substr($material, 0, 11);
    $key = ApiKey::query()->where('identifier', $identifier)->first();
    if ($key === null) {
        $key = new ApiKey();
        $key->forceFill([
            'user_id' => $user->id, 'key_type' => $type, 'identifier' => $identifier,
            'token' => substr($material, 11, 32), 'memo' => 'StackKits custody', 'allowed_ips' => [],
            'permissions' => $permissions,
        ])->save();
    }
};
$full = [];
foreach (['server', 'node', 'allocation', 'user', 'egg', 'database_host', 'server_database', 'mount'] as $resource) {
    $full[$resource] = 3;
}
$ensureKey(envOrFail('STACKKIT_APPLICATION_API_KEY'), ApiKey::TYPE_APPLICATION, $full);
$ensureKey(envOrFail('STACKKIT_CLIENT_API_KEY'), ApiKey::TYPE_ACCOUNT, []);

// Minecraft Bedrock and Java, Terraria, and Valheim (game plus query port).
$ports = ['19132', '25565', '25566', '25567', '25568', '25569', '25570', '7777', '2456', '2457'];
$existing = Allocation::query()->where('node_id', $node->id)->pluck('port')->map(fn ($p) => (string) $p)->all();
$missing = array_values(array_diff($ports, $existing));
if ($missing !== []) {
    app(App\Services\Allocations\AssignmentService::class)->handle($node, ['allocation_ip' => '0.0.0.0', 'allocation_ports' => $missing]);
}

foreach (glob('/stackkit/eggs/*.json') ?: [] as $eggFile) {
    $definition = json_decode((string) file_get_contents($eggFile), true);
    if (Egg::query()->where('name', $definition['name'])->exists()) {
        continue;
    }
    app(App\Services\Eggs\Sharing\EggImporterService::class)->fromContent((string) file_get_contents($eggFile), EggFormat::JSON);
}

$config = Yaml::parse($node->fresh()->getYamlConfiguration());
$config['remote'] = 'http://panel';
$config['allowed_origins'] = [$origin];
$config['api']['host'] = '0.0.0.0';
$config['api']['port'] = 8080;
$config['api']['ssl']['enabled'] = false;
$config['system']['root_directory'] = $dataHost;
$config['system']['data'] = $dataHost . '/volumes';
$config['system']['archive_directory'] = $dataHost . '/archives';
$config['system']['backup_directory'] = $dataHost . '/backups';
$config['system']['tmp_directory'] = $dataHost . '/tmp';
$config['system']['log_directory'] = '/var/log/pelican';
$config['system']['user']['passwd'] = ['enable' => true, 'directory' => $dataHost . '/passwd'];
$config['system']['machine_id']['enable'] = true;
$config['system']['machine_id']['directory'] = $dataHost . '/machine-id';
$config['docker']['network']['name'] = 'sk_game_nw';
$config['docker']['network']['network_mode'] = 'sk_game_nw';
$config['docker']['network']['interface'] = '10.213.0.1';
$config['docker']['network']['interfaces']['v4'] = ['subnet' => '10.213.0.0/24', 'gateway' => '10.213.0.1'];
$config['docker']['network']['interfaces']['v6'] = ['subnet' => 'fdba:17c8:6c94::/64', 'gateway' => 'fdba:17c8:6c94::1011'];
file_put_contents('/stackkit/game-data/config.yml', Yaml::dump($config, 8, 2, Yaml::DUMP_EMPTY_ARRAY_AS_SEQUENCE));
chmod('/stackkit/game-data/config.yml', 0600);
} catch (Throwable $e) {
    // Laravel's console handler would report and still exit 0.
    fwrite(STDERR, "stackkit: Pelican bootstrap failed: " . $e->getMessage() . "\n");
    exit(1);
}
fwrite(STDOUT, "Pelican bootstrap converged\n");
`
