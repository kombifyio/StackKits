package hostconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
)

// containerRuntimeNamePattern bounds the runtime names a daemon may report;
// they travel into the inventory and the plan.
var containerRuntimeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// GvisorRunscNetworkArg makes runsc use the network namespace Docker created
// for the container instead of gVisor's own netstack. Docker's embedded DNS
// (127.0.0.11) is an iptables redirect inside that namespace, which netstack
// never sees, so a sandboxed component could not resolve the services it
// talks to (Paperclip its PostgreSQL, OpenHands the node's Ollama). The
// container keeps its Docker networks and their internal-only egress rule;
// gVisor still intercepts every syscall and owns the filesystem view.
const GvisorRunscNetworkArg = "--network=host"

// ObserveContainerRuntimes lists the OCI runtimes the local Docker daemon has
// registered (runc, runsc, ...), sorted. It returns nil when the daemon was
// not observed, so an absent fact is never mistaken for "no sandbox runtime".
func ObserveContainerRuntimes(ctx context.Context, source LocalSource) []string {
	if source == nil {
		source = osLocalSource{}
	}
	if _, err := source.LookPath("docker"); err != nil {
		return nil
	}
	output, err := source.Run(ctx, "docker", "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		return nil
	}
	return ParseDockerRuntimes(output)
}

// ParseDockerRuntimes parses `docker info --format '{{json .Runtimes}}'`: a
// map from runtime name to its configuration. nil when it is not parseable.
// runsc counts only when registered with GvisorRunscNetworkArg: without it a
// sandboxed component cannot resolve its services, so it is not the runtime
// the governed components need and the gvisor-runsc resolution applies.
func ParseDockerRuntimes(output []byte) []string {
	var runtimes map[string]struct {
		RuntimeArgs []string `json:"runtimeArgs"`
	}
	if err := json.Unmarshal(output, &runtimes); err != nil || runtimes == nil {
		return nil
	}
	names := make([]string, 0, len(runtimes))
	for name, runtime := range runtimes {
		if name == "runsc" && !slices.Contains(runtime.RuntimeArgs, GvisorRunscNetworkArg) {
			continue
		}
		if containerRuntimeNamePattern.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func validateContainerRuntimeFacts(runtimes []string) error {
	seen := map[string]bool{}
	for _, runtime := range runtimes {
		if !containerRuntimeNamePattern.MatchString(runtime) {
			return fmt.Errorf("container runtime name %q is malformed", runtime)
		}
		if seen[runtime] {
			return fmt.Errorf("container runtime %q is reported twice", runtime)
		}
		seen[runtime] = true
	}
	return nil
}

func containerRuntimesDocument(runtimes []string) []any {
	result := make([]any, 0, len(runtimes))
	for _, runtime := range runtimes {
		result = append(result, runtime)
	}
	return result
}
