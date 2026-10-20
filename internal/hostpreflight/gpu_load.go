package hostpreflight

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
	"github.com/kombifyio/stackkits/internal/hostconformance"
)

// CheckGPULoad proves an NVIDIA GPU under real CUDA work. nvidia-smi answering
// is not enough: a GPU on a weak power supply or a marginal link (typical for
// a Thunderbolt eGPU) lists fine at idle and falls off the bus (Xid 79) the
// moment a CUDA context draws power. Only a load plus the kernel's NVRM Xid
// record shows that.
const CheckGPULoad = "gpu-load"

// GPULoadImage is NVIDIA's CUDA vectorAdd sample, the image NVIDIA's
// Container Toolkit documentation verifies GPU containers with. It is pinned
// by digest; CUDA 12 minor-version compatibility runs it on every driver from
// 525 on, so the 550 floor and every newer branch qualify.
const GPULoadImage = "nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0@sha256:7c7540bdf1f942d4fb6db97069fd6c289471b54ac29e3c7fcdf914cf77af7d41"

// DefaultGPULoadRuns is how often the sample runs. Each run opens a fresh CUDA
// context, so ten runs exercise context setup, memory copies and kernel
// launches ten times in well under a minute on a warm image.
const DefaultGPULoadRuns = 10

const (
	gpuLoadRunTimeout   = 5 * time.Minute // the first run pulls the image
	gpuLoadProbeTimeout = 30 * time.Second
	gpuLoadPassMarker   = "Test PASSED"
)

// XidEvent is one NVRM Xid line from the kernel log.
type XidEvent struct {
	Device string `json:"device"`
	Code   int    `json:"code"`
	Line   string `json:"line"`
}

// NVRM: Xid (PCI:0000:01:00): 79, pid='<unknown>', name=<unknown>, GPU has fallen off the bus.
var xidPattern = regexp.MustCompile(`NVRM: Xid \(PCI:([0-9A-Fa-f:.]+)\): ([0-9]+)`)

// ParseXidEvents returns the Xid events of a kernel log in order.
func ParseXidEvents(kernelLog []byte) []XidEvent {
	var events []XidEvent
	for _, line := range strings.Split(string(kernelLog), "\n") {
		match := xidPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		code, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		events = append(events, XidEvent{Device: match[1], Code: code, Line: strings.TrimSpace(line)})
	}
	return events
}

// xidMeanings names the Xids that say the GPU or its link is broken, after
// NVIDIA's Xid catalog. Codes not listed are application-level faults (13,
// 31, 43, 45, ...): reported, but not a reason to refuse the host.
var xidMeanings = map[int]string{
	48:  "double-bit ECC error",
	62:  "internal micro-controller halt",
	74:  "NVLink error",
	79:  "GPU has fallen off the bus",
	92:  "high single-bit ECC error rate",
	95:  "uncontained ECC error",
	119: "GSP RPC timeout",
	120: "GSP error",
}

// GPULoadEvidence is what the load check observed, kept for the operator.
type GPULoadEvidence struct {
	Image          string     `json:"image"`
	Runs           int        `json:"runs"`
	PassedRuns     int        `json:"passedRuns"`
	KernelLogRead  bool       `json:"kernelLogRead"`
	XidBefore      []XidEvent `json:"xidBefore,omitempty"`
	XidDuringLoad  []XidEvent `json:"xidDuringLoad,omitempty"`
	AnsweredAfter  bool       `json:"nvidiaSmiAnsweredAfter"`
	LoadFailureLog string     `json:"loadFailureLog,omitempty"`
	// WSL is set on a WSL2 host, where Windows logs the GPU's Xid errors
	// (Event Viewer, nvlddmkm) and the distribution's kernel log never does.
	WSL bool `json:"wsl,omitempty"`
}

// RunGPULoadCheck runs the CUDA sample through the Docker CDI device
// nvidia.com/gpu=all, diffs the kernel log for new Xid events and asks
// nvidia-smi again. run executes one argv and returns its combined output.
func RunGPULoadCheck(ctx context.Context, run func(context.Context, []string) ([]byte, error), image string, runs int) (Check, GPULoadEvidence) {
	if image == "" {
		image = GPULoadImage
	}
	if runs < 1 {
		runs = DefaultGPULoadRuns
	}
	evidence := GPULoadEvidence{Image: image, Runs: runs, WSL: hostconformance.OnWSL(nil)}
	smi, found := hostconformance.NVIDIASMICommand(nil)
	if !found {
		smi = "nvidia-smi"
	}
	bounded := func(timeout time.Duration, argv ...string) ([]byte, error) {
		probe, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return run(probe, argv)
	}
	kernelLog := func() ([]byte, bool) {
		if output, err := bounded(gpuLoadProbeTimeout, "dmesg"); err == nil {
			return output, true
		}
		output, err := bounded(gpuLoadProbeTimeout, "journalctl", "-k", "-b", "--no-pager", "-o", "cat")
		return output, err == nil
	}

	before, readBefore := kernelLog()
	evidence.XidBefore = ParseXidEvents(before)
	for attempt := 0; attempt < runs; attempt++ {
		output, err := bounded(gpuLoadRunTimeout, "docker", "run", "--rm", "--device", "nvidia.com/gpu=all", image)
		if err != nil || !strings.Contains(string(output), gpuLoadPassMarker) {
			evidence.LoadFailureLog = boundedRemediationDetail(string(output))
			break
		}
		evidence.PassedRuns++
	}
	after, readAfter := kernelLog()
	evidence.KernelLogRead = readBefore && readAfter
	evidence.XidDuringLoad = newXidEvents(evidence.XidBefore, ParseXidEvents(after))
	_, smiErr := bounded(gpuLoadProbeTimeout, smi, "--query-gpu=name", "--format=csv,noheader")
	evidence.AnsweredAfter = smiErr == nil

	return gpuLoadVerdict(evidence), evidence
}

// newXidEvents returns the events of after that before did not already hold.
// Kernel log lines carry their timestamp, so equal lines are the same event.
func newXidEvents(before, after []XidEvent) []XidEvent {
	seen := map[string]int{}
	for _, event := range before {
		seen[event.Line]++
	}
	var fresh []XidEvent
	for _, event := range after {
		if seen[event.Line] > 0 {
			seen[event.Line]--
			continue
		}
		fresh = append(fresh, event)
	}
	return fresh
}

func gpuLoadVerdict(evidence GPULoadEvidence) Check {
	check := Check{ID: CheckGPULoad, Status: StatusPass}
	fatal := fatalXids(evidence.XidDuringLoad)
	var problems []string
	switch {
	case len(fatal) > 0:
		problems = append(problems, "the GPU failed under CUDA load: "+describeXids(fatal))
	case evidence.PassedRuns < evidence.Runs:
		problems = append(problems, fmt.Sprintf("the CUDA sample passed %d of %d runs", evidence.PassedRuns, evidence.Runs))
	}
	if !evidence.AnsweredAfter {
		problems = append(problems, "nvidia-smi no longer answers after the load")
	}
	if len(problems) > 0 {
		check.Status = StatusBlocked
		check.FailureClass = string(applyoutcome.ClassHostIncompatible)
		check.Summary = strings.Join(problems, "; ")
		check.Remediation = gpuLoadRemediation(fatal, evidence)
		return check
	}
	var notes []string
	if earlier := fatalXids(evidence.XidBefore); len(earlier) > 0 {
		notes = append(notes, "this boot already logged "+describeXids(earlier)+" before the check")
	}
	if len(evidence.XidDuringLoad) > 0 {
		notes = append(notes, "application-level Xid during the load: "+describeXids(evidence.XidDuringLoad))
	}
	if !evidence.KernelLogRead {
		notes = append(notes, "the kernel log was unreadable, so Xid errors could not be ruled out; run as root")
	}
	check.Summary = fmt.Sprintf("The GPU ran %d CUDA sample runs without a GPU or link error", evidence.Runs)
	if len(notes) > 0 {
		check.Status = StatusWarning
		check.Summary += "; but " + strings.Join(notes, "; ")
		check.Remediation = gpuLoadRemediation(fatalXids(evidence.XidBefore), evidence)
	}
	return check
}

func fatalXids(events []XidEvent) []XidEvent {
	var fatal []XidEvent
	for _, event := range events {
		if _, known := xidMeanings[event.Code]; known {
			fatal = append(fatal, event)
		}
	}
	return fatal
}

func describeXids(events []XidEvent) string {
	var parts []string
	for _, event := range events {
		part := fmt.Sprintf("Xid %d on %s", event.Code, event.Device)
		if meaning, known := xidMeanings[event.Code]; known {
			part += " (" + meaning + ")"
		}
		if !slices.Contains(parts, part) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

func gpuLoadRemediation(fatal []XidEvent, evidence GPULoadEvidence) []string {
	var remediation []string
	if slices.ContainsFunc(fatal, func(event XidEvent) bool { return event.Code == 79 }) || !evidence.AnsweredAfter {
		remediation = append(remediation,
			"Xid 79 means the GPU dropped off the PCIe bus, usually from power or the link: give the card (or the eGPU enclosure) a power supply with headroom over its board power, and check the Thunderbolt or PCIe cable, port and link speed.",
			"The GPU stays lost until the host is rebooted (in a VM: the Proxmox host, which resets the passed-through device).",
			"For links that drop under load, disabling PCIe power management on the host kernel command line (pcie_aspm=off) can help.")
	}
	if slices.ContainsFunc(fatal, func(event XidEvent) bool { return event.Code != 79 }) {
		remediation = append(remediation, "Look up the Xid in NVIDIA's Xid catalog; ECC and GSP errors point at the card or its firmware, not at StackKits.")
	}
	if evidence.PassedRuns < evidence.Runs && len(fatal) == 0 && evidence.AnsweredAfter {
		remediation = append(remediation,
			"The CUDA sample did not pass; check that Docker reaches nvidia.com/gpu through CDI (stackkit host preflight, check gpu-container-access) and read the failure log.")
	}
	if !evidence.KernelLogRead {
		remediation = append(remediation, "Run sudo stackkit host gpu-check so the kernel log (dmesg) can be read.")
	}
	if evidence.WSL && (evidence.PassedRuns < evidence.Runs || !evidence.AnsweredAfter) {
		remediation = append(remediation,
			"On WSL2 the GPU's Xid errors are logged by Windows, not by this distribution: look in Event Viewer under Windows Logs > System for source nvlddmkm, update the NVIDIA driver on Windows, then run wsl --shutdown and start the distribution again.")
	}
	remediation = append(remediation, "Or choose CPU: re-author without --module-accelerator-profile (the Private AI Accelerator setting \"CPU only\").")
	return remediation
}
