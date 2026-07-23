package architecturev2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutordispatch"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// ProductRuntimeOwnerSelector is the stable CUE/catalog identity a product
// integration is willing to realize. Dynamic contract hashes, target scope,
// workload payload, artifact refs, and access authority remain in the exact
// RuntimeTarget passed to the factory and bound by OwnerRouter.
type ProductRuntimeOwnerSelector struct {
	OwnerKind               string
	OwnerRef                string
	ProviderRef             string
	ModuleRef               string
	UnitRef                 string
	RuntimeKind             string
	RuntimeDelivery         string
	RuntimeEngine           string
	WorkloadRef             string
	RuntimeAdapterRef       string
	RuntimeAdapterModuleRef string
}

// ProductRuntimeOwnerRequest is the complete provider-free preparation input
// for one exact target. A factory is service-owned and fixed at registry
// construction; Apply callers cannot provide or replace it.
type ProductRuntimeOwnerRequest struct {
	Target        runtimeexecutor.RuntimeTarget
	HealthTargets []runtimeexecutor.HealthTarget
}

// ProductRuntimeOwnerFactory prepares one typed executor. Preparation must be
// free of target mutation; actual operations occur only through Execute.
type ProductRuntimeOwnerFactory interface {
	PrepareRuntimeOwner(ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error)
}

// ProductRuntimeOwnerExecutionMode declares whether a selector has a local
// factory in this process or is admitted only through an authenticated remote
// execution channel. Empty is the compatibility spelling of local.
type ProductRuntimeOwnerExecutionMode string

const (
	ProductRuntimeOwnerExecutionLocal      ProductRuntimeOwnerExecutionMode = "local"
	ProductRuntimeOwnerExecutionRemoteOnly ProductRuntimeOwnerExecutionMode = "remote-only"
)

// ProductRuntimeOwnerRegistration binds one closed selector either to one
// local factory or to explicit remote-only channel admission.
type ProductRuntimeOwnerRegistration struct {
	Selector     ProductRuntimeOwnerSelector
	Factory      ProductRuntimeOwnerFactory
	Execution    ProductRuntimeOwnerExecutionMode
	Compensation runtimeapply.CompensationMode
}

// NewProductRemoteRuntimeOwnerRegistration explicitly admits one selector
// only through a service-owned remote channel. It carries no placeholder local
// Operations dependency and cannot be used by a local channel admission.
func NewProductRemoteRuntimeOwnerRegistration(selector ProductRuntimeOwnerSelector) (ProductRuntimeOwnerRegistration, error) {
	if err := validateProductRuntimeOwnerSelector(selector); err != nil {
		return ProductRuntimeOwnerRegistration{}, err
	}
	return ProductRuntimeOwnerRegistration{Selector: selector, Execution: ProductRuntimeOwnerExecutionRemoteOnly}, nil
}

// ProductRuntimeOwnerLocalAdmissionError reports that a selector was
// deliberately registered for authenticated remote execution and therefore
// cannot be materialized by the local channel builder.
type ProductRuntimeOwnerLocalAdmissionError struct {
	RequirementID string
}

func (e *ProductRuntimeOwnerLocalAdmissionError) Error() string {
	if e == nil {
		return "product runtime owner local admission failed"
	}
	return fmt.Sprintf("runtime target %q is registered for remote execution only and has no local factory", e.RequirementID)
}

// ProductExecutionChannelRequest retains the StackKits-facing name for the
// shared provider-free execution-channel contract. StackKits constructs the
// exact target closure; service integrations own local or remote routing.
type ProductExecutionChannelRequest = runtimeexecutor.ExecutionChannelRequest

// ProductExecutionChannelLocalExecutor lazily constructs the exact channel-local
// owner router. A remote channel never needs to call it or possess local
// operations implementations.
type ProductExecutionChannelLocalExecutor = runtimeexecutor.ExecutionChannelLocalExecutor

// ProductExecutionChannelAdmission binds one already admitted channel to an
// executor. A remote admission may return its authenticated transport executor
// without invoking local; an explicit local admission invokes local and may
// wrap its result. Direct execution is never the default.
type ProductExecutionChannelAdmission = runtimeexecutor.ExecutionChannelAdmission

// ProductExecutionChannelFactory admits exact channel/Site/node scopes from
// service-owned state before any owner factory can run.
type ProductExecutionChannelFactory = runtimeexecutor.ExecutionChannelFactory

// ProductRuntimeOwnerRegistry is immutable after construction. It owns no
// provider lifecycle, endpoint, credential, transport, lease, or generation
// authority.
type ProductRuntimeOwnerRegistry struct {
	identity      runtimeexecutor.ExecutorIdentity
	registrations map[ProductRuntimeOwnerSelector]productRuntimeOwnerRegistration
	channels      ProductExecutionChannelFactory
	journal       runtimeapply.Journal
	recovery      ProductApplyRecoveryStore
}

type productRuntimeOwnerRegistration struct {
	factory      ProductRuntimeOwnerFactory
	local        bool
	compensation runtimeapply.CompensationMode
}

type productPreparedRuntimeTarget struct {
	target   runtimeexecutor.RuntimeTarget
	executor runtimeexecutor.Executor
	identity runtimeexecutor.ExecutorIdentity
	mode     runtimeapply.CompensationMode
}

type productPlannedRuntimeTarget struct {
	target       runtimeexecutor.RuntimeTarget
	health       []runtimeexecutor.HealthTarget
	factory      ProductRuntimeOwnerFactory
	localFactory bool
	mode         runtimeapply.CompensationMode
}

// NewProductRuntimeOwnerRegistry constructs a service-owned exact registry.
func NewProductRuntimeOwnerRegistry(identity runtimeexecutor.ExecutorIdentity, registrations []ProductRuntimeOwnerRegistration, channels ProductExecutionChannelFactory) (*ProductRuntimeOwnerRegistry, error) {
	return newProductRuntimeOwnerRegistry(identity, registrations, channels, nil, nil)
}

// NewProductRuntimeOwnerRegistryWithJournal fixes the integration-owned
// provider-neutral durable journal at construction. Apply callers cannot
// provide or replace it.
func NewProductRuntimeOwnerRegistryWithJournal(identity runtimeexecutor.ExecutorIdentity, registrations []ProductRuntimeOwnerRegistration, channels ProductExecutionChannelFactory, journal runtimeapply.Journal) (*ProductRuntimeOwnerRegistry, error) {
	if nilProductRuntimeOwnerValue(journal) {
		return nil, errors.New("product runtime-owner registry requires a journal")
	}
	return newProductRuntimeOwnerRegistry(identity, registrations, channels, journal, nil)
}

// NewProductRuntimeOwnerRegistryWithRecovery fixes both durable operation state
// and exact pre-mutation request custody at construction.
func NewProductRuntimeOwnerRegistryWithRecovery(identity runtimeexecutor.ExecutorIdentity, registrations []ProductRuntimeOwnerRegistration, channels ProductExecutionChannelFactory, journal runtimeapply.Journal, recovery ProductApplyRecoveryStore) (*ProductRuntimeOwnerRegistry, error) {
	if nilProductRuntimeOwnerValue(journal) || nilProductRuntimeOwnerValue(recovery) {
		return nil, errors.New("product runtime-owner registry requires a journal and recovery store")
	}
	return newProductRuntimeOwnerRegistry(identity, registrations, channels, journal, recovery)
}

func newProductRuntimeOwnerRegistry(identity runtimeexecutor.ExecutorIdentity, registrations []ProductRuntimeOwnerRegistration, channels ProductExecutionChannelFactory, journal runtimeapply.Journal, recovery ProductApplyRecoveryStore) (*ProductRuntimeOwnerRegistry, error) {
	if err := generationartifact.ValidateApplyExecutorIdentity(generationartifact.ApplyExecutorIdentity{
		ID: identity.ID, Version: identity.Version, Digest: identity.Digest,
	}); err != nil {
		return nil, fmt.Errorf("product runtime-owner registry identity is invalid: %w", err)
	}
	if len(registrations) == 0 {
		return nil, errors.New("product runtime-owner registry requires at least one registration")
	}
	if nilProductRuntimeOwnerValue(channels) {
		return nil, errors.New("product runtime-owner registry requires an execution-channel factory")
	}
	registered := make(map[ProductRuntimeOwnerSelector]productRuntimeOwnerRegistration, len(registrations))
	for index, registration := range registrations {
		if err := validateProductRuntimeOwnerSelector(registration.Selector); err != nil {
			return nil, fmt.Errorf("product runtime-owner registration %d: %w", index, err)
		}
		if _, duplicate := registered[registration.Selector]; duplicate {
			return nil, fmt.Errorf("product runtime-owner selector %#v is registered more than once", registration.Selector)
		}
		execution := registration.Execution
		if execution == "" {
			execution = ProductRuntimeOwnerExecutionLocal
		}
		local := false
		switch execution {
		case ProductRuntimeOwnerExecutionLocal:
			if nilProductRuntimeOwnerFactory(registration.Factory) {
				return nil, fmt.Errorf("product runtime-owner registration %d has no local factory", index)
			}
			local = true
		case ProductRuntimeOwnerExecutionRemoteOnly:
			if !nilProductRuntimeOwnerFactory(registration.Factory) {
				return nil, fmt.Errorf("product runtime-owner registration %d is remote-only and must not carry a local factory", index)
			}
		default:
			return nil, fmt.Errorf("product runtime-owner registration %d has unsupported execution mode %q", index, execution)
		}
		mode := registration.Compensation
		if mode == "" {
			mode = runtimeapply.CompensationNone
		}
		if mode != runtimeapply.CompensationNone && mode != runtimeapply.CompensationExplicit {
			return nil, fmt.Errorf("product runtime-owner registration %d has unsupported compensation mode", index)
		}
		registered[registration.Selector] = productRuntimeOwnerRegistration{
			factory: registration.Factory, local: local, compensation: mode,
		}
	}
	return &ProductRuntimeOwnerRegistry{identity: identity, registrations: registered, channels: channels, journal: journal, recovery: recovery}, nil
}

func (r *ProductRuntimeOwnerRegistry) storeProductApplyRecovery(ctx context.Context, digest string, canonical []byte) error {
	if r == nil || nilProductRuntimeOwnerValue(r.recovery) {
		return errors.New("product runtime-owner registry has no recovery store")
	}
	return safeSaveProductApplyRecovery(r.recovery, ctx, digest, canonical)
}

// reconcileProductApply resumes one exact custody-bound request after process
// restart. It is deliberately package-private until the product Service adds
// held-workspace/output revalidation around it.
func (r *ProductRuntimeOwnerRegistry) reconcileProductApply(ctx context.Context, requestDigest string, at time.Time) (runtimeexecutor.ExecutionResult, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionResult{}, errors.New("Product Apply reconcile requires a context")
	}
	if r == nil || nilProductRuntimeOwnerValue(r.recovery) {
		return runtimeexecutor.ExecutionResult{}, errors.New("Product Apply reconcile requires recovery custody")
	}
	if at.IsZero() || at.Location() != time.UTC {
		return runtimeexecutor.ExecutionResult{}, errors.New("Product Apply reconcile requires an exact UTC instant")
	}
	canonical, err := safeLoadProductApplyRecovery(r.recovery, ctx, requestDigest)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, err
	}
	capsule, err := parseProductApplyRecoveryCapsule(canonical)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, err
	}
	validUntil, err := time.Parse(time.RFC3339Nano, capsule.ValidUntil)
	if err != nil || !at.Before(validUntil) {
		return runtimeexecutor.ExecutionResult{}, errors.New("Product Apply recovery authority is expired")
	}
	if len(capsule.Shared.AccessBindings) != 0 {
		return runtimeexecutor.ExecutionResult{}, errors.New("access-bound Product Apply recovery requires a versioned fresh-instant continuation contract")
	}
	if capsule.Shared.Executor != r.identity {
		return runtimeexecutor.ExecutionResult{}, errors.New("Product Apply recovery request does not bind the service-owned registry identity")
	}
	return runtimeexecutor.InvokeAt(ctx, r, capsule.Shared, at)
}

// Identity returns the immutable product-owned root executor identity. It is
// fixed before authorization and cannot be selected by an Apply request.
func (r *ProductRuntimeOwnerRegistry) Identity() runtimeexecutor.ExecutorIdentity {
	if r == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return r.identity
}

// Execute realizes the already-sealed request only through the immutable
// service-owned registry. Complete factory/routing preflight finishes before
// the nested dispatcher can invoke a child.
func (r *ProductRuntimeOwnerRegistry) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("product runtime-owner registry requires a context")
	}
	dispatcher, err := r.prepare(request)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	var result runtimeexecutor.ExecutionResult
	if len(request.AccessBindings) == 0 {
		result, err = runtimeexecutor.Invoke(ctx, dispatcher, request)
	} else {
		authorizationTime, parseErr := time.Parse(time.RFC3339Nano, request.AuthorizationTime)
		if parseErr != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("parse product runtime authorization time: %w", parseErr)
		}
		result, err = runtimeexecutor.InvokeAt(ctx, dispatcher, request, authorizationTime)
	}
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	return runtimeexecutor.ExecutionOutcome{
		Runtime: append([]runtimeexecutor.RuntimeOutcome(nil), result.Runtime...),
		Health:  append([]runtimeexecutor.HealthOutcome(nil), result.Health...),
	}, nil
}

// prepare validates the complete sealed target set and constructs the nested
// channel -> owner dispatcher before any child executor can be called.
func (r *ProductRuntimeOwnerRegistry) prepare(request runtimeexecutor.ExecutionRequest) (runtimeexecutor.Executor, error) {
	if r == nil || len(r.registrations) == 0 || nilProductRuntimeOwnerValue(r.channels) {
		return nil, errors.New("product runtime-owner registry is not initialized")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("validate product runtime-owner request: %w", err)
	}
	if request.Executor != r.identity {
		return nil, errors.New("product runtime-owner request does not bind the service-owned registry identity")
	}

	planned := make([]productPlannedRuntimeTarget, 0, len(request.RuntimeTargets))
	channelScopes := make(map[string][2]string)
	healthByRuntime, err := productRuntimeOwnerHealthAssignments(request.RuntimeTargets, request.HealthTargets)
	if err != nil {
		return nil, err
	}
	for _, target := range request.RuntimeTargets {
		if strings.TrimSpace(target.ExecutionChannelRef) == "" || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 {
			return nil, fmt.Errorf("runtime target %q is not bound to one execution channel and Site/node", target.RequirementID)
		}
		scope := [2]string{target.SiteRefs[0], target.NodeRefs[0]}
		if existing, found := channelScopes[target.ExecutionChannelRef]; found && existing != scope {
			return nil, fmt.Errorf("execution channel %q spans multiple Site/node authorities", target.ExecutionChannelRef)
		}
		channelScopes[target.ExecutionChannelRef] = scope
		selector := productRuntimeOwnerSelectorForTarget(target)
		registration, exists := r.registrations[selector]
		if !exists {
			return nil, fmt.Errorf("runtime target %q has no service-owned registration", target.RequirementID)
		}
		planned = append(planned, productPlannedRuntimeTarget{
			target: target, health: healthByRuntime[target.RequirementID], factory: registration.factory,
			localFactory: registration.local, mode: registration.compensation,
		})
	}
	channelAdmissions, err := admitProductExecutionChannels(planned, r.channels)
	if err != nil {
		return nil, err
	}

	byChannel := make(map[string][]productPlannedRuntimeTarget)
	for _, target := range planned {
		byChannel[target.target.ExecutionChannelRef] = append(byChannel[target.target.ExecutionChannelRef], target)
	}

	channels := make([]string, 0, len(byChannel))
	for channelRef := range byChannel {
		channels = append(channels, channelRef)
	}
	sort.Strings(channels)
	routes := make([]runtimeexecutordispatch.Route, 0, len(channels))
	for _, channelRef := range channels {
		channelTargets := append([]productPlannedRuntimeTarget(nil), byChannel[channelRef]...)
		channelExecutor, err := safelyPrepareProductExecutionChannel(channelAdmissions[channelRef], func() (runtimeexecutor.Executor, error) {
			return prepareProductLocalExecutionChannel(request.Executor.Version, channelRef, channelTargets, r.journal)
		})
		if err != nil {
			return nil, fmt.Errorf("prepare execution channel %q: %w", channelRef, err)
		}
		routes = append(routes, runtimeexecutordispatch.Route{ChannelRef: channelRef, Executor: channelExecutor})
	}
	if nilProductRuntimeOwnerValue(r.journal) {
		return runtimeexecutordispatch.New(request.Executor, routes)
	}
	return runtimeexecutordispatch.NewWithJournal(request.Executor, routes, r.journal)
}

func prepareProductLocalExecutionChannel(version, channelRef string, planned []productPlannedRuntimeTarget, journal runtimeapply.Journal) (runtimeexecutor.Executor, error) {
	for _, target := range planned {
		if !target.localFactory || nilProductRuntimeOwnerFactory(target.factory) {
			return nil, &ProductRuntimeOwnerLocalAdmissionError{RequirementID: target.target.RequirementID}
		}
	}
	prepared := make([]productPreparedRuntimeTarget, 0, len(planned))
	for _, target := range planned {
		executor, err := safelyPrepareProductRuntimeOwner(target.factory, ProductRuntimeOwnerRequest{
			Target:        cloneProductRuntimeTarget(target.target),
			HealthTargets: cloneProductHealthTargets(target.health),
		})
		if err != nil {
			return nil, fmt.Errorf("prepare runtime owner for %q: %w", target.target.RequirementID, err)
		}
		identity, err := safeProductRuntimeExecutorIdentity(executor)
		if err != nil {
			return nil, fmt.Errorf("runtime owner for %q: %w", target.target.RequirementID, err)
		}
		prepared = append(prepared, productPreparedRuntimeTarget{
			target: target.target, executor: executor, identity: identity, mode: target.mode,
		})
	}
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].target.RequirementID < prepared[j].target.RequirementID })
	ownerRoutes := make([]runtimeexecutordispatch.OwnerRoute, len(prepared))
	for index, child := range prepared {
		ownerRoutes[index] = runtimeexecutordispatch.OwnerRoute{
			Target: child.target, Executor: child.executor, Compensation: child.mode,
		}
	}
	identity, err := productRuntimeOwnerRouterIdentity(version, channelRef, prepared)
	if err != nil {
		return nil, err
	}
	if nilProductRuntimeOwnerValue(journal) {
		return runtimeexecutordispatch.NewOwnerRouter(identity, ownerRoutes)
	}
	return runtimeexecutordispatch.NewOwnerRouterWithJournal(identity, ownerRoutes, journal)
}

func admitProductExecutionChannels(planned []productPlannedRuntimeTarget, factory ProductExecutionChannelFactory) (map[string]ProductExecutionChannelAdmission, error) {
	requests := make(map[string]*ProductExecutionChannelRequest)
	for _, item := range planned {
		channelRef := item.target.ExecutionChannelRef
		request := requests[channelRef]
		if request == nil {
			request = &ProductExecutionChannelRequest{
				ChannelRef: channelRef, SiteRef: item.target.SiteRefs[0], NodeRef: item.target.NodeRefs[0],
			}
			requests[channelRef] = request
		}
		request.RuntimeTargets = append(request.RuntimeTargets, cloneProductRuntimeTarget(item.target))
		request.HealthTargets = append(request.HealthTargets, cloneProductHealthTargets(item.health)...)
	}
	channels := make([]string, 0, len(requests))
	for channelRef := range requests {
		channels = append(channels, channelRef)
	}
	sort.Strings(channels)
	result := make(map[string]ProductExecutionChannelAdmission, len(channels))
	for _, channelRef := range channels {
		request := cloneProductExecutionChannelRequest(*requests[channelRef])
		admission, err := safelyAdmitProductExecutionChannel(factory, request)
		if err != nil {
			return nil, fmt.Errorf("admit execution channel %q: %w", channelRef, err)
		}
		result[channelRef] = admission
	}
	return result, nil
}

func safelyAdmitProductExecutionChannel(factory ProductExecutionChannelFactory, request ProductExecutionChannelRequest) (admission ProductExecutionChannelAdmission, err error) {
	defer func() {
		if recover() != nil {
			admission = nil
			err = errors.New("execution-channel factory panicked")
		}
	}()
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("execution-channel request is invalid: %w", err)
	}
	admission, err = factory.AdmitExecutionChannel(cloneProductExecutionChannelRequest(request))
	if err != nil {
		return nil, err
	}
	if nilProductRuntimeOwnerValue(admission) {
		return nil, errors.New("execution-channel factory returned no admission")
	}
	return admission, nil
}

func safelyPrepareProductExecutionChannel(admission ProductExecutionChannelAdmission, local ProductExecutionChannelLocalExecutor) (executor runtimeexecutor.Executor, err error) {
	defer func() {
		if recover() != nil {
			executor = nil
			err = errors.New("execution-channel admission panicked")
		}
	}()
	if nilProductRuntimeOwnerValue(admission) {
		return nil, errors.New("execution-channel admission is missing")
	}
	if local == nil {
		return nil, errors.New("execution-channel local executor builder is missing")
	}
	localCalls := 0
	var localErr error
	guardedLocal := func() (runtimeexecutor.Executor, error) {
		localCalls++
		if localCalls > 1 {
			localErr = errors.New("execution-channel admission invoked the local executor builder more than once")
			return nil, localErr
		}
		var localExecutor runtimeexecutor.Executor
		localExecutor, localErr = local()
		return localExecutor, localErr
	}
	executor, err = admission.PrepareExecutionChannel(guardedLocal)
	if err != nil {
		return nil, err
	}
	if localErr != nil {
		return nil, localErr
	}
	if localCalls > 1 {
		return nil, errors.New("execution-channel admission invoked the local executor builder more than once")
	}
	if nilRuntimeExecutor(executor) {
		return nil, errors.New("execution-channel admission returned no executor")
	}
	if _, err := safeProductRuntimeExecutorIdentity(executor); err != nil {
		return nil, err
	}
	return executor, nil
}

func productRuntimeOwnerSelectorForTarget(target runtimeexecutor.RuntimeTarget) ProductRuntimeOwnerSelector {
	selector := ProductRuntimeOwnerSelector{
		OwnerKind: target.OwnerKind, OwnerRef: target.OwnerRef, ProviderRef: target.ProviderRef,
		ModuleRef: target.ModuleRef, UnitRef: target.UnitRef, RuntimeKind: target.RuntimeKind,
		RuntimeDelivery: target.RuntimeDelivery, RuntimeEngine: target.RuntimeEngine, WorkloadRef: target.WorkloadRef,
	}
	if target.RuntimeAdapter != nil {
		selector.RuntimeAdapterRef = target.RuntimeAdapter.ID
		selector.RuntimeAdapterModuleRef = target.RuntimeAdapter.ModuleRef
	}
	return selector
}

func validateProductRuntimeOwnerSelector(selector ProductRuntimeOwnerSelector) error {
	values := []string{
		selector.OwnerKind, selector.OwnerRef, selector.ProviderRef, selector.ModuleRef, selector.UnitRef,
		selector.RuntimeKind, selector.RuntimeDelivery, selector.RuntimeEngine, selector.WorkloadRef,
		selector.RuntimeAdapterRef, selector.RuntimeAdapterModuleRef,
	}
	for _, value := range values {
		if value != strings.TrimSpace(value) {
			return errors.New("selector fields must be canonical")
		}
	}
	if selector.OwnerKind == "" || selector.OwnerRef == "" || selector.ProviderRef == "" ||
		selector.RuntimeKind == "" || selector.RuntimeDelivery == "" {
		return errors.New("selector requires owner, provider, runtime kind, and delivery")
	}
	if (selector.RuntimeAdapterRef == "") != (selector.RuntimeAdapterModuleRef == "") {
		return errors.New("runtime adapter selector fields must be paired")
	}
	return nil
}

func productRuntimeOwnerHealthAssignments(targets []runtimeexecutor.RuntimeTarget, health []runtimeexecutor.HealthTarget) (map[string][]runtimeexecutor.HealthTarget, error) {
	result := make(map[string][]runtimeexecutor.HealthTarget, len(targets))
	for _, candidate := range health {
		matchedRequirementID := ""
		matches := 0
		for _, target := range targets {
			if !productHealthTargetsRuntime(candidate, target) {
				continue
			}
			matchedRequirementID = target.RequirementID
			matches++
		}
		if matches != 1 {
			return nil, fmt.Errorf("health target %q has %d exact runtime owners", candidate.RequirementID, matches)
		}
		result[matchedRequirementID] = append(result[matchedRequirementID], candidate)
	}
	for _, target := range targets {
		if len(result[target.RequirementID]) == 0 {
			return nil, fmt.Errorf("runtime target %q has no exact health owner", target.RequirementID)
		}
	}
	return result, nil
}

func productHealthTargetsRuntime(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) bool {
	if len(health.SiteRefs) != 1 || len(health.NodeRefs) != 1 || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		health.SiteRefs[0] != target.SiteRefs[0] || health.NodeRefs[0] != target.NodeRefs[0] {
		return false
	}
	if health.RuntimeRequirementID != "" {
		return health.RuntimeRequirementID == target.RequirementID
	}
	return health.TargetKind == "module" && health.TargetRef == target.ModuleRef ||
		health.TargetKind == "provider" && health.TargetRef == target.ProviderRef ||
		health.TargetKind == "runtime" && health.TargetRef == target.InstanceRef
}

func safelyPrepareProductRuntimeOwner(factory ProductRuntimeOwnerFactory, request ProductRuntimeOwnerRequest) (executor runtimeexecutor.Executor, err error) {
	defer func() {
		if recover() != nil {
			executor = nil
			err = errors.New("runtime-owner factory panicked")
		}
	}()
	executor, err = factory.PrepareRuntimeOwner(request)
	if err != nil {
		return nil, err
	}
	if nilRuntimeExecutor(executor) {
		return nil, errors.New("runtime-owner factory returned no executor")
	}
	return executor, nil
}

func safeProductRuntimeExecutorIdentity(executor runtimeexecutor.Executor) (identity runtimeexecutor.ExecutorIdentity, err error) {
	defer func() {
		if recover() != nil {
			identity = runtimeexecutor.ExecutorIdentity{}
			err = errors.New("runtime-owner executor identity panicked")
		}
	}()
	return executor.Identity(), nil
}

func productRuntimeOwnerRouterIdentity(version, channelRef string, prepared []productPreparedRuntimeTarget) (runtimeexecutor.ExecutorIdentity, error) {
	type component struct {
		Target   runtimeexecutor.RuntimeTarget    `json:"target"`
		Executor runtimeexecutor.ExecutorIdentity `json:"executor"`
	}
	components := make([]component, len(prepared))
	for index, child := range prepared {
		components[index] = component{Target: child.target, Executor: child.identity}
	}
	canonical, err := resolvedplan.CanonicalJSON(struct {
		Contract   string      `json:"contract"`
		ChannelRef string      `json:"channelRef"`
		Components []component `json:"components"`
	}{Contract: "stackkits-runtime-owner-router/v1", ChannelRef: channelRef, Components: components})
	if err != nil {
		return runtimeexecutor.ExecutorIdentity{}, err
	}
	digest := sha256.Sum256(canonical)
	return runtimeexecutor.ExecutorIdentity{
		ID: "stackkits-runtime-owner-router", Version: version, Digest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func cloneProductRuntimeTarget(target runtimeexecutor.RuntimeTarget) runtimeexecutor.RuntimeTarget {
	request := runtimeexecutor.CloneExecutionRequest(runtimeexecutor.ExecutionRequest{RuntimeTargets: []runtimeexecutor.RuntimeTarget{target}})
	return request.RuntimeTargets[0]
}

func cloneProductHealthTargets(health []runtimeexecutor.HealthTarget) []runtimeexecutor.HealthTarget {
	request := runtimeexecutor.CloneExecutionRequest(runtimeexecutor.ExecutionRequest{HealthTargets: health})
	return request.HealthTargets
}

func cloneProductExecutionChannelRequest(request ProductExecutionChannelRequest) ProductExecutionChannelRequest {
	return runtimeexecutor.CloneExecutionChannelRequest(request)
}

func cloneProductRuntimeTargets(targets []runtimeexecutor.RuntimeTarget) []runtimeexecutor.RuntimeTarget {
	request := runtimeexecutor.CloneExecutionRequest(runtimeexecutor.ExecutionRequest{RuntimeTargets: targets})
	return request.RuntimeTargets
}

func nilProductRuntimeOwnerFactory(factory ProductRuntimeOwnerFactory) bool {
	return nilProductRuntimeOwnerValue(factory)
}

func nilRuntimeExecutor(executor runtimeexecutor.Executor) bool {
	return nilProductRuntimeOwnerValue(executor)
}

func nilProductRuntimeOwnerValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
