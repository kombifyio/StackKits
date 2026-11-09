package localevidence

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

// The public-names certificate of a public identity origin (homelab identity
// target §5): `stackkit reach public-certificate issue` installs it in Owner
// custody and the Basement core router mounts the live directory read-only at
// PublicRouterCertificateMount.
const (
	PublicRouterCertificateRelDir = ".stackkit/custody/public-tls/live"
	PublicRouterCertificateMount  = "/public-tls"

	// The router keeps the step-ca leaves it ordered in an ACME store. Exact
	// names win over a wildcard in its certificate store, so once the public
	// certificate serves the node the router orders from a separate store:
	// leaves ordered before the certificate existed would otherwise keep
	// shadowing it for id.<name> and every other host they name.
	routerACMEStore       = "acme.json"
	routerPublicACMEStore = "acme-public.json"

	// routerCertResolver is the step-ca ACME resolver of the Basement core
	// router. The core routes name it through STACKKIT_ROUTER_CERT_RESOLVER.
	routerCertResolver = "stackkits"
)

// PublicRouterCertificateServes reports whether the installed public-names
// certificate and its key load, the certificate covers id.<domain> of the
// node's identity origin and stays valid for at least another hour. The
// router policy, the router's ACME store and its route resolvers follow this
// one decision.
func PublicRouterCertificateServes(workspaceRoot string, now time.Time) bool {
	_, ok := publicRouterCertificateDomain(workspaceRoot, now)
	return ok
}

// PublicRouterCertificateCovers reports whether the serving public-names
// certificate (*.<domain> and <domain>) covers host. A route it covers names
// no step-ca resolver, so no exact-name leaf can shadow the certificate,
// whichever of the router's providers loads first.
func PublicRouterCertificateCovers(workspaceRoot, host string) bool {
	domain, ok := publicRouterCertificateDomain(workspaceRoot, time.Now())
	return ok && architecturev2renderer.PublicNameCovers(domain, host)
}

func publicRouterCertificateDomain(workspaceRoot string, now time.Time) (string, bool) {
	domain, _, ok := publicRouterCertificate(workspaceRoot, now)
	return domain, ok
}

func publicRouterCertificate(workspaceRoot string, now time.Time) (string, *x509.Certificate, bool) {
	address, err := LocalIdentityRuntimeAddress(workspaceRoot)
	if err != nil || address.SubdomainPrefix != "" || address.Domain == "" {
		return "", nil, false
	}
	domain := strings.ToLower(address.Domain)
	live := filepath.Join(workspaceRoot, filepath.FromSlash(PublicRouterCertificateRelDir))
	pair, err := tls.LoadX509KeyPair(filepath.Join(live, "cert.pem"), filepath.Join(live, "key.pem"))
	if err != nil || len(pair.Certificate) == 0 {
		return "", nil, false
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.VerifyHostname("id."+domain) != nil {
		return "", nil, false
	}
	return domain, leaf, now.After(leaf.NotBefore) && now.Add(time.Hour).Before(leaf.NotAfter)
}

// PublicRouterCertificateServesUntil is the moment the serving public-names
// certificate stops serving (an hour before it expires). The identity signer
// refreshes the router policy then, so the covered routes fall back to step-ca
// without waiting for its next renewal round.
func PublicRouterCertificateServesUntil(workspaceRoot string, now time.Time) (time.Time, bool) {
	_, leaf, ok := publicRouterCertificate(workspaceRoot, now)
	if !ok {
		return time.Time{}, false
	}
	return leaf.NotAfter.Add(-time.Hour), true
}

// The router store record (kombify-StackKits-igti.15). When the public-names
// certificate lapses, the identity signer orders step-ca leaves for the routes
// it covered into the ACME store the router runs with. Those exact-name leaves
// would shadow the certificate again once it serves, so the signer then names
// a fresh store generation here, which the next apply that finds the
// certificate serving gives the router. The record sits in the signer-owned
// public-tls custody and holds no secret.
const (
	PublicRouterStoreRecordName = "router-store.json"
	PublicRouterStoreSchema     = "stackkit.public-router-store/v1"
	publicRouterStoreMaxBytes   = 4 << 10
)

// PublicRouterStoreRecord names the public store generation the next serving
// apply uses, the store the last fallback ordered step-ca leaves into and the
// lapse that retired it (the certificate's sha256, or "none" once removed).
type PublicRouterStoreRecord struct {
	Schema     string `json:"schema"`
	Generation string `json:"generation"`
	Retired    string `json:"retired"`
	Lapse      string `json:"lapse"`
}

var (
	publicRouterStoreGeneration = regexp.MustCompile(`^[0-9a-f]{12}$`)
	publicRouterStoreLapse      = regexp.MustCompile(`^(none|sha256:[0-9a-f]{64})$`)
)

// PublicRouterStoreCustodyDir is the public-tls custody directory that holds
// the record.
func PublicRouterStoreCustodyDir(workspaceRoot string) string {
	return filepath.Dir(filepath.Join(workspaceRoot, filepath.FromSlash(PublicRouterCertificateRelDir)))
}

// LoadPublicRouterStoreRecord reads the record confined to the custody
// directory; a missing or invalid record is no record.
func LoadPublicRouterStoreRecord(workspaceRoot string) (PublicRouterStoreRecord, bool) {
	root, err := os.OpenRoot(PublicRouterStoreCustodyDir(workspaceRoot))
	if err != nil {
		return PublicRouterStoreRecord{}, false
	}
	defer root.Close()
	file, err := root.Open(PublicRouterStoreRecordName)
	if err != nil {
		return PublicRouterStoreRecord{}, false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, publicRouterStoreMaxBytes+1))
	if err != nil || len(raw) > publicRouterStoreMaxBytes {
		return PublicRouterStoreRecord{}, false
	}
	var record PublicRouterStoreRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || record.Schema != PublicRouterStoreSchema ||
		!publicRouterStoreGeneration.MatchString(record.Generation) || !validRouterStoreName(record.Retired) ||
		!publicRouterStoreLapse.MatchString(record.Lapse) {
		return PublicRouterStoreRecord{}, false
	}
	return record, true
}

// PublicRouterACMEStore is the store the router orders into while the public
// certificate serves: acme-public.json until a fallback retired it, then the
// generation the record names.
func PublicRouterACMEStore(workspaceRoot string) string {
	record, ok := LoadPublicRouterStoreRecord(workspaceRoot)
	if !ok {
		return routerPublicACMEStore
	}
	return "acme-public-" + record.Generation + ".json"
}

func validRouterStoreName(name string) bool {
	if name == routerPublicACMEStore {
		return true
	}
	generation, ok := strings.CutPrefix(name, "acme-public-")
	generation, suffixed := strings.CutSuffix(generation, ".json")
	return ok && suffixed && publicRouterStoreGeneration.MatchString(generation)
}

// RouterACMEStoreNames are the ACME stores the router may hold, the active
// one first: the browser CA rebind reads them.
func RouterACMEStoreNames(workspaceRoot string) []string {
	public := PublicRouterACMEStore(workspaceRoot)
	names := []string{routerACMEStore, public}
	if PublicRouterCertificateServes(workspaceRoot, time.Now()) {
		names = []string{public, routerACMEStore}
	}
	if record, ok := LoadPublicRouterStoreRecord(workspaceRoot); ok && !slices.Contains(names, record.Retired) {
		names = append(names, record.Retired)
	}
	if !slices.Contains(names, routerPublicACMEStore) {
		names = append(names, routerPublicACMEStore)
	}
	return names
}

func routerACMEStoreFor(workspaceRoot string, serves bool) string {
	if serves {
		return PublicRouterACMEStore(workspaceRoot)
	}
	return routerACMEStore
}

// routerCertResolverFor is the resolver the Basement core routes name: none
// while the public-names certificate serves the node, because every core host
// is one label under its domain and the certificate covers it.
func routerCertResolverFor(serves bool) string {
	if serves {
		return ""
	}
	return routerCertResolver
}
