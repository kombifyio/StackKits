package workloadrelocation

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

type Receipt struct {
	APIVersion string                                 `json:"apiVersion"`
	TransferID string                                 `json:"transferId"`
	Phase      string                                 `json:"phase"`
	PlanSHA256 string                                 `json:"planSha256"`
	Binding    localevidence.LocalBinding             `json:"binding"`
	RecordedAt time.Time                              `json:"recordedAt"`
	Data       json.RawMessage                        `json:"data"`
	Signature  localevidence.OwnerRelocationSignature `json:"signature"`
}

type Status struct {
	APIVersion string                                 `json:"apiVersion"`
	Receipt    Receipt                                `json:"receipt"`
	VerifiedAt time.Time                              `json:"verifiedAt"`
	Signature  localevidence.OwnerRelocationSignature `json:"signature"`
}

func SignStatus(workspace string, receipt Receipt, observed time.Time) (Status, error) {
	status := Status{APIVersion: "stackkit.relocation-status/v1", Receipt: receipt, VerifiedAt: observed}
	raw, err := json.Marshal(status)
	if err != nil {
		return status, err
	}
	status.Signature, err = localevidence.SignOwnerRelocation(workspace, raw)
	return status, err
}

func receiptPath(workspace, transfer, phase string) (string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(transfer) || (phase != "sent" && phase != "received" && phase != "prepared" && phase != "aborted" && phase != "resumed") {
		return "", errors.New("invalid relocation receipt identity")
	}
	return filepath.Join(workspace, ".stackkit", "relocation", "receipts", transfer+"-"+phase+".json"), nil
}

func StoreReceipt(workspace, transfer, phase string, plan, data []byte) (Receipt, error) {
	var receipt Receipt
	var payload struct {
		TransferID    string `json:"transfer_id"`
		PlanSHA256    string `json:"plan_sha256"`
		ArchiveSHA256 string `json:"archive_sha256"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.TransferID != transfer || payload.PlanSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(plan)) || ((phase != "aborted" && phase != "resumed") && !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(payload.ArchiveSHA256)) {
		return receipt, errors.New("node result differs from transfer, checked plan or archive")
	}
	path, err := receiptPath(workspace, transfer, phase)
	if err != nil {
		return receipt, err
	}
	if prior, err := LoadReceipt(workspace, transfer, phase, plan); err == nil {
		return prior, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return receipt, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return receipt, err
	}
	receipt = Receipt{APIVersion: "stackkit.relocation-receipt/v1", TransferID: transfer, Phase: phase, PlanSHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(plan)), Binding: owner.Binding, RecordedAt: time.Now().UTC(), Data: append(json.RawMessage(nil), data...)}
	unsigned, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.Signature, err = localevidence.SignOwnerRelocation(workspace, unsigned)
	if err != nil {
		return receipt, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if err := ensureReceiptDirectory(filepath.Dir(path)); err != nil {
		return receipt, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return receipt, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Link(f.Name(), path)
	}
	if err == nil {
		d, openErr := os.Open(filepath.Dir(path))
		if openErr != nil {
			return receipt, openErr
		}
		err = d.Sync()
		closeErr := d.Close()
		if err == nil {
			err = closeErr
		}
	}
	return receipt, err
}

func LoadReceipt(workspace, transfer, phase string, plan []byte) (Receipt, error) {
	var receipt Receipt
	path, err := receiptPath(workspace, transfer, phase)
	if err != nil {
		return receipt, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return receipt, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return receipt, errors.New("relocation receipt must be a bounded regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return receipt, err
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.APIVersion != "stackkit.relocation-receipt/v1" || receipt.TransferID != transfer || receipt.Phase != phase || receipt.PlanSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(plan)) {
		return receipt, errors.New("relocation receipt differs from exact operation and plan")
	}
	signature := receipt.Signature
	receipt.Signature = localevidence.OwnerRelocationSignature{}
	unsigned, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if err = localevidence.VerifyOwnerRelocation(workspace, unsigned, signature); err != nil {
		return receipt, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return receipt, err
	}
	if receipt.Binding != owner.Binding {
		return receipt, errors.New("relocation receipt belongs to another local node binding")
	}
	receipt.Signature = signature
	return receipt, nil
}

// ensureReceiptDirectory also persists newly created directory entries in
// their parents before a journal can authorize a runtime effect.
func ensureReceiptDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	current, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		dir, err := os.Open(current)
		if err != nil {
			return err
		}
		err = dir.Sync()
		closeErr := dir.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
