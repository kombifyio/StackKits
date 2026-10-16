package architecturev2renderer

// nativeDockerSecretReader is how one container reads its secret values
// from files (ADR-0045 addendum A1 secret delivery rule; plan 20 S2.2).
// Exactly one form is set.
type nativeDockerSecretReader struct {
	// EnvFile names the variable that receives the path of one mounted
	// dotenv file holding every secret value of the container.
	EnvFile string
	// FileSuffix delivers every secret variable NAME as its own mounted file
	// whose path the container reads from NAME_FILE.
	FileSuffix bool
	// Shim is the governed entrypoint shim for an image that reads a secret
	// only from its environment (owner decision 2026-10-02, plan 20 option
	// 1): every secret variable is its own mounted file, which the shim
	// reads into the process environment before it execs the image's own
	// entrypoint and command. Valid only for the pinned image it names.
	Shim *nativeDockerShim
	// Files are the fixed-path files the pinned image itself reads its
	// secrets from (a config file, a libpq password file, a value file at
	// a fixed path, a dotenv file). Each is rendered by the executor from
	// the private .env and copied into the container by the provider with
	// its owner and mode, like a Compose secret file; only its path and
	// digest enter the root and the state. Every secret variable of the
	// container is bound by exactly one file.
	Files []nativeDockerSecretFileSpec
	// Environment holds the literal, non-secret variables a Files reader
	// needs to find its files (a path such as PGPASSFILE). Only valid with
	// Files.
	Environment map[string]string
}

// nativeDockerSecretFileSpec is one fixed-path secret file of a Files
// reader.
type nativeDockerSecretFileSpec struct {
	// Target is the absolute path the image reads.
	Target string
	// Format is NativeDockerSecretFormatValue, ...Dotenv, ...YAML or
	// ...Pgpass.
	Format string
	// Variables are the container's secret variables in file order.
	Variables []string
	// Keys name each variable's entry: the dotted mapping path in a YAML
	// file, the variable name in a dotenv file (defaults to the variable).
	Keys []string
	// Optional variables may be absent from the container (a companion
	// secret that exists only with another workload); a file left with no
	// value is not written.
	Optional []string
	// UID, GID and Mode are the owner and mode the image's reader needs.
	UID, GID string
	Mode     int
}

// nativeDockerShim is the original start of one pinned image, read from the
// image configuration at that digest; the shim execs exactly this.
type nativeDockerShim struct {
	ImageDigest string
	Entrypoint  []string
	Command     []string
}

// nativeDockerSecretReaders is the governed per-image inventory of
// file-based secret inputs, keyed by module and Compose service. Each entry
// is verified against the upstream source of the pinned image (plan 20,
// "Secret delivery inventory"); an image upgrade re-verifies its entry. A
// container with secret environment values and no entry here cannot render
// natively: its values would otherwise enter the root, the state or the
// container environment. Readers never get the plain variable and its file
// form at once (several entrypoints exit when both are set).
var nativeDockerSecretReaders = map[string]map[string]nativeDockerSecretReader{
	// Vaultwarden loads ENV_FILE with dotenv semantics; real environment
	// variables would win, so none of the secret ones are set.
	"stackkits-vaultwarden-runtime": {"vaultwarden": {EnvFile: "ENV_FILE"}},
	// immich-server start.sh exports DB_PASSWORD_FILE; the Immich Postgres
	// image maps POSTGRES_PASSWORD_FILE. The init job runs psql and
	// createdb as root; libpq reads PGPASSFILE only when it is a 0600 file.
	"stackkits-immich-runtime": {
		"immich-server": {FileSuffix: true}, "immich-postgres": {FileSuffix: true}, "immich-postgres-init": immichInitPgpassReader,
	},
	"stackkits-immich-lite-runtime": {
		"immich-server": {FileSuffix: true}, "immich-postgres": {FileSuffix: true}, "immich-postgres-init": immichInitPgpassReader,
	},
	// docker-library nextcloud and postgres entrypoints (file_env).
	"stackkits-nextcloud-runtime": {"nextcloud": {FileSuffix: true}, "nextcloud-postgres": {FileSuffix: true}},
	// passbolt env.sh (file_env) and the mariadb entrypoint.
	"stackkits-passbolt-runtime": {"passbolt": {FileSuffix: true}, "passbolt-mariadb": {FileSuffix: true}},
	// paperless-ngx s6 init-env-file reads PAPERLESS_*_FILE (a trailing
	// newline would be kept; the file is written without one).
	"stackkits-paperless-runtime":   {"paperless": {FileSuffix: true}, "paperless-postgres": {FileSuffix: true}},
	"stackkits-paperclip-runtime":   {"paperclip-postgres": {FileSuffix: true}},
	"stackkits-pterodactyl-runtime": {"panel-database": {FileSuffix: true}},
	"stackkits-calagopus-runtime":   {"panel-database": {FileSuffix: true}},
	// immich-kiosk reads KIOSK_IMMICH_API_KEY_FILE (trimmed; overrides env).
	"stackkits-immich-kiosk-runtime": {"immich-kiosk": {FileSuffix: true}},
	// immich-power-tools reads IMMICH_API_KEY (and DB_PASSWORD) only from
	// its environment. Image config at the pin: Entrypoint
	// ["docker-entrypoint.sh"], Cmd ["node", "server.js"], user nextjs.
	"stackkits-immich-power-tools-runtime": {"immich-power-tools": {Shim: &nativeDockerShim{
		ImageDigest: "sha256:0bb87c70270ee7a95848a5b8ecbd90cd850e7bdacc0f64b70e33e4524f123fa4",
		Entrypoint:  []string{"docker-entrypoint.sh"}, Command: []string{"node", "server.js"},
	}}},
	// SearXNG reads server.secret_key from settings.yml unless SEARXNG_SECRET
	// is set; the governed entrypoint appends this file to the settings it
	// writes, as root, before the image entrypoint.
	"stackkits-searxng-runtime": {"searxng": {Files: []nativeDockerSecretFileSpec{{
		Target: SearxngSecretSettingsPath, Format: NativeDockerSecretFormatYAML,
		Variables: []string{"SEARXNG_SECRET"}, Keys: []string{"server.secret_key"}, UID: "0", GID: "0", Mode: 0o600,
	}}}},
	// The Roundcube image prefers /run/secrets/roundcube_des_key (read with
	// file_get_contents, not trimmed); the governed config reads it as the
	// web server user (www-data, gid 33) when the variable is unset.
	"stackkits-roundcube-runtime": {"roundcube": {Files: []nativeDockerSecretFileSpec{{
		Target: "/run/secrets/roundcube_des_key", Format: NativeDockerSecretFormatValue,
		Variables: []string{"ROUNDCUBEMAIL_DES_KEY"}, UID: "0", GID: "33", Mode: 0o440,
	}}}},
	// The Euro-Office entrypoint (root) reads Data/.private/jwt_secret when
	// JWT_SECRET is unset, the file it otherwise generates in the data
	// volume.
	"stackkits-euro-office-runtime": {"euro-office": {Files: []nativeDockerSecretFileSpec{{
		Target: "/var/www/euro-office/Data/.private/jwt_secret", Format: NativeDockerSecretFormatValue,
		Variables: []string{"JWT_SECRET"}, UID: "0", GID: "0", Mode: 0o600,
	}}}},
	// Open WebUI v0.11.3: start.sh (root) reads WEBUI_SECRET_KEY_FILE when
	// the variable is unset; open_webui/env.py loads /app/.env without
	// overriding the environment. The speech keys exist only with the
	// SpeechKit companion.
	"stackkits-private-ai-runtime": {"open-webui": {
		Files: []nativeDockerSecretFileSpec{{
			Target: "/run/stackkit/secrets/open-webui-secret-key", Format: NativeDockerSecretFormatValue,
			Variables: []string{"WEBUI_SECRET_KEY"}, UID: "0", GID: "0", Mode: 0o600,
		}, {
			Target: "/app/.env", Format: NativeDockerSecretFormatDotenv,
			Variables: []string{"WEBUI_ADMIN_PASSWORD", "AUDIO_STT_OPENAI_API_KEY", "AUDIO_TTS_OPENAI_API_KEY"},
			Optional:  []string{"AUDIO_STT_OPENAI_API_KEY", "AUDIO_TTS_OPENAI_API_KEY"}, UID: "0", GID: "0", Mode: 0o600,
		}},
		Environment: map[string]string{"WEBUI_SECRET_KEY_FILE": "/run/stackkit/secrets/open-webui-secret-key"},
	}},
}

// immichInitPgpassReader is the libpq password file of the Immich database
// init job.
var immichInitPgpassReader = nativeDockerSecretReader{
	Files: []nativeDockerSecretFileSpec{{
		Target: "/run/stackkit/secrets/pgpass", Format: NativeDockerSecretFormatPgpass,
		Variables: []string{"PGPASSWORD"}, UID: "0", GID: "0", Mode: 0o600,
	}},
	Environment: map[string]string{"PGPASSFILE": "/run/stackkit/secrets/pgpass"},
}

// SearxngSecretSettingsPath is where a native SearXNG container receives
// its secret settings; the governed entrypoint appends the file to
// settings.yml when it exists.
const SearxngSecretSettingsPath = "/run/stackkit/secrets/searxng-settings.yml"

// NativeDockerSpec is the complete input of one native workload root.
