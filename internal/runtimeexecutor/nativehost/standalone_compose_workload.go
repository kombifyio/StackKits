package nativehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/originca"
	"gopkg.in/yaml.v3"
)

const (
	standaloneComposeAdapterRef = "standalone-compose"
	standaloneComposeModuleRef  = "stackkits-standalone-compose-runtime"
	// standaloneComposeHealthNetwork carries only the entry component of an
	// unrouted workload so its loopback health port can bind.
	standaloneComposeHealthNetwork = "stackkit-workload-health"
	standaloneComposeOutputMax     = 256 << 10
	// standaloneComposeEnvFile is the private interpolation file beside
	// compose.yaml; `docker compose --env-file` reads it.
	standaloneComposeEnvFile = ".env"
	// standaloneComposeApplyBudget bounds startup plus readiness.
	standaloneComposeApplyBudget = 600 * time.Second
	// Verification permits one ordinary image health interval after a service
	// Start or Restart. It never extends the caller's cancellation/deadline.
	standaloneComposeVerifyReadinessBudget = 90 * time.Second
)

// standaloneComposeStartingError is limited to an exact, running component
// whose Docker health check is still starting. Missing, stopped, unhealthy,
// or foreign-image components remain permanent observation failures.
type standaloneComposeStartingError struct{ component string }

func (err *standaloneComposeStartingError) Error() string {
	return fmt.Sprintf("standalone Compose daemon component %q is still starting", err.component)
}

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
	// ensureOIDCClient registers an application's Pocket ID client; nil uses
	// the local owner service.
	ensureOIDCClient func(context.Context, string, localowner.ApplicationOIDCClientRequest) error
	mediaRootProbe   func(string) error
}

// NewOSStandaloneComposeWorkloadOperations constructs the local no-PaaS
// workload adapter for an existing owner workspace.
func NewOSStandaloneComposeWorkloadOperations(workspaceRoot string) (NativeWorkloadComposeOperations, error) {
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
	prepared, err := o.PrepareWorkloadCompose(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	project := prepared.project
	// Container startup and application readiness share the existing Compose
	// wait budget. A running container without a healthcheck is not HTTP-ready.
	ctx, cancel := context.WithTimeout(ctx, standaloneComposeApplyBudget)
	defer cancel()
	if _, err := o.runner.Run(ctx, standaloneComposeArgs(project, "up"), project.directory); err != nil {
		return SelectedPaaSApplyReceipt{}, fmt.Errorf("standalone Docker Compose Apply did not complete: %w", err)
	}
	if err := o.completeWorkloadCompose(ctx, project); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	return SelectedPaaSApplyReceipt{
		InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied",
	}, nil
}

// PrepareWorkloadCompose is the native Apply up to, not including, `docker
// compose up`: it validates the deployment, renders the Compose project and
// its .env, and persists them owner-only under
// .stackkit/runtime/applications/<project>/, exactly as ApplyWorkload does.
func (o *osStandaloneComposeWorkloadOperations) PrepareWorkloadCompose(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (NativeWorkloadCompose, error) {
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return NativeWorkloadCompose{}, err
	}
	if err := o.persist(project); err != nil {
		return NativeWorkloadCompose{}, err
	}
	return preparedWorkloadCompose(project), nil
}

func preparedWorkloadCompose(project standaloneComposeProject) NativeWorkloadCompose {
	wait := true
	for _, component := range project.bundle.Components {
		if component.HealthFailure == "degraded" {
			wait = false
		}
	}
	return NativeWorkloadCompose{
		ProjectName: project.name, Directory: project.directory,
		Compose: append([]byte(nil), project.compose...), EnvFile: standaloneComposeEnvFile, Wait: wait,
		project: project,
	}
}

// CompleteWorkloadCompose is the native Apply after `docker compose up`: the
// game node's Wings recreation and the blocking-component and application
// HTTP readiness waits, within the native Apply budget.
func (o *osStandaloneComposeWorkloadOperations) CompleteWorkloadCompose(ctx context.Context, prepared NativeWorkloadCompose) error {
	if ctx == nil {
		return errors.New("standalone Compose operations require a context")
	}
	if prepared.project.name == "" || prepared.project.name != prepared.ProjectName {
		return errors.New("standalone Compose completion requires a prepared project")
	}
	ctx, cancel := context.WithTimeout(ctx, standaloneComposeApplyBudget)
	defer cancel()
	return o.completeWorkloadCompose(ctx, prepared.project)
}

func (o *osStandaloneComposeWorkloadOperations) completeWorkloadCompose(ctx context.Context, project standaloneComposeProject) error {
	if gameNode, ok := architecturev2renderer.GameNodeModuleFor(project.bundle.ModuleRef); ok {
		// Wings reads the configuration its bootstrap just converged only at
		// start. Recreating Wings leaves running game servers attached.
		if _, err := o.runner.Run(ctx, standaloneComposeRecreateArgs(project, gameNode.WingsComponent), project.directory); err != nil {
			return fmt.Errorf("restart the game node with its converged configuration: %w", err)
		}
	}
	if err := o.waitForBlockingComponents(ctx, project); err != nil {
		return err
	}
	return o.waitForApplicationHTTP(ctx, project)
}

func (o *osStandaloneComposeWorkloadOperations) waitForBlockingComponents(
	ctx context.Context,
	project standaloneComposeProject,
) error {
	return o.waitForComponentReadiness(ctx, project, func(statuses map[string]standaloneComposePS) (bool, error) {
		return blockingStandaloneComposeReadiness(project.bundle.Components, statuses)
	})
}

func (o *osStandaloneComposeWorkloadOperations) waitForComponentReadiness(
	ctx context.Context,
	project standaloneComposeProject,
	validate func(map[string]standaloneComposePS) (bool, error),
) error {
	for {
		raw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
		if err != nil {
			return fmt.Errorf("standalone Docker Compose readiness observation failed: %w", err)
		}
		statuses, err := parseStandaloneComposeStatuses(raw)
		if err != nil {
			return err
		}
		ready, err := validate(statuses)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("standalone Docker Compose blocking component readiness did not complete: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func blockingStandaloneComposeReadiness(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	statuses map[string]standaloneComposePS,
) (bool, error) {
	for _, component := range components {
		if component.HealthFailure == "degraded" {
			continue
		}
		status, exists := statuses[component.ID]
		if !exists {
			return false, nil
		}
		if status.Image != component.ImageRef+"@"+component.ImageDigest {
			return false, fmt.Errorf("standalone Compose blocking component %q differs from its pinned image", component.ID)
		}
		if component.Lifecycle == "one-shot" {
			if status.State == "exited" && status.ExitCode == 0 {
				continue
			}
			if status.State == "exited" {
				return false, fmt.Errorf("standalone Compose blocking one-shot component %q failed", component.ID)
			}
			return false, nil
		}
		if status.State == "exited" || status.State == "dead" {
			return false, fmt.Errorf("standalone Compose blocking daemon component %q stopped", component.ID)
		}
		if status.State != "running" {
			return false, nil
		}
		if len(component.HealthCommand) > 0 {
			if status.Health == "healthy" {
				continue
			}
			if status.Health == "unhealthy" {
				return false, fmt.Errorf("standalone Compose blocking daemon component %q is unhealthy", component.ID)
			}
			return false, nil
		}
		if status.Health != "" && status.Health != "healthy" {
			if status.Health == "unhealthy" {
				return false, fmt.Errorf("standalone Compose blocking daemon component %q is unhealthy", component.ID)
			}
			return false, nil
		}
	}
	return true, nil
}

func (o *osStandaloneComposeWorkloadOperations) waitForApplicationHTTP(ctx context.Context, project standaloneComposeProject) error {
	portRaw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "port"), project.directory)
	if err != nil {
		return fmt.Errorf("standalone Docker Compose readiness port observation failed: %w", err)
	}
	address, err := standaloneComposeLoopbackAddress(portRaw)
	if err != nil {
		return err
	}
	for {
		status, probeErr := o.prober.Probe(ctx, "http://"+address+project.entry.HealthPath)
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("standalone application readiness interrupted: %w", errors.Join(err, probeErr))
		}
		if probeErr == nil && (status == http.StatusOK || status == http.StatusFound) {
			return o.recordOriginBackend(ctx, project, address)
		}
		if probeErr == nil {
			probeErr = fmt.Errorf("application returned HTTP status %d", status)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("standalone application readiness did not complete: %w", errors.Join(ctx.Err(), probeErr))
		case <-timer.C:
		}
	}
}

// observeReadyWorkload is used only by Verify. Ordinary inventory and backup
// observations keep their immediate readback behavior. The existing readiness
// owner polls Compose status without invoking a runtime action or completion.
func (o *osStandaloneComposeWorkloadOperations) observeReadyWorkload(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (SelectedPaaSWorkloadObservation, error) {
	observation, err := o.ObserveWorkload(ctx, deployment)
	var starting *standaloneComposeStartingError
	if !errors.As(err, &starting) {
		return observation, err
	}
	project, err := o.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.verifyPersisted(project); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.waitForComponentReadiness(ctx, project, func(statuses map[string]standaloneComposePS) (bool, error) {
		if err := o.verifyPersisted(project); err != nil {
			return false, err
		}
		if err := validateStandaloneComposeRouteReadback(statuses[project.bundle.EntryComponent], project.bundle.Route, o.originServes(project.bundle.Route)); err != nil {
			return false, err
		}
		// The strict observer validates the entire authorized graph and its pins
		// before classifying starting. Apply keeps its broader startup semantics.
		_, err := observeStandaloneComposeComponents(project.bundle.Components, statuses)
		var starting *standaloneComposeStartingError
		if errors.As(err, &starting) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	return o.ObserveWorkload(ctx, deployment)
}

func (o *osStandaloneComposeWorkloadOperations) ObserveWorkload(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (SelectedPaaSWorkloadObservation, error) {
	// Inspection uses existing signed custody. In particular, backup repeats
	// this while identity services may be stopped; it must not register clients,
	// rotate secrets, or reconcile IdP policy as a side effect of readback.
	project, err := o.prepareWithIdentityMutation(ctx, deployment, false)
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
		o.originServes(project.bundle.Route),
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
	workloadStatus := "running"
	for _, component := range components {
		if component.Status == "degraded" {
			workloadStatus = "degraded"
			break
		}
	}
	return SelectedPaaSWorkloadObservation{
		WorkloadRef: deployment.WorkloadRef, Release: deployment.Release,
		InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest,
		Status: workloadStatus, Components: components,
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

func (o *osStandaloneComposeWorkloadOperations) ValidateWorkloadObservation(
	deployment SelectedPaaSWorkloadDeployment,
	observation SelectedPaaSWorkloadObservation,
) error {
	return ValidateSelectedPaaSWorkloadObservation(deployment, observation)
}

type standaloneComposeProject struct {
	unitRef     string
	name        string
	directory   string
	compose     []byte
	environment []byte
	configFiles map[string][]byte
	bundle      architecturev2renderer.ApplicationDeliveryBundleDescriptor
	entry       architecturev2renderer.ApplicationDeliveryComponentDescriptor
}

func (o *osStandaloneComposeWorkloadOperations) prepare(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (standaloneComposeProject, error) {
	return o.prepareWithIdentityMutation(ctx, deployment, true)
}

// prepareWithIdentityMutation renders from existing owner custody when
// ensureIdentity is false. Inspection and recovery must keep existing users,
// passkeys and issued client secrets; missing or invalid custody fails closed
// instead of contacting the IdP or rotating a credential.
func (o *osStandaloneComposeWorkloadOperations) prepareWithIdentityMutation(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
	ensureIdentity bool,
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
		normalizeApplicationDeliveryRoute(bundle.Route) != normalizeApplicationDeliveryRoute(deployment.Route) {
		return standaloneComposeProject{}, errors.New("standalone workload bundle differs from the authorized deployment")
	}
	entry, ok := standaloneComposeComponent(bundle.Components, bundle.EntryComponent)
	if !ok || entry.HealthKind != "http" || !strings.HasPrefix(entry.HealthPath, "/") ||
		(bundle.Route.ID != "" && entry.HealthPort != bundle.Route.TargetPort) {
		return standaloneComposeProject{}, errors.New("standalone workload entry component has no exact HTTP health contract")
	}
	if ensureIdentity {
		if err := o.requireMediaLibrarySource(ctx, bundle); err != nil {
			return standaloneComposeProject{}, err
		}
		// This guard precedes every mutating step. In particular, a refused
		// Jellyfin major transition must not register a new Pocket ID client,
		// replace runtime files, or reach Compose up.
		if err := o.requireJellyfinApplySafety(ctx, bundle); err != nil {
			return standaloneComposeProject{}, err
		}
	}
	dockerRoot := ""
	if standaloneComposeNeedsDockerRoot(bundle) {
		raw, err := o.runner.Run(ctx, standaloneComposeDockerRootArgs, o.workspaceRoot)
		if err != nil {
			return standaloneComposeProject{}, fmt.Errorf("observe Docker root for the game node data volume: %w", err)
		}
		dockerRoot = strings.TrimSpace(string(raw))
		if !filepath.IsAbs(dockerRoot) || filepath.Clean(dockerRoot) != dockerRoot {
			return standaloneComposeProject{}, errors.New("Docker root directory is not a clean absolute path")
		}
	}
	if ensureIdentity {
		if err := o.ensurePocketIDClients(ctx, bundle); err != nil {
			return standaloneComposeProject{}, err
		}
	}
	compose, environment, configFiles, err := o.renderWithDockerRoot(bundle, dockerRoot)
	if err != nil {
		return standaloneComposeProject{}, err
	}
	name := "stackkit-" + bundle.WorkloadRef + "-" + bundle.NodeRef
	directory := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "applications", name)
	return standaloneComposeProject{
		unitRef: deployment.UnitRef,
		name:    name, directory: directory, compose: compose, environment: environment,
		configFiles: configFiles, bundle: bundle, entry: entry,
	}, nil
}

// The workload Compose project is the shared typed model of the renderer
// package, so the Stage 2 native renderer translates exactly what this owner
// emits.
type (
	standaloneComposeDocument       = architecturev2renderer.WorkloadComposeDocument
	standaloneComposeSecret         = architecturev2renderer.WorkloadComposeSecret
	standaloneComposeServiceSecret  = architecturev2renderer.WorkloadComposeServiceSecret
	standaloneComposeService        = architecturev2renderer.WorkloadComposeService
	standaloneComposeDependency     = architecturev2renderer.WorkloadComposeDependency
	standaloneComposeDeploy         = architecturev2renderer.WorkloadComposeDeploy
	standaloneComposeResources      = architecturev2renderer.WorkloadComposeResources
	standaloneComposeResourceBounds = architecturev2renderer.WorkloadComposeResourceBounds
	standaloneComposeDeviceRequest  = architecturev2renderer.WorkloadComposeDeviceRequest
	standaloneComposeLogging        = architecturev2renderer.WorkloadComposeLogging
	standaloneComposeNetwork        = architecturev2renderer.WorkloadComposeNetwork
	standaloneComposeHealthcheck    = architecturev2renderer.WorkloadComposeHealthcheck
)

// NVIDIACDIAllGPUs is the CDI device the NVIDIA Container Toolkit spec
// (nvidia-ctk cdi generate) declares for every GPU of the node.
const NVIDIACDIAllGPUs = "nvidia.com/gpu=all"

// applyComponentAccelerator renders the GPU grant of a selected accelerator
// profile. NVIDIA: a CDI device reservation for nvidia.com/gpu=all, which
// needs Docker Engine 25+ with CDI enabled and the CDI spec on the host.
// AMD ROCm: the /dev/kfd compute node and the /dev/dri render nodes. Nothing
// else reaches a container as a GPU.
func applyComponentAccelerator(accelerator *architecturev2renderer.ApplicationDeliveryAccelerator, service *standaloneComposeService) error {
	if accelerator == nil {
		return nil
	}
	switch accelerator.Access {
	case "cdi":
		if service.Deploy == nil {
			service.Deploy = &standaloneComposeDeploy{}
		}
		if service.Deploy.Resources.Reservations == nil {
			service.Deploy.Resources.Reservations = &standaloneComposeResourceBounds{}
		}
		service.Deploy.Resources.Reservations.Devices = append(service.Deploy.Resources.Reservations.Devices, standaloneComposeDeviceRequest{
			Driver: "cdi", DeviceIDs: []string{NVIDIACDIAllGPUs}, Capabilities: []string{"gpu"},
		})
	case "rocm-device-nodes":
		service.Devices = append(service.Devices, "/dev/kfd:/dev/kfd", "/dev/dri:/dev/dri")
	default:
		return fmt.Errorf("accelerator access %q is not governed", accelerator.Access)
	}
	return nil
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

// standaloneComposeMailNodeModuleRef is the only workload admitted to the
// ADR-0046 mail-node rights: mail ports published on every host address, the
// route host as the mail host name and a TLS-ALPN-01 router passthrough.
const standaloneComposeMailNodeModuleRef = "stackkits-stalwart-runtime"

// standaloneComposeControlPlaneModuleRef is the only workload admitted to the
// route-host binding on a private route: Paperclip's hostname guard must know
// the route host, and Paperclip is never published.
const standaloneComposeControlPlaneModuleRef = "stackkits-paperclip-runtime"

// standaloneComposeControlPlaneRouteHost binds the private route host to the
// Paperclip entry component's declared variable. Without a route nothing is
// bound and Paperclip answers only loopback.
func standaloneComposeControlPlaneRouteHost(
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
	component architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	service *standaloneComposeService,
) error {
	if component.ID != bundle.EntryComponent || len(component.PublishedTCPPorts) != 0 || component.ACMETLSALPNPort != 0 {
		return errors.New("the control plane binds only its route host")
	}
	if bundle.Route.Exposure == "public" {
		return errors.New("the control plane is never published")
	}
	if bundle.Route.ID == "" || bundle.Route.Host == "" {
		return nil
	}
	for _, name := range component.RouteHostEnvironment {
		if _, conflict := service.Environment[name]; conflict {
			return errors.New("control plane route-host binding conflicts with a declared variable")
		}
		service.Environment[name] = strings.ReplaceAll(bundle.Route.Host, "$", "$$")
	}
	return nil
}

// standaloneComposeHostBindings applies the ADR-0046 mail-node rights and the
// Paperclip route-host binding to their entry components. It refuses them
// anywhere else, so every other workload keeps publishing nothing beyond its
// loopback health port.
func standaloneComposeHostBindings(
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
	component architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	service *standaloneComposeService,
) error {
	if len(component.PublishedTCPPorts) == 0 && len(component.RouteHostEnvironment) == 0 && component.ACMETLSALPNPort == 0 {
		return nil
	}
	if bundle.ModuleRef == standaloneComposeControlPlaneModuleRef {
		return standaloneComposeControlPlaneRouteHost(bundle, component, service)
	}
	if bundle.ModuleRef != standaloneComposeMailNodeModuleRef || component.ID != bundle.EntryComponent ||
		bundle.Route.ID == "" || bundle.Route.Host == "" || bundle.Route.Exposure != "public" {
		return errors.New("published mail ports are admitted only for the public mail node entry")
	}
	for _, port := range component.PublishedTCPPorts {
		service.Ports = append(service.Ports, fmt.Sprintf("%d:%d/tcp", port, port))
	}
	for _, name := range component.RouteHostEnvironment {
		if _, conflict := service.Environment[name]; conflict {
			return errors.New("mail node route-host binding conflicts with a declared variable")
		}
		service.Environment[name] = strings.ReplaceAll(bundle.Route.Host, "$", "$$")
	}
	if component.ACMETLSALPNPort != 0 {
		router := "stackkit-" + bundle.Route.ServiceRef + "-acme-tls-alpn"
		service.Labels["traefik.tcp.routers."+router+".entrypoints"] = "websecure"
		service.Labels["traefik.tcp.routers."+router+".rule"] = "HostSNI(`" + bundle.Route.Host + "`) && ALPN(`acme-tls/1`)"
		service.Labels["traefik.tcp.routers."+router+".tls.passthrough"] = "true"
		service.Labels["traefik.tcp.routers."+router+".service"] = router
		service.Labels["traefik.tcp.services."+router+".loadbalancer.server.port"] = strconv.Itoa(component.ACMETLSALPNPort)
	}
	return nil
}

func standaloneComposeNeedsDockerRoot(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) bool {
	for _, component := range bundle.Components {
		for _, volume := range component.Volumes {
			if volume.SelfPath {
				return true
			}
		}
		if component.DockerLifecycleOwner {
			return true
		}
	}
	return false
}

func (o *osStandaloneComposeWorkloadOperations) render(
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
) ([]byte, []byte, map[string][]byte, error) {
	return o.renderWithDockerRoot(bundle, "")
}

//nolint:gocyclo // One renderer keeps every admitted mount and exception visible together.
func (o *osStandaloneComposeWorkloadOperations) renderWithDockerRoot(
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
	dockerRoot string,
) ([]byte, []byte, map[string][]byte, error) {
	routingNetwork := ""
	if bundle.Route.ID != "" {
		var err error
		routingNetwork, err = standaloneComposeCoreNetwork(bundle.Route.CoreModuleRef)
		if err != nil {
			return nil, nil, nil, err
		}
	}
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
	// Only the admitted Game platform modules receive the ADR-0043 game-node
	// mounts: Wings' Docker socket, its self-path data volume, the shared
	// configuration volume and a Panel's loopback route host (ADR-0048).
	gameNodeShape, gameNode := architecturev2renderer.GameNodeModuleFor(bundle.ModuleRef)
	selfPaths := map[string]string{}
	for _, component := range bundle.Components {
		for _, volume := range component.Volumes {
			if !volume.SelfPath {
				continue
			}
			if !gameNode || component.ID != gameNodeShape.WingsComponent || volume.ID != "data" || dockerRoot == "" {
				return nil, nil, nil, errors.New("a self-path volume is admitted only for the game node's Wings data")
			}
			ref := component.ID + "-" + volume.ID
			selfPaths[ref] = filepath.Join(dockerRoot, "volumes", document.Name+"_"+ref, "_data")
		}
	}
	secretValues := map[string]string{}
	configFiles := map[string][]byte{}
	assignedConfig := map[string]struct{}{}
	for _, component := range bundle.Components {
		service := standaloneComposeService{
			Image:       component.ImageRef + "@" + component.ImageDigest,
			Command:     standaloneComposeLiteralArguments(component.Command),
			Entrypoint:  standaloneComposeLiteralArguments(component.Entrypoint),
			DependsOn:   map[string]standaloneComposeDependency{},
			Environment: map[string]string{}, Networks: append([]string(nil), component.NetworkRefs...),
			Labels: map[string]string{
				"io.stackkit.health-failure": component.HealthFailure,
				"io.stackkit.lifecycle":      component.Lifecycle,
			},
		}
		service.Deploy = componentDeploy(component.Resources)
		if component.Egress {
			// The component remains un-published. This bridge supplies outbound
			// access independently of the application's ingress route.
			document.Networks["stackkit-workload-egress"] = standaloneComposeNetwork{}
			service.Networks = append(service.Networks, "stackkit-workload-egress")
		}
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
			service.Environment[key] = strings.ReplaceAll(value, "$", "$$")
		}
		if len(component.OwnerEnvironment) > 0 {
			owner, err := localevidence.LoadOwnerCustody(o.workspaceRoot)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("resolve workload owner identity: %w", err)
			}
			for key, field := range component.OwnerEnvironment {
				_, hasPublic := component.Environment[key]
				_, hasSecret := component.SecretEnvironment[key]
				if key == "" || field != "email" || strings.TrimSpace(owner.PocketID.Email) == "" || hasPublic || hasSecret {
					return nil, nil, nil, errors.New("workload owner identity binding is invalid or ambiguous")
				}
				service.Environment[key] = strings.ReplaceAll(owner.PocketID.Email, "$", "$$")
			}
		}
		for environmentName, slot := range component.SecretEnvironment {
			secretRef, exists := bundle.SecretRefs[slot]
			if !exists {
				return nil, nil, nil, errors.New("standalone component references an absent secret slot")
			}
			material, err := localevidence.ResolveLocalSecretMaterial(o.workspaceRoot, secretRef)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("resolve owner-only material for secret slot %q: %w", slot, err)
			}
			if file, governed := architecturev2renderer.GovernedWorkloadSecretReferenceFile(bundle.ModuleRef, component.ID, environmentName); governed {
				// The variable carries only the reference; the value reaches
				// the container as a 0400 file outside every data volume.
				if standaloneComposeVolumeOwnsPath(component.Volumes, file.Target) {
					return nil, nil, nil, errors.New("a secret reference file must stay outside the workload volumes")
				}
				content, err := file.Content(string(material))
				if err != nil {
					return nil, nil, nil, fmt.Errorf("secret slot %q: %w", slot, err)
				}
				variable := standaloneComposeSecretVariable(component.ID, "FILE_"+slot)
				secretValues[variable] = content
				if document.Secrets == nil {
					document.Secrets = map[string]standaloneComposeSecret{}
				}
				name := component.ID + "-" + slot
				document.Secrets[name] = standaloneComposeSecret{Environment: variable}
				service.Secrets = append(service.Secrets, standaloneComposeServiceSecret{
					Source: name, Target: file.Target, UID: strconv.Itoa(file.UID), GID: strconv.Itoa(file.GID), Mode: 0o400,
				})
				service.Environment[environmentName] = strings.ReplaceAll(file.Reference, "$", "$$")
				continue
			}
			variable := standaloneComposeSecretVariable(component.ID, environmentName)
			secretValues[variable] = string(material)
			service.Environment[environmentName] = "${" + variable + ":?required}"
		}
		for _, file := range component.SecretFiles {
			secretRef, exists := bundle.SecretRefs[file.Slot]
			if !exists {
				return nil, nil, nil, errors.New("standalone component references an absent secret file slot")
			}
			material, err := localevidence.ResolveLocalSecretMaterial(o.workspaceRoot, secretRef)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("resolve owner-only material for secret file slot %q: %w", file.Slot, err)
			}
			// The value reaches the container only as a 0400 file; it never
			// enters the container environment.
			variable := standaloneComposeSecretVariable(component.ID, "FILE_"+file.Slot)
			secretValues[variable] = string(material)
			if document.Secrets == nil {
				document.Secrets = map[string]standaloneComposeSecret{}
			}
			name := component.ID + "-" + file.Slot
			document.Secrets[name] = standaloneComposeSecret{Environment: variable}
			service.Secrets = append(service.Secrets, standaloneComposeServiceSecret{
				Source: name, Target: file.Target, UID: strconv.Itoa(file.UID), GID: strconv.Itoa(file.GID), Mode: 0o400,
			})
			if _, conflict := service.Environment[file.PathEnvironment]; conflict || file.PathEnvironment == "" {
				return nil, nil, nil, errors.New("secret file path binding is missing or conflicts with a declared variable")
			}
			service.Environment[file.PathEnvironment] = file.Target
		}
		for _, name := range component.RestoreActivationEnvironment {
			if _, conflict := service.Environment[name]; conflict {
				return nil, nil, nil, errors.New("restore activation binding conflicts with a declared variable")
			}
			service.Environment[name] = "${" + architecturev2renderer.RestoreActivationComposeVariable + ":-false}"
		}
		for _, volume := range component.Volumes {
			if volume.HostPath != "" {
				if !architecturev2renderer.GovernedMediaLibraryMount(bundle.ModuleRef, component.ID, volume.ID, volume.Target) || !volume.ReadOnly || volume.Backup {
					return nil, nil, nil, errors.New("host source is only permitted for the read-only owner-custodied media library")
				}
				service.Volumes = append(service.Volumes, map[string]any{
					"type": "bind", "source": volume.HostPath, "target": volume.Target,
					"read_only": true, "bind": map[string]any{"create_host_path": false},
				})
				continue
			}
			ref := component.ID + "-" + volume.ID
			if volume.SharedFromComponent != "" {
				if !gameNode || component.ID != gameNodeShape.ConfigWriter || !slices.Contains(gameNodeShape.ConfigWriterShares, volume.SharedFromComponent+"/"+volume.SharedFromVolume) {
					return nil, nil, nil, errors.New("a shared volume is admitted only for the game node bootstrap")
				}
				ref = volume.SharedFromComponent + "-" + volume.SharedFromVolume
			}
			document.Volumes[ref] = map[string]any{}
			mount := ref + ":" + volume.Target
			if volume.ReadOnly {
				mount += ":ro"
			}
			service.Volumes = append(service.Volumes, mount)
			if hostPath, ok := selfPaths[ref]; ok {
				// Wings hands this path to the daemon for game containers, so
				// the same volume is also visible at its own host path.
				service.Volumes = append(service.Volumes, ref+":"+hostPath)
				service.Environment[architecturev2renderer.GameDataHostPathEnv] = hostPath
			}
		}
		configDigest, mountedConfig := sha256.New(), 0
		for _, file := range bundle.ConfigFiles {
			if !standaloneComposeVolumeOwnsPath(component.Volumes, file.Path) {
				continue
			}
			rel := architecturev2renderer.StandaloneComposeConfigRelPath(file.Path)
			service.Volumes = append(service.Volumes, "./"+rel+":"+file.Path+":ro")
			configFiles[rel] = []byte(file.Body)
			assignedConfig[file.Path] = struct{}{}
			fmt.Fprintf(configDigest, "%s\x00%d\x00%s", file.Path, len(file.Body), file.Body)
			mountedConfig++
		}
		if mountedConfig > 0 {
			// A changed config file keeps the Compose file identical and the
			// running container keeps the replaced file's old inode; the
			// digest label makes Compose recreate exactly the affected service.
			service.Labels["io.stackkit.config-digest"] = "sha256:" + hex.EncodeToString(configDigest.Sum(nil))
		}
		if component.DockerLifecycleOwner {
			if !gameNode || component.ID != gameNodeShape.WingsComponent || bundle.DaemonSocketPath == "" || dockerRoot == "" {
				return nil, nil, nil, errors.New("Docker lifecycle ownership is admitted only for the game node's Wings")
			}
			service.Volumes = append(service.Volumes,
				map[string]any{"type": "bind", "source": bundle.DaemonSocketPath, "target": "/var/run/docker.sock", "bind": map[string]any{"create_host_path": false}},
				map[string]any{"type": "bind", "source": filepath.Join(dockerRoot, "containers"), "target": filepath.Join(dockerRoot, "containers"), "read_only": true, "bind": map[string]any{"create_host_path": false}},
			)
		}
		if component.StopSignal != "" {
			// The bundle parser admits only the governed declaration; the
			// quiesce owner must be able to stop the container with it.
			if !backupexec.SupportedStopSignal(component.StopSignal) {
				return nil, nil, nil, fmt.Errorf("component %s declares stop signal %q, which the backup quiesce owner does not admit", component.ID, component.StopSignal)
			}
			service.StopSignal = component.StopSignal
		}
		if component.Init {
			enabled := true
			service.Init = &enabled
		}
		if bundle.ModuleRef == "stackkits-roundcube-runtime" && component.ID == "roundcube" {
			// The Apache image declares SIGWINCH (graceful drain), which the
			// backup quiesce owner does not admit; SIGTERM stops Apache at once
			// and Roundcube keeps no in-flight state worth draining.
			service.StopSignal = "SIGTERM"
		}
		if gameNode && slices.Contains(gameNodeShape.SIGTERMComponents, component.ID) {
			// The Pterodactyl and Pelican Panel images declare SIGQUIT;
			// supervisord stops cleanly on SIGTERM, which the backup quiesce
			// owner admits.
			service.StopSignal = "SIGTERM"
		}
		if gameNode && component.ID == gameNodeShape.RootComponent {
			// The Pelican bootstrap writes Wings' configuration into the
			// root-owned Wings data volume; its image declares www-data.
			service.User = "0:0"
		}
		if component.RouteHostLoopback {
			if !gameNode || component.ID != gameNodeShape.LoopbackComponent || bundle.Route.Host == "" {
				return nil, nil, nil, errors.New("a loopback route host is admitted only for the game node Panel")
			}
			service.ExtraHosts = []string{bundle.Route.Host + ":127.0.0.1"}
		}
		if len(component.HealthCommand) > 0 {
			service.Healthcheck = &standaloneComposeHealthcheck{
				Test:     append([]string{"CMD"}, standaloneComposeLiteralArguments(component.HealthCommand)...),
				Interval: "10s", Timeout: "5s", Retries: 12, StartPeriod: "10s",
				StartInterval: architecturev2renderer.WorkloadImageHealthcheckStartInterval(component.ImageDigest),
			}
		}
		if component.ID == bundle.EntryComponent {
			service.Ports = []string{fmt.Sprintf("127.0.0.1::%d", component.HealthPort)}
			if bundle.Route.ID != "" {
				document.Networks["stackkit-routing"] = standaloneComposeNetwork{Name: routingNetwork, External: true}
				service.Networks = append(service.Networks, "stackkit-routing")
				for key, value := range standaloneComposeRouteLabels(bundle.Route, o.originServes(bundle.Route)) {
					service.Labels[key] = value
				}
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
		if err := standaloneComposeHostBindings(bundle, component, &service); err != nil {
			return nil, nil, nil, err
		}
		// Owner-enabled LAN rights, already validated against the governed
		// component declaration by the bundle parser.
		for _, listener := range component.LANListeners {
			service.Ports = append(service.Ports, fmt.Sprintf("%d:%d/%s", listener.Port, listener.Port, listener.Protocol))
		}
		for _, device := range component.Devices {
			service.Devices = append(service.Devices, device.HostPath+":"+device.Target)
		}
		if err := applyComponentAccelerator(component.Accelerator, &service); err != nil {
			return nil, nil, nil, err
		}
		// The bundle parser admits the sandbox runtime only for its governed
		// component; Compose fails closed when the host has not registered it.
		service.Runtime = component.SandboxRuntime
		// A governed add-on joins its primary workload's internal network on
		// this node; Compose fails closed when that workload is not applied.
		if err := applyJellyfinSSOPlugin(component.JellyfinSSOPlugin, &service, configFiles); err != nil {
			return nil, nil, nil, err
		}
		if err := applyHomeAssistantOIDC(o.workspaceRoot, component.HomeAssistantOIDC, &service, configFiles); err != nil {
			return nil, nil, nil, err
		}
		if err := applyHomeIdentityAccess(o.workspaceRoot, bundle.Route.CoreModuleRef, document.Networks["stackkit-routing"], component.HomeIdentityAccess, &service, configFiles); err != nil {
			return nil, nil, nil, err
		}
		if err := applyPocketIDClientEnvironment(o.workspaceRoot, bundle, component, &service, secretValues, configFiles); err != nil {
			return nil, nil, nil, err
		}
		for _, peer := range component.PeerNetworks {
			key := "peer-" + peer.WorkloadRef + "-" + peer.NetworkRef
			document.Networks[key] = standaloneComposeNetwork{Name: "stackkit-" + peer.WorkloadRef + "-" + bundle.NodeRef + "_" + peer.NetworkRef, External: true}
			service.Networks = append(service.Networks, key)
		}
		sort.Strings(service.Networks)
		sort.SliceStable(service.Volumes, func(i, j int) bool {
			return fmt.Sprint(service.Volumes[i]) < fmt.Sprint(service.Volumes[j])
		})
		document.Services[component.ID] = service
	}
	if len(assignedConfig) != len(bundle.ConfigFiles) {
		return nil, nil, nil, errors.New("standalone config file is not bound to a workload volume")
	}
	compose, err := yaml.Marshal(document)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal standalone Compose definition: %w", err)
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
	return compose, []byte(environment.String()), configFiles, nil
}

// Values in the governed bundle belong to the container, not the Compose host.
// Only the generated secret environment references intentionally interpolate.
func standaloneComposeLiteralArguments(values []string) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = strings.ReplaceAll(value, "$", "$$")
	}
	return result
}

func standaloneComposeVolumeOwnsPath(volumes []architecturev2renderer.ApplicationDeliveryVolumeDescriptor, path string) bool {
	for _, volume := range volumes {
		prefix := strings.TrimSuffix(volume.Target, "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func standaloneComposeRouteLabels(route architecturev2renderer.ApplicationDeliveryRouteDescriptor, originServed bool) map[string]string {
	routingNetwork, _ := standaloneComposeCoreNetwork(route.CoreModuleRef) // validated before render or readback
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
		"traefik.enable":                                                  "true",
		"traefik.docker.network":                                          routingNetwork,
		"traefik.http.routers." + router + ".entrypoints":                 entrypoint,
		"traefik.http.routers." + router + ".rule":                        rule,
		"traefik.http.services." + router + ".loadbalancer.server.port":   strconv.Itoa(route.TargetPort),
		"traefik.http.services." + router + ".loadbalancer.server.scheme": route.UpstreamProtocol,
		"io.stackkit.route.id":                                            route.ID,
		"io.stackkit.route.core-module-ref":                               route.CoreModuleRef,
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
		// A managed kombify.me route served by the delivered Origin CA
		// certificate (ADR-0047) requests no ACME certificate.
		if route.TLSIssuerRef != "" && !originServed {
			// The plan carries provider-neutral issuer/profile identities. All
			// supported Compose core owners expose that authority to Traefik
			// through their installed resolver named "stackkits".
			labels["traefik.http.routers."+router+".tls.certresolver"] = "stackkits"
		}
	}
	if route.IngressAuth == "forward-auth" {
		labels["traefik.http.routers."+router+".middlewares"] = "stackkit-forward-auth@docker"
	}
	return labels
}

func normalizeApplicationDeliveryRoute(route architecturev2renderer.ApplicationDeliveryRouteDescriptor) architecturev2renderer.ApplicationDeliveryRouteDescriptor {
	if route.IngressAuth == "" {
		route.IngressAuth = "native"
	}
	return route
}

func standaloneComposeSecretVariable(componentID, environmentName string) string {
	value := strings.ToUpper(componentID + "_" + environmentName)
	replacer := strings.NewReplacer("-", "_", ".", "_")
	return "STACKKIT_SECRET_" + replacer.Replace(value)
}

func (o *osStandaloneComposeWorkloadOperations) persist(project standaloneComposeProject) error {
	return o.persistFiles(project, standaloneComposeProjectFiles(project))
}

// standaloneComposeProjectFiles are the governed Compose project files of one
// prepared project by project-relative name: compose.yaml, the private .env
// and every configuration file.
func standaloneComposeProjectFiles(project standaloneComposeProject) map[string][]byte {
	files := map[string][]byte{"compose.yaml": project.compose, standaloneComposeEnvFile: project.environment}
	for name, content := range project.configFiles {
		files[name] = content
	}
	return files
}

// persistFiles installs the given project files atomically and owner-only;
// only the public CA bundle is readable by a container user.
func (o *osStandaloneComposeWorkloadOperations) persistFiles(project standaloneComposeProject, files map[string][]byte) error {
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
	if len(project.configFiles) > 0 {
		if err := os.MkdirAll(filepath.Join(project.directory, "files"), 0o700); err != nil {
			return fmt.Errorf("create standalone Compose config directory: %w", err)
		}
	}
	readable := standaloneComposeReadableConfigFiles(project.bundle)
	for name, content := range files {
		write := view.WriteAtomic0600
		if readable[name] {
			write = view.WriteAtomic0644
		}
		result, err := write(name, content)
		if err != nil {
			return fmt.Errorf("persist private standalone Compose %s: %w", name, err)
		}
		if !result.Installed || !result.FileSynced {
			return errors.New("standalone Compose persistence did not prove an installed private artifact")
		}
	}
	return nil
}

// standaloneComposeReadableConfigFiles are the project files a container user
// other than root must read: the CA bundle holds public certificates only,
// and an image that drops to an unprivileged user (Paperless runs its web
// server as uid 1000) cannot open an owner-only bind mount. The 0700 project
// directory still keeps host users out.
func standaloneComposeReadableConfigFiles(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) map[string]bool {
	readable := map[string]bool{}
	if gameNode, ok := architecturev2renderer.GameNodeModuleFor(bundle.ModuleRef); ok && gameNode.ReadableConfigFiles {
		for _, file := range bundle.ConfigFiles {
			readable[architecturev2renderer.StandaloneComposeConfigRelPath(file.Path)] = true
		}
	}
	for _, component := range bundle.Components {
		if access := component.HomeIdentityAccess; access != nil {
			readable[architecturev2renderer.StandaloneComposeConfigRelPath(access.CABundleTarget)] = true
		}
	}
	return readable
}

// StandaloneComposeDriftError reports that a persisted project file differs
// from the authorized rendering. It is drift, not a failed observation: the
// caller reports it as a typed difference, and an Advanced reconcile restores
// the authorized files by re-applying the workload.
type StandaloneComposeDriftError struct{ projectRef string }

func (err *StandaloneComposeDriftError) Error() string {
	return "standalone Compose runtime differs from the authorized workload"
}
func (err *StandaloneComposeDriftError) DriftSubject() string { return "runtime-configuration" }
func (err *StandaloneComposeDriftError) DriftCode() string {
	return "standalone-compose-runtime-changed"
}
func (err *StandaloneComposeDriftError) DriftProjectRef() string { return err.projectRef }

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
	for name, expected := range standaloneComposeProjectFiles(project) {
		actual, info, err := transaction.ReadStable(name)
		if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("standalone Compose runtime custody is unavailable")
		}
		if !bytes.Equal(actual, expected) {
			return &StandaloneComposeDriftError{projectRef: project.name}
		}
	}
	return nil
}

// standaloneComposeRecreateArgs recreates one service without its
// dependencies, so a game node's Wings reads its converged configuration.
func standaloneComposeRecreateArgs(project standaloneComposeProject, service string) []string {
	return []string{
		"compose", "--project-name", project.name, "--env-file", filepath.Join(project.directory, ".env"),
		"-f", filepath.Join(project.directory, "compose.yaml"), "up", "-d", "--no-deps", "--force-recreate", service,
	}
}

func standaloneComposeArgs(project standaloneComposeProject, operation string) []string {
	prefix := []string{
		"compose", "--project-name", project.name, "--env-file", filepath.Join(project.directory, ".env"),
		"-f", filepath.Join(project.directory, "compose.yaml"),
	}
	switch operation {
	case "up":
		// The adapter owns the project: a component the bundle no longer
		// declares (an optional component the owner turned off, such as the
		// kombify AI connector) is removed, never left running.
		args := append(prefix, "up", "-d", "--remove-orphans")
		for _, component := range project.bundle.Components {
			if component.HealthFailure == "degraded" {
				return args
			}
		}
		return append(args, "--wait", "--wait-timeout", "600")
	case "ps":
		return append(prefix, "ps", "--all", "--no-trunc", "--format", "json")
	case "port":
		return append(prefix, "port", project.bundle.EntryComponent, strconv.Itoa(project.entry.HealthPort))
	default:
		return nil
	}
}

// standaloneComposeDockerRootArgs is the one read-only non-Compose query the
// runner admits: the daemon's data root for the game node's self-path volume.
var standaloneComposeDockerRootArgs = []string{"info", "--format", "{{.DockerRootDir}}"}

type osStandaloneComposeProcessRunner struct{}

func (osStandaloneComposeProcessRunner) Run(
	ctx context.Context,
	args []string,
	directory string,
) ([]byte, error) {
	dockerRootQuery := slices.Equal(args, standaloneComposeDockerRootArgs)
	jellyfinDiscoveryQuery := validJellyfinDiscoveryQuery(args)
	if !dockerRootQuery && !jellyfinDiscoveryQuery && (len(args) < 9 || args[0] != "compose" || args[1] != "--project-name" ||
		args[3] != "--env-file" || filepath.Dir(args[4]) != directory ||
		filepath.Base(args[4]) != ".env" || args[5] != "-f" ||
		filepath.Dir(args[6]) != directory || filepath.Base(args[6]) != "compose.yaml") {
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
	// Docker discovers system-wide Windows plugins below ProgramFiles. Keep
	// user profiles/config and transport overrides out of this local runner.
	if runtime.GOOS == "windows" {
		if directory := os.Getenv("ProgramFiles"); directory != "" {
			command.Env = append(command.Env, "ProgramFiles="+directory)
		}
	}
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
	ID       string `json:"ID"`
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
	originServed bool,
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
		for _, network := range []string{"stackkit-basement-core", "stackkit-cloud-core", "stackkit-cloud-core-standalone"} {
			if _, exists := networks[network]; exists {
				return errors.New("unrouted standalone workload joined a routing network")
			}
		}
		return nil
	}
	routingNetwork, err := standaloneComposeCoreNetwork(route.CoreModuleRef)
	if err != nil {
		return err
	}
	for key, expected := range standaloneComposeRouteLabels(route, originServed) {
		if labels[key] != expected {
			return fmt.Errorf("standalone Compose route readback differs at label %q", key)
		}
	}
	if _, exists := networks[routingNetwork]; !exists {
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

func standaloneComposeContainerIdentities(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	statuses map[string]standaloneComposePS,
	requireIDs bool,
) (map[string]string, error) {
	if missing := standaloneComposeMissingComponents(components, statuses); missing != "" {
		return nil, fmt.Errorf("standalone Compose components %s have no container in the project", missing)
	}
	if len(statuses) != len(components) {
		return nil, errors.New("standalone Compose service set differs from the authorized component graph")
	}
	identities := make(map[string]string, len(components))
	seenComponents := make(map[string]struct{}, len(components))
	seenIDs := make(map[string]string, len(components))
	for _, component := range components {
		if _, duplicate := seenComponents[component.ID]; duplicate {
			return nil, fmt.Errorf("standalone Compose component %q occurs more than once in the authorized graph", component.ID)
		}
		seenComponents[component.ID] = struct{}{}
		status, exists := statuses[component.ID]
		if !exists || status.Image != component.ImageRef+"@"+component.ImageDigest {
			return nil, fmt.Errorf("standalone Compose component %q differs from its pinned image", component.ID)
		}
		if requireIDs && strings.TrimSpace(status.ID) == "" {
			return nil, fmt.Errorf("standalone Compose component %q has no exact container identity", component.ID)
		}
		if status.ID != "" {
			if previous, duplicate := seenIDs[status.ID]; duplicate {
				return nil, fmt.Errorf("standalone Compose components %q and %q share a container identity", previous, component.ID)
			}
			seenIDs[status.ID] = component.ID
		}
		identities[component.ID] = status.ID
	}
	return identities, nil
}

func observeStandaloneComposeComponents(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	statuses map[string]standaloneComposePS,
) ([]SelectedPaaSComponentObservation, error) {
	return observeStandaloneComposeComponentsWithIdentity(components, statuses, false)
}

func observeStandaloneComposeComponentsWithIdentity(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	statuses map[string]standaloneComposePS,
	requireIDs bool,
) ([]SelectedPaaSComponentObservation, error) {
	if _, err := standaloneComposeContainerIdentities(components, statuses, requireIDs); err != nil {
		return nil, err
	}
	result := make([]SelectedPaaSComponentObservation, len(components))
	var starting *standaloneComposeStartingError
	for index, component := range components {
		status, exists := statuses[component.ID]
		if !exists {
			return nil, fmt.Errorf("standalone Compose component %q is absent from status readback", component.ID)
		}
		observation := SelectedPaaSComponentObservation{ID: component.ID, ImageDigest: component.ImageDigest}
		if component.Lifecycle == "one-shot" {
			if status.State != "exited" || status.ExitCode != 0 {
				if component.HealthFailure != "degraded" {
					return nil, fmt.Errorf("standalone Compose one-shot component %q did not complete", component.ID)
				}
				observation.Status, observation.Health = "degraded", standaloneComposeObservedHealth(status)
				observation.Reason = standaloneComposeDegradedReason(status)
				result[index] = observation
				continue
			}
			observation.Status, observation.Health = "completed", "completed"
		} else {
			if status.State != "running" {
				if component.HealthFailure != "degraded" {
					return nil, fmt.Errorf("standalone Compose daemon component %q is not healthy", component.ID)
				}
				observation.Status, observation.Health = "degraded", standaloneComposeObservedHealth(status)
				observation.Reason = standaloneComposeDegradedReason(status)
				result[index] = observation
				continue
			}
			if len(component.HealthCommand) > 0 {
				if status.Health != "healthy" {
					if component.HealthFailure != "degraded" {
						if status.Health == "starting" {
							starting = &standaloneComposeStartingError{component: component.ID}
							continue
						}
						return nil, fmt.Errorf("standalone Compose daemon component %q is not healthy", component.ID)
					}
					observation.Status, observation.Health = "degraded", standaloneComposeObservedHealth(status)
					observation.Reason = standaloneComposeDegradedReason(status)
					result[index] = observation
					continue
				}
			} else if status.Health != "" && status.Health != "healthy" {
				if component.HealthFailure != "degraded" {
					if status.Health == "starting" {
						starting = &standaloneComposeStartingError{component: component.ID}
						continue
					}
					return nil, fmt.Errorf("standalone Compose daemon component %q is not healthy", component.ID)
				}
				observation.Status, observation.Health = "degraded", standaloneComposeObservedHealth(status)
				observation.Reason = standaloneComposeDegradedReason(status)
				result[index] = observation
				continue
			}
			observation.Status, observation.Health = "running", "healthy"
		}
		result[index] = observation
	}
	if starting != nil {
		return nil, starting
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func standaloneComposeObservedHealth(status standaloneComposePS) string {
	if status.Health != "" {
		return status.Health
	}
	return "unavailable"
}

func standaloneComposeDegradedReason(status standaloneComposePS) string {
	if status.State != "running" {
		return "container-state-" + normalizedComposeDiagnostic(status.State)
	}
	return "container-health-" + normalizedComposeDiagnostic(standaloneComposeObservedHealth(status))
}

func normalizedComposeDiagnostic(value string) string {
	switch value {
	case "created", "exited", "paused", "restarting", "running", "starting", "unhealthy", "unavailable":
		return value
	default:
		return "unknown"
	}
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
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
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

// originServes reports whether the node holds an installed origin certificate
// for a TLS route's host.
func (o *osStandaloneComposeWorkloadOperations) originServes(route architecturev2renderer.ApplicationDeliveryRouteDescriptor) bool {
	return route.TLSRequired && route.Host != "" && originca.Covers(o.workspaceRoot, route.Host)
}
