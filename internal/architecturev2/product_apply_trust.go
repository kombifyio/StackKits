package architecturev2

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	productApplyTrustAPIVersion = "stackkit.product-apply-trust/v1"
	productApplyTrustKind       = "ProductApplyTrust"
	productApplyTrustMaxBytes   = 1 << 20
)

type productApplyTrustDocument struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Producers  []productApplyTrustAnchor `json:"producers"`
}

type productApplyTrustAnchor struct {
	Producer         generationartifact.ApplyEvidenceProducer `json:"producer"`
	PublicKey        string                                   `json:"publicKey"`
	RequirementKinds []string                                 `json:"requirementKinds"`
	publicKey        ed25519.PublicKey
}

func productApplyTrustStorePath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve product Apply trust directory: %w", err)
	}
	return filepath.Join(root, "stackkit", "apply-producers.json"), nil
}

func loadDefaultProductApplyTrust() ([]productApplyTrustAnchor, error) {
	path, err := productApplyTrustStorePath()
	if err != nil {
		return nil, err
	}
	return loadProductApplyTrust(path)
}

func loadProductApplyTrust(path string) ([]productApplyTrustAnchor, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []productApplyTrustAnchor{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect product Apply trust store: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("product Apply trust store must be a regular non-symlink file")
	}
	if info.Size() > productApplyTrustMaxBytes {
		return nil, fmt.Errorf("product Apply trust store exceeds %d bytes", productApplyTrustMaxBytes)
	}
	// Windows exposes synthetic POSIX mode bits; integrity there is inherited
	// from the fixed per-user config directory ACL. Unix-like systems can and
	// must additionally reject group/world-writable trust files directly.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("product Apply trust store must not be group- or world-writable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open product Apply trust store: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened product Apply trust store: %w", err)
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("product Apply trust store changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, productApplyTrustMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read product Apply trust store: %w", err)
	}
	if len(data) > productApplyTrustMaxBytes {
		return nil, fmt.Errorf("product Apply trust store exceeds %d bytes", productApplyTrustMaxBytes)
	}
	var document productApplyTrustDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode product Apply trust store: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode product Apply trust store: %w", err)
	}
	canonical, err := resolvedplan.CanonicalJSON(document)
	if err != nil {
		return nil, fmt.Errorf("canonicalize product Apply trust store: %w", err)
	}
	if !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("product Apply trust store must be canonical JSON")
	}
	if document.APIVersion != productApplyTrustAPIVersion || document.Kind != productApplyTrustKind || len(document.Producers) == 0 {
		return nil, fmt.Errorf("product Apply trust store must be %s %s with at least one producer", productApplyTrustAPIVersion, productApplyTrustKind)
	}
	previousKey := ""
	for index := range document.Producers {
		anchor := &document.Producers[index]
		if anchor.Producer.KeyID <= previousKey {
			return nil, fmt.Errorf("product Apply trust producers must be strictly sorted and unique by keyId")
		}
		key, err := base64.RawStdEncoding.Strict().DecodeString(anchor.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize || base64.RawStdEncoding.EncodeToString(key) != anchor.PublicKey {
			return nil, fmt.Errorf("product Apply trust producer %q has an invalid canonical Ed25519 public key", anchor.Producer.ID)
		}
		anchor.publicKey = append(ed25519.PublicKey(nil), key...)
		if err := generationartifact.ValidateApplyEvidenceProducerAnchor(anchor.Producer, anchor.publicKey); err != nil {
			return nil, fmt.Errorf("validate product Apply trust producer %q: %w", anchor.Producer.ID, err)
		}
		if err := validateProductApplyRequirementKinds(anchor.RequirementKinds); err != nil {
			return nil, fmt.Errorf("validate product Apply trust producer %q: %w", anchor.Producer.ID, err)
		}
		previousKey = anchor.Producer.KeyID
	}
	return append([]productApplyTrustAnchor(nil), document.Producers...), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values are forbidden")
		}
		return err
	}
	return nil
}

func validateProductApplyRequirementKinds(kinds []string) error {
	if len(kinds) == 0 {
		return fmt.Errorf("at least one requirement kind is required")
	}
	previous := ""
	for _, kind := range kinds {
		if kind <= previous {
			return fmt.Errorf("requirement kinds must be strictly sorted and unique")
		}
		if !strings.Contains(" evidence health host provider-owner runtime secret workload ", " "+kind+" ") {
			return fmt.Errorf("unsupported requirement kind %q", kind)
		}
		previous = kind
	}
	return nil
}

func materializeProductApplyTrust(plan generationartifact.VerifiedPlan, anchors []productApplyTrustAnchor) (map[string]generationartifact.ApplyEvidenceProducerTrust, error) {
	request, err := plan.ApplyEvidenceRequest()
	if err != nil {
		return nil, err
	}
	result := make(map[string]generationartifact.ApplyEvidenceProducerTrust, len(anchors))
	for _, anchor := range anchors {
		allowedKinds := make(map[string]struct{}, len(anchor.RequirementKinds))
		for _, kind := range anchor.RequirementKinds {
			allowedKinds[kind] = struct{}{}
		}
		var receiptIDs []string
		for _, expectation := range request.Expectations {
			if _, allowed := allowedKinds[expectation.RequirementKind]; allowed {
				receiptIDs = append(receiptIDs, expectation.ReceiptID)
			}
		}
		if len(receiptIDs) == 0 {
			continue
		}
		sort.Strings(receiptIDs)
		trust := generationartifact.ApplyEvidenceProducerTrust{
			Producer: anchor.Producer, PublicKey: append(ed25519.PublicKey(nil), anchor.publicKey...),
			RequirementKinds: append([]string(nil), anchor.RequirementKinds...), ReceiptIDs: receiptIDs,
		}
		if err := generationartifact.ValidateApplyEvidenceProducerTrust(trust); err != nil {
			return nil, err
		}
		result[anchor.Producer.KeyID] = trust
	}
	return result, nil
}
