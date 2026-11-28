package attackpath

import (
	"net/netip"
	"path"
	"strings"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
)

// Snapshot is everything Evaluate reads. A nil or stale section makes the
// edges it owns unknown.
type Snapshot struct {
	// Host is a stable reference to the node, never a secret.
	Host string
	// Evidence is the stackkit.host-security-evidence/v1 document. It carries
	// its own observation time and expiry.
	Evidence *hostsecurity.Evidence
	// Routes is the compiler-owned route table with the backend of each route.
	Routes *RouteFacts
	// Containers is the container inspection of the host.
	Containers *ContainerFacts
	// Backups names where backup credentials rest.
	Backups *BackupFacts
}

// RouteFacts is configured, not observed: it says what the plan declares.
type RouteFacts struct {
	Provenance
	Table accessgatetable.Table
	// Backends maps a route ID to the container that serves it. A route with
	// no entry has an unknown backend.
	Backends map[string]string
}

// ContainerFacts is the observed container runtime state.
type ContainerFacts struct {
	Provenance
	Containers []Container
}

// Container is the attack-relevant projection of docker inspect.
type Container struct {
	Name       string
	Running    bool
	Privileged bool
	CapAdd     []string
	Mounts     []Mount
	Ports      []PortBinding
}

// Mount is one bind or volume mount of a container.
type Mount struct {
	Source      string
	Destination string
	ReadWrite   bool
}

// PortBinding is one container port published on the host.
type PortBinding struct {
	HostIP    string
	Port      string
	Transport string
}

// BackupFacts names the backup credentials and where they rest on the host.
type BackupFacts struct {
	Provenance
	Credentials []BackupCredential
}

// BackupCredential is one backup credential file. Ref is a stable name, never
// the secret; HostPath is where the file rests on the host.
type BackupCredential struct {
	Ref      string
	HostPath string
}

// FromInspect projects a docker inspect result. Binds and mounts are merged,
// the same source and destination counted once.
func FromInspect(info docker.ContainerInfo) Container {
	container := Container{
		Name:       strings.TrimPrefix(info.Name, "/"),
		Running:    info.State.Running,
		Privileged: info.HostConfig.Privileged,
		CapAdd:     append([]string(nil), info.HostConfig.CapAdd...),
	}
	seen := map[string]bool{}
	add := func(source, destination string, rw bool) {
		key := source + "\x00" + destination
		if seen[key] {
			return
		}
		seen[key] = true
		container.Mounts = append(container.Mounts, Mount{Source: source, Destination: destination, ReadWrite: rw})
	}
	for _, mount := range info.Mounts {
		add(mount.Source, mount.Destination, mount.RW)
	}
	for _, mount := range info.HostConfig.Mounts {
		add(mount.Source, mount.Target, !mount.ReadOnly)
	}
	for _, bind := range info.HostConfig.Binds {
		parts := strings.Split(bind, ":")
		if len(parts) < 2 {
			continue
		}
		rw := true
		if len(parts) > 2 && strings.Contains(","+parts[2]+",", ",ro,") {
			rw = false
		}
		add(parts[0], parts[1], rw)
	}
	for port, bindings := range info.HostConfig.PortBindings {
		transport := "tcp"
		if slash := strings.LastIndex(port, "/"); slash >= 0 {
			transport = port[slash+1:]
		}
		for _, binding := range bindings {
			container.Ports = append(container.Ports, PortBinding{HostIP: binding.HostIP, Port: binding.HostPort, Transport: transport})
		}
	}
	return container
}

// dangerousCapabilities are the capabilities that give a container the host's
// kernel or namespaces. The list is fixed.
var dangerousCapabilities = map[string]bool{"SYS_ADMIN": true, "SYS_MODULE": true, "SYS_RAWIO": true}

func hasDangerousCapability(capabilities []string) (string, bool) {
	for _, capability := range capabilities {
		name := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(capability)), "CAP_")
		if dangerousCapabilities[name] {
			return name, true
		}
	}
	return "", false
}

func isDockerSocket(mount Mount) bool {
	for _, value := range []string{mount.Source, mount.Destination} {
		base := path.Base(path.Clean(value))
		if base == "docker.sock" || base == "podman.sock" {
			return true
		}
	}
	return false
}

// exposesHostRoot reports a writable bind of the host root directory.
func exposesHostRoot(mount Mount) bool {
	return mount.ReadWrite && path.Clean(mount.Source) == "/"
}

// reachesPath reports whether the mount source is the file or a directory
// containing it, so the container can read the credential.
func reachesPath(source, target string) bool {
	source, target = path.Clean(source), path.Clean(target)
	if source == "/" || source == "." || source == "" {
		return false // reported as host_root_mount, not as a credential mount
	}
	return source == target || strings.HasPrefix(target, source+"/")
}

// beyondLoopback reports a published port whose host address is not loopback.
// An empty host address means every interface.
func beyondLoopback(hostIP string) bool {
	hostIP = strings.Trim(hostIP, "[]")
	if hostIP == "" {
		return true
	}
	address, err := netip.ParseAddr(hostIP)
	if err != nil {
		return true // an unparsable address is not shown to be loopback
	}
	return !address.IsLoopback()
}
