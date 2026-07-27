package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/kombifyio/stackkits/internal/releaseindex"
)

const (
	linuxCurrentProcessImagePath                = "/proc/self/exe"
	currentProcessImageBindingLinuxProc         = "linux-proc-self-exe"
	currentProcessImageBindingWindowsLock       = "windows-running-image-lock"
	maxCurrentProcessImageSnapshotBytes   int64 = 512 << 20
	maxCurrentReleaseArchiveBytes         int64 = 256 << 20
)

type currentProcessImage struct {
	path    string
	binding string
	cleanup func() error
}

var (
	verifyCurrentReleaseBootstrap = verifyExactCurrentReleaseForInit
	currentExecutablePath         = os.Executable
	currentReleasePlatform        = func() releaseindex.Platform {
		return releaseindex.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	}
	acquireCurrentProcessImage  = acquireKernelBoundCurrentProcessImage
	openCurrentProcessImageFile = os.Open
)

func verifyExactCurrentReleaseForInit(
	ctx context.Context,
	workspaceRoot string,
	kit string,
	buildVersion string,
) (releaseindex.Receipt, error) {
	exactTag, err := releaseindex.ExactTagForBuildVersion(buildVersion)
	if err != nil {
		return releaseindex.Receipt{}, err
	}
	platform := currentReleasePlatform()
	processImage, err := acquireCurrentProcessImage(platform.OS)
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("bind current StackKit process image: %w", err)
	}
	if processImage.cleanup == nil {
		return releaseindex.Receipt{}, fmt.Errorf("current StackKit process-image cleanup contract is required")
	}
	defer func() { _ = processImage.cleanup() }()
	identity := releaseindex.ExecutableIdentity{
		Path: processImage.path, BuildVersion: buildVersion,
	}
	installDir := filepath.Join(
		workspaceRoot,
		".stackkit",
		"releases",
		kit,
		exactTag,
		platform.OS+"-"+platform.Arch,
	)
	if _, statErr := os.Lstat(installDir); statErr == nil {
		receipt, verifyErr := (releaseindex.Installer{
			Attestations: newPublicAttestationVerifier(),
			MaxBlobBytes: maxCurrentReleaseArchiveBytes,
		}).VerifyCurrentExecutable(ctx, installDir, identity)
		if verifyErr != nil {
			return releaseindex.Receipt{}, fmt.Errorf(
				"re-verify cached exact current release without network fallback: %w",
				verifyErr,
			)
		}
		if verifyErr := validateExpectedCurrentReleaseReceipt(
			receipt, kit, exactTag, platform,
		); verifyErr != nil {
			return releaseindex.Receipt{}, verifyErr
		}
		return receipt, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return releaseindex.Receipt{}, fmt.Errorf("inspect exact current release cache: %w", statErr)
	}

	source := newPublicReleaseSource()
	attestations := newPublicAttestationVerifier()
	resolution, err := (releaseindex.Resolver{
		Source: source, Attestations: attestations,
	}).Resolve(ctx, releaseindex.ResolveRequest{
		Kit: kit, Target: exactTag, OS: platform.OS, Arch: platform.Arch,
	})
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("resolve exact current public release %s: %w", exactTag, err)
	}
	receipt, err := (releaseindex.Installer{
		Source: source, Attestations: attestations,
		MaxBlobBytes: maxCurrentReleaseArchiveBytes,
	}).InstallCurrentExecutable(ctx, resolution, workspaceRoot, identity)
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("install exact current release authority: %w", err)
	}
	if err := validateExpectedCurrentReleaseReceipt(
		receipt, kit, exactTag, platform,
	); err != nil {
		return releaseindex.Receipt{}, err
	}
	return receipt, nil
}

func validateExpectedCurrentReleaseReceipt(
	receipt releaseindex.Receipt,
	kit string,
	exactTag string,
	platform releaseindex.Platform,
) error {
	if receipt.Kit != kit ||
		receipt.Version != exactTag ||
		receipt.Platform != platform {
		return fmt.Errorf(
			"verified receipt %s/%s/%s/%s does not match expected current release %s/%s/%s/%s",
			receipt.Kit,
			receipt.Version,
			receipt.Platform.OS,
			receipt.Platform.Arch,
			kit,
			exactTag,
			platform.OS,
			platform.Arch,
		)
	}
	return nil
}

func acquireKernelBoundCurrentProcessImage(goos string) (currentProcessImage, error) {
	switch goos {
	case "linux":
		return snapshotLinuxCurrentProcessImage()
	case "windows":
		// Windows keeps the mapped executable image open without delete/write
		// sharing for the process lifetime. Therefore os.Executable names the
		// loader-locked process image; releaseindex reopens and hashes that
		// regular file while validating that it does not change during read.
		executablePath, err := currentExecutablePath()
		if err != nil {
			return currentProcessImage{}, fmt.Errorf("resolve Windows running image: %w", err)
		}
		return currentProcessImage{
			path:    executablePath,
			binding: currentProcessImageBindingWindowsLock,
			cleanup: func() error { return nil },
		}, nil
	default:
		return currentProcessImage{}, fmt.Errorf(
			"platform %q has no supported kernel-bound current process-image source",
			goos,
		)
	}
}

func snapshotLinuxCurrentProcessImage() (currentProcessImage, error) {
	source, err := openCurrentProcessImageFile(linuxCurrentProcessImagePath)
	if err != nil {
		return currentProcessImage{}, fmt.Errorf("open Linux kernel process image: %w", err)
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil {
		return currentProcessImage{}, fmt.Errorf("inspect Linux kernel process image: %w", err)
	}
	if !before.Mode().IsRegular() ||
		before.Size() <= 0 ||
		before.Size() > maxCurrentProcessImageSnapshotBytes {
		return currentProcessImage{}, fmt.Errorf(
			"Linux kernel process image must be a bounded non-empty regular file",
		)
	}

	stage, err := os.MkdirTemp("", "stackkit-current-process-image-")
	if err != nil {
		return currentProcessImage{}, fmt.Errorf("create current process-image snapshot directory: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(stage) }
	snapshotPath := filepath.Join(stage, "stackkit")
	snapshot, err := os.OpenFile(snapshotPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = cleanup()
		return currentProcessImage{}, fmt.Errorf("create current process-image snapshot: %w", err)
	}
	written, copyErr := io.Copy(snapshot, io.LimitReader(source, before.Size()+1))
	syncErr := snapshot.Sync()
	closeErr := snapshot.Close()
	after, statErr := source.Stat()
	if copyErr != nil || syncErr != nil || closeErr != nil || statErr != nil {
		_ = cleanup()
		return currentProcessImage{}, fmt.Errorf(
			"snapshot Linux kernel process image: %w",
			errors.Join(copyErr, syncErr, closeErr, statErr),
		)
	}
	if written != before.Size() ||
		after.Size() != before.Size() ||
		!after.ModTime().Equal(before.ModTime()) {
		_ = cleanup()
		return currentProcessImage{}, fmt.Errorf(
			"Linux kernel process image changed while it was snapshotted",
		)
	}
	return currentProcessImage{
		path:    snapshotPath,
		binding: currentProcessImageBindingLinuxProc,
		cleanup: cleanup,
	}, nil
}
