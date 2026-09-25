package opentofu

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const contractObservationSchemaVersion = "stackkit.opentofu-contract-apply-observation/v1"

var contractRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,254}$`)

// ContractRootExecutor gives a native owner that has no Compose project (the
// Cloud public edge, the Modern federation link, and the bridge origin mTLS
// owner) a Terramate-visible OpenTofu root. Under the compose target it is
// the native executor, byte for byte. Under opentofu and terramate the native
// owner operation runs first, unchanged; after it succeeds the executor
// writes .stackkit/runtime/modules/<moduleRef>/opentofu/main.tf, a root with
// one terraform_data resource whose triggers_replace is the digest of the
// owner's contract artifact, and applies it, so OpenTofu state records the
// contract the owner applied. The root starts no process: StackKits has no
// safe CLI entrypoint that re-applies one owner from a local-exec.
type ContractRootExecutor struct {
	native  runtimeexecutor.Executor
	runtime Runtime
}

// NewContractRootExecutor wraps one prepared native owner executor.
func NewContractRootExecutor(native runtimeexecutor.Executor, runtime Runtime) *ContractRootExecutor {
	return &ContractRootExecutor{native: native, runtime: runtime}
}

// Identity is the native owner's identity: the wrapper adds no authority.
func (e *ContractRootExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	if e == nil || e.native == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return e.native.Identity()
}

// Execute runs the native owner and, under an OpenTofu target, records its
// applied contract in the owner's contract root.
func (e *ContractRootExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if e == nil || e.native == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("OpenTofu contract root executor is not initialized")
	}
	generationTarget, err := nativehost.GenerationTargetFromArtifacts(request.Artifacts)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if !nativehost.GenerationTargetExecutesOpenTofu(generationTarget) {
		return e.native.Execute(ctx, request)
	}
	if strings.TrimSpace(e.runtime.WorkspaceRoot) == "" || len(request.RuntimeTargets) != 1 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("OpenTofu contract root requires a workspace and exactly one runtime target")
	}
	target := request.RuntimeTargets[0]
	contract, err := ownerContractArtifact(request.Artifacts, target)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	relative, err := ModuleRootRelativePath(target.ModuleRef)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	config, err := RenderContractRoot(target, contract)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	binary, providers, err := e.runtime.packagedTools()
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	workspace, err := filepath.Abs(e.runtime.WorkspaceRoot)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("resolve workspace: %w", err)
	}
	outcome, err := e.native.Execute(ctx, request)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	root, err := ensureRootDir(workspace, relative)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if err := writeRoot(root, RootMarker{
		SchemaVersion: RootMarkerSchemaVersion, ModuleRef: target.ModuleRef, InstanceRef: target.InstanceRef,
		RuntimeDir: target.ModuleRef, Kind: RootKindModule,
	}, providers, rootFile{ConfigFile, config, 0o640}); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	record := contractObservation{
		SchemaVersion: contractObservationSchemaVersion, ModuleRef: target.ModuleRef, InstanceRef: target.InstanceRef,
		RequirementID: target.RequirementID, ArtifactID: contract.ID, ArtifactDigest: contract.Digest, Root: relative,
	}
	if record.tofuRun, err = e.runtime.runRoot(ctx, root, binary, []string{"LANG=C", "LC_ALL=C"}); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("record the applied %s contract in OpenTofu state: %w", target.ModuleRef, err)
	}
	if _, err := writeObservation(root, record); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	return outcome, nil
}

// ownerContractArtifact is the one immutable contract artifact the target
// owns.
func ownerContractArtifact(artifacts []runtimeexecutor.Artifact, target runtimeexecutor.RuntimeTarget) (runtimeexecutor.Artifact, error) {
	if len(target.ArtifactRefs) != 1 {
		return runtimeexecutor.Artifact{}, fmt.Errorf("OpenTofu contract root of %s requires exactly one owner contract artifact", target.ModuleRef)
	}
	for _, artifact := range artifacts {
		if artifact.ID != target.ArtifactRefs[0] {
			continue
		}
		if artifact.ModuleRef != target.ModuleRef || artifact.InstanceRef != target.InstanceRef ||
			artifact.Digest != digestBytes(artifact.Content) {
			return runtimeexecutor.Artifact{}, fmt.Errorf("owner contract artifact of %s is not bound to its immutable content", target.ModuleRef)
		}
		return artifact, nil
	}
	return runtimeexecutor.Artifact{}, fmt.Errorf("owner contract artifact of %s is absent", target.ModuleRef)
}

// RenderContractRoot renders the contract root of one owner: OpenTofu state
// records the digest of the contract artifact the owner applied, and a new
// contract replaces the record.
func RenderContractRoot(target runtimeexecutor.RuntimeTarget, contract runtimeexecutor.Artifact) ([]byte, error) {
	for _, value := range []string{target.ModuleRef, target.InstanceRef, target.RequirementID, contract.ID} {
		if !contractRefPattern.MatchString(value) {
			return nil, fmt.Errorf("owner contract reference %q is not a portable contract ID", value)
		}
	}
	if !validDigest(contract.Digest) || !validDigest(target.UnitContractHash) || !validDigest(target.ModuleContractHash) {
		return nil, errors.New("owner contract root requires canonical contract digests")
	}
	return []byte(fmt.Sprintf(`terraform {
  required_version = %q
}

# The native %[2]s owner operation applied this contract. OpenTofu state
# records its digest; a new contract replaces the record.
resource "terraform_data" "owner_contract" {
  triggers_replace = [%[3]q]

  input = {
    module_ref      = %[2]q
    instance_ref    = %[4]q
    requirement_id  = %[5]q
    artifact_id     = %[6]q
    artifact_digest = %[3]q
    unit_contract   = %[7]q
    module_contract = %[8]q
  }
}
`, architecturev2renderer.ComposePayloadOpenTofuRequiredVersion, target.ModuleRef, contract.Digest, target.InstanceRef,
		target.RequirementID, contract.ID, target.UnitContractHash, target.ModuleContractHash)), nil
}

type contractObservation struct {
	SchemaVersion  string `json:"schemaVersion"`
	ModuleRef      string `json:"moduleRef"`
	InstanceRef    string `json:"instanceRef"`
	RequirementID  string `json:"requirementId"`
	ArtifactID     string `json:"artifactId"`
	ArtifactDigest string `json:"artifactDigest"`
	Root           string `json:"root"`
	tofuRun
}

var _ runtimeexecutor.Executor = (*ContractRootExecutor)(nil)
