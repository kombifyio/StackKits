package commands

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// httpProbeClientForWorkspace returns an HTTP client that trusts the
// workspace step-ca root in addition to the system pool, so verify and status
// probes can validate the websecure routes the kit router serves with
// step-ca-issued certificates. A nil client means no local custody anchor
// exists and callers fall back to the default system trust; probes against
// step-ca URLs then fail closed with a certificate error instead of silently
// skipping verification.
func httpProbeClientForWorkspace(wd string) *http.Client {
	raw, _, err := localevidence.BasementStepCARootCAPEM(wd)
	if err != nil || len(raw) == 0 {
		return nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(raw) {
		return nil
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil
	}
	transport := base.Clone()
	tlsConfig := transport.TLSClientConfig.Clone()
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	tlsConfig.RootCAs = pool
	transport.TLSClientConfig = tlsConfig
	if resolver, err := localevidence.BasementLANDNSResolverAddress(wd); err == nil && resolver != "" {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		resolverAddr := net.JoinHostPort(resolver, "53")
		kitResolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				proto := "udp"
				if network == "tcp" || network == "tcp4" || network == "tcp6" {
					proto = "tcp"
				}
				return dialer.DialContext(ctx, proto, resolverAddr)
			},
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if ip := net.ParseIP(host); ip != nil {
				return dialer.DialContext(ctx, network, addr)
			}
			ips, err := kitResolver.LookupIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		}
	}
	return &http.Client{Transport: transport}
}
