package cookiescrubber

import (
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/auth/accessgate"
)

// DefaultTinyAuthCookieNames are the default exact TinyAuth session cookie
// names scrubbed on every request and protected against downstream overwrites.
var DefaultTinyAuthCookieNames = []string{
	"tinyauth",
	"tinyauth_session",
}

// DefaultTinyAuthCookiePrefixes are the case-insensitive cookie name prefixes
// scrubbed in both directions. Pinned TinyAuth v5.0.7 uses
// `tinyauth-session-<instance suffix>` as well as `tinyauth-oauth-*` and
// `tinyauth-csrf-*` cookies.
var DefaultTinyAuthCookiePrefixes = []string{
	"tinyauth-",
}

// DefaultHealthPath is the optional local liveness probe path served by the
// sidecar without contacting the downstream Coolify proxy.
const DefaultHealthPath = "/.well-known/stackkit/cookie-scrubber/healthz"

// Config configures the isolated zero-custody cookie-scrubber reverse proxy.
// It carries no custody paths, no signer socket, no identity verifier, and no
// MCP credentials.
type Config struct {
	// UpstreamURL is Coolify's internal HTTP proxy endpoint (e.g.,
	// http://coolify-proxy:80 on coolify-edge or http://<mesh-ip>:8088 on an
	// isolated Extension VM).
	UpstreamURL string

	// TinyAuthCookieNames lists additional operator-configured TinyAuth cookie
	// names to scrub alongside DefaultTinyAuthCookieNames and Access Gate
	// cookies.
	TinyAuthCookieNames []string

	// Transport overrides the HTTP round tripper used to reach UpstreamURL.
	Transport http.RoundTripper

	// HealthPath, when non-empty, answers GET/HEAD locally with 200 OK instead
	// of forwarding to the upstream proxy.
	HealthPath string
}

// Scrubber is an HTTP reverse proxy sitting between Kit-Traefik (after
// ForwardAuth succeeds) and Coolify's internal HTTP proxy. It strips StackKits
// Access Gate and TinyAuth cookies upstream and prevents downstream workloads
// from tossing cross-subdomain cookies (Domain=) or overwriting gate/TinyAuth
// cookies via Set-Cookie.
type Scrubber struct {
	upstream          *url.URL
	forbidden         map[string]struct{}
	forbiddenL        []string
	forbiddenPrefixes []string
	healthPath        string
	proxy             *httputil.ReverseProxy
}

// ValidateUpstreamURL validates and parses a cookie-scrubber upstream URL
// fail-closed. It rejects missing hosts, non-HTTP(S) schemes, embedded
// userinfo credentials, query strings (RawQuery or ForceQuery), and fragments
// so credentials can never enter startup logs or proxied requests.
func ValidateUpstreamURL(raw string) (*url.URL, error) {
	upstreamRaw := strings.TrimSpace(raw)
	if upstreamRaw == "" {
		return nil, errors.New("cookiescrubber: upstream URL is required")
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		return nil, errors.New("cookiescrubber: invalid upstream URL")
	}
	if (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" {
		return nil, errors.New("cookiescrubber: upstream URL must use http or https with a host")
	}
	if upstream.User != nil || upstream.RawQuery != "" || upstream.ForceQuery || upstream.Fragment != "" {
		return nil, errors.New("cookiescrubber: upstream URL must not carry credentials, query string, or fragment")
	}
	return upstream, nil
}

// ParseTinyAuthCookieNames splits a comma-separated list of cookie names,
// trimming whitespace and removing empty/duplicate entries.
func ParseTinyAuthCookieNames(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var names []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
}

// New validates cfg and constructs a zero-custody cookie-scrubber proxy.
func New(cfg Config) (*Scrubber, error) {
	upstream, err := ValidateUpstreamURL(cfg.UpstreamURL)
	if err != nil {
		return nil, err
	}

	forbidden := make(map[string]struct{})
	addForbidden := func(name string) {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return
		}
		forbidden[strings.ToLower(trimmed)] = struct{}{}
	}
	addForbidden(accessgate.SessionCookie)
	addForbidden(accessgate.LaunchStateCookie)
	for _, name := range DefaultTinyAuthCookieNames {
		addForbidden(name)
	}
	for _, name := range cfg.TinyAuthCookieNames {
		addForbidden(name)
	}

	forbiddenList := make([]string, 0, len(forbidden))
	for name := range forbidden {
		forbiddenList = append(forbiddenList, name)
	}
	slices.Sort(forbiddenList)

	prefixSet := make(map[string]struct{}, len(DefaultTinyAuthCookiePrefixes))
	var forbiddenPrefixes []string
	for _, prefix := range DefaultTinyAuthCookiePrefixes {
		p := strings.ToLower(strings.TrimSpace(prefix))
		if p == "" {
			continue
		}
		if _, dup := prefixSet[p]; dup {
			continue
		}
		prefixSet[p] = struct{}{}
		forbiddenPrefixes = append(forbiddenPrefixes, p)
	}
	slices.Sort(forbiddenPrefixes)

	s := &Scrubber{
		upstream:          upstream,
		forbidden:         forbidden,
		forbiddenL:        forbiddenList,
		forbiddenPrefixes: forbiddenPrefixes,
		healthPath:        strings.TrimSpace(cfg.HealthPath),
	}

	transport := cfg.Transport
	if transport == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if ok {
			transport = base.Clone()
		} else {
			transport = http.DefaultTransport
		}
	}

	s.proxy = &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			origHost := pr.In.Host
			origXFF := slices.Clone(pr.In.Header.Values("X-Forwarded-For"))
			origXFP := pr.In.Header.Get("X-Forwarded-Proto")
			origXFH := pr.In.Header.Get("X-Forwarded-Host")
			origXFPort := pr.In.Header.Get("X-Forwarded-Port")

			pr.SetURL(s.upstream)
			if origHost != "" {
				// Preserve the user-facing FQDN so Coolify's internal HTTP
				// Traefik routes on the app's Host rule.
				pr.Out.Host = origHost
			}

			// Restore TLS-terminated forwarding metadata from Kit-Traefik
			// instead of overwriting X-Forwarded-Proto with internal HTTP.
			if len(origXFF) > 0 {
				pr.Out.Header["X-Forwarded-For"] = origXFF
			}
			if clientIP, _, splitErr := net.SplitHostPort(pr.In.RemoteAddr); splitErr == nil && clientIP != "" {
				if prior := strings.Join(pr.Out.Header.Values("X-Forwarded-For"), ", "); prior != "" {
					pr.Out.Header.Set("X-Forwarded-For", prior+", "+clientIP)
				} else {
					pr.Out.Header.Set("X-Forwarded-For", clientIP)
				}
			}
			if origXFP != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", origXFP)
			} else if pr.In.TLS != nil {
				pr.Out.Header.Set("X-Forwarded-Proto", "https")
			} else {
				pr.Out.Header.Set("X-Forwarded-Proto", "http")
			}
			if origXFH != "" {
				pr.Out.Header.Set("X-Forwarded-Host", origXFH)
			} else if origHost != "" {
				pr.Out.Header.Set("X-Forwarded-Host", origHost)
			}
			if origXFPort != "" {
				pr.Out.Header.Set("X-Forwarded-Port", origXFPort)
			}

			s.ScrubRequestCookies(pr.Out.Header)
		},
		ModifyResponse: func(resp *http.Response) error {
			s.ScrubResponseSetCookies(resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}

	return s, nil
}

// NewServer constructs an HTTP server wrapping a zero-custody Scrubber.
func NewServer(listen string, cfg Config) (*http.Server, error) {
	addr := strings.TrimSpace(listen)
	if addr == "" {
		return nil, errors.New("cookiescrubber: listen address is required")
	}
	scrubber, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              addr,
		Handler:           scrubber,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    256 << 10,
	}, nil
}

// ForbiddenCookieNames returns the sorted lowercase list of exact cookie names
// scrubbed upstream and blocked from downstream Set-Cookie responses.
func (s *Scrubber) ForbiddenCookieNames() []string {
	return slices.Clone(s.forbiddenL)
}

// ForbiddenCookiePrefixes returns the sorted lowercase list of cookie name
// prefixes scrubbed upstream and blocked from downstream Set-Cookie responses.
func (s *Scrubber) ForbiddenCookiePrefixes() []string {
	return slices.Clone(s.forbiddenPrefixes)
}

// ServeHTTP handles incoming HTTP requests from Kit-Traefik.
func (s *Scrubber) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.healthPath != "" && r.URL.Path == s.healthPath && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("ok\n"))
		}
		return
	}
	s.proxy.ServeHTTP(w, r)
}

// ScrubRequestCookies strips Access Gate and TinyAuth cookies (exact names and
// case-insensitive `tinyauth-` prefix family) from upstream Cookie headers
// while preserving unrelated user-application cookies verbatim.
func (s *Scrubber) ScrubRequestCookies(header http.Header) {
	rawValues := header.Values("Cookie")
	if len(rawValues) == 0 {
		return
	}
	var kept []string
	for _, raw := range rawValues {
		for _, segment := range strings.Split(raw, ";") {
			pair := strings.TrimSpace(segment)
			if pair == "" || hasControlOrNonASCII(pair) {
				continue
			}
			name, _, ok := strings.Cut(pair, "=")
			if !ok {
				// Drop malformed cookie fragments fail-closed.
				continue
			}
			name = strings.TrimSpace(name)
			if !isValidHTTPToken(name) || s.isForbiddenCookieName(name) {
				continue
			}
			kept = append(kept, pair)
		}
	}
	if len(kept) == 0 {
		header.Del("Cookie")
		return
	}
	header.Set("Cookie", strings.Join(kept, "; "))
}

// ScrubResponseSetCookies removes any downstream Set-Cookie header that:
//   - fails strict RFC 6265 / http.ParseSetCookie validation (dropped fail-closed),
//   - sets a Domain attribute (preventing cross-app / cross-install cookie
//     tossing without a Public Suffix List), or
//   - attempts to set or overwrite an Access Gate or TinyAuth cookie (exact
//     name or case-insensitive `tinyauth-` prefix family).
func (s *Scrubber) ScrubResponseSetCookies(header http.Header) {
	rawValues := header.Values("Set-Cookie")
	if len(rawValues) == 0 {
		return
	}
	var kept []string
	for _, raw := range rawValues {
		if s.allowSetCookie(raw) {
			kept = append(kept, raw)
		}
	}
	if len(kept) == 0 {
		header.Del("Set-Cookie")
		return
	}
	header["Set-Cookie"] = kept
}

func (s *Scrubber) allowSetCookie(raw string) bool {
	if strings.TrimSpace(raw) == "" || hasControlOrNonASCII(raw) {
		return false
	}
	cookie, err := http.ParseSetCookie(raw)
	if err != nil || cookie == nil {
		return false
	}
	if !isValidHTTPToken(cookie.Name) || s.isForbiddenCookieName(cookie.Name) {
		return false
	}
	if strings.TrimSpace(cookie.Domain) != "" {
		return false
	}

	parts := strings.Split(raw, ";")
	if len(parts) == 0 {
		return false
	}
	namePart, _, hasEq := strings.Cut(strings.TrimSpace(parts[0]), "=")
	if !hasEq || strings.TrimSpace(namePart) != cookie.Name {
		return false
	}

	for _, attr := range parts[1:] {
		trimmed := strings.TrimSpace(attr)
		if trimmed == "" {
			// Empty attribute segment (e.g. `;;` or trailing `;`) is malformed.
			return false
		}
		attrName, attrVal, attrHasEq := strings.Cut(trimmed, "=")
		attrName = strings.TrimSpace(attrName)
		if !isValidHTTPToken(attrName) {
			return false
		}
		valTrimmed := strings.TrimSpace(attrVal)
		switch strings.ToLower(attrName) {
		case "domain":
			// Always reject Domain attribute regardless of value or syntax.
			return false
		case "secure", "httponly", "partitioned":
			if attrHasEq {
				return false
			}
		case "path":
			if !attrHasEq || valTrimmed == "" {
				return false
			}
		case "max-age":
			if !attrHasEq || !isValidCookieMaxAge(valTrimmed) {
				return false
			}
		case "expires":
			if !attrHasEq || valTrimmed == "" || cookie.Expires.IsZero() {
				return false
			}
		case "samesite":
			if !attrHasEq {
				return false
			}
			switch strings.ToLower(valTrimmed) {
			case "strict", "lax", "none":
			default:
				return false
			}
		case "priority":
			if !attrHasEq {
				return false
			}
			switch strings.ToLower(valTrimmed) {
			case "low", "medium", "high":
			default:
				return false
			}
		default:
			// Drop unrecognized/malformed Set-Cookie attributes fail-closed.
			return false
		}
	}
	return true
}

func (s *Scrubber) isForbiddenCookieName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if _, blocked := s.forbidden[lower]; blocked {
		return true
	}
	for _, prefix := range s.forbiddenPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func hasControlOrNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] >= 0x7f {
			return true
		}
	}
	return false
}

// isValidCookieMaxAge enforces RFC 6265 §5.2.2 Max-Age syntax: optional
// leading '-' followed by one or more ASCII digits 0-9 (never leading '+').
func isValidCookieMaxAge(s string) bool {
	if s == "" {
		return false
	}
	digits := s
	if digits[0] == '-' {
		digits = digits[1:]
		if digits == "" {
			return false
		}
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

// isValidHTTPToken enforces RFC 7230 / RFC 6265 token syntax for cookie and
// attribute names.
func isValidHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}
