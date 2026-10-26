package applicationlifecycle

import (
	"encoding/json"
	"errors"
	"path"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// leaseOwner identifies the process that holds an operation lease. Liveness is
// proven solely by the kernel lock. Orphan reaping requires the exact Token in
// a target's environment; the numeric fields are diagnostics only.
type leaseOwner struct {
	PID        int    `json:"pid"`
	PGID       int    `json:"pgid"`
	SID        int    `json:"sid"`
	StartTicks uint64 `json:"startTicks"`
	BootID     string `json:"bootId"`
	// Token is the random owner token its lifecycle children inherit.
	Token string `json:"token,omitempty"`
}

const leaseOwnerFile = "owner.json"

func leaseOwnerPath(id string) string { return path.Join(leaseMarker(id), leaseOwnerFile) }

// recordLeaseOwner replaces the marker's owner record with this process.
func recordLeaseOwner(transaction *confinedfs.Transaction, id string) error {
	owner, ok := currentLeaseOwner()
	if !ok {
		return nil
	}
	raw, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	if exists, info, err := transaction.Exists(leaseOwnerPath(id)); err != nil {
		return err
	} else if exists {
		if err := transaction.RemoveRegularFile(leaseOwnerPath(id), info); err != nil {
			return err
		}
	}
	return transaction.WriteFileExclusive(leaseOwnerPath(id), raw, 0o600)
}

// stopOrphansOfDeadOwner stops child processes that outlived the dead owner
// and describes what was done for the interruption record.
func stopOrphansOfDeadOwner(transaction *confinedfs.Transaction, id string) string {
	raw, _, err := transaction.ReadStableBounded(leaseOwnerPath(id), 4096)
	if err != nil {
		return "the owner record is unavailable, so no child process was stopped; effects of the old operation may still be in progress"
	}
	var owner leaseOwner
	if err := json.Unmarshal(raw, &owner); err != nil || owner.PID <= 1 {
		return "the owner record is invalid, so no child process was stopped; effects of the old operation may still be in progress"
	}
	return reapLeaseOwnerOrphans(owner)
}

var errLeaseHeld = errors.New("application lifecycle operation is held by a live process")
