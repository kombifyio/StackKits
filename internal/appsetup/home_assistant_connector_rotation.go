package appsetup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/kombifyio/stackkits/internal/guardenrollment"
)

// localGuardIdentity is the Guard identity of this node from its own Guard
// enrollment, never from a request: "guard:<server>/<worker>".
func localGuardIdentity() (string, error) {
	enrollment, err := guardenrollment.Read()
	if err != nil {
		return "", errors.New("home_assistant_connector_guard_required: the connector credential is bound to the Techstack Guard of this node; no Guard enrollment was found")
	}
	return enrollment.Identity(), nil
}

type haConnectorToken struct {
	ID      string `json:"id"`
	Name    string `json:"client_name"`
	Type    string `json:"type"`
	Created string `json:"created_at"`
	Current bool   `json:"is_current"`
}

// haConnectorSocket is one authenticated Home Assistant WebSocket session.
type haConnectorSocket struct {
	ctx      context.Context
	conn     *websocket.Conn
	sequence int64
}

func dialHomeAssistantConnector(ctx context.Context, client *http.Client, origin, version, token string) (*haConnectorSocket, error) {
	wsURL, err := homeAssistantEndpointURL(origin, "/api/websocket")
	if err != nil {
		return nil, err
	}
	wsURL = "ws" + strings.TrimPrefix(wsURL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return nil, errors.New("home_assistant_connector_session_unavailable")
	}
	conn.SetReadLimit(homeAssistantWSLimit)
	socket := &haConnectorSocket{ctx: ctx, conn: conn}
	var greeting homeAssistantWSGreeting
	if wsjson.Read(ctx, conn, &greeting) != nil || greeting.Type != "auth_required" || greeting.HAVersion != version {
		socket.close()
		return nil, errors.New("home_assistant_connector_version_changed")
	}
	if wsjson.Write(ctx, conn, map[string]string{"type": "auth", "access_token": token}) != nil {
		socket.close()
		return nil, errors.New("home_assistant_connector_auth_failed")
	}
	var auth homeAssistantWSAuthResult
	if wsjson.Read(ctx, conn, &auth) != nil || auth.Type != "auth_ok" || auth.HAVersion != version {
		socket.close()
		return nil, errors.New("home_assistant_connector_auth_failed")
	}
	return socket, nil
}

func (w *haConnectorSocket) close() { _ = w.conn.CloseNow() }

func (w *haConnectorSocket) command(kind string, fields map[string]any, result any) error {
	w.sequence++
	if fields == nil {
		fields = map[string]any{}
	}
	fields["id"], fields["type"] = w.sequence, kind
	if wsjson.Write(w.ctx, w.conn, fields) != nil {
		return ErrHomeAssistantConnectorUncertain
	}
	var reply homeAssistantWSResult
	if wsjson.Read(w.ctx, w.conn, &reply) != nil || reply.ID != w.sequence || reply.Type != "result" || !reply.Success {
		return ErrHomeAssistantConnectorUncertain
	}
	if result != nil && json.Unmarshal(reply.Result, result) != nil {
		return ErrHomeAssistantConnectorUncertain
	}
	return nil
}

func (w *haConnectorSocket) list() ([]haConnectorToken, error) {
	var tokens []haConnectorToken
	err := w.command("auth/refresh_tokens", nil, &tokens)
	if err == nil && tokens == nil {
		err = ErrHomeAssistantConnectorUncertain
	}
	return tokens, err
}

// issue creates one long-lived access token of the fixed lifetime. A transport
// error is uncertain and is never repeated under the same name.
func (w *haConnectorSocket) issue(name string) (string, error) {
	var token string
	if err := w.command("auth/long_lived_access_token", map[string]any{"client_name": name, "lifespan": homeAssistantConnectorDays}, &token); err != nil {
		return "", err
	}
	if strings.TrimSpace(token) == "" || len(token) > 4096 {
		return "", ErrHomeAssistantConnectorUncertain
	}
	return token, nil
}

// deleteGeneration deletes the token of one generation, if it exists, and
// confirms it is gone.
func (w *haConnectorSocket) deleteGeneration(generation haConnectorGeneration) error {
	tokens, err := w.list()
	if err != nil {
		return err
	}
	matched, err := matchHomeAssistantToken(tokens, generation)
	if err != nil || matched == nil {
		return err
	}
	if err := w.command("auth/delete_refresh_token", map[string]any{"refresh_token_id": matched.ID}, nil); err != nil {
		return err
	}
	if tokens, err = w.list(); err != nil {
		return err
	}
	if matched, err = matchHomeAssistantToken(tokens, generation); err != nil || matched != nil {
		return ErrHomeAssistantConnectorUncertain
	}
	return nil
}

// matchHomeAssistantToken finds the one token of a generation: its client
// name, a long-lived access token created inside the generation's window and,
// once known, its exact ID and creation time.
func matchHomeAssistantToken(tokens []haConnectorToken, generation haConnectorGeneration) (*haConnectorToken, error) {
	var found *haConnectorToken
	for _, token := range tokens {
		if token.Name != generation.ClientName {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, token.Created)
		if found != nil || !haConnectorID.MatchString(token.ID) || token.Type != "long_lived_access_token" || err != nil ||
			created.Before(generation.IntentAt) || created.After(generation.ExpiresAt) ||
			(generation.TokenID != "" && (token.ID != generation.TokenID || token.Created != generation.TokenCreatedAt)) {
			return nil, errors.New("home_assistant_connector_token_identity_ambiguous")
		}
		copy := token
		found = &copy
	}
	return found, nil
}

func newHomeAssistantConnectorGeneration(tokens []haConnectorToken, now time.Time) (haConnectorGeneration, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return haConnectorGeneration{}, err
	}
	name := "kombify-agent-" + hex.EncodeToString(nonce[:])
	for _, token := range tokens {
		if token.Name == name {
			return haConnectorGeneration{}, errors.New("home_assistant_connector_token_identity_ambiguous")
		}
	}
	now = now.UTC()
	return haConnectorGeneration{ClientName: name, IntentAt: now, ExpiresAt: now.Add(HomeAssistantConnectorLifetime)}, nil
}

// issueGeneration issues, finds and verifies the token of a generation whose
// intent is already durable.
func (s *haConnectorSession) issueGeneration(ctx context.Context, client *http.Client, ws *haConnectorSocket, generation haConnectorGeneration) (haConnectorGeneration, error) {
	token, err := ws.issue(generation.ClientName)
	if err != nil {
		return generation, err
	}
	tokens, err := ws.list()
	if err != nil {
		return generation, err
	}
	issued, err := matchHomeAssistantToken(tokens, generation)
	if err != nil {
		return generation, err
	}
	if issued == nil {
		return generation, ErrHomeAssistantConnectorUncertain
	}
	generation.TokenID, generation.TokenCreatedAt, generation.Token = issued.ID, issued.Created, token
	observed, version, err := readHomeAssistantCurrentUser(ctx, client, s.record.Origin, token, s.record.Version)
	if err != nil || !haConnectorOwnerMatches(observed, s.record.User) || version != s.record.Version {
		return generation, ErrHomeAssistantConnectorUncertain
	}
	return generation, nil
}

// issueFirst enrolls the first credential. The durable intent precedes the
// only token-creation effect; an uncertain issuance is revoked by the next
// call, never repeated.
func (s *haConnectorSession) issueFirst(ctx context.Context, client *http.Client, ws *haConnectorSocket, tokens []haConnectorToken) error {
	generation, err := newHomeAssistantConnectorGeneration(tokens, time.Now())
	if err != nil {
		return err
	}
	s.record.haConnectorGeneration, s.record.State = generation, "issuing"
	if err := s.save(); err != nil {
		return err
	}
	issued, err := s.issueGeneration(ctx, client, ws, generation)
	s.record.TokenID, s.record.TokenCreatedAt = issued.TokenID, issued.TokenCreatedAt
	if err != nil {
		return err
	}
	s.record.haConnectorGeneration, s.record.State = issued, "active"
	return s.save()
}

// maintain settles and rotates the active credential over a session of its
// own token.
func (s *haConnectorSession) maintain(ctx context.Context, client *http.Client) error {
	ctx, cancel := context.WithTimeout(ctx, homeAssistantWebSocketTimeout)
	defer cancel()
	ws, err := dialHomeAssistantConnector(ctx, client, s.record.Origin, s.record.Version, s.record.Token)
	if err != nil {
		return err
	}
	defer ws.close()
	return s.maintainWith(ctx, client, ws, nil)
}

// maintainWith first deletes the token of an interrupted rotation (its value
// was never stored, so it is never used) and a retired token, then rotates at
// half-life: a new generation under a durable intent, issued and verified,
// stored as current, and only then the old token deleted.
func (s *haConnectorSession) maintainWith(ctx context.Context, client *http.Client, ws *haConnectorSocket, tokens []haConnectorToken) error {
	for _, pending := range []**haConnectorGeneration{&s.record.Next, &s.record.Retired} {
		if *pending == nil {
			continue
		}
		if err := ws.deleteGeneration(**pending); err != nil {
			return err
		}
		*pending = nil
		if err := s.save(); err != nil {
			return err
		}
	}
	now := time.Now()
	if now.Before(s.record.IntentAt.Add(HomeAssistantConnectorLifetime / 2)) {
		return nil
	}
	if tokens == nil {
		listed, err := ws.list()
		if err != nil {
			return err
		}
		tokens = listed
	}
	next, err := newHomeAssistantConnectorGeneration(tokens, now)
	if err != nil {
		return err
	}
	s.record.Next = &next
	if err := s.save(); err != nil {
		return err
	}
	issued, err := s.issueGeneration(ctx, client, ws, next)
	if err != nil {
		return err
	}
	retired := s.record.haConnectorGeneration
	retired.Token = ""
	s.record.haConnectorGeneration, s.record.Next, s.record.Retired = issued, nil, &retired
	if err := s.save(); err != nil {
		return err
	}
	fresh, err := dialHomeAssistantConnector(ctx, client, s.record.Origin, s.record.Version, issued.Token)
	if err != nil {
		return nil // the retired token is deleted on the next call
	}
	defer fresh.close()
	if err := fresh.deleteGeneration(retired); err != nil {
		return nil //nolint:nilerr // the retired token stays recorded and is deleted on the next call
	}
	s.record.Retired = nil
	return s.save()
}

// CurrentHomeAssistantConnectorEvidence is Owner-signed issuer evidence of
// the credential generation the node holds now (its expiry moves later with
// every rotation); a receipt names the generation of setup.
func CurrentHomeAssistantConnectorEvidence(request HomeAssistantConnectorRequest, origin, version string) (*HomeAssistantConnectorEvidence, error) {
	origin, err := normalizeHomeAssistantBaseURL(origin)
	if err != nil {
		return nil, err
	}
	s, err := prepareHomeAssistantConnector(&request, origin, version)
	if err != nil {
		return nil, err
	}
	defer s.close()
	if !s.exists || s.record.State != "active" {
		return nil, ErrHomeAssistantConnectorRevoked
	}
	return s.evidence()
}
