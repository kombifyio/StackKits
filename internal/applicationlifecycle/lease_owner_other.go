//go:build !linux

package applicationlifecycle

func currentLeaseOwner() (leaseOwner, bool) { return leaseOwner{}, false }

func reapLeaseOwnerOrphans(leaseOwner) string {
	return "child processes of the owner were not inspected on this platform; effects of the old operation may still be in progress"
}
