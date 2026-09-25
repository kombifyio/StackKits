package localevidence

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const (
	// MemberCustodyRelPath is the verify-only custody of a Fleet member host.
	// A workspace holds either this record or the Home owner custody, never both.
	MemberCustodyRelPath = ".stackkit/custody/member.json"

	// ReasonMemberCustodyVerifyOnly is the stable denial reason for an
	// enrollment, signing, or Owner-approval operation on a member host.
	ReasonMemberCustodyVerifyOnly = "member_custody_verify_only"

	memberAdmissionDomain = "stackkit.owner-member-admission/v1\x00"
)

// MemberSigningDenial reports that the workspace belongs to a verify-only
// Fleet member. It matches ErrOwnerCustodyMissing so every existing caller
// keeps treating the workspace as ownerless, while a caller that renders
// guidance can classify the exact reason.
type MemberSigningDenial struct {
	Binding LocalBinding
}

func (e *MemberSigningDenial) Error() string {
	return fmt.Sprintf(
		"member custody denied: this workspace is the verify-only Fleet member %s/%s/%s; enrollment, signing, and Owner approval stay at the Home ControlAuthority",
		e.Binding.SiteRef, e.Binding.NodeRef, e.Binding.ChannelRef,
	)
}

// Is keeps errors.Is(err, ErrOwnerCustodyMissing) true for a member workspace.
func (e *MemberSigningDenial) Is(target error) bool {
	return target == ErrOwnerCustodyMissing
}

// Guidance names the recovery path for the denied operation.
func (e *MemberSigningDenial) Guidance() []string {
	return []string{
		"Run the enrollment, signing, or approval operation on the Foundation Node that holds the Home owner custody.",
		"Bring the result to this member host through a new Owner-signed member admission.",
	}
}

type memberCustodyMarker struct {
	APIVersion string       `json:"apiVersion"`
	Binding    LocalBinding `json:"localBinding"`
}

// memberCustodyDenial returns the typed denial when the workspace carries a
// member custody record. The record's integrity is verified by its owning
// package; this check only decides that owner custody must not be minted or
// used here.
func memberCustodyDenial(workspaceRoot string) (*MemberSigningDenial, error) {
	path, err := confinedCustodyPath(workspaceRoot, MemberCustodyRelPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path below the explicit workspace
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("localevidence: read member custody: %w", err)
	}
	var marker memberCustodyMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return nil, fmt.Errorf("localevidence: decode member custody: %w", err)
	}
	return &MemberSigningDenial{Binding: marker.Binding}, nil
}

// PersistMemberCustody installs one member custody record. It refuses a
// workspace that already holds Home owner custody or its signing key, so a
// Foundation Node cannot silently turn into a member of its own StackInstance.
func PersistMemberCustody(workspaceRoot string, record any) error {
	if _, err := LoadOwnerKey(workspaceRoot); !errors.Is(err, ErrOwnerKeyMissing) {
		if err != nil {
			return err
		}
		return errors.New("localevidence: this workspace holds Home owner signing custody and cannot join as a member")
	}
	path, err := confinedCustodyPath(workspaceRoot, ownerCustodyRelPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("localevidence: this workspace holds Home owner custody and cannot join as a member")
	}
	memberPath, err := confinedCustodyPath(workspaceRoot, MemberCustodyRelPath)
	if err != nil {
		return err
	}
	if err := writePrivateJSON(memberPath, record); err != nil {
		return fmt.Errorf("localevidence: persist member custody: %w", err)
	}
	return nil
}

// ReadMemberCustody returns the raw member custody record, or os.ErrNotExist.
func ReadMemberCustody(workspaceRoot string) ([]byte, error) {
	path, err := confinedCustodyPath(workspaceRoot, MemberCustodyRelPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path below the explicit workspace
	if err != nil {
		return nil, err
	}
	if err := requireFilePrivateToCurrentUser(path); err != nil {
		return nil, err
	}
	return raw, nil
}

// OwnerVerificationKey returns the public Home owner key a member pins. It
// never exposes private key material.
func OwnerVerificationKey(workspaceRoot string) (ownerRef, keyID string, public ed25519.PublicKey, err error) {
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return "", "", nil, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return "", "", nil, err
	}
	if owner.OwnerRef != key.OwnerRef || owner.KeyID != key.KeyID {
		return "", "", nil, errors.New("localevidence: owner verification key differs from established owner")
	}
	return owner.OwnerRef, owner.KeyID, key.Public(), nil
}

// SignOwnerMemberAdmission signs one exact member admission with the Home
// owner key under a dedicated domain, so it cannot be replayed as any other
// Owner-signed document.
func SignOwnerMemberAdmission(workspaceRoot string, canonical []byte) (OwnerPolicyStateSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(workspaceRoot, canonical, memberAdmissionDomain, "member admission")
	return OwnerPolicyStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, err
}

// VerifyMemberAdmission verifies a member admission against the public Home
// key the member pinned. The key ID must be derived from that exact key.
func VerifyMemberAdmission(canonical []byte, signature OwnerPolicyStateSignature, ownerRef, keyID string, public ed25519.PublicKey) error {
	if !verifyPinnedOwnerSignature(memberAdmissionDomain, canonical, signature, ownerRef, keyID, public) {
		return errors.New("localevidence: member admission does not verify against the pinned Home owner key")
	}
	return nil
}
