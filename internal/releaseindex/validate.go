package releaseindex

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (index Index) Validate() error {
	if index.SchemaVersion != SchemaVersion {
		return fmt.Errorf("release index schemaVersion %q is unsupported", index.SchemaVersion)
	}
	if index.Release.Repository != "kombifyio/stackKits" {
		return fmt.Errorf("release repository %q is not trusted", index.Release.Repository)
	}
	version, err := parseVersion(index.Release.Version)
	if err != nil {
		return fmt.Errorf("release version: %w", err)
	}
	if index.Release.PublishedAt.IsZero() {
		return fmt.Errorf("release publishedAt is required")
	}
	if err := index.Release.TrustedRoot.validate("trusted root"); err != nil {
		return err
	}
	if index.Release.TrustedRoot.MediaType != SigstoreTrustedRootMediaType {
		return fmt.Errorf("trusted root mediaType must be %q", SigstoreTrustedRootMediaType)
	}
	if len(index.Assets) == 0 {
		return fmt.Errorf("release assets are required")
	}
	seen := make(map[string]struct{}, len(index.Assets))
	for position, asset := range index.Assets {
		if err := asset.validate(version); err != nil {
			return fmt.Errorf("assets[%d]: %w", position, err)
		}
		key := strings.Join([]string{asset.Kit, asset.Version, string(asset.Channel), asset.Platform.OS, asset.Platform.Arch}, "\x00")
		if _, exists := seen[key]; exists {
			return fmt.Errorf("assets[%d]: duplicate kit/version/channel/platform", position)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (asset Asset) validate(release parsedVersion) error {
	switch asset.Kit {
	case "basement-kit", "cloud-kit", "modern-homelab":
	default:
		return fmt.Errorf("kit %q is unsupported", asset.Kit)
	}
	if asset.Version != release.original {
		return fmt.Errorf("version %q does not equal release version %q", asset.Version, release.original)
	}
	expectedChannel := release.channel()
	if asset.Channel != expectedChannel {
		return fmt.Errorf("channel %q does not match version channel %q", asset.Channel, expectedChannel)
	}
	switch asset.Platform.OS {
	case "linux", "darwin", "windows":
	default:
		return fmt.Errorf("platform OS %q is unsupported", asset.Platform.OS)
	}
	switch asset.Platform.Arch {
	case "amd64", "arm64":
	default:
		return fmt.Errorf("platform architecture %q is unsupported", asset.Platform.Arch)
	}
	if err := asset.Archive.validate("archive"); err != nil {
		return err
	}
	if err := asset.SBOM.validate("sbom"); err != nil {
		return err
	}
	if asset.SBOM.MediaType != SPDXJSONMediaType {
		return fmt.Errorf("sbom mediaType must be %q", SPDXJSONMediaType)
	}
	if err := asset.Attestation.Blob.validate("attestation"); err != nil {
		return err
	}
	if asset.Attestation.MediaType != InTotoJSONLMediaType {
		return fmt.Errorf("attestation mediaType must be %q", InTotoJSONLMediaType)
	}
	if asset.Attestation.Issuer != GitHubOIDCIssuer {
		return fmt.Errorf("attestation issuer %q is not trusted", asset.Attestation.Issuer)
	}
	expectedIdentity := "https://github.com/kombifyio/StackKits/.github/workflows/release.yml@refs/tags/" + asset.Version
	if asset.Attestation.CertificateIdentity != expectedIdentity {
		return fmt.Errorf("attestation certificateIdentity must be %q", expectedIdentity)
	}
	if asset.Attestation.Subject != asset.Archive.Name {
		return fmt.Errorf("attestation subject must equal archive name")
	}
	if asset.Attestation.PredicateType != GitHubAttestationPredicate {
		return fmt.Errorf("attestation predicateType %q is unsupported", asset.Attestation.PredicateType)
	}
	return nil
}

func (blob Blob) validate(label string) error {
	if strings.TrimSpace(blob.Name) == "" || filepath.Base(blob.Name) != blob.Name || blob.Name == "." {
		return fmt.Errorf("%s name %q is unsafe", label, blob.Name)
	}
	if strings.TrimSpace(blob.URL) == "" {
		return fmt.Errorf("%s URL is required", label)
	}
	if !sha256Pattern.MatchString(blob.SHA256) {
		return fmt.Errorf("%s SHA-256 is invalid", label)
	}
	if strings.TrimSpace(blob.MediaType) == "" {
		return fmt.Errorf("%s mediaType is required", label)
	}
	return nil
}
