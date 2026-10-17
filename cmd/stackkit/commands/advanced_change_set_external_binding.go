package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"gopkg.in/yaml.v3"
)

// Home backup target and federation link bindings bind the StackSpec hash like
// the node's backup target binding, so a candidate that changes the spec fails
// with *_binding_mismatch. They are issued from admission evidence, not by the
// node, so the successor is derived only from the binding already in the
// node Inventory: same target, custody attestation, release, Candidate and
// validity window, never renewed (resolvedplan.ReissueInventoryExternalBindings).
// Create admits the candidate with the successors without persisting them;
// apply writes them into the Inventory when the candidate becomes the baseline,
// and the checkpoint recovery restores the Inventory if the target fails.

// advancedExternalBindingSuccessors returns the Inventory holding the
// successors of every Home and federation binding the candidate invalidates.
func advancedExternalBindingSuccessors(
	service *architecturev2.Service, candidateRaw, inventory []byte,
) ([]byte, []resolvedplan.ReissuedExternalBinding, error) {
	document, err := decodeInventoryDocument(inventory)
	if err != nil {
		return nil, nil, err
	}
	// The requirements of a spec do not depend on its bindings, and an
	// Inventory without them still resolves, so it yields the candidate's.
	unbound, err := decodeInventoryDocument(inventory)
	if err != nil {
		return nil, nil, err
	}
	delete(unbound, "externalHomeBackupTargetBindings")
	delete(unbound, "externalFederationLinkBindings")
	unboundRaw, err := resolvedplan.CanonicalJSON(unbound)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: candidateRaw, Inventory: unboundRaw})
	if err != nil {
		return nil, nil, err
	}
	var plan struct {
		Home       map[string]any `json:"homeBackupTargetRequirements"`
		Federation map[string]any `json:"federationLinkRequirements"`
	}
	if err := json.Unmarshal(resolved.CanonicalPlan, &plan); err != nil {
		return nil, nil, err
	}
	updated, reissued, err := resolvedplan.ReissueInventoryExternalBindings(resolvedplan.InventoryFacts(document), plan.Home, plan.Federation)
	if err != nil {
		return nil, nil, err
	}
	if len(reissued) == 0 {
		return nil, nil, errors.New("no Home or federation binding in the Inventory can be carried over to the candidate")
	}
	encoded, err := resolvedplan.CanonicalJSON(updated)
	if err != nil {
		return nil, nil, err
	}
	return encoded, reissued, nil
}

// adoptAdvancedCandidateExternalBindings writes the candidate's Home and
// federation bindings into the node Inventory before the target resolves the
// candidate.
func adoptAdvancedCandidateExternalBindings(workspace string, verified verifiedAdvancedMutation) error {
	reissued := verified.admission.candidateExternal
	if len(reissued) == 0 {
		return nil
	}
	raw, path, err := locateArchitectureV2Inventory(workspace, "")
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("Home and federation binding re-issue requires the existing owner Inventory")
	}
	var inventory map[string]any
	if err := yaml.Unmarshal(raw, &inventory); err != nil {
		return errors.New("Home and federation binding Inventory is malformed")
	}
	updated, err := resolvedplan.ApplyReissuedExternalBindings(inventory, reissued)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(inventory, map[string]any(updated)) {
		return nil
	}
	relative, err := federationWorkspacePath(workspace, path, "Inventory")
	if err != nil {
		return err
	}
	canonical, err := resolvedplan.CanonicalJSON(updated)
	if err != nil {
		return err
	}
	if err := writeFederationPrivateAtomic(workspace, relative, canonical); err != nil {
		return fmt.Errorf("persist Home and federation bindings: %w", err)
	}
	return nil
}
