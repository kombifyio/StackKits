package offlinerecovery

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/contracts/canonicaljson"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const revocationRelativePath = ".stackkit/offline-recovery/revocations.json"

type custodySnapshot struct {
	OwnerRef   string
	OwnerKeyID string
	SiteRef    string
	NodeRef    string
	ChannelRef string
	Digest     string
}

func loadCustody(workspaceRoot string) (custodySnapshot, error) {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return custodySnapshot{}, deny("node_custody_unavailable", "the established local Owner custody cannot be loaded", "restore the existing node Owner custody; offline recovery cannot create or replace it")
	}
	key, err := localevidence.LoadOwnerKey(workspaceRoot)
	if err != nil {
		return custodySnapshot{}, deny("node_custody_unavailable", "the local Owner policy key cannot be loaded", "restore the existing node Owner key before recovery")
	}
	if owner.OwnerRef != key.OwnerRef || owner.KeyID != key.KeyID {
		return custodySnapshot{}, deny("node_custody_mismatch", "the local Owner custody and policy key disagree", "restore the matching established Owner custody and key")
	}
	binding := struct {
		OwnerRef   string `json:"ownerRef"`
		OwnerKeyID string `json:"ownerKeyId"`
		SiteRef    string `json:"siteRef"`
		NodeRef    string `json:"nodeRef"`
		ChannelRef string `json:"channelRef"`
	}{owner.OwnerRef, owner.KeyID, owner.Binding.SiteRef, owner.Binding.NodeRef, owner.Binding.ChannelRef}
	raw, err := canonicalJSON(binding)
	if err != nil {
		return custodySnapshot{}, err
	}
	return custodySnapshot{
		OwnerRef: owner.OwnerRef, OwnerKeyID: owner.KeyID,
		SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef,
		ChannelRef: owner.Binding.ChannelRef, Digest: canonicaljson.SHA256Hex(raw),
	}, nil
}

func decodeAuthority(raw []byte) (Authority, error) {
	var authority Authority
	if err := decodeCanonical(raw, MaxAuthorityBytes, &authority); err != nil {
		return Authority{}, deny("authority_invalid", "the offline recovery authority is not bounded canonical JSON", "obtain a newly encoded authority from the local Owner policy authority")
	}
	if err := validateAuthorityShape(authority); err != nil {
		return Authority{}, err
	}
	return authority, nil
}

func verifyAuthorityBinding(
	workspaceRoot string,
	authority Authority,
	plan Plan,
	planDigest, artifactSetDigest string,
	custody custodySnapshot,
) error {
	if authority.PlanDigest != planDigest || authority.ArtifactSetDigest != artifactSetDigest {
		return deny("authority_plan_mismatch", "the offline authority does not bind the exact recovery plan and artifact set", "obtain a new authority for the verified plan and cached artifacts")
	}
	if authority.OperationID != plan.OperationID || authority.OwnerRef != plan.OwnerRef ||
		authority.SiteRef != plan.SiteRef || authority.NodeRef != plan.NodeRef ||
		authority.ChannelRef != plan.ChannelRef || authority.OwnerRef != custody.OwnerRef ||
		authority.OwnerKeyID != custody.OwnerKeyID || authority.SiteRef != custody.SiteRef ||
		authority.NodeRef != custody.NodeRef || authority.ChannelRef != custody.ChannelRef {
		return deny("node_custody_mismatch", "the offline authority, plan, and current node custody do not name the same operation and node", "obtain a new authority on this node after reconciling its established custody")
	}
	signingBytes, err := authority.SigningBytes()
	if err != nil {
		return err
	}
	if err := localevidence.VerifyOwnerPolicyState(workspaceRoot, signingBytes, authority.Signature); err != nil {
		return deny("authority_signature_invalid", "the offline authority signature does not verify against current local Owner custody", "obtain a newly signed authority from the established Owner; never bypass signature verification")
	}
	return nil
}

func admitAuthorityClock(authority Authority, received ClockReading) error {
	if err := validateClock(received); err != nil {
		return err
	}
	if !received.Wall.UTC().Before(authority.ExpiresAt) {
		return deny("authority_expired", "the offline recovery authority expired before admission", "obtain a new short-lived authority; cached artifacts alone are not authorization")
	}
	if authority.IssuedAt.After(received.Wall.UTC().Add(MaxClockSkew)) {
		return deny("clock_untrusted", "the local wall clock disagrees with the signed authority issue time", "repair the local clock, then obtain a new authority; do not extend the old one")
	}
	return nil
}

func checkAuthorityWindow(authority Authority, received, current ClockReading) error {
	if err := validateClock(current); err != nil {
		return err
	}
	wallElapsed := current.Wall.UTC().Sub(received.Wall.UTC())
	monoElapsed := current.Monotonic - received.Monotonic
	if wallElapsed < 0 || monoElapsed < 0 {
		return deny("clock_untrusted", "the local clock moved backwards during offline recovery", "stop and inspect the operation journal; obtain a new authority on a trusted continuing clock")
	}
	drift := wallElapsed - monoElapsed
	if drift > MaxClockSkew || drift < -MaxClockSkew {
		return deny("clock_untrusted", "wall and monotonic clock progress diverged during offline recovery", "stop and inspect the operation journal; do not use wall-clock rollback to extend authority")
	}
	remainingWall := authority.ExpiresAt.Sub(current.Wall.UTC())
	if remainingWall <= 0 || monoElapsed >= authority.ExpiresAt.Sub(received.Wall.UTC()) {
		return deny("authority_expired", "the offline recovery authority expired before the next local effect", "inspect the durable operation receipt and obtain a new authority before any new effect")
	}
	return nil
}

type revocation struct {
	AuthorityDigest string    `json:"authorityDigest,omitempty"`
	PlanDigest      string    `json:"planDigest,omitempty"`
	OwnerRef        string    `json:"ownerRef,omitempty"`
	OwnerKeyID      string    `json:"ownerKeyId,omitempty"`
	RevokedAt       time.Time `json:"revokedAt"`
}

type revocationDocument struct {
	SchemaVersion string       `json:"schemaVersion"`
	Revocations   []revocation `json:"revocations"`
}

func checkRevocation(workspaceRoot string, authority Authority, authorityDigest, planDigest string) error {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return deny("revocation_state_unavailable", "the local revocation projection cannot be opened", "restore readable local custody before recovery")
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return deny("revocation_state_unavailable", "the local revocation projection cannot be read stably", "restore readable local custody before recovery")
	}
	defer func() { _ = transaction.Close() }()
	raw, _, err := transaction.ReadStableBounded(revocationRelativePath, MaxRevocationBytes)
	if errors.Is(err, os.ErrNotExist) {
		// No observed revocation is admissible only inside the signed, bounded
		// authority window. The receipt states the remote unknown explicitly.
		return nil
	}
	if err != nil {
		return deny("revocation_state_unavailable", "the local revocation projection is unsafe, oversized, or unreadable", "repair the revocation projection; missing authority never means revoked authority is admissible")
	}
	var document revocationDocument
	if err := decodeCanonical(raw, MaxRevocationBytes, &document); err != nil ||
		document.SchemaVersion != RevocationSchemaVersion {
		return deny("revocation_state_invalid", "the local revocation projection is malformed", "restore a canonical current revocation projection before recovery")
	}
	previous := ""
	for _, entry := range document.Revocations {
		key := strings.Join([]string{entry.AuthorityDigest, entry.PlanDigest, entry.OwnerRef, entry.OwnerKeyID, entry.RevokedAt.Format(time.RFC3339)}, "\x00")
		if key <= previous || !canonicalUTC(entry.RevokedAt) ||
			(entry.AuthorityDigest != "" && !digestPattern.MatchString(entry.AuthorityDigest)) ||
			(entry.PlanDigest != "" && !digestPattern.MatchString(entry.PlanDigest)) ||
			(entry.OwnerRef != "" && !validOwnerRef(entry.OwnerRef)) ||
			(entry.OwnerKeyID != "" && !keyIDPattern.MatchString(entry.OwnerKeyID)) ||
			(entry.AuthorityDigest == "" && entry.PlanDigest == "" && entry.OwnerRef == "" && entry.OwnerKeyID == "") {
			return deny("revocation_state_invalid", "the local revocation projection contains an invalid or unsorted entry", "restore a canonical current revocation projection before recovery")
		}
		previous = key
		if (entry.AuthorityDigest != "" && entry.AuthorityDigest == authorityDigest) ||
			(entry.PlanDigest != "" && entry.PlanDigest == planDigest) ||
			(entry.OwnerRef != "" && entry.OwnerRef == authority.OwnerRef) ||
			(entry.OwnerKeyID != "" && entry.OwnerKeyID == authority.OwnerKeyID) {
			return deny("authority_revoked", "the offline recovery authority was revoked in local custody", "obtain a new authority after the revocation is resolved; never reuse the revoked operation")
		}
	}
	return nil
}
