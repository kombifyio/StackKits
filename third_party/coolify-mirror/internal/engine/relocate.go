package engine

// Relocation plan and check (kombify fork addition, see KOMBIFY-FORK.md).
//
// A plan is the read-only inventory a backup would take on the source server:
// resources, volumes, host paths, images, database dumps, host dependencies
// and sizes. It carries names, sizes and fingerprints only; no secret, no
// database export and no transfer bundle. A check runs on the destination
// server against such a plan and lists what forbids or complicates the move,
// with the same preflight rules the restore enforces. Neither writes
// anything, pauses a container or opens a port.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
)

// Plan and check document formats.
const (
	PlanFormat  = "kombify.io/coolify-relocation-plan/v1"
	CheckFormat = "kombify.io/coolify-relocation-check/v1"
)

// ToolInfo identifies the exact coolify-mirror build.
type ToolInfo struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	UpstreamCommit string `json:"upstream_commit"`
	ForkRevision   string `json:"fork_revision"`
}

// Tool returns this build's identity.
func Tool() ToolInfo {
	return ToolInfo{Name: "coolify-mirror", Version: Version, UpstreamCommit: UpstreamCommit, ForkRevision: ForkRevision}
}

// RelocationPlan is the source side of a move.
type RelocationPlan struct {
	Format    string    `json:"format"`
	Tool      ToolInfo  `json:"tool"`
	CreatedAt time.Time `json:"created_at"`
	// Blockers forbid a backup on the source (Coolify changed, helper broken).
	Blockers []string `json:"blockers"`
	Warnings []string `json:"warnings"`
	Manifest Manifest `json:"manifest"`
}

// ResourceRef names one resource in a check result.
type ResourceRef struct {
	UUID string `json:"uuid"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// RelocationCheck is the destination side of a move.
type RelocationCheck struct {
	Format      string        `json:"format"`
	Tool        ToolInfo      `json:"tool"`
	CheckedAt   time.Time     `json:"checked_at"`
	Destination SourceInfo    `json:"destination"`
	Mode        string        `json:"mode"`
	Ready       bool          `json:"ready"`
	Blockers    []string      `json:"blockers"`
	Warnings    []string      `json:"warnings"`
	PlanSource  SourceInfo    `json:"plan_source"`
	Conflicts   []ResourceRef `json:"conflicts"`
	// DomainClashes are domains of the plan already served here.
	DomainClashes []string `json:"domain_clashes"`
}

// Plan inventories what a backup with req would take, without taking it.
func Plan(ctx context.Context, in *coolify.Instance, req BackupRequest) (*RelocationPlan, error) {
	if req.Consistency == "" {
		req.Consistency = ConsistencyPause
	}
	if req.Images == "" {
		req.Images = ImagesApps
	}
	if req.Mode == "" {
		req.Mode = ModeSelective
	}
	pr := NewProgress("Plan")
	b := &backupper{in: in, req: req, pr: pr, pathSeen: map[string]bool{}, volSeen: map[string]bool{}, imgSeen: map[string]bool{}}
	v4, v6 := in.PublicIPs(ctx)
	b.man = &Manifest{Format: FormatName, ToolVersion: Version, CreatedAt: time.Now().UTC(), Mode: req.Mode,
		Source:  SourceInfo{Hostname: in.Hostname, IPv4: v4, IPv6: v6, CoolifyVersion: in.Version, Arch: in.Arch},
		Options: BackupOptions{Consistency: req.Consistency, Images: req.Images, IncludeBackups: req.IncludeBackups}}
	p := &RelocationPlan{Format: PlanFormat, Tool: Tool(), CreatedAt: b.man.CreatedAt, Blockers: []string{}, Warnings: []string{}}

	compat := CheckCompat(ctx, in)
	p.Warnings = append(p.Warnings, compat.Warnings...)
	if len(compat.Blockers) > 0 {
		p.Blockers = append(p.Blockers, compat.Blockers...)
		p.Manifest = redactedManifest(b.man)
		return p, nil
	}
	var err error
	if req.Mode == ModeFull {
		err = b.discoverFull(ctx)
	} else {
		err = b.discoverSelective(ctx)
	}
	if err != nil {
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
	if err = b.planHostDeps(ctx, local); err != nil {
		return nil, err
	}
	b.recordRuntime(ctx, local)
	if blockers, err := relocationAutomationBlockers(ctx, in, local); err != nil {
		return nil, err
	} else {
		p.Blockers = append(p.Blockers, blockers...)
	}
	if err = b.measure(ctx, pr.Add("Measure data", 0)); err != nil {
		return nil, err
	}
	p.Warnings = append(p.Warnings, pr.Snapshot().Warnings...)
	p.Manifest = redactedManifest(b.man)
	return p, nil
}

// redactedManifest copies man without what a backup keeps only inside its
// encrypted archive: the database containers' environment (their
// credentials) and buildx builder definitions. A plan crosses servers in the
// clear, so it carries names, sizes and fingerprints only.
func redactedManifest(man *Manifest) Manifest {
	out := *man
	out.Dumps = make([]DumpEntry, len(man.Dumps))
	for i, d := range man.Dumps {
		d.Env = nil
		out.Dumps[i] = d
	}
	out.Builders = make([]BuilderEntry, len(man.Builders))
	for i, b := range man.Builders {
		out.Builders[i] = BuilderEntry{Name: b.Name}
	}
	return out
}

// CheckPlan checks whether the planned move can land on this server.
func CheckPlan(ctx context.Context, in *coolify.Instance, p *RelocationPlan) (*RelocationCheck, error) {
	if p == nil || p.Format != PlanFormat {
		return nil, errors.New("not a coolify relocation plan (format " + PlanFormat + ")")
	}
	man := &p.Manifest
	c := &RelocationCheck{Format: CheckFormat, Tool: Tool(), CheckedAt: time.Now().UTC(), Mode: man.Mode,
		Destination: SourceInfo{Hostname: in.Hostname, CoolifyVersion: in.Version, Arch: in.Arch},
		PlanSource:  man.Source, Blockers: []string{}, Warnings: []string{}, Conflicts: []ResourceRef{}, DomainClashes: []string{}}
	block := func(s ...string) { c.Blockers = append(c.Blockers, s...) }
	warn := func(s ...string) { c.Warnings = append(c.Warnings, s...) }

	if p.Tool.Version != Version || p.Tool.ForkRevision != ForkRevision {
		block(fmt.Sprintf("the plan was made by coolify-mirror %s (%s) but this server runs %s (%s) - both servers must run the same pinned build",
			p.Tool.Version, p.Tool.ForkRevision, Version, ForkRevision))
	}
	for _, s := range p.Blockers {
		block("source: " + s)
	}
	if b := versionBlocker(in, man); b != "" {
		block(b)
	}
	compat := CheckCompat(ctx, in)
	block(compat.RestoreBlockers()...)
	warn(compat.Warnings...)
	if s := SpaceWarning(in.DockerRoot, man); s != "" {
		block("not enough disk space: " + s)
	}
	block(hostDepBlockers(ctx, man)...)
	if man.Mode == ModeFull {
		content, err := targetContent(ctx, in)
		switch {
		case err != nil:
			block(err.Error())
		case len(content) > 0:
			block("a full move only goes onto a fresh, empty Coolify - this one already has " + strings.Join(content, ", "))
		}
	} else {
		block(hostRequirementBlockers(man)...)
		block(hostDepConflicts(man)...)
	}
	if a := man.Source.Arch; a != "" && in.Arch != "" && a != in.Arch {
		warn(fmt.Sprintf("the source is %s and this server is %s: application images cannot be reused, Coolify will rebuild them", a, in.Arch))
	}

	here, err := in.ListResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list the resources of this Coolify: %w", err)
	}
	byUUID := map[string]bool{}
	served := map[string]bool{}
	for _, r := range here {
		byUUID[r.UUID] = true
		for _, d := range r.Domains {
			served[strings.ToLower(coolify.Host(d))] = true
		}
	}
	for _, r := range man.Resources {
		if !r.Local() {
			continue
		}
		if byUUID[r.UUID] {
			c.Conflicts = append(c.Conflicts, ResourceRef{UUID: r.UUID, Kind: r.Label(), Name: r.Name})
		}
		for _, d := range r.Domains {
			if h := strings.ToLower(coolify.Host(d)); served[h] {
				c.DomainClashes = append(c.DomainClashes, h)
			}
		}
	}
	if len(c.Conflicts) > 0 {
		warn(fmt.Sprintf("%d resource(s) already exist here; the restore asks whether to replace them or import a copy", len(c.Conflicts)))
	}
	if len(c.DomainClashes) > 0 {
		warn("domains already served here: " + strings.Join(c.DomainClashes, ", ") + " - change them during the restore")
	}
	c.Ready = len(c.Blockers) == 0
	return c, nil
}
