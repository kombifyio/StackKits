package localorigin

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// NewServer constructs the dedicated origin-only TLS listener in the existing
// StackKits process. No management route or caller-selected upstream is exposed.
// It is disabled unless the process explicitly supplies a loopback listen socket.
func NewServer(workspaceRoot, address string) (*http.Server, error) {
	if err := validateLoopbackAddress(address); err != nil {
		return nil, err
	}
	if _, err := localevidence.LoadOwnerCustody(workspaceRoot); err != nil {
		return nil, err
	}
	if err := writeState(workspaceRoot, stateRef("listener", "origin"), "listener", address); err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert, SessionTicketsDisabled: true, NextProtos: []string{"http/1.1"}}
	tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		_, certificate, err := loadPublication(workspaceRoot, hello.ServerName)
		return &certificate, err
	}
	return &http.Server{Addr: address, TLSConfig: tlsConfig, Handler: originHandler(workspaceRoot), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

func originHandler(workspaceRoot string) http.Handler {
	return originHandlerWithDial(workspaceRoot, func(ctx context.Context, backend Backend) (net.Conn, error) {
		if err := verifyBackendSocket(ctx, backend); err != nil {
			return nil, err
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, "tcp", backend.Address)
	})
}

func originHandlerWithDial(workspaceRoot string, dial func(context.Context, Backend) (net.Conn, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version < tls.VersionTLS13 || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "workload TLS proof required", http.StatusUnauthorized)
			return
		}
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		if host != r.TLS.ServerName {
			http.Error(w, "origin identity mismatch", http.StatusMisdirectedRequest)
			return
		}
		publication, _, err := loadPublication(workspaceRoot, r.TLS.ServerName)
		if err != nil {
			http.Error(w, "origin unavailable", http.StatusServiceUnavailable)
			return
		}
		policy := publication.Policy
		if r.URL.Path == localevidence.OriginProofPath {
			serveProof(w, r, workspaceRoot, publication, dial)
			return
		}
		if _, err := localevidence.AuthorizeWorkloadPeer(workspaceRoot, r.TLS.PeerCertificates, localevidence.WorkloadPeerScope{ServiceRef: policy.ServiceRef, EdgeSiteRef: policy.EdgeSiteRef, Audience: policy.Audience}, time.Now().UTC()); err != nil {
			http.Error(w, "workload peer denied", http.StatusForbidden)
			return
		}
		// Only application WebSockets may upgrade. CONNECT and arbitrary protocol
		// tunnels have no authority in the declared HTTP origin contract.
		upgrade := r.Header.Get("Upgrade")
		if r.Method == http.MethodConnect || (upgrade != "" && (r.Method != http.MethodGet || !strings.EqualFold(upgrade, "websocket"))) {
			http.Error(w, "unbounded tunnel unavailable", http.StatusNotImplemented)
			return
		}
		backend, err := loadBackend(workspaceRoot, policy)
		if err != nil {
			http.Error(w, "origin backend unavailable", http.StatusServiceUnavailable)
			return
		}
		budget := time.Duration(policy.RevocationMaxStalenessSeconds) * time.Second
		if budget <= 0 || budget > time.Minute {
			budget = time.Minute
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		deadline := time.Now().Add(budget)
		for _, certificate := range r.TLS.PeerCertificates {
			if certificate.NotAfter.Before(deadline) {
				deadline = certificate.NotAfter
			}
		}
		if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil {
			http.Error(w, "bounded origin response unavailable", http.StatusServiceUnavailable)
			return
		}
		// A new backend connection verifies the exact observed container still owns
		// the loopback mapping. Reused host ports can never redirect a publication.
		transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: budget, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			connection, err := dial(ctx, backend)
			if err != nil {
				return nil, err
			}
			// ReverseProxy hijacks the downstream for a 101 response: context
			// cancellation alone cannot bound its bidirectional copy. A deadline on
			// the pinned upstream ends both copies, including an idle WebSocket.
			// Reconnection rechecks current publication and Owner admission; removal
			// and revocation therefore take effect within the declared stale budget.
			if err := connection.SetDeadline(deadline); err != nil {
				connection.Close()
				return nil, err
			}
			return connection, nil
		}}
		defer transport.CloseIdleConnections()
		target := &url.URL{Scheme: "http", Host: backend.Address}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = transport
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "origin backend unavailable", http.StatusBadGateway)
		}
		original := proxy.Director
		proxy.Director = func(request *http.Request) {
			original(request)
			// Workload possession never becomes a forwarded human/Owner identity.
			for key := range request.Header {
				lower := strings.ToLower(key)
				if originProxyIdentityHeader(lower) {
					request.Header.Del(key)
				}
			}
			request.Header["X-Forwarded-For"] = nil
		}
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

func originProxyIdentityHeader(name string) bool {
	if strings.HasPrefix(name, "x-forwarded-") || strings.HasPrefix(name, "x-auth-") || strings.HasPrefix(name, "remote-") || strings.HasPrefix(name, "x-remote-") || strings.HasPrefix(name, "x-user-") || strings.HasPrefix(name, "x-webauth-") || strings.HasPrefix(name, "x-ssl-client-") || strings.HasPrefix(name, "ssl_client_") {
		return true
	}
	switch name {
	case "forwarded", "x-real-ip", "true-client-ip", "x-email", "x-user", "x-name", "x-group", "x-groups", "x-role", "x-roles", "x-org-id", "x-kombify-service-auth", "x-client-cert", "x-original-url", "x-rewrite-url":
		return true
	default:
		return false
	}
}

func verifyBackendSocket(ctx context.Context, backend Backend) error {
	if !validContainerID(backend.ContainerID) {
		return errors.New("localorigin: invalid attested container")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "inspect", backend.ContainerID)
	output := &boundedOutput{remaining: 256 << 10}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return errors.New("localorigin: current container observation failed")
	}
	var values []struct {
		ID    string `json:"Id"`
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	if err := json.Unmarshal(output.Bytes(), &values); err != nil || len(values) != 1 || values[0].ID != backend.ContainerID || !values[0].State.Running {
		return errors.New("localorigin: attested container is no longer running")
	}
	ports := values[0].NetworkSettings.Ports[strconv.Itoa(backend.TargetPort)+"/tcp"]
	if len(ports) != 1 || net.JoinHostPort(ports[0].HostIP, ports[0].HostPort) != backend.Address {
		return errors.New("localorigin: application socket mapping changed")
	}
	return nil
}

type boundedOutput struct {
	bytes.Buffer
	remaining int
}

func (b *boundedOutput) Write(value []byte) (int, error) {
	if len(value) > b.remaining {
		return 0, errors.New("localorigin: oversized runtime observation")
	}
	b.remaining -= len(value)
	return b.Buffer.Write(value)
}
