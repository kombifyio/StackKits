package releaseindex

import (
	"context"
	"errors"
	"time"
)

const (
	SchemaVersion                          = "stackkits-release-index/v1"
	ReceiptSchemaVersion                   = "stackkit.release-receipt/v1"
	ReleaseIndexAssetName                  = "stackkits-release-index-v1.json"
	ReleaseIndexAttestationAssetName       = "stackkits-release-index-v1.json.intoto.jsonl"
	TrustedRootAssetName                   = "sigstore-trusted-root.jsonl"
	ReleaseReceiptName                     = "release-receipt.json"
	SPDXJSONMediaType                      = "application/spdx+json"
	InTotoJSONLMediaType                   = "application/vnd.in-toto+jsonl"
	GitHubOIDCIssuer                       = "https://token.actions.githubusercontent.com"
	GitHubAttestationPredicate             = "https://slsa.dev/provenance/v1"
	SigstoreTrustedRootMediaType           = "application/vnd.dev.sigstore.trustedroot+json;version=0.1"
	DefaultMaxBlobBytes              int64 = 4 << 30
)

var (
	ErrDigestMismatch = errors.New("release blob digest mismatch")
	ErrNoRelease      = errors.New("no matching StackKits release")
)

type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
	ChannelEdge   Channel = "edge"
)

type Index struct {
	SchemaVersion string            `json:"schemaVersion"`
	Release       ReleaseDescriptor `json:"release"`
	Assets        []Asset           `json:"assets"`
}

type ReleaseDescriptor struct {
	Repository  string    `json:"repository"`
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"publishedAt"`
	TrustedRoot Blob      `json:"trustedRoot"`
}

type Asset struct {
	Kit         string      `json:"kit"`
	Version     string      `json:"version"`
	Channel     Channel     `json:"channel"`
	Platform    Platform    `json:"platform"`
	Archive     Blob        `json:"archive"`
	SBOM        Blob        `json:"sbom"`
	Attestation Attestation `json:"attestation"`
}

type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type Blob struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"mediaType"`
}

type Attestation struct {
	Blob
	Issuer              string `json:"issuer"`
	CertificateIdentity string `json:"certificateIdentity"`
	Subject             string `json:"subject"`
	PredicateType       string `json:"predicateType"`
}

type Release struct {
	TagName             string
	Prerelease          bool
	PublishedAt         time.Time
	IndexURL            string
	IndexAttestationURL string
	TrustedRootURL      string
}

type Source interface {
	ListReleases(context.Context) ([]Release, error)
	Fetch(context.Context, string, int64) ([]byte, error)
}

type ResolveRequest struct {
	Kit    string
	Target string
	OS     string
	Arch   string
}

type Resolution struct {
	Release             Release
	Index               Index
	Asset               Asset
	RawIndex            []byte
	RawIndexAttestation []byte
	RawTrustedRoot      []byte
}

type Receipt struct {
	SchemaVersion          string    `json:"schemaVersion"`
	Kit                    string    `json:"kit"`
	Version                string    `json:"version"`
	Channel                Channel   `json:"channel"`
	Platform               Platform  `json:"platform"`
	ArchiveSHA256          string    `json:"archiveSha256"`
	SBOMSHA256             string    `json:"sbomSha256"`
	AttestationSHA256      string    `json:"attestationSha256"`
	AttestationIssuer      string    `json:"attestationIssuer"`
	AttestationSubject     string    `json:"attestationSubject"`
	TrustedRootSHA256      string    `json:"trustedRootSha256"`
	IndexSHA256            string    `json:"indexSha256"`
	IndexAttestationSHA256 string    `json:"indexAttestationSha256"`
	VerifiedAt             time.Time `json:"verifiedAt"`
	InstallDir             string    `json:"installDir"`
}

type AttestationInput struct {
	Index           Index
	Asset           Asset
	ArchivePath     string
	SBOMPath        string
	BundlePath      string
	TrustedRootPath string
}

type IndexAttestationInput struct {
	Version     string
	Index       []byte
	Bundle      []byte
	TrustedRoot []byte
}

type AttestationVerifier interface {
	Verify(context.Context, AttestationInput) error
	VerifyIndex(context.Context, IndexAttestationInput) error
}
