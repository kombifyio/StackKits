package backupexec

// Pre-snapshot database quiesce hooks. backup_run loads the generated
// .stackkit/backup-hooks.json manifest (rendered from the CUE contract
// internal/backuphooks/db-hooks.cue) and runs each hook against its database
// container before the Kopia snapshot, so the snapshot carries a consistent
// dump instead of torn database files.
//
// v1 executes postgres and redis hooks — the pair the Immich photo stack
// needs. Other engines are reported as skipped so the caller sees honestly
// which volumes are snapshotted without quiesce.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuphooks"
)

// ContainerExecutor runs a command inside an arbitrary container — unlike
// Executor it is not bound to the kopia-agent. Hook commands run against the
// database containers themselves.
type ContainerExecutor func(ctx context.Context, container string, command []string) (string, error)

// HookResult reports one hook execution for run-state and backup_status.
type HookResult struct {
	Container string `json:"container"`
	Engine    string `json:"engine"`
	Status    string `json:"status"` // ok | skipped | failed
	Detail    string `json:"detail,omitempty"`
}

const (
	HookStatusOK      = "ok"
	HookStatusSkipped = "skipped"
	HookStatusFailed  = "failed"
)

// LoadHookManifest reads the generated manifest from the deployed stack's
// tofu_dir. A missing manifest is not an error: stacks generated before the
// manifest existed simply run without quiesce hooks.
func LoadHookManifest(tofuDir string) (*backuphooks.Manifest, error) {
	if tofuDir == "" {
		return nil, nil
	}
	path := filepath.Join(filepath.Clean(tofuDir), ".stackkit", backuphooks.ManifestFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup hook manifest %s: %w", path, err)
	}
	var manifest backuphooks.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse backup hook manifest %s: %w", path, err)
	}
	return &manifest, nil
}

// RunPreSnapshotHooks executes every hook whose container exists on this
// node. A postgres dump failure fails the run (the database class would be
// inconsistent without it); redis cache-only hooks and unsupported engines
// are reported as skipped.
func RunPreSnapshotHooks(ctx context.Context, exec ContainerExecutor, manifest *backuphooks.Manifest) ([]HookResult, error) {
	if manifest == nil || len(manifest.Hooks) == 0 {
		return nil, nil
	}
	results := make([]HookResult, 0, len(manifest.Hooks))
	var failure error
	for _, hook := range manifest.Hooks {
		result := runHook(ctx, exec, hook)
		results = append(results, result)
		if result.Status == HookStatusFailed && failure == nil {
			failure = fmt.Errorf("pre-snapshot hook %s (%s) failed: %s", hook.Container, hook.Engine, result.Detail)
		}
	}
	return results, failure
}

func runHook(ctx context.Context, exec ContainerExecutor, hook backuphooks.Hook) HookResult {
	result := HookResult{Container: hook.Container, Engine: hook.Engine}
	switch hook.Engine {
	case "postgres":
		return runPostgresHook(ctx, exec, hook, result)
	case "redis":
		return runRedisHook(ctx, exec, hook, result)
	default:
		result.Status = HookStatusSkipped
		result.Detail = "engine not supported by the v1 hook executor — volume snapshotted without quiesce"
		return result
	}
}

// containerMissing distinguishes "hook target container does not exist" from
// "command failed inside a running container". Only the typed sentinel and
// the docker daemon's own container-level phrases classify as missing —
// generic markers like a bare "not found" would misread an in-container
// failure such as "sh: pg_dump: not found" as skipped and silently persist a
// torn database backup.
func containerMissing(out string, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContainerNotPresent) {
		return true
	}
	combined := out + "\n" + err.Error()
	for _, marker := range []string{"No such container", "is not running"} {
		if containsFold(combined, marker) {
			return true
		}
	}
	return false
}

func runPostgresHook(ctx context.Context, exec ContainerExecutor, hook backuphooks.Hook, result HookResult) HookResult {
	settings := hook.Postgres
	if settings == nil {
		result.Status = HookStatusFailed
		result.Detail = "postgres hook without postgres settings"
		return result
	}
	// $-forms in database/user/outFile expand inside the container shell, so
	// the hook picks up the container's own POSTGRES_* / PGDATA environment.
	script := fmt.Sprintf(
		`set -e; mkdir -p "$(dirname %[3]s)"; pg_dump -U %[1]s --format=custom --file=%[3]s %[2]s`,
		shellWord(settings.User), shellWord(settings.Database), shellWord(settings.OutFile),
	)
	if settings.IncludeGlobals {
		script += fmt.Sprintf(`; pg_dumpall -U %[1]s --globals-only > "$(dirname %[2]s)/globals.sql"`,
			shellWord(settings.User), shellWord(settings.OutFile))
	}
	out, err := exec(ctx, hook.Container, []string{"sh", "-c", script})
	if err != nil {
		if containerMissing(out, err) {
			result.Status = HookStatusSkipped
			result.Detail = "container not present on this node"
			return result
		}
		result.Status = HookStatusFailed
		result.Detail = firstNonEmpty(out, err.Error())
		return result
	}
	result.Status = HookStatusOK
	result.Detail = "pg_dump completed to " + settings.OutFile
	return result
}

func runRedisHook(ctx context.Context, exec ContainerExecutor, hook backuphooks.Hook, result HookResult) HookResult {
	settings := hook.Redis
	if settings == nil {
		result.Status = HookStatusFailed
		result.Detail = "redis hook without redis settings"
		return result
	}
	if settings.CacheOnly {
		result.Status = HookStatusSkipped
		result.Detail = "cache-only redis — volume snapshotted as-is"
		return result
	}
	timeout := settings.BgsaveTimeout
	if timeout <= 0 {
		timeout = 30
	}
	script := fmt.Sprintf(
		`set -e; prev=$(redis-cli LASTSAVE); redis-cli BGSAVE >/dev/null; i=0; while [ "$i" -lt %d ]; do cur=$(redis-cli LASTSAVE); [ "$cur" != "$prev" ] && exit 0; i=$((i+1)); sleep 1; done; echo "BGSAVE did not complete within %d s" >&2; exit 1`,
		timeout, timeout,
	)
	out, err := exec(ctx, hook.Container, []string{"sh", "-c", script})
	if err != nil {
		if containerMissing(out, err) {
			result.Status = HookStatusSkipped
			result.Detail = "container not present on this node"
			return result
		}
		result.Status = HookStatusFailed
		result.Detail = firstNonEmpty(out, err.Error())
		return result
	}
	result.Status = HookStatusOK
	result.Detail = "BGSAVE completed into " + settings.DumpDir
	return result
}

// shellWord wraps a manifest value for safe interpolation into the hook
// shell script while keeping $VAR forms expandable: values are placed in
// double quotes with backslash, backtick, and double-quote escaped.
var shellWordEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`")

func shellWord(value string) string {
	return `"` + shellWordEscaper.Replace(value) + `"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
