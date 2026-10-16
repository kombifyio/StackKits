package engine

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/archive"
	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/run"
)

// Path restore modes.
const (
	pathSkip    = "skip"
	pathReplace = "replace" // move the existing directory aside, restore a fresh copy
	pathMerge   = "merge"   // write over existing content, keep other files
)

type extractPlan struct {
	PathTarget   func(PathEntry) (target, mode string)
	VolumeTarget func(VolumeEntry) (VolumeEntry, bool)
	// OnEntry handles db/* and extra/* entries; return false to fall through.
	OnEntry   func(h *tar.Header, r io.Reader) (bool, error)
	PathStep  func(PathEntry) *Step
	VolStep   func(VolumeEntry) *Step
	ImageStep func(i int) *Step
	AsideDir  string // where replaced directories are moved
	// Dumps receives the database dumps written to disk (loaded afterwards).
	Dumps *[]pendingDump
	// DumpWanted filters dumps (selective restore skips skipped resources).
	DumpWanted func(DumpEntry) bool
}

// undoLog records changes so a failed restore can be rolled back.
type undoLog struct {
	volumes []string          // volumes we created
	created []string          // directories we created
	aside   map[string]string // original path -> moved-aside path
	swaps   []volSwap         // existing volumes whose data was set aside
	images  []string          // tags we added
}

// volSwap: the previous data of an existing volume was moved into an "aside"
// volume (same filesystem, instant rename) instead of being deleted.
type volSwap struct {
	name, aside   string
	mount, asideM string
}

func newUndo() *undoLog { return &undoLog{aside: map[string]string{}} }

// rollback removes what the restore created and puts replaced directories
// and replaced volume data back.
func (u *undoLog) rollback() []error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var errs []error
	for _, v := range u.volumes {
		if err := docker.RemoveVolume(ctx, v); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(u.swaps) - 1; i >= 0; i-- {
		sw := u.swaps[i]
		if err := os.RemoveAll(sw.mount); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(sw.asideM, sw.mount); err != nil {
			errs = append(errs, err)
			continue
		}
		_ = os.Mkdir(sw.asideM, 0o755)
		_ = docker.RemoveVolume(ctx, sw.aside)
	}
	for _, d := range u.created {
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, err)
		}
	}
	for orig, aside := range u.aside {
		_ = os.RemoveAll(orig)
		if err := os.Rename(aside, orig); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// topMissing returns the highest directory on the way to p that does not
// exist yet (p itself when its parent exists): removing it on rollback also
// removes the parent directories the restore created.
func topMissing(p string) string {
	p = filepath.Clean(p)
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		if _, err := os.Lstat(parent); err == nil {
			return p
		}
		p = parent
	}
}

// asideVolumes lists the volumes holding previous data (kept after success).
func (u *undoLog) asideVolumes() []string {
	var out []string
	for _, sw := range u.swaps {
		out = append(out, sw.aside)
	}
	return out
}

type pathState struct {
	entry  PathEntry
	prefix string
	target string
	single bool
	ex     *archive.Extractor
	step   *Step
	skip   bool
}

type volState struct {
	entry    VolumeEntry
	ex       *archive.Extractor
	step     *Step
	skip     bool
	prepared bool
}

type imageLoader struct {
	pw   *io.PipeWriter
	tw   *tar.Writer
	done chan error
	once sync.Once
	err  error
}

// wait returns the result of `docker load` (safe to call more than once).
func (l *imageLoader) wait() error {
	l.once.Do(func() { l.err = <-l.done })
	return l.err
}

func startLoader(ctx context.Context) *imageLoader {
	pr, pw := io.Pipe()
	l := &imageLoader{pw: pw, tw: tar.NewWriter(pw), done: make(chan error, 1)}
	go func() {
		err := docker.Load(ctx, pr)
		pr.CloseWithError(errOr(err, io.ErrClosedPipe))
		l.done <- err
	}()
	return l
}

func errOr(err, def error) error {
	if err != nil {
		return err
	}
	return def
}

func (l *imageLoader) finish() error {
	werr := l.tw.Close()
	l.pw.Close()
	if lerr := l.wait(); lerr != nil {
		return fmt.Errorf("docker load: %w", lerr)
	}
	return werr
}

// extractArchive streams the backup once and writes every entry to its place.
func extractArchive(ctx context.Context, f *Fetched, xp extractPlan, pr *Progress, undo *undoLog) (err error) {
	r, err := archive.Open(f.Path, f.Key)
	if err != nil {
		return err
	}
	defer r.Close()
	warn := func(m string) { pr.Warn("%s", m) }

	// Longest prefix first, so nested paths resolve to the most specific entry.
	var paths []*pathState
	for _, p := range f.Manifest.Paths {
		paths = append(paths, &pathState{entry: p, prefix: prefixPaths + p.Path})
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i].prefix) > len(paths[j].prefix) })
	vols := map[string]*volState{}
	for _, v := range f.Manifest.Volumes {
		vols[v.Name] = &volState{entry: v}
	}

	copyBuf := make([]byte, 1<<20)
	var curPath *pathState
	var curVol *volState
	var loader *imageLoader
	var loaderStep *Step
	loaderGroup := ""
	finishPath := func() {
		if curPath != nil && curPath.ex != nil {
			_ = curPath.ex.Finish()
			if curPath.step != nil {
				curPath.step.Finish(HumanBytes(curPath.entry.Size))
			}
		}
		curPath = nil
	}
	finishVol := func() {
		if curVol != nil && curVol.ex != nil {
			_ = curVol.ex.Finish()
			if curVol.step != nil {
				curVol.step.Finish(HumanBytes(curVol.entry.Size))
			}
		}
		curVol = nil
	}
	finishImages := func() error {
		if loader == nil {
			return nil
		}
		err := loader.finish()
		loader = nil
		if loaderStep != nil {
			if err != nil {
				loaderStep.Fail(err)
			} else {
				loaderStep.Finish("loaded")
			}
		}
		return err
	}
	defer func() {
		if loader != nil {
			loader.pw.CloseWithError(errors.New("aborted"))
			_ = loader.wait()
		}
	}()

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
		name := h.Name
		switch {
		case name == entryManifest || name == entryExport || name == entryTransfer:
			continue

		case strings.HasPrefix(name, prefixPaths+"/"):
			var ps *pathState
			for _, p := range paths {
				if name == p.prefix || name == p.prefix+"/" || strings.HasPrefix(name, p.prefix+"/") {
					ps = p
					break
				}
			}
			if ps == nil {
				warn("unexpected file in backup: " + name)
				continue
			}
			if ps != curPath {
				finishPath()
				finishVol()
				curPath = ps
				// A saved directory starts with its "prefix/" entry; a single file is "prefix".
				ps.single = name == ps.prefix && h.Typeflag != tar.TypeDir
				if err := preparePath(ps, xp, undo, warn); err != nil {
					return err
				}
			}
			if ps.skip {
				continue
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(name, ps.prefix), "/")
			if ps.single {
				rel = filepath.Base(ps.target)
			}
			linkRel := ""
			if h.Typeflag == tar.TypeLink {
				linkRel = strings.TrimPrefix(strings.TrimPrefix(h.Linkname, ps.prefix), "/")
			}
			if err := ps.ex.Entry(rel, h, r, linkRel); err != nil {
				return fmt.Errorf("restore %s: %w", ps.target, err)
			}

		case strings.HasPrefix(name, prefixVolumes+"/"):
			rest := strings.TrimPrefix(name, prefixVolumes+"/")
			vname, rel, _ := strings.Cut(rest, "/")
			vs := vols[vname]
			if vs == nil {
				warn("unexpected volume in backup: " + vname)
				continue
			}
			if vs != curVol {
				finishPath()
				finishVol()
				curVol = vs
				if err := prepareVolume(ctx, vs, xp, undo, warn); err != nil {
					return err
				}
			}
			if vs.skip {
				continue
			}
			linkRel := ""
			if h.Typeflag == tar.TypeLink {
				linkRel = strings.TrimPrefix(strings.TrimPrefix(h.Linkname, prefixVolumes+"/"+vname), "/")
			}
			if err := vs.ex.Entry(rel, h, r, linkRel); err != nil {
				return fmt.Errorf("restore volume %s: %w", vs.entry.Name, err)
			}

		case strings.HasPrefix(name, prefixDumps+"/"):
			finishPath()
			finishVol()
			pd, err := extractDump(f.Manifest, name, r)
			if err != nil {
				return err
			}
			if xp.Dumps == nil || (xp.DumpWanted != nil && !xp.DumpWanted(pd.DumpEntry)) {
				_ = os.Remove(pd.file)
				continue
			}
			*xp.Dumps = append(*xp.Dumps, *pd)

		case strings.HasPrefix(name, prefixImages+"/"):
			rest := strings.TrimPrefix(name, prefixImages+"/")
			group, inner, _ := strings.Cut(rest, "/")
			if group != loaderGroup || loader == nil {
				finishPath()
				finishVol()
				if err := finishImages(); err != nil {
					return err
				}
				loaderGroup = group
				var idx int
				fmt.Sscan(group, &idx)
				loaderStep = nil
				if xp.ImageStep != nil {
					loaderStep = xp.ImageStep(idx)
				}
				if loaderStep == nil {
					// images not wanted (e.g. different CPU architecture)
					loader = nil
					continue
				}
				loaderStep.Begin("docker load")
				loader = startLoader(ctx)
			}
			if loader == nil {
				continue
			}
			h2 := *h
			h2.Name = inner
			if h.Typeflag == tar.TypeLink {
				h2.Linkname = strings.TrimPrefix(strings.TrimPrefix(h.Linkname, prefixImages+"/"+group), "/")
			}
			if err := loader.tw.WriteHeader(&h2); err != nil {
				return fmt.Errorf("docker load: %w", errOr(loader.wait(), err))
			}
			n, err := io.CopyBuffer(loader.tw, r, copyBuf)
			if loaderStep != nil {
				loaderStep.Advance(n)
			}
			if err != nil {
				return fmt.Errorf("docker load: %w", err)
			}

		default:
			finishPath()
			finishVol()
			handled := false
			if xp.OnEntry != nil {
				if handled, err = xp.OnEntry(h, r); err != nil {
					return err
				}
			}
			if !handled {
				run.Logf("ignored archive entry %s", name)
			}
		}
	}
	finishPath()
	finishVol()
	if err := finishImages(); err != nil {
		return err
	}
	// Volumes stored as definition only (NFS/CIFS/bind options, or database
	// volumes saved as dumps) have no entries.
	for _, vs := range vols {
		if !vs.prepared && (vs.entry.External || vs.entry.Dumped) {
			if err := prepareVolume(ctx, vs, xp, undo, warn); err != nil {
				return err
			}
		}
	}
	return nil
}

func preparePath(ps *pathState, xp extractPlan, undo *undoLog, warn func(string)) error {
	target, mode := xp.PathTarget(ps.entry)
	if mode == pathSkip || target == "" {
		ps.skip = true
		return nil
	}
	if deniedHostPath(target) && !strings.HasPrefix(target, "/data/coolify/") {
		warn("refusing to write to protected path " + target)
		ps.skip = true
		return nil
	}
	ps.target = target
	if xp.PathStep != nil {
		ps.step = xp.PathStep(ps.entry)
		if ps.step != nil {
			ps.step.Begin(target)
		}
	}
	_, err := os.Lstat(target)
	exists := err == nil
	if exists && mode == pathReplace {
		aside := filepath.Join(xp.AsideDir, strings.TrimPrefix(target, "/"))
		if err := os.MkdirAll(filepath.Dir(aside), 0o700); err != nil {
			return err
		}
		if err := os.Rename(target, aside); err != nil {
			aside = target + ".coolify-mirror-old-" + time.Now().Format("20060102-150405")
			if err := os.Rename(target, aside); err != nil {
				return fmt.Errorf("move existing %s aside: %w", target, err)
			}
		}
		undo.aside[target] = aside
		exists = false
	}
	if !exists {
		undo.created = append(undo.created, topMissing(target))
	}
	root := target
	if ps.single {
		root = filepath.Dir(target)
	}
	ex, err := archive.NewExtractor(root)
	if err != nil {
		return err
	}
	ex.Warn = warn
	if ps.step != nil {
		ex.Progress = ps.step.Advance
	}
	ps.ex = ex
	return nil
}

func prepareVolume(ctx context.Context, vs *volState, xp extractPlan, undo *undoLog, warn func(string)) error {
	vs.prepared = true
	target, ok := xp.VolumeTarget(vs.entry)
	if !ok {
		vs.skip = true
		return nil
	}
	if xp.VolStep != nil {
		vs.step = xp.VolStep(vs.entry)
		if vs.step != nil {
			vs.step.Begin(target.Name)
		}
	}
	existing, err := docker.InspectVolume(ctx, target.Name)
	if err != nil {
		return err
	}
	if target.Driver == "" {
		target.Driver = "local"
	}
	var mount string
	if existing != nil {
		users, err := docker.Containers(ctx, "volume="+target.Name, "status=running")
		if err != nil {
			return err
		}
		if len(users) > 0 {
			return fmt.Errorf("volume %s already exists and is used by running container(s) %s; stop them first", target.Name, docker.Describe(users))
		}
		if vs.entry.External || existing.Mountpoint == "" {
			vs.skip = true // definition only: keep the existing volume as it is
			if vs.step != nil {
				vs.step.Finish("exists")
			}
			return nil
		}
		// Never delete existing data: move it into an aside volume (an instant
		// rename on the same filesystem). Rollback moves it back.
		aside := target.Name + ".cm-old-" + time.Now().Format("20060102-150405")
		if err := docker.CreateVolume(ctx, docker.Volume{Name: aside, Driver: "local"}); err != nil {
			return fmt.Errorf("create aside volume for %s: %w", target.Name, err)
		}
		av, err := docker.InspectVolume(ctx, aside)
		if err != nil || av == nil {
			return fmt.Errorf("inspect aside volume %s: %v", aside, err)
		}
		tmp := existing.Mountpoint + ".cm-swap"
		if err := os.Rename(existing.Mountpoint, tmp); err != nil {
			_ = docker.RemoveVolume(ctx, aside)
			return fmt.Errorf("set aside data of volume %s: %w", target.Name, err)
		}
		if err := os.Rename(av.Mountpoint, existing.Mountpoint); err != nil {
			_ = os.Rename(tmp, existing.Mountpoint)
			_ = docker.RemoveVolume(ctx, aside)
			return fmt.Errorf("set aside data of volume %s: %w", target.Name, err)
		}
		if err := os.Rename(tmp, av.Mountpoint); err != nil {
			return fmt.Errorf("set aside data of volume %s: %w", target.Name, err)
		}
		undo.swaps = append(undo.swaps, volSwap{name: target.Name, aside: aside, mount: existing.Mountpoint, asideM: av.Mountpoint})
		warn(fmt.Sprintf("volume %s already existed: its previous data is kept in volume %s", target.Name, aside))
		mount = existing.Mountpoint
	} else {
		if err := docker.CreateVolume(ctx, docker.Volume{Name: target.Name, Driver: target.Driver, Labels: target.Labels, Options: target.Options}); err != nil {
			return fmt.Errorf("create volume %s: %w", target.Name, err)
		}
		undo.volumes = append(undo.volumes, target.Name)
		v, err := docker.InspectVolume(ctx, target.Name)
		if err != nil || v == nil {
			return fmt.Errorf("inspect new volume %s: %v", target.Name, err)
		}
		mount = v.Mountpoint
		if vs.entry.External {
			vs.skip = true
			if vs.step != nil {
				vs.step.Finish("created (data lives outside the volume)")
			}
			return nil
		}
	}
	if vs.entry.Dumped {
		// The data comes from the database dump, loaded later.
		vs.skip = true
		if vs.step != nil {
			vs.step.Finish("filled from the database dump")
		}
		return nil
	}
	v := struct{ Mountpoint string }{mount}
	ex, err := archive.NewExtractor(v.Mountpoint)
	if err != nil {
		return err
	}
	ex.Warn = warn
	if vs.step != nil {
		ex.Progress = vs.step.Advance
	}
	vs.ex = ex
	return nil
}
