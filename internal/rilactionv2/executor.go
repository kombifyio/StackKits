package rilaction

import (
	"context"
	"time"
)

// ExecutorIdentity is the exact product-selected runtime-owner contract.
// ContractHash is supplied by the product authority (for StackKits, CUE); it
// is not an implementation binary digest or an external attestation.
type ExecutorIdentity struct {
	Ref          string `json:"ref"`
	Version      string `json:"version"`
	ContractHash string `json:"contract_hash"`
}

// ValidateExecutorIdentity validates the closed provider-free owner identity.
func ValidateExecutorIdentity(identity ExecutorIdentity) error {
	if err := validateContractID("executor.ref", identity.Ref); err != nil {
		return err
	}
	if err := validateStableID("executor.version", identity.Version); err != nil {
		return err
	}
	if err := validateDigest("executor.contract_hash", identity.ContractHash); err != nil {
		return err
	}
	return nil
}

// ExecutorInvocation is an immutable in-process handoff created only after the
// product has admitted the exact request. It intentionally exposes no
// executor selection, provider, lease, endpoint, credential, transport,
// command, path, or raw runtime authority.
type ExecutorInvocation struct {
	request       Request
	requestDigest string
	evaluatedAt   string
}

// NewExecutorInvocation binds one defensively copied request to its exact
// digest and the same trusted UTC instant used for final invocation.
func NewExecutorInvocation(request Request, requestDigest string, evaluatedAt time.Time) (ExecutorInvocation, error) {
	if evaluatedAt.Location() != time.UTC {
		return ExecutorInvocation{}, invalid("executor.evaluated_at", "must use a trusted UTC instant")
	}
	if err := ValidateRequestAt(request, evaluatedAt); err != nil {
		return ExecutorInvocation{}, err
	}
	computed, err := ComputeRequestDigest(request)
	if err != nil {
		return ExecutorInvocation{}, err
	}
	if requestDigest != computed {
		return ExecutorInvocation{}, invalid("executor.request_digest", "does not match the approved request")
	}
	invocation := ExecutorInvocation{
		request: cloneExecutorRequest(request), requestDigest: computed,
		evaluatedAt: evaluatedAt.Format(time.RFC3339Nano),
	}
	if err := ValidateExecutorInvocation(invocation); err != nil {
		return ExecutorInvocation{}, err
	}
	return invocation, nil
}

// ValidateExecutorInvocation revalidates deterministic closure and freshness
// at the captured invocation instant. Executors should call it immediately
// before their own side effects.
func ValidateExecutorInvocation(invocation ExecutorInvocation) error {
	evaluatedAt, err := parseTimestamp("executor.evaluated_at", invocation.evaluatedAt)
	if err != nil {
		return err
	}
	if err := ValidateRequestAt(invocation.request, evaluatedAt); err != nil {
		return err
	}
	computed, err := ComputeRequestDigest(invocation.request)
	if err != nil {
		return err
	}
	if invocation.requestDigest != computed {
		return invalid("executor.request_digest", "does not match the approved request")
	}
	return nil
}

// Request returns a defensive copy of the already admitted request.
func (i ExecutorInvocation) Request() Request {
	return cloneExecutorRequest(i.request)
}

// RequestDigest returns the exact canonical digest of Request.
func (i ExecutorInvocation) RequestDigest() string {
	return i.requestDigest
}

// EvaluatedAt returns the canonical RFC3339Nano UTC invocation instant.
func (i ExecutorInvocation) EvaluatedAt() string {
	return i.evaluatedAt
}

// Executor executes one already admitted provider-free action through the
// product-selected owner. The product remains responsible for selecting and
// validating Identity against its own authority before calling Execute.
type Executor interface {
	Identity() ExecutorIdentity
	Execute(context.Context, ExecutorInvocation) (Evidence, error)
}

func cloneExecutorRequest(request Request) Request {
	clone := request
	clone.Grant.Scopes = append([]string(nil), request.Grant.Scopes...)
	clone.Inputs = make([]Input, len(request.Inputs))
	for index, input := range request.Inputs {
		clone.Inputs[index] = input
		if input.Boolean != nil {
			value := *input.Boolean
			clone.Inputs[index].Boolean = &value
		}
		if input.Integer != nil {
			value := *input.Integer
			clone.Inputs[index].Integer = &value
		}
	}
	return clone
}
