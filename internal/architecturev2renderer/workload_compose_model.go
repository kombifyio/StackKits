package architecturev2renderer

// The typed workload Compose model. The standalone Compose owner
// (internal/runtimeexecutor/nativehost) renders every workload project into
// it and marshals it to the compose.yaml the Stage 1 wrapper embeds; the
// Stage 2 native renderer (compose_native_docker_opentofu.go) decodes that
// file strictly back into the same model and translates it into
// kreuzwerker/docker resources. One model keeps both targets describing the
// same containers: a field the Compose renderer starts to emit is a field the
// native renderer must translate or refuse.

// WorkloadComposeDocument is one workload Compose project.
type WorkloadComposeDocument struct {
	Name     string                            `yaml:"name"`
	Services map[string]WorkloadComposeService `yaml:"services"`
	Networks map[string]WorkloadComposeNetwork `yaml:"networks"`
	Volumes  map[string]map[string]any         `yaml:"volumes,omitempty"`
	// Secrets are custody secrets Compose copies into a container as files;
	// each reads its value from the private .env interpolation file.
	Secrets map[string]WorkloadComposeSecret `yaml:"secrets,omitempty"`
}

// WorkloadComposeSecret is an environment-sourced Compose secret.
type WorkloadComposeSecret struct {
	Environment string `yaml:"environment"`
}

// WorkloadComposeServiceSecret places one secret file. Compose writes an
// environment-sourced secret into the container with this owner and mode.
type WorkloadComposeServiceSecret struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
	UID    string `yaml:"uid"`
	GID    string `yaml:"gid"`
	Mode   int    `yaml:"mode"`
}

// WorkloadComposeService is one container of a workload project.
type WorkloadComposeService struct {
	Image   string `yaml:"image"`
	Restart string `yaml:"restart,omitempty"`
	// Runtime names a registered OCI runtime other than the daemon default
	// (gVisor's runsc for the governed agent harness).
	Runtime     string                               `yaml:"runtime,omitempty"`
	Logging     *WorkloadComposeLogging              `yaml:"logging,omitempty"`
	OOMScoreAdj *int                                 `yaml:"oom_score_adj,omitempty"`
	Deploy      *WorkloadComposeDeploy               `yaml:"deploy,omitempty"`
	Command     []string                             `yaml:"command,omitempty"`
	Entrypoint  []string                             `yaml:"entrypoint,omitempty"`
	DependsOn   map[string]WorkloadComposeDependency `yaml:"depends_on,omitempty"`
	Environment map[string]string                    `yaml:"environment,omitempty"`
	Volumes     []any                                `yaml:"volumes,omitempty"`
	Networks    []string                             `yaml:"networks"`
	Ports       []string                             `yaml:"ports,omitempty"`
	Devices     []string                             `yaml:"devices,omitempty"`
	ExtraHosts  []string                             `yaml:"extra_hosts,omitempty"`
	StopSignal  string                               `yaml:"stop_signal,omitempty"`
	Init        *bool                                `yaml:"init,omitempty"`
	Labels      map[string]string                    `yaml:"labels,omitempty"`
	Healthcheck *WorkloadComposeHealthcheck          `yaml:"healthcheck,omitempty"`
	Secrets     []WorkloadComposeServiceSecret       `yaml:"secrets,omitempty"`
}

// WorkloadComposeDependency is one depends_on entry.
type WorkloadComposeDependency struct {
	Condition string `yaml:"condition"`
}

// WorkloadComposeDeploy carries the declared per-container ceiling. Compose
// applies deploy.resources outside Swarm, so this is the ordinary way to cap a
// container on a single host.
type WorkloadComposeDeploy struct {
	Resources WorkloadComposeResources `yaml:"resources"`
}

// WorkloadComposeResources holds the limits and reservations.
type WorkloadComposeResources struct {
	Limits       *WorkloadComposeResourceBounds `yaml:"limits,omitempty"`
	Reservations *WorkloadComposeResourceBounds `yaml:"reservations,omitempty"`
}

// WorkloadComposeResourceBounds is one limit or reservation.
type WorkloadComposeResourceBounds struct {
	Memory string `yaml:"memory,omitempty"`
	CPUs   string `yaml:"cpus,omitempty"`
	// Devices are reservation-only device requests (a GPU through CDI).
	Devices []WorkloadComposeDeviceRequest `yaml:"devices,omitempty"`
}

// WorkloadComposeDeviceRequest is one Compose device reservation. Docker
// hands a request with driver "cdi" to its CDI device driver, which injects
// the devices of the named CDI spec entry.
type WorkloadComposeDeviceRequest struct {
	Driver       string   `yaml:"driver"`
	DeviceIDs    []string `yaml:"device_ids"`
	Capabilities []string `yaml:"capabilities"`
}

// WorkloadComposeLogging bounds container logs. Without it the json-file
// driver grows without limit, and a homelab that ran fine for weeks fills its
// disk and takes the whole stack down with it.
type WorkloadComposeLogging struct {
	Driver  string            `yaml:"driver"`
	Options map[string]string `yaml:"options"`
}

// WorkloadComposeNetwork is a project network or an external reference.
type WorkloadComposeNetwork struct {
	Name     string `yaml:"name,omitempty"`
	External bool   `yaml:"external,omitempty"`
	Internal bool   `yaml:"internal,omitempty"`
}

// WorkloadComposeHealthcheck is a container health command.
type WorkloadComposeHealthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
}

// Lifecycle labels the Compose renderer sets on every workload service.
const (
	WorkloadComposeLifecycleLabel = "io.stackkit.lifecycle"
	WorkloadComposeLifecycleOnce  = "one-shot"
)
