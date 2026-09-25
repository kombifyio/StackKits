package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

// Release authority kinds of a lifecycle mutation target
// (docs/ARCHITECTURE.md "Advanced change sets through Terramate (Stage 1)").
const (
	releaseAuthorityRunningExecutable     = "running-executable"
	releaseAuthorityWorkspaceReleaseCache = "workspace-release-cache"
)

// runningStackKitExecutable resolves the executable running this process.
var runningStackKitExecutable = os.Executable

// releaseAuthorityRecord is the evidence of which release authority executed
// a lifecycle mutation target: the running executable (a same-release target
// on a host without a workspace release cache) or a Sigstore-verified
// workspace release cache receipt. SHA256 is the executable digest.
type releaseAuthorityRecord struct {
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	SHA256   string `json:"sha256,omitempty"`
}

// lifecycleReleaseAuthority is the release that executes a lifecycle
// mutation target. A same-release target (change-set apply, Advanced
// reconcile, coordinated rollback) is the release already executing: without
// a workspace release cache for it, the running executable is the authority.
// A cross-version target always requires a verified workspace release cache.
type lifecycleReleaseAuthority struct {
	record     releaseAuthorityRecord
	kit        string
	receipt    releaseindex.Receipt
	executable string
}

func (authority *lifecycleReleaseAuthority) runningExecutable() bool {
	return authority.record.Kind == releaseAuthorityRunningExecutable
}

// version is the exact release tag of the target.
func (authority *lifecycleReleaseAuthority) version() string {
	return authority.record.Version
}

// verifyReceipt is the release receipt a target verify must report, or nil
// for the running executable, whose identity the lifecycle join binds by
// executable digest instead.
func (authority *lifecycleReleaseAuthority) verifyReceipt() *releaseindex.Receipt {
	if authority.runningExecutable() {
		return nil
	}
	receipt := authority.receipt
	return &receipt
}

// resolution is the checkpoint target. The running executable has no
// archive, so its resolution carries no archive digest.
func (authority *lifecycleReleaseAuthority) resolution() releaseindex.Resolution {
	if authority.runningExecutable() {
		platform, _ := parseReleaseAuthorityPlatform(authority.record.Platform)
		return releaseindex.Resolution{Asset: releaseindex.Asset{
			Kit: authority.kit, Version: authority.record.Version, Platform: platform,
		}}
	}
	receipt := authority.receipt
	return releaseindex.Resolution{Asset: releaseindex.Asset{
		Kit: receipt.Kit, Version: receipt.Version, Channel: receipt.Channel,
		Platform: receipt.Platform, Archive: releaseindex.Blob{SHA256: receipt.ArchiveSHA256},
	}}
}

// journal is the signed lifecycle journal identity of the target.
func (authority *lifecycleReleaseAuthority) journal(executableDigest string) lifecyclemutation.ReleaseAuthority {
	if authority.runningExecutable() {
		return lifecyclemutation.ReleaseAuthority{
			Authority: lifecyclemutation.AuthorityRunningExecutable,
			Version:   architectureV2ComponentVersion(authority.record.Version),
			// No archive exists for the running executable.
			ExecutableSHA256: executableDigest,
		}
	}
	return lifecyclemutation.ReleaseAuthority{
		Version:          architectureV2ComponentVersion(authority.receipt.Version),
		ArchiveSHA256:    "sha256:" + authority.receipt.ArchiveSHA256,
		ExecutableSHA256: executableDigest,
	}
}

// withExecutable runs invoke with the exact target executable and records its
// digest. The running executable is re-hashed on every use, so a binary
// replaced during the mutation fails closed.
func (authority *lifecycleReleaseAuthority) withExecutable(
	ctx context.Context,
	invoke func(string) error,
) error {
	if invoke == nil {
		return errors.New("target executable callback is required")
	}
	if !authority.runningExecutable() {
		return withPublicUpgradeInstalledExecutable(ctx, authority.receipt, func(binary string) error {
			digest, err := executableFileSHA256(binary)
			if err != nil {
				return err
			}
			if authority.record.SHA256 != "" && authority.record.SHA256 != digest {
				return errors.New("verified installed target executable changed during the mutation")
			}
			authority.record.SHA256 = digest
			return invoke(binary)
		})
	}
	digest, err := streamFileSHA256(authority.executable)
	if err != nil {
		return fmt.Errorf("hash the running StackKit executable: %w", err)
	}
	if digest != authority.record.SHA256 {
		return errors.New("the running StackKit executable changed during the mutation")
	}
	return invoke(authority.executable)
}

// priorReleaseAuthority is the signed journal identity of a checkpoint's
// prior release.
func priorReleaseAuthority(snapshot upgradelifecycle.ExecutorStateSnapshot) lifecyclemutation.ReleaseAuthority {
	prior := lifecyclemutation.ReleaseAuthority{
		Version:          architectureV2ComponentVersion(snapshot.Release.Version),
		ArchiveSHA256:    snapshot.Release.ArchiveSHA256,
		ExecutableSHA256: snapshot.Executable.Blob.SHA256,
	}
	if snapshot.Release.Authority == upgradelifecycle.ExecutorStateReleaseRunningExecutable {
		prior.Authority = lifecyclemutation.AuthorityRunningExecutable
	}
	return prior
}

// snapshotVerifyReceipt is the release receipt a verify by a checkpoint's
// prior executable must report, or nil when the checkpoint captured the
// running executable (no receipt exists; the join binds its digest).
func snapshotVerifyReceipt(snapshot upgradelifecycle.ExecutorStateSnapshot) *releaseindex.Receipt {
	if snapshot.Release.Authority == upgradelifecycle.ExecutorStateReleaseRunningExecutable {
		return nil
	}
	return &releaseindex.Receipt{
		SchemaVersion: releaseindex.ReceiptSchemaVersion,
		Kit:           snapshot.Release.Kit, Version: snapshot.Release.Version,
		Channel: snapshot.Release.Channel, Platform: snapshot.Release.Platform,
		ArchiveSHA256: strings.TrimPrefix(snapshot.Release.ArchiveSHA256, "sha256:"),
	}
}

// resolveLifecycleReleaseAuthority selects the release authority of a
// lifecycle mutation whose target is targetTag. The running executable is the
// authority only when targetTag is the running release and the workspace
// holds no release cache entry for it; an existing cache entry must verify,
// and a cross-version target always requires the verified cache.
func resolveLifecycleReleaseAuthority(
	cmd *cobra.Command,
	workspace, targetTag string,
) (lifecycleReleaseAuthority, error) {
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return lifecycleReleaseAuthority{}, err
	}
	platform := currentReleasePlatform()
	runningTag, runningErr := releaseindex.ExactTagForBuildVersion(version)
	sameRelease := runningErr == nil && targetTag == runningTag
	if sameRelease {
		_, installDir, pathErr := appliedPublicUpgradeReleasePath(workspace, kit, runningTag, platform)
		if pathErr != nil {
			return lifecycleReleaseAuthority{}, pathErr
		}
		if _, statErr := os.Lstat(installDir); errors.Is(statErr, os.ErrNotExist) {
			return runningExecutableReleaseAuthority(kit, runningTag, platform)
		}
	}
	receipts, err := verifyWorkspaceReleaseReceipts(cmd, workspace)
	if err != nil {
		return lifecycleReleaseAuthority{}, err
	}
	var matches []releaseindex.Receipt
	for _, receipt := range receipts {
		if receipt.Kit == kit && receipt.Version == targetTag && receipt.Platform == platform {
			matches = append(matches, receipt)
		}
	}
	if len(matches) != 1 || strings.TrimSpace(matches[0].InstallDir) == "" {
		return lifecycleReleaseAuthority{}, fmt.Errorf(
			"target release %s requires exactly one verified installed receipt in the workspace release cache", targetTag,
		)
	}
	return lifecycleReleaseAuthority{
		record: releaseAuthorityRecord{
			Kind: releaseAuthorityWorkspaceReleaseCache, Version: targetTag,
			Platform: platform.OS + "/" + platform.Arch,
		},
		kit: kit, receipt: matches[0],
	}, nil
}

// currentLifecycleReleaseAuthority is the authority of a same-release
// mutation: its target is the running release.
func currentLifecycleReleaseAuthority(
	cmd *cobra.Command,
	workspace string,
) (lifecycleReleaseAuthority, error) {
	runningTag, err := releaseindex.ExactTagForBuildVersion(version)
	if err != nil {
		return lifecycleReleaseAuthority{}, fmt.Errorf("bind the mutation to the exact running release: %w", err)
	}
	return resolveLifecycleReleaseAuthority(cmd, workspace, runningTag)
}

func runningExecutableReleaseAuthority(
	kit, tag string,
	platform releaseindex.Platform,
) (lifecycleReleaseAuthority, error) {
	path, err := runningStackKitExecutable()
	if err != nil {
		return lifecycleReleaseAuthority{}, fmt.Errorf("resolve the running StackKit executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}
	if err := requireUpgradeTargetBinary(path); err != nil {
		return lifecycleReleaseAuthority{}, err
	}
	digest, err := streamFileSHA256(path)
	if err != nil {
		return lifecycleReleaseAuthority{}, fmt.Errorf("hash the running StackKit executable: %w", err)
	}
	return lifecycleReleaseAuthority{
		record: releaseAuthorityRecord{
			Kind: releaseAuthorityRunningExecutable, Version: tag,
			Platform: platform.OS + "/" + platform.Arch, SHA256: digest,
		},
		kit: kit, executable: path,
	}, nil
}

func parseReleaseAuthorityPlatform(value string) (releaseindex.Platform, error) {
	osName, arch, found := strings.Cut(value, "/")
	if !found || osName == "" || arch == "" {
		return releaseindex.Platform{}, fmt.Errorf("release authority platform %q is invalid", value)
	}
	return releaseindex.Platform{OS: osName, Arch: arch}, nil
}

func streamFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}
