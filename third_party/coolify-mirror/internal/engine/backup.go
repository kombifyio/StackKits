package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/archive"
	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/run"
)

// Consistency modes for running containers while their volumes are saved.
const (
	ConsistencyPause = "pause" // freeze (crash-consistent snapshot, no restart)
	ConsistencyStop  = "stop"  // clean stop + start
	ConsistencyLive  = "live"  // no interruption (not safe for databases)
)

// Image modes.
const (
	ImagesApps = "apps" // images of applications (lets the target skip rebuilds)
	ImagesAll  = "all"  // every image used by the saved resources
	ImagesNone = "none"
)

// BackupRequest describes what to back up.
type BackupRequest struct {
	Mode           string
	Resources      []coolify.Resource // selective mode: final list incl. dependencies
	OutputDir      string
	Consistency    string
	Images         string
	IncludeBackups bool // full mode: also /data/coolify/backups (database backup files)
	// Key, when set, is the caller's archive key (kombify fork): it is used
	// instead of a new one and is never written next to the backup file.
	Key string
	// Drained maps resources stopped by a relocation drain to the compose
	// services that ran before it (kombify fork, DrainRecord.Running).
	Drained map[string][]string
}

// BackupResult describes the finished backup file.
type BackupResult struct {
	Path     string
	Key      string
	Size     int64
	RawBytes int64
	Duration time.Duration
	Manifest *Manifest
}

type saveItem struct {
	kind       string // path | volume | image | dump
	dump       *DumpEntry
	path       PathEntry
	vol        VolumeEntry
	img        ImageEntry
	mountpoint string
	running    []string // running containers using a volume
	step       *Step
}

type backupper struct {
	in       *coolify.Instance
	req      BackupRequest
	pr       *Progress
	man      *Manifest
	export   []byte
	bundle   []byte
	items    []*saveItem
	pathSeen map[string]bool
	volSeen  map[string]bool
	imgSeen  map[string]bool
}

// Backup creates an encrypted backup file and returns its location and key.
func Backup(ctx context.Context, in *coolify.Instance, req BackupRequest, pr *Progress) (res *BackupResult, err error) {
	defer func() { pr.End(err) }()
	unlock, err := Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	start := time.Now()
	if req.Consistency == "" {
		req.Consistency = ConsistencyPause
	}
	if req.Images == "" {
		req.Images = ImagesApps
	}
	if req.OutputDir == "" {
		req.OutputDir = BackupsDir
	}
	b := &backupper{in: in, req: req, pr: pr, pathSeen: map[string]bool{}, volSeen: map[string]bool{}, imgSeen: map[string]bool{}}
	v4, v6 := in.PublicIPs(ctx)
	b.man = &Manifest{Format: FormatName, ToolVersion: Version, CreatedAt: time.Now().UTC(), Mode: req.Mode,
		Source:  SourceInfo{Hostname: in.Hostname, IPv4: v4, IPv6: v6, CoolifyVersion: in.Version, Arch: in.Arch},
		Options: BackupOptions{Consistency: req.Consistency, Images: req.Images, IncludeBackups: req.IncludeBackups}}

	stRead := pr.Add("Read Coolify configuration", 0)
	stMeasure := pr.Add("Measure data", 0)

	stRead.Begin("database")
	compat := CheckCompat(ctx, in)
	if len(compat.Blockers) > 0 {
		err = fmt.Errorf("backup not possible: %s", strings.Join(compat.Blockers, "; "))
		stRead.Fail(err)
		return nil, err
	}
	for _, w := range compat.Warnings {
		pr.Warn("%s", w)
	}
	if compat.Failed != "" {
		pr.Warn("backing up works, restoring onto Coolify %s is refused for now: %s", in.Version, compat.Failed)
	}
	if req.Mode == ModeFull {
		err = b.discoverFull(ctx)
	} else {
		err = b.discoverSelective(ctx)
	}
	if err != nil {
		stRead.Fail(err)
		return nil, err
	}
	var owners []string
	var local []coolify.Resource
	for _, r := range b.man.Resources {
		if r.Local() {
			owners = append(owners, r.UUID)
			local = append(local, r)
		}
	}
	b.planDumps(ctx, owners)
	stRead.SetDetail("host dependencies")
	if err = b.planHostDeps(ctx, local); err != nil {
		stRead.Fail(err)
		return nil, err
	}
	b.recordRuntime(ctx, local)
	// Coolify's own Server Transfer bundle of the same resources, so the backup
	// also carries the official export format.
	stRead.SetDetail("Coolify transfer bundle")
	if bundle, err := in.TransferBundle(ctx, owners); err != nil {
		pr.Warn("Coolify's own transfer export failed (the backup is complete without it): %v", err)
	} else if bundle != nil {
		b.bundle = bundle
		b.man.HasTransferBundle = true
	}
	stRead.Finish(fmt.Sprintf("%d resource(s)", len(b.man.Resources)))

	stMeasure.Begin("")
	if err = b.measure(ctx, stMeasure); err != nil {
		stMeasure.Fail(err)
		return nil, err
	}
	stMeasure.Finish(HumanBytes(b.man.TotalBytes))

	if err = os.MkdirAll(req.OutputDir, 0o700); err != nil {
		return nil, err
	}
	if free := FreeSpace(req.OutputDir); free >= 0 && free < b.man.TotalBytes/2 {
		pr.Warn("only %s free in %s for up to %s of data; the backup may not fit", HumanBytes(free), req.OutputDir, HumanBytes(b.man.TotalBytes))
	}

	// Steps for the heavy work, now that we know what there is.
	stDB := pr.Add("Save Coolify database", 0)
	for _, it := range b.items {
		switch it.kind {
		case "path":
			it.step = pr.Add("Files  "+it.path.Path, it.path.Size)
		case "volume":
			it.step = pr.Add("Volume "+it.vol.Name, it.vol.Size)
		case "image":
			it.step = pr.Add(imageTitle(it.img.Refs), it.img.Size)
		case "dump":
			it.step = pr.Add("Database dump "+it.dump.Container, 0)
		}
	}
	stFinal := pr.Add("Finish and verify backup file", 0)

	key := req.Key
	if key == "" {
		key = NewPassphrase()
	}
	name := fmt.Sprintf("coolify-%s-%s-%s.cmb", safeName(in.Hostname), req.Mode, time.Now().Format("20060102-150405"))
	final := filepath.Join(req.OutputDir, name)
	tmp := final + ".part"
	w, err := archive.Create(tmp, key)
	if err != nil {
		return nil, err
	}
	pr.Output = w.Written
	ok := false
	defer func() {
		if !ok {
			w.Abort()
			_ = os.Remove(tmp)
		}
	}()

	manJSON, _ := json.MarshalIndent(b.man, "", " ")
	if err = w.AddBytes(entryManifest, manJSON); err != nil {
		return nil, err
	}
	if b.bundle != nil {
		if err = w.AddBytes(entryTransfer, b.bundle); err != nil {
			return nil, err
		}
	}

	stDB.Begin("")
	if err = b.saveDatabase(ctx, w, stDB); err != nil {
		stDB.Fail(err)
		return nil, err
	}

	if err = b.saveItems(ctx, w); err != nil {
		return nil, err
	}

	stFinal.Begin("flushing")
	if err = w.Close(); err != nil {
		stFinal.Fail(err)
		return nil, err
	}
	if err = os.Rename(tmp, final); err != nil {
		stFinal.Fail(err)
		return nil, err
	}
	ok = true
	if req.Key == "" {
		_ = os.WriteFile(final+".key", []byte(key+"\n"), 0o600)
	}
	st, _ := os.Stat(final)
	stFinal.SetDetail("verifying")
	if err = VerifyFile(ctx, final, key, nil); err != nil {
		stFinal.Fail(err)
		return nil, fmt.Errorf("the backup file failed verification: %w", err)
	}
	stFinal.Finish(HumanBytes(st.Size()))
	return &BackupResult{Path: final, Key: key, Size: st.Size(), RawBytes: w.RawWritten(),
		Duration: time.Since(start), Manifest: b.man}, nil
}

func safeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}

// --- discovery ---------------------------------------------------------------

func (b *backupper) addPath(p, owner string) {
	p = filepath.Clean(p)
	if deniedHostPath(p) {
		run.Logf("skip denied host path %s", p)
		return
	}
	if b.pathSeen[p] {
		return
	}
	for seen := range b.pathSeen {
		if strings.HasPrefix(p, seen+"/") {
			return
		}
	}
	if _, err := os.Lstat(p); err != nil {
		run.Logf("skip missing host path %s: %v", p, err)
		return
	}
	// Drop already added children of p.
	var kept []*saveItem
	for _, it := range b.items {
		if it.kind == "path" && strings.HasPrefix(it.path.Path, p+"/") {
			delete(b.pathSeen, it.path.Path)
			continue
		}
		kept = append(kept, it)
	}
	b.items = kept
	b.pathSeen[p] = true
	b.items = append(b.items, &saveItem{kind: "path", path: PathEntry{Path: p, Owner: owner}})
}

func (b *backupper) addVolume(ctx context.Context, name, owner string) {
	if name == "" || b.volSeen[name] || isAnonymousVolume(name) {
		return
	}
	switch name {
	case "coolify-db", "coolify-redis":
		return
	}
	v, err := docker.InspectVolume(ctx, name)
	if err != nil || v == nil {
		run.Logf("skip volume %s: not found (%v)", name, err)
		return
	}
	if v.Driver != "local" || v.Mountpoint == "" {
		b.pr.Warn("volume %s uses driver %q and cannot be read directly; it was skipped", name, v.Driver)
		return
	}
	b.volSeen[name] = true
	entry := VolumeEntry{Name: name, Driver: v.Driver, Labels: v.Labels, Options: v.Options, Owner: owner}
	// A "local" volume with driver options (NFS/CIFS mount, or bind to a host
	// directory) keeps its data somewhere else, not in _data.
	if dev := v.Options["device"]; dev != "" || v.Options["type"] != "" {
		entry.External = true
		if strings.Contains(v.Options["o"], "bind") && strings.HasPrefix(dev, "/") {
			b.addPath(dev, owner)
		} else {
			b.pr.Warn("volume %s is a %s mount (%s): its data stays there; it is recreated with the same options", name, v.Options["type"], dev)
		}
	}
	b.items = append(b.items, &saveItem{kind: "volume", mountpoint: v.Mountpoint, vol: entry})
}

func (b *backupper) addImage(ref, owner string) {
	if ref == "" || b.imgSeen[ref] {
		return
	}
	b.imgSeen[ref] = true
	b.items = append(b.items, &saveItem{kind: "image", img: ImageEntry{Refs: []string{ref}, Owner: owner}})
}

func resourceDir(r coolify.Resource) string {
	switch {
	case r.Table == "applications":
		return filepath.Join(coolify.BaseDir, "applications", r.UUID)
	case r.Table == "services":
		return filepath.Join(coolify.BaseDir, "services", r.UUID)
	case r.IsDatabase():
		return filepath.Join(coolify.BaseDir, "databases", r.UUID)
	}
	return ""
}

func (b *backupper) discoverSelective(ctx context.Context) error {
	for _, r := range b.req.Resources {
		if !r.Local() {
			return fmt.Errorf("%s %q runs on remote server %q; only resources on this server can be backed up",
				r.Label(), r.Name, r.ServerName)
		}
	}
	ex, err := dbx.Collect(ctx, b.in, b.req.Resources)
	if err != nil {
		return err
	}
	for _, w := range append(ex.Warnings, ex.Drift...) {
		b.pr.Warn("%s", w)
	}
	b.export, err = json.Marshal(ex)
	if err != nil {
		return err
	}
	b.man.Resources = b.req.Resources

	owners := map[string]string{} // morph#id -> root uuid
	for _, r := range b.req.Resources {
		owners[r.Morph()+"#"+fmt.Sprint(r.ID)] = r.UUID
		if dir := resourceDir(r); dir != "" {
			b.addPath(dir, r.UUID)
		}
	}
	for _, t := range []string{"service_applications", "service_databases"} {
		morph := coolify.TableToMorph[t]
		for _, row := range ex.Tables[t] {
			sid, _ := dbx.Int64(row["service_id"])
			id, _ := dbx.Int64(row["id"])
			owners[morph+"#"+fmt.Sprint(id)] = owners[coolify.MorphService+"#"+fmt.Sprint(sid)]
		}
	}
	ownerOf := func(row coolify.Row) string {
		t, _ := row["resource_type"].(string)
		id, _ := dbx.Int64(row["resource_id"])
		return owners[t+"#"+fmt.Sprint(id)]
	}
	for _, row := range ex.Tables["local_persistent_volumes"] {
		host, _ := dbx.PlainString(row["host_path"])
		name, _ := dbx.PlainString(row["name"])
		if strings.TrimSpace(host) != "" {
			b.addPath(host, ownerOf(row))
		} else {
			b.addVolume(ctx, name, ownerOf(row))
		}
	}
	for _, row := range ex.Tables["local_file_volumes"] {
		fsPath, _ := dbx.PlainString(row["fs_path"])
		if fsPath != "" {
			b.addPath(fsPath, ownerOf(row))
		}
	}
	for _, r := range b.req.Resources {
		if err := b.fromContainers(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// fromContainers adds named volumes and images used by a resource's containers.
func (b *backupper) fromContainers(ctx context.Context, r coolify.Resource) error {
	cs, err := docker.Containers(ctx, "label=com.docker.compose.project="+r.UUID)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if pr := c.Label("coolify.pullRequestId"); pr != "" && pr != "0" {
			continue // preview deployments are not part of the mirror
		}
		mounts, err := docker.Mounts(ctx, c.ID)
		if err != nil {
			continue
		}
		for _, m := range mounts {
			if m.Type == "volume" {
				b.addVolume(ctx, m.Name, r.UUID)
			}
		}
		if b.wantImages(r) {
			b.addImage(c.Image, r.UUID)
		}
	}
	if r.Table == "applications" && b.wantImages(r) {
		b.addDeployedImages(ctx, r)
	}
	return nil
}

func (b *backupper) wantImages(r coolify.Resource) bool {
	return b.req.Images == ImagesAll || (b.req.Images == ImagesApps && r.Table == "applications")
}

// addDeployedImages adds the images built by the application's last
// successful deployment (<uuid>:<commit>, <uuid>_<service>:<commit>), also
// when no container runs right now, so the target can start without a build.
func (b *backupper) addDeployedImages(ctx context.Context, r coolify.Resource) {
	commit, err := b.in.Scalar(ctx, fmt.Sprintf(`SELECT commit FROM application_deployment_queues WHERE application_id = '%d'
  AND status = 'finished' AND pull_request_id = 0 ORDER BY created_at DESC, id DESC LIMIT 1`, r.ID))
	if err != nil || commit == "" {
		return
	}
	refs, err := docker.Images(ctx)
	if err != nil {
		return
	}
	for _, ref := range deployedImages(refs, r.UUID, commit) {
		b.addImage(ref, r.UUID)
	}
}

// deployedImages picks the images Coolify built for an application at a
// commit: <uuid>:<commit> (Dockerfile/Nixpacks) and <uuid>_<service>:<commit>
// (Docker Compose).
func deployedImages(refs []string, uuid, commit string) []string {
	var out []string
	for _, ref := range refs {
		repo, tag, ok := strings.Cut(ref, ":")
		if ok && tag == commit && (repo == uuid || strings.HasPrefix(repo, uuid+"_")) {
			out = append(out, ref)
		}
	}
	return out
}

// recordRuntime stores which compose services of each resource are up now,
// and warns about resources that are not healthy on the source: a restore can
// only be as healthy as what was backed up.
func (b *backupper) recordRuntime(ctx context.Context, rs []coolify.Resource) {
	b.man.Runtime = map[string][]string{}
	for _, r := range rs {
		// A drained resource is stopped for a consistent backup: it restores
		// with the services that ran before the drain (kombify fork).
		if up, ok := b.req.Drained[r.UUID]; ok {
			b.man.Runtime[r.UUID] = uniq(append([]string{}, up...))
			continue
		}
		cs, err := docker.Containers(ctx, "label=com.docker.compose.project="+r.UUID)
		if err != nil {
			continue
		}
		var ids []string
		for _, c := range cs {
			if pr := c.Label("coolify.pullRequestId"); pr == "" || pr == "0" {
				ids = append(ids, c.ID)
			}
		}
		ds, _ := docker.Inspect(ctx, ids...)
		var up, bad []string
		for _, d := range ds {
			svc := d.Config.Labels["com.docker.compose.service"]
			if svc == "" {
				svc = strings.TrimPrefix(d.Name, "/")
			}
			switch containerState(d) {
			case stateUp, stateDone:
				up = append(up, svc)
			case stateStarting:
				up = append(up, svc)
			default:
				bad = append(bad, svc+" "+describeState(d))
			}
		}
		sort.Strings(up)
		if len(up) > 0 {
			b.man.Runtime[r.UUID] = uniq(up)
		}
		switch {
		case !r.Running() && len(up) == 0:
			b.pr.Warn("%s is not running on this server: it is restored stopped, as it is here", r.Name)
		case len(bad) > 0:
			b.pr.Warn("%s is not healthy on this server (%s): the restored copy will have the same problem", r.Name, strings.Join(bad, ", "))
		}
	}
}

func (b *backupper) discoverFull(ctx context.Context) error {
	all, err := b.in.ListResources(ctx)
	if err != nil {
		return err
	}
	var local []coolify.Resource
	for _, r := range all {
		if r.Local() {
			local = append(local, r)
		}
	}
	b.man.Resources = all
	var loc []struct {
		User string `json:"user"`
		Key  string `json:"key_uuid"`
	}
	if err := b.in.Query(ctx, fmt.Sprintf(`SELECT s."user", pk.uuid AS key_uuid FROM servers s JOIN private_keys pk ON pk.id = s.private_key_id WHERE s.id = %d`, coolify.LocalServerID), &loc); err == nil && len(loc) == 1 {
		b.man.LocalUser, b.man.LocalKeyUUID = loc[0].User, loc[0].Key
	}

	skipTop := map[string]bool{"source": true, "sentinel": true, "metrics": true,
		"webhooks-during-maintenance": true, "clone": true, "backups": !b.req.IncludeBackups}
	entries, err := os.ReadDir(coolify.BaseDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if skipTop[e.Name()] {
			continue
		}
		b.addPath(filepath.Join(coolify.BaseDir, e.Name()), "")
	}

	// Volumes and bind mounts registered in Coolify for resources on this server.
	localOwner := map[string]string{}
	for _, r := range local {
		localOwner[r.Morph()+"#"+fmt.Sprint(r.ID)] = r.UUID
	}
	var children []struct {
		Morph string `json:"morph"`
		ID    int64  `json:"id"`
		SID   int64  `json:"service_id"`
	}
	_ = b.in.Query(ctx, `SELECT 'App\Models\ServiceApplication' AS morph, id, service_id FROM service_applications WHERE deleted_at IS NULL
UNION ALL SELECT 'App\Models\ServiceDatabase', id, service_id FROM service_databases WHERE deleted_at IS NULL`, &children)
	for _, c := range children {
		if u, ok := localOwner[coolify.MorphService+"#"+fmt.Sprint(c.SID)]; ok {
			localOwner[c.Morph+"#"+fmt.Sprint(c.ID)] = u
		}
	}
	var vols []struct {
		Name  string  `json:"name"`
		Host  *string `json:"host_path"`
		Type  string  `json:"resource_type"`
		ResID int64   `json:"resource_id"`
	}
	if err := b.in.Query(ctx, `SELECT name, host_path, resource_type, resource_id FROM local_persistent_volumes`, &vols); err != nil {
		return err
	}
	for _, v := range vols {
		owner, ok := localOwner[v.Type+"#"+fmt.Sprint(v.ResID)]
		if !ok {
			continue
		}
		if v.Host != nil && strings.TrimSpace(*v.Host) != "" {
			b.addPath(*v.Host, owner)
		} else {
			b.addVolume(ctx, v.Name, owner)
		}
	}
	var files []struct {
		Path  string `json:"fs_path"`
		Type  string `json:"resource_type"`
		ResID int64  `json:"resource_id"`
	}
	if err := b.in.Query(ctx, `SELECT fs_path, resource_type, resource_id FROM local_file_volumes`, &files); err == nil {
		for _, f := range files {
			if owner, ok := localOwner[f.Type+"#"+fmt.Sprint(f.ResID)]; ok {
				b.addPath(f.Path, owner)
			}
		}
	}
	for _, r := range local {
		if err := b.fromContainers(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (b *backupper) measure(ctx context.Context, st *Step) error {
	var total int64
	for _, it := range b.items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch it.kind {
		case "path":
			st.SetDetail(it.path.Path)
			n, _, err := archive.TreeSize(it.path.Path, b.pathSkip(it.path.Path))
			if err != nil {
				b.pr.Warn("cannot measure %s: %v", it.path.Path, err)
			}
			it.path.Size = n
			total += n
			// Containers writing into this directory through a bind mount are
			// frozen while it is copied, exactly like volume users.
			it.running, _ = docker.BindUsers(ctx, it.path.Path)
		case "volume":
			st.SetDetail("volume " + it.vol.Name)
			if it.vol.External || it.vol.Dumped {
				continue
			}
			n, _, err := archive.TreeSize(it.mountpoint, nil)
			if err != nil {
				b.pr.Warn("cannot measure volume %s: %v", it.vol.Name, err)
			}
			it.vol.Size = n
			total += n
			it.running, _ = volumeUsers(ctx, it.vol.Name)
		case "image":
			st.SetDetail("image " + it.img.Refs[0])
			n := docker.ImageSize(ctx, it.img.Refs[0])
			if n < 0 {
				b.pr.Warn("image %s is not present locally; skipped", it.img.Refs[0])
				it.kind = "skip"
				continue
			}
			it.img.Size = n
			total += n
		}
	}
	// Merge all images into one `docker save` so shared layers are stored once.
	var imgs []*saveItem
	var rest []*saveItem
	for _, it := range b.items {
		switch it.kind {
		case "image":
			imgs = append(imgs, it)
		case "skip":
		default:
			rest = append(rest, it)
		}
	}
	// Paths first, then volumes; within each kind, items used by the same
	// containers are adjacent so those containers are frozen only once.
	sort.SliceStable(rest, func(i, j int) bool {
		a, c := rest[i], rest[j]
		if a.kind != c.kind {
			return a.kind == "path"
		}
		return strings.Join(a.running, ",") < strings.Join(c.running, ",")
	})
	if len(imgs) > 0 {
		merged := &saveItem{kind: "image"}
		for _, it := range imgs {
			merged.img.Refs = append(merged.img.Refs, it.img.Refs...)
			merged.img.Size += it.img.Size
		}
		rest = append(rest, merged)
	}
	b.items = rest
	b.man.TotalBytes = total
	b.man.Paths, b.man.Volumes, b.man.Images = nil, nil, nil
	for _, it := range b.items {
		switch it.kind {
		case "path":
			b.man.Paths = append(b.man.Paths, it.path)
		case "volume":
			b.man.Volumes = append(b.man.Volumes, it.vol)
		case "image":
			b.man.Images = append(b.man.Images, it.img)
		case "dump":
			b.man.Dumps = append(b.man.Dumps, *it.dump)
		}
	}
	return nil
}

// planDumps replaces the raw copy of PostgreSQL/MySQL/MariaDB data volumes by
// native dumps for databases that are running now.
func (b *backupper) planDumps(ctx context.Context, owners []string) {
	vols := map[string]*saveItem{}
	for _, it := range b.items {
		if it.kind == "volume" {
			vols[it.vol.Name] = it
		}
	}
	for _, owner := range owners {
		cs, err := docker.Containers(ctx, "label=com.docker.compose.project="+owner, "status=running")
		if err != nil {
			continue
		}
		for _, c := range cs {
			d, note := detectDump(ctx, c.ID, owner)
			if note != "" {
				b.pr.Warn("%s", note)
			}
			if d == nil {
				continue
			}
			v := vols[d.Volume]
			if v == nil || v.vol.External {
				continue
			}
			v.vol.Dumped = true
			b.items = append(b.items, &saveItem{kind: "dump", dump: d})
		}
	}
}

func (b *backupper) pathSkip(root string) func(string) bool {
	if root == filepath.Join(coolify.BaseDir, "ssh") {
		return func(rel string) bool { return rel == "mux" || strings.HasPrefix(rel, "mux/") }
	}
	return nil
}

// --- writing -------------------------------------------------------------------

func (b *backupper) saveDatabase(ctx context.Context, w *archive.Writer, st *Step) error {
	if b.req.Mode != ModeFull {
		if err := w.AddBytes(entryExport, b.export); err != nil {
			return err
		}
		// Registry logins, so images from private registries can be pulled on the target.
		if c, err := os.ReadFile("/root/.docker/config.json"); err == nil {
			if err := w.AddBytes(entryDockerConfig, c); err != nil {
				return err
			}
		}
		st.Finish(HumanBytes(int64(len(b.export))))
		return nil
	}
	st.SetDetail("pg_dump")
	tmp, err := os.CreateTemp(b.req.OutputDir, ".coolify-db-*.dump")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := b.in.Dump(ctx, tmp); err != nil {
		return err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := w.AddStream(entryDump, size, tmp, nil); err != nil {
		return err
	}
	env, err := os.ReadFile(coolify.EnvPath)
	if err != nil {
		return err
	}
	if err := w.AddBytes(entryEnv, env); err != nil {
		return err
	}
	if c, err := os.ReadFile(filepath.Join(coolify.SourceDir, "docker-compose.custom.yml")); err == nil {
		if err := w.AddBytes(entryCustomCompose, c); err != nil {
			return err
		}
	}
	if c, err := os.ReadFile("/root/.docker/config.json"); err == nil {
		if err := w.AddBytes(entryDockerConfig, c); err != nil {
			return err
		}
	}
	st.Finish(HumanBytes(size))
	return nil
}

func (b *backupper) saveItems(ctx context.Context, w *archive.Writer) error {
	// Paths and volumes used by the same running containers are saved while
	// those containers are frozen (or stopped) once.
	var group []string
	groupKey := "\x00"
	flush := func() error {
		err := resume(b.req.Consistency, group)
		group, groupKey = nil, "\x00"
		return err
	}
	defer func() { _ = flush() }()

	// enter freezes the containers of a new group. The users are looked up
	// again right now: the list from the measuring step may be stale.
	enter := func(it *saveItem) error {
		key := strings.Join(it.running, ",")
		if key == groupKey {
			return nil
		}
		if err := flush(); err != nil {
			return err
		}
		groupKey = key
		if len(it.running) == 0 || b.req.Consistency == ConsistencyLive {
			return nil
		}
		var users []string
		if it.kind == "volume" {
			users, _ = volumeUsers(ctx, it.vol.Name)
		} else {
			users, _ = docker.BindUsers(ctx, it.path.Path)
		}
		if len(users) == 0 {
			return nil
		}
		it.step.Begin(fmt.Sprintf("%s %d container(s)", b.req.Consistency, len(users)))
		savePaused(b.req.Consistency, users)
		var err error
		if b.req.Consistency == ConsistencyStop {
			err = docker.Stop(ctx, users...)
		} else {
			err = docker.Pause(ctx, users...)
		}
		if err == nil {
			group = users
			return nil
		}
		// Some containers may have changed state before the error: remember
		// exactly those so they are resumed later.
		rctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var changed []string
		for _, id := range users {
			running, paused := docker.State(rctx, id)
			if (b.req.Consistency == ConsistencyStop && !running) || (b.req.Consistency != ConsistencyStop && paused) {
				changed = append(changed, id)
			}
		}
		group = changed
		if len(changed) > 0 {
			savePaused(b.req.Consistency, changed)
		} else {
			clearPaused()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.pr.Warn("could not %s every container using %s (%v); copying the rest live", b.req.Consistency, it.step.Title, err)
		return nil
	}
	frozen := func() string {
		if len(group) > 0 {
			return fmt.Sprintf("%d container(s) %sd", len(group), b.req.Consistency)
		}
		return ""
	}

	imgIndex := 0
	for _, it := range b.items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch it.kind {
		case "path":
			if err := enter(it); err != nil {
				return err
			}
			it.step.Begin(frozen())
			stats, err := w.AddTree(prefixPaths+it.path.Path, it.path.Path, archive.TreeOptions{
				Ctx: ctx, Progress: it.step.Advance, Skip: b.pathSkip(it.path.Path),
				Warn: func(m string) { b.pr.Warn("%s", m) }})
			if err != nil {
				it.step.Fail(err)
				return fmt.Errorf("save %s: %w", it.path.Path, err)
			}
			it.step.Finish(fmt.Sprintf("%s · %d files", HumanBytes(stats.Bytes), stats.Files))

		case "volume":
			if it.vol.External {
				it.step.Begin("")
				it.step.Finish("definition only (data lives outside the volume)")
				continue
			}
			if it.vol.Dumped {
				it.step.Begin("")
				it.step.Finish("saved as a database dump")
				continue
			}
			if err := enter(it); err != nil {
				return err
			}
			it.step.Begin(frozen())
			stats, err := w.AddTree(prefixVolumes+"/"+it.vol.Name, it.mountpoint, archive.TreeOptions{
				Ctx: ctx, Progress: it.step.Advance, Warn: func(m string) { b.pr.Warn("%s", m) }})
			if err != nil {
				it.step.Fail(err)
				return fmt.Errorf("save volume %s: %w", it.vol.Name, err)
			}
			it.step.Finish(fmt.Sprintf("%s · %d files", HumanBytes(stats.Bytes), stats.Files))

		case "dump":
			it.step.Begin(it.dump.Engine + " dump, database keeps running")
			if err := saveDump(ctx, w, it.dump, it.step); err != nil {
				it.step.Fail(err)
				return err
			}
			it.step.Finish(HumanBytes(it.dump.Size))

		case "image":
			if err := flush(); err != nil {
				return err
			}
			it.step.Begin("docker save")
			if err := b.saveImages(ctx, w, fmt.Sprintf("%s/%d", prefixImages, imgIndex), it); err != nil {
				it.step.Fail(err)
				return err
			}
			imgIndex++
			it.step.Finish(HumanBytes(it.img.Size))
		}
	}
	return flush()
}

// volumeUsers lists running containers (other than Coolify's own) mounting a volume.
func volumeUsers(ctx context.Context, volume string) ([]string, error) {
	cs, err := docker.RunningUsing(ctx, volume)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range cs {
		if docker.IsInfra(c.Names) || c.Label("coolify-mirror.share") != "" {
			continue
		}
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

func (b *backupper) saveImages(ctx context.Context, w *archive.Writer, prefix string, it *saveItem) error {
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := docker.Save(ctx, it.img.Refs, pw)
		pw.CloseWithError(err)
		errc <- err
	}()
	err := w.AddTarStream(prefix, pr, it.step.Advance)
	pr.CloseWithError(err)
	if serr := <-errc; serr != nil {
		return fmt.Errorf("docker save: %w", serr)
	}
	return err
}

// VerifyFile reads the whole backup, checking encryption tags, compression
// checksums and archive structure. progress receives consumed file bytes.
func VerifyFile(ctx context.Context, path, key string, progress func(int64)) error {
	r, err := archive.Open(path, key)
	if err != nil {
		return err
	}
	defer r.Close()
	var last int64
	buf := make([]byte, 1<<20)
	sawManifest := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Name == entryManifest {
			sawManifest = true
		}
		if _, err := io.CopyBuffer(io.Discard, r, buf); err != nil {
			return err
		}
		if progress != nil {
			c := r.Consumed()
			progress(c - last)
			last = c
		}
	}
	if !sawManifest {
		return errors.New("backup has no manifest")
	}
	return nil
}
