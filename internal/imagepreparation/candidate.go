package imagepreparation

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/tofu"
)

type SourceKind string

const (
	PublicReleaseSource    SourceKind = "public-release"
	PreviewCandidateSource SourceKind = "preview-candidate"
)

// CandidateSelection is an explicit byte selection, never an installation grant.
// Directory is needed only when retaining new bytes during online preparation.
type CandidateSelection struct {
	Directory string
	Selector  releaseindex.PreviewSourceSelector
}

// CandidateSource contains only identities authenticated by the signed index,
// payload and actual process equality. Actions locator IDs are deliberately absent.
type CandidateSource struct {
	Identity  releaseindex.AttestedPreviewCandidateIdentity `json:"identity"`
	Kit       string                                        `json:"kit"`
	Version   string                                        `json:"version"`
	Platform  releaseindex.Platform                         `json:"platform"`
	CLISHA256 string                                        `json:"cliSha256"`
}

func (options Options) candidateSource() bool { return options.SourceKind == PreviewCandidateSource }

func candidateDirectory(options Options) (string, error) {
	if options.Candidate == nil {
		return "", errors.New("candidate admission requires independent exact index/archive/CLI selection")
	}
	return releaseindex.PreviewSourceCacheDirectory(options.CacheRoot, options.Candidate.Selector)
}

func inspectCandidate(options Options, profile Profile, platform Platform, candidate releaseindex.VerifiedPreviewCandidate) (Manifest, error) {
	manifest := Manifest{SchemaVersion: "stackkit.neutral-image/v1", Profile: profile, Platform: platform, SourceKind: PreviewCandidateSource}
	if _, err := bundleModels(profile); err != nil {
		return Manifest{}, err
	}
	selector := options.Candidate.Selector
	err := candidate.Inspect(func(identity releaseindex.AttestedPreviewCandidateIdentity, kit, version string, selected releaseindex.Platform, archive io.Reader) error {
		if kit != profile.Kit || kit != selector.Kit || version != selector.Version || version != strings.TrimPrefix(options.Version, "v") || selected != selector.Platform || selected.OS != platform.OS || selected.Arch != platform.Arch || identity.IndexSHA256 != selector.IndexSHA256 || identity.ArchiveSHA256 != selector.ArchiveSHA256 {
			return errors.New("candidate differs from explicit source, profile, running version or observed host")
		}
		if identity.BuildProfile != releaseindex.CandidateFullLocal || !reflect.DeepEqual(identity.BuildTags, []string{releaseindex.FullLocalBuildTag}) {
			return errors.New("Full Local installation requires an authenticated Full Local compiler variant")
		}
		digests, providers, err := archiveBinaryDigests(archive, identity.BuildProfile)
		if err != nil {
			return err
		}
		if digests["stackkit"] != selector.CLISHA256 {
			return errors.New("candidate archive CLI differs from independent exact CLI selection")
		}
		actual, err := localevidence.CurrentExecutableSHA256()
		if err != nil || strings.TrimPrefix(actual, "sha256:") != selector.CLISHA256 {
			return errors.New("candidate CLI differs from the actual executing process")
		}
		if err := verifyPackagedClosure(options, digests, providers); err != nil {
			return err
		}
		manifest.Binaries, manifest.ProviderFiles = digests, providers
		manifest.Candidate = &CandidateSource{Identity: identity, Kit: kit, Version: version, Platform: selected, CLISHA256: selector.CLISHA256}
		return nil
	})
	return manifest, err
}

func trustedCandidateManifest(ctx context.Context, options Options, profile Profile, platform Platform) (Manifest, error) {
	directory, err := candidateDirectory(options)
	if err != nil {
		return Manifest{}, err
	}
	if err := neutralCacheRoot(options.CacheRoot, directory); err != nil {
		return Manifest{}, err
	}
	if err := candidateCacheFiles(directory, options.Candidate.Selector); err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	err = (releaseindex.SigstoreVerifier{}).InspectPreviewSourceCache(ctx, options.CacheRoot, options.Candidate.Selector, func(candidate releaseindex.VerifiedPreviewCandidate) error {
		var err error
		manifest, err = inspectCandidate(options, profile, platform, candidate)
		return err
	})
	return manifest, err
}

// VerifyCandidateToolDirectory reuses this owner's complete authenticated tool
// closure for the existing historical-source extractor. It grants no fresh
// installation permission and makes no current-process or original-Apply claim.
func VerifyCandidateToolDirectory(candidate releaseindex.VerifiedPreviewCandidate, directory string) error {
	return candidate.Inspect(func(identity releaseindex.AttestedPreviewCandidateIdentity, _, _ string, _ releaseindex.Platform, archive io.Reader) error {
		if identity.BuildProfile != releaseindex.CandidateFullLocal {
			return errors.New("complete Full Local tool verification requires the authenticated compiler variant")
		}
		digests, providers, err := archiveBinaryDigests(archive, identity.BuildProfile)
		if err != nil {
			return err
		}
		return verifyPackagedClosure(Options{Executable: filepath.Join(directory, "stackkit"), SourceKind: PreviewCandidateSource}, digests, providers)
	})
}

func retainCandidate(ctx context.Context, options Options, profile Profile, platform Platform) error {
	directory, err := candidateDirectory(options)
	if err != nil {
		return err
	}
	if err := neutralCacheRoot(options.CacheRoot, directory); err != nil {
		return err
	}
	if options.Candidate.Directory == "" {
		return errors.New("candidate preparation requires --source-candidate-dir")
	}
	if err := SafePath(options.Candidate.Directory); err != nil {
		return err
	}
	if err := candidateCacheFiles(options.Candidate.Directory, options.Candidate.Selector); err != nil {
		return err
	}
	return (releaseindex.SigstoreVerifier{}).RetainPreviewSourceCache(ctx, options.CacheRoot, options.Candidate.Directory, options.Candidate.Selector, func(candidate releaseindex.VerifiedPreviewCandidate) error {
		_, err := inspectCandidate(options, profile, platform, candidate)
		return err
	})
}

// Public and candidate paths share complete packaged-tool and provider admission.
func verifyPackagedClosure(options Options, digests, providers map[string]string) error {
	for binary, digest := range digests {
		file := filepath.Join(filepath.Dir(options.Executable), binary)
		if binary == "stackkit" {
			file = options.Executable
		}
		if options.candidateSource() {
			info, err := os.Lstat(file)
			if err != nil || info.Mode()&0111 == 0 {
				return fmt.Errorf("candidate packaged %s is not executable", binary)
			}
		}
		actual, err := FileDigest(file)
		if err != nil {
			return err
		}
		if actual != digest {
			return fmt.Errorf("%w: packaged %s bytes differ from the authenticated archive", ErrAdmissionMismatch, binary)
		}
	}
	providerRoot, err := installedProviderRoot(options.Executable)
	if err != nil {
		return err
	}
	if _, err := tofu.LoadProviderClosure(providerRoot); err != nil {
		return err
	}
	actualProviders, err := digestTree(providerRoot)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(providers, actualProviders) {
		return errors.New("packaged provider mirror bytes differ from the authenticated archive")
	}
	return nil
}

func verifyArchiveBuildProfile(raw []byte, profile releaseindex.CandidateBuildProfile) error {
	if profile != releaseindex.CandidateFullLocal {
		return nil
	}
	info, err := buildinfo.Read(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("read Full Local compiler settings: %w", err)
	}
	for _, setting := range info.Settings {
		if setting.Key == "-tags" && setting.Value == releaseindex.FullLocalBuildTag {
			return nil
		}
	}
	return errors.New("authenticated Full Local archive contains a binary without the canonical build tag")
}

func candidateCacheFiles(directory string, selector releaseindex.PreviewSourceSelector) error {
	if err := SafePath(directory); err != nil {
		return err
	}
	archive := "stackkits-" + selector.Kit + "_" + selector.Version + "_" + selector.Platform.OS + "_" + selector.Platform.Arch + ".tar.gz"
	allowed := map[string]bool{}
	for _, name := range []string{"candidate.provenance.json", releaseindex.PreviewCandidateIndexName, releaseindex.PreviewCandidateIndexName + ".intoto.jsonl", releaseindex.PreviewCandidatePayloadAttestationName, releaseindex.TrustedRootAssetName, archive, archive + ".spdx.json"} {
		allowed[name] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != len(allowed) {
		return errors.New("neutral candidate source must contain only the selected archive and its complete trust material")
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !allowed[entry.Name()] {
			return errors.New("neutral candidate source contains an unexpected or unsafe input")
		}
	}
	return nil
}
