package ownapplication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
)

// Guard C producer: one own-application image change set carries an admitted
// image identity before any effect runs. The node's owner-signed trust names
// the Sigstore roots and exact keyless signers; the update supplies only the
// candidate Sigstore bundle. The node verifies the bundle's digest, OIDC
// issuer and signer subject against its own trust and refuses a mismatch before
// a container is stopped or a checkpoint is taken.
//
// The admission is bound to the one stateless image change it describes: the
// application name, the target image, the resolved digest, the node trust
// digest and the previously admitted image/configuration. A different tag, a
// moved tag, a different signer or a reused change set cannot satisfy it.
const (
	// ImageAdmissionSchema is the wire version of one admitted image identity.
	ImageAdmissionSchema = "stackkit.image-admission/v1"

	maxImageIdentityPolicySigners = 8
	maxImageIdentityBundleBytes   = 2 << 20
	maxImageIdentityFieldBytes    = 512
)

var (
	imageIdentityDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	imageIdentityHexPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	imageIdentitySubjectChars  = regexp.MustCompile(`^[^\s\x00-\x1f\x7f]+$`)
	imageOperationIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// ImageIdentityEvidence is the candidate evidence of one tag update: a Sigstore
// bundle whose signed artifact digest is the exact resolved image digest. It
// carries no trust root and no signer allowlist; those are node configuration.
// A signature over a separate simple-signing payload is a different form and is
// refused rather than interpreted. The evidence never carries a private key and
// is hashed into the update's review digest, so the reviewed identity is the
// executed identity.
type ImageIdentityEvidence struct {
	Bundle []byte `json:"bundle"`
}

// AdmittedImage is the image and configuration custody of one side of an
// image change. Image is the declared reference, Resolved is the digest-pinned
// registry reference, ImageID is the node-derived OCI image configuration and
// BundleRevision is the signed own-application bundle the image ran with.
type AdmittedImage struct {
	Image          string `json:"image"`
	Resolved       string `json:"resolvedImage"`
	ImageID        string `json:"imageID,omitempty"`
	BundleRevision string `json:"bundleRevision,omitempty"`
}

// ImageAdmission is the deterministic receipt of one admitted image identity.
// ChangeSet is content-addressed over the application, both sides of the image
// change and the node trust, so it names exactly one stateless image change.
// ImageID, BundleRevision and ReviewDigest are filled once the execution has
// admitted the image and computed the update's review digest.
type ImageAdmission struct {
	SchemaVersion  string        `json:"schemaVersion"`
	Name           string        `json:"name"`
	ChangeSet      string        `json:"changeSet"`
	OperationID    string        `json:"operationId,omitempty"`
	ReviewDigest   string        `json:"reviewDigest,omitempty"`
	Image          string        `json:"image"`
	Resolved       string        `json:"resolvedImage"`
	Digest         string        `json:"digest"`
	ImageID        string        `json:"imageID,omitempty"`
	BundleRevision string        `json:"bundleRevision,omitempty"`
	Signer         string        `json:"signer"`
	Issuer         string        `json:"issuer"`
	TrustDigest    string        `json:"trustDigest"`
	Previous       AdmittedImage `json:"previous"`
}

// Validate refuses evidence that cannot be verified without a permissive
// fallback. The bundle is bounded; trust is checked separately from the node
// configuration.
func (e ImageIdentityEvidence) Validate() error {
	if len(e.Bundle) == 0 || len(e.Bundle) > maxImageIdentityBundleBytes {
		return fmt.Errorf("image identity evidence requires one bundle of 1..%d bytes", maxImageIdentityBundleBytes)
	}
	return nil
}

// Validate refuses an admission that does not name one exact image change.
func (a ImageAdmission) Validate() error {
	if a.SchemaVersion != ImageAdmissionSchema {
		return fmt.Errorf("image admission schema %q is not %s", a.SchemaVersion, ImageAdmissionSchema)
	}
	if !contract.ValidName(a.Name) {
		return errors.New("image admission names an invalid own application")
	}
	if !imageIdentityDigestPattern.MatchString(a.ChangeSet) {
		return errors.New("image admission has no content-addressed change set")
	}
	if a.OperationID != "" && !imageOperationIDPattern.MatchString(a.OperationID) {
		return errors.New("image admission operation ID is invalid")
	}
	if a.ReviewDigest != "" && !imageIdentityDigestPattern.MatchString(a.ReviewDigest) {
		return errors.New("image admission review digest is invalid")
	}
	if _, declared, err := contract.SplitImage(a.Image); err != nil {
		return fmt.Errorf("image admission image: %w", err)
	} else if declared != "" && declared != a.Digest {
		return errors.New("image admission declared digest differs from its resolved image")
	}
	_, digest, err := contract.SplitImage(a.Resolved)
	if err != nil || digest == "" {
		return errors.New("image admission requires a digest-pinned resolved image")
	}
	if a.Digest != digest || !imageIdentityDigestPattern.MatchString(a.Digest) {
		return errors.New("image admission digest differs from its resolved image")
	}
	if a.ImageID != "" && !imageIdentityDigestPattern.MatchString(a.ImageID) {
		return errors.New("image admission image configuration identity is invalid")
	}
	if a.BundleRevision != "" && !imageIdentityDigestPattern.MatchString(a.BundleRevision) {
		return errors.New("image admission bundle revision is invalid")
	}
	if len(a.Signer) == 0 || len(a.Signer) > maxImageIdentityFieldBytes || !imageIdentitySubjectChars.MatchString(a.Signer) {
		return errors.New("image admission has no bounded signer")
	}
	if !strings.HasPrefix(a.Issuer, "https://") || len(a.Issuer) > maxImageIdentityFieldBytes || !imageIdentitySubjectChars.MatchString(a.Issuer) {
		return errors.New("image admission has no bounded https issuer")
	}
	if !imageIdentityDigestPattern.MatchString(a.TrustDigest) {
		return errors.New("image admission has no trust digest")
	}
	return a.Previous.validate()
}

func (i AdmittedImage) validate() error {
	if _, _, err := contract.SplitImage(i.Image); err != nil {
		return fmt.Errorf("admitted image reference: %w", err)
	}
	_, digest, err := contract.SplitImage(i.Resolved)
	if err != nil || digest == "" {
		return errors.New("admitted image requires a digest-pinned resolved reference")
	}
	if i.ImageID != "" && !imageIdentityDigestPattern.MatchString(i.ImageID) {
		return errors.New("admitted image configuration identity is invalid")
	}
	if i.BundleRevision != "" && !imageIdentityDigestPattern.MatchString(i.BundleRevision) {
		return errors.New("admitted image bundle revision is invalid")
	}
	return nil
}

func (s ImageSigner) validate() error {
	if !strings.HasPrefix(s.Issuer, "https://") || len(s.Issuer) > maxImageIdentityFieldBytes || !imageIdentitySubjectChars.MatchString(s.Issuer) {
		return errors.New("image identity signer issuer must be one bounded https OIDC issuer")
	}
	if len(s.Subject) == 0 || len(s.Subject) > maxImageIdentityFieldBytes || !imageIdentitySubjectChars.MatchString(s.Subject) {
		return errors.New("image identity signer subject must be one bounded exact subject alternative name")
	}
	return nil
}

// admitImageIdentity verifies the candidate bundle against the node's installed
// trust and binds the resulting identity to one stateless image change. It
// performs no effect; a refusal leaves the running application untouched.
func admitImageIdentity(ctx context.Context, evidence ImageIdentityEvidence, trust ImageIdentityTrust, name, operationID, image, resolved string, previous AdmittedImage) (ImageAdmission, error) {
	if err := ctx.Err(); err != nil {
		return ImageAdmission{}, err
	}
	if err := evidence.Validate(); err != nil {
		return ImageAdmission{}, err
	}
	if err := trust.Validate(); err != nil {
		return ImageAdmission{}, err
	}
	if err := previous.validate(); err != nil {
		return ImageAdmission{}, fmt.Errorf("previous admitted image: %w", err)
	}
	_, digest, err := contract.SplitImage(resolved)
	if err != nil || digest == "" {
		return ImageAdmission{}, errors.New("an image identity admission requires the resolved digest-pinned image")
	}
	if _, _, err = contract.SplitImage(image); err != nil {
		return ImageAdmission{}, fmt.Errorf("admitted image reference: %w", err)
	}
	signer, err := verifyImageIdentityBundle(ctx, trust, digest, evidence.Bundle)
	if err != nil {
		return ImageAdmission{}, err
	}
	trustDigest := trust.Digest()
	return ImageAdmission{
		SchemaVersion: ImageAdmissionSchema,
		Name:          name,
		ChangeSet: hash(struct {
			Name        string
			Image       string
			Resolved    string
			Digest      string
			TrustDigest string
			Previous    AdmittedImage
		}{name, image, resolved, digest, trustDigest, previous}),
		OperationID: operationID,
		Image:       image,
		Resolved:    resolved,
		Digest:      digest,
		Signer:      signer.Subject,
		Issuer:      signer.Issuer,
		TrustDigest: trustDigest,
		Previous:    previous,
	}, nil
}

// verifyImageIdentityBundle is the composition seam: production calls the
// Sigstore verifier; tests replace only this verifier, never the admission
// decision around it.
var verifyImageIdentityBundle = verifyImageIdentity

// verifyImageIdentity verifies one Sigstore bundle over the exact image digest
// and returns the trust signer it matched. A transparency log entry and an
// observer timestamp are both required; there is no unsigned or best-effort
// path.
func verifyImageIdentity(ctx context.Context, trust ImageIdentityTrust, digest string, bundleJSON []byte) (ImageSigner, error) {
	rawDigest, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if err != nil || len(rawDigest) != sha256.Size {
		return ImageSigner{}, errors.New("image identity digest is not a SHA-256 digest")
	}
	return verifyImageIdentityDigest(ctx, trust, "sha256", rawDigest, bundleJSON)
}

// verifyImageIdentityDigest is the shared verifier for the production SHA-256
// image digest and the SHA-512 subject digest of the upstream Sigstore fixture
// used by the real-verifier tests.
func verifyImageIdentityDigest(ctx context.Context, trust ImageIdentityTrust, algorithm string, rawDigest []byte, bundleJSON []byte) (ImageSigner, error) {
	trustedMaterial, err := root.NewTrustedRootFromJSON(bytes.TrimSpace(trust.TrustedRoot))
	if err != nil {
		return ImageSigner{}, fmt.Errorf("image identity trusted root is invalid: %w", err)
	}
	var candidate bundle.Bundle
	if err = candidate.UnmarshalJSON(bundleJSON); err != nil {
		return ImageSigner{}, fmt.Errorf("image identity bundle is invalid: %w", err)
	}
	verifier, err := verify.NewVerifier(trustedMaterial,
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1))
	if err != nil {
		return ImageSigner{}, fmt.Errorf("image identity verifier: %w", err)
	}
	var last error
	for _, signer := range trust.Signers {
		if err = ctx.Err(); err != nil {
			return ImageSigner{}, err
		}
		identity, e := verify.NewShortCertificateIdentity(signer.Issuer, "", signer.Subject, "")
		if e != nil {
			last = e
			continue
		}
		result, e := verifier.Verify(&candidate, verify.NewPolicy(
			verify.WithArtifactDigest(algorithm, rawDigest),
			verify.WithCertificateIdentity(identity),
		))
		if e != nil {
			last = e
			continue
		}
		if result.Signature == nil || result.Signature.Certificate == nil {
			last = errors.New("verified image bundle carries no signing certificate")
			continue
		}
		if result.Signature.Certificate.SubjectAlternativeName != signer.Subject {
			last = errors.New("verified image certificate subject differs from the admitted signer")
			continue
		}
		return signer, nil
	}
	if last == nil {
		last = errors.New("no image identity signer matched")
	}
	return ImageSigner{}, fmt.Errorf("image identity refused: %w", last)
}

// admittedImage names the current image and configuration custody of a record.
func admittedImage(b contract.Bundle, revision string) AdmittedImage {
	return AdmittedImage{Image: b.Application.Image, Resolved: b.ResolvedImage, ImageID: b.ImageID, BundleRevision: revision}
}
