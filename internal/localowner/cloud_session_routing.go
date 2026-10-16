package localowner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"

	"github.com/kombifyio/stackkits/internal/addressplan"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

type CloudSessionRuntime interface {
	Check(context.Context, addressplan.SessionTopology, addressplan.SessionTopology) error
	Capture(context.Context, addressplan.SessionTopology, addressplan.SessionTopology) (localevidence.CloudSessionCapture, error)
	Activate(context.Context, localevidence.CloudSessionRouting) (localevidence.CloudSessionRouting, error)
	Verify(context.Context, localevidence.CloudSessionRouting) error
	Restore(context.Context, localevidence.CloudSessionRouting) error
}

func (s *Service) CheckCloudSessionRoutes(ctx context.Context, specPath string, intent CloudSessionIntent, runtime CloudSessionRuntime) (addressplan.SessionTopology, error) {
	if _, err := s.Verify(ctx); err != nil {
		return addressplan.SessionTopology{}, err
	}
	state, stateErr := localevidence.LoadCloudSessionRouting(s.workspaceRoot)
	if stateErr == nil {
		candidate, err := intent.Candidate(state.BeforeSpec)
		if state.Phase != "active" || state.SpecPath != specPath || err != nil || !bytes.Equal(candidate, state.AfterSpec) {
			return addressplan.SessionTopology{}, errors.New("cloud_session_transition_pending: inspect or resume the original transition, retaining --abort during abort recovery")
		}
		return state.After, runtime.Verify(ctx, state)
	}
	if !errors.Is(stateErr, os.ErrNotExist) {
		return addressplan.SessionTopology{}, stateErr
	}
	raw, err := localevidence.ReadCloudSessionFile(s.workspaceRoot, specPath)
	if err != nil {
		return addressplan.SessionTopology{}, err
	}
	candidate, err := intent.Candidate(raw)
	if err != nil {
		return addressplan.SessionTopology{}, err
	}
	before, err := addressplan.SessionTopologyFromCanonical(raw)
	if err != nil {
		return before, err
	}
	after, err := addressplan.SessionTopologyFromCanonical(candidate)
	if err != nil {
		return after, err
	}
	return after, runtime.Check(ctx, before, after)
}

type CloudSessionIntent interface {
	Candidate([]byte) ([]byte, error)
	Persist(string, string, []byte, []byte) error
	Restore(string, string, []byte, []byte) error
}

// TransitionCloudSessionRoutes runs beneath the existing lifecycle mutation
// lock. Each uncertain mutation is observed on resume before it is repeated.
func (s *Service) TransitionCloudSessionRoutes(ctx context.Context, specPath string, intent CloudSessionIntent, runtime CloudSessionRuntime) (localevidence.CloudSessionRouting, error) {
	state, err := localevidence.LoadCloudSessionRouting(s.workspaceRoot)
	if errors.Is(err, os.ErrNotExist) {
		binding, err := s.Verify(ctx)
		if err != nil {
			return state, err
		}
		raw, err := localevidence.ReadCloudSessionFile(s.workspaceRoot, specPath)
		if err != nil {
			return state, err
		}
		after, err := intent.Candidate(raw)
		if err != nil {
			return state, err
		}
		beforeTopology, err := addressplan.SessionTopologyFromCanonical(raw)
		if err != nil {
			return state, err
		}
		afterTopology, err := addressplan.SessionTopologyFromCanonical(after)
		if err != nil || afterTopology.ValidateTransitionFrom(beforeTopology) != nil {
			return state, errors.New("cloud_session_transition_invalid: candidate changes the enrolled issuer")
		}
		cloud, err := localevidence.LoadCloudRuntimeCustody(s.workspaceRoot)
		if err != nil {
			return state, err
		}
		tinyAuth, err := localevidence.LoadBasementTinyAuthPocketIDBinding(s.workspaceRoot)
		if err != nil {
			return state, err
		}
		_, client, err := s.ready(ctx)
		if err != nil {
			return state, err
		}
		policy, err := client.GetOIDCClient(ctx, tinyAuth.ClientID)
		if err != nil || policy == nil {
			return state, errors.New("cloud_session_client_unavailable: existing TinyAuth client cannot be read")
		}
		credentials, err := client.ListUserWebAuthnCredentials(ctx, binding.PocketIDSubject)
		if err != nil || len(credentialIDs(credentials)) == 0 {
			return state, errors.New("cloud_session_passkey_required: existing Owner passkey must be retained")
		}
		capture, err := runtime.Capture(ctx, beforeTopology, afterTopology)
		if err != nil {
			return state, err
		}
		state = localevidence.CloudSessionRouting{
			Version: "stackkit.cloud-session-routing/v1",
			Phase:   "prepared", SpecPath: specPath, BeforeSpec: raw, AfterSpec: after, Before: beforeTopology, After: afterTopology,
			CloudDigest: localevidence.SessionRoutingValueDigest(cloud), BindingDigest: localevidence.SessionRoutingValueDigest(tinyAuth), OwnerDigest: localevidence.OwnerRuntimeBindingDigest(binding),
			ClientPolicyDigest: localevidence.SessionRoutingValueDigest(policy.Settings()), OwnerPasskeysDigest: localevidence.SessionRoutingValueDigest(credentialIDs(credentials)),
			SourceDigest: capture.SourceDigest, OverlayDigest: capture.OverlayDigest, Containers: capture.Containers, Migration: capture.Migration,
		}
		if err := localevidence.SaveCloudSessionRouting(s.workspaceRoot, state); err != nil {
			return state, err
		}
		state, err = localevidence.LoadCloudSessionRouting(s.workspaceRoot)
		if err != nil {
			return state, err
		}
	} else if err != nil {
		return state, err
	}
	if state.Phase == "aborting" || state.Phase == "aborted" {
		return state, errors.New("cloud_session_transition_pending: this transition is aborting or aborted; repeat the original --abort to finish verified restoration and receipt archival")
	}
	if state.SpecPath != specPath {
		return state, errors.New("cloud_session_transition_pending: resume with the original StackSpec path")
	}
	candidate, err := intent.Candidate(state.BeforeSpec)
	if err != nil || !bytes.Equal(candidate, state.AfterSpec) {
		return state, errors.New("cloud_session_transition_pending: resume with the original allocated zone")
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
	tinyAuth, err := localevidence.LoadBasementTinyAuthPocketIDBinding(s.workspaceRoot)
	if err != nil {
		return state, err
	}
	groups, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return state, err
	}
	policy, err := client.GetOIDCClient(ctx, tinyAuth.ClientID)
	if err != nil || policy == nil {
		return state, errors.New("cloud_session_client_unavailable: existing client cannot be read")
	}
	settings := policy.Settings()
	oldCallback, newCallback := state.Before.CallbackURL(), state.After.CallbackURL()
	if !slices.Equal(settings.CallbackURLs, []string{oldCallback}) && !slices.Equal(settings.CallbackURLs, []string{newCallback}) {
		return state, errors.New("cloud_session_client_drift: callback is neither the original nor intended value")
	}
	settings.CallbackURLs = []string{oldCallback}
	if localevidence.SessionRoutingValueDigest(settings) != state.ClientPolicyDigest {
		return state, errors.New("cloud_session_client_drift: non-routing client policy changed")
	}
	if err := tinyAuthBindingDiagnostic(policy, nil, tinyAuth, owner, policy.CallbackURLs[0], groups, true); err != nil {
		return state, err
	}
	if slices.Equal(policy.CallbackURLs, []string{oldCallback}) {
		if state.Phase != "prepared" {
			return state, errors.New("cloud_session_client_drift: previously bound callback reverted")
		}
		updater, ok := client.(oidcClientUpdater)
		if !ok {
			return state, errors.New("cloud_session_client_unavailable: existing client update is unavailable")
		}
		settings.CallbackURLs = []string{newCallback}
		if _, err := updater.UpdateOIDCClient(ctx, policy.ID, settings); err != nil {
			return state, errors.New("cloud_session_callback_pending: callback update was uncertain; resume to read back before retry")
		}
		policy, err = client.GetOIDCClient(ctx, policy.ID)
		if err != nil || policy == nil {
			return state, errors.New("cloud_session_callback_pending: callback readback unavailable; resume the transition")
		}
	}
	settings = policy.Settings()
	settings.CallbackURLs = []string{oldCallback}
	if !slices.Equal(policy.CallbackURLs, []string{newCallback}) || localevidence.SessionRoutingValueDigest(settings) != state.ClientPolicyDigest || tinyAuthBindingDiagnostic(policy, nil, tinyAuth, owner, newCallback, groups, true) != nil {
		return state, errors.New("cloud_session_client_drift: callback-only update did not preserve policy")
	}
	if state.Phase == "prepared" {
		state.Phase = "callback-bound"
		if err := localevidence.SaveCloudSessionRouting(s.workspaceRoot, state); err != nil {
			return state, err
		}
	}
	if state.Phase == "callback-bound" {
		state, err = runtime.Activate(ctx, state)
		if err != nil {
			return state, err
		}
		state.Phase = "runtime-bound"
		if err := localevidence.SaveCloudSessionRouting(s.workspaceRoot, state); err != nil {
			return state, err
		}
	}
	if err := runtime.Verify(ctx, state); err != nil {
		return state, err
	}
	if err := intent.Persist(s.workspaceRoot, specPath, state.BeforeSpec, state.AfterSpec); err != nil {
		return state, err
	}
	state.Phase = "active"
	if err := localevidence.SaveCloudSessionRouting(s.workspaceRoot, state); err != nil {
		return state, err
	}
	return localevidence.LoadCloudSessionRouting(s.workspaceRoot)
}
