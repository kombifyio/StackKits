package runtimeexecutorlocal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	basementComposeProviderRef      = "stackkits-basement-compose"
	basementComposeModuleRef        = "socket-proxy"
	basementComposeUnitRef          = "compose"
	basementComposeOutputRef        = "foundation/socket-proxy/compose.yaml"
	basementComposeArtifactPrefix   = "socket-proxy-compose-instance-"
	basementComposeHealthSourceRef  = "socket-proxy-contract"
	basementComposeImageRef         = "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2"
	basementComposeImageDigest      = "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
	basementComposeServiceRef       = "socket-proxy"
	basementComposeDaemonRef        = "docker-default"
	basementComposeRuntimeEngine    = "docker"
	basementComposeMaxArtifactBytes = 64 << 10
)

// ComposeProject is the closed operation input for one already-authorized
// local Compose target. It contains no provider, endpoint, credential,
// workspace path, executable, argument, or discovery authority.
type ComposeProject struct {
	ProjectRef          string
	SiteRef             string
	NodeRef             string
	ExecutionChannelRef string
	ArtifactID          string
	ArtifactDigest      string
	Definition          []byte
	Service             ComposeServiceExpectation
}

type ComposeServiceExpectation struct {
	Ref         string
	ImageRef    string
	ImageDigest string
}

type ComposeApplyObservation struct {
	ProjectRef     string `json:"projectRef"`
	ArtifactDigest string `json:"artifactDigest"`
	Status         string `json:"status"`
}

type ComposeServiceObservation struct {
	Ref         string `json:"ref"`
	ImageRef    string `json:"imageRef"`
	ImageDigest string `json:"imageDigest"`
	Status      string `json:"status"`
	Health      string `json:"health"`
}

type ComposeVerifyObservation struct {
	ProjectRef     string                      `json:"projectRef"`
	ArtifactDigest string                      `json:"artifactDigest"`
	Status         string                      `json:"status"`
	Services       []ComposeServiceObservation `json:"services"`
}

// BasementComposeOperations is supplied by the authenticated local execution
// channel owner. The adapter cannot choose or discover a Docker endpoint and
// cannot fall back to shell execution.
type BasementComposeOperations interface {
	ApplyProject(context.Context, ComposeProject) (ComposeApplyObservation, error)
	VerifyProject(context.Context, ComposeProject) (ComposeVerifyObservation, error)
}

// BasementComposeAuthority is the service-owned catalog binding selected when
// the adapter is registered. Hashes are never learned from the request.
type BasementComposeAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

// BasementComposeExecutor is an isolated adapter for the first concrete
// Basement Compose unit. It is intentionally not registered in the product
// registry until an authenticated local operations implementation exists.
type BasementComposeExecutor struct {
	identity  runtimeexecutor.ExecutorIdentity
	binding   LocalTargetBinding
	authority BasementComposeAuthority
	compose   BasementComposeOperations
}

func NewBasementComposeExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority BasementComposeAuthority, compose BasementComposeOperations) *BasementComposeExecutor {
	return &BasementComposeExecutor{identity: identity, binding: binding, authority: authority, compose: compose}
}

func (e *BasementComposeExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *BasementComposeExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement Compose executor requires a context")
	}
	if e == nil || e.compose == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement Compose executor requires one explicit authenticated local target binding")
	}
	target, health, project, err := validateBasementComposeRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	applyObservation, err := e.compose.ApplyProject(ctx, defensiveComposeProject(project))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Basement Compose project: %w", err)
	}
	if applyObservation.ProjectRef != project.ProjectRef || applyObservation.ArtifactDigest != project.ArtifactDigest || applyObservation.Status != "applied" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Compose apply observation does not prove the exact project and artifact")
	}
	verifyObservation, err := e.compose.VerifyProject(ctx, defensiveComposeProject(project))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Basement Compose project: %w", err)
	}
	if err := validateComposeVerification(project, verifyObservation); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                   `json:"schemaVersion"`
		Apply         ComposeApplyObservation  `json:"apply"`
		Verify        ComposeVerifyObservation `json:"verify"`
	}{SchemaVersion: "stackkit.basement-compose-evidence/v1", Apply: applyObservation, Verify: verifyObservation})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Basement Compose observation: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://basement-compose/" + target.InstanceRef, ObservationDigest: digestString,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://basement-compose/" + target.InstanceRef, ObservationDigest: digestString,
		}},
	}, nil
}

func defensiveComposeProject(project ComposeProject) ComposeProject {
	project.Definition = append([]byte(nil), project.Definition...)
	return project
}

func validateBasementComposeRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority BasementComposeAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, ComposeProject, error) {
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("Basement Compose executor requires exactly one runtime, one health target, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.SocketProxyRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != basementComposeModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != basementComposeProviderRef || target.ProviderContractHash != authority.ProviderContractHash || target.ModuleRef != basementComposeModuleRef || target.UnitRef != basementComposeUnitRef ||
		target.OwnerContractHash != authority.ModuleContractHash || target.ModuleContractHash != authority.ModuleContractHash || target.UnitContractHash != contract.ContractHash ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != basementComposeRuntimeEngine ||
		target.WorkloadRef != "" || target.ImageRef != basementComposeImageRef || target.ImageDigest != basementComposeImageDigest || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.DaemonBindings) != 1 || len(target.ArtifactRefs) != 1 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("runtime target is not the exact locally bound Basement socket-proxy Compose contract")
	}
	daemon := target.DaemonBindings[0]
	wantInstance := basementComposeUnitRef + "-node-" + binding.NodeRef + "-daemon-" + daemon.InstanceRef
	if daemon.Ref != basementComposeDaemonRef || daemon.Engine != basementComposeRuntimeEngine || strings.TrimSpace(daemon.InstanceRef) == "" || target.InstanceRef != wantInstance {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("runtime target does not carry the exact governed Docker daemon instance")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != basementComposeHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != basementComposeModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("health target is not the exact Basement socket-proxy postcondition")
	}
	wantArtifactID := basementComposeArtifactPrefix + target.InstanceRef
	if target.ArtifactRefs[0] != wantArtifactID {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("runtime target does not bind the exact instance Compose artifact")
	}
	var artifact runtimeexecutor.Artifact
	found := 0
	for _, candidate := range request.Artifacts {
		if candidate.ID == wantArtifactID {
			artifact = candidate
			found++
		}
	}
	if found != 1 || artifact.Kind != "compose" || artifact.Format != "yaml" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != target.InstanceRef || artifact.ProviderRef != basementComposeProviderRef ||
		artifact.ModuleRef != basementComposeModuleRef || artifact.UnitRef != basementComposeUnitRef || artifact.InstanceRef != target.InstanceRef || artifact.OutputRef != basementComposeOutputRef ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitContractHash != target.UnitContractHash ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 || len(artifact.Content) > basementComposeMaxArtifactBytes {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("artifact is not the exact CUE-owned Basement socket-proxy Compose instance")
	}
	wantNetworkRef := target.InstanceRef + "-network-docker-api-readonly-interface-docker-api-readonly"
	wantContent, err := architecturev2renderer.ExpectedSocketProxyComposeArtifact(daemon.SocketPath, wantNetworkRef)
	if err != nil || !bytes.Equal(artifact.Content, wantContent) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("Compose content differs from the exact governed socket-proxy policy")
	}
	digest := sha256.Sum256(artifact.Content)
	wantDigest := "sha256:" + hex.EncodeToString(digest[:])
	if artifact.Digest != wantDigest {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, ComposeProject{}, errors.New("Compose artifact digest does not match its immutable content")
	}
	return target, health, ComposeProject{
		ProjectRef: target.InstanceRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		ArtifactID: artifact.ID, ArtifactDigest: artifact.Digest, Definition: append([]byte(nil), artifact.Content...),
		Service: ComposeServiceExpectation{Ref: basementComposeServiceRef, ImageRef: basementComposeImageRef, ImageDigest: basementComposeImageDigest},
	}, nil
}

func validateComposeVerification(project ComposeProject, observation ComposeVerifyObservation) error {
	if observation.ProjectRef != project.ProjectRef || observation.ArtifactDigest != project.ArtifactDigest || observation.Status != "ready" || len(observation.Services) != 1 {
		return errors.New("Compose verification does not prove the exact ready project")
	}
	service := observation.Services[0]
	if service.Ref != project.Service.Ref || service.ImageRef != project.Service.ImageRef || service.ImageDigest != project.Service.ImageDigest || service.Status != "running" || service.Health != "healthy" {
		return errors.New("Compose verification does not prove the exact pinned healthy service")
	}
	return nil
}
