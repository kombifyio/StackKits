// Package applyoutcome owns the StackKits failure-class taxonomy for local
// runtime execution.
//
// It classifies bounded process output and error text into one closed class
// with retry and remediation guidance. The package performs no I/O, owns no
// redaction, and imports nothing beyond the standard library so that evidence
// and rollout packages can consume it without an import cycle. Callers bound
// and redact any text before persisting it alongside a classification.
package applyoutcome

// Class is the closed StackKits failure-class vocabulary. Values are the
// wire representation used by rollout evidence and apply outcome reporting;
// they extend the historical rollout failure classes rather than replacing
// them.
type Class string

const (
	// ClassUnknown means no matcher recognized the text. It is never a
	// silent default: callers keep the bounded excerpt so an unmatched
	// failure remains diagnosable.
	ClassUnknown Class = "unknown_failure"

	ClassOOMKilled              Class = "oom_killed"
	ClassDiskFull               Class = "disk_full"
	ClassImageArchMismatch      Class = "image_arch_mismatch"
	ClassRegistryRateLimited    Class = "registry_rate_limited"
	ClassRegistryUnreachable    Class = "registry_unreachable"
	ClassImagePullDenied        Class = "image_pull_denied"
	ClassImageNotFound          Class = "image_not_found"
	ClassPortConflict           Class = "port_conflict"
	ClassDependencyUnhealthy    Class = "dependency_unhealthy"
	ClassHealthTimeout          Class = "health_timeout"
	ClassDockerBridgeFailed     Class = "docker_bridge_failed"
	ClassDockerSocketDenied     Class = "docker_socket_denied"
	ClassDockerDaemonFailed     Class = "docker_daemon_failed"
	ClassDockerMissing          Class = "docker_missing"
	ClassKernelNamespaceBlocked Class = "kernel_namespace_blocked"
	ClassClockSkew              Class = "clock_skew"
	ClassSecretUnavailable      Class = "secret_unavailable"
	ClassCancelled              Class = "cancelled"

	// The classes below are preflight conclusions rather than signatures found
	// in process output: they are measured before Apply mutates anything, so
	// Classify never produces them from text.
	ClassHostResourceShortage Class = "host_resource_shortage"
	ClassHostIncompatible     Class = "host_incompatible"
	ClassCgroupMemoryMissing  Class = "cgroup_memory_controller_missing"
)

// Classification is the closed result of classifying one failure text.
//
// Retryable states whether repeating the same operation can succeed once the
// named condition is addressed. Transient additionally states that the
// condition may clear on its own, which is the only case an automatic retry
// may act on.
type Classification struct {
	Class       Class
	Retryable   bool
	Transient   bool
	Remediation []string
}

// classProfile binds one class to its retry semantics and operator guidance.
type classProfile struct {
	retryable   bool
	transient   bool
	remediation []string
}

var classProfiles = map[Class]classProfile{
	ClassOOMKilled: {
		retryable: true,
		remediation: []string{
			"A container was killed for exceeding available memory (exit code 137).",
			"Free memory, add swap, or deselect an optional workload, then retry.",
		},
	},
	ClassDiskFull: {
		retryable: true,
		remediation: []string{
			"The container storage or workspace filesystem is full.",
			"Free space (for example `docker system prune`) and retry.",
		},
	},
	ClassImageArchMismatch: {
		remediation: []string{
			"A pinned image has no build for this machine's CPU architecture.",
			"Run this workload on a supported architecture; it cannot be applied here.",
		},
	},
	ClassRegistryRateLimited: {
		retryable: true,
		transient: true,
		remediation: []string{
			"The container registry rejected the pull with a rate limit.",
			"Wait for the limit to reset or authenticate to the registry, then retry.",
		},
	},
	ClassRegistryUnreachable: {
		retryable: true,
		transient: true,
		remediation: []string{
			"The container registry could not be reached.",
			"Check DNS and outbound network access from this host, then retry.",
		},
	},
	ClassImagePullDenied: {
		remediation: []string{
			"The registry denied access to a pinned image.",
			"Authenticate to the registry or correct the image reference.",
		},
	},
	ClassImageNotFound: {
		remediation: []string{
			"A pinned image reference or digest does not exist in the registry.",
			"Regenerate from the current authority so the pinned digests match.",
		},
	},
	ClassPortConflict: {
		retryable: true,
		remediation: []string{
			"A required host port is already bound by another process.",
			"Stop the conflicting listener or change the published port, then retry.",
		},
	},
	ClassDependencyUnhealthy: {
		retryable: true,
		remediation: []string{
			"A dependency container never reported healthy, so dependents were not started.",
			"Inspect the named container's logs, then retry.",
		},
	},
	ClassHealthTimeout: {
		retryable: true,
		transient: true,
		remediation: []string{
			"A service did not become healthy inside its wait budget.",
			"Slow storage or a cold image pull is the common cause on small devices; retry.",
		},
	},
	ClassDockerBridgeFailed: {
		retryable: true,
		remediation: []string{
			"Docker could not create or attach the container network.",
			"Check bridge networking, iptables, and address pool availability on this host.",
		},
	},
	ClassDockerSocketDenied: {
		retryable: true,
		remediation: []string{
			"This user cannot reach the Docker daemon socket.",
			"Add the user to the docker group and start a new session, then retry.",
		},
	},
	ClassDockerDaemonFailed: {
		retryable: true,
		transient: true,
		remediation: []string{
			"The Docker daemon is not reachable.",
			"Start the Docker service, then retry.",
		},
	},
	ClassDockerMissing: {
		retryable: true,
		remediation: []string{
			"No Docker binary is available on this host.",
			"Install Docker, then retry.",
		},
	},
	ClassKernelNamespaceBlocked: {
		remediation: []string{
			"The kernel blocks the container namespaces Docker requires.",
			"Restricted container virtualization cannot host this StackKit; use a host that exposes namespaces and cgroups.",
		},
	},
	ClassClockSkew: {
		retryable: true,
		remediation: []string{
			"A certificate was rejected as not yet valid or expired, which indicates host clock skew.",
			"Enable time synchronization on this host, then retry.",
		},
	},
	ClassSecretUnavailable: {
		remediation: []string{
			"A required runtime secret or environment value was not resolvable.",
			"Establish workload secret custody, then retry.",
		},
	},
	ClassCancelled: {
		retryable: true,
		remediation: []string{
			"The operation was cancelled before it completed.",
			"Retry when the host is ready.",
		},
	},
	ClassHostResourceShortage: {
		retryable: true,
		remediation: []string{
			"The host has less CPU, memory, or storage than this kit declares it needs.",
			"Use a larger host or select fewer workloads, then retry.",
		},
	},
	ClassHostIncompatible: {
		remediation: []string{
			"This host cannot run the kit: its architecture, kernel, or virtualization is unsupported.",
			"Move the StackKit to a supported host.",
		},
	},
	ClassCgroupMemoryMissing: {
		remediation: []string{
			"The kernel cgroup memory controller is disabled, so container memory limits are silently ignored.",
			"On Raspberry Pi OS add cgroup_enable=memory cgroup_memory=1 to the kernel command line and reboot.",
		},
	},
}

// profile returns the closed retry semantics and guidance for a class.
func profile(class Class) Classification {
	entry, known := classProfiles[class]
	if !known {
		return Classification{Class: ClassUnknown}
	}
	return Classification{
		Class:       class,
		Retryable:   entry.retryable,
		Transient:   entry.transient,
		Remediation: append([]string(nil), entry.remediation...),
	}
}

// Remediation returns the operator guidance declared for a class. It is a
// direct lookup, not a text match: callers that already know the class must
// never route its name back through Classify, which reads process output.
func Remediation(class Class) []string {
	entry, known := classProfiles[class]
	if !known {
		return nil
	}
	return append([]string(nil), entry.remediation...)
}

// Retryable reports whether the declared class can succeed on a retry once its
// condition is addressed.
func Retryable(class Class) bool {
	return classProfiles[class].retryable
}

// Classes returns every closed class in a stable order. It exists so the
// rollout event schema and documentation can be checked against one source.
func Classes() []Class {
	return []Class{
		ClassCancelled,
		ClassClockSkew,
		ClassDependencyUnhealthy,
		ClassDiskFull,
		ClassDockerBridgeFailed,
		ClassDockerDaemonFailed,
		ClassDockerMissing,
		ClassDockerSocketDenied,
		ClassHealthTimeout,
		ClassImageArchMismatch,
		ClassImageNotFound,
		ClassImagePullDenied,
		ClassKernelNamespaceBlocked,
		ClassOOMKilled,
		ClassPortConflict,
		ClassRegistryRateLimited,
		ClassRegistryUnreachable,
		ClassSecretUnavailable,
		ClassCgroupMemoryMissing,
		ClassHostIncompatible,
		ClassHostResourceShortage,
	}
}
