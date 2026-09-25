package appsetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	roundcubeResponseBodyLimit = 1 << 20
	roundcubeSetupTimeout      = 2 * time.Minute
)

var (
	roundcubeLoginTokenPattern   = regexp.MustCompile(`name="_token" value="([A-Za-z0-9]{16,128})"`)
	roundcubeRequestTokenPattern = regexp.MustCompile(`"request_token":"([A-Za-z0-9]{16,128})"`)
	roundcubeVersionPattern      = regexp.MustCompile(`"rcversion":([0-9]{5,6})`)
	roundcubeTaskPattern         = regexp.MustCompile(`"task":"([a-z]+)"`)
	roundcubeReleasePattern      = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-[a-z0-9.-]+)?$`)
)

// RoundcubeMailboxEndpoint is one owner mail server. Security is explicit:
// "ssl" is implicit TLS, "starttls" upgrades a plain connection.
type RoundcubeMailboxEndpoint struct {
	Host     string
	Port     int
	Security string
}

// URI is the Roundcube host form stored in the owner mailbox file.
func (e RoundcubeMailboxEndpoint) URI() string {
	scheme := "ssl"
	if e.Security == "starttls" {
		scheme = "tls"
	}
	return scheme + "://" + net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// ResolveRoundcubeMailboxEndpoint validates one owner endpoint. The security
// mode is never guessed from an unusual port: it defaults only on the
// registered ports (IMAP 993/143, SMTP 465/587) and is required otherwise.
func ResolveRoundcubeMailboxEndpoint(protocol, host string, port int, security string) (RoundcubeMailboxEndpoint, error) {
	field := strings.ToLower(protocol)
	normalized, err := normalizeRoundcubeMailHost(host)
	if err != nil {
		return RoundcubeMailboxEndpoint{}, fmt.Errorf("%sHost must be the provider's %s server name, for example %s.example.com", field, protocol, field)
	}
	if port < 1 || port > 65535 {
		return RoundcubeMailboxEndpoint{}, fmt.Errorf("%sPort must be a TCP port", field)
	}
	security = strings.ToLower(strings.TrimSpace(security))
	if security == "" {
		switch {
		case (field == "imap" && port == 993) || (field == "smtp" && port == 465):
			security = "ssl"
		case (field == "imap" && port == 143) || (field == "smtp" && port == 587):
			security = "starttls"
		default:
			return RoundcubeMailboxEndpoint{}, fmt.Errorf("%sSecurity is required for port %d: \"ssl\" (implicit TLS) or \"starttls\"", field, port)
		}
	}
	if security != "ssl" && security != "starttls" {
		return RoundcubeMailboxEndpoint{}, fmt.Errorf("%sSecurity must be \"ssl\" or \"starttls\"", field)
	}
	return RoundcubeMailboxEndpoint{Host: normalized, Port: port, Security: security}, nil
}

// RoundcubeMailboxLoginRequest is the private owner input for one bounded
// mailbox login check. Password is used for the login request only and is
// never logged, returned or persisted. IMAP only names the mailbox reference.
type RoundcubeMailboxLoginRequest struct {
	IMAP            RoundcubeMailboxEndpoint
	Username        string
	Password        string
	ExpectedRelease string
}

// RoundcubeMailboxLoginResult is secret-free. MailboxRef is a digest of the
// server and account so lifecycle evidence never records the address itself.
type RoundcubeMailboxLoginResult struct {
	MailboxRef string
	Version    int
}

// VerifyRoundcubeMailboxLogin signs in to the owner's existing mailbox through
// Roundcube's own login form with its CSRF token, succeeds only when Roundcube
// opens the mailbox view, then signs out and confirms the session has ended.
// Roundcube connects only to the endpoints the owner file configures; the
// form carries no server.
func VerifyRoundcubeMailboxLogin(ctx context.Context, client *http.Client, baseURL string, request RoundcubeMailboxLoginRequest) (RoundcubeMailboxLoginResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, roundcubeSetupTimeout)
	defer cancel()
	username := strings.TrimSpace(request.Username)
	if username == "" || len(username) > 320 || strings.IndexFunc(username, unicode.IsControl) >= 0 {
		return RoundcubeMailboxLoginResult{}, errors.New("username must be the mailbox login name")
	}
	if request.Password == "" || len(request.Password) > 1024 {
		return RoundcubeMailboxLoginResult{}, errors.New("password must be the mailbox password")
	}
	expectedVersion, err := roundcubeReleaseVersion(request.ExpectedRelease)
	if err != nil {
		return RoundcubeMailboxLoginResult{}, err
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return RoundcubeMailboxLoginResult{}, errors.New("Roundcube setup requires the admitted application endpoint")
	}
	if client == nil || client.Transport == nil {
		return RoundcubeMailboxLoginResult{}, errors.New("Roundcube setup requires the already-admitted application HTTP client")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return RoundcubeMailboxLoginResult{}, err
	}
	session := &roundcubeSession{base: base, client: &http.Client{
		Transport: client.Transport, Timeout: client.Timeout, Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}

	status, _, body, err := session.do(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return RoundcubeMailboxLoginResult{}, fmt.Errorf("open the Roundcube login form: %w", err)
	}
	if status != http.StatusOK || roundcubeTask(body) != "login" {
		return RoundcubeMailboxLoginResult{}, fmt.Errorf("Roundcube did not show its login form (HTTP %d)", status)
	}
	version := roundcubeEnvVersion(body)
	if version != expectedVersion {
		return RoundcubeMailboxLoginResult{}, fmt.Errorf("running Roundcube version differs from the admitted release %q", request.ExpectedRelease)
	}
	loginToken := firstSubmatch(roundcubeLoginTokenPattern, body)
	if loginToken == "" {
		return RoundcubeMailboxLoginResult{}, errors.New("Roundcube login form has no request token")
	}

	form := url.Values{
		"_token": {loginToken}, "_task": {"login"}, "_action": {"login"}, "_timezone": {"UTC"}, "_url": {""},
		"_user": {username}, "_pass": {request.Password},
	}
	status, location, _, err := session.do(ctx, http.MethodPost, "/?_task=login", form)
	form.Del("_pass")
	if err != nil {
		return RoundcubeMailboxLoginResult{}, fmt.Errorf("submit the Roundcube login: %w", err)
	}
	if status != http.StatusFound || !strings.Contains(location, "_task=mail") {
		return RoundcubeMailboxLoginResult{}, fmt.Errorf("Roundcube rejected the mailbox login (HTTP %d); check the servers, ports, security modes, user name and password (the Roundcube log names the cause), and wait a minute after repeated failures", status)
	}
	mailboxPath, err := session.sameOriginPath(location)
	if err != nil {
		return RoundcubeMailboxLoginResult{}, err
	}
	status, _, body, err = session.do(ctx, http.MethodGet, mailboxPath, nil)
	if err != nil {
		return RoundcubeMailboxLoginResult{}, fmt.Errorf("open the Roundcube mailbox view: %w", err)
	}
	if status != http.StatusOK || roundcubeTask(body) != "mail" {
		return RoundcubeMailboxLoginResult{}, fmt.Errorf("Roundcube did not open the mailbox after login (HTTP %d)", status)
	}
	requestToken := firstSubmatch(roundcubeRequestTokenPattern, body)
	if requestToken == "" {
		return RoundcubeMailboxLoginResult{}, errors.New("Roundcube mailbox view has no request token for sign-out")
	}

	status, _, _, err = session.do(ctx, http.MethodGet, "/?_task=logout&_token="+url.QueryEscape(requestToken), nil)
	if err != nil || (status != http.StatusFound && status != http.StatusOK) {
		return RoundcubeMailboxLoginResult{}, errors.New("Roundcube did not sign out the verification session")
	}
	status, _, body, err = session.do(ctx, http.MethodGet, "/?_task=mail", nil)
	if err != nil || status != http.StatusOK || roundcubeTask(body) != "login" {
		return RoundcubeMailboxLoginResult{}, errors.New("the Roundcube verification session is still signed in after sign-out")
	}
	digest := sha256.Sum256([]byte(strings.ToLower(username) + "\x00" + request.IMAP.URI()))
	return RoundcubeMailboxLoginResult{MailboxRef: "mailbox:sha256:" + hex.EncodeToString(digest[:12]), Version: version}, nil
}

type roundcubeSession struct {
	base   *url.URL
	client *http.Client
}

func (s *roundcubeSession) do(ctx context.Context, method, path string, form url.Values) (int, string, string, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, s.base.String()+path, body)
	if err != nil {
		return 0, "", "", err
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := s.client.Do(request)
	if err != nil {
		// Never wrap the transport error: it can quote the request URL.
		return 0, "", "", errors.New("the Roundcube request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, roundcubeResponseBodyLimit+1))
	if err != nil || len(raw) > roundcubeResponseBodyLimit {
		return 0, "", "", errors.New("the Roundcube response could not be read within its size limit")
	}
	return response.StatusCode, response.Header.Get("Location"), string(raw), nil
}

// sameOriginPath resolves a Roundcube redirect and refuses to leave the
// admitted application origin.
func (s *roundcubeSession) sameOriginPath(location string) (string, error) {
	target, err := url.Parse(location)
	if err != nil {
		return "", errors.New("Roundcube redirected to an invalid location")
	}
	resolved := s.base.JoinPath("/").ResolveReference(target)
	if resolved.Scheme != s.base.Scheme || resolved.Host != s.base.Host {
		return "", errors.New("Roundcube redirected outside its admitted endpoint")
	}
	path := resolved.EscapedPath()
	if resolved.RawQuery != "" {
		path += "?" + resolved.RawQuery
	}
	return path, nil
}

func normalizeRoundcubeMailHost(value string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		// Roundcube's host URI and the governed mailbox script admit IPv4
		// literals and DNS names only.
		if ip.To4() == nil {
			return "", errors.New("IPv6 literals are not supported; use the server name")
		}
		return ip.To4().String(), nil
	}
	if host == "" || len(host) > 253 {
		return "", errors.New("invalid mail server name")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") ||
			strings.IndexFunc(label, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') }) >= 0 {
			return "", errors.New("invalid mail server name")
		}
	}
	return host, nil
}

func roundcubeReleaseVersion(release string) (int, error) {
	parts := roundcubeReleasePattern.FindStringSubmatch(strings.TrimSpace(release))
	if parts == nil {
		return 0, fmt.Errorf("Roundcube release %q has no numeric version", release)
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	patch, _ := strconv.Atoi(parts[3])
	return major*10000 + minor*100 + patch, nil
}

func roundcubeEnvVersion(body string) int {
	version, _ := strconv.Atoi(firstSubmatch(roundcubeVersionPattern, body))
	return version
}

func roundcubeTask(body string) string {
	return firstSubmatch(roundcubeTaskPattern, body)
}

func firstSubmatch(pattern *regexp.Regexp, body string) string {
	match := pattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}
