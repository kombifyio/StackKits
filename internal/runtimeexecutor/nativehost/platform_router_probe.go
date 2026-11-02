package nativehost

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// verifyKitRouterDeniesAnonymous proves that a Layer-2 platform UI is only
// reachable through the node's own Core router with Kit TLS 1.3 and that the
// router's forward-auth refuses an anonymous request. The Marketplace and
// Dokploy bootstraps share it; dockerRun runs one Docker CLI command.
func verifyKitRouterDeniesAnonymous(ctx context.Context, workspace, label, coreProject, coreNetwork, host string, dockerRun func(context.Context, []string) ([]byte, error)) error {
	raw, err := dockerRun(ctx, []string{"ps", "--filter", "label=com.docker.compose.project=" + coreProject, "--filter", "label=com.docker.compose.service=router", "--no-trunc", "--quiet"})
	if err != nil {
		return err
	}
	id := strings.TrimSpace(string(raw))
	if len(id) != 64 {
		return fmt.Errorf("%s requires its own single Core router", label)
	}
	raw, err = dockerRun(ctx, []string{"inspect", "--format", fmt.Sprintf(`{{(index .NetworkSettings.Networks %q).IPAddress}}`, coreNetwork), id})
	if err != nil {
		return err
	}
	ip := strings.TrimSpace(string(raw))
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("%s Core router has no own private interface", label)
	}
	ca, err := homeIdentityCABundle(workspace)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return fmt.Errorf("%s Kit CA is invalid", label)
	}
	return waitKitRouterDenial(ctx, label, func() error {
		return probeKitRouterDenial(ctx, label, ip, host, roots)
	}, 90*time.Second, 2*time.Second)
}

// kitRouterPendingError is a router state that settles on its own right after
// a platform container starts: the Docker provider has not loaded its labels
// yet (404, 502-504) or the internal ACME certificate is not issued yet.
type kitRouterPendingError struct{ detail string }

func (e *kitRouterPendingError) Error() string { return e.detail }

// waitKitRouterDenial retries only pending router states within its budget.
// An anonymous success is never retried: the UI would be open.
func waitKitRouterDenial(ctx context.Context, label string, probe func() error, budget, interval time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		err := probe()
		var pending *kitRouterPendingError
		if err == nil || !errors.As(err, &pending) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s UI is not routed by the Kit router after %s: %w", label, budget, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s UI router check: %w (last: %v)", label, ctx.Err(), err)
		case <-time.After(interval):
		}
	}
}

func probeKitRouterDenial(ctx context.Context, label, ip, host string, roots *x509.CertPool) error {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS13}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip, "443"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return err
	}
	reply, err := client.Do(request)
	if err != nil {
		return &kitRouterPendingError{detail: fmt.Sprintf("kit_router_ui_tls_unverified: %s UI Kit CA/TLS verification failed: %v", label, err)}
	}
	defer reply.Body.Close()
	switch {
	case reply.StatusCode == http.StatusUnauthorized && reply.TLS != nil && reply.TLS.Version == tls.VersionTLS13:
		return nil
	case reply.StatusCode == http.StatusNotFound || reply.StatusCode == http.StatusBadGateway || reply.StatusCode == http.StatusServiceUnavailable || reply.StatusCode == http.StatusGatewayTimeout:
		return &kitRouterPendingError{detail: fmt.Sprintf("kit_router_ui_not_routed: %s UI answered HTTP %d through the Kit TLS router", label, reply.StatusCode)}
	default:
		return fmt.Errorf("kit_router_ui_not_denied: %s UI did not deny anonymous access through the Kit TLS router (HTTP %d)", label, reply.StatusCode)
	}
}
