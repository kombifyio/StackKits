// Package runtimeexecutor defines the provider-neutral v1beta1 contract for
// executing already-authorized governed runtime targets.
//
// The package has no provider adapters, credentials, database, network,
// workspace paths, StackSpec types, or product policy. A control plane retains
// authorization and adapter-selection authority; this package validates the
// immutable execution envelope and the exact adapter outcome set.
package runtimeexecutor

import (
	"context"
	"time"
)

// APIVersion is the closed runtime executor contract version.
const APIVersion = "runtimeexecutor/v1beta1"

// Bounded envelope limits keep validation and hashing predictable.
const (
	MaxRuntimeTargets       = 256
	MaxHealthTargets        = 256
	MaxAccessBindings       = 256
	MaxBackupTargetBindings = 256
	MaxArtifacts            = 512
	MaxArtifactBytes        = 4 << 20
	MaxTotalArtifactBytes   = 32 << 20
	MaxNodeRefsPerTarget    = 128
	MaxSiteRefsPerTarget    = 128
	MaxAdapterAgents        = 128
)

// MaxAccessBindingValidity bounds one externally issued admission receipt.
// It does not bound the lifetime of the externally managed access fabric; a
// fresh binding must be issued when a later Apply is authorized.
const MaxAccessBindingValidity = 24 * time.Hour

// MaxBackupTargetBindingValidity bounds one externally issued backup-target
// custody receipt. The backing target may outlive this receipt, but every
// execution must carry a fresh authorization envelope.
const MaxBackupTargetBindingValidity = 24 * time.Hour

// ExecutorIdentity is the immutable identity selected by the owning control
// plane. Digest is a canonical SHA-256 digest of the executable or adapter
// release, not a provider credential.
type ExecutorIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// DaemonTarget is an exact plan-owned runtime daemon binding. SocketPath is
// the sole path-shaped value in this package; it names a runtime API socket,
// never a workspace artifact or credential file.
type DaemonTarget struct {
	Ref         string `json:"ref"`
	InstanceRef string `json:"instance_ref"`
	Engine      string `json:"engine"`
	SocketPath  string `json:"socket_path"`
}

// AccessCapability is the exact capability authority exposed by one runtime
// target for matching AccessBindings. It is separate from provider/module
// contracts because capability contracts are independently hash-bound by the
// StackKits catalog.
type AccessCapability struct {
	Ref          string `json:"ref"`
	ContractHash string `json:"contract_hash"`
}

// RuntimeAdapterAgentBinding binds one adapter-owned companion agent module
// and its exact contract-handoff artifacts. It carries authority identity
// only; endpoints, credentials, host access, provider lifecycle, and transport
// configuration remain in the selected executor's custody.
type RuntimeAdapterAgentBinding struct {
	ID                 string   `json:"id"`
	ModuleRef          string   `json:"module_ref"`
	ModuleVersion      string   `json:"module_version"`
	ModuleContractHash string   `json:"module_contract_hash"`
	ArtifactRefs       []string `json:"artifact_refs"`
}

// RuntimeAdapterBinding is the exact workload-scoped adapter authority chosen
// by the upstream plan. Provider/module versions and hashes bind that choice;
// ArtifactRefs bind only provider-neutral, content-addressed handoff material.
// Agents are separately named so a control-plane adapter cannot silently
// widen itself into node-agent authority.
type RuntimeAdapterBinding struct {
	ID                   string                       `json:"id"`
	ProviderRef          string                       `json:"provider_ref"`
	ProviderVersion      string                       `json:"provider_version"`
	ProviderContractHash string                       `json:"provider_contract_hash"`
	ModuleRef            string                       `json:"module_ref"`
	ModuleVersion        string                       `json:"module_version"`
	ModuleContractHash   string                       `json:"module_contract_hash"`
	ArtifactRefs         []string                     `json:"artifact_refs"`
	Agents               []RuntimeAdapterAgentBinding `json:"agents,omitempty"`
}

// RuntimeTarget is one exact governed runtime instance. Contract hashes bind
// every authority level involved in selecting it. Module and unit pairs may be
// absent only when the corresponding authority level is not used.
// ExecutionChannelRef is an opaque, already-authorized routing identity. It is
// never an endpoint, address, provider reference, credential, or permission to
// discover a target.
type RuntimeTarget struct {
	RequirementID            string                 `json:"requirement_id"`
	OwnerKind                string                 `json:"owner_kind"`
	OwnerRef                 string                 `json:"owner_ref"`
	OwnerVersion             string                 `json:"owner_version,omitempty"`
	OwnerContractHash        string                 `json:"owner_contract_hash"`
	ProviderRef              string                 `json:"provider_ref"`
	ProviderContractHash     string                 `json:"provider_contract_hash"`
	ModuleRef                string                 `json:"module_ref,omitempty"`
	ModuleContractHash       string                 `json:"module_contract_hash,omitempty"`
	UnitRef                  string                 `json:"unit_ref,omitempty"`
	UnitContractHash         string                 `json:"unit_contract_hash,omitempty"`
	RuntimeKind              string                 `json:"runtime_kind"`
	RuntimeDelivery          string                 `json:"runtime_delivery"`
	RuntimeEngine            string                 `json:"runtime_engine,omitempty"`
	InstanceRef              string                 `json:"instance_ref"`
	ExecutionChannelRef      string                 `json:"execution_channel_ref,omitempty"`
	SiteRefs                 []string               `json:"site_refs"`
	NodeRefs                 []string               `json:"node_refs"`
	WorkloadRef              string                 `json:"workload_ref,omitempty"`
	ImageRef                 string                 `json:"image_ref,omitempty"`
	ImageDigest              string                 `json:"image_digest,omitempty"`
	DaemonBindings           []DaemonTarget         `json:"daemon_bindings"`
	ArtifactRefs             []string               `json:"artifact_refs"`
	RuntimeAdapter           *RuntimeAdapterBinding `json:"runtime_adapter,omitempty"`
	AccessCapabilities       []AccessCapability     `json:"access_capabilities,omitempty"`
	AccessBindingRefs        []string               `json:"access_binding_refs,omitempty"`
	BackupTargetCapabilities []AccessCapability     `json:"backup_target_capabilities,omitempty"`
	BackupTargetBindingRefs  []string               `json:"backup_target_binding_refs,omitempty"`
}

// AccessBinding is one exact, already-authorized Home access dependency for a
// governed runtime target. The shared contract carries only StackKits-owned
// requirement identity and opaque external binding identity. It deliberately
// has no transport, endpoint, address, credential, provider resource,
// account, region, discovery, lease, generation, or lifecycle field.
// Freshness is checked by Invoke immediately before the adapter call; no
// caller-supplied timestamp is accepted as proof of current validity.
type AccessBinding struct {
	ID                     string   `json:"id"`
	Kind                   string   `json:"kind"`
	RuntimeRequirementID   string   `json:"runtime_requirement_id"`
	StackID                string   `json:"stack_id"`
	SiteRef                string   `json:"site_ref"`
	CapabilityRef          string   `json:"capability_ref"`
	ContractOwnerRef       string   `json:"contract_owner_ref"`
	CapabilityContractHash string   `json:"capability_contract_hash"`
	TargetNodeRefs         []string `json:"target_node_refs"`
	RequirementsHash       string   `json:"requirements_hash"`
	BindingRef             string   `json:"binding_ref"`
	BindingHash            string   `json:"binding_hash"`
	AccessFabricRef        string   `json:"access_fabric_ref"`
	StackKitsVersion       string   `json:"stackkits_version"`
	CandidateDigest        string   `json:"candidate_digest"`
	SpecHash               string   `json:"spec_hash"`
	IssuedAt               string   `json:"issued_at"`
	ValidUntil             string   `json:"valid_until"`
	ProjectionHash         string   `json:"projection_hash"`
}

// BackupTargetBinding is one exact, already-authorized Cloud backup-target
// dependency for a governed runtime target. It carries only StackKits-owned
// requirement identity plus opaque target and custody-attestation references.
// Provider, bucket, endpoint, credential, region, resource, lease, transport,
// generation, and lifecycle details are deliberately absent.
type BackupTargetBinding struct {
	ID                     string   `json:"id"`
	Kind                   string   `json:"kind"`
	RuntimeRequirementID   string   `json:"runtime_requirement_id"`
	StackID                string   `json:"stack_id"`
	SiteRef                string   `json:"site_ref"`
	CapabilityRef          string   `json:"capability_ref"`
	ContractOwnerRef       string   `json:"contract_owner_ref"`
	CapabilityContractHash string   `json:"capability_contract_hash"`
	TargetNodeRefs         []string `json:"target_node_refs"`
	RequirementsHash       string   `json:"requirements_hash"`
	BindingRef             string   `json:"binding_ref"`
	BindingHash            string   `json:"binding_hash"`
	BackupTargetRef        string   `json:"backup_target_ref"`
	CustodyAttestationRef  string   `json:"custody_attestation_ref"`
	StackKitsVersion       string   `json:"stackkits_version"`
	CandidateDigest        string   `json:"candidate_digest"`
	SpecHash               string   `json:"spec_hash"`
	IssuedAt               string   `json:"issued_at"`
	ValidUntil             string   `json:"valid_until"`
	ProjectionHash         string   `json:"projection_hash"`
}

// HealthProbe is a closed, address-free probe descriptor. The authenticated
// runtime owner resolves the target inside its exact execution channel; callers
// cannot provide a URL, host, credential, trust root, or redirect policy.
type HealthProbe struct {
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	Method           string `json:"method,omitempty"`
	FollowRedirects  bool   `json:"follow_redirects,omitempty"`
	Path             string `json:"path,omitempty"`
	ExpectedStatuses []int  `json:"expected_statuses,omitempty"`
}

// HealthTarget is one exact post-execution health requirement.
type HealthTarget struct {
	RequirementID        string       `json:"requirement_id"`
	RuntimeRequirementID string       `json:"runtime_requirement_id,omitempty"`
	SourceRef            string       `json:"source_ref"`
	ContractHash         string       `json:"contract_hash"`
	Phase                string       `json:"phase"`
	Kind                 string       `json:"kind"`
	TargetKind           string       `json:"target_kind"`
	TargetRef            string       `json:"target_ref"`
	RouteRef             string       `json:"route_ref,omitempty"`
	BackendPoolRef       string       `json:"backend_pool_ref,omitempty"`
	Probe                *HealthProbe `json:"probe,omitempty"`
	SiteRefs             []string     `json:"site_refs"`
	NodeRefs             []string     `json:"node_refs"`
}

// Artifact is an immutable content-addressed artifact snapshot. It deliberately
// contains no workspace or host path. Digest must match Content exactly.
type Artifact struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	Format               string   `json:"format"`
	Mode                 string   `json:"mode"`
	ExecutionClass       string   `json:"execution_class"`
	OwnerKind            string   `json:"owner_kind"`
	OwnerRef             string   `json:"owner_ref"`
	OwnerContractHash    string   `json:"owner_contract_hash"`
	ProviderRef          string   `json:"provider_ref,omitempty"`
	ProviderContractHash string   `json:"provider_contract_hash,omitempty"`
	ModuleRef            string   `json:"module_ref,omitempty"`
	ModuleContractHash   string   `json:"module_contract_hash,omitempty"`
	UnitRef              string   `json:"unit_ref,omitempty"`
	UnitContractHash     string   `json:"unit_contract_hash,omitempty"`
	InstanceRef          string   `json:"instance_ref,omitempty"`
	OutputRef            string   `json:"output_ref,omitempty"`
	SiteRefs             []string `json:"site_refs"`
	NodeRefs             []string `json:"node_refs"`
	Digest               string   `json:"digest"`
	Content              []byte   `json:"content"`
}

// ExecutionRequest is the sealed, canonical input to an Executor. Its hashes
// bind the upstream plan, generated output, generation receipt, complete Apply
// requirements, authenticated evidence, and exact artifact set.
type ExecutionRequest struct {
	APIVersion            string                `json:"api_version"`
	Executor              ExecutorIdentity      `json:"executor"`
	PlanHash              string                `json:"plan_hash"`
	ManifestHash          string                `json:"manifest_hash"`
	GenerationReceiptHash string                `json:"generation_receipt_hash"`
	RequirementsHash      string                `json:"requirements_hash"`
	EvidenceBundleHash    string                `json:"evidence_bundle_hash"`
	AuthorizationTime     string                `json:"authorization_time,omitempty"`
	ArtifactSetHash       string                `json:"artifact_set_hash"`
	RuntimeTargets        []RuntimeTarget       `json:"runtime_targets"`
	HealthTargets         []HealthTarget        `json:"health_targets"`
	AccessBindings        []AccessBinding       `json:"access_bindings,omitempty"`
	BackupTargetBindings  []BackupTargetBinding `json:"backup_target_bindings,omitempty"`
	Artifacts             []Artifact            `json:"artifacts"`
	RequestDigest         string                `json:"request_digest"`
}

// RuntimeStatus is the closed successful runtime outcome vocabulary.
type RuntimeStatus string

// RuntimeStatusApplied means the exact governed instance was applied.
const RuntimeStatusApplied RuntimeStatus = "applied"

// Artifact execution classes separate executable runtime material from
// workload-scoped adapter handoffs and immutable plan metadata.
const (
	ArtifactExecutionClassExecutable      = "executable"
	ArtifactExecutionClassContractHandoff = "contract-handoff"
	ArtifactExecutionClassPlan            = "plan"
)

// HealthStatus is the closed successful health outcome vocabulary.
type HealthStatus string

// HealthStatusHealthy means the exact health requirement passed.
const HealthStatusHealthy HealthStatus = "healthy"

// RuntimeOutcome is an opaque, digest-bound observation for one runtime target.
type RuntimeOutcome struct {
	RequirementID     string        `json:"requirement_id"`
	InstanceRef       string        `json:"instance_ref"`
	Status            RuntimeStatus `json:"status"`
	ObservationRef    string        `json:"observation_ref"`
	ObservationDigest string        `json:"observation_digest"`
}

// HealthOutcome is an opaque, digest-bound observation for one health target.
type HealthOutcome struct {
	RequirementID     string       `json:"requirement_id"`
	TargetRef         string       `json:"target_ref"`
	Status            HealthStatus `json:"status"`
	ObservationRef    string       `json:"observation_ref"`
	ObservationDigest string       `json:"observation_digest"`
}

// ExecutionOutcome is the untrusted adapter return value. It contains no
// authority hashes or result digest; Invoke supplies and verifies those.
type ExecutionOutcome struct {
	Runtime []RuntimeOutcome `json:"runtime"`
	Health  []HealthOutcome  `json:"health"`
}

// ExecutionResult is the immutable verified result projection returned by
// Invoke. ResultDigest binds every upstream hash, executor identity, artifact
// set, and exact outcome.
type ExecutionResult struct {
	APIVersion            string           `json:"api_version"`
	Executor              ExecutorIdentity `json:"executor"`
	PlanHash              string           `json:"plan_hash"`
	ManifestHash          string           `json:"manifest_hash"`
	GenerationReceiptHash string           `json:"generation_receipt_hash"`
	RequirementsHash      string           `json:"requirements_hash"`
	EvidenceBundleHash    string           `json:"evidence_bundle_hash"`
	ArtifactSetHash       string           `json:"artifact_set_hash"`
	RequestDigest         string           `json:"request_digest"`
	Runtime               []RuntimeOutcome `json:"runtime"`
	Health                []HealthOutcome  `json:"health"`
	ResultDigest          string           `json:"result_digest"`
}

// Executor is transport-neutral. The owning control plane selects an
// implementation and retains authorization, retries, and durable state.
type Executor interface {
	Identity() ExecutorIdentity
	Execute(context.Context, ExecutionRequest) (ExecutionOutcome, error)
}
