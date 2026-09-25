// Package fleetmember binds a physical host to one existing member node of a
// StackInstance. The Home owner on the Foundation Node signs a member
// admission; the member host verifies it against the pinned Home key,
// recompiles the same ResolvedPlan, and keeps verify-only custody. It grants
// no enrollment, signing, credential issuance, or ControlAuthority.
package fleetmember

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
	"github.com/kombifyio/stackkits/internal/fleetlifecycle"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	AdmissionAPIVersion = "stackkit.member-admission/v1"
	AdmissionKind       = "MemberAdmission"

	// MaxAdmissionValidity bounds the join window. The resulting member
	// custody does not expire; a changed plan needs a new admission.
	MaxAdmissionValidity = 24 * time.Hour
	// admissionClockSkew tolerates a member clock slightly behind the Home.
	admissionClockSkew = 5 * time.Minute
	// MaxAdmissionBytes bounds the document, which carries spec and inventory.
	MaxAdmissionBytes = 8 << 20

	memberKit = "modern-homelab"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ExecutionBinding is one exact Site/node/execution-channel tuple.
type ExecutionBinding struct {
	SiteRef             string `json:"siteRef"`
	SiteKind            string `json:"siteKind"`
	NodeRef             string `json:"nodeRef"`
	ExecutionChannelRef string `json:"executionChannelRef"`
}

// LocalBinding projects the tuple into the local custody binding shape.
func (b ExecutionBinding) LocalBinding() localevidence.LocalBinding {
	return localevidence.LocalBinding{SiteRef: b.SiteRef, NodeRef: b.NodeRef, ChannelRef: b.ExecutionChannelRef}
}

// HomeVerifier is the public Home owner key a member uses to verify
// Home-signed documents. It never carries private or credential material.
type HomeVerifier struct {
	OwnerRef  string `json:"ownerRef"`
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
}

// Grants are closed: a member is verify-only. Every field must be false.
type Grants struct {
	Enrollment         bool `json:"enrollment"`
	Signing            bool `json:"signing"`
	CredentialIssuance bool `json:"credentialIssuance"`
	ControlAuthority   bool `json:"controlAuthority"`
}

// Payload carries the exact bytes both hosts compile.
type Payload struct {
	Format  string `json:"format"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"`
}

// Admission is the Owner-signed `stackkit.member-admission/v1` document.
type Admission struct {
	APIVersion               string                                  `json:"apiVersion"`
	Kind                     string                                  `json:"kind"`
	StackID                  string                                  `json:"stackId"`
	FleetRef                 string                                  `json:"fleetRef,omitempty"`
	KitSlug                  string                                  `json:"kitSlug"`
	CompilerVersion          string                                  `json:"compilerVersion"`
	PlanHash                 string                                  `json:"planHash"`
	SpecHash                 string                                  `json:"specHash"`
	InventoryHash            string                                  `json:"inventoryHash"`
	Authority                ExecutionBinding                        `json:"authority"`
	Member                   ExecutionBinding                        `json:"member"`
	MemberRoles              []string                                `json:"memberRoles"`
	Grants                   Grants                                  `json:"grants"`
	Verifier                 HomeVerifier                            `json:"verifier"`
	VerifierDistributionRefs []string                                `json:"verifierDistributionRefs"`
	StackSpec                Payload                                 `json:"stackSpec"`
	Inventory                Payload                                 `json:"inventory"`
	IssuedAt                 time.Time                               `json:"issuedAt"`
	ValidUntil               time.Time                               `json:"validUntil"`
	Signature                localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// SigningBytes are the canonical bytes covered by the Owner signature.
func (a Admission) SigningBytes() ([]byte, error) {
	a.Signature = localevidence.OwnerPolicyStateSignature{}
	return resolvedplan.CanonicalJSON(a)
}

// IssueRequest is the Foundation Node input for one member admission.
type IssueRequest struct {
	Plan            resolvedplan.ResolvedPlan
	StackSpec       []byte
	Inventory       []byte
	InventoryFormat string
	MemberNodeRef   string
	Authority       localevidence.LocalBinding
	OwnerRef        string
	KeyID           string
	PublicKey       ed25519.PublicKey
	Now             time.Time
	ValidFor        time.Duration
}

// Issue derives the unsigned admission from the exact compiled plan. The
// caller signs SigningBytes with the Home owner key.
func Issue(request IssueRequest) (Admission, error) {
	if request.ValidFor <= 0 || request.ValidFor > MaxAdmissionValidity {
		return Admission{}, fmt.Errorf("member admission validity must be within (0, %s]", MaxAdmissionValidity)
	}
	if len(request.StackSpec) == 0 || len(request.Inventory) == 0 {
		return Admission{}, errors.New("member admission requires the exact StackSpec and Inventory")
	}
	if request.InventoryFormat != "yaml" && request.InventoryFormat != "json" {
		return Admission{}, errors.New("member admission Inventory format must be yaml or json")
	}
	if len(request.PublicKey) != ed25519.PublicKeySize || applyevidence.ProducerKeyID(request.PublicKey) != request.KeyID ||
		strings.TrimSpace(request.OwnerRef) == "" {
		return Admission{}, errors.New("member admission requires the exact Home owner verification key")
	}
	derived, err := derive(request.Plan, request.Authority, request.MemberNodeRef)
	if err != nil {
		return Admission{}, err
	}
	issuedAt := request.Now.UTC().Truncate(time.Second)
	return Admission{
		APIVersion: AdmissionAPIVersion, Kind: AdmissionKind,
		StackID: derived.stackID, FleetRef: derived.fleetRef, KitSlug: derived.kitSlug,
		CompilerVersion: derived.compilerVersion, PlanHash: derived.planHash,
		SpecHash: derived.specHash, InventoryHash: derived.inventoryHash,
		Authority: derived.authority, Member: derived.member, MemberRoles: derived.memberRoles,
		Grants: Grants{},
		Verifier: HomeVerifier{
			OwnerRef: request.OwnerRef, KeyID: request.KeyID,
			PublicKey: base64.RawStdEncoding.EncodeToString(request.PublicKey),
		},
		VerifierDistributionRefs: derived.verifierDistributionRefs,
		StackSpec:                newPayload(payloadFormat(request.StackSpec), request.StackSpec),
		Inventory:                newPayload(request.InventoryFormat, request.Inventory),
		IssuedAt:                 issuedAt,
		ValidUntil:               issuedAt.Add(request.ValidFor),
	}, nil
}

// Verified is an admission whose signature, pin, window, and payloads hold.
type Verified struct {
	Admission Admission
	Digest    string
	StackSpec []byte
	Inventory []byte
}

// Verify decodes one bounded admission and verifies it against the Home key
// ID the member operator pinned out of band.
func Verify(raw []byte, pinnedKeyID string, now time.Time) (Verified, error) {
	if len(raw) == 0 || len(raw) > MaxAdmissionBytes {
		return Verified{}, errors.New("member admission is empty or exceeds its bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var admission Admission
	if err := decoder.Decode(&admission); err != nil {
		return Verified{}, fmt.Errorf("decode member admission: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Verified{}, errors.New("member admission has trailing input")
	}
	if admission.APIVersion != AdmissionAPIVersion || admission.Kind != AdmissionKind {
		return Verified{}, errors.New("member admission has an unsupported contract identity")
	}
	if admission.Grants != (Grants{}) {
		return Verified{}, errors.New("member admission widens verify-only member custody")
	}
	pinnedKeyID = strings.TrimSpace(pinnedKeyID)
	if pinnedKeyID == "" || admission.Verifier.KeyID != pinnedKeyID {
		return Verified{}, errors.New("member admission is not issued by the pinned Home owner key")
	}
	public, err := base64.RawStdEncoding.Strict().DecodeString(admission.Verifier.PublicKey)
	if err != nil {
		return Verified{}, errors.New("member admission Home verification key is malformed")
	}
	signing, err := admission.SigningBytes()
	if err != nil {
		return Verified{}, err
	}
	if err := localevidence.VerifyMemberAdmission(
		signing, admission.Signature, admission.Verifier.OwnerRef, admission.Verifier.KeyID, ed25519.PublicKey(public),
	); err != nil {
		return Verified{}, err
	}
	now = now.UTC()
	if admission.IssuedAt.Location() != time.UTC || admission.ValidUntil.Location() != time.UTC ||
		!admission.ValidUntil.After(admission.IssuedAt) ||
		admission.ValidUntil.Sub(admission.IssuedAt) > MaxAdmissionValidity ||
		now.Add(admissionClockSkew).Before(admission.IssuedAt) || !now.Before(admission.ValidUntil) {
		return Verified{}, errors.New("member admission is expired or outside its bounded join window")
	}
	spec, err := admission.StackSpec.decode("")
	if err != nil {
		return Verified{}, fmt.Errorf("member admission StackSpec: %w", err)
	}
	inventory, err := admission.Inventory.decode("")
	if err != nil {
		return Verified{}, fmt.Errorf("member admission Inventory: %w", err)
	}
	return Verified{Admission: admission, Digest: contentDigest(signing), StackSpec: spec, Inventory: inventory}, nil
}

// BindPlan requires the plan the member compiled from the admitted payloads
// to reproduce every admitted fact, including the exact plan hash.
func (v Verified) BindPlan(plan resolvedplan.ResolvedPlan) error {
	admission := v.Admission
	authority := localevidence.LocalBinding{
		SiteRef: admission.Authority.SiteRef, NodeRef: admission.Authority.NodeRef,
		ChannelRef: admission.Authority.ExecutionChannelRef,
	}
	derived, err := derive(plan, authority, admission.Member.NodeRef)
	if err != nil {
		return err
	}
	if derived.planHash != admission.PlanHash || derived.compilerVersion != admission.CompilerVersion {
		return fmt.Errorf(
			"member plan hash %s (compiler %s) differs from the admitted %s (compiler %s); run the same StackKits version on both hosts",
			derived.planHash, derived.compilerVersion, admission.PlanHash, admission.CompilerVersion,
		)
	}
	if derived.stackID != admission.StackID || derived.fleetRef != admission.FleetRef ||
		derived.kitSlug != admission.KitSlug || derived.specHash != admission.SpecHash ||
		derived.inventoryHash != admission.InventoryHash || derived.authority != admission.Authority ||
		derived.member != admission.Member || !reflect.DeepEqual(derived.memberRoles, admission.MemberRoles) ||
		!reflect.DeepEqual(derived.verifierDistributionRefs, admission.VerifierDistributionRefs) {
		return errors.New("member admission differs from the member-compiled ResolvedPlan")
	}
	return nil
}

type derivation struct {
	stackID, fleetRef, kitSlug, compilerVersion string
	planHash, specHash, inventoryHash           string
	authority, member                           ExecutionBinding
	memberRoles, verifierDistributionRefs       []string
}

type planView struct {
	StackID         string `json:"stackId"`
	FleetRef        string `json:"fleetRef"`
	PlanHash        string `json:"planHash"`
	SpecHash        string `json:"specHash"`
	InventoryHash   string `json:"inventoryHash"`
	CompilerVersion string `json:"compilerVersion"`
	Kit             struct {
		Slug string `json:"slug"`
	} `json:"kit"`
	Sites []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"sites"`
	Source struct {
		Inventory struct {
			Document struct {
				ExecutionChannels map[string]struct {
					ChannelRef string `json:"channelRef"`
					SiteRef    string `json:"siteRef"`
					NodeRef    string `json:"nodeRef"`
				} `json:"executionChannels"`
			} `json:"document"`
		} `json:"inventory"`
	} `json:"source"`
	IdentityTrust struct {
		VerifierDistributions []struct {
			ID   string `json:"id"`
			From struct {
				SiteRefs []string `json:"siteRefs"`
			} `json:"from"`
			To struct {
				SiteRefs []string `json:"siteRefs"`
			} `json:"to"`
			IncludesCredentialMaterial  bool `json:"includesCredentialMaterial"`
			IncludesEnrollmentAuthority bool `json:"includesEnrollmentAuthority"`
			IncludesPrivateKeyMaterial  bool `json:"includesPrivateKeyMaterial"`
			IncludesSigningAuthority    bool `json:"includesSigningAuthority"`
			ReverseAllowed              bool `json:"reverseAllowed"`
		} `json:"verifierDistributions"`
	} `json:"identityTrust"`
}

// derive is the single rule both hosts apply: the Home authority tuple is a
// ControlAuthority member at the authority Site, the member is an enabled
// non-controller at a Cloud Site, each has exactly one Inventory execution
// channel, and Home distributes only verification material to the member Site.
//
//nolint:gocyclo // One fail-closed admission rule set is easier to audit in one place.
func derive(plan resolvedplan.ResolvedPlan, authority localevidence.LocalBinding, memberNodeRef string) (derivation, error) {
	fleet, err := fleetlifecycle.Project(plan)
	if err != nil {
		return derivation{}, fmt.Errorf("project Fleet membership: %w", err)
	}
	canonical, err := plan.MarshalCanonical()
	if err != nil {
		return derivation{}, err
	}
	var view planView
	if err := json.Unmarshal(canonical, &view); err != nil {
		return derivation{}, fmt.Errorf("decode member admission plan view: %w", err)
	}
	if view.Kit.Slug != memberKit {
		return derivation{}, fmt.Errorf("member join is defined for %s Cloud edges; this plan is %q", memberKit, view.Kit.Slug)
	}
	if !digestPattern.MatchString(view.PlanHash) || !digestPattern.MatchString(view.SpecHash) ||
		!digestPattern.MatchString(view.InventoryHash) || view.CompilerVersion == "" {
		return derivation{}, errors.New("member admission plan identity is incomplete")
	}
	siteKinds := make(map[string]string, len(view.Sites))
	for _, site := range view.Sites {
		siteKinds[site.ID] = site.Kind
	}
	channelFor := func(siteRef, nodeRef string) (string, error) {
		matches := make([]string, 0, 1)
		for ref, channel := range view.Source.Inventory.Document.ExecutionChannels {
			if channel.SiteRef == siteRef && channel.NodeRef == nodeRef {
				matches = append(matches, ref)
			}
		}
		if len(matches) != 1 {
			return "", fmt.Errorf("Inventory must declare exactly one execution channel for %s/%s, found %d", siteRef, nodeRef, len(matches))
		}
		return matches[0], nil
	}

	control := fleet.Membership.ControlAuthority
	if authority.SiteRef != control.AuthoritySiteRef || siteKinds[authority.SiteRef] != "home" ||
		!containsString(control.MemberRefs, authority.NodeRef) {
		return derivation{}, fmt.Errorf("owner binding %s/%s is not a Home ControlAuthority member of this plan", authority.SiteRef, authority.NodeRef)
	}
	authorityChannel, err := channelFor(authority.SiteRef, authority.NodeRef)
	if err != nil {
		return derivation{}, err
	}
	if authorityChannel != authority.ChannelRef {
		return derivation{}, fmt.Errorf("owner execution channel %q differs from the Inventory channel %q", authority.ChannelRef, authorityChannel)
	}

	memberNodeRef = strings.TrimSpace(memberNodeRef)
	var member *fleetlifecycle.Member
	for index := range fleet.Membership.Members {
		if fleet.Membership.Members[index].NodeRef == memberNodeRef {
			member = &fleet.Membership.Members[index]
		}
	}
	if member == nil {
		return derivation{}, fmt.Errorf("node %q is not a member of this plan; add it through the Fleet add lifecycle first", memberNodeRef)
	}
	if !member.Enabled || member.ControlPlaneMember || containsString(member.Roles, "controller") {
		return derivation{}, fmt.Errorf("node %q must be an enabled worker or edge outside the ControlAuthority", memberNodeRef)
	}
	if siteKinds[member.SiteRef] != "cloud" || member.SiteRef == authority.SiteRef {
		return derivation{}, fmt.Errorf("node %q must belong to a Cloud Site", memberNodeRef)
	}
	memberChannel, err := channelFor(member.SiteRef, member.NodeRef)
	if err != nil {
		return derivation{}, err
	}

	distributions := make([]string, 0)
	for _, distribution := range view.IdentityTrust.VerifierDistributions {
		if !containsString(distribution.To.SiteRefs, member.SiteRef) {
			continue
		}
		if distribution.IncludesCredentialMaterial || distribution.IncludesEnrollmentAuthority ||
			distribution.IncludesPrivateKeyMaterial || distribution.IncludesSigningAuthority ||
			distribution.ReverseAllowed || !containsString(distribution.From.SiteRefs, authority.SiteRef) {
			return derivation{}, fmt.Errorf("verifier distribution %q grants the member more than Home verification material", distribution.ID)
		}
		distributions = append(distributions, distribution.ID)
	}
	if len(distributions) == 0 {
		return derivation{}, fmt.Errorf("plan distributes no Home verifier state to Site %q", member.SiteRef)
	}
	sort.Strings(distributions)
	roles := append([]string(nil), member.Roles...)
	sort.Strings(roles)
	return derivation{
		stackID: view.StackID, fleetRef: view.FleetRef, kitSlug: view.Kit.Slug,
		compilerVersion: view.CompilerVersion, planHash: view.PlanHash,
		specHash: view.SpecHash, inventoryHash: view.InventoryHash,
		authority: ExecutionBinding{
			SiteRef: authority.SiteRef, SiteKind: "home", NodeRef: authority.NodeRef,
			ExecutionChannelRef: authorityChannel,
		},
		member: ExecutionBinding{
			SiteRef: member.SiteRef, SiteKind: "cloud", NodeRef: member.NodeRef,
			ExecutionChannelRef: memberChannel,
		},
		memberRoles: roles, verifierDistributionRefs: distributions,
	}, nil
}

func newPayload(format string, content []byte) Payload {
	return Payload{Format: format, SHA256: contentDigest(content), Content: base64.StdEncoding.EncodeToString(content)}
}

func (p Payload) decode(requiredFormat string) ([]byte, error) {
	if (requiredFormat != "" && p.Format != requiredFormat) || (p.Format != "json" && p.Format != "yaml") {
		return nil, errors.New("payload format is not admitted")
	}
	content, err := base64.StdEncoding.Strict().DecodeString(p.Content)
	if err != nil || len(content) == 0 || contentDigest(content) != p.SHA256 {
		return nil, errors.New("payload bytes differ from their admitted digest")
	}
	return content, nil
}

func payloadFormat(content []byte) string {
	if json.Valid(content) {
		return "json"
	}
	return "yaml"
}

func contentDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
