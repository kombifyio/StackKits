package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/archive"
	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/run"

	"golang.org/x/sys/unix"
)

// Everything this tool leaves on a server, so it can be listed and deleted
// (the "Saved files" screen of the menu and `coolify-mirror files`):
//
//   - backups made here (backups/*.cmb with their .key),
//   - backups downloaded from another server (incoming/, kept when a restore
//     does not finish, deleted after a successful one),
//   - unfinished backups and downloads (*.part),
//   - safety copies of a restore: this server's Coolify database and .env
//     before a full restore (pre-restore-*), folders a restore replaced
//     (replaced-*), and the previous data of volumes that already existed
//     (docker volumes *.cm-old-*),
//   - logs and leftovers of interrupted runs.
//
// Nothing outside the tool's own folder (and its own *.cm-old-* volumes) is
// ever listed or deleted.

// Kinds of saved files.
const (
	StoredBackup   = "backup"
	StoredDownload = "download"
	StoredPartial  = "partial"
	StoredSafety   = "safety"
	StoredVolume   = "volume"
	StoredLogs     = "logs"
	StoredTemp     = "temp"
)

// StoredFile is one thing this tool keeps on the server.
type StoredFile struct {
	Kind    string
	Name    string // unique: shown, and used by `coolify-mirror files delete`
	Path    string // file or folder ("" for a volume)
	Volume  string // docker volume name (StoredVolume)
	Size    int64
	ModTime time.Time
	About   string // what it is, in a few words
	// Busy says why it cannot be deleted right now ("" = it can be).
	Busy string
	// Shares serving this backup right now; deleting it stops them.
	Shares []ShareInfo
	files  []string // the files to delete (several for logs; the .key of a backup)
}

// KindLabel names a kind for people.
func KindLabel(kind string) string {
	switch kind {
	case StoredBackup:
		return "backup"
	case StoredDownload:
		return "download"
	case StoredPartial:
		return "unfinished"
	case StoredSafety:
		return "safety copy"
	case StoredVolume:
		return "old volume"
	case StoredLogs:
		return "logs"
	}
	return "leftover"
}

// DeleteWarnings says what is lost when f is deleted.
func DeleteWarnings(f StoredFile) []string {
	var w []string
	if len(f.Shares) > 0 {
		w = append(w, "it is being shared right now: sharing stops, a server that has not finished downloading it cannot get it anymore")
	}
	switch {
	case f.Kind == StoredDownload:
		w = append(w, "restoring the same share code again downloads it again (while the source still shares it)")
	case f.Kind == StoredPartial && strings.HasPrefix(f.Path, IncomingDir):
		w = append(w, "a new restore of the same code starts this download from the beginning")
	case f.Kind == StoredSafety && strings.HasPrefix(f.Name, "pre-restore-"):
		w = append(w, "the way back to the Coolify this server had before that full restore is gone")
	case f.Kind == StoredSafety:
		w = append(w, "the folders that restore replaced are gone for good")
	case f.Kind == StoredVolume:
		w = append(w, "the data this volume had before that restore is gone for good")
	}
	return w
}

// StoredOf returns the files of the given kinds, in list order.
func StoredOf(all []StoredFile, kinds ...string) []StoredFile {
	var out []StoredFile
	for _, f := range all {
		for _, k := range kinds {
			if f.Kind == k {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// ReadyBackups are the complete backups kept here: made here or downloaded.
func ReadyBackups(all []StoredFile) []StoredFile {
	return StoredOf(all, StoredBackup, StoredDownload)
}

// StoredTotal is the size of a list of saved files.
func StoredTotal(fs []StoredFile) int64 {
	var n int64
	for _, f := range fs {
		n += f.Size
	}
	return n
}

// CurrentLog is the log file of this run (never deleted).
var CurrentLog string

const oldVolumeMark = ".cm-old-"

// ListStored lists what this tool keeps on this server, newest first within
// each kind (backups, downloads, unfinished, safety copies, volumes, logs,
// leftovers).
func ListStored(ctx context.Context) ([]StoredFile, error) {
	list := listStoredIn(HomeDir, activeShares(ctx), lockHeld())
	vols, err := oldVolumes(ctx)
	if err != nil {
		run.Logf("list old volumes: %v", err)
	}
	list = append(list, vols...)
	sortStored(list)
	return list, nil
}

var kindOrder = map[string]int{StoredBackup: 0, StoredDownload: 1, StoredPartial: 2, StoredSafety: 3, StoredVolume: 4, StoredLogs: 5, StoredTemp: 6}

func sortStored(list []StoredFile) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if kindOrder[a.Kind] != kindOrder[b.Kind] {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		return a.ModTime.After(b.ModTime)
	})
}

const busyRun = "a backup or restore is running on this server - wait until it ends"

// listStoredIn lists the files under home (the tool's folder).
func listStoredIn(home string, shares []ShareInfo, running bool) []StoredFile {
	var out []StoredFile
	add := func(f StoredFile) {
		if running && f.Busy == "" {
			f.Busy = busyRun
		}
		out = append(out, f)
	}
	sharesOf := func(p string) []ShareInfo {
		var s []ShareInfo
		for _, sh := range shares {
			if filepath.Clean(sh.File) == filepath.Clean(p) {
				s = append(s, sh)
			}
		}
		return s
	}

	// Backups made here, also in sub-folders (backup --output).
	backups := filepath.Join(home, "backups")
	_ = filepath.WalkDir(backups, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return nil
		}
		name, _ := filepath.Rel(backups, p)
		base := e.Name()
		switch {
		case e.Type().IsRegular() && strings.HasSuffix(base, ".cmb"):
			f := StoredFile{Kind: StoredBackup, Name: name, Path: p, Size: fi.Size(), ModTime: stampTime(strings.TrimSuffix(base, ".cmb"), fi.ModTime()), files: []string{p}}
			if kfi, err := os.Stat(p + ".key"); err == nil {
				f.Size += kfi.Size()
				f.files = append(f.files, p+".key")
			}
			f.About = describeBackup(p, base)
			f.Shares = sharesOf(p)
			for _, s := range f.Shares {
				if !s.Stoppable() {
					f.Busy = fmt.Sprintf("being shared from another coolify-mirror window (pid %d) - stop sharing there first", s.PID)
				}
			}
			add(f)
		case e.Type().IsRegular() && strings.HasSuffix(base, ".cmb.part"):
			add(StoredFile{Kind: StoredPartial, Name: name, Path: p, Size: fi.Size(), ModTime: stampTime(strings.TrimSuffix(base, ".cmb.part"), fi.ModTime()), files: []string{p},
				About: "unfinished backup (the backup was interrupted)"})
		case e.Type().IsRegular() && strings.HasSuffix(base, ".key"):
			if _, err := os.Stat(strings.TrimSuffix(p, ".key")); err != nil {
				add(StoredFile{Kind: StoredTemp, Name: name, Path: p, Size: fi.Size(), ModTime: fi.ModTime(), files: []string{p},
					About: "key of a backup file that no longer exists"})
			}
		case e.Type().IsRegular() && strings.HasPrefix(base, ".coolify-db-"):
			add(StoredFile{Kind: StoredTemp, Name: name, Path: p, Size: fi.Size(), ModTime: fi.ModTime(), files: []string{p},
				About: "database copy left by an interrupted backup"})
		}
		return nil
	})

	incoming := filepath.Join(home, "incoming")
	for _, e := range readDir(incoming) {
		p := filepath.Join(incoming, e.Name())
		fi, err := e.Info()
		if err != nil || !e.Type().IsRegular() {
			continue
		}
		switch name := e.Name(); {
		case strings.HasSuffix(name, ".cmb"):
			add(StoredFile{Kind: StoredDownload, Name: name, Path: p, Size: fi.Size(), ModTime: fi.ModTime(), files: []string{p},
				About: "backup downloaded from another server (kept because its restore did not finish)"})
		case strings.HasSuffix(name, ".cmb.part"):
			add(StoredFile{Kind: StoredPartial, Name: name, Path: p, Size: fi.Size(), ModTime: fi.ModTime(), files: []string{p},
				About: "unfinished download (restoring the same code again continues it)"})
		}
	}

	for _, e := range readDir(home) {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(home, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "pre-restore-"):
			add(StoredFile{Kind: StoredSafety, Name: name, Path: p, Size: dirSize(p), ModTime: stampTime(name, fi.ModTime()), files: []string{p},
				About: "this server's Coolify database and .env from before the full restore of " + stampText(name, fi.ModTime())})
		case strings.HasPrefix(name, "replaced-"):
			add(StoredFile{Kind: StoredSafety, Name: name, Path: p, Size: dirSize(p), ModTime: stampTime(name, fi.ModTime()), files: []string{p},
				About: "folders replaced by the restore of " + stampText(name, fi.ModTime())})
		case strings.HasPrefix(name, "share-"):
			if len(sharesOfDir(shares, p)) > 0 {
				continue // an active share's certificate
			}
			add(StoredFile{Kind: StoredTemp, Name: name, Path: p, Size: dirSize(p), ModTime: fi.ModTime(), files: []string{p},
				About: "certificate of a share that has ended"})
		case name == "tmp":
			if n := dirSize(p); n > 0 {
				add(StoredFile{Kind: StoredTemp, Name: "tmp", Path: p, Size: n, ModTime: fi.ModTime(), files: []string{p},
					About: "database dumps left by an interrupted run"})
			}
		}
	}

	logs := filepath.Join(home, "logs")
	lf := StoredFile{Kind: StoredLogs, Name: "logs", Path: logs}
	count := 0
	for _, e := range readDir(logs) {
		p := filepath.Join(logs, e.Name())
		fi, err := e.Info()
		if err != nil || !e.Type().IsRegular() || p == CurrentLog || shareLogInUse(shares, p) {
			continue
		}
		count++
		lf.Size += fi.Size()
		lf.files = append(lf.files, p)
		if fi.ModTime().After(lf.ModTime) {
			lf.ModTime = fi.ModTime()
		}
	}
	if count > 0 {
		lf.About = fmt.Sprintf("%d log file(s) of earlier runs", count)
		add(lf)
	}
	return out
}

func readDir(dir string) []os.DirEntry {
	es, _ := os.ReadDir(dir)
	return es
}

// dirSize is the size of the files under p (symlinks are not followed).
func dirSize(p string) int64 {
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// stampTime reads the "20060102-150405" time at the end of a name.
func stampTime(name string, def time.Time) time.Time {
	if len(name) < 15 {
		return def
	}
	if t, err := time.ParseInLocation("20060102-150405", name[len(name)-15:], time.Local); err == nil {
		return t
	}
	return def
}

func stampText(name string, def time.Time) string {
	return stampTime(name, def).Format("2006-01-02 15:04")
}

// describeBackup says what a backup made here contains (from its manifest,
// when its key is next to it; otherwise from its name).
func describeBackup(p, name string) string {
	mode := "selected resources"
	if strings.Contains(name, "-"+ModeFull+"-") {
		mode = "FULL server"
	}
	key, err := os.ReadFile(p + ".key")
	if err != nil {
		return mode
	}
	m, err := manifestOf(p, strings.TrimSpace(string(key)))
	if err != nil {
		return mode
	}
	if m.Mode == ModeFull {
		return fmt.Sprintf("FULL server · %d resource(s)", len(m.Resources))
	}
	return describeProjects(m.Resources)
}

// describeProjects names what a selective backup holds, by project:
// "Shop (4 resources), Blog: blog".
func describeProjects(rs []coolify.Resource) string {
	var parts []string
	for _, p := range coolify.GroupProjects(rs) {
		if p.Name == "" {
			for _, r := range p.Resources {
				parts = append(parts, r.Name)
			}
			continue
		}
		if len(p.Resources) == 1 {
			parts = append(parts, p.Title()+": "+p.Resources[0].Name)
		} else {
			parts = append(parts, fmt.Sprintf("%s (%d resources)", p.Title(), len(p.Resources)))
		}
	}
	if len(parts) > 3 {
		parts = append(parts[:3], fmt.Sprintf("+%d", len(parts)-3))
	}
	return strings.Join(parts, ", ")
}

// manifestOf reads only the manifest at the start of a backup.
func manifestOf(p, key string) (*Manifest, error) {
	r, err := archive.Open(p, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	h, err := r.Next()
	if err != nil {
		return nil, err
	}
	if h.Name != entryManifest {
		return nil, errors.New("no manifest")
	}
	b, err := r.ReadAllEntries(64 << 20)
	if err != nil {
		return nil, err
	}
	m := &Manifest{}
	return m, json.Unmarshal(b, m)
}

// oldVolumes lists the volumes holding data that a restore set aside.
func oldVolumes(ctx context.Context) ([]StoredFile, error) {
	if !run.Exists("docker") {
		return nil, nil
	}
	names, err := docker.VolumeNames(ctx)
	if err != nil {
		return nil, err
	}
	var out []StoredFile
	for _, n := range names {
		i := strings.LastIndex(n, oldVolumeMark)
		if i <= 0 {
			continue
		}
		f := StoredFile{Kind: StoredVolume, Name: n, Volume: n}
		f.ModTime = stampTime(n, time.Time{})
		f.About = "data volume " + n[:i] + " had before the restore of " + f.ModTime.Format("2006-01-02 15:04")
		if v, err := docker.InspectVolume(ctx, n); err == nil && v != nil && v.Mountpoint != "" {
			f.Size = dirSize(v.Mountpoint)
		}
		if cs, err := docker.Containers(ctx, "volume="+n); err == nil && len(cs) > 0 {
			f.Busy = "used by container " + strings.TrimPrefix(cs[0].Names, "/")
		}
		out = append(out, f)
	}
	return out, nil
}

// DeleteStored deletes one saved file (stopping the shares that serve it).
func DeleteStored(ctx context.Context, f StoredFile) error {
	if f.Busy != "" {
		return errors.New(f.Busy)
	}
	if f.Kind != StoredVolume && lockHeld() {
		return errors.New(busyRun)
	}
	for _, s := range f.Shares {
		if err := s.stop(ctx); err != nil {
			return fmt.Errorf("stop sharing %s: %w", f.Name, err)
		}
	}
	if f.Kind == StoredVolume {
		if !strings.Contains(f.Volume, oldVolumeMark) {
			return fmt.Errorf("%s is not a volume set aside by coolify-mirror", f.Volume)
		}
		return docker.RemoveVolume(ctx, f.Volume)
	}
	return removeInside(HomeDir, f.files)
}

// removeInside deletes paths, refusing anything outside home.
func removeInside(home string, paths []string) error {
	for _, p := range paths {
		rel, err := filepath.Rel(home, filepath.Clean(p))
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return fmt.Errorf("refusing to delete %s: it is outside %s", p, home)
		}
	}
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		// A subfolder of backups/ (from --output) left empty goes too;
		// backups/ itself and the other top folders stay.
		for d := filepath.Dir(filepath.Clean(p)); filepath.Dir(d) != filepath.Clean(home) && d != filepath.Clean(home); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
	return nil
}

// UsageSummary is a quick look at the space this tool uses here (no volumes):
// the number of backups made here and the size of everything under its folder.
func UsageSummary() (backups int, size int64) {
	_ = filepath.WalkDir(BackupsDir, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && strings.HasSuffix(e.Name(), ".cmb") {
			backups++
		}
		return nil
	})
	return backups, dirSize(HomeDir)
}

// DiskSpace returns the free and total bytes of the filesystem holding path.
func DiskSpace(path string) (free, total int64) {
	var st unix.Statfs_t
	for p := path; ; p = filepath.Dir(p) {
		if err := unix.Statfs(p, &st); err == nil {
			return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize)
		}
		if p == "/" || p == "." {
			return -1, -1
		}
	}
}

// --- active shares ---------------------------------------------------------------

// ShareInfo is a running share of a backup file, recorded by the process that
// serves it (shares/<id>.json) so other runs know the file is in use.
type ShareInfo struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	File      string    `json:"file"`
	Container string    `json:"container,omitempty"`
	CertDir   string    `json:"cert_dir,omitempty"`
	Log       string    `json:"log,omitempty"`
	Dedicated bool      `json:"dedicated"` // the process only shares (serve command): stopping it is safe
	Since     time.Time `json:"since"`
}

// Stoppable: the share runs in a process of its own (a background share or
// the serve command), which can be ended without touching anything else.
func (s ShareInfo) Stoppable() bool { return s.Dedicated || s.PID == 0 }

func sharesDir() string { return filepath.Join(HomeDir, "shares") }

func registerShare(s ShareInfo) {
	if err := os.MkdirAll(sharesDir(), 0o700); err != nil {
		return
	}
	b, _ := json.Marshal(s)
	_ = os.WriteFile(filepath.Join(sharesDir(), s.ID+".json"), b, 0o600)
}

func unregisterShare(id string) {
	if id != "" {
		_ = os.Remove(filepath.Join(sharesDir(), id+".json"))
	}
}

// activeShares lists the shares running on this server: the ones recorded by
// a live process, plus share containers nobody recorded (an older version, or
// a process that was killed).
func activeShares(ctx context.Context) []ShareInfo {
	var out []ShareInfo
	known := map[string]bool{}
	for _, e := range readDir(sharesDir()) {
		p := filepath.Join(sharesDir(), e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s ShareInfo
		if json.Unmarshal(b, &s) != nil || !processAlive(s.PID) {
			_ = os.Remove(p) // its process is gone
			continue
		}
		out = append(out, s)
		if s.Container != "" {
			known[s.Container] = true
		}
	}
	if !run.Exists("docker") {
		return out
	}
	cs, err := docker.Containers(ctx, "label=coolify-mirror.share=true")
	if err != nil {
		return out
	}
	for _, c := range cs {
		name := strings.TrimPrefix(c.Names, "/")
		if known[name] {
			continue
		}
		s := ShareInfo{Container: name}
		ms, _ := docker.Mounts(ctx, c.ID)
		for _, m := range ms {
			switch m.Destination {
			case "/share/backup.cmb":
				s.File = m.Source
			case "/share/tls":
				s.CertDir = m.Source
			}
		}
		out = append(out, s)
	}
	return out
}

func sharesOfDir(shares []ShareInfo, dir string) []ShareInfo {
	var out []ShareInfo
	for _, s := range shares {
		if s.CertDir != "" && filepath.Clean(s.CertDir) == filepath.Clean(dir) {
			out = append(out, s)
		}
	}
	return out
}

func shareLogInUse(shares []ShareInfo, p string) bool {
	for _, s := range shares {
		if s.Log != "" && filepath.Clean(s.Log) == filepath.Clean(p) {
			return true
		}
	}
	return false
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := unix.Kill(pid, 0); err != nil && !errors.Is(err, unix.EPERM) {
		return false
	}
	// A recycled pid is not ours.
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return err != nil || strings.Contains(string(b), "coolify-mirror")
}

// stop ends a share: its process (which removes its container, certificate
// and firewall rule itself), or an unrecorded share container.
func (s ShareInfo) stop(ctx context.Context) error {
	if s.PID > 0 && s.PID != os.Getpid() && processAlive(s.PID) {
		if !s.Dedicated {
			return fmt.Errorf("it is shared from another coolify-mirror window (pid %d) - stop sharing there first", s.PID)
		}
		_ = unix.Kill(s.PID, unix.SIGTERM)
		deadline := time.Now().Add(20 * time.Second)
		for processAlive(s.PID) && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
		if processAlive(s.PID) {
			_ = unix.Kill(s.PID, unix.SIGKILL)
			time.Sleep(500 * time.Millisecond)
		}
	}
	if s.Container != "" {
		_ = docker.Remove(ctx, s.Container)
	}
	if s.CertDir != "" {
		_ = removeInside(HomeDir, []string{s.CertDir})
	}
	unregisterShare(s.ID)
	return nil
}
