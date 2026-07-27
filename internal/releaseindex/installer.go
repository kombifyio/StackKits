package releaseindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	tempPrefix             = ".install-"
	maxReleaseReceiptBytes = int64(1 << 20)
)

type Installer struct {
	Source       Source
	Attestations AttestationVerifier
	MaxBlobBytes int64
	Now          func() time.Time
}

// VerifiedArchive is a short-lived, fully verified release payload. Paths are
// valid only for the duration of InspectVerifiedArchive's callback.
type VerifiedArchive struct {
	ArchivePath     string
	SBOMPath        string
	AttestationPath string
	TrustedRootPath string
}

// InspectVerifiedArchive downloads and verifies one resolved release in a
// process-owned temporary directory without installing or caching anything in
// the Stack workspace. The callback cannot retain the paths after it returns.
func (installer Installer) InspectVerifiedArchive(ctx context.Context, resolution Resolution, inspect func(VerifiedArchive) error) error {
	if installer.Source == nil || installer.Attestations == nil {
		return fmt.Errorf("release source and attestation verifier are required")
	}
	if inspect == nil {
		return fmt.Errorf("verified archive inspection callback is required")
	}
	if err := resolution.Index.Validate(); err != nil {
		return fmt.Errorf("validate resolved release index: %w", err)
	}
	if len(resolution.RawIndex) == 0 || len(resolution.RawIndexAttestation) == 0 || len(resolution.RawTrustedRoot) == 0 {
		return fmt.Errorf("resolved release index, index attestation, and trusted root are required")
	}
	if err := installer.Attestations.VerifyIndex(ctx, IndexAttestationInput{
		Version: resolution.Release.TagName, Index: resolution.RawIndex,
		Bundle: resolution.RawIndexAttestation, TrustedRoot: resolution.RawTrustedRoot,
	}); err != nil {
		return fmt.Errorf("verify resolved release index attestation: %w", err)
	}
	if digestBytes(resolution.RawTrustedRoot) != resolution.Index.Release.TrustedRoot.SHA256 {
		return fmt.Errorf("%w for %s", ErrDigestMismatch, TrustedRootAssetName)
	}
	limit := installer.MaxBlobBytes
	if limit <= 0 {
		limit = DefaultMaxBlobBytes
	}
	stage, err := os.MkdirTemp("", "stackkit-release-inspection-")
	if err != nil {
		return fmt.Errorf("create bounded release inspection directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(
		filepath.Join(stage, ReleaseIndexAssetName),
		resolution.RawIndex,
		0o600,
	); err != nil {
		return fmt.Errorf("stage release index: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(stage, ReleaseIndexAttestationAssetName),
		resolution.RawIndexAttestation,
		0o600,
	); err != nil {
		return fmt.Errorf("stage release index attestation: %w", err)
	}
	trustedRootPath := filepath.Join(stage, TrustedRootAssetName)
	if err := os.WriteFile(trustedRootPath, resolution.RawTrustedRoot, 0o600); err != nil {
		return fmt.Errorf("stage trusted root: %w", err)
	}
	archivePath, err := installer.fetchVerified(ctx, resolution.Asset.Archive, stage, limit)
	if err != nil {
		return err
	}
	sbomPath, err := installer.fetchVerified(ctx, resolution.Asset.SBOM, stage, limit)
	if err != nil {
		return err
	}
	attestationPath, err := installer.fetchVerified(ctx, resolution.Asset.Attestation.Blob, stage, limit)
	if err != nil {
		return err
	}
	if err := installer.Attestations.Verify(ctx, AttestationInput{
		Index: resolution.Index, Asset: resolution.Asset, ArchivePath: archivePath, SBOMPath: sbomPath,
		BundlePath: attestationPath, TrustedRootPath: trustedRootPath,
	}); err != nil {
		return fmt.Errorf("verify GitHub OIDC attestation: %w", err)
	}
	if err := revalidateStagedReleaseTrustSet(
		resolution,
		stage,
		archivePath,
		sbomPath,
		attestationPath,
		trustedRootPath,
		limit,
	); err != nil {
		return err
	}
	return inspect(VerifiedArchive{
		ArchivePath: archivePath, SBOMPath: sbomPath,
		AttestationPath: attestationPath, TrustedRootPath: trustedRootPath,
	})
}

func (installer Installer) Install(ctx context.Context, resolution Resolution, workspaceRoot string) (Receipt, error) {
	return installer.install(ctx, resolution, workspaceRoot, nil)
}

type verifiedArchiveValidator func([]byte, Asset) error

func (installer Installer) install(
	ctx context.Context,
	resolution Resolution,
	workspaceRoot string,
	validateArchive verifiedArchiveValidator,
) (Receipt, error) {
	if installer.Attestations == nil {
		return Receipt{}, fmt.Errorf("attestation verifier is required")
	}
	workspaceRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return Receipt{}, fmt.Errorf("resolve release workspace: %w", err)
	}
	if err := resolution.Index.Validate(); err != nil {
		return Receipt{}, fmt.Errorf("validate resolved release index: %w", err)
	}
	limit := installer.MaxBlobBytes
	if limit <= 0 {
		limit = DefaultMaxBlobBytes
	}
	now := installer.Now
	if now == nil {
		now = time.Now
	}
	releaseRoot := filepath.Join(workspaceRoot, ".stackkit", "releases")
	target := filepath.Join(releaseRoot, resolution.Asset.Kit, resolution.Asset.Version, resolution.Asset.Platform.OS+"-"+resolution.Asset.Platform.Arch)
	if err := ensureReleaseCachePathConfined(workspaceRoot, target); err != nil {
		return Receipt{}, err
	}
	if _, err := os.Stat(filepath.Join(target, ReleaseReceiptName)); err == nil {
		receipt, verifyErr := installer.VerifyInstalled(ctx, target)
		if verifyErr != nil {
			return Receipt{}, verifyErr
		}
		if verifyErr := verifyReceiptMatchesResolution(receipt, resolution); verifyErr != nil {
			return Receipt{}, verifyErr
		}
		if validateArchive != nil {
			archiveBytes, readErr := readInstalledFileBounded(
				filepath.Join(target, resolution.Asset.Archive.Name),
				limit,
			)
			if readErr != nil {
				return Receipt{}, fmt.Errorf("read cached release archive snapshot: %w", readErr)
			}
			if digestBytes(archiveBytes) != receipt.ArchiveSHA256 ||
				receipt.ArchiveSHA256 != resolution.Asset.Archive.SHA256 {
				return Receipt{}, fmt.Errorf("%w for cached release archive snapshot", ErrDigestMismatch)
			}
			if verifyErr := validateArchive(archiveBytes, resolution.Asset); verifyErr != nil {
				return Receipt{}, verifyErr
			}
		}
		return receipt, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Receipt{}, fmt.Errorf("read existing release receipt: %w", err)
	}
	if installer.Source == nil {
		return Receipt{}, fmt.Errorf("release source is required for an uncached release")
	}

	stage, err := os.MkdirTemp(workspaceRoot, tempPrefix)
	if err != nil {
		return Receipt{}, fmt.Errorf("create bounded release staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	if len(resolution.RawIndex) == 0 || len(resolution.RawIndexAttestation) == 0 || len(resolution.RawTrustedRoot) == 0 {
		return Receipt{}, fmt.Errorf("resolved release index, index attestation, and trusted root are required")
	}
	if err := installer.Attestations.VerifyIndex(ctx, IndexAttestationInput{
		Version: resolution.Release.TagName, Index: resolution.RawIndex,
		Bundle: resolution.RawIndexAttestation, TrustedRoot: resolution.RawTrustedRoot,
	}); err != nil {
		return Receipt{}, fmt.Errorf("verify resolved release index attestation: %w", err)
	}
	if digestBytes(resolution.RawTrustedRoot) != resolution.Index.Release.TrustedRoot.SHA256 {
		return Receipt{}, fmt.Errorf("%w for %s", ErrDigestMismatch, TrustedRootAssetName)
	}
	if err := os.WriteFile(filepath.Join(stage, ReleaseIndexAssetName), resolution.RawIndex, 0o600); err != nil {
		return Receipt{}, fmt.Errorf("cache release index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, ReleaseIndexAttestationAssetName), resolution.RawIndexAttestation, 0o600); err != nil {
		return Receipt{}, fmt.Errorf("cache release index attestation: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, TrustedRootAssetName), resolution.RawTrustedRoot, 0o600); err != nil {
		return Receipt{}, fmt.Errorf("cache trusted root: %w", err)
	}

	archivePath, err := installer.fetchVerified(ctx, resolution.Asset.Archive, stage, limit)
	if err != nil {
		return Receipt{}, err
	}
	sbomPath, err := installer.fetchVerified(ctx, resolution.Asset.SBOM, stage, limit)
	if err != nil {
		return Receipt{}, err
	}
	attestationPath, err := installer.fetchVerified(ctx, resolution.Asset.Attestation.Blob, stage, limit)
	if err != nil {
		return Receipt{}, err
	}
	trustedRootPath := filepath.Join(stage, TrustedRootAssetName)
	if err := installer.Attestations.Verify(ctx, AttestationInput{
		Index: resolution.Index, Asset: resolution.Asset, ArchivePath: archivePath, SBOMPath: sbomPath,
		BundlePath: attestationPath, TrustedRootPath: trustedRootPath,
	}); err != nil {
		return Receipt{}, fmt.Errorf("verify GitHub OIDC attestation: %w", err)
	}
	if validateArchive != nil {
		archiveBytes, readErr := readInstalledFileBounded(archivePath, limit)
		if readErr != nil {
			return Receipt{}, fmt.Errorf("read staged release archive snapshot: %w", readErr)
		}
		if digestBytes(archiveBytes) != resolution.Asset.Archive.SHA256 {
			return Receipt{}, fmt.Errorf("%w for staged release archive snapshot", ErrDigestMismatch)
		}
		if err := validateArchive(archiveBytes, resolution.Asset); err != nil {
			return Receipt{}, err
		}
	}
	receipt := Receipt{
		SchemaVersion: ReceiptSchemaVersion, Kit: resolution.Asset.Kit, Version: resolution.Asset.Version,
		Channel: resolution.Asset.Channel, Platform: resolution.Asset.Platform,
		ArchiveSHA256: resolution.Asset.Archive.SHA256, SBOMSHA256: resolution.Asset.SBOM.SHA256,
		AttestationSHA256: resolution.Asset.Attestation.SHA256,
		AttestationIssuer: resolution.Asset.Attestation.Issuer, AttestationSubject: resolution.Asset.Attestation.Subject,
		TrustedRootSHA256:      resolution.Index.Release.TrustedRoot.SHA256,
		IndexSHA256:            digestBytes(resolution.RawIndex),
		IndexAttestationSHA256: digestBytes(resolution.RawIndexAttestation),
		VerifiedAt:             now().UTC(), InstallDir: target,
	}
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return Receipt{}, err
	}
	receiptBytes = append(receiptBytes, '\n')
	if err := os.WriteFile(filepath.Join(stage, ReleaseReceiptName), receiptBytes, 0o600); err != nil {
		return Receipt{}, fmt.Errorf("write release receipt: %w", err)
	}
	if err := revalidateStagedReleaseTrustSet(
		resolution,
		stage,
		archivePath,
		sbomPath,
		attestationPath,
		trustedRootPath,
		limit,
	); err != nil {
		return Receipt{}, err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Receipt{}, fmt.Errorf("create release parent: %w", err)
	}
	if err := ensureReleaseCachePathConfined(workspaceRoot, parent); err != nil {
		return Receipt{}, err
	}
	if err := os.Rename(stage, target); err != nil {
		return Receipt{}, fmt.Errorf("atomically install verified release: %w", err)
	}
	return receipt, nil
}

func revalidateStagedReleaseTrustSet(
	resolution Resolution,
	stage string,
	archivePath string,
	sbomPath string,
	attestationPath string,
	trustedRootPath string,
	blobLimit int64,
) error {
	checks := []struct {
		path     string
		expected string
		limit    int64
	}{
		{
			path:     filepath.Join(stage, ReleaseIndexAssetName),
			expected: digestBytes(resolution.RawIndex),
			limit:    maxIndexBytes,
		},
		{
			path:     filepath.Join(stage, ReleaseIndexAttestationAssetName),
			expected: digestBytes(resolution.RawIndexAttestation),
			limit:    maxIndexBytes,
		},
		{
			path:     trustedRootPath,
			expected: resolution.Index.Release.TrustedRoot.SHA256,
			limit:    maxIndexBytes,
		},
		{
			path:     archivePath,
			expected: resolution.Asset.Archive.SHA256,
			limit:    blobLimit,
		},
		{
			path:     sbomPath,
			expected: resolution.Asset.SBOM.SHA256,
			limit:    blobLimit,
		},
		{
			path:     attestationPath,
			expected: resolution.Asset.Attestation.SHA256,
			limit:    blobLimit,
		},
	}
	for _, check := range checks {
		if err := verifyFileDigest(check.path, check.expected, check.limit); err != nil {
			return fmt.Errorf("revalidate staged release trust set: %w", err)
		}
	}
	return nil
}

func verifyReceiptMatchesResolution(receipt Receipt, resolution Resolution) error {
	if receipt.Kit != resolution.Asset.Kit ||
		receipt.Version != resolution.Asset.Version ||
		receipt.Channel != resolution.Asset.Channel ||
		receipt.Platform != resolution.Asset.Platform ||
		receipt.ArchiveSHA256 != resolution.Asset.Archive.SHA256 ||
		receipt.SBOMSHA256 != resolution.Asset.SBOM.SHA256 ||
		receipt.AttestationSHA256 != resolution.Asset.Attestation.SHA256 ||
		receipt.AttestationIssuer != resolution.Asset.Attestation.Issuer ||
		receipt.AttestationSubject != resolution.Asset.Attestation.Subject ||
		receipt.TrustedRootSHA256 != resolution.Index.Release.TrustedRoot.SHA256 ||
		receipt.IndexSHA256 != digestBytes(resolution.RawIndex) ||
		receipt.IndexAttestationSHA256 != digestBytes(resolution.RawIndexAttestation) {
		return fmt.Errorf("cached release receipt does not match the exact resolved release")
	}
	return nil
}

func (installer Installer) VerifyInstalled(ctx context.Context, installDir string) (Receipt, error) {
	if installer.Attestations == nil {
		return Receipt{}, fmt.Errorf("attestation verifier is required")
	}
	info, err := os.Lstat(installDir)
	if err != nil {
		return Receipt{}, fmt.Errorf("inspect installed release directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Receipt{}, fmt.Errorf("installed release path must be a real directory")
	}
	receipt, err := readReceipt(filepath.Join(installDir, ReleaseReceiptName))
	if err != nil {
		return Receipt{}, fmt.Errorf("read release receipt: %w", err)
	}
	if filepath.Clean(receipt.InstallDir) != filepath.Clean(installDir) {
		return Receipt{}, fmt.Errorf("release receipt installDir does not match its cache location")
	}
	if filepath.Base(filepath.Dir(installDir)) != receipt.Version {
		return Receipt{}, fmt.Errorf("release receipt version does not match its cache location")
	}
	indexPath := filepath.Join(installDir, ReleaseIndexAssetName)
	indexAttestationPath := filepath.Join(installDir, ReleaseIndexAttestationAssetName)
	trustedRootPath := filepath.Join(installDir, TrustedRootAssetName)
	rawIndex, err := readInstalledFileBounded(indexPath, maxIndexBytes)
	if err != nil {
		return Receipt{}, fmt.Errorf("read cached release index: %w", err)
	}
	rawIndexAttestation, err := readInstalledFileBounded(indexAttestationPath, maxIndexBytes)
	if err != nil {
		return Receipt{}, fmt.Errorf("read cached release index attestation: %w", err)
	}
	rawTrustedRoot, err := readInstalledFileBounded(trustedRootPath, maxIndexBytes)
	if err != nil {
		return Receipt{}, fmt.Errorf("read cached trusted root: %w", err)
	}
	if digestBytes(rawIndex) != receipt.IndexSHA256 ||
		digestBytes(rawIndexAttestation) != receipt.IndexAttestationSHA256 ||
		digestBytes(rawTrustedRoot) != receipt.TrustedRootSHA256 {
		return Receipt{}, fmt.Errorf("%w for cached release trust metadata", ErrDigestMismatch)
	}
	if err := installer.Attestations.VerifyIndex(ctx, IndexAttestationInput{
		Version: receipt.Version, Index: rawIndex, Bundle: rawIndexAttestation, TrustedRoot: rawTrustedRoot,
	}); err != nil {
		return Receipt{}, fmt.Errorf("verify cached release index attestation: %w", err)
	}
	index, err := Decode(rawIndex)
	if err != nil {
		return Receipt{}, fmt.Errorf("verify cached release index: %w", err)
	}
	var asset *Asset
	for position := range index.Assets {
		candidate := &index.Assets[position]
		if candidate.Kit == receipt.Kit && candidate.Version == receipt.Version && candidate.Platform == receipt.Platform {
			asset = candidate
			break
		}
	}
	if asset == nil {
		return Receipt{}, fmt.Errorf("receipt target is absent from cached release index")
	}
	if receipt.Channel != asset.Channel ||
		receipt.ArchiveSHA256 != asset.Archive.SHA256 ||
		receipt.SBOMSHA256 != asset.SBOM.SHA256 ||
		receipt.AttestationSHA256 != asset.Attestation.SHA256 ||
		receipt.AttestationIssuer != asset.Attestation.Issuer ||
		receipt.AttestationSubject != asset.Attestation.Subject ||
		receipt.TrustedRootSHA256 != index.Release.TrustedRoot.SHA256 {
		return Receipt{}, fmt.Errorf("release receipt does not match cached release index")
	}
	limit := installer.MaxBlobBytes
	if limit <= 0 {
		limit = DefaultMaxBlobBytes
	}
	for _, blob := range []Blob{asset.Archive, asset.SBOM, asset.Attestation.Blob} {
		path := filepath.Join(installDir, blob.Name)
		if err := verifyFileDigest(path, blob.SHA256, limit); err != nil {
			return Receipt{}, err
		}
	}
	if err := installer.Attestations.Verify(ctx, AttestationInput{
		Index: index, Asset: *asset,
		ArchivePath:     filepath.Join(installDir, asset.Archive.Name),
		SBOMPath:        filepath.Join(installDir, asset.SBOM.Name),
		BundlePath:      filepath.Join(installDir, asset.Attestation.Name),
		TrustedRootPath: trustedRootPath,
	}); err != nil {
		return Receipt{}, fmt.Errorf("verify cached GitHub OIDC attestation: %w", err)
	}
	return receipt, nil
}

// VerifyWorkspace re-verifies every cached release receipt for one kit and
// platform without consulting the release source or making network requests.
func (installer Installer) VerifyWorkspace(ctx context.Context, workspaceRoot, kit string, platform Platform) ([]Receipt, error) {
	if strings.TrimSpace(kit) == "" || filepath.Base(kit) != kit {
		return nil, fmt.Errorf("safe kit name is required")
	}
	if strings.TrimSpace(platform.OS) == "" || strings.TrimSpace(platform.Arch) == "" {
		return nil, fmt.Errorf("release platform is required")
	}
	kitRoot := filepath.Join(workspaceRoot, ".stackkit", "releases", kit)
	versions, err := os.ReadDir(kitRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w in local cache for kit=%s platform=%s/%s", ErrNoRelease, kit, platform.OS, platform.Arch)
		}
		return nil, fmt.Errorf("read cached release versions: %w", err)
	}
	platformDir := platform.OS + "-" + platform.Arch
	receipts := make([]Receipt, 0, len(versions))
	for _, version := range versions {
		if !version.IsDir() || version.Type()&os.ModeSymlink != 0 {
			continue
		}
		installDir := filepath.Join(kitRoot, version.Name(), platformDir)
		info, statErr := os.Lstat(installDir)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("inspect cached release %s: %w", version.Name(), statErr)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("cached release %s platform path must be a real directory", version.Name())
		}
		receipt, verifyErr := installer.VerifyInstalled(ctx, installDir)
		if verifyErr != nil {
			return nil, fmt.Errorf("verify cached release %s: %w", version.Name(), verifyErr)
		}
		receipts = append(receipts, receipt)
	}
	if len(receipts) == 0 {
		return nil, fmt.Errorf("%w in local cache for kit=%s platform=%s/%s", ErrNoRelease, kit, platform.OS, platform.Arch)
	}
	sort.Slice(receipts, func(left, right int) bool {
		return receipts[left].Version < receipts[right].Version
	})
	return receipts, nil
}

func (installer Installer) fetchVerified(ctx context.Context, blob Blob, stage string, limit int64) (string, error) {
	data, err := installer.Source.Fetch(ctx, blob.URL, limit)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", blob.Name, err)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if actual != blob.SHA256 {
		return "", fmt.Errorf("%w for %s: expected %s, got %s", ErrDigestMismatch, blob.Name, blob.SHA256, actual)
	}
	path := filepath.Join(stage, blob.Name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("stage %s: %w", blob.Name, err)
	}
	return path, nil
}

func readReceipt(path string) (Receipt, error) {
	data, err := readInstalledFileBounded(path, maxReleaseReceiptBytes)
	if err != nil {
		return Receipt{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return Receipt{}, fmt.Errorf("release receipt contains trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return Receipt{}, fmt.Errorf("decode release receipt trailing data: %w", err)
	}
	if receipt.SchemaVersion != ReceiptSchemaVersion {
		return Receipt{}, fmt.Errorf("unsupported receipt schema %q", receipt.SchemaVersion)
	}
	return receipt, nil
}

func verifyFileDigest(path, expected string, limit int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect cached release blob %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > limit {
		return fmt.Errorf("cached release blob %s must be a bounded regular file", filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open cached release blob %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return fmt.Errorf("cached release blob %s changed before it was read", filepath.Base(path))
	}
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, info.Size()+1))
	if err != nil {
		return fmt.Errorf("read cached release blob %s: %w", filepath.Base(path), err)
	}
	after, err := file.Stat()
	if err != nil || written != info.Size() || after.Size() != info.Size() ||
		!after.ModTime().Equal(opened.ModTime()) {
		return fmt.Errorf("cached release blob %s changed while it was read", filepath.Base(path))
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if actual != expected {
		return fmt.Errorf("%w for cached %s: expected %s, got %s", ErrDigestMismatch, filepath.Base(path), expected, actual)
	}
	return nil
}

func ensureReleaseCachePathConfined(workspaceRoot, candidate string) error {
	workspaceRoot = filepath.Clean(workspaceRoot)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(workspaceRoot, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("release cache path escapes the workspace")
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolve release cache workspace: %w", err)
	}
	current := workspaceRoot
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect release cache path: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("release cache path must contain only real directories")
		}
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr != nil {
			return fmt.Errorf("resolve release cache path: %w", resolveErr)
		}
		resolvedRelative, relativeErr := filepath.Rel(resolvedWorkspace, resolved)
		if relativeErr != nil || resolvedRelative == ".." ||
			strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release cache path escapes the workspace through a link or junction")
		}
	}
	return nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
