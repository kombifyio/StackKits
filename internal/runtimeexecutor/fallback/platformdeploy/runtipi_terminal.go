package platformdeploy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
)

// PROVISIONAL: this is the private native terminal capability, NOT stock
// Runtipi 4.10.2. Expected source/image/principal originate in retained node
// qualification; this package supplies no catalog default or credential store.
const RuntipiTerminalAPI = "runtipi.app-control/v1"

type RuntipiTerminalQualification struct {
	ManagerID          string `json:"managerId"`
	SourceDigest       string `json:"sourceDigest"`
	ManagerContainerID string `json:"managerContainerId"`
	ManagerImage       string `json:"managerImage"`
	PrincipalID        int    `json:"principalId"`
	HelperDigest       string `json:"helperDigest"`
	// Issued through the existing encrypted node installation custody only
	// after exact attached-stdin/EOF/handle recovery qualification. Engine
	// version admission alone does not qualify this new credential channel.
	NodeExecutable              string `json:"nodeExecutable"`
	InputTransport              string `json:"inputTransport"`
	InputTransportReceiptDigest string `json:"inputTransportReceiptDigest"`
}
type RuntipiTerminalCapability struct {
	APIVersion   string `json:"apiVersion"`
	BaseVersion  string `json:"baseVersion"`
	ManagerID    string `json:"managerId"`
	SourceDigest string `json:"sourceDigest"`
}
type RuntipiTerminalIntent struct {
	APIVersion           string `json:"apiVersion"`
	ManagerID            string `json:"managerId"`
	SourceDigest         string `json:"sourceDigest"`
	InstallationDigest   string `json:"installationDigest"`
	BindingDigest        string `json:"bindingDigest"`
	JournalOperationID   string `json:"journalOperationId"`
	RecipeDigest         string `json:"recipeDigest"`
	EffectiveInputDigest string `json:"effectiveInputDigest"`
}
type RuntipiTerminalSubmission struct {
	RuntipiTerminalIntent
	PredecessorOperationID *string `json:"predecessorOperationId"`
	Action                 string  `json:"action"`
	Effect                 string  `json:"effect"`
}
type RuntipiTerminalOutcome struct {
	State                string `json:"state"`
	Reason               string `json:"reason"`
	ComposeExit          *int   `json:"composeExit"`
	EffectiveInputDigest string `json:"effectiveInputDigest"`
}
type RuntipiTerminalOperation struct {
	RuntipiTerminalSubmission
	AppURN        string `json:"appUrn"`
	AppID         int    `json:"appId"`
	PrincipalID   int    `json:"principalId"`
	GroupID       string `json:"groupId"`
	OperationID   string `json:"operationId"`
	RequestID     string `json:"requestId"`
	RequestDigest string `json:"requestDigest"`
	WitnessDigest string `json:"witnessDigest"`
	Terminal      bool   `json:"terminal"`
	RuntipiTerminalOutcome
	Proof *struct {
		Kind         string `json:"kind"`
		Phase        string `json:"phase"`
		Cancellation string `json:"cancellation"`
	} `json:"proof"`
	Resolution *struct {
		WitnessDigest string                 `json:"witnessDigest"`
		Outcome       RuntipiTerminalOutcome `json:"outcome"`
	} `json:"resolution"`
}

var terminalHex = regexp.MustCompile(`^[a-f0-9]{64}$`)
var terminalUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[1-8][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var ErrRuntipiTerminalUnresolved = errors.New("original Runtipi operation is unresolved; inspect it without redispatch")
var ErrRuntipiTerminalFailed = errors.New("Runtipi terminal negative result is not actuation success")

func ValidateRuntipiTerminalQualification(q RuntipiTerminalQualification) error {
	if !terminalUUID.MatchString(q.ManagerID) || !terminalHex.MatchString(q.SourceDigest) || !terminalHex.MatchString(q.ManagerContainerID) || !regexp.MustCompile(`^.+@sha256:[a-f0-9]{64}$`).MatchString(q.ManagerImage) || q.PrincipalID < 1 || !terminalHex.MatchString(q.HelperDigest) || q.NodeExecutable != "/usr/local/bin/node" || q.InputTransport != "docker-exec-stdin/v1" || !terminalHex.MatchString(q.InputTransportReceiptDigest) {
		return errors.New("Runtipi private terminal capability requires exact retained source/image/helper/principal qualification")
	}
	return nil
}
func (a *RuntipiAdapter) TerminalCapability(ctx context.Context, q RuntipiTerminalQualification) (RuntipiTerminalCapability, error) {
	var c RuntipiTerminalCapability
	if err := ValidateRuntipiTerminalQualification(q); err != nil {
		return c, err
	}
	if err := a.request(ctx, http.MethodGet, "/api/app-controls/capability", nil, &c); err != nil {
		return c, err
	}
	if c.APIVersion != RuntipiTerminalAPI || c.BaseVersion != "4.10.2" || c.ManagerID != q.ManagerID || c.SourceDigest != q.SourceDigest {
		return c, errors.New("Runtipi terminal source/manager capability mismatch")
	}
	return c, nil
}
func terminalPath(urn, group string) (string, error) {
	if !runtipiAppName.MatchString(urnName(urn)) || urn != urnName(urn)+":_user" || !terminalUUID.MatchString(group) {
		return "", errors.New("invalid native app-group identity")
	}
	return "/api/app-controls/apps/" + url.PathEscape(urn) + "/groups/" + group, nil
}
func urnName(urn string) string {
	for i, c := range urn {
		if c == ':' {
			return urn[:i]
		}
	}
	return ""
}
func (a *RuntipiAdapter) OpenTerminalGroup(ctx context.Context, urn, group string, intent RuntipiTerminalIntent) error {
	path, err := terminalPath(urn, group)
	if err != nil {
		return err
	}
	if err = ValidateRuntipiTerminalIntent(intent); err != nil {
		return err
	}
	var reply struct {
		RuntipiTerminalIntent
		GroupID string `json:"groupId"`
		AppURN  string `json:"appUrn"`
	}
	if err = a.request(ctx, http.MethodPost, path, intent, &reply); err != nil {
		return err
	}
	// Native open returns a table row; apiVersion belongs to the request and
	// authenticated capability, not that row. Compare every persisted field.
	expected := intent
	expected.APIVersion = ""
	if reply.GroupID != group || reply.AppURN != urn || !reflect.DeepEqual(reply.RuntipiTerminalIntent, expected) {
		return errors.New("native app-group acknowledgment differs from retained intent")
	}
	return nil
}
func ValidateRuntipiTerminalIntent(i RuntipiTerminalIntent) error {
	if i.APIVersion != RuntipiTerminalAPI || !terminalUUID.MatchString(i.ManagerID) || !terminalUUID.MatchString(i.JournalOperationID) {
		return errors.New("invalid terminal intent identity")
	}
	for _, d := range []string{i.SourceDigest, i.InstallationDigest, i.BindingDigest, i.RecipeDigest, i.EffectiveInputDigest} {
		if !terminalHex.MatchString(d) {
			return errors.New("invalid terminal intent digest")
		}
	}
	return nil
}
func (a *RuntipiAdapter) SubmitTerminalOperation(ctx context.Context, urn, group, id string, body RuntipiTerminalSubmission) (RuntipiTerminalOperation, error) {
	var out RuntipiTerminalOperation
	path, err := terminalPath(urn, group)
	if err != nil {
		return out, err
	}
	if !terminalUUID.MatchString(id) {
		return out, errors.New("invalid original terminal operation ID")
	}
	if err = ValidateRuntipiTerminalIntent(body.RuntipiTerminalIntent); err != nil {
		return out, err
	}
	if body.Action != "start" && body.Action != "stop" || body.Effect != body.Action && body.Effect != "rollback-"+body.Action {
		return out, errors.New("terminal action/effect mismatch")
	}
	// Exactly one HTTP attempt; a reply loss is recovered only by GET original.
	err = a.request(ctx, http.MethodPut, path+"/operations/"+id, body, &out)
	return out, err
}
func (a *RuntipiAdapter) TerminalOperation(ctx context.Context, urn, group, id string) (RuntipiTerminalOperation, error) {
	var out RuntipiTerminalOperation
	path, err := terminalPath(urn, group)
	if err != nil {
		return out, err
	}
	if !terminalUUID.MatchString(id) {
		return out, errors.New("invalid original terminal operation ID")
	}
	err = a.request(ctx, http.MethodGet, path+"/operations/"+id, nil, &out)
	return out, err
}
func ValidateRuntipiTerminalResult(out RuntipiTerminalOperation, q RuntipiTerminalQualification, urn, group, id string, appID int, body RuntipiTerminalSubmission) (bool, error) {
	if err := ValidateRuntipiTerminalQualification(q); err != nil {
		return false, err
	}
	if out.AppURN != urn || out.AppID != appID || out.PrincipalID != q.PrincipalID || out.GroupID != group || out.OperationID != id || out.RequestID != id || !terminalHex.MatchString(out.RequestDigest) || !reflect.DeepEqual(out.RuntipiTerminalSubmission, body) || body.ManagerID != q.ManagerID || body.SourceDigest != q.SourceDigest {
		return false, errors.New("terminal result differs from exact original operation/app/action/principal")
	}
	outcome := out.RuntipiTerminalOutcome
	witness := out.WitnessDigest
	if out.State == "unresolved" {
		if !out.Terminal || out.Resolution == nil {
			return false, ErrRuntipiTerminalUnresolved
		}
		outcome = out.Resolution.Outcome
		witness = out.Resolution.WitnessDigest
	}
	if outcome.State == "accepted" || outcome.State == "executing" {
		if out.Terminal {
			return false, errors.New("invalid terminal flag")
		}
		return false, nil
	}
	if outcome.State == "failed" || outcome.State == "skipped" {
		if !out.Terminal || !terminalHex.MatchString(witness) {
			return false, ErrRuntipiTerminalUnresolved
		}
		return false, ErrRuntipiTerminalFailed
	}
	if !out.Terminal || outcome.State != "succeeded" || outcome.ComposeExit == nil || *outcome.ComposeExit != 0 || outcome.EffectiveInputDigest != body.EffectiveInputDigest || !terminalHex.MatchString(witness) {
		return false, ErrRuntipiTerminalUnresolved
	}
	return true, nil
}
