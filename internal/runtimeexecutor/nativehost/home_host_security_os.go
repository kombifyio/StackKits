package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/hostsecurity"
)

// osHomeHostSecurityOperations enforces the Home baseline on the local host
// through the same hostsecurity engine `stackkit host security repair` uses, so
// an apply and an operator repair cannot disagree about what the baseline is or
// what cutting the management path means.
type osHomeHostSecurityOperations struct {
	workspaceRoot string
	mode          hostsecurity.Mode
	engine        hostsecurity.Engine
	mu            sync.Mutex
}

// NewOSHomeHostSecurityOperations explicitly selects the local operating
// system as the closed Home host-security capability owner. Constructing an
// executor does not grant this authority; product composition must opt in. The
// mode is recorded in the evidence: standard for the account-free CLI,
// advanced for a deployment dispatched by Techstack.
func NewOSHomeHostSecurityOperations(workspaceRoot string, mode hostsecurity.Mode) (*osHomeHostSecurityOperations, error) {
	root, err := ownerWorkspaceRoot(workspaceRoot, "local Home host security")
	if err != nil {
		return nil, err
	}
	if mode != hostsecurity.ModeStandard && mode != hostsecurity.ModeAdvanced {
		return nil, errors.New("local Home host security requires the standard or advanced mode")
	}
	return &osHomeHostSecurityOperations{workspaceRoot: root, mode: mode, engine: hostsecurity.Engine{Host: hostsecurity.LocalHost{}}}, nil
}

// Enforce repairs every drifted enforced control, then reads the host back. A
// refusal (for example no authorized key to keep ssh reachable) or a control
// that is still not compliant fails the apply with the reason.
func (o *osHomeHostSecurityOperations) Enforce(ctx context.Context) (HomeHostSecurityObservation, error) {
	if ctx == nil {
		return HomeHostSecurityObservation{}, errors.New("local Home host security requires a context")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	report := o.engine.Repair(ctx, hostsecurity.RepairOptions{
		Options: hostsecurity.Options{
			Mode: o.mode, SiteKind: hostsecurity.SiteHome,
			ExceptionsPath: hostsecurity.DefaultExceptionsPath, WorkspaceRoot: o.workspaceRoot,
		},
		Controls: hostsecurity.EnforcedControls(),
		Apply:    true,
	})
	if report.Outcome == hostsecurity.OutcomeFailed || (report.Outcome == hostsecurity.OutcomeBlocked && !onlyNoAuthorizedKeyBlocked(report)) {
		return HomeHostSecurityObservation{}, errors.New(homeHostSecurityRefusal(report))
	}
	if report.After == nil {
		return HomeHostSecurityObservation{}, errors.New("the repair did not read the host back")
	}
	evidence := *report.After
	if _, err := o.engine.SaveEvidence(o.workspaceRoot, evidence); err != nil {
		return HomeHostSecurityObservation{}, fmt.Errorf("commit Home host-security evidence: %w", err)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return HomeHostSecurityObservation{}, fmt.Errorf("digest Home host-security evidence: %w", err)
	}
	sum := sha256.Sum256(raw)
	observation := HomeHostSecurityObservation{
		BaselineVersion: evidence.BaselineVersion,
		ObservedAt:      evidence.ObservedAt.UTC().Format(time.RFC3339Nano),
		EvidenceDigest:  "sha256:" + hex.EncodeToString(sum[:]),
	}
	for _, control := range evidence.Controls {
		observation.Controls = append(observation.Controls, HomeHostSecurityControlState{ID: control.ID, State: string(control.State), ReasonCode: control.ReasonCode})
		if control.ID == hostsecurity.ControlSSHPassword && control.ReasonCode == hostsecurity.ReasonNoAuthorizedKey {
			finding := control.ID + ": " + hostsecurity.ReasonNoAuthorizedKey + ": " + control.Remediation.Action
			observation.Findings = append(observation.Findings, finding)
			// The apply succeeds, so the owner must be told where it matters.
			fmt.Fprintln(os.Stderr, "kombify host security: password logins stay enabled. "+finding)
		}
	}
	return observation, nil
}

// onlyNoAuthorizedKeyBlocked reports whether every refusal in the report is the
// tolerated no-authorized-key finding on ssh password authentication.
func onlyNoAuthorizedKeyBlocked(report hostsecurity.RepairReport) bool {
	for _, step := range report.Steps {
		if step.Status != hostsecurity.StepBlocked {
			continue
		}
		if step.Control != hostsecurity.ControlSSHPassword || step.ReasonCode != hostsecurity.ReasonNoAuthorizedKey {
			return false
		}
	}
	return true
}

// homeHostSecurityRefusal states why the baseline was not enforced, control by
// control, so an owner can act on it (add a login key, record an exception).
func homeHostSecurityRefusal(report hostsecurity.RepairReport) string {
	var reasons []string
	for _, step := range report.Steps {
		if (step.Status == hostsecurity.StepBlocked || step.Status == hostsecurity.StepFailed) && step.Reason != "" {
			reasons = append(reasons, step.Control+": "+step.Reason)
		}
	}
	if len(reasons) == 0 {
		return "the Home host security baseline was not enforced"
	}
	return "the Home host security baseline was not enforced; nothing that would cut the management path was changed: " + strings.Join(reasons, "; ")
}

var _ HomeHostSecurityOperations = (*osHomeHostSecurityOperations)(nil)
