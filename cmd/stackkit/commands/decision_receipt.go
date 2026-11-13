package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kombifyio/stackkits/internal/crypto/decisionreceipt"
)

// The decision receipt carrier. A receipt is the person's confirmation the
// platform's decision ledger signs (docs/DECISION-RECEIPTS.md); it is
// verified and spent here, in the command that performs the effect, against
// node custody only.
//
// The acting principal travels next to the receipt because the signed intent
// binds it and the receipt itself does not repeat it: only the entry point
// that authenticated the caller knows who acts.
var (
	decisionReceiptValue   string
	decisionPrincipalValue string
)

// maxDecisionReceiptFileBytes bounds the file a receipt is read from.
const maxDecisionReceiptFileBytes = 1 << 20

// sha256DigestPattern is the lowercase sha256:<hex> form of every plan digest
// a first-slice command takes.
var sha256DigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// addDecisionReceiptFlags registers the receipt carrier on one command.
func addDecisionReceiptFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&decisionReceiptValue, "decision-receipt", "",
		"Compact decision receipt (JWS) that carries the person's confirmation of this exact operation, or @file to read it from a file")
	cmd.Flags().StringVar(&decisionPrincipalValue, "decision-principal", "",
		"Acting principal the receipt is bound to, as <person|agent_instance>:<id>")
}

// decisionReceiptPresented reports whether the caller presented a receipt or
// a principal. Either one is a receipt call: it is verified or refused, never
// ignored.
func decisionReceiptPresented() bool {
	return strings.TrimSpace(decisionReceiptValue) != "" || strings.TrimSpace(decisionPrincipalValue) != ""
}

// refuseDecisionReceipt refuses a presented receipt on a call outside the
// first slice. A call without one is left exactly as it is.
func refuseDecisionReceipt(reason string) error {
	if !decisionReceiptPresented() {
		return nil
	}
	return fmt.Errorf("%s: %s", decisionreceipt.RefusalInvalid, reason)
}

// admitDecisionReceipt verifies the receipt a caller presented for one
// first-slice call and spends it on this node before the call's first effect.
//
// It returns false without reading anything when no receipt was presented, so
// a person at a local shell keeps --owner-approve and --auto-approve exactly
// as before: no prompt, no network call and no account. A presented receipt is
// verified or the call is refused; it is never ignored.
func admitDecisionReceipt(resolve func() (decisionreceipt.Request, error)) (bool, error) {
	if !decisionReceiptPresented() {
		return false, nil
	}
	token, err := decisionReceiptToken(decisionReceiptValue)
	if err != nil {
		return false, err
	}
	if token == "" {
		return false, errors.New(decisionreceipt.RefusalInvalid + ": --decision-principal requires --decision-receipt")
	}
	principal, err := decisionPrincipal(decisionPrincipalValue)
	if err != nil {
		return false, err
	}
	request, err := resolve()
	if err != nil {
		return false, fmt.Errorf("%s: %w", decisionreceipt.RefusalInvalid, err)
	}
	request.Principal = principal
	if _, err := decisionreceipt.Admit(token, request, time.Now()); err != nil {
		return false, err
	}
	return true, nil
}

// decisionReceiptToken reads the receipt a caller presented: the compact JWS
// itself, or the file it names.
func decisionReceiptToken(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, "@") {
		return trimmed, nil
	}
	path := strings.TrimSpace(strings.TrimPrefix(trimmed, "@"))
	if path == "" {
		return "", fmt.Errorf("%s: the receipt file name is empty", decisionreceipt.RefusalInvalid)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDecisionReceiptFileBytes {
		return "", fmt.Errorf("%s: the receipt file is not a plain readable file", decisionreceipt.RefusalInvalid)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: the receipt file is not a plain readable file", decisionreceipt.RefusalInvalid)
	}
	return strings.TrimSpace(string(raw)), nil
}

// decisionPrincipal reads the acting principal the receipt is bound to.
func decisionPrincipal(value string) (decisionreceipt.Principal, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decisionreceipt.Principal{}, errors.New(decisionreceipt.RefusalInvalid +
			": --decision-principal is required with --decision-receipt; it names the acting principal the caller's entry point authenticated")
	}
	kind, id, found := strings.Cut(trimmed, ":")
	id = strings.TrimSpace(id)
	if !found || (kind != decisionreceipt.PrincipalPerson && kind != decisionreceipt.PrincipalAgentInstance) || id == "" {
		return decisionreceipt.Principal{}, errors.New(decisionreceipt.RefusalInvalid +
			": --decision-principal must be <person|agent_instance>:<id>")
	}
	return decisionreceipt.Principal{Kind: kind, ID: id}, nil
}

// decisionReceiptRevision is the revision of the workspace the operation was
// resolved against: sha256:<hex> of the bytes of the workspace's own native
// spec. It is never empty, and it changes whenever the spec changes under the
// operation.
func decisionReceiptRevision(workspace string) (string, error) {
	path := filepath.Join(workspace, specFile)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("the workspace revision could not be read: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDecisionReceiptFileBytes {
		return "", errors.New("the workspace revision could not be read")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("the workspace revision could not be read: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// decisionPlanDigest turns one sha256:<hex> plan digest into the 64-hex form
// the ledger binds as planSha256. A first-slice call that has a plan cannot
// present a receipt without naming the plan it was reviewed against.
func decisionPlanDigest(flag, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !sha256DigestPattern.MatchString(value) {
		return "", fmt.Errorf("a decision receipt for this operation requires %s sha256:<64 lowercase hex>", flag)
	}
	return strings.TrimPrefix(value, "sha256:"), nil
}
