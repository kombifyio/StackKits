package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/archive"
	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/run"
)

// PostgreSQL, MySQL and MariaDB data is saved with the database's own dump
// tool (pg_dumpall / mysqldump) instead of copying its data directory: the
// dump is transactionally consistent without pausing the database and does
// not depend on the on-disk format. On restore the data volume is created
// empty, the same image initialises it with the same credentials in a
// temporary container, and the dump is loaded before Coolify starts anything.

// Database engines handled with native dumps.
const (
	EnginePostgres = "postgresql"
	EngineMySQL    = "mysql" // MySQL and MariaDB
)

const prefixDumps = "dumps"

// DumpEntry is one database container saved as a native dump.
type DumpEntry struct {
	Owner     string   `json:"owner"`     // resource uuid
	Container string   `json:"container"` // container name on the source
	Engine    string   `json:"engine"`
	Image     string   `json:"image"`
	Volume    string   `json:"volume"`   // data volume (restored empty, then loaded)
	DataDir   string   `json:"data_dir"` // where the volume is mounted
	Env       []string `json:"env"`      // container environment (credentials for init)
	Size      int64    `json:"size"`
}

func (d DumpEntry) entryName() string { return prefixDumps + "/" + d.Container + ".sql" }

type containerInfo struct {
	Name   string
	Image  string
	Env    []string
	Mounts []docker.Mount
}

func inspectContainer(ctx context.Context, id string) (*containerInfo, error) {
	out, err := run.Output(ctx, "docker", "inspect", "--format", "{{json .}}", id)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Name   string
		Config struct {
			Image string
			Env   []string
		}
		Mounts []docker.Mount
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	return &containerInfo{Name: strings.TrimPrefix(raw.Name, "/"), Image: raw.Config.Image, Env: raw.Config.Env, Mounts: raw.Mounts}, nil
}

func envValue(env []string, keys ...string) string {
	for _, k := range keys {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

// detectDump decides whether a running container is a PostgreSQL/MySQL/MariaDB
// server whose data volume can be saved as a dump. It only accepts the
// official image layouts (credentials in POSTGRES_*/MYSQL_*/MARIADB_*
// variables, data in a named volume), so a restore can re-create it exactly.
func detectDump(ctx context.Context, id, owner string) (*DumpEntry, string) {
	ci, err := inspectContainer(ctx, id)
	if err != nil {
		return nil, ""
	}
	has := func(bin string) bool {
		_, err := run.Output(ctx, "docker", "exec", id, "sh", "-c", "command -v "+bin)
		return err == nil
	}
	var engine, dataDir string
	switch {
	case envValue(ci.Env, "POSTGRES_PASSWORD", "POSTGRES_USER") != "" && has("pg_dumpall"):
		engine = EnginePostgres
		dataDir = envValue(ci.Env, "PGDATA")
		if dataDir == "" {
			dataDir = "/var/lib/postgresql/data"
		}
	case envValue(ci.Env, "MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD") != "" && (has("mysqldump") || has("mariadb-dump")):
		engine, dataDir = EngineMySQL, "/var/lib/mysql"
	default:
		return nil, ""
	}
	for _, m := range ci.Mounts {
		if m.Type != "volume" || m.Name == "" {
			continue
		}
		// The volume holds the data directory (PGDATA may be a sub folder).
		if m.Destination == dataDir || strings.HasPrefix(dataDir, strings.TrimSuffix(m.Destination, "/")+"/") {
			return &DumpEntry{Owner: owner, Container: ci.Name, Engine: engine, Image: ci.Image,
				Volume: m.Name, DataDir: m.Destination, Env: ci.Env}, ""
		}
	}
	return nil, fmt.Sprintf("%s (%s) keeps its data outside a named volume; its files are copied instead of a %s dump", ci.Name, engine, engine)
}

const pgDumpCmd = `pg_dumpall -U "${POSTGRES_USER:-postgres}" --quote-all-identifiers`

// mysqlDumpCmd dumps every user database (not the system schemas, which the
// image re-creates from the same environment on the target).
const mysqlDumpCmd = `set -e
P="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"
C=$(command -v mariadb || command -v mysql); D=$(command -v mariadb-dump || command -v mysqldump)
DBS=$(MYSQL_PWD="$P" $C -uroot -N -B -e "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','information_schema','performance_schema','sys')")
[ -n "$DBS" ] || { echo "-- no user databases"; exit 0; }
MYSQL_PWD="$P" $D -uroot --single-transaction --routines --events --triggers --hex-blob --databases $DBS`

// saveDump streams a native dump of the container into the archive.
func saveDump(ctx context.Context, w *archive.Writer, d *DumpEntry, st *Step) error {
	cmd := pgDumpCmd
	if d.Engine == EngineMySQL {
		cmd = mysqlDumpCmd
	}
	if err := os.MkdirAll(filepath.Join(HomeDir, "tmp"), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(HomeDir, "tmp"), "dump-*.sql")
	if err != nil {
		return err
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	cw := &countWriter{w: tmp, fn: st.Advance}
	if _, err := run.Do(ctx, run.Spec{Name: "docker", Args: []string{"exec", d.Container, "sh", "-c", cmd}, Stdout: cw}); err != nil {
		return fmt.Errorf("%s dump of %s: %w", d.Engine, d.Container, err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	d.Size = cw.n
	return w.AddStream(d.entryName(), cw.n, tmp, nil)
}

type countWriter struct {
	w  io.Writer
	n  int64
	fn func(int64)
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.fn != nil {
		c.fn(int64(n))
	}
	return n, err
}

// --- restore ----------------------------------------------------------------------

// pendingDump is a dump extracted to disk, waiting to be loaded.
type pendingDump struct {
	DumpEntry
	file string
}

// loadDump initialises the (empty) data volume with a temporary container of
// the same image and environment, loads the dump and stops it cleanly.
// rename maps source names to target names (copy mode).
func loadDump(ctx context.Context, d pendingDump, rename func(string) string, st *Step) (err error) {
	vol := rename(d.Volume)
	name := "coolify-mirror-dbload-" + time.Now().Format("150405") + "-" + safeName(d.Container)
	envFile, err := os.CreateTemp(filepath.Join(HomeDir, "tmp"), "env-*")
	if err != nil {
		return err
	}
	defer os.Remove(envFile.Name())
	for _, kv := range d.Env {
		if !strings.ContainsAny(kv, "\n\r") {
			fmt.Fprintln(envFile, kv)
		}
	}
	envFile.Close()

	st.SetDetail("starting " + path.Base(d.Image))
	if _, err := run.Do(ctx, run.Spec{Name: "docker", Args: []string{"run", "-d", "--name", name,
		"--label", "coolify-mirror.share=dbload", "--network", "none",
		"--env-file", envFile.Name(), "-v", vol + ":" + d.DataDir, d.Image}}); err != nil {
		return fmt.Errorf("start %s to load the dump: %w", d.Image, err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		// A clean shutdown flushes the data files before Coolify starts the real container.
		_, _ = run.Output(cctx, "docker", "stop", "-t", "90", name)
		_, _ = run.Output(cctx, "docker", "rm", "-f", name)
	}()

	ready := `pg_isready -h 127.0.0.1 -U "${POSTGRES_USER:-postgres}" >/dev/null 2>&1`
	if d.Engine == EngineMySQL {
		ready = `P="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"; A=$(command -v mariadb-admin || command -v mysqladmin); MYSQL_PWD="$P" $A -uroot -h 127.0.0.1 --protocol=tcp ping >/dev/null 2>&1`
	}
	st.SetDetail("initialising the empty database")
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if _, err := run.Output(ctx, "docker", "exec", name, "sh", "-c", ready); err == nil {
			break
		}
		if running, _ := docker.State(ctx, name); !running {
			logs, _ := run.Output(ctx, "docker", "logs", "--tail", "20", name)
			return fmt.Errorf("the temporary %s container stopped while initialising:\n%s", d.Engine, logs)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return errors.Join(errors.New("the temporary database did not become ready in 5 minutes"), ctx.Err())
		}
		time.Sleep(2 * time.Second)
	}

	f, err := os.Open(d.file)
	if err != nil {
		return err
	}
	defer f.Close()
	st.SetDetail("loading the dump")
	in := io.TeeReader(f, advanceWriter(st.Advance))
	if d.Engine == EngineMySQL {
		_, err = run.Do(ctx, run.Spec{Name: "docker", Args: []string{"exec", "-i", name, "sh", "-c",
			`P="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"; C=$(command -v mariadb || command -v mysql); MYSQL_PWD="$P" exec $C -uroot`}, Stdin: in})
		return err
	}
	// pg_dumpall re-creates the roles and databases the image just created
	// from the same environment: those "already exists" errors are expected,
	// anything else fails the restore.
	out, err := run.Do(ctx, run.Spec{Name: "docker", Args: []string{"exec", "-i", name, "sh", "-c",
		`psql -X -q -U "${POSTGRES_USER:-postgres}" -d postgres -v ON_ERROR_STOP=0 -f - 2>&1 >/dev/null`}, Stdin: in})
	if err != nil {
		return err
	}
	var bad []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "ERROR:") && !strings.Contains(line, "already exists") {
			bad = append(bad, strings.TrimSpace(line))
		}
	}
	if len(bad) > 0 {
		if len(bad) > 5 {
			bad = append(bad[:5], fmt.Sprintf("… %d more", len(bad)-5))
		}
		return fmt.Errorf("loading the dump into %s failed:\n%s", d.Container, strings.Join(bad, "\n"))
	}
	return nil
}

type advanceWriter func(int64)

func (a advanceWriter) Write(p []byte) (int, error) { a(int64(len(p))); return len(p), nil }

// loadDumps loads every extracted dump; the files are removed afterwards.
func loadDumps(ctx context.Context, dumps []pendingDump, rename func(string) string, steps map[string]*Step) error {
	defer func() {
		for _, d := range dumps {
			_ = os.Remove(d.file)
		}
	}()
	for _, d := range dumps {
		st := steps[d.Container]
		st.Begin("")
		if err := loadDump(ctx, d, rename, st); err != nil {
			st.Fail(err)
			return err
		}
		size := d.Size
		if fi, err := os.Stat(d.file); err == nil {
			size = fi.Size()
		}
		st.Finish(HumanBytes(size))
	}
	return nil
}

// extractDump writes a dump entry to a private file for loadDumps.
func extractDump(man *Manifest, name string, r io.Reader) (*pendingDump, error) {
	for _, d := range man.Dumps {
		if d.entryName() != name {
			continue
		}
		if err := os.MkdirAll(filepath.Join(HomeDir, "tmp"), 0o700); err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(filepath.Join(HomeDir, "tmp"), "load-*.sql")
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(f, r)
		f.Close()
		if err != nil {
			os.Remove(f.Name())
			return nil, err
		}
		return &pendingDump{DumpEntry: d, file: f.Name()}, nil
	}
	return nil, fmt.Errorf("unexpected dump %s in backup", name)
}
