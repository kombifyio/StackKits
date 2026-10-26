package releaseindex

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/productkits"
)

// PreviewSourceSelectorFromIndex resolves metadata only. A deployment's kit
// label is not necessarily the packaged bootstrap archive's kit. The exact
// independently selected index/archive digests choose one asset; the opaque
// Sigstore verifier and executable equality still decide whether it is proof.
func PreviewSourceSelectorFromIndex(raw []byte, version string, platform Platform, indexSHA256, archiveSHA256, cliSHA256 string) (PreviewSourceSelector, error) {
	var selector PreviewSourceSelector
	if len(raw) == 0 || len(raw) > 1<<20 || !sha256Pattern.MatchString(indexSHA256) || digestBytes(raw) != indexSHA256 || !sha256Pattern.MatchString(archiveSHA256) || !sha256Pattern.MatchString(cliSHA256) {
		return selector, errors.New("source selector needs exact bounded index/archive/CLI identity")
	}
	var index previewCandidateIndex
	if err := decodePreviewCandidateJSON(raw, &index); err != nil {
		return selector, err
	}
	if index.DeclaredVersion != strings.TrimPrefix(version, "v") || len(index.Assets) < 2 || len(index.Assets) > 128 {
		return selector, errors.New("source selector differs from the candidate index version")
	}
	extension := ".tar.gz"
	if platform.OS == "windows" {
		extension = ".zip"
	}
	suffix := "_" + index.DeclaredVersion + "_" + platform.OS + "_" + platform.Arch + extension
	found := 0
	kit := ""
	seen := map[string]bool{}
	for _, asset := range index.Assets {
		key := strings.ToLower(asset.Name)
		if seen[key] {
			return selector, errors.New("candidate source index has duplicate assets")
		}
		seen[key] = true
		if asset.SHA256 != archiveSHA256 || !strings.HasSuffix(asset.Name, suffix) {
			continue
		}
		found++
		name, ok := strings.CutPrefix(asset.Name, "stackkits-")
		if !ok {
			return selector, errors.New("selected source archive has no canonical kit identity")
		}
		kit = strings.TrimSuffix(name, suffix)
		if err := productkits.Validate(kit); err != nil {
			return selector, err
		}
	}
	if found != 1 {
		return selector, errors.New("candidate source archive digest must identify exactly one canonical index asset")
	}
	selector = PreviewSourceSelector{Kit: kit, Version: index.DeclaredVersion, Platform: platform, IndexSHA256: indexSHA256, ArchiveSHA256: archiveSHA256, CLISHA256: cliSHA256}
	if err := validatePreviewSourcePlatform(selector); err != nil {
		return PreviewSourceSelector{}, err
	}
	return selector, nil
}
