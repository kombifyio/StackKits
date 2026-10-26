package upgradelifecycle

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/imagepreparation"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/tofu"
)

func PreviewExecutablesFromVerifiedCandidate(proof releaseindex.VerifiedPreviewCandidate) (cli, server []byte, err error) {
	return previewExecutables(proof, "", "")
}

// PreviewExecutablesWithOpenTofu stages only the verified candidate's tooling.
// workspace is existing Owner custody, never inferred from a public Receipt.
func PreviewExecutablesWithOpenTofu(proof releaseindex.VerifiedPreviewCandidate, dir, workspace string) (cli, server []byte, err error) {
	if !filepath.IsAbs(dir) || !filepath.IsAbs(workspace) {
		return nil, nil, errors.New("candidate tooling needs absolute staging and workspace custody")
	}
	return previewExecutables(proof, dir, workspace)
}

func previewExecutables(proof releaseindex.VerifiedPreviewCandidate, dir, workspace string) (cli, server []byte, err error) {
	err = proof.Inspect(func(identity releaseindex.AttestedPreviewCandidateIdentity, kit, version string, platform releaseindex.Platform, archive io.Reader) error {
		extension := ".tar.gz"
		if platform.OS == "windows" {
			extension = ".zip"
		}
		name := "stackkits-" + kit + "_" + version + "_" + platform.OS + "_" + platform.Arch + extension
		var inner error
		cli, server, inner = inspectVerifiedArchiveExecutables(archive, name, identity.ArchiveSHA256, platform, dir, func(extract string) error {
			if identity.BuildProfile == releaseindex.CandidateFullLocal {
				// Source inspection keeps its own complete temporary tool closure.
				// Full Local never accepts the historical optional-provider path.
				if err := imagepreparation.VerifyCandidateToolDirectory(proof, extract); err != nil {
					return err
				}
				for _, binary := range []string{"stackkit-mcp", "stackkit-content-bridge", "terramate"} {
					if err := copyProviderMirrorFile(filepath.Join(extract, binary), filepath.Join(dir, binary), 0700); err != nil {
						return err
					}
				}
				mirror := filepath.Join(dir, tofu.ProvidersDirName)
				if err := os.Mkdir(mirror, 0700); err != nil {
					return err
				}
				if err := copyProviderMirrorTree(filepath.Join(extract, tofu.ProvidersDirName), mirror); err != nil {
					return err
				}
				return writeReleaseProviderMirrorFile(dir, mirror)
			}
			mirror := filepath.Join(extract, tofu.ProvidersDirName)
			if _, err := os.Lstat(mirror); errors.Is(err, os.ErrNotExist) {
				return nil
			}
			manifest, err := tofu.ReadProviderManifest(mirror)
			if err != nil {
				return err
			}
			retained, err := ensureDurableProviderMirror(workspace, mirror, manifest)
			if err != nil {
				return err
			}
			return writeReleaseProviderMirrorFile(dir, retained)
		})
		return inner
	})
	return cli, server, err
}
