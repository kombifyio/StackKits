package hostpreflight

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
)

// CheckSandboxRuntime refuses a host whose Docker daemon has not registered
// the sandbox runtime a selected component runs under. The agent harness
// executes code an agent writes; without gVisor between that shell and the
// host kernel it would run with the same kernel exposure as every other
// container, so it never runs at all.
const CheckSandboxRuntime = "sandbox-runtime"

// SandboxRuntimeRequirement is one component that must run under a registered
// runtime other than the daemon default, projected from the module's CUE
// runtime components.
type SandboxRuntimeRequirement struct {
	ModuleRef    string `json:"moduleRef"`
	ComponentRef string `json:"componentRef"`
	Runtime      string `json:"runtime"`
}

// SandboxRuntimeRequirementsFromModule projects the sandbox runtimes of one
// catalog module (a decoded foundation.#ModuleContractV2).
func SandboxRuntimeRequirementsFromModule(moduleRef string, module map[string]any) []SandboxRuntimeRequirement {
	runtime, _ := module["runtime"].(map[string]any)
	components, _ := runtime["components"].([]any)
	var result []SandboxRuntimeRequirement
	for _, raw := range components {
		component, _ := raw.(map[string]any)
		name, _ := component["sandboxRuntime"].(string)
		if name == "" {
			continue
		}
		id, _ := component["id"].(string)
		result = append(result, SandboxRuntimeRequirement{ModuleRef: moduleRef, ComponentRef: id, Runtime: name})
	}
	return result
}

// checkSandboxRuntime returns no check when nothing selected a sandbox
// runtime, so every other plan keeps its exact report.
func checkSandboxRuntime(facts Facts, requirements Requirements) []Check {
	if len(requirements.SandboxRuntimes) == 0 {
		return nil
	}
	if !facts.Docker.DaemonReachable {
		return []Check{{
			ID: CheckSandboxRuntime, Status: StatusUnknown,
			Summary: "The registered container runtimes could not be read while the Docker daemon is unreachable",
		}}
	}
	return []Check{CheckSandboxRuntimeRegistered(facts.Docker.Runtimes, requirements.SandboxRuntimes)}
}

// CheckSandboxRuntimeRegistered evaluates the selected sandbox runtimes
// against the runtimes one observed daemon reports. Apply calls it before
// plan resolution so a host without gVisor is refused with the fix, not with
// a bare readiness blocker.
func CheckSandboxRuntimeRegistered(registered []string, requirements []SandboxRuntimeRequirement) Check {
	check := Check{ID: CheckSandboxRuntime, Status: StatusPass}
	selected := append([]SandboxRuntimeRequirement(nil), requirements...)
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].ModuleRef+selected[i].ComponentRef < selected[j].ModuleRef+selected[j].ComponentRef
	})
	var problems, remediation []string
	for _, requirement := range selected {
		if slices.Contains(registered, requirement.Runtime) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s (%s) runs only under the %s runtime, which Docker has not registered", requirement.ModuleRef, requirement.ComponentRef, requirement.Runtime))
		if requirement.Runtime == "runsc" {
			remediation = append(remediation,
				"Install gVisor and register its runsc runtime with Docker: sudo stackkit host remediate --apply gvisor-runsc --yes (Debian and Ubuntu), or follow https://gvisor.dev/docs/user_guide/install/ and add runsc to the runtimes of /etc/docker/daemon.json, then restart Docker.",
				"Or turn the agent-harness module off: re-author with --use-case-capability ai.agent-harness=off.")
		}
	}
	if len(problems) == 0 {
		check.Summary = "Docker has registered the selected sandbox runtimes"
		return check
	}
	check.Status = StatusBlocked
	check.FailureClass = string(applyoutcome.ClassHostIncompatible)
	check.Summary = strings.Join(problems, "; ")
	check.Remediation = uniqueStrings(remediation)
	return check
}
