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
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	coreHostBootstrapModuleRef = "stackkits-core-host-bootstrap"
	coreHostBootstrapUnitRef   = "host-policy"
	coreHostBootstrapOutputRef = "foundation/host-bootstrap/policy.json"
	coreHostBootstrapMaxBytes  = 64 << 10
)

// LocalTargetBinding binds the current-process adapter to one exact planned
// node. It is operator/runtime authority and can never be inferred from an
// artifact, a hostname, LAN discovery, or the first target in a plan.
type LocalTargetBinding struct {
	SiteRef             string
	NodeRef             string
	ExecutionChannelRef string
}

// RuntimeExpectation is the only runtime observation the bootstrap adapter
// can request from its host operations implementation.
type RuntimeExpectation struct {
	Runtime  string
	Engine   string
	DataRoot string
}

// RuntimeObservation is bounded evidence for an already present runtime.
type RuntimeObservation struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// CoreHostBootstrapOperations is a closed host capability. It deliberately
// has no generic command, package-manager, network, provider, or file-write
// method.
type CoreHostBootstrapOperations interface {
	EnsureDirectory(context.Context, string, fs.FileMode) error
	ObserveRuntime(context.Context, RuntimeExpectation) (RuntimeObservation, error)
}

// CoreHostBootstrapExecutor applies one exact node-local CUE policy to the
// host already bound by the caller. Multi-node dispatch belongs to a future
// execution-channel router, not this adapter.
type CoreHostBootstrapExecutor struct {
	identity runtimeexecutor.ExecutorIdentity
	binding  LocalTargetBinding
	host     CoreHostBootstrapOperations
}

func NewCoreHostBootstrapExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, host CoreHostBootstrapOperations) *CoreHostBootstrapExecutor {
	if host == nil {
		host = osCoreHostBootstrapOperations{}
	}
	return &CoreHostBootstrapExecutor{identity: identity, binding: binding, host: host}
}

func (e *CoreHostBootstrapExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *CoreHostBootstrapExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Core host-bootstrap executor requires a context")
	}
	if e == nil || e.host == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Core host-bootstrap executor requires one explicit local target binding")
	}
	target, health, policy, err := validateCoreHostBootstrapRequest(request, e.binding)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	for _, directory := range policy.Policy.Directories {
		if err := e.host.EnsureDirectory(ctx, directory.Path, 0o750); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("prepare %s storage directory: %w", directory.Purpose, err)
		}
	}
	runtimeObservation, err := e.host.ObserveRuntime(ctx, RuntimeExpectation{
		Runtime: policy.Policy.Runtime.Runtime, Engine: policy.Policy.Runtime.Engine, DataRoot: policy.Policy.Runtime.DataRoot,
	})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("observe existing host runtime: %w", err)
	}
	if runtimeObservation.Engine != "docker" || runtimeObservation.Status != "ready" || runtimeObservation.Version == "" || strings.ContainsAny(runtimeObservation.Version, "\r\n\t ") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("host runtime observation does not prove the exact ready Docker postcondition")
	}
	evidence := coreHostBootstrapEvidence{
		SchemaVersion: "stackkit.core-host-bootstrap-evidence/v1", Status: "pass", StackID: policy.Policy.StackID,
		Target: policy.Policy.Target, Runtime: runtimeObservation,
	}
	for _, directory := range policy.Policy.Directories {
		evidence.Directories = append(evidence.Directories, coreHostBootstrapDirectoryEvidence{
			Path: directory.Path, Mode: directory.Mode, Purpose: directory.Purpose, Status: "prepared",
		})
	}
	canonical, err := json.Marshal(evidence)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Core host-bootstrap evidence: %w", err)
	}
	digest := sha256.Sum256(canonical)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://core-host-bootstrap/" + target.InstanceRef, ObservationDigest: digestString,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://core-host-bootstrap/" + target.InstanceRef, ObservationDigest: digestString,
		}},
	}, nil
}

type coreHostBootstrapDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		NetworkAccess     string   `json:"networkAccess"`
		Operations        []string `json:"operations"`
		ProviderLifecycle string   `json:"providerLifecycle"`
		Scope             string   `json:"scope"`
	} `json:"contract"`
	Policy coreHostBootstrapPolicyDocument `json:"policy"`
}

type coreHostBootstrapPolicyDocument struct {
	StackID string `json:"stackId"`
	Kit     struct {
		Slug           string `json:"slug"`
		Version        string `json:"version"`
		DefinitionHash string `json:"definitionHash"`
	} `json:"kit"`
	Target struct {
		SiteRef string `json:"siteRef"`
		NodeRef string `json:"nodeRef"`
	} `json:"target"`
	Runtime struct {
		InstallMode string `json:"installMode"`
		Runtime     string `json:"runtime"`
		Engine      string `json:"engine"`
		DataRoot    string `json:"dataRoot"`
	} `json:"runtime"`
	Directories []coreHostBootstrapDirectoryDocument `json:"directories"`
}

type coreHostBootstrapDirectoryDocument struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Purpose string `json:"purpose"`
}

type coreHostBootstrapEvidence struct {
	SchemaVersion string `json:"schemaVersion"`
	Status        string `json:"status"`
	StackID       string `json:"stackId"`
	Target        struct {
		SiteRef string `json:"siteRef"`
		NodeRef string `json:"nodeRef"`
	} `json:"target"`
	Runtime     RuntimeObservation                   `json:"runtime"`
	Directories []coreHostBootstrapDirectoryEvidence `json:"directories"`
}

type coreHostBootstrapDirectoryEvidence struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Purpose string `json:"purpose"`
	Status  string `json:"status"`
}

func validateCoreHostBootstrapRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, coreHostBootstrapDocument, error) {
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, errors.New("Core host-bootstrap executor requires exactly one runtime and one health target")
	}
	target := request.RuntimeTargets[0]
	if target.OwnerKind != "module" || target.OwnerRef != coreHostBootstrapModuleRef || target.OwnerVersion != "" ||
		target.ModuleRef != coreHostBootstrapModuleRef || target.UnitRef != coreHostBootstrapUnitRef ||
		target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || len(target.ArtifactRefs) != 1 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, errors.New("runtime target is not the exact locally bound Core host-bootstrap contract")
	}
	if target.ExecutionChannelRef != binding.ExecutionChannelRef {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, errors.New("runtime target does not bind the selected local execution channel")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != "core-host-bootstrap-contract" || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != coreHostBootstrapModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, errors.New("health target is not the exact Core host-bootstrap postcondition")
	}
	var artifact runtimeexecutor.Artifact
	found := 0
	for _, candidate := range request.Artifacts {
		if candidate.ID == target.ArtifactRefs[0] {
			artifact = candidate
			found++
		}
	}
	contract := architecturev2renderer.CoreHostBootstrapRendererContract()
	if found != 1 || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0600" ||
		artifact.OwnerKind != "render-instance" || artifact.ModuleRef != coreHostBootstrapModuleRef || artifact.UnitRef != coreHostBootstrapUnitRef ||
		artifact.OutputRef != coreHostBootstrapOutputRef || target.UnitContractHash != contract.ContractHash || artifact.UnitContractHash != contract.ContractHash ||
		len(artifact.Content) == 0 || len(artifact.Content) > coreHostBootstrapMaxBytes {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, errors.New("artifact is not the exact CUE-owned Core host-bootstrap policy")
	}
	var document coreHostBootstrapDocument
	decoder := json.NewDecoder(bytes.NewReader(artifact.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, errors.New("Core host-bootstrap policy is not the closed v1 schema")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, errors.New("Core host-bootstrap policy has trailing data")
	}
	if err := validateCoreHostBootstrapDocument(document, binding); err != nil {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, coreHostBootstrapDocument{}, err
	}
	return target, health, document, nil
}

func validateCoreHostBootstrapDocument(document coreHostBootstrapDocument, binding LocalTargetBinding) error {
	if document.APIVersion != "stackkit.core-host-bootstrap-policy/v1" || document.Kind != "CoreHostBootstrapPolicy" ||
		document.Contract.NetworkAccess != "none" || document.Contract.ProviderLifecycle != "not-owned" || document.Contract.Scope != "node-local" ||
		!slices.Equal(document.Contract.Operations, []string{"ensure-storage-directories", "observe-existing-runtime"}) {
		return errors.New("Core host-bootstrap policy widens its closed operation contract")
	}
	policy := document.Policy
	if strings.TrimSpace(policy.StackID) == "" ||
		!slices.Contains([]string{"basement-kit", "cloud-kit", "modern-homelab"}, policy.Kit.Slug) || policy.Kit.Version == "" || !validCoreHostBootstrapDigest(policy.Kit.DefinitionHash) ||
		policy.Target.SiteRef != binding.SiteRef || policy.Target.NodeRef != binding.NodeRef ||
		policy.Runtime.InstallMode != "bootstrapped" || policy.Runtime.Runtime != "docker" || policy.Runtime.Engine != "docker" ||
		policy.Runtime.DataRoot == "" || len(policy.Directories) < 3 || len(policy.Directories) > 4 {
		return errors.New("Core host-bootstrap policy does not bind the exact local target and supported runtime")
	}
	seenPath := make(map[string]struct{}, len(policy.Directories))
	seenPurpose := make(map[string]struct{}, len(policy.Directories))
	for _, directory := range policy.Directories {
		if !safeCoreHostBootstrapDirectory(directory.Path) || directory.Mode != "0750" ||
			!slices.Contains([]string{"data", "backup", "stacks", "media"}, directory.Purpose) {
			return errors.New("Core host-bootstrap directory is outside the closed storage contract")
		}
		if _, duplicate := seenPath[directory.Path]; duplicate {
			return errors.New("Core host-bootstrap policy contains a duplicate directory")
		}
		if _, duplicate := seenPurpose[directory.Purpose]; duplicate {
			return errors.New("Core host-bootstrap policy contains a duplicate storage purpose")
		}
		seenPath[directory.Path] = struct{}{}
		seenPurpose[directory.Purpose] = struct{}{}
	}
	for _, required := range []string{"data", "backup", "stacks"} {
		if _, exists := seenPurpose[required]; !exists {
			return errors.New("Core host-bootstrap policy omits a required storage purpose")
		}
	}
	return nil
}

func validCoreHostBootstrapDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func safeCoreHostBootstrapDirectory(value string) bool {
	if value == "" || pathpkg.Clean(value) != value || !strings.HasPrefix(value, "/") {
		return false
	}
	for _, root := range []string{"/opt", "/srv", "/mnt", "/media", "/var/lib/stackkits"} {
		if strings.HasPrefix(value, root+"/") {
			return true
		}
	}
	return false
}

type osCoreHostBootstrapOperations struct{}

// NewOSCoreHostBootstrapOperations explicitly selects the local operating
// system as the closed Core host-bootstrap capability owner. Merely creating
// an executor does not grant this authority; product composition must opt in.
func NewOSCoreHostBootstrapOperations() CoreHostBootstrapOperations {
	return osCoreHostBootstrapOperations{}
}

func (osCoreHostBootstrapOperations) EnsureDirectory(ctx context.Context, path string, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safeCoreHostBootstrapDirectory(path) || mode != 0o750 {
		return errors.New("directory operation is outside the closed Core host-bootstrap contract")
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != mode {
		return errors.New("directory postcondition was not observed")
	}
	return nil
}

func (osCoreHostBootstrapOperations) ObserveRuntime(ctx context.Context, expected RuntimeExpectation) (RuntimeObservation, error) {
	if expected.Runtime != "docker" || expected.Engine != "docker" || expected.DataRoot == "" {
		return RuntimeObservation{}, errors.New("runtime expectation is outside the closed Docker observation contract")
	}
	executable, err := exec.LookPath("docker")
	if err != nil {
		return RuntimeObservation{}, errors.New("required Docker runtime is not installed")
	}
	command := exec.CommandContext(ctx, executable, "version", "--format", "{{.Server.Version}}")
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	output := &coreRuntimeObservationBuffer{remaining: 4096}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return RuntimeObservation{}, fmt.Errorf("Docker runtime is not ready: %w", err)
	}
	if output.exceeded {
		return RuntimeObservation{}, errors.New("Docker runtime observation exceeded the output bound")
	}
	version := strings.TrimSpace(output.String())
	if version == "" || strings.ContainsAny(version, "\r\n\t ") {
		return RuntimeObservation{}, errors.New("Docker runtime returned an invalid version")
	}
	return RuntimeObservation{Engine: "docker", Version: version, Status: "ready"}, nil
}

type coreRuntimeObservationBuffer struct {
	bytes.Buffer
	remaining int
	exceeded  bool
}

func (b *coreRuntimeObservationBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > b.remaining {
		value = value[:b.remaining]
		b.exceeded = true
	}
	b.remaining -= len(value)
	_, _ = b.Buffer.Write(value)
	return original, nil
}
