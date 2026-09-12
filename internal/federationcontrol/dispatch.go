package federationcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorprocess"
)

// verifyPlanAuthority re-reads the actual local canonical plan through the same
// immutable CUE authority as native CLI execution. Owner signatures alone never
// authorize a rehashed or substituted policy, management-only or multi-site plan.
func verifyPlanAuthority(root string, c ReceiverCustody, action ...Action) (generationartifact.VerifiedPlan, error) {
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(c.ExecutableVersion))
	if err != nil {
		return generationartifact.VerifiedPlan{}, err
	}
	for _, a := range action {
		raw, err := json.Marshal(a)
		if err != nil {
			return generationartifact.VerifiedPlan{}, err
		}
		if err := service.ValidateFederationRemoteActionEnvelope(raw); err != nil {
			return generationartifact.VerifiedPlan{}, err
		}
	}
	fs, err := readPlan(root, c.PlanPath)
	if err != nil {
		return generationartifact.VerifiedPlan{}, err
	}
	plan, err := service.VerifyCanonicalPlan(fs)
	if err != nil {
		return plan, err
	}
	if err := plan.RequireExpectedPlanHash(c.PlanHash); err != nil {
		return plan, err
	}
	var projection struct {
		Nodes []struct {
			ID      string `json:"id"`
			SiteRef string `json:"siteRef"`
		} `json:"nodes"`
		FailurePolicy architecturev2renderer.FederationControlAgentPartition `json:"failurePolicy"`
		Sites         []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"sites"`
		ControlPlane struct {
			AuthoritySiteRef string   `json:"authoritySiteRef"`
			Members          []string `json:"members"`
		} `json:"controlPlane"`
		Bridge struct {
			Overlay struct {
				TrafficMode  string   `json:"trafficMode"`
				PeerSiteRefs []string `json:"peerSiteRefs"`
			} `json:"overlay"`
			ControlAgent struct {
				Enabled              bool                                                  `json:"enabled"`
				ProviderContractHash string                                                `json:"providerContractHash"`
				Actions              []architecturev2renderer.FederationControlAgentAction `json:"actions"`
			} `json:"controlAgent"`
		} `json:"bridge"`
	}
	if err = json.Unmarshal(plan.Canonical(), &projection); err != nil {
		return plan, err
	}
	if len(projection.Sites) != 2 || len(projection.Nodes) != 2 || len(projection.Bridge.Overlay.PeerSiteRefs) != 2 || projection.Bridge.Overlay.TrafficMode != "policy-scoped" || projection.ControlPlane.AuthoritySiteRef != c.Trust.HomeSiteRef || len(projection.ControlPlane.Members) != 1 || !projection.Bridge.ControlAgent.Enabled || projection.Bridge.ControlAgent.ProviderContractHash != c.Policy.ContractHash || !reflect.DeepEqual(projection.Bridge.ControlAgent.Actions, c.Policy.Actions) {
		return plan, errors.New("control: receiver differs from exact two-site CUE control authority")
	}
	home, cloud := false, false
	for _, site := range projection.Sites {
		home = home || (site.ID == c.Trust.HomeSiteRef && site.Kind == "home")
		cloud = cloud || (site.ID == c.Policy.SiteRef && site.Kind == "cloud")
	}
	if !home || !cloud {
		return plan, errors.New("control: selected Home/Cloud sites differ from plan")
	}
	homeMember := false
	for _, node := range projection.Nodes {
		if node.SiteRef == c.Trust.HomeSiteRef && node.ID == projection.ControlPlane.Members[0] {
			homeMember = true
		}
	}
	if !homeMember {
		return plan, errors.New("control: control member is not the exact Home node")
	}
	if projection.FailurePolicy != c.Policy.Partition {
		return plan, errors.New("control: partition policy differs from the current CUE plan")
	}
	found := false
	for _, target := range plan.ApplyRequirements().RuntimeInstances {
		if target.ModuleRef == "stackkits-federation-control-agent-runtime" && len(target.SiteRefs) == 1 && target.SiteRefs[0] == c.Policy.SiteRef && len(target.NodeRefs) == 1 && target.NodeRefs[0] == c.Policy.NodeRef {
			found = true
		}
	}
	if !found {
		return plan, errors.New("control: target is not a plan-selected control-agent node")
	}
	channelFound := false
	for _, host := range plan.ApplyRequirements().Hosts {
		if host.NodeRef == c.Policy.NodeRef && host.SiteRef == c.Policy.SiteRef && (host.ExecutionChannelRef == "" || host.ExecutionChannelRef == c.Policy.ExecutionChannelRef) {
			channelFound = true
		}
	}
	if !channelFound {
		return plan, errors.New("control: execution channel differs from plan")
	}
	return plan, nil
}

func readPlan(root, path string) ([]byte, error) {
	fs, err := openRead(root, path, 16<<20, false)
	return fs, err
}

type Result struct {
	Schema             string          `json:"schema"`
	ActionDigest       string          `json:"actionDigest"`
	Action             string          `json:"action"`
	PlanHash           string          `json:"planHash"`
	Status             string          `json:"status"`
	ObservedAt         time.Time       `json:"observedAt"`
	Output             json.RawMessage `json:"output,omitempty"`
	ReasonCode         string          `json:"reasonCode,omitempty"`
	MutateCapabilities string          `json:"mutateCapabilities"`
}

func dispatch(ctx context.Context, root string, c ReceiverCustody, a Action) (Result, error) {
	result := Result{Schema: "stackkit.federation-action-result/v1", ActionDigest: a.Digest(), Action: a.Action, PlanHash: a.PlanHash, Status: "failed", MutateCapabilities: "unavailable"}
	if _, err := verifyPlanAuthority(root, c); err != nil {
		return result, err
	}
	ctx, cancel := context.WithDeadline(ctx, a.ExpiresAt)
	defer cancel()
	raw, err := runtimeexecutorprocess.InvokeReadOnly(ctx, runtimeexecutorprocess.Binding{ChannelRef: c.Policy.ExecutionChannelRef, SiteRef: c.Policy.SiteRef, NodeRef: c.Policy.NodeRef, Executable: c.Executable, ExecutableSHA256: c.ExecutableSHA256}, root, filepath.Join(root, filepath.FromSlash(c.PlanPath)), a.PlanHash, a.Action)
	if ctx.Err() != nil {
		// Preserve the unfinished claim: cancellation is not a completed owner
		// result and must never be cached as a permanent verification failure.
		return result, ctx.Err()
	}
	result.ObservedAt = time.Now().UTC()
	if json.Valid(raw) {
		result.Output = append([]byte(nil), raw...)
	}
	if err != nil {
		result.ReasonCode = "local_owner_verification_failed"
		return result, nil
	}
	if !json.Valid(raw) {
		result.ReasonCode = "local_owner_result_invalid"
		return result, nil
	}
	// Revalidate current authority before publishing success. A connection or
	// artifact change must not inherit the admission at request start.
	if _, err = verifyPlanAuthority(root, c); err != nil {
		return result, err
	}
	if !a.ExpiresAt.After(time.Now().UTC()) {
		return result, errors.New("control: execution outlived action authority")
	}
	result.Status = "succeeded"
	return result, nil
}
