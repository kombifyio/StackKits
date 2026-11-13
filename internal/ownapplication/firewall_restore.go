package ownapplication

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/ownappfirewall"
)

// ReviewedFirewall returns the firewall receipt of the signed operation record
// that owns network, for the boot-time restore. Only a running firewalled
// application (outbound Internet, published ports, or both) whose last
// reviewed operation succeeded and applied its rules qualifies; a pending,
// stopped, transitioning or ambiguous record refuses. Those refusals are
// permanent: no retry in this boot changes the signed record, so the restore
// unit stops retrying until a reviewed operation enables it again.
func ReviewedFirewall(workspace, network string) (ownappfirewall.Receipt, error) {
	s, err := Load(workspace)
	if err != nil {
		return ownappfirewall.Receipt{}, err
	}
	var found *ownappfirewall.Receipt
	for _, r := range s.Applications {
		b := r.Bundle
		if "stackkit-"+b.Application.WorkloadRef()+"-"+b.Installation.NodeRef+"_private" != network {
			continue
		}
		if found != nil {
			return ownappfirewall.Receipt{}, ownappfirewall.PermanentRefusal{Err: errors.New("own firewall: record_ambiguous: more than one signed record owns this network; restore refused")}
		}
		if !b.Application.Firewalled() || r.TransitionFrom != nil || r.State != "running" || r.Operation.Status != "succeeded" || r.Operation.FirewallAction != "applied" || r.Operation.Firewall == nil {
			return ownappfirewall.Receipt{}, ownappfirewall.PermanentRefusal{Err: errors.New("own firewall: record_not_running: no completed reviewed firewalled start in the signed record; restore refused")}
		}
		receipt := *r.Operation.Firewall
		found = &receipt
	}
	if found == nil {
		return ownappfirewall.Receipt{}, ownappfirewall.PermanentRefusal{Err: errors.New("own firewall: record_missing: no signed record owns this network; restore refused")}
	}
	return *found, nil
}
