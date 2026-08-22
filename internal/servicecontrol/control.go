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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/pkg/models"
	"gopkg.in/yaml.v3"
)

const (
	StateAPIVersion  = "stackkit.service-desired-state/v1"
	ResultAPIVersion = "stackkit.service-action-result/v1"
	LogsAPIVersion   = "stackkit.service-logs/v1"

	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"
	ActionLogs    = "logs"

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
)

var (
	cursorPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	contractIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
)

type serviceDefinition struct {
	Key            string   `json:"key"`
	ServiceRef     string   `json:"serviceRef"`
	Adapter        string   `json:"adapter"`
	RuntimeRef     string   `json:"runtimeRef"`
	ComponentRefs  []string `json:"componentRefs"`
	AllowedActions []string `json:"allowedActions"`
	Critical       bool     `json:"critical"`
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
	OperationRef  string  `json:"operationRef,omitempty"`
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

// runtimeCommandRequest is the fully bounded adapter invocation derived from
// one canonical ResolvedPlan service-control declaration.
type runtimeCommandRequest struct {
	Adapter       string
	RuntimeRef    string
	ComposePath   string
	ExternalID    string
	HTTPConfig    platformdeploy.HTTPConfig
	Args          []string
	ComponentRefs []string
}

type runtimeCommandOutput struct {
	Bytes         []byte
	ObservedState string
	OperationRef  string
}

type runner interface {
	Run(context.Context, runtimeCommandRequest) (runtimeCommandOutput, error)
}

type signer interface {
	OwnerRef(string) (string, error)
	Sign(string, []byte) (localevidence.OwnerPolicyStateSignature, error)
	Verify(string, []byte, localevidence.OwnerPolicyStateSignature) error
}

type Controller struct {
	workspace  string
	planPath   string
	runner     runner
	signer     signer
	now        func() time.Time
	verifyPlan func([]byte) error
}

func NewOSController(workspace, planPath string, plan generationartifact.VerifiedPlan) (*Controller, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("service control requires an absolute workspace")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("service control requires an existing plain workspace directory")
	}
	if !cursorPattern.MatchString(plan.Binding().PlanHash) {
		return nil, errors.New("service control requires a CUE-verified ResolvedPlan")
	}
	if !filepath.IsAbs(planPath) {
		planPath = filepath.Join(absolute, filepath.FromSlash(planPath))
	}
	planPath = filepath.Clean(planPath)
	relativePlan, err := filepath.Rel(absolute, planPath)
	if err != nil || relativePlan == ".." || strings.HasPrefix(relativePlan, ".."+string(filepath.Separator)) {
		return nil, errors.New("service control requires a workspace-confined ResolvedPlan")
	}
	return &Controller{
		workspace: filepath.Clean(absolute), planPath: planPath, runner: osRunner{}, signer: ownerSigner{}, now: time.Now,
		verifyPlan: plan.VerifyCurrentResolution,
	}, nil
}

func newController(workspace string, runner runner, signer signer, now func() time.Time, canonical []byte) *Controller {
	expected := append([]byte(nil), canonical...)
	return &Controller{workspace: workspace, planPath: filepath.Join(workspace, filepath.FromSlash(defaultPlanPath)), runner: runner, signer: signer, now: now, verifyPlan: func(candidate []byte) error {
		if !bytes.Equal(candidate, expected) {
			return errors.New("persisted plan differs from the verified plan")
		}
		return nil
	}}
}

func (c *Controller) Mutate(ctx context.Context, action, serviceKey string, ownerApproved bool) (Result, error) {
	result := Result{APIVersion: ResultAPIVersion, Action: strings.ToLower(strings.TrimSpace(action)), ServiceKey: normalizeServiceKey(serviceKey), Retryable: false}
	if ctx == nil || c == nil || c.runner == nil || c.signer == nil || c.now == nil || c.verifyPlan == nil {
		return result, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service runtime is not initialized"}
	}
	if !ownerApproved {
		return result, &Error{ReasonCode: ReasonOwnerApproval, Message: "service mutation requires explicit owner approval"}
	}
	if result.Action != ActionStart && result.Action != ActionStop && result.Action != ActionRestart {
		return result, &Error{ReasonCode: "unknown_action", Message: "unsupported service action " + result.Action}
	}
	authority, err := c.authority()
	if err != nil {
		return result, err
	}
	definition, ok := authority.Services[result.ServiceKey]
	if !ok {
		return result, &Error{ReasonCode: ReasonUnknownService, Message: "unknown managed service " + result.ServiceKey}
	}
	if !slices.Contains(definition.AllowedActions, result.Action) {
		if result.Action == ActionStop && definition.Critical {
			return result, &Error{ReasonCode: ReasonCriticalControlPlane, Message: "service stop denied: critical control-plane service"}
		}
		return result, &Error{ReasonCode: "unknown_action", Message: "service action is not declared by the ResolvedPlan"}
	}
	target, err := c.runtimeTarget(definition)
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
	args := mutationArgs(result.Action, definition.ComponentRefs)
	actionOutput, err := c.runner.Run(ctx, runtimeCommand(target, args, definition.ComponentRefs))
	if err != nil {
		return result, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service action failed: " + logging.RedactText(err.Error())}
	}
	observed := strings.TrimSpace(actionOutput.ObservedState)
	if target.Adapter == "compose" {
		observed, err = c.observe(ctx, target, definition.ComponentRefs)
		if err != nil {
			return result, err
		}
	} else if observed != DesiredRunning && observed != DesiredStopped {
		return result, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service adapter returned no bounded observed state"}
	}
	result.ObservedState = observed
	result.OperationRef = strings.TrimSpace(actionOutput.OperationRef)
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
	if err := c.updateAccessManifest(result.ServiceKey, definition.AllowedActions, result.DesiredState, result.EvidenceRef); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Controller) updateAccessManifest(serviceKey string, allowedActions []string, desiredState, evidenceRef string) error {
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
		service["allowedActions"] = append([]string(nil), allowedActions...)
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
	definition, ok := authority.Services[result.ServiceKey]
	if !ok {
		return result, &Error{ReasonCode: ReasonUnknownService, Message: "unknown managed service " + result.ServiceKey}
	}
	if !slices.Contains(definition.AllowedActions, ActionLogs) {
		return result, &Error{ReasonCode: "unknown_action", Message: "service logs are not declared by the ResolvedPlan"}
	}
	target, err := c.runtimeTarget(definition)
	if err != nil {
		return result, err
	}
	args := logsArgs(tail, definition.ComponentRefs)
	output, err := c.runner.Run(ctx, runtimeCommand(target, args, definition.ComponentRefs))
	if err != nil {
		return result, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service logs unavailable: " + logging.RedactText(err.Error())}
	}
	result.Entries, result.Truncated = parseLogs(output.Bytes, tail, c.now().UTC())
	if len(result.Entries) > 0 {
		payload, _ := json.Marshal(result.Entries[len(result.Entries)-1])
		sum := sha256.Sum256(payload)
		result.NextCursor = "sha256:" + hex.EncodeToString(sum[:])
	}
	result.EvidenceRef, err = c.persistEvidence(result)
	return result, err
}

// ReconcileAfterApply reapplies durable stopped intent through the declared adapter.
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
		definition, ok := authority.Services[serviceKey]
		if !ok || definition.Critical || !slices.Contains(definition.AllowedActions, ActionStop) {
			return &Error{ReasonCode: ReasonContractChanged, Message: "persisted desired state references a service that is no longer stoppable"}
		}
		target, err := c.runtimeTarget(definition)
		if err != nil {
			return err
		}
		args := mutationArgs(ActionStop, definition.ComponentRefs)
		if _, err := c.runner.Run(ctx, runtimeCommand(target, args, definition.ComponentRefs)); err != nil {
			return &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "reconcile desired service state: " + logging.RedactText(err.Error())}
		}
	}
	return nil
}

type runtimeAuthority struct {
	PlanHash            string
	ServiceContractHash string
	Services            map[string]serviceDefinition
}

type runtimeTarget struct {
	Adapter     string
	RuntimeRef  string
	ComposePath string
	ExternalID  string
	HTTPConfig  platformdeploy.HTTPConfig
}

func (c *Controller) authority() (runtimeAuthority, error) {
	info, err := os.Lstat(c.planPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_unavailable", Message: "canonical ResolvedPlan is not a regular owner-custodied file"}
	}
	raw, err := os.ReadFile(c.planPath)
	if err != nil {
		return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_unavailable", Message: "read canonical ResolvedPlan: " + err.Error()}
	}
	if _, err := resolvedplan.DecodeCanonicalPlan(raw); err != nil {
		return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: "verify canonical ResolvedPlan: " + err.Error()}
	}
	if err := c.verifyPlan(raw); err != nil {
		return runtimeAuthority{}, &Error{ReasonCode: ReasonPlanChanged, Message: "canonical ResolvedPlan differs from the CUE-verified execution authority"}
	}
	var plan struct {
		PlanHash string `json:"planHash"`
		Modules  []struct {
			ServiceControls []serviceDefinition `json:"serviceControls"`
		} `json:"modules"`
	}
	if json.Unmarshal(raw, &plan) != nil || !cursorPattern.MatchString(plan.PlanHash) {
		return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: "canonical ResolvedPlan has no valid planHash"}
	}
	definitions := make([]serviceDefinition, 0)
	services := map[string]serviceDefinition{}
	serviceRefs := map[string]struct{}{}
	componentRefs := map[string]struct{}{}
	for _, module := range plan.Modules {
		for _, candidate := range module.ServiceControls {
			definition, err := normalizeServiceDefinition(candidate)
			if err != nil {
				return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: err.Error()}
			}
			if _, duplicate := services[definition.Key]; duplicate {
				return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: "canonical ResolvedPlan controls a service more than once"}
			}
			if _, duplicate := serviceRefs[definition.ServiceRef]; duplicate {
				return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: "canonical ResolvedPlan controls a service endpoint more than once"}
			}
			for _, componentRef := range definition.ComponentRefs {
				componentIdentity := definition.RuntimeRef + "\x00" + componentRef
				if _, duplicate := componentRefs[componentIdentity]; duplicate {
					return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: "canonical ResolvedPlan assigns a runtime component more than once"}
				}
				componentRefs[componentIdentity] = struct{}{}
			}
			services[definition.Key] = definition
			serviceRefs[definition.ServiceRef] = struct{}{}
			definitions = append(definitions, definition)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Key < definitions[j].Key })
	hash, err := serviceContractHash(definitions)
	if err != nil {
		return runtimeAuthority{}, &Error{ReasonCode: "resolved_plan_invalid", Message: "hash service controls: " + err.Error()}
	}
	return runtimeAuthority{PlanHash: plan.PlanHash, ServiceContractHash: hash, Services: services}, nil
}

func normalizeServiceDefinition(candidate serviceDefinition) (serviceDefinition, error) {
	rawKey, rawServiceRef, rawAdapter, rawRuntimeRef := candidate.Key, candidate.ServiceRef, candidate.Adapter, candidate.RuntimeRef
	candidate.Key = strings.TrimSpace(candidate.Key)
	candidate.ServiceRef = strings.TrimSpace(candidate.ServiceRef)
	candidate.Adapter = strings.TrimSpace(candidate.Adapter)
	candidate.RuntimeRef = strings.TrimSpace(candidate.RuntimeRef)
	if candidate.Key != rawKey || candidate.Key != normalizeServiceKey(candidate.Key) || candidate.ServiceRef != rawServiceRef ||
		candidate.Adapter != rawAdapter || candidate.RuntimeRef != rawRuntimeRef {
		return serviceDefinition{}, errors.New("canonical ResolvedPlan has a non-canonical service-control identity")
	}
	if !contractIDPattern.MatchString(candidate.Key) || !contractIDPattern.MatchString(candidate.ServiceRef) || !contractIDPattern.MatchString(candidate.RuntimeRef) {
		return serviceDefinition{}, errors.New("canonical ResolvedPlan has an invalid service-control identity")
	}
	if candidate.Adapter != "compose" && candidate.Adapter != "komodo" {
		return serviceDefinition{}, errors.New("canonical ResolvedPlan has an unsupported service-control adapter")
	}
	componentRefs, err := normalizedContractIDs(candidate.ComponentRefs)
	if err != nil {
		return serviceDefinition{}, fmt.Errorf("canonical ResolvedPlan service %s components: %w", candidate.Key, err)
	}
	actions, err := normalizedActions(candidate.AllowedActions)
	if err != nil {
		return serviceDefinition{}, fmt.Errorf("canonical ResolvedPlan service %s actions: %w", candidate.Key, err)
	}
	if candidate.Critical && slices.Contains(actions, ActionStop) {
		return serviceDefinition{}, fmt.Errorf("canonical ResolvedPlan critical service %s allows stop", candidate.Key)
	}
	candidate.ComponentRefs, candidate.AllowedActions = componentRefs, actions
	return candidate, nil
}

func normalizedContractIDs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("must not be empty")
	}
	result := append([]string(nil), values...)
	for index := range result {
		trimmed := strings.TrimSpace(result[index])
		if trimmed != result[index] {
			return nil, errors.New("contains a non-canonical component reference")
		}
		result[index] = trimmed
		if !contractIDPattern.MatchString(result[index]) {
			return nil, errors.New("contains an invalid component reference")
		}
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, errors.New("contains a duplicate component reference")
		}
	}
	return result, nil
}

func normalizedActions(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("must not be empty")
	}
	allowed := map[string]struct{}{ActionStart: {}, ActionStop: {}, ActionRestart: {}, ActionLogs: {}}
	result := append([]string(nil), values...)
	for index := range result {
		trimmed := strings.TrimSpace(result[index])
		if trimmed != result[index] {
			return nil, errors.New("contains a non-canonical action")
		}
		result[index] = trimmed
		if _, ok := allowed[result[index]]; !ok {
			return nil, errors.New("contains an unsupported action")
		}
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, errors.New("contains a duplicate action")
		}
	}
	return result, nil
}

func serviceContractHash(definitions []serviceDefinition) (string, error) {
	payload, err := json.Marshal(struct {
		APIVersion string              `json:"apiVersion"`
		Services   []serviceDefinition `json:"services"`
	}{APIVersion: "stackkit.service-control/v2", Services: definitions})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c *Controller) runtimeTarget(definition serviceDefinition) (runtimeTarget, error) {
	if definition.Adapter == "compose" {
		path := filepath.Join(c.workspace, ".stackkit", "runtime", definition.RuntimeRef, "compose.yaml")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return runtimeTarget{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "declared Compose runtime is unavailable"}
		}
		return runtimeTarget{Adapter: definition.Adapter, RuntimeRef: definition.RuntimeRef, ComposePath: filepath.Clean(path)}, nil
	}
	cfg, err := platformdeploy.LoadOwnerKomodoConfig(c.workspace)
	if err != nil {
		return runtimeTarget{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "owner-custodied Komodo configuration is unavailable: " + err.Error()}
	}
	ref, err := loadKomodoDeploymentRef(c.workspace, definition.RuntimeRef)
	if err != nil {
		return runtimeTarget{}, err
	}
	return runtimeTarget{Adapter: definition.Adapter, RuntimeRef: definition.RuntimeRef, ExternalID: ref.ExternalID, HTTPConfig: cfg}, nil
}

func loadKomodoDeploymentRef(workspace, runtimeRef string) (platformdeploy.DeploymentRef, error) {
	payload, err := readOwnerFile(workspace, ".stackkit/state.yaml")
	if err != nil {
		return platformdeploy.DeploymentRef{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "owner-custodied deployment state is unavailable: " + err.Error()}
	}
	var state models.DeploymentState
	if err := yaml.Unmarshal(payload, &state); err != nil {
		return platformdeploy.DeploymentRef{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Message: "decode owner-custodied deployment state: " + err.Error()}
	}
	matches := make([]models.PlatformAppState, 0, 1)
	apps := append(append([]models.PlatformAppState(nil), state.PlatformSystemApps...), state.PlatformApps...)
	for _, app := range apps {
		if app.Name == runtimeRef && app.Platform == "komodo" && app.Management == platformdeploy.AppManagementManaged && app.ExternalID != "" && app.ExternalID == strings.TrimSpace(app.ExternalID) {
			matches = append(matches, app)
		}
	}
	if len(matches) != 1 {
		return platformdeploy.DeploymentRef{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "declared Komodo runtime has no unique managed deployment identity"}
	}
	return platformdeploy.DeploymentRef{Platform: "komodo", AppName: matches[0].Name, ExternalID: matches[0].ExternalID}, nil
}

func readOwnerFile(workspace, relative string) ([]byte, error) {
	const maxOwnerFileBytes = 4 << 20
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return nil, err
	}
	file, err := view.Open(relative)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("owner-custodied file is not private")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxOwnerFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxOwnerFileBytes {
		return nil, errors.New("owner-custodied file exceeds size limit")
	}
	return payload, nil
}

func runtimeCommand(target runtimeTarget, args, componentRefs []string) runtimeCommandRequest {
	return runtimeCommandRequest{Adapter: target.Adapter, RuntimeRef: target.RuntimeRef, ComposePath: target.ComposePath, ExternalID: target.ExternalID, HTTPConfig: target.HTTPConfig, Args: args, ComponentRefs: append([]string(nil), componentRefs...)}
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
	return DesiredState{APIVersion: StateAPIVersion, OwnerRef: ownerRef, PlanHash: authority.PlanHash, ServiceContractHash: authority.ServiceContractHash, Services: map[string]DesiredService{}}, nil
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
	if state.ServiceContractHash != authority.ServiceContractHash {
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

func (c *Controller) observe(ctx context.Context, target runtimeTarget, components []string) (string, error) {
	args := append([]string{"ps", "--all", "--format", "json"}, components...)
	output, err := c.runner.Run(ctx, runtimeCommand(target, args, components))
	if err != nil {
		return "unknown", &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "service observation failed: " + logging.RedactText(err.Error())}
	}
	lower := strings.ToLower(string(output.Bytes))
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

func (osRunner) Run(ctx context.Context, request runtimeCommandRequest) (runtimeCommandOutput, error) {
	if ctx == nil || !contractIDPattern.MatchString(request.RuntimeRef) || len(request.Args) == 0 || !allowedArgs(request.Args, request.ComponentRefs) {
		return runtimeCommandOutput{}, errors.New("service control rejected an unbounded command")
	}
	if request.Adapter == "komodo" {
		adapter := platformdeploy.NewKomodoAdapter(request.HTTPConfig)
		ref := platformdeploy.DeploymentRef{Platform: "komodo", AppName: request.RuntimeRef, ExternalID: request.ExternalID}
		if request.Args[0] == ActionLogs {
			tail, _ := strconv.Atoi(request.Args[4])
			output, err := adapter.ReadStackLogs(ctx, ref, request.ComponentRefs, tail)
			return runtimeCommandOutput{Bytes: output}, err
		}
		result, err := adapter.ControlStack(ctx, ref, request.Args[0], request.ComponentRefs)
		if err != nil {
			return runtimeCommandOutput{}, err
		}
		return runtimeCommandOutput{ObservedState: result.ObservedState, OperationRef: result.UpdateID}, nil
	}
	if request.Adapter != "compose" || filepath.Base(request.ComposePath) != "compose.yaml" || filepath.Base(filepath.Dir(request.ComposePath)) != request.RuntimeRef {
		return runtimeCommandOutput{}, errors.New("service control rejected an unbounded runtime adapter")
	}
	prefix := []string{"compose", "--project-name", "stackkit-" + request.RuntimeRef, "-f", request.ComposePath}
	command := exec.CommandContext(ctx, "docker", append(prefix, request.Args...)...) //nolint:gosec // finite arguments validated above
	command.Dir = filepath.Dir(request.ComposePath)
	workspace := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(request.ComposePath))))
	command.Env = []string{"LANG=C", "LC_ALL=C", "STACKKIT_CUSTODY_DIR=" + filepath.Join(workspace, ".stackkit", "custody")}
	output, err := command.CombinedOutput()
	if err != nil {
		return runtimeCommandOutput{}, fmt.Errorf("docker compose: %s", logging.RedactText(string(output)))
	}
	return runtimeCommandOutput{Bytes: output}, nil
}

func allowedArgs(args, components []string) bool {
	if slices.Equal(args[:1], []string{ActionStart}) {
		return allowedComponents(args[1:], components)
	}
	if (args[0] == ActionStop || args[0] == ActionRestart) && len(args) >= 4 && slices.Equal(args[1:3], []string{"--timeout", "60"}) {
		return allowedComponents(args[3:], components)
	}
	if args[0] == "ps" && len(args) >= 5 && slices.Equal(args[1:4], []string{"--all", "--format", "json"}) {
		return allowedComponents(args[4:], components)
	}
	if args[0] == ActionLogs && len(args) >= 6 && slices.Equal(args[1:4], []string{"--no-color", "--timestamps", "--tail"}) {
		tail, err := strconv.Atoi(args[4])
		return err == nil && tail >= 1 && tail <= 200 && allowedComponents(args[5:], components)
	}
	return false
}

func allowedComponents(requested, declared []string) bool {
	if len(requested) == 0 || len(declared) == 0 {
		return false
	}
	return slices.Equal(requested, declared)
}
