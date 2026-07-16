package resolvedplan

import (
	pathpkg "path"
	"regexp"
	"strings"
)

// unixSocketPathPattern deliberately uses portable ASCII path segments. Socket
// paths are Linux runtime authority, so host-OS filepath semantics must never
// influence whether a path is accepted by the compiler.
var unixSocketPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*[.]sock$`)

const maxUnixSocketPathBytes = 107

type directSocketEndpointPath struct {
	fixedPath         string
	fromDaemonBinding bool
}

// parseDirectSocketEndpointPath enforces the raw and resolved endpoint XOR:
// either the authority asserts one fixed path for every selected daemon, or
// each concrete render instance takes its path from its exact daemon binding.
// Treating omission as a default would make privileged socket selection
// implicit, so both and neither are rejected before CUE normalization too.
func parseDirectSocketEndpointPath(endpoint map[string]any, endpointPath string, code ErrorCode) (directSocketEndpointPath, error) {
	rawPath, hasPath := endpoint["path"]
	rawSource, hasSource := endpoint["pathSource"]
	if hasPath == hasSource {
		return directSocketEndpointPath{}, fail(code, endpointPath, "exactly one of path or pathSource must be declared for a direct Docker socket endpoint")
	}
	if hasPath {
		socketPath, ok := rawPath.(string)
		if !ok || strings.TrimSpace(socketPath) == "" {
			return directSocketEndpointPath{}, fail(code, endpointPath+".path", "direct Docker socket path must be a non-empty string")
		}
		if err := validateUnixSocketPath(socketPath, endpointPath+".path", code); err != nil {
			return directSocketEndpointPath{}, err
		}
		return directSocketEndpointPath{fixedPath: socketPath}, nil
	}
	pathSource, ok := rawSource.(string)
	if !ok || pathSource != dockerSocketPathSourceDaemonBinding {
		return directSocketEndpointPath{}, fail(code, endpointPath+".pathSource", "direct Docker socket pathSource must be %q", dockerSocketPathSourceDaemonBinding)
	}
	return directSocketEndpointPath{fromDaemonBinding: true}, nil
}

func validateUnixSocketPath(socketPath, valuePath string, code ErrorCode) error {
	// len(string) is the UTF-8 byte length in Go, which is the Linux ABI bound.
	if byteLength := len(socketPath); byteLength > maxUnixSocketPathBytes {
		return fail(code, valuePath, "Unix socket path is %d UTF-8 bytes; Linux AF_UNIX paths permit at most %d bytes before the terminating NUL", byteLength, maxUnixSocketPathBytes)
	}
	if !unixSocketPathPattern.MatchString(socketPath) || pathpkg.Clean(socketPath) != socketPath {
		return fail(code, valuePath, "Unix socket path %q must be an absolute canonical path with portable ASCII segments and a .sock suffix", socketPath)
	}
	for _, segment := range strings.Split(strings.TrimPrefix(socketPath, "/"), "/") {
		if segment == "." || segment == ".." {
			return fail(code, valuePath, "Unix socket path %q must not contain dot or parent-directory segments", socketPath)
		}
	}
	return nil
}

// validateRawInventoryRuntimeDaemonSocketPaths validates the security-relevant
// value before CUE normalization. Structural/schema errors remain owned by CUE;
// this pass only acts when the raw shape exposes a concrete string socket path.
func validateRawInventoryRuntimeDaemonSocketPaths(inventory map[string]any) error {
	rawNodes, ok := inventory["nodes"].(map[string]any)
	if !ok {
		return nil
	}
	for _, nodeRef := range sortedStringMapKeys(rawNodes) {
		rawNode, ok := rawNodes[nodeRef].(map[string]any)
		if !ok {
			continue
		}
		rawDaemons, ok := rawNode["runtimeDaemons"].(map[string]any)
		if !ok {
			continue
		}
		for _, daemonRef := range sortedStringMapKeys(rawDaemons) {
			rawDaemon, ok := rawDaemons[daemonRef].(map[string]any)
			if !ok {
				continue
			}
			socketPath, ok := rawDaemon["socketPath"].(string)
			if !ok {
				continue
			}
			valuePath := "inventory.nodes." + nodeRef + ".runtimeDaemons." + daemonRef + ".socketPath"
			if err := validateUnixSocketPath(socketPath, valuePath, ErrInvalidInput); err != nil {
				return err
			}
		}
	}
	return nil
}
