package releaseindex

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// FixtureSource is a hermetic in-memory release source for contract and OSS E2E
// tests. Product code depends only on Source; it never branches on fixture mode.
type FixtureSource struct {
	releases []Release
	blobs    map[string][]byte
}

func NewFixtureSource() *FixtureSource {
	return &FixtureSource{blobs: make(map[string][]byte)}
}

func (source *FixtureSource) AddRelease(index Index, prerelease bool, publishedAt time.Time) error {
	if err := index.Validate(); err != nil {
		return fmt.Errorf("fixture index: %w", err)
	}
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	indexURL := "fixture://index/" + index.Release.Version
	indexAttestationURL := "fixture://index-attestation/" + index.Release.Version
	source.blobs[indexURL] = raw
	source.blobs[indexAttestationURL] = []byte(`{"dsseEnvelope":{"payloadType":"application/vnd.in-toto+json"}}`)
	source.blobs[index.Release.TrustedRoot.URL] = []byte(`{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.1"}`)
	asset := index.Assets[0]
	source.blobs[asset.Archive.URL] = []byte("archive-" + index.Release.Version)
	source.blobs[asset.SBOM.URL] = []byte(`{"spdxVersion":"SPDX-2.3"}`)
	source.blobs[asset.Attestation.URL] = []byte(`{"dsseEnvelope":{"payloadType":"application/vnd.in-toto+json"}}`)
	source.releases = append(source.releases, Release{
		TagName: index.Release.Version, Prerelease: prerelease, PublishedAt: publishedAt,
		IndexURL: indexURL, IndexAttestationURL: indexAttestationURL, TrustedRootURL: index.Release.TrustedRoot.URL,
	})
	return nil
}

func (source *FixtureSource) SetBlob(url string, data []byte) {
	source.blobs[url] = append([]byte(nil), data...)
}

func (source *FixtureSource) ListReleases(context.Context) ([]Release, error) {
	return append([]Release(nil), source.releases...), nil
}

func (source *FixtureSource) Fetch(_ context.Context, url string, limit int64) ([]byte, error) {
	data, exists := source.blobs[url]
	if !exists {
		return nil, fmt.Errorf("fixture blob %q not found", url)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("fixture blob %q exceeds %d bytes", url, limit)
	}
	return append([]byte(nil), data...), nil
}
