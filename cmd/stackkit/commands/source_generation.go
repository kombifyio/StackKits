package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"golang.org/x/mod/semver"
)

const maxSourceGenerationInspectionBytes = 2 << 20

// Export public distribution identity only, never workspace paths or custody.
type sourceGenerationReleaseProof struct {
	SchemaVersion          string                `json:"schemaVersion"`
	Kit                    string                `json:"kit"`
	Version                string                `json:"version"`
	Channel                releaseindex.Channel  `json:"channel"`
	Platform               releaseindex.Platform `json:"platform"`
	ArchiveSHA256          string                `json:"archiveSha256"`
	IndexSHA256            string                `json:"indexSha256"`
	AttestationSHA256      string                `json:"attestationSha256"`
	IndexAttestationSHA256 string                `json:"indexAttestationSha256"`
	TrustedRootSHA256      string                `json:"trustedRootSha256"`
}

// inspectSourceGeneration never installs or selects an arbitrary compiler. The
// receipt in the old generation is a hint only; existing attested release
// custody and that source CLI's generation / offline Owner proof decide.
func inspectSourceGeneration(ctx context.Context, workspace, requestedSpec string) (publicUpgradeBridge, bool, error) {
	tag, err := currentGenerationSourceHint(workspace, requestedSpec)
	if err != nil {
		return publicUpgradeBridge{}, false, err
	}
	if tag == "" {
		return publicUpgradeBridge{}, false, errors.New("source generation release is unavailable")
	}
	current := "v" + architectureV2ComponentVersion(version)
	if tag == current {
		return publicUpgradeBridge{}, false, nil // Ordinary current CUE inspection.
	}
	if !semver.IsValid(current) || semver.Compare(tag, current) >= 0 {
		return publicUpgradeBridge{}, false, errors.New("source generation must precede the installed compiler")
	}
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return publicUpgradeBridge{}, false, err
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(workspace, ".stackkit", "releases", kit, tag, platform.OS+"-"+platform.Arch)
	var receipt releaseindex.Receipt
	err = (releaseindex.Installer{Attestations: newPublicAttestationVerifier()}).InspectInstalled(ctx, installDir, func(proof releaseindex.VerifiedInstallation) error {
		return proof.Inspect(func(current releaseindex.Receipt, _ releaseindex.Asset, _ io.Reader) error {
			if current.SchemaVersion != releaseindex.ReceiptSchemaVersion || current.Channel != releaseindex.ChannelStable || filepath.Clean(current.InstallDir) != filepath.Clean(installDir) {
				return errors.New("source generation receipt is outside installed stable custody")
			}
			if err := validateExpectedCurrentReleaseReceipt(current, kit, tag, platform); err != nil {
				return err
			}
			receipt = current
			return nil
		})
	})
	if err != nil {
		return publicUpgradeBridge{}, false, fmt.Errorf("verify installed source generation release: %w", err)
	}
	bridge, err := inspectAttestedSourceAuthority(ctx, workspace, requestedSpec, receipt, true, true)
	if err != nil {
		return publicUpgradeBridge{}, false, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil || owner.OwnerRef != bridge.Verify.Owner.OwnerRef || owner.KeyID != bridge.Verify.Owner.KeyID {
		return publicUpgradeBridge{}, false, errors.New("source generation differs from retained local Owner custody")
	}
	runtimeBinding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil || runtimeBinding.PocketIDSubject != bridge.Verify.Owner.PocketIDSubject || localevidence.OwnerRuntimeBindingDigest(runtimeBinding) != bridge.Verify.Owner.OwnerBindingDigest {
		return publicUpgradeBridge{}, false, errors.New("source generation differs from retained signed Owner runtime binding")
	}
	return bridge, true, nil
}

func writeSourceGenerationInspection(writer io.Writer, bridge publicUpgradeBridge) error {
	// Preserve the original compiler, Inventory and applied evidence. This is
	// preparation provenance, never permission to execute an old plan now.
	publicRelease := sourceGenerationReleaseProof{
		SchemaVersion: bridge.Receipt.SchemaVersion, Kit: bridge.Receipt.Kit,
		Version: bridge.Receipt.Version, Channel: bridge.Receipt.Channel,
		Platform: bridge.Receipt.Platform, ArchiveSHA256: bridge.Receipt.ArchiveSHA256,
		IndexSHA256: bridge.Receipt.IndexSHA256, AttestationSHA256: bridge.Receipt.AttestationSHA256,
		IndexAttestationSHA256: bridge.Receipt.IndexAttestationSHA256, TrustedRootSHA256: bridge.Receipt.TrustedRootSHA256,
	}
	raw, err := json.Marshal(struct {
		APIVersion         string                            `json:"apiVersion"`
		Inspection         generationartifact.PlanInspection `json:"inspection"`
		Release            sourceGenerationReleaseProof      `json:"sourceRelease"`
		Owner              architectureV2OwnerVerifySummary  `json:"owner"`
		ApplyResultHash    string                            `json:"applyResultHash"`
		EvidenceBundleHash string                            `json:"evidenceBundleHash"`
	}{"stackkit.source-generation-inspection/v1", bridge.Current, publicRelease, bridge.Verify.Owner, bridge.Verify.Apply.ResultHash, bridge.Verify.Apply.EvidenceBundleHash})
	if err != nil {
		return err
	}
	if len(raw) > maxSourceGenerationInspectionBytes {
		return errors.New("source generation inspection exceeds its public proof bound")
	}
	_, err = writer.Write(append(raw, '\n'))
	return err
}

func runSourceGenerationPlan(cmdContext context.Context, workspace, requestedSpec string, writer io.Writer) (bool, error) {
	bridge, historical, err := inspectSourceGeneration(cmdContext, workspace, requestedSpec)
	if err != nil || !historical {
		return historical, err
	}
	if bridge.Current.Binding.CompilerVersion != "stackkits-resolver/"+strings.TrimPrefix(bridge.Receipt.Version, "v") {
		return true, errors.New("source generation compiler differs from its attested release")
	}
	return true, writeSourceGenerationInspection(writer, bridge)
}
