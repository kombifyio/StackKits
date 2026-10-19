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
	CheckGPUDriver          = "gpu-driver"
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
	return []Check{checkGPUDevice(facts, selected), checkGPUDriver(facts, selected), checkGPUContainerAccess(facts, selected)}
}

// CheckAcceleratorDevices evaluates the device, VRAM and driver part of the
// selected profiles and returns the first refusal, else the device check.
// Apply calls it before plan resolution so a host without a qualifying GPU is
// refused with guidance, not with a bare readiness blocker.
func CheckAcceleratorDevices(accelerators []hostconformance.AcceleratorFacts, requirements []AcceleratorRequirement) Check {
	selected := append([]AcceleratorRequirement(nil), requirements...)
	sort.Slice(selected, func(i, j int) bool { return selected[i].ModuleRef < selected[j].ModuleRef })
	facts := Facts{Accelerators: accelerators}
	device := checkGPUDevice(facts, selected)
	if device.Status == StatusBlocked {
		return device
	}
	if driver := checkGPUDriver(facts, selected); driver.Status == StatusBlocked {
		return driver
	}
	return device
}

// driverPending reports a GPU whose vendor driver must be installed or
// upgraded before its memory can even be measured.
func driverPending(requirement AcceleratorRequirement, observed hostconformance.AcceleratorFacts) bool {
	if requirement.Vendor != "nvidia" {
		return false
	}
	return observed.DriverVersion == "" || (requirement.MinDriverMajor > 0 && observed.DriverMajor() < requirement.MinDriverMajor)
}

// checkGPUDriver refuses a present NVIDIA GPU whose driver is missing or older
// than the profile floor. It is a check of its own so that the driver
// resolution answers exactly this finding and never a VRAM shortfall.
func checkGPUDriver(facts Facts, selected []AcceleratorRequirement) Check {
	check := Check{ID: CheckGPUDriver, Status: StatusPass, Summary: "The selected GPUs have a qualifying driver"}
	var problems []string
	floor := 0
	for _, requirement := range selected {
		observed, found := acceleratorByVendor(facts, requirement.Vendor)
		if !found || observed.Devices < 1 || !driverPending(requirement, observed) {
			continue // no GPU is the device check's refusal
		}
		subject := fmt.Sprintf("%s (accelerator profile %s)", requirement.ModuleRef, requirement.Profile)
		if observed.DriverVersion == "" {
			problems = append(problems, fmt.Sprintf("%s has %d NVIDIA GPU(s) on this host, but no NVIDIA driver answers (nvidia-smi)", subject, observed.Devices))
		} else {
			problems = append(problems, fmt.Sprintf("%s needs NVIDIA driver %d or newer, found %s", subject, requirement.MinDriverMajor, observed.DriverVersion))
		}
		floor = max(floor, requirement.MinDriverMajor)
	}
	if len(problems) == 0 {
		return check
	}
	check.Status = StatusBlocked
	check.FailureClass = string(applyoutcome.ClassHostIncompatible)
	check.Summary = strings.Join(problems, "; ")
	check.Remediation = []string{
		"On Ubuntu, install the newest prebuilt NVIDIA server driver: sudo stackkit host remediate --apply " + ResolutionNVIDIADriver + " --yes, then reboot.",
		fmt.Sprintf("Elsewhere, install the NVIDIA driver %d or newer from your distribution or NVIDIA, reboot, and check that nvidia-smi lists the GPU.", floor),
		"Or choose CPU: re-author without --module-accelerator-profile (the Private AI Accelerator setting \"CPU only\").",
	}
	return check
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
			remediation = append(remediation, deviceGuidance(requirement))
		case driverPending(requirement, observed):
			// The driver check refuses; memory is measured once it answers.
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
	waitingForDriver := false
	for _, requirement := range selected {
		observed, found := acceleratorByVendor(facts, requirement.Vendor)
		if !found || observed.Devices < 1 {
			continue // the device check already refuses with guidance
		}
		if driverPending(requirement, observed) {
			waitingForDriver = true // the CDI spec is generated from the driver
			continue
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
				remediation = append(remediation, "Load the amdgpu kernel driver with ROCm (KFD) support for this card; StackKits installs NVIDIA drivers only.")
			}
		}
	}
	if len(problems) == 0 && waitingForDriver {
		check.Status = StatusUnknown
		check.Summary = "Container access to the GPU is measured once the NVIDIA driver answers"
		check.Remediation = []string{"Install the driver first (see " + CheckGPUDriver + "), reboot, then run stackkit host remediate again."}
		return check
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

func deviceGuidance(requirement AcceleratorRequirement) string {
	if requirement.Vendor == "nvidia" {
		return "Attach an NVIDIA GPU to this host; in a VM, pass it through from the hypervisor (Proxmox: hostpci). Once lspci lists it, stackkit host remediate offers the driver install."
	}
	return "Use a ROCm-capable AMD GPU with the amdgpu kernel driver on an amd64 host; StackKits installs NVIDIA drivers only."
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
