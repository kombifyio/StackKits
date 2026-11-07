package nativehost

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// A joined server's Basement core (homelab identity target §4, P2) runs no
// Pocket ID: Compose leaves the identity-authority profile inactive, TinyAuth
// signs in against the Authority's Pocket ID with the client the Authority
// registered and the Wallet delivered, and step-ca issues under the joined
// intermediate. The core's owner identity is this server's Owner custody plus
// the Authority's Owner subject; nothing is realized in a local Pocket ID.

// basementCoreJoined reports a core joined to an Authority.
func basementCoreJoined(workspaceRoot string) bool {
	return localevidence.JoinedCoreActive(workspaceRoot)
}

// joinedBasementOwnerIdentity is the joined core's owner identity: the
// Authority's client must be bound for TinyAuth before the core completes.
func joinedBasementOwnerIdentity(workspaceRoot string) (localevidence.OwnerRuntimeBinding, error) {
	if _, err := localevidence.LoadJoinedTinyAuthPocketIDBinding(workspaceRoot); err != nil {
		return localevidence.OwnerRuntimeBinding{}, fmt.Errorf("this joined server's TinyAuth has no client of the Authority's Pocket ID yet (Techstack registers it at the Authority and delivers it through the Wallet): %w", err)
	}
	return localevidence.JoinedOwnerRuntimeBinding(workspaceRoot)
}

// joinedBasementCoreServices drops the services of the identity-authority
// profile from the pinned service set a joined core runs.
func joinedBasementCoreServices(services []BasementCoreServiceExpectation) []BasementCoreServiceExpectation {
	result := make([]BasementCoreServiceExpectation, 0, len(services))
	for _, service := range services {
		if service.Ref == "pocketid" {
			continue
		}
		result = append(result, service)
	}
	return result
}

const basementCorePocketIDPort = 1411

// probeJoinedAuthorityPocketID is a joined core's Pocket ID health: the
// Authority's issuer answers its discovery document at the Owner-signed LAN
// address, over TLS verified against the Authority root.
func probeJoinedAuthorityPocketID(ctx context.Context, workspaceRoot string) error {
	joined, err := localevidence.LoadJoinedCore(workspaceRoot)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(joined.AuthorityRootPEM)) {
		return errors.New("the joined Authority root is unreadable")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: joined.AuthorityIdentityHost},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if joined.AuthorityLANAddress != "" {
				address = net.JoinHostPort(joined.AuthorityLANAddress, "443")
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+joined.AuthorityIdentityHost+"/.well-known/openid-configuration", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("the Authority's Pocket ID is unreachable from this joined server: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the Authority's Pocket ID answered HTTP %d", response.StatusCode)
	}
	return nil
}

// joinedAwareProber probes a joined core's Pocket ID contract at the
// Authority and every other contract locally.
type joinedAwareProber struct {
	workspaceRoot string
	local         basementCoreProber
}

func (p joinedAwareProber) Probe(ctx context.Context, expectation BasementCoreHealthExpectation) error {
	if expectation.Kind == "http" && expectation.Port == basementCorePocketIDPort && basementCoreJoined(p.workspaceRoot) {
		return probeJoinedAuthorityPocketID(ctx, p.workspaceRoot)
	}
	return p.local.Probe(ctx, expectation)
}
