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
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	homeBackupTargetModuleRef = "stackkits-home-backup-target"
	homeBackupTargetUnitRef   = "backup-policy"
	homeBackupTargetOutputRef = "home/backup/target-policy.json"
	homeBackupTargetMaxBytes  = 32 << 10
)

// BackupDirectoryObservation is the bounded post-bootstrap fact returned by
// the Home backup-target host adapter.
type BackupDirectoryObservation struct {
	Path   string      `json:"path"`
	Mode   fs.FileMode `json:"-"`
	Status string      `json:"status"`
}

// HomeBackupTargetOperations is intentionally observation-only. Core owns
// directory preparation; this Home owner may neither create storage nor run a
// generic command, network operation, discovery flow, or provider lifecycle.
type HomeBackupTargetOperations interface {
	ObserveBackupDirectory(context.Context, string) (BackupDirectoryObservation, error)
}

// HomeBackupTargetExecutor verifies one exact CUE-declared backup target on
// the Home control-plane node already bound by the caller.
type HomeBackupTargetExecutor struct {
	identity runtimeexecutor.ExecutorIdentity
	binding  LocalTargetBinding
	host     HomeBackupTargetOperations
}

func NewHomeBackupTargetExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, host HomeBackupTargetOperations) *HomeBackupTargetExecutor {
	if host == nil {
		host = osHomeBackupTargetOperations{}
	}
	return &HomeBackupTargetExecutor{identity: identity, binding: binding, host: host}
}

func (e *HomeBackupTargetExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *HomeBackupTargetExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home backup-target executor requires a context")
	}
	if e == nil || e.host == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home backup-target executor requires one explicit local target binding")
	}
	target, health, policy, err := validateHomeBackupTargetRequest(request, e.binding)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	observation, err := e.host.ObserveBackupDirectory(ctx, policy.Policy.Directory.Path)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("observe Home backup target: %w", err)
	}
	if observation.Path != policy.Policy.Directory.Path || observation.Mode.Perm() != 0o750 || observation.Status != "ready" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("backup directory observation does not prove the exact ready postcondition")
	}
	evidence := homeBackupTargetEvidence{
		SchemaVersion: "stackkit.home-backup-target-evidence/v1", Status: "pass", StackID: policy.Policy.StackID,
		Target: policy.Policy.Target,
	}
	evidence.Directory.Path = observation.Path
	evidence.Directory.Mode = "0750"
	evidence.Directory.Purpose = "backup"
	evidence.Directory.Status = observation.Status
	canonical, err := json.Marshal(evidence)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Home backup-target evidence: %w", err)
	}
	digest := sha256.Sum256(canonical)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://home-backup-target/" + target.InstanceRef, ObservationDigest: digestString,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://home-backup-target/" + target.InstanceRef, ObservationDigest: digestString,
		}},
	}, nil
}

type homeBackupTargetDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		NetworkAccess     string   `json:"networkAccess"`
		Operations        []string `json:"operations"`
		ProviderLifecycle string   `json:"providerLifecycle"`
		Scope             string   `json:"scope"`
	} `json:"contract"`
	Policy homeBackupTargetPolicyDocument `json:"policy"`
}

type homeBackupTargetPolicyDocument struct {
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
	Directory struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Purpose string `json:"purpose"`
	} `json:"directory"`
}

type homeBackupTargetEvidence struct {
	SchemaVersion string `json:"schemaVersion"`
	Status        string `json:"status"`
	StackID       string `json:"stackId"`
	Target        struct {
		SiteRef string `json:"siteRef"`
		NodeRef string `json:"nodeRef"`
	} `json:"target"`
	Directory struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Purpose string `json:"purpose"`
		Status  string `json:"status"`
	} `json:"directory"`
}

func validateHomeBackupTargetRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, homeBackupTargetDocument, error) {
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, errors.New("Home backup-target executor requires exactly one runtime and one health target")
	}
	target := request.RuntimeTargets[0]
	if target.OwnerKind != "module" || target.OwnerRef != homeBackupTargetModuleRef || target.OwnerVersion != "" ||
		target.ModuleRef != homeBackupTargetModuleRef || target.UnitRef != homeBackupTargetUnitRef ||
		target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || len(target.ArtifactRefs) != 1 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, errors.New("runtime target is not the exact locally bound Home backup-target contract")
	}
	if target.ExecutionChannelRef != binding.ExecutionChannelRef {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, errors.New("runtime target does not bind the selected local execution channel")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != "home-backup-target-contract" || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != homeBackupTargetModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, errors.New("health target is not the exact Home backup-target postcondition")
	}
	var artifact runtimeexecutor.Artifact
	found := 0
	for _, candidate := range request.Artifacts {
		if candidate.ID == target.ArtifactRefs[0] {
			artifact = candidate
			found++
		}
	}
	contract := architecturev2renderer.HomeBackupTargetRendererContract()
	if found != 1 || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0600" ||
		artifact.OwnerKind != "render-instance" || artifact.ModuleRef != homeBackupTargetModuleRef || artifact.UnitRef != homeBackupTargetUnitRef ||
		artifact.OutputRef != homeBackupTargetOutputRef || target.UnitContractHash != contract.ContractHash || artifact.UnitContractHash != contract.ContractHash ||
		len(artifact.Content) == 0 || len(artifact.Content) > homeBackupTargetMaxBytes {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, errors.New("artifact is not the exact CUE-owned Home backup-target policy")
	}
	var document homeBackupTargetDocument
	decoder := json.NewDecoder(bytes.NewReader(artifact.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, errors.New("Home backup-target policy is not the closed v1 schema")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, errors.New("Home backup-target policy has trailing data")
	}
	if err := validateHomeBackupTargetDocument(document, binding); err != nil {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, homeBackupTargetDocument{}, err
	}
	return target, health, document, nil
}

func validateHomeBackupTargetDocument(document homeBackupTargetDocument, binding LocalTargetBinding) error {
	if document.APIVersion != "stackkit.home-backup-target-policy/v1" || document.Kind != "HomeBackupTargetPolicy" ||
		document.Contract.NetworkAccess != "none" || document.Contract.ProviderLifecycle != "not-owned" || document.Contract.Scope != "home-control-plane-node" ||
		!slices.Equal(document.Contract.Operations, []string{"observe-backup-directory"}) {
		return errors.New("Home backup-target policy widens its closed operation contract")
	}
	policy := document.Policy
	if strings.TrimSpace(policy.StackID) == "" ||
		!slices.Contains([]string{"basement-kit", "modern-homelab"}, policy.Kit.Slug) || policy.Kit.Version == "" || !validCoreHostBootstrapDigest(policy.Kit.DefinitionHash) ||
		policy.Target.SiteRef != binding.SiteRef || policy.Target.NodeRef != binding.NodeRef ||
		!safeCoreHostBootstrapDirectory(policy.Directory.Path) || policy.Directory.Mode != "0750" || policy.Directory.Purpose != "backup" {
		return errors.New("Home backup-target policy does not bind the exact local target and safe backup directory")
	}
	return nil
}

type osHomeBackupTargetOperations struct{}

// NewOSHomeBackupTargetOperations explicitly selects local filesystem
// observation as the closed Home backup-target capability owner.
func NewOSHomeBackupTargetOperations() HomeBackupTargetOperations {
	return osHomeBackupTargetOperations{}
}

func (osHomeBackupTargetOperations) ObserveBackupDirectory(ctx context.Context, path string) (BackupDirectoryObservation, error) {
	if err := ctx.Err(); err != nil {
		return BackupDirectoryObservation{}, err
	}
	if !safeCoreHostBootstrapDirectory(path) {
		return BackupDirectoryObservation{}, errors.New("directory observation is outside the closed Home backup-target contract")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return BackupDirectoryObservation{}, err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || info.Mode().Perm() != 0o750 {
		return BackupDirectoryObservation{}, errors.New("backup directory postcondition was not observed")
	}
	return BackupDirectoryObservation{Path: path, Mode: info.Mode(), Status: "ready"}, nil
}
