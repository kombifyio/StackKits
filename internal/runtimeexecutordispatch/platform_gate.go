package runtimeexecutordispatch

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/applyoutcome"
)

// errMarketplaceNotInstalled is the result of a workload that deploys through a
// Marketplace platform adapter when that platform failed earlier in the same
// Apply. Such a workload is not attempted: its platform was never configured, so
// it could only wait on a deployment the platform cannot run (Coolify without its
// own-host connection polled until the Apply deadline and hid the real failure).
var errMarketplaceNotInstalled = errors.New("not attempted: the Marketplace or Dokploy platform this workload deploys through failed earlier in this Apply; repair that platform and apply again")

// platformGate carries the Marketplace outcome across the children of one Apply.
type platformGate struct{ failed bool }

func (g *platformGate) invoke(ctx context.Context, child preparedExecution, position, total int) (runtimeexecutor.ExecutionResult, error) {
	if g.failed && child.platformBound {
		child.executor = refusedExecutor{identity: child.executor.Identity(), err: errMarketplaceNotInstalled}
	}
	result, err := invokeObserved(ctx, child, position, total)
	if err != nil && child.platform && !applyoutcome.DegradedOnly(err) {
		g.failed = true
	}
	return result, err
}

// refusedExecutor stands in for a child that is not attempted, so the refusal
// passes through the same progress, journal and failure reporting as any other.
type refusedExecutor struct {
	identity runtimeexecutor.ExecutorIdentity
	err      error
}

func (e refusedExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e refusedExecutor) Execute(context.Context, runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	return runtimeexecutor.ExecutionOutcome{}, e.err
}
