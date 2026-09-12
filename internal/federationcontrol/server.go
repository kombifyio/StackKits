package federationcontrol

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
)

const ActionPath = "/api/v1/federation/control/actions"
const StatusPath = "/api/v1/federation/control/status"

var errActionInterrupted = errors.New("control: action claim is interrupted")

type Denial struct {
	ErrorCode    string `json:"error_code"`
	ReasonCode   string `json:"reason_code"`
	Retryable    bool   `json:"retryable"`
	UserGuidance string `json:"user_guidance"`
}

func (d *Denial) Error() string { return d.ErrorCode + ": " + d.ReasonCode }
func writeDenial(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Denial{ErrorCode: "federation_action_denied", ReasonCode: reason, Retryable: false, UserGuidance: "Inspect current local receiver custody and the exact Home-signed action, plan, target and expiry; issue a new authorized action after correcting the denial."})
}

// NewServer is mounted by the existing stackkit-server process. It is a Cloud
// receiver for Home-initiated requests, not a Cloud-to-Home management tunnel.
func NewServer(root, address string) (*http.Server, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return nil, errors.New("control: explicit local listener IP/socket required")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// Expired or withdrawn trust is a normal persistent state. Keep the main
	// API alive on restart; current custody is required at every handshake.
	return &http.Server{Addr: address, Handler: Handler(root), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return receiverTLS(root) }}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

func receiverTLS(root string) (*tls.Config, error) {
	c, err := loadCustody(root)
	if err != nil {
		return nil, err
	}
	cert, err := readPrivate(root, c.CertificatePath, 64<<10)
	if err != nil {
		return nil, err
	}
	key, err := readPrivate(root, c.PrivateKeyPath, 64<<10)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(c.Trust.RootCertificatePEM)) {
		return nil, errors.New("control: invalid admitted Home certificate root")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		intermediates.AddCert(cert)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: c.ServerName, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, NextProtos: []string{"http/1.1"}}, nil
}

func authorizeTLS(c ReceiverCustody, state *tls.ConnectionState) error {
	if state == nil || state.Version < tls.VersionTLS13 || len(state.PeerCertificates) == 0 || hashBytes(state.PeerCertificates[0].Raw) != c.Trust.ClientCertificateSHA256 {
		return errors.New("control: exact admitted Home mTLS peer required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(c.Trust.RootCertificatePEM)) {
		return errors.New("control: admitted Home roots unavailable")
	}
	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return err
}

type actionRecord struct {
	Digest         string  `json:"digest"`
	IdempotencyKey string  `json:"idempotencyKey"`
	Nonce          string  `json:"nonce"`
	Result         *Result `json:"result,omitempty"`
}

// Handler never substitutes forwarded certificates or ordinary API credentials
// for live TLS proof. Its journal is durable before dispatch and survives a
// server restart; an interrupted command remains consumed and fails closed.
func Handler(root string) http.Handler {
	absoluteRoot, rootErr := filepath.Abs(root)
	root = absoluteRoot
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rootErr != nil {
			writeDenial(w, http.StatusServiceUnavailable, "receiver_workspace_unavailable")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		c, err := loadCustody(root)
		if err != nil || authorizeTLS(c, r.TLS) != nil {
			writeDenial(w, http.StatusForbidden, "home_peer_or_receiver_authority_unavailable")
			return
		}
		if r.URL.Path == StatusPath && r.Method == http.MethodGet {
			writeJSON(w, map[string]any{"schema": "stackkit.federation-control-status/v1", "active": true, "siteRef": c.Policy.SiteRef, "nodeRef": c.Policy.NodeRef, "planHash": c.PlanHash, "receiveDirection": "home-to-cloud", "mutateCapabilities": "unavailable", "runtimeReadiness": "not-evaluated", "validUntil": c.Trust.ValidUntil})
			return
		}
		if r.URL.Path != ActionPath || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
		if err != nil {
			writeDenial(w, http.StatusBadRequest, "invalid_action_envelope")
			return
		}
		a, err := DecodeAction(raw)
		if err != nil {
			writeDenial(w, http.StatusBadRequest, "invalid_action_envelope")
			return
		}
		if a.Action == "apply" || a.Action == "destroy" {
			writeDenial(w, http.StatusForbidden, "mutation_capability_unavailable")
			return
		}
		var result Result
		err = lifecyclemutation.WithIdleMutation(root, lifecyclemutation.JoinRequest{}, func() error {
			current, err := loadCustody(root)
			if err != nil {
				return err
			}
			if err := authorizeTLS(current, r.TLS); err != nil {
				return err
			}
			if a.PlanHash != current.PlanHash || a.TargetSiteRef != current.Policy.SiteRef || a.TargetNodeRef != current.Policy.NodeRef || a.ExecutionChannelRef != current.Policy.ExecutionChannelRef {
				return errors.New("control: target/plan mismatch")
			}
			matched := false
			for _, contract := range current.Policy.Actions {
				if contract.ID == a.Action {
					if err := authorizeAction(a, current.Trust, contract, time.Now().UTC()); err != nil {
						return err
					}
					matched = true
				}
			}
			if !matched {
				return errors.New("control: unavailable action")
			}
			if _, err := verifyPlanAuthority(root, current, a); err != nil {
				return err
			}
			idPath := ".stackkit/custody/federation-control/actions/" + strings.TrimPrefix(hashBytes([]byte(a.IdempotencyKey)), "sha256:") + ".json"
			noncePath := ".stackkit/custody/federation-control/nonces/" + a.Nonce + ".json"
			var prior actionRecord
			if err := readState(root, idPath, &prior); err == nil {
				if prior.Digest != a.Digest() || prior.Nonce != a.Nonce {
					return errors.New("control: idempotency conflict")
				}
				if prior.Result == nil {
					return errActionInterrupted
				}
				result = *prior.Result
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := readState(root, noncePath, &prior); err == nil {
				return errors.New("control: nonce already consumed")
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			record := actionRecord{Digest: a.Digest(), IdempotencyKey: a.IdempotencyKey, Nonce: a.Nonce}
			if err := writeState(root, noncePath, record); err != nil {
				return err
			}
			if err := writeState(root, idPath, record); err != nil {
				return err
			}
			result, err = dispatch(r.Context(), root, current, a)
			if err != nil {
				return err
			}
			// Every response remains conditional on present local trust, not the
			// handshake's historical admission or a previous successful action.
			latest, err := loadCustody(root)
			if err != nil {
				return err
			}
			if err := authorizeTLS(latest, r.TLS); err != nil {
				return err
			}
			record.Result = &result
			return writeState(root, idPath, record)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errActionInterrupted) {
				writeDenial(w, http.StatusConflict, "action_interrupted")
				return
			}
			writeDenial(w, http.StatusForbidden, "action_authority_or_current_plan_unavailable")
			return
		}
		writeJSON(w, result)
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
