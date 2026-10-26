package nativehost

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// Qualification is reachable only through the existing explicit custody issuer.
// A terminalControl JSON object alone cannot authorize control. This verifies
// its actual attached probe receipt and Owner signature, without exposing the
// installation operator secret or adding a competing qualification store.
func validateRuntipiQualificationEvidence(workspace string, raw []byte, q platformdeploy.RuntipiTerminalQualification) error {
	var saved struct {
		Evidence *runtipiQualificationEvidence `json:"terminalControlEvidence"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.Evidence == nil {
		return errors.New("terminal qualification lacks signed actual-probe evidence")
	}
	e := *saved.Evidence
	if e.Version != 1 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(e.ProbeExecID) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(e.CommandDigest) || !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(e.Nonce) || !reflect.DeepEqual(e.Qualification, q) {
		return errors.New("terminal qualification probe binding changed")
	}
	invocation := runtipiTerminalInvocation{Mode: "qualify", PrincipalID: q.PrincipalID, Nonce: e.Nonce, Body: platformdeploy.RuntipiTerminalSubmission{RuntipiTerminalIntent: platformdeploy.RuntipiTerminalIntent{APIVersion: platformdeploy.RuntipiTerminalAPI, ManagerID: q.ManagerID, SourceDigest: q.SourceDigest}}}
	command, err := terminalCommand(q.NodeExecutable, invocation)
	if err != nil {
		return err
	}
	if e.CommandDigest != backuplifecycle.AppGroupDigest(command) {
		return errors.New("terminal qualification command differs from current fixed helper")
	}
	signature := e.Signature
	e.Signature = localevidence.OwnerLifecycleMutationSignature{}
	canonical, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerLifecycleMutation(workspace, canonical, signature)
}
