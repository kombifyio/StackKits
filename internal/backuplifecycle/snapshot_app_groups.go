package backuplifecycle

import "errors"

func validateSnapshotAppGroups(q SnapshotQuiescence) error {
	if len(q.AppGroups) == 0 || q.GraphDigest == "" {
		return errors.New("v2 snapshot lacks bound app-group graph")
	}
	if err := ValidateAppGroups(q.AppGroups); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, c := range q.Controls {
		ids[c.ContainerID] = true
	}
	for _, g := range q.AppGroups {
		for _, c := range g.Original.Containers {
			if ids[c.ID] {
				return errors.New("app-group identity overlaps another control")
			}
			ids[c.ID] = true
			found := false
			for _, original := range q.Containers {
				if AppGroupDigest(original) == AppGroupDigest(c) {
					found = true
				}
			}
			if !found {
				return errors.New("app-group original is outside snapshot anchor")
			}
		}
		if (q.Phase == "stopped" || q.Phase == "snapshot-created") && !AppGroupStopped(g) {
			return errors.New("snapshot requires verified app-group stop/absence")
		}
		if q.Phase == "restored" && !AppGroupRestored(g) {
			return ErrControlUnresolved
		}
	}
	if len(ids) != len(q.Containers) {
		return errors.New("v2 snapshot control coverage differs from exact original containers")
	}
	return nil
}
