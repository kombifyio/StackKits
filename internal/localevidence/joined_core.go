package localevidence

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"go.step.sm/crypto/pemutil"
)

// A joined server's core (homelab identity target §4, P2). A further server
// of a homelab runs no Pocket ID of its own: its TinyAuth signs in against the
// Authority's Pocket ID, its step-ca issues under the intermediate CA the
// Authority's root signed for this server's hostnames only, and its LAN
// resolver serves the homelab zone. The joined-core record is the one
// Owner-signed anchor every consumer of this server's custody reads: the
// Authority's root and Owner subject, the intermediate, and where the
// Authority's identity host answers on the LAN.
const (
	JoinedCoreAPIVersion = "stackkit.joined-core/v1"
	joinedCoreRelPath    = ".stackkit/custody/joined-core.json"
	joinedCoreJournal    = ".stackkit/custody/basement-joined-core.json"
	lanDNSZoneJournal    = ".stackkit/custody/basement-lan-zone.json"

	// ComposeAuthorityProfile is the Compose profile of the core services only
	// an Authority runs (Pocket ID). A joined server's core never starts them.
	ComposeAuthorityProfile = "identity-authority"
	// ComposeAuthorityHostEnvironment and ComposeAuthorityAddressEnvironment
	// let a joined server's TinyAuth reach the Authority's Pocket ID at the
	// Owner-signed LAN address without asking DNS.
	ComposeAuthorityHostEnvironment    = "STACKKIT_IDENTITY_AUTHORITY_HOST"
	ComposeAuthorityAddressEnvironment = "STACKKIT_IDENTITY_AUTHORITY_ADDRESS"
)

var (
	// ErrJoinedCoreMissing reports a server that runs its own identity.
	ErrJoinedCoreMissing = errors.New("localevidence: this server's core is not joined to an Authority")
	joinedCoreHostname   = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

// JoinedCore is the Owner-signed anchor of a joined server's core.
type JoinedCore struct {
	APIVersion            string                    `json:"apiVersion"`
	OwnerRef              string                    `json:"ownerRef"`
	KeyID                 string                    `json:"keyId"`
	AuthoritySiteRef      string                    `json:"authoritySiteRef"`
	AuthorityOwnerSubject string                    `json:"authorityOwnerSubject"`
	AuthorityRootPEM      string                    `json:"authorityRootPem"`
	IntermediatePEM       string                    `json:"intermediatePem"`
	AuthorityIdentityHost string                    `json:"authorityIdentityHost"`
	AuthorityLANAddress   string                    `json:"authorityLanAddress,omitempty"`
	BoundAt               time.Time                 `json:"boundAt"`
	Signature             OwnerPolicyStateSignature `json:"signature"`
}

// JoinedCoreInput is what a joined server's identity installs: the bundle's
// public trust and the intermediate key that stayed in host custody.
type JoinedCoreInput struct {
	AuthoritySiteRef      string
	AuthorityOwnerSubject string
	AuthorityRootPEM      string
	IntermediatePEM       string
	IntermediateKey       *ecdsa.PrivateKey
	AuthorityIdentityHost string
	AuthorityLANAddress   string
}

func joinedCoreSigningBytes(record JoinedCore) ([]byte, error) {
	record.Signature = OwnerPolicyStateSignature{}
	return json.Marshal(record)
}

// LoadJoinedCore reads and verifies the joined-core anchor. A server without
// one returns ErrJoinedCoreMissing and runs its own identity.
func LoadJoinedCore(workspaceRoot string) (JoinedCore, error) {
	filePath, err := confinedCustodyPath(workspaceRoot, joinedCoreRelPath)
	if err != nil {
		return JoinedCore{}, err
	}
	raw, err := os.ReadFile(filePath) //nolint:gosec // fixed custody path
	if errors.Is(err, os.ErrNotExist) {
		return JoinedCore{}, ErrJoinedCoreMissing
	}
	if err != nil {
		return JoinedCore{}, err
	}
	if err := requirePrivateRuntimeFile(filePath); err != nil {
		return JoinedCore{}, err
	}
	var record JoinedCore
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil || record.APIVersion != JoinedCoreAPIVersion {
		return JoinedCore{}, errors.New("localevidence: the joined-core record is unreadable")
	}
	signing, err := joinedCoreSigningBytes(record)
	if err != nil || VerifyOwnerPolicyState(workspaceRoot, signing, record.Signature) != nil {
		return JoinedCore{}, errors.New("localevidence: the joined-core record is not signed by this server's Owner")
	}
	root, intermediate, err := parseJoinedCoreChain(record.AuthorityRootPEM, record.IntermediatePEM)
	if err != nil || root == nil || intermediate == nil || !joinedCoreHostname.MatchString(record.AuthorityIdentityHost) {
		return JoinedCore{}, errors.New("localevidence: the joined-core record does not verify")
	}
	if !validJoinedCoreLANAddress(record.AuthorityLANAddress) {
		return JoinedCore{}, errors.New("localevidence: the joined-core record names an invalid Authority LAN address")
	}
	return record, nil
}

// validJoinedCoreLANAddress admits no address (the homelab zone then
// answers the Authority's names) or one canonical private address.
func validJoinedCoreLANAddress(value string) bool {
	if value == "" {
		return true
	}
	address, err := netip.ParseAddr(value)
	return err == nil && address.String() == value && (address.IsPrivate() || address.IsLinkLocalUnicast())
}

// JoinedCoreActive reports a server whose core is joined to an Authority. A
// present but damaged anchor counts as joined: such a server never starts a
// Pocket ID of its own and fails its custody checks instead.
func JoinedCoreActive(workspaceRoot string) bool {
	_, err := LoadJoinedCore(workspaceRoot)
	return !errors.Is(err, ErrJoinedCoreMissing)
}

// ReplayBasementRuntimeRewrites finishes a joined-core or homelab-zone
// custody rewrite a crash interrupted, before anything loads runtime custody
// again (its MACs verify only after the replay).
func ReplayBasementRuntimeRewrites(workspaceRoot string) error {
	_, err := withDomainMigrationLock(workspaceRoot, func(tx *confinedfs.Transaction, view confinedfs.View) (bool, error) {
		for _, journalPath := range []string{joinedCoreJournal, lanDNSZoneJournal} {
			journal, err := loadRuntimeRewriteJournal(workspaceRoot, tx, journalPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return false, err
			}
			if err := applyRuntimeRewrite(workspaceRoot, tx, view, journalPath, journal); err != nil {
				return false, err
			}
		}
		return false, nil
	})
	return err
}

func parseJoinedCoreChain(rootPEM, intermediatePEM string) (*x509.Certificate, *x509.Certificate, error) {
	root, err := parseCertificatePEM(rootPEM, "Authority root")
	if err != nil || !root.IsCA || root.CheckSignatureFrom(root) != nil {
		return nil, nil, errors.New("localevidence: the Authority root is not a self-signed CA")
	}
	intermediate, err := parseCertificatePEM(intermediatePEM, "joined intermediate")
	if err != nil || !intermediate.IsCA || intermediate.CheckSignatureFrom(root) != nil || !intermediate.MaxPathLenZero {
		return nil, nil, errors.New("localevidence: the joined intermediate is not issued by the Authority root")
	}
	return root, intermediate, nil
}

// joinedIntermediateCovers reports a name inside one of the intermediate's
// permitted DNS subtrees and outside every excluded one.
func joinedIntermediateCovers(intermediate *x509.Certificate, name string) bool {
	inside := func(name, tree string) bool { return name == tree || strings.HasSuffix(name, "."+tree) }
	for _, excluded := range intermediate.ExcludedDNSDomains {
		if inside(name, excluded) {
			return false
		}
	}
	return slices.ContainsFunc(intermediate.PermittedDNSDomains, func(tree string) bool { return inside(name, tree) })
}

// JoinedCoreNames are the core names a joined server's intermediate must
// cover: step-ca's own TLS name and TinyAuth.
func JoinedCoreNames(domain string) []string {
	address := IdentityRuntimeAddress{Domain: domain}
	return []string{address.ServiceHost("ca"), address.ServiceHost("auth")}
}

// BindJoinedCore anchors this server's core at the Authority (host only,
// after the trust bundle verified): it records the Owner-signed anchor and
// moves the runtime step-ca onto the joined intermediate, keeping its
// password, provisioners and database. The step-ca TLS name becomes
// ca.<domain> only, because the intermediate names nothing else.
func BindJoinedCore(workspaceRoot string, input JoinedCoreInput) (JoinedCore, error) {
	if err := ReplayBasementRuntimeRewrites(workspaceRoot); err != nil {
		return JoinedCore{}, err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return JoinedCore{}, err
	}
	runtime, err := LoadBasementRuntimeCustody(workspaceRoot)
	if err != nil {
		return JoinedCore{}, fmt.Errorf("localevidence: a joined core needs this server's Basement runtime custody (stackkit init) first: %w", err)
	}
	root, intermediate, err := parseJoinedCoreChain(input.AuthorityRootPEM, input.IntermediatePEM)
	if err != nil {
		return JoinedCore{}, err
	}
	if input.IntermediateKey == nil || !input.IntermediateKey.PublicKey.Equal(intermediate.PublicKey) || input.IntermediateKey.Curve != elliptic.P256() {
		return JoinedCore{}, errors.New("localevidence: the joined intermediate does not match the key kept in host custody")
	}
	for _, name := range JoinedCoreNames(runtime.Domain) {
		if !joinedIntermediateCovers(intermediate, name) {
			return JoinedCore{}, fmt.Errorf("localevidence: the joined intermediate does not cover this server's core name %s; request the server's own domain %s as a hostname", name, runtime.Domain)
		}
	}
	if !joinedCoreHostname.MatchString(input.AuthorityIdentityHost) || strings.TrimSpace(input.AuthorityOwnerSubject) == "" || strings.TrimSpace(input.AuthoritySiteRef) == "" {
		return JoinedCore{}, errors.New("localevidence: a joined core needs the Authority's site, Owner subject and identity host")
	}
	if !validJoinedCoreLANAddress(input.AuthorityLANAddress) {
		return JoinedCore{}, errors.New("localevidence: the trust bundle's Authority LAN address is invalid")
	}
	record := JoinedCore{
		APIVersion: JoinedCoreAPIVersion, OwnerRef: owner.OwnerRef, KeyID: owner.KeyID,
		AuthoritySiteRef: input.AuthoritySiteRef, AuthorityOwnerSubject: input.AuthorityOwnerSubject,
		AuthorityRootPEM: certificatePEM(root.Raw), IntermediatePEM: certificatePEM(intermediate.Raw),
		AuthorityIdentityHost: input.AuthorityIdentityHost, AuthorityLANAddress: input.AuthorityLANAddress,
		BoundAt: time.Now().UTC().Truncate(time.Second),
	}
	signing, err := joinedCoreSigningBytes(record)
	if err != nil {
		return JoinedCore{}, err
	}
	if record.Signature, err = SignOwnerPolicyState(workspaceRoot, signing); err != nil {
		return JoinedCore{}, err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return JoinedCore{}, err
	}
	filePath, err := confinedCustodyPath(workspaceRoot, joinedCoreRelPath)
	if err != nil {
		return JoinedCore{}, err
	}
	// The anchor first: runtime custody under the Authority root verifies only
	// against it, and a crash after it replays the step-ca move below.
	if err := writePrivateRuntimeFile(filePath, raw); err != nil {
		return JoinedCore{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(input.IntermediateKey)
	if err != nil {
		return JoinedCore{}, err
	}
	defer clear(der)
	if _, err := rewriteBasementStepCA(workspaceRoot, record.AuthorityRootPEM, record.IntermediatePEM, der, []string{"ca." + runtime.Domain}); err != nil {
		return JoinedCore{}, err
	}
	return record, nil
}

// UnbindJoinedCore returns a server that left its Authority to its own
// identity (host only): step-ca issues again under a fresh intermediate of
// this server's own root with its own names, and the anchor is removed.
func UnbindJoinedCore(workspaceRoot string) error {
	if _, err := LoadJoinedCore(workspaceRoot); errors.Is(err, ErrJoinedCoreMissing) {
		return nil
	}
	if err := ReplayBasementRuntimeRewrites(workspaceRoot); err != nil {
		return err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return err
	}
	runtime, err := LoadBasementRuntimeCustody(workspaceRoot)
	if err != nil {
		return err
	}
	rootCertificate, err := parseCertificatePEM(owner.StepCARootCertificatePEM, "step-ca root")
	if err != nil {
		return err
	}
	rootKey, err := loadStepCARootKey(workspaceRoot)
	if err != nil {
		return err
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := certificateSerial()
	if err != nil {
		return err
	}
	notAfter := time.Now().UTC().AddDate(5, 0, 0)
	if !rootCertificate.NotAfter.After(notAfter) {
		notAfter = rootCertificate.NotAfter
	}
	intermediateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "StackKits " + owner.Trust.TrustDomainRef + " Online Intermediate"},
		NotBefore: time.Now().UTC().Add(-5 * time.Minute), NotAfter: notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true, IsCA: true, MaxPathLen: 0, MaxPathLenZero: true,
	}, rootCertificate, private.Public(), rootKey)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return err
	}
	defer clear(der)
	if _, err := rewriteBasementStepCA(workspaceRoot, owner.StepCARootCertificatePEM, certificatePEM(intermediateDER),
		der, []string{"step-ca", "localhost", "ca." + runtime.Domain}); err != nil {
		return err
	}
	filePath, err := confinedCustodyPath(workspaceRoot, joinedCoreRelPath)
	if err != nil {
		return err
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// rewriteBasementStepCA replaces the runtime step-ca root, intermediate, key
// (encrypted under the existing password) and TLS names in one journaled
// custody rewrite.
func rewriteBasementStepCA(workspaceRoot, rootPEM, intermediatePEM string, keyDER []byte, dnsNames []string) (bool, error) {
	password, _, err := readBasementRuntimeFile(workspaceRoot, "step-ca/secrets/password")
	if err != nil {
		return false, err
	}
	defer clear(password)
	encrypted, err := pemutil.EncryptPKCS8PrivateKey(rand.Reader, keyDER, password, x509.PEMCipherAES256)
	if err != nil {
		return false, fmt.Errorf("localevidence: encrypt the step-ca intermediate key: %w", err)
	}
	configRaw, _, err := readBasementRuntimeFile(workspaceRoot, "step-ca/config/ca.json")
	if err != nil {
		return false, err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return false, errors.New("localevidence: the runtime step-ca configuration is unreadable")
	}
	if config["dnsNames"], err = json.Marshal(dnsNames); err != nil {
		return false, err
	}
	configAfter, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return false, err
	}
	return rewriteBasementRuntimeFiles(workspaceRoot, joinedCoreJournal, map[string][]byte{
		"step-ca/certs/root_ca.crt":           []byte(rootPEM),
		"step-ca/certs/intermediate_ca.crt":   []byte(intermediatePEM),
		"step-ca/secrets/intermediate_ca_key": pem.EncodeToMemory(encrypted),
		"step-ca/config/ca.json":              append(configAfter, '\n'),
	})
}

type runtimeRewriteJournal struct {
	Files     map[string]domainMigrationFile `json:"files"`
	Signature OwnerPolicyStateSignature      `json:"signature"`
}

// rewriteBasementRuntimeFiles replaces runtime custody files (paths inside
// basement-runtime), their MACs and the signed manifest through a signed
// private journal, exactly as the LAN address rebind does: a partial write
// replays from the journal, and custody must verify afterwards.
func rewriteBasementRuntimeFiles(workspaceRoot, journalPath string, replacements map[string][]byte) (bool, error) {
	changed := false
	_, err := withDomainMigrationLock(workspaceRoot, func(tx *confinedfs.Transaction, view confinedfs.View) (bool, error) {
		if journal, err := loadRuntimeRewriteJournal(workspaceRoot, tx, journalPath); err == nil {
			if err := applyRuntimeRewrite(workspaceRoot, tx, view, journalPath, journal); err != nil {
				return false, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		journal, err := prepareRuntimeRewrite(workspaceRoot, tx, replacements)
		if err != nil || len(journal.Files) == 0 {
			return false, err
		}
		raw, err := json.Marshal(journal)
		if err != nil {
			return false, err
		}
		if _, err := view.WriteAtomic0600(journalPath, raw); err != nil {
			return false, err
		}
		if _, err := tx.SyncDirectory(".stackkit/custody"); err != nil {
			return false, err
		}
		changed = true
		return false, applyRuntimeRewrite(workspaceRoot, tx, view, journalPath, journal)
	})
	return changed, err
}

func prepareRuntimeRewrite(workspaceRoot string, tx *confinedfs.Transaction, replacements map[string][]byte) (runtimeRewriteJournal, error) {
	journal := runtimeRewriteJournal{Files: map[string]domainMigrationFile{}}
	custody, err := LoadBasementRuntimeCustody(workspaceRoot)
	if err != nil {
		return journal, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return journal, err
	}
	for index, file := range custody.Files {
		after, replaced := replacements[file.Path]
		if !replaced {
			continue
		}
		relative := basementRuntimeCustodyRelDir + "/" + file.Path
		before, _, err := tx.ReadStableBounded(relative, 1<<20)
		if err != nil {
			return journal, err
		}
		if basementRuntimeFileMAC(key, file.Path, before) != file.MAC {
			return journal, fmt.Errorf("localevidence: runtime input %s changed while preparing its rewrite", file.Path)
		}
		if bytes.Equal(before, after) {
			continue
		}
		custody.Files[index].MAC = basementRuntimeFileMAC(key, file.Path, after)
		journal.Files[relative] = domainMigrationFile{Before: before, After: append([]byte(nil), after...)}
	}
	for relative := range replacements {
		if !slices.Contains(basementRuntimeFilePaths, relative) {
			return journal, fmt.Errorf("localevidence: %s is not a runtime custody input", relative)
		}
	}
	if len(journal.Files) == 0 {
		return journal, nil
	}
	manifestPath := basementRuntimeCustodyRelDir + "/" + basementRuntimeManifestRelPath
	manifestBefore, _, err := tx.ReadStableBounded(manifestPath, 1<<20)
	if err != nil {
		return journal, err
	}
	custody.Signature = ""
	signing, err := basementRuntimeSigningBytes(custody)
	if err != nil {
		return journal, err
	}
	custody.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signing))
	manifestAfter, err := json.MarshalIndent(custody, "", "  ")
	if err != nil {
		return journal, err
	}
	journal.Files[manifestPath] = domainMigrationFile{Before: manifestBefore, After: manifestAfter}
	signingJournal, err := json.Marshal(journal)
	if err != nil {
		return journal, err
	}
	journal.Signature, err = SignOwnerPolicyState(workspaceRoot, signingJournal)
	return journal, err
}

func loadRuntimeRewriteJournal(workspaceRoot string, tx *confinedfs.Transaction, journalPath string) (runtimeRewriteJournal, error) {
	var journal runtimeRewriteJournal
	raw, _, err := tx.ReadStableBounded(journalPath, 8<<20)
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
	return journal, VerifyOwnerPolicyState(workspaceRoot, signing, signature)
}

func applyRuntimeRewrite(workspaceRoot string, tx *confinedfs.Transaction, view confinedfs.View, journalPath string, journal runtimeRewriteJournal) error {
	paths := make([]string, 0, len(journal.Files))
	manifestPath := path.Join(basementRuntimeCustodyRelDir, basementRuntimeManifestRelPath)
	for relative, file := range journal.Files {
		current, _, err := tx.ReadStableBounded(relative, 1<<20)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, file.Before) && !bytes.Equal(current, file.After) {
			return errors.New("localevidence: custody changed outside the journaled runtime rewrite")
		}
		if relative != manifestPath {
			paths = append(paths, relative)
		}
	}
	// Inputs first, manifest last: a crash between them replays from the journal.
	sort.Strings(paths)
	if _, ok := journal.Files[manifestPath]; ok {
		paths = append(paths, manifestPath)
	}
	for _, relative := range paths {
		if err := tx.VerifyPathIdentity(); err != nil {
			return err
		}
		if _, err := view.WriteAtomic0600(relative, journal.Files[relative].After); err != nil {
			return err
		}
	}
	if _, err := LoadBasementRuntimeCustody(workspaceRoot); err != nil {
		return fmt.Errorf("localevidence: runtime custody does not verify after its rewrite: %w", err)
	}
	info, err := tx.Lstat(journalPath)
	if err != nil {
		return err
	}
	if err := tx.RemoveRegularFile(journalPath, info); err != nil {
		return err
	}
	_, err = tx.SyncDirectory(".stackkit/custody")
	return err
}

// StepCALocalTrust is how a process on this server verifies its own step-ca:
// the root step-ca issues under and the TLS name it serves. A joined server's
// step-ca chains to the Authority's root and names only ca.<domain>.
func StepCALocalTrust(workspaceRoot string) (*x509.CertPool, string, error) {
	roots := x509.NewCertPool()
	joined, err := LoadJoinedCore(workspaceRoot)
	switch {
	case err == nil:
		runtime, runtimeErr := LoadBasementRuntimeCustody(workspaceRoot)
		if runtimeErr != nil {
			return nil, "", runtimeErr
		}
		if !roots.AppendCertsFromPEM([]byte(joined.AuthorityRootPEM)) {
			return nil, "", errors.New("localevidence: the joined Authority root is unreadable")
		}
		return roots, "ca." + runtime.Domain, nil
	case !errors.Is(err, ErrJoinedCoreMissing):
		return nil, "", err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return nil, "", err
	}
	if !roots.AppendCertsFromPEM([]byte(owner.StepCARootCertificatePEM)) {
		return nil, "", errors.New("localevidence: missing step-ca trust root")
	}
	return roots, "localhost", nil
}

// JoinedOwnerRuntimeBinding is a joined server's owner identity for its
// core: this server's Owner custody and the Authority's Owner subject. It is
// never written; the Authority's Pocket ID holds the account.
func JoinedOwnerRuntimeBinding(workspaceRoot string) (OwnerRuntimeBinding, error) {
	joined, err := LoadJoinedCore(workspaceRoot)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	root, err := parseCertificatePEM(joined.AuthorityRootPEM, "Authority root")
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	return OwnerRuntimeBinding{
		APIVersion: OwnerRuntimeBindingAPIVersion, Kind: "JoinedOwnerRuntimeBinding",
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID, PocketIDSubject: joined.AuthorityOwnerSubject,
		PocketIDEmail: owner.PocketID.Email, StepCARootCertificateSHA256: certificateDigest(root.Raw), BoundAt: joined.BoundAt,
	}, nil
}

// JoinedCoreComposeEnvironment is the Compose environment of the core's
// identity placement: the Authority profile on a server that runs its own
// identity, the Authority's identity host and LAN address on a joined
// server. A damaged joined-core anchor keeps the server joined: it starts no
// Pocket ID and maps no Authority host.
func JoinedCoreComposeEnvironment(workspaceRoot string) []string {
	joined, err := LoadJoinedCore(workspaceRoot)
	switch {
	case err == nil && joined.AuthorityLANAddress != "":
		return []string{
			ComposeAuthorityHostEnvironment + "=" + joined.AuthorityIdentityHost,
			ComposeAuthorityAddressEnvironment + "=" + joined.AuthorityLANAddress,
		}
	case err == nil:
		return nil
	case errors.Is(err, ErrJoinedCoreMissing):
		return []string{"COMPOSE_PROFILES=" + ComposeAuthorityProfile}
	default:
		return nil
	}
}
