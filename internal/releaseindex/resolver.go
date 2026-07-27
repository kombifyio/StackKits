package releaseindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const maxIndexBytes int64 = 8 << 20

type Resolver struct {
	Source       Source
	Attestations AttestationVerifier
}

func (resolver Resolver) Resolve(ctx context.Context, request ResolveRequest) (Resolution, error) {
	if resolver.Source == nil || resolver.Attestations == nil {
		return Resolution{}, fmt.Errorf("release source and attestation verifier are required")
	}
	if strings.TrimSpace(request.Kit) == "" || strings.TrimSpace(request.OS) == "" || strings.TrimSpace(request.Arch) == "" {
		return Resolution{}, fmt.Errorf("kit, OS, and architecture are required")
	}
	target, exact, err := normalizeTarget(request.Target)
	if err != nil {
		return Resolution{}, err
	}
	releases, err := resolver.Source.ListReleases(ctx)
	if err != nil {
		return Resolution{}, fmt.Errorf("list GitHub releases: %w", err)
	}
	sort.SliceStable(releases, func(left, right int) bool {
		leftVersion, leftErr := parseVersion(releases[left].TagName)
		rightVersion, rightErr := parseVersion(releases[right].TagName)
		if leftErr == nil && rightErr == nil {
			return compareVersions(leftVersion, rightVersion) > 0
		}
		return releases[left].PublishedAt.After(releases[right].PublishedAt)
	})
	for _, release := range releases {
		version, parseErr := parseVersion(release.TagName)
		if parseErr != nil || !releaseMatches(release, version, target, exact) {
			continue
		}
		raw, fetchErr := resolver.Source.Fetch(ctx, release.IndexURL, maxIndexBytes)
		if fetchErr != nil {
			return Resolution{}, fmt.Errorf("fetch release index for %s: %w", release.TagName, fetchErr)
		}
		rawAttestation, fetchErr := resolver.Source.Fetch(ctx, release.IndexAttestationURL, maxIndexBytes)
		if fetchErr != nil {
			return Resolution{}, fmt.Errorf("fetch release index attestation for %s: %w", release.TagName, fetchErr)
		}
		rawTrustedRoot, fetchErr := resolver.Source.Fetch(ctx, release.TrustedRootURL, maxIndexBytes)
		if fetchErr != nil {
			return Resolution{}, fmt.Errorf("fetch release trusted root for %s: %w", release.TagName, fetchErr)
		}
		if verifyErr := resolver.Attestations.VerifyIndex(ctx, IndexAttestationInput{
			Version: release.TagName, Index: raw, Bundle: rawAttestation, TrustedRoot: rawTrustedRoot,
		}); verifyErr != nil {
			return Resolution{}, fmt.Errorf("verify release index attestation for %s: %w", release.TagName, verifyErr)
		}
		index, decodeErr := Decode(raw)
		if decodeErr != nil {
			return Resolution{}, fmt.Errorf("release %s index: %w", release.TagName, decodeErr)
		}
		if index.Release.Version != release.TagName {
			return Resolution{}, fmt.Errorf("release index version %q does not match GitHub tag %q", index.Release.Version, release.TagName)
		}
		if index.Release.TrustedRoot.Name != TrustedRootAssetName ||
			!sameReleaseAssetURL(index.Release.TrustedRoot.URL, release.TrustedRootURL) ||
			index.Release.TrustedRoot.SHA256 != fmt.Sprintf("%x", sha256.Sum256(rawTrustedRoot)) {
			return Resolution{}, fmt.Errorf("release index trusted root does not match the attested GitHub release asset")
		}
		for _, asset := range index.Assets {
			if asset.Kit == request.Kit && asset.Platform.OS == request.OS && asset.Platform.Arch == request.Arch {
				return Resolution{
					Release: release, Index: index, Asset: asset,
					RawIndex:            append([]byte(nil), raw...),
					RawIndexAttestation: append([]byte(nil), rawAttestation...),
					RawTrustedRoot:      append([]byte(nil), rawTrustedRoot...),
				}, nil
			}
		}
	}
	return Resolution{}, fmt.Errorf("%w for kit=%s target=%s platform=%s/%s", ErrNoRelease, request.Kit, request.Target, request.OS, request.Arch)
}

func Decode(raw []byte) (Index, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var index Index
	if err := decoder.Decode(&index); err != nil {
		return Index{}, fmt.Errorf("decode %s: %w", SchemaVersion, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Index{}, fmt.Errorf("decode %s: trailing JSON value", SchemaVersion)
		}
		return Index{}, fmt.Errorf("decode %s trailing data: %w", SchemaVersion, err)
	}
	if err := index.Validate(); err != nil {
		return Index{}, err
	}
	return index, nil
}

func normalizeTarget(value string) (Channel, string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "latest" {
		return ChannelStable, "", nil
	}
	value = strings.TrimPrefix(value, "channel:")
	switch Channel(value) {
	case ChannelStable, ChannelBeta, ChannelEdge:
		return Channel(value), "", nil
	}
	version, err := parseVersion(value)
	if err != nil {
		return "", "", fmt.Errorf("target must be latest, stable, beta, edge, channel:<name>, or explicit SemVer: %w", err)
	}
	return version.channel(), value, nil
}

func releaseMatches(release Release, version parsedVersion, channel Channel, exact string) bool {
	if exact != "" {
		return release.TagName == exact
	}
	if version.channel() != channel {
		return false
	}
	if channel == ChannelStable {
		return !release.Prerelease
	}
	return release.Prerelease
}

// sameReleaseAssetURL reports whether two GitHub release asset URLs address the
// same asset.
//
// The URL is a locator, not the security control: the asset's bytes are bound by
// the SHA-256 checked alongside this and by the attestation verified above.
// GitHub treats the owner and repository segments case-insensitively and echoes
// them back in the repository's canonical casing, so a byte-exact comparison
// rejected indexes that named the very asset just fetched. Every published
// 0.8.0-beta recorded "kombifyio/stackKits" while GitHub returned
// "kombifyio/StackKits", which made those releases impossible to resolve,
// install, or pin -- the generator side is fixed for future releases, and this
// makes the already-published ones usable again.
//
// Only the case of the scheme, host, owner and repository is ignored. The tag
// and file name stay byte-exact, because those select which asset is meant.
func sameReleaseAssetURL(indexed, actual string) bool {
	if indexed == actual {
		return true
	}
	indexedPrefix, indexedRest, indexedOK := splitReleaseAssetURL(indexed)
	actualPrefix, actualRest, actualOK := splitReleaseAssetURL(actual)
	if !indexedOK || !actualOK {
		return false
	}
	return strings.EqualFold(indexedPrefix, actualPrefix) && indexedRest == actualRest
}

// splitReleaseAssetURL separates the case-insensitive
// scheme://host/owner/repository prefix from the case-sensitive remainder.
func splitReleaseAssetURL(raw string) (prefix, rest string, ok bool) {
	const scheme = "https://"
	if !strings.HasPrefix(strings.ToLower(raw), scheme) {
		return "", "", false
	}
	segments := strings.SplitN(raw[len(scheme):], "/", 4)
	if len(segments) != 4 {
		return "", "", false
	}
	host, owner, repository := segments[0], segments[1], segments[2]
	if host == "" || owner == "" || repository == "" {
		return "", "", false
	}
	return scheme + host + "/" + owner + "/" + repository, segments[3], true
}
