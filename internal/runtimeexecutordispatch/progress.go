package runtimeexecutordispatch

import (
	"context"
	"time"

	"github.com/kombifyio/stackkits/contracts/runtimeapply"
	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/applyoutcome"
)

// Scope names the routing level of a prepared child.
const (
	// ScopeChannel is one execution-channel child of a Dispatcher.
	ScopeChannel = "channel"
	// ScopeOwner is one runtime-owner child of an OwnerRouter.
	ScopeOwner = "owner"
)

// Owner event statuses are the rollout-event statuses a progress consumer
// already understands: the lifecycle of one child is started, then repeated
// running liveness events, then exactly one of succeeded or failed.
const (
	OwnerStarted   = "started"
	OwnerRunning   = "running"
	OwnerSucceeded = "succeeded"
	OwnerFailed    = "failed"
)

// DefaultOwnerLivenessInterval is the cadence of OwnerRunning events while one
// child executes, when the observer does not choose another.
const DefaultOwnerLivenessInterval = 15 * time.Second

// OwnerEvent is one observation of one prepared child. It carries identities
// and timing only: no request payload, artifact, command line, path or error
// text. Err is the typed cause of a failed child for the receiver to classify;
// it is not meant for serialization.
type OwnerEvent struct {
	Scope    string
	Label    string
	Module   string
	Workload string
	// Position is the 1-based place of the child in the serial execution order.
	Position int
	Total    int
	Targets  int
	Status   string
	// Elapsed is the time since the child started; zero on OwnerStarted and on
	// a replayed child.
	Elapsed time.Duration
	// Replayed reports a child whose durable result was reused and not run.
	Replayed bool
	// Degraded reports a failed child that only reported degraded units.
	Degraded    bool
	FailureCode runtimeapply.FailureCode
	Err         error
}

// OwnerObserver receives the lifecycle of prepared children. It is called from
// the executing goroutine and, for liveness events, from a second goroutine
// while a child runs, so it must be safe for concurrent use and must return
// quickly. A panic inside it never reaches the Apply.
type OwnerObserver func(OwnerEvent)

type ownerObservationKey struct{}

type ownerObservation struct {
	observer OwnerObserver
	interval time.Duration
}

// WithOwnerObserver attaches a request-scoped observer to ctx. A non-positive
// interval selects DefaultOwnerLivenessInterval.
func WithOwnerObserver(ctx context.Context, observer OwnerObserver, interval time.Duration) context.Context {
	if observer == nil {
		return ctx
	}
	if interval <= 0 {
		interval = DefaultOwnerLivenessInterval
	}
	return context.WithValue(ctx, ownerObservationKey{}, ownerObservation{observer: observer, interval: interval})
}

func ownerObservationFrom(ctx context.Context) (ownerObservation, bool) {
	observation, ok := ctx.Value(ownerObservationKey{}).(ownerObservation)
	return observation, ok && observation.observer != nil
}

func (o ownerObservation) notify(event OwnerEvent) {
	defer func() { _ = recover() }()
	o.observer(event)
}

func ownerEventBase(child preparedExecution, position, total int) OwnerEvent {
	event := OwnerEvent{
		Scope: child.scope, Label: child.label, Position: position, Total: total,
		Targets: len(child.request.RuntimeTargets),
	}
	if len(child.request.RuntimeTargets) == 1 {
		event.Module = child.request.RuntimeTargets[0].ModuleRef
		event.Workload = child.request.RuntimeTargets[0].WorkloadRef
	}
	return event
}

// invokeObserved runs one prepared child and reports its start, a periodic
// liveness event while it runs, and its end. Without an observer it is exactly
// invokePrepared.
func invokeObserved(ctx context.Context, child preparedExecution, position, total int) (runtimeexecutor.ExecutionResult, error) {
	observation, observed := ownerObservationFrom(ctx)
	if !observed {
		return invokePrepared(ctx, child)
	}
	base := ownerEventBase(child, position, total)
	started := time.Now()
	emit := func(status string) {
		event := base
		event.Status = status
		if status != OwnerStarted {
			event.Elapsed = time.Since(started)
		}
		observation.notify(event)
	}
	emit(OwnerStarted)

	result, err := invokeWithLiveness(ctx, child, observation.interval, func() { emit(OwnerRunning) })

	event := base
	event.Elapsed = time.Since(started)
	if err != nil {
		event.Status = OwnerFailed
		event.FailureCode = classifyFailure(err)
		event.Degraded = applyoutcome.DegradedOnly(err)
		event.Err = err
	} else {
		event.Status = OwnerSucceeded
	}
	observation.notify(event)
	return result, err
}

// invokeWithLiveness runs one child while a second goroutine calls beat every
// interval. The goroutine is stopped and joined by a deferred call, so it is
// gone on every exit path, a panic of the child included, and it can never beat
// after this function returned: the caller reports the terminal state only
// after the join, so no event follows it.
func invokeWithLiveness(ctx context.Context, child preparedExecution, interval time.Duration, beat func()) (runtimeexecutor.ExecutionResult, error) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// A tick that raced with the stop must not beat.
				select {
				case <-done:
					return
				default:
				}
				beat()
			}
		}
	}()
	defer func() {
		close(done)
		<-stopped
	}()
	return invokePrepared(ctx, child)
}

// observeReplayed reports a child whose durable result was reused.
func observeReplayed(ctx context.Context, child preparedExecution, position, total int) {
	observation, observed := ownerObservationFrom(ctx)
	if !observed {
		return
	}
	event := ownerEventBase(child, position, total)
	event.Status = OwnerSucceeded
	event.Replayed = true
	observation.notify(event)
}
