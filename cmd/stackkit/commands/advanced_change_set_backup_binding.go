package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"gopkg.in/yaml.v3"
)

// A backup target requirement binds the hash of the StackSpec it was compiled
// from, so the binding a node issued for the applied spec never matches the
// requirement of a candidate spec. The node holds the custody of the target
// and issued that binding itself, so it also issues the candidate's: the same
// target, custody attestation, release and validity window under the
// candidate's requirement. Create admits the candidate with that binding
// without persisting it; apply persists it into the node Inventory and the
// custody authority at the point the candidate becomes the applied baseline.
// Both sides derive the binding from the persisted one, so they agree on every
// byte and the Owner-signed candidate plan hash covers it.
type advancedCandidateBackupBinding struct {
	requirement  resolvedplan.BackupTargetRequirement
	binding      resolvedplan.ExternalBackupTargetBinding
	previousHash string
}

// resolveAdvancedCandidate resolves the candidate against the persisted
// Inventory. Only when the node's own backup target binding is what rejects
// it, the candidate resolves against the Inventory holding the node's
// successor binding instead.
func resolveAdvancedCandidate(
	service *architecturev2.Service, workspace string, candidateRaw, inventory []byte,
) (architecturev2.CurrentResolution, *advancedCandidateBackupBinding, error) {
	resolve := func(inventory []byte) (architecturev2.CurrentResolution, error) {
		return service.ResolveCurrentScoped(
			architecturev2.ResolveInput{StackSpec: candidateRaw, Inventory: inventory}, advancedCandidateAuthorityScope,
		)
	}
	current, err := resolve(inventory)
	var compileErr *resolvedplan.CompileError
	if err == nil || !errors.As(err, &compileErr) || compileErr.Code != resolvedplan.ErrExternalBackupTargetBindingMismatch {
		return current, nil, err
	}
	successorInventory, successor, successorErr := advancedBackupBindingSuccessor(service, workspace, candidateRaw, inventory)
	if successorErr != nil {
		return architecturev2.CurrentResolution{}, nil, errors.Join(err, fmt.Errorf("candidate backup target binding: %w", successorErr))
	}
	current, err = resolve(successorInventory)
	return current, successor, err
}

// advancedBackupBindingSuccessor issues the node's binding for the candidate's
// backup target requirement and returns the Inventory that holds it. It
// refuses a candidate whose requirement names any other target than the one
// in the node's custody.
func advancedBackupBindingSuccessor(
	service *architecturev2.Service, workspace string, candidateRaw, inventory []byte,
) ([]byte, *advancedCandidateBackupBinding, error) {
	stored, err := backupcustody.StoredS3TargetAuthority(workspace)
	if err != nil {
		return nil, nil, fmt.Errorf("the node holds no backup target custody to issue the binding from: %w", err)
	}
	siteRef, capabilityRef := stored.Binding.SiteRef, stored.Binding.CapabilityRef
	bindingAt := func(document map[string]any) (map[string]any, map[string]any) {
		sites, _ := document["externalBackupTargetBindings"].(map[string]any)
		bindings, _ := sites[siteRef].(map[string]any)
		binding, _ := bindings[capabilityRef].(map[string]any)
		return bindings, binding
	}
	document, err := decodeInventoryDocument(inventory)
	if err != nil {
		return nil, nil, err
	}
	bindings, previous := bindingAt(document)
	if previous == nil || previous["bindingHash"] != stored.Binding.BindingHash {
		return nil, nil, errors.New("the Inventory binding differs from the binding in owner custody")
	}
	// The requirement of a spec does not depend on its binding, and an
	// Inventory without the binding still resolves (the Apply readiness only
	// reports it missing), so it yields the candidate's requirement.
	delete(bindings, capabilityRef)
	unbound, err := resolvedplan.CanonicalJSON(document)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: candidateRaw, Inventory: unbound})
	if err != nil {
		return nil, nil, err
	}
	var plan struct {
		Requirements map[string]map[string]map[string]any `json:"backupTargetRequirements"`
	}
	if err := json.Unmarshal(resolved.CanonicalPlan, &plan); err != nil {
		return nil, nil, err
	}
	requirement := resolvedplan.BackupTargetRequirement(plan.Requirements[siteRef][capabilityRef])
	if requirement == nil {
		return nil, nil, errors.New("the candidate has no backup target requirement for the custodied target")
	}
	binding, err := resolvedplan.ReissueExternalBackupTargetBinding(resolvedplan.ExternalBackupTargetBinding(previous), requirement)
	if err != nil {
		return nil, nil, err
	}
	if binding["backupTargetRef"] != stored.Binding.BackupTargetRef || binding["custodyAttestationRef"] != stored.Binding.CustodyAttestationRef {
		return nil, nil, errors.New("the Inventory binding names another target than owner custody")
	}
	original, err := decodeInventoryDocument(inventory)
	if err != nil {
		return nil, nil, err
	}
	updated, err := resolvedplan.AttachExternalBackupTargetBinding(resolvedplan.InventoryFacts(original), requirement, binding, stored.Binding.BindingHash)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := resolvedplan.CanonicalJSON(updated)
	if err != nil {
		return nil, nil, err
	}
	return encoded, &advancedCandidateBackupBinding{requirement: requirement, binding: binding, previousHash: stored.Binding.BindingHash}, nil
}

// advancedCandidateBackupAuthority is the owner custody authority the
// candidate's Apply verifies: the candidate plan's projection of the node's
// binding and the digest of the backup source policy the candidate renders.
// It is derived from the verified candidate and its render, before any side
// effect, so a candidate that cannot be custodied is refused up front.
func advancedCandidateBackupAuthority(
	plan generationartifact.VerifiedPlan, render architecturev2renderer.RenderResult, owner localevidence.OwnerCustody,
) (backupcustody.S3TargetAuthority, error) {
	var authority backupcustody.S3TargetAuthority
	for _, binding := range plan.ApplyRequirements().BackupTargetBindings {
		if binding.SiteRef == owner.Binding.SiteRef && slices.Equal(binding.TargetNodeRefs, []string{owner.Binding.NodeRef}) {
			if authority.Binding.BindingHash != "" {
				return authority, errors.New("candidate backup target authority is ambiguous")
			}
			authority.Binding = binding
		}
	}
	if authority.Binding.BindingHash == "" {
		return authority, errors.New("candidate plan carries no backup target binding for this node")
	}
	_, requirement, err := nativeV2BackupPolicyRequirement(plan, owner.Binding.SiteRef, owner.Binding.NodeRef)
	if err != nil {
		return authority, err
	}
	for _, artifact := range render.Artifacts() {
		if artifact.ID != requirement.ID {
			continue
		}
		policy, err := localbackuppolicy.Decode(artifact.Bytes)
		if err != nil {
			return authority, err
		}
		authority.SourceDigest, err = localbackuppolicy.SourceDigest(policy.SourceProjection())
		return authority, err
	}
	return authority, errors.New("candidate render carries no backup source policy")
}

// adoptAdvancedCandidateBackupBinding makes the candidate the applied baseline
// for the node's backup target: it attaches the successor binding to the node
// Inventory and moves owner custody to the candidate's authority, with the
// credentials already in custody. The caller holds the lifecycle mutation and
// runs this before the target generate. The returned restore moves custody
// back when the target fails; the checkpoint recovery restores the Inventory.
func adoptAdvancedCandidateBackupBinding(workspace string, verified verifiedAdvancedMutation) (restore func() error, err error) {
	successor := verified.admission.candidateBackup
	if successor == nil {
		return func() error { return nil }, nil
	}
	if verified.candidateBackupAuthority == nil {
		return nil, errors.New("the candidate backup target authority was not derived during admission")
	}
	previous, err := backupcustody.StoredS3TargetAuthority(workspace)
	if err != nil {
		return nil, err
	}
	raw, path, err := locateArchitectureV2Inventory(workspace, "")
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("backup target binding requires the existing owner Inventory")
	}
	var inventory map[string]any
	if err := yaml.Unmarshal(raw, &inventory); err != nil {
		return nil, errors.New("backup target Inventory is malformed")
	}
	updated, err := resolvedplan.AttachExternalBackupTargetBinding(inventory, successor.requirement, successor.binding, successor.previousHash)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(inventory, map[string]any(updated)) {
		relative, err := federationWorkspacePath(workspace, path, "Inventory")
		if err != nil {
			return nil, err
		}
		canonical, err := resolvedplan.CanonicalJSON(updated)
		if err != nil {
			return nil, err
		}
		if err := writeFederationPrivateAtomic(workspace, relative, canonical); err != nil {
			return nil, err
		}
	}
	restore = func() error { return backupcustody.RebindStoredS3Target(workspace, previous) }
	if err := backupcustody.RebindStoredS3Target(workspace, *verified.candidateBackupAuthority); err != nil {
		// The move may have landed before its read-back failed.
		return nil, errors.Join(err, restore())
	}
	return restore, nil
}
