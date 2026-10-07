package architecturev2renderer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"

	"github.com/kombifyio/stackkits/internal/secretexec"
)

// Stage 2 of ADR-0045 (addendum 2026-10-01): a workload root whose
// containers, networks, volumes and images are native kreuzwerker/docker
// resources instead of the Stage 1 Compose wrapper. The root is translated
// from the typed workload Compose model (workload_compose_model.go) the
// wrapper's compose.yaml is rendered from, so both targets describe the same
// containers; any setting without an exact native translation fails closed.
const (
	// NativeDockerProviderVersion is the exact kreuzwerker/docker release
	// every native root pins; offline packaging mirrors exactly this one.
	NativeDockerProviderVersion = "4.6.0"
	// NativeDockerWaitTimeoutSeconds bounds the provider's health wait, like
	// the wrapper's `docker compose up --wait-timeout`.
	NativeDockerWaitTimeoutSeconds = 600
	// NativeDockerSecretDir is the owner-only directory beside compose.yaml
	// that holds one secret env file per container.
	NativeDockerSecretDir = "secrets"
	// nativeDockerSecretMountDir is where a container reads its env file.
	nativeDockerSecretMountDir = "/run/stackkit/env"
	// nativeDockerStopGraceSeconds matches Compose's default stop timeout.
	nativeDockerStopGraceSeconds = 10
	// NativeDockerParityGapOOMScoreAdj names the governed OOM bias the
	// provider cannot express (no oom_score_adj attribute in 4.6.0).
	NativeDockerParityGapOOMScoreAdj = "oom_score_adj"
)

// nativeDockerPilotModules is the closed set of modules an operator may opt
// into native execution (STACKKIT_OPENTOFU_NATIVE_MODULES). Admission needs
// the module's full Compose document inside the renderer's translation and
// its own parity receipts (plan 20, evidence/); secret delivery alone does
// not admit a module. Admission is opt-in only: no default changes.
var nativeDockerPilotModules = map[string]struct{}{
	"stackkits-vaultwarden-runtime": {},
	// S2.1: Paperless-ngx, PostgreSQL and Valkey (health checks, depends_on,
	// command, NAME_FILE secrets); receipt
	// evidence/stage2-s21-native-paperless-2026-10-02.json.
	"stackkits-paperless-runtime": {},
	// Mosquitto: the LAN listener and the governed command's Compose secret
	// file (provider upload); receipt
	// evidence/stage2-native-mosquitto-2026-10-03.json.
	"stackkits-mosquitto-runtime": {},
	// Stalwart: the published mail ports, an egress network and the governed
	// entrypoint's Compose secret file (provider upload); receipt
	// evidence/stage2-native-stalwart-2026-10-03.json.
	"stackkits-stalwart-runtime": {},
	// zigbee2mqtt: the USB device and the MQTT password reference file, a
	// Compose secret file (provider upload) on both executions; receipt
	// evidence/stage2-native-zigbee2mqtt-2026-10-03.json.
	"stackkits-zigbee2mqtt-runtime": {},
	// Modules whose secrets the fixed-path readers cover; receipts
	// evidence/stage2-native-<module>-2026-10-03.json.
	// SearXNG: the secret settings file.
	"stackkits-searxng-runtime": {},
	// Roundcube: the des_key file and the governed project files.
	"stackkits-roundcube-runtime": {},
	// Euro-Office: the JWT secret pre-seeded in the data volume.
	"stackkits-euro-office-runtime": {},
	// Wave 1 single-container modules without secrets; receipts
	// evidence/stage2-native-<module>-2026-10-03.json.
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
type NativeDockerSpec struct {
	ModuleRef   string
	ProjectName string
	// Compose is the exact workload Compose document the wrapper would embed.
	Compose []byte
	// Wait makes the provider wait for container health, like `up --wait`.
	Wait bool
	// SecretExecBinary is the host path of the static governed entrypoint
	// shim (the stackkit binary, internal/secretexec). Only a container
	// whose governed reader is the shim needs it; without it such a root
	// fails closed.
	SecretExecBinary string
	// PilotNetworkAliases renders the S2.0 pilot's network attachments,
	// which lacked the Compose service aliases. Only Advanced restore uses
	// it, to recognise a checkpoint root the pilot release rendered.
	PilotNetworkAliases bool
}

// NativeDockerSecretBinding maps one container variable to the private .env
// interpolation variable that holds its value. In a YAML file Name is the
// dotted key path; in a dotenv file it is the variable the file sets.
type NativeDockerSecretBinding struct {
	Name     string
	Variable string
}

// Formats of a secret file.
const (
	// NativeDockerSecretFormatDotenv is one dotenv file with every binding.
	NativeDockerSecretFormatDotenv = "dotenv"
	// NativeDockerSecretFormatValue is exactly one value, no newline.
	NativeDockerSecretFormatValue = "value"
	// NativeDockerSecretFormatYAML is a YAML mapping of the bound values
	// under their dotted key paths, each a double-quoted scalar.
	NativeDockerSecretFormatYAML = "yaml"
	// NativeDockerSecretFormatPgpass is one libpq password file line that
	// matches every host, port, database and user.
	NativeDockerSecretFormatPgpass = "pgpass"
)

// NativeDockerSecretFile is one owner-only secret file the executor writes
// from the private .env before apply into the project's secrets directory;
// the root only mounts it read-only and records its digest.
type NativeDockerSecretFile struct {
	Service  string
	RelPath  string
	Format   string
	Bindings []NativeDockerSecretBinding
	// Uploaded marks a file the provider copies into the container (a
	// Compose secret file or a Files reader) at Target with Owner, Group
	// and Permissions instead of mounting it.
	Uploaded     bool
	Target       string
	Owner, Group string
	Permissions  int
}

// Mode is the file mode of the written secret file. A dotenv file is read
// by a root process (Vaultwarden); a mounted value file is read by the
// container's own user (postgres runs its entrypoint as uid 999), so it is
// readable inside the owner-only 0700 directory like a Compose secret
// (0444); host users cannot traverse to it. An uploaded file is read only by
// the OpenTofu process, which copies it in with the declared owner and mode.
func (f NativeDockerSecretFile) Mode() os.FileMode {
	if f.Uploaded {
		return 0o600
	}
	if f.Format == NativeDockerSecretFormatValue {
		return 0o444
	}
	return 0o600
}

// NativeDockerRoot is a rendered native root and what it needs beside it.
type NativeDockerRoot struct {
	Config      []byte
	SecretFiles []NativeDockerSecretFile
	// ParityGaps lists governed Compose settings the provider cannot express.
	ParityGaps []string
}

var (
	nativeSecretReferencePattern = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]{0,127}):\?required\}$`)
	nativeSecretVariablePattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	nativeIdentifierPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	nativeBindSourcePattern      = regexp.MustCompile(`^\./[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	nativeLoopbackPortPattern    = regexp.MustCompile(`^127\.0\.0\.1::([0-9]{1,5})$`)
	// A LAN listener or a published mail port: the same port on every host
	// address, as the Compose renderer emits it.
	nativeListenerPortPattern = regexp.MustCompile(`^([0-9]{1,5}):([0-9]{1,5})/(tcp|udp)$`)
	nativeDevicePattern       = regexp.MustCompile(`^(/dev/[A-Za-z0-9._/-]+):(/dev/[A-Za-z0-9._/-]+)$`)
	nativeMemoryPattern       = regexp.MustCompile(`^([1-9][0-9]{0,6})([mg])$`)
	nativeResourceNamePattern = regexp.MustCompile(`[^a-z0-9_]`)
	nativeDurationPattern     = regexp.MustCompile(`^[0-9]{1,6}(ms|s|m|h)$`)
	nativeStopSignalPattern   = regexp.MustCompile(`^SIG[A-Z0-9]{1,12}$`)
	nativeNumericIDPattern    = regexp.MustCompile(`^[0-9]{1,10}$`)
)

// nativeDockerTranslatedServiceFields are the WorkloadComposeService fields
// the native renderer translates. A service that sets any other field (a
// device, a sandbox runtime, a field the Compose renderer starts to emit
// later) fails closed instead of losing that setting.
var nativeDockerTranslatedServiceFields = map[string]struct{}{
	"Image": {}, "Restart": {}, "Logging": {}, "OOMScoreAdj": {}, "Deploy": {}, "Command": {}, "Entrypoint": {},
	"DependsOn": {}, "Environment": {}, "Volumes": {}, "Networks": {}, "Ports": {}, "ExtraHosts": {},
	"StopSignal": {}, "Init": {}, "Labels": {}, "Healthcheck": {}, "Secrets": {}, "Devices": {}, "User": {},
}

// nativeDockerRender is the state of one native root translation.
type nativeDockerRender struct {
	spec        NativeDockerSpec
	document    WorkloadComposeDocument
	body        *hclwrite.Body
	readers     map[string]nativeDockerSecretReader
	networkRefs map[string]hclwrite.Tokens
	volumeRefs  map[string]hclwrite.Tokens
	oneShot     map[string]bool
	root        NativeDockerRoot
	gaps        map[string]struct{}
}

// RenderNativeDockerOpenTofu translates one workload Compose document, decoded
// strictly into the typed workload model, into a native root. Secret
// environment values (`${VAR:?required}`) and Compose secret files are bound
// to owner-only files; literal values keep Compose's `$$` escape semantics.
// Containers keep the Compose project labels, so the StackKits observation,
// backup quiesce and removal owners keep finding them. Anything the provider
// cannot express exactly fails closed.
func RenderNativeDockerOpenTofu(spec NativeDockerSpec) (NativeDockerRoot, error) {
	path := "renderer.native-docker"
	if !composePayloadProjectPattern.MatchString(spec.ProjectName) {
		return NativeDockerRoot{}, fail(ErrRendererFailure, path+".projectName", "must be a Docker Compose project name")
	}
	render := &nativeDockerRender{
		spec: spec, readers: nativeDockerSecretReaders[spec.ModuleRef], networkRefs: map[string]hclwrite.Tokens{},
		volumeRefs: map[string]hclwrite.Tokens{}, oneShot: map[string]bool{}, gaps: map[string]struct{}{},
	}
	decoder := yaml.NewDecoder(bytes.NewReader(spec.Compose))
	decoder.KnownFields(true)
	if err := decoder.Decode(&render.document); err != nil {
		return NativeDockerRoot{}, wrap(ErrRendererFailure, path+".compose", "decode the typed workload Compose model", err)
	}
	if render.document.Name != spec.ProjectName || len(render.document.Services) == 0 {
		return NativeDockerRoot{}, fail(ErrRendererFailure, path+".compose.name", "Compose project differs from the workload project")
	}

	file := hclwrite.NewEmptyFile()
	render.body = file.Body()
	terraform := render.body.AppendNewBlock("terraform", nil).Body()
	terraform.SetAttributeValue("required_version", cty.StringVal(ComposePayloadOpenTofuRequiredVersion))
	terraform.AppendNewBlock("required_providers", nil).Body().SetAttributeValue("docker", cty.ObjectVal(map[string]cty.Value{
		"source": cty.StringVal("kreuzwerker/docker"), "version": cty.StringVal("= " + NativeDockerProviderVersion),
	}))
	render.body.AppendNewline()
	render.body.AppendNewBlock("provider", []string{"docker"})

	if err := render.networks(path); err != nil {
		return NativeDockerRoot{}, err
	}
	if err := render.volumes(path); err != nil {
		return NativeDockerRoot{}, err
	}
	for name, service := range render.document.Services {
		lifecycle := service.Labels[WorkloadComposeLifecycleLabel]
		if lifecycle != "" && lifecycle != "daemon" && lifecycle != WorkloadComposeLifecycleOnce {
			return NativeDockerRoot{}, fail(ErrRendererFailure, path+".services."+name+".labels", "unknown component lifecycle %q", lifecycle)
		}
		render.oneShot[name] = lifecycle == WorkloadComposeLifecycleOnce
	}
	for _, name := range sortedKeys(render.document.Services) {
		if err := render.service(path+".services."+name, name, render.document.Services[name]); err != nil {
			return NativeDockerRoot{}, err
		}
	}
	for gap := range render.gaps {
		render.root.ParityGaps = append(render.root.ParityGaps, gap)
	}
	sort.Strings(render.root.ParityGaps)
	render.root.Config = hclwrite.Format(file.Bytes())
	return render.root, nil
}

func (r *nativeDockerRender) networks(path string) error {
	for _, key := range sortedKeys(r.document.Networks) {
		network := r.document.Networks[key]
		if !nativeIdentifierPattern.MatchString(key) {
			return fail(ErrRendererFailure, path+".networks", "network %q is not a portable name", key)
		}
		if network.External {
			if network.Name == "" || network.Internal || !nativeIdentifierPattern.MatchString(network.Name) {
				return fail(ErrRendererFailure, path+".networks."+key, "an external network needs exactly one portable name")
			}
			// Owned by the core root or a peer workload; referenced by name only.
			r.networkRefs[key] = hclwrite.TokensForValue(cty.StringVal(network.Name))
			continue
		}
		if network.Name != "" {
			return fail(ErrRendererFailure, path+".networks."+key, "a project network carries no explicit name")
		}
		resource := nativeResourceName("network", key)
		r.body.AppendNewline()
		block := r.body.AppendNewBlock("resource", []string{"docker_network", resource}).Body()
		block.SetAttributeValue("name", cty.StringVal(r.spec.ProjectName+"_"+key))
		if network.Internal {
			block.SetAttributeValue("internal", cty.True)
		}
		appendNativeLabels(block, map[string]string{
			"com.docker.compose.project": r.spec.ProjectName, "com.docker.compose.network": key,
		})
		r.networkRefs[key] = nativeTraversal("docker_network", resource, "name")
	}
	return nil
}

func (r *nativeDockerRender) volumes(path string) error {
	for _, key := range sortedKeys(r.document.Volumes) {
		if !nativeIdentifierPattern.MatchString(key) || len(r.document.Volumes[key]) != 0 {
			return fail(ErrRendererFailure, path+".volumes", "volume %q must be a plain project volume", key)
		}
		resource := nativeResourceName("volume", key)
		r.body.AppendNewline()
		block := r.body.AppendNewBlock("resource", []string{"docker_volume", resource}).Body()
		block.SetAttributeValue("name", cty.StringVal(r.spec.ProjectName+"_"+key))
		appendNativeLabels(block, map[string]string{
			"com.docker.compose.project": r.spec.ProjectName, "com.docker.compose.volume": key,
		})
		// Data outlives the root like `docker compose down` without -v, and a
		// volume Compose created (with its per-version labels) is adopted
		// instead of replaced. Destroy runs with -suppress-forget-errors.
		lifecycle := block.AppendNewBlock("lifecycle", nil).Body()
		lifecycle.SetAttributeValue("destroy", cty.False)
		lifecycle.SetAttributeRaw("ignore_changes", hclwrite.TokensForTuple([]hclwrite.Tokens{hclwrite.TokensForIdentifier("labels")}))
		r.volumeRefs[key] = nativeTraversal("docker_volume", resource, "name")
	}
	return nil
}

// service renders one container. A daemon is kept running (and, with Wait,
// waited for until healthy, like `up --wait`); a one-shot component runs to
// completion during apply, and a non-zero exit fails the apply like Compose's
// service_completed_successfully.
//
//nolint:gocyclo // One closed translation boundary; every branch fails closed.
func (r *nativeDockerRender) service(servicePath, name string, service WorkloadComposeService) error {
	if !nativeIdentifierPattern.MatchString(name) {
		return fail(ErrRendererFailure, servicePath, "service name is not portable")
	}
	if field := nativeUntranslatedServiceField(service); field != "" {
		return fail(ErrRendererFailure, servicePath+"."+field, "Compose key has no native translation")
	}
	ref, digest, pinned := strings.Cut(service.Image, "@sha256:")
	if !pinned || ref == "" || len(digest) != 64 {
		return fail(ErrRendererFailure, servicePath+".image", "image must be pinned by digest")
	}
	oneShot := r.oneShot[name]
	imageResource := nativeResourceName("image", name)
	r.body.AppendNewline()
	image := r.body.AppendNewBlock("resource", []string{"docker_image", imageResource}).Body()
	image.SetAttributeValue("name", cty.StringVal(service.Image))
	image.SetAttributeValue("keep_locally", cty.True)

	environment := []string{}
	var secrets []NativeDockerSecretBinding
	for _, key := range sortedKeys(service.Environment) {
		value := service.Environment[key]
		if match := nativeSecretReferencePattern.FindStringSubmatch(value); match != nil {
			secrets = append(secrets, NativeDockerSecretBinding{Name: key, Variable: match[1]})
			continue
		}
		literal, err := unescapeComposeLiteral(value)
		if err != nil {
			return wrap(ErrRendererFailure, servicePath+".environment."+key, "environment value is not a literal", err)
		}
		environment = append(environment, key+"="+literal)
	}

	containerResource := nativeResourceName("container", name)
	r.body.AppendNewline()
	block := r.body.AppendNewBlock("resource", []string{"docker_container", containerResource}).Body()
	block.SetAttributeValue("name", cty.StringVal(r.spec.ProjectName+"-"+name+"-1"))
	// The pinned reference, not the image ID: the container then reports
	// ref@digest like a Compose container, which the StackKits observation
	// compares with the governed digest.
	block.SetAttributeTraversal("image", hcl.Traversal{
		hcl.TraverseRoot{Name: "docker_image"}, hcl.TraverseAttr{Name: imageResource}, hcl.TraverseAttr{Name: "name"},
	})
	if err := r.dependencies(servicePath, block, service); err != nil {
		return err
	}
	entrypoint, command, shim, err := r.shimStart(servicePath, name, digest, service, secrets)
	if err != nil {
		return err
	}
	for _, argument := range []struct {
		attribute string
		values    []string
	}{{"entrypoint", entrypoint}, {"command", command}} {
		if len(argument.values) == 0 {
			continue
		}
		list, err := nativeLiteralList(argument.values)
		if err != nil {
			return wrap(ErrRendererFailure, servicePath+"."+argument.attribute, "argument is not a literal", err)
		}
		block.SetAttributeValue(argument.attribute, list)
	}
	if service.Restart != "" {
		if service.Restart != "unless-stopped" && service.Restart != "no" && service.Restart != "always" && service.Restart != "on-failure" {
			return fail(ErrRendererFailure, servicePath+".restart", "unsupported restart policy")
		}
		if oneShot && service.Restart != "no" {
			return fail(ErrRendererFailure, servicePath+".restart", "a one-shot component is never restarted")
		}
		block.SetAttributeValue("restart", cty.StringVal(service.Restart))
	}
	if oneShot {
		if service.Healthcheck != nil {
			return fail(ErrRendererFailure, servicePath+".healthcheck", "a one-shot component has no health state")
		}
		// Created, started and attached until it exits; the exited container
		// is not drift.
		block.SetAttributeValue("must_run", cty.False)
		block.SetAttributeValue("attach", cty.True)
	} else if r.spec.Wait && (service.Healthcheck != nil || !nativeDockerNoImageHealthcheck[r.spec.ModuleRef][name]) {
		// The provider refuses to wait for a container without any health
		// check; for those `up --wait` only waits for running, which the
		// provider does anyway, and the StackKits completion probes the
		// HTTP health contract afterwards.
		block.SetAttributeValue("wait", cty.True)
		block.SetAttributeValue("wait_timeout", cty.NumberIntVal(NativeDockerWaitTimeoutSeconds))
	}
	block.SetAttributeValue("destroy_grace_seconds", cty.NumberIntVal(nativeDockerStopGraceSeconds))
	if service.StopSignal != "" {
		if !nativeStopSignalPattern.MatchString(service.StopSignal) {
			return fail(ErrRendererFailure, servicePath+".stop_signal", "stop signal is not a signal name")
		}
		block.SetAttributeValue("stop_signal", cty.StringVal(service.StopSignal))
	}
	if service.Init != nil {
		block.SetAttributeValue("init", cty.BoolVal(*service.Init))
	}
	if service.User != "" {
		if service.User != "0:0" {
			return fail(ErrRendererFailure, servicePath+".user", "only the governed root user override is admitted")
		}
		block.SetAttributeValue("user", cty.StringVal(service.User))
	}
	if err := nativeResources(servicePath, block, service.Deploy); err != nil {
		return err
	}
	if service.Logging != nil {
		block.SetAttributeValue("log_driver", cty.StringVal(service.Logging.Driver))
		if len(service.Logging.Options) > 0 {
			options := map[string]cty.Value{}
			for key, value := range service.Logging.Options {
				options[key] = cty.StringVal(value)
			}
			block.SetAttributeValue("log_opts", cty.MapVal(options))
		}
	}
	if service.OOMScoreAdj != nil && *service.OOMScoreAdj != 0 {
		r.gaps[NativeDockerParityGapOOMScoreAdj] = struct{}{}
	}
	if err := nativeHealthcheck(servicePath, block, service.Healthcheck); err != nil {
		return err
	}

	labels := map[string]string{}
	for key, value := range service.Labels {
		if strings.HasPrefix(key, "com.docker.compose.") {
			return fail(ErrRendererFailure, servicePath+".labels", "the workload may not set Compose-owned labels")
		}
		labels[key] = value
	}
	serviceDigest, err := nativeServiceConfigDigest(service)
	if err != nil {
		return wrap(ErrRendererFailure, servicePath, "digest service", err)
	}
	labels["com.docker.compose.project"] = r.spec.ProjectName
	labels["com.docker.compose.service"] = name
	labels["com.docker.compose.container-number"] = "1"
	labels["com.docker.compose.oneoff"] = "False"
	// Compose lists a container only when it carries a config hash.
	labels["com.docker.compose.config-hash"] = hex.EncodeToString(serviceDigest[:])

	serviceFiles, err := r.secretEnvironment(servicePath, name, secrets, &environment)
	if err != nil {
		return err
	}
	for _, file := range serviceFiles {
		if file.Uploaded {
			appendNativeUpload(block, file)
			continue
		}
		appendNativeBindMount(block, file.RelPath, nativeDockerSecretMountDir+"/"+strings.TrimPrefix(file.RelPath, NativeDockerSecretDir+"/"))
	}
	if shim {
		mounts := block.AppendNewBlock("mounts", nil).Body()
		mounts.SetAttributeValue("type", cty.StringVal("bind"))
		mounts.SetAttributeValue("source", cty.StringVal(r.spec.SecretExecBinary))
		mounts.SetAttributeValue("target", cty.StringVal(secretexec.MountPath))
		mounts.SetAttributeValue("read_only", cty.True)
		// A new shim binary replaces the container, like a new image.
		label := block.AppendNewBlock("labels", nil).Body()
		label.SetAttributeValue("label", cty.StringVal("io.stackkit.secret-exec-digest"))
		label.SetAttributeRaw("value", hclwrite.TokensForFunctionCall("filesha256", hclwrite.TokensForValue(cty.StringVal(r.spec.SecretExecBinary))))
	}
	uploads, err := r.secretUploads(servicePath, name, block, service.Secrets)
	if err != nil {
		return err
	}
	serviceFiles = append(serviceFiles, uploads...)
	r.root.SecretFiles = append(r.root.SecretFiles, serviceFiles...)
	envValues := make([]cty.Value, 0, len(environment))
	for _, entry := range environment {
		envValues = append(envValues, cty.StringVal(entry))
	}
	if len(envValues) > 0 {
		block.SetAttributeValue("env", cty.SetVal(envValues))
	}

	if err := r.mounts(servicePath, block, service.Volumes); err != nil {
		return err
	}
	networks := append([]string(nil), service.Networks...)
	sort.Strings(networks)
	for _, network := range networks {
		reference, known := r.networkRefs[network]
		if !known {
			return fail(ErrRendererFailure, servicePath+".networks", "network %q is not declared", network)
		}
		attachment := block.AppendNewBlock("networks_advanced", nil).Body()
		attachment.SetAttributeRaw("name", reference)
		if !r.spec.PilotNetworkAliases {
			// Compose's aliases: peers reach a service by its service name.
			attachment.SetAttributeValue("aliases", cty.SetVal([]cty.Value{
				cty.StringVal(r.spec.ProjectName + "-" + name + "-1"), cty.StringVal(name),
			}))
		}
	}
	ports, err := nativePorts(servicePath, service.Ports)
	if err != nil {
		return err
	}
	for _, port := range ports {
		attributes := block.AppendNewBlock("ports", nil).Body()
		attributes.SetAttributeValue("internal", cty.NumberIntVal(int64(port.number)))
		if !port.listener {
			attributes.SetAttributeValue("ip", cty.StringVal("127.0.0.1"))
			continue
		}
		// Compose publishes "N:N/proto" on every host address (HostIp ""),
		// IPv6 included; the provider's default would bind 0.0.0.0 only.
		attributes.SetAttributeValue("external", cty.NumberIntVal(int64(port.number)))
		attributes.SetAttributeValue("protocol", cty.StringVal(port.protocol))
		attributes.SetAttributeValue("ip", cty.StringVal(""))
	}
	for _, device := range service.Devices {
		match := nativeDevicePattern.FindStringSubmatch(device)
		if match == nil || path.Clean(match[1]) != match[1] || path.Clean(match[2]) != match[2] {
			return fail(ErrRendererFailure, servicePath+".devices", "only a host device node mapped without permissions is admitted, got %q", device)
		}
		devices := block.AppendNewBlock("devices", nil).Body()
		devices.SetAttributeValue("host_path", cty.StringVal(match[1]))
		devices.SetAttributeValue("container_path", cty.StringVal(match[2]))
		// Compose's default cgroup permissions for a device mapping.
		devices.SetAttributeValue("permissions", cty.StringVal("rwm"))
	}
	for _, entry := range service.ExtraHosts {
		host, address, ok := strings.Cut(entry, ":")
		if !ok || host == "" || address == "" {
			return fail(ErrRendererFailure, servicePath+".extra_hosts", "malformed host entry")
		}
		hosts := block.AppendNewBlock("host", nil).Body()
		hosts.SetAttributeValue("host", cty.StringVal(host))
		hosts.SetAttributeValue("ip", cty.StringVal(address))
	}
	if len(serviceFiles) > 0 {
		// A rotated secret replaces the container, like Compose recreating
		// a service whose interpolated environment changed. Only the digest
		// enters the state: the file's own for one file, a digest over the
		// file digests for several.
		value := nativeFileDigestTokens(serviceFiles[0].RelPath)
		if len(serviceFiles) > 1 {
			digests := make([]hclwrite.Tokens, 0, len(serviceFiles))
			for _, file := range serviceFiles {
				digests = append(digests, nativeFileDigestTokens(file.RelPath))
			}
			value = hclwrite.TokensForFunctionCall("sha256", hclwrite.TokensForFunctionCall("join",
				hclwrite.TokensForValue(cty.StringVal(",")), hclwrite.TokensForTuple(digests)))
		}
		digest := block.AppendNewBlock("labels", nil).Body()
		digest.SetAttributeValue("label", cty.StringVal("io.stackkit.secret-env-digest"))
		digest.SetAttributeRaw("value", value)
	}
	appendNativeLabels(block, labels)
	if oneShot {
		lifecycle := block.AppendNewBlock("lifecycle", nil).Body()
		postcondition := lifecycle.AppendNewBlock("postcondition", nil).Body()
		postcondition.SetAttributeRaw("condition", hclwrite.Tokens{
			{Type: hclsyntax.TokenIdent, Bytes: []byte("self.exit_code")},
			{Type: hclsyntax.TokenEqualOp, Bytes: []byte("==")},
			{Type: hclsyntax.TokenNumberLit, Bytes: []byte("0")},
		})
		postcondition.SetAttributeValue("error_message", cty.StringVal("one-shot component "+name+" did not complete successfully"))
	}
	return nil
}

type nativePort struct {
	number   int
	protocol string
	listener bool
}

// nativePorts parses the admitted port forms: the loopback health port
// ("127.0.0.1::N") and a listener on the same host and container port
// ("N:N/tcp" or "N:N/udp"). They are ordered by port and protocol, the order
// the provider reads them back in, so a re-plan settles.
func nativePorts(servicePath string, entries []string) ([]nativePort, error) {
	ports := make([]nativePort, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		port := nativePort{protocol: "tcp"}
		if match := nativeLoopbackPortPattern.FindStringSubmatch(entry); match != nil {
			port.number, _ = strconv.Atoi(match[1])
		} else if match := nativeListenerPortPattern.FindStringSubmatch(entry); match != nil && match[1] == match[2] {
			port.number, _ = strconv.Atoi(match[1])
			port.protocol, port.listener = match[3], true
		} else {
			return nil, fail(ErrRendererFailure, servicePath+".ports", "only the loopback health port and same-port listeners are admitted, got %q", entry)
		}
		key := strconv.Itoa(port.number) + "/" + port.protocol
		if port.number < 1 || port.number > 65535 || seen[key] {
			return nil, fail(ErrRendererFailure, servicePath+".ports", "port %q is out of range or published twice", entry)
		}
		seen[key] = true
		ports = append(ports, port)
	}
	slices.SortFunc(ports, func(a, b nativePort) int {
		if a.number != b.number {
			return a.number - b.number
		}
		return strings.Compare(a.protocol, b.protocol)
	})
	return ports, nil
}

// shimStart returns the container's entrypoint and command. For a container
// whose governed reader is the entrypoint shim, the shim becomes the
// entrypoint: it reads each secret file into the process environment and
// execs the start the container would otherwise have (the Compose
// entrypoint and command, or the image's own, pinned with its digest).
// Arguments stay Compose literals ($$ escaped) like every other value.
func (r *nativeDockerRender) shimStart(servicePath, name, digest string, service WorkloadComposeService, secrets []NativeDockerSecretBinding) ([]string, []string, bool, error) {
	reader := r.readers[name]
	if reader.Shim == nil || len(secrets) == 0 {
		return service.Entrypoint, service.Command, false, nil
	}
	if "sha256:"+digest != reader.Shim.ImageDigest {
		return nil, nil, false, fail(ErrRendererFailure, servicePath+".image", "the entrypoint shim is governed only for its pinned image")
	}
	if !filepath.IsAbs(r.spec.SecretExecBinary) || filepath.Clean(r.spec.SecretExecBinary) != r.spec.SecretExecBinary {
		return nil, nil, false, fail(ErrRendererFailure, servicePath, "the entrypoint shim needs the absolute path of its binary")
	}
	escape := func(values []string) []string {
		escaped := make([]string, len(values))
		for index, value := range values {
			escaped[index] = strings.ReplaceAll(value, "$", "$$")
		}
		return escaped
	}
	original, command := service.Entrypoint, service.Command
	if len(original) == 0 {
		original = escape(reader.Shim.Entrypoint)
		if len(command) == 0 {
			command = escape(reader.Shim.Command)
		}
	}
	entrypoint := []string{secretexec.MountPath}
	for _, binding := range secrets {
		entrypoint = append(entrypoint, "--env", binding.Name+"="+nativeDockerSecretMountDir+"/"+name+"."+binding.Name)
	}
	entrypoint = append(append(entrypoint, "--"), original...)
	return entrypoint, command, true, nil
}

// dependencies renders depends_on. Every condition the Compose renderer
// emits has an exact native order: a started daemon (created, and with Wait
// also healthy), a one-shot that completed with exit 0 (attached, with the
// exit-code postcondition), or a healthy daemon (needs Wait and a health
// check). Anything else fails closed.
func (r *nativeDockerRender) dependencies(servicePath string, block *hclwrite.Body, service WorkloadComposeService) error {
	if len(service.DependsOn) == 0 {
		return nil
	}
	references := make([]hclwrite.Tokens, 0, len(service.DependsOn))
	for _, dependency := range sortedKeys(service.DependsOn) {
		target, known := r.document.Services[dependency]
		if !known {
			return fail(ErrRendererFailure, servicePath+".depends_on", "dependency %q is not a service of the project", dependency)
		}
		switch condition := service.DependsOn[dependency].Condition; condition {
		case "service_started":
			if r.oneShot[dependency] {
				return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "a one-shot dependency must complete successfully")
			}
		case "service_completed_successfully":
			if !r.oneShot[dependency] {
				return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "only a one-shot component completes")
			}
		case "service_healthy":
			if r.oneShot[dependency] || target.Healthcheck == nil || !r.spec.Wait {
				return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "a healthy dependency needs a waited daemon with a health check")
			}
		default:
			return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "unsupported dependency condition %q", condition)
		}
		references = append(references, nativeTraversal("docker_container", nativeResourceName("container", dependency)))
	}
	block.SetAttributeRaw("depends_on", hclwrite.TokensForTuple(references))
	return nil
}

// secretEnvironment binds the secret environment values of one container to
// its governed file-based reader and adds the reader variables.
func (r *nativeDockerRender) secretEnvironment(servicePath, name string, secrets []NativeDockerSecretBinding, environment *[]string) ([]NativeDockerSecretFile, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	reader, governed := r.readers[name]
	if !governed {
		return nil, fail(ErrRendererFailure, servicePath+".environment", "service has secret values but no governed file-based secret input")
	}
	var files []NativeDockerSecretFile
	switch {
	case reader.EnvFile != "":
		files = append(files, NativeDockerSecretFile{
			Service: name, RelPath: NativeDockerSecretDir + "/" + name + ".env",
			Format: NativeDockerSecretFormatDotenv, Bindings: secrets,
		})
		*environment = append(*environment, reader.EnvFile+"="+nativeDockerSecretMountDir+"/"+name+".env")
	case reader.FileSuffix || reader.Shim != nil:
		for _, binding := range secrets {
			if reader.Shim != nil {
				// The shim reads the file; the container gets no variable.
				files = append(files, NativeDockerSecretFile{
					Service: name, RelPath: NativeDockerSecretDir + "/" + name + "." + binding.Name,
					Format: NativeDockerSecretFormatValue, Bindings: []NativeDockerSecretBinding{binding},
				})
				continue
			}
			files = append(files, NativeDockerSecretFile{
				Service: name, RelPath: NativeDockerSecretDir + "/" + name + "." + binding.Name,
				Format: NativeDockerSecretFormatValue, Bindings: []NativeDockerSecretBinding{binding},
			})
			*environment = append(*environment, binding.Name+"_FILE="+nativeDockerSecretMountDir+"/"+name+"."+binding.Name)
		}
	case len(reader.Files) > 0:
		readerFiles, err := nativeReaderFiles(servicePath, name, reader, secrets)
		if err != nil {
			return nil, err
		}
		files = append(files, readerFiles...)
		for _, variable := range sortedKeys(reader.Environment) {
			*environment = append(*environment, variable+"="+reader.Environment[variable])
		}
	default:
		return nil, fail(ErrRendererFailure, servicePath+".environment", "governed secret reader declares no form")
	}
	seen := map[string]bool{}
	for _, entry := range *environment {
		variable, _, _ := strings.Cut(entry, "=")
		if seen[variable] {
			return nil, fail(ErrRendererFailure, servicePath+".environment", "secret file variable %s conflicts with a declared variable", variable)
		}
		seen[variable] = true
	}
	sort.Strings(*environment)
	return files, nil
}

var (
	nativeSecretTargetPattern = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)
	nativeSecretKeyPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}(\.[A-Za-z_][A-Za-z0-9_]{0,63}){0,3}$`)
)

// nativeReaderFiles binds the secret values of one container to the
// fixed-path files of its Files reader. Every secret is bound by exactly
// one file and every file variable is a secret of the container, so a
// catalog change on either side fails closed instead of losing a value.
func nativeReaderFiles(servicePath, name string, reader nativeDockerSecretReader, secrets []NativeDockerSecretBinding) ([]NativeDockerSecretFile, error) {
	byName := map[string]NativeDockerSecretBinding{}
	for _, binding := range secrets {
		byName[binding.Name] = binding
	}
	bound := map[string]bool{}
	targets := map[string]bool{}
	var files []NativeDockerSecretFile
	for index, spec := range reader.Files {
		specPath := fmt.Sprintf("%s.secretReader.files[%d]", servicePath, index)
		if !nativeSecretTargetPattern.MatchString(spec.Target) || path.Clean(spec.Target) != spec.Target || targets[spec.Target] ||
			!nativeNumericIDPattern.MatchString(spec.UID) || !nativeNumericIDPattern.MatchString(spec.GID) ||
			spec.Mode <= 0 || spec.Mode > 0o777 || len(spec.Variables) == 0 ||
			(len(spec.Keys) != 0 && len(spec.Keys) != len(spec.Variables)) {
			return nil, fail(ErrRendererFailure, specPath, "secret file needs an exact path, owner, mode and variables")
		}
		targets[spec.Target] = true
		single := spec.Format == NativeDockerSecretFormatValue || spec.Format == NativeDockerSecretFormatPgpass
		switch {
		case single && (len(spec.Variables) != 1 || len(spec.Keys) != 0):
			return nil, fail(ErrRendererFailure, specPath, "a %s file holds exactly one value", spec.Format)
		case spec.Format == NativeDockerSecretFormatYAML && len(spec.Keys) != len(spec.Variables):
			return nil, fail(ErrRendererFailure, specPath, "a YAML file names a key for every value")
		case !single && spec.Format != NativeDockerSecretFormatYAML && spec.Format != NativeDockerSecretFormatDotenv:
			return nil, fail(ErrRendererFailure, specPath, "unknown secret file format %q", spec.Format)
		}
		file := NativeDockerSecretFile{
			Service: name, RelPath: NativeDockerSecretDir + "/" + name + ".file-" + strconv.Itoa(index), Format: spec.Format,
			Uploaded: true, Target: spec.Target, Owner: spec.UID, Group: spec.GID, Permissions: spec.Mode,
		}
		for position, variable := range spec.Variables {
			binding, secret := byName[variable]
			if !secret && slices.Contains(spec.Optional, variable) {
				continue
			}
			if !secret || bound[variable] {
				return nil, fail(ErrRendererFailure, specPath, "variable %s is not an unbound secret of the container", variable)
			}
			bound[variable] = true
			if len(spec.Keys) != 0 {
				if !nativeSecretKeyPattern.MatchString(spec.Keys[position]) {
					return nil, fail(ErrRendererFailure, specPath, "key %q is not a plain key path", spec.Keys[position])
				}
				binding.Name = spec.Keys[position]
			}
			file.Bindings = append(file.Bindings, binding)
		}
		if len(file.Bindings) > 0 {
			files = append(files, file)
		}
	}
	for _, binding := range secrets {
		if !bound[binding.Name] {
			return nil, fail(ErrRendererFailure, servicePath+".environment."+binding.Name, "secret has no file of the governed reader")
		}
	}
	for variable, value := range reader.Environment {
		if !nativeSecretVariablePattern.MatchString(variable) || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fail(ErrRendererFailure, servicePath+".secretReader.environment", "reader variable %s is not a plain literal", variable)
		}
	}
	return files, nil
}

// secretUploads renders the Compose secret files of one container. Compose
// copies an environment-sourced secret into the container with its owner and
// mode; the provider's upload does the same from the owner-only file the
// executor writes. The root and state hold the path and the file digest only.
func (r *nativeDockerRender) secretUploads(servicePath, name string, block *hclwrite.Body, placements []WorkloadComposeServiceSecret) ([]NativeDockerSecretFile, error) {
	var files []NativeDockerSecretFile
	targets := map[string]bool{}
	for _, placement := range placements {
		secret, declared := r.document.Secrets[placement.Source]
		if !declared || !nativeIdentifierPattern.MatchString(placement.Source) || !nativeSecretVariablePattern.MatchString(secret.Environment) {
			return nil, fail(ErrRendererFailure, servicePath+".secrets", "secret %q is not a declared environment-sourced secret", placement.Source)
		}
		if !strings.HasPrefix(placement.Target, "/") || filepath.Clean(placement.Target) != placement.Target || targets[placement.Target] ||
			!nativeNumericIDPattern.MatchString(placement.UID) || !nativeNumericIDPattern.MatchString(placement.GID) ||
			placement.Mode < 0 || placement.Mode > 0o777 {
			return nil, fail(ErrRendererFailure, servicePath+".secrets."+placement.Source, "secret file placement is not an exact path, owner and mode")
		}
		targets[placement.Target] = true
		file := NativeDockerSecretFile{
			Service: name, RelPath: NativeDockerSecretDir + "/" + name + ".secret-" + placement.Source,
			Format: NativeDockerSecretFormatValue, Uploaded: true,
			Bindings: []NativeDockerSecretBinding{{Name: placement.Source, Variable: secret.Environment}},
			Target:   placement.Target, Owner: placement.UID, Group: placement.GID, Permissions: placement.Mode,
		}
		appendNativeUpload(block, file)
		files = append(files, file)
	}
	return files, nil
}

func (r *nativeDockerRender) mounts(servicePath string, block *hclwrite.Body, volumes []any) error {
	for _, entry := range volumes {
		mount, short := entry.(string)
		if !short {
			return fail(ErrRendererFailure, servicePath+".volumes", "only the short volume form is admitted")
		}
		parts := strings.Split(mount, ":")
		readOnly := false
		if len(parts) == 3 && parts[2] == "ro" {
			readOnly = true
			parts = parts[:2]
		}
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "/") {
			return fail(ErrRendererFailure, servicePath+".volumes", "unsupported volume form %q", mount)
		}
		if nativeBindSourcePattern.MatchString(parts[0]) && !strings.Contains(parts[0], "..") {
			if !readOnly {
				return fail(ErrRendererFailure, servicePath+".volumes", "a project file mount must be read-only")
			}
			appendNativeBindMount(block, strings.TrimPrefix(parts[0], "./"), parts[1])
			continue
		}
		source, known := r.volumeRefs[parts[0]]
		if !known {
			return fail(ErrRendererFailure, servicePath+".volumes", "volume %q is not a project volume", parts[0])
		}
		mounts := block.AppendNewBlock("mounts", nil).Body()
		mounts.SetAttributeValue("type", cty.StringVal("volume"))
		mounts.SetAttributeRaw("source", source)
		mounts.SetAttributeValue("target", cty.StringVal(parts[1]))
		if readOnly {
			mounts.SetAttributeValue("read_only", cty.True)
		}
	}
	return nil
}

func nativeResources(servicePath string, block *hclwrite.Body, deploy *WorkloadComposeDeploy) error {
	if deploy == nil {
		return nil
	}
	for _, bounds := range []*WorkloadComposeResourceBounds{deploy.Resources.Limits, deploy.Resources.Reservations} {
		if bounds != nil && (bounds.CPUs != "" || len(bounds.Devices) != 0) {
			return fail(ErrRendererFailure, servicePath+".deploy", "CPU limits and device reservations have no native translation")
		}
	}
	if limits := deploy.Resources.Limits; limits != nil && limits.Memory != "" {
		megabytes, err := nativeMemoryMegabytes(limits.Memory)
		if err != nil {
			return wrap(ErrRendererFailure, servicePath+".deploy", "memory limit", err)
		}
		block.SetAttributeValue("memory", cty.NumberIntVal(megabytes))
		// Docker's effective default when only a limit is set; rendering it
		// keeps a re-plan empty.
		block.SetAttributeValue("memory_swap", cty.NumberIntVal(2*megabytes))
	}
	if reservations := deploy.Resources.Reservations; reservations != nil && reservations.Memory != "" {
		megabytes, err := nativeMemoryMegabytes(reservations.Memory)
		if err != nil {
			return wrap(ErrRendererFailure, servicePath+".deploy", "memory reservation", err)
		}
		block.SetAttributeValue("memory_reservation", cty.NumberIntVal(megabytes))
	}
	return nil
}

func nativeHealthcheck(servicePath string, block *hclwrite.Body, check *WorkloadComposeHealthcheck) error {
	if check == nil {
		return nil
	}
	if len(check.Test) < 2 || (check.Test[0] != "CMD" && check.Test[0] != "CMD-SHELL") || check.Retries < 0 {
		return fail(ErrRendererFailure, servicePath+".healthcheck", "health check must be a CMD or CMD-SHELL test")
	}
	test, err := nativeLiteralList(check.Test)
	if err != nil {
		return wrap(ErrRendererFailure, servicePath+".healthcheck", "health check argument is not a literal", err)
	}
	health := block.AppendNewBlock("healthcheck", nil).Body()
	health.SetAttributeValue("test", test)
	for _, duration := range [][2]string{{"interval", check.Interval}, {"timeout", check.Timeout}, {"start_period", check.StartPeriod}, {"start_interval", check.StartInterval}} {
		if duration[1] == "" {
			continue
		}
		if !nativeDurationPattern.MatchString(duration[1]) {
			return fail(ErrRendererFailure, servicePath+".healthcheck."+duration[0], "duration %q is not a plain duration", duration[1])
		}
		health.SetAttributeValue(duration[0], cty.StringVal(duration[1]))
	}
	if check.Retries > 0 {
		health.SetAttributeValue("retries", cty.NumberIntVal(int64(check.Retries)))
	}
	return nil
}

// nativeUntranslatedServiceField names the first set service field the
// native renderer does not translate, by its Compose key.
func nativeUntranslatedServiceField(service WorkloadComposeService) string {
	value := reflect.ValueOf(service)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if _, translated := nativeDockerTranslatedServiceFields[field.Name]; translated || value.Field(index).IsZero() {
			continue
		}
		key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		return key
	}
	return ""
}

// nativeServiceConfigDigest is the per-service Compose config hash label.
// The pilot (S2.0) digested a subset document; a service inside that subset
// keeps the same digest, so a root rendered before S2.1 re-renders byte for
// byte (Advanced restore compares the restored root with its re-rendering)
// and its container is not replaced by the upgrade. Settings S2.1 added are
// digested after it, only when set.
func nativeServiceConfigDigest(service WorkloadComposeService) ([32]byte, error) {
	type bounds struct{ Memory string }
	pilot := struct {
		Image       string
		Restart     string
		Logging     *WorkloadComposeLogging
		OOMScoreAdj *int
		Deploy      *struct {
			Resources struct{ Limits, Reservations *bounds }
		}
		Environment map[string]string
		Volumes     []any
		Networks    []string
		Ports       []string
		ExtraHosts  []string
		Labels      map[string]string
	}{
		Image: service.Image, Restart: service.Restart, Logging: service.Logging, OOMScoreAdj: service.OOMScoreAdj,
		Environment: service.Environment, Volumes: service.Volumes, Networks: service.Networks, Ports: service.Ports,
		ExtraHosts: service.ExtraHosts, Labels: service.Labels,
	}
	if service.Deploy != nil {
		pilot.Deploy = &struct {
			Resources struct{ Limits, Reservations *bounds }
		}{}
		if limits := service.Deploy.Resources.Limits; limits != nil {
			pilot.Deploy.Resources.Limits = &bounds{Memory: limits.Memory}
		}
		if reservations := service.Deploy.Resources.Reservations; reservations != nil {
			pilot.Deploy.Resources.Reservations = &bounds{Memory: reservations.Memory}
		}
	}
	encoded, err := json.Marshal(pilot)
	if err != nil {
		return [32]byte{}, err
	}
	added := WorkloadComposeService{
		Command: service.Command, Entrypoint: service.Entrypoint, DependsOn: service.DependsOn,
		StopSignal: service.StopSignal, Init: service.Init, Healthcheck: service.Healthcheck, Secrets: service.Secrets,
		Devices: service.Devices, User: service.User,
	}
	if !reflect.ValueOf(added).IsZero() {
		extension, err := json.Marshal(added)
		if err != nil {
			return [32]byte{}, err
		}
		encoded = append(append(encoded, 0), extension...)
	}
	return sha256.Sum256(encoded), nil
}

func nativeLiteralList(values []string) (cty.Value, error) {
	list := make([]cty.Value, 0, len(values))
	for _, value := range values {
		literal, err := unescapeComposeLiteral(value)
		if err != nil {
			return cty.NilVal, err
		}
		list = append(list, cty.StringVal(literal))
	}
	return cty.ListVal(list), nil
}

func nativeTraversal(names ...string) hclwrite.Tokens {
	traversal := hcl.Traversal{hcl.TraverseRoot{Name: names[0]}}
	for _, name := range names[1:] {
		traversal = append(traversal, hcl.TraverseAttr{Name: name})
	}
	return hclwrite.TokensForTraversal(traversal)
}

// RenderNativeDockerSecretFile renders one secret file from the private .env
// the native preparation wrote. A dotenv file single-quotes each value
// (dotenv literal form); a value file holds exactly the one value without a
// newline (some readers keep a trailing newline). A value that cannot be
// expressed exactly fails closed instead of being reinterpreted.
func RenderNativeDockerSecretFile(file NativeDockerSecretFile, dotenv []byte) ([]byte, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(dotenv))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		variable, value, ok := strings.Cut(line, "=")
		if !ok || variable == "" {
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "private .env has a malformed line")
		}
		values[variable] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.native-docker.env", "read private .env", err)
	}
	var out bytes.Buffer
	tree := nativeYAMLNode{}
	for _, binding := range file.Bindings {
		value, ok := values[binding.Variable]
		if !ok || value == "" {
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "private .env has no value for a bound secret")
		}
		switch file.Format {
		case NativeDockerSecretFormatYAML:
			if strings.ContainsAny(value, "\r\n\x00") || !tree.set(strings.Split(binding.Name, "."), value) {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as a YAML scalar")
			}
		case NativeDockerSecretFormatPgpass:
			// libpq: hostname:port:database:username:password, `*` matches
			// anything, and `\` escapes a colon or backslash in a field.
			if len(file.Bindings) != 1 || strings.ContainsAny(value, "\r\n\x00") {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as a password file line")
			}
			out.WriteString("*:*:*:*:" + strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(value) + "\n")
		case NativeDockerSecretFormatValue:
			if len(file.Bindings) != 1 || strings.ContainsAny(value, "\r\n\x00") || strings.TrimSpace(value) != value {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as an exact value file")
			}
			out.WriteString(value)
		case NativeDockerSecretFormatDotenv:
			if strings.ContainsAny(value, "'\r\n\x00") {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as a literal dotenv value")
			}
			fmt.Fprintf(&out, "%s='%s'\n", binding.Name, value)
		default:
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "unknown secret file format")
		}
	}
	if file.Format == NativeDockerSecretFormatYAML {
		tree.write(&out, 0)
	}
	return out.Bytes(), nil
}

// nativeYAMLNode is a mapping of a YAML secret file: a key holds either a
// string scalar or a nested mapping.
type nativeYAMLNode map[string]any

// set stores value under the key path; a path that collides with another
// value or mapping is refused.
func (n nativeYAMLNode) set(keys []string, value string) bool {
	if len(keys) == 1 {
		if _, taken := n[keys[0]]; taken {
			return false
		}
		n[keys[0]] = value
		return true
	}
	child, exists := n[keys[0]]
	if !exists {
		child = nativeYAMLNode{}
		n[keys[0]] = child
	}
	mapping, ok := child.(nativeYAMLNode)
	return ok && mapping.set(keys[1:], value)
}

// write emits the mapping in key order, each scalar double-quoted with JSON
// escapes (a JSON string is a YAML double-quoted scalar).
func (n nativeYAMLNode) write(out *bytes.Buffer, depth int) {
	for _, key := range sortedKeys(n) {
		out.WriteString(strings.Repeat("  ", depth) + key + ":")
		switch value := n[key].(type) {
		case nativeYAMLNode:
			out.WriteString("\n")
			value.write(out, depth+1)
		case string:
			var quoted bytes.Buffer
			encoder := json.NewEncoder(&quoted)
			encoder.SetEscapeHTML(false)
			_ = encoder.Encode(value)
			out.WriteString(" " + strings.TrimSuffix(quoted.String(), "\n") + "\n")
		}
	}
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nativeResourceName(kind, key string) string {
	return kind + "_" + nativeResourceNamePattern.ReplaceAllString(key, "_")
}

func appendNativeLabels(block *hclwrite.Body, labels map[string]string) {
	for _, key := range sortedKeys(labels) {
		label := block.AppendNewBlock("labels", nil).Body()
		label.SetAttributeValue("label", cty.StringVal(key))
		label.SetAttributeValue("value", cty.StringVal(labels[key]))
	}
}

// nativeProjectPathTokens is abspath("${path.module}/../<rel>"): a file in
// the workload project directory beside compose.yaml.
func nativeProjectPathTokens(rel string) hclwrite.Tokens {
	return hclwrite.TokensForFunctionCall("abspath", nativeProjectTemplateTokens(rel))
}

func nativeFileDigestTokens(rel string) hclwrite.Tokens {
	return hclwrite.TokensForFunctionCall("filesha256", nativeProjectTemplateTokens(rel))
}

func nativeProjectTemplateTokens(rel string) hclwrite.Tokens {
	return hclwrite.Tokens{
		{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		{Type: hclsyntax.TokenTemplateInterp, Bytes: []byte(`${`)},
		{Type: hclsyntax.TokenIdent, Bytes: []byte(`path.module`)},
		{Type: hclsyntax.TokenTemplateSeqEnd, Bytes: []byte(`}`)},
		{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("/../" + escapeHCLTemplateLiteral(rel))},
		{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}
}

// appendNativeUpload renders the provider upload of one secret file: the
// owner-only source beside compose.yaml is copied into the container with
// its owner and mode; the root and state hold its path and digest only.
func appendNativeUpload(block *hclwrite.Body, file NativeDockerSecretFile) {
	upload := block.AppendNewBlock("upload", nil).Body()
	upload.SetAttributeValue("file", cty.StringVal(file.Target))
	upload.SetAttributeRaw("source", nativeProjectPathTokens(file.RelPath))
	upload.SetAttributeRaw("source_hash", nativeFileDigestTokens(file.RelPath))
	upload.SetAttributeValue("owner", cty.StringVal(file.Owner))
	upload.SetAttributeValue("group", cty.StringVal(file.Group))
	upload.SetAttributeValue("permissions", cty.StringVal(fmt.Sprintf("%04o", file.Permissions)))
}

func appendNativeBindMount(block *hclwrite.Body, rel, target string) {
	mounts := block.AppendNewBlock("mounts", nil).Body()
	mounts.SetAttributeValue("type", cty.StringVal("bind"))
	mounts.SetAttributeRaw("source", nativeProjectPathTokens(rel))
	mounts.SetAttributeValue("target", cty.StringVal(target))
	mounts.SetAttributeValue("read_only", cty.True)
}

func nativeMemoryMegabytes(value string) (int64, error) {
	match := nativeMemoryPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("unsupported memory value %q", value)
	}
	amount, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, err
	}
	if match[2] == "g" {
		amount *= 1024
	}
	return amount, nil
}

// unescapeComposeLiteral undoes the renderer's `$` → `$$` escape; any other
// `$` would be a Compose interpolation and is refused.
func unescapeComposeLiteral(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '$' {
			out.WriteByte(value[index])
			continue
		}
		if index+1 < len(value) && value[index+1] == '$' {
			out.WriteByte('$')
			index++
			continue
		}
		return "", fmt.Errorf("unescaped interpolation")
	}
	return out.String(), nil
}

func firstSubmatch(match []string) string {
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// NativeDockerRootConfig reports whether an OpenTofu root configuration is a
// native Docker-provider root (docker_container resources) rather than a
// Stage 1 wrapper (terraform_data triggers). Lifecycle callers use it to pick
// the convergence of a restored or drifted root: a wrapper is forced through
// its trigger, a native root converges with a plain apply. A root carrying
// both forms is refused.
func NativeDockerRootConfig(config []byte) (bool, error) {
	file, diagnostics := hclsyntax.ParseConfig(config, "main.tf", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return false, fmt.Errorf("parse the OpenTofu root: %s", diagnostics.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return false, fmt.Errorf("OpenTofu root has no native HCL body")
	}
	containers, triggers := 0, 0
	for _, block := range body.Blocks {
		if block.Type != "resource" || len(block.Labels) != 2 {
			continue
		}
		switch block.Labels[0] {
		case "docker_container":
			containers++
		case "terraform_data":
			triggers++
		}
	}
	if containers > 0 && triggers > 0 {
		return false, fmt.Errorf("OpenTofu root mixes native containers and wrapper triggers")
	}
	return containers > 0, nil
}
