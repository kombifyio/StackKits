package releaseindex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PreviewSourceSelector selects bytes, never installation authority. Every
// readback re-verifies the canonical offline signature policy. Artifact IDs
// retained beside those bytes remain untrusted and are not exported as proof.
type PreviewSourceSelector struct {
	Kit, Version                          string
	Platform                              Platform
	IndexSHA256, ArchiveSHA256, CLISHA256 string
}

type PreviewSourceCacheProof struct {
	SchemaVersion            string                   `json:"schemaVersion"`
	Kit                      string                   `json:"kit"`
	Version                  string                   `json:"version"`
	Platform                 Platform                 `json:"platform"`
	IndexSHA256              string                   `json:"indexSha256"`
	ArchiveSHA256            string                   `json:"archiveSha256"`
	CLISHA256                string                   `json:"cliSha256"`
	IndexAttestationSHA256   string                   `json:"indexAttestationSha256"`
	PayloadAttestationSHA256 string                   `json:"payloadAttestationSha256"`
	TrustedRootSHA256        string                   `json:"trustedRootSha256"`
	SBOMSHA256               string                   `json:"sbomSha256"`
	Source                   PreviewCandidateSource   `json:"source"`
	Producer                 PreviewCandidateProducer `json:"producer"`
	BuildProfile             CandidateBuildProfile    `json:"buildProfile,omitempty"`
	BuildTags                []string                 `json:"buildTags,omitempty"`
}

func PreviewSourceCacheDirectory(cache string, selector PreviewSourceSelector) (string, error) {
	if !filepath.IsAbs(cache) || !sha256Pattern.MatchString(selector.IndexSHA256) || !sha256Pattern.MatchString(selector.ArchiveSHA256) || !sha256Pattern.MatchString(selector.CLISHA256) {
		return "", errors.New("candidate source cache requires absolute custody and exact index/archive/CLI digests")
	}
	// Digests, not a shared descriptive version, identify a private compiler.
	return filepath.Join(cache, "preview-candidates", selector.IndexSHA256, selector.ArchiveSHA256, selector.CLISHA256), nil
}

func readPreviewSource(directory string, selector PreviewSourceSelector) (PreviewCandidateInput, map[string][]byte, error) {
	var input PreviewCandidateInput
	root, err := os.OpenRoot(directory)
	if err != nil {
		return input, nil, err
	}
	defer root.Close()
	files := map[string][]byte{}
	read := func(name string, limit int64) ([]byte, error) {
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
			return nil, fmt.Errorf("candidate source %s must be bounded and regular", name)
		}
		file, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return nil, errors.New("candidate source changed before opening")
		}
		raw, err := io.ReadAll(io.LimitReader(file, limit+1))
		after, statErr := file.Stat()
		if err != nil || statErr != nil || int64(len(raw)) != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return nil, errors.New("candidate source changed while reading")
		}
		files[name] = raw
		return raw, nil
	}
	raw, err := read("candidate.provenance.json", 64<<10)
	if err != nil {
		return input, nil, err
	}
	if err = decodePreviewCandidateJSON(raw, &input.Expected); err != nil {
		return input, nil, err
	}
	if input.Expected.IndexSHA256 != selector.IndexSHA256 {
		return input, nil, errors.New("candidate source index differs from exact selector")
	}
	input.Kit = selector.Kit
	input.Platform = selector.Platform
	input.ArchiveSHA256 = selector.ArchiveSHA256
	for _, item := range []struct {
		name   string
		target *[]byte
		limit  int64
	}{
		{PreviewCandidateIndexName, &input.Index, 1 << 20}, {PreviewCandidateIndexName + ".intoto.jsonl", &input.IndexAttestation, 32 << 20},
		{PreviewCandidatePayloadAttestationName, &input.PayloadAttestation, 64 << 20}, {TrustedRootAssetName, &input.TrustedRoot, 16 << 20},
	} {
		raw, err = read(item.name, item.limit)
		if err != nil {
			return input, nil, err
		}
		*item.target = raw
	}
	var index previewCandidateIndex
	if err = decodePreviewCandidateJSON(input.Index, &index); err != nil {
		return input, nil, err
	}
	if index.DeclaredVersion != strings.TrimPrefix(selector.Version, "v") {
		return input, nil, errors.New("candidate source version differs from selector")
	}
	// Validate selector components before using any in a relative filename.
	if err = input.Expected.Validate(); err != nil {
		return input, nil, err
	}
	if err = validatePreviewSourcePlatform(selector); err != nil {
		return input, nil, err
	}
	extension := ".tar.gz"
	if selector.Platform.OS == "windows" {
		extension = ".zip"
	}
	archive := "stackkits-" + selector.Kit + "_" + index.DeclaredVersion + "_" + selector.Platform.OS + "_" + selector.Platform.Arch + extension
	input.Archive, err = read(archive, 256<<20)
	if err != nil {
		return input, nil, err
	}
	input.SBOM, err = read(archive+".spdx.json", 32<<20)
	if err != nil {
		return input, nil, err
	}
	return input, files, nil
}

func validatePreviewSourcePlatform(selector PreviewSourceSelector) error {
	if selector.Kit != "basement-kit" && selector.Kit != "cloud-kit" && selector.Kit != "modern-homelab" {
		return errors.New("candidate source kit is invalid")
	}
	if (selector.Platform.OS != "linux" && selector.Platform.OS != "windows" && selector.Platform.OS != "darwin") || (selector.Platform.Arch != "amd64" && selector.Platform.Arch != "arm64") {
		return errors.New("candidate source platform is invalid")
	}
	return nil
}

func (verifier SigstoreVerifier) InspectPreviewSourceDirectory(ctx context.Context, directory string, selector PreviewSourceSelector, inspect func(VerifiedPreviewCandidate) error) error {
	if _, err := PreviewSourceCacheDirectory(directory, selector); err != nil {
		return err
	}
	input, _, err := readPreviewSource(directory, selector)
	if err != nil {
		return err
	}
	verified, err := verifier.VerifyPreviewCandidate(ctx, input)
	if err != nil {
		return err
	}
	if inspect == nil {
		return errors.New("candidate source inspection callback is required")
	}
	return inspect(verified)
}

func (verifier SigstoreVerifier) InspectPreviewSourceCache(ctx context.Context, cache string, selector PreviewSourceSelector, inspect func(VerifiedPreviewCandidate) error) error {
	directory, err := PreviewSourceCacheDirectory(cache, selector)
	if err != nil {
		return err
	}
	return verifier.InspectPreviewSourceDirectory(ctx, directory, selector, inspect)
}

// RetainPreviewSourceCache acquires only verified immutable bytes. The caller's
// callback must match the archive executable against its exact source selector.
// Existing entries are verified and refused on corruption, never overwritten.
func (verifier SigstoreVerifier) RetainPreviewSourceCache(ctx context.Context, cache, source string, selector PreviewSourceSelector, inspect func(VerifiedPreviewCandidate) error) error {
	directory, err := PreviewSourceCacheDirectory(cache, selector)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(directory); err == nil {
		return verifier.InspectPreviewSourceDirectory(ctx, directory, selector, inspect)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	input, files, err := readPreviewSource(source, selector)
	if err != nil {
		return err
	}
	verified, err := verifier.VerifyPreviewCandidate(ctx, input)
	if err != nil {
		return err
	}
	if inspect == nil {
		return errors.New("candidate executable verification is required")
	}
	if err = inspect(verified); err != nil {
		return err
	}
	parent := filepath.Dir(directory)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".candidate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	for name, raw := range files {
		if err = os.WriteFile(filepath.Join(temporary, name), raw, 0600); err != nil {
			return err
		}
	}
	if err = os.Rename(temporary, directory); err != nil {
		// A concurrent retainer may have committed these exact bytes first.
		// Re-inspection authenticates that winner; no corrupt entry is repaired.
		if _, statErr := os.Lstat(directory); statErr == nil {
			return verifier.InspectPreviewSourceDirectory(ctx, directory, selector, inspect)
		}
		return err
	}
	return verifier.InspectPreviewSourceDirectory(ctx, directory, selector, inspect)
}

func (candidate VerifiedPreviewCandidate) SourceCacheProof(cliSHA256 string) (PreviewSourceCacheProof, error) {
	if candidate.token == nil || !sha256Pattern.MatchString(cliSHA256) {
		return PreviewSourceCacheProof{}, errors.New("verified candidate and exact executable digest required")
	}
	expected := candidate.input.Expected
	profile := candidate.buildProfile
	if profile == CandidateStandard {
		profile = "" // Preserve the historical Standard source-cache result shape.
	}
	return PreviewSourceCacheProof{SchemaVersion: "stackkit.preview-source-cache/v1", Kit: candidate.input.Kit, Version: candidate.version, Platform: candidate.input.Platform,
		IndexSHA256: expected.IndexSHA256, ArchiveSHA256: candidate.input.ArchiveSHA256, CLISHA256: cliSHA256,
		IndexAttestationSHA256: expected.IndexAttestationSHA256, PayloadAttestationSHA256: expected.PayloadAttestationSHA256,
		TrustedRootSHA256: expected.TrustedRootSHA256, SBOMSHA256: candidate.sbomSHA256, Source: expected.Source, Producer: expected.Producer,
		BuildProfile: profile, BuildTags: append([]string(nil), candidate.buildTags...)}, nil
}
