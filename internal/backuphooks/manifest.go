// Package backuphooks materializes its package-local database quiesce-hook
// contract (db-hooks.cue, embedded at build time) into the
// generated backup-hooks.json manifest, and gives the node-side backup engine
// a typed view of it. CUE remains the single source of truth: this package
// never defines hooks of its own, it only evaluates and transports them.
package backuphooks

import (
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// SchemaVersion identifies the manifest wire format.
const SchemaVersion = "backup-hooks/v1"

// ManifestFileName is the artifact name under the generated .stackkit/
// metadata directory (mirrors backup-recovery-plan.json).
const ManifestFileName = "backup-hooks.json"

// Manifest is the generated artifact consumed by the node-side backup_run
// handler. Hooks carry the concretized CUE defaults so the executor never
// re-derives contract values.
type Manifest struct {
	SchemaVersion string `json:"schemaVersion"`
	Hooks         []Hook `json:"hooks"`
}

// Hook mirrors db-hooks.cue #DBHook. Engine-specific blocks are
// pointers so JSON round-trips stay sparse.
type Hook struct {
	Engine    string        `json:"engine"`
	Container string        `json:"container"`
	Detect    Detect        `json:"detect"`
	Sqlite    *SqliteHook   `json:"sqlite,omitempty"`
	Postgres  *PostgresHook `json:"postgres,omitempty"`
	Redis     *RedisHook    `json:"redis,omitempty"`
	Mariadb   *MariadbHook  `json:"mariadb,omitempty"`
	Mongodb   *MongodbHook  `json:"mongodb,omitempty"`
}

type Detect struct {
	ImagePattern  string `json:"imagePattern,omitempty"`
	VolumePattern string `json:"volumePattern,omitempty"`
	EnvVar        string `json:"envVar,omitempty"`
}

type SqliteHook struct {
	DBFile  string `json:"dbFile"`
	OutFile string `json:"outFile"`
}

type PostgresHook struct {
	Database       string `json:"database"`
	User           string `json:"user"`
	OutFile        string `json:"outFile"`
	IncludeGlobals bool   `json:"includeGlobals"`
}

type RedisHook struct {
	DumpDir       string `json:"dumpDir"`
	BgsaveTimeout int    `json:"bgsaveTimeout"`
	CacheOnly     bool   `json:"cacheOnly"`
}

type MariadbHook struct {
	User    string `json:"user"`
	OutFile string `json:"outFile"`
}

type MongodbHook struct {
	OutDir string `json:"outDir"`
}

// Build evaluates the embedded db-hooks contract and returns the manifest
// with the builtin hooks and their defaults concretized.
func Build() (Manifest, error) {
	ctx := cuecontext.New()
	value := ctx.CompileBytes(dbHooksCUE)
	if err := value.Err(); err != nil {
		return Manifest{}, fmt.Errorf("compile db-hooks contract: %w", err)
	}
	builtin := value.LookupPath(cue.ParsePath("#BuiltinHooks"))
	if err := builtin.Err(); err != nil {
		return Manifest{}, fmt.Errorf("lookup #BuiltinHooks: %w", err)
	}
	var hooks []Hook
	if err := builtin.Decode(&hooks); err != nil {
		return Manifest{}, fmt.Errorf("decode #BuiltinHooks: %w", err)
	}
	if len(hooks) == 0 {
		return Manifest{}, fmt.Errorf("db-hooks contract yielded no builtin hooks")
	}
	return Manifest{SchemaVersion: SchemaVersion, Hooks: hooks}, nil
}

// MarshalIndent renders the manifest in the generated-artifact style
// (trailing newline, two-space indent) used by backup-recovery-plan.json.
func (m Manifest) MarshalIndent() ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
