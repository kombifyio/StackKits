package commands

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/fleetmember"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// architectureV2ProcessDispatchedOwners are the remote-only Runtime Owners an
// Inventory-declared execution channel dispatches as a digest-pinned
// operations process (Techstack-bound). In a multi-host StackInstance the
// Foundation Node dispatches them for every Site; a member never does.
var architectureV2ProcessDispatchedOwners = []architecturev2.ProductRuntimeOwnerID{
	architecturev2.ProductRuntimeOwnerModernHomeIdentity,
	architecturev2.ProductRuntimeOwnerModernCloudIdentity,
	architecturev2.ProductRuntimeOwnerFederationControlAgent,
	architecturev2.ProductRuntimeOwnerBridgePublication,
	architecturev2.ProductRuntimeOwnerModernFederationPolicy,
	architecturev2.ProductRuntimeOwnerFederationBackup,
	architecturev2.ProductRuntimeOwnerFederationObservability,
	architecturev2.ProductRuntimeOwnerHomePrivateRemoteAccess,
	architecturev2.ProductRuntimeOwnerHAModernWarm,
	architecturev2.ProductRuntimeOwnerHAModernQuorum,
}

// architectureV2LocalEvidenceObservers supplies the requirement observers of
// the local Apply evidence collector. Tests replace it with stubbed observers.
var architectureV2LocalEvidenceObservers = func(workspaceRoot string) (map[string]localevidence.Observer, error) {
	hostObserver, err := localevidence.NewHostObserver(hostconformance.LocalProbe{})
	if err != nil {
		return nil, fmt.Errorf("configure local host observer: %w", err)
	}
	secretObserver, err := localevidence.NewSecretObserver(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("configure local secret observer: %w", err)
	}
	return map[string]localevidence.Observer{"host": hostObserver, "secret": secretObserver}, nil
}

// architectureV2LocalExecution is the construction-owned execution custody of
// one workspace: the Home owner on a Foundation Node, or a joined member with a
// Home-certified evidence key. Scope is nil for a single-host StackInstance.
type architectureV2LocalExecution struct {
	binding localevidence.LocalBinding
	owner   *localevidence.OwnerCustody
	member  *fleetmember.MemberEvidence
	scope   *generationartifact.ApplyExecutionScope
}

// loadArchitectureV2LocalExecution derives the local execution custody. A
// member is scoped to its own tuple. A Foundation Node that certified member
// evidence keys leaves those members' local targets to them and still
// dispatches the remote-only owners of every Site; without a certified member
// it executes the whole plan as before.
func loadArchitectureV2LocalExecution(workspaceRoot string, now time.Time) (architectureV2LocalExecution, error) {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	var memberDenial *localevidence.MemberSigningDenial
	if errors.As(err, &memberDenial) {
		evidence, err := fleetmember.LoadMemberEvidence(workspaceRoot, now)
		if err != nil {
			return architectureV2LocalExecution{}, fmt.Errorf("member-local execution requires a Home-certified evidence key: %w", err)
		}
		return architectureV2LocalExecution{
			binding: evidence.Custody.Binding, member: &evidence,
			scope: &generationartifact.ApplyExecutionScope{
				Role: generationartifact.ApplyExecutionRoleMember, StackID: evidence.Custody.StackID,
				Local:               architectureV2ScopeBinding(evidence.Custody.Binding),
				DispatchedOwnerRefs: architectureV2DispatchedOwnerRefs(),
			},
		}, nil
	}
	if err != nil {
		return architectureV2LocalExecution{}, err
	}
	local := architectureV2LocalExecution{binding: owner.Binding, owner: &owner}
	ownerRef, keyID, public, err := localevidence.OwnerVerificationKey(workspaceRoot)
	if err != nil {
		return architectureV2LocalExecution{}, err
	}
	certified, err := fleetmember.CertifiedMembers(workspaceRoot, fleetmember.HomeVerifier{
		OwnerRef: ownerRef, KeyID: keyID, PublicKey: base64.RawStdEncoding.EncodeToString(public),
	})
	if err != nil {
		return architectureV2LocalExecution{}, err
	}
	if len(certified) == 0 {
		return local, nil
	}
	members := make([]generationartifact.ApplyExecutionScopeBinding, 0, len(certified))
	for _, member := range certified {
		if member.StackID != certified[0].StackID {
			return architectureV2LocalExecution{}, errors.New("issued member evidence keys name more than one StackInstance; a Foundation Node workspace certifies members of exactly one")
		}
		members = append(members, architectureV2ScopeBinding(member.Member.LocalBinding()))
	}
	local.scope = &generationartifact.ApplyExecutionScope{
		Role: generationartifact.ApplyExecutionRoleAuthority, StackID: certified[0].StackID,
		Local: architectureV2ScopeBinding(owner.Binding), DispatchedOwnerRefs: architectureV2DispatchedOwnerRefs(),
		Members: members,
	}
	return local, nil
}

// applyEvidence builds the construction-owned collector and its public trust
// anchor. A member signs with its evidence key; the anchor is the public key
// of the verified Home-issued certificate for this exact tuple.
func (l architectureV2LocalExecution) applyEvidence(workspaceRoot string) (architecturev2.ProductApplyEvidenceCollector, architecturev2.ProductApplyTrustAnchor, error) {
	if l.member == nil {
		collector, anchor, _, err := newLocalOwnerApplyEvidenceCollector(workspaceRoot)
		return collector, anchor, err
	}
	observers, err := architectureV2LocalEvidenceObservers(workspaceRoot)
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, err
	}
	collector, err := localevidence.NewMemberCollector(l.member.Key, architectureV2ComponentVersion(version), observers, nil)
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, fmt.Errorf("configure member Apply evidence collector: %w", err)
	}
	anchor, err := architectureV2MemberTrustAnchor(collector, l.member.Public)
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, err
	}
	return collector, anchor, nil
}

// architectureV2MemberTrustAnchor accepts member evidence only under the
// certified public key; a collector holding any other key is refused.
func architectureV2MemberTrustAnchor(collector *localevidence.OwnerCollector, certified ed25519.PublicKey) (architecturev2.ProductApplyTrustAnchor, error) {
	producer, public, err := collector.ProducerTrust()
	if err != nil {
		return architecturev2.ProductApplyTrustAnchor{}, err
	}
	if !bytes.Equal(public, certified) {
		return architecturev2.ProductApplyTrustAnchor{}, errors.New("member evidence collector key differs from the Home-certified member key")
	}
	return architecturev2.ProductApplyTrustAnchor{
		Producer: generationartifact.ApplyEvidenceProducer{
			ID: producer.ID, Version: producer.Version, KeyID: producer.KeyID,
		},
		PublicKey: append(ed25519.PublicKey(nil), certified...), RequirementKinds: []string{"host", "secret"},
	}, nil
}

// bindScope fixes the execution scope on a composed product service.
func (l architectureV2LocalExecution) bindScope(authority architectureV2ExecutionAuthority) error {
	if l.scope == nil {
		return nil
	}
	product, ok := authority.(*architectureV2ProductRuntimeAuthority)
	if !ok || product == nil || product.Service == nil {
		return errors.New("host-scoped execution requires the product runtime authority")
	}
	return product.BindProductExecutionScope(*l.scope)
}

// scopedPlan returns the host-scoped projection this workspace executes.
func (l architectureV2LocalExecution) scopedPlan(plan generationartifact.VerifiedPlan) (generationartifact.VerifiedPlan, error) {
	if l.scope == nil {
		return plan, nil
	}
	return plan.WithExecutionScope(*l.scope)
}

// requireArchitectureV2MemberPlan keeps a member on the exact plan the Home
// owner admitted. A changed plan needs a new admission and certificate.
func requireArchitectureV2MemberPlan(workspaceRoot string, plan generationartifact.VerifiedPlan) error {
	custody, err := fleetmember.LoadCustody(workspaceRoot)
	if fleetmember.IsNotMember(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if plan.Binding().PlanHash != custody.PlanHash {
		return fmt.Errorf(
			"this member joined plan %s but the current plan is %s; relay the shared Inventory the Home owner admitted, or admit and join the member again",
			custody.PlanHash, plan.Binding().PlanHash,
		)
	}
	return nil
}

func architectureV2ScopeBinding(binding localevidence.LocalBinding) generationartifact.ApplyExecutionScopeBinding {
	return generationartifact.ApplyExecutionScopeBinding{
		SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ChannelRef,
	}
}

func architectureV2DispatchedOwnerRefs() []string {
	refs := make([]string, 0, len(architectureV2ProcessDispatchedOwners))
	for _, id := range architectureV2ProcessDispatchedOwners {
		refs = append(refs, string(id))
	}
	sort.Strings(refs)
	return refs
}

// architectureV2ScopeIncludesUnit reports whether a ledger subject is one this
// host executes.
func architectureV2ScopeIncludesSubject(scope *generationartifact.ApplyExecutionScope, ownerRef, siteRef, nodeRef string) bool {
	if scope == nil {
		return true
	}
	return scope.Includes(generationartifact.ApplyRuntimeRequirement{
		OwnerRef: ownerRef, SiteRefs: []string{siteRef}, NodeRefs: []string{nodeRef},
	})
}
