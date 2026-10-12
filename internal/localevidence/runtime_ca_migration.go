package localevidence

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"go.step.sm/crypto/pemutil"
)

const browserCAMigrationPath = ".stackkit/custody/basement-browser-ca-migration.json"

type BrowserCAMigrationObservation struct {
	OwnerRef            string `json:"ownerRef"`
	PreviousFingerprint string `json:"previousFingerprint"`
	Fingerprint         string `json:"fingerprint"`
	Status              string `json:"status"`
	RuntimeActivation   string `json:"runtimeActivation"`
	ClientTrust         string `json:"clientTrust"`
	RootKey             string `json:"rootKey,omitempty"`
	IntermediateKey     string `json:"intermediateKey,omitempty"`
}

type browserCAMigration struct {
	Observation BrowserCAMigrationObservation  `json:"observation"`
	Files       map[string]domainMigrationFile `json:"files"`
	Signature   OwnerPolicyStateSignature      `json:"signature"`
}

// MigrateBasementBrowserCA is an explicit, local custody migration. The caller
// holds the lifecycle mutation session and obtains Owner approval. It preserves
// the Owner signing key, PocketID subject/passkeys, runtime secrets, leaf keys,
// addresses and volumes. Only legacy CA signing keys change. The private signed journal retains both exact
// generations until the new CA has been activated by a verified native Apply.
func MigrateBasementBrowserCA(root string) (BrowserCAMigrationObservation, error) {
	var observation BrowserCAMigrationObservation
	_, err := withDomainMigrationLock(root, func(tx *confinedfs.Transaction, view confinedfs.View) (bool, error) {
		journal, err := loadBrowserCAMigration(root, tx)
		if errors.Is(err, os.ErrNotExist) {
			owner, loadErr := LoadOwnerCustody(root)
			if loadErr != nil {
				return false, loadErr
			}
			cert, loadErr := parseCertificatePEM(owner.StepCARootCertificatePEM, "step-ca root")
			if loadErr != nil {
				return false, loadErr
			}
			if _, loadErr := LoadBasementRuntimeCustody(root); loadErr != nil {
				return false, loadErr
			}
			intermediateRaw, _, loadErr := tx.ReadStableBounded(path.Join(basementRuntimeCustodyRelDir, "step-ca/certs/intermediate_ca.crt"), 1<<20)
			if loadErr != nil {
				return false, loadErr
			}
			intermediate, loadErr := parseCertificatePEM(string(intermediateRaw), "runtime step-ca intermediate")
			if loadErr != nil {
				return false, loadErr
			}
			if browserCAP256(cert) && browserCAP256(intermediate) {
				observation = BrowserCAMigrationObservation{OwnerRef: owner.OwnerRef, Fingerprint: certificateDigest(cert.Raw), Status: "current", RuntimeActivation: "not-requested", ClientTrust: "unverified"}
				return false, nil
			}
			if (!browserCAP256(cert) && cert.PublicKeyAlgorithm != x509.Ed25519) || (!browserCAP256(intermediate) && intermediate.PublicKeyAlgorithm != x509.Ed25519) {
				return false, errors.New("browser CA migration admits only authenticated P-256 or legacy Ed25519 CA keys")
			}
			journal, err = prepareBrowserCAMigration(root, tx, owner)
			if err != nil {
				return false, err
			}
			raw, err := json.Marshal(journal)
			if err != nil {
				return false, err
			}
			if _, err := view.WriteAtomic0600(browserCAMigrationPath, raw); err != nil {
				return false, err
			}
			if _, err := tx.SyncDirectory(".stackkit/custody"); err != nil {
				return false, err
			}
		} else if err != nil {
			return false, err
		}
		if err := applyBrowserCAMigration(root, tx, view, journal); err != nil {
			return false, err
		}
		observation = journal.Observation
		return true, nil
	})
	return observation, err
}

func browserCAP256(cert *x509.Certificate) bool {
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	return ok && key.Curve == elliptic.P256()
}

func prepareBrowserCAMigration(root string, tx *confinedfs.Transaction, owner OwnerCustody) (browserCAMigration, error) {
	journal := browserCAMigration{Files: map[string]domainMigrationFile{}}
	runtime, err := LoadBasementRuntimeCustody(root)
	if err != nil {
		return journal, err
	}
	key, err := LoadOwnerKey(root)
	if err != nil {
		return journal, err
	}
	previous, err := parseCertificatePEM(owner.StepCARootCertificatePEM, "step-ca root")
	if err != nil {
		return journal, err
	}
	var private crypto.Signer
	private, err = loadStepCARootKey(root)
	if err != nil {
		return journal, err
	}
	nextRoot := previous
	nextOwner := owner
	replacements := map[string][]byte{}
	encode := func(value any) ([]byte, error) { return json.MarshalIndent(value, "", "  ") }
	rootKey, intermediateKey, clientTrust := "retained", "retained", "existing-root-retained"
	for _, relative := range []string{stepCARootKeyRelPath, ownerCustodyRelPath} {
		replacements[relative], _, err = tx.ReadStableBounded(relative, 1<<20)
		if err != nil {
			return journal, err
		}
	}
	if !browserCAP256(previous) {
		private, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return journal, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return journal, err
		}
		rootDER, ownerDER, err := issueOwnerCertificates(private, key, owner.Trust, time.Now().UTC().Truncate(time.Second))
		if err != nil {
			return journal, err
		}
		nextRoot, err = x509.ParseCertificate(rootDER)
		if err != nil {
			return journal, err
		}
		nextOwner.StepCARootCertificatePEM, nextOwner.OwnerCertificatePEM = certificatePEM(rootDER), certificatePEM(ownerDER)
		signing, err := ownerCustodySigningBytes(nextOwner)
		if err != nil {
			return journal, err
		}
		nextOwner.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signing))
		replacements[stepCARootKeyRelPath], err = encode(stepCARootKeyFile{APIVersion: stepCARootKeyAPIVersion, PKCS8: base64.RawStdEncoding.EncodeToString(der)})
		if err != nil {
			return journal, err
		}
		replacements[ownerCustodyRelPath], err = encode(nextOwner)
		if err != nil {
			return journal, err
		}
		rootKey, clientTrust = "rotated", "approval-pending"
	}
	intermediatePath := path.Join(basementRuntimeCustodyRelDir, "step-ca/certs/intermediate_ca.crt")
	intermediateRaw, _, err := tx.ReadStableBounded(intermediatePath, 1<<20)
	if err != nil {
		return journal, err
	}
	intermediate, err := parseCertificatePEM(string(intermediateRaw), "runtime step-ca intermediate")
	if err != nil {
		return journal, err
	}
	intermediatePublic := intermediate.PublicKey
	if !browserCAP256(intermediate) {
		issuerPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return journal, err
		}
		password, _, err := tx.ReadStableBounded(path.Join(basementRuntimeCustodyRelDir, "step-ca/secrets/password"), 1<<20)
		if err != nil {
			return journal, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(issuerPrivate)
		if err != nil {
			return journal, err
		}
		encrypted, err := pemutil.EncryptPKCS8PrivateKey(rand.Reader, der, password, x509.PEMCipherAES256)
		if err != nil {
			return journal, err
		}
		replacements[path.Join(basementRuntimeCustodyRelDir, "step-ca/secrets/intermediate_ca_key")] = pem.EncodeToMemory(encrypted)
		intermediatePublic, intermediateKey = issuerPrivate.Public(), "rotated"
	}
	template := *intermediate
	template.SerialNumber, err = certificateSerial()
	if err != nil {
		return journal, err
	}
	template.SignatureAlgorithm, template.AuthorityKeyId, template.SubjectKeyId = x509.UnknownSignatureAlgorithm, nil, nil
	if template.NotAfter.After(nextRoot.NotAfter) {
		template.NotAfter = nextRoot.NotAfter
	}
	nextIntermediateDER, err := x509.CreateCertificate(rand.Reader, &template, nextRoot, intermediatePublic, private)
	if err != nil {
		return journal, err
	}
	replacements[path.Join(basementRuntimeCustodyRelDir, "step-ca/certs/root_ca.crt")] = []byte(nextOwner.StepCARootCertificatePEM)
	replacements[intermediatePath] = []byte(certificatePEM(nextIntermediateDER))
	for i, file := range runtime.Files {
		if data, changed := replacements[path.Join(basementRuntimeCustodyRelDir, file.Path)]; changed {
			runtime.Files[i].MAC = basementRuntimeFileMAC(key, file.Path, data)
		}
	}
	runtime.Signature = ""
	signing, err := basementRuntimeSigningBytes(runtime)
	if err != nil {
		return journal, err
	}
	runtime.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signing))
	replacements[path.Join(basementRuntimeCustodyRelDir, basementRuntimeManifestRelPath)], err = encode(runtime)
	if err != nil {
		return journal, err
	}
	binding, err := LoadOwnerRuntimeBinding(root)
	if err == nil && rootKey == "rotated" {
		nextCert, parseErr := parseCertificatePEM(nextOwner.OwnerCertificatePEM, "owner certificate")
		if parseErr != nil {
			return journal, parseErr
		}
		binding.OwnerCertificateSHA256, binding.OwnerCertificateSerial, binding.StepCARootCertificateSHA256 = certificateDigest(nextCert.Raw), strings.ToLower(nextCert.SerialNumber.Text(16)), certificateDigest(nextRoot.Raw)
		if err := signOwnerRuntimeBinding(root, &binding); err != nil {
			return journal, err
		}
		replacements[ownerRuntimeBindingRelPath], err = encode(binding)
		if err != nil {
			return journal, err
		}
	} else if err != nil && !errors.Is(err, ErrOwnerRuntimeBindingMissing) {
		return journal, err
	}
	for relative, after := range replacements {
		before, _, err := tx.ReadStableBounded(relative, 1<<20)
		if err != nil {
			return journal, err
		}
		journal.Files[relative] = domainMigrationFile{Before: before, After: after}
	}
	journal.Observation = BrowserCAMigrationObservation{OwnerRef: owner.OwnerRef, PreviousFingerprint: certificateDigest(previous.Raw), Fingerprint: certificateDigest(nextRoot.Raw), Status: "staged", RuntimeActivation: "pending-native-apply", ClientTrust: clientTrust, RootKey: rootKey, IntermediateKey: intermediateKey}
	signing, err = json.Marshal(journal)
	if err != nil {
		return journal, err
	}
	journal.Signature, err = signBrowserCAMigration(root, signing)
	return journal, err
}

func loadBrowserCAMigration(root string, tx *confinedfs.Transaction) (browserCAMigration, error) {
	var journal browserCAMigration
	raw, _, err := tx.ReadStableBounded(browserCAMigrationPath, 8<<20)
	if err != nil {
		return journal, err
	}
	if err := json.Unmarshal(raw, &journal); err != nil {
		return journal, err
	}
	signature := journal.Signature
	journal.Signature = OwnerPolicyStateSignature{}
	signing, err := json.Marshal(journal)
	journal.Signature = signature
	if err != nil {
		return journal, err
	}
	// Owner custody may be between the two signed generations after a crash.
	// Its unchanged evidence key authenticates the journal before replay.
	key, err := LoadOwnerKey(root)
	if err != nil {
		return journal, err
	}
	signatureBytes, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	digest := browserCAMigrationDigest(signing)
	if err != nil || signature.OwnerRef != key.OwnerRef || signature.KeyID != key.KeyID || !ed25519.Verify(key.Public(), digest, signatureBytes) {
		return journal, errors.New("browser CA migration signature does not verify against the retained Owner key")
	}
	required := []string{stepCARootKeyRelPath, ownerCustodyRelPath, path.Join(basementRuntimeCustodyRelDir, basementRuntimeManifestRelPath), path.Join(basementRuntimeCustodyRelDir, "step-ca/certs/root_ca.crt"), path.Join(basementRuntimeCustodyRelDir, "step-ca/certs/intermediate_ca.crt")}
	issuerKeyPath := path.Join(basementRuntimeCustodyRelDir, "step-ca/secrets/intermediate_ca_key")
	allowed := map[string]bool{ownerRuntimeBindingRelPath: true, issuerKeyPath: true}
	if journal.Observation.IntermediateKey == "rotated" {
		if _, exists := journal.Files[issuerKeyPath]; !exists {
			return journal, errors.New("browser CA issuer migration is incomplete")
		}
	}
	for _, relative := range required {
		allowed[relative] = true
		if _, exists := journal.Files[relative]; !exists {
			return journal, errors.New("browser CA migration is incomplete")
		}
	}
	for relative := range journal.Files {
		if !allowed[relative] {
			return journal, errors.New("browser CA migration contains an undeclared custody path")
		}
	}
	if journal.Observation.Status != "staged" || journal.Observation.OwnerRef != signature.OwnerRef {
		return journal, errors.New("browser CA migration has a different Owner")
	}
	return journal, nil
}

func browserCAMigrationDigest(raw []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte("stackkit.basement-browser-ca-migration/v1\x00"))
	_, _ = digest.Write(raw)
	return digest.Sum(nil)
}

func signBrowserCAMigration(root string, raw []byte) (OwnerPolicyStateSignature, error) {
	key, err := LoadOwnerKey(root)
	if err != nil {
		return OwnerPolicyStateSignature{}, err
	}
	return OwnerPolicyStateSignature{OwnerRef: key.OwnerRef, KeyID: key.KeyID, Value: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, browserCAMigrationDigest(raw)))}, nil
}

func applyBrowserCAMigration(root string, tx *confinedfs.Transaction, view confinedfs.View, journal browserCAMigration) error {
	paths := make([]string, 0, len(journal.Files))
	for relative, file := range journal.Files {
		current, _, err := tx.ReadStableBounded(relative, 1<<20)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, file.Before) && !bytes.Equal(current, file.After) {
			return errors.New("custody changed outside the signed browser CA migration")
		}
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		if err := tx.VerifyPathIdentity(); err != nil {
			return err
		}
		if _, err := view.WriteAtomic0600(relative, journal.Files[relative].After); err != nil {
			return err
		}
	}
	if _, err := LoadOwnerCustody(root); err != nil {
		return err
	}
	if _, err := LoadBasementRuntimeCustody(root); err != nil {
		return err
	}
	if _, exists := journal.Files[ownerRuntimeBindingRelPath]; exists {
		if _, err := LoadOwnerRuntimeBinding(root); err != nil {
			return err
		}
	}
	_, err := tx.SyncDirectory(".stackkit/custody")
	return err
}

// BasementBrowserCAMigrationChain supplies only the authenticated public
// intermediate generations. Runtime consumers can preserve existing leaf keys,
// identities by replacing their old intermediate in a stored chain, or renewing
// a legacy-issuer leaf using its retained private key.
func BasementBrowserCAMigrationChain(root string) (previous, next []byte, pending bool, err error) {
	_, err = withDomainMigrationLock(root, func(tx *confinedfs.Transaction, _ confinedfs.View) (bool, error) {
		journal, loadErr := loadBrowserCAMigration(root, tx)
		if errors.Is(loadErr, os.ErrNotExist) {
			return false, nil
		}
		if loadErr != nil {
			return false, loadErr
		}
		file := journal.Files[path.Join(basementRuntimeCustodyRelDir, "step-ca/certs/intermediate_ca.crt")]
		previous, next, pending = append([]byte(nil), file.Before...), append([]byte(nil), file.After...), true
		return true, nil
	})
	return
}

// CompleteBasementBrowserCAMigration is called only after the current native
// signed Apply and ordinary TLS consumption of the new CA have been verified.
func CompleteBasementBrowserCAMigration(root string) (BrowserCAMigrationObservation, error) {
	var observation BrowserCAMigrationObservation
	_, err := withDomainMigrationLock(root, func(tx *confinedfs.Transaction, view confinedfs.View) (bool, error) {
		journal, err := loadBrowserCAMigration(root, tx)
		if err != nil {
			return false, err
		}
		for relative, file := range journal.Files {
			current, _, err := tx.ReadStableBounded(relative, 1<<20)
			if err != nil {
				return false, err
			}
			if !bytes.Equal(current, file.After) {
				return false, fmt.Errorf("browser CA migration generation changed: %s", relative)
			}
		}
		if _, err := LoadBasementRuntimeCustody(root); err != nil {
			return false, err
		}
		observation = journal.Observation
		observation.Status, observation.RuntimeActivation = "activated", "verified-native-apply"
		raw, err := json.Marshal(observation)
		if err != nil {
			return false, err
		}
		signature, err := SignOwnerPolicyState(root, raw)
		if err != nil {
			return false, err
		}
		receipt, err := json.Marshal(struct {
			Observation BrowserCAMigrationObservation `json:"observation"`
			Signature   OwnerPolicyStateSignature     `json:"signature"`
		}{observation, signature})
		if err != nil {
			return false, err
		}
		if _, err := view.WriteAtomic0600(".stackkit/evidence/basement-browser-ca-migration.json", receipt); err != nil {
			return false, err
		}
		info, err := tx.Lstat(browserCAMigrationPath)
		if err != nil {
			return false, err
		}
		if err := tx.RemoveRegularFile(browserCAMigrationPath, info); err != nil {
			return false, err
		}
		_, err = tx.SyncDirectory(".stackkit/custody")
		return true, err
	})
	return observation, err
}
