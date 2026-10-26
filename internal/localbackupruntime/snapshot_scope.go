package localbackupruntime

import (
	"context"
	"errors"
	"slices"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// NewApplicationSnapshotRuntime uses the existing repository and quiescence
// journal with an explicit signed selection, never a replacement backup owner.
func NewApplicationSnapshotRuntime(workspace string, policy localbackuppolicy.Policy, custody backupexec.ApplicationContainerCustodyVerifier, workloads []string, bindings ...backupexec.ApplicationRuntimeBinding) (*Runtime, error) {
	if len(workloads) == 0 {
		return nil, errors.New("application snapshot requires scope")
	}
	if _, err := policy.ApplicationSnapshotSources(workloads); err != nil {
		return nil, err
	}
	r, err := New(workspace, policy, custody, bindings...)
	if err != nil {
		return nil, err
	}
	e, err := backupexec.NewDockerV2EngineForPolicyWithApplicationBindings(*r.policy, bindings)
	if err != nil {
		return nil, err
	}
	e, err = e.WithApplicationSnapshotScope(workloads)
	if err != nil {
		return nil, err
	}
	r.newEngine = func() engineAPI { return e }
	r.snapshotWorkloads = slices.Clone(workloads)
	r.quiescer = applicationScopeQuiescer{ContainerQuiescer: r.quiescer, workloads: slices.Clone(workloads)}
	return r, nil
}

type applicationScopeQuiescer struct {
	backupexec.ContainerQuiescer
	workloads []string
}

// BindApplicationTransferExport runs only after the selected writers' stopped
// state and completed snapshot were persisted by the existing backup journal.
// Cleanup on failure uses the same exact-container resume and settlement path.
func (r *Runtime) BindApplicationTransferExport(export func(context.Context) error) error {
	if r == nil || len(r.snapshotWorkloads) == 0 || export == nil {
		return errors.New("transfer export requires an application-scoped snapshot runtime")
	}
	r.quiescedSnapshotExport = export
	return nil
}

func (r *Runtime) BindApplicationTransferHold(id func() string) error {
	if r == nil || len(r.snapshotWorkloads) == 0 || id == nil {
		return errors.New("transfer hold requires existing scoped snapshot runtime")
	}
	r.transferHoldID = id
	return nil
}

// BindApplicationTransferResume still requires a signed stopped destination
// outcome in the original snapshot journal before any deferred resume can run.
func (r *Runtime) BindApplicationTransferResume(id string) error {
	if r == nil || len(r.snapshotWorkloads) == 0 || id == "" {
		return errors.New("transfer resume requires original application scope")
	}
	r.transferResumeID = id
	return nil
}
func (q applicationScopeQuiescer) ManagedContainers(ctx context.Context) ([]backupexec.QuiesceContainer, error) {
	all, err := q.ContainerQuiescer.ManagedContainers(ctx)
	if err != nil {
		return nil, err
	}
	var selected []backupexec.QuiesceContainer
	for _, c := range all {
		if slices.Contains(q.workloads, c.WorkloadRef) {
			selected = append(selected, c)
		}
	}
	return selected, nil
}

// BindApplicationTransferClosure keeps data sources scoped to persistent owners
// while including stateless companions in the same signed writer control journal.
func (r *Runtime) BindApplicationTransferClosure(closure []localbackuppolicy.ApplicationRuntime, custody backupexec.ApplicationContainerCustodyVerifier) error {
	canonical, err := localbackuppolicy.CanonicalApplicationTransferRuntimes(closure)
	if err != nil {
		return err
	}
	quiescer, err := backupexec.NewDockerV2TransferQuiescer(r.sourceProjection(), canonical, custody)
	if err != nil {
		return err
	}
	r.transferRuntimes = canonical
	r.quiescer = quiescer
	return nil
}
