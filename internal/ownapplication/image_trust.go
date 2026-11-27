package ownapplication

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Guard C node trust: the trusted Sigstore roots and the exact keyless signers
// this node accepts for own-application images. The document is owner-signed
// and installed out of band by the node composition root. It is never read
// from an update request, so a caller cannot bring its own root and signer.
//
// On a managed node the own-application.lifecycle capability reviews the update
// digest, not this trust document; the trust document's authority comes from
// the pinned Authority signature below.
const (
	// ImageIdentityTrustSchema is the only node trust document accepted.
	ImageIdentityTrustSchema = "stackkit.image-identity-trust/v1"
	imageIdentityTrustPath   = ".stackkit/image-identity-trust.json"
	maxImageIdentityTrust    = 4 << 20
)

// ErrImageIdentityTrustUnavailable means this node has no installed image
// trust, so no image identity can be admitted.
var ErrImageIdentityTrustUnavailable = errors.New("own application image identity trust is not installed")

// ImageIdentityTrust is the owner-signed node trust policy. The trusted root
// digest must be named by TrustedRootSHA256, so a replaced root is refused even
// when it parses. Signers is a closed allowlist.
type ImageIdentityTrust struct {
	SchemaVersion     string                                        `json:"schemaVersion"`
	TrustedRoot       []byte                                        `json:"trustedRoot"`
	TrustedRootSHA256 []string                                      `json:"trustedRootSha256"`
	Signers           []ImageSigner                                 `json:"signers"`
	Signature         localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

// ImageSigner is one exact keyless signer an image may come from. Issuer is
// the OIDC issuer of the signing certificate; Subject is the exact certificate
// subject alternative name.
type ImageSigner struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

func (t ImageIdentityTrust) signingBytes() []byte {
	t.Signature = localevidence.OwnerLifecycleMutationSignature{}
	raw, _ := json.Marshal(t)
	return raw
}

// Validate refuses trust this node cannot apply safely.
func (t ImageIdentityTrust) Validate() error {
	if t.SchemaVersion != ImageIdentityTrustSchema {
		return fmt.Errorf("image identity trust schema %q is not %s", t.SchemaVersion, ImageIdentityTrustSchema)
	}
	trustedRoot := bytes.TrimSpace(t.TrustedRoot)
	if len(trustedRoot) == 0 || len(trustedRoot) > maxImageIdentityTrust {
		return errors.New("image identity trust requires one bounded Sigstore trusted root")
	}
	sum := sha256.Sum256(trustedRoot)
	digest := hex.EncodeToString(sum[:])
	admitted := false
	for _, candidate := range t.TrustedRootSHA256 {
		if !imageIdentityHexPattern.MatchString(candidate) {
			return errors.New("image identity trust trusted-root digests must be lowercase SHA-256")
		}
		if candidate == digest {
			admitted = true
		}
	}
	if !admitted {
		return fmt.Errorf("image identity trust trusted root %s is not named by its own digest allowlist", digest)
	}
	if len(t.Signers) == 0 || len(t.Signers) > maxImageIdentityPolicySigners {
		return fmt.Errorf("image identity trust requires 1..%d signers", maxImageIdentityPolicySigners)
	}
	seen := make(map[ImageSigner]struct{}, len(t.Signers))
	for _, signer := range t.Signers {
		if err := signer.validate(); err != nil {
			return err
		}
		if _, duplicate := seen[signer]; duplicate {
			return errors.New("image identity trust repeats a signer")
		}
		seen[signer] = struct{}{}
	}
	if _, err := root.NewTrustedRootFromJSON(trustedRoot); err != nil {
		return fmt.Errorf("image identity trust trusted root is invalid: %w", err)
	}
	return nil
}

// Digest is the stable digest of the exact trust policy.
func (t ImageIdentityTrust) Digest() string {
	trustedRoot := bytes.TrimSpace(t.TrustedRoot)
	sum := sha256.Sum256(trustedRoot)
	return hash(struct {
		SchemaVersion     string
		TrustedRootSHA256 string
		Allowlist         []string
		Signers           []ImageSigner
	}{t.SchemaVersion, hex.EncodeToString(sum[:]), t.TrustedRootSHA256, t.Signers})
}

// SaveImageIdentityTrust installs the node trust document under owner custody.
// The composition root calls it; an update request never writes trust.
func SaveImageIdentityTrust(workspace string, trust ImageIdentityTrust) error {
	trust.Signature = localevidence.OwnerLifecycleMutationSignature{}
	if err := trust.Validate(); err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(workspace, trust.signingBytes())
	if err != nil {
		return err
	}
	trust.Signature = signature
	raw, err := json.Marshal(trust)
	if err != nil || len(raw) > maxImageIdentityTrust {
		return errors.New("image identity trust exceeds its bound")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err = transaction.MkdirAll(".stackkit", 0700); err != nil {
		transaction.Close()
		return err
	}
	transaction.Close()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600(imageIdentityTrustPath, raw)
	return err
}

// LoadImageIdentityTrust reads and verifies this node's installed trust. A
// missing, malformed, tampered or foreign document is an error; there is no
// fallback root.
func LoadImageIdentityTrust(workspace string) (ImageIdentityTrust, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return ImageIdentityTrust{}, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return ImageIdentityTrust{}, err
	}
	defer transaction.Close()
	raw, _, err := transaction.ReadStableBounded(imageIdentityTrustPath, maxImageIdentityTrust)
	if errors.Is(err, os.ErrNotExist) {
		return ImageIdentityTrust{}, ErrImageIdentityTrustUnavailable
	}
	if err != nil {
		return ImageIdentityTrust{}, err
	}
	var trust ImageIdentityTrust
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&trust) != nil || decoder.Decode(new(any)) != io.EOF {
		return ImageIdentityTrust{}, errors.New("image identity trust document is malformed")
	}
	if err = trust.Validate(); err != nil {
		return ImageIdentityTrust{}, err
	}
	authority, err := localevidence.LoadHomeAuthorityPublic(workspace)
	if err != nil {
		return ImageIdentityTrust{}, err
	}
	if err = authority.VerifyLifecycleMutation(trust.signingBytes(), trust.Signature); err != nil {
		return ImageIdentityTrust{}, err
	}
	return trust, nil
}

// loadNodeImageTrust is the composition seam. Tests replace it with an
// in-memory node trust; production always reads the installed document.
var loadNodeImageTrust = LoadImageIdentityTrust
