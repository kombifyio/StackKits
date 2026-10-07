package nativehost

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
)

const (
	bridgeEdgeListenerSchema = "stackkit.bridge-publication-listener/v1"
	bridgeEdgeListenerPath   = ".stackkit/custody/bridge-publication/listener.json"
	bridgeEdgeTLSDirectory   = ".stackkit/custody/bridge-publication/tls"
	bridgeEdgeProbeHeader    = "X-Stackkit-Edge-Probe"
	bridgeEdgeOriginHeader   = "X-Stackkit-Edge-Origin"
	bridgeEdgeProbeLifetime  = 30 * time.Second
	bridgeEdgeStateLimit     = 64 << 10
)

var bridgeEdgeHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// BridgePublicationEdgeAuth verifies the human, device or workload credential
// a governed rule binds (Authentication, AuthPolicyRef). The edge never serves
// a rule without it: no verifier means the rule refuses every request, except
// the Owner-signed probe the owner's own served-traffic verification sends.
type BridgePublicationEdgeAuth func(*http.Request, architecturev2renderer.BridgePublicationRule) bool

// bridgeEdgeUpstream resolves the only upstream the edge may use. Production
// is the activated Cloud federation link; tests replace the transport only.
type bridgeEdgeUpstream func(root string) (http.RoundTripper, string, error)

type bridgeEdge struct {
	root     string
	now      func() time.Time
	auth     BridgePublicationEdgeAuth
	upstream bridgeEdgeUpstream

	mu      sync.Mutex
	windows map[string]*bridgeEdgeWindow
	nonces  map[string]time.Time
}

type bridgeEdgeWindow struct {
	start time.Time
	count int
}

// NewBridgePublicationEdgeServer is the Cloud edge data plane inside the
// existing stackkit-server process. It serves exactly the Owner-signed route
// table and nothing else, has no management route and reaches Home only over
// the activated federation link. It is closed until a signed table exists.
func NewBridgePublicationEdgeServer(workspaceRoot, address string, auth BridgePublicationEdgeAuth) (*http.Server, error) {
	edge := newBridgeEdge(workspaceRoot, auth)
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("bridge edge: listen address: %w", err)
	}
	if err := recordBridgeEdgeListener(workspaceRoot, address); err != nil {
		return nil, err
	}
	return &http.Server{
		Addr: address, Handler: edge,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: edge.tlsConfigFor, NextProtos: []string{"http/1.1"}},
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
	}, nil
}

func newBridgeEdge(root string, auth BridgePublicationEdgeAuth) *bridgeEdge {
	return &bridgeEdge{
		root: root, now: time.Now, auth: auth,
		upstream: bridgeEdgeFederationUpstream,
		windows:  map[string]*bridgeEdgeWindow{}, nonces: map[string]time.Time{},
	}
}

func bridgeEdgeFederationUpstream(root string) (http.RoundTripper, string, error) {
	link, err := activatedCloudFederationLink(root)
	if err != nil {
		return nil, "", err
	}
	transport, serverName, err := localorigin.NewPeerTransport(root, link.PeerRef, link.OriginSocket, link.OriginServerName)
	if err != nil {
		return nil, "", err
	}
	return transport, serverName, nil
}

func bridgeEdgeMinVersion(rule architecturev2renderer.BridgePublicationRule) uint16 {
	if rule.TLSMinVersion == "TLS1.2" {
		return tls.VersionTLS12
	}
	return tls.VersionTLS13
}

// tlsConfigFor serves a certificate only for a host the signed table lists,
// and enforces that rule's bound TLS minimum on the handshake itself.
func (e *bridgeEdge) tlsConfigFor(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	table, err := readBridgePublicationTable(e.root)
	if err != nil {
		return nil, errors.New("bridge edge: closed")
	}
	host := strings.ToLower(hello.ServerName)
	minimum := uint16(0)
	for _, rule := range table.Publications {
		if rule.Host == host {
			if v := bridgeEdgeMinVersion(rule); v > minimum {
				minimum = v
			}
		}
	}
	if minimum == 0 {
		return nil, errors.New("bridge edge: host is not published")
	}
	certificate, err := bridgeEdgeCertificate(e.root, host)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: minimum, Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}, nil
}

// bridgeEdgeCertificate reads the public host certificate another owner
// installed in node custody; the edge owner issues no certificates.
func bridgeEdgeCertificate(root, host string) (tls.Certificate, error) {
	if !bridgeEdgeHostPattern.MatchString(host) {
		return tls.Certificate{}, errors.New("bridge edge: invalid host")
	}
	fs, err := confinedfs.Open(root)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return tls.Certificate{}, err
	}
	defer tx.Close()
	certPEM, _, err := tx.ReadStableBounded(bridgeEdgeTLSDirectory+"/"+host+".crt", bridgeEdgeStateLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("bridge edge: host certificate is not installed: %w", err)
	}
	keyPEM, _, err := tx.ReadStableBounded(bridgeEdgeTLSDirectory+"/"+host+".key", bridgeEdgeStateLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("bridge edge: host key is not installed: %w", err)
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func matchBridgeRule(table bridgePublicationRouteTable, host, requestPath string) (architecturev2renderer.BridgePublicationRule, bool) {
	var best architecturev2renderer.BridgePublicationRule
	found := false
	for _, rule := range table.Publications {
		if rule.Host != host || rule.EdgeSiteRef != table.Binding.SiteRef || rule.Protocol != "https" || len(rule.OriginTargets) == 0 {
			continue
		}
		prefix := path.Clean(rule.Path)
		if prefix != "/" && requestPath != prefix && !strings.HasPrefix(requestPath, prefix+"/") {
			continue
		}
		if !found || len(prefix) > len(path.Clean(best.Path)) {
			best, found = rule, true
		}
	}
	return best, found
}

func (e *bridgeEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Del(bridgeEdgeOriginHeader)
	// An absent, unsigned, substituted or foreign-target table closes the edge.
	table, err := readBridgePublicationTable(e.root)
	if err != nil {
		http.Error(w, "edge closed", http.StatusServiceUnavailable)
		return
	}
	host := strings.ToLower(r.Host)
	if parsed, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = parsed
	}
	clean := path.Clean("/" + r.URL.Path)
	rule, ok := matchBridgeRule(table, host, clean)
	if !ok || r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version < bridgeEdgeMinVersion(rule) || !strings.EqualFold(r.TLS.ServerName, host) {
		http.Error(w, "route not published", http.StatusNotFound)
		return
	}
	methods := rule.AllowedMethods
	if len(methods) == 0 {
		methods = []string{http.MethodGet, http.MethodHead}
	}
	if !containsString(methods, r.Method) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !e.allow(rule) {
		w.Header().Set("Retry-After", fmt.Sprint(max(rule.RateLimitWindowSeconds, 1)))
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	probe := e.ownerProbe(r, rule, clean)
	if !probe && (e.auth == nil || !e.auth(r, rule)) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	// A governed HTTP route is not a tunnel.
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
		http.Error(w, "unbounded tunnel unavailable", http.StatusNotImplemented)
		return
	}
	transport, serverName, err := e.upstream(e.root)
	if err != nil {
		http.Error(w, "origin unavailable", http.StatusBadGateway)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(request *http.Request) {
			request.URL = &url.URL{Scheme: "https", Host: serverName, Path: clean, RawQuery: r.URL.RawQuery}
			request.Host = serverName
			localorigin.StripForwardedIdentity(request.Header)
			request.Header.Del(bridgeEdgeProbeHeader)
		},
		ModifyResponse: func(response *http.Response) error {
			// Only this branch proves the pinned origin answered.
			response.Header.Del(bridgeEdgeOriginHeader)
			if probe {
				response.Header.Set(bridgeEdgeOriginHeader, serverName)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "origin unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (e *bridgeEdge) allow(rule architecturev2renderer.BridgePublicationRule) bool {
	if rule.RateLimitRequests <= 0 || rule.RateLimitWindowSeconds <= 0 {
		return false // an unbound limit is not an unlimited route
	}
	now := e.now()
	key := rule.Host + "|" + path.Clean(rule.Path)
	e.mu.Lock()
	defer e.mu.Unlock()
	window := e.windows[key]
	if window == nil || now.Sub(window.start) >= time.Duration(rule.RateLimitWindowSeconds)*time.Second {
		window = &bridgeEdgeWindow{start: now}
		e.windows[key] = window
	}
	window.count++
	return window.count <= rule.RateLimitRequests
}

// edgeProbeToken is a short-lived Owner-signed read-only request credential for
// the owner's own served-traffic verification. It names one host and path.
type edgeProbeToken struct {
	Host      string                                  `json:"host"`
	Path      string                                  `json:"path"`
	Nonce     string                                  `json:"nonce"`
	ExpiresAt int64                                   `json:"expiresAt"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func newBridgeEdgeProbeToken(root, host, requestPath string, now time.Time) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	token := edgeProbeToken{Host: host, Path: path.Clean(requestPath), Nonce: hex.EncodeToString(nonce), ExpiresAt: now.Add(bridgeEdgeProbeLifetime).Unix()}
	unsigned, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	if token.Signature, err = localevidence.SignOwnerPolicyState(root, unsigned); err != nil {
		return "", err
	}
	raw, err := json.Marshal(token)
	return base64.RawURLEncoding.EncodeToString(raw), err
}

func (e *bridgeEdge) ownerProbe(r *http.Request, rule architecturev2renderer.BridgePublicationRule, clean string) bool {
	encoded := r.Header.Get(bridgeEdgeProbeHeader)
	if encoded == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > 4096 {
		return false
	}
	var token edgeProbeToken
	if json.Unmarshal(raw, &token) != nil {
		return false
	}
	signature := token.Signature
	token.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(token)
	if err != nil || localevidence.VerifyOwnerPolicyState(e.root, unsigned, signature) != nil {
		return false
	}
	now := e.now()
	if token.Host != rule.Host || token.Path != clean || token.Nonce == "" || now.Unix() > token.ExpiresAt {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for nonce, expires := range e.nonces {
		if now.After(expires) {
			delete(e.nonces, nonce)
		}
	}
	if _, replay := e.nonces[token.Nonce]; replay {
		return false
	}
	e.nonces[token.Nonce] = time.Unix(token.ExpiresAt, 0)
	return true
}

type bridgeEdgeListenerRecord struct {
	Schema    string                                  `json:"schema"`
	Binding   localevidence.LocalBinding              `json:"binding"`
	Address   string                                  `json:"address"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// recordBridgeEdgeListener lets the owner's verification find the edge it must
// send its served-traffic probe through.
func recordBridgeEdgeListener(root, address string) error {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return err
	}
	record := bridgeEdgeListenerRecord{Schema: bridgeEdgeListenerSchema, Binding: owner.Binding, Address: address}
	unsigned, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if record.Signature, err = localevidence.SignOwnerPolicyState(root, unsigned); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	fs, err := confinedfs.Open(root)
	if err != nil {
		return err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	directory := filepath.ToSlash(filepath.Dir(bridgeEdgeListenerPath))
	if err := tx.MkdirAll(directory, 0700); err != nil {
		return err
	}
	view, err := fs.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(bridgeEdgeListenerPath, raw)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("bridge edge: listener record was not durably installed")
	}
	_, err = tx.SyncDirectory(directory)
	return err
}

// readBridgeEdgeListener returns "" when no edge is declared on this node.
func readBridgeEdgeListener(root string) (string, error) {
	fs, err := confinedfs.Open(root)
	if err != nil {
		return "", err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStableBounded(bridgeEdgeListenerPath, bridgeEdgeStateLimit)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var record bridgeEdgeListenerRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return "", err
	}
	signature := record.Signature
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	if err := localevidence.VerifyOwnerPolicyState(root, unsigned, signature); err != nil {
		return "", err
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return "", err
	}
	if record.Schema != bridgeEdgeListenerSchema || record.Binding != owner.Binding {
		return "", errors.New("bridge edge: substituted listener record")
	}
	return record.Address, nil
}

// BridgePublicationServedObservation is the proof that a request through the
// edge listener reached the pinned origin. It holds no endpoint or credential.
type BridgePublicationServedObservation struct {
	Status           string `json:"status"`
	HTTPStatus       int    `json:"httpStatus"`
	OriginServerName string `json:"originServerName"`
	ObservedAt       string `json:"observedAt"`
}

// BridgePublicationServedProbe sends one real request through the edge for a
// governed rule. It returns nil without error only when no edge is declared.
type BridgePublicationServedProbe func(ctx context.Context, root string, rule architecturev2renderer.BridgePublicationRule, now time.Time) (*BridgePublicationServedObservation, error)

func probeBridgePublicationServed(ctx context.Context, root string, rule architecturev2renderer.BridgePublicationRule, now time.Time) (*BridgePublicationServedObservation, error) {
	address, err := readBridgeEdgeListener(root)
	if err != nil || address == "" {
		return nil, err
	}
	if rule.HealthProbe == nil || rule.HealthProbe.Kind != "http" {
		return nil, errors.New("bridge publication: served proof needs the governed HTTP Health probe")
	}
	probePath := path.Clean(rule.HealthProbe.Path)
	if prefix := path.Clean(rule.Path); prefix != "/" && probePath != prefix && !strings.HasPrefix(probePath, prefix+"/") {
		return nil, errors.New("bridge publication: Health probe path is outside the published route")
	}
	link, err := activatedCloudFederationLink(root)
	if err != nil {
		return nil, err
	}
	certificate, err := bridgeEdgeCertificate(root, rule.Host)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("bridge publication: custodied host certificate is unreadable: %w", err)
	}
	// The only trust anchor is the host certificate in node custody, so the
	// probe needs no public CA and a listener presenting any other
	// certificate fails standard verification (identity, host name, validity).
	custodied := x509.NewCertPool()
	custodied.AddCert(leaf)
	token, err := newBridgeEdgeProbeToken(root, rule.Host, probePath, now)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, ServerName: rule.Host, RootCAs: custodied,
	}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	method := rule.HealthProbe.Method
	if method != http.MethodHead {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, (&url.URL{Scheme: "https", Host: rule.Host, Path: probePath}).String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(bridgeEdgeProbeHeader, token)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("bridge publication: request through the edge failed: %w", err)
	}
	defer response.Body.Close()
	if response.Header.Get(bridgeEdgeOriginHeader) != link.OriginServerName {
		return nil, fmt.Errorf("bridge publication: edge answered %d without reaching the pinned origin", response.StatusCode)
	}
	if !containsStatus(rule.HealthProbe.ExpectedStatuses, response.StatusCode) {
		return nil, fmt.Errorf("bridge publication: origin answered %d through the edge, outside the governed Health statuses", response.StatusCode)
	}
	return &BridgePublicationServedObservation{Status: "served", HTTPStatus: response.StatusCode, OriginServerName: link.OriginServerName, ObservedAt: now.UTC().Format(time.RFC3339Nano)}, nil
}
