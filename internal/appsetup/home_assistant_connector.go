package appsetup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// HomeAssistantConnectorBinding is the local caller's requested installation
// tuple. This issuer verifies node custody and HA owner/version, NOT Docker
// container identity. The deployment owner must independently verify the tuple.
type HomeAssistantConnectorBinding struct {
	ResourceID      string                     `json:"resourceId"`
	ContainerID     string                     `json:"containerId"`
	ImageDigest     string                     `json:"imageDigest"`
	ServiceRevision int64                      `json:"serviceRevision"`
	Node            localevidence.LocalBinding `json:"node"`
}

type HomeAssistantConnectorRequest struct {
	Workspace string                        `json:"-"`
	Binding   HomeAssistantConnectorBinding `json:"binding"`
	// ensure creates once; revoke retains a tombstone and never re-registers.
	Action       string `json:"action"`
	LifespanDays int    `json:"lifespanDays"`
}

// Evidence never contains an endpoint or provider token. It does not attest
// deployment identity, MCP compatibility, agent equipment or cloud admission.
type HomeAssistantConnectorEvidence struct {
	OwnerRef         string                                  `json:"ownerRef"`
	HAUserID         string                                  `json:"haUserId"`
	HAVersion        string                                  `json:"haVersion"`
	RequestedBinding HomeAssistantConnectorBinding           `json:"requestedBinding"`
	SecretRef        string                                  `json:"secretRef"`
	Status           string                                  `json:"status"`
	ObservedAt       time.Time                               `json:"observedAt"`
	ExpiresAt        time.Time                               `json:"expiresAt"`
	Signature        localevidence.OwnerPolicyStateSignature `json:"signature"`
}

var (
	haConnectorID                      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	haContainerID                      = regexp.MustCompile(`^[a-f0-9]{64}$`)
	ErrHomeAssistantConnectorUncertain = errors.New("home_assistant_connector_issuance_uncertain: reconcile this node's retained intent; do not repeat issuance")
	ErrHomeAssistantConnectorRevoked   = errors.New("home_assistant_connector_revoked: retained local revocation requires a new explicit enrollment")
)

type haConnectorRecord struct {
	Schema         string                        `json:"schema"`
	Owner          string                        `json:"owner"`
	Binding        HomeAssistantConnectorBinding `json:"binding"`
	Origin         string                        `json:"origin"`
	Version        string                        `json:"version"`
	User           string                        `json:"user"`
	State          string                        `json:"state"`
	ClientName     string                        `json:"clientName"`
	IntentAt       time.Time                     `json:"intentAt"`
	ExpiresAt      time.Time                     `json:"expiresAt"`
	TokenID        string                        `json:"tokenId,omitempty"`
	TokenCreatedAt string                        `json:"tokenCreatedAt,omitempty"`
	Token          string                        `json:"token,omitempty"`
}

type haConnectorSession struct {
	request HomeAssistantConnectorRequest
	ref     string
	record  haConnectorRecord
	exists  bool
	root    *confinedfs.Root
	tx      *confinedfs.Transaction
	lock    *confinedfs.OutputLock
}

func prepareHomeAssistantConnector(request *HomeAssistantConnectorRequest, origin, version string) (_ *haConnectorSession, err error) {
	if request == nil {
		return nil, nil
	}
	b := request.Binding
	if (request.Action != "ensure" && request.Action != "revoke") || !haConnectorID.MatchString(b.ResourceID) ||
		!haContainerID.MatchString(b.ContainerID) || !strings.HasPrefix(b.ImageDigest, "sha256:") || !haContainerID.MatchString(strings.TrimPrefix(b.ImageDigest, "sha256:")) || b.ServiceRevision <= 0 ||
		request.LifespanDays < 1 || request.LifespanDays > 365 || version != HomeAssistantPinnedVersion {
		return nil, errors.New("home_assistant_connector_binding_invalid")
	}
	owner, err := localevidence.LoadOwnerCustody(request.Workspace)
	if err != nil || owner.Binding != b.Node {
		return nil, errors.New("home_assistant_connector_node_owner_required")
	}
	identity, _ := json.Marshal(struct {
		Owner, Resource string
		Node            localevidence.LocalBinding
	}{owner.OwnerRef, b.ResourceID, b.Node})
	digest := sha256.Sum256(identity)
	id := hex.EncodeToString(digest[:])
	s := &haConnectorSession{request: *request, ref: "secret://home-assistant/connector/" + id,
		record: haConnectorRecord{Schema: "stackkit.home-assistant-connector/v1", Owner: owner.OwnerRef, Binding: b, Origin: origin, Version: version}}
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	s.root, err = confinedfs.Open(request.Workspace)
	if err != nil {
		return nil, err
	}
	s.tx, err = s.root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	s.lock, err = s.tx.TryAcquireOutputLock(".stackkit/custody/home-assistant/" + id)
	if err != nil {
		return nil, errors.New("home_assistant_connector_setup_busy")
	}
	raw, readErr := localevidence.ResolveEncryptedLocalIssuedSecret(request.Workspace, s.ref)
	defer clear(raw)
	if errors.Is(readErr, os.ErrNotExist) {
		return s, nil
	}
	if readErr != nil {
		return nil, errors.New("home_assistant_connector_custody_unreadable")
	}
	var retained haConnectorRecord
	if json.Unmarshal(raw, &retained) != nil || retained.Schema != s.record.Schema || retained.Owner != owner.OwnerRef || retained.Binding != b || retained.Origin != origin || retained.Version != version || !haConnectorID.MatchString(retained.User) || retained.ClientName == "" || retained.IntentAt.IsZero() || !retained.ExpiresAt.After(retained.IntentAt) {
		return nil, errors.New("home_assistant_connector_binding_changed")
	}
	if retained.State != "issuing" && retained.State != "active" && retained.State != "revoking" && retained.State != "revoked" {
		return nil, errors.New("home_assistant_connector_custody_invalid")
	}
	if request.Action == "ensure" && retained.ExpiresAt.Sub(retained.IntentAt) != time.Duration(request.LifespanDays)*24*time.Hour {
		return nil, errors.New("home_assistant_connector_lifetime_changed")
	}
	s.record, s.exists = retained, true
	return s, nil
}

func (s *haConnectorSession) close() {
	s.record.Token = ""
	if s.lock != nil {
		_ = s.lock.Close()
	}
	if s.tx != nil {
		_ = s.tx.Close()
	}
	if s.root != nil {
		_ = s.root.Close()
	}
}

func (s *haConnectorSession) save() error {
	raw, err := json.Marshal(s.record)
	if err != nil {
		return errors.New("home_assistant_connector_custody_invalid")
	}
	defer clear(raw)
	if err := localevidence.StoreEncryptedLocalIssuedSecret(s.request.Workspace, s.ref, raw); err != nil {
		return errors.New("home_assistant_connector_custody_not_durable")
	}
	return nil
}

func (s *haConnectorSession) evidence() (*HomeAssistantConnectorEvidence, error) {
	evidence := &HomeAssistantConnectorEvidence{OwnerRef: s.record.Owner, HAUserID: s.record.User, HAVersion: s.record.Version, RequestedBinding: s.record.Binding, SecretRef: s.ref, Status: s.record.State, ObservedAt: time.Now().UTC(), ExpiresAt: s.record.ExpiresAt}
	payload, _ := json.Marshal(evidence)
	signature, err := localevidence.SignOwnerPolicyState(s.request.Workspace, payload)
	if err != nil {
		return nil, errors.New("home_assistant_connector_receipt_unsigned")
	}
	evidence.Signature = signature
	return evidence, nil
}

type haConnectorToken struct {
	ID      string `json:"id"`
	Name    string `json:"client_name"`
	Type    string `json:"type"`
	Created string `json:"created_at"`
	Current bool   `json:"is_current"`
}

func (s *haConnectorSession) apply(ctx context.Context, client *http.Client, ownerToken, user string) (*HomeAssistantConnectorEvidence, error) {
	if s.exists && s.record.User != user {
		return nil, errors.New("home_assistant_connector_owner_changed")
	}
	s.record.User = user
	if s.record.State == "revoked" {
		if s.request.Action == "revoke" {
			return s.evidence()
		}
		return nil, ErrHomeAssistantConnectorRevoked
	}
	ctx, cancel := context.WithTimeout(ctx, homeAssistantWebSocketTimeout)
	defer cancel()
	wsURL, err := homeAssistantEndpointURL(s.record.Origin, "/api/websocket")
	if err != nil {
		return nil, err
	}
	wsURL = "ws" + strings.TrimPrefix(wsURL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return nil, errors.New("home_assistant_connector_session_unavailable")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(homeAssistantWSLimit)
	var greeting homeAssistantWSGreeting
	if wsjson.Read(ctx, conn, &greeting) != nil || greeting.Type != "auth_required" || greeting.HAVersion != s.record.Version {
		return nil, errors.New("home_assistant_connector_version_changed")
	}
	if wsjson.Write(ctx, conn, map[string]string{"type": "auth", "access_token": ownerToken}) != nil {
		return nil, errors.New("home_assistant_connector_auth_failed")
	}
	var auth homeAssistantWSAuthResult
	if wsjson.Read(ctx, conn, &auth) != nil || auth.Type != "auth_ok" || auth.HAVersion != s.record.Version {
		return nil, errors.New("home_assistant_connector_auth_failed")
	}
	var sequence int64
	command := func(kind string, fields map[string]any, result any) error {
		sequence++
		if fields == nil {
			fields = map[string]any{}
		}
		fields["id"], fields["type"] = sequence, kind
		if wsjson.Write(ctx, conn, fields) != nil {
			return ErrHomeAssistantConnectorUncertain
		}
		var reply homeAssistantWSResult
		if wsjson.Read(ctx, conn, &reply) != nil || reply.ID != sequence || reply.Type != "result" || !reply.Success {
			return ErrHomeAssistantConnectorUncertain
		}
		if result != nil && json.Unmarshal(reply.Result, result) != nil {
			return ErrHomeAssistantConnectorUncertain
		}
		return nil
	}
	list := func() ([]haConnectorToken, error) {
		var tokens []haConnectorToken
		err := command("auth/refresh_tokens", nil, &tokens)
		if err == nil && tokens == nil {
			err = ErrHomeAssistantConnectorUncertain
		}
		return tokens, err
	}
	match := func(tokens []haConnectorToken) (*haConnectorToken, error) {
		var found *haConnectorToken
		for _, token := range tokens {
			if token.Name != s.record.ClientName {
				continue
			}
			created, e := time.Parse(time.RFC3339Nano, token.Created)
			if found != nil || !haConnectorID.MatchString(token.ID) || token.Type != "long_lived_access_token" || token.Current || e != nil || created.Before(s.record.IntentAt) || created.After(s.record.ExpiresAt) || (s.record.TokenID != "" && (token.ID != s.record.TokenID || token.Created != s.record.TokenCreatedAt)) {
				return nil, errors.New("home_assistant_connector_token_identity_ambiguous")
			}
			copy := token
			found = &copy
		}
		return found, nil
	}
	tokens, err := list()
	if err != nil {
		return nil, err
	}
	if !s.exists {
		if s.request.Action == "revoke" {
			return nil, errors.New("home_assistant_connector_not_enrolled")
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		s.record.ClientName = "kombify-agent-" + hex.EncodeToString(nonce[:])
		for _, token := range tokens {
			if token.Name == s.record.ClientName {
				return nil, errors.New("home_assistant_connector_token_identity_ambiguous")
			}
		}
		s.record.IntentAt = time.Now().UTC()
		s.record.ExpiresAt = s.record.IntentAt.Add(time.Duration(s.request.LifespanDays) * 24 * time.Hour)
		s.record.State = "issuing"
		if err = s.save(); err != nil {
			return nil, err
		}
		// The durable intent precedes the only token-creation effect. Neither
		// transport errors nor subsequent invocations may replay this command.
		if err = command("auth/long_lived_access_token", map[string]any{"client_name": s.record.ClientName, "lifespan": s.request.LifespanDays}, &s.record.Token); err != nil {
			return nil, err
		}
		if strings.TrimSpace(s.record.Token) == "" || len(s.record.Token) > 4096 {
			return nil, ErrHomeAssistantConnectorUncertain
		}
		tokens, err = list()
		if err != nil {
			return nil, err
		}
		issued, err := match(tokens)
		if err != nil {
			return nil, err
		}
		if issued == nil {
			return nil, ErrHomeAssistantConnectorUncertain
		}
		s.record.TokenID, s.record.TokenCreatedAt = issued.ID, issued.Created
		observed, version, err := readHomeAssistantCurrentUser(ctx, client, s.record.Origin, s.record.Token, s.record.Version)
		if err != nil || !haConnectorOwnerMatches(observed, user) || version != s.record.Version {
			return nil, ErrHomeAssistantConnectorUncertain
		}
		s.record.State = "active"
		if err = s.save(); err != nil {
			return nil, err
		}
		return s.evidence()
	}
	matched, err := match(tokens)
	if err != nil {
		return nil, err
	}
	if s.request.Action == "revoke" || s.record.State == "issuing" || s.record.State == "revoking" || !time.Now().Before(s.record.ExpiresAt) {
		s.record.State = "revoking"
		s.record.Token = ""
		if matched != nil {
			s.record.TokenID, s.record.TokenCreatedAt = matched.ID, matched.Created
		}
		if err = s.save(); err != nil {
			return nil, err
		}
		if matched != nil {
			if err = command("auth/delete_refresh_token", map[string]any{"refresh_token_id": matched.ID}, nil); err != nil {
				return nil, err
			}
			tokens, err = list()
			if err != nil {
				return nil, err
			}
			matched, err = match(tokens)
			if err != nil {
				return nil, err
			}
			if matched != nil {
				return nil, ErrHomeAssistantConnectorUncertain
			}
		}
		s.record.State = "revoked"
		if err = s.save(); err != nil {
			return nil, err
		}
		if s.request.Action == "ensure" {
			return nil, ErrHomeAssistantConnectorRevoked
		}
		return s.evidence()
	}
	if matched == nil {
		s.record.State = "revoked"
		s.record.Token = ""
		if err = s.save(); err != nil {
			return nil, err
		}
		return nil, ErrHomeAssistantConnectorRevoked
	}
	observed, version, err := readHomeAssistantCurrentUser(ctx, client, s.record.Origin, s.record.Token, s.record.Version)
	if err != nil || !haConnectorOwnerMatches(observed, user) || version != s.record.Version {
		return nil, errors.New("home_assistant_connector_credential_no_longer_valid")
	}
	return s.evidence()
}

// WithHomeAssistantConnectorCredential opens an active node credential only
// for a local consumer, under the same lifecycle lock and exact original
// binding. The consumer still owes fresh Gateway lease/relay authorization;
// this API is not agent admission and never exports the value in a receipt.
func WithHomeAssistantConnectorCredential(ctx context.Context, client *http.Client, request HomeAssistantConnectorRequest, origin, version string, use func(string) error) error {
	if use == nil || client == nil || request.Action != "ensure" {
		return errors.New("home_assistant_connector_local_consumer_required")
	}
	origin, err := normalizeHomeAssistantBaseURL(origin)
	if err != nil {
		return err
	}
	s, err := prepareHomeAssistantConnector(&request, origin, version)
	if err != nil {
		return err
	}
	defer s.close()
	if !s.exists || s.record.State != "active" || s.record.Token == "" || !time.Now().Before(s.record.ExpiresAt) {
		return ErrHomeAssistantConnectorRevoked
	}
	observed, observedVersion, err := readHomeAssistantCurrentUser(ctx, cloneHomeAssistantHTTPClient(client), origin, s.record.Token, version)
	if err != nil || !haConnectorOwnerMatches(observed, s.record.User) || observedVersion != version {
		return errors.New("home_assistant_connector_credential_no_longer_valid")
	}
	return use(s.record.Token)
}

func haConnectorOwnerMatches(user homeAssistantCurrentUser, expected string) bool {
	return user.ID == expected && user.IsOwner != nil && *user.IsOwner && user.IsAdmin != nil && *user.IsAdmin
}

// VerifyHomeAssistantConnectorCustody compares signed receipt facts with the
// existing encrypted node record. It neither changes custody nor issues a key.
// A retained revocation cannot be softened by an older active receipt.
func VerifyHomeAssistantConnectorCustody(request HomeAssistantConnectorRequest, origin, version string, evidence HomeAssistantConnectorEvidence) error {
	origin, err := normalizeHomeAssistantBaseURL(origin)
	if err != nil {
		return err
	}
	s, err := prepareHomeAssistantConnector(&request, origin, version)
	if err != nil {
		return err
	}
	defer s.close()
	if !s.exists || (s.record.State != "active" && s.record.State != "revoked") || s.record.State != evidence.Status || s.record.Owner != evidence.OwnerRef || s.record.User != evidence.HAUserID || s.record.Version != evidence.HAVersion || s.ref != evidence.SecretRef || s.record.Binding != evidence.RequestedBinding || !s.record.ExpiresAt.Equal(evidence.ExpiresAt) || (s.record.State == "active" && !time.Now().Before(s.record.ExpiresAt)) {
		return errors.New("home_assistant_connector_receipt_no_longer_current")
	}
	unsigned := evidence
	unsigned.Signature = localevidence.OwnerPolicyStateSignature{}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerPolicyState(request.Workspace, payload, evidence.Signature)
}
