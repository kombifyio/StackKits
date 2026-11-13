package ownapplication

import (
	"context"
	"fmt"
	"sort"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// SuspendOutboundForKitRollback runs before restoring any old executor or
// checkpoint authority, for every firewalled application (outbound Internet,
// published ports, or both). It revokes the runtime grant without deleting volumes
// or changing the desired v2 document. Old readers refuse that version; a new
// reader requires a reviewed update to recreate and restart it. This applies
// conservatively to every kit recovery, without guessing a release threshold.
func SuspendOutboundForKitRollback(ctx context.Context, workspace string) error {
	s, err := Load(workspace)
	if err != nil {
		return err
	}
	names := outboundRollbackNames(s)
	if len(names) == 0 {
		return nil
	}
	runtime, err := nativehost.NewOwnApplicationRuntime(workspace)
	if err != nil {
		return err
	}
	return suspendOutbound(ctx, workspace, s, names, runtime.Control, save)
}
func outboundRollbackNames(s State) []string {
	names := []string{}
	for name, r := range s.Applications {
		if r.Bundle.Application.Firewalled() || r.TransitionFrom != nil && r.TransitionFrom.Application.Firewalled() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
func suspendOutbound(ctx context.Context, workspace string, s State, names []string, control func(context.Context, contract.Bundle, string, bool, string) error, persist func(string, State) error) error {
	for _, name := range names {
		r := s.Applications[name]
		b := r.Bundle
		if r.TransitionFrom != nil {
			b = *r.TransitionFrom
		}
		// Persist pending before teardown: rollback may not invoke old code if an
		// exact removal or signed receipt fails, including a partially applied grant.
		r.Operation = Operation{Firewall: r.Operation.Firewall, FirewallAction: "removing", ID: "kit-rollback-" + r.Generation[7:23], Action: "kit-rollback-stop", Status: "pending", RequestDigest: hash(r.Bundle), ReviewDigest: r.Operation.ReviewDigest, At: time.Now().UTC()}
		s.Applications[name] = r
		if err := persist(workspace, s); err != nil {
			return err
		}
		if err := control(ctx, b, "remove", false, r.Generation); err != nil {
			return fmt.Errorf("kit rollback blocked while revoking outbound application %s: %w", name, err)
		}
		r.State = "stopped"
		r.TransitionFrom = nil
		r.Operation.Status = "succeeded"
		r.Operation.FirewallAction = "removed"
		r.Operation.At = time.Now().UTC()
		s.Applications[name] = r
		if err := persist(workspace, s); err != nil {
			return err
		}
	}
	return nil
}
