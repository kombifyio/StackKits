package releaseindex

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type verifiedInstallationToken struct{}

const maxInstalledInspectionBytes int64 = 256 << 20

// VerifiedInstallation is an immutable proof of one offline-verified cached
// release. Its private token and fields make the zero value invalid and prevent
// another package from fabricating or mutating release authority.
type VerifiedInstallation struct {
	token   *verifiedInstallationToken
	receipt Receipt
	asset   Asset
	archive []byte
}

// Inspect exposes defensive copies only after validating the package-private
// proof token and the retained archive digest.
func (installation VerifiedInstallation) Inspect(
	inspect func(Receipt, Asset, io.Reader) error,
) error {
	if installation.token == nil || inspect == nil {
		return fmt.Errorf("valid verified installation proof and inspection callback are required")
	}
	if digestBytes(installation.archive) != installation.asset.Archive.SHA256 ||
		installation.receipt.ArchiveSHA256 != installation.asset.Archive.SHA256 {
		return fmt.Errorf("%w for retained verified installed archive", ErrDigestMismatch)
	}
	return inspect(
		installation.receipt,
		installation.asset,
		bytes.NewReader(installation.archive),
	)
}

// InspectInstalled re-verifies an installed release and returns an immutable
// proof containing the exact bounded archive bytes verified in this operation.
func (installer Installer) InspectInstalled(
	ctx context.Context,
	installDir string,
	inspect func(VerifiedInstallation) error,
) error {
	if inspect == nil {
		return fmt.Errorf("verified installation inspection callback is required")
	}
	receipt, err := installer.VerifyInstalled(ctx, installDir)
	if err != nil {
		return err
	}
	rawIndex, err := readInstalledFileBounded(
		filepath.Join(installDir, ReleaseIndexAssetName),
		maxIndexBytes,
	)
	if err != nil {
		return fmt.Errorf("read verified installed release index: %w", err)
	}
	if digestBytes(rawIndex) != receipt.IndexSHA256 {
		return fmt.Errorf("%w for verified installed release index copy", ErrDigestMismatch)
	}
	index, err := Decode(rawIndex)
	if err != nil {
		return fmt.Errorf("decode verified installed release index: %w", err)
	}
	var asset *Asset
	for position := range index.Assets {
		candidate := &index.Assets[position]
		if candidate.Kit == receipt.Kit &&
			candidate.Version == receipt.Version &&
			candidate.Platform == receipt.Platform {
			asset = candidate
			break
		}
	}
	if asset == nil {
		return fmt.Errorf("verified installed release asset is absent from its index")
	}
	limit := installer.MaxBlobBytes
	if limit <= 0 || limit > maxInstalledInspectionBytes {
		limit = maxInstalledInspectionBytes
	}
	archiveBytes, err := readInstalledFileBounded(
		filepath.Join(installDir, asset.Archive.Name),
		limit,
	)
	if err != nil {
		return fmt.Errorf("read verified installed archive: %w", err)
	}
	if digestBytes(archiveBytes) != asset.Archive.SHA256 {
		return fmt.Errorf("%w for verified installed archive copy", ErrDigestMismatch)
	}
	proof := VerifiedInstallation{
		token: &verifiedInstallationToken{}, receipt: receipt,
		asset: *asset, archive: archiveBytes,
	}
	return inspect(proof)
}

func readInstalledFileBounded(filePath string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("positive installed file limit is required")
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("installed file must be a bounded regular non-symlink file")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return nil, fmt.Errorf("installed file changed before it was read")
	}
	var buffer bytes.Buffer
	if _, err := io.CopyN(&buffer, file, info.Size()+1); err != nil && err != io.EOF {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if int64(buffer.Len()) != info.Size() || after.Size() != opened.Size() ||
		!after.ModTime().Equal(opened.ModTime()) {
		return nil, fmt.Errorf("installed file changed while it was read")
	}
	return buffer.Bytes(), nil
}
