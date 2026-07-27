package releaseindex

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxCurrentExecutableBytes = int64(512 << 20)
	maxReleaseArchiveFiles    = 20_000
	maxReleaseExtractedBytes  = int64(1 << 30)
)

// ExecutableIdentity names the exact running StackKit binary and the version
// embedded into it by the release build.
type ExecutableIdentity struct {
	Path         string
	BuildVersion string
}

type executableDigest struct {
	sha256 string
	size   int64
}

// ExactTagForBuildVersion converts the release build value embedded by
// GoReleaser into the one exact GitHub tag that may authorize the binary.
func ExactTagForBuildVersion(buildVersion string) (string, error) {
	normalized := normalizeReleaseBuildVersion(buildVersion)
	if normalized == "" {
		return "", fmt.Errorf(
			"build version %q is not an exact stable, beta, or edge StackKits release",
			buildVersion,
		)
	}
	return "v" + normalized, nil
}

// InstallCurrentExecutable verifies that the canonical stackkit executable in
// the resolved, attested archive is byte-identical to identity.Path before the
// release receipt and cache are committed atomically.
func (installer Installer) InstallCurrentExecutable(
	ctx context.Context,
	resolution Resolution,
	workspaceRoot string,
	identity ExecutableIdentity,
) (Receipt, error) {
	expectedVersion := normalizeReleaseBuildVersion(identity.BuildVersion)
	if expectedVersion == "" || expectedVersion != normalizeReleaseBuildVersion(resolution.Asset.Version) {
		return Receipt{}, fmt.Errorf(
			"current executable build version %q does not match exact release %q",
			identity.BuildVersion,
			resolution.Asset.Version,
		)
	}
	current, err := digestBoundedRegularFile(
		identity.Path,
		"current StackKit executable",
		maxCurrentExecutableBytes,
	)
	if err != nil {
		return Receipt{}, err
	}
	return installer.install(
		ctx,
		resolution,
		workspaceRoot,
		func(archiveBytes []byte, asset Asset) error {
			published, err := digestCanonicalArchiveExecutableBytes(archiveBytes, asset)
			if err != nil {
				return fmt.Errorf("verify canonical executable in release archive: %w", err)
			}
			if published != current {
				return fmt.Errorf("current StackKit executable differs from verified release")
			}
			return nil
		},
	)
}

// VerifyCurrentExecutable re-verifies one cached exact release and binds its
// canonical executable to identity.Path without consulting a release source.
func (installer Installer) VerifyCurrentExecutable(
	ctx context.Context,
	installDir string,
	identity ExecutableIdentity,
) (Receipt, error) {
	var verifiedReceipt Receipt
	err := installer.InspectInstalled(ctx, installDir, func(installation VerifiedInstallation) error {
		receipt := installation.receipt
		if normalizeReleaseBuildVersion(identity.BuildVersion) == "" ||
			normalizeReleaseBuildVersion(identity.BuildVersion) != normalizeReleaseBuildVersion(receipt.Version) {
			return fmt.Errorf(
				"current executable build version %q does not match cached release %q",
				identity.BuildVersion,
				receipt.Version,
			)
		}
		current, digestErr := digestBoundedRegularFile(
			identity.Path,
			"current StackKit executable",
			maxCurrentExecutableBytes,
		)
		if digestErr != nil {
			return digestErr
		}
		published, digestErr := digestCanonicalArchiveExecutableBytes(
			installation.archive,
			installation.asset,
		)
		if digestErr != nil {
			return fmt.Errorf("verify canonical executable in cached release archive: %w", digestErr)
		}
		if published != current {
			return fmt.Errorf("current StackKit executable differs from verified release")
		}
		verifiedReceipt = receipt
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return verifiedReceipt, nil
}

func normalizeReleaseBuildVersion(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "v") {
		value = value[1:]
	}
	if value == "" || strings.Contains(value, "+") {
		return ""
	}
	parsed, err := parseVersion(value)
	if err != nil || parsed.channel() == "" {
		return ""
	}
	return value
}

func digestBoundedRegularFile(filePath, label string, limit int64) (executableDigest, error) {
	if strings.TrimSpace(filePath) == "" {
		return executableDigest{}, fmt.Errorf("%s path is required", label)
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return executableDigest{}, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > limit {
		return executableDigest{}, fmt.Errorf("%s must be a bounded regular non-symlink file", label)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return executableDigest{}, fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return executableDigest{}, fmt.Errorf("inspect opened %s: %w", label, err)
	}
	if !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return executableDigest{}, fmt.Errorf("%s changed before it was read", label)
	}
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, info.Size()+1))
	if err != nil {
		return executableDigest{}, fmt.Errorf("read %s: %w", label, err)
	}
	after, err := file.Stat()
	if err != nil {
		return executableDigest{}, fmt.Errorf("reinspect %s: %w", label, err)
	}
	if written != info.Size() || after.Size() != info.Size() ||
		!after.ModTime().Equal(opened.ModTime()) {
		return executableDigest{}, fmt.Errorf("%s changed while it was read", label)
	}
	return executableDigest{sha256: hex.EncodeToString(digest.Sum(nil)), size: written}, nil
}

func digestCanonicalArchiveExecutableBytes(archiveBytes []byte, asset Asset) (executableDigest, error) {
	name := strings.ToLower(asset.Archive.Name)
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
		if err != nil {
			return executableDigest{}, err
		}
		return digestCanonicalZipReader(reader, asset.Platform)
	}
	return digestCanonicalTarReader(bytes.NewReader(archiveBytes), name, asset.Platform)
}

func digestCanonicalZipReader(reader *zip.Reader, platform Platform) (executableDigest, error) {
	if len(reader.File) > maxReleaseArchiveFiles {
		return executableDigest{}, fmt.Errorf("archive exceeds %d entries", maxReleaseArchiveFiles)
	}
	canonical := canonicalExecutableName(platform)
	seen := make(map[string]struct{}, len(reader.File))
	var result executableDigest
	found := false
	var total uint64
	for _, entry := range reader.File {
		relative, err := safeReleaseArchivePath(entry.Name, entry.FileInfo().IsDir())
		if err != nil {
			return executableDigest{}, err
		}
		key := strings.ToLower(relative)
		if _, exists := seen[key]; exists {
			return executableDigest{}, fmt.Errorf("duplicate archive path %q", entry.Name)
		}
		seen[key] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return executableDigest{}, fmt.Errorf("archive entry %q has a forbidden type", entry.Name)
		}
		if entry.UncompressedSize64 > uint64(maxReleaseExtractedBytes) ||
			total > uint64(maxReleaseExtractedBytes)-entry.UncompressedSize64 {
			return executableDigest{}, fmt.Errorf("archive exceeds %d uncompressed bytes", maxReleaseExtractedBytes)
		}
		total += entry.UncompressedSize64
		if relative != canonical {
			continue
		}
		if found || !mode.IsRegular() {
			return executableDigest{}, fmt.Errorf("canonical executable %q must be one regular file", canonical)
		}
		opened, err := entry.Open()
		if err != nil {
			return executableDigest{}, err
		}
		result, err = digestArchiveReader(opened, int64(entry.UncompressedSize64))
		closeErr := opened.Close()
		if err != nil {
			return executableDigest{}, err
		}
		if closeErr != nil {
			return executableDigest{}, closeErr
		}
		found = true
	}
	if !found {
		return executableDigest{}, fmt.Errorf("canonical executable %q is missing", canonical)
	}
	return result, nil
}

func digestCanonicalTarReader(stream io.Reader, archiveName string, platform Platform) (executableDigest, error) {
	if strings.HasSuffix(archiveName, ".gz") || strings.HasSuffix(archiveName, ".tgz") {
		gzipReader, err := gzip.NewReader(stream)
		if err != nil {
			return executableDigest{}, err
		}
		defer gzipReader.Close()
		stream = gzipReader
	}
	reader := tar.NewReader(stream)
	canonical := canonicalExecutableName(platform)
	seen := map[string]struct{}{}
	var result executableDigest
	found := false
	var total int64
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return executableDigest{}, err
		}
		if count >= maxReleaseArchiveFiles {
			return executableDigest{}, fmt.Errorf("archive exceeds %d entries", maxReleaseArchiveFiles)
		}
		isDirectory := header.Typeflag == tar.TypeDir
		relative, err := safeReleaseArchivePath(header.Name, isDirectory)
		if err != nil {
			return executableDigest{}, err
		}
		key := strings.ToLower(relative)
		if _, exists := seen[key]; exists {
			return executableDigest{}, fmt.Errorf("duplicate archive path %q", header.Name)
		}
		seen[key] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return executableDigest{}, fmt.Errorf(
					"archive directory entry %q must have zero size",
					header.Name,
				)
			}
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return executableDigest{}, fmt.Errorf("archive entry %q has a forbidden type", header.Name)
		}
		if header.Size < 0 || header.Size > maxReleaseExtractedBytes-total {
			return executableDigest{}, fmt.Errorf("archive exceeds %d uncompressed bytes", maxReleaseExtractedBytes)
		}
		total += header.Size
		if relative != canonical {
			continue
		}
		if found {
			return executableDigest{}, fmt.Errorf("canonical executable %q is duplicated", canonical)
		}
		result, err = digestArchiveReader(reader, header.Size)
		if err != nil {
			return executableDigest{}, err
		}
		found = true
	}
	if !found {
		return executableDigest{}, fmt.Errorf("canonical executable %q is missing", canonical)
	}
	return result, nil
}

func digestArchiveReader(reader io.Reader, size int64) (executableDigest, error) {
	if size <= 0 || size > maxCurrentExecutableBytes {
		return executableDigest{}, fmt.Errorf("canonical executable must be a bounded non-empty file")
	}
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(reader, size+1))
	if err != nil {
		return executableDigest{}, err
	}
	if written != size {
		return executableDigest{}, fmt.Errorf("canonical executable changed or was truncated while read")
	}
	return executableDigest{sha256: hex.EncodeToString(digest.Sum(nil)), size: written}, nil
}

func canonicalExecutableName(platform Platform) string {
	if platform.OS == "windows" {
		return "stackkit.exe"
	}
	return "stackkit"
}

func safeReleaseArchivePath(name string, directory bool) (string, error) {
	if directory {
		name = strings.TrimRight(name, "/")
	}
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) ||
		path.IsAbs(name) || filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		clean != name || (len(clean) > 1 && clean[1] == ':') {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return clean, nil
}
