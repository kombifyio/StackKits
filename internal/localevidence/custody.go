package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	OwnerCustodyAPIVersion  = "stackkit.local-owner-custody/v1"
	ownerCustodyRelPath     = ".stackkit/custody/owner.json"
	stepCARootKeyRelPath    = ".stackkit/custody/step-ca/root-key.json"
	stepCARootKeyAPIVersion = "stackkit.local-step-ca-root-key/v1"
)

var ErrOwnerCustodyMissing = errors.New("localevidence: no local owner custody")

type OwnerProjection struct {
	Subject     string `json:"subject"`
	Email       string `json:"email"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
}

type LocalBinding struct {
	SiteRef    string `json:"siteRef"`
	NodeRef    string `json:"nodeRef"`
	ChannelRef string `json:"executionChannelRef"`
}

type TrustProfile struct {
	IdentityProvider     string `json:"identityProvider"`
	CertificateAuthority string `json:"certificateAuthority"`
	HumanAuthorityRef    string `json:"humanAuthorityRef"`
	HumanIssuerRef       string `json:"humanIssuerRef"`
	TrustDomainRef       string `json:"trustDomainRef"`
}

type OwnerCustodyRequest struct {
	Binding     LocalBinding
	Trust       TrustProfile
	Email       string
	Username    string
	DisplayName string
}

type OwnerCustody struct {
	APIVersion               string          `json:"apiVersion"`
	Kind                     string          `json:"kind"`
	OwnerRef                 string          `json:"ownerRef"`
	KeyID                    string          `json:"keyId"`
	Source                   string          `json:"source"`
	PocketID                 OwnerProjection `json:"pocketId"`
	Binding                  LocalBinding    `json:"localBinding"`
	Trust                    TrustProfile    `json:"trust"`
	StepCARootCertificatePEM string          `json:"stepCaRootCertificatePem"`
	OwnerCertificatePEM      string          `json:"ownerCertificatePem"`
	EstablishedAt            time.Time       `json:"establishedAt"`
	Signature                string          `json:"signature"`
}

type stepCARootKeyFile struct {
	APIVersion string `json:"apiVersion"`
	Seed       string `json:"seed"`
}

func EstablishOwnerCustody(workspaceRoot string, request OwnerCustodyRequest) (OwnerCustody, error) {
	if err := validateOwnerCustodyRequest(request); err != nil {
		return OwnerCustody{}, err
	}
	existing, err := LoadOwnerCustody(workspaceRoot)
	if err == nil {
		requestedProjection := normalizeOwnerProjection(request, existing.OwnerRef)
		if existing.Binding != request.Binding || existing.Trust != request.Trust ||
			existing.PocketID != requestedProjection {
			return OwnerCustody{}, errors.New("localevidence: requested local owner projection or binding differs from established custody")
		}
		return existing, nil
	}
	if !errors.Is(err, ErrOwnerCustodyMissing) {
		return OwnerCustody{}, err
	}

	ownerKey, err := LoadOwnerKey(workspaceRoot)
	if errors.Is(err, ErrOwnerKeyMissing) {
		ownerRef, refErr := newOwnerRef()
		if refErr != nil {
			return OwnerCustody{}, refErr
		}
		ownerKey, err = EstablishOwnerKey(workspaceRoot, ownerRef)
	}
	if err != nil {
		return OwnerCustody{}, fmt.Errorf("localevidence: establish owner evidence key: %w", err)
	}
	ownerRef := ownerKey.OwnerRef
	rootPrivate, err := establishStepCARootKey(workspaceRoot)
	if err != nil {
		return OwnerCustody{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	rootDER, ownerDER, err := issueOwnerCertificates(rootPrivate, ownerKey, request.Trust, now)
	if err != nil {
		return OwnerCustody{}, err
	}
	projection := normalizeOwnerProjection(request, ownerRef)
	record := OwnerCustody{
		APIVersion: OwnerCustodyAPIVersion, Kind: "LocalOwnerCustody",
		OwnerRef: ownerRef, KeyID: ownerKey.KeyID, Source: "local",
		PocketID: projection, Binding: request.Binding, Trust: request.Trust,
		StepCARootCertificatePEM: certificatePEM(rootDER),
		OwnerCertificatePEM:      certificatePEM(ownerDER),
		EstablishedAt:            now,
	}
	signingBytes, err := ownerCustodySigningBytes(record)
	if err != nil {
		return OwnerCustody{}, fmt.Errorf("localevidence: encode owner custody for signing: %w", err)
	}
	record.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(ownerKey.private, signingBytes))
	path, err := confinedCustodyPath(workspaceRoot, ownerCustodyRelPath)
	if err != nil {
		return OwnerCustody{}, err
	}
	if err := writePrivateJSON(path, record); err != nil {
		return OwnerCustody{}, fmt.Errorf("localevidence: persist owner custody: %w", err)
	}
	return LoadOwnerCustody(workspaceRoot)
}

func normalizeOwnerProjection(request OwnerCustodyRequest, ownerRef string) OwnerProjection {
	projection := OwnerProjection{
		Subject: ownerRef, Email: strings.TrimSpace(request.Email),
		Username: strings.TrimSpace(request.Username), DisplayName: strings.TrimSpace(request.DisplayName),
		State: "desired",
	}
	if projection.Email == "" {
		projection.Email = "owner@home.test"
	}
	if projection.Username == "" {
		projection.Username = "owner"
	}
	if projection.DisplayName == "" {
		projection.DisplayName = "Home Owner"
	}
	return projection
}

func LoadOwnerCustody(workspaceRoot string) (OwnerCustody, error) {
	path, err := confinedCustodyPath(workspaceRoot, ownerCustodyRelPath)
	if err != nil {
		return OwnerCustody{}, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path below the explicit workspace
	if errors.Is(err, os.ErrNotExist) {
		return OwnerCustody{}, ErrOwnerCustodyMissing
	}
	if err != nil {
		return OwnerCustody{}, fmt.Errorf("localevidence: read owner custody: %w", err)
	}
	var record OwnerCustody
	if err := json.Unmarshal(raw, &record); err != nil {
		return OwnerCustody{}, fmt.Errorf("localevidence: decode owner custody: %w", err)
	}
	if record.APIVersion != OwnerCustodyAPIVersion || record.Kind != "LocalOwnerCustody" ||
		record.Source != "local" || record.OwnerRef == "" || record.KeyID == "" || record.EstablishedAt.IsZero() {
		return OwnerCustody{}, errors.New("localevidence: owner custody is not a recognised record")
	}
	if record.PocketID.Subject != record.OwnerRef || record.PocketID.State != "desired" {
		return OwnerCustody{}, errors.New("localevidence: PocketID owner projection is not bound to ownerRef")
	}
	if err := validateOwnerCustodyRequest(OwnerCustodyRequest{Binding: record.Binding, Trust: record.Trust}); err != nil {
		return OwnerCustody{}, err
	}
	ownerKey, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return OwnerCustody{}, err
	}
	if ownerKey.OwnerRef != record.OwnerRef || ownerKey.KeyID != record.KeyID {
		return OwnerCustody{}, errors.New("localevidence: owner custody does not match its evidence key")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(record.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return OwnerCustody{}, errors.New("localevidence: owner custody signature is malformed")
	}
	signingBytes, err := ownerCustodySigningBytes(record)
	if err != nil || !ed25519.Verify(ownerKey.Public(), signingBytes, signature) {
		return OwnerCustody{}, errors.New("localevidence: owner custody signature does not verify")
	}
	rootPrivate, err := loadStepCARootKey(workspaceRoot)
	if err != nil {
		return OwnerCustody{}, err
	}
	rootCert, err := parseCertificatePEM(record.StepCARootCertificatePEM, "step-ca root")
	if err != nil {
		return OwnerCustody{}, err
	}
	ownerCert, err := parseCertificatePEM(record.OwnerCertificatePEM, "owner")
	if err != nil {
		return OwnerCustody{}, err
	}
	if err := rootCert.CheckSignatureFrom(rootCert); err != nil {
		return OwnerCustody{}, fmt.Errorf("localevidence: verify step-ca root certificate: %w", err)
	}
	if err := ownerCert.CheckSignatureFrom(rootCert); err != nil {
		return OwnerCustody{}, fmt.Errorf("localevidence: verify owner certificate: %w", err)
	}
	rootPublic, ok := rootCert.PublicKey.(ed25519.PublicKey)
	if !ok || !bytes.Equal(rootPublic, rootPrivate.Public().(ed25519.PublicKey)) {
		return OwnerCustody{}, errors.New("localevidence: step-ca root certificate does not match its custody key")
	}
	ownerPublic, ok := ownerCert.PublicKey.(ed25519.PublicKey)
	if !ok || !bytes.Equal(ownerPublic, ownerKey.Public()) || ownerCert.Subject.CommonName != record.OwnerRef {
		return OwnerCustody{}, errors.New("localevidence: owner certificate does not match local owner custody")
	}
	return record, nil
}

func ownerCustodySigningBytes(record OwnerCustody) ([]byte, error) {
	record.Signature = ""
	return json.Marshal(record)
}

func validateOwnerCustodyRequest(request OwnerCustodyRequest) error {
	for label, value := range map[string]string{
		"siteRef": request.Binding.SiteRef, "nodeRef": request.Binding.NodeRef,
		"executionChannelRef": request.Binding.ChannelRef,
		"humanAuthorityRef":   request.Trust.HumanAuthorityRef,
		"humanIssuerRef":      request.Trust.HumanIssuerRef, "trustDomainRef": request.Trust.TrustDomainRef,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("localevidence: %s is required for local owner custody", label)
		}
	}
	if request.Trust.IdentityProvider != "pocketid" || request.Trust.CertificateAuthority != "step-ca" {
		return errors.New("localevidence: local owner custody requires the CUE-owned pocketid/step-ca trust profile")
	}
	return nil
}

func newOwnerRef() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("localevidence: generate owner reference: %w", err)
	}
	return "owner/local/" + hex.EncodeToString(random), nil
}

func establishStepCARootKey(workspaceRoot string) (ed25519.PrivateKey, error) {
	existing, err := loadStepCARootKey(workspaceRoot)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("localevidence: generate step-ca root key: %w", err)
	}
	path, err := confinedCustodyPath(workspaceRoot, stepCARootKeyRelPath)
	if err != nil {
		return nil, err
	}
	if err := writePrivateJSON(path, stepCARootKeyFile{
		APIVersion: stepCARootKeyAPIVersion, Seed: base64.RawStdEncoding.EncodeToString(seed),
	}); err != nil {
		return nil, fmt.Errorf("localevidence: persist step-ca root key: %w", err)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func loadStepCARootKey(workspaceRoot string) (ed25519.PrivateKey, error) {
	path, err := confinedCustodyPath(workspaceRoot, stepCARootKeyRelPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path below the explicit workspace
	if err != nil {
		return nil, err
	}
	var file stepCARootKeyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("localevidence: decode step-ca root key: %w", err)
	}
	seed, err := base64.RawStdEncoding.Strict().DecodeString(file.Seed)
	if file.APIVersion != stepCARootKeyAPIVersion || err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("localevidence: step-ca root key is malformed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func issueOwnerCertificates(rootPrivate ed25519.PrivateKey, ownerKey OwnerKey, trust TrustProfile, now time.Time) ([]byte, []byte, error) {
	rootSerial, err := certificateSerial()
	if err != nil {
		return nil, nil, err
	}
	root := &x509.Certificate{
		SerialNumber: rootSerial, Subject: pkix.Name{CommonName: "StackKits " + trust.TrustDomainRef},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true, IsCA: true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, rootPrivate.Public(), rootPrivate)
	if err != nil {
		return nil, nil, fmt.Errorf("localevidence: issue step-ca root certificate: %w", err)
	}
	ownerSerial, err := certificateSerial()
	if err != nil {
		return nil, nil, err
	}
	owner := &x509.Certificate{
		SerialNumber: ownerSerial, Subject: pkix.Name{CommonName: ownerKey.OwnerRef},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(2, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageCodeSigning},
	}
	ownerDER, err := x509.CreateCertificate(rand.Reader, owner, root, ownerKey.Public(), rootPrivate)
	if err != nil {
		return nil, nil, fmt.Errorf("localevidence: issue owner certificate: %w", err)
	}
	return rootDER, ownerDER, nil
}

func certificateSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("localevidence: generate certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		return big.NewInt(1), nil
	}
	return serial, nil
}

func certificatePEM(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func parseCertificatePEM(value, label string) (*x509.Certificate, error) {
	block, remainder := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(remainder)) != 0 {
		return nil, fmt.Errorf("localevidence: %s certificate PEM is malformed", label)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("localevidence: parse %s certificate: %w", label, err)
	}
	return certificate, nil
}

func confinedCustodyPath(workspaceRoot, relative string) (string, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "", errors.New("localevidence: workspace root is required")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("localevidence: resolve workspace root: %w", err)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	relativePath, err := filepath.Rel(root, path)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", errors.New("localevidence: custody path escapes workspace")
	}
	return path, nil
}

func writePrivateJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".stackkit-custody-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(ownerKeyFileMode); err != nil {
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	if err := os.Chmod(path, ownerKeyFileMode); err != nil {
		return err
	}
	return restrictFileToCurrentUser(path)
}
