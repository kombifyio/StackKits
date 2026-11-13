package appsetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The Home Assistant connector credential (homelab identity target §10): a
// rotating credential bound to the Guard identity of the node, not a 30-day
// bearer. Home Assistant knows only long-lived access tokens, so the issuer
// gives each one the fixed short lifetime below and replaces it at half-life:
// the new token is issued under a durable intent, verified, stored, and only
// then is the old one deleted. The record binds the Guard identity that the
// node reads from its own Guard enrollment; another Guard identity cannot open
// the credential, and a caller never chooses a lifetime.
const (
	HomeAssistantConnectorLifetime = 7 * 24 * time.Hour
	homeAssistantConnectorDays     = 7
	homeAssistantConnectorSchema   = "stackkit.home-assistant-connector/v2"
	// homeAssistantConnectorLegacySchema is the retired 30-day-bearer record.
	homeAssistantConnectorLegacySchema = "stackkit.home-assistant-connector/v1"
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
	// ensure creates once and rotates; revoke retains a tombstone and never
	// re-registers.
	Action string `json:"action"`
}

// Evidence never contains an endpoint or provider token. It does not attest
// deployment identity, MCP compatibility, agent equipment or cloud admission.
// ExpiresAt is the expiry of the credential generation current at the
// receipt; rotation only moves it later.
type HomeAssistantConnectorEvidence struct {
	OwnerRef         string                                  `json:"ownerRef"`
	HAUserID         string                                  `json:"haUserId"`
	HAVersion        string                                  `json:"haVersion"`
	RequestedBinding HomeAssistantConnectorBinding           `json:"requestedBinding"`
	GuardIdentity    string                                  `json:"guardIdentity"`
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

// haConnectorGeneration is one generation of the credential.
type haConnectorGeneration struct {
	ClientName     string    `json:"clientName"`
	IntentAt       time.Time `json:"intentAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	TokenID        string    `json:"tokenId,omitempty"`
	TokenCreatedAt string    `json:"tokenCreatedAt,omitempty"`
	Token          string    `json:"token,omitempty"`
}

type haConnectorRecord struct {
	Schema  string                        `json:"schema"`
	Owner   string                        `json:"owner"`
	Binding HomeAssistantConnectorBinding `json:"binding"`
	Origin  string                        `json:"origin"`
	Version string                        `json:"version"`
	User    string                        `json:"user"`
	Guard   string                        `json:"guard"`
	State   string                        `json:"state"`
	haConnectorGeneration
	// Next is a rotation whose new token may exist but was not stored yet;
	// Retired is a replaced token whose deletion was not confirmed yet.
	Next    *haConnectorGeneration `json:"next,omitempty"`
	Retired *haConnectorGeneration `json:"retired,omitempty"`
}

type haConnectorSession struct {
	request HomeAssistantConnectorRequest
	ref     string
	guard   string
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
		!homeAssistantVersionAdmitted(version) {
		return nil, errors.New("home_assistant_connector_binding_invalid")
	}
	owner, err := localevidence.LoadOwnerCustody(request.Workspace)
	if err != nil || owner.Binding != b.Node {
		return nil, errors.New("home_assistant_connector_node_owner_required")
	}
	guard, guardErr := localGuardIdentity()
	if guardErr != nil && request.Action == "ensure" {
		return nil, guardErr
	}
	identity, _ := json.Marshal(struct {
		Owner, Resource string
		Node            localevidence.LocalBinding
	}{owner.OwnerRef, b.ResourceID, b.Node})
	digest := sha256.Sum256(identity)
	id := hex.EncodeToString(digest[:])
	s := &haConnectorSession{request: *request, ref: "secret:local/home-assistant-connector/" + id, guard: guard,
		record: haConnectorRecord{Schema: homeAssistantConnectorSchema, Owner: owner.OwnerRef, Binding: b, Origin: origin, Version: version, Guard: guard}}
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
	if json.Unmarshal(raw, &retained) != nil {
		return nil, errors.New("home_assistant_connector_custody_invalid")
	}
	// A record of the retired v1 schema (caller-chosen lifespan of up to 365
	// days) is opened only to revoke it: revoke deletes its token and keeps a
	// v2 tombstone. It is never used or rotated.
	legacy := retained.Schema == homeAssistantConnectorLegacySchema
	if legacy && request.Action != "revoke" {
		return nil, errors.New("home_assistant_connector_legacy_record: this connector holds a retired v1 credential; revoke it, then enroll again")
	}
	if (retained.Schema != s.record.Schema && !legacy) || retained.Owner != owner.OwnerRef || retained.Binding != b || retained.Origin != origin || retained.Version != version || !haConnectorID.MatchString(retained.User) || retained.ClientName == "" || retained.IntentAt.IsZero() || (!legacy && retained.ExpiresAt.Sub(retained.IntentAt) != HomeAssistantConnectorLifetime) {
		return nil, errors.New("home_assistant_connector_binding_changed")
	}
	retained.Schema = homeAssistantConnectorSchema
	if retained.State != "issuing" && retained.State != "active" && retained.State != "revoking" && retained.State != "revoked" {
		return nil, errors.New("home_assistant_connector_custody_invalid")
	}
	// The credential belongs to the Guard identity it was issued for. Revoking
	// it stays possible under another one; opening or rotating it does not.
	if request.Action == "ensure" && retained.State != "revoked" && retained.Guard != guard {
		return nil, errors.New("home_assistant_connector_guard_changed: the credential is bound to another Guard identity; revoke it and enroll again")
	}
	s.record, s.exists = retained, true
	return s, nil
}

func (s *haConnectorSession) close() {
	s.record.Token = ""
	if s.record.Next != nil {
		s.record.Next.Token = ""
	}
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
	evidence := &HomeAssistantConnectorEvidence{OwnerRef: s.record.Owner, HAUserID: s.record.User, HAVersion: s.record.Version, RequestedBinding: s.record.Binding,
		GuardIdentity: s.record.Guard, SecretRef: s.ref, Status: s.record.State, ObservedAt: time.Now().UTC(), ExpiresAt: s.record.ExpiresAt}
	payload, _ := json.Marshal(evidence)
	signature, err := localevidence.SignOwnerPolicyState(s.request.Workspace, payload)
	if err != nil {
		return nil, errors.New("home_assistant_connector_receipt_unsigned")
	}
	evidence.Signature = signature
	return evidence, nil
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
	ws, err := dialHomeAssistantConnector(ctx, client, s.record.Origin, s.record.Version, ownerToken)
	if err != nil {
		return nil, err
	}
	defer ws.close()
	tokens, err := ws.list()
	if err != nil {
		return nil, err
	}
	if !s.exists {
		if s.request.Action == "revoke" {
			return nil, errors.New("home_assistant_connector_not_enrolled")
		}
		if err := s.issueFirst(ctx, client, ws, tokens); err != nil {
			return nil, err
		}
		return s.evidence()
	}
	matched, err := matchHomeAssistantToken(tokens, s.record.haConnectorGeneration)
	if err != nil {
		return nil, err
	}
	if s.request.Action == "revoke" || s.record.State == "issuing" || s.record.State == "revoking" || !time.Now().Before(s.record.ExpiresAt) {
		return s.revoke(ws, matched)
	}
	if matched == nil {
		// The current token is gone: revoke the whole record, so a pending
		// or retired token of it does not stay alive either.
		return s.revoke(ws, nil)
	}
	if err := s.maintainWith(ctx, client, ws, tokens); err != nil {
		return nil, err
	}
	observed, version, err := readHomeAssistantCurrentUser(ctx, client, s.record.Origin, s.record.Token, s.record.Version)
	if err != nil || !haConnectorOwnerMatches(observed, user) || version != s.record.Version {
		return nil, errors.New("home_assistant_connector_credential_no_longer_valid")
	}
	return s.evidence()
}

// revoke deletes the current and every pending or retired token and retains
// the revocation.
func (s *haConnectorSession) revoke(ws *haConnectorSocket, matched *haConnectorToken) (*HomeAssistantConnectorEvidence, error) {
	s.record.State, s.record.Token = "revoking", ""
	if matched != nil {
		s.record.TokenID, s.record.TokenCreatedAt = matched.ID, matched.Created
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	for _, generation := range []*haConnectorGeneration{&s.record.haConnectorGeneration, s.record.Next, s.record.Retired} {
		if generation == nil {
			continue
		}
		if err := ws.deleteGeneration(*generation); err != nil {
			return nil, err
		}
	}
	s.record.State, s.record.Next, s.record.Retired = "revoked", nil, nil
	if err := s.save(); err != nil {
		return nil, err
	}
	if s.request.Action == "ensure" {
		return nil, ErrHomeAssistantConnectorRevoked
	}
	return s.evidence()
}

// WithHomeAssistantConnectorCredential opens an active node credential only
// for a local consumer on the Guard identity it is bound to, under the same
// lifecycle lock and exact original binding, and rotates it at half-life
// first. `setup connector --verify-only` calls it too, and is the periodic
// driver of the rotation until the local consumer exists: verification still
// issues no first credential, revokes nothing, changes no binding, owner or
// state, and rotation moves only the expiry later under the same durable
// intent rules, so the retained receipt stays current. The "read-only" HTTP
// session of verification means no Compose identity mutation of the
// application, which rotation does not need either. The consumer still owes fresh Gateway lease/relay authorization;
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
	if !s.exists || s.record.State != "active" || s.record.Token == "" || !time.Now().Before(s.record.ExpiresAt) || s.record.Guard != s.guard {
		return ErrHomeAssistantConnectorRevoked
	}
	client = cloneHomeAssistantHTTPClient(client)
	if err := s.maintain(ctx, client); err != nil {
		return err
	}
	observed, observedVersion, err := readHomeAssistantCurrentUser(ctx, client, origin, s.record.Token, version)
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
// A retained revocation cannot be softened by an older active receipt; an
// active receipt stays current across rotations of the same credential.
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
	if !s.exists || (s.record.State != "active" && s.record.State != "revoked") || s.record.State != evidence.Status || s.record.Owner != evidence.OwnerRef ||
		s.record.User != evidence.HAUserID || s.record.Version != evidence.HAVersion || s.ref != evidence.SecretRef || s.record.Binding != evidence.RequestedBinding ||
		s.record.Guard != evidence.GuardIdentity || s.record.ExpiresAt.Before(evidence.ExpiresAt) ||
		(s.record.State == "active" && (!time.Now().Before(s.record.ExpiresAt) || s.record.Guard != s.guard)) {
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
