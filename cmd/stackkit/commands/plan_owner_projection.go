package commands

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// attachPlanLocalOwner uses the same verified custody Advanced change-set
// admission reads. Missing signing capability is a read fact; malformed or
// substituted custody is an error, never a fabricated execution binding.
func attachPlanLocalOwner(workspace string, plan generationartifact.VerifiedPlan, inspection *generationartifact.PlanInspection) error {
	if inspection == nil || plan.Binding().PlanHash == "" || inspection.Binding.PlanHash != plan.Binding().PlanHash {
		return errors.New("local Owner inspection requires the verified generation Plan")
	}
	var identity struct {
		StackID string `json:"stackId"`
		Nodes   []struct {
			ID      string `json:"id"`
			SiteRef string `json:"siteRef"`
		} `json:"nodes"`
	}
	if json.Unmarshal(plan.Canonical(), &identity) != nil || identity.StackID == "" {
		return errors.New("local Owner inspection has no verified StackInstance")
	}
	projection := &generationartifact.PlanInspectionLocalOwner{
		State: "unavailable", StackID: identity.StackID, PlanHash: plan.Binding().PlanHash,
	}
	inspection.LocalOwner = projection
	owner, err := localevidence.LoadOwnerCustody(workspace)
	var memberOnly *localevidence.MemberSigningDenial
	if errors.As(err, &memberOnly) {
		projection.ReasonCode = "member_custody_only"
		return nil
	}
	if errors.Is(err, localevidence.ErrOwnerCustodyMissing) {
		projection.ReasonCode = "owner_custody_missing"
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify local Owner for Plan inspection: %w", err)
	}
	matched := false
	for _, node := range identity.Nodes {
		if node.ID == owner.Binding.NodeRef && node.SiteRef == owner.Binding.SiteRef {
			if matched {
				return errors.New("local Owner inspection has an ambiguous Plan node")
			}
			matched = true
		}
	}
	if !matched || owner.OwnerRef == "" || owner.KeyID == "" || owner.Binding.ChannelRef == "" {
		return errors.New("verified local Owner binding does not name an exact inspected Plan host")
	}
	projection.State, projection.OwnerRef, projection.KeyID = "verified", owner.OwnerRef, owner.KeyID
	projection.Binding = &generationartifact.ApplyExecutionScopeBinding{
		SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, ExecutionChannelRef: owner.Binding.ChannelRef,
	}
	return nil
}
