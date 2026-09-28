package architecturev2renderer

// governedStopBehavior is the stop behavior each module declares for the one
// component whose PID 1 does not act on SIGTERM. Without it `docker stop`
// and the snapshot quiescer wait out the grace period and end in SIGKILL.
// Measured 2026-09-28 on the pinned images.
var governedStopBehavior = map[string]struct {
	component  string
	stopSignal string
	init       bool
}{
	// python main.py ignores SIGTERM and shuts down on SIGINT.
	comfyUIWorkloadModuleID: {component: "comfyui", stopSignal: "SIGINT"},
	// The entrypoint shell has no signal handlers; the engine's init forwards.
	anythingLLMWorkloadModuleID: {component: "anythingllm", init: true},
}

// validateStopBehavior admits a stop signal or init only as the exact
// governed declaration of the module's component.
func validateStopBehavior(moduleRef string, component selectedPaaSRuntimeComponent, path string) error {
	if component.StopSignal == "" && !component.Init {
		return nil
	}
	rights, ok := governedStopBehavior[moduleRef]
	if !ok || rights.component != component.ID || rights.stopSignal != component.StopSignal || rights.init != component.Init {
		return fail(ErrInvalidPlan, path, "a stop signal or init is admitted only for its governed component")
	}
	return nil
}
