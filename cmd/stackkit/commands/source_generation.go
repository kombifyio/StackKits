package commands

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const maxSourceGenerationInspectionBytes = 2 << 20

func init() {
	kitCmd.AddCommand(newSourceGenerationPrepareCmd())
}

// Preparing cache is an explicit operation. PLAN, including historical source
// inspection, remains read-only and cannot acquire missing release custody.
func newSourceGenerationPrepareCmd() *cobra.Command {
	var asJSON bool
	var cacheOnly bool
	var cache sourceReleaseCacheRequest
	command := &cobra.Command{
		Use:         "prepare-source-generation",
		Short:       "Cache and verify the attested release that authored the retained generation",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cacheOnly {
				receipt, cliDigest, err := prepareSourceReleaseCache(cmd.Context(), cache)
				if err != nil {
					return err
				}
				if asJSON {
					return writeCommandResult(cmd, cmd.CommandPath(), struct {
						Prepared      bool                         `json:"prepared"`
						SourceRelease sourceGenerationReleaseProof `json:"sourceRelease"`
						CLISHA256     string                       `json:"cliSha256"`
					}{true, sourceGenerationProof(receipt), cliDigest})
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Verified retained source release %s\n", receipt.Version)
				return err
			}
			if cache != (sourceReleaseCacheRequest{}) {
				return errors.New("explicit source release cache arguments require --cache-only")
			}
			workspace := getWorkDir()
			current, err := releaseindex.ExactTagForBuildVersion(version)
			if err != nil {
				return err
			}
			tag, err := currentGenerationSourceHint(workspace, specFile)
			if err != nil {
				return err
			}
			if tag == "" {
				return errors.New("source generation release is unavailable")
			}
			if semver.Compare(tag, current) > 0 {
				return errors.New("source generation cannot follow the installed compiler")
			}
			if tag == current {
				if asJSON {
					return writeCommandResult(cmd, cmd.CommandPath(), struct {
						Prepared bool `json:"prepared"`
					}{})
				}
				return nil
			}
			owner, err := localevidence.LoadOwnerCustody(workspace)
			if err != nil {
				return err
			}
			binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
			if err != nil {
				return err
			}
			if binding.OwnerRef != owner.OwnerRef || binding.KeyID != owner.KeyID {
				return errors.New("source cache preparation differs from retained Owner custody")
			}
			kit, err := loadWorkspaceKit(workspace)
			if err != nil {
				return err
			}
			if err := installAttestedSourceForGeneration(cmd.Context(), workspace, specFile, kit, current, currentReleasePlatform()); err != nil {
				return err
			}
			bridge, historical, err := inspectSourceGeneration(cmd.Context(), workspace, specFile)
			if err != nil {
				return err
			}
			if !historical || bridge.Receipt.Version != tag || bridge.Verify.Owner.OwnerRef != owner.OwnerRef || bridge.Verify.Owner.KeyID != owner.KeyID || bridge.Verify.Owner.OwnerBindingDigest != localevidence.OwnerRuntimeBindingDigest(binding) {
				return errors.New("source cache preparation changed its historical or current Owner authority")
			}
			if asJSON {
				return writeSourceGenerationInspection(cmd.OutOrStdout(), bridge)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Verified source release %s for the retained generation\n", bridge.Receipt.Version)
			return err
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Emit bounded source-generation inspection proof after authenticated cache preparation.")
	command.Flags().BoolVar(&cacheOnly, "cache-only", false, "Retain an exact attested public release independently of workspace lifecycle or Owner custody.")
	command.Flags().StringVar(&cache.Directory, "release-cache", "", "Absolute persistent release cache directory for cache-only preparation.")
	command.Flags().StringVar(&cache.Kit, "source-kit", "", "Exact public source kit for cache-only preparation.")
	command.Flags().StringVar(&cache.Tag, "source-release", "", "Exact public source release tag, no newer than this CLI.")
	command.Flags().StringVar(&cache.Platform.OS, "source-os", "", "Exact public source operating system.")
	command.Flags().StringVar(&cache.Platform.Arch, "source-arch", "", "Exact public source architecture.")
	command.Flags().StringVar(&cache.ArchiveSHA256, "source-archive-sha256", "", "Require the exact public source archive digest.")
	command.Flags().StringVar(&cache.IndexSHA256, "source-index-sha256", "", "Require the exact signed source release-index digest.")
	command.Flags().StringVar(&cache.CLISHA256, "source-cli-sha256", "", "Require the installed source CLI digest to match the verified archive executable.")
	return command
}

type sourceReleaseCacheRequest struct {
	Directory, Kit, Tag                   string
	Platform                              releaseindex.Platform
	ArchiveSHA256, IndexSHA256, CLISHA256 string
}

// Caller-provided identities select data only. Public attestations and the
// extracted executable decide whether this cache may survive runtime replacement.
func prepareSourceReleaseCache(ctx context.Context, request sourceReleaseCacheRequest) (releaseindex.Receipt, string, error) {
	current, err := releaseindex.ExactTagForBuildVersion(version)
	hexDigest := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if err != nil || !semver.IsValid(request.Tag) || semver.Canonical(request.Tag) != request.Tag || semver.Prerelease(request.Tag) != "" || semver.Compare(request.Tag, current) > 0 ||
		!hexDigest.MatchString(request.ArchiveSHA256) || !hexDigest.MatchString(request.IndexSHA256) || !hexDigest.MatchString(request.CLISHA256) {
		return releaseindex.Receipt{}, "", errors.New("cache-only preparation requires an exact prior public release and archive/index/CLI digests")
	}
	if err := productkits.Validate(request.Kit); err != nil {
		return releaseindex.Receipt{}, "", err
	}
	if (request.Platform.OS != "linux" && request.Platform.OS != "windows" && request.Platform.OS != "darwin") ||
		(request.Platform.Arch != "amd64" && request.Platform.Arch != "arm64") || !filepath.IsAbs(request.Directory) {
		return releaseindex.Receipt{}, "", errors.New("cache-only preparation requires an exact public platform and absolute cache directory")
	}
	directory, err := releaseindex.CacheInstallationDirectory(request.Directory, request.Kit, request.Tag, request.Platform)
	if err != nil {
		return releaseindex.Receipt{}, "", err
	}
	validateCLI := func(proof releaseindex.VerifiedInstallation) error {
		cli, _, err := upgradelifecycle.ReleaseExecutablesFromVerifiedRelease(proof)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(cli)) != request.CLISHA256 {
			return errors.New("installed source CLI differs from the verified public archive executable")
		}
		return nil
	}
	installer := releaseindex.Installer{Source: newPublicReleaseSource(), Attestations: newPublicAttestationVerifier()}
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		resolved, err := (releaseindex.Resolver{Source: installer.Source, Attestations: installer.Attestations}).Resolve(ctx,
			releaseindex.ResolveRequest{Kit: request.Kit, Target: request.Tag, OS: request.Platform.OS, Arch: request.Platform.Arch})
		if err != nil {
			return releaseindex.Receipt{}, "", err
		}
		if resolved.Asset.Archive.SHA256 != request.ArchiveSHA256 || fmt.Sprintf("%x", sha256.Sum256(resolved.RawIndex)) != request.IndexSHA256 {
			return releaseindex.Receipt{}, "", errors.New("published source release differs from the installed admission")
		}
		if _, err := installer.InstallToCache(ctx, resolved, request.Directory, validateCLI); err != nil {
			return releaseindex.Receipt{}, "", err
		}
	} else if err != nil {
		return releaseindex.Receipt{}, "", err
	}
	var receipt releaseindex.Receipt
	err = installer.InspectInstalled(ctx, directory, func(proof releaseindex.VerifiedInstallation) error {
		if err := proof.Inspect(func(current releaseindex.Receipt, _ releaseindex.Asset, _ io.Reader) error {
			if err := validateExpectedCurrentReleaseReceipt(current, request.Kit, request.Tag, request.Platform); err != nil {
				return err
			}
			if current.Channel != releaseindex.ChannelStable || current.ArchiveSHA256 != request.ArchiveSHA256 || current.IndexSHA256 != request.IndexSHA256 {
				return errors.New("cached source release differs from the installed admission")
			}
			receipt = current
			return nil
		}); err != nil {
			return err
		}
		return validateCLI(proof)
	})
	return receipt, request.CLISHA256, err
}

// Export public distribution identity only, never workspace paths or custody.
type sourceGenerationReleaseProof struct {
	SchemaVersion          string                `json:"schemaVersion"`
	Kit                    string                `json:"kit"`
	Version                string                `json:"version"`
	Channel                releaseindex.Channel  `json:"channel"`
	Platform               releaseindex.Platform `json:"platform"`
	ArchiveSHA256          string                `json:"archiveSha256"`
	IndexSHA256            string                `json:"indexSha256"`
	AttestationSHA256      string                `json:"attestationSha256"`
	IndexAttestationSHA256 string                `json:"indexAttestationSha256"`
	TrustedRootSHA256      string                `json:"trustedRootSha256"`
}

// inspectSourceGeneration never installs or selects an arbitrary compiler. The
// receipt in the old generation is a hint only; existing attested release
// custody and that source CLI's generation / offline Owner proof decide.
func inspectSourceGeneration(ctx context.Context, workspace, requestedSpec string) (publicUpgradeBridge, bool, error) {
	tag, err := currentGenerationSourceHint(workspace, requestedSpec)
	if err != nil {
		return publicUpgradeBridge{}, false, err
	}
	if tag == "" {
		return publicUpgradeBridge{}, false, errors.New("source generation release is unavailable")
	}
	current := "v" + architectureV2ComponentVersion(version)
	if tag == current {
		return publicUpgradeBridge{}, false, nil // Ordinary current CUE inspection.
	}
	if !semver.IsValid(current) || semver.Compare(tag, current) >= 0 {
		return publicUpgradeBridge{}, false, errors.New("source generation must precede the installed compiler")
	}
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return publicUpgradeBridge{}, false, err
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(workspace, ".stackkit", "releases", kit, tag, platform.OS+"-"+platform.Arch)
	var receipt releaseindex.Receipt
	err = (releaseindex.Installer{Attestations: newPublicAttestationVerifier()}).InspectInstalled(ctx, installDir, func(proof releaseindex.VerifiedInstallation) error {
		return proof.Inspect(func(current releaseindex.Receipt, _ releaseindex.Asset, _ io.Reader) error {
			if current.SchemaVersion != releaseindex.ReceiptSchemaVersion || current.Channel != releaseindex.ChannelStable || filepath.Clean(current.InstallDir) != filepath.Clean(installDir) {
				return errors.New("source generation receipt is outside installed stable custody")
			}
			if err := validateExpectedCurrentReleaseReceipt(current, kit, tag, platform); err != nil {
				return err
			}
			receipt = current
			return nil
		})
	})
	if err != nil {
		return publicUpgradeBridge{}, false, fmt.Errorf("verify installed source generation release: %w", err)
	}
	bridge, err := inspectAttestedSourceAuthority(ctx, workspace, requestedSpec, receipt, true, true)
	if err != nil {
		return publicUpgradeBridge{}, false, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil || owner.OwnerRef != bridge.Verify.Owner.OwnerRef || owner.KeyID != bridge.Verify.Owner.KeyID {
		return publicUpgradeBridge{}, false, errors.New("source generation differs from retained local Owner custody")
	}
	runtimeBinding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil || runtimeBinding.PocketIDSubject != bridge.Verify.Owner.PocketIDSubject || localevidence.OwnerRuntimeBindingDigest(runtimeBinding) != bridge.Verify.Owner.OwnerBindingDigest {
		return publicUpgradeBridge{}, false, errors.New("source generation differs from retained signed Owner runtime binding")
	}
	return bridge, true, nil
}

func writeSourceGenerationInspection(writer io.Writer, bridge publicUpgradeBridge) error {
	// Preserve the original compiler, Inventory and applied evidence. This is
	// preparation provenance, never permission to execute an old plan now.
	publicRelease := sourceGenerationProof(bridge.Receipt)
	raw, err := json.Marshal(struct {
		APIVersion         string                            `json:"apiVersion"`
		Inspection         generationartifact.PlanInspection `json:"inspection"`
		Release            sourceGenerationReleaseProof      `json:"sourceRelease"`
		Owner              architectureV2OwnerVerifySummary  `json:"owner"`
		ApplyResultHash    string                            `json:"applyResultHash"`
		EvidenceBundleHash string                            `json:"evidenceBundleHash"`
	}{"stackkit.source-generation-inspection/v1", bridge.Current, publicRelease, bridge.Verify.Owner, bridge.Verify.Apply.ResultHash, bridge.Verify.Apply.EvidenceBundleHash})
	if err != nil {
		return err
	}
	if len(raw) > maxSourceGenerationInspectionBytes {
		return errors.New("source generation inspection exceeds its public proof bound")
	}
	_, err = writer.Write(append(raw, '\n'))
	return err
}

func sourceGenerationProof(receipt releaseindex.Receipt) sourceGenerationReleaseProof {
	return sourceGenerationReleaseProof{
		SchemaVersion: receipt.SchemaVersion, Kit: receipt.Kit, Version: receipt.Version, Channel: receipt.Channel,
		Platform: receipt.Platform, ArchiveSHA256: receipt.ArchiveSHA256, IndexSHA256: receipt.IndexSHA256,
		AttestationSHA256: receipt.AttestationSHA256, IndexAttestationSHA256: receipt.IndexAttestationSHA256,
		TrustedRootSHA256: receipt.TrustedRootSHA256,
	}
}

func runSourceGenerationPlan(cmdContext context.Context, workspace, requestedSpec string, writer io.Writer) (bool, error) {
	bridge, historical, err := inspectSourceGeneration(cmdContext, workspace, requestedSpec)
	if err != nil || !historical {
		return historical, err
	}
	if bridge.Current.Binding.CompilerVersion != "stackkits-resolver/"+strings.TrimPrefix(bridge.Receipt.Version, "v") {
		return true, errors.New("source generation compiler differs from its attested release")
	}
	return true, writeSourceGenerationInspection(writer, bridge)
}
