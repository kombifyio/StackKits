package fleetmember

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	CustodyAPIVersion = "stackkit.local-member-custody/v1"
	CustodyKind       = "LocalMemberCustody"
)

// Custody is the verify-only record a member host keeps after joining. It
// holds no private key: its integrity is the embedded Owner-signed admission,
// re-verified against the pinned Home key on every load.
type Custody struct {
	APIVersion               string                     `json:"apiVersion"`
	Kind                     string                     `json:"kind"`
	Binding                  localevidence.LocalBinding `json:"localBinding"`
	Authority                localevidence.LocalBinding `json:"authorityBinding"`
	StackID                  string                     `json:"stackId"`
	FleetRef                 string                     `json:"fleetRef,omitempty"`
	PlanHash                 string                     `json:"planHash"`
	Grants                   Grants                     `json:"grants"`
	Verifier                 HomeVerifier               `json:"verifier"`
	VerifierDistributionRefs []string                   `json:"verifierDistributionRefs"`
	AdmissionDigest          string                     `json:"admissionDigest"`
	Admission                Admission                  `json:"admission"`
	JoinedAt                 time.Time                  `json:"joinedAt"`
}

// NewCustody projects a verified, plan-bound admission into member custody.
func NewCustody(verified Verified, joinedAt time.Time) Custody {
	admission := verified.Admission
	return Custody{
		APIVersion: CustodyAPIVersion, Kind: CustodyKind,
		Binding:   admission.Member.LocalBinding(),
		Authority: admission.Authority.LocalBinding(),
		StackID:   admission.StackID, FleetRef: admission.FleetRef, PlanHash: admission.PlanHash,
		Grants: Grants{}, Verifier: admission.Verifier,
		VerifierDistributionRefs: append([]string(nil), admission.VerifierDistributionRefs...),
		AdmissionDigest:          verified.Digest, Admission: admission,
		JoinedAt: joinedAt.UTC().Truncate(time.Second),
	}
}

// LoadCustody reads and re-verifies the member custody of a workspace. It
// returns os.ErrNotExist when the workspace is not a member.
func LoadCustody(workspaceRoot string) (Custody, error) {
	raw, err := localevidence.ReadMemberCustody(workspaceRoot)
	if err != nil {
		return Custody{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var custody Custody
	if err := decoder.Decode(&custody); err != nil {
		return Custody{}, fmt.Errorf("decode member custody: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Custody{}, errors.New("member custody has trailing input")
	}
	if custody.APIVersion != CustodyAPIVersion || custody.Kind != CustodyKind || custody.Grants != (Grants{}) {
		return Custody{}, errors.New("member custody is not a verify-only record")
	}
	admission, err := json.Marshal(custody.Admission)
	if err != nil {
		return Custody{}, err
	}
	// The join window bounded admission; loading re-verifies only integrity.
	verified, err := Verify(admission, custody.Verifier.KeyID, custody.Admission.IssuedAt)
	if err != nil {
		return Custody{}, fmt.Errorf("verify member custody admission: %w", err)
	}
	expected := NewCustody(verified, custody.JoinedAt)
	if !equalCustody(expected, custody) {
		return Custody{}, errors.New("member custody differs from its Owner-signed admission")
	}
	return custody, nil
}

// Persist installs member custody. The workspace must not hold Home owner custody.
func Persist(workspaceRoot string, custody Custody) error {
	return localevidence.PersistMemberCustody(workspaceRoot, custody)
}

func equalCustody(left, right Custody) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

// IsNotMember reports whether err means the workspace holds no member custody.
func IsNotMember(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
