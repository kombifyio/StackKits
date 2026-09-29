package architecturev2renderer

// SandboxRuntimeRunsc is the gVisor OCI runtime Docker registers as `runsc`.
// A component that carries it runs with a user-space kernel between the
// agent's shell and the host kernel; the daemon default (runc) shares the
// host kernel with every container.
const SandboxRuntimeRunsc = "runsc"

// governedSandboxRuntimeComponents lists the only components that may run
// under the sandbox runtime, and which one. The right exists for a component
// that executes code an agent writes; it is never a generic knob, and a
// component that needs it never runs without it.
var governedSandboxRuntimeComponents = map[string]struct {
	component string
	runtime   string
}{
	openHandsWorkloadModuleID: {component: "openhands", runtime: SandboxRuntimeRunsc},
	// Paperclip starts the agent CLIs of its local adapters and the process
	// adapter's commands as child processes of its own container.
	paperclipWorkloadModuleID: {component: "paperclip", runtime: SandboxRuntimeRunsc},
}

// validateSandboxRuntime accepts a component's sandbox runtime only when it is
// exactly the governed declaration of its module, and refuses the governed
// component without it.
func validateSandboxRuntime(moduleRef string, component selectedPaaSRuntimeComponent, path string) error {
	rights, governed := governedSandboxRuntimeComponents[moduleRef]
	governed = governed && rights.component == component.ID
	switch {
	case component.SandboxRuntime == "" && !governed:
		return nil
	case !governed:
		return fail(ErrInvalidPlan, path+".sandboxRuntime", "the sandbox runtime is admitted only for the governed agent-executing component")
	case component.SandboxRuntime != rights.runtime:
		return fail(ErrInvalidPlan, path+".sandboxRuntime", "%s runs only under the %s runtime; it never shares the host kernel with the agent's shell", component.ID, rights.runtime)
	}
	return nil
}
