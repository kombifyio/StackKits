// Package mereach is the homelab side of kombify Me's reach API (Gateway
// services/me/docs/API.md "Homelab reach: tunnel mode and DNS-01"): the
// homelab's own Me registration enables tunnel mode for its base, receives the
// connector token sealed to its local custody, and publishes DNS-01 values and
// the LAN name's private address for the LAN-name certificate (homelab
// identity target §5, §8, §14 decisions 3 and 4). It holds the Me registration key in Owner custody and never prints,
// logs or returns it.
package mereach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/hostcustody"
)

const (
	// Zone is the shared parent of every homelab base.
	Zone = "kombify.me"
	// DefaultAPI is Me's public API root.
	DefaultAPI = "https://kombify.me/_kombify/api/v1"
	// RemoteListenerIngress is the only ingress tunnel mode may carry: the
	// Access Gate's remote entrypoint on the connector network.
	RemoteListenerIngress = "http://stackkit-remote-listener.internal:8000"

	registrationAPIVersion = "stackkit.kombify-me-registration/v1"
	custodyDirName         = "kombify-me"
	registrationName       = "registration.json"
	maxResponseBytes       = 256 << 10
)

var (
	baseLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	apiKey    = regexp.MustCompile(`^kbi_[A-Za-z0-9_-]{16,128}$`)
	// ErrNoRegistration reports a homelab without a Me registration in custody.
	ErrNoRegistration = errors.New("mereach: no Me registration in custody; run 'sudo stackkit reach registration set'")
)

// BaseOf returns the Me base of a homelab domain <base>.kombify.me.
func BaseOf(domain string) (string, bool) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	base, ok := strings.CutSuffix(domain, "."+Zone)
	if !ok || !baseLabel.MatchString(base) {
		return "", false
	}
	return base, true
}

// Registration is the homelab's own Me registration: its base and the
// registration key Me answered once.
type Registration struct {
	APIVersion string `json:"apiVersion"`
	Base       string `json:"base"`
	API        string `json:"api"`
	APIKey     string `json:"apiKey"`
}

func custodyDir(workspace string) string {
	return filepath.Join(workspace, ".stackkit", "custody", custodyDirName)
}

// SaveRegistration stores the registration 0600 in Owner custody.
func SaveRegistration(workspace string, registration Registration) error {
	if !baseLabel.MatchString(registration.Base) || !apiKey.MatchString(registration.APIKey) {
		return errors.New("mereach: the registration needs a base label and a kbi_ registration key")
	}
	if err := validAPI(registration.API); err != nil {
		return err
	}
	registration.APIVersion = registrationAPIVersion
	raw, err := json.Marshal(registration)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(custodyDir(workspace), registrationName), raw)
}

// LoadRegistration reads the registration from Owner custody.
func LoadRegistration(workspace string) (Registration, error) {
	raw, err := os.ReadFile(filepath.Join(custodyDir(workspace), registrationName))
	if errors.Is(err, os.ErrNotExist) {
		return Registration{}, ErrNoRegistration
	}
	if err != nil {
		return Registration{}, err
	}
	var registration Registration
	if json.Unmarshal(raw, &registration) != nil || registration.APIVersion != registrationAPIVersion ||
		!baseLabel.MatchString(registration.Base) || !apiKey.MatchString(registration.APIKey) || validAPI(registration.API) != nil {
		return Registration{}, errors.New("mereach: the Me registration in custody is invalid")
	}
	return registration, nil
}

// RemoveRegistration deletes the registration from custody.
func RemoveRegistration(workspace string) error {
	err := os.Remove(filepath.Join(custodyDir(workspace), registrationName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// validAPI admits Me's https API root, or plain http only on a loopback or
// private development address (a local Me Worker).
func validAPI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return errors.New("mereach: the Me API must be an absolute URL without query, fragment or credentials")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if address, err := netip.ParseAddr(parsed.Hostname()); err == nil && parsed.Scheme == "http" && address.IsLoopback() {
		return nil
	}
	return errors.New("mereach: the Me API must use https (plain http only for a development Worker on loopback)")
}

// Client calls Me with the homelab's own registration key.
type Client struct {
	Registration Registration
	HTTP         *http.Client
}

// APIError is Me's error answer.
type APIError struct {
	Status    int
	Code      string `json:"error_code"`
	Message   string `json:"error"`
	Retryable bool   `json:"retryable"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("kombify Me answered %d %s: %s", e.Status, e.Code, e.Message)
}

// DNSRecord is one record of the tunnel view.
type DNSRecord struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Content string `json:"content,omitempty"`
	Proxied bool   `json:"proxied,omitempty"`
	State   string `json:"state,omitempty"`
}

// TunnelView is Me's Cloudflare read-back of a base's tunnel mode. It never
// carries a token.
type TunnelView struct {
	Base           string      `json:"base"`
	Enabled        bool        `json:"enabled"`
	TunnelID       string      `json:"tunnel_id"`
	TunnelName     string      `json:"tunnel_name"`
	TunnelStatus   string      `json:"tunnel_status"`
	Hostnames      []string    `json:"hostnames"`
	IngressService string      `json:"ingress_service"`
	IngressCurrent bool        `json:"ingress_current"`
	DNSRecords     []DNSRecord `json:"dns_records"`
	Custody        string      `json:"custody,omitempty"`
	Tunnel         string      `json:"tunnel,omitempty"`
}

type sealedDelivery struct {
	Format    string `json:"format"`
	Custody   string `json:"custody"`
	KID       string `json:"kid"`
	JWE       string `json:"jwe"`
	ExpiresAt int64  `json:"expires_at"`
}

func (c Client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = raw
	}
	client := c.HTTP
	if client == nil {
		// Never follow a redirect: it would carry the registration key to
		// another host.
		client = &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	endpoint := strings.TrimSuffix(c.Registration.API, "/") + "/subdomains/reach/" + url.PathEscape(c.Registration.Base) + path
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 3 * time.Second):
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		request.Header.Set("X-Kombify-API-Key", c.Registration.APIKey)
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			last = fmt.Errorf("kombify Me is unreachable: %w", err)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
		_ = response.Body.Close()
		if readErr != nil {
			last = readErr
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(raw, out); err != nil {
				return errors.New("mereach: Me answered an unreadable body")
			}
			return nil
		}
		apiErr := &APIError{Status: response.StatusCode}
		_ = json.Unmarshal(raw, apiErr)
		if !apiErr.Retryable {
			return apiErr
		}
		last = apiErr
	}
	return last
}

// EnableTunnel enables or repairs tunnel mode for the base. It answers no
// token. The node refuses a tunnel whose ingress is not the remote listener.
func (c Client) EnableTunnel(ctx context.Context) (TunnelView, error) {
	var view TunnelView
	if err := c.do(ctx, http.MethodPut, "/tunnel", map[string]any{}, &view); err != nil {
		return TunnelView{}, err
	}
	if err := c.checkView(view); err != nil {
		return TunnelView{}, err
	}
	return view, nil
}

// Tunnel reads the base's tunnel mode back from Cloudflare through Me.
func (c Client) Tunnel(ctx context.Context) (TunnelView, error) {
	var view TunnelView
	err := c.do(ctx, http.MethodGet, "/tunnel", nil, &view)
	return view, err
}

// DeliverToken rotates the tunnel secret and receives the new connector token
// sealed to a fresh key of this node's local custody, which opens it. The
// caller owns and clears the token.
func (c Client) DeliverToken(ctx context.Context, now func() time.Time) (TunnelView, []byte, error) {
	recipient, err := hostcustody.NewLocalCustodyRecipient("urn:stackkit:kombify-me:"+c.Registration.Base, "remote-reach-connector", now())
	if err != nil {
		return TunnelView{}, nil, err
	}
	defer recipient.Close()
	var answer struct {
		TunnelView
		TokenRotated bool           `json:"token_rotated"`
		Delivery     sealedDelivery `json:"delivery"`
	}
	if err := c.do(ctx, http.MethodPost, "/tunnel/token", map[string]any{"recipient": recipient.Recipient}, &answer); err != nil {
		return TunnelView{}, nil, err
	}
	if answer.Delivery.Format != hostcustody.WalletTransportFormat || answer.Delivery.Custody != "local" ||
		answer.Delivery.KID != recipient.Recipient.KID || answer.Custody != "local" || !answer.TokenRotated {
		return TunnelView{}, nil, errors.New("mereach: the token delivery is not sealed to this node's local custody")
	}
	if err := c.checkView(answer.TunnelView); err != nil {
		return TunnelView{}, nil, err
	}
	token, err := recipient.Open(answer.Delivery.JWE, now())
	if err != nil {
		return TunnelView{}, nil, err
	}
	return answer.TunnelView, token, nil
}

// DisableTunnel removes the base's tunnel and its DNS record; Me reads both
// back absent before it answers.
func (c Client) DisableTunnel(ctx context.Context) (TunnelView, error) {
	var receipt TunnelView
	if err := c.do(ctx, http.MethodDelete, "/tunnel", nil, &receipt); err != nil {
		return TunnelView{}, err
	}
	if receipt.Tunnel != "absent" || receipt.Enabled {
		return TunnelView{}, errors.New("mereach: Me did not confirm the tunnel absent")
	}
	for _, record := range receipt.DNSRecords {
		if record.State != "absent" {
			return TunnelView{}, fmt.Errorf("mereach: Me did not confirm %s absent", record.Name)
		}
	}
	return receipt, nil
}

// LANAddressRecord is Me's Cloudflare read-back of the base's LAN address
// record lan.<base>.kombify.me. Proxied is a pointer so that an answer without
// it never reads as DNS-only.
type LANAddressRecord struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	Proxied *bool  `json:"proxied"`
	TTL     int    `json:"ttl"`
	Created bool   `json:"created"`
}

// ErrLANAddressReadBack reports a Me answer that does not read back the LAN
// address record as it was sent.
var ErrLANAddressReadBack = errors.New("mereach: kombify Me's read-back of the LAN address record differs from what was sent")

// LANName is the LAN name of this registration's base.
func (c Client) LANName() string { return "lan." + c.Registration.Base + "." + Zone }

// SetLANAddress publishes the node's private address as the one DNS-only A or
// AAAA record of name, which must be this base's LAN name, and checks Me's
// read-back: the same name, DNS-only and the same address. A Me refusal is an
// *APIError naming Me's answer code; Me itself admits only private addresses
// and only for the caller's own base.
func (c Client) SetLANAddress(ctx context.Context, name string, address netip.Addr) error {
	if !strings.EqualFold(name, c.LANName()) {
		return fmt.Errorf("mereach: %s is not the LAN name of this registration's base %s.%s", name, c.Registration.Base, Zone)
	}
	if !address.IsValid() || !address.IsPrivate() || address.Is4In6() || address.Zone() != "" {
		return fmt.Errorf("mereach: %s is not a private address (RFC 1918 or IPv6 ULA); it never leaves this node", address)
	}
	var record LANAddressRecord
	if err := c.do(ctx, http.MethodPut, "/lan-address", map[string]string{"address": address.String()}, &record); err != nil {
		return err
	}
	want := "A"
	if address.Is6() {
		want = "AAAA"
	}
	answered, err := netip.ParseAddr(record.Address)
	if !strings.EqualFold(record.Name, c.LANName()) || record.Type != want || record.Proxied == nil || *record.Proxied ||
		err != nil || answered != address {
		return ErrLANAddressReadBack
	}
	return nil
}

// RemoveLANAddress removes the base's LAN address record; Me proves it absent.
func (c Client) RemoveLANAddress(ctx context.Context) error {
	var receipt struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := c.do(ctx, http.MethodDelete, "/lan-address", map[string]any{}, &receipt); err != nil {
		return err
	}
	if !strings.EqualFold(receipt.Name, c.LANName()) || receipt.State != "absent" {
		return fmt.Errorf("mereach: Me did not confirm %s absent", c.LANName())
	}
	return nil
}

// SetChallenge publishes one DNS-01 value for a name under the base.
func (c Client) SetChallenge(ctx context.Context, identifier, value string) error {
	return c.do(ctx, http.MethodPut, "/acme-challenges", map[string]string{"identifier": identifier, "value": value}, nil)
}

// RemoveChallenge removes one DNS-01 value; Me proves it absent.
func (c Client) RemoveChallenge(ctx context.Context, identifier, value string) error {
	return c.do(ctx, http.MethodDelete, "/acme-challenges", map[string]string{"identifier": identifier, "value": value}, nil)
}

func (c Client) checkView(view TunnelView) error {
	if view.Base != c.Registration.Base || view.IngressService != RemoteListenerIngress || !view.IngressCurrent {
		return errors.New("mereach: Me's tunnel does not lead exactly to this base's remote listener")
	}
	return nil
}

func writePrivate(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".kombify-me-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(raw)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
