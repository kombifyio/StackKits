package commands

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
)

func originalGenerationReceipt(workspace, spec string) (generationartifact.GenerationReceipt, string, error) {
	var receipt generationartifact.GenerationReceipt
	output, err := historicalGenerationOutputRoot(workspace, spec)
	if err != nil {
		return receipt, "", err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return receipt, "", err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return receipt, "", err
	}
	defer transaction.Close()
	path := filepath.ToSlash(filepath.Join(output, ".stackkit", generationartifact.GenerationReceiptFileName))
	raw, info, err := transaction.ReadStableBounded(path, 1<<20)
	if err != nil || !info.Mode().IsRegular() {
		return receipt, "", errors.New("original generation receipt is unavailable")
	}
	if err = decodeUpgradeExactJSON(raw, &receipt); err != nil {
		return receipt, "", err
	}
	digest, err := receipt.Hash()
	return receipt, digest, err
}

// The original source selector leaves the existing Owner/member receipt gate
// only as an opaque verifiedOriginalApplyProgram. Installation provenance alone
// can never identify an old Apply. No cache scan or semantic-version inference
// is used, including when both binaries describe themselves as 0.54.8.
func inspectPreviewSourceGeneration(ctx context.Context, workspace, spec, tag string) (publicUpgradeBridge, bool, error) {
	var empty publicUpgradeBridge
	receipt, receiptHash, err := originalGenerationReceipt(workspace, spec)
	if err != nil {
		return empty, false, err
	}
	original, historyErr := readVerifiedOriginalApplyProgram(workspace, receipt.Binding, receipt.ManifestHash, receiptHash)
	var program architecturev2.ApplyProgramIdentity
	if historyErr == nil {
		program, historyErr = original.Program()
	}
	if historyErr != nil || program.PreviewCandidate == nil {
		// A private current process may not reinterpret an unproven same-version
		// generation. An older public source still follows its existing verifier.
		if tag == "v"+architectureV2ComponentVersion(version) {
			observed, err := localevidence.CurrentExecutableSHA256()
			if err != nil {
				return empty, false, err
			}
			current, err := inspectCurrentPreviewProgram(ctx, observed, workspace)
			if err != nil {
				return empty, false, err
			}
			if current != nil {
				return empty, true, errors.New("same-version original source does not bind this private reader; preserve its evidence and use the admitted forward checkpoint")
			}
		}
		return empty, false, nil
	}
	source := program.PreviewCandidate
	if tag != "v"+source.Version {
		return empty, true, errors.New("signed original Apply source differs from its generation compiler")
	}
	observed, err := localevidence.CurrentExecutableSHA256()
	if err != nil {
		return empty, true, err
	}
	if observed == program.ExecutableSHA256 {
		current, err := inspectCurrentPreviewProgram(ctx, observed, workspace)
		if err != nil {
			return empty, true, err
		}
		if current != nil && current.Kit == source.Kit && current.Version == source.Version && current.Platform == source.Platform && current.IndexSHA256 == source.IndexSHA256 && current.ArchiveSHA256 == source.ArchiveSHA256 && current.Source == source.Source && current.Producer == source.Producer {
			return empty, false, nil // Exact original process, not version equality.
		}
	}
	selector := releaseindex.PreviewSourceSelector{Kit: source.Kit, Version: source.Version, Platform: source.Platform, IndexSHA256: source.IndexSHA256, ArchiveSHA256: source.ArchiveSHA256, CLISHA256: strings.TrimPrefix(program.ExecutableSHA256, "sha256:")}
	cache := filepath.Join(workspace, ".stackkit", "releases")
	if sourceGenerationReleaseCache == "" {
		persisted, err := persistedPreviewSourceCache(workspace)
		if err != nil {
			return empty, true, err
		}
		if persisted != "" {
			cache = persisted
		}
	}
	if sourceGenerationReleaseCache != "" {
		if !filepath.IsAbs(sourceGenerationReleaseCache) {
			return empty, true, errors.New("original source cache locator must be absolute")
		}
		cache = sourceGenerationReleaseCache
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return empty, true, err
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return empty, true, err
	}
	bridge := publicUpgradeBridge{Enabled: true, SourceProgram: &program}
	err = withPreviewSourceExecutable(ctx, cache, workspace, selector, func(binary string, proof releaseindex.PreviewSourceCacheProof) error {
		if proof.Source != source.Source || proof.Producer != source.Producer {
			return errors.New("verified source cache differs from the signed original Apply program")
		}
		if source.BuildProfile != "" && (proof.BuildProfile != source.BuildProfile || !reflect.DeepEqual(proof.BuildTags, source.BuildTags) || proof.IndexAttestationSHA256 != source.IndexAttestationSHA256 || proof.PayloadAttestationSHA256 != source.PayloadAttestationSHA256 || proof.TrustedRootSHA256 != source.TrustedRootSHA256 || proof.SBOMSHA256 != source.SBOMSHA256) {
			return errors.New("verified source compiler and attestation identities differ from the signed original Apply program")
		}
		bridge.SourceCandidate = &proof
		inspectionSpec, contextProof, remove, err := materializeHistoricalCloudSourceSpec(workspace, spec)
		if err != nil {
			return err
		}
		defer remove()
		bridge.SourceContext = contextProof
		inventory, cleanup, err := materializeAttestedSourceInventory(workspace, inspectionSpec, tag, true)
		if err != nil {
			return err
		}
		defer cleanup()
		runner := newUpgradeInspectionRunner()
		if runner == nil {
			return errors.New("original source inspection runner is unavailable")
		}
		common := publicUpgradeCommandPrefix(workspace, inspectionSpec)
		args := func(values ...string) []string {
			result := append(append([]string(nil), common...), values...)
			if inventory != "" {
				result = append(result, "--inventory", inventory)
			}
			return result
		}
		rawPlan, err := runner.Run(ctx, binary, args("plan", "--json"), workspace)
		if err != nil {
			return err
		}
		if len(rawPlan) > maxSourceGenerationInspectionBytes {
			return errors.New("original source Plan exceeds its bound")
		}
		if err = decodeUpgradeExactJSON(rawPlan, &bridge.Current); err != nil {
			return err
		}
		if err = validateUpgradePlanInspection(bridge.Current, "original private source"); err != nil {
			return err
		}
		if bridge.Current.Binding != receipt.Binding || bridge.Current.Binding.CompilerVersion != "stackkits-resolver/"+source.Version {
			return errors.New("source program did not reproduce the exact original Plan")
		}
		rawVerify, err := runner.Run(ctx, binary, args("verify", "--offline", "--json"), workspace)
		if err != nil {
			return err
		}
		if len(rawVerify) > maxSourceGenerationInspectionBytes {
			return errors.New("original source Owner/Apply proof exceeds its bound")
		}
		var envelope publicUpgradeRawCommandResult
		if err = decodeUpgradeExactJSON(rawVerify, &envelope); err != nil {
			return err
		}
		if envelope.SchemaVersion != commandResultSchemaVersion || envelope.Command != "stackkit verify" || envelope.Status != "success" {
			return errors.New("original source did not verify its Owner/Apply")
		}
		if err = decodeUpgradeExactJSON(envelope.Data, &bridge.Verify); err != nil {
			return err
		}
		report := bridge.Verify
		if report.SchemaVersion != "stackkit.verify-result/v1" || !report.Offline || report.PlanHash != receipt.Binding.PlanHash || report.Runtime != nil || report.Apply.ResultHash != original.ApplyResultHash() || !reflect.DeepEqual(report.Apply.Program, &program) || report.Apply.EvidenceBundleHash == "" || report.Owner.OwnerRef != owner.OwnerRef || report.Owner.KeyID != owner.KeyID || report.Owner.PocketIDSubject != binding.PocketIDSubject || report.Owner.OwnerBindingDigest != localevidence.OwnerRuntimeBindingDigest(binding) {
			return errors.New("source program proof differs from the original signed Plan/Apply/Owner")
		}
		return nil
	})
	if err != nil {
		return empty, true, err
	}
	if bridge.SourceContext != nil {
		if err = verifyHistoricalCloudSourceContext(workspace, bridge.SourceContext); err != nil {
			return empty, true, err
		}
	}
	return bridge, true, nil
}

func withPreviewSourceExecutable(ctx context.Context, cache, workspace string, selector releaseindex.PreviewSourceSelector, invoke func(string, releaseindex.PreviewSourceCacheProof) error) error {
	if invoke == nil {
		return errors.New("source executable callback is required")
	}
	return (releaseindex.SigstoreVerifier{}).InspectPreviewSourceCache(ctx, cache, selector, func(verified releaseindex.VerifiedPreviewCandidate) error {
		temporary, err := os.MkdirTemp("", "stackkit-preview-source-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temporary)
		cli, server, err := upgradelifecycle.PreviewExecutablesWithOpenTofu(verified, temporary, workspace)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(cli)) != selector.CLISHA256 {
			return errors.New("original source executable differs from its signed Apply identity")
		}
		name := "stackkit"
		serverName := "stackkit-server"
		if selector.Platform.OS == "windows" {
			name += ".exe"
			serverName += ".exe"
		}
		path := filepath.Join(temporary, name)
		if err = os.WriteFile(path, cli, 0700); err != nil {
			return err
		}
		if len(server) > 0 {
			if err = os.WriteFile(filepath.Join(temporary, serverName), server, 0700); err != nil {
				return err
			}
		}
		proof, err := verified.SourceCacheProof(selector.CLISHA256)
		if err != nil {
			return err
		}
		return invoke(path, proof)
	})
}
