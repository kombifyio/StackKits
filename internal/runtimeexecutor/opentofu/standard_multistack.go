package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	standardApplyCheckpointFile = ".stackkit/runtime/.standard-apply-checkpoint.json"
	checkpointSchemaVersion     = "stackkit.standard-apply-checkpoint/v1"
)

var (
	// ErrCheckpointPlanHashMismatch refuses resuming an interrupted Standard
	// Mode multi-root apply when the ResolvedPlan hash changed between attempts.
	ErrCheckpointPlanHashMismatch = errors.New("interrupted apply checkpoint planHash does not match current ResolvedPlan")
)

// CanonicalRootEntry identifies one discovered canonical OpenTofu root under
// `.stackkit/runtime/<core|applications/*|modules/*>/opentofu` (layout.go:24).
type CanonicalRootEntry struct {
	Order        int    `json:"order"` // 0 = core, 1 = applications, 2 = modules
	Kind         string `json:"kind"`  // "", "workload", "module"
	Name         string `json:"name"`
	RelativeRoot string `json:"relativeRoot"`
}

// StandardApplyCheckpoint persists idempotent interrupted-apply resume state
// for sequential Standard Mode multi-root applies (zero Terramate).
type StandardApplyCheckpoint struct {
	SchemaVersion  string    `json:"schemaVersion"`
	PlanHash       string    `json:"planHash"`
	LeaseGen       string    `json:"leaseGeneration"`
	CompletedRoots []string  `json:"completedRoots"`
	InProgressRoot string    `json:"inProgressRoot,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// DiscoverCanonicalRoots enumerates every OpenTofu root under `.stackkit/runtime`
// in deterministic Standard Mode order: Core roots first, then `applications/*`,
// then `modules/*`. Zero Terramate files or binaries are required.
func DiscoverCanonicalRoots(workspace string) ([]CanonicalRootEntry, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	runtimeDir := filepath.Join(absWorkspace, ".stackkit", "runtime")
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var roots []CanonicalRootEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch name {
		case ApplicationsDir:
			apps, err := os.ReadDir(filepath.Join(runtimeDir, ApplicationsDir))
			if err != nil {
				return nil, err
			}
			for _, app := range apps {
				if !app.IsDir() {
					continue
				}
				rel, err := WorkloadRootRelativePath(app.Name())
				if err != nil {
					continue
				}
				if hasRootMainTF(absWorkspace, rel) {
					roots = append(roots, CanonicalRootEntry{
						Order: 1, Kind: RootKindWorkload, Name: app.Name(), RelativeRoot: rel,
					})
				}
			}
		case ModulesDir:
			mods, err := os.ReadDir(filepath.Join(runtimeDir, ModulesDir))
			if err != nil {
				return nil, err
			}
			for _, mod := range mods {
				if !mod.IsDir() {
					continue
				}
				rel, err := ModuleRootRelativePath(mod.Name())
				if err != nil {
					continue
				}
				if hasRootMainTF(absWorkspace, rel) {
					roots = append(roots, CanonicalRootEntry{
						Order: 2, Kind: RootKindModule, Name: mod.Name(), RelativeRoot: rel,
					})
				}
			}
		default:
			rel, err := RootRelativePath(name)
			if err != nil {
				continue
			}
			if hasRootMainTF(absWorkspace, rel) {
				roots = append(roots, CanonicalRootEntry{
					Order: 0, Kind: "", Name: name, RelativeRoot: rel,
				})
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].Order != roots[j].Order {
			return roots[i].Order < roots[j].Order
		}
		return roots[i].RelativeRoot < roots[j].RelativeRoot
	})
	return roots, nil
}

// RunStandardSequentialApply executes every discovered canonical OpenTofu root
// sequentially in Standard Mode (zero Terramate, CONSTITUTION.md rule 18) under
// the unified node action_lease, journaling progress to resume cleanly after an
// interrupted apply once PlanHash is re-verified.
func RunStandardSequentialApply(
	ctx context.Context,
	workspace string,
	planHash string,
	actor LeaseActor,
	applyRoot func(ctx context.Context, root CanonicalRootEntry) error,
) ([]CanonicalRootEntry, error) {
	if strings.TrimSpace(planHash) == "" {
		return nil, errors.New("standard sequential apply requires a non-empty planHash")
	}
	if applyRoot == nil {
		return nil, errors.New("standard sequential apply requires an applyRoot callback")
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	lease, err := AcquireNodeLease(absWorkspace, actor, "standard-sequential-apply")
	if err != nil {
		return nil, err
	}
	defer func() { _ = lease.Release() }()

	// Fail closed if retired legacy PaaS workloads are present.
	if legacy, err := DetectLegacyPaaSWorkloads(absWorkspace); err != nil {
		return nil, err
	} else if len(legacy) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrLegacyPaaSDetected, FormatLegacyPaaSFindings(legacy))
	}

	roots, err := DiscoverCanonicalRoots(absWorkspace)
	if err != nil {
		return nil, err
	}
	cpPath := filepath.Join(absWorkspace, filepath.FromSlash(standardApplyCheckpointFile))
	completed := make(map[string]bool)
	if prev, err := ReadStandardApplyCheckpoint(absWorkspace); err == nil {
		if prev.PlanHash != planHash {
			return nil, fmt.Errorf("%w: checkpoint has %s, current plan has %s", ErrCheckpointPlanHashMismatch, prev.PlanHash, planHash)
		}
		for _, r := range prev.CompletedRoots {
			completed[r] = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read standard apply checkpoint: %w", err)
	}

	var executed []CanonicalRootEntry
	for _, root := range roots {
		if completed[root.RelativeRoot] {
			continue
		}
		if err := lease.VerifyGeneration(); err != nil {
			return executed, err
		}
		cp := StandardApplyCheckpoint{
			SchemaVersion:  checkpointSchemaVersion,
			PlanHash:       planHash,
			LeaseGen:       lease.Generation(),
			CompletedRoots: sortedMapKeys(completed),
			InProgressRoot: root.RelativeRoot,
			UpdatedAt:      time.Now().UTC(),
		}
		if err := writeStandardApplyCheckpoint(cpPath, cp); err != nil {
			return executed, err
		}
		if err := applyRoot(ctx, root); err != nil {
			return executed, fmt.Errorf("standard sequential apply failed at %s (resumable from checkpoint): %w", root.RelativeRoot, err)
		}
		completed[root.RelativeRoot] = true
		executed = append(executed, root)
		cp.CompletedRoots = sortedMapKeys(completed)
		cp.InProgressRoot = ""
		cp.UpdatedAt = time.Now().UTC()
		if err := writeStandardApplyCheckpoint(cpPath, cp); err != nil {
			return executed, err
		}
	}
	// All roots converged cleanly: remove checkpoint.
	_ = os.Remove(cpPath)
	return executed, nil
}

// ReadStandardApplyCheckpoint reads an interrupted Standard Mode apply journal.
func ReadStandardApplyCheckpoint(workspace string) (StandardApplyCheckpoint, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return StandardApplyCheckpoint{}, err
	}
	raw, err := os.ReadFile(filepath.Join(absWorkspace, filepath.FromSlash(standardApplyCheckpointFile)))
	if err != nil {
		return StandardApplyCheckpoint{}, err
	}
	var cp StandardApplyCheckpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		return StandardApplyCheckpoint{}, err
	}
	return cp, nil
}

func writeStandardApplyCheckpoint(path string, cp StandardApplyCheckpoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Dir(path), filepath.Base(path), append(encoded, '\n'), 0o600)
}

func hasRootMainTF(workspace, relativeRoot string) bool {
	info, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(relativeRoot), ConfigFile))
	return err == nil && !info.IsDir()
}

func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
