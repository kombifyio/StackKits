package hostpreflight

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
	"github.com/kombifyio/stackkits/internal/hostconformance"
)

// Accelerator check IDs. The device check refuses a host whose GPU, driver or
// VRAM cannot serve the selected profile; the container-access check refuses
// until containers can reach it (CDI for NVIDIA, ROCm device nodes for AMD).
const (
	CheckGPUAccelerator     = "gpu-accelerator"
	CheckGPUContainerAccess = "gpu-container-access"
)

// AcceleratorRequirement is one selected accelerator profile, projected from
// the module's CUE acceleratorProfiles entry.
type AcceleratorRequirement struct {
	ModuleRef      string `json:"moduleRef"`
	Profile        string `json:"profile"`
	Vendor         string `json:"vendor"`
	MinVRAMGiB     int    `json:"minVramGiB,omitempty"`
	MinDriverMajor int    `json:"minDriverMajor,omitempty"`
}

// AcceleratorRequirementsFromModule projects the selected profile of one
// catalog module (a decoded foundation.#ModuleContractV2). ok is false when
// the module does not declare the profile.
func AcceleratorRequirementsFromModule(moduleRef string, module map[string]any, profile string) (AcceleratorRequirement, bool) {
	profiles, _ := module["acceleratorProfiles"].(map[string]any)
	body, _ := profiles[profile].(map[string]any)
	accelerator, _ := body["accelerator"].(map[string]any)
	vendor, _ := accelerator["vendor"].(string)
	if vendor == "" {
		return AcceleratorRequirement{}, false
	}
	return AcceleratorRequirement{
		ModuleRef: moduleRef, Profile: profile, Vendor: vendor,
		MinVRAMGiB: intField(accelerator, "minVramGiB"), MinDriverMajor: intField(accelerator, "minDriverMajor"),
	}, true
}

func acceleratorByVendor(facts Facts, vendor string) (hostconformance.AcceleratorFacts, bool) {
	for _, accelerator := range facts.Accelerators {
		if accelerator.Vendor == vendor {
			return accelerator, true
		}
	}
	return hostconformance.AcceleratorFacts{}, false
}

// checkAccelerators returns no check when nothing selected a GPU, so CPU
// hosts and CPU plans keep their exact report.
func checkAccelerators(facts Facts, requirements Requirements) []Check {
	if len(requirements.Accelerators) == 0 {
		return nil
	}
	selected := make([]AcceleratorRequirement, len(requirements.Accelerators))
	copy(selected, requirements.Accelerators)
	sort.Slice(selected, func(i, j int) bool { return selected[i].ModuleRef < selected[j].ModuleRef })
	return []Check{checkGPUDevice(facts, selected), checkGPUContainerAccess(facts, selected)}
}

// CheckAcceleratorDevices evaluates only the device, driver and VRAM part of
// the selected profiles. Apply calls it before plan resolution so a host
// without a qualifying GPU is refused with guidance, not with a bare
// readiness blocker.
func CheckAcceleratorDevices(accelerators []hostconformance.AcceleratorFacts, requirements []AcceleratorRequirement) Check {
	selected := append([]AcceleratorRequirement(nil), requirements...)
	sort.Slice(selected, func(i, j int) bool { return selected[i].ModuleRef < selected[j].ModuleRef })
	return checkGPUDevice(Facts{Accelerators: accelerators}, selected)
}

func checkGPUDevice(facts Facts, selected []AcceleratorRequirement) Check {
	check := Check{ID: CheckGPUAccelerator, Status: StatusPass}
	var problems, remediation []string
	for _, requirement := range selected {
		subject := fmt.Sprintf("%s (accelerator profile %s)", requirement.ModuleRef, requirement.Profile)
		observed, found := acceleratorByVendor(facts, requirement.Vendor)
		switch {
		case !found || observed.Devices < 1:
			problems = append(problems, fmt.Sprintf("%s needs an %s GPU, but none was found", subject, vendorName(requirement.Vendor)))
			remediation = append(remediation, driverGuidance(requirement)...)
		case requirement.Vendor == "nvidia" && observed.DriverVersion == "":
			problems = append(problems, fmt.Sprintf("%s needs the NVIDIA driver, but nvidia-smi did not answer", subject))
			remediation = append(remediation, driverGuidance(requirement)...)
		case requirement.MinDriverMajor > 0 && observed.DriverMajor() < requirement.MinDriverMajor:
			problems = append(problems, fmt.Sprintf("%s needs NVIDIA driver %d or newer, found %s", subject, requirement.MinDriverMajor, observed.DriverVersion))
			remediation = append(remediation, driverGuidance(requirement)...)
		case requirement.MinVRAMGiB > 0 && observed.MinVRAMGiB == 0:
			check.Status = StatusUnknown
			problems = append(problems, fmt.Sprintf("%s needs %d GiB of GPU memory, which could not be measured", subject, requirement.MinVRAMGiB))
		case requirement.MinVRAMGiB > 0 && observed.MinVRAMGiB < requirement.MinVRAMGiB:
			problems = append(problems, fmt.Sprintf("%s needs %d GiB of GPU memory, the smallest GPU has %d GiB", subject, requirement.MinVRAMGiB, observed.MinVRAMGiB))
			remediation = append(remediation, "Use a GPU with enough memory, or run this module without the accelerator profile.")
		}
	}
	if len(problems) == 0 {
		check.Summary = "The selected GPU profiles have a qualifying GPU and driver"
		return check
	}
	if len(remediation) > 0 {
		check.Status = StatusBlocked
		check.FailureClass = string(applyoutcome.ClassHostIncompatible)
		check.Remediation = uniqueStrings(append(remediation,
			"Or choose CPU: re-author without --module-accelerator-profile (the Private AI Accelerator setting \"CPU only\")."))
	}
	check.Summary = strings.Join(problems, "; ")
	return check
}

func checkGPUContainerAccess(facts Facts, selected []AcceleratorRequirement) Check {
	check := Check{ID: CheckGPUContainerAccess, Status: StatusPass}
	var problems, remediation []string
	for _, requirement := range selected {
		observed, found := acceleratorByVendor(facts, requirement.Vendor)
		if !found || observed.Devices < 1 {
			continue // the device check already refuses with guidance
		}
		switch requirement.Vendor {
		case "nvidia":
			if observed.ContainerAccess != hostconformance.AcceleratorAccessCDI {
				problems = append(problems, "no CDI spec for nvidia.com/gpu was found under /etc/cdi or /var/run/cdi")
				remediation = append(remediation,
					"Install the NVIDIA Container Toolkit and generate the CDI spec: sudo stackkit host remediate --apply nvidia-container-toolkit --yes",
					"After every NVIDIA driver upgrade, regenerate it: sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml")
			}
			if facts.Docker.DaemonReachable && !facts.Docker.CDIEnabled {
				problems = append(problems, "the Docker daemon does not have CDI enabled")
				remediation = append(remediation,
					"Use Docker Engine 28.2 or newer (CDI is on by default), or add \"features\": {\"cdi\": true} to /etc/docker/daemon.json and restart Docker.")
			}
		case "amd":
			if observed.ContainerAccess != hostconformance.AcceleratorAccessROCmDeviceNodes {
				problems = append(problems, "the ROCm device nodes /dev/kfd and /dev/dri/renderD* are missing")
				remediation = append(remediation, "Load the amdgpu kernel driver with ROCm (KFD) support for this card; StackKits does not install GPU drivers.")
			}
		}
	}
	if len(problems) == 0 {
		check.Summary = "Containers can use the selected GPUs"
		return check
	}
	check.Status = StatusBlocked
	check.FailureClass = string(applyoutcome.ClassHostIncompatible)
	check.Summary = "Containers cannot use the GPU yet: " + strings.Join(uniqueStrings(problems), "; ")
	check.Remediation = uniqueStrings(remediation)
	return check
}

func driverGuidance(requirement AcceleratorRequirement) []string {
	if requirement.Vendor == "nvidia" {
		return []string{fmt.Sprintf("Install the NVIDIA driver %d or newer for this GPU from your distribution or NVIDIA, reboot, and check that nvidia-smi lists the GPU. StackKits does not install GPU drivers.", requirement.MinDriverMajor)}
	}
	return []string{"Use a ROCm-capable AMD GPU with the amdgpu kernel driver on an amd64 host. StackKits does not install GPU drivers."}
}

func vendorName(vendor string) string {
	if vendor == "amd" {
		return "AMD"
	}
	return "NVIDIA"
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
