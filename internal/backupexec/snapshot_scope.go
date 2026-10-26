package backupexec

import "errors"

// WithApplicationSnapshotScope retains the held policy/mount authority and
// narrows the exact create selection. It never authorizes a foreign volume.
func (e V2Engine) WithApplicationSnapshotScope(workloads []string) (V2Engine, error) {
	if e.policy == nil || len(workloads) == 0 {
		return V2Engine{}, errors.New("snapshot scope requires held policy and selected workloads")
	}
	sources, err := e.policy.ApplicationSnapshotSources(workloads)
	if err != nil {
		return V2Engine{}, err
	}
	e.sources = sources
	return e, nil
}
