package applyoutcome

import "strings"

// matcher binds one class to the lowercase substrings that prove it. The
// table is ordered most specific first: the first matching entry wins, so a
// narrow signature such as a denied Docker socket is never swallowed by a
// broad one such as "operation not permitted".
type matcher struct {
	class    Class
	markers  []string
	requires []string
}

// matchers recognizes the failure signatures container runtimes actually emit
// on small self-hosted hosts. Every marker is a literal lowercase substring of
// observed Docker, Compose, containerd, or registry output.
var matchers = []matcher{
	{class: ClassDockerMissing, markers: []string{
		"docker-not-found",
		"required local docker runtime is not installed",
		"executable file not found",
	}},
	{class: ClassDockerSocketDenied, markers: []string{
		"permission denied while trying to connect to the docker daemon socket",
		"docker.sock: connect: permission denied",
		"got permission denied while trying to connect",
	}},
	{class: ClassDockerDaemonFailed, markers: []string{
		"cannot connect to the docker daemon",
		"is the docker daemon running",
		"docker daemon is not running",
		"error during connect",
	}},
	{class: ClassOOMKilled, markers: []string{
		"oomkilled",
		"exited (137)",
		"exit code 137",
		"exit status 137",
		"out of memory",
		"cannot allocate memory",
	}},
	{class: ClassDiskFull, markers: []string{
		"no space left on device",
		"disk quota exceeded",
		"insufficient space",
	}},
	// Namespace denial shares the "failed to register layer" prefix with a
	// full disk, so this entry is matched only after the resource classes and
	// additionally requires the kernel's permission signal.
	{class: ClassKernelNamespaceBlocked, requires: []string{"not permitted"}, markers: []string{
		"failed to register layer",
		"unshare",
		"applylayer",
		"clone:",
	}},
	{class: ClassImageArchMismatch, markers: []string{
		"no matching manifest for linux/",
		"does not match the specified platform",
		"no match for platform in manifest",
		"exec format error",
	}},
	{class: ClassRegistryRateLimited, markers: []string{
		"toomanyrequests",
		"pull rate limit",
		"429 too many requests",
	}},
	{class: ClassImagePullDenied, markers: []string{
		"pull access denied",
		"unauthorized: authentication required",
		"requested access to the resource is denied",
		"authentication required",
	}},
	{class: ClassImageNotFound, markers: []string{
		"manifest unknown",
		"repository does not exist",
		"not found: manifest",
	}},
	{class: ClassRegistryUnreachable, markers: []string{
		"tls handshake timeout",
		"no such host",
		"temporary failure in name resolution",
		"failed to resolve reference",
		"failed to do request",
		"connection reset by peer",
		"unexpected eof",
		"i/o timeout",
		"network is unreachable",
	}},
	{class: ClassPortConflict, markers: []string{
		"port is already allocated",
		"address already in use",
		"failed to bind host port",
		"ports are not available",
		"bind for ",
	}},
	{class: ClassDockerBridgeFailed, markers: []string{
		"non-overlapping ipv4 address pool",
		"failed to create network",
		"failed to set up container networking",
		"failed to program nat chain",
		"iptables failed",
		"could not find an available",
	}},
	{class: ClassClockSkew, markers: []string{
		"certificate is not yet valid",
		"certificate has expired",
		"is not yet valid",
	}},
	{class: ClassSecretUnavailable, markers: []string{
		"is missing a value",
		"variable is not set",
		"resolve owner-only material",
		"required variable",
	}},
	{class: ClassDependencyUnhealthy, markers: []string{
		"dependency failed to start",
		"is unhealthy",
		"container is unhealthy",
		"did not complete successfully",
	}},
	{class: ClassHealthTimeout, markers: []string{
		"timed out waiting",
		"timeout waiting",
		"wait timeout",
		"application failed to start",
		"health check timed out",
	}},
	{class: ClassCancelled, markers: []string{
		"context canceled",
		"context cancelled",
	}},
}

// Classify recognizes the failure signature in bounded process output or error
// text. An unmatched text returns ClassUnknown, never a guessed class: the
// caller keeps its bounded excerpt so the failure stays diagnosable.
//
// The input is matched case-insensitively and is never retained.
func Classify(text string) Classification {
	normalized := strings.ToLower(text)
	if strings.TrimSpace(normalized) == "" {
		return Classification{Class: ClassUnknown}
	}
	for _, candidate := range matchers {
		if !containsAll(normalized, candidate.requires) {
			continue
		}
		if containsAny(normalized, candidate.markers) {
			return profile(candidate.class)
		}
	}
	return Classification{Class: ClassUnknown}
}

// Recognized reports whether Classify produced a closed class for this text.
func Recognized(text string) bool {
	return Classify(text).Class != ClassUnknown
}

func containsAny(normalized string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func containsAll(normalized string, required []string) bool {
	for _, marker := range required {
		if !strings.Contains(normalized, marker) {
			return false
		}
	}
	return true
}
