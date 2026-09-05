package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.step.sm/crypto/pemutil"
)

const (
	BasementRuntimeCustodyAPIVersion = "stackkit.basement-runtime-custody/v3"
	defaultBasementSessionTTLSeconds = 900
	basementRuntimeCustodyRelDir     = ".stackkit/custody/basement-runtime"
	basementRuntimeManifestRelPath   = "manifest.json"
)

var (
	ErrBasementRuntimeCustodyMissing = errors.New("localevidence: no Basement runtime custody")
	basementRuntimeDomainPattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
	basementRuntimeFilePaths         = []string{
		"coolify.env",
		"pocketid.env",
		"step-ca/certs/intermediate_ca.crt",
		"step-ca/certs/root_ca.crt",
		"step-ca/config/ca.json",
		"step-ca/secrets/intermediate_ca_key",
		"step-ca/secrets/password",
		"tinyauth.env",
	}
)

func validBasementRuntimeDomain(domain string) bool {
	return len(domain) <= 253 && basementRuntimeDomainPattern.MatchString(domain)
}

type BasementRuntimeCustodyFile struct {
	Path string `json:"path"`
	MAC  string `json:"mac"`
}

// BasementRuntimeCustody is a signed, secret-free index of the local service
// inputs derived from one established owner. The referenced files remain
// private below .stackkit/custody and are never returned through this API.
type BasementRuntimeCustody struct {
	APIVersion    string                       `json:"apiVersion"`
	Kind          string                       `json:"kind"`
	OwnerRef      string                       `json:"ownerRef"`
	KeyID         string                       `json:"keyId"`
	Domain        string                       `json:"domain"`
	EstablishedAt time.Time                    `json:"establishedAt"`
	Files         []BasementRuntimeCustodyFile `json:"files"`
	Signature     string                       `json:"signature"`
}

// EstablishBasementRuntimeCustody creates the service runtime bundle exactly
// once. A complete bundle is installed by one directory rename; a preexisting
// incomplete or modified bundle is rejected instead of repaired or rotated.
// sessionTTLSeconds selects TinyAuth session expiry; non-positive values use
// the Basement kit human-issuer default (900 in
// basement-kit/stackfile.cue home-human-credential-issuer sessionTTLSeconds).
// The 60..86400 range is owned by foundation/architecture_v2.cue
// sessionTTLSeconds; this default must stay in sync with the CUE authority.
func EstablishBasementRuntimeCustody(workspaceRoot, domain string, sessionTTLSeconds int) (BasementRuntimeCustody, error) {
	if sessionTTLSeconds <= 0 {
		sessionTTLSeconds = defaultBasementSessionTTLSeconds
	}
	if sessionTTLSeconds < 60 || sessionTTLSeconds > 86400 {
		return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody requires a session TTL between 60 and 86400 seconds")
	}
	domain = strings.TrimSpace(strings.ToLower(domain))
	if !validBasementRuntimeDomain(domain) {
		return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody requires a canonical domain")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return BasementRuntimeCustody{}, err
	}
	existing, err := LoadBasementRuntimeCustody(workspaceRoot)
	if err == nil {
		if existing.Domain != domain {
			return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody domain differs from established custody")
		}
		return existing, nil
	}
	if !errors.Is(err, ErrBasementRuntimeCustodyMissing) {
		return BasementRuntimeCustody{}, err
	}

	finalDirectory, err := confinedCustodyPath(workspaceRoot, basementRuntimeCustodyRelDir)
	if err != nil {
		return BasementRuntimeCustody{}, err
	}
	if _, statErr := os.Lstat(finalDirectory); statErr == nil {
		return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody exists without a valid signed manifest")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: inspect Basement runtime custody: %w", statErr)
	}
	parent := filepath.Dir(finalDirectory)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: create Basement runtime custody parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".basement-runtime-*")
	if err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: create Basement runtime custody transaction: %w", err)
	}
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := os.Chmod(stage, 0o700); err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: restrict Basement runtime custody transaction: %w", err)
	}

	files, err := buildBasementRuntimeFiles(workspaceRoot, owner, domain, sessionTTLSeconds)
	if err != nil {
		return BasementRuntimeCustody{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return BasementRuntimeCustody{}, err
	}
	manifestFiles := make([]BasementRuntimeCustodyFile, 0, len(basementRuntimeFilePaths))
	for _, relative := range basementRuntimeFilePaths {
		content, ok := files[relative]
		if !ok {
			return BasementRuntimeCustody{}, fmt.Errorf("localevidence: internal Basement runtime file %q is missing", relative)
		}
		target := filepath.Join(stage, filepath.FromSlash(relative))
		if err := writePrivateRuntimeFile(target, content); err != nil {
			return BasementRuntimeCustody{}, fmt.Errorf("localevidence: persist Basement runtime file %q: %w", relative, err)
		}
		manifestFiles = append(manifestFiles, BasementRuntimeCustodyFile{
			Path: relative, MAC: basementRuntimeFileMAC(key, relative, content),
		})
	}
	if err := os.MkdirAll(filepath.Join(stage, "step-ca", "db"), 0o700); err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: create Basement runtime step-ca database directory: %w", err)
	}

	record := BasementRuntimeCustody{
		APIVersion:    BasementRuntimeCustodyAPIVersion,
		Kind:          "BasementRuntimeCustody",
		OwnerRef:      owner.OwnerRef,
		KeyID:         owner.KeyID,
		Domain:        domain,
		EstablishedAt: time.Now().UTC().Truncate(time.Second),
		Files:         manifestFiles,
	}
	signingBytes, err := basementRuntimeSigningBytes(record)
	if err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: encode Basement runtime custody for signing: %w", err)
	}
	record.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signingBytes))
	manifest, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: encode Basement runtime custody manifest: %w", err)
	}
	if err := writePrivateRuntimeFile(filepath.Join(stage, basementRuntimeManifestRelPath), manifest); err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: persist Basement runtime custody manifest: %w", err)
	}
	if err := os.Rename(stage, finalDirectory); err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: atomically install Basement runtime custody: %w", err)
	}
	removeStage = false
	return LoadBasementRuntimeCustody(workspaceRoot)
}

// LoadBasementRuntimeCustody verifies the owner signature, the closed file
// inventory, every digest, and the step-ca chain before returning metadata.
func LoadBasementRuntimeCustody(workspaceRoot string) (BasementRuntimeCustody, error) {
	directory, err := confinedCustodyPath(workspaceRoot, basementRuntimeCustodyRelDir)
	if err != nil {
		return BasementRuntimeCustody{}, err
	}
	manifestPath := filepath.Join(directory, basementRuntimeManifestRelPath)
	raw, err := os.ReadFile(manifestPath) //nolint:gosec // fixed path below explicit workspace
	if errors.Is(err, os.ErrNotExist) {
		return BasementRuntimeCustody{}, ErrBasementRuntimeCustodyMissing
	}
	if err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: read Basement runtime custody manifest: %w", err)
	}
	if err := requirePrivateRuntimeFile(manifestPath); err != nil {
		return BasementRuntimeCustody{}, err
	}
	var record BasementRuntimeCustody
	if err := json.Unmarshal(raw, &record); err != nil {
		return BasementRuntimeCustody{}, fmt.Errorf("localevidence: decode Basement runtime custody manifest: %w", err)
	}
	if record.APIVersion != BasementRuntimeCustodyAPIVersion ||
		record.Kind != "BasementRuntimeCustody" ||
		record.OwnerRef == "" || record.KeyID == "" || !validBasementRuntimeDomain(record.Domain) || record.EstablishedAt.IsZero() {
		return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody is not a recognised record")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return BasementRuntimeCustody{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return BasementRuntimeCustody{}, err
	}
	if record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID ||
		key.OwnerRef != record.OwnerRef || key.KeyID != record.KeyID {
		return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody is not bound to the established owner")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(record.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody signature is malformed")
	}
	signingBytes, err := basementRuntimeSigningBytes(record)
	if err != nil || !ed25519.Verify(key.Public(), signingBytes, signature) {
		return BasementRuntimeCustody{}, errors.New("localevidence: Basement runtime custody signature does not verify")
	}
	if err := validateBasementRuntimeInventory(directory, record.Files, key); err != nil {
		return BasementRuntimeCustody{}, err
	}
	if err := validateBasementStepCA(directory, owner); err != nil {
		return BasementRuntimeCustody{}, err
	}
	return record, nil
}

// BasementStepCARootCAPEM loads the established step-ca root certificate PEM
// from Basement runtime custody for use as a trust anchor only: TLS client
// verification and enrollment fingerprint display. The private root key never
// leaves custody. It also returns the workspace-relative certificate path for
// user-facing client enrollment guidance.
func BasementStepCARootCAPEM(workspaceRoot string) (certificate []byte, relPath string, err error) {
	path, err := confinedCustodyPath(workspaceRoot, basementRuntimeCustodyRelDir+"/step-ca/certs/root_ca.crt")
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path below explicit workspace
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", ErrBasementRuntimeCustodyMissing
	}
	if err != nil {
		return nil, "", fmt.Errorf("localevidence: read step-ca root certificate: %w", err)
	}
	if _, err := parseCertificatePEM(string(raw), "step-ca root"); err != nil {
		return nil, "", err
	}
	return raw, filepath.ToSlash(filepath.Join(basementRuntimeCustodyRelDir, "step-ca", "certs", "root_ca.crt")), nil
}

func buildBasementRuntimeFiles(workspaceRoot string, owner OwnerCustody, domain string, sessionTTLSeconds int) (map[string][]byte, error) {
	rootPrivate, err := loadStepCARootKey(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("localevidence: load established step-ca root key for runtime intermediate: %w", err)
	}
	rootCertificate, err := parseCertificatePEM(owner.StepCARootCertificatePEM, "step-ca root")
	if err != nil {
		return nil, err
	}
	_, intermediatePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("localevidence: generate step-ca runtime intermediate key: %w", err)
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, err
	}
	notAfter := time.Now().UTC().AddDate(5, 0, 0)
	if !rootCertificate.NotAfter.After(notAfter) {
		notAfter = rootCertificate.NotAfter
	}
	intermediateTemplate := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "StackKits " + owner.Trust.TrustDomainRef + " Online Intermediate"},
		NotBefore:             time.Now().UTC().Add(-5 * time.Minute),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	intermediateDER, err := x509.CreateCertificate(
		rand.Reader, intermediateTemplate, rootCertificate, intermediatePrivate.Public(), rootPrivate,
	)
	if err != nil {
		return nil, fmt.Errorf("localevidence: issue step-ca runtime intermediate certificate: %w", err)
	}
	password, err := randomRuntimeSecret(32, base64.RawStdEncoding)
	if err != nil {
		return nil, err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(intermediatePrivate)
	if err != nil {
		return nil, fmt.Errorf("localevidence: encode step-ca runtime intermediate key: %w", err)
	}
	encryptedBlock, err := pemutil.EncryptPKCS8PrivateKey(
		rand.Reader, privateDER, []byte(password), x509.PEMCipherAES256,
	)
	if err != nil {
		return nil, fmt.Errorf("localevidence: encrypt step-ca runtime intermediate key: %w", err)
	}
	environments, err := basementRuntimeEnvironments(owner, domain, sessionTTLSeconds)
	if err != nil {
		return nil, err
	}
	config, err := json.MarshalIndent(basementStepCAConfig{
		Root:     "/home/step/certs/root_ca.crt",
		Crt:      "/home/step/certs/intermediate_ca.crt",
		Key:      "/home/step/secrets/intermediate_ca_key",
		Address:  ":9000",
		DNSNames: []string{"step-ca", "localhost", "ca." + domain},
		Logger:   basementStepCALogger{Format: "text"},
		DB:       basementStepCADatabase{Type: "badgerV2", DataSource: "/home/step/db"},
		Authority: basementStepCAAuthority{
			Provisioners: []basementStepCAProvisioner{{Type: "ACME", Name: "acme"}},
		},
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("localevidence: encode step-ca runtime config: %w", err)
	}
	files := map[string][]byte{
		"step-ca/certs/root_ca.crt":           []byte(owner.StepCARootCertificatePEM),
		"step-ca/certs/intermediate_ca.crt":   []byte(certificatePEM(intermediateDER)),
		"step-ca/config/ca.json":              append(config, '\n'),
		"step-ca/secrets/intermediate_ca_key": pem.EncodeToMemory(encryptedBlock),
		"step-ca/secrets/password":            []byte(password),
	}
	for path, content := range environments {
		files[path] = content
	}
	return files, nil
}

func basementRuntimeEnvironments(owner OwnerCustody, domain string, sessionTTLSeconds int) (map[string][]byte, error) {
	encryptionKey, err := randomRuntimeSecret(32, base64.StdEncoding)
	if err != nil {
		return nil, err
	}
	staticAPIKey, err := randomRuntimeSecret(32, base64.RawURLEncoding)
	if err != nil {
		return nil, err
	}
	appID, err := randomRuntimeHex(16)
	if err != nil {
		return nil, err
	}
	appKey, err := randomRuntimeSecret(32, base64.StdEncoding)
	if err != nil {
		return nil, err
	}
	tinyAuthBootstrapSecret, err := randomRuntimeSecret(32, base64.RawURLEncoding)
	if err != nil {
		return nil, err
	}
	names := []string{
		"DB_PASSWORD", "REDIS_PASSWORD", "PUSHER_APP_ID", "PUSHER_APP_KEY", "PUSHER_APP_SECRET",
	}
	secrets := make(map[string]string, len(names))
	for _, name := range names {
		value, secretErr := randomRuntimeHex(32)
		if secretErr != nil {
			return nil, secretErr
		}
		secrets[name] = value
	}
	encode := func(lines ...string) []byte {
		return []byte(strings.Join(lines, "\n") + "\n")
	}
	return map[string][]byte{
		"pocketid.env": encode(
			"APP_URL=https://id."+domain,
			"ENCRYPTION_KEY="+encryptionKey,
			"STATIC_API_KEY="+staticAPIKey,
			"TRUST_PROXY=true",
			"VERSION_CHECK_DISABLED=true",
			"ANALYTICS_DISABLED=true",
		),
		"tinyauth.env": encode(
			"TINYAUTH_APPURL=https://auth."+domain,
			"TINYAUTH_AUTH_SESSIONEXPIRY="+fmt.Sprintf("%d", sessionTTLSeconds),
			// The router serves application routes websecure-only and redirects
			// web to websecure, so the browser session cookie must carry the
			// Secure flag; container-internal service traffic is unaffected.
			"TINYAUTH_AUTH_SECURECOOKIE=true",
			"TINYAUTH_DATABASE_PATH=/data/tinyauth.db",
			"TINYAUTH_ANALYTICS_ENABLED=false",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTID=stackkit-tinyauth",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTSECRET="+tinyAuthBootstrapSecret,
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_AUTHURL=https://id."+domain+"/authorize",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_TOKENURL=http://pocketid:1411/api/oidc/token",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_USERINFOURL=http://pocketid:1411/api/oidc/userinfo",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL=https://auth."+domain+"/api/oauth/callback/pocketid",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_SCOPES=openid email profile groups",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_NAME=Pocket ID",
			// Provider TLS is verified: server-side provider endpoints are
			// container-local http (no TLS to skip), and the step-ca root is
			// mounted into TinyAuth with SSL_CERT_FILE for any TLS endpoint.
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_INSECURE=false",
			"TINYAUTH_OAUTH_AUTOREDIRECT=pocketid",
			"TINYAUTH_OAUTH_WHITELIST="+owner.PocketID.Email,
		),
		"coolify.env": encode(
			"APP_ID="+appID,
			"APP_NAME=Coolify",
			"APP_KEY=base64:"+appKey,
			"APP_ENV=production",
			"DB_USERNAME=coolify",
			"DB_PASSWORD="+secrets["DB_PASSWORD"],
			"DB_DATABASE=coolify",
			"DB_HOST=coolify-postgres",
			"POSTGRES_USER=coolify",
			"POSTGRES_PASSWORD="+secrets["DB_PASSWORD"],
			"POSTGRES_DB=coolify",
			"REDIS_PASSWORD="+secrets["REDIS_PASSWORD"],
			"REDIS_HOST=coolify-redis",
			"PUSHER_APP_ID="+secrets["PUSHER_APP_ID"],
			"PUSHER_APP_KEY="+secrets["PUSHER_APP_KEY"],
			"PUSHER_APP_SECRET="+secrets["PUSHER_APP_SECRET"],
			"PUSHER_HOST=coolify-realtime",
			"SOKETI_DEFAULT_APP_ID="+secrets["PUSHER_APP_ID"],
			"SOKETI_DEFAULT_APP_KEY="+secrets["PUSHER_APP_KEY"],
			"SOKETI_DEFAULT_APP_SECRET="+secrets["PUSHER_APP_SECRET"],
			"SOKETI_HOST=0.0.0.0",
			"REGISTRY_URL=ghcr.io",
			"AUTOUPDATE=false",
			"CDN_URL=http://hub/.stackkit/offline/coolify/cdn",
			"VERSIONS_URL=http://hub/.stackkit/offline/coolify/versions.json",
			"UPGRADE_SCRIPT_URL=http://hub/.stackkit/offline/coolify/upgrade.sh",
			"RELEASES_URL=http://hub/.stackkit/offline/coolify/releases.json",
		),
	}, nil
}

// ReadBasementRuntimePocketIDAdminKey returns the declarative PocketID
// bootstrap key only after the complete owner-signed runtime custody has been
// verified. The key is never included in a public record or diagnostic.
func ReadBasementRuntimePocketIDAdminKey(workspaceRoot string) (string, error) {
	if _, err := LoadBasementRuntimeCustody(workspaceRoot); err != nil {
		return "", err
	}
	directory, err := confinedCustodyPath(workspaceRoot, basementRuntimeCustodyRelDir)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(directory, "pocketid.env")) //nolint:gosec // fixed verified custody path
	if err != nil {
		return "", errors.New("localevidence: read verified PocketID runtime input")
	}
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		name, value, found := strings.Cut(line, "=")
		if !found || name == "" {
			return "", errors.New("localevidence: verified PocketID runtime input is malformed")
		}
		if _, duplicate := values[name]; duplicate {
			return "", errors.New("localevidence: verified PocketID runtime input contains a duplicate key")
		}
		values[name] = value
	}
	key := values["STATIC_API_KEY"]
	if len(key) < 32 {
		return "", errors.New("localevidence: verified PocketID admin key is malformed")
	}
	return key, nil
}

func randomRuntimeSecret(size int, encoding *base64.Encoding) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("localevidence: generate Basement runtime secret: %w", err)
	}
	return encoding.EncodeToString(raw), nil
}

func randomRuntimeHex(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("localevidence: generate Basement runtime secret: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func basementRuntimeSigningBytes(record BasementRuntimeCustody) ([]byte, error) {
	record.Signature = ""
	return json.Marshal(record)
}

func validateBasementRuntimeInventory(directory string, files []BasementRuntimeCustodyFile, key OwnerKey) error {
	if len(files) != len(basementRuntimeFilePaths) {
		return errors.New("localevidence: Basement runtime custody file inventory is not complete")
	}
	expected := append([]string(nil), basementRuntimeFilePaths...)
	sort.Strings(expected)
	actual := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if _, duplicate := seen[file.Path]; duplicate {
			return errors.New("localevidence: Basement runtime custody file inventory contains a duplicate")
		}
		seen[file.Path] = struct{}{}
		actual = append(actual, file.Path)
	}
	sort.Strings(actual)
	if !equalStrings(actual, expected) {
		return errors.New("localevidence: Basement runtime custody file inventory is not the closed expected set")
	}
	for _, file := range files {
		path := filepath.Join(directory, filepath.FromSlash(file.Path))
		relative, err := filepath.Rel(directory, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("localevidence: Basement runtime custody file path escapes its bundle")
		}
		if err := requirePrivateRuntimeFile(path); err != nil {
			return err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // closed signed inventory below explicit workspace
		if err != nil {
			return fmt.Errorf("localevidence: read Basement runtime custody file %q: %w", file.Path, err)
		}
		want := basementRuntimeFileMAC(key, file.Path, raw)
		if !hmac.Equal([]byte(file.MAC), []byte(want)) {
			return fmt.Errorf("localevidence: Basement runtime custody file %q MAC does not match its signed manifest", file.Path)
		}
	}
	return validateBasementRuntimeDiskTree(directory)
}

func basementRuntimeFileMAC(key OwnerKey, path string, content []byte) string {
	authenticator := hmac.New(sha256.New, key.private.Seed())
	_, _ = authenticator.Write([]byte("stackkit.basement-runtime-file/v1\x00"))
	_, _ = authenticator.Write([]byte(path))
	_, _ = authenticator.Write([]byte{0})
	_, _ = authenticator.Write(content)
	return "hmac-sha256:" + hex.EncodeToString(authenticator.Sum(nil))
}

func validateBasementRuntimeDiskTree(directory string) error {
	allowedFiles := map[string]struct{}{basementRuntimeManifestRelPath: {}}
	for _, relative := range basementRuntimeFilePaths {
		allowedFiles[relative] = struct{}{}
	}
	allowedDirectories := map[string]struct{}{
		".": {}, "step-ca": {}, "step-ca/certs": {}, "step-ca/config": {},
		"step-ca/secrets": {}, "step-ca/db": {},
	}
	return filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("localevidence: inspect Basement runtime custody tree: %w", walkErr)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return fmt.Errorf("localevidence: resolve Basement runtime custody tree entry: %w", err)
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("localevidence: Basement runtime custody contains a symbolic link")
		}
		if entry.IsDir() {
			if relative == "step-ca/db" {
				return filepath.SkipDir
			}
			if _, ok := allowedDirectories[relative]; !ok {
				return fmt.Errorf("localevidence: Basement runtime custody contains unexpected directory %q", relative)
			}
			return nil
		}
		if _, ok := allowedFiles[relative]; !ok {
			return fmt.Errorf("localevidence: Basement runtime custody contains unexpected file %q", relative)
		}
		return nil
	})
}

func validateBasementStepCA(directory string, owner OwnerCustody) error {
	rootPath := filepath.Join(directory, "step-ca", "certs", "root_ca.crt")
	intermediatePath := filepath.Join(directory, "step-ca", "certs", "intermediate_ca.crt")
	rootRaw, err := os.ReadFile(rootPath) //nolint:gosec // closed signed inventory
	if err != nil {
		return err
	}
	if !bytes.Equal(rootRaw, []byte(owner.StepCARootCertificatePEM)) {
		return errors.New("localevidence: Basement runtime step-ca root differs from owner custody")
	}
	rootCertificate, err := parseCertificatePEM(string(rootRaw), "runtime step-ca root")
	if err != nil {
		return err
	}
	intermediateRaw, err := os.ReadFile(intermediatePath) //nolint:gosec // closed signed inventory
	if err != nil {
		return err
	}
	intermediateCertificate, err := parseCertificatePEM(string(intermediateRaw), "runtime step-ca intermediate")
	if err != nil {
		return err
	}
	if err := intermediateCertificate.CheckSignatureFrom(rootCertificate); err != nil {
		return fmt.Errorf("localevidence: verify Basement runtime step-ca intermediate: %w", err)
	}
	password, err := os.ReadFile(filepath.Join(directory, "step-ca", "secrets", "password")) //nolint:gosec // closed signed inventory
	if err != nil {
		return err
	}
	encrypted, err := os.ReadFile(filepath.Join(directory, "step-ca", "secrets", "intermediate_ca_key")) //nolint:gosec // closed signed inventory
	if err != nil {
		return err
	}
	parsed, err := pemutil.Parse(encrypted, pemutil.WithPassword(password))
	if err != nil {
		return errors.New("localevidence: Basement runtime step-ca intermediate key cannot be decrypted")
	}
	private, privateOK := parsed.(ed25519.PrivateKey)
	public, publicOK := intermediateCertificate.PublicKey.(ed25519.PublicKey)
	if !privateOK || !publicOK || !bytes.Equal(private.Public().(ed25519.PublicKey), public) {
		return errors.New("localevidence: Basement runtime step-ca intermediate key does not match its certificate")
	}
	return nil
}

func writePrivateRuntimeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, ownerKeyFileMode); err != nil {
		return err
	}
	if err := os.Chmod(path, ownerKeyFileMode); err != nil {
		return err
	}
	return restrictFileToCurrentUser(path)
}

func requirePrivateRuntimeFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("localevidence: inspect Basement runtime custody file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("localevidence: Basement runtime custody contains a non-regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != ownerKeyFileMode {
		return errors.New("localevidence: Basement runtime custody file permissions are not owner-only")
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type basementStepCAConfig struct {
	Root      string                  `json:"root"`
	Crt       string                  `json:"crt"`
	Key       string                  `json:"key"`
	Address   string                  `json:"address"`
	DNSNames  []string                `json:"dnsNames"`
	Logger    basementStepCALogger    `json:"logger"`
	DB        basementStepCADatabase  `json:"db"`
	Authority basementStepCAAuthority `json:"authority"`
}

type basementStepCALogger struct {
	Format string `json:"format"`
}

type basementStepCADatabase struct {
	Type       string `json:"type"`
	DataSource string `json:"dataSource"`
}

type basementStepCAAuthority struct {
	Provisioners []basementStepCAProvisioner `json:"provisioners"`
}

type basementStepCAProvisioner struct {
	Type string `json:"type"`
	Name string `json:"name"`
}
