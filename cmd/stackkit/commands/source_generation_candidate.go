package commands

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"golang.org/x/mod/semver"
)

func preparePreviewSourceCache(ctx context.Context, request sourceReleaseCacheRequest, directory string) (releaseindex.PreviewSourceCacheProof, error) {
	var output releaseindex.PreviewSourceCacheProof
	current, err := sourceGenerationCompilerVersion()
	tag := "v" + strings.TrimPrefix(request.Tag, "v")
	if err != nil || !semver.IsValid(tag) || semver.Canonical(tag) != tag || semver.Prerelease(tag) != "" || semver.Compare(tag, current) > 0 || !filepath.IsAbs(directory) {
		return output, errors.New("candidate source cache needs an exact installed source version and absolute retained directory")
	}
	selector := releaseindex.PreviewSourceSelector{Kit: request.Kit, Version: strings.TrimPrefix(tag, "v"), Platform: request.Platform, IndexSHA256: request.IndexSHA256, ArchiveSHA256: request.ArchiveSHA256, CLISHA256: request.CLISHA256}
	err = (releaseindex.SigstoreVerifier{}).RetainPreviewSourceCache(ctx, request.Directory, directory, selector, func(proof releaseindex.VerifiedPreviewCandidate) error {
		cli, _, err := upgradelifecycle.PreviewExecutablesFromVerifiedCandidate(proof)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(cli)) != request.CLISHA256 {
			return errors.New("candidate source CLI differs from its verified archive")
		}
		output, err = proof.SourceCacheProof(request.CLISHA256)
		return err
	})
	return output, err
}
