package fleetlifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	APIVersion = "stackkit.fleet-lifecycle/v1"
	Kind       = "FleetLifecycle"
)

type Operation string

const (
	OperationAdd     Operation = "add"
	OperationReplace Operation = "replace"
	OperationDrain   Operation = "drain"
	OperationRecover Operation = "recover"
	OperationRemove  Operation = "remove"
)

type Contract struct {
	APIVersion    string                          `json:"apiVersion"`
	Kind          string                          `json:"kind"`
	StackID       string                          `json:"stackId"`
	FleetRef      string                          `json:"fleetRef,omitempty"`
	SpecHash      string                          `json:"specHash"`
	InventoryHash string                          `json:"inventoryHash"`
	Scope         string                          `json:"scope"`
	Membership    Membership                      `json:"membership"`
	Authority     Authority                       `json:"authority"`
	Operations    map[Operation]OperationContract `json:"operations"`
}

type Membership struct {
	Members          []Member         `json:"members"`
	ControlAuthority ControlAuthority `json:"controlAuthority"`
}

type Member struct {
	NodeRef            string   `json:"nodeRef"`
	SiteRef            string   `json:"siteRef"`
	Roles              []string `json:"roles"`
	Enabled            bool     `json:"enabled"`
	FailureDomain      string   `json:"failureDomain"`
	ControlPlaneMember bool     `json:"controlPlaneMember"`
}

type ControlAuthority struct {
	Mode             string   `json:"mode"`
	AuthoritySiteRef string   `json:"authoritySiteRef"`
	MemberRefs       []string `json:"memberRefs"`
}

type Authority struct {
	Source                        string      `json:"source"`
	PlanBinding                   PlanBinding `json:"planBinding"`
	OwnerApprovalRequired         bool        `json:"ownerApprovalRequired"`
	LocalCustodyRequired          bool        `json:"localCustodyRequired"`
	ProviderLifecycleOwned        bool        `json:"providerLifecycleOwned"`
	MultiServerOrchestrationOwned bool        `json:"multiServerOrchestrationOwned"`
	CredentialCustodyOwned        bool        `json:"credentialCustodyOwned"`
}

type PlanBinding struct {
	CurrentHashField   string `json:"currentHashField"`
	TargetHashField    string `json:"targetHashField"`
	StackIdentityField string `json:"stackIdentityField"`
	FleetIdentityField string `json:"fleetIdentityField"`
	SpecIdentityField  string `json:"specIdentityField"`
	InventoryField     string `json:"inventoryField"`
}

type OperationContract struct {
	Name                 Operation `json:"name"`
	SourceMember         string    `json:"sourceMember"`
	TargetMember         string    `json:"targetMember"`
	TargetPlanRequired   bool      `json:"targetPlanRequired"`
	TargetPlanMustDiffer bool      `json:"targetPlanMustDiffer"`
	Mutation             bool      `json:"mutation"`
	Destructive          bool      `json:"destructive"`
	OwnerApproval        Approval  `json:"ownerApproval"`
	CheckpointRequired   bool      `json:"checkpointRequired"`
	RecoveryRequired     bool      `json:"recoveryRequired"`
	Phases               []string  `json:"phases"`
	Evidence             Evidence  `json:"evidence"`
}

type Approval struct {
	Required bool   `json:"required"`
	Class    string `json:"class"`
}

type Evidence struct {
	Schema         string   `json:"schema"`
	Immutable      bool     `json:"immutable"`
	LocalCustody   bool     `json:"localCustody"`
	RequiredPhases []string `json:"requiredPhases"`
}

// Project returns the exact compiler-owned fleet lifecycle contract carried by
// one integrity-valid ResolvedPlan. Callers do not reconstruct membership,
// operation semantics, or authority exclusions themselves.
func Project(plan resolvedplan.ResolvedPlan) (Contract, error) {
	if _, err := resolvedplan.VerifyPlanHash(plan); err != nil {
		return Contract{}, fmt.Errorf("verify fleet lifecycle ResolvedPlan: %w", err)
	}
	canonical, err := plan.MarshalCanonical()
	if err != nil {
		return Contract{}, fmt.Errorf("marshal fleet lifecycle ResolvedPlan: %w", err)
	}
	var wire planProjection
	if err := json.Unmarshal(canonical, &wire); err != nil {
		return Contract{}, fmt.Errorf("decode fleet lifecycle ResolvedPlan projection: %w", err)
	}
	if wire.FleetLifecycle.APIVersion == "" {
		return Contract{}, errors.New("resolvedPlan.fleetLifecycle is required")
	}
	if err := validateProjection(wire); err != nil {
		return Contract{}, err
	}
	return wire.FleetLifecycle, nil
}

type planProjection struct {
	StackID       string     `json:"stackId"`
	FleetRef      string     `json:"fleetRef,omitempty"`
	SpecHash      string     `json:"specHash"`
	InventoryHash string     `json:"inventoryHash"`
	Nodes         []planNode `json:"nodes"`
	ControlPlane  struct {
		Mode             string   `json:"mode"`
		AuthoritySiteRef string   `json:"authoritySiteRef"`
		Members          []string `json:"members"`
	} `json:"controlPlane"`
	FleetLifecycle Contract `json:"fleetLifecycle"`
}

type planNode struct {
	ID            string   `json:"id"`
	SiteRef       string   `json:"siteRef"`
	Roles         []string `json:"roles"`
	Enabled       bool     `json:"enabled"`
	FailureDomain string   `json:"failureDomain"`
}

func validateProjection(wire planProjection) error {
	contract := wire.FleetLifecycle
	if contract.APIVersion != APIVersion || contract.Kind != Kind || contract.Scope != "single-stack-instance" {
		return errors.New("resolvedPlan.fleetLifecycle has unsupported contract identity")
	}
	if contract.StackID != wire.StackID || contract.FleetRef != wire.FleetRef ||
		contract.SpecHash != wire.SpecHash || contract.InventoryHash != wire.InventoryHash {
		return errors.New("resolvedPlan.fleetLifecycle differs from ResolvedPlan identity")
	}
	if !reflect.DeepEqual(contract.Authority, expectedAuthority()) {
		return errors.New("resolvedPlan.fleetLifecycle widened StackKits authority")
	}
	if !reflect.DeepEqual(contract.Operations, expectedOperationContracts()) {
		return errors.New("resolvedPlan.fleetLifecycle operation contracts differ from compiler authority")
	}
	expectedMembership, err := expectedMembership(wire)
	if err != nil {
		return err
	}
	actualMembership := normalizedMembership(contract.Membership)
	if !reflect.DeepEqual(actualMembership, expectedMembership) {
		return errors.New("resolvedPlan.fleetLifecycle membership differs from ResolvedPlan topology")
	}
	return nil
}

func expectedMembership(wire planProjection) (Membership, error) {
	if wire.ControlPlane.Mode == "" || wire.ControlPlane.AuthoritySiteRef == "" ||
		len(wire.ControlPlane.Members) == 0 {
		return Membership{}, errors.New("resolvedPlan.controlPlane is incomplete for fleet lifecycle")
	}
	controlMembers := make(map[string]struct{}, len(wire.ControlPlane.Members))
	for _, memberRef := range wire.ControlPlane.Members {
		if memberRef == "" {
			return Membership{}, errors.New("resolvedPlan.controlPlane contains an empty member")
		}
		if _, duplicate := controlMembers[memberRef]; duplicate {
			return Membership{}, errors.New("resolvedPlan.controlPlane contains a duplicate member")
		}
		controlMembers[memberRef] = struct{}{}
	}
	members := make([]Member, 0, len(wire.Nodes))
	nodes := make(map[string]planNode, len(wire.Nodes))
	for _, node := range wire.Nodes {
		if node.ID == "" || node.SiteRef == "" || node.FailureDomain == "" || len(node.Roles) == 0 {
			return Membership{}, errors.New("resolvedPlan node identity is incomplete for fleet lifecycle")
		}
		if _, duplicate := nodes[node.ID]; duplicate {
			return Membership{}, errors.New("resolvedPlan contains a duplicate node for fleet lifecycle")
		}
		nodes[node.ID] = node
		roles := append([]string(nil), node.Roles...)
		sort.Strings(roles)
		for index := 1; index < len(roles); index++ {
			if roles[index-1] == roles[index] {
				return Membership{}, errors.New("resolvedPlan node contains duplicate fleet roles")
			}
		}
		_, isControlMember := controlMembers[node.ID]
		members = append(members, Member{
			NodeRef: node.ID, SiteRef: node.SiteRef, Roles: roles, Enabled: node.Enabled,
			FailureDomain: node.FailureDomain, ControlPlaneMember: isControlMember,
		})
	}
	for memberRef := range controlMembers {
		node, exists := nodes[memberRef]
		if !exists || !node.Enabled || node.SiteRef != wire.ControlPlane.AuthoritySiteRef ||
			!containsRole(node.Roles, "controller") {
			return Membership{}, errors.New("resolvedPlan ControlAuthority member is not an enabled controller at its authority Site")
		}
	}
	return normalizedMembership(Membership{
		Members: members,
		ControlAuthority: ControlAuthority{
			Mode: wire.ControlPlane.Mode, AuthoritySiteRef: wire.ControlPlane.AuthoritySiteRef,
			MemberRefs: append([]string(nil), wire.ControlPlane.Members...),
		},
	}), nil
}

func containsRole(roles []string, target string) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}

func normalizedMembership(membership Membership) Membership {
	membership.Members = append([]Member(nil), membership.Members...)
	for index := range membership.Members {
		membership.Members[index].Roles = append([]string(nil), membership.Members[index].Roles...)
		sort.Strings(membership.Members[index].Roles)
	}
	sort.Slice(membership.Members, func(left, right int) bool {
		return membership.Members[left].NodeRef < membership.Members[right].NodeRef
	})
	membership.ControlAuthority.MemberRefs = append([]string(nil), membership.ControlAuthority.MemberRefs...)
	sort.Strings(membership.ControlAuthority.MemberRefs)
	return membership
}

func expectedAuthority() Authority {
	return Authority{
		Source: "resolved-plan",
		PlanBinding: PlanBinding{
			CurrentHashField: "planHash", TargetHashField: "planHash",
			StackIdentityField: "stackId", FleetIdentityField: "fleetRef",
			SpecIdentityField: "specHash", InventoryField: "inventoryHash",
		},
		OwnerApprovalRequired: true, LocalCustodyRequired: true,
		ProviderLifecycleOwned: false, MultiServerOrchestrationOwned: false,
		CredentialCustodyOwned: false,
	}
}

func expectedOperationContracts() map[Operation]OperationContract {
	operation := func(name Operation, sourceMember, targetMember string, targetMustDiffer, destructive, checkpoint bool, approvalClass string, phases ...string) OperationContract {
		phaseCopy := append([]string(nil), phases...)
		return OperationContract{
			Name: name, SourceMember: sourceMember, TargetMember: targetMember,
			TargetPlanRequired: true, TargetPlanMustDiffer: targetMustDiffer,
			Mutation: true, Destructive: destructive,
			OwnerApproval:      Approval{Required: true, Class: approvalClass},
			CheckpointRequired: checkpoint, RecoveryRequired: true, Phases: phaseCopy,
			Evidence: Evidence{
				Schema: "stackkit.fleet-mutation-evidence/v1", Immutable: true,
				LocalCustody: true, RequiredPhases: append([]string(nil), phaseCopy...),
			},
		}
	}
	return map[Operation]OperationContract{
		OperationAdd: operation(OperationAdd, "forbidden", "required", true, false, true, "owner-step-up",
			"admit", "checkpoint", "attach", "place", "verify", "commit"),
		OperationReplace: operation(OperationReplace, "required", "required", true, true, true, "owner-step-up",
			"admit", "checkpoint", "drain", "attach", "place", "verify", "detach", "commit"),
		OperationDrain: operation(OperationDrain, "required", "optional", true, true, true, "owner-step-up",
			"admit", "checkpoint", "drain", "verify", "commit"),
		OperationRecover: operation(OperationRecover, "required", "optional", false, true, false, "break-glass",
			"admit", "inspect", "resume-or-rollback", "verify", "commit"),
		OperationRemove: operation(OperationRemove, "required", "forbidden", true, true, true, "owner-step-up",
			"admit", "checkpoint", "drain", "detach", "verify", "commit"),
	}
}
