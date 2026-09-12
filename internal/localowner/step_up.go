package localowner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const StepUpClientID = "stackkits-owner-step-up"

// RemoteActionApprovalBinding is supplied by the admitted action receiver, never
// copied from the receipt. ActionDigest covers the unsigned action envelope,
// including its nonce and idempotency key, but excludes the approval itself.
type RemoteActionApprovalBinding struct {
	ActionDigest  string    `json:"actionDigest"`
	PlanHash      string    `json:"planHash"`
	Action        string    `json:"action"`
	OwnerRef      string    `json:"ownerRef"`
	HomeSiteRef   string    `json:"homeSiteRef"`
	TargetSiteRef string    `json:"targetSiteRef"`
	TargetNodeRef string    `json:"targetNodeRef"`
	IssuedAt      time.Time `json:"issuedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// RemoteActionApproval preserves the actual PocketID signature. A lifecycle
// owner-key signature cannot replace this token or satisfy human approval.
// Treat this structure as a credential: never put it in logs or public receipts.
type RemoteActionApproval struct {
	IDToken string `json:"idToken"`
}

// StepUpTrust must come from admitted Home identity custody. A caller must never
// accept issuer, subject, or keys supplied alongside an untrusted approval.
// Remote consumers also need a current admitted client/owner projection: offline
// verification alone cannot discover a disabled owner or changed client policy.
type StepUpTrust struct {
	Issuer      string
	Subject     string
	OwnerRef    string
	HomeSiteRef string
	Keys        jose.JSONWebKeySet
}

var ErrStepUpRejected = errors.New("localowner: current PocketID owner approval required")

func StepUpNonce(binding RemoteActionApprovalBinding) (string, error) {
	if !isApprovalDigest(binding.ActionDigest) || !isApprovalDigest(binding.PlanHash) ||
		strings.TrimSpace(binding.OwnerRef) == "" || strings.TrimSpace(binding.HomeSiteRef) == "" ||
		strings.TrimSpace(binding.TargetSiteRef) == "" || strings.TrimSpace(binding.TargetNodeRef) == "" {
		return "", ErrStepUpRejected
	}
	limit := 60 * time.Second
	if binding.Action == "destroy" {
		limit = 30 * time.Second
	} else if binding.Action != "apply" {
		return "", ErrStepUpRejected
	}
	if binding.IssuedAt.IsZero() || !binding.ExpiresAt.After(binding.IssuedAt) || binding.ExpiresAt.Sub(binding.IssuedAt) > limit {
		return "", ErrStepUpRejected
	}
	// Normalize timestamps before hashing so equivalent timezone representations
	// cannot create a second replay identity for the same action.
	binding.IssuedAt = binding.IssuedAt.UTC()
	binding.ExpiresAt = binding.ExpiresAt.UTC()
	body, err := json.Marshal(binding)
	if err != nil {
		return "", ErrStepUpRejected
	}
	digest := sha256.Sum256(append([]byte("stackkit.owner-step-up/v1\x00"), body...))
	return hex.EncodeToString(digest[:]), nil
}

// VerifyRemoteActionApprovalClaims verifies the independent human signature.
// It performs no mutation. The executor must consume the approval durably before
// its first side effect, using ConsumeRemoteActionApproval or its existing replay
// journal, after all action, device, target and current Home-trust admission.
func VerifyRemoteActionApprovalClaims(receipt json.RawMessage, expected RemoteActionApprovalBinding, trust StepUpTrust, now time.Time) error {
	nonce, err := StepUpNonce(expected)
	if err != nil || now.Before(expected.IssuedAt) || !now.Before(expected.ExpiresAt) || trust.OwnerRef != expected.OwnerRef || trust.HomeSiteRef != expected.HomeSiteRef || trust.Subject == "" || !strings.HasPrefix(trust.Issuer, "https://") {
		return ErrStepUpRejected
	}
	var approval RemoteActionApproval
	if len(receipt) > 32768 {
		return ErrStepUpRejected
	}
	decoder := json.NewDecoder(bytes.NewReader(receipt))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&approval) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrStepUpRejected
	}
	token, err := jwt.ParseSigned(approval.IDToken, []jose.SignatureAlgorithm{jose.RS256, jose.ES256, jose.EdDSA})
	if err != nil || len(token.Headers) != 1 {
		return ErrStepUpRejected
	}
	header := token.Headers[0]
	switch header.Algorithm {
	case "RS256", "ES256", "EdDSA":
	default:
		return ErrStepUpRejected
	}
	if header.KeyID == "" {
		return ErrStepUpRejected
	}
	keys := trust.Keys.Key(header.KeyID)
	if len(keys) != 1 || !keys[0].IsPublic() || (keys[0].Algorithm != "" && keys[0].Algorithm != header.Algorithm) || (keys[0].Use != "" && keys[0].Use != "sig") {
		return ErrStepUpRejected
	}
	var claims struct {
		jwt.Claims
		Nonce string   `json:"nonce"`
		AMR   []string `json:"amr"`
		Type  string   `json:"type"`
	}
	if token.Claims(keys[0].Key, &claims) != nil || claims.Issuer != trust.Issuer || claims.Subject != trust.Subject ||
		len(claims.Audience) != 1 || claims.Audience[0] != StepUpClientID || claims.Nonce != nonce ||
		claims.Type != "id-token" || len(claims.AMR) != 1 || claims.AMR[0] != "phr" ||
		claims.IssuedAt == nil || claims.Expiry == nil || claims.IssuedAt.Time().After(now) ||
		claims.IssuedAt.Time().Before(expected.IssuedAt.Truncate(time.Second)) || !now.Before(claims.Expiry.Time()) ||
		(claims.NotBefore != nil && now.Before(claims.NotBefore.Time())) {
		return ErrStepUpRejected
	}
	return nil
}

// ConsumeRemoteActionApproval is a cross-process no-replace replay journal in
// existing local custody. A crash after consumption leaves the approval spent;
// resume through the executor's existing operation journal, never replay it.
func ConsumeRemoteActionApproval(workspaceRoot string, receipt json.RawMessage, expected RemoteActionApprovalBinding, trust StepUpTrust, now time.Time) error {
	if err := VerifyRemoteActionApprovalClaims(receipt, expected, trust, now); err != nil {
		return err
	}
	nonce, _ := StepUpNonce(expected)
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err = tx.MkdirAll(".stackkit/custody/owner-step-up-consumed", 0700); err != nil {
		_ = tx.Close()
		return err
	}
	if err = tx.Close(); err != nil {
		return err
	}
	view, err := root.View(".stackkit/custody/owner-step-up-consumed")
	if err != nil {
		return err
	}
	// Store only the action identity and expiry, never the bearer credential.
	body, _ := json.Marshal(struct {
		ActionDigest string    `json:"actionDigest"`
		ExpiresAt    time.Time `json:"expiresAt"`
	}{expected.ActionDigest, expected.ExpiresAt})
	_, err = view.WriteAtomic0600NoReplace(nonce+".json", body)
	return err
}

func isApprovalDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
