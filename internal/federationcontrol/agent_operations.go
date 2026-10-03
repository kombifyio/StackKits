package federationcontrol

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const agentPolicyPath = ".stackkit/custody/federation-control/agent-policy.json"

// agentPolicyRecord is the Home owner's signed record of the outbound control
// policy it holds. It carries digests and the closed action list only.
type agentPolicyRecord struct {
	PolicyDigest string                                                `json:"policyDigest"`
	ContractHash string                                                `json:"contractHash"`
	Actions      []architecturev2renderer.FederationControlAgentAction `json:"actions"`
}

type agentOperations struct {
	root       string
	verifyPlan func(root string, c ReceiverCustody) error
	now        func() time.Time
	mu         sync.Mutex
}

// NewControlAgentOperations selects the local node as the owner of the
// Federation outbound control path. At the Cloud node it holds the admitted
// receiver custody to the CUE policy; at Home it records the outbound-only
// policy and refuses any admitted receiver. It never creates trust: Home
// keys and the Cloud server identity stay in the receiver custody that
// `federation control bind` admitted under local Owner authority.
func NewControlAgentOperations(workspaceRoot string) nativehost.FederationControlAgentOperations {
	return &agentOperations{
		root: workspaceRoot, now: time.Now,
		verifyPlan: func(root string, c ReceiverCustody) error { _, err := verifyPlanAuthority(root, c); return err },
	}
}

func (o *agentOperations) BindOutboundControlAgent(ctx context.Context, p nativehost.FederationControlAgentApplyPolicy) (nativehost.FederationControlAgentObservation, error) {
	return o.reconcile(ctx, p, "bound", false)
}

func (o *agentOperations) RemoveObsoleteOutboundControlAgent(ctx context.Context, p nativehost.FederationControlAgentExpectation) (nativehost.FederationControlAgentObservation, error) {
	return o.reconcile(ctx, p, "obsolete-removed", true)
}

func (o *agentOperations) VerifyOutboundControlAgent(ctx context.Context, p nativehost.FederationControlAgentExpectation) (nativehost.FederationControlAgentObservation, error) {
	return o.reconcile(ctx, p, "ready", false)
}

func (o *agentOperations) reconcile(ctx context.Context, p nativehost.FederationControlAgentApplyPolicy, status string, withdrawOnDivergence bool) (nativehost.FederationControlAgentObservation, error) {
	if err := ctx.Err(); err != nil {
		return nativehost.FederationControlAgentObservation{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(o.root)
	if err != nil {
		return nativehost.FederationControlAgentObservation{}, err
	}
	if owner.Binding.SiteRef != p.SiteRef || owner.Binding.NodeRef != p.NodeRef || owner.Binding.ChannelRef != p.ExecutionChannelRef {
		return nativehost.FederationControlAgentObservation{}, errors.New("control agent: target differs from current local custody")
	}
	part := p.Partition
	if part.OnCloudLoss != "local-continues" || part.OnLinkLoss != "local-continues" || part.CloudEdge != "fail-closed" ||
		!part.LocalIdentityAuthorityAvailable || !part.DenyNewCrossSiteSessions {
		return nativehost.FederationControlAgentObservation{}, errors.New("control agent: partition policy does not keep Home authority and fail new cross-site sessions closed")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	switch p.SiteKind {
	case "cloud":
		err = o.holdReceiver(p, withdrawOnDivergence)
	case "home":
		err = o.holdOutboundOnly(p)
	default:
		err = errors.New("control agent: unknown Site kind")
	}
	if err != nil {
		return nativehost.FederationControlAgentObservation{}, err
	}
	at := o.now().UTC().Format(time.RFC3339Nano)
	return nativehost.FederationControlAgentObservation{
		PolicyDigest: p.PolicyDigest, Status: status, EvaluatedAt: p.EvaluatedAt, ObservedAt: at, ConfigurationObservedAt: at,
		StackID: p.StackID, SiteRef: p.SiteRef, NodeRef: p.NodeRef, SiteKind: p.SiteKind, ExecutionChannelRef: p.ExecutionChannelRef,
		ContractHash: p.ContractHash, Actions: append([]architecturev2renderer.FederationControlAgentAction(nil), p.Actions...),
		OnCloudLoss: part.OnCloudLoss, OnLinkLoss: part.OnLinkLoss, CloudEdge: part.CloudEdge,
		MaxStaleVerificationSeconds:     part.MaxStaleVerificationSeconds,
		LocalIdentityAuthorityAvailable: part.LocalIdentityAuthorityAvailable, DenyNewCrossSiteSessions: part.DenyNewCrossSiteSessions,
		OutboundOnly: true, InboundCloudToHomeAllowed: false, GeneralLANAccess: false,
		LocalAuthorityContinues: true, NewCrossSiteSessionsFailClosed: true,
	}, nil
}

// holdReceiver requires the admitted Cloud receiver custody to match the
// policy exactly. A divergent admission is withdrawn when the owner removes
// obsolete state, so it stops serving, and the mutation fails either way.
func (o *agentOperations) holdReceiver(p nativehost.FederationControlAgentApplyPolicy, withdrawOnDivergence bool) error {
	var c ReceiverCustody
	if err := readState(o.root, custodyPath, &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("control agent: receiver-custody-missing: admit the Home authority with `federation control bind` first")
		}
		return err
	}
	diverges := c.Policy.ContractHash != p.ContractHash || c.Policy.SiteRef != p.SiteRef || c.Policy.NodeRef != p.NodeRef ||
		c.Policy.ExecutionChannelRef != p.ExecutionChannelRef || !reflect.DeepEqual(c.Policy.Actions, p.Actions) ||
		c.Policy.Partition != p.Partition
	if diverges {
		if withdrawOnDivergence {
			if err := WithdrawReceiver(o.root); err != nil {
				return err
			}
		}
		return errors.New("control agent: admitted receiver differs from the governed control policy")
	}
	if err := validateCustody(o.root, c, o.now().UTC()); err != nil {
		return err
	}
	return o.verifyPlan(o.root, c)
}

// holdOutboundOnly records the signed outbound policy at Home and refuses any
// receiver admission there: Home never accepts a Cloud-initiated connection.
func (o *agentOperations) holdOutboundOnly(p nativehost.FederationControlAgentApplyPolicy) error {
	var receiver ReceiverCustody
	switch err := readState(o.root, custodyPath, &receiver); {
	case err == nil:
		return errors.New("control agent: a control receiver is admitted at Home; Home accepts no Cloud-initiated control path")
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	return writeState(o.root, agentPolicyPath, agentPolicyRecord{
		PolicyDigest: p.PolicyDigest, ContractHash: p.ContractHash, Actions: p.Actions,
	})
}

var _ nativehost.FederationControlAgentOperations = (*agentOperations)(nil)
