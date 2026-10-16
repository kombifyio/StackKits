package localowner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// AbortCloudSessionRoutes restores only the source topology retained by the
// signed transition. It never changes the issuer, user, passkeys or secrets.
func (s *Service) AbortCloudSessionRoutes(ctx context.Context, specPath string, intent CloudSessionIntent, runtime CloudSessionRuntime) (localevidence.CloudSessionRouting, error) {
	state, err := localevidence.LoadCloudSessionRouting(s.workspaceRoot)
	if errors.Is(err, os.ErrNotExist) {
		if _, _, err := s.ready(ctx); err != nil {
			return state, err
		}
		return localevidence.CloudSessionRouting{Phase: "no-active-transition"}, nil
	}
	if err != nil {
		return state, err
	}
	if state.SpecPath != specPath {
		return state, errors.New("cloud_session_transition_pending: abort with the original StackSpec path")
	}
	candidate, err := intent.Candidate(state.BeforeSpec)
	if err != nil || !bytes.Equal(candidate, state.AfterSpec) {
		return state, errors.New("cloud_session_transition_pending: abort with the original allocated zone")
	}
	owner, client, err := s.ready(ctx)
	if err != nil {
		return state, err
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(s.workspaceRoot)
	if err != nil || s.verifyBoundOwner(ctx, client, owner, binding) != nil {
		return state, errors.New("cloud_session_owner_drift: established Owner is unavailable")
	}
	credentials, err := client.ListUserWebAuthnCredentials(ctx, binding.PocketIDSubject)
	if err != nil || localevidence.SessionRoutingValueDigest(credentialIDs(credentials)) != state.OwnerPasskeysDigest {
		return state, errors.New("cloud_session_passkey_drift: enrolled Owner credentials changed")
	}
	policy, err := client.GetOIDCClient(ctx, localevidence.TinyAuthPocketIDClientID)
	if err != nil || policy == nil {
		return state, errors.New("cloud_session_client_unavailable: original client cannot be read")
	}
	oldCallback, newCallback := state.Before.CallbackURL(), state.After.CallbackURL()
	if !slices.Equal(policy.CallbackURLs, []string{oldCallback}) && !slices.Equal(policy.CallbackURLs, []string{newCallback}) {
		return state, errors.New("cloud_session_client_drift: abort refuses an unrelated callback")
	}
	settings := policy.Settings()
	settings.CallbackURLs = []string{oldCallback}
	if localevidence.SessionRoutingValueDigest(settings) != state.ClientPolicyDigest {
		return state, errors.New("cloud_session_client_drift: abort refuses non-routing client drift")
	}
	if state.Phase != "aborted" {
		state.Phase = "aborting"
		if err := localevidence.SaveCloudSessionRouting(s.workspaceRoot, state); err != nil {
			return state, err
		}
	}
	if err := runtime.Restore(ctx, state); err != nil {
		return state, err
	}
	state, err = localevidence.LoadCloudSessionRouting(s.workspaceRoot)
	if err != nil {
		return state, err
	}
	if slices.Equal(policy.CallbackURLs, []string{newCallback}) {
		updater, ok := client.(oidcClientUpdater)
		if !ok {
			return state, errors.New("cloud_session_client_unavailable: original client cannot be updated")
		}
		if _, err := updater.UpdateOIDCClient(ctx, policy.ID, settings); err != nil {
			return state, errors.New("cloud_session_callback_pending: abort callback update uncertain; resume the same abort")
		}
	}
	readback, err := client.GetOIDCClient(ctx, policy.ID)
	if err != nil || readback == nil || localevidence.SessionRoutingValueDigest(readback.Settings()) != state.ClientPolicyDigest {
		return state, errors.New("cloud_session_client_drift: original callback and policy readback failed")
	}
	groups, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return state, err
	}
	tinyAuth, err := localevidence.LoadBasementTinyAuthPocketIDBinding(s.workspaceRoot)
	if err != nil {
		return state, err
	}
	if err := tinyAuthBindingDiagnostic(readback, nil, tinyAuth, owner, oldCallback, groups, true); err != nil {
		return state, err
	}
	if err := intent.Restore(s.workspaceRoot, specPath, state.BeforeSpec, state.AfterSpec); err != nil {
		return state, err
	}
	state.Phase = "aborted"
	if err := localevidence.SaveCloudSessionRouting(s.workspaceRoot, state); err != nil {
		return state, err
	}
	state, err = localevidence.LoadCloudSessionRouting(s.workspaceRoot)
	if err != nil {
		return state, err
	}
	if err := localevidence.ArchiveAbortedCloudSessionRouting(s.workspaceRoot); err != nil {
		return state, err
	}
	return state, nil
}
