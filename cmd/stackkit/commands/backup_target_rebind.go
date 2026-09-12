package commands

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Rebinding first detaches only the old opaque Inventory projection, retaining
// signed encrypted custody. Existing generation then resolves the current intent
// without the old spec hash blocking it. Any interrupted attempt is safely
// resumed with the same explicit rebind command and unchanged credentials.
func prepareBackupTargetRebind(cmd *cobra.Command, material backupcustody.S3TargetMaterial) error {
	workspace := getWorkDir()
	if err := withLifecycleMutation(workspace, "backup target rebind", func() error {
		if err := backupcustody.VerifyS3RebindMaterial(workspace, material); err != nil {
			return err
		}
		stored, err := backupcustody.StoredS3TargetAuthority(workspace)
		if err != nil {
			return err
		}
		raw, path, err := locateArchitectureV2Inventory(workspace, "")
		if err != nil {
			return err
		}
		var inventory map[string]any
		if err := yaml.Unmarshal(raw, &inventory); err != nil {
			return errors.New("backup target Inventory is malformed")
		}
		sites, _ := inventory["externalBackupTargetBindings"].(map[string]any)
		bindings, _ := sites[stored.Binding.SiteRef].(map[string]any)
		if current, exists := bindings[stored.Binding.CapabilityRef]; exists {
			binding, ok := current.(map[string]any)
			if !ok || binding["bindingHash"] != stored.Binding.BindingHash {
				return errors.New("backup target Inventory differs from owner custody before rebind")
			}
			delete(bindings, stored.Binding.CapabilityRef)
			relative, err := federationWorkspacePath(workspace, path, "Inventory")
			if err != nil {
				return err
			}
			canonical, err := resolvedplan.CanonicalJSON(inventory)
			if err != nil {
				return err
			}
			return writeFederationPrivateAtomic(workspace, relative, canonical)
		}
		return nil
	}); err != nil {
		return err
	}
	gate := newArchitectureV2ExecutionGate()
	handled, err := gate.preflight(workspace, specFile, architectureV2Generate, architectureV2ExecutionCLIOptions{context: cmd.Context()})
	if err != nil {
		return err
	}
	if !handled {
		return errors.New("backup target rebind requires native StackSpec generation")
	}
	return nil
}
