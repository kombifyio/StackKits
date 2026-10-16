package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/run"
)

// Host dependencies are things outside Coolify's database and the resources'
// own folders that a resource needs to build or run: a script on the host
// called by a custom build command, a named buildx builder, a system tool.
// They are found in the commands Coolify runs for the resource and in the
// helper files kept in its folder. Files the tool may copy are saved and
// restored; the others must already exist on the target (checked before
// anything changes). After a restore every dependency is checked again, so a
// later deploy on the target does not fail on something left behind.

const (
	depPath    = "path"
	depBuilder = "builder"
)

// buildxDir holds the buildx builder definitions. Coolify mounts
// ~/.docker/buildx into its build helper, so builders named in a custom
// build command must exist there.
var buildxDir = "/root/.docker/buildx/instances"

// runtimeRoots hold runtime state (locks, sockets, temporary files): never a
// dependency to copy or check.
var runtimeRoots = []string{"/proc", "/sys", "/dev", "/run", "/var/run", "/tmp", "/var/tmp",
	"/var/lib/docker", "/var/lib/containerd", "/data/coolify-mirror"}

// HostDep is one dependency of a resource on its host.
type HostDep struct {
	Owner string `json:"owner,omitempty"` // resource uuid ("" = whole server)
	Kind  string `json:"kind"`            // path | builder
	Ref   string `json:"ref"`             // host path or builder name
	Where string `json:"where"`           // where it is referenced
	// Saved: the file or builder definition is in the backup and restored;
	// otherwise it must already exist on the target.
	Saved bool `json:"saved,omitempty"`
	// SHA256 of a saved regular file. A selective restore never replaces a
	// different file of the same path on an existing server (server policy
	// shared by other resources); an identical one is left as it is.
	SHA256 string `json:"sha256,omitempty"`
}

// BuilderEntry is a saved buildx builder definition.
type BuilderEntry struct {
	Name string          `json:"name"`
	Def  json.RawMessage `json:"def"`
}

var (
	reBuilder  = regexp.MustCompile(`(?:--builder[= ]+|BUILDX_BUILDER=)["']?([A-Za-z0-9][A-Za-z0-9_.-]*)`)
	reHostPath = regexp.MustCompile(`(?:^|[\s"'=:,(])(/[A-Za-z0-9._@+-]+(?:/[A-Za-z0-9._@+-]+)+)`)
)

// appCommandColumns are the application columns whose content Coolify runs
// on the host or in its build helper.
var appCommandColumns = []string{
	"docker_compose_custom_build_command", "docker_compose_custom_start_command",
	"pre_deployment_command", "post_deployment_command", "custom_docker_run_options",
	"install_command", "build_command", "start_command",
}

type depText struct {
	owner, where, text string
}

// scanText returns the builder names and absolute paths mentioned in a text.
func scanText(text string) (builders, paths []string) {
	for _, m := range reBuilder.FindAllStringSubmatch(text, -1) {
		builders = append(builders, m[1])
	}
	for _, m := range reHostPath.FindAllStringSubmatch(text, -1) {
		paths = append(paths, filepath.Clean(m[1]))
	}
	return builders, paths
}

func runtimePath(p string) bool {
	for _, d := range runtimeRoots {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// coolifyManaged reports Coolify's own folders: restored as part of the
// resources (or the whole server), never as a separate dependency.
func coolifyManaged(p string) bool {
	if p == coolify.BaseDir {
		return true
	}
	for _, d := range []string{"applications", "services", "databases", "ssh", "proxy", "source", "backups", "sentinel", "metrics"} {
		dir := filepath.Join(coolify.BaseDir, d)
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

// textFile reads a small text file (nil for binaries and large files).
func textFile(p string) []byte {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 256<<10 {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil || bytes.IndexByte(b, 0) >= 0 {
		return nil
	}
	return b
}

// helperFiles lists the hand-made files in a resource folder (the files
// Coolify generates itself are skipped).
func helperFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		switch strings.ToLower(e.Name()) {
		case "docker-compose.yaml", "docker-compose.yml", ".env", "readme.md":
			continue
		}
		if e.Type().IsRegular() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// depTexts collects the commands and helper files of the given resources.
func depTexts(ctx context.Context, in *coolify.Instance, rs []coolify.Resource) ([]depText, error) {
	var out []depText
	var apps []string
	for _, r := range rs {
		if r.Table == "applications" {
			apps = append(apps, r.UUID)
		}
		if dir := resourceDir(r); dir != "" {
			for _, f := range helperFiles(dir) {
				if b := textFile(f); b != nil {
					out = append(out, depText{owner: r.UUID, where: f, text: string(b)})
				}
			}
		}
	}
	if len(apps) == 0 {
		return out, nil
	}
	var rows []struct {
		UUID string         `json:"uuid"`
		Row  map[string]any `json:"row"`
	}
	if err := in.Query(ctx, "SELECT uuid, to_jsonb(a) AS row FROM applications a WHERE uuid IN "+coolify.SQLList(apps), &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		for _, col := range appCommandColumns {
			if s, _ := r.Row[col].(string); strings.TrimSpace(s) != "" {
				out = append(out, depText{owner: r.UUID, where: strings.ReplaceAll(col, "_", " "), text: s})
			}
		}
	}
	return out, nil
}

// planHostDeps finds the host dependencies of the backed-up resources: files
// that can be copied are added to the backup, builders are saved, and the
// rest is recorded to be checked on the target.
func (b *backupper) planHostDeps(ctx context.Context, rs []coolify.Resource) error {
	texts, err := depTexts(ctx, b.in, rs)
	if err != nil {
		return err
	}
	deps, builders, paths := findHostDeps(texts, b.req.Mode == ModeFull)
	for _, p := range paths {
		b.addPath(p.Path, p.Owner)
	}
	b.man.HostDeps, b.man.Builders = deps, builders
	for _, d := range deps {
		run.Logf("host dependency of %s: %s %s (%s) saved=%v", ownerName(b.man, d.Owner), d.Kind, d.Ref, d.Where, d.Saved)
	}
	return nil
}

// findHostDeps scans commands and helper files: it returns the dependencies,
// the builder definitions to save and the host files to add to the backup.
func findHostDeps(texts []depText, allBuilders bool) (deps []HostDep, saved []BuilderEntry, paths []PathEntry) {
	seen := map[string]bool{}
	builders := map[string]bool{}
	add := func(d HostDep) {
		k := d.Owner + "\x00" + d.Kind + "\x00" + d.Ref
		if !seen[k] {
			seen[k] = true
			deps = append(deps, d)
		}
	}
	var scan func(t depText, nested bool)
	scan = func(t depText, nested bool) {
		bs, ps := scanText(t.text)
		for _, name := range bs {
			def, err := os.ReadFile(filepath.Join(buildxDir, name))
			if err != nil || !json.Valid(def) {
				continue // created on demand by the command itself
			}
			if !builders[name] {
				builders[name] = true
				saved = append(saved, BuilderEntry{Name: name, Def: def})
			}
			add(HostDep{Owner: t.owner, Kind: depBuilder, Ref: name, Where: t.where, Saved: true})
		}
		for _, p := range ps {
			if runtimePath(p) || coolifyManaged(p) {
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				continue // not a host path (e.g. a path inside a container)
			}
			if deniedHostPath(p) {
				// System files are not copied; the target must have them.
				if !nested {
					add(HostDep{Owner: t.owner, Kind: depPath, Ref: p, Where: t.where})
				}
				continue
			}
			if st.IsDir() && len(strings.Split(strings.Trim(p, "/"), "/")) < 3 {
				continue // too broad to be a dependency (/data, /opt/x)
			}
			paths = append(paths, PathEntry{Path: p, Owner: t.owner})
			sum := ""
			if st.Mode().IsRegular() {
				sum, _ = fileSHA256(p)
			}
			add(HostDep{Owner: t.owner, Kind: depPath, Ref: p, Where: t.where, Saved: true, SHA256: sum})
			if !nested {
				if c := textFile(p); c != nil {
					scan(depText{owner: t.owner, where: p, text: string(c)}, true)
				}
			}
		}
	}
	for _, t := range texts {
		scan(t, false)
	}
	if allBuilders {
		// A full mirror carries every builder of the server.
		entries, _ := os.ReadDir(buildxDir)
		for _, e := range entries {
			if builders[e.Name()] || !e.Type().IsRegular() {
				continue
			}
			if def, err := os.ReadFile(filepath.Join(buildxDir, e.Name())); err == nil && json.Valid(def) {
				builders[e.Name()] = true
				saved = append(saved, BuilderEntry{Name: e.Name(), Def: def})
			}
		}
	}
	return deps, saved, paths
}

func ownerName(man *Manifest, uuid string) string {
	for _, r := range man.Resources {
		if r.UUID == uuid {
			return r.Name
		}
	}
	if uuid == "" {
		return "the server"
	}
	return uuid
}

// hostDepBlockers lists dependencies that are not in the backup and missing
// here (checked before anything changes).
func hostDepBlockers(ctx context.Context, man *Manifest) []string {
	var out []string
	for _, d := range man.HostDeps {
		if d.Saved || d.Kind != depPath {
			continue
		}
		if _, err := os.Stat(d.Ref); err != nil {
			out = append(out, fmt.Sprintf("%s needs %s on this server (%s) - install it here first", ownerName(man, d.Owner), d.Ref, d.Where))
		}
	}
	if len(man.Builders) > 0 && !docker.BuildxAvailable(ctx) {
		out = append(out, "the backup builds with docker buildx builders ("+builderNames(man)+") but the docker buildx plugin is not installed here")
	}
	return out
}

// hostDepConflicts lists saved host files that already exist here with other
// content. A selective restore merges into a running server whose other
// resources may use them, so it does not replace them (a full restore goes
// onto an empty server and restores them).
func hostDepConflicts(man *Manifest) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range man.HostDeps {
		if !d.Saved || d.Kind != depPath || seen[d.Ref] {
			continue
		}
		seen[d.Ref] = true
		st, err := os.Lstat(d.Ref)
		if err != nil {
			continue // missing: restored
		}
		if d.SHA256 != "" && st.Mode().IsRegular() {
			if sum, err := fileSHA256(d.Ref); err == nil && strings.EqualFold(sum, d.SHA256) {
				continue // identical: kept
			}
		}
		out = append(out, fmt.Sprintf("host file %s (used by %s, %s) already exists here with different content - a selective restore does not replace server files other resources may use; make it identical to the source or remove it, then restore", d.Ref, ownerName(man, d.Owner), d.Where))
	}
	return out
}

// sameHostFile reports a saved host file that already exists here with the
// same content (nothing to restore).
func sameHostFile(man *Manifest, path string) bool {
	for _, d := range man.HostDeps {
		if d.Saved && d.Kind == depPath && d.Ref == path && d.SHA256 != "" {
			sum, err := fileSHA256(path)
			return err == nil && strings.EqualFold(sum, d.SHA256)
		}
	}
	return false
}

// isHostFile reports a host path saved as a single file (it has a checksum).
func isHostFile(man *Manifest, path string) bool {
	for _, d := range man.HostDeps {
		if d.Saved && d.Kind == depPath && d.Ref == path && d.SHA256 != "" {
			return true
		}
	}
	return false
}

func builderNames(man *Manifest) string {
	var n []string
	for _, b := range man.Builders {
		n = append(n, b.Name)
	}
	return strings.Join(n, ", ")
}

// restoreBuilders installs the saved builder definitions that do not exist
// here yet (an existing builder of the same name is kept).
func restoreBuilders(man *Manifest, undo *undoLog) (notes []string, err error) {
	for _, b := range man.Builders {
		if strings.ContainsAny(b.Name, "/\\") || b.Name == "" || b.Name[0] == '.' {
			continue
		}
		dst := filepath.Join(buildxDir, b.Name)
		if _, e := os.Stat(dst); e == nil {
			notes = append(notes, "buildx builder "+b.Name+" already exists here - kept")
			continue
		}
		created := topMissing(dst)
		if err := os.MkdirAll(buildxDir, 0o700); err != nil {
			return notes, err
		}
		undo.created = append(undo.created, created)
		if err := os.WriteFile(dst, b.Def, 0o600); err != nil {
			return notes, err
		}
	}
	return notes, nil
}

// checkHostDeps verifies after a restore that every dependency of the
// restored resources is present and usable, so the next deploy works.
func checkHostDeps(ctx context.Context, man *Manifest, rename func(string) string, skip func(owner string) bool) []string {
	var out []string
	booted := map[string]error{}
	for _, d := range man.HostDeps {
		if skip(d.Owner) {
			continue
		}
		who := ownerName(man, d.Owner)
		switch d.Kind {
		case depPath:
			p := rename(d.Ref)
			if _, err := os.Stat(p); err != nil {
				out = append(out, fmt.Sprintf("%s: host file %s (used in %s) is missing - its next deploy would fail", who, p, d.Where))
			}
		case depBuilder:
			err, done := booted[d.Ref]
			if !done {
				err = docker.BootstrapBuilder(ctx, d.Ref)
				booted[d.Ref] = err
			}
			if err != nil {
				out = append(out, fmt.Sprintf("%s: buildx builder %s (used in %s) does not start here: %v", who, d.Ref, d.Where, err))
			}
		}
	}
	return out
}
