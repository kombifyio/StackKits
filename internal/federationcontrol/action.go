// Package federationcontrol binds the existing StackKits server to Home-issued
// Federation actions. It owns no fabric, provider lifecycle or identity issuer.
package federationcontrol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const ActionSchema = "stackkit.federation-remote-action/v1"

var (
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	tokenPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)
	noncePattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Action is the closed CUE remote-action envelope. Approval is preserved for
// the separate Home human-authentication verifier; this slice admits no mutation.
type Action struct {
	Schema              string                                  `json:"schema"`
	Action              string                                  `json:"action"`
	Nonce               string                                  `json:"nonce"`
	IdempotencyKey      string                                  `json:"idempotencyKey"`
	PlanHash            string                                  `json:"planHash"`
	OwnerRef            string                                  `json:"ownerRef"`
	HomeSiteRef         string                                  `json:"homeSiteRef"`
	TargetSiteRef       string                                  `json:"targetSiteRef"`
	TargetNodeRef       string                                  `json:"targetNodeRef"`
	ExecutionChannelRef string                                  `json:"executionChannelRef"`
	IssuedAt            time.Time                               `json:"issuedAt"`
	ExpiresAt           time.Time                               `json:"expiresAt"`
	Approval            json.RawMessage                         `json:"approval,omitempty"`
	Signature           localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func DecodeAction(raw []byte) (Action, error) {
	var a Action
	if len(raw) == 0 || len(raw) > 64<<10 {
		return a, errors.New("control: action exceeds bounded envelope")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&a); err != nil {
		return a, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return a, errors.New("control: trailing action input")
	}
	return a, nil
}

func (a Action) signingBytes() []byte {
	a.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(a)
	return raw
}

// Digest excludes the approval and signature so human approval binds the action
// independently of its receipt. The action signature includes the approval.
func (a Action) Digest() string {
	a.Approval = nil
	sum := sha256.Sum256(a.signingBytes())
	return "sha256:" + hex.EncodeToString(sum[:])
}

func SignAction(root string, a Action) (Action, error) {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return a, err
	}
	if a.OwnerRef != owner.OwnerRef || a.HomeSiteRef != owner.Binding.SiteRef {
		return a, errors.New("control: signer is not the selected Home authority")
	}
	a.Schema = ActionSchema
	a.IssuedAt, a.ExpiresAt = a.IssuedAt.UTC(), a.ExpiresAt.UTC()
	if a.Nonce == "" {
		var nonce [32]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return a, err
		}
		a.Nonce = hex.EncodeToString(nonce[:])
	}
	if err := validateAction(a, time.Now().UTC()); err != nil {
		return a, err
	}
	a.Signature, err = localevidence.SignRemoteAction(root, a.signingBytes())
	return a, err
}

func validateAction(a Action, now time.Time) error {
	if a.Schema != ActionSchema || !noncePattern.MatchString(a.Nonce) || !tokenPattern.MatchString(a.IdempotencyKey) || !digestPattern.MatchString(a.PlanHash) {
		return errors.New("control: exact action, nonce, idempotency and plan binding required")
	}
	for _, ref := range []string{a.OwnerRef, a.HomeSiteRef, a.TargetSiteRef, a.TargetNodeRef, a.ExecutionChannelRef} {
		if !tokenPattern.MatchString(ref) {
			return errors.New("control: canonical owner and target refs required")
		}
	}
	maxTTL := time.Minute
	if a.Action == "destroy" {
		maxTTL = 30 * time.Second
	}
	if a.Action != "plan" && a.Action != "verify" && a.Action != "apply" && a.Action != "destroy" {
		return errors.New("control: unsupported action")
	}
	if a.IssuedAt.IsZero() || a.IssuedAt.Location() != time.UTC || a.ExpiresAt.Location() != time.UTC || a.IssuedAt.After(now) || !a.ExpiresAt.After(now) || a.ExpiresAt.Sub(a.IssuedAt) > maxTTL {
		return errors.New("control: action expired or outside its bounded lifetime")
	}
	return nil
}

func authorizeAction(a Action, trust HomeTrust, contract architecturev2renderer.FederationControlAgentAction, now time.Time) error {
	if err := validateAction(a, now); err != nil {
		return err
	}
	if a.Action != "plan" && a.Action != "verify" {
		return errors.New("control: mutation capability unavailable until Home step-up verification is bound")
	}
	if len(a.Approval) != 0 || contract.ID != a.Action || contract.Transport != "mtls-agent" || contract.IssuerRef != "home-workload-issuer" || contract.Audience != "stackkit-workload" || contract.Destructive || contract.ApprovalReceiptRequired || contract.ApprovalClass != "none" || !contract.RequiresSignedActions || !contract.RequiresNonce || !contract.RequiresIdempotencyKey || !contract.RequiresResolvedPlanHash || !contract.ReplayProtection || !contract.CapabilityScopedActions || a.ExpiresAt.Sub(a.IssuedAt) > time.Duration(contract.MaxTTLSeconds)*time.Second {
		return errors.New("control: action differs from CUE remote-action authority")
	}
	if a.OwnerRef != trust.OwnerRef || a.HomeSiteRef != trust.HomeSiteRef {
		return errors.New("control: foreign Home action")
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(trust.PublicKey)
	if err != nil {
		return err
	}
	return localevidence.VerifyRemoteAction(a.signingBytes(), a.Signature, trust.OwnerRef, trust.KeyID, ed25519.PublicKey(key))
}
