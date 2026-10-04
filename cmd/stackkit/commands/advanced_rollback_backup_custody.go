package commands

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"gopkg.in/yaml.v3"
)

// restoreBackupCustody makes the owner custody of the node's off-site backup
// target follow the baseline the checkpoint restores. The checkpoint restores
// the StackSpec and the Inventory, whose backup target binding is the one the
// node issued for the checkpoint's spec; a change set that succeeded since
// then moved the custody authority to its candidate's binding, so without
// this the rollback apply fails on "Cloud offsite target custody does not
// verify". It runs under the rollback's lifecycle mutation, before the joined
// generate, and repeats harmlessly when the rollback resumes.
func (rollback *coordinatedRollback) restoreBackupCustody() error {
	advancedRollbackEvent("restore-backup-custody", "started", nil)
	if err := followCheckpointBackupCustody(rollback.workspace, rollback.custody.Artifacts); err != nil {
		rolloutFailure(advancedRollbackRolloutPrefix+"restore-backup-custody", err)
		return fmt.Errorf("restore the backup target custody of the checkpoint: %w", err)
	}
	advancedRollbackEvent("restore-backup-custody", "succeeded", nil)
	return nil
}

// followCheckpointBackupCustody moves the owner custody authority of the S3
// backup target to the authority the checkpoint's own ResolvedPlan and
// generated backup source policy carry for this node, from the credentials
// already in custody (backupcustody.RebindStoredS3Target; nobody supplies
// material). artifacts are the checkpoint's verified generation artifacts by
// ID. It changes nothing when the node holds no backup target custody, when
// the checkpoint carries no binding for the node, or when custody already
// holds the checkpoint's authority.
//
// It refuses any other target, as ReissueExternalBackupTargetBinding does: the
// checkpoint must name the same Stack, Site, node, capability, contract owner
// and contract, the same backup target and custody attestation as the custody
// it replaces, and the Inventory restored from the checkpoint must hold the
// binding the checkpoint plan was generated from.
func followCheckpointBackupCustody(workspace string, artifacts map[string][]byte) error {
	stored, err := backupcustody.StoredS3TargetAuthority(workspace)
	if errors.Is(err, backupcustody.ErrMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	planRaw, found := artifacts[nativehost.ResolvedPlanArtifactID]
	if !found {
		return nil
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	plan, err := service.VerifyCanonicalPlan(planRaw)
	if err != nil {
		return fmt.Errorf("verify the checkpoint plan: %w", err)
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	checkpoint, err := nodeBackupTargetAuthority(plan, owner, func(id string) ([]byte, bool) {
		data, found := artifacts[id]
		return data, found
	})
	if errors.Is(err, errNoNodeBackupTargetBinding) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("derive the checkpoint backup target authority: %w", err)
	}
	if !sameBackupTarget(stored.Binding, checkpoint.Binding) {
		return errors.New("the checkpoint names a different backup target than owner custody")
	}
	inventoryHash, err := inventoryBackupBindingHash(workspace, checkpoint.Binding.SiteRef, checkpoint.Binding.CapabilityRef)
	if err != nil {
		return err
	}
	if inventoryHash != checkpoint.Binding.BindingHash {
		return errors.New("the restored Inventory holds another backup target binding than the checkpoint plan")
	}
	if reflect.DeepEqual(stored, checkpoint) {
		return nil
	}
	return backupcustody.RebindStoredS3Target(workspace, checkpoint)
}

// sameBackupTarget reports whether two authorities name the same backup
// target of the same Stack, Site, node, capability and contract; the binding,
// spec and validity window may differ.
func sameBackupTarget(a, b generationartifact.ApplyBackupTargetBindingRequirement) bool {
	return a.StackID == b.StackID && a.SiteRef == b.SiteRef && slices.Equal(a.TargetNodeRefs, b.TargetNodeRefs) &&
		a.CapabilityRef == b.CapabilityRef && a.ContractOwnerRef == b.ContractOwnerRef &&
		a.CapabilityContractHash == b.CapabilityContractHash &&
		a.BackupTargetRef == b.BackupTargetRef && a.CustodyAttestationRef == b.CustodyAttestationRef
}

// inventoryBackupBindingHash is the hash of the backup target binding the node
// Inventory holds for one Site and capability, or "" when it holds none.
func inventoryBackupBindingHash(workspace, siteRef, capabilityRef string) (string, error) {
	raw, path, err := locateArchitectureV2Inventory(workspace, "")
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	var inventory map[string]any
	if err := yaml.Unmarshal(raw, &inventory); err != nil {
		return "", errors.New("backup target Inventory is malformed")
	}
	sites, _ := inventory["externalBackupTargetBindings"].(map[string]any)
	bindings, _ := sites[siteRef].(map[string]any)
	binding, _ := bindings[capabilityRef].(map[string]any)
	hash, _ := binding["bindingHash"].(string)
	return hash, nil
}
