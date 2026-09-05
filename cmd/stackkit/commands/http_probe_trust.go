package commands

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"

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
	return &http.Client{Transport: transport}
}
