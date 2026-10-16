package engine

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
	"github.com/mtalavi/coolify-mirror/internal/docker"
)

// SelectiveRestore restores individual resources into a running Coolify.
type SelectiveRestore struct {
	In        *coolify.Instance
	F         *Fetched
	Target    *dbx.TargetState
	Conflicts []coolify.Resource // resources whose uuid already exists here
	Plan      *dbx.Plan
	// ExistingVolumes lists target volumes that get the backup's data (their
	// current data is kept in an aside volume).
	ExistingVolumes []string
	// HostPaths describes directories outside Coolify's own folders that the
	// restore writes (bind mounts of the resources).
	HostPaths []HostPath
	// DomainClashes lists domains already used by resources on this server.
	DomainClashes []string
	// Blockers forbid the restore (version mismatch, disk space).
	Blockers []string
	// CoolifyWarnings come from Coolify's own transfer bundle validation.
	CoolifyWarnings []string
	// Warnings: what to set up on this server first so everything works
	// (private addresses that Coolify must be allowed to connect to).
	Warnings []string

	sql       string
	decisions map[string]dbx.Decision
	shared    map[string]bool // host paths not restored (shared with the original of a copy)
}

// HostPath is a bind-mount directory the restore will write.
type HostPath struct {
	Path   string
	Exists bool // current content is moved aside first
	Shared bool // not restored: a copy keeps using the original's directory
	Same   bool // not restored: this server already has the identical file
	File   bool // a single file (a build policy script, ...), not a folder
}

// isResourceDir reports Coolify's per-resource folders (always restored).
func isResourceDir(p string) bool {
	for _, d := range []string{"applications", "services", "databases"} {
		if strings.HasPrefix(p, filepath.Join(coolify.BaseDir, d)+"/") {
			return true
		}
	}
	return false
}

// PrepareSelective checks the target and finds conflicts.
func PrepareSelective(ctx context.Context, in *coolify.Instance, f *Fetched, teamID int64) (*SelectiveRestore, error) {
	if f.Export == nil {
		return nil, errors.New("this is not a selective backup")
	}
	if err := in.CheckCrypto(ctx); err != nil {
		return nil, fmt.Errorf("encryption self-test against this Coolify failed: %w", err)
	}
	if err := in.EnsureLocalServer(ctx); err != nil {
		return nil, err
	}
	ts, err := dbx.LoadTarget(ctx, in, f.Export, teamID)
	if err != nil {
		return nil, err
	}
	s := &SelectiveRestore{In: in, F: f, Target: ts, Blockers: PreflightSelective(ctx, in, f), CoolifyWarnings: BundleWarnings(ctx, in, f),
		Warnings: dbx.InternalHostWarnings(f.Export, ts)}
	for _, r := range f.Export.Roots {
		if _, ok := ts.Existing[r.Table][r.UUID]; ok {
			s.Conflicts = append(s.Conflicts, r)
		}
	}
	return s, nil
}

// Build computes the import plan for the given conflict decisions and
// validates the generated SQL with a rolled-back trial run.
func (s *SelectiveRestore) Build(ctx context.Context, decisions map[string]dbx.Decision) error {
	s.decisions = decisions
	plan, err := dbx.BuildPlan(s.F.Export, s.Target, func(r coolify.Resource) dbx.Decision {
		if d, ok := decisions[r.UUID]; ok {
			return d
		}
		return dbx.NewCopy
	}, dbx.NextIDs(ctx, s.In), s.In.Crypt.EncryptString)
	if err != nil {
		return err
	}
	sql, err := plan.SQL()
	if err != nil {
		return err
	}
	if err := s.In.ExecSQL(ctx, "BEGIN;\n"+sql+"ROLLBACK;\n", false); err != nil {
		return fmt.Errorf("the configuration cannot be imported into this Coolify: %w", err)
	}
	s.Plan, s.sql = plan, sql

	s.ExistingVolumes = nil
	for _, v := range s.F.Manifest.Volumes {
		if s.skipOwner(v.Owner) {
			continue
		}
		name := plan.Rename(v.Name)
		if ex, _ := docker.InspectVolume(ctx, name); ex != nil {
			s.ExistingVolumes = append(s.ExistingVolumes, name)
		}
	}
	s.HostPaths, s.shared = nil, map[string]bool{}
	for _, p := range s.F.Manifest.Paths {
		if s.skipOwner(p.Owner) || isResourceDir(p.Path) {
			continue
		}
		hp := HostPath{Path: plan.Rename(p.Path), File: isHostFile(s.F.Manifest, p.Path)}
		if sameHostFile(s.F.Manifest, p.Path) {
			hp.Same = true
			s.shared[p.Path] = true
		} else if hp.Path == p.Path && s.decisions[p.Owner] == dbx.NewCopy {
			// The copy points at the same host directory as the original, which
			// keeps running: never overwrite it.
			hp.Shared = true
			s.shared[p.Path] = true
		} else if _, err := os.Stat(hp.Path); err == nil {
			hp.Exists = true
		}
		s.HostPaths = append(s.HostPaths, hp)
	}
	s.DomainClashes = s.domainClashes(ctx)
	return nil
}

func (s *SelectiveRestore) skipOwner(owner string) bool {
	return owner != "" && s.decisions[owner] == dbx.Skip
}

// domainClashes finds restored resources whose domains are already served by
// a resource on this server. Those are restored but not started: two routers
// for the same host would split the traffic between them.
func (s *SelectiveRestore) domainClashes(ctx context.Context) []string {
	existing, err := s.In.ListResources(ctx)
	if err != nil {
		return nil
	}
	used := map[string]string{}
	for _, r := range existing {
		for _, d := range r.Domains {
			used[coolify.Host(d)] = r.Name
		}
	}
	var out []string
	for i := range s.Plan.Resources {
		pr := &s.Plan.Resources[i]
		var clashes []string
		for _, d := range pr.Domains {
			if owner, ok := used[coolify.Host(d)]; ok {
				clashes = append(clashes, coolify.Host(d))
				out = append(out, fmt.Sprintf("%s (already used by %s) - change it in the last step (Domains), or %s stays stopped", coolify.Host(d), owner, pr.Name))
			}
		}
		if len(clashes) > 0 {
			pr.Hold = "not started: " + strings.Join(clashes, ", ") + " is already used on this server - change the domain in Coolify, then deploy"
		}
	}
	return out
}

// RestoreReport summarizes a finished restore.
type RestoreReport struct {
	Resources []dbx.PlannedResource
	Notes     []string
	// Problems: the restore is complete but something a resource needs to
	// run or deploy again is missing here (never reported as a success).
	Problems []string
	AsideDir string
	Duration time.Duration
}

// Apply writes files, volumes and images, then imports the configuration.
func (s *SelectiveRestore) Apply(ctx context.Context, pr *Progress) (rep *RestoreReport, err error) {
	defer func() { pr.End(err) }()
	if err := blockersErr(PreflightSelective(ctx, s.In, s.F)); err != nil {
		return nil, err
	}
	if s.Plan == nil {
		return nil, errors.New("restore not allowed: the import was not prepared and trial-run first")
	}
	unlock, err := Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	start := time.Now()
	plan := s.Plan
	man := s.F.Manifest
	asideDir := filepath.Join(HomeDir, "replaced-"+time.Now().Format("20060102-150405"))

	pathSteps := map[string]*Step{}
	for _, p := range man.Paths {
		if s.skipOwner(p.Owner) {
			continue
		}
		st := pr.Add("Files  "+plan.Rename(p.Path), p.Size)
		switch {
		case sameHostFile(man, p.Path):
			st.SkipStep("identical here - kept")
		case s.shared[p.Path]:
			st.SkipStep("shared with the original - kept as it is")
		default:
			pathSteps[p.Path] = st
		}
	}
	volSteps := map[string]*Step{}
	for _, v := range man.Volumes {
		if !s.skipOwner(v.Owner) {
			volSteps[v.Name] = pr.Add("Volume "+plan.Rename(v.Name), v.Size)
		}
	}
	imgSteps := map[int]*Step{}
	for i, im := range man.Images {
		imgSteps[i] = pr.Add(imageTitle(im.Refs), im.Size)
	}
	dumpSteps := map[string]*Step{}
	for _, d := range man.Dumps {
		if !s.skipOwner(d.Owner) {
			dumpSteps[d.Container] = pr.Add("Load database dump "+plan.Rename(d.Container), d.Size)
		}
	}
	stDB := pr.Add("Add resources to Coolify", 0)
	stNet := pr.Add("Prepare networks", 0)
	stDeps := pr.Add("Check build and runtime dependencies", 0)

	if man.Source.Arch != "" && s.In.Arch != "" && man.Source.Arch != s.In.Arch && len(man.Images) > 0 {
		pr.Warn("images were built for %s but this server is %s; they are skipped and Coolify will rebuild", man.Source.Arch, s.In.Arch)
		for i := range imgSteps {
			imgSteps[i].SkipStep("other CPU architecture")
			delete(imgSteps, i)
		}
	}

	undo := newUndo()
	failed := true
	var dumps []pendingDump
	defer func() {
		for _, d := range dumps {
			_ = os.Remove(d.file)
		}
		if failed {
			if errs := undo.rollback(); len(errs) > 0 {
				pr.Warn("rollback was incomplete: %v", errors.Join(errs...))
			}
		}
	}()

	xp := extractPlan{
		AsideDir: asideDir,
		PathTarget: func(p PathEntry) (string, string) {
			if s.skipOwner(p.Owner) || s.shared[p.Path] {
				return "", pathSkip
			}
			return plan.Rename(p.Path), pathReplace
		},
		VolumeTarget: func(v VolumeEntry) (VolumeEntry, bool) {
			if s.skipOwner(v.Owner) {
				return v, false
			}
			t := v
			t.Name = plan.Rename(v.Name)
			t.Labels = map[string]string{}
			for k, val := range v.Labels {
				t.Labels[k] = plan.Rename(val)
			}
			return t, true
		},
		PathStep:   func(p PathEntry) *Step { return pathSteps[p.Path] },
		VolStep:    func(v VolumeEntry) *Step { return volSteps[v.Name] },
		ImageStep:  func(i int) *Step { return imgSteps[i] },
		Dumps:      &dumps,
		DumpWanted: func(d DumpEntry) bool { return !s.skipOwner(d.Owner) },
		OnEntry: func(h *tar.Header, r io.Reader) (bool, error) {
			if h.Name != entryDockerConfig {
				return false, nil
			}
			b, err := io.ReadAll(io.LimitReader(r, 4<<20))
			if err != nil {
				return true, err
			}
			if err := mergeDockerConfig(b); err != nil {
				pr.Warn("docker registry logins were not merged: %v", err)
			}
			return true, nil
		},
	}
	if err = extractArchive(ctx, s.F, xp, pr, undo); err != nil {
		return nil, err
	}

	// Images of copied resources carry the old uuid in their tag.
	for i, im := range man.Images {
		if imgSteps[i] == nil {
			continue
		}
		for _, ref := range im.Refs {
			if nr := plan.Rename(ref); nr != ref {
				if err := docker.Tag(ctx, ref, nr); err != nil {
					pr.Warn("tag %s as %s: %v", ref, nr, err)
				}
			}
		}
	}

	if err = loadDumps(ctx, dumps, plan.Rename, dumpSteps); err != nil {
		return nil, err
	}
	dumps = nil
	builderNotes, err := restoreBuilders(man, undo)
	if err != nil {
		return nil, fmt.Errorf("install buildx builders: %w", err)
	}

	stDB.Begin("database transaction")
	if err = failpoint("selective-before-import"); err != nil {
		stDB.Fail(err)
		return nil, err
	}
	if err = s.In.ExecSQL(ctx, s.sql, true); err != nil {
		stDB.Fail(err)
		return nil, fmt.Errorf("import into Coolify failed (nothing was changed in its database): %w", err)
	}
	failed = false
	var marks []map[string]string
	for _, r := range plan.Resources {
		if r.Table == "applications" && r.DeploymentUUID != "" {
			marks = append(marks, map[string]string{"app_uuid": r.UUID, "deployment_uuid": r.DeploymentUUID})
		}
	}
	if len(marks) > 0 {
		if err := s.In.PHP(ctx, "mark_applied", marks, nil); err != nil {
			pr.Warn("could not record the restored deployment state (Coolify may rebuild): %v", err)
		}
	}
	stDB.Finish(fmt.Sprintf("%d resource(s)", len(plan.Resources)))

	stNet.Begin("")
	for _, n := range plan.Networks {
		if err := docker.EnsureNetwork(ctx, n); err != nil {
			pr.Warn("network %s: %v", n, err)
			continue
		}
		if st, _ := coolify.ContainerState(ctx, coolify.ProxyContainer); st == "running" {
			_ = docker.Connect(ctx, n, coolify.ProxyContainer)
		}
	}
	stNet.Finish("")

	stDeps.Begin("")
	problems := checkHostDeps(ctx, man, plan.Rename, s.skipOwner)
	if len(problems) > 0 {
		stDeps.Fail(fmt.Errorf("%d missing", len(problems)))
	} else {
		stDeps.Finish(fmt.Sprintf("%d checked", len(man.HostDeps)))
	}
	for i := range plan.Resources {
		r := &plan.Resources[i]
		// A copy gets new uuids, and single-container applications are
		// named after theirs.
		r.Expect = nil
		for _, s := range man.Runtime[r.SourceUUID] {
			r.Expect = append(r.Expect, plan.Rename(s))
		}
		// Coolify's status column can lag behind; running containers count.
		r.WasRunning = r.WasRunning || len(r.Expect) > 0
	}

	notes := append(append([]string{}, plan.Notes...), builderNotes...)
	for _, w := range plan.Warnings {
		pr.Warn("%s", w)
	}
	if len(undo.aside) > 0 {
		notes = append(notes, "replaced directories were moved to "+asideDir+" (delete them from the menu, 'Saved files & disk space', once everything works)")
	}
	if av := undo.asideVolumes(); len(av) > 0 {
		notes = append(notes, "previous data of volumes that already existed is kept in: "+strings.Join(av, ", ")+" (delete them from the menu, 'Saved files & disk space', once everything works)")
	}
	return &RestoreReport{Resources: plan.Resources, Notes: notes, Problems: problems, AsideDir: asideDir, Duration: time.Since(start)}, nil
}
