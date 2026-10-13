package restoreactivation

import "github.com/kombifyio/stackkits/internal/generationartifact"

// recoveryPlanData supplies data to the common recovery extraction, never to
// an execution gate. Historical callers authenticate source authority first.
type recoveryPlanData interface {
	Canonical() []byte
	Binding() generationartifact.PlanBinding
	OutputRoot() string
}

func DeriveHistoricalRuntimeRecoveryGraph(workspace string, plan generationartifact.HistoricalPlan, manifest generationartifact.ArtifactManifest, operation string) (RuntimeRecoveryGraph, error) {
	derived, err := deriveRuntimeRecoveryGraph(workspace, plan, manifest, operation)
	if err != nil {
		return RuntimeRecoveryGraph{}, err
	}
	return cloneRuntimeRecoveryGraph(derived.graph), nil
}

func DeriveHistoricalStandaloneComposeRuntimeCustody(workspace string, plan generationartifact.HistoricalPlan, manifest generationartifact.ArtifactManifest, operation string) ([]StandaloneComposeRuntimeCustody, error) {
	return deriveStandaloneComposeRuntimeCustody(workspace, plan, manifest, operation)
}

func DeriveHistoricalMarketplaceRuntimeCustody(workspace string, plan generationartifact.HistoricalPlan, manifest generationartifact.ArtifactManifest, operation string) (*MarketplaceRuntimeCustody, error) {
	return deriveMarketplacePlanRuntimeCustody(workspace, plan, manifest, operation)
}
