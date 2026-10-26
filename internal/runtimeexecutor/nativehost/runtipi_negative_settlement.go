package nativehost

import (
	"context"
	"errors"
	"reflect"
	"regexp"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// RecoverUndispatched uses the original signed journal and held OS lease as
// negative admission proof. Prepare only creates a dormant exec; Dispatch is
// prohibited until a different, signed dispatched CAS. A late Prepare reply
// cannot win that CAS after this settlement. No HTTP absence inference occurs.
func (d *runtipiBackupControl) RecoverUndispatched(ctx context.Context, g backuplifecycle.AppGroupControl, e backuplifecycle.AppGroupEffect) (backuplifecycle.AppGroupNegative, backuplifecycle.AppGroupGeneration, error) {
	var n backuplifecycle.AppGroupNegative
	var observed backuplifecycle.AppGroupGeneration
	if e.State != backuplifecycle.ControlPreparing && e.State != backuplifecycle.ControlPrepared {
		return n, observed, backuplifecycle.ErrControlUnresolved
	}
	if err := d.custody.VerifyJournal(ctx, g); err != nil {
		return n, observed, err
	}
	if len(g.Effects) == 0 || !reflect.DeepEqual(g.Effects[len(g.Effects)-1], e) {
		return n, observed, errors.New("undispatched settlement differs from signed last intent")
	}
	if e.Handle != "" {
		if _, err := d.handle(e.Handle, e.Action); err != nil {
			return n, observed, err
		}
	}
	prefix := g
	prefix.Effects = g.Effects[:len(g.Effects)-1]
	observed, err := d.custody.Observe(ctx, prefix, "verify")
	if err != nil {
		return n, observed, err
	}
	if !reflect.DeepEqual(observed, prefix.Current()) {
		return n, observed, errors.New("pre-dispatch exact graph changed")
	}
	previous, err := d.custody.PredecessorDigest(ctx, g)
	if err != nil {
		return n, observed, err
	}
	n = backuplifecycle.AppGroupNegative{Kind: "undispatched", DispatchPhase: e.State, Phase: "pre-dispatch", Reason: "JOURNALED_DISPATCH_NOT_ADMITTED", NoNativeActuation: true, WitnessDigest: backuplifecycle.AppGroupDigest(g), PreviousCustodyDigest: previous}
	return n, observed, nil
}

func (d *runtipiBackupControl) ReadbackNegative(ctx context.Context, g backuplifecycle.AppGroupControl, raw, action string) (backuplifecycle.AppGroupNegative, backuplifecycle.AppGroupGeneration, error) {
	var n backuplifecycle.AppGroupNegative
	var observed backuplifecycle.AppGroupGeneration
	if err := d.validate(ctx); err != nil {
		return n, observed, err
	}
	if err := d.custody.VerifyJournal(ctx, g); err != nil {
		return n, observed, err
	}
	h, err := d.handle(raw, action)
	if err != nil {
		return n, observed, err
	}
	code, ended, err := d.client.InputExit(ctx, h.ExecID, h.ContainerID, h.Command)
	if err != nil || !ended || code != 73 && code != 74 {
		return n, observed, errors.Join(backuplifecycle.ErrControlUnresolved, err)
	}
	result, err := d.recoverOriginalResult(ctx, h)
	if err != nil {
		return n, observed, err
	}
	_, err = platformdeploy.ValidateRuntipiTerminalResult(result, d.q, g.Binding.AppURN, g.Binding.GroupID, h.OperationID, g.Binding.AppID, h.Body)
	// Cancel only through the upstream row-lock CAS of this exact accepted op.
	// A claim/token winning that race refuses cancellation. The original closed
	// outer exec is not proof that an upstream child died.
	if err == nil && result.State == "accepted" {
		result, err = d.originalMutation(ctx, h, "cancel", "")
		if err != nil {
			return n, observed, err
		}
		_, err = platformdeploy.ValidateRuntipiTerminalResult(result, d.q, g.Binding.AppURN, g.Binding.GroupID, h.OperationID, g.Binding.AppID, h.Body)
	}
	if !errors.Is(err, platformdeploy.ErrRuntipiTerminalFailed) {
		return n, observed, backuplifecycle.ErrControlUnresolved
	}
	outcome := result.RuntipiTerminalOutcome
	witness := result.WitnessDigest
	kind, phase := "", ""
	if result.Proof != nil {
		kind, phase = result.Proof.Kind, result.Proof.Phase
	}
	if result.Resolution != nil {
		outcome = result.Resolution.Outcome
		witness = result.Resolution.WitnessDigest
		kind = "command"
		if outcome.Reason == "COMPOSE_FAILED" {
			phase = "child-closed"
		} else {
			phase = "preparation"
		}
	}
	n = backuplifecycle.AppGroupNegative{OperationID: h.OperationID, RequestDigest: result.RequestDigest, WitnessDigest: witness, Kind: kind, Phase: phase, Reason: outcome.Reason, ComposeExit: outcome.ComposeExit}
	if h.Body.PredecessorOperationID != nil {
		n.PredecessorOperationID = *h.Body.PredecessorOperationID
	}
	switch {
	case kind == "never-claimed" && phase == "admission" && outcome.Reason == "NEVER_CLAIMED":
		n.NoNativeActuation = true
	case kind == "command" && phase == "preparation":
		switch outcome.Reason {
		case "PREPARATION_FAILED", "SPAWN_FAILED", "INPUT_CHANGED", "MISSING_INSTALLED_CONFIG":
			n.NoNativeActuation = true
		default:
			return n, observed, backuplifecycle.ErrControlUnresolved
		}
	case kind == "command" && phase == "child-closed" && outcome.Reason == "COMPOSE_FAILED" && outcome.ComposeExit != nil && *outcome.ComposeExit != 0:
	default:
		return n, observed, backuplifecycle.ErrControlUnresolved
	}
	n.PreviousCustodyDigest, err = d.custody.PredecessorDigest(ctx, g)
	if err != nil {
		return n, observed, err
	}
	if n.NoNativeActuation {
		prefix := g
		prefix.Effects = g.Effects[:len(g.Effects)-1]
		observed, err = d.custody.Observe(ctx, prefix, "verify")
		if err == nil && !reflect.DeepEqual(observed, prefix.Current()) {
			err = errors.New("negative native proof differs from preceding exact graph")
		}
	} else {
		observed, err = d.custody.Observe(ctx, g, "settle-negative")
	}
	return n, observed, err
}

// Bounded original-ID cancellation/resolve transport. This is never a new PUT
// and never retries an unknown actuation. Resolve remains restricted to the
// original native witness; a timeout or observed power supplies no evidence.
func (d *runtipiBackupControl) originalMutation(ctx context.Context, h runtipiGroupHandle, mode, witness string) (platformdeploy.RuntipiTerminalOperation, error) {
	var result platformdeploy.RuntipiTerminalOperation
	p := runtipiTerminalInvocation{Mode: mode, URN: d.group.Binding.AppURN, Group: d.group.Binding.GroupID, Operation: h.OperationID, AppID: d.group.Binding.AppID, PrincipalID: d.q.PrincipalID, Nonce: h.Nonce, Body: h.Body}
	body := map[string]any{"apiVersion": platformdeploy.RuntipiTerminalAPI, "bindingDigest": d.group.Binding.BindingDigest, "journalOperationId": d.group.Binding.NativeJournalOperationID, "lastOperationId": h.OperationID}
	if mode == "resolve" {
		body["witnessDigest"] = witness
	} else if mode != "cancel" {
		return result, errors.New("unknown original mutation")
	}
	p.Close = body
	return d.invokeOriginalReadback(ctx, h, p)
}

// Exact witness reconciliation uses R7's persisted original command token,
// epoch, source and witness-digest checks. No witness means no resolution. The
// native unresolved tombstone remains immutable; only its resolution is added.
func (d *runtipiBackupControl) recoverOriginalResult(ctx context.Context, h runtipiGroupHandle) (platformdeploy.RuntipiTerminalOperation, error) {
	result, err := d.lookup(ctx, h)
	if err != nil {
		return result, err
	}
	_, validation := platformdeploy.ValidateRuntipiTerminalResult(result, d.q, d.group.Binding.AppURN, d.group.Binding.GroupID, h.OperationID, d.group.Binding.AppID, h.Body)
	if errors.Is(validation, platformdeploy.ErrRuntipiTerminalUnresolved) && result.State == "unresolved" && !result.Terminal && result.Resolution == nil && regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(result.WitnessDigest) {
		return d.originalMutation(ctx, h, "resolve", result.WitnessDigest)
	}
	return result, nil
}
