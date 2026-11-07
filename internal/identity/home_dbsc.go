package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Device Bound Session Credentials (W3C WebAppSec DBSC; homelab identity
// target §6.3 "Browser-native device-bound sessions replace this where
// available"; StackKits igti.14).
//
// The Hub launch proof stays the way a browser first proves its device to the
// Gate, in every browser. Its answer also carries a DBSC registration offer. A
// browser that supports DBSC generates a key it never exports, registers it,
// and from then on the Gate trusts only a short-lived bound cookie that the
// browser renews with a proof signed by that key; the full-length cookie the
// launch set stops working the moment the registration succeeds. A browser
// without DBSC ignores the offer and keeps the launch cookie. That is
// capability detection, not a second protocol: the session is the same
// per-host session, recorded against the same device, and revoking the device
// ends it at the next refresh or request.
const (
	// HomeDBSCRegisterPath and HomeDBSCRefreshPath are reserved on every
	// gated host, next to HomeLaunchPath.
	HomeDBSCRegisterPath = "/.well-known/stackkit/dbsc/register"
	HomeDBSCRefreshPath  = "/.well-known/stackkit/dbsc/refresh"
	// HomeDBSCCookieLifetime is how long one bound cookie lives between
	// refreshes.
	HomeDBSCCookieLifetime = 10 * time.Minute

	// The DBSC header names of the W3C Editor's Draft.
	HomeDBSCRegistrationHeader = "Secure-Session-Registration"
	HomeDBSCResponseHeader     = "Secure-Session-Response"
	HomeDBSCSessionIDHeader    = "Sec-Secure-Session-Id"
	HomeDBSCChallengeHeader    = "Secure-Session-Challenge"

	homeDBSCProofType        = "dbsc+jwt"
	homeDBSCStatePath        = ".stackkit/custody/home-verifier/dbsc-sessions.json"
	homeDBSCStateSchema      = "stackkit.home-dbsc-sessions/v1"
	homeDBSCStateMaxBytes    = 1 << 20
	homeDBSCSessionLimit     = 512
	homeDBSCRegisterWindow   = 2 * time.Minute
	homeDBSCChallengeWindow  = 5 * time.Minute
	homeDBSCRegistrationMAC  = "stackkit.home-dbsc-registration/v1\x00"
	homeDBSCRequestBodyBytes = 1 << 10
	homeDBSCCacheFor         = 3 * time.Second
)

type homeDBSCRecord struct {
	ID        string          `json:"id"`
	Host      string          `json:"host"`
	Account   string          `json:"account"`
	Device    string          `json:"device"`
	Home      bool            `json:"home"`
	Key       jose.JSONWebKey `json:"key"`
	IssuedAt  time.Time       `json:"issuedAt"`
	ExpiresAt time.Time       `json:"expiresAt"`
	// Challenge is the one refresh challenge outstanding; it is single use.
	Challenge   string    `json:"challenge,omitempty"`
	ChallengeAt time.Time `json:"challengeAt,omitempty"`
}

type homeDBSCState struct {
	Schema   string           `json:"schema"`
	Sessions []homeDBSCRecord `json:"sessions"`
}

type homeDBSCCacheEntry struct {
	at       time.Time
	sessions map[string]homeDBSCRecord
}

var (
	homeDBSCCache   sync.Map // root -> *homeDBSCCacheEntry
	homeDBSCWriteMu sync.Mutex
)

// homeDBSCSessions is the recorded DBSC sessions, cached for a few seconds and
// refreshed at once by this process's own writes.
func homeDBSCSessions(root string, now time.Time) map[string]homeDBSCRecord {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	if cached, ok := homeDBSCCache.Load(absolute); ok {
		entry := cached.(*homeDBSCCacheEntry)
		if now.Sub(entry.at) < homeDBSCCacheFor && !now.Before(entry.at) {
			return entry.sessions
		}
	}
	state, err := loadHomeDBSCState(absolute)
	if err != nil {
		// An unreadable record must never turn a bound session back into a
		// full-length cookie: callers treat a failed read as "unknown".
		return nil
	}
	sessions := map[string]homeDBSCRecord{}
	for _, record := range state.Sessions {
		sessions[record.ID] = record
	}
	homeDBSCCache.Store(absolute, &homeDBSCCacheEntry{at: now, sessions: sessions})
	return sessions
}

func loadHomeDBSCState(root string) (homeDBSCState, error) {
	state := homeDBSCState{Schema: homeDBSCStateSchema}
	raw, err := readHomePrivate(root, homeDBSCStatePath, homeDBSCStateMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if json.Unmarshal(raw, &state) != nil || state.Schema != homeDBSCStateSchema {
		return homeDBSCState{Schema: homeDBSCStateSchema}, errors.New("identity: unreadable DBSC session records")
	}
	return state, nil
}

// updateHomeDBSC runs one read-modify-write of the records under a lock and
// refreshes this process's cache.
func updateHomeDBSC(root string, now time.Time, mutate func(*homeDBSCState) error) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	homeDBSCWriteMu.Lock()
	defer homeDBSCWriteMu.Unlock()
	release, err := localevidence.AcquireCustodyLock(absolute, homeDBSCStatePath, 203)
	if err != nil {
		return err
	}
	defer release()
	state, err := loadHomeDBSCState(absolute)
	if err != nil {
		return err
	}
	if err := mutate(&state); err != nil {
		return err
	}
	kept := state.Sessions[:0]
	for _, record := range state.Sessions {
		if now.Before(record.ExpiresAt) {
			kept = append(kept, record)
		}
	}
	state.Sessions = kept
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := writeHomePrivate(absolute, homeDBSCStatePath, raw, false); err != nil {
		return err
	}
	sessions := map[string]homeDBSCRecord{}
	for _, record := range state.Sessions {
		sessions[record.ID] = record
	}
	homeDBSCCache.Store(absolute, &homeDBSCCacheEntry{at: now, sessions: sessions})
	return nil
}

// checkHomeDBSCSession decides, for a session cookie that already passed its
// MAC and lifetime checks, whether its DBSC state admits it. A bound cookie
// needs its live record. A launch cookie is refused once its session is bound:
// that is what makes a copied launch cookie useless after registration.
func checkHomeDBSCSession(root string, claims homeBrowserSessionClaims, now time.Time) bool {
	sessions := homeDBSCSessions(root, now)
	record, bound := sessions[claims.ID]
	if claims.DBSC {
		return bound && record.Host == claims.Host && record.Account == claims.Account && record.Device == claims.Device && now.Before(record.ExpiresAt)
	}
	if sessions == nil {
		// Records unreadable: a launch cookie is admitted only if no record
		// file exists at all.
		_, err := readHomePrivate(root, homeDBSCStatePath, 1)
		return errors.Is(err, fs.ErrNotExist)
	}
	return !bound
}

// HomeDBSCRegistration is the registration offer for a launch cookie value:
// the "Secure-Session-Registration" header value, or "" for a value that is
// not this Gate's session.
func HomeDBSCRegistration(root, sessionValue string, now time.Time) string {
	claims, err := parseHomeBrowserSessionClaims(root, sessionValue)
	if err != nil || claims.DBSC {
		return ""
	}
	challenge, err := homeDBSCRegistrationChallenge(root, claims, now)
	if err != nil {
		return ""
	}
	return `(ES256);path="` + HomeDBSCRegisterPath + `";challenge="` + challenge + `"`
}

func homeDBSCRegistrationChallenge(root string, claims homeBrowserSessionClaims, now time.Time) (string, error) {
	key, err := homeBrowserSessionKey(root, false)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		ID      string `json:"sid"`
		Host    string `json:"host"`
		Expires int64  `json:"exp"`
		Nonce   []byte `json:"n"`
	}{claims.ID, claims.Host, now.Add(homeDBSCRegisterWindow).Unix(), nonce})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(homeDBSCRegistrationMAC + encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// verifyHomeDBSCRegistrationChallenge returns the session ID the challenge was
// issued for on host.
func verifyHomeDBSCRegistrationChallenge(root, challenge, host string, now time.Time) (string, bool) {
	encoded, macPart, ok := strings.Cut(challenge, ".")
	if !ok || len(challenge) > 512 {
		return "", false
	}
	key, err := homeBrowserSessionKey(root, false)
	if err != nil {
		return "", false
	}
	got, err := base64.RawURLEncoding.DecodeString(macPart)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(homeDBSCRegistrationMAC + encoded))
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	var claims struct {
		ID      string `json:"sid"`
		Host    string `json:"host"`
		Expires int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.ID == "" || claims.Host != host || !now.Before(time.Unix(claims.Expires, 0)) {
		return "", false
	}
	return claims.ID, true
}

// HomeDBSCHandler serves the registration and refresh endpoints of every gated
// host. Neither takes a session of its own: the registration is proved by the
// launch cookie and the one challenge the Gate issued for it, the refresh by
// the key that registered.
func HomeDBSCHandler(root string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+HomeDBSCRegisterPath, func(w http.ResponseWriter, r *http.Request) { homeDBSCRegister(root, w, r) })
	mux.HandleFunc("POST "+HomeDBSCRefreshPath, func(w http.ResponseWriter, r *http.Request) { homeDBSCRefresh(root, w, r) })
	return mux
}

func homeDBSCHost(r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(r.Host))
}

// homeDBSCStructuredString reads a one-value sf-string header.
func homeDBSCStructuredString(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", false
	}
	value = value[1 : len(value)-1]
	if strings.ContainsAny(value, `"\`) {
		return "", false
	}
	return value, true
}

type homeDBSCProof struct {
	Key jose.JSONWebKey
	JTI string
}

// parseHomeDBSCProof verifies a DBSC proof JWT: type dbsc+jwt, ES256, the
// embedded key on registration (and only then), the signature, and its jti.
func parseHomeDBSCProof(raw string, registration bool, known jose.JSONWebKey, audience string) (homeDBSCProof, error) {
	deny := func() (homeDBSCProof, error) { return homeDBSCProof{}, ErrHomeAccessDenied }
	if len(raw) == 0 || len(raw) > homeDeviceMaxTokenBytes || strings.Count(raw, ".") != 2 {
		return deny()
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(token.Headers) != 1 {
		return deny()
	}
	header := token.Headers[0]
	if header.ExtraHeaders[jose.HeaderType] != homeDBSCProofType {
		return deny()
	}
	key := known
	if registration {
		if header.JSONWebKey == nil {
			return deny()
		}
		key = *header.JSONWebKey
		if !validHomeDeviceKey(key, string(jose.ES256)) {
			return deny()
		}
	} else if header.JSONWebKey != nil {
		return deny()
	}
	var loose map[string]any
	if token.Claims(key.Key, &loose) != nil {
		return deny()
	}
	jti, _ := loose["jti"].(string)
	if jti == "" || len(jti) > 512 {
		return deny()
	}
	if aud, ok := loose["aud"].(string); ok && aud != "" && aud != audience {
		return deny()
	}
	return homeDBSCProof{Key: key, JTI: jti}, nil
}

func homeDBSCConfig(record homeDBSCRecord) map[string]any {
	return map[string]any{
		"session_identifier": record.ID,
		"refresh_url":        HomeDBSCRefreshPath,
		"scope":              map[string]any{"origin": "https://" + record.Host, "include_site": false},
		"credentials": []map[string]any{{
			"type": "cookie", "name": HomeBrowserSessionCookie, "attributes": "Path=/; Secure; HttpOnly; SameSite=Lax",
		}},
	}
}

func homeDBSCReply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	if body != nil {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func homeDBSCRegister(root string, w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	host := homeDBSCHost(r)
	raw, ok := homeDBSCStructuredString(r.Header, HomeDBSCResponseHeader)
	if !ok {
		homeDBSCReply(w, http.StatusBadRequest, nil)
		return
	}
	proof, err := parseHomeDBSCProof(raw, true, jose.JSONWebKey{}, "https://"+host+HomeDBSCRegisterPath)
	if err != nil {
		homeDBSCReply(w, http.StatusUnauthorized, nil)
		return
	}
	sid, ok := verifyHomeDBSCRegistrationChallenge(root, proof.JTI, host, now)
	if !ok {
		homeDBSCReply(w, http.StatusUnauthorized, nil)
		return
	}
	var cookie string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == HomeBrowserSessionCookie {
			cookie, count = c.Value, count+1
		}
	}
	verifier, err := HomeVerifierFor(root)
	if err != nil || count != 1 {
		homeDBSCReply(w, http.StatusUnauthorized, nil)
		return
	}
	session, err := verifier.VerifyHomeBrowserSession(cookie, host, false)
	if err != nil || session.ID != sid {
		homeDBSCReply(w, http.StatusUnauthorized, nil)
		return
	}
	next, err := homeRandomID()
	if err != nil {
		homeDBSCReply(w, http.StatusInternalServerError, nil)
		return
	}
	record := homeDBSCRecord{
		ID: sid, Host: host, Account: session.Account, Device: session.Device, Home: session.Home, Key: proof.Key,
		IssuedAt: session.IssuedAt, ExpiresAt: session.ExpiresAt, Challenge: next, ChallengeAt: now,
	}
	refused := false
	err = updateHomeDBSC(root, now, func(state *homeDBSCState) error {
		for _, existing := range state.Sessions {
			if existing.ID == sid {
				refused = true
				return nil
			}
		}
		if len(state.Sessions) >= homeDBSCSessionLimit {
			refused = true
			return nil
		}
		state.Sessions = append(state.Sessions, record)
		return nil
	})
	if err != nil || refused {
		homeDBSCReply(w, http.StatusForbidden, nil)
		return
	}
	homeDBSCIssue(root, w, record, now)
	homeDBSCReply(w, http.StatusOK, homeDBSCConfig(record))
}

func homeDBSCRefresh(root string, w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	host := homeDBSCHost(r)
	sid, ok := homeDBSCStructuredString(r.Header, HomeDBSCSessionIDHeader)
	if !ok {
		homeDBSCReply(w, http.StatusBadRequest, nil)
		return
	}
	record, found := homeDBSCSessions(root, now)[sid]
	verifier, err := HomeVerifierFor(root)
	// Any other 4xx ends the session in the browser: unknown, expired, another
	// host's, or one whose device was revoked or lapsed.
	if err != nil || !found || record.Host != host || !now.Before(record.ExpiresAt) || !verifier.activeDevice(record.Device, record.Account, now) {
		homeDBSCReply(w, http.StatusUnauthorized, nil)
		return
	}
	challenge := func() {
		next, nextErr := homeRandomID()
		if nextErr != nil {
			homeDBSCReply(w, http.StatusInternalServerError, nil)
			return
		}
		if updateHomeDBSC(root, now, func(state *homeDBSCState) error {
			for i := range state.Sessions {
				if state.Sessions[i].ID == sid {
					state.Sessions[i].Challenge, state.Sessions[i].ChallengeAt = next, now
				}
			}
			return nil
		}) != nil {
			homeDBSCReply(w, http.StatusServiceUnavailable, nil)
			return
		}
		w.Header().Set(HomeDBSCChallengeHeader, `"`+next+`";id="`+sid+`"`)
		homeDBSCReply(w, http.StatusForbidden, nil)
	}
	raw, ok := homeDBSCStructuredString(r.Header, HomeDBSCResponseHeader)
	if !ok {
		challenge()
		return
	}
	proof, err := parseHomeDBSCProof(raw, false, record.Key, "https://"+host+HomeDBSCRefreshPath)
	if err != nil {
		homeDBSCReply(w, http.StatusUnauthorized, nil)
		return
	}
	if record.Challenge == "" || now.Sub(record.ChallengeAt) > homeDBSCChallengeWindow ||
		subtle.ConstantTimeCompare([]byte(proof.JTI), []byte(record.Challenge)) != 1 {
		challenge()
		return
	}
	next, err := homeRandomID()
	if err != nil {
		homeDBSCReply(w, http.StatusInternalServerError, nil)
		return
	}
	used := false
	err = updateHomeDBSC(root, now, func(state *homeDBSCState) error {
		for i := range state.Sessions {
			if state.Sessions[i].ID == sid && state.Sessions[i].Challenge == proof.JTI {
				state.Sessions[i].Challenge, state.Sessions[i].ChallengeAt = next, now
				used = true
			}
		}
		return nil
	})
	if err != nil || !used {
		// The challenge was spent by another request: start over.
		challenge()
		return
	}
	homeDBSCIssue(root, w, record, now)
	w.Header().Set(HomeDBSCChallengeHeader, `"`+next+`";id="`+sid+`"`)
	homeDBSCReply(w, http.StatusOK, homeDBSCConfig(record))
}

// homeDBSCIssue sets the short-lived bound cookie of record.
func homeDBSCIssue(root string, w http.ResponseWriter, record homeDBSCRecord, now time.Time) {
	expires := now.Add(HomeDBSCCookieLifetime)
	if record.ExpiresAt.Before(expires) {
		expires = record.ExpiresAt
	}
	claims := homeBrowserSessionClaims{Version: 1, ID: record.ID, Host: record.Host, Account: record.Account, Device: record.Device,
		Home: record.Home, IssuedAt: record.IssuedAt.Unix(), Expires: expires.Unix(), DBSC: true}
	value, err := signHomeBrowserSession(root, claims)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: HomeBrowserSessionCookie, Value: value, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(expires.Sub(now) / time.Second)})
}
