package homegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// Wire schema ids of the node adoption (HOME-GRAPH-TARGET.md sections 6 and
// 8): adopting an existing Home Assistant and changing the leading system
// later run the same inventory, plan and confirmation.
const (
	AdoptionRequestSchema = "stackkit.home-graph-adoption-request/v1"
	AdoptionResultSchema  = "stackkit.home-graph-adoption-result/v1"
	// RegistryBackupSchema identifies the Home Assistant registry backup the
	// node writes before an adoption is applied.
	RegistryBackupSchema = "kombify.home_graph.ha_registry_backup.v1"

	// AdoptionInventory reads Home Assistant and returns the plan; it never
	// writes to Home Assistant. AdoptionApply applies a confirmed plan.
	AdoptionInventory = "inventory"
	AdoptionApply     = "apply"

	// StatusPlanned answers an inventory; StatusMirrored answers an apply
	// while Home Assistant leads after the confirmed decisions converge.
	StatusPlanned  = "planned"
	StatusMirrored = "mirrored"

	// Adoption record states in the stored mapping.
	AdoptionPending   = "pending"
	AdoptionConfirmed = "confirmed"

	maxAdoptionDecisions = 4096
	maxAdoptionList      = 1024
)

var rePlanDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ErrAdoptionStale means Home Assistant or the graph changed since the
// inventory the person confirmed; the inventory has to run again.
var ErrAdoptionStale = errors.New("homegraph: the adoption plan changed since the inventory")

// ErrAdoptionUndecided means a conflict of the plan carries no decision and
// no side was chosen for all conflicts.
var ErrAdoptionUndecided = errors.New("homegraph: a conflict of the adoption plan has no decision")

// AdoptionRecord is the adoption state the node keeps in its mapping. A
// pending record (an inventory of a Home Assistant kombify never wrote to)
// keeps every plain sync from writing until the plan is confirmed; a confirmed
// record names the leading system the person chose, so a later leader change
// goes through the same plan.
type AdoptionRecord struct {
	State      string `json:"state"`
	Leading    string `json:"leading_system"`
	PlanDigest string `json:"plan_digest"`
	At         string `json:"at"`
	Backup     string `json:"backup,omitempty"`
}

// SyncBlocked reports whether a plain sync with the given leading system must
// not run: an adoption is pending, or the leading system differs from the one
// the person confirmed (a leader change runs through the adoption plan).
func (m Mapping) SyncBlocked(leading string) bool {
	if m.Adoption == nil {
		return false
	}
	return m.Adoption.State != AdoptionConfirmed || m.Adoption.Leading != leading
}

// AdoptionDecision is the person's choice for one conflict.
type AdoptionDecision struct {
	ConflictID string `json:"conflictId"`
	Side       Side   `json:"side"`
}

// AdoptionRequest is the document Cloud hands to the node through Techstack.
// The graph's leading_system is the leading system the person chose (kombify
// is preselected). Apply carries the planDigest of the inventory the person
// confirmed and a decision for every conflict, or decideAll.
type AdoptionRequest struct {
	SchemaVersion string             `json:"schemaVersion"`
	Mode          string             `json:"mode"`
	Graph         Graph              `json:"graph"`
	PlanDigest    string             `json:"planDigest,omitempty"`
	DecideAll     Side               `json:"decideAll,omitempty"`
	Decisions     []AdoptionDecision `json:"decisions,omitempty"`
}

// Validate checks the schema id, the mode, the graph and the decisions.
func (r AdoptionRequest) Validate() error {
	if r.SchemaVersion != AdoptionRequestSchema {
		return fmt.Errorf("schemaVersion must be %q", AdoptionRequestSchema)
	}
	if err := r.Graph.Validate(); err != nil {
		return err
	}
	switch r.Mode {
	case AdoptionInventory:
		if r.PlanDigest != "" || r.DecideAll != "" || len(r.Decisions) > 0 {
			return errors.New("an inventory carries no planDigest, decideAll or decisions")
		}
		return nil
	case AdoptionApply:
	default:
		return fmt.Errorf("mode must be %q or %q", AdoptionInventory, AdoptionApply)
	}
	if !rePlanDigest.MatchString(r.PlanDigest) {
		return errors.New("planDigest must be the sha256 digest of the confirmed inventory")
	}
	if r.DecideAll != "" && r.DecideAll != SideKombify && r.DecideAll != SideHA {
		return fmt.Errorf("decideAll %q", r.DecideAll)
	}
	if len(r.Decisions) > maxAdoptionDecisions {
		return fmt.Errorf("decisions: at most %d items", maxAdoptionDecisions)
	}
	seen := map[string]bool{}
	for _, d := range r.Decisions {
		if d.ConflictID == "" || len(d.ConflictID) > 512 {
			return errors.New("decisions: conflictId is invalid")
		}
		if d.Side != SideKombify && d.Side != SideHA {
			return fmt.Errorf("decisions: side %q", d.Side)
		}
		if seen[d.ConflictID] {
			return errors.New("decisions: duplicate conflictId")
		}
		seen[d.ConflictID] = true
	}
	return nil
}

// AdoptionItem is one matched or one-sided object of the plan.
type AdoptionItem struct {
	Kind      string `json:"kind"`
	KombifyID string `json:"kombifyId"`
	HAID      string `json:"haId"`
	Name      string `json:"name"`
}

// AdoptionConflict is one conflict the person decides.
type AdoptionConflict struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Subject    string   `json:"subject"`
	KombifyID  string   `json:"kombifyId"`
	HAID       string   `json:"haId"`
	Name       string   `json:"name"`
	Kombify    string   `json:"kombify"`
	HA         string   `json:"ha"`
	Candidates []string `json:"candidates"`
	DeletedOn  string   `json:"deletedOn"`
}

// AdoptionBackup names the Home Assistant registry backup written before an
// apply. File is workspace-relative.
type AdoptionBackup struct {
	File    string `json:"file"`
	TakenAt string `json:"takenAt"`
	Floors  int    `json:"floors"`
	Areas   int    `json:"areas"`
	Labels  int    `json:"labels"`
	Devices int    `json:"devices"`
}

// RegistryBackup is the stored Home Assistant registry backup document.
type RegistryBackup struct {
	Schema  string     `json:"schema"`
	TakenAt string     `json:"taken_at"`
	Floors  []HAFloor  `json:"floors"`
	Areas   []HAArea   `json:"areas"`
	Labels  []HALabel  `json:"labels"`
	Devices []HADevice `json:"devices"`
}

// NewRegistryBackup wraps a snapshot as a backup document.
func NewRegistryBackup(snap HASnapshot, now time.Time) RegistryBackup {
	return RegistryBackup{Schema: RegistryBackupSchema, TakenAt: now.UTC().Format(time.RFC3339),
		Floors: nonNil(snap.Floors), Areas: nonNil(snap.Areas), Labels: nonNil(snap.Labels), Devices: nonNil(snap.Devices)}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// AdoptionResult is the document the node returns. An inventory carries the
// plan only; an apply also carries the merged graph Cloud stores, the links,
// the drift and the backup.
type AdoptionResult struct {
	SchemaVersion string             `json:"schemaVersion"`
	Mode          string             `json:"mode"`
	Status        string             `json:"status"`
	Revision      int64              `json:"revision"`
	LeadingSystem string             `json:"leadingSystem"`
	FirstRun      bool               `json:"firstRun"`
	PlanDigest    string             `json:"planDigest"`
	Matched       []AdoptionItem     `json:"matched"`
	OnlyKombify   []AdoptionItem     `json:"onlyKombify"`
	OnlyHA        []AdoptionItem     `json:"onlyHomeAssistant"`
	Conflicts     []AdoptionConflict `json:"conflicts"`
	// Truncated is set when a list was cut at its maximum; decideAll then
	// decides the conflicts not listed.
	Truncated  bool            `json:"truncated"`
	Graph      *Graph          `json:"graph"`
	Operations int             `json:"operations"`
	Links      SyncLinks       `json:"links"`
	Drift      []SyncDrift     `json:"drift"`
	Backup     *AdoptionBackup `json:"backup"`
	Inventory  SyncInventory   `json:"inventory"`
	SyncedAt   string          `json:"syncedAt"`
}

// Digest is the content address of the plan the person sees: matches,
// one-sided objects and conflicts with both sides' values. The leading system
// and the graph revision are not part of it, so choosing the leader after the
// inventory does not make the plan stale.
func (ap AdoptionPlan) Digest() string {
	matched := append([]Match(nil), ap.Matched...)
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Kind != matched[j].Kind {
			return matched[i].Kind < matched[j].Kind
		}
		return matched[i].KombifyID < matched[j].KombifyID
	})
	sortOnly := func(in []Only) []Only {
		out := append([]Only(nil), in...)
		sort.Slice(out, func(i, j int) bool {
			if out[i].Kind != out[j].Kind {
				return out[i].Kind < out[j].Kind
			}
			return out[i].ID < out[j].ID
		})
		return out
	}
	conflicts := append([]Conflict(nil), ap.Conflicts...)
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].ID != conflicts[j].ID {
			return conflicts[i].ID < conflicts[j].ID
		}
		return conflicts[i].HAID < conflicts[j].HAID
	})
	graph := ap.Graph
	graph.Revision, graph.LeadingSystem = 0, ""
	snapshot := ap.Snapshot
	snapshot.Floors = append([]HAFloor{}, snapshot.Floors...)
	snapshot.Areas = append([]HAArea{}, snapshot.Areas...)
	snapshot.Devices = append([]HADevice{}, snapshot.Devices...)
	snapshot.Labels = append([]HALabel{}, snapshot.Labels...)
	sort.Slice(snapshot.Floors, func(i, j int) bool { return snapshot.Floors[i].FloorID < snapshot.Floors[j].FloorID })
	sort.Slice(snapshot.Areas, func(i, j int) bool { return snapshot.Areas[i].AreaID < snapshot.Areas[j].AreaID })
	sort.Slice(snapshot.Devices, func(i, j int) bool { return snapshot.Devices[i].ID < snapshot.Devices[j].ID })
	sort.Slice(snapshot.Labels, func(i, j int) bool { return snapshot.Labels[i].LabelID < snapshot.Labels[j].LabelID })
	raw, _ := json.Marshal(struct {
		Graph       Graph
		Snapshot    HASnapshot
		FirstRun    bool
		Matched     []Match
		OnlyKombify []Only
		OnlyHA      []Only
		Conflicts   []Conflict
	}{graph, snapshot, ap.FirstRun, matched, sortOnly(ap.OnlyKombify), sortOnly(ap.OnlyHA), conflicts})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IsFirstAdoption reports whether kombify has never linked or adopted
// anything in this Home Assistant.
func (m Mapping) IsFirstAdoption() bool {
	return (m.Adoption == nil || m.Adoption.State == AdoptionPending) && len(m.Floors) == 0 && len(m.Areas) == 0 && len(m.Devices) == 0
}

// RunAdoption runs one adoption step against Home Assistant.
//
// Inventory reads the registries, plans the adoption and writes nothing to
// Home Assistant; on a first adoption the returned mapping carries a pending
// record so plain syncs stay observed until the plan is confirmed.
//
// Apply re-plans, refuses with ErrAdoptionStale when the plan differs from the
// confirmed digest and with ErrAdoptionUndecided when a conflict has no
// decision, calls backup with the registry snapshot before any write, resolves
// the decisions and applies their projection before mirroring the chosen
// leader. Nothing is ever deleted in Home Assistant.
// On an apply error the returned mapping holds the progress made.
func RunAdoption(ctx context.Context, reg Registry, req AdoptionRequest, m Mapping, now time.Time, backup func(HASnapshot) (AdoptionBackup, error)) (AdoptionResult, Mapping, error) {
	m = m.Clone()
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		return AdoptionResult{}, m, err
	}
	firstRun := m.IsFirstAdoption()
	ap := PlanAdoption(req.Graph, snap, m, firstRun)
	ap.Leading = req.Graph.LeadingSystem
	digest := ap.Digest()
	at := now.UTC().Format(time.RFC3339)

	result := AdoptionResult{SchemaVersion: AdoptionResultSchema, Mode: req.Mode, Status: StatusPlanned, Revision: req.Graph.Revision,
		LeadingSystem: req.Graph.LeadingSystem, FirstRun: firstRun, PlanDigest: digest, Drift: []SyncDrift{}, SyncedAt: at}
	result.fillPlan(ap)

	if req.Mode == AdoptionInventory {
		next := m
		if firstRun {
			next.Adoption = &AdoptionRecord{State: AdoptionPending, Leading: req.Graph.LeadingSystem, PlanDigest: digest, At: at}
		}
		result.Links = SyncLinks{Floors: cloneMap(next.Floors), Areas: cloneMap(next.Areas), Devices: cloneMap(next.Devices)}
		result.Inventory = inventoryOf(snap, next)
		return result, next, nil
	}

	if req.PlanDigest != digest {
		return AdoptionResult{}, m, ErrAdoptionStale
	}
	decisions := map[string]Side{}
	for _, d := range req.Decisions {
		decisions[d.ConflictID] = d.Side
	}
	known := map[string]bool{}
	for _, c := range ap.Conflicts {
		known[c.ID] = true
		if _, ok := decisions[c.ID]; !ok {
			if req.DecideAll == "" {
				return AdoptionResult{}, m, ErrAdoptionUndecided
			}
			decisions[c.ID] = req.DecideAll
		}
	}
	for id := range decisions {
		if !known[id] {
			return AdoptionResult{}, m, ErrAdoptionStale
		}
	}

	merged, projection, err := Resolve(ap, decisions)
	if err != nil {
		return AdoptionResult{}, m, err
	}
	ref, err := backup(snap)
	if err != nil {
		return AdoptionResult{}, m, fmt.Errorf("homegraph: registry backup: %w", err)
	}
	result.Backup = &ref

	result.Status = StatusApplied
	next := projection.Mapping
	if len(projection.Ops) > 0 {
		applied, skipped, applyErr := ApplyPlan(ctx, reg, projection)
		if applyErr != nil {
			return AdoptionResult{}, applied, applyErr
		}
		next = applied
		result.Status = StatusApplied
		result.Operations = len(projection.Ops)
		for _, d := range append(append([]Drift{}, projection.Drift...), skipped...) {
			if len(result.Drift) == maxDrift {
				break
			}
			result.Drift = append(result.Drift, SyncDrift{Kind: d.Kind, Field: d.Field, KombifyID: d.KombifyID, HAID: clip(d.HAID), Graph: clip(d.Graph), HA: clip(d.HA)})
		}
		if result.Operations > 0 {
			if after, err := reg.Snapshot(ctx); err == nil {
				snap = after
			}
		}
	}
	if merged.LeadingSystem == LeadingHomeAssistant {
		result.Status = StatusMirrored
	}
	next.normalize()
	next.Adoption = &AdoptionRecord{State: AdoptionConfirmed, Leading: merged.LeadingSystem, PlanDigest: digest, At: at, Backup: ref.File}
	result.LeadingSystem = merged.LeadingSystem
	result.Graph = &merged
	result.Links = SyncLinks{Floors: cloneMap(next.Floors), Areas: cloneMap(next.Areas), Devices: cloneMap(next.Devices)}
	result.Inventory = inventoryOf(snap, next)
	return result, next, nil
}

func (r *AdoptionResult) fillPlan(ap AdoptionPlan) {
	r.Matched, r.OnlyKombify, r.OnlyHA, r.Conflicts = []AdoptionItem{}, []AdoptionItem{}, []AdoptionItem{}, []AdoptionConflict{}
	for _, m := range ap.Matched {
		if len(r.Matched) == maxAdoptionList {
			r.Truncated = true
			break
		}
		r.Matched = append(r.Matched, AdoptionItem{Kind: m.Kind, KombifyID: m.KombifyID, HAID: clip(m.HAID), Name: clip(m.Name)})
	}
	for _, o := range ap.OnlyKombify {
		if len(r.OnlyKombify) == maxAdoptionList {
			r.Truncated = true
			break
		}
		r.OnlyKombify = append(r.OnlyKombify, AdoptionItem{Kind: o.Kind, KombifyID: o.ID, Name: clip(o.Name)})
	}
	for _, o := range ap.OnlyHA {
		if len(r.OnlyHA) == maxAdoptionList {
			r.Truncated = true
			break
		}
		r.OnlyHA = append(r.OnlyHA, AdoptionItem{Kind: o.Kind, HAID: clip(o.ID), Name: clip(o.Name)})
	}
	for _, c := range ap.Conflicts {
		if len(r.Conflicts) == maxAdoptionList {
			r.Truncated = true
			break
		}
		cands := make([]string, 0, len(c.Candidates))
		for _, cand := range c.Candidates {
			cands = append(cands, clip(cand))
		}
		r.Conflicts = append(r.Conflicts, AdoptionConflict{ID: c.ID, Kind: string(c.Kind), Subject: c.Subject, KombifyID: c.KombifyID, HAID: clip(c.HAID),
			Name: clip(c.Name), Kombify: clip(c.Kombify), HA: clip(c.HA), Candidates: cands, DeletedOn: string(c.DeletedOn)})
	}
}
