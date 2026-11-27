package offlinerecovery

import (
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

func receiptRelativePath(operationID string) string {
	return path.Join(".stackkit", "offline-recovery", "operations", operationID+".json")
}

func loadReceipt(workspaceRoot, operationID string) (Receipt, bool, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return Receipt{}, false, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Receipt{}, false, err
	}
	defer func() { _ = transaction.Close() }()
	raw, _, err := transaction.ReadStableBounded(receiptRelativePath(operationID), MaxReceiptBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	var receipt Receipt
	if err := decodeCanonical(raw, MaxReceiptBytes, &receipt); err != nil {
		return Receipt{}, false, fmt.Errorf("decode operation receipt: %w", err)
	}
	if receipt.OperationID != operationID {
		return Receipt{}, false, errors.New("stored operation receipt identity differs from its journal path")
	}
	if err := receipt.Verify(); err != nil {
		return Receipt{}, false, err
	}
	return receipt, true, nil
}

func saveReceipt(workspaceRoot string, receipt Receipt, replace bool) error {
	if err := receipt.Verify(); err != nil {
		return err
	}
	raw, err := canonicalJSON(receipt)
	if err != nil {
		return err
	}
	if int64(len(raw)) > MaxReceiptBytes {
		return errors.New("operation receipt exceeds its bounded size")
	}
	relative := receiptRelativePath(receipt.OperationID)
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err := transaction.MkdirAll(path.Dir(relative), 0o700); err != nil {
		_ = transaction.Close()
		return err
	}
	if err := transaction.Close(); err != nil {
		return err
	}
	view, err := root.View(path.Dir(relative))
	if err != nil {
		return err
	}
	if replace {
		_, err = view.WriteAtomic0600(path.Base(relative), raw)
		return err
	}
	_, err = view.WriteAtomic0600NoReplace(path.Base(relative), raw)
	return err
}

func receiptMatchesBinding(stored Receipt, expected string) bool {
	digest, err := receiptBindingDigest(stored)
	return err == nil && digest == expected
}
