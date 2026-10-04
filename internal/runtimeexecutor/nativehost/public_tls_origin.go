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
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/originca"
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
