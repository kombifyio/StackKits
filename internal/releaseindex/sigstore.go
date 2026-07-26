package releaseindex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// SigstoreVerifier verifies a cached GitHub artifact-attestation bundle against
// the exact cached trusted root, archive digest, GitHub OIDC issuer, workflow
// identity, predicate type, and subject name declared by the release index.
// It performs no network requests.
type SigstoreVerifier struct{}

func (SigstoreVerifier) Verify(ctx context.Context, input AttestationInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trustedRootJSON, err := os.ReadFile(input.TrustedRootPath)
	if err != nil {
		return fmt.Errorf("read cached Sigstore trusted root: %w", err)
	}
	bundleJSONL, err := os.ReadFile(input.BundlePath)
	if err != nil {
		return fmt.Errorf("read cached attestation bundle: %w", err)
	}
	archiveDigest, err := hex.DecodeString(input.Asset.Archive.SHA256)
	if err != nil {
		return fmt.Errorf("decode archive digest: %w", err)
	}
	return verifyBundle(
		ctx, trustedRootJSON, bundleJSONL, archiveDigest,
		input.Asset.Attestation.Issuer,
		input.Asset.Attestation.CertificateIdentity,
		input.Asset.Attestation.Subject,
		input.Asset.Attestation.PredicateType,
		input.Asset.Archive.SHA256,
		"archive",
	)
}

func (SigstoreVerifier) VerifyIndex(ctx context.Context, input IndexAttestationInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := parseVersion(input.Version); err != nil {
		return fmt.Errorf("release index version: %w", err)
	}
	digest := sha256.Sum256(input.Index)
	digestHex := hex.EncodeToString(digest[:])
	return verifyBundle(
		ctx, input.TrustedRoot, input.Bundle, digest[:],
		GitHubOIDCIssuer,
		"https://github.com/kombifyio/StackKits/.github/workflows/release.yml@refs/tags/"+input.Version,
		ReleaseIndexAssetName,
		GitHubAttestationPredicate,
		digestHex,
		"release index",
	)
}

func verifyBundle(
	ctx context.Context,
	trustedRootJSON []byte,
	bundleJSONL []byte,
	artifactDigest []byte,
	issuer string,
	identity string,
	subjectName string,
	predicateType string,
	digestHex string,
	label string,
) error {
	trustedMaterial, err := parseTrustedRootJSONL(trustedRootJSON)
	if err != nil {
		return fmt.Errorf("parse cached Sigstore trusted root: %w", err)
	}
	verifier, err := verify.NewVerifier(
		trustedMaterial,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("construct Sigstore verifier: %w", err)
	}
	certificateIdentity, err := verify.NewShortCertificateIdentity(
		issuer,
		"",
		identity,
		"",
	)
	if err != nil {
		return fmt.Errorf("construct GitHub workflow identity policy: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(bundleJSONL))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	verified := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var candidate bundle.Bundle
		if err := candidate.UnmarshalJSON([]byte(line)); err != nil {
			return fmt.Errorf("parse Sigstore bundle: %w", err)
		}
		result, err := verifier.Verify(&candidate, verify.NewPolicy(
			verify.WithArtifactDigest("sha256", artifactDigest),
			verify.WithCertificateIdentity(certificateIdentity),
		))
		if err != nil {
			continue
		}
		if result.Statement == nil || result.Statement.GetPredicateType() != predicateType {
			continue
		}
		for _, subject := range result.Statement.GetSubject() {
			if subject.GetName() == subjectName && subject.GetDigest()["sha256"] == digestHex {
				verified = true
				break
			}
		}
		if verified {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read cached attestation bundle: %w", err)
	}
	if !verified {
		return fmt.Errorf("no Sigstore bundle satisfied the exact %s, GitHub workflow identity, predicate, and subject policy", label)
	}
	return nil
}

// parseTrustedRootJSONL accepts both the single JSON trusted-root document
// emitted by Sigstore tooling and the JSONL collection emitted by
// `gh attestation trusted-root`. GitHub documents that the collection contains
// the public-good and GitHub private Sigstore roots so an offline consumer can
// select the material matching the included bundle.
func parseTrustedRootJSONL(data []byte) (root.TrustedMaterial, error) {
	if trustedRoot, err := root.NewTrustedRootFromJSON(bytes.TrimSpace(data)); err == nil {
		return trustedRoot, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	materials := root.TrustedMaterialCollection{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		trustedRoot, err := root.NewTrustedRootFromJSON(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		materials = append(materials, trustedRoot)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read JSONL: %w", err)
	}
	if len(materials) == 0 {
		return nil, fmt.Errorf("trusted root collection is empty")
	}
	return materials, nil
}
