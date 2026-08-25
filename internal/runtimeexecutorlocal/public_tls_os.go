package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	publicTLSTraefikAPIBase = "http://127.0.0.1:8080"
	publicTLSCustodyRoot    = "public-tls"
	publicTLSStateScope     = "state"
	publicTLSEvidenceScope  = "evidence"
	publicTLSMaxStateBytes  = 512 << 10
	publicTLSProbeTimeout   = 30 * time.Second
)

type publicTLSRouteObservation struct {
	RouteRef      string
	ValidUntil    time.Time
	CertificateID string
}

type publicTLSProbe interface {
	Probe(context.Context, architecturev2renderer.PublicTLSRuntimeRoute) (publicTLSRouteObservation, error)
}

type osPublicTLSOperations struct {
	workspaceRoot string
	probe         publicTLSProbe
	now           func() time.Time
	mu            sync.Mutex
}

// NewOSPublicTLSOperations binds Public TLS to the node-local Traefik edge.
// The owner never reads or writes private key material: Traefik's ACME
// resolver owns that custody. Route/config owners provide the declared router;
// this adapter requires its ACME resolver and proves the resulting public
// HTTPS certificate over the wire before it records lifecycle evidence.
func NewOSPublicTLSOperations(workspaceRoot string) (*osPublicTLSOperations, error) {
	root, err := ownerWorkspaceRoot(workspaceRoot, "local Cloud public TLS")
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: publicTLSProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("public TLS HTTPS verification must not follow redirects")
		},
	}
	return &osPublicTLSOperations{
		workspaceRoot: root,
		probe: &traefikPublicTLSProbe{
			apiBaseURL: publicTLSTraefikAPIBase,
			client:     client,
			now:        func() time.Time { return time.Now().UTC() },
		},
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func newOSPublicTLSOperations(workspaceRoot string, probe publicTLSProbe) (*osPublicTLSOperations, error) {
	root, err := ownerWorkspaceRoot(workspaceRoot, "local Cloud public TLS")
	if err != nil {
		return nil, err
	}
	if probe == nil {
		return nil, errors.New("local Cloud public TLS requires an HTTPS probe")
	}
	return &osPublicTLSOperations{
		workspaceRoot: root,
		probe:         probe,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

func (o *osPublicTLSOperations) MaterializePublicTLS(ctx context.Context, policy PublicTLSApplyPolicy) (PublicTLSObservation, error) {
	if err := publicTLSOperationContext(ctx); err != nil {
		return PublicTLSObservation{}, err
	}
	if err := validatePublicTLSApplyPolicy(policy); err != nil {
		return PublicTLSObservation{}, err
	}
	if err := o.ready(); err != nil {
		return PublicTLSObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	observation, routes, err := o.observe(ctx, policy.PolicyDigest, policy.EvaluatedAt, policy.Issuer.MaterialSlots, policy.Routes, "materialized")
	if err != nil {
		return PublicTLSObservation{}, fmt.Errorf("observe materialized public TLS edge: %w", err)
	}
	if err := o.persistState(publicTLSState{PolicyDigest: policy.PolicyDigest, EvaluatedAt: policy.EvaluatedAt, Status: observation.Status,
		RouteRefs: observation.RouteRefs, MaterialSlotIDs: observation.MaterialSlotIDs, Routes: routes, ValidUntil: observation.ValidUntil}); err != nil {
		return PublicTLSObservation{}, err
	}
	return observation, nil
}

func (o *osPublicTLSOperations) RenewPublicTLS(ctx context.Context, expectation PublicTLSExpectation) (PublicTLSObservation, error) {
	if err := publicTLSOperationContext(ctx); err != nil {
		return PublicTLSObservation{}, err
	}
	if err := validatePublicTLSExpectation(expectation); err != nil {
		return PublicTLSObservation{}, err
	}
	if err := o.ready(); err != nil {
		return PublicTLSObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	state, err := o.loadState(expectation.PolicyDigest)
	if err != nil {
		return PublicTLSObservation{}, err
	}
	if state.Status != "materialized" || state.EvaluatedAt != expectation.EvaluatedAt ||
		!slices.Equal(state.RouteRefs, expectation.RouteRefs) || !slices.Equal(state.MaterialSlotIDs, expectation.MaterialSlotIDs) {
		return PublicTLSObservation{}, errors.New("public TLS renewal is not bound to the materialized policy")
	}
	observation, routes, err := o.observe(ctx, expectation.PolicyDigest, expectation.EvaluatedAt, slotsFromIDs(expectation.MaterialSlotIDs), state.Routes, "renewed")
	if err != nil {
		return PublicTLSObservation{}, fmt.Errorf("observe renewed public TLS edge: %w", err)
	}
	state.Status = observation.Status
	state.ValidUntil = observation.ValidUntil
	state.Routes = routes
	if err := o.persistState(state); err != nil {
		return PublicTLSObservation{}, err
	}
	return observation, nil
}

func (o *osPublicTLSOperations) VerifyPublicTLS(ctx context.Context, expectation PublicTLSExpectation) (PublicTLSObservation, error) {
	if err := publicTLSOperationContext(ctx); err != nil {
		return PublicTLSObservation{}, err
	}
	if err := validatePublicTLSExpectation(expectation); err != nil {
		return PublicTLSObservation{}, err
	}
	if err := o.ready(); err != nil {
		return PublicTLSObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	state, err := o.loadState(expectation.PolicyDigest)
	if err != nil {
		return PublicTLSObservation{}, err
	}
	if state.Status != "renewed" || state.EvaluatedAt != expectation.EvaluatedAt ||
		!slices.Equal(state.RouteRefs, expectation.RouteRefs) || !slices.Equal(state.MaterialSlotIDs, expectation.MaterialSlotIDs) {
		return PublicTLSObservation{}, errors.New("public TLS verification is not bound to the renewed policy")
	}
	observation, routes, err := o.observe(ctx, expectation.PolicyDigest, expectation.EvaluatedAt, slotsFromIDs(expectation.MaterialSlotIDs), state.Routes, "ready")
	if err != nil {
		return PublicTLSObservation{}, fmt.Errorf("observe verified public TLS edge: %w", err)
	}
	state.Status = observation.Status
	state.ValidUntil = observation.ValidUntil
	state.Routes = routes
	if err := o.persistState(state); err != nil {
		return PublicTLSObservation{}, err
	}
	return observation, nil
}

func (o *osPublicTLSOperations) observe(ctx context.Context, policyDigest, evaluatedAt string, slots []architecturev2renderer.PublicTLSRuntimeMaterialSlot, routes []architecturev2renderer.PublicTLSRuntimeRoute, status string) (PublicTLSObservation, []architecturev2renderer.PublicTLSRuntimeRoute, error) {
	if len(routes) == 0 {
		return PublicTLSObservation{}, nil, errors.New("public TLS materialization requires at least one declared public route")
	}
	evaluated, err := time.Parse(time.RFC3339Nano, evaluatedAt)
	if err != nil || evaluated.IsZero() || evaluated.Location() != time.UTC {
		return PublicTLSObservation{}, nil, errors.New("public TLS policy has no canonical evaluation time")
	}
	if status != "materialized" && status != "renewed" && status != "ready" {
		return PublicTLSObservation{}, nil, errors.New("public TLS observation status is outside the owner contract")
	}
	routeRefs := make([]string, len(routes))
	observedRoutes := make([]architecturev2renderer.PublicTLSRuntimeRoute, len(routes))
	validUntil := time.Time{}
	for index, route := range routes {
		if err := validatePublicTLSRoute(route); err != nil {
			return PublicTLSObservation{}, nil, err
		}
		if index > 0 && route.ID <= routeRefs[index-1] {
			return PublicTLSObservation{}, nil, errors.New("public TLS routes are not unique and sorted")
		}
		observation, err := o.probe.Probe(ctx, route)
		if err != nil {
			return PublicTLSObservation{}, nil, fmt.Errorf("probe public TLS route %q: %w", route.ID, err)
		}
		if observation.RouteRef != route.ID || observation.ValidUntil.IsZero() || observation.ValidUntil.Before(evaluated) {
			return PublicTLSObservation{}, nil, errors.New("public TLS HTTPS probe returned an unbound certificate observation")
		}
		if validUntil.IsZero() || observation.ValidUntil.Before(validUntil) {
			validUntil = observation.ValidUntil
		}
		routeRefs[index] = route.ID
		observedRoutes[index] = route
	}
	materialSlotIDs := make([]string, len(slots))
	for index, slot := range slots {
		materialSlotIDs[index] = slot.ID
	}
	if validUntil.IsZero() {
		return PublicTLSObservation{}, nil, errors.New("public TLS HTTPS verification returned no certificate expiry")
	}
	return PublicTLSObservation{
		PolicyDigest: policyDigest, Status: status, EvaluatedAt: evaluatedAt,
		ValidUntil: validUntil.UTC().Format(time.RFC3339Nano), RouteRefs: routeRefs, MaterialSlotIDs: materialSlotIDs,
	}, observedRoutes, nil
}

func (o *osPublicTLSOperations) ready() error {
	if o == nil || o.workspaceRoot == "" || o.probe == nil || o.now == nil {
		return errors.New("local Cloud public TLS is not initialized")
	}
	return nil
}

type publicTLSState struct {
	PolicyDigest    string                                         `json:"policyDigest"`
	EvaluatedAt     string                                         `json:"evaluatedAt"`
	Status          string                                         `json:"status"`
	ValidUntil      string                                         `json:"validUntil"`
	RouteRefs       []string                                       `json:"routeRefs"`
	MaterialSlotIDs []string                                       `json:"materialSlotIds"`
	Routes          []architecturev2renderer.PublicTLSRuntimeRoute `json:"routes"`
}

func (o *osPublicTLSOperations) persistState(state publicTLSState) error {
	if !validCoreHostBootstrapDigest(state.PolicyDigest) || state.Status == "" {
		return errors.New("public TLS custody state is not bound to an operation")
	}
	if err := o.persist(publicTLSStateScope, publicTLSDigestName(state.PolicyDigest), state); err != nil {
		return err
	}
	return o.persist(publicTLSEvidenceScope, publicTLSDigestName(state.PolicyDigest)+"-"+state.Status, state)
}

func (o *osPublicTLSOperations) loadState(policyDigest string) (publicTLSState, error) {
	if !validCoreHostBootstrapDigest(policyDigest) {
		return publicTLSState{}, errors.New("public TLS custody lookup is not bound to an operation")
	}
	name := publicTLSDigestName(policyDigest)
	path := filepath.Join(o.workspaceRoot, ".stackkit", publicTLSCustodyRoot, publicTLSStateScope, name+".json")
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return publicTLSState{}, errors.New("public TLS custody has no materialized state for the policy")
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is derived from a validated SHA-256 digest under owner custody.
	if err != nil || len(raw) == 0 || len(raw) > publicTLSMaxStateBytes {
		return publicTLSState{}, errors.New("public TLS custody state is unavailable")
	}
	var state publicTLSState
	if err := json.Unmarshal(raw, &state); err != nil || state.PolicyDigest != policyDigest {
		return publicTLSState{}, errors.New("public TLS custody state is corrupt")
	}
	return state, nil
}

func (o *osPublicTLSOperations) persist(scope, name string, value publicTLSState) error {
	if filepath.Base(scope) != scope || filepath.Base(name) != name {
		return errors.New("public TLS custody path is outside the owner boundary")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal public TLS custody evidence: %w", err)
	}
	if len(raw) > publicTLSMaxStateBytes {
		return errors.New("public TLS custody evidence exceeds the owner boundary")
	}
	directory := filepath.Join(o.workspaceRoot, ".stackkit", publicTLSCustodyRoot, scope)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create public TLS custody directory: %w", err)
	}
	path := filepath.Join(directory, name+".json")
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("public TLS custody path is a symlink")
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("persist public TLS custody evidence: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func publicTLSDigestName(digest string) string { return strings.TrimPrefix(digest, "sha256:") }

func slotsFromIDs(ids []string) []architecturev2renderer.PublicTLSRuntimeMaterialSlot {
	result := make([]architecturev2renderer.PublicTLSRuntimeMaterialSlot, len(ids))
	for index, id := range ids {
		result[index] = architecturev2renderer.PublicTLSRuntimeMaterialSlot{ID: id}
	}
	return result
}

func publicTLSOperationContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("local Cloud public TLS requires a context")
	}
	return ctx.Err()
}

func validatePublicTLSApplyPolicy(policy PublicTLSApplyPolicy) error {
	if !validCoreHostBootstrapDigest(policy.PolicyDigest) || strings.TrimSpace(policy.StackID) == "" || strings.TrimSpace(policy.SiteRef) == "" ||
		strings.TrimSpace(policy.NodeRef) == "" || strings.TrimSpace(policy.ExecutionChannelRef) == "" || policy.Profile.Mode != "terminate-at-edge" ||
		policy.Profile.ID == "" || policy.Profile.TrustDomain != "web-pki" || policy.Issuer.ID == "" || policy.Issuer.Kind != "acme" || policy.Issuer.Challenge != "http-01" ||
		policy.Issuer.ValiditySeconds <= policy.Issuer.RenewBeforeSeconds || policy.Issuer.RenewBeforeSeconds <= 0 {
		return errors.New("public TLS policy is outside the authenticated edge owner contract")
	}
	if len(policy.Issuer.MaterialSlots) == 0 {
		return errors.New("public TLS policy has no logical material slots")
	}
	if _, err := time.Parse(time.RFC3339Nano, policy.EvaluatedAt); err != nil {
		return errors.New("public TLS policy has an invalid evaluation time")
	}
	seenSlots := make(map[string]struct{}, len(policy.Issuer.MaterialSlots))
	for _, slot := range policy.Issuer.MaterialSlots {
		if strings.TrimSpace(slot.ID) == "" {
			return errors.New("public TLS material slot is empty")
		}
		if _, exists := seenSlots[slot.ID]; exists {
			return errors.New("public TLS material slots are not unique")
		}
		seenSlots[slot.ID] = struct{}{}
	}
	for _, route := range policy.Routes {
		if err := validatePublicTLSRoute(route); err != nil {
			return err
		}
		if route.ProfileRef != policy.Profile.ID || route.IssuerRef != policy.Issuer.ID {
			return errors.New("public TLS route is bound to a different profile or issuer")
		}
	}
	return nil
}

func validatePublicTLSExpectation(expectation PublicTLSExpectation) error {
	if !validCoreHostBootstrapDigest(expectation.PolicyDigest) || strings.TrimSpace(expectation.StackID) == "" ||
		strings.TrimSpace(expectation.SiteRef) == "" || strings.TrimSpace(expectation.NodeRef) == "" || strings.TrimSpace(expectation.ExecutionChannelRef) == "" ||
		expectation.ValiditySeconds <= expectation.RenewBeforeSeconds || expectation.RenewBeforeSeconds <= 0 || len(expectation.RouteRefs) == 0 || len(expectation.MaterialSlotIDs) == 0 {
		return errors.New("public TLS expectation is outside the authenticated edge owner contract")
	}
	if _, err := time.Parse(time.RFC3339Nano, expectation.EvaluatedAt); err != nil {
		return errors.New("public TLS expectation has an invalid evaluation time")
	}
	if !slices.IsSorted(expectation.RouteRefs) || !uniqueStrings(expectation.RouteRefs) || !uniqueStrings(expectation.MaterialSlotIDs) {
		return errors.New("public TLS expectation refs are not unique and sorted")
	}
	return nil
}

func uniqueStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validatePublicTLSRoute(route architecturev2renderer.PublicTLSRuntimeRoute) error {
	if strings.TrimSpace(route.ID) == "" || !validPublicTLSHost(route.Host) || route.Port < 1 || route.Port > 65535 ||
		route.Protocol != "https" || route.TLSMode != "terminate-at-edge" || !strings.HasPrefix(route.Path, "/") ||
		strings.ContainsAny(route.Host+route.Path, "\r\n\x00") {
		return errors.New("public TLS route is outside the authenticated HTTPS edge contract")
	}
	return nil
}

func validPublicTLSHost(host string) bool {
	if host == "" || host != strings.TrimSpace(host) || len(host) > 253 || strings.ContainsAny(host, "@/:\\") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && character != '_' && (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

type traefikPublicTLSProbe struct {
	apiBaseURL string
	client     *http.Client
	now        func() time.Time
}

type traefikHTTPRouter struct {
	Status string `json:"status"`
	Rule   string `json:"rule"`
	TLS    *struct {
		CertResolver string `json:"certResolver"`
	} `json:"tls"`
}

func (p *traefikPublicTLSProbe) Probe(ctx context.Context, route architecturev2renderer.PublicTLSRuntimeRoute) (publicTLSRouteObservation, error) {
	if p == nil || p.client == nil || p.now == nil || strings.TrimSpace(p.apiBaseURL) == "" {
		return publicTLSRouteObservation{}, errors.New("Traefik public TLS probe is not initialized")
	}
	if err := validatePublicTLSRoute(route); err != nil {
		return publicTLSRouteObservation{}, err
	}
	routers, err := p.routers(ctx)
	if err != nil {
		return publicTLSRouteObservation{}, err
	}
	matched := false
	for _, router := range routers {
		if !strings.EqualFold(router.Status, "enabled") || !publicTLSRouterMatches(router.Rule, route) || router.TLS == nil || strings.TrimSpace(router.TLS.CertResolver) == "" {
			continue
		}
		matched = true
		break
	}
	if !matched {
		return publicTLSRouteObservation{}, errors.New("Traefik has no enabled TLS router with an ACME resolver for the declared route")
	}
	return p.verifyHTTPS(ctx, route)
}

func (p *traefikPublicTLSProbe) routers(ctx context.Context) ([]traefikHTTPRouter, error) {
	base, err := url.Parse(strings.TrimRight(p.apiBaseURL, "/"))
	if err != nil || base.Scheme != "http" || base.Host != "127.0.0.1:8080" {
		return nil, errors.New("Traefik public TLS API endpoint is not the fixed local endpoint")
	}
	base.Path = "/api/http/routers"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read Traefik HTTP routers: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Traefik HTTP router API returned status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, publicTLSMaxStateBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > publicTLSMaxStateBytes {
		return nil, errors.New("Traefik HTTP router API returned an invalid bounded response")
	}
	var routers []traefikHTTPRouter
	if err := json.Unmarshal(raw, &routers); err != nil {
		return nil, fmt.Errorf("decode Traefik HTTP router API: %w", err)
	}
	return routers, nil
}

func publicTLSRouterMatches(rule string, route architecturev2renderer.PublicTLSRuntimeRoute) bool {
	hostToken := "Host(`" + route.Host + "`)"
	if !strings.Contains(rule, hostToken) {
		return false
	}
	return route.Path == "/" || strings.Contains(rule, "PathPrefix(`"+route.Path+"`)") || strings.Contains(rule, "Path(`"+route.Path+"`)")
}

func (p *traefikPublicTLSProbe) verifyHTTPS(ctx context.Context, route architecturev2renderer.PublicTLSRuntimeRoute) (publicTLSRouteObservation, error) {
	minimumVersion := uint16(tls.VersionTLS12)
	if route.MinVersion == "TLS1.3" {
		minimumVersion = tls.VersionTLS13
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: minimumVersion, ServerName: route.Host}}
	client := &http.Client{Transport: transport, Timeout: publicTLSProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("public TLS HTTPS verification must not follow redirects")
	}}
	address := net.JoinHostPort(route.Host, strconv.Itoa(route.Port))
	requestURL := "https://" + address + route.Path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return publicTLSRouteObservation{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return publicTLSRouteObservation{}, fmt.Errorf("connect to declared HTTPS route: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode >= http.StatusInternalServerError || response.TLS == nil || len(response.TLS.PeerCertificates) == 0 {
		return publicTLSRouteObservation{}, errors.New("declared HTTPS route did not return a healthy TLS certificate")
	}
	certificate := response.TLS.PeerCertificates[0]
	now := p.now().UTC()
	if now.IsZero() || certificate.NotBefore.After(now) || !certificate.NotAfter.After(now) {
		return publicTLSRouteObservation{}, errors.New("declared HTTPS route certificate is not currently valid")
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	return publicTLSRouteObservation{RouteRef: route.ID, ValidUntil: certificate.NotAfter.UTC(), CertificateID: "sha256:" + hex.EncodeToString(fingerprint[:])}, nil
}

var _ PublicTLSOperations = (*osPublicTLSOperations)(nil)
var _ publicTLSProbe = (*traefikPublicTLSProbe)(nil)
var _ runtimeexecutor.Executor = (*PublicTLSExecutor)(nil)
