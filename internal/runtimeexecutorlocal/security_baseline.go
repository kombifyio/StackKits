package runtimeexecutorlocal

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
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/kombifyio/stackkits/internal/securitybaseline"
)

const (
	securityBaselineModuleRef = "security-baseline"
	securityBaselineUnitRef   = "host-policy"
	securityBaselineOutputRef = "foundation/security-baseline/apply.sh"
	securityBaselineShell     = "/bin/sh"
	maxCommandOutputBytes     = 64 << 10
	maxEvidenceBytes          = 64 << 10
)

// Command is the closed host-process capability used by the local adapter.
// The executor never accepts an executable, environment, directory, or
// argument from an ExecutionRequest.
type Command struct {
	Executable string
	Args       []string
	Dir        string
	Env        []string
	Stdin      []byte
}

// CommandRunner allows bounded process execution to be replaced in tests.
type CommandRunner interface {
	Run(context.Context, Command) error
}

// SecurityBaselineExecutor applies only the exact CUE-owned Architecture-v2
// Foundation host policy to the current local host. It has no provider,
// network, Docker, workspace, or credential authority.
type SecurityBaselineExecutor struct {
	identity runtimeexecutor.ExecutorIdentity
	runner   CommandRunner
	tempDir  func() (string, error)
}

// NewSecurityBaselineExecutor constructs the isolated local adapter. A nil
// runner selects the real bounded /bin/sh process runner.
func NewSecurityBaselineExecutor(identity runtimeexecutor.ExecutorIdentity, runner CommandRunner) *SecurityBaselineExecutor {
	if runner == nil {
		runner = osCommandRunner{}
	}
	return &SecurityBaselineExecutor{
		identity: identity,
		runner:   runner,
		tempDir: func() (string, error) {
			return os.MkdirTemp("", "stackkit-security-baseline-")
		},
	}
}

func (e *SecurityBaselineExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *SecurityBaselineExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("security-baseline executor requires a context")
	}
	if e == nil || e.runner == nil || e.tempDir == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("security-baseline executor is not initialized")
	}
	target, health, artifact, err := validateSecurityBaselineRequest(request)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	scratch, err := e.tempDir()
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("create isolated security-baseline scratch: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	command := Command{
		Executable: securityBaselineShell,
		Args:       []string{"-s"},
		Dir:        scratch,
		Env:        []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"},
		Stdin:      append([]byte(nil), artifact.Content...),
	}
	if err := e.runner.Run(ctx, command); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact security-baseline host policy: %w", err)
	}
	evidence, err := readSecurityBaselineEvidence(scratch)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:            runtimeexecutor.RuntimeStatusApplied,
			ObservationRef:    "runtime-observation://security-baseline/" + target.InstanceRef,
			ObservationDigest: digestString,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef,
			Status:            runtimeexecutor.HealthStatusHealthy,
			ObservationRef:    "health-observation://security-baseline/" + health.RequirementID,
			ObservationDigest: digestString,
		}},
	}, nil
}

func validateSecurityBaselineRequest(request runtimeexecutor.ExecutionRequest) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, runtimeexecutor.Artifact, error) {
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, runtimeexecutor.Artifact{}, errors.New("security-baseline executor requires exactly one runtime and one health target")
	}
	target := request.RuntimeTargets[0]
	if target.OwnerKind != "module" || target.OwnerRef != securityBaselineModuleRef || target.OwnerVersion != "" ||
		target.ModuleRef != securityBaselineModuleRef || target.UnitRef != securityBaselineUnitRef ||
		target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || len(target.ArtifactRefs) != 1 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, runtimeexecutor.Artifact{}, errors.New("runtime target is not the exact node-local security-baseline contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != "security-baseline-contract" || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != securityBaselineModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, runtimeexecutor.Artifact{}, errors.New("health target is not the exact security-baseline postcondition")
	}
	var artifact runtimeexecutor.Artifact
	found := 0
	for _, candidate := range request.Artifacts {
		if candidate.ID == target.ArtifactRefs[0] {
			artifact = candidate
			found++
		}
	}
	policy, err := securitybaseline.RenderV2HostPolicy()
	if err != nil {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, runtimeexecutor.Artifact{}, fmt.Errorf("render canonical security-baseline policy: %w", err)
	}
	contractHash := securitybaseline.ContractHash(policy)
	if found != 1 || artifact.Kind != "script" || artifact.Format != "shell" || artifact.Mode != "0700" ||
		artifact.OwnerKind != "render-instance" || artifact.ModuleRef != securityBaselineModuleRef || artifact.UnitRef != securityBaselineUnitRef ||
		artifact.OutputRef != securityBaselineOutputRef || target.UnitContractHash != contractHash || artifact.UnitContractHash != contractHash ||
		!bytes.Equal(artifact.Content, policy) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, runtimeexecutor.Artifact{}, errors.New("artifact is not the exact CUE-owned security-baseline host policy")
	}
	return target, health, artifact, nil
}

type securityBaselineEvidence struct {
	SchemaVersion string `json:"schemaVersion"`
	Status        string `json:"status"`
	Mode          string `json:"mode"`
	AppliedAt     string `json:"appliedAt"`
	Controls      struct {
		Firewall                  string `json:"firewall"`
		SSHPasswordAuthentication string `json:"sshPasswordAuthentication"`
		SSHRootLogin              string `json:"sshRootLogin"`
		SSHPort                   string `json:"sshPort"`
		Fail2ban                  string `json:"fail2ban"`
		UnattendedUpgrades        string `json:"unattendedUpgrades"`
		Sysctl                    string `json:"sysctl"`
	} `json:"controls"`
}

func readSecurityBaselineEvidence(scratch string) ([]byte, error) {
	path := filepath.Join(scratch, ".stackkit", "security-baseline.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxEvidenceBytes {
		return nil, errors.New("security-baseline evidence is missing, non-regular, or oversized")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read security-baseline evidence: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence securityBaselineEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return nil, errors.New("security-baseline evidence is not the closed v2 schema")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("security-baseline evidence has trailing data")
	}
	if evidence.SchemaVersion != securitybaseline.EvidenceSchemaVersionArchitectureV2 || evidence.Status != "pass" ||
		evidence.Mode != securitybaseline.EvidenceModeArchitectureV2 || evidence.Controls.Firewall != "delegated" ||
		evidence.Controls.SSHPasswordAuthentication != "delegated" || evidence.Controls.SSHRootLogin != "delegated" ||
		evidence.Controls.SSHPort != "delegated" || evidence.Controls.Fail2ban != "delegated" ||
		evidence.Controls.UnattendedUpgrades != "security" || evidence.Controls.Sysctl != "applied" {
		return nil, errors.New("security-baseline evidence does not prove the exact governed controls")
	}
	if _, err := time.Parse(time.RFC3339, evidence.AppliedAt); err != nil {
		return nil, errors.New("security-baseline evidence has invalid appliedAt")
	}
	return append([]byte(nil), data...), nil
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, command Command) error {
	if command.Executable != securityBaselineShell || !slices.Equal(command.Args, []string{"-s"}) {
		return errors.New("command is outside the closed security-baseline process contract")
	}
	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = append([]string(nil), command.Env...)
	cmd.Stdin = bytes.NewReader(command.Stdin)
	output := &boundedBuffer{remaining: maxCommandOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("security-baseline process failed: %w", err)
	}
	if output.exceeded {
		return errors.New("security-baseline process output exceeded the bound")
	}
	return nil
}

type boundedBuffer struct {
	remaining int
	exceeded  bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > b.remaining {
		value = value[:b.remaining]
		b.exceeded = true
	}
	b.remaining -= len(value)
	return original, nil
}
