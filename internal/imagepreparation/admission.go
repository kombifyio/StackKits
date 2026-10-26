package imagepreparation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
)

// FreshBundleAdmission is created only by this owner after full byte
// verification and fresh-target admission. It is neither a public release
// Receipt nor a conversion of a source-cache proof into installation authority.
type FreshBundleAdmission struct {
	manifest BundleManifest
	ref      runtimeprofile.OfflineBundleRef
	target   string
}

func freshBundleAdmission(options Options, ref runtimeprofile.OfflineBundleRef, manifest BundleManifest) (*FreshBundleAdmission, error) {
	if err := CleanTarget(options.Target); err != nil {
		return nil, err
	}
	admission := &FreshBundleAdmission{manifest: manifest, ref: ref, target: options.Target}
	if err := admission.recheck(); err != nil {
		return nil, err
	}
	return admission, nil
}

func (admission *FreshBundleAdmission) recheck() error {
	if admission == nil || admission.target == "" {
		return errors.New("fresh verified offline-bundle admission is required")
	}
	if err := CleanTarget(admission.target); err != nil {
		return err
	}
	actual, err := FileDigest(filepath.Join(admission.ref.Path, BundleManifestName))
	if err != nil || actual != admission.ref.ManifestSHA256 {
		return errors.New("offline bundle manifest changed after fresh admission")
	}
	return nil
}

// AdmitFreshOfflineBundle checks every signature, tool, provider, image and
// model before even observing Docker. Import retains the separate effect gate.
func AdmitFreshOfflineBundle(ctx context.Context, options Options, root string) (*FreshBundleAdmission, error) {
	ref, err := OfflineBundleReference(root)
	if err != nil {
		return nil, err
	}
	manifest, err := VerifyOfflineBundle(ctx, options, root)
	if err != nil {
		return nil, err
	}
	return freshBundleAdmission(options, ref, manifest)
}

func OfflineBundleReference(root string) (runtimeprofile.OfflineBundleRef, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return runtimeprofile.OfflineBundleRef{}, err
	}
	digest, err := FileDigest(filepath.Join(absolute, BundleManifestName))
	if err != nil {
		return runtimeprofile.OfflineBundleRef{}, err
	}
	ref := runtimeprofile.OfflineBundleRef{Path: absolute, ManifestSHA256: digest}
	return ref, ref.Validate()
}

// referenceOptions derives byte selectors only from the canonical persisted
// path+manifest hash. These hints still undergo the original signature policy
// and equality to this running process on every readback. No ambient selector
// or retained artifact locator is an authority.
func referenceOptions(options Options, ref runtimeprofile.OfflineBundleRef) (Options, error) {
	if err := ref.Validate(); err != nil {
		return Options{}, err
	}
	digest, err := FileDigest(filepath.Join(ref.Path, BundleManifestName))
	if err != nil || digest != ref.ManifestSHA256 {
		return Options{}, errors.New("offline bundle reference changed since native init")
	}
	var manifest BundleManifest
	if err := readBundleJSON(filepath.Join(ref.Path, BundleManifestName), &manifest); err != nil {
		return Options{}, err
	}
	if manifest.Neutral.SourceKind == PreviewCandidateSource {
		candidate := manifest.Neutral.Candidate
		if candidate == nil || manifest.Neutral.Release != (Release{}) {
			return Options{}, errors.New("candidate bundle cannot assert a public release")
		}
		options.SourceKind = PreviewCandidateSource
		options.Candidate = &CandidateSelection{Selector: releaseindex.PreviewSourceSelector{
			Kit: candidate.Kit, Version: candidate.Version, Platform: candidate.Platform,
			IndexSHA256: candidate.Identity.IndexSHA256, ArchiveSHA256: candidate.Identity.ArchiveSHA256, CLISHA256: candidate.CLISHA256,
		}}
	} else if manifest.Neutral.Candidate != nil || (manifest.Neutral.SourceKind != "" && manifest.Neutral.SourceKind != PublicReleaseSource) {
		return Options{}, errors.New("offline bundle source kind and provenance disagree")
	}
	options.CacheRoot = filepath.Join(ref.Path, "cache")
	return options, nil
}

// VerifyOfflineBundleRef is routine lifecycle verification. An initialized
// workspace is legitimate here; fresh-target admission is never replayed.
func VerifyOfflineBundleRef(ctx context.Context, options Options, ref runtimeprofile.OfflineBundleRef) (BundleManifest, error) {
	options, err := referenceOptions(options, ref)
	if err != nil {
		return BundleManifest{}, err
	}
	manifest, err := VerifyOfflineBundle(ctx, options, ref.Path)
	if err != nil {
		return BundleManifest{}, err
	}
	digest, err := FileDigest(filepath.Join(ref.Path, BundleManifestName))
	if err != nil || digest != ref.ManifestSHA256 {
		return BundleManifest{}, errors.New("offline bundle reference changed during verification")
	}
	return manifest, nil
}

// VerifyOfflineBundleInit authenticates the complete closure before any
// native intent or custody effect and re-observes the neutral Docker cache.
// A populated workspace is handled only by the caller's existing same-intent
// RequireAlreadyApplied path, never granted a fresh installation capability.
func VerifyOfflineBundleInit(ctx context.Context, options Options, ref runtimeprofile.OfflineBundleRef) (Manifest, error) {
	manifest, err := VerifyOfflineBundleRef(ctx, options, ref)
	if err != nil {
		return Manifest{}, err
	}
	options, err = referenceOptions(options, ref)
	if err != nil {
		return Manifest{}, err
	}
	var admission *FreshBundleAdmission
	if err := CleanTarget(options.Target); err == nil {
		admission, err = freshBundleAdmission(options, ref, manifest)
		if err != nil {
			return Manifest{}, err
		}
	} else if !errors.Is(err, ErrNotNeutral) {
		return Manifest{}, err
	}
	neutral, err := VerifyCache(ctx, options, filepath.Join(options.CacheRoot, ManifestName))
	if err != nil {
		return Manifest{}, err
	}
	if admission != nil {
		if err := admission.recheck(); err != nil {
			return Manifest{}, err
		}
	}
	return neutral, nil
}

func validateManifestSource(manifest Manifest) error {
	if manifest.SourceKind == PreviewCandidateSource {
		if manifest.Candidate == nil || manifest.Release != (Release{}) || manifest.Candidate.Version == "" || manifest.Candidate.Version != strings.TrimPrefix(manifest.Candidate.Version, "v") {
			return errors.New("candidate metadata needs a canonical private identity and no public release")
		}
		return nil
	}
	if manifest.Candidate != nil || (manifest.SourceKind != "" && manifest.SourceKind != PublicReleaseSource) {
		return errors.New("neutral metadata has an unknown or mixed source kind")
	}
	if manifest.Release == (Release{}) {
		return errors.New("public neutral metadata requires its release identity")
	}
	return nil
}
