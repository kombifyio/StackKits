package decisionreceipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Record is what the spent record stores about one receipt. It holds the
// receipt identity and the operation it was spent on, never the receipt
// itself: the receipt is a bearer credential and does not belong in a file.
type Record struct {
	ReceiptID  string    `json:"receiptId"`
	DecisionID string    `json:"decisionId"`
	Operation  string    `json:"operation"`
	Resource   string    `json:"resource"`
	SpentAt    time.Time `json:"spentAt"`
}

// spend records one receipt as used on this node.
//
// The record lives at one node-level path (custody.go) that no caller can
// select: not under a workspace, not through a flag, not through the
// environment. That directory and every parent belong to root and only root
// can write them, so a process that cannot write there refuses the call, and
// no other account can delete a record to replay a receipt inside its window.
//
// Each receipt is written by exclusive create under two names, one per
// receipt id and one per decision id. A second presentation of the same
// receipt, or of another receipt for the same decision, from any workspace,
// finds the name already taken and is refused with stackkit_receipt_spent.
// The record survives a restart of the command, of the MCP server and of the
// node: a receipt spent before a crash stays spent, and an interrupted
// operation is resumed through the operation's own journal rather than by
// presenting the receipt again.
func (c custody) spend(record Record) error {
	if uid, ok := effectiveUser(); !ok || uid != c.owner {
		return errors.New("only the node's privileged account can record a spent receipt")
	}
	if err := c.ensureJournal(); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	for _, name := range []string{journalName("receipt", record.ReceiptID), journalName("decision", record.DecisionID)} {
		file, err := createExclusive(filepath.Join(c.journalDir, name))
		if errors.Is(err, os.ErrExist) {
			return refuse(RefusalSpent, "the receipt or its decision was already used on this node")
		}
		if err != nil {
			return fmt.Errorf("record the spent receipt: %w", err)
		}
		_, writeErr := file.Write(body)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return fmt.Errorf("record the spent receipt: %w", err)
		}
	}
	directory, err := os.Open(c.journalDir)
	if err != nil {
		return fmt.Errorf("record the spent receipt: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("record the spent receipt: %w", err)
	}
	return nil
}

// ensureJournal creates the spent-record directory with mode 0700 when it is
// missing and then requires it, and every parent, to belong to the custody
// owner and to be writable by nobody else.
func (c custody) ensureJournal() error {
	parent := filepath.Dir(c.journalDir)
	if _, err := os.Lstat(parent); errors.Is(err, os.ErrNotExist) {
		if err := c.requireOwnedChain(filepath.Dir(parent)); err != nil {
			return err
		}
		if err := os.Mkdir(parent, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if err := c.requireOwnedChain(parent); err != nil {
		return err
	}
	if err := os.Mkdir(c.journalDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := c.requireOwnedChain(c.journalDir); err != nil {
		return err
	}
	// The record directory itself is exactly 0700 on every use, not only when
	// this process created it: a directory another process left readable or
	// searchable by group or other is not this node's private spent record.
	info, err := os.Lstat(c.journalDir)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s is not a private directory of mode 0700", c.journalDir)
	}
	return nil
}

// journalName is the file name of one spent key. The name is a digest of a
// namespace and the id, so every id maps to one short, file-system-safe name
// and a receipt id never takes a decision id's name.
func journalName(namespace, id string) string {
	sum := sha256.Sum256([]byte(namespace + "\n" + id))
	return namespace + "-" + hex.EncodeToString(sum[:]) + ".json"
}
