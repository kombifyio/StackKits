package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Access Gate's agent class (homelab identity target §7): a Gateway-signed
// envelope (subject, agent instance, lease, body hash) verified against the
// key pinned at linking, mapped through the D10 reference to the Homelab
// account. The verification follows Gateway ADR 0042 "Verification at the
// node" in order; every refusal is ErrHomeAgentEnvelopeRefused and never a
// downgrade to another class. Forward-auth carries no body, so the gate
// verifies everything but the body hash and admits the envelope once; the
// endpoint behind it (the node /mcp) verifies the envelope again with the body
// it read and consumes that admission, so a captured envelope sent to the
// endpoint directly is refused.
const (
	// HomeAgentEnvelopeLeeway is the clock leeway of the envelope contract:
	// an envelope is accepted until its exp plus this.
	HomeAgentEnvelopeLeeway   = 30 * time.Second
	homeAgentEnvelopeMaxBytes = 4 << 10
	homeAgentEnvelopeLeeway   = HomeAgentEnvelopeLeeway
	homeAgentEnvelopeMaxLife  = 60
	homeAgentReplayCapacity   = 1 << 16
	homeAgentReplaySweep      = 5 * time.Second
)

// ErrHomeAgentEnvelopeRefused is the one answer to every refused envelope.
var ErrHomeAgentEnvelopeRefused = errors.New("identity: the agent envelope is not valid for this request on this node")

// homeAgentEnvelopeStarted is this process's start. Used envelope IDs live
// in memory only, so an envelope whose iat, less the leeway, is not after the
// start is refused: no envelope a previous process could have admitted is
// accepted after a restart (the HomeVerifier.afterStart rule). The cost is
// that envelopes of the first 30 seconds after a start are refused and the
// agent asks the Gateway for a new one.
var homeAgentEnvelopeStarted = time.Now().UTC()

var (
	homeAgentBodyHash = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	homeAgentJTI      = regexp.MustCompile(`^[A-Za-z0-9_-]{22,128}$`)
	homeAgentClaimRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:|@/+-]{0,127}$`)
)

// HomeAgentRequest is one agent request as the gate observed it. Target is
// the route origin of the gate's own table plus the request path, without
// query or fragment; never a request header.
type HomeAgentRequest struct {
	Method   string
	Target   string
	Envelope string
}

// HomeAgentPrincipal is a verified agent call: the Homelab account it acts
// for, the kombify subject, the agent instance, the lease and capability, and
// the envelope's single-use id and expiry.
type HomeAgentPrincipal struct {
	Account       string
	CloudSubject  string
	AgentInstance string
	LeaseID       string
	Capability    string
	JTI           string
	ExpiresAt     time.Time
	// LinkRef names the pinned link the envelope verified under: its
	// binding generation and install time. Every install, re-key or remap
	// gives a new one; approvals are bound to it.
	LinkRef string
}

type homeAgentClaims struct {
	Iss string `json:"iss"`
	Aud string `json:"aud"`
	Sub string `json:"sub"`
	Tid string `json:"tid"`
	Act struct {
		Sub string `json:"sub"`
	} `json:"act"`
	Lid string      `json:"lid"`
	Cap string      `json:"cap"`
	Srv string      `json:"srv"`
	Gen json.Number `json:"gen"`
	Htm string      `json:"htm"`
	Htu string      `json:"htu"`
	Bdh string      `json:"bdh"`
	Jti string      `json:"jti"`
	Iat json.Number `json:"iat"`
	Exp json.Number `json:"exp"`
}

// VerifyHomeAgentEnvelope is the gate's verification: shape, signature under
// the pinned key only, issuer, audience, generation, server, tenant, time,
// request binding, single use and the D10 mapping. It records the admission
// the endpoint consumes.
func VerifyHomeAgentEnvelope(root string, request HomeAgentRequest, now time.Time) (HomeAgentPrincipal, error) {
	principal, claims, err := verifyHomeAgentEnvelope(root, request.Envelope, now)
	if err != nil || claims.Htm != request.Method || claims.Htu != request.Target {
		return HomeAgentPrincipal{}, ErrHomeAgentEnvelopeRefused
	}
	if !homeAgentAdmissionsFor(root).admit(principal.JTI, homeAgentAdmissionKey(request.Envelope, principal), principal.ExpiresAt, now) {
		return HomeAgentPrincipal{}, ErrHomeAgentEnvelopeRefused
	}
	return principal, nil
}

// ConsumeHomeAgentEnvelope is the endpoint's verification of an envelope the
// gate admitted for this request: the same checks again, the method and path
// of the request as received, the hash of the exact body bytes, and the
// gate's admission, which it spends.
func ConsumeHomeAgentEnvelope(root, envelope, method, path string, body []byte, now time.Time) (HomeAgentPrincipal, error) {
	principal, claims, err := verifyHomeAgentEnvelope(root, envelope, now)
	if err != nil || claims.Htm != method {
		return HomeAgentPrincipal{}, ErrHomeAgentEnvelopeRefused
	}
	target, parseErr := url.Parse(claims.Htu)
	digest := sha256.Sum256(body)
	if parseErr != nil || target.Path != path || claims.Bdh != base64.RawURLEncoding.EncodeToString(digest[:]) {
		return HomeAgentPrincipal{}, ErrHomeAgentEnvelopeRefused
	}
	if !homeAgentAdmissionsFor(root).consume(principal.JTI, homeAgentAdmissionKey(envelope, principal), now) {
		return HomeAgentPrincipal{}, ErrHomeAgentEnvelopeRefused
	}
	return principal, nil
}

//nolint:gocyclo // One ordered verification list; each step is a guard.
func verifyHomeAgentEnvelope(root, envelope string, now time.Time) (HomeAgentPrincipal, homeAgentClaims, error) {
	refuse := func() (HomeAgentPrincipal, homeAgentClaims, error) {
		return HomeAgentPrincipal{}, homeAgentClaims{}, ErrHomeAgentEnvelopeRefused
	}
	if len(envelope) == 0 || len(envelope) > homeAgentEnvelopeMaxBytes {
		return refuse()
	}
	parts := strings.Split(envelope, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return refuse()
	}
	headerRaw, headerErr := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	payload, payloadErr := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	signature, signatureErr := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if headerErr != nil || payloadErr != nil || signatureErr != nil || len(signature) != ed25519.SignatureSize {
		return refuse()
	}
	// The protected header is exactly alg, typ and kid: no jwk, jku, x5u,
	// crit, b64 or any other member, so nothing in the envelope chooses its
	// verification key or algorithm. kid is not used to select a key.
	var header map[string]json.RawMessage
	if json.Unmarshal(headerRaw, &header) != nil || len(header) != 3 {
		return refuse()
	}
	var alg, typ, kid string
	if json.Unmarshal(header["alg"], &alg) != nil || json.Unmarshal(header["typ"], &typ) != nil || json.Unmarshal(header["kid"], &kid) != nil ||
		alg != "EdDSA" || typ != HomeAgentEnvelopeType || kid == "" {
		return refuse()
	}
	link, err := LoadHomeAgentLink(root)
	if err != nil {
		return refuse()
	}
	public, ok := link.Key.publicKey()
	if !ok || !ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), signature) {
		return refuse()
	}
	var claims homeAgentClaims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(&claims) != nil || decoder.Decode(new(any)) != io.EOF {
		return refuse()
	}
	generation, genErr := claims.Gen.Int64()
	issued, iatErr := claims.Iat.Int64()
	expires, expErr := claims.Exp.Int64()
	if genErr != nil || iatErr != nil || expErr != nil {
		return refuse()
	}
	if claims.Iss != HomeAgentEnvelopeIssuer || claims.Aud != link.Key.Aud || generation != link.Key.BindingGeneration ||
		claims.Srv != link.ServerID || claims.Tid != link.TenantID {
		return refuse()
	}
	leeway := int64(homeAgentEnvelopeLeeway / time.Second)
	if issued > now.Unix()+leeway || expires <= now.Unix()-leeway || expires-issued < 1 || expires-issued > homeAgentEnvelopeMaxLife ||
		!time.Unix(issued, 0).Add(-homeAgentEnvelopeLeeway).After(homeAgentEnvelopeStarted.Truncate(time.Second)) {
		return refuse()
	}
	if !validHomeMethod(claims.Htm) || !validHomeAgentTarget(claims.Htu) || !homeAgentBodyHash.MatchString(claims.Bdh) ||
		!homeAgentJTI.MatchString(claims.Jti) || !homeAgentClaimRef.MatchString(claims.Act.Sub) || !homeAgentClaimRef.MatchString(claims.Lid) ||
		!slices.Contains([]string{HomeAgentCapabilityRead, HomeAgentCapabilityOperate, HomeAgentCapabilityMaintenance}, claims.Cap) {
		return refuse()
	}
	index := slices.IndexFunc(link.Subjects, func(subject HomeAgentSubject) bool { return subject.CloudSubject == claims.Sub })
	if index < 0 {
		return refuse()
	}
	return HomeAgentPrincipal{
		Account: link.Subjects[index].Account, CloudSubject: claims.Sub, AgentInstance: claims.Act.Sub, LeaseID: claims.Lid,
		Capability: claims.Cap, JTI: claims.Jti, ExpiresAt: time.Unix(expires, 0).UTC(),
		LinkRef: homeAgentLinkRef(link),
	}, claims, nil
}

// homeAgentLinkRef names one pinned link: its generation, install time and a
// digest of its key and mappings, so a re-key at the same generation or a
// remap within the same second is another link.
func homeAgentLinkRef(link HomeAgentLink) string {
	mapping, _ := json.Marshal([]any{link.Key.JKT, link.OwnerSubject, link.Subjects})
	digest := sha256.Sum256(mapping)
	return strconv.FormatInt(link.Key.BindingGeneration, 10) + "." + strconv.FormatInt(link.InstalledAt.Unix(), 10) + "." + hex.EncodeToString(digest[:4])
}

// validHomeAgentTarget admits scheme://host[:port]/path without user
// information, query or fragment, as the Gateway normalizes htu.
func validHomeAgentTarget(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" &&
		parsed.Fragment == "" && !parsed.ForceQuery && strings.HasPrefix(parsed.Path, "/") && len(raw) <= 1024
}

// homeAgentAdmissionKey binds an admission to the envelope and to what the
// gate checked it against: the account (whose groups the gate applied) and
// the pinned link. A re-install, re-key or remap between the gate and the
// endpoint changes the link reference, so the endpoint never spends an
// admission under another account or key.
func homeAgentAdmissionKey(envelope string, principal HomeAgentPrincipal) string {
	digest := sha256.Sum256([]byte(envelope))
	return base64.RawURLEncoding.EncodeToString(digest[:]) + "|" + principal.Account + "|" + principal.LinkRef
}

// homeAgentAdmissions is the single-use state of one node: every jti the
// gate accepted (kept until exp plus the leeway, then pruned; a full cache
// refuses rather than forgets) and the admissions the endpoint has not
// consumed yet. It lives in the stackkit-server process that runs both the
// gate listener and the endpoint.
type homeAgentAdmissions struct {
	mu       sync.Mutex
	seen     map[string]time.Time
	admitted map[string]string
	swept    time.Time
}

var homeAgentAdmissionStores sync.Map

func homeAgentAdmissionsFor(root string) *homeAgentAdmissions {
	absolute, err := filepath.Abs(root)
	if err != nil {
		absolute = root
	}
	value, _ := homeAgentAdmissionStores.LoadOrStore(absolute, &homeAgentAdmissions{seen: map[string]time.Time{}, admitted: map[string]string{}})
	return value.(*homeAgentAdmissions)
}

func (s *homeAgentAdmissions) admit(jti, digest string, expires, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.swept) >= homeAgentReplaySweep || len(s.seen) >= homeAgentReplayCapacity {
		for id, until := range s.seen {
			if !now.Before(until) {
				delete(s.seen, id)
				delete(s.admitted, id)
			}
		}
		s.swept = now
	}
	if _, used := s.seen[jti]; used || len(s.seen) >= homeAgentReplayCapacity {
		return false
	}
	s.seen[jti] = expires.Add(homeAgentEnvelopeLeeway)
	s.admitted[jti] = digest
	return true
}

func (s *homeAgentAdmissions) consume(jti, digest string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	admitted, ok := s.admitted[jti]
	until, seen := s.seen[jti]
	if !ok || !seen || !now.Before(until) || admitted != digest {
		return false
	}
	delete(s.admitted, jti)
	return true
}
