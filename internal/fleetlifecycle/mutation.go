package fleetlifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// MutationAuthority is the immutable authority carried by every durable fleet
// mutation record and phase-evidence document.
type MutationAuthority struct {
	StackID              string    `json:"stackId"`
	FleetRef             string    `json:"fleetRef,omitempty"`
	Operation            Operation `json:"operation"`
	CurrentPlanHash      string    `json:"currentPlanHash"`
	TargetPlanHash       string    `json:"targetPlanHash"`
	CurrentSpecHash      string    `json:"currentSpecHash"`
	TargetSpecHash       string    `json:"targetSpecHash"`
	CurrentInventoryHash string    `json:"currentInventoryHash"`
	TargetInventoryHash  string    `json:"targetInventoryHash"`
	SourceMemberRef      string    `json:"sourceMemberRef,omitempty"`
	TargetMemberRef      string    `json:"targetMemberRef,omitempty"`
	ContractDigest       string    `json:"contractDigest"`
}

// Mutation is the exact compiler-owned operation bound to current and target
// ResolvedPlans. The operation contract is kept beside the persisted authority
// so runners never reconstruct phases or approval semantics.
type Mutation struct {
	Authority MutationAuthority
	Contract  OperationContract
}

// BindMutation verifies both plans and admits only the membership delta owned
// by the selected compiler contract.
func BindMutation(
	currentPlan, targetPlan resolvedplan.ResolvedPlan,
	operation Operation,
	sourceMemberRef, targetMemberRef string,
) (Mutation, error) {
	current, err := Project(currentPlan)
	if err != nil {
		return Mutation{}, fmt.Errorf("project current fleet lifecycle: %w", err)
	}
	target, err := Project(targetPlan)
	if err != nil {
		return Mutation{}, fmt.Errorf("project target fleet lifecycle: %w", err)
	}
	contract, ok := current.Operations[operation]
	if !ok {
		return Mutation{}, fmt.Errorf("fleet operation %q is not admitted by the current ResolvedPlan", operation)
	}
	targetContract, ok := target.Operations[operation]
	if !ok || !reflect.DeepEqual(contract, targetContract) {
		return Mutation{}, errors.New("fleet operation contract differs between current and target ResolvedPlans")
	}
	if current.StackID != target.StackID || current.FleetRef != target.FleetRef {
		return Mutation{}, errors.New("fleet mutation target belongs to a different Stack or Fleet")
	}
	currentPlanHash, err := canonicalPlanHashField(currentPlan)
	if err != nil {
		return Mutation{}, fmt.Errorf("current fleet plan: %w", err)
	}
	targetPlanHash, err := canonicalPlanHashField(targetPlan)
	if err != nil {
		return Mutation{}, fmt.Errorf("target fleet plan: %w", err)
	}
	if contract.TargetPlanMustDiffer && currentPlanHash == targetPlanHash {
		return Mutation{}, fmt.Errorf("fleet operation %q requires a different target ResolvedPlan", operation)
	}
	sourceMemberRef = strings.TrimSpace(sourceMemberRef)
	targetMemberRef = strings.TrimSpace(targetMemberRef)
	if err := validateMutationMembers(
		operation, current.Membership, target.Membership,
		sourceMemberRef, targetMemberRef,
	); err != nil {
		return Mutation{}, err
	}
	contractDigest, err := canonicalContractDigest(contract)
	if err != nil {
		return Mutation{}, err
	}
	return Mutation{
		Authority: MutationAuthority{
			StackID: current.StackID, FleetRef: current.FleetRef,
			Operation: operation, CurrentPlanHash: currentPlanHash,
			TargetPlanHash:  targetPlanHash,
			CurrentSpecHash: current.SpecHash, TargetSpecHash: target.SpecHash,
			CurrentInventoryHash: current.InventoryHash,
			TargetInventoryHash:  target.InventoryHash,
			SourceMemberRef:      sourceMemberRef, TargetMemberRef: targetMemberRef,
			ContractDigest: contractDigest,
		},
		Contract: contract,
	}, nil
}

func canonicalPlanHashField(plan resolvedplan.ResolvedPlan) (string, error) {
	hash, ok := plan["planHash"].(string)
	if !ok || !digestPattern.MatchString(hash) {
		return "", errors.New("ResolvedPlan planHash is invalid")
	}
	return hash, nil
}

func canonicalContractDigest(contract OperationContract) (string, error) {
	raw, err := resolvedplan.CanonicalJSON(contract)
	if err != nil {
		return "", fmt.Errorf("canonicalize fleet operation contract: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateMutationMembers(
	operation Operation,
	current, target Membership,
	sourceRef, targetRef string,
) error {
	currentControl := normalizedMembership(current).ControlAuthority
	targetControl := normalizedMembership(target).ControlAuthority
	if !reflect.DeepEqual(currentControl, targetControl) {
		return errors.New("supplemental fleet mutation cannot change ControlAuthority")
	}
	currentMembers := membershipByRef(current)
	targetMembers := membershipByRef(target)
	requireCurrent := func(ref, role string) error {
		if ref == "" {
			return fmt.Errorf("fleet %s member reference is required", role)
		}
		if _, ok := currentMembers[ref]; !ok {
			return fmt.Errorf("fleet %s member %q is absent from the current ResolvedPlan", role, ref)
		}
		return nil
	}
	requireTarget := func(ref, role string) error {
		if ref == "" {
			return fmt.Errorf("fleet %s member reference is required", role)
		}
		if _, ok := targetMembers[ref]; !ok {
			return fmt.Errorf("fleet %s member %q is absent from the target ResolvedPlan", role, ref)
		}
		return nil
	}

	switch operation {
	case OperationAdd:
		if sourceRef != "" {
			return errors.New("fleet add forbids a source member")
		}
		if err := requireTarget(targetRef, "target"); err != nil {
			return err
		}
		if _, exists := currentMembers[targetRef]; exists {
			return errors.New("fleet add target already belongs to the current ResolvedPlan")
		}
		return requireOnlyMemberChanges(currentMembers, targetMembers, "", targetRef)
	case OperationReplace:
		if err := requireCurrent(sourceRef, "source"); err != nil {
			return err
		}
		if err := requireTarget(targetRef, "target"); err != nil {
			return err
		}
		if sourceRef == targetRef {
			return errors.New("fleet replace requires distinct source and target members")
		}
		if _, exists := targetMembers[sourceRef]; exists {
			return errors.New("fleet replace source remains in the target ResolvedPlan")
		}
		if _, exists := currentMembers[targetRef]; exists {
			return errors.New("fleet replace target already belongs to the current ResolvedPlan")
		}
		return requireOnlyMemberChanges(currentMembers, targetMembers, sourceRef, targetRef)
	case OperationDrain:
		if err := requireCurrent(sourceRef, "source"); err != nil {
			return err
		}
		if targetRef != "" {
			if targetRef == sourceRef {
				return errors.New("fleet drain target must differ from its source")
			}
			if err := requireTarget(targetRef, "target"); err != nil {
				return err
			}
			if _, ok := currentMembers[targetRef]; !ok {
				return errors.New("fleet drain target must already belong to the current ResolvedPlan")
			}
		}
		if member, retained := targetMembers[sourceRef]; retained &&
			(member.Enabled || member.ControlPlaneMember) {
			return errors.New("fleet drain source must be removed or disabled outside ControlAuthority")
		}
		return requireOnlyMemberChanges(currentMembers, targetMembers, sourceRef, "")
	case OperationRecover:
		if err := requireCurrent(sourceRef, "source"); err != nil {
			return err
		}
		if targetRef != "" {
			if err := requireTarget(targetRef, "target"); err != nil {
				return err
			}
		}
		return requireOnlyMemberChanges(currentMembers, targetMembers, sourceRef, targetRef)
	case OperationRemove:
		if err := requireCurrent(sourceRef, "source"); err != nil {
			return err
		}
		if targetRef != "" {
			return errors.New("fleet remove forbids a target member")
		}
		if _, exists := targetMembers[sourceRef]; exists {
			return errors.New("fleet remove source remains in the target ResolvedPlan")
		}
		return requireOnlyMemberChanges(currentMembers, targetMembers, sourceRef, "")
	default:
		return fmt.Errorf("unsupported fleet operation %q", operation)
	}
}

func membershipByRef(membership Membership) map[string]Member {
	members := make(map[string]Member, len(membership.Members))
	for _, member := range membership.Members {
		normalized := member
		normalized.Roles = append([]string(nil), member.Roles...)
		sort.Strings(normalized.Roles)
		members[member.NodeRef] = normalized
	}
	return members
}

func requireOnlyMemberChanges(
	current, target map[string]Member,
	sourceRef, targetRef string,
) error {
	allowed := map[string]bool{}
	if sourceRef != "" {
		allowed[sourceRef] = true
	}
	if targetRef != "" {
		allowed[targetRef] = true
	}
	for ref, member := range current {
		if allowed[ref] {
			continue
		}
		if targetMember, ok := target[ref]; !ok || !reflect.DeepEqual(member, targetMember) {
			return fmt.Errorf("fleet mutation changes unrelated member %q", ref)
		}
	}
	for ref, member := range target {
		if allowed[ref] {
			continue
		}
		if currentMember, ok := current[ref]; !ok || !reflect.DeepEqual(member, currentMember) {
			return fmt.Errorf("fleet mutation changes unrelated member %q", ref)
		}
	}
	return nil
}
