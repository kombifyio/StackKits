package federationcontrol

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Endpoint is selected locally by Home, never accepted in a remote Action.
type Endpoint struct {
	URL                     string `json:"url"`
	ServerCertificateSHA256 string `json:"serverCertificateSHA256"`
	RootCertificatePath     string `json:"rootCertificatePath"`
	CertificatePath         string `json:"certificatePath"`
	PrivateKeyPath          string `json:"privateKeyPath"`
}

func SendAction(ctx context.Context, root string, e Endpoint, a Action) (Result, error) {
	var result Result
	if err := validateAction(a, time.Now().UTC()); err != nil {
		return result, err
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return result, err
	}
	key, err := localevidence.LoadOwnerKey(root)
	if err != nil {
		return result, err
	}
	if a.HomeSiteRef != owner.Binding.SiteRef || a.OwnerRef != owner.OwnerRef {
		return result, errors.New("control: only selected Home initiates an action")
	}
	if err := localevidence.VerifyRemoteAction(a.signingBytes(), a.Signature, owner.OwnerRef, owner.KeyID, key.Public()); err != nil {
		return result, err
	}
	endpoint, err := url.Parse(e.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Host == "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") || !digestPattern.MatchString(e.ServerCertificateSHA256) {
		return result, errors.New("control: exact pinned HTTPS endpoint required")
	}
	cert, err := readPrivate(root, e.CertificatePath, 64<<10)
	if err != nil {
		return result, err
	}
	private, err := readPrivate(root, e.PrivateKeyPath, 64<<10)
	if err != nil {
		return result, err
	}
	rootPEM, err := openRead(root, e.RootCertificatePath, 64<<10, false)
	if err != nil {
		return result, err
	}
	pair, err := tls.X509KeyPair(cert, private)
	if err != nil {
		return result, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return result, errors.New("control: invalid Home root")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{pair}, VerifyConnection: func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 || hashBytes(cs.PeerCertificates[0].Raw) != e.ServerCertificateSHA256 {
			return errors.New("control: endpoint certificate changed")
		}
		return nil
	}}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Until(a.ExpiresAt), CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("control: redirects forbidden") }}
	ctx, cancel := context.WithDeadline(ctx, a.ExpiresAt)
	defer cancel()
	raw, err := json.Marshal(a)
	if err != nil {
		return result, err
	}
	endpoint.Path = ActionPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var denial Denial
		if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&denial); err == nil && denial.ErrorCode != "" && denial.ReasonCode != "" {
			return result, &denial
		}
		return result, errors.New("control: remote action denied or unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return result, errors.New("control: response exceeded bound")
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return result, err
	}
	if result.Schema != "stackkit.federation-action-result/v1" || result.ActionDigest != a.Digest() || result.PlanHash != a.PlanHash || result.Action != a.Action || result.ObservedAt.Before(a.IssuedAt) || !result.ObservedAt.Before(a.ExpiresAt) || (result.Status != "succeeded" && result.Status != "failed") {
		return result, errors.New("control: response is not bound to issued action")
	}
	return result, nil
}
