package architecturev2

import (
	"context"
	"fmt"

	"github.com/kombifyio/stackkits/internal/rilactionv2"
)

type rilActionExecutorFactory func(*Service, CurrentResolution, RILActionValidation, rilaction.ExecutorIdentity) (rilaction.Executor, error)

// rilActionExecutorRegistry is immutable construction-owned runtime wiring.
// CUE selects an exact identity; this registry can only prove whether this
// binary owns that same identity and prepare it for one admitted invocation.
type rilActionExecutorRegistry struct {
	entries map[string]rilActionExecutorRegistration
}

type rilActionExecutorRegistration struct {
	identity rilaction.ExecutorIdentity
	factory  rilActionExecutorFactory
}

func newRILActionExecutorRegistry(catalog []RILActionExecutorCatalogEntry) (*rilActionExecutorRegistry, error) {
	registry := &rilActionExecutorRegistry{entries: make(map[string]rilActionExecutorRegistration)}
	for _, contract := range catalog {
		if contract.Ref != rilGovernedStateVerifierRef {
			continue
		}
		identity := contract.identity()
		if err := rilaction.ValidateExecutorIdentity(identity); err != nil {
			return nil, fmt.Errorf("register governed state verifier: %w", err)
		}
		registry.entries[identity.Ref] = rilActionExecutorRegistration{
			identity: identity,
			factory: func(service *Service, current CurrentResolution, validation RILActionValidation, selected rilaction.ExecutorIdentity) (rilaction.Executor, error) {
				return &governedStateVerifierExecutor{
					service: service, current: current, validation: validation, identity: selected,
				}, nil
			},
		}
	}
	return registry, nil
}

func (r *rilActionExecutorRegistry) owns(identity rilaction.ExecutorIdentity) bool {
	if r == nil {
		return false
	}
	registration, exists := r.entries[identity.Ref]
	return exists && registration.identity == identity
}

func (r *rilActionExecutorRegistry) prepare(service *Service, current CurrentResolution, validation RILActionValidation, identity rilaction.ExecutorIdentity) (rilaction.Executor, error) {
	if !r.owns(identity) {
		return nil, fmt.Errorf("CUE-selected RIL action executor is not owned by this service")
	}
	registration := r.entries[identity.Ref]
	executor, err := registration.factory(service, current, validation, identity)
	if err != nil {
		return nil, err
	}
	if executor == nil || executor.Identity() != identity {
		return nil, fmt.Errorf("prepared RIL action executor identity does not match CUE authority")
	}
	return executor, nil
}

type governedStateVerifierExecutor struct {
	service    *Service
	current    CurrentResolution
	validation RILActionValidation
	identity   rilaction.ExecutorIdentity
}

func (e *governedStateVerifierExecutor) Identity() rilaction.ExecutorIdentity {
	if e == nil {
		return rilaction.ExecutorIdentity{}
	}
	return e.identity
}

func (e *governedStateVerifierExecutor) Execute(_ context.Context, invocation rilaction.ExecutorInvocation) (rilaction.Evidence, error) {
	if e == nil || e.service == nil {
		return rilaction.Evidence{}, fmt.Errorf("governed state verifier is not initialized")
	}
	if err := rilaction.ValidateExecutorInvocation(invocation); err != nil {
		return rilaction.Evidence{}, err
	}
	return e.service.executeGovernedStateVerification(e.current, invocation.Request(), e.validation)
}
