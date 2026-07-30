// Package runtimeexecutorprocess implements the account-free, digest-pinned
// standard execution-channel process boundary. It owns no provider, endpoint,
// credential, or transport lifecycle.
package runtimeexecutorprocess

import (
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
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

const (
	requestSchema         = "stackkit.standard-execution-request/v1"
	responseSchema        = "stackkit.standard-execution-result/v1"
	removalRequestSchema  = "stackkit.standard-workload-removal-request/v1"
	removalResponseSchema = "stackkit.standard-workload-removal-result/v1"
	maxExecutable         = 128 << 20
	maxPayload            = 16 << 20
	maxOutput             = 16 << 20
)

type Binding struct {
	ChannelRef       string
	SiteRef          string
	NodeRef          string
	Executable       string
	ExecutableSHA256 string
}

type Executor struct {
	identity runtimeexecutor.ExecutorIdentity
	binding  Binding
}

type requestEnvelope struct {
	SchemaVersion string                           `json:"schemaVersion"`
	ChannelRef    string                           `json:"channelRef"`
	Request       runtimeexecutor.ExecutionRequest `json:"request"`
}

type responseEnvelope struct {
	SchemaVersion string                           `json:"schemaVersion"`
	ChannelRef    string                           `json:"channelRef"`
	Outcome       runtimeexecutor.ExecutionOutcome `json:"outcome"`
}

type removalRequestEnvelope struct {
	SchemaVersion string                  `json:"schemaVersion"`
	ChannelRef    string                  `json:"channelRef"`
	Request       workloadremoval.Request `json:"request"`
}

type removalResponseEnvelope struct {
	SchemaVersion string                 `json:"schemaVersion"`
	ChannelRef    string                 `json:"channelRef"`
	Result        workloadremoval.Result `json:"result"`
}

func ValidateBinding(binding Binding) error {
	if strings.TrimSpace(binding.ChannelRef) == "" ||
		strings.TrimSpace(binding.SiteRef) == "" ||
		strings.TrimSpace(binding.NodeRef) == "" {
		return errors.New("standard process binding requires exact channel, Site, and node refs")
	}
	if binding.ChannelRef != strings.TrimSpace(binding.ChannelRef) ||
		binding.SiteRef != strings.TrimSpace(binding.SiteRef) ||
		binding.NodeRef != strings.TrimSpace(binding.NodeRef) {
		return errors.New("standard process binding refs must be canonical")
	}
	if !filepath.IsAbs(binding.Executable) || filepath.Clean(binding.Executable) != binding.Executable {
		return errors.New("standard process executable must be an exact clean absolute path")
	}
	if !validDigest(binding.ExecutableSHA256) {
		return errors.New("standard process executable requires an exact sha256 digest")
	}
	if _, err := loadExecutable(binding); err != nil {
		return fmt.Errorf("verify standard process executable: %w", err)
	}
	return nil
}

func New(runtimeVersion string, binding Binding) (*Executor, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) {
		return nil, errors.New("standard process executor requires a canonical runtime version")
	}
	if err := ValidateBinding(binding); err != nil {
		return nil, err
	}
	identity := runtimeexecutor.ExecutorIdentity{
		ID: "stackkits-standard-process-channel", Version: runtimeVersion,
		Digest: binding.ExecutableSHA256,
	}
	if err := generationartifact.ValidateApplyExecutorIdentity(generationartifact.ApplyExecutorIdentity{
		ID: identity.ID, Version: identity.Version, Digest: identity.Digest,
	}); err != nil {
		return nil, fmt.Errorf("standard process identity is invalid: %w", err)
	}
	return &Executor{identity: identity, binding: binding}, nil
}

func (e *Executor) Identity() runtimeexecutor.ExecutorIdentity {
	if e == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return e.identity
}

func (e *Executor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("standard process execution requires a context")
	}
	if e == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("standard process executor is not initialized")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate standard process request: %w", err)
	}
	if err := validateScope(e.binding, request); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	executable, err := loadExecutable(e.binding)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("standard process executable changed before invocation: %w", err)
	}
	tempRoot, err := os.MkdirTemp("", "stackkit-standard-channel-")
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("create private standard process directory: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	executableName := "channel-executor"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	privateExecutable := filepath.Join(tempRoot, executableName)
	if err := os.WriteFile(privateExecutable, executable, 0o700); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("materialize verified standard process executable: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(privateExecutable, 0o700); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("make verified standard process executable private: %w", err)
		}
	}
	payload, err := json.Marshal(requestEnvelope{
		SchemaVersion: requestSchema, ChannelRef: e.binding.ChannelRef, Request: request,
	})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("encode standard process request: %w", err)
	}
	if len(payload) > maxPayload {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("standard process request exceeds the closed payload limit")
	}

	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = maxOutput, maxOutput
	command := exec.CommandContext(ctx, privateExecutable) //nolint:gosec // private copy of exact digest-verified bytes
	command.Env = []string{}
	command.Stdin = bytes.NewReader(payload)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf(
			"standard process channel %q failed: %w",
			e.binding.ChannelRef, err,
		)
	}
	if stdout.exceeded || stderr.exceeded {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("standard process output exceeded the closed limit")
	}

	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var response responseEnvelope
	if err := decoder.Decode(&response); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("decode standard process result: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("standard process result contains trailing JSON")
	}
	if response.SchemaVersion != responseSchema || response.ChannelRef != e.binding.ChannelRef {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("standard process result is not bound to the admitted channel")
	}
	if err := validateOutcome(request, response.Outcome); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	return response.Outcome, nil
}

// RemoveWorkload invokes the same digest-pinned Standard execution channel
// with a distinct closed protocol. It never reinterprets Apply as deletion and
// accepts only a fresh Owner-signed request bound to one previously applied
// workload.
func (e *Executor) RemoveWorkload(ctx context.Context, request workloadremoval.Request) (workloadremoval.Result, error) {
	if ctx == nil {
		return workloadremoval.Result{}, errors.New("standard process workload removal requires a context")
	}
	if e == nil {
		return workloadremoval.Result{}, errors.New("standard process executor is not initialized")
	}
	now := time.Now().UTC()
	if err := request.ValidateAt(now); err != nil {
		return workloadremoval.Result{}, fmt.Errorf("validate standard process workload removal: %w", err)
	}
	if err := validateScope(e.binding, request.Applied); err != nil {
		return workloadremoval.Result{}, err
	}
	executable, err := loadExecutable(e.binding)
	if err != nil {
		return workloadremoval.Result{}, fmt.Errorf("standard process executable changed before workload removal: %w", err)
	}
	tempRoot, err := os.MkdirTemp("", "stackkit-standard-removal-")
	if err != nil {
		return workloadremoval.Result{}, fmt.Errorf("create private standard workload-removal directory: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	executableName := "channel-executor"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	privateExecutable := filepath.Join(tempRoot, executableName)
	if err := os.WriteFile(privateExecutable, executable, 0o700); err != nil {
		return workloadremoval.Result{}, fmt.Errorf("materialize verified workload-removal executable: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(privateExecutable, 0o700); err != nil {
			return workloadremoval.Result{}, fmt.Errorf("make verified workload-removal executable private: %w", err)
		}
	}
	payload, err := json.Marshal(removalRequestEnvelope{
		SchemaVersion: removalRequestSchema, ChannelRef: e.binding.ChannelRef, Request: request,
	})
	if err != nil {
		return workloadremoval.Result{}, fmt.Errorf("encode standard workload-removal request: %w", err)
	}
	if len(payload) > maxPayload {
		return workloadremoval.Result{}, errors.New("standard workload-removal request exceeds the closed payload limit")
	}
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = maxOutput, maxOutput
	command := exec.CommandContext(ctx, privateExecutable) //nolint:gosec // private copy of exact digest-verified bytes
	command.Env = []string{}
	command.Stdin = bytes.NewReader(payload)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return workloadremoval.Result{}, fmt.Errorf("standard process channel %q workload removal failed: %w", e.binding.ChannelRef, err)
	}
	if stdout.exceeded || stderr.exceeded {
		return workloadremoval.Result{}, errors.New("standard workload-removal output exceeded the closed limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var response removalResponseEnvelope
	if err := decoder.Decode(&response); err != nil {
		return workloadremoval.Result{}, fmt.Errorf("decode standard workload-removal result: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return workloadremoval.Result{}, errors.New("standard workload-removal result contains trailing JSON")
	}
	if response.SchemaVersion != removalResponseSchema || response.ChannelRef != e.binding.ChannelRef {
		return workloadremoval.Result{}, errors.New("standard workload-removal result is not bound to the admitted channel")
	}
	if err := response.Result.Validate(request); err != nil {
		return workloadremoval.Result{}, fmt.Errorf("validate standard workload-removal result: %w", err)
	}
	return response.Result, nil
}

func validateScope(binding Binding, request runtimeexecutor.ExecutionRequest) error {
	if len(request.RuntimeTargets) == 0 {
		return errors.New("standard process request contains no runtime targets")
	}
	for _, target := range request.RuntimeTargets {
		if target.ExecutionChannelRef != binding.ChannelRef ||
			len(target.SiteRefs) != 1 || target.SiteRefs[0] != binding.SiteRef ||
			len(target.NodeRefs) != 1 || target.NodeRefs[0] != binding.NodeRef {
			return fmt.Errorf("runtime target %q escaped the admitted process channel scope", target.RequirementID)
		}
	}
	for _, target := range request.HealthTargets {
		if len(target.SiteRefs) != 1 || target.SiteRefs[0] != binding.SiteRef ||
			len(target.NodeRefs) != 1 || target.NodeRefs[0] != binding.NodeRef {
			return fmt.Errorf("health target %q escaped the admitted process channel scope", target.RequirementID)
		}
	}
	return nil
}

func validateOutcome(request runtimeexecutor.ExecutionRequest, outcome runtimeexecutor.ExecutionOutcome) error {
	if len(outcome.Runtime) != len(request.RuntimeTargets) ||
		len(outcome.Health) != len(request.HealthTargets) {
		return errors.New("standard process result does not cover the exact target set")
	}
	runtimeTargets := make(map[string]runtimeexecutor.RuntimeTarget, len(request.RuntimeTargets))
	for _, target := range request.RuntimeTargets {
		if _, duplicate := runtimeTargets[target.RequirementID]; duplicate {
			return errors.New("standard process request contains duplicate runtime requirements")
		}
		runtimeTargets[target.RequirementID] = target
	}
	seenRuntime := make(map[string]struct{}, len(outcome.Runtime))
	for _, result := range outcome.Runtime {
		target, exists := runtimeTargets[result.RequirementID]
		if !exists || target.InstanceRef != result.InstanceRef ||
			result.Status != runtimeexecutor.RuntimeStatusApplied ||
			strings.TrimSpace(result.ObservationRef) == "" ||
			!validDigest(result.ObservationDigest) {
			return fmt.Errorf("standard process returned an invalid runtime outcome for %q", result.RequirementID)
		}
		if _, duplicate := seenRuntime[result.RequirementID]; duplicate {
			return errors.New("standard process returned duplicate runtime outcomes")
		}
		seenRuntime[result.RequirementID] = struct{}{}
	}
	healthTargets := make(map[string]runtimeexecutor.HealthTarget, len(request.HealthTargets))
	for _, target := range request.HealthTargets {
		if _, duplicate := healthTargets[target.RequirementID]; duplicate {
			return errors.New("standard process request contains duplicate health requirements")
		}
		healthTargets[target.RequirementID] = target
	}
	seenHealth := make(map[string]struct{}, len(outcome.Health))
	for _, result := range outcome.Health {
		target, exists := healthTargets[result.RequirementID]
		if !exists || target.TargetRef != result.TargetRef ||
			result.Status != runtimeexecutor.HealthStatusHealthy ||
			strings.TrimSpace(result.ObservationRef) == "" ||
			!validDigest(result.ObservationDigest) {
			return fmt.Errorf("standard process returned an invalid health outcome for %q", result.RequirementID)
		}
		if _, duplicate := seenHealth[result.RequirementID]; duplicate {
			return errors.New("standard process returned duplicate health outcomes")
		}
		seenHealth[result.RequirementID] = struct{}{}
	}
	return nil
}

func loadExecutable(binding Binding) ([]byte, error) {
	info, err := os.Lstat(binding.Executable)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxExecutable {
		return nil, errors.New("executable must be a non-empty plain regular file no larger than 128 MiB")
	}
	file, err := os.Open(binding.Executable) //nolint:gosec // exact absolute owner-configured path
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	executable, err := io.ReadAll(io.LimitReader(file, maxExecutable+1))
	if err != nil {
		return nil, err
	}
	if len(executable) == 0 || len(executable) > maxExecutable {
		return nil, errors.New("executable changed outside the closed size limit while reading")
	}
	digest := sha256.Sum256(executable)
	actual := "sha256:" + hex.EncodeToString(digest[:])
	if actual != binding.ExecutableSHA256 {
		return nil, errors.New("executable digest does not match Inventory")
	}
	return executable, nil
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.exceeded {
		return len(data), nil
	}
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.exceeded = true
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

var _ runtimeexecutor.Executor = (*Executor)(nil)
