package backuplifecycle

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// ApplicationTransferExportReceipt is outside the encrypted archive so its
// source signature can bind the final ciphertext digest and durable hold.
type ApplicationTransferExportReceipt struct {
	APIVersion        string                                     `json:"apiVersion"`
	Export            EmergencyExportResult                      `json:"export"`
	SourceOperationID string                                     `json:"sourceOperationId"`
	State             string                                     `json:"state"`
	Signature         localevidence.ApplicationTransferSignature `json:"signature"`
}

func SignApplicationTransferExportReceipt(workspace string, result EmergencyExportResult, operation, mode string) (ApplicationTransferExportReceipt, error) {
	if result.Manifest.ApplicationTransfer == nil || result.Manifest.ApplicationTransfer.Mode != mode || result.ArchiveSHA256 == "" {
		return ApplicationTransferExportReceipt{}, errors.New("incomplete transfer export receipt")
	}
	state := "source-resumed-copy"
	if mode == "migrate" {
		if _, err := ReadApplicationTransferHold(workspace, operation, result.Manifest.ApplicationTransfer.ID); err != nil {
			return ApplicationTransferExportReceipt{}, err
		}
		state = "source-held"
	}
	receipt := ApplicationTransferExportReceipt{APIVersion: "stackkit.application-transfer-export-receipt/v1", Export: result, SourceOperationID: operation, State: state}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.Signature, err = localevidence.SignApplicationTransfer(workspace, canonical)
	return receipt, err
}

func VerifyApplicationTransferExportReceipt(receipt ApplicationTransferExportReceipt, sourceOwner, fingerprint, id, archiveDigest string) (ApplicationTransfer, error) {
	signature := receipt.Signature
	receipt.Signature = localevidence.ApplicationTransferSignature{}
	if receipt.APIVersion != "stackkit.application-transfer-export-receipt/v1" || strings.TrimPrefix(receipt.Export.ArchiveSHA256, "sha256:") != strings.TrimPrefix(archiveDigest, "sha256:") || !validOperationID(receipt.SourceOperationID) {
		return ApplicationTransfer{}, errors.New("transfer ciphertext/source operation differs from admitted export receipt")
	}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return ApplicationTransfer{}, err
	}
	if err = localevidence.VerifyApplicationTransfer(canonical, signature, sourceOwner, fingerprint); err != nil {
		return ApplicationTransfer{}, err
	}
	t, err := VerifyApplicationTransfer(receipt.Export.Manifest, sourceOwner, fingerprint, id)
	if err != nil {
		return t, err
	}
	if t.Mode == "migrate" && receipt.State != "source-held" || t.Mode == "copy" && receipt.State != "source-resumed-copy" {
		return t, errors.New("transfer source state differs from signed mode")
	}
	return t, nil
}

type ApplicationTransferOutcome struct {
	APIVersion     string                                     `json:"apiVersion"`
	TransferID     string                                     `json:"transferId"`
	Destination    ApplicationTransferDestination             `json:"destination"`
	State          string                                     `json:"state"`
	ActivationID   string                                     `json:"activationId,omitempty"`
	ReadbackDigest string                                     `json:"readbackDigest,omitempty"`
	CreatedAt      time.Time                                  `json:"createdAt"`
	Signature      localevidence.ApplicationTransferSignature `json:"signature"`
}

func SignApplicationTransferOutcome(workspace string, outcome ApplicationTransferOutcome) (ApplicationTransferOutcome, error) {
	outcome.APIVersion = "stackkit.application-transfer-outcome/v1"
	outcome.CreatedAt = time.Now().UTC()
	outcome.Signature = localevidence.ApplicationTransferSignature{}
	canonical, err := json.Marshal(outcome)
	if err != nil {
		return outcome, err
	}
	outcome.Signature, err = localevidence.SignApplicationTransfer(workspace, canonical)
	if err == nil && outcome.Signature.OwnerRef != outcome.Destination.OwnerRef {
		return outcome, errors.New("transfer outcome signer is not receiving Owner")
	}
	return outcome, err
}

func VerifyApplicationTransferOutcome(outcome ApplicationTransferOutcome, t ApplicationTransfer, fingerprint string) error {
	if fingerprint != t.Destination.SigningFingerprint {
		return errors.New("destination signing key differs from original transfer admission")
	}
	if outcome.APIVersion != "stackkit.application-transfer-outcome/v1" || outcome.TransferID != t.ID || outcome.Destination != t.Destination || outcome.CreatedAt.IsZero() || outcome.CreatedAt.After(time.Now().Add(time.Minute)) || (outcome.State != "activated" && outcome.State != "stopped") {
		return errors.New("destination outcome differs from exact application transfer")
	}
	if outcome.State == "activated" && (!validDigest(outcome.ReadbackDigest) || outcome.ActivationID == "") {
		return errors.New("destination success has no activation/data readback")
	}
	signature := outcome.Signature
	outcome.Signature = localevidence.ApplicationTransferSignature{}
	canonical, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	return localevidence.VerifyApplicationTransfer(canonical, signature, t.Destination.OwnerRef, fingerprint)
}
