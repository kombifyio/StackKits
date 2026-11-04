package identity

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localowner"
)

// The Homelab Hub (homelab identity target §6.1, §6.3, D-2026-11-02-10) is
// the device client of everyone without the kombify app: an installable PWA
// on the Hub origin (https://base.<domain>) that holds a WebCrypto
// non-extractable device key, enrolls it with the person's own passkey,
// renews its device credential with DPoP proofs, lists and revokes the
// person's devices, confirms away enrollments and receives notices by Web
// Push. Its routes are a sign-in surface: the shell grants nothing, and every
// identity route authenticates the device proof or the passkey sign-in itself.
const (
	// HomeHubPathPrefix is the Hub's path on the Hub origin. The device
	// sign-in client's redirect (localowner.HomeDeviceCallbackPath) is inside
	// it, so the installed app's scope covers its own sign-in.
	HomeHubPathPrefix = "/device/"
	// The configuration and the code exchange are identity routes, so an
	// installed Hub works away while its shell installs and updates only from
	// the LAN listener.
	homeHubConfigPath = "/api/v1/identity/hub/config"
	homeHubTokenPath  = "/api/v1/identity/hub/token"
	// homeAccessListenerHeader is the listener class the Access Gate sets for
	// the backend (the router removes any client copy).
	homeAccessListenerHeader = "Stackkit-Access-Listener"

	homeHubMaxBodyBytes = 16 << 10
)

//go:embed hub
var homeHubAssets embed.FS

// homeHubConfig is what the Hub needs before the person signs in. It holds no
// secret.
type homeHubConfig struct {
	Issuer           string `json:"issuer"`
	ClientID         string `json:"clientId"`
	RedirectURI      string `json:"redirectUri"`
	HubOrigin        string `json:"hubOrigin"`
	HomeEnrollment   string `json:"homeEnrollmentUrl,omitempty"`
	AwayEnrollment   string `json:"awayEnrollmentUrl"`
	VAPIDPublicKey   string `json:"vapidPublicKey,omitempty"`
	CredentialWindow int    `json:"credentialSeconds"`
	// LaunchHosts are the only hosts the Hub signs a launch proof for.
	LaunchHosts []string `json:"launchHosts"`
	// AppHosts are the hosts the Owner opened to app passes away.
	AppHosts []string `json:"appHosts"`
}

// NewHomeHubServer is the Hub listener of the Authority: the Hub PWA and the
// device-facing identity routes, behind the core routers on the Hub host. It
// carries no node API key, MCP or lifecycle route.
func NewHomeHubServer(ctx context.Context, root, listen string, options HomeHubOptions) (*http.Server, error) {
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return nil, errors.New("identity: the Hub listener needs host:port")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	RegisterHomeHubRoutes(mux, absolute, options)
	go deliverHomePushNotices(ctx, absolute)
	return &http.Server{
		Addr: listen, Handler: homeHubHost(absolute, mux),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}, nil
}

// HomeHubOptions wire the Hub to what its process does not hold itself.
type HomeHubOptions struct {
	// Signer is the host-side identity signer. Without it the recovery routes
	// answer recovery_unavailable.
	Signer HomeAuthoritySigner
	// AppHosts are the hosts with a route the Owner opened to app passes away
	// (the Access Gate's table); the Hub issues app passes for these only.
	AppHosts []string
	// LaunchHosts are the hosts a browser session opens (user routes of the
	// Access Gate's table); the Hub signs launch proofs for these only.
	LaunchHosts []string
}

// RegisterHomeHubRoutes mounts the Hub shell, its configuration and code
// exchange, and every device-facing identity route on mux.
func RegisterHomeHubRoutes(mux *http.ServeMux, root string, options HomeHubOptions) {
	assets, _ := fs.Sub(homeHubAssets, "hub")
	mux.HandleFunc("GET "+homeHubConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		config, err := loadHomeHubConfig(root, options)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "hub_unavailable", true)
			return
		}
		writeHomeIdentityJSON(w, http.StatusOK, config)
	})
	mux.HandleFunc("POST "+homeHubTokenPath, func(w http.ResponseWriter, r *http.Request) {
		hub, ok := homeHubOrigin(root)
		if !ok || r.Header.Get("Origin") != hub {
			denyHomeIdentity(w, http.StatusForbidden, "origin_refused", false)
			return
		}
		var body struct {
			Code     string `json:"code"`
			Verifier string `json:"verifier"`
		}
		if !decodeHomeJSON(r.Body, homeHubMaxBodyBytes, &body, false) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_request", false)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		token, err := localowner.ExchangeHomeDeviceCode(ctx, root, body.Code, body.Verifier)
		if err != nil {
			denyHomeIdentity(w, http.StatusUnauthorized, "sign_in_failed", false)
			return
		}
		writeHomeIdentityJSON(w, http.StatusOK, map[string]string{"idToken": token})
	})
	mux.HandleFunc("GET "+HomeHubPathPrefix+"{file...}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		if name == "" || name == "launch" || name == strings.TrimPrefix(localowner.HomeDeviceCallbackPath, HomeHubPathPrefix) {
			name = "index.html"
		}
		raw, err := fs.ReadFile(assets, path.Clean(name))
		if err != nil || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		writeHomeHubAsset(w, root, name, raw)
	})
	RegisterHomeIdentityRoutes(mux, root)
	mux.Handle("POST "+HomeEnrollmentPath, HomeEnrollmentHandler(root))
	mux.Handle("POST "+HomeDeviceRenewalPath, HomeDeviceRenewalHandler(root))
	// Recovery and the Owner's approvals stay on the LAN listener (homelab
	// identity target §8): a readable edge never carries a recovery code, a
	// passkey registration link or a custody step-up. The recovery state and
	// the Pocket ID admin key are the host's, so the host-side signer answers
	// the recovery routes; the notices are projected here.
	var recovery http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		denyHomeIdentity(w, http.StatusServiceUnavailable, "recovery_unavailable", true)
	})
	if options.Signer != nil {
		recovery = options.Signer.RecoveryHandler()
	}
	for _, route := range []string{RecoveryRequestPath, RecoveryCompletePath, RecoveryCancelPath} {
		mux.Handle("POST "+route, homeLANListenerOnly(recovery))
	}
	mux.Handle("GET "+RecoveryDeviceNoticesPath, homeLANListenerOnly(RecoveryDeviceHandler(root, func(context.Context, string) (RecoveryPasskeyLink, error) {
		return RecoveryPasskeyLink{}, ErrRecoveryDenied
	})))
	registerHomePushRoutes(mux, root)
	registerHomeAppPassRoutes(mux, root, options.AppHosts)
	approvals := http.NewServeMux()
	registerHomeStepUpRequestRoutes(approvals, root)
	mux.Handle(HomeStepUpRequestsPath, homeLANListenerOnly(approvals))
	mux.Handle(HomeStepUpRequestsPath+"/", homeLANListenerOnly(approvals))
}

// homeLANListenerOnly serves a route only for a request the Access Gate
// admitted on the LAN listener.
func homeLANListenerOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values(homeAccessListenerHeader); len(values) != 1 || values[0] != "lan" {
			denyHomeIdentity(w, http.StatusForbidden, "lan_only", false)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loadHomeHubConfig(root string, options HomeHubOptions) (homeHubConfig, error) {
	address, err := homeIdentityAddress(root)
	if err != nil {
		return homeHubConfig{}, err
	}
	hub, ok := homeHubOrigin(root)
	if !ok {
		return homeHubConfig{}, errors.New("identity: no Hub origin")
	}
	redirect, err := localowner.HomeDeviceCallbackURL(root)
	if err != nil {
		return homeHubConfig{}, err
	}
	config := homeHubConfig{
		Issuer: address.PocketIDOrigin(), ClientID: HomeDeviceClientID, RedirectURI: redirect, HubOrigin: hub,
		AwayEnrollment: hub + HomeEnrollmentPath, CredentialWindow: int(HomeDeviceCredentialLifetime / time.Second),
	}
	if bootstrap, bootstrapErr := LoadHomeEnrollmentBootstrap(root); bootstrapErr == nil {
		config.HomeEnrollment = bootstrap.HubEnrollmentURL
	}
	config.LaunchHosts, config.AppHosts = append([]string{}, options.LaunchHosts...), append([]string{}, options.AppHosts...)
	if key, keyErr := loadHomePushVAPIDPublicKey(root); keyErr == nil {
		config.VAPIDPublicKey = key
	}
	return config, nil
}

// homeHubHost serves only the Hub host. The routers send nothing else here; a
// request naming another host never reaches the identity routes.
func homeHubHost(root string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub, ok := homeHubOrigin(root)
		host := strings.ToLower(r.Host)
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		if !ok || "https://"+host != hub {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var homeHubContentTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".webmanifest": "application/manifest+json", ".png": "image/png", ".svg": "image/svg+xml",
}

func writeHomeHubAsset(w http.ResponseWriter, root, name string, raw []byte) {
	contentType, ok := homeHubContentTypes[path.Ext(name)]
	if !ok {
		contentType = "application/octet-stream"
	}
	connect := "'self'"
	if bootstrap, err := LoadHomeEnrollmentBootstrap(root); err == nil && bootstrap.HubEnrollmentURL != "" {
		if origin, originErr := homeOriginOf(bootstrap.HubEnrollmentURL); originErr == nil {
			connect += " " + origin
		}
	}
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src "+connect+
		"; manifest-src 'self'; worker-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func homeOriginOf(raw string) (string, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || scheme != "https" {
		return "", errors.New("identity: not an https URL")
	}
	host, _, _ := strings.Cut(rest, "/")
	if host == "" {
		return "", errors.New("identity: no host")
	}
	return "https://" + host, nil
}
