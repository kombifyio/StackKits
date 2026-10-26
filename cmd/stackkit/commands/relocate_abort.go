package commands

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/workloadrelocation"
	relocatecoolify "github.com/kombifyio/stackkits/internal/workloadrelocation/coolify"
	"github.com/spf13/cobra"
	"os"
	"time"
)

func newRelocateAbortCommand() *cobra.Command {
	o := &relocateOptions{}
	c := &cobra.Command{Use: "abort", Short: "Permanently reject a transfer only when no destination restore effects could have begun", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if !o.ownerApprove || o.transferID == "" || o.planPath == "" {
			return errors.New("abort requires Owner approval, exact plan and transfer identity")
		}
		if err := requireCoolifyPlatform(o.platform); err != nil {
			return err
		}
		plan, err := readRelocationPlan(c.InOrStdin(), o.planPath)
		if err != nil {
			return err
		}
		r, err := relocatecoolify.Locate(c.Context(), o.engine)
		if err != nil {
			return err
		}
		raw, err := r.Abort(c.Context(), plan, o.transferID, relocationEngineReceiptPath(o.transferID))
		if err != nil {
			return err
		}
		receipt, err := workloadrelocation.StoreReceipt(getWorkDir(), o.transferID, "aborted", plan, raw)
		if err != nil {
			return err
		}
		observed, err := r.Observe(c.Context(), raw, "aborted", getWorkDir())
		if err != nil {
			return err
		}
		status, err := workloadrelocation.SignStatus(getWorkDir(), receipt, observed)
		if err != nil {
			return err
		}
		return writeCommandResult(c, c.CommandPath(), status)
	}}
	addRelocateCommonFlags(c, o)
	c.Flags().StringVar(&o.planPath, "plan", "", "Exact source plan, or -")
	c.Flags().StringVar(&o.transferID, "transfer-id", "", "Semantic move identity")
	c.Flags().BoolVar(&o.ownerApprove, "owner-approve", false, "Confirm Owner-approved destination abort")
	addRelocateJSONFlag(c, o)
	return c
}

type managedRelocationResume struct {
	APIVersion          string                    `json:"apiVersion"`
	TransferID          string                    `json:"transferId"`
	PlanJSON            string                    `json:"planJson"`
	Destination         workloadrelocation.Status `json:"destination"`
	SourceOwnerRef      string                    `json:"sourceOwnerRef"`
	SourceNodeRef       string                    `json:"sourceNodeRef"`
	DestinationOwnerRef string                    `json:"destinationOwnerRef"`
	DestinationNodeRef  string                    `json:"destinationNodeRef"`
	ExpiresAt           time.Time                 `json:"expiresAt"`
	ArchiveSHA256       string                    `json:"archiveSha256,omitempty"`
}

// The authenticated managed caller approves this freshly Guard-attested foreign
// observation. This CLI verifies its own custody; it does not claim detached
// cryptographic verification of a foreign Owner key it does not possess.
func runManagedRelocationResume(c *cobra.Command, o *relocateOptions, r *relocatecoolify.Runner) error {
	raw, err := readRelocationPlan(c.InOrStdin(), o.resumePath)
	if err != nil {
		return err
	}
	var v managedRelocationResume
	if json.Unmarshal(raw, &v) != nil || v.APIVersion != "stackkit.relocation-resume/v1" || v.TransferID != o.transferID || v.ExpiresAt.Before(time.Now()) || v.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
		return errors.New("invalid or expired managed resume authority")
	}
	custody, err := localevidence.LoadOwnerCustody(getWorkDir())
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(v.PlanJSON)))
	d := v.Destination
	var effect struct {
		TransferID string `json:"transfer_id"`
		PlanSHA256 string `json:"plan_sha256"`
		State      string `json:"state"`
		NoEffects  bool   `json:"no_destination_effects"`
	}
	if v.SourceOwnerRef != custody.OwnerRef || v.SourceNodeRef != custody.Binding.NodeRef || d.Receipt.TransferID != v.TransferID || d.Receipt.PlanSHA256 != digest || d.Receipt.Phase != "aborted" || d.Receipt.Binding.NodeRef != v.DestinationNodeRef || d.Signature.OwnerRef != v.DestinationOwnerRef || d.Receipt.Signature.OwnerRef != v.DestinationOwnerRef || d.Signature.Value == "" || d.Receipt.Signature.Value == "" || time.Since(d.VerifiedAt) > 5*time.Minute || d.VerifiedAt.After(time.Now().Add(time.Minute)) || json.Unmarshal(d.Receipt.Data, &effect) != nil || effect.TransferID != v.TransferID || effect.PlanSHA256 != digest || effect.State != "aborted" || !effect.NoEffects {
		return errors.New("managed destination proof differs from this source, transfer or plan")
	}
	if prior, err := workloadrelocation.LoadReceipt(getWorkDir(), v.TransferID, "resumed", []byte(v.PlanJSON)); err == nil {
		observed, err := r.Observe(c.Context(), prior.Data, "resumed", getWorkDir())
		if err != nil {
			return err
		}
		status, err := workloadrelocation.SignStatus(getWorkDir(), prior, observed)
		if err != nil {
			return err
		}
		return writeCommandResult(c, c.CommandPath(), status)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if prepared, err := workloadrelocation.LoadReceipt(getWorkDir(), v.TransferID, "prepared", []byte(v.PlanJSON)); err == nil {
		var h struct {
			ArchiveSHA256 string `json:"archive_sha256"`
		}
		if json.Unmarshal(prepared.Data, &h) != nil || h.ArchiveSHA256 != v.ArchiveSHA256 {
			return errors.New("managed resume archive differs from source prepared bytes")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record := relocatecoolify.DrainRecordPath(getWorkDir(), v.TransferID)
	if err := r.Resume(c.Context(), []byte(v.PlanJSON), v.TransferID, record); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"transfer_id": v.TransferID, "plan_sha256": digest, "archive_sha256": v.ArchiveSHA256, "source_resumed": true, "plan_json": v.PlanJSON})
	observed, err := r.Observe(c.Context(), data, "resumed", getWorkDir())
	if err != nil {
		return err
	}
	receipt, err := workloadrelocation.StoreReceipt(getWorkDir(), v.TransferID, "resumed", []byte(v.PlanJSON), data)
	if err != nil {
		return err
	}
	status, err := workloadrelocation.SignStatus(getWorkDir(), receipt, observed)
	if err != nil {
		return err
	}
	return writeCommandResult(c, c.CommandPath(), status)
}
