package stackkitmcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/crypto/decisionreceipt"
	"github.com/kombifyio/stackkits/internal/standaloneoperations"
)

// A signed decision receipt (docs/DECISION-RECEIPTS.md) is never approval at
// this server. The server's own approval rules (the write switch, the exact
// confirmation, the local Owner approval, an agent's per-call step-up) apply
// to every call exactly as before, receipted or not. The receipt is an
// additional requirement of the first-slice calls, and it is passed on only
// to a command that verifies and spends it before its first effect:
//
//   - stackkit_remove with delete_data (application_remove),
//   - stackkit_backup_restore_activate (backup_restore),
//   - stackkit_advanced_change_set_apply (advanced_change_set_apply),
//   - stackkit_own_application with action remove and delete_data, not a dry
//     run (application_remove).
//
// On every other tool, action or flag combination a presented receipt is
// refused, not ignored.

// receiptShape is the compact JWS form: three base64url segments. Anything
// else, including an @file reference the command line would read, is refused
// before it reaches a command.
var receiptShape = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

const maxReceiptBytes = 16384

// catalogReceiptCall reports whether one call of a catalog-projected
// operation is a first-slice call whose command verifies a receipt.
func catalogReceiptCall(operation standaloneoperations.Contract, in map[string]any) bool {
	switch operation.ID {
	case "stackkit.backup.restore.activate", "stackkit.advanced.change-set.apply":
		return true
	case "stackkit.app.own":
		deleteData, _ := in["delete_data"].(bool)
		dryRun, _ := in["dry_run"].(bool)
		return strings.TrimSpace(stringInput(in, "action")) == "remove" && deleteData && !dryRun
	default:
		return false
	}
}

// decisionReceiptArgs admits the receipt of one call: required on a
// first-slice call, refused on every other call, checked for its compact
// form, and passed on with the acting principal this server authenticated.
// The receipt flag itself is returned for the typed tools; a catalog tool
// already renders its decision_receipt argument.
func (a *App) decisionReceiptArgs(ctx context.Context, tool, operation, baseDir, receipt string, firstSlice bool) ([]string, map[string]any) {
	receipt = strings.TrimSpace(receipt)
	if receipt == "" {
		if firstSlice {
			return nil, decisionReceiptRequiredOutput(tool, operation)
		}
		return nil, nil
	}
	if !firstSlice {
		return nil, decisionReceiptRefusedOutput(tool, "a decision receipt covers only the first-slice calls of this tool (a removal with its data, a restore activation, a change-set apply), never a dry run; this call keeps its own approval")
	}
	if len(receipt) > maxReceiptBytes || !receiptShape.MatchString(receipt) {
		return nil, decisionReceiptRefusedOutput(tool, "decision_receipt must be the compact JWS the decision ledger signed")
	}
	workspace, err := a.workspaceDir(baseDir)
	if err != nil {
		return nil, errorOutput(tool, err)
	}
	principal, err := a.decisionPrincipal(ctx, workspace)
	if err != nil {
		return nil, errorOutput(tool, err)
	}
	return []string{"--decision-receipt=" + receipt, "--decision-principal=" + principal}, nil
}

// decisionPrincipal is the acting principal a receipt is bound to, spelled
// exactly as the platform's filer files it (docs/DECISION-RECEIPTS.md,
// "Acting principal"). Every branch derives from that one definition:
//
//   - an envelope agent is agent_instance:gateway:<link ref>:<agent
//     instance>/<account>, the agent id the Gateway envelope names;
//   - a directly attached agent credential, or the agent credential this
//     server is started with, is a node-local credential the platform has
//     never seen: no filer can file a decision for it, so its call is refused
//     instead of being bound to an id no receipt carries;
//   - with no agent, the caller is the local Owner, and the principal is
//     person:<the Owner's platform subject>, read from the node's enrolled
//     identity (root-owned custody), never the Owner's email.
//
// The command recomputes the intent digest with exactly this principal and
// refuses a person principal that is not the node's enrolled Owner.
func (a *App) decisionPrincipal(ctx context.Context, _ string) (string, error) {
	if envelope, ok := EnvelopeAgentFromContext(ctx); ok {
		principal, err := decisionreceipt.EnvelopeAgentPrincipal(
			strings.TrimSpace(envelope.LinkRef), strings.TrimSpace(envelope.AgentInstance), strings.TrimSpace(envelope.Account))
		if err != nil {
			return "", err
		}
		return principal.Kind + ":" + principal.ID, nil
	}
	if _, ok := AgentFromContext(ctx); ok {
		return "", errors.New("a node-local agent credential has no platform agent identity, so no decision receipt can be filed for it; call through the kombify Gateway agent link or as the Owner")
	}
	if a.opts.AgentToken != "" {
		return "", errors.New("this server acts with a node-local agent credential, which has no platform agent identity, so no decision receipt can be filed for it")
	}
	principal, err := decisionreceipt.EnrolledOwnerPrincipal()
	if err != nil {
		return "", fmt.Errorf("the acting Owner of the decision receipt is unknown on this node: %w", err)
	}
	return principal.Kind + ":" + principal.ID, nil
}

// decisionReceiptRequiredOutput refuses a first-slice call that carries no
// signed decision receipt.
//
// The reason code stays the existing owner approval reason, because a
// person's confirmation is what is missing; the guidance names the receipt
// that carries it.
func decisionReceiptRequiredOutput(tool, operation string) map[string]any {
	message := fmt.Sprintf("a signed decision receipt is required for %q", operation)
	out := errorOutput(tool, fmt.Errorf("%s; ask the Owner for the decision receipt of this exact operation and pass it as decision_receipt", message))
	out["error_details"] = actionableErrorMap(actionableerror.New(
		"stackkit_mcp_request_failed", "owner_approval_required", message,
		[]string{
			"Ask the Owner for the decision receipt of this exact operation and pass it as decision_receipt.",
			"A receipt is used once and names this operation, its target on this node, its arguments, plan and revision, and the acting principal. The call's own approval still applies.",
		}, false,
	))
	out["approval_required"] = true
	out["decision_receipt_required"] = true
	return out
}

// decisionReceiptRefusedOutput refuses a receipt the server does not pass on.
func decisionReceiptRefusedOutput(tool, message string) map[string]any {
	out := errorOutput(tool, fmt.Errorf("%s: %s", decisionreceipt.RefusalInvalid, message))
	out["error_details"] = actionableErrorMap(actionableerror.New(
		"stackkit_mcp_request_failed", decisionreceipt.RefusalInvalid, message,
		[]string{"Call again without decision_receipt, or with the receipt of exactly this first-slice operation."}, false,
	))
	return out
}
