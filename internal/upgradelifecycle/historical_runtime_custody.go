package upgradelifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"path"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
)

// Called only after the attested source release, generation, owner signature
// and Kopia anchor have passed the existing cross-release constructor.
func appendHistoricalAppliedRuntimeClosure(input *LegacyCurrentStateAuthorityInput) error {
	var envelope struct {
		SharedRequestDigest string `json:"sharedRequestDigest"`
	}
	if err := json.Unmarshal(input.ApplyResult, &envelope); err != nil {
		return err
	}
	if envelope.SharedRequestDigest == "" {
		return nil
	} // Pre-capsule releases retain their existing recovery ABI.
	root, err := confinedfs.Open(input.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	planBytes, _, err := transaction.ReadStableBounded(path.Join(input.Inspection.OutputRoot, ".stackkit", "resolved-plan.json"), 16<<20)
	if err != nil {
		return err
	}
	plan, err := generationartifact.ParseHistoricalPlan(planBytes, input.Inspection)
	if err != nil {
		return err
	}
	journal, err := architecturev2.NewProductApplyFileJournal(input.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer journal.Close()
	custody, err := journal.LoadHistoricalAppliedRuntimeCustody(context.Background(), plan.Binding(), plan.OutputRoot(), input.ApplyResult)
	if err != nil {
		return err
	}
	request := custody.Request()
	if request.ManifestHash != input.Capture.Lineage.ManifestHash || request.GenerationReceiptHash != input.Capture.Lineage.GenerationReceiptHash {
		return errors.New("historical runtime custody differs from the owner-signed source generation")
	}
	if err := plan.MatchApplyRequirements(custody.Requirements(), request.RequirementsHash); err != nil {
		return err
	}
	applications, err := restoreactivation.DeriveHistoricalStandaloneComposeRuntimeCustody(input.WorkspaceRoot, plan, input.Manifest, input.Capture.OperationID)
	if err != nil {
		return err
	}
	marketplace, err := restoreactivation.DeriveHistoricalMarketplaceRuntimeCustody(input.WorkspaceRoot, plan, input.Manifest, input.Capture.OperationID)
	if err != nil {
		return err
	}
	data := CurrentStateAuthorityInput{WorkspaceRoot: input.WorkspaceRoot, Manifest: input.Manifest, Capture: input.Capture}
	if err := appendRuntimeCustodyFiles(&data, applications, marketplace); err != nil {
		return err
	}
	graph, err := restoreactivation.DeriveHistoricalRuntimeRecoveryGraph(input.WorkspaceRoot, plan, input.Manifest, input.Capture.OperationID)
	if err != nil {
		return err
	}
	graphBlob, err := runtimeRecoveryGraphBlob(data, graph)
	if err != nil {
		return err
	}
	controls := []ExecutorStateBlobInput{{ID: "applied-runtime-custody", Path: custody.Path(), Mode: "0600", Data: custody.Canonical()}, graphBlob}
	for _, control := range controls {
		for _, existing := range data.Capture.Artifacts {
			if existing.ID == control.ID || executorStatePathsCollide(filepathToSlash(existing.Path), control.Path) {
				return errors.New("historical runtime custody collides with captured authority")
			}
		}
		data.Capture.Artifacts = append(data.Capture.Artifacts, control)
	}
	input.Capture = data.Capture
	return nil
}
