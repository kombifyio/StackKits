// Package hostpreflight observes a target host and admits or refuses a local
// Apply before it mutates anything.
//
// The package answers one question: can this device run what this kit declares
// it needs? It only reports what it measured — an unobservable fact is stated
// as unknown, never assumed to pass. Requirements come from the kit's CUE
// authority, never from constants here, so a kit cannot silently change what it
// demands of a device.
package hostpreflight

import "time"

// SchemaVersion identifies the machine-readable preflight report.
const SchemaVersion = "stackkit.host-preflight/v1"

// Status is the closed outcome vocabulary for a single check and for a report.
type Status string

const (
	// StatusPass means the check was performed and satisfied.
	StatusPass Status = "pass"
	// StatusWarning means the check was performed and the host is usable but
	// degraded. Apply proceeds.
	StatusWarning Status = "warning"
	// StatusBlocked means the check was performed and Apply must not mutate
	// this host.
	StatusBlocked Status = "blocked"
	// StatusUnknown means the fact could not be observed. It never silently
	// passes: strict policy treats it as blocking.
	StatusUnknown Status = "unknown"
	// StatusSkipped means the check does not apply to this host.
	StatusSkipped Status = "skipped"
)

// Policy decides how a report is turned into an admission decision.
type Policy string

const (
	// PolicyWarn admits the host unless a check is blocking. It is the default:
	// an unverifiable fact must not stop a rollout that would otherwise work.
	PolicyWarn Policy = "warn"
	// PolicyStrict additionally refuses on warnings and unknown facts.
	PolicyStrict Policy = "strict"
	// PolicySkip performs no observation and admits the host.
	PolicySkip Policy = "skip"
)

// ValidPolicy reports whether value names a supported policy.
func ValidPolicy(value string) bool {
	switch Policy(value) {
	case PolicyWarn, PolicyStrict, PolicySkip:
		return true
	default:
		return false
	}
}

// Requirements is the kit-declared host floor, projected from the CUE
// KitDefinition. Zero values mean the kit declared nothing and the matching
// check reports unknown rather than inventing a threshold.
type Requirements struct {
	MinCPUCores          int     `json:"minCpuCores"`
	MinRAMGB             int     `json:"minRamGB"`
	MinStorageGB         int     `json:"minStorageGB"`
	RecommendedCPUCores  int     `json:"recommendedCpuCores"`
	RecommendedRAMGB     int     `json:"recommendedRamGB"`
	RecommendedStorageGB int     `json:"recommendedStorageGB"`
	HeadroomFactor       float64 `json:"headroomFactor"`
}

// Declared reports whether the kit supplied a usable floor.
func (r Requirements) Declared() bool {
	return r.MinCPUCores > 0 || r.MinRAMGB > 0 || r.MinStorageGB > 0
}

// DockerFacts is what one `docker info` and one `docker compose version`
// observation proves about the container runtime.
type DockerFacts struct {
	BinaryPresent        bool   `json:"binaryPresent"`
	DaemonReachable      bool   `json:"daemonReachable"`
	PermissionDenied     bool   `json:"permissionDenied"`
	ServerVersion        string `json:"serverVersion,omitempty"`
	ComposePluginVersion string `json:"composePluginVersion,omitempty"`
	StorageDriver        string `json:"storageDriver,omitempty"`
	CgroupDriver         string `json:"cgroupDriver,omitempty"`
	CgroupVersion        string `json:"cgroupVersion,omitempty"`
	MemoryLimitSupported bool   `json:"memoryLimitSupported"`
	SwapLimitSupported   bool   `json:"swapLimitSupported"`
	Rootless             bool   `json:"rootless"`
	RootDir              string `json:"rootDir,omitempty"`
	Diagnostic           string `json:"diagnostic,omitempty"`
}

// MemoryFacts reports host memory in gibibytes. Available and Swap are read
// separately from Total because a host with enough installed memory can still
// be unable to start a workload.
type MemoryFacts struct {
	TotalGB     float64 `json:"totalGb"`
	AvailableGB float64 `json:"availableGb"`
	SwapGB      float64 `json:"swapGb"`
	Observed    bool    `json:"observed"`
}

// DiskFact is free space at one path that Apply will write to.
type DiskFact struct {
	Path     string  `json:"path"`
	FreeGB   float64 `json:"freeGb"`
	Observed bool    `json:"observed"`
}

// PortFact records whether a port Apply must publish is already bound.
type PortFact struct {
	Port                  int    `json:"port"`
	InUse                 bool   `json:"inUse"`
	OwnedByCurrentRuntime bool   `json:"ownedByCurrentRuntime,omitempty"`
	Detail                string `json:"detail,omitempty"`
}

// Facts is everything the probe measured about the host.
type Facts struct {
	ObservedAt     time.Time   `json:"observedAt"`
	OS             string      `json:"os"`
	Distribution   string      `json:"distribution,omitempty"`
	OSVersion      string      `json:"osVersion,omitempty"`
	Architecture   string      `json:"architecture"`
	KernelRelease  string      `json:"kernelRelease,omitempty"`
	Virtualization string      `json:"virtualization,omitempty"`
	CPUCores       int         `json:"cpuCores"`
	CPUBaseline    *bool       `json:"cpuBaselineX8664V2,omitempty"`
	Memory         MemoryFacts `json:"memory"`
	CgroupVersion  string      `json:"cgroupVersion,omitempty"`
	MemoryCgroup   *bool       `json:"memoryCgroupController,omitempty"`
	NamespacesOK   *bool       `json:"namespacesAvailable,omitempty"`
	ClockSynced    *bool       `json:"clockSynchronized,omitempty"`
	Docker         DockerFacts `json:"docker"`
	Disks          []DiskFact  `json:"disks,omitempty"`
	Ports          []PortFact  `json:"ports,omitempty"`
}

// Check is one performed admission check.
type Check struct {
	ID           string   `json:"id"`
	Status       Status   `json:"status"`
	Summary      string   `json:"summary"`
	FailureClass string   `json:"failureClass,omitempty"`
	Remediation  []string `json:"remediation,omitempty"`
}

// Report is the machine-readable preflight result.
type Report struct {
	SchemaVersion string `json:"schemaVersion"`
	Policy        Policy `json:"policy"`
	// Status is the worst measured check; Admitted is the policy decision made
	// from it. They stay separate so a strict refusal still shows what it
	// refused on, and a warn-policy pass still shows what was degraded.
	Status       Status       `json:"status"`
	Admitted     bool         `json:"admitted"`
	KitSlug      string       `json:"kitSlug,omitempty"`
	Requirements Requirements `json:"requirements"`
	Facts        Facts        `json:"facts"`
	Checks       []Check      `json:"checks"`
}

// Blocking reports whether this report refuses the host under its policy.
func (r Report) Blocking() bool { return !r.Admitted }

// BlockedChecks returns the checks that caused a refusal, in report order.
func (r Report) BlockedChecks() []Check {
	blocked := make([]Check, 0, len(r.Checks))
	for _, check := range r.Checks {
		if check.Status == StatusBlocked {
			blocked = append(blocked, check)
		}
	}
	return blocked
}
