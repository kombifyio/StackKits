package nativehost

import "context"

// NativeWorkloadCompose is one standalone workload's Compose project as the
// native Apply persists it before `docker compose up`: the project name, the
// runtime directory .stackkit/runtime/applications/<project>/, the exact
// compose.yaml bytes, and the private .env beside it. The OpenTofu wrapper
// root (ADR-0045 Stage 1) embeds Compose and references EnvFile; it never
// reads the .env content.
type NativeWorkloadCompose struct {
	ProjectName string
	Directory   string
	Compose     []byte
	EnvFile     string
	// Wait reports whether the native `up` waits for health. It does not
	// when a component may run degraded.
	Wait    bool
	project standaloneComposeProject
}

// NativeWorkloadComposeOperations is the standalone Compose owner split
// around `docker compose up`, so the OpenTofu executor can run the native
// preparation, apply the wrapper root instead of `up`, and run the native
// completion and observation unchanged.
type NativeWorkloadComposeOperations interface {
	SelectedPaaSWorkloadOperations
	SelectedPaaSWorkloadObservationValidator
	PrepareWorkloadCompose(context.Context, SelectedPaaSWorkloadDeployment) (NativeWorkloadCompose, error)
	CompleteWorkloadCompose(context.Context, NativeWorkloadCompose) error
}
