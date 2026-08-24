package hostpreflight

import (
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
)

// RequirementsFromDefinition projects the kit-declared host floor out of a
// decoded foundation.#KitDefinition. A kit that declares nothing yields a zero
// Requirements, and the resource checks then report unknown instead of
// inventing a threshold.
func RequirementsFromDefinition(definition map[string]any) Requirements {
	block, ok := definition["hostRequirements"].(map[string]any)
	if !ok {
		return Requirements{}
	}
	return Requirements{
		MinCPUCores:          intField(block, "minCpuCores"),
		MinRAMGB:             intField(block, "minRamGB"),
		MinStorageGB:         intField(block, "minStorageGB"),
		RecommendedCPUCores:  intField(block, "recommendedCpuCores"),
		RecommendedRAMGB:     intField(block, "recommendedRamGB"),
		RecommendedStorageGB: intField(block, "recommendedStorageGB"),
		HeadroomFactor:       floatField(block, "headroomFactor"),
	}
}

func intField(block map[string]any, key string) int {
	switch value := block[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func floatField(block map[string]any, key string) float64 {
	switch value := block[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

// Evaluate turns measured facts into an admission decision under one policy.
//
// A check that could not be measured reports unknown rather than pass, so a
// probe that silently failed can never admit a host it did not verify.
func Evaluate(facts Facts, requirements Requirements, kitSlug string, policy Policy) Report {
	report := Report{
		SchemaVersion: SchemaVersion,
		Policy:        policy,
		KitSlug:       kitSlug,
		Requirements:  requirements,
		Facts:         facts,
	}
	if policy == PolicySkip {
		report.Status = StatusSkipped
		report.Admitted = true
		return report
	}

	checks := []Check{
		checkArchitecture(facts),
		checkContainerRuntime(facts),
		checkComposePlugin(facts),
		checkNamespaces(facts),
		checkCPU(facts, requirements),
		checkMemory(facts, requirements),
		checkStorage(facts, requirements),
		checkPorts(facts),
		checkMemoryCgroup(facts),
		checkSwap(facts),
		checkCPUBaseline(facts),
		checkClock(facts),
	}
	sort.SliceStable(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
	report.Checks = checks
	report.Status = worstStatus(checks)
	report.Admitted = admits(report.Status, policy)
	return report
}

func admits(status Status, policy Policy) bool {
	switch status {
	case StatusBlocked:
		return false
	case StatusWarning, StatusUnknown:
		// Warn admits a usable-but-degraded host; strict refuses anything it
		// could not fully verify.
		return policy != PolicyStrict
	default:
		return true
	}
}

// statusRank orders outcomes from most to least severe so a report takes the
// worst measured check.
func statusRank(status Status) int {
	switch status {
	case StatusBlocked:
		return 4
	case StatusUnknown:
		return 3
	case StatusWarning:
		return 2
	case StatusPass:
		return 1
	default:
		return 0
	}
}

func worstStatus(checks []Check) Status {
	worst := StatusPass
	for _, check := range checks {
		if statusRank(check.Status) > statusRank(worst) {
			worst = check.Status
		}
	}
	return worst
}

func checkArchitecture(facts Facts) Check {
	check := Check{ID: "host-architecture"}
	switch facts.Architecture {
	case "amd64", "arm64":
		check.Status = StatusPass
		check.Summary = "Host architecture " + facts.Architecture + " is supported"
	default:
		check.Status = StatusBlocked
		check.Summary = "Host architecture " + facts.Architecture + " has no StackKits runtime images"
		check.FailureClass = string(applyoutcome.ClassHostIncompatible)
		check.Remediation = []string{"Run this StackKit on an amd64 or arm64 host."}
	}
	return check
}

func checkContainerRuntime(facts Facts) Check {
	check := Check{ID: "container-runtime"}
	switch {
	case !facts.Docker.BinaryPresent:
		check.Status = StatusBlocked
		check.Summary = "No Docker binary is available on this host"
		check.FailureClass = string(applyoutcome.ClassDockerMissing)
		check.Remediation = applyoutcome.Remediation(applyoutcome.ClassDockerMissing)
	case facts.Docker.PermissionDenied:
		check.Status = StatusBlocked
		check.Summary = "This user cannot reach the Docker daemon socket"
		check.FailureClass = string(applyoutcome.ClassDockerSocketDenied)
		check.Remediation = applyoutcome.Remediation(applyoutcome.ClassDockerSocketDenied)
	case !facts.Docker.DaemonReachable:
		check.Status = StatusBlocked
		check.Summary = "The Docker daemon is not reachable: " + facts.Docker.Diagnostic
		check.FailureClass = string(applyoutcome.ClassDockerDaemonFailed)
		check.Remediation = applyoutcome.Remediation(applyoutcome.ClassDockerDaemonFailed)
	default:
		check.Status = StatusPass
		check.Summary = "Docker " + facts.Docker.ServerVersion + " is reachable"
	}
	return check
}

func checkComposePlugin(facts Facts) Check {
	check := Check{ID: "container-compose-plugin"}
	switch {
	case !facts.Docker.DaemonReachable:
		check.Status = StatusSkipped
		check.Summary = "Compose plugin not probed while the daemon is unreachable"
	case facts.Docker.ComposePluginVersion == "":
		check.Status = StatusBlocked
		check.Summary = "The Docker Compose plugin is not installed"
		check.FailureClass = string(applyoutcome.ClassDockerMissing)
		check.Remediation = []string{"Install the Docker Compose v2 plugin, then retry."}
	default:
		check.Status = StatusPass
		check.Summary = "Docker Compose " + facts.Docker.ComposePluginVersion + " is available"
	}
	return check
}

func checkNamespaces(facts Facts) Check {
	check := Check{ID: "kernel-namespaces"}
	switch {
	case facts.NamespacesOK == nil:
		check.Status = StatusUnknown
		check.Summary = "Container namespace support could not be probed"
	case *facts.NamespacesOK:
		check.Status = StatusPass
		check.Summary = "The kernel grants the container namespaces Docker requires"
	default:
		check.Status = StatusBlocked
		check.Summary = "The kernel blocks the container namespaces Docker requires"
		check.FailureClass = string(applyoutcome.ClassKernelNamespaceBlocked)
		check.Remediation = applyoutcome.Remediation(applyoutcome.ClassKernelNamespaceBlocked)
	}
	return check
}

func checkCPU(facts Facts, requirements Requirements) Check {
	check := Check{ID: "host-cpu"}
	if requirements.MinCPUCores <= 0 {
		check.Status = StatusSkipped
		check.Summary = "This kit declares no CPU floor"
		return check
	}
	switch {
	case facts.CPUCores <= 0:
		check.Status = StatusUnknown
		check.Summary = "CPU count could not be observed"
	case facts.CPUCores < requirements.MinCPUCores:
		check.Status = StatusBlocked
		check.Summary = fmt.Sprintf("%d CPU cores observed, kit requires %d", facts.CPUCores, requirements.MinCPUCores)
		check.FailureClass = string(applyoutcome.ClassHostResourceShortage)
		check.Remediation = []string{"Use a host with more CPU cores."}
	case requirements.RecommendedCPUCores > 0 && facts.CPUCores < requirements.RecommendedCPUCores:
		check.Status = StatusWarning
		check.Summary = fmt.Sprintf("%d CPU cores observed, kit recommends %d", facts.CPUCores, requirements.RecommendedCPUCores)
	default:
		check.Status = StatusPass
		check.Summary = fmt.Sprintf("%d CPU cores observed", facts.CPUCores)
	}
	return check
}

func checkMemory(facts Facts, requirements Requirements) Check {
	check := Check{ID: "host-memory"}
	if requirements.MinRAMGB <= 0 {
		check.Status = StatusSkipped
		check.Summary = "This kit declares no memory floor"
		return check
	}
	if !facts.Memory.Observed {
		check.Status = StatusUnknown
		check.Summary = "Host memory could not be observed"
		return check
	}
	switch {
	case facts.Memory.TotalGB < float64(requirements.MinRAMGB):
		check.Status = StatusBlocked
		check.Summary = fmt.Sprintf("%.1f GB memory observed, kit requires %d GB", facts.Memory.TotalGB, requirements.MinRAMGB)
		check.FailureClass = string(applyoutcome.ClassHostResourceShortage)
		check.Remediation = []string{
			"Use a host with more memory, or select fewer workloads.",
			"Adding swap does not replace the missing memory but reduces out-of-memory kills.",
		}
	case requirements.RecommendedRAMGB > 0 && facts.Memory.TotalGB < float64(requirements.RecommendedRAMGB):
		check.Status = StatusWarning
		check.Summary = fmt.Sprintf("%.1f GB memory observed, kit recommends %d GB", facts.Memory.TotalGB, requirements.RecommendedRAMGB)
		check.Remediation = []string{"Expect slow first starts and out-of-memory risk under load."}
	default:
		check.Status = StatusPass
		check.Summary = fmt.Sprintf("%.1f GB memory observed", facts.Memory.TotalGB)
	}
	return check
}

func checkStorage(facts Facts, requirements Requirements) Check {
	check := Check{ID: "host-storage"}
	if requirements.MinStorageGB <= 0 {
		check.Status = StatusSkipped
		check.Summary = "This kit declares no storage floor"
		return check
	}
	observed := false
	for _, disk := range facts.Disks {
		if !disk.Observed {
			continue
		}
		observed = true
		if disk.FreeGB < float64(requirements.MinStorageGB) {
			check.Status = StatusBlocked
			check.Summary = fmt.Sprintf("%.1f GB free at %s, kit requires %d GB", disk.FreeGB, disk.Path, requirements.MinStorageGB)
			check.FailureClass = string(applyoutcome.ClassDiskFull)
			check.Remediation = []string{
				"Free space at " + disk.Path + " before applying.",
				"Reclaim container storage with docker system prune.",
			}
			return check
		}
	}
	if !observed {
		check.Status = StatusUnknown
		check.Summary = "Free space could not be observed"
		return check
	}
	for _, disk := range facts.Disks {
		if disk.Observed && requirements.RecommendedStorageGB > 0 && disk.FreeGB < float64(requirements.RecommendedStorageGB) {
			check.Status = StatusWarning
			check.Summary = fmt.Sprintf("%.1f GB free at %s, kit recommends %d GB", disk.FreeGB, disk.Path, requirements.RecommendedStorageGB)
			return check
		}
	}
	check.Status = StatusPass
	check.Summary = "Free space satisfies the kit storage floor"
	return check
}

func checkPorts(facts Facts) Check {
	check := Check{ID: "host-ports"}
	if len(facts.Ports) == 0 {
		check.Status = StatusSkipped
		check.Summary = "No published ports were declared for this Apply"
		return check
	}
	for _, port := range facts.Ports {
		if !port.InUse {
			continue
		}
		check.Status = StatusBlocked
		check.Summary = fmt.Sprintf("Port %d is already bound by another process", port.Port)
		check.FailureClass = string(applyoutcome.ClassPortConflict)
		check.Remediation = []string{
			fmt.Sprintf("Stop whatever is listening on port %d, then retry.", port.Port),
			"On a host running systemd-resolved, port 53 is held by its stub listener.",
		}
		return check
	}
	check.Status = StatusPass
	check.Summary = "Every port this Apply publishes is free"
	return check
}

func checkMemoryCgroup(facts Facts) Check {
	check := Check{ID: "cgroup-memory-controller"}
	switch {
	case facts.MemoryCgroup == nil:
		check.Status = StatusUnknown
		check.Summary = "The cgroup memory controller could not be observed"
	case *facts.MemoryCgroup:
		check.Status = StatusPass
		check.Summary = "The cgroup memory controller is enabled"
	default:
		check.Status = StatusWarning
		check.Summary = "The cgroup memory controller is disabled, so container memory limits are ignored"
		check.FailureClass = string(applyoutcome.ClassCgroupMemoryMissing)
		check.Remediation = applyoutcome.Remediation(applyoutcome.ClassCgroupMemoryMissing)
	}
	return check
}

func checkSwap(facts Facts) Check {
	check := Check{ID: "host-swap"}
	if !facts.Memory.Observed {
		check.Status = StatusUnknown
		check.Summary = "Swap could not be observed"
		return check
	}
	if facts.Memory.SwapGB <= 0 && facts.Memory.TotalGB > 0 && facts.Memory.TotalGB <= 4 {
		check.Status = StatusWarning
		check.Summary = "No swap is configured on a small-memory host"
		check.FailureClass = string(applyoutcome.ClassOOMKilled)
		check.Remediation = []string{
			"Add swap or zram so a memory spike degrades instead of killing a container.",
		}
		return check
	}
	check.Status = StatusPass
	check.Summary = fmt.Sprintf("%.1f GB swap configured", facts.Memory.SwapGB)
	return check
}

func checkCPUBaseline(facts Facts) Check {
	check := Check{ID: "cpu-baseline"}
	switch {
	case facts.Architecture != "amd64":
		check.Status = StatusSkipped
		check.Summary = "The x86-64-v2 baseline applies only to amd64 hosts"
	case facts.CPUBaseline == nil:
		check.Status = StatusUnknown
		check.Summary = "CPU feature flags could not be observed"
	case *facts.CPUBaseline:
		check.Status = StatusPass
		check.Summary = "The CPU provides the x86-64-v2 baseline"
	default:
		check.Status = StatusWarning
		check.Summary = "The CPU lacks the x86-64-v2 baseline, so v2-built service images crash at runtime"
		check.Remediation = []string{
			"On a KVM or Proxmox guest, set the VM CPU type to host.",
		}
	}
	return check
}

func checkClock(facts Facts) Check {
	check := Check{ID: "clock-synchronized"}
	switch {
	case facts.ClockSynced == nil:
		check.Status = StatusUnknown
		check.Summary = "Time synchronization could not be observed"
	case *facts.ClockSynced:
		check.Status = StatusPass
		check.Summary = "The host clock is synchronized"
	default:
		check.Status = StatusWarning
		check.Summary = "The host clock is not synchronized, which breaks TLS issuance and evidence validity"
		check.FailureClass = string(applyoutcome.ClassClockSkew)
		check.Remediation = applyoutcome.Remediation(applyoutcome.ClassClockSkew)
	}
	return check
}
