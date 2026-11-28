package architecturev2renderer

import "github.com/kombifyio/stackkits/internal/engine"

// The typed workload Compose model lives in internal/engine, the canonical
// render boundary of ADR-0055. These aliases keep every existing caller
// (the standalone Compose owner and the Stage 2 native renderer) on the same
// types until Phase 2 deletes the legacy bundle renderers.
type (
	WorkloadComposeDocument       = engine.WorkloadComposeDocument
	WorkloadComposeSecret         = engine.WorkloadComposeSecret
	WorkloadComposeServiceSecret  = engine.WorkloadComposeServiceSecret
	WorkloadComposeService        = engine.WorkloadComposeService
	WorkloadComposeDependency     = engine.WorkloadComposeDependency
	WorkloadComposeDeploy         = engine.WorkloadComposeDeploy
	WorkloadComposeResources      = engine.WorkloadComposeResources
	WorkloadComposeResourceBounds = engine.WorkloadComposeResourceBounds
	WorkloadComposeDeviceRequest  = engine.WorkloadComposeDeviceRequest
	WorkloadComposeLogging        = engine.WorkloadComposeLogging
	WorkloadComposeNetwork        = engine.WorkloadComposeNetwork
	WorkloadComposeHealthcheck    = engine.WorkloadComposeHealthcheck
)

// Lifecycle labels the Compose renderer sets on every workload service.
const (
	WorkloadComposeLifecycleLabel = engine.WorkloadComposeLifecycleLabel
	WorkloadComposeLifecycleOnce  = engine.WorkloadComposeLifecycleOnce
)
