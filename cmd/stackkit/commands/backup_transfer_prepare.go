package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/spf13/cobra"
)

type applicationTransferPreparation struct {
	APIVersion        string                                         `json:"apiVersion"`
	TransferID        string                                         `json:"transferId"`
	SourceOwner       string                                         `json:"sourceOwner"`
	SourceFingerprint string                                         `json:"sourceFingerprint"`
	Destination       backuplifecycle.ApplicationTransferDestination `json:"destination"`
	SecretDigests     map[string]map[string]string                   `json:"secretDigests"`
	Signature         localevidence.OwnerPolicyStateSignature        `json:"signature"`
}

func transferPreparationPath(id string) string {
	return ".stackkit/backups/transfer-preparations/" + strings.TrimPrefix(id, "sha256:") + ".json"
}

func runApplicationTransferPrepare(cmd *cobra.Command, _ []string) error {
	if err := requireApplicationTransferDestinationMode(getWorkDir()); err != nil {
		return err
	}
	values := map[string]string{}
	for _, f := range []string{"archive", "archive-sha256", "identity-file", "source-receipt", "source-owner", "source-fingerprint", "transfer-id", "expected-plan-hash"} {
		values[f], _ = cmd.Flags().GetString(f)
		if values[f] == "" {
			return fmt.Errorf("--%s is required", f)
		}
	}
	admittedReceipt, err := readAdmittedApplicationTransferReceipt(values)
	if err != nil {
		return err
	}
	approved, _ := cmd.Flags().GetBool("owner-approve")
	if !approved {
		return errors.New("transfer preparation requires --owner-approve")
	}
	operation, err := restoreactivation.TransferOperationID(values["transfer-id"])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	generated, err := inspectNativeV2GeneratedAuthority(ctx, getWorkDir(), specFile)
	if err != nil {
		return err
	}
	if err = generated.Plan.RequireExpectedPlanHash(values["expected-plan-hash"]); err != nil {
		return err
	}
	root, err := confinedfs.Open(getWorkDir())
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	_, requirement, err := nativeV2BackupPolicyRequirement(generated.Plan, generated.Owner.Binding.SiteRef, generated.Owner.Binding.NodeRef)
	if err != nil {
		return err
	}
	_, _, policy, err := readNativeV2BackupPolicyForArtifact(tx, generated.Manifest, requirement.ID)
	if err != nil {
		return err
	}
	file, err := os.Open(values["archive"])
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err = errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.TrimPrefix(values["archive-sha256"], "sha256:") {
		return errors.New("transfer ciphertext differs from admitted archive")
	}
	identities, err := backuplifecycle.ReadEmergencyIdentities(values["identity-file"])
	if err != nil {
		return err
	}
	extraction, cleanup, err := newApplicationTransferExtraction(generated.WorkspaceRoot, operation+"-prepare")
	if err != nil {
		return err
	}
	defer cleanup()
	maxBytes, _ := cmd.Flags().GetInt64("max-bytes")
	staged, err := backuplifecycle.RestoreEmergency(ctx, backuplifecycle.EmergencyRestoreInput{Archive: values["archive"], Identities: identities, Target: extraction, MaxBytes: maxBytes, MemoryFile: fmt.Sprintf("sources/%04d/application-secrets.json", *admittedReceipt.Export.Manifest.ApplicationTransfer.CredentialSourceIndex)})
	if err != nil {
		return err
	}
	defer func() {
		for _, data := range staged.PrivateFiles {
			clear(data)
		}
	}()
	t, err := backuplifecycle.VerifyApplicationTransfer(staged.Manifest, values["source-owner"], values["source-fingerprint"], values["transfer-id"])
	if err != nil {
		return err
	}
	if err = verifyApplicationTransferIssuedAdmission(generated.WorkspaceRoot, t, true); err != nil {
		return err
	}
	var plan struct {
		StackID string `json:"stackId"`
	}
	if err = json.Unmarshal(generated.Plan.Canonical(), &plan); err != nil {
		return err
	}
	d := t.Destination
	if d.OwnerRef != generated.OwnerRef || d.StackID != plan.StackID || d.SiteRef != generated.Owner.Binding.SiteRef || d.NodeRef != generated.Owner.Binding.NodeRef || d.PlanHash != generated.Plan.Binding().PlanHash {
		return errors.New("transfer preparation differs from exact receiving Owner/stack/node/candidate Plan")
	}
	if err = backuplifecycle.VerifyApplicationTransferStaging(staged.Target, staged.Manifest, staged.PrivateFiles); err != nil {
		return err
	}
	workloads := make([]string, len(t.Runtimes))
	for i, r := range t.Runtimes {
		workloads[i] = r.WorkloadRef
	}
	closure, err := restoreactivation.ApplicationTransferProjection(generated.WorkspaceRoot, generated.Plan, generated.Manifest, workloads, policy)
	if err != nil {
		return err
	}
	if err = restoreactivation.RequireApplicationTransferConfiguration(t, generated.Plan, workloads); err != nil {
		return err
	}
	if _, err = backuplifecycle.MatchApplicationTransfer(t, policy, closure); err != nil {
		return err
	}
	bindings, err := restoreactivation.ApplicationTransferSecretBindings(generated.WorkspaceRoot, generated.Plan, generated.Manifest, workloads)
	if err != nil {
		return err
	}
	for workload, slots := range bindings {
		expected, exists := t.SecretDigests[workload]
		if !exists || len(slots) != len(expected) {
			return errors.New("source and destination secret slots differ")
		}
		for slot, ref := range slots {
			if ref != "secret://workloads/"+workload+"/"+slot {
				return errors.New("initial transfer preparation only adopts workload-scoped generated secret refs")
			}
		}
	}
	credentialPath := fmt.Sprintf("sources/%04d/application-secrets.json", *t.CredentialSourceIndex)
	material, exists := staged.PrivateFiles[credentialPath]
	if !exists {
		return errors.New("transfer credentials were not extracted into memory")
	}
	defer clear(material)
	var payload backuplifecycle.ApplicationTransferSecretPayload
	if err = json.Unmarshal(material, &payload); err != nil {
		return errors.New("transfer credential payload is invalid")
	}
	if len(payload.CommitmentKey) != 32 {
		return errors.New("transfer commitment key is invalid")
	}
	defer clear(payload.CommitmentKey)
	secrets := payload.Materials
	defer restoreactivation.ClearApplicationTransferSecrets(secrets)
	if len(secrets) != len(bindings) {
		return errors.New("transfer credential payload has an unselected workload")
	}
	for workload, slots := range bindings {
		if len(secrets[workload]) != len(slots) {
			return errors.New("transfer credential payload has an unexpected slot")
		}
		for slot := range slots {
			value := secrets[workload][slot]
			if len(value) == 0 || len(value) > 8192 {
				return errors.New("transfer credential payload has an invalid secret size")
			}
			if backuplifecycle.ApplicationTransferSecretCommitment(payload.CommitmentKey, workload, slot, value) != t.SecretDigests[workload][slot] {
				return errors.New("transfer credential payload differs from signed source digest")
			}
		}
	}
	var receipt applicationTransferPreparation
	err = withLifecycleMutation(generated.WorkspaceRoot, "backup transfer-prepare", func() error {
		return withArchitectureV2OutputLock(generated.WorkspaceRoot, generated.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
			if e := requireApplicationTransferDestinationMode(generated.WorkspaceRoot); e != nil {
				return e
			}
			if _, exists, e := readStoredApplicationTransferOutcome(generated.WorkspaceRoot, t, "stopped"); e != nil {
				return e
			} else if exists {
				return errors.New("aborted transfer cannot adopt credentials")
			}
			if _, exists, e := readStoredApplicationTransferOutcome(generated.WorkspaceRoot, t, "stopped", true); e != nil {
				return e
			} else if exists {
				return errors.New("pending abort cannot adopt credentials")
			}
			current, e := inspectNativeV2GeneratedAuthority(ctx, getWorkDir(), specFile)
			if e != nil {
				return e
			}
			if current.Plan.Binding() != generated.Plan.Binding() || current.OwnerRef != generated.OwnerRef {
				return errors.New("receiving candidate authority changed")
			}
			if e = restoreactivation.RequireApplicationTransferTargetAbsent(ctx, generated.WorkspaceRoot, policy, workloads, closure); e != nil {
				return e
			}
			if e = cleanupApplicationTransferExtractions(generated.WorkspaceRoot, operation, staged.Target); e != nil {
				return e
			}
			if e = consumeApplicationTransferAdmission(generated.WorkspaceRoot, t, values["source-fingerprint"], values["archive-sha256"]); e != nil {
				return e
			}
			if e = localevidence.StoreEncryptedLocalIssuedSecret(generated.WorkspaceRoot, backuplifecycle.ApplicationTransferCommitmentKeyRef(t.ID), payload.CommitmentKey); e != nil {
				return e
			}
			for workload, slots := range bindings {
				for slot, ref := range slots {
					if e = localevidence.StoreEncryptedLocalIssuedSecret(generated.WorkspaceRoot, ref, secrets[workload][slot]); e != nil {
						return e
					}
				}
			}
			if e = restoreactivation.VerifyApplicationTransferSecretCustody(generated.WorkspaceRoot, t, bindings); e != nil {
				return e
			}
			receipt = applicationTransferPreparation{APIVersion: "stackkit.application-transfer-preparation/v1", TransferID: t.ID, SourceOwner: values["source-owner"], SourceFingerprint: values["source-fingerprint"], Destination: d, SecretDigests: t.SecretDigests}
			canonical, e := json.Marshal(receipt)
			if e != nil {
				return e
			}
			receipt.Signature, e = localevidence.SignOwnerPolicyState(generated.WorkspaceRoot, canonical)
			if e != nil {
				return e
			}
			encoded, e := json.Marshal(receipt)
			if e != nil {
				return e
			}
			if e = tx.MkdirAll(filepath.ToSlash(filepath.Dir(transferPreparationPath(t.ID))), 0o700); e != nil {
				return e
			}
			if prior, _, e := tx.ReadStable(transferPreparationPath(t.ID)); e == nil {
				if string(prior) == string(encoded) {
					return nil
				}
				return errors.New("preparation receipt differs from original admitted credential closure")
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
			return tx.WriteFileExclusive(transferPreparationPath(t.ID), encoded, 0o600)
		})
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(receipt)
}

func verifyApplicationTransferPreparation(workspace string, t backuplifecycle.ApplicationTransfer, sourceFingerprint string) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStable(transferPreparationPath(t.ID))
	if err != nil {
		return errors.New("transfer-import requires successful transfer-prepare before the receiving application's first Apply")
	}
	var receipt applicationTransferPreparation
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	signature := receipt.Signature
	receipt.Signature = localevidence.OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err = localevidence.VerifyOwnerPolicyState(workspace, canonical, signature); err != nil {
		return err
	}
	if receipt.SourceOwner != t.SourceOwner || receipt.SourceFingerprint != sourceFingerprint || receipt.TransferID != t.ID || receipt.Destination != t.Destination {
		return errors.New("receiving credential preparation differs from transfer admission")
	}
	return nil
}
