package commands

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/kombifyio/stackkits/internal/capacityfit"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// The caller invokes this only after the generation inspection has passed.
// Advisory output reuses the exact retained plan, not a second resolution or
// current host read that would differ from the displayed inventory binding.
func writeArchitectureV2PlanCapacity(writer io.Writer, verified generationartifact.VerifiedPlan) error {
	plan, err := resolvedplan.DecodeCanonicalPlan(verified.Canonical())
	if err != nil {
		return err
	}
	assessment, err := resolvedplan.AssessCapacity(plan)
	if err != nil {
		return err
	}
	return writePlanCapacityAssessment(writer, assessment)
}

func writePlanCapacityAssessment(writer io.Writer, assessment capacityfit.Assessment) error {
	if _, err := fmt.Fprintf(writer, "\nCapacity: %s (advisory)\n? = not declared or not observed\n", assessment.Overall); err != nil {
		return err
	}
	for _, node := range assessment.Nodes {
		if _, err := fmt.Fprintf(writer, "\nNode: %s | capacity: %s | verdict: %s\n", node.NodeRef, node.Origin, node.Overall); err != nil {
			return err
		}
		table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(table, "Axis\tFloor\tReserved\tRecommended\tObserved\tMinimum\tVerdict"); err != nil {
			return err
		}
		for _, check := range node.Checks {
			minimum := check.Minimum
			if node.Origin == capacityfit.Inventory && minimum != nil && check.Axis != capacityfit.DataStorage {
				var threshold capacityfit.Vector
				threshold.Set(check.Axis, *minimum)
				minimum = capacityfit.InventoryMinimum(threshold).Value(check.Axis)
			}
			if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", check.Axis,
				capacityNumber(node.Demand.HostFloor.Value(check.Axis)), capacityNumber(node.Demand.Reservation.Value(check.Axis)),
				capacityNumber(check.Recommended), capacityNumber(check.Observed), capacityNumber(minimum), check.Status); err != nil {
				return err
			}
		}
		if err := table.Flush(); err != nil {
			return err
		}
		if len(node.ReasonCodes) > 0 {
			if _, err := fmt.Fprintf(writer, "Reasons: %s\n", strings.Join(node.ReasonCodes, ", ")); err != nil {
				return err
			}
		}
		for _, advice := range node.Advice {
			if _, err := fmt.Fprintf(writer, "  %s\n", advice); err != nil {
				return err
			}
		}
	}
	return nil
}

func capacityNumber(value *float64) string {
	if value == nil {
		return "?"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}
