package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
)

// Historical data and today's signed Owner observation are deliberately
// separate. Neither this context nor an attested old Plan authorizes Apply.
type historicalCloudSourceContext struct {
	SessionTransitionDigest string                           `json:"sessionTransitionDigest"`
	SourceSpecHash          string                           `json:"sourceSpecHash"`
	CurrentSpecHash         string                           `json:"currentSpecHash"`
	RetainedOwner           architectureV2OwnerVerifySummary `json:"retainedOwner"`
}

func materializeHistoricalCloudSourceSpec(workspace, requestedSpec string) (string, *historicalCloudSourceContext, func(), error) {
	noop := func() {}
	state, err := localevidence.LoadCloudSessionRouting(workspace)
	if errors.Is(err, os.ErrNotExist) {
		return requestedSpec, nil, noop, nil
	}
	if err != nil {
		return "", nil, noop, err
	}
	requested := requestedSpec
	if filepath.IsAbs(requested) {
		requested, err = filepath.Rel(workspace, requested)
	}
	if err != nil || filepath.ToSlash(filepath.Clean(requested)) != state.SpecPath || state.Phase != "active" {
		return "", nil, noop, errors.New("historical Cloud source inspection requires the original path and a verified active session transition")
	}
	context := &historicalCloudSourceContext{SessionTransitionDigest: localevidence.SessionRoutingValueDigest(state), SourceSpecHash: localevidence.SessionRoutingDigest(state.BeforeSpec), CurrentSpecHash: localevidence.SessionRoutingDigest(state.AfterSpec)}
	directory, err := os.MkdirTemp(workspace, ".stackkit-source-inspection-")
	if err != nil {
		return "", nil, noop, err
	}
	file := filepath.Join(directory, "stack-spec.yaml")
	remove := func() { _ = os.Remove(file); _ = os.Remove(directory) }
	if err := os.WriteFile(file, state.BeforeSpec, 0o600); err != nil {
		remove()
		return "", nil, noop, err
	}
	relative, err := filepath.Rel(workspace, file)
	if err != nil {
		remove()
		return "", nil, noop, err
	}
	return filepath.ToSlash(relative), context, remove, nil
}

// v0.40's offline result includes a non-live runtime evidence summary and
// leaves PocketID runtime fields empty. Admit only that exact authenticated
// Cloud archive, preserving its report rather than backfilling its fields.
func validateHistoricalCloudSourceVerify(workspace string, raw []byte, current generationartifact.PlanInspection, receipt releaseindex.Receipt, source *historicalCloudSourceContext) (architectureV2VerifyReport, error) {
	var report architectureV2VerifyReport
	if source == nil || receipt.Kit != "cloud-kit" || receipt.Version != "v0.40.0" ||
		receipt.ArchiveSHA256 != "1bcd7cb4c9eaa13457a943f3185c2b64bab9aac18740f82eb73ad0f9cf669e21" ||
		receipt.IndexSHA256 != "edc8174139665182816477990c985584f390c1482d5e3327b0c6fc603e72c971" ||
		current.Binding.CompilerVersion != "stackkits-resolver/0.40.0" || current.Binding.Renderer.Version != "0.40.0" || current.Binding.SpecHash != source.SourceSpecHash {
		return report, errors.New("historical Cloud source is outside the qualified v0.40 release and signed original intent")
	}
	var envelope struct {
		SchemaVersion string          `json:"schemaVersion"`
		Command       string          `json:"command"`
		Status        string          `json:"status"`
		Data          json.RawMessage `json:"data"`
	}
	if err := decodeUpgradeExactJSON(raw, &envelope); err != nil {
		return report, err
	}
	if envelope.SchemaVersion != commandResultSchemaVersion || envelope.Command != "stackkit verify" || envelope.Status != "success" {
		return report, errors.New("historical Cloud source did not verify its own generation")
	}
	if err := decodeUpgradeExactJSON(envelope.Data, &report); err != nil {
		return report, err
	}
	if report.SchemaVersion != "stackkit.verify-result/v1" || !report.Offline || report.PlanHash != current.Binding.PlanHash || report.Apply.PlanHash != current.Binding.PlanHash ||
		report.Apply.ResultHash == "" || report.Apply.EvidenceBundleHash == "" ||
		report.Owner.OwnerRef == "" || report.Owner.KeyID == "" || report.Owner.PocketIDSubject != "" || report.Owner.OwnerBindingDigest != "" || report.Owner.Member != nil ||
		report.Runtime == nil || report.Runtime.Live || report.Runtime.ExecutionMode != "standard-process" || report.Runtime.Status != "verified-apply-evidence" ||
		exactBeta4ReceiptMatches(report.Releases, receipt) != 1 {
		return report, errors.New("historical Cloud source report differs from its qualified offline evidence semantics")
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil || owner.OwnerRef != report.Owner.OwnerRef || owner.KeyID != report.Owner.KeyID {
		return report, errors.New("historical Cloud source Owner differs from retained signing custody")
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil || binding.OwnerRef != owner.OwnerRef || binding.KeyID != owner.KeyID {
		return report, errors.New("historical Cloud source has no independently signed retained Owner binding")
	}
	// The attested source verified its full historical Plan and Apply closure.
	// Independently verify the retained original result signature with today's
	// reader too; never synthesize a new result, receipt or expired request.
	resultPath, receiptPath, err := historicalCloudApplyPaths(report.Apply.ResultHash)
	if err != nil {
		return report, err
	}
	result, err := localevidence.ReadCloudSessionFile(workspace, resultPath)
	if err != nil {
		return report, err
	}
	proof, err := localevidence.ReadCloudSessionFile(workspace, receiptPath)
	if err != nil {
		return report, err
	}
	if err := verifyOwnerApplyResultReceipt(workspace, result, proof, report.Apply.ResultHash); err != nil {
		return report, err
	}
	source.RetainedOwner = architectureV2OwnerVerifySummary{OwnerRef: owner.OwnerRef, KeyID: owner.KeyID, PocketIDSubject: binding.PocketIDSubject, OwnerBindingDigest: localevidence.OwnerRuntimeBindingDigest(binding)}
	return report, nil
}

func historicalCloudApplyPaths(digest string) (string, string, error) {
	value, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(value) != 64 || strings.Trim(value, "0123456789abcdef") != "" {
		return "", "", fmt.Errorf("historical Cloud Apply result digest is invalid")
	}
	return architectureV2ApplyEvidenceRoot + "/results/" + value + ".json", architectureV2ApplyEvidenceRoot + "/receipts/" + value + ".json", nil
}

func sourceGenerationOwner(bridge publicUpgradeBridge) architectureV2OwnerVerifySummary {
	if bridge.SourceContext != nil && bridge.SourceContext.RetainedOwner.PocketIDSubject != "" {
		return bridge.SourceContext.RetainedOwner
	}
	return bridge.Verify.Owner
}

func verifyHistoricalCloudSourceContext(workspace string, source *historicalCloudSourceContext) error {
	state, err := localevidence.LoadCloudSessionRouting(workspace)
	if err != nil || state.Phase != "active" || localevidence.SessionRoutingValueDigest(state) != source.SessionTransitionDigest ||
		(source.RetainedOwner.PocketIDSubject != "" && source.RetainedOwner.OwnerBindingDigest != state.OwnerDigest) {
		return errors.New("historical Cloud session or signed Owner binding changed during source inspection")
	}
	return nil
}
