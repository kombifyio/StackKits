package identity

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The host-side identity signer (homelab identity target §4, §9; StackKits
// enpy). It runs on the Authority's host with the Owner custody and is the
// only party that uses the Owner key, the step-ca root and the Pocket ID admin
// key for the Basement core: it keeps the Owner-signed Home trust, the public
// Authority record, the recovery notice projection and the household groups
// current, and answers exactly two requests from the core over a Unix socket
// that only the core server's account may use:
//
//	POST /v1/listener-certificate   a CSR for the LAN enrollment listener's own
//	                                names only; answer: the verified chain (PEM)
//	POST <recovery routes>          the Owner device recovery routes, which
//	                                authenticate the device proof and the code
//	                                themselves and mint the recovery link
//
// It never signs data the core chooses and never returns a key.
const (
	HomeSignerListenerCertificatePath = "/v1/listener-certificate"

	homeSignerMaxCSRBytes          = 8 << 10
	homeSignerListenerLimit        = 24
	homeSignerListenerWindow       = time.Hour
	homeSignerAuthorityInterval    = 10 * time.Minute
	homeSignerReadHeaderTimeout    = 5 * time.Second
	homeSignerRequestTimeout       = 45 * time.Second
	homeSignerRecoveryMaxBodyBytes = recoveryMaxBodyBytes
)

// HomeAuthoritySigner is the core's view of the host-side signer.
type HomeAuthoritySigner interface {
	// IssueListenerCertificate has step-ca sign a certificate request for the
	// LAN enrollment listener and returns the verified chain (PEM).
	IssueListenerCertificate(ctx context.Context, csrDER []byte) ([]byte, error)
	// RecoveryHandler serves the Owner device recovery routes at the signer.
	RecoveryHandler() http.Handler
}

// ServeHomeAuthoritySigner runs the signer until ctx ends: the background
// duties and the socket. peerUID is the only account that may call it (the
// Basement core server's); root is the Authority's workspace with the Owner
// custody.
func ServeHomeAuthoritySigner(ctx context.Context, root, socketPath string, peerUID int) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if _, err := localevidence.LoadOwnerCustody(absolute); err != nil {
		return errors.New("identity: the identity signer runs only where the Owner custody is (the Authority's host): " + err.Error())
	}
	listener, err := listenHomeSignerSocket(socketPath, peerUID)
	if err != nil {
		return err
	}
	go keepHomeAuthorityPublicCurrent(ctx, absolute)
	go KeepHomePublicTrustCurrent(ctx, absolute)
	go KeepHomeEnrollmentTrustCurrent(ctx, absolute)
	go keepHomeAccountGroupsCurrent(ctx, absolute)
	go keepHomeLANCertificateCurrent(ctx, absolute)
	go keepHomePublicCertificateCurrent(ctx, absolute)
	if err := SyncRecoveryNoticeProjection(absolute); err != nil {
		slog.Warn("recovery notices were not projected", "error", err)
	}
	server := &http.Server{
		Handler: homeSignerHandler(absolute), ReadHeaderTimeout: homeSignerReadHeaderTimeout,
		ReadTimeout: homeSignerRequestTimeout, WriteTimeout: homeSignerRequestTimeout, MaxHeaderBytes: 16 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("identity signer listening", "socket", socketPath, "peerUid", peerUID)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// listenHomeSignerSocket replaces a stale socket and opens a new one with
// mode 0600 in a 0700 directory of this process's account (the core server's).
func listenHomeSignerSocket(socketPath string, peerUID int) (net.Listener, error) {
	if !filepath.IsAbs(socketPath) || peerUID < 0 {
		return nil, errors.New("identity: the signer needs an absolute socket path and the core server's uid")
	}
	directory := filepath.Dir(socketPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	// Nobody else traverses the directory, also while the socket is created.
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("identity: the signer socket path holds something that is not a socket")
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, err
		}
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	// The signer runs as the core server's account (the unit's User=), so the
	// socket and its directory are that account's; nobody else reaches them.
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return &homeSignerListener{Listener: listener, peerUID: peerUID}, nil
}

// homeSignerHandler is the signer's whole request surface.
func homeSignerHandler(root string) http.Handler {
	mux := http.NewServeMux()
	limiter := &homeSignerLimiter{}
	mux.HandleFunc("POST "+HomeSignerListenerCertificatePath, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, homeSignerMaxCSRBytes+1))
		if err != nil || len(raw) == 0 || len(raw) > homeSignerMaxCSRBytes {
			http.Error(w, "one certificate request (DER) is required", http.StatusBadRequest)
			return
		}
		if !limiter.allow(time.Now()) {
			http.Error(w, "too many listener certificates this hour", http.StatusTooManyRequests)
			return
		}
		chain, err := localevidence.IssueHomeEnrollmentListenerCertificate(r.Context(), root, raw)
		if err != nil {
			slog.Warn("listener certificate refused", "error", err)
			http.Error(w, "listener certificate refused", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		_, _ = w.Write(chain)
	})
	recovery := RecoveryDeviceHandler(root, LocalRecoveryPasskeyIssuer(root))
	for _, route := range []string{RecoveryRequestPath, RecoveryCompletePath, RecoveryCancelPath, RecoveryOwnerDevicePath} {
		mux.Handle("POST "+route, recovery)
	}
	// The device notices are the signer's too: the pin that addresses them is
	// host custody the core cannot read.
	mux.Handle("GET "+RecoveryDeviceNoticesPath, recovery)
	return mux
}

// keepHomeAuthorityPublicCurrent rewrites the public Authority record (only
// when it changed) at start and every homeSignerAuthorityInterval, so a
// rebinding or a domain migration reaches the core without an Apply.
func keepHomeAuthorityPublicCurrent(ctx context.Context, root string) {
	for {
		if _, err := localevidence.WriteHomeAuthorityPublic(root, time.Now()); err != nil {
			slog.Warn("the public Authority record was not written", "error", err)
		}
		if err := SyncHomeVPNAnnouncement(root); err != nil {
			slog.Warn("the VPN add-on announcement was not projected", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(homeSignerAuthorityInterval):
		}
	}
}

type homeSignerLimiter struct {
	mu     sync.Mutex
	issued []time.Time
}

func (l *homeSignerLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.issued[:0]
	for _, at := range l.issued {
		if now.Sub(at) < homeSignerListenerWindow {
			kept = append(kept, at)
		}
	}
	l.issued = kept
	if len(l.issued) >= homeSignerListenerLimit {
		return false
	}
	l.issued = append(l.issued, now)
	return true
}

// homeSignerClient calls the signer over its socket.
type homeSignerClient struct {
	socket string
	client *http.Client
}

// NewHomeAuthoritySignerClient is the core's client of the host-side signer.
func NewHomeAuthoritySignerClient(socketPath string) HomeAuthoritySigner {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &homeSignerClient{socket: socketPath, client: &http.Client{
		Timeout: homeSignerRequestTimeout,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		}, MaxIdleConns: 2},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *homeSignerClient) IssueListenerCertificate(ctx context.Context, csrDER []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://identity-signer"+HomeSignerListenerCertificatePath, bytes.NewReader(csrDER))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/pkcs10")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, errors.New("identity: the host-side identity signer is unreachable: " + err.Error())
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, errors.New("identity: the host-side identity signer refused the listener certificate")
	}
	return raw, nil
}

// RecoveryHandler forwards one recovery request: its method, path, DPoP proof
// and body, nothing else; the signer authenticates it.
func (c *homeSignerClient) RecoveryHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, homeSignerRecoveryMaxBodyBytes+1))
		if err != nil || len(body) > homeSignerRecoveryMaxBodyBytes {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_recovery_request", false)
			return
		}
		forward, err := http.NewRequestWithContext(r.Context(), r.Method, "http://identity-signer"+r.URL.Path, bytes.NewReader(body))
		if err != nil {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_recovery_request", false)
			return
		}
		for _, value := range r.Header.Values("DPoP") {
			forward.Header.Add("DPoP", value)
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "" {
			forward.Header.Set("Content-Type", contentType)
		}
		response, err := c.client.Do(forward)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "recovery_unavailable", true)
			return
		}
		defer func() { _ = response.Body.Close() }()
		answer, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "recovery_unavailable", true)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(answer)
	})
}
