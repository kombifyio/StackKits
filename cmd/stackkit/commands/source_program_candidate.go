package commands

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/imagepreparation"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
)

type currentApplyProgramSourceVerifier struct{ workspaceRoot string }

func newCurrentApplyProgramSourceVerifier(workspaceRoot string) (architecturev2.ProductApplyProgramSourceVerifier, error) {
	if !filepath.IsAbs(workspaceRoot) {
		return nil, errors.New("Apply program composition needs its absolute existing workspace")
	}
	return currentApplyProgramSourceVerifier{workspaceRoot: workspaceRoot}, nil
}

func (verifier currentApplyProgramSourceVerifier) VerifyApplyProgramSource(ctx context.Context, observedExecutableSHA256 string) (*architecturev2.ApplyPreviewProgramSource, error) {
	proof, err := inspectCurrentPreviewProgram(ctx, observedExecutableSHA256, verifier.workspaceRoot)
	if err != nil || proof == nil {
		return nil, err
	}
	return &architecturev2.ApplyPreviewProgramSource{Kit: proof.Kit, Version: proof.Version, Platform: proof.Platform, IndexSHA256: proof.IndexSHA256, ArchiveSHA256: proof.ArchiveSHA256, Source: proof.Source, Producer: proof.Producer,
		IndexAttestationSHA256: proof.IndexAttestationSHA256, PayloadAttestationSHA256: proof.PayloadAttestationSHA256,
		TrustedRootSHA256: proof.TrustedRootSHA256, SBOMSHA256: proof.SBOMSHA256, BuildProfile: proof.BuildProfile, BuildTags: append([]string(nil), proof.BuildTags...)}, nil
}

func inspectBundlePreviewProgram(ctx context.Context, observedExecutableSHA256, workspace string) (*releaseindex.PreviewSourceCacheProof, bool, error) {
	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument("stack-spec.yaml")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, true, err
	}
	if err == nil && loaded.Document.V2 != nil && loaded.Document.V2.OfflineBundle != nil {
		ref := *loaded.Document.V2.OfflineBundle
		options, cleanup, err := neutralImageOptions(filepath.Join(ref.Path, "cache"), workspace)
		if err != nil {
			return nil, true, err
		}
		defer cleanup()
		manifest, err := imagepreparation.VerifyOfflineBundleRef(ctx, options, ref)
		if err != nil {
			return nil, true, err
		}
		if candidate := manifest.Neutral.Candidate; candidate != nil {
			if candidate.CLISHA256 != strings.TrimPrefix(observedExecutableSHA256, "sha256:") {
				return nil, true, errors.New("bundle candidate differs from observed Apply process")
			}
			identity := candidate.Identity
			return &releaseindex.PreviewSourceCacheProof{SchemaVersion: "stackkit.preview-source-cache/v1",
				Kit: candidate.Kit, Version: candidate.Version, Platform: candidate.Platform, CLISHA256: candidate.CLISHA256,
				IndexSHA256: identity.IndexSHA256, ArchiveSHA256: identity.ArchiveSHA256, Source: identity.Source, Producer: identity.Producer,
				IndexAttestationSHA256: identity.IndexAttestationSHA256, PayloadAttestationSHA256: identity.PayloadAttestationSHA256,
				TrustedRootSHA256: identity.TrustedRootSHA256, SBOMSHA256: identity.SBOMSHA256,
				BuildProfile: identity.BuildProfile, BuildTags: append([]string(nil), identity.BuildTags...)}, true, nil
		}
		return nil, true, nil
	}
	return nil, false, nil
}

// inspectCurrentPreviewProgram describes the process Core actually observed.
// The adjacent installed pin selects bytes only. Offline signatures plus the
// archive executable's equality to actual process bytes prove their provenance;
// this helper supplies no installation, Owner, Plan or Apply authority.
func inspectCurrentPreviewProgram(ctx context.Context, actualExecutableSHA256, workspace string) (*releaseindex.PreviewSourceCacheProof, error) {
	if proof, bound, err := inspectBundlePreviewProgram(ctx, actualExecutableSHA256, workspace); err != nil || bound {
		return proof, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	rootDirectory := filepath.Dir(filepath.Dir(executable))
	root, err := os.OpenRoot(rootDirectory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	sourceInfo, err := root.Lstat("candidate-source")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("current candidate source is not confined installed custody")
	}
	raw, err := readCurrentPreviewMaterial(root, "stackkits-release-pin.json", 64<<10)
	if err != nil {
		return nil, err
	}
	var hint struct {
		SchemaVersion string                `json:"schemaVersion"`
		Kit           string                `json:"kit"`
		Version       string                `json:"version"`
		Platform      releaseindex.Platform `json:"platform"`
		IndexSHA256   string                `json:"indexSha256"`
		ArchiveSHA256 string                `json:"archiveSha256"`
	}
	if json.Unmarshal(raw, &hint) != nil || hint.SchemaVersion != "techstack.stackkit-preview-candidate-pin/v1" {
		return nil, errors.New("current candidate source selector is invalid")
	}
	actual := strings.TrimPrefix(actualExecutableSHA256, "sha256:")
	index, err := readCurrentPreviewMaterial(root, filepath.Join("candidate-source", releaseindex.PreviewCandidateIndexName), 1<<20)
	if err != nil {
		return nil, err
	}
	selector, err := releaseindex.PreviewSourceSelectorFromIndex(index, hint.Version, hint.Platform, hint.IndexSHA256, hint.ArchiveSHA256, actual)
	if err != nil {
		return nil, err
	}

	var output releaseindex.PreviewSourceCacheProof
	err = (releaseindex.SigstoreVerifier{}).InspectPreviewSourceDirectory(ctx, filepath.Join(rootDirectory, "candidate-source"), selector, func(proof releaseindex.VerifiedPreviewCandidate) error {
		cli, _, err := upgradelifecycle.PreviewExecutablesFromVerifiedCandidate(proof)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(cli)) != actual {
			return errors.New("candidate archive CLI differs from the actual Apply process")
		}
		output, err = proof.SourceCacheProof(actual)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &output, nil
}

// This is a locator for an already signed original-program selector. It never
// authenticates source bytes and never scans for a same-version cache entry.
func persistedPreviewSourceCache(workspace string) (string, error) {
	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument("stack-spec.yaml")
	if err != nil {
		return "", err
	}
	if loaded.Document.V2 == nil || loaded.Document.V2.OfflineBundle == nil {
		return "", nil
	}
	ref := *loaded.Document.V2.OfflineBundle
	if err := ref.Validate(); err != nil {
		return "", err
	}
	digest, err := imagepreparation.FileDigest(filepath.Join(ref.Path, imagepreparation.BundleManifestName))
	if err != nil || digest != ref.ManifestSHA256 {
		return "", errors.New("persisted source bundle manifest changed")
	}
	return filepath.Join(ref.Path, "cache"), nil
}

func readCurrentPreviewMaterial(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("current candidate material must be bounded and regular")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("current candidate material changed before reading")
	}
	raw, err := io.ReadAll(io.LimitReader(file, info.Size()+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || int64(len(raw)) != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("current candidate material changed while reading")
	}
	return raw, nil
}
