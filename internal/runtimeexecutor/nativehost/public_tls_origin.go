package nativehost

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/originca"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// originPublicTLSProbe proves a managed kombify.me route from the node: the
// local router must present the delivered Cloudflare Origin CA certificate for
// the route host. Origin CA certificates are not browser trust, so the probe
// trusts the Origin CA roots instead of system trust. The public edge
// certificate is proven separately from outside the node.
type originPublicTLSProbe struct {
	// address is the local router listener; empty dials 127.0.0.1 and the route port.
	address       string
	roots         *x509.CertPool
	now           func() time.Time
	wait          time.Duration
	retryInterval time.Duration
}

func (p originPublicTLSProbe) Probe(ctx context.Context, route architecturev2renderer.PublicTLSRuntimeRoute) (publicTLSRouteObservation, error) {
	if err := validatePublicTLSRoute(route); err != nil {
		return publicTLSRouteObservation{}, err
	}
	var observation publicTLSRouteObservation
	err := waitForCertificateIssuance(ctx, p.wait, p.retryInterval, func() error {
		var err error
		observation, err = p.once(ctx, route)
		return err
	})
	return observation, err
}

func (p originPublicTLSProbe) once(ctx context.Context, route architecturev2renderer.PublicTLSRuntimeRoute) (publicTLSRouteObservation, error) {
	minimumVersion := uint16(tls.VersionTLS12)
	if route.MinVersion == "TLS1.3" {
		minimumVersion = tls.VersionTLS13
	}
	address := p.address
	if address == "" {
		address = net.JoinHostPort("127.0.0.1", strconv.Itoa(route.Port))
	}
	dialer := &tls.Dialer{Config: &tls.Config{MinVersion: minimumVersion, ServerName: route.Host, RootCAs: p.roots}}
	probeCtx, cancel := context.WithTimeout(ctx, publicTLSProbeTimeout)
	defer cancel()
	connection, err := dialer.DialContext(probeCtx, "tcp", address)
	if err != nil {
		return publicTLSRouteObservation{}, fmt.Errorf("managed route %q is not served by the delivered origin certificate: %w", route.ID, err)
	}
	defer func() { _ = connection.Close() }()
	state := connection.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return publicTLSRouteObservation{}, errors.New("managed route returned no TLS certificate")
	}
	certificate := state.PeerCertificates[0]
	if now := p.now().UTC(); certificate.NotBefore.After(now) || !certificate.NotAfter.After(now) {
		return publicTLSRouteObservation{}, fmt.Errorf("managed route %q origin certificate is not currently valid", route.ID)
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	return publicTLSRouteObservation{RouteRef: route.ID, ValidUntil: certificate.NotAfter.UTC(), CertificateID: "sha256:" + hex.EncodeToString(fingerprint[:])}, nil
}

// probeRoute selects the origin probe for a route the delivered origin
// certificate serves, and the configured ACME-edge probe for every other route.
func (o *osPublicTLSOperations) probeRoute(ctx context.Context, route architecturev2renderer.PublicTLSRuntimeRoute) (publicTLSRouteObservation, error) {
	if originca.Serves(o.workspaceRoot, route.Host, o.now()) {
		return originPublicTLSProbe{roots: originca.RootPool(), now: o.now, wait: certificateIssuanceWait, retryInterval: certificateIssuanceInterval}.Probe(ctx, route)
	}
	return o.probe.Probe(ctx, route)
}

// originCertificateMissing reports a managed kombify.me host that no installed
// origin certificate covers. An installed certificate that has expired or is
// presented wrongly still covers its host and stays a verification failure.
func originCertificateMissing(workspace, host string) bool {
	return originca.ManagedHost(host) && !originca.Covers(workspace, host)
}

// publicTLSOriginCertificatePendingError reports managed routes the local
// router serves in origin-certificate mode (ADR-0047) while no installed
// Cloudflare Origin CA certificate covers them. Techstack delivers the
// certificate through the origin-certificate operations; until then the
// routes cannot terminate TLS, and no ACME issuance wait can change that.
type publicTLSOriginCertificatePendingError struct {
	routeRefs []string
	hosts     []string
}

func (e *publicTLSOriginCertificatePendingError) Error() string {
	return fmt.Sprintf("managed public routes %s (%s) have no installed Cloudflare Origin CA certificate; the router serves them only from the delivered origin certificate",
		strings.Join(e.routeRefs, ", "), strings.Join(e.hosts, ", "))
}

// issuanceTerminal stops the issuance wait: only a delivered origin
// certificate can serve the route.
func (e *publicTLSOriginCertificatePendingError) issuanceTerminal() bool { return true }

func (e *publicTLSOriginCertificatePendingError) join(other *publicTLSOriginCertificatePendingError) *publicTLSOriginCertificatePendingError {
	if e == nil {
		return &publicTLSOriginCertificatePendingError{routeRefs: append([]string(nil), other.routeRefs...), hosts: append([]string(nil), other.hosts...)}
	}
	e.routeRefs = append(e.routeRefs, other.routeRefs...)
	e.hosts = append(e.hosts, other.hosts...)
	return e
}

// publicTLSOperationFailure wraps a failed public TLS operation. Routes that
// wait only for their origin certificate make the unit degraded and
// retryable instead of failed; every other failure stays a failure.
func publicTLSOperationFailure(target runtimeexecutor.RuntimeTarget, operation string, err error) error {
	wrapped := fmt.Errorf("%s: %w", operation, err)
	var pending *publicTLSOriginCertificatePendingError
	if errors.As(err, &pending) {
		return &applyoutcome.DegradedUnitError{RequirementID: target.RequirementID, Class: applyoutcome.ClassOriginCertificateMissing, Err: wrapped}
	}
	return wrapped
}
