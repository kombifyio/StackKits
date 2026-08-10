// Package servicecontrol owns the local, owner-approved desired state and
// bounded execution contract for StackKits-managed services.
package servicecontrol

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/logging"
)

const (
	StateAPIVersion  = "stackkit.service-desired-state/v1"
	ResultAPIVersion = "stackkit.service-action-result/v1"
	LogsAPIVersion   = "stackkit.service-logs/v1"

	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"

	DesiredRunning = "running"
	DesiredStopped = "stopped"

	ReasonCriticalControlPlane = "critical_control_plane"
	ReasonPlanChanged          = "desired_state_plan_changed"
	ReasonContractChanged      = "desired_state_contract_changed"
	ReasonUnknownService       = "unknown_service"
	ReasonOwnerApproval        = "owner_approval_required"
	ReasonRuntimeUnavailable   = "service_runtime_unavailable"

	stateRelativePath = ".stackkit/service-control/desired-state.json"
	evidenceRoot      = ".stackkit/service-control/evidence"
	defaultPlanPath   = "deploy/.stackkit/resolved-plan.json"
	defaultCompose    = ".stackkit/runtime/cloud-core/compose.yaml"
)

const serviceContractMaterial = "stackkit.service-control/v1|base:router,socket-proxy,step-ca,hub,kopia-agent:start,restart,logs|auth:tinyauth:start,restart,logs|id:pocketid:start,restart,logs|coolify:coolify,coolify-postgres,coolify-redis,coolify-realtime:start,stop,restart,logs"

var cursorPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type serviceDefinition struct {
	Components []string
	CanStop    bool
}

var serviceDefinitions = map[string]serviceDefinition{
	"base":    {Components: []string{"router", "socket-proxy", "step-ca", "hub", "kopia-agent"}},
	"auth":    {Components: []string{"tinyauth"}},
	"id":      {Components: []string{"pocketid"}},
	"coolify": {Components: []string{"coolify", "coolify-postgres", "coolify-redis", "coolify-realtime"}, CanStop: true},
}

type DesiredService struct {
	State     string    `json:"state"`
	ChangedAt time.Time `json:"changedAt"`
}

type DesiredState struct {
	APIVersion          string                                  `json:"apiVersion"`
	OwnerRef            string                                  `json:"ownerRef"`
	PlanHash            string                                  `json:"planHash"`
	ServiceContractHash string                                  `json:"serviceContractHash"`
	Revision            uint64                                  `json:"revision"`
	Services            map[string]DesiredService               `json:"services"`
	UpdatedAt           time.Time                               `json:"updatedAt"`
	Signature           localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type Result struct {
	APIVersion    string  `json:"apiVersion"`
	Action        string  `json:"action"`
	ServiceKey    string  `json:"serviceKey"`
	DesiredState  string  `json:"desiredState"`
	ObservedState string  `json:"observedState"`
	Revision      uint64  `json:"revision"`
	Checks        []Check `json:"checks"`
	ReasonCode    string  `json:"reasonCode,omitempty"`
	Retryable     bool    `json:"retryable"`
	EvidenceRef   string  `json:"evidenceRef,omitempty"`
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
}

type LogsResult struct {
	APIVersion  string     `json:"apiVersion"`
	ServiceKey  string     `json:"serviceKey"`
	Entries     []LogEntry `json:"entries"`
	NextCursor  string     `json:"nextCursor,omitempty"`
	Truncated   bool       `json:"truncated"`
	EvidenceRef string     `json:"evidenceRef,omitempty"`
}

type Error struct {
	ReasonCode string
	Retryable  bool
	Message    string
}

func (e *Error) Error() string { return e.Message }

type Runner interface {
	Run(context.Context, string, []string) ([]byte, error)
}

type Signer interface {
	OwnerRef(string) (string, error)
	Sign(string, []byte) (localevidence.OwnerPolicyStateSignature, error)
	Verify(string, []byte, localevidence.OwnerPolicyStateSignature) error
}

type Controller struct {
	workspace string
	runner    Runner
	signer    Signer
	now       func() time.Time
}

func NewOSController(workspace string) (*Controller, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("service control requires an absolute workspace")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("service control requires an existing plain workspace directory")
	}
	return &Controller{workspace: filepath.Clean(absolute), runner: osRunner{}, signer: ownerSigner{}, now: time.Now}, nil
}

func NewController(workspace string, runner Runner, signer Signer, now func() time.Time) *Controller {
	return &Controller{workspace: workspace, runner: runner, signer: signer, now: now}
}

func ServiceContractHash() string {
	sum := sha256.Sum256([]byte(serviceContractMaterial))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func AllowedActions(serviceKey string) []string {
	definition, ok := serviceDefinitions[normalizeServiceKey(serviceKey)]
	if !ok {
		return []string{}
	}
	actions := []string{ActionStart}
	if definition.CanStop {
		actions = append(actions, ActionStop)
	}
	return append(actions, ActionRestart, "logs")
}

func (c *Controller) Mutate(ctx context.Context, action, serviceKey string, ownerApproved bool) (Result, error) {
	result := Result{APIVersion: ResultAPIVersion, Action: strings.ToLower(strings.TrimSpace(action)), ServiceKey: normalizeServiceKey(serviceKey), Retryable: false}
	if ctx == nil || c == nil || c.runner == nil || c.signer == nil || c.now == nil {
		return result, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service runtime is not initialized"}
	}
	if !ownerApproved {
		return result, &Error{ReasonCode: ReasonOwnerApproval, Message: "service mutation requires explicit owner approval"}
	}
	definition, ok := serviceDefinitions[result.ServiceKey]
	if !ok {
		return result, &Error{ReasonCode: ReasonUnknownService, Message: "unknown managed service " + result.ServiceKey}
	}
	if result.Action != ActionStart && result.Action != ActionStop && result.Action != ActionRestart {
		return result, &Error{ReasonCode: "unknown_action", Message: "unsupported service action " + result.Action}
	}
	if result.Action == ActionStop && !definition.CanStop {
		return result, &Error{ReasonCode: ReasonCriticalControlPlane, Message: "service stop denied: critical control-plane service"}
	}
	authority, err := c.authority()
	if err != nil {
		return result, err
	}
	state, err := c.loadState(authority)
	if err != nil {
		return result, err
	}
	desired := DesiredRunning
	if existing, exists := state.Services[result.ServiceKey]; exists {
		desired = existing.State
	}
	if result.Action == ActionStop {
		desired = DesiredStopped
	} else if result.Action == ActionStart {
		desired = DesiredRunning
	}
	result.DesiredState = desired
	args := mutationArgs(result.Action, definition.Components)
	if _, err := c.runner.Run(ctx, authority.ComposePath, args); err != nil {
		return result, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service action failed: " + logging.RedactText(err.Error())}
	}
	observed, err := c.observe(ctx, authority.ComposePath, definition.Components)
	if err != nil {
		return result, err
	}
	result.ObservedState = observed
	if result.Action == ActionStart || result.Action == ActionStop {
		state.Revision++
		state.Services[result.ServiceKey] = DesiredService{State: desired, ChangedAt: c.now().UTC()}
		state.UpdatedAt = c.now().UTC()
		if err := c.persistState(&state); err != nil {
			return result, err
		}
	}
	result.Revision = state.Revision
	result.Checks = []Check{{Name: "owner_approval", Status: "ok"}, {Name: "runtime_action", Status: "ok"}, {Name: "runtime_observation", Status: "ok", Detail: observed}}
	result.EvidenceRef, err = c.persistEvidence(result)
	if err != nil {
		return result, err
	}
	if err := c.updateAccessManifest(result.ServiceKey, result.DesiredState, result.EvidenceRef); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Controller) updateAccessManifest(serviceKey, desiredState, evidenceRef string) error {
	path := filepath.Join(c.workspace, ".stackkit", "access.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "read access manifest: " + err.Error()}
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return &Error{ReasonCode: ReasonRuntimeUnavailable, Message: "decode access manifest: " + err.Error()}
	}
	services, _ := manifest["services"].([]any)
	for _, item := range services {
		service, _ := item.(map[string]any)
		if normalizeServiceKey(fmt.Sprint(service["key"])) != serviceKey {
			continue
		}
		service["desiredState"] = desiredState
		service["allowedActions"] = AllowedActions(serviceKey)
		service["evidenceRef"] = evidenceRef
	}
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	return writePrivateAtomic(c.workspace, filepath.ToSlash(filepath.Join(".stackkit", "access.json")), updated)
}

func (c *Controller) Logs(ctx context.Context, serviceKey string, tail int, cursor string) (LogsResult, error) {
	result := LogsResult{APIVersion: LogsAPIVersion, ServiceKey: normalizeServiceKey(serviceKey), Entries: []LogEntry{}}
	definition, ok := serviceDefinitions[result.ServiceKey]
	if !ok {
		return result, &Error{ReasonCode: ReasonUnknownService, Message: "unknown managed service " + result.ServiceKey}
	}
	if tail < 1 || tail > 200 {
		return result, &Error{ReasonCode: "invalid_log_limit", Message: "service log tail must be between 1 and 200"}
	}
	if cursor != "" && !cursorPattern.MatchString(cursor) {
		return result, &Error{ReasonCode: "invalid_log_cursor", Message: "service log cursor is invalid"}
	}
	authority, err := c.authority()
	if err != nil {
		return result, err
	}
	raw, err := c.runner.Run(ctx, authority.ComposePath, logsArgs(tail, definition.Components))
	if err != nil {
		return result, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service logs unavailable: " + logging.RedactText(err.Error())}
	}
	result.Entries, result.Truncated = parseLogs(raw, tail, c.now().UTC())
	if len(result.Entries) > 0 {
		payload, _ := json.Marshal(result.Entries[len(result.Entries)-1])
		sum := sha256.Sum256(payload)
		result.NextCursor = "sha256:" + hex.EncodeToString(sum[:])
	}
	result.EvidenceRef, err = c.persistEvidence(result)
	return result, err
}

// ReconcileAfterApply reapplies durable stopped intent after Compose Apply.
// Any plan or service-contract change invalidates the state fail-closed.
func (c *Controller) ReconcileAfterApply(ctx context.Context) error {
	if _, err := os.Lstat(filepath.Join(c.workspace, filepath.FromSlash(stateRelativePath))); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	authority, err := c.authority()
	if err != nil {
		return err
	}
	state, exists, err := c.loadStateIfExists(authority)
	if err != nil || !exists {
		return err
	}
	for serviceKey, desired := range state.Services {
		if desired.State != DesiredStopped {
			continue
		}
		definition, ok := serviceDefinitions[serviceKey]
		if !ok || !definition.CanStop {
			return &Error{ReasonCode: ReasonContractChanged, Message: "persisted desired state references a service that is no longer stoppable"}
		}
		if _, err := c.runner.Run(ctx, authority.ComposePath, mutationArgs(ActionStop, definition.Components)); err != nil {
			return &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "reconcile desired service state: " + logging.RedactText(err.Error())}
		}
	}
	return nil
}

type runtimeAuthority struct {
	PlanHash    string
	ComposePath string
}

func (c *Controller) authority() (runtimeAuthority, error) {
	planPath := filepath.Join(c.workspace, filepath.FromSlash(defaultPlanPath))
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_unavailable", Message: "read canonical ResolvedPlan: " + err.Error()}
	}
	var plan struct {
		PlanHash string `json:"planHash"`
	}
	if json.Unmarshal(raw, &plan) != nil || !cursorPattern.MatchString(plan.PlanHash) {
		return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: "canonical ResolvedPlan has no valid planHash"}
	}
	composePath := filepath.Join(c.workspace, filepath.FromSlash(defaultCompose))
	info, err := os.Lstat(composePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return runtimeAuthority{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "Cloud core Compose runtime is unavailable"}
	}
	return runtimeAuthority{PlanHash: plan.PlanHash, ComposePath: filepath.Clean(composePath)}, nil
}

func (c *Controller) loadState(authority runtimeAuthority) (DesiredState, error) {
	state, exists, err := c.loadStateIfExists(authority)
	if err != nil {
		return DesiredState{}, err
	}
	if exists {
		return state, nil
	}
	ownerRef, err := c.signer.OwnerRef(c.workspace)
	if err != nil {
		return DesiredState{}, &Error{ReasonCode: "owner_custody_unavailable", Message: err.Error()}
	}
	return DesiredState{APIVersion: StateAPIVersion, OwnerRef: ownerRef, PlanHash: authority.PlanHash, ServiceContractHash: ServiceContractHash(), Services: map[string]DesiredService{}}, nil
}

func (c *Controller) loadStateIfExists(authority runtimeAuthority) (DesiredState, bool, error) {
	path := filepath.Join(c.workspace, filepath.FromSlash(stateRelativePath))
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DesiredState{}, false, nil
	}
	if err != nil {
		return DesiredState{}, false, err
	}
	var state DesiredState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || state.APIVersion != StateAPIVersion || state.Services == nil {
		return DesiredState{}, false, &Error{ReasonCode: "desired_state_invalid", Message: "desired service state is malformed"}
	}
	canonical, err := stateSigningBytes(state)
	if err != nil || c.signer.Verify(c.workspace, canonical, state.Signature) != nil {
		return DesiredState{}, false, &Error{ReasonCode: "desired_state_signature_invalid", Message: "desired service state signature does not verify"}
	}
	if state.PlanHash != authority.PlanHash {
		return DesiredState{}, false, &Error{ReasonCode: ReasonPlanChanged, Message: "desired service state was invalidated by a plan change"}
	}
	if state.ServiceContractHash != ServiceContractHash() {
		return DesiredState{}, false, &Error{ReasonCode: ReasonContractChanged, Message: "desired service state was invalidated by a service-contract change"}
	}
	return state, true, nil
}

func (c *Controller) persistState(state *DesiredState) error {
	canonical, err := stateSigningBytes(*state)
	if err != nil {
		return err
	}
	state.Signature, err = c.signer.Sign(c.workspace, canonical)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateAtomic(c.workspace, stateRelativePath, append(payload, '\n'))
}

func (c *Controller) persistEvidence(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	relative := filepath.ToSlash(filepath.Join(evidenceRoot, digest+".json"))
	if err := writePrivateAtomic(c.workspace, relative, append(payload, '\n')); err != nil {
		return "", err
	}
	return "stackkit-evidence://service-control/sha256:" + digest, nil
}

func stateSigningBytes(state DesiredState) ([]byte, error) {
	state.Signature = localevidence.OwnerPolicyStateSignature{}
	return json.Marshal(state)
}

func writePrivateAtomic(workspace, relative string, payload []byte) error {
	directory := filepath.Dir(filepath.Join(workspace, filepath.FromSlash(relative)))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(filepath.ToSlash(relative), payload)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("service control evidence was not durably installed")
	}
	return root.VerifyPathIdentity()
}

func (c *Controller) observe(ctx context.Context, composePath string, components []string) (string, error) {
	raw, err := c.runner.Run(ctx, composePath, append([]string{"ps", "--all", "--format", "json"}, components...))
	if err != nil {
		return "unknown", &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service observation failed: " + logging.RedactText(err.Error())}
	}
	lower := strings.ToLower(string(raw))
	if strings.Contains(lower, "running") || strings.Contains(lower, "up") {
		return DesiredRunning, nil
	}
	if strings.Contains(lower, "exited") || strings.Contains(lower, "stopped") || strings.Contains(lower, "created") {
		return DesiredStopped, nil
	}
	return "unknown", nil
}

func mutationArgs(action string, components []string) []string {
	switch action {
	case ActionStart:
		return append([]string{"start"}, components...)
	case ActionStop:
		return append([]string{"stop", "--timeout", "60"}, components...)
	default:
		return append([]string{"restart", "--timeout", "60"}, components...)
	}
}

func logsArgs(tail int, components []string) []string {
	return append([]string{"logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(tail)}, components...)
}

func normalizeServiceKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func parseLogs(raw []byte, limit int, fallback time.Time) ([]LogEntry, bool) {
	entries := make([]LogEntry, 0, limit)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 256*1024)
	truncated := false
	for scanner.Scan() {
		line := logging.RedactText(scanner.Text())
		if len(line) > 4096 {
			line = line[:4096]
			truncated = true
		}
		timestamp := fallback
		fields := strings.Fields(line)
		for _, field := range fields {
			if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(field)); err == nil {
				timestamp = parsed.UTC()
				break
			}
		}
		entries = append(entries, LogEntry{Timestamp: timestamp, Message: line})
		if len(entries) == limit {
			if scanner.Scan() {
				truncated = true
			}
			break
		}
	}
	return entries, truncated
}

type ownerSigner struct{}

func (ownerSigner) OwnerRef(workspace string) (string, error) {
	owner, err := localevidence.LoadOwnerCustody(workspace)
	return owner.OwnerRef, err
}
func (ownerSigner) Sign(workspace string, payload []byte) (localevidence.OwnerPolicyStateSignature, error) {
	return localevidence.SignOwnerPolicyState(workspace, payload)
}
func (ownerSigner) Verify(workspace string, payload []byte, signature localevidence.OwnerPolicyStateSignature) error {
	return localevidence.VerifyOwnerPolicyState(workspace, payload, signature)
}

type osRunner struct{}

func (osRunner) Run(ctx context.Context, composePath string, args []string) ([]byte, error) {
	if ctx == nil || filepath.Base(composePath) != "compose.yaml" || len(args) == 0 || !allowedArgs(args) {
		return nil, errors.New("service control rejected an unbounded command")
	}
	prefix := []string{"compose", "--project-name", "stackkit-cloud-core", "-f", composePath}
	command := exec.CommandContext(ctx, "docker", append(prefix, args...)...) //nolint:gosec // finite arguments validated above
	command.Dir = filepath.Dir(composePath)
	workspace := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(composePath))))
	command.Env = []string{"LANG=C", "LC_ALL=C", "STACKKIT_CUSTODY_DIR=" + filepath.Join(workspace, ".stackkit", "custody")}
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker compose: %s", logging.RedactText(string(output)))
	}
	return output, nil
}

func allowedArgs(args []string) bool {
	if slices.Equal(args[:1], []string{ActionStart}) {
		return allowedComponents(args[1:])
	}
	if (args[0] == ActionStop || args[0] == ActionRestart) && len(args) >= 4 && slices.Equal(args[1:3], []string{"--timeout", "60"}) {
		return allowedComponents(args[3:])
	}
	if args[0] == "ps" && len(args) >= 5 && slices.Equal(args[1:4], []string{"--all", "--format", "json"}) {
		return allowedComponents(args[4:])
	}
	if args[0] == "logs" && len(args) >= 6 && slices.Equal(args[1:4], []string{"--no-color", "--timestamps", "--tail"}) {
		tail, err := strconv.Atoi(args[4])
		return err == nil && tail >= 1 && tail <= 200 && allowedComponents(args[5:])
	}
	return false
}

func allowedComponents(components []string) bool {
	if len(components) == 0 {
		return false
	}
	allowed := map[string]bool{}
	for _, definition := range serviceDefinitions {
		for _, component := range definition.Components {
			allowed[component] = true
		}
	}
	for _, component := range components {
		if !allowed[component] {
			return false
		}
	}
	return true
}
