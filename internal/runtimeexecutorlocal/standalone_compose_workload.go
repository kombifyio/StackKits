package runtimeexecutorlocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"gopkg.in/yaml.v3"
)

const (
	standaloneComposeAdapterRef     = "standalone-compose"
	standaloneComposeModuleRef      = "stackkits-standalone-compose-runtime"
	standaloneComposeRoutingNetwork = "stackkit-basement-core"
	// standaloneComposeHealthNetwork carries only the entry component of an
	// unrouted workload so its loopback health port can bind.
	standaloneComposeHealthNetwork = "stackkit-workload-health"
	standaloneComposeOutputMax     = 256 << 10
)

type standaloneComposeProcessRunner interface {
	Run(context.Context, []string, string) ([]byte, error)
}

// standaloneComposeProcessError carries the bounded, sanitized output of a
// failed closed-contract Compose process. The excerpt is what distinguishes a
// full disk, a missing architecture build, and a rate-limited registry from
// each other; discarding it made every failure read the same.
type standaloneComposeProcessError struct {
	output string
	cause  error
}

func (err *standaloneComposeProcessError) Error() string {
	return fmt.Sprintf("docker-compose-exit; output=%q", err.output)
}

func (err *standaloneComposeProcessError) Unwrap() error { return err.cause }

type standaloneComposeHTTPProber interface {
	Probe(context.Context, string) (int, error)
}

// osStandaloneComposeWorkloadOperations is the StackKits-owned no-PaaS
// application adapter. It receives only a validated workload bundle and exact
// route authority, persists owner-only runtime files, and calls the fixed local
// Docker Compose capability. Server and Docker-daemon lifecycle stay outside
// this boundary.
type osStandaloneComposeWorkloadOperations struct {
	workspaceRoot string
	runner        standaloneComposeProcessRunner
	prober        standaloneComposeHTTPProber
}

// NewOSStandaloneComposeWorkloadOperations constructs the local no-PaaS
// workload adapter for an existing owner workspace.
func NewOSStandaloneComposeWorkloadOperations(workspaceRoot string) (SelectedPaaSWorkloadOperations, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, errors.New("standalone Compose operations require a workspace root")
	}
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, errors.New("resolve standalone Compose workspace")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("standalone Compose operations require an existing plain workspace directory")
	}
	return &osStandaloneComposeWorkloadOperations{
		workspaceRoot: filepath.Clean(absolute),
		runner:        osStandaloneComposeProcessRunner{},
		prober:        osStandaloneComposeHTTPProber{},
	}, nil
}

func (o *osStandaloneComposeWorkloadOperations) ApplyWorkload(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (SelectedPaaSApplyReceipt, error) {
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if err := o.persist(project); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if _, err := o.runner.Run(ctx, standaloneComposeArgs(project, "up"), project.directory); err != nil {
		return SelectedPaaSApplyReceipt{}, fmt.Errorf("standalone Docker Compose Apply did not complete: %w", err)
	}
	return SelectedPaaSApplyReceipt{
		InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied",
	}, nil
}

func (o *osStandaloneComposeWorkloadOperations) ObserveWorkload(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (SelectedPaaSWorkloadObservation, error) {
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.verifyPersisted(project); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	raw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, fmt.Errorf("standalone Docker Compose status observation failed: %w", err)
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := validateStandaloneComposeRouteReadback(
		statuses[project.bundle.EntryComponent],
		project.bundle.Route,
	); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	components, err := observeStandaloneComposeComponents(project.bundle.Components, statuses)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	portRaw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "port"), project.directory)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, fmt.Errorf("standalone Docker Compose route port observation failed: %w", err)
	}
	address, err := standaloneComposeLoopbackAddress(portRaw)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	entry := project.entry
	status, err := o.prober.Probe(ctx, "http://"+address+entry.HealthPath)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, fmt.Errorf("standalone workload route health probe failed: %w", err)
	}
	return SelectedPaaSWorkloadObservation{
		WorkloadRef: deployment.WorkloadRef, Release: deployment.Release,
		InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest,
		Status: "running", Components: components,
		Route: SelectedPaaSRouteObservation{
			RouteRef: deployment.Route.ID, ServiceRef: deployment.Route.ServiceRef,
			ModuleRef: deployment.Route.ModuleRef, Exposure: deployment.Route.Exposure,
			Protocol: deployment.Route.Protocol, UpstreamProtocol: deployment.Route.UpstreamProtocol,
			HealthGateRef: deployment.Route.HealthGateRef, BackendPoolRef: deployment.Route.BackendPoolRef,
			Host: deployment.Route.Host, RoutePath: deployment.Route.Path,
			Port: deployment.Route.Port, TargetPort: deployment.Route.TargetPort,
			TLSRequired: deployment.Route.TLSRequired, TLSMode: deployment.Route.TLSMode,
			TLSMinVersion: deployment.Route.TLSMinVersion,
			TLSProfileRef: deployment.Route.TLSProfileRef, TLSIssuerRef: deployment.Route.TLSIssuerRef,
			TLSOwnerCapabilityRef: deployment.Route.TLSOwnerCapabilityRef,
			Method:                "GET", Path: entry.HealthPath, Status: "healthy", HTTPStatus: status,
		},
	}, nil
}

type standaloneComposeProject struct {
	name        string
	directory   string
	compose     []byte
	environment []byte
	bundle      architecturev2renderer.ApplicationDeliveryBundleDescriptor
	entry       architecturev2renderer.ApplicationDeliveryComponentDescriptor
}

func (o *osStandaloneComposeWorkloadOperations) prepare(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (standaloneComposeProject, error) {
	if ctx == nil {
		return standaloneComposeProject{}, errors.New("standalone Compose operations require a context")
	}
	if err := ctx.Err(); err != nil {
		return standaloneComposeProject{}, err
	}
	if o == nil || o.workspaceRoot == "" || o.runner == nil || o.prober == nil {
		return standaloneComposeProject{}, errors.New("standalone Compose operations are not initialized")
	}
	if deployment.RuntimeAdapter.ID != standaloneComposeAdapterRef ||
		deployment.RuntimeAdapter.ModuleRef != standaloneComposeModuleRef {
		return standaloneComposeProject{}, errors.New("deployment is not bound to the standalone Compose adapter")
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
	if err != nil {
		return standaloneComposeProject{}, fmt.Errorf("validate standalone workload bundle: %w", err)
	}
	if bundle.WorkloadRef != deployment.WorkloadRef || bundle.ModuleRef != deployment.ModuleRef ||
		bundle.Release != deployment.Release || bundle.SiteRef != deployment.SiteRef ||
		bundle.NodeRef != deployment.NodeRef || bundle.InstanceRef != deployment.InstanceRef ||
		bundle.Route != deployment.Route {
		return standaloneComposeProject{}, errors.New("standalone workload bundle differs from the authorized deployment")
	}
	entry, ok := standaloneComposeComponent(bundle.Components, bundle.EntryComponent)
	if !ok || entry.HealthKind != "http" || !strings.HasPrefix(entry.HealthPath, "/") ||
		(bundle.Route.ID != "" && entry.HealthPort != bundle.Route.TargetPort) {
		return standaloneComposeProject{}, errors.New("standalone workload entry component has no exact HTTP health contract")
	}
	compose, environment, err := o.render(bundle)
	if err != nil {
		return standaloneComposeProject{}, err
	}
	name := "stackkit-" + bundle.WorkloadRef + "-" + bundle.NodeRef
	directory := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "applications", name)
	return standaloneComposeProject{
		name: name, directory: directory, compose: compose, environment: environment,
		bundle: bundle, entry: entry,
	}, nil
}

type standaloneComposeDocument struct {
	Name     string                              `yaml:"name"`
	Services map[string]standaloneComposeService `yaml:"services"`
	Networks map[string]standaloneComposeNetwork `yaml:"networks"`
	Volumes  map[string]map[string]any           `yaml:"volumes,omitempty"`
}

type standaloneComposeService struct {
	Image       string                                 `yaml:"image"`
	Restart     string                                 `yaml:"restart,omitempty"`
	Logging     *standaloneComposeLogging              `yaml:"logging,omitempty"`
	OOMScoreAdj *int                                   `yaml:"oom_score_adj,omitempty"`
	Deploy      *standaloneComposeDeploy               `yaml:"deploy,omitempty"`
	Command     []string                               `yaml:"command,omitempty"`
	DependsOn   map[string]standaloneComposeDependency `yaml:"depends_on,omitempty"`
	Environment map[string]string                      `yaml:"environment,omitempty"`
	Volumes     []string                               `yaml:"volumes,omitempty"`
	Networks    []string                               `yaml:"networks"`
	Ports       []string                               `yaml:"ports,omitempty"`
	Labels      map[string]string                      `yaml:"labels,omitempty"`
	Healthcheck *standaloneComposeHealthcheck          `yaml:"healthcheck,omitempty"`
}

type standaloneComposeDependency struct {
	Condition string `yaml:"condition"`
}

// standaloneComposeDeploy carries the declared per-container ceiling. Compose
// applies deploy.resources outside Swarm, so this is the ordinary way to cap a
// container on a single host.
type standaloneComposeDeploy struct {
	Resources standaloneComposeResources `yaml:"resources"`
}

type standaloneComposeResources struct {
	Limits       *standaloneComposeResourceBounds `yaml:"limits,omitempty"`
	Reservations *standaloneComposeResourceBounds `yaml:"reservations,omitempty"`
}

type standaloneComposeResourceBounds struct {
	Memory string `yaml:"memory,omitempty"`
	CPUs   string `yaml:"cpus,omitempty"`
}

// componentDeploy renders only what the component actually declared. A
// component without declared resources gets no deploy block at all: a ceiling
// invented for a footprint nobody measured would cause the very kill this
// exists to prevent.
func componentDeploy(declared *architecturev2renderer.ApplicationDeliveryResourcesDescriptor) *standaloneComposeDeploy {
	if declared == nil {
		return nil
	}
	deploy := &standaloneComposeDeploy{}
	if declared.MemoryLimit != "" || declared.CPUs > 0 {
		deploy.Resources.Limits = &standaloneComposeResourceBounds{Memory: declared.MemoryLimit}
		if declared.CPUs > 0 {
			deploy.Resources.Limits.CPUs = strconv.FormatFloat(declared.CPUs, 'f', -1, 64)
		}
	}
	if declared.MemoryReservation != "" {
		deploy.Resources.Reservations = &standaloneComposeResourceBounds{Memory: declared.MemoryReservation}
	}
	if deploy.Resources.Limits == nil && deploy.Resources.Reservations == nil {
		return nil
	}
	return deploy
}

// standaloneComposeLogging bounds container logs. Without it the json-file
// driver grows without limit, and a homelab that ran fine for weeks fills its
// disk and takes the whole stack down with it.
type standaloneComposeLogging struct {
	Driver  string            `yaml:"driver"`
	Options map[string]string `yaml:"options"`
}

// workloadLogging is the bounded log policy every workload container gets.
func workloadLogging() *standaloneComposeLogging {
	return &standaloneComposeLogging{
		Driver:  "json-file",
		Options: map[string]string{"max-size": "10m", "max-file": "3"},
	}
}

// oomScoreAdjForRole biases which container the kernel kills first when the
// host runs out of memory.
//
// The default OOM killer picks by memory footprint, which on a small device
// means it takes the database a workload cannot survive losing. The declared
// component role already says what each container is, so the bias follows it:
// stateful components are protected, and a recomputable worker such as machine
// learning is offered up first. This does not prevent an out-of-memory kill; it
// decides which one hurts least.
func oomScoreAdjForRole(role string) *int {
	scores := map[string]int{
		"database":         -500,
		"database-init":    -500,
		"cache":            -250,
		"application":      -100,
		"machine-learning": 500,
	}
	score, declared := scores[role]
	if !declared {
		return nil
	}
	return &score
}

type standaloneComposeNetwork struct {
	Name     string `yaml:"name,omitempty"`
	External bool   `yaml:"external,omitempty"`
	Internal bool   `yaml:"internal,omitempty"`
}

type standaloneComposeHealthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
}

func (o *osStandaloneComposeWorkloadOperations) render(
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
) ([]byte, []byte, error) {
	document := standaloneComposeDocument{
		Name:     "stackkit-" + bundle.WorkloadRef + "-" + bundle.NodeRef,
		Services: map[string]standaloneComposeService{},
		Networks: map[string]standaloneComposeNetwork{},
		Volumes:  map[string]map[string]any{},
	}
	lifecycle := make(map[string]string, len(bundle.Components))
	for _, component := range bundle.Components {
		lifecycle[component.ID] = component.Lifecycle
		for _, networkRef := range component.NetworkRefs {
			document.Networks[networkRef] = standaloneComposeNetwork{Internal: true}
		}
	}
	secretValues := map[string]string{}
	for _, component := range bundle.Components {
		service := standaloneComposeService{
			Image:       component.ImageRef + "@" + component.ImageDigest,
			Command:     append([]string(nil), component.Command...),
			DependsOn:   map[string]standaloneComposeDependency{},
			Environment: map[string]string{}, Networks: append([]string(nil), component.NetworkRefs...),
		}
		service.Deploy = componentDeploy(component.Resources)
		if component.Lifecycle == "daemon" {
			service.Restart = "unless-stopped"
			service.Logging = workloadLogging()
			service.OOMScoreAdj = oomScoreAdjForRole(component.Role)
		}
		for _, dependency := range component.DependsOn {
			condition := "service_started"
			if lifecycle[dependency] == "one-shot" {
				condition = "service_completed_successfully"
			}
			service.DependsOn[dependency] = standaloneComposeDependency{Condition: condition}
		}
		for key, value := range component.Environment {
			service.Environment[key] = value
		}
		for environmentName, slot := range component.SecretEnvironment {
			secretRef, exists := bundle.SecretRefs[slot]
			if !exists {
				return nil, nil, errors.New("standalone component references an absent secret slot")
			}
			material, err := localevidence.ResolveLocalSecretMaterial(o.workspaceRoot, secretRef)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve owner-only material for secret slot %q: %w", slot, err)
			}
			variable := standaloneComposeSecretVariable(component.ID, environmentName)
			secretValues[variable] = string(material)
			service.Environment[environmentName] = "${" + variable + ":?required}"
		}
		for _, volume := range component.Volumes {
			ref := component.ID + "-" + volume.ID
			document.Volumes[ref] = map[string]any{}
			service.Volumes = append(service.Volumes, ref+":"+volume.Target)
		}
		if len(component.HealthCommand) > 0 {
			service.Healthcheck = &standaloneComposeHealthcheck{
				Test:     append([]string{"CMD"}, component.HealthCommand...),
				Interval: "10s", Timeout: "5s", Retries: 12, StartPeriod: "10s",
			}
		}
		if component.ID == bundle.EntryComponent {
			service.Ports = []string{fmt.Sprintf("127.0.0.1::%d", component.HealthPort)}
			if bundle.Route.ID != "" {
				document.Networks["stackkit-routing"] = standaloneComposeNetwork{Name: standaloneComposeRoutingNetwork, External: true}
				service.Networks = append(service.Networks, "stackkit-routing")
				service.Labels = standaloneComposeRouteLabels(bundle.Route)
			} else {
				// Docker cannot bind a published port for a container attached
				// only to internal networks, so the loopback health port the
				// observation contract requires would never materialize. Give
				// the entry component one workload-local, non-internal bridge;
				// every other component stays internal-only.
				document.Networks[standaloneComposeHealthNetwork] = standaloneComposeNetwork{}
				service.Networks = append(service.Networks, standaloneComposeHealthNetwork)
			}
		}
		sort.Strings(service.Networks)
		sort.Strings(service.Volumes)
		document.Services[component.ID] = service
	}
	compose, err := yaml.Marshal(document)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal standalone Compose definition: %w", err)
	}
	variables := make([]string, 0, len(secretValues))
	for variable := range secretValues {
		variables = append(variables, variable)
	}
	sort.Strings(variables)
	var environment strings.Builder
	for _, variable := range variables {
		environment.WriteString(variable)
		environment.WriteByte('=')
		environment.WriteString(secretValues[variable])
		environment.WriteByte('\n')
	}
	return compose, []byte(environment.String()), nil
}

func standaloneComposeRouteLabels(route architecturev2renderer.ApplicationDeliveryRouteDescriptor) map[string]string {
	router := "stackkit-" + route.ServiceRef
	rule := "PathPrefix(`" + route.Path + "`)"
	if route.Host != "" {
		rule = "Host(`" + route.Host + "`)"
		if route.Path != "" && route.Path != "/" {
			rule += " && PathPrefix(`" + route.Path + "`)"
		}
	}
	entrypoint := "web"
	if route.TLSRequired {
		entrypoint = "websecure"
	}
	labels := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": standaloneComposeRoutingNetwork,
		"traefik.http.routers." + router + ".entrypoints":                 entrypoint,
		"traefik.http.routers." + router + ".rule":                        rule,
		"traefik.http.services." + router + ".loadbalancer.server.port":   strconv.Itoa(route.TargetPort),
		"traefik.http.services." + router + ".loadbalancer.server.scheme": route.UpstreamProtocol,
		"io.stackkit.route.id":                                            route.ID,
		"io.stackkit.route.exposure":                                      route.Exposure,
		"io.stackkit.route.protocol":                                      route.Protocol,
		"io.stackkit.route.upstream-protocol":                             route.UpstreamProtocol,
		"io.stackkit.route.health-gate-ref":                               route.HealthGateRef,
		"io.stackkit.route.backend-pool-ref":                              route.BackendPoolRef,
		"io.stackkit.route.path":                                          route.Path,
		"io.stackkit.route.port":                                          strconv.Itoa(route.Port),
		"io.stackkit.route.target-port":                                   strconv.Itoa(route.TargetPort),
		"io.stackkit.route.tls.required":                                  strconv.FormatBool(route.TLSRequired),
		"io.stackkit.route.tls.mode":                                      route.TLSMode,
		"io.stackkit.route.tls.min-version":                               route.TLSMinVersion,
		"io.stackkit.route.tls.profile-ref":                               route.TLSProfileRef,
		"io.stackkit.route.tls.issuer-ref":                                route.TLSIssuerRef,
		"io.stackkit.route.tls.owner-capability-ref":                      route.TLSOwnerCapabilityRef,
	}
	if route.TLSRequired {
		labels["traefik.http.routers."+router+".tls"] = "true"
		if route.TLSProfileRef != "" {
			labels["traefik.http.routers."+router+".tls.options"] = route.TLSProfileRef + "@file"
		}
		if route.TLSIssuerRef != "" {
			labels["traefik.http.routers."+router+".tls.certresolver"] = route.TLSIssuerRef
		}
	}
	return labels
}

func standaloneComposeSecretVariable(componentID, environmentName string) string {
	value := strings.ToUpper(componentID + "_" + environmentName)
	replacer := strings.NewReplacer("-", "_", ".", "_")
	return "STACKKIT_SECRET_" + replacer.Replace(value)
}

func (o *osStandaloneComposeWorkloadOperations) persist(project standaloneComposeProject) error {
	if err := os.MkdirAll(project.directory, 0o700); err != nil {
		return fmt.Errorf("create standalone Compose runtime directory: %w", err)
	}
	if err := os.Chmod(project.directory, 0o700); err != nil {
		return fmt.Errorf("restrict standalone Compose runtime directory: %w", err)
	}
	root, err := confinedfs.Open(project.directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	for name, content := range map[string][]byte{"compose.yaml": project.compose, ".env": project.environment} {
		result, err := view.WriteAtomic0600(name, content)
		if err != nil {
			return fmt.Errorf("persist private standalone Compose %s: %w", name, err)
		}
		if !result.Installed || !result.FileSynced {
			return errors.New("standalone Compose persistence did not prove an installed private artifact")
		}
	}
	return nil
}

func (o *osStandaloneComposeWorkloadOperations) verifyPersisted(project standaloneComposeProject) error {
	root, err := confinedfs.Open(project.directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Close() }()
	for name, expected := range map[string][]byte{"compose.yaml": project.compose, ".env": project.environment} {
		actual, info, err := transaction.ReadStable(name)
		if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("standalone Compose runtime custody is unavailable")
		}
		if !bytes.Equal(actual, expected) {
			return errors.New("standalone Compose runtime differs from the authorized workload")
		}
	}
	return nil
}

func standaloneComposeArgs(project standaloneComposeProject, operation string) []string {
	prefix := []string{
		"compose", "--project-name", project.name, "--env-file", filepath.Join(project.directory, ".env"),
		"-f", filepath.Join(project.directory, "compose.yaml"),
	}
	switch operation {
	case "up":
		return append(prefix, "up", "-d", "--wait", "--wait-timeout", "600")
	case "ps":
		return append(prefix, "ps", "--all", "--format", "json")
	case "port":
		return append(prefix, "port", project.bundle.EntryComponent, strconv.Itoa(project.entry.HealthPort))
	default:
		return nil
	}
}

type osStandaloneComposeProcessRunner struct{}

func (osStandaloneComposeProcessRunner) Run(
	ctx context.Context,
	args []string,
	directory string,
) ([]byte, error) {
	if len(args) < 9 || args[0] != "compose" || args[1] != "--project-name" ||
		args[3] != "--env-file" || filepath.Dir(args[4]) != directory ||
		filepath.Base(args[4]) != ".env" || args[5] != "-f" ||
		filepath.Dir(args[6]) != directory || filepath.Base(args[6]) != "compose.yaml" {
		return nil, &standaloneComposeProcessError{
			output: "closed-contract-rejected", cause: errors.New("invalid process contract"),
		}
	}
	executable, err := exec.LookPath("docker")
	if err != nil {
		return nil, &standaloneComposeProcessError{output: "docker-not-found", cause: err}
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output := &standaloneComposeBoundedBuffer{remaining: standaloneComposeOutputMax}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, &standaloneComposeProcessError{
			output: boundedBasementCoreProcessDiagnostic(output.Bytes()), cause: err,
		}
	}
	if output.exceeded {
		return nil, &standaloneComposeProcessError{
			output: "output-exceeded", cause: errors.New("bounded process output exceeded"),
		}
	}
	return append([]byte(nil), output.Bytes()...), nil
}

type standaloneComposeBoundedBuffer struct {
	bytes.Buffer
	remaining int
	exceeded  bool
}

func (b *standaloneComposeBoundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > b.remaining {
		value = value[:b.remaining]
		b.exceeded = true
	}
	b.remaining -= len(value)
	_, _ = b.Buffer.Write(value)
	return original, nil
}

type standaloneComposePS struct {
	Service  string `json:"Service"`
	Image    string `json:"Image"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	ExitCode int    `json:"ExitCode"`
	Labels   string `json:"Labels"`
	Networks string `json:"Networks"`
}

func parseStandaloneComposeStatuses(raw []byte) (map[string]standaloneComposePS, error) {
	raw = bytes.TrimSpace(raw)
	var values []standaloneComposePS
	if len(raw) == 0 {
		return nil, errors.New("standalone Compose returned no service status")
	}
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, errors.New("standalone Compose status is malformed")
		}
	} else {
		for _, line := range bytes.Split(raw, []byte{'\n'}) {
			var value standaloneComposePS
			if err := json.Unmarshal(bytes.TrimSpace(line), &value); err != nil {
				return nil, errors.New("standalone Compose status is malformed")
			}
			values = append(values, value)
		}
	}
	result := make(map[string]standaloneComposePS, len(values))
	for _, value := range values {
		if value.Service == "" {
			return nil, errors.New("standalone Compose status has no service identity")
		}
		if _, duplicate := result[value.Service]; duplicate {
			return nil, errors.New("standalone Compose status duplicates a service")
		}
		result[value.Service] = value
	}
	return result, nil
}

func validateStandaloneComposeRouteReadback(
	status standaloneComposePS,
	route architecturev2renderer.ApplicationDeliveryRouteDescriptor,
) error {
	labels := standaloneComposeCSVMap(status.Labels)
	networks := standaloneComposeCSVSet(status.Networks)
	if route.ID == "" {
		for key := range labels {
			if strings.HasPrefix(key, "traefik.http.") ||
				strings.HasPrefix(key, "io.stackkit.route.") ||
				key == "traefik.enable" || key == "traefik.docker.network" {
				return errors.New("unrouted standalone workload gained route labels")
			}
		}
		if _, exists := networks[standaloneComposeRoutingNetwork]; exists {
			return errors.New("unrouted standalone workload joined the routing network")
		}
		return nil
	}
	for key, expected := range standaloneComposeRouteLabels(route) {
		if labels[key] != expected {
			return fmt.Errorf("standalone Compose route readback differs at label %q", key)
		}
	}
	if _, exists := networks[standaloneComposeRoutingNetwork]; !exists {
		return errors.New("standalone Compose route readback lacks the routing network")
	}
	return nil
}

func standaloneComposeCSVMap(raw string) map[string]string {
	result := map[string]string{}
	for _, field := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(field), "=")
		if found && key != "" {
			result[key] = value
		}
	}
	return result
}

func standaloneComposeCSVSet(raw string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, field := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(field); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func observeStandaloneComposeComponents(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	statuses map[string]standaloneComposePS,
) ([]SelectedPaaSComponentObservation, error) {
	if len(statuses) != len(components) {
		return nil, errors.New("standalone Compose service set differs from the authorized component graph")
	}
	result := make([]SelectedPaaSComponentObservation, len(components))
	for index, component := range components {
		status, exists := statuses[component.ID]
		if !exists || status.Image != component.ImageRef+"@"+component.ImageDigest {
			return nil, fmt.Errorf("standalone Compose component %q differs from its pinned image", component.ID)
		}
		observation := SelectedPaaSComponentObservation{ID: component.ID, ImageDigest: component.ImageDigest}
		if component.Lifecycle == "one-shot" {
			if status.State != "exited" || status.ExitCode != 0 {
				return nil, fmt.Errorf("standalone Compose one-shot component %q did not complete", component.ID)
			}
			observation.Status, observation.Health = "completed", "completed"
		} else {
			if status.State != "running" || (status.Health != "" && status.Health != "healthy") {
				return nil, fmt.Errorf("standalone Compose daemon component %q is not healthy", component.ID)
			}
			observation.Status, observation.Health = "running", "healthy"
		}
		result[index] = observation
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func standaloneComposeLoopbackAddress(raw []byte) (string, error) {
	value := strings.TrimSpace(string(raw))
	host, port, err := net.SplitHostPort(value)
	if err != nil || (host != "127.0.0.1" && host != "::1") {
		return "", errors.New("standalone Compose route is not bound to loopback")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", errors.New("standalone Compose route has an invalid loopback port")
	}
	return net.JoinHostPort(host, port), nil
}

type osStandaloneComposeHTTPProber struct{}

func (osStandaloneComposeHTTPProber) Probe(ctx context.Context, target string) (int, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode, nil
}

func standaloneComposeComponent(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	id string,
) (architecturev2renderer.ApplicationDeliveryComponentDescriptor, bool) {
	for _, component := range components {
		if component.ID == id {
			return component, true
		}
	}
	return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, false
}

var _ SelectedPaaSWorkloadOperations = (*osStandaloneComposeWorkloadOperations)(nil)
