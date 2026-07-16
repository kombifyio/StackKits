package architecturev2renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/kombifyio/stackkits/internal/generationartifact"
)

// UnitRenderer renders exactly one explicit execution instance of one
// governed logical render unit. Implementations get only normalized,
// immutable plan inputs and return logical outputs by declared ref; they never
// receive legacy StackSpec models or derive artifact paths or node placement.
type UnitRenderer interface {
	RenderUnit(context.Context, RenderUnit) ([]UnitOutput, error)
}

// UnitRendererFunc adapts a function to UnitRenderer.
type UnitRendererFunc func(context.Context, RenderUnit) ([]UnitOutput, error)

func (f UnitRendererFunc) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	return f(ctx, unit)
}

// RendererContract is the complete immutable implementation identity carried
// by one render unit. Every field participates in registry lookup.
type RendererContract struct {
	Kind         string
	RendererRef  string
	TemplateRef  string
	Version      string
	ContractHash string
}

// Registry maps exact renderer contracts to implementations. There is no
// renderer-only, prefix, semantic-version, latest-version, or template/hash
// fallback lookup.
type Registry struct {
	mu        sync.RWMutex
	renderers map[RendererContract]UnitRenderer
}

// NewRegistry returns an empty exact-ID registry.
func NewRegistry() *Registry {
	return &Registry{renderers: make(map[RendererContract]UnitRenderer)}
}

// Register installs one implementation under an exact, hash-bound renderer
// contract. A new template version or contract hash requires a new explicit
// registration even when rendererRef is unchanged.
func (r *Registry) Register(contract RendererContract, renderer UnitRenderer) error {
	if r == nil {
		return fail(ErrUnknownRenderer, "renderer.registry", "registry is required")
	}
	if err := requireContractID(contract.Kind, "renderer.registry.kind"); err != nil {
		return err
	}
	if err := requireContractID(contract.RendererRef, "renderer.registry.rendererRef"); err != nil {
		return err
	}
	if strings.TrimSpace(contract.TemplateRef) == "" || strings.ContainsAny(contract.TemplateRef, "\r\n\t") {
		return fail(ErrInvalidPlan, "renderer.registry.templateRef", "a non-empty template reference is required")
	}
	if strings.TrimSpace(contract.Version) == "" {
		return fail(ErrInvalidPlan, "renderer.registry.version", "an exact renderer contract version is required")
	}
	if !validSHA256(contract.ContractHash) {
		return fail(ErrInvalidPlan, "renderer.registry.contractHash", "must be a lowercase sha256 digest")
	}
	if renderer == nil {
		return fail(ErrUnknownRenderer, "renderer.registry."+contract.RendererRef, "renderer implementation is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.renderers[contract]; exists {
		return fail(ErrDuplicate, "renderer.registry."+contract.RendererRef, "exact renderer contract is already registered")
	}
	r.renderers[contract] = renderer
	return nil
}

func (r *Registry) exact(contract renderUnitContract) (UnitRenderer, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	renderer, exists := r.renderers[RendererContract{
		Kind: contract.kind, RendererRef: contract.rendererRef, TemplateRef: contract.templateRef,
		Version: contract.version, ContractHash: contract.contractHash,
	}]
	return renderer, exists
}

// RenderUnit is the immutable logical-unit plus exact-instance projection
// handed to a renderer. JSON and list accessors return defensive copies.
type RenderUnit struct {
	moduleID                   string
	id                         string
	instanceID                 string
	instanceScope              string
	siteRef                    string
	nodeRef                    string
	daemonRef                  string
	daemonInstanceRef          string
	daemonEngine               string
	daemonSocketPath           string
	kind                       string
	rendererRef                string
	templateRef                string
	version                    string
	contractHash               string
	publicInputRefs            []string
	secretInputRefs            []string
	logicalSiteRefs            []string
	logicalNodeRefs            []string
	valuesJSON                 []byte
	secretRefsJSON             []byte
	placementJSON              []byte
	serviceEndpointsJSON       []byte
	providedInterfacesJSON     []byte
	requiredInterfacesJSON     []byte
	runtimeNetworkBindingsJSON []byte
	declaredOutputRef          []string
}

func (u RenderUnit) ModuleID() string          { return u.moduleID }
func (u RenderUnit) ID() string                { return u.id }
func (u RenderUnit) InstanceID() string        { return u.instanceID }
func (u RenderUnit) InstanceScope() string     { return u.instanceScope }
func (u RenderUnit) Kind() string              { return u.kind }
func (u RenderUnit) RendererRef() string       { return u.rendererRef }
func (u RenderUnit) TemplateRef() string       { return u.templateRef }
func (u RenderUnit) Version() string           { return u.version }
func (u RenderUnit) ContractHash() string      { return u.contractHash }
func (u RenderUnit) PublicInputRefs() []string { return append([]string(nil), u.publicInputRefs...) }
func (u RenderUnit) SecretInputRefs() []string { return append([]string(nil), u.secretInputRefs...) }

// LogicalSiteRefs and LogicalNodeRefs expose the governed eligible set for a
// module-scoped renderer. They are never an instruction to choose a node;
// node-local work always uses the exact optional instance accessors below.
func (u RenderUnit) LogicalSiteRefs() []string { return append([]string(nil), u.logicalSiteRefs...) }
func (u RenderUnit) LogicalNodeRefs() []string { return append([]string(nil), u.logicalNodeRefs...) }
func (u RenderUnit) SiteRef() (string, bool)   { return optionalAccessor(u.siteRef) }
func (u RenderUnit) NodeRef() (string, bool)   { return optionalAccessor(u.nodeRef) }
func (u RenderUnit) DaemonRef() (string, bool) { return optionalAccessor(u.daemonRef) }
func (u RenderUnit) DaemonInstanceRef() (string, bool) {
	return optionalAccessor(u.daemonInstanceRef)
}

// DaemonEngine and DaemonSocketPath expose the exact node-scoped daemon
// binding only for one-per-daemon instances. Other placement shapes return
// false rather than inheriting or guessing host runtime metadata.
func (u RenderUnit) DaemonEngine() (string, bool)     { return optionalAccessor(u.daemonEngine) }
func (u RenderUnit) DaemonSocketPath() (string, bool) { return optionalAccessor(u.daemonSocketPath) }
func (u RenderUnit) ValuesJSON() []byte               { return append([]byte(nil), u.valuesJSON...) }
func (u RenderUnit) SecretRefsJSON() []byte           { return append([]byte(nil), u.secretRefsJSON...) }
func (u RenderUnit) PlacementJSON() []byte            { return append([]byte(nil), u.placementJSON...) }

// ServiceEndpointsJSON returns the exact catalog-owned backend contracts for
// this logical render unit. Renderers may consume them but cannot infer,
// discover, or widen endpoint identity, protocol, port, exposure, or locality.
func (u RenderUnit) ServiceEndpointsJSON() []byte {
	return append([]byte(nil), u.serviceEndpointsJSON...)
}
func (u RenderUnit) ProvidedInterfacesJSON() []byte {
	return append([]byte(nil), u.providedInterfacesJSON...)
}
func (u RenderUnit) RequiredInterfacesJSON() []byte {
	return append([]byte(nil), u.requiredInterfacesJSON...)
}

// RuntimeNetworkBindingsJSON returns only the exact, reciprocal network
// memberships bound to this render instance. It never exposes the global
// runtime-network graph and therefore cannot be used to derive membership
// from a logical networkRef.
func (u RenderUnit) RuntimeNetworkBindingsJSON() []byte {
	return append([]byte(nil), u.runtimeNetworkBindingsJSON...)
}
func (u RenderUnit) DeclaredOutputs() []string { return append([]string(nil), u.declaredOutputRef...) }

func optionalAccessor(value string) (string, bool) { return value, value != "" }

// UnitOutput carries bytes for exactly one declared render-unit output ref.
type UnitOutput struct {
	Ref   string
	Bytes []byte
}

// Artifact is one governed generated file. Path is relative to the deployment
// workspace and always uses portable slash separators.
type Artifact struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Format     string `json:"format"`
	Mode       string `json:"mode"`
	ModuleID   string `json:"moduleId,omitempty"`
	UnitID     string `json:"unitId,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	OutputRef  string `json:"outputRef,omitempty"`
	Bytes      []byte `json:"bytes"`
}

// RenderResult is bound to the exact authorized plan that produced it. Its
// fields are private so production installation cannot be fed an arbitrary
// artifact collection.
type RenderResult struct {
	binding   generationartifact.PlanBinding
	artifacts []Artifact
}

// Artifacts returns a deep defensive copy in deterministic order.
func (r RenderResult) Artifacts() []Artifact {
	return cloneArtifacts(r.artifacts)
}

// MarshalCanonical emits deterministic result bytes. Artifact order is fixed
// by Render, struct field order is fixed here, and byte slices use JSON's
// stable base64 representation.
func (r RenderResult) MarshalCanonical() ([]byte, error) {
	if len(r.artifacts) == 0 {
		return nil, fail(ErrInvalidPlan, "renderResult", "result is empty")
	}
	return json.Marshal(struct {
		Binding   generationartifact.PlanBinding `json:"binding"`
		Artifacts []Artifact                     `json:"artifacts"`
	}{Binding: r.binding, Artifacts: cloneArtifacts(r.artifacts)})
}

// RenderVerifiedPlan is the plan-pure renderer kernel. It performs no
// filesystem mutation and accepts only the immutable result of the governed
// CUE contract verifier. The Architecture v2 service owns the authorization
// session that may call this kernel and any later installation transaction.
func RenderVerifiedPlan(ctx context.Context, plan generationartifact.VerifiedPlan, registry *Registry) (RenderResult, error) {
	projection, err := parseVerifiedPlan(plan)
	if err != nil {
		return RenderResult{}, err
	}
	return renderProjection(ctx, projection, plan.Canonical(), plan.Binding(), registry)
}

// renderProjection is an unexported, byte-pure seam used to prove instance
// invocation semantics without creating a bypass around VerifiedPlan for
// production callers.
func renderProjection(ctx context.Context, projection renderPlan, canonical []byte, binding generationartifact.PlanBinding, registry *Registry) (RenderResult, error) {
	if registry == nil {
		return RenderResult{}, fail(ErrUnknownRenderer, "renderer.registry", "registry is required")
	}

	artifacts := make([]Artifact, 0, len(projection.artifacts))
	resolvedPlanArtifact, exists := projection.artifacts["resolved-plan"]
	if !exists {
		return RenderResult{}, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "resolved-plan artifact is missing")
	}
	artifacts = append(artifacts, Artifact{
		ID: resolvedPlanArtifact.id, Path: resolvedPlanArtifact.path, Kind: resolvedPlanArtifact.kind,
		Format: resolvedPlanArtifact.format, Mode: resolvedPlanArtifact.mode, Bytes: append([]byte(nil), canonical...),
	})

	for _, module := range projection.modules {
		for _, contract := range module.units {
			renderer, found := registry.exact(contract)
			if !found {
				return RenderResult{}, fail(ErrUnknownRenderer, "resolvedPlan.modules."+module.id+".renderUnits."+contract.id, "renderer contract %s/%s@%s (%s) is not registered exactly", contract.rendererRef, contract.templateRef, contract.version, contract.contractHash)
			}
			for _, instance := range contract.instances {
				unit := newRenderUnit(module.id, contract, instance)
				outputs, err := renderUnit(ctx, renderer, unit)
				if err != nil {
					return RenderResult{}, err
				}
				for _, output := range outputs {
					key := instanceOutputKey{moduleID: module.id, unitID: contract.id, instanceID: instance.id, output: output.Ref}
					binding, declared := projection.bindings[key]
					if !declared {
						return RenderResult{}, fail(ErrUndeclaredOutput, fmt.Sprintf("resolvedPlan.modules.%s.renderUnits.%s.instances.%s.outputs", module.id, contract.id, instance.id), "renderer produced unbound logical output %q", output.Ref)
					}
					artifact, declared := projection.artifacts[binding.artifactID]
					if !declared {
						return RenderResult{}, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "instance binding references undeclared artifact %q", binding.artifactID)
					}
					artifacts = append(artifacts, Artifact{
						ID: artifact.id, Path: artifact.path, Kind: artifact.kind, Format: artifact.format, Mode: artifact.mode,
						ModuleID: module.id, UnitID: contract.id, InstanceID: instance.id, OutputRef: output.Ref, Bytes: append([]byte(nil), output.Bytes...),
					})
				}
			}
		}
	}
	if err := validateRenderedArtifacts(projection, artifacts); err != nil {
		return RenderResult{}, err
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Path == artifacts[j].Path {
			return artifacts[i].ID < artifacts[j].ID
		}
		return artifacts[i].Path < artifacts[j].Path
	})
	return RenderResult{binding: binding, artifacts: artifacts}, nil
}

// ValidateManagedOutput is the pure handoff contract from rendering to the
// Architecture v2 execution boundary. It proves that result belongs to plan,
// contains the complete governed artifact set, and returns the portable
// managed output root. It never opens or mutates a filesystem path.
func ValidateManagedOutput(plan generationartifact.VerifiedPlan, result RenderResult) (string, error) {
	if result.binding != plan.Binding() || len(result.artifacts) == 0 {
		return "", fail(ErrAuthorization, "renderResult.binding", "result was not produced for the authorized plan")
	}
	projection, err := parseVerifiedPlan(plan)
	if err != nil {
		return "", err
	}
	if err := validateRenderedArtifacts(projection, result.artifacts); err != nil {
		return "", err
	}
	return projection.outputRoot, nil
}

func newRenderUnit(moduleID string, contract renderUnitContract, instance renderUnitInstance) RenderUnit {
	return RenderUnit{
		moduleID: moduleID, id: contract.id, instanceID: instance.id, instanceScope: instance.scope,
		siteRef: instance.siteRef, nodeRef: instance.nodeRef, daemonRef: instance.daemonRef, daemonInstanceRef: instance.daemonInstanceRef,
		daemonEngine: instance.daemonEngine, daemonSocketPath: instance.daemonSocketPath,
		kind: contract.kind, rendererRef: contract.rendererRef,
		templateRef: contract.templateRef, version: contract.version, contractHash: contract.contractHash,
		publicInputRefs: append([]string(nil), contract.publicInputRefs...), secretInputRefs: append([]string(nil), contract.secretInputRefs...),
		logicalSiteRefs: append([]string(nil), contract.siteRefs...), logicalNodeRefs: append([]string(nil), contract.nodeRefs...),
		valuesJSON: append([]byte(nil), contract.valuesCanonical...), secretRefsJSON: append([]byte(nil), contract.secretsCanonical...),
		placementJSON:              append([]byte(nil), contract.placementCanonical...),
		serviceEndpointsJSON:       append([]byte(nil), contract.serviceEndpointsCanonical...),
		providedInterfacesJSON:     append([]byte(nil), contract.providedInterfacesCanonical...),
		requiredInterfacesJSON:     append([]byte(nil), contract.requiredInterfacesCanonical...),
		runtimeNetworkBindingsJSON: append([]byte(nil), instance.networkCanonical...),
		declaredOutputRef:          instanceLogicalOutputRefs(instance.outputs),
	}
}

func instanceLogicalOutputRefs(outputs []renderInstanceOutput) []string {
	refs := make([]string, len(outputs))
	for index, output := range outputs {
		refs[index] = output.ref
	}
	return refs
}

// renderUnit is intentionally unexported so narrow unit tests can exercise an
// implementation without weakening the authorized production boundary.
func renderUnit(ctx context.Context, renderer UnitRenderer, unit RenderUnit) ([]UnitOutput, error) {
	if renderer == nil {
		return nil, fail(ErrUnknownRenderer, "renderer", "implementation is required")
	}
	outputs, err := renderer.RenderUnit(ctx, unit)
	if err != nil {
		return nil, wrap(ErrRendererFailure, unit.moduleID+"/"+unit.id+"/"+unit.instanceID, "renderer returned an error", err)
	}
	declared := make(map[string]struct{}, len(unit.declaredOutputRef))
	for _, output := range unit.declaredOutputRef {
		declared[output] = struct{}{}
	}
	seen := make(map[string]struct{}, len(outputs))
	normalized := make([]UnitOutput, 0, len(outputs))
	for index, output := range outputs {
		if _, err := validatePortablePath(output.Ref); err != nil {
			return nil, wrap(ErrInvalidPath, fmt.Sprintf("renderer.outputs[%d].ref", index), "invalid output ref", err)
		}
		key := output.Ref
		if _, exists := declared[key]; !exists {
			return nil, fail(ErrUndeclaredOutput, fmt.Sprintf("renderer.outputs[%d].ref", index), "output %q is not declared by the render unit", output.Ref)
		}
		if _, exists := seen[key]; exists {
			return nil, fail(ErrDuplicate, fmt.Sprintf("renderer.outputs[%d].ref", index), "output %q was returned more than once", output.Ref)
		}
		seen[key] = struct{}{}
		bytes := append([]byte(nil), output.Bytes...)
		if bytes == nil {
			bytes = []byte{}
		}
		normalized = append(normalized, UnitOutput{Ref: output.Ref, Bytes: bytes})
	}
	for _, output := range unit.declaredOutputRef {
		if _, exists := seen[output]; !exists {
			return nil, fail(ErrMissingOutput, "renderer.outputs", "renderer did not produce declared output %q", output)
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Ref < normalized[j].Ref })
	return normalized, nil
}

func validateRenderedArtifacts(plan renderPlan, artifacts []Artifact) error {
	seenIDs := make(map[string]struct{}, len(artifacts))
	seenPaths := make(map[string]struct{}, len(artifacts))
	for index, artifact := range artifacts {
		artifactPath := fmt.Sprintf("renderResult.artifacts[%d]", index)
		contract, exists := plan.artifacts[artifact.ID]
		if !exists {
			return fail(ErrUndeclaredOutput, artifactPath+".id", "artifact %q is not declared", artifact.ID)
		}
		if err := validateRenderedArtifactContract(contract, artifact, artifactPath); err != nil {
			return err
		}
		idKey, pathKey := artifact.ID, portableKey(artifact.Path)
		if _, exists := seenIDs[idKey]; exists {
			return fail(ErrDuplicate, artifactPath+".id", "artifact %q appears more than once", artifact.ID)
		}
		if _, exists := seenPaths[pathKey]; exists {
			return fail(ErrDuplicate, artifactPath+".path", "path %q appears more than once", artifact.Path)
		}
		seenIDs[idKey], seenPaths[pathKey] = struct{}{}, struct{}{}
	}
	for _, contract := range plan.artifacts {
		if contract.required {
			if _, exists := seenIDs[contract.id]; !exists {
				return fail(ErrMissingOutput, "renderResult.artifacts", "required artifact %q was not rendered", contract.id)
			}
		}
	}
	return nil
}

func validateRenderedArtifactContract(contract artifactContract, artifact Artifact, artifactPath string) error {
	if contract.path != artifact.Path || contract.kind != artifact.Kind || contract.format != artifact.Format || contract.mode != artifact.Mode {
		return fail(ErrOutputChanged, artifactPath, "artifact metadata differs from the governed contract")
	}
	if contract.owner.kind == "plan" {
		if artifact.ModuleID != "" || artifact.UnitID != "" || artifact.InstanceID != "" || artifact.OutputRef != "" {
			return fail(ErrOutputChanged, artifactPath, "plan-owned artifact carries render-instance identity")
		}
		return nil
	}
	if artifact.ModuleID != contract.owner.moduleRef || artifact.UnitID != contract.owner.unitRef || artifact.InstanceID != contract.owner.instanceRef || artifact.OutputRef != contract.owner.outputRef {
		return fail(ErrOutputChanged, artifactPath, "artifact render-instance ownership differs from the governed contract")
	}
	return nil
}

func cloneArtifacts(artifacts []Artifact) []Artifact {
	result := make([]Artifact, len(artifacts))
	for index, artifact := range artifacts {
		result[index] = artifact
		result[index].Bytes = append([]byte(nil), artifact.Bytes...)
	}
	return result
}
