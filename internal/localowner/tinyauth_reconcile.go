package localowner

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// TinyAuthReconciliation contains public policy results, never identity
// credentials or enrollment links.
type TinyAuthReconciliation struct {
	Status          string `json:"status"`
	Changed         bool   `json:"changed"`
	PKCEEnabled     bool   `json:"pkceEnabled"`
	BindingVerified bool   `json:"bindingVerified"`
}

// ReconcileTinyAuthPKCE repairs only legacy PKCE drift against the existing
// signed Owner/client binding. The caller holds the native mutation lock.
// Unlike Realize it cannot recreate identities, seed groups or issue links.
func (s *Service) ReconcileTinyAuthPKCE(ctx context.Context, approved bool) (TinyAuthReconciliation, error) {
	if !approved {
		return TinyAuthReconciliation{}, actionableerror.WithDiagnostic(
			"pocketid_tinyauth_reconcile_owner_approval_required", errors.New("owner approval required"))
	}
	before, err := s.verifyOwnerRuntime(ctx, false)
	if err != nil {
		return TinyAuthReconciliation{}, err
	}
	_, client, err := s.ready(ctx)
	if err != nil {
		return TinyAuthReconciliation{}, err
	}
	binding, err := localevidence.LoadBasementTinyAuthPocketIDBinding(s.workspaceRoot)
	if err != nil {
		return TinyAuthReconciliation{}, err
	}
	current, err := client.GetOIDCClient(ctx, binding.ClientID)
	if err != nil || current == nil {
		return TinyAuthReconciliation{}, actionableerror.WithDiagnostic(
			"pocketid_tinyauth_client_unreadable", errors.New("client readback unavailable"))
	}
	changed := !current.PkceEnabled
	groups, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return TinyAuthReconciliation{}, err
	}
	if err := s.convergeTinyAuthPKCE(ctx, client, groups); err != nil {
		return TinyAuthReconciliation{}, err
	}
	after, err := s.Verify(ctx)
	if err != nil {
		return TinyAuthReconciliation{}, err
	}
	if after.Signature != before.Signature {
		return TinyAuthReconciliation{}, actionableerror.WithDiagnostic(
			"pocketid_tinyauth_binding_mismatch", errors.New("owner binding changed during reconciliation"))
	}
	return TinyAuthReconciliation{Status: "verified", Changed: changed, PKCEEnabled: true, BindingVerified: true}, nil
}
