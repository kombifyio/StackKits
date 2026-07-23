package applyevidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"time"
)

var (
	idPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	semVerPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	keyPattern    = regexp.MustCompile(`^ed25519://sha256/[a-f0-9]{64}$`)
)

// ProducerKeyID derives the public, provider-neutral identity of an Ed25519
// producer key.
func ProducerKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return "ed25519://sha256/" + hex.EncodeToString(digest[:])
}

// SignReceipt creates one exact satisfied receipt for an expectation already
// issued by the product authority. Observation collection remains caller-owned.
func SignReceipt(input ReceiptInput, privateKey ed25519.PrivateKey) (Receipt, error) {
	if len(privateKey) != ed25519.PrivateKeySize || ProducerKeyID(privateKey.Public().(ed25519.PublicKey)) != input.Producer.KeyID {
		return Receipt{}, errors.New("applyevidence: private key does not match producer keyId")
	}
	if err := validateRequest(input.Request); err != nil {
		return Receipt{}, err
	}
	if !containsExpectation(input.Request.Expectations, input.Expectation) {
		return Receipt{}, errors.New("applyevidence: expectation is not an exact member of the request")
	}
	receipt := Receipt{
		APIVersion: ReceiptAPIVersion, Kind: ReceiptKind,
		ID: input.Expectation.ReceiptID, RequirementKind: input.Expectation.RequirementKind,
		RequirementID: input.Expectation.RequirementID, RequirementHash: input.Expectation.RequirementHash,
		Binding: input.Request.Binding, ManifestHash: input.ManifestHash, Executor: input.Executor,
		Subject: input.Expectation.Subject, Result: "satisfied", Producer: input.Producer,
		ObservationRef: input.ObservationRef, ObservedAt: canonicalTime(input.ObservedAt), ValidUntil: canonicalTime(input.ValidUntil),
	}
	if err := validateReceiptUnsigned(receipt); err != nil {
		return Receipt{}, err
	}
	signingBytes, err := canonicalJSON(receipt)
	if err != nil {
		return Receipt{}, err
	}
	receipt.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(privateKey, signingBytes))
	receipt.ReceiptDigest, err = receiptDigest(receipt)
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// SealBundle exact-matches signed receipts to the producer request and returns
// the canonical hash-bound envelope. It never reads a key or performs I/O.
func SealBundle(request Request, manifestHash string, executor ExecutorIdentity, receipts []Receipt) (Bundle, error) {
	if err := validateRequest(request); err != nil {
		return Bundle{}, err
	}
	copyReceipts := append([]Receipt(nil), receipts...)
	sort.Slice(copyReceipts, func(i, j int) bool { return copyReceipts[i].ID < copyReceipts[j].ID })
	if len(copyReceipts) != len(request.Expectations) {
		return Bundle{}, fmt.Errorf("applyevidence: got %d receipts, want exact set of %d", len(copyReceipts), len(request.Expectations))
	}
	for index, expectation := range request.Expectations {
		receipt := copyReceipts[index]
		if err := validateReceipt(receipt); err != nil {
			return Bundle{}, err
		}
		if receipt.ID != expectation.ReceiptID || receipt.RequirementKind != expectation.RequirementKind ||
			receipt.RequirementID != expectation.RequirementID || receipt.RequirementHash != expectation.RequirementHash || receipt.Subject != expectation.Subject ||
			receipt.Binding != request.Binding || receipt.ManifestHash != manifestHash || receipt.Executor != executor {
			return Bundle{}, fmt.Errorf("applyevidence: receipt %q does not match its exact request, manifest, and executor", receipt.ID)
		}
	}
	bundle := Bundle{
		APIVersion: BundleAPIVersion, Kind: BundleKind, Binding: request.Binding, ManifestHash: manifestHash,
		Executor: executor, RequirementsHash: request.RequirementsHash, Receipts: copyReceipts,
	}
	if err := validateBundleUnsigned(bundle); err != nil {
		return Bundle{}, err
	}
	canonical, err := canonicalJSON(bundle)
	if err != nil {
		return Bundle{}, err
	}
	bundle.BundleHash = hash(canonical)
	return bundle, nil
}

// MarshalCanonical returns the byte representation consumed by StackKits.
func MarshalCanonical(bundle Bundle) ([]byte, error) {
	if err := validateBundle(bundle); err != nil {
		return nil, err
	}
	return canonicalJSON(bundle)
}

func validateRequest(request Request) error {
	if request.APIVersion != RequestAPIVersion || !validBinding(request.Binding) || !digestPattern.MatchString(request.RequirementsHash) || len(request.Expectations) == 0 {
		return errors.New("applyevidence: invalid producer request")
	}
	previous := ""
	for _, expectation := range request.Expectations {
		if expectation.ReceiptID <= previous || expectation.ReceiptID != expectation.RequirementKind+"/"+expectation.RequirementID ||
			!digestPattern.MatchString(expectation.RequirementHash) || expectation.Subject.OwnerKind == "" || expectation.Subject.OwnerRef == "" {
			return errors.New("applyevidence: expectations must be exact, sorted, and canonical")
		}
		if _, valid := observationPrefix(expectation.RequirementKind); !valid {
			return errors.New("applyevidence: unsupported requirement kind")
		}
		previous = expectation.ReceiptID
	}
	return nil
}

func validateReceiptUnsigned(receipt Receipt) error {
	if receipt.APIVersion != ReceiptAPIVersion || receipt.Kind != ReceiptKind || receipt.ID != receipt.RequirementKind+"/"+receipt.RequirementID ||
		!digestPattern.MatchString(receipt.RequirementHash) || !validBinding(receipt.Binding) || !digestPattern.MatchString(receipt.ManifestHash) ||
		!validExecutor(receipt.Executor) || receipt.Subject.OwnerKind == "" || receipt.Subject.OwnerRef == "" || receipt.Result != "satisfied" ||
		!idPattern.MatchString(receipt.Producer.ID) || !semVerPattern.MatchString(receipt.Producer.Version) || !keyPattern.MatchString(receipt.Producer.KeyID) {
		return errors.New("applyevidence: invalid receipt contract")
	}
	prefix, valid := observationPrefix(receipt.RequirementKind)
	if !valid || !regexp.MustCompile(`^`+regexp.QuoteMeta(prefix)+`://sha256/[a-f0-9]{64}$`).MatchString(receipt.ObservationRef) {
		return errors.New("applyevidence: invalid typed observation reference")
	}
	observedAt, err := parseCanonicalTime(receipt.ObservedAt)
	if err != nil {
		return err
	}
	validUntil, err := parseCanonicalTime(receipt.ValidUntil)
	if err != nil || !observedAt.Before(validUntil) || validUntil.Sub(observedAt) > MaxValidity {
		return errors.New("applyevidence: invalid receipt validity window")
	}
	if receipt.Signature != "" || receipt.ReceiptDigest != "" {
		return errors.New("applyevidence: unsigned receipt contains signature state")
	}
	return nil
}

func validateReceipt(receipt Receipt) error {
	unsigned := receipt
	unsigned.Signature, unsigned.ReceiptDigest = "", ""
	if err := validateReceiptUnsigned(unsigned); err != nil {
		return err
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(receipt.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawStdEncoding.EncodeToString(signature) != receipt.Signature {
		return errors.New("applyevidence: invalid canonical Ed25519 signature")
	}
	want, err := receiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return errors.New("applyevidence: receipt digest mismatch")
	}
	return nil
}

func receiptDigest(receipt Receipt) (string, error) {
	copy := receipt
	copy.ReceiptDigest = ""
	canonical, err := canonicalJSON(copy)
	if err != nil {
		return "", err
	}
	return hash(canonical), nil
}

func validateBundleUnsigned(bundle Bundle) error {
	if bundle.APIVersion != BundleAPIVersion || bundle.Kind != BundleKind || !validBinding(bundle.Binding) ||
		!digestPattern.MatchString(bundle.ManifestHash) || !digestPattern.MatchString(bundle.RequirementsHash) || !validExecutor(bundle.Executor) || len(bundle.Receipts) == 0 {
		return errors.New("applyevidence: invalid bundle contract")
	}
	for _, receipt := range bundle.Receipts {
		if err := validateReceipt(receipt); err != nil {
			return err
		}
	}
	if bundle.BundleHash != "" {
		return errors.New("applyevidence: unsigned bundle contains a hash")
	}
	return nil
}

func validateBundle(bundle Bundle) error {
	copy := bundle
	copy.BundleHash = ""
	if err := validateBundleUnsigned(copy); err != nil {
		return err
	}
	canonical, err := canonicalJSON(copy)
	if err != nil {
		return err
	}
	if bundle.BundleHash != hash(canonical) {
		return errors.New("applyevidence: bundle hash mismatch")
	}
	return nil
}

func validBinding(binding PlanBinding) bool {
	return digestPattern.MatchString(binding.PlanHash) && digestPattern.MatchString(binding.SpecHash) && digestPattern.MatchString(binding.InventoryHash) &&
		digestPattern.MatchString(binding.DefinitionHash) && binding.CompilerVersion != "" && binding.Renderer.ID != "" && binding.Renderer.Version != "" &&
		binding.Authority.Class != "" && binding.Authority.Document != "" && binding.Authority.Issuer != ""
}

func validExecutor(executor ExecutorIdentity) bool {
	return idPattern.MatchString(executor.ID) && semVerPattern.MatchString(executor.Version) && digestPattern.MatchString(executor.Digest)
}

func containsExpectation(values []Expectation, want Expectation) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func observationPrefix(kind string) (string, bool) {
	prefix, exists := map[string]string{
		"workload": "workload-observation", "secret": "secret-materialization", "runtime": "runtime-observation",
		"host": "host-observation", "provider-owner": "provider-owner-observation", "evidence": "evidence-observation", "health": "health-observation",
	}[kind]
	return prefix, exists
}

func canonicalTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, errors.New("applyevidence: timestamp must be canonical UTC RFC3339Nano")
	}
	return parsed, nil
}

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	if err := requireEOF(decoder); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("applyevidence: canonical JSON has trailing data")
	}
	return nil
}

func hash(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
