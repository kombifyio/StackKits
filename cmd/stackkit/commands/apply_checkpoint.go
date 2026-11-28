package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/kombifyio/stackkits/internal/runtimeexecutordispatch"
)

const applyCheckpointPath = ".stackkit/runtime/apply-checkpoint.json"

type applyResumeReport struct {
	Resumed      bool     `json:"resumed"`
	SkippedRoots []string `json:"skippedRoots"`
}
type applyCheckpoint struct {
	PlanHash                 string   `json:"planHash"`
	RequestDigest            string   `json:"requestDigest"`
	CompletedRoots           []string `json:"completedRoots"`
	workspace, node          string
	roots, previous, skipped []string
	tx                       *confinedfs.Transaction
}

func openApplyCheckpoint(workspace string, tx *confinedfs.Transaction, hash string, roots []string, node string) (*applyCheckpoint, error) {
	cp := &applyCheckpoint{PlanHash: hash, workspace: workspace, node: node, roots: roots, tx: tx}
	raw, _, err := tx.ReadStableBounded(applyCheckpointPath, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return cp, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, cp); err != nil {
		return nil, err
	}
	if cp.PlanHash != hash {
		return nil, opentofu.ErrCheckpointPlanHashMismatch
	}
	for _, root := range cp.CompletedRoots {
		if !slices.Contains(roots, root) {
			return nil, fmt.Errorf("checkpoint contains unselected root %q", root)
		}
	}
	cp.previous = slices.Clone(cp.CompletedRoots)
	return cp, nil
}
func (cp *applyCheckpoint) context(ctx context.Context) context.Context {
	observer := runtimeexecutordispatch.JournalObserver{Completed: cp.committed}
	if len(cp.previous) > 0 {
		observer.MayRetry = func(target runtimeexecutor.RuntimeTarget) bool {
			root := cp.targetRoot(target)
			return root == "" || !slices.Contains(cp.previous, root)
		}
		observer.BeforeExecute = func(target runtimeexecutor.RuntimeTarget) error {
			if slices.Contains(cp.previous, cp.targetRoot(target)) {
				return fmt.Errorf("completed root lacks verified journal replay; refusing to reapply")
			}
			return nil
		}
	}
	return runtimeexecutordispatch.WithJournalObserver(ctx, observer)
}
func (cp *applyCheckpoint) targetRoot(target runtimeexecutor.RuntimeTarget) string {
	var root string
	if target.RuntimeAdapter != nil {
		root, _ = opentofu.WorkloadRootRelativePath("stackkit-" + target.WorkloadRef + "-" + cp.node)
	} else {
		root, _ = opentofu.ModuleRootRelativePath(target.ModuleRef)
		for _, binding := range opentofu.DefaultModuleBindings() {
			if binding.ModuleRef == target.ModuleRef {
				root, _ = opentofu.RootRelativePath(binding.RuntimeDir)
				break
			}
		}
	}
	return root
}
func (cp *applyCheckpoint) committed(digest string, target runtimeexecutor.RuntimeTarget, replayed bool) error {
	root := cp.targetRoot(target)
	if !slices.Contains(cp.roots, root) {
		return nil
	}
	if replayed && slices.Contains(cp.previous, root) && !slices.Contains(cp.skipped, root) {
		cp.skipped = append(cp.skipped, root)
	}
	if slices.Contains(cp.CompletedRoots, root) {
		return nil
	}
	cp.CompletedRoots = append(cp.CompletedRoots, root)
	cp.RequestDigest = digest
	slices.Sort(cp.CompletedRoots)
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	if err = cp.tx.MkdirAll(".stackkit/runtime", 0700); err != nil {
		return err
	}
	fs, err := confinedfs.Open(cp.workspace)
	if err != nil {
		return err
	}
	defer fs.Close()
	view, err := fs.View(".")
	if err != nil {
		return err
	}
	if _, err = view.WriteAtomic0600(applyCheckpointPath, data); err != nil {
		return err
	}
	rolloutEvent("apply.root", "completed", "OpenTofu root completed", map[string]string{"root": root})
	return nil
}
func (cp *applyCheckpoint) resumeReport() *applyResumeReport {
	if len(cp.skipped) == 0 {
		return nil
	}
	slices.Sort(cp.skipped)
	return &applyResumeReport{true, cp.skipped}
}
func (cp *applyCheckpoint) finish(afterApply func(func() error)) error {
	if len(cp.CompletedRoots) == 0 {
		return nil
	}
	_, info, err := cp.tx.ReadStable(applyCheckpointPath)
	if err != nil {
		return err
	}
	if afterApply == nil {
		return cp.tx.RemoveRegularFile(applyCheckpointPath, info)
	}
	workspace := cp.workspace
	afterApply(func() error {
		fs, err := confinedfs.Open(workspace)
		if err != nil {
			return err
		}
		defer fs.Close()
		tx, err := fs.BeginTransaction()
		if err != nil {
			return err
		}
		defer tx.Close()
		return tx.RemoveRegularFile(applyCheckpointPath, info)
	})
	return nil
}
