package localbackupruntime

import (
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// NewWithAppGroupSnapshots binds native group control at construction, so no
// partially configured runtime can snapshot an uncontrolled group. Repository,
// Kopia policy, source translation and settlement retain their existing owners.
func NewWithAppGroupSnapshots(workspace string, policy localbackuppolicy.Policy, custody backupexec.ApplicationContainerCustodyVerifier, groups AppGroupSnapshotBinding, bindings ...backupexec.ApplicationRuntimeBinding) (*Runtime, error) {
	raw, err := localbackuppolicy.ArtifactBytes(policy)
	if err != nil {
		return nil, err
	}
	held, err := localbackuppolicy.Decode(raw)
	if err != nil {
		return nil, err
	}
	engine, err := backupexec.NewDockerV2EngineForPolicyWithApplicationBindings(held, bindings)
	if err != nil {
		return nil, err
	}
	r, err := newWithEngine(workspace, func() engineAPI { return engine })
	if err != nil {
		return nil, err
	}
	r.policy = &held
	groups.Core, err = backupexec.NewDockerV2CoreQuiescerWithApplicationBindings(held.SourceProjection(), custody, bindings)
	if err != nil {
		return nil, err
	}
	r.snapshotSettler, err = backupexec.NewDockerV2SnapshotSettlerWithApplicationBindings(held.SourceProjection(), bindings)
	if err != nil {
		return nil, err
	}
	if err = r.BindAppGroupSnapshots(groups); err != nil {
		return nil, err
	}
	return r, nil
}
