package nativehost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

//go:embed runtipi_terminal_helper.mjs
var runtipiTerminalHelper string

func RuntipiTerminalHelperDigest() string {
	s := sha256.Sum256([]byte(runtipiTerminalHelper))
	return hex.EncodeToString(s[:])
}

type runtipiGroupHandle struct {
	Version            int                                      `json:"version"`
	ExecID             string                                   `json:"execId"`
	Nonce              string                                   `json:"nonce"`
	ContainerID        string                                   `json:"containerId"`
	Command            []string                                 `json:"command"`
	Body               platformdeploy.RuntipiTerminalSubmission `json:"body"`
	OperationID        string                                   `json:"operationId"`
	GroupBindingDigest string                                   `json:"groupBindingDigest"`
}
type runtipiTerminalInvocation struct {
	Mode        string                                   `json:"mode"`
	URN         string                                   `json:"urn"`
	Group       string                                   `json:"group"`
	Operation   string                                   `json:"operation,omitempty"`
	AppID       int                                      `json:"appId"`
	PrincipalID int                                      `json:"principalId"`
	Nonce       string                                   `json:"nonce"`
	Body        platformdeploy.RuntipiTerminalSubmission `json:"body"`
	Close       any                                      `json:"close,omitempty"`
}

// RuntipiGroupCustody is the existing node owner seam. Observe must independently
// inspect the local daemon/volumes; Adopt must CAS the EXISTING signed deployment
// custody from the original/prior generation, using the signed owning journal.
// No default callback is supplied: opaque hashes do not verify signatures.
type RuntipiGroupCustody struct {
	PredecessorDigest func(context.Context, backuplifecycle.AppGroupControl) (string, error)
	Observe           func(context.Context, backuplifecycle.AppGroupControl, string) (backuplifecycle.AppGroupGeneration, error)
	Adopt             func(context.Context, backuplifecycle.AppGroupControl) error
	VerifyJournal     func(context.Context, backuplifecycle.AppGroupControl) error
	VerifyCompletion  func(context.Context, backuplifecycle.AppGroupControl) error
}
type runtipiBackupControl struct {
	workspace string
	group     backuplifecycle.AppGroupControl
	q         platformdeploy.RuntipiTerminalQualification
	client    *docker.ExecHandleClient
	custody   RuntipiGroupCustody
}

func NewRuntipiBackupControlDriver(workspace string, group backuplifecycle.AppGroupControl, custody RuntipiGroupCustody) (backuplifecycle.AppGroupControlDriver, error) {
	if err := backuplifecycle.ValidateAppGroups([]backuplifecycle.AppGroupControl{group}); err != nil {
		return nil, err
	}
	if custody.Observe == nil || custody.Adopt == nil || custody.VerifyJournal == nil || custody.VerifyCompletion == nil || custody.PredecessorDigest == nil {
		return nil, errors.New("Runtipi group requires existing signed journal/atomic node-generation custody")
	}
	q, _, err := loadRuntipiTerminalCustody(workspace, group.Binding)
	if err != nil {
		return nil, err
	}
	client, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return nil, err
	}
	return &runtipiBackupControl{workspace: workspace, group: backuplifecycle.CloneAppGroups([]backuplifecycle.AppGroupControl{group})[0], q: q, client: client, custody: custody}, nil
}
func loadRuntipiTerminalCustody(workspace string, b backuplifecycle.AppGroupBinding) (platformdeploy.RuntipiTerminalQualification, []byte, error) {
	var q platformdeploy.RuntipiTerminalQualification
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil || owner.OwnerRef != b.OwnerRef || owner.Binding.SiteRef != b.SiteRef || owner.Binding.NodeRef != b.NodeRef {
		return q, nil, errors.New("Runtipi terminal node Owner changed")
	}
	raw, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef)
	if err != nil {
		return q, nil, errors.New("Runtipi terminal requires existing encrypted installation custody")
	}
	defer clear(raw)
	var saved struct {
		runtipiInstallationCustody
		TerminalControl *platformdeploy.RuntipiTerminalQualification `json:"terminalControl"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.TerminalControl == nil || !saved.Bootstrapped || saved.InstallationID != b.InstallationDigest || saved.OwnerRef != b.OwnerRef || saved.SiteRef != b.SiteRef || saved.NodeRef != b.NodeRef || saved.Username == "" || saved.Password == "" {
		return q, nil, errors.New("private terminal qualification absent from exact installation custody")
	}
	q = *saved.TerminalControl
	if err = validateRuntipiQualificationEvidence(workspace, raw, q); err != nil {
		return q, nil, err
	}
	if err = platformdeploy.ValidateRuntipiTerminalQualification(q); err != nil {
		return q, nil, err
	}
	if q.ManagerID != b.ManagerID || q.SourceDigest != b.SourceDigest || q.ManagerContainerID != b.ManagerContainerID || q.ManagerImage != b.ManagerImage || q.HelperDigest != RuntipiTerminalHelperDigest() || b.QualificationDigest != "" && b.QualificationDigest != backuplifecycle.AppGroupDigest(q) {
		return q, nil, errors.New("private source/image/helper qualification mismatch")
	}
	secret, err := json.Marshal(struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{saved.Username, saved.Password})
	return q, secret, err
}
func (d *runtipiBackupControl) validate(ctx context.Context) error {
	_, secret, err := loadRuntipiTerminalCustody(d.workspace, d.group.Binding)
	clear(secret)
	if err != nil {
		return err
	}
	c, err := docker.NewLocalClient().InspectContainer(ctx, d.q.ManagerContainerID)
	if err != nil {
		return err
	}
	if !c.State.Running || c.Config.Image != d.q.ManagerImage {
		return errors.New("Runtipi manager exact container/image changed")
	}
	if err := validateRuntipiHelperEnvironment(c); err != nil {
		return err
	}
	return d.client.Qualify(ctx)
}
func runtipiControlUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func (d *runtipiBackupControl) invocation(action, mode string) (runtipiTerminalInvocation, error) {
	b := d.group.Binding
	var predecessor *string
	for _, e := range d.group.Effects {
		if e.Receipt != nil {
			value := e.Receipt.OperationID
			predecessor = &value
		} else if e.Negative != nil && e.Negative.OperationID != "" {
			value := e.Negative.OperationID
			predecessor = &value
		}
	}
	operation, nonce := runtipiControlUUID(), runtipiControlUUID()
	if operation == "" || nonce == "" {
		return runtipiTerminalInvocation{}, errors.New("native control entropy unavailable")
	}
	native := strings.TrimPrefix(action, "rollback-")
	if native != "start" && native != "stop" {
		return runtipiTerminalInvocation{}, errors.New("invalid app-group action")
	}
	return runtipiTerminalInvocation{Mode: mode, URN: b.AppURN, Group: b.GroupID, Operation: operation, AppID: b.AppID, PrincipalID: d.q.PrincipalID, Nonce: nonce, Body: platformdeploy.RuntipiTerminalSubmission{RuntipiTerminalIntent: platformdeploy.RuntipiTerminalIntent{APIVersion: platformdeploy.RuntipiTerminalAPI, ManagerID: b.ManagerID, SourceDigest: b.SourceDigest, InstallationDigest: b.InstallationDigest, BindingDigest: b.BindingDigest, JournalOperationID: b.NativeJournalOperationID, RecipeDigest: b.RecipeDigest, EffectiveInputDigest: b.EffectiveInputDigest}, PredecessorOperationID: predecessor, Action: native, Effect: action}}, nil
}
func terminalCommand(node string, p runtipiTerminalInvocation) ([]string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return []string{node, "--input-type=module", "-e", runtipiTerminalHelper, string(raw)}, nil
}
func (d *runtipiBackupControl) Prepare(ctx context.Context, action string) (string, error) {
	if err := d.validate(ctx); err != nil {
		return "", err
	}
	p, err := d.invocation(action, "actuate")
	if err != nil {
		return "", err
	}
	command, err := terminalCommand(d.q.NodeExecutable, p)
	if err != nil {
		return "", err
	}
	id, err := d.client.PrepareInput(ctx, d.q.ManagerContainerID, command)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(runtipiGroupHandle{Version: 2, ExecID: id, Nonce: p.Nonce, ContainerID: d.q.ManagerContainerID, Command: command, Body: p.Body, OperationID: p.Operation, GroupBindingDigest: backuplifecycle.AppGroupDigest(d.group.Binding)})
	return string(raw), err
}
func (d *runtipiBackupControl) handle(raw, action string) (runtipiGroupHandle, error) {
	var h runtipiGroupHandle
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &h) != nil || h.Version != 2 || h.ContainerID != d.q.ManagerContainerID || h.GroupBindingDigest != backuplifecycle.AppGroupDigest(d.group.Binding) || h.Body.Effect != action || len(h.Command) != 5 {
		return h, errors.New("retained app-group handle binding changed")
	}
	p := runtipiTerminalInvocation{Mode: "actuate", URN: d.group.Binding.AppURN, Group: d.group.Binding.GroupID, Operation: h.OperationID, AppID: d.group.Binding.AppID, PrincipalID: d.q.PrincipalID, Nonce: h.Nonce, Body: h.Body}
	expected, err := terminalCommand(d.q.NodeExecutable, p)
	if err != nil || !reflect.DeepEqual(h.Command, expected) {
		return h, errors.New("retained app-group command/nonce changed")
	}
	// Full fixed body/identity is also validated when reading its native result.
	if err = platformdeploy.ValidateRuntipiTerminalIntent(h.Body.RuntipiTerminalIntent); err != nil {
		return h, err
	}
	// A historical effect binds the predecessor before that effect, not a
	// later receipt appended to the same signed group during recovery.
	prior := *d
	for index, effect := range d.group.Effects {
		if effect.Handle == raw {
			prior.group.Effects = d.group.Effects[:index]
			break
		}
	}
	expectedBody, err := prior.invocation(action, "actuate")
	if err != nil {
		return h, err
	}
	if !reflect.DeepEqual(h.Body, expectedBody.Body) || h.OperationID == "" || h.Nonce == "" {
		return h, errors.New("retained command differs from group intent/predecessor")
	}
	return h, nil
}
func (d *runtipiBackupControl) Dispatch(ctx context.Context, raw, action string) error {
	if err := d.validate(ctx); err != nil {
		return err
	}
	h, err := d.handle(raw, action)
	if err != nil {
		return err
	}
	done, err := d.client.Complete(ctx, h.ExecID, h.ContainerID, h.Command, 73)
	if err != nil {
		return err
	}
	if done {
		return errors.New("native handle already completed before dispatch")
	}
	_, secret, err := loadRuntipiTerminalCustody(d.workspace, d.group.Binding)
	if err != nil {
		return err
	}
	defer clear(secret)
	return d.client.StartInput(ctx, h.ExecID, h.Command, secret)
}
func (d *runtipiBackupControl) Complete(ctx context.Context, raw, action string) (bool, error) {
	if err := d.validate(ctx); err != nil {
		return false, err
	}
	h, err := d.handle(raw, action)
	if err != nil {
		return false, err
	}
	code, ended, err := d.client.InputExit(ctx, h.ExecID, h.ContainerID, h.Command)
	if err != nil || !ended {
		return false, err
	}
	if code != 73 && code != 74 {
		return false, backuplifecycle.ErrControlUnresolved
	}
	// HTTP reply loss may make this exact outer helper exit74 even after the
	// native child committed. Recover only through GET of this original ID,
	// after proving the original outer process ended. Never create another PUT.
	result, err := d.recoverOriginalResult(ctx, h)
	if err != nil {
		return false, err
	}
	return platformdeploy.ValidateRuntipiTerminalResult(result, d.q, d.group.Binding.AppURN, d.group.Binding.GroupID, h.OperationID, d.group.Binding.AppID, h.Body)
}

// lookup is read-only and never creates a replacement ACTUATION handle. It uses
// existing ExecWithStdin for an independent authenticated GET of original IDs.
func (d *runtipiBackupControl) lookup(ctx context.Context, h runtipiGroupHandle) (platformdeploy.RuntipiTerminalOperation, error) {
	p := runtipiTerminalInvocation{Mode: "lookup", URN: d.group.Binding.AppURN, Group: d.group.Binding.GroupID, Operation: h.OperationID, AppID: d.group.Binding.AppID, PrincipalID: d.q.PrincipalID, Nonce: h.Nonce, Body: h.Body}
	return d.invokeOriginalReadback(ctx, h, p)
}
func (d *runtipiBackupControl) invokeOriginalReadback(ctx context.Context, h runtipiGroupHandle, p runtipiTerminalInvocation) (platformdeploy.RuntipiTerminalOperation, error) {
	var out platformdeploy.RuntipiTerminalOperation
	command, err := terminalCommand(d.q.NodeExecutable, p)
	if err != nil {
		return out, err
	}
	_, secret, err := loadRuntipiTerminalCustody(d.workspace, d.group.Binding)
	if err != nil {
		return out, err
	}
	defer clear(secret)
	raw, err := docker.NewLocalClient().ExecWithStdin(ctx, h.ContainerID, command, secret)
	if err != nil {
		return out, errors.New("original native operation lookup unconfirmed")
	}
	if len(raw) > 262144 || json.Unmarshal([]byte(raw), &out) != nil {
		return out, errors.New("invalid bounded native operation result")
	}
	return out, nil
}
func (d *runtipiBackupControl) Readback(ctx context.Context, g backuplifecycle.AppGroupControl, raw, action string) (backuplifecycle.AppGroupReceipt, backuplifecycle.AppGroupGeneration, error) {
	var receipt backuplifecycle.AppGroupReceipt
	var generation backuplifecycle.AppGroupGeneration
	if !reflect.DeepEqual(g.Binding, d.group.Binding) {
		return receipt, generation, errors.New("app-group readback binding changed")
	}
	h, err := d.handle(raw, action)
	if err != nil {
		return receipt, generation, err
	}
	result, err := d.recoverOriginalResult(ctx, h)
	if err != nil {
		return receipt, generation, err
	}
	ok, err := platformdeploy.ValidateRuntipiTerminalResult(result, d.q, g.Binding.AppURN, g.Binding.GroupID, h.OperationID, g.Binding.AppID, h.Body)
	if err != nil || !ok {
		return receipt, generation, errors.Join(backuplifecycle.ErrControlUnresolved, err)
	}
	witness := result.WitnessDigest
	if result.Resolution != nil {
		witness = result.Resolution.WitnessDigest
	}
	receipt = backuplifecycle.AppGroupReceipt{OperationID: h.OperationID, RequestDigest: result.RequestDigest, WitnessDigest: witness}
	if h.Body.PredecessorOperationID != nil {
		receipt.PredecessorOperationID = *h.Body.PredecessorOperationID
	}
	receipt.PreviousCustodyDigest, err = d.custody.PredecessorDigest(ctx, g)
	if err != nil {
		return receipt, generation, err
	}
	generation, err = d.custody.Observe(ctx, g, action)
	return receipt, generation, err
}
func (d *runtipiBackupControl) Adopt(ctx context.Context, g backuplifecycle.AppGroupControl) error {
	if err := d.custody.VerifyJournal(ctx, g); err != nil {
		return err
	}
	return d.custody.Adopt(ctx, g)
}
func (d *runtipiBackupControl) Close(ctx context.Context, g backuplifecycle.AppGroupControl) error {
	if !backuplifecycle.AppGroupRestored(g) {
		return backuplifecycle.ErrControlUnresolved
	}
	if err := d.custody.VerifyCompletion(ctx, g); err != nil {
		return err
	}
	if len(g.Effects) == 0 {
		if err := d.custody.Adopt(ctx, g); err != nil {
			return err
		}
	}
	p, err := d.invocation("stop", "close")
	if err != nil {
		return err
	}
	var last *string
	for _, e := range g.Effects {
		if e.Receipt != nil {
			v := e.Receipt.OperationID
			last = &v
		} else if e.Negative != nil && e.Negative.OperationID != "" {
			v := e.Negative.OperationID
			last = &v
		}
	}
	p.Close = map[string]any{"apiVersion": platformdeploy.RuntipiTerminalAPI, "bindingDigest": g.Binding.BindingDigest, "journalOperationId": g.Binding.NativeJournalOperationID, "lastOperationId": last, "completionDigest": backuplifecycle.AppGroupDigest(g)}
	command, err := terminalCommand(d.q.NodeExecutable, p)
	if err != nil {
		return err
	}
	_, secret, err := loadRuntipiTerminalCustody(d.workspace, g.Binding)
	if err != nil {
		return err
	}
	defer clear(secret)
	_, err = docker.NewLocalClient().ExecWithStdin(ctx, d.q.ManagerContainerID, command, secret)
	return err
}

func (d *runtipiBackupControl) Hold(ctx context.Context, g backuplifecycle.AppGroupControl) error {
	if err := d.validate(ctx); err != nil {
		return err
	}
	if err := d.custody.VerifyJournal(ctx, g); err != nil {
		return err
	}
	p, err := d.invocation("stop", "open")
	if err != nil {
		return err
	}
	command, err := terminalCommand(d.q.NodeExecutable, p)
	if err != nil {
		return err
	}
	_, secret, err := loadRuntipiTerminalCustody(d.workspace, g.Binding)
	if err != nil {
		return err
	}
	defer clear(secret)
	_, err = docker.NewLocalClient().ExecWithStdin(ctx, d.q.ManagerContainerID, command, secret)
	if err != nil {
		return err
	}
	// Capture preceded native hold admission. A legacy restart in that gap
	// must not authorize stopping a different G0 generation with the same name.
	if len(g.Effects) == 0 {
		return d.VerifyCurrent(ctx, g)
	}
	return nil
}
func (d *runtipiBackupControl) VerifyCurrent(ctx context.Context, g backuplifecycle.AppGroupControl) error {
	if err := d.validate(ctx); err != nil {
		return err
	}
	observed, err := d.custody.Observe(ctx, g, "verify")
	if err != nil {
		return err
	}
	if backuplifecycle.AppGroupDigest(observed) != backuplifecycle.AppGroupDigest(g.Current()) {
		return errors.New("current app-group generation differs from signed adoption")
	}
	return nil
}

// InspectRuntipiBackupEffect performs only authenticated GET of the original
// retained native operation. It does not prepare/dispatch a handle, cancel,
// resolve, adopt custody or infer success from current power. Its caller must
// authenticate g through the owning snapshot/restore journal first.
func InspectRuntipiBackupEffect(ctx context.Context, workspace string, g backuplifecycle.AppGroupControl, index int) (platformdeploy.RuntipiTerminalOperation, error) {
	var result platformdeploy.RuntipiTerminalOperation
	if err := backuplifecycle.ValidateAppGroups([]backuplifecycle.AppGroupControl{g}); err != nil {
		return result, err
	}
	if index < 0 || index >= len(g.Effects) || g.Effects[index].Handle == "" {
		return result, errors.New("original group effect/handle absent")
	}
	q, secret, err := loadRuntipiTerminalCustody(workspace, g.Binding)
	clear(secret)
	if err != nil {
		return result, err
	}
	client, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return result, err
	}
	d := &runtipiBackupControl{workspace: workspace, group: g, q: q, client: client}
	if err = d.validate(ctx); err != nil {
		return result, err
	}
	e := g.Effects[index]
	h, err := d.handle(e.Handle, e.Action)
	if err != nil {
		return result, err
	}
	result, err = d.lookup(ctx, h)
	if err != nil {
		return result, err
	}
	_, err = platformdeploy.ValidateRuntipiTerminalResult(result, q, g.Binding.AppURN, g.Binding.GroupID, h.OperationID, g.Binding.AppID, h.Body)
	if err != nil && !errors.Is(err, platformdeploy.ErrRuntipiTerminalUnresolved) && !errors.Is(err, platformdeploy.ErrRuntipiTerminalFailed) {
		return result, err
	}
	return result, nil
}

func validateRuntipiHelperEnvironment(c *docker.ContainerInfo) error {
	// The closed Node helper cannot inherit interpreter/preload injection.
	for _, entry := range c.Config.Env {
		key, value, _ := strings.Cut(entry, "=")
		if value != "" && (key == "NODE_OPTIONS" || key == "NODE_PATH" || key == "LD_PRELOAD" || key == "LD_LIBRARY_PATH") {
			return errors.New("Runtipi qualified helper interpreter environment changed")
		}
	}
	return nil
}
