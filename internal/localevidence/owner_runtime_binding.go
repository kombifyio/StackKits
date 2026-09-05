package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	OwnerRuntimeBindingAPIVersion = "stackkit.owner-runtime-binding/v1"
	ownerRuntimeBindingRelPath    = ".stackkit/evidence/owner-runtime-binding.json"
	ownerEnrollmentAPIVersion     = "stackkit.pocketid-owner-enrollment/v1"
	ownerEnrollmentRelPath        = ".stackkit/custody/pocketid-owner-enrollment.json"
	ownerRuntimeBindingDomain     = "stackkit.owner-runtime-binding/v1\x00"
	ownerEnrollmentDomain         = "stackkit.pocketid-owner-enrollment/v1\x00"
)

var ErrOwnerRuntimeBindingMissing = errors.New("localevidence: no local owner runtime binding")

type OwnerRuntimeObservation struct {
	PocketIDSubject string
	Username        string
	Email           string
	DisplayName     string
	Groups          []string
}

// OwnerRuntimeBinding is a secret-free, owner-signed statement connecting
// PocketID's immutable user subject with the stable local ownerRef and the
// exact step-ca certificates established by init.
type OwnerRuntimeBinding struct {
	APIVersion                  string    `json:"apiVersion"`
	Kind                        string    `json:"kind"`
	OwnerRef                    string    `json:"ownerRef"`
	KeyID                       string    `json:"keyId"`
	PocketIDSubject             string    `json:"pocketIdSubject"`
	PocketIDUsername            string    `json:"pocketIdUsername"`
	PocketIDEmail               string    `json:"pocketIdEmail"`
	PocketIDDisplayName         string    `json:"pocketIdDisplayName"`
	PocketIDGroups              []string  `json:"pocketIdGroups"`
	OwnerCertificateSHA256      string    `json:"ownerCertificateSha256"`
	OwnerCertificateSerial      string    `json:"ownerCertificateSerial"`
	StepCARootCertificateSHA256 string    `json:"stepCaRootCertificateSha256"`
	BoundAt                     time.Time `json:"boundAt"`
	Signature                   string    `json:"signature"`
}

type PocketIDOwnerEnrollment struct {
	OwnerRef        string
	PocketIDSubject string
	SetupURL        string
	ExpiresAt       time.Time
}

type pocketIDOwnerEnrollmentRecord struct {
	APIVersion      string    `json:"apiVersion"`
	Kind            string    `json:"kind"`
	OwnerRef        string    `json:"ownerRef"`
	KeyID           string    `json:"keyId"`
	PocketIDSubject string    `json:"pocketIdSubject"`
	SetupURL        string    `json:"setupUrl"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Signature       string    `json:"signature"`
}

func EstablishOwnerRuntimeBinding(workspaceRoot string, observation OwnerRuntimeObservation) (OwnerRuntimeBinding, error) {
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	normalized, err := validateOwnerRuntimeObservation(owner, observation)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	existing, err := LoadOwnerRuntimeBinding(workspaceRoot)
	if err == nil {
		if existing.PocketIDSubject != normalized.PocketIDSubject ||
			existing.PocketIDUsername != normalized.Username ||
			existing.PocketIDEmail != normalized.Email ||
			existing.PocketIDDisplayName != normalized.DisplayName {
			return OwnerRuntimeBinding{}, errors.New("localevidence: observed PocketID owner differs from established runtime binding")
		}
		return existing, nil
	}
	if !errors.Is(err, ErrOwnerRuntimeBindingMissing) {
		return OwnerRuntimeBinding{}, err
	}
	ownerCertificate, err := parseCertificatePEM(owner.OwnerCertificatePEM, "owner")
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	rootCertificate, err := parseCertificatePEM(owner.StepCARootCertificatePEM, "step-ca root")
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	record := OwnerRuntimeBinding{
		APIVersion: OwnerRuntimeBindingAPIVersion, Kind: "LocalOwnerRuntimeBinding",
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID,
		PocketIDSubject: normalized.PocketIDSubject, PocketIDUsername: normalized.Username,
		PocketIDEmail: normalized.Email, PocketIDDisplayName: normalized.DisplayName,
		PocketIDGroups:              []string{"admins", "owners"},
		OwnerCertificateSHA256:      certificateDigest(ownerCertificate.Raw),
		OwnerCertificateSerial:      strings.ToLower(ownerCertificate.SerialNumber.Text(16)),
		StepCARootCertificateSHA256: certificateDigest(rootCertificate.Raw),
		BoundAt:                     time.Now().UTC().Truncate(time.Second),
	}
	if err := signOwnerRuntimeBinding(workspaceRoot, &record); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	path, err := confinedCustodyPath(workspaceRoot, ownerRuntimeBindingRelPath)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	if err := writePrivateJSON(path, record); err != nil {
		return OwnerRuntimeBinding{}, fmt.Errorf("localevidence: persist owner runtime binding: %w", err)
	}
	return LoadOwnerRuntimeBinding(workspaceRoot)
}

// DiscardOwnerRuntimeBinding removes the recorded projection of the PocketID
// owner: the signed subject binding and the one-time enrollment that names it.
// Both describe rows inside PocketID's own database, so a host whose runtime
// data was destroyed can only be rebuilt once they are gone. Owner custody, the
// owner key, and step-ca stay untouched; they are workspace-resident authority
// that survives any host wipe and keeps the rebuilt owner the same owner.
func DiscardOwnerRuntimeBinding(workspaceRoot string) error {
	for _, relative := range []string{ownerRuntimeBindingRelPath, ownerEnrollmentRelPath} {
		path, err := confinedCustodyPath(workspaceRoot, relative)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("localevidence: discard stale owner runtime projection: %w", err)
		}
	}
	return nil
}

func LoadOwnerRuntimeBinding(workspaceRoot string) (OwnerRuntimeBinding, error) {
	path, err := confinedCustodyPath(workspaceRoot, ownerRuntimeBindingRelPath)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed workspace evidence path
	if errors.Is(err, os.ErrNotExist) {
		return OwnerRuntimeBinding{}, ErrOwnerRuntimeBindingMissing
	}
	if err != nil {
		return OwnerRuntimeBinding{}, fmt.Errorf("localevidence: read owner runtime binding: %w", err)
	}
	if err := requirePrivateRuntimeFile(path); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	var record OwnerRuntimeBinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return OwnerRuntimeBinding{}, errors.New("localevidence: owner runtime binding is malformed")
	}
	canonical, err := json.MarshalIndent(record, "", "  ")
	if err != nil || !bytes.Equal(raw, canonical) {
		return OwnerRuntimeBinding{}, errors.New("localevidence: owner runtime binding is not canonical")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	observation := OwnerRuntimeObservation{
		PocketIDSubject: record.PocketIDSubject, Username: record.PocketIDUsername,
		Email: record.PocketIDEmail, DisplayName: record.PocketIDDisplayName,
		Groups: record.PocketIDGroups,
	}
	if _, err := validateOwnerRuntimeObservation(owner, observation); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	if record.APIVersion != OwnerRuntimeBindingAPIVersion ||
		record.Kind != "LocalOwnerRuntimeBinding" ||
		record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID ||
		record.BoundAt.IsZero() || record.BoundAt.Location() != time.UTC {
		return OwnerRuntimeBinding{}, errors.New("localevidence: owner runtime binding is not bound to established custody")
	}
	ownerCertificate, err := parseCertificatePEM(owner.OwnerCertificatePEM, "owner")
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	rootCertificate, err := parseCertificatePEM(owner.StepCARootCertificatePEM, "step-ca root")
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	if record.OwnerCertificateSHA256 != certificateDigest(ownerCertificate.Raw) ||
		record.OwnerCertificateSerial != strings.ToLower(ownerCertificate.SerialNumber.Text(16)) ||
		record.StepCARootCertificateSHA256 != certificateDigest(rootCertificate.Raw) {
		return OwnerRuntimeBinding{}, errors.New("localevidence: owner runtime binding certificate identity differs from step-ca custody")
	}
	if err := verifyOwnerRuntimeBindingSignature(workspaceRoot, record); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	return record, nil
}

func PersistPocketIDOwnerEnrollment(workspaceRoot string, enrollment PocketIDOwnerEnrollment) (string, error) {
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return "", err
	}
	if enrollment.OwnerRef != owner.OwnerRef || strings.TrimSpace(enrollment.PocketIDSubject) == "" ||
		enrollment.ExpiresAt.IsZero() || !enrollment.ExpiresAt.After(time.Now().UTC()) {
		return "", errors.New("localevidence: PocketID owner enrollment is not bound to the established owner")
	}
	runtimeCustody, err := LoadBasementRuntimeCustody(workspaceRoot)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(enrollment.SetupURL)
	if err != nil {
		return "", errors.New("localevidence: PocketID owner enrollment URL is not the fixed local endpoint")
	}
	query := parsed.Query()
	if parsed.Scheme != "https" || parsed.Host != "id."+runtimeCustody.Domain ||
		parsed.User != nil || parsed.Fragment != "" || parsed.Path != "/setup-account" ||
		len(query) != 1 || len(query["token"]) != 1 || query.Get("token") == "" {
		return "", errors.New("localevidence: PocketID owner enrollment URL is not the fixed local endpoint")
	}
	record := pocketIDOwnerEnrollmentRecord{
		APIVersion: ownerEnrollmentAPIVersion, Kind: "PocketIDOwnerEnrollment",
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID,
		PocketIDSubject: enrollment.PocketIDSubject, SetupURL: enrollment.SetupURL,
		ExpiresAt: enrollment.ExpiresAt.UTC().Truncate(time.Second),
	}
	path, err := confinedCustodyPath(workspaceRoot, ownerEnrollmentRelPath)
	if err != nil {
		return "", err
	}
	if existing, readErr := loadPocketIDOwnerEnrollment(workspaceRoot); readErr == nil {
		if existing.OwnerRef != record.OwnerRef || existing.PocketIDSubject != record.PocketIDSubject {
			return "", errors.New("localevidence: PocketID owner enrollment differs from established custody")
		}
		if existing.ExpiresAt.After(time.Now().UTC()) {
			return path, nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", readErr
	}
	if err := signPocketIDOwnerEnrollment(workspaceRoot, &record); err != nil {
		return "", err
	}
	if err := writePrivateJSON(path, record); err != nil {
		return "", fmt.Errorf("localevidence: persist PocketID owner enrollment: %w", err)
	}
	if _, err := loadPocketIDOwnerEnrollment(workspaceRoot); err != nil {
		return "", err
	}
	return path, nil
}

func validateOwnerRuntimeObservation(owner OwnerCustody, observation OwnerRuntimeObservation) (OwnerRuntimeObservation, error) {
	observation.PocketIDSubject = strings.TrimSpace(observation.PocketIDSubject)
	observation.Username = strings.TrimSpace(observation.Username)
	observation.Email = strings.TrimSpace(observation.Email)
	observation.DisplayName = strings.TrimSpace(observation.DisplayName)
	observation.Groups = append([]string(nil), observation.Groups...)
	sort.Strings(observation.Groups)
	observation.Groups = slices.Compact(observation.Groups)
	if observation.PocketIDSubject == "" || len(observation.PocketIDSubject) > 128 ||
		strings.ContainsAny(observation.PocketIDSubject, "/?#\\\x00") ||
		observation.Username != owner.PocketID.Username ||
		observation.Email != owner.PocketID.Email ||
		observation.DisplayName != owner.PocketID.DisplayName ||
		!slices.Contains(observation.Groups, "admins") ||
		!slices.Contains(observation.Groups, "owners") {
		return OwnerRuntimeObservation{}, errors.New("localevidence: PocketID owner observation differs from desired local projection")
	}
	return observation, nil
}

func signOwnerRuntimeBinding(workspaceRoot string, record *OwnerRuntimeBinding) error {
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return err
	}
	payload, err := ownerRuntimeBindingPayload(*record)
	if err != nil {
		return err
	}
	record.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, payload))
	return nil
}

func verifyOwnerRuntimeBindingSignature(workspaceRoot string, record OwnerRuntimeBinding) error {
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return err
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(record.Signature)
	payload, payloadErr := ownerRuntimeBindingPayload(record)
	if err != nil || payloadErr != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key.Public(), payload, signature) {
		return errors.New("localevidence: owner runtime binding signature does not verify")
	}
	return nil
}

func ownerRuntimeBindingPayload(record OwnerRuntimeBinding) ([]byte, error) {
	record.Signature = ""
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return domainDigest(ownerRuntimeBindingDomain, raw), nil
}

func signPocketIDOwnerEnrollment(workspaceRoot string, record *pocketIDOwnerEnrollmentRecord) error {
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return err
	}
	payload, err := pocketIDOwnerEnrollmentPayload(*record)
	if err != nil {
		return err
	}
	record.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, payload))
	return nil
}

func loadPocketIDOwnerEnrollment(workspaceRoot string) (pocketIDOwnerEnrollmentRecord, error) {
	path, err := confinedCustodyPath(workspaceRoot, ownerEnrollmentRelPath)
	if err != nil {
		return pocketIDOwnerEnrollmentRecord{}, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed private custody path
	if err != nil {
		return pocketIDOwnerEnrollmentRecord{}, err
	}
	if err := requirePrivateRuntimeFile(path); err != nil {
		return pocketIDOwnerEnrollmentRecord{}, err
	}
	var record pocketIDOwnerEnrollmentRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return pocketIDOwnerEnrollmentRecord{}, errors.New("localevidence: PocketID owner enrollment is malformed")
	}
	canonical, err := json.MarshalIndent(record, "", "  ")
	if err != nil || !bytes.Equal(raw, canonical) {
		return pocketIDOwnerEnrollmentRecord{}, errors.New("localevidence: PocketID owner enrollment is not canonical")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return pocketIDOwnerEnrollmentRecord{}, err
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(record.Signature)
	payload, payloadErr := pocketIDOwnerEnrollmentPayload(record)
	if record.APIVersion != ownerEnrollmentAPIVersion || record.Kind != "PocketIDOwnerEnrollment" ||
		record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID ||
		record.PocketIDSubject == "" || record.SetupURL == "" || record.ExpiresAt.IsZero() ||
		err != nil || payloadErr != nil || len(signature) != ed25519.SignatureSize {
		return pocketIDOwnerEnrollmentRecord{}, errors.New("localevidence: PocketID owner enrollment is not recognised")
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return pocketIDOwnerEnrollmentRecord{}, err
	}
	if !ed25519.Verify(key.Public(), payload, signature) {
		return pocketIDOwnerEnrollmentRecord{}, errors.New("localevidence: PocketID owner enrollment signature does not verify")
	}
	return record, nil
}

func pocketIDOwnerEnrollmentPayload(record pocketIDOwnerEnrollmentRecord) ([]byte, error) {
	record.Signature = ""
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return domainDigest(ownerEnrollmentDomain, raw), nil
}

func domainDigest(domain string, payload []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(domain))
	_, _ = digest.Write(payload)
	return digest.Sum(nil)
}

func certificateDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// OwnerRuntimeBindingDigest identifies the exact signed, secret-free binding
// for inclusion in Apply and Verify evidence.
func OwnerRuntimeBindingDigest(record OwnerRuntimeBinding) string {
	raw, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
