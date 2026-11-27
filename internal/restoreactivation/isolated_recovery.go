package restoreactivation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
)

const (
	isolatedCloneStartTimeout   = 300 * time.Second
	isolatedCloneCleanupTimeout = 2 * time.Minute
	isolatedCloneHTTPTimeout    = 30 * time.Second
)

// IsolatedRecoveryError categorizes a fail-closed failure during isolated application recovery execution.
type IsolatedRecoveryError struct {
	Code  string
	Cause error
}

func (e *IsolatedRecoveryError) Error() string {
	if e == nil || e.Cause == nil {
		return "isolated application recovery failed"
	}
	return e.Cause.Error()
}

func (e *IsolatedRecoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsolatedVolumeInput identifies one application volume to create and, if Backup is true, populate from staging.
type IsolatedVolumeInput struct {
	ComponentRef string
	LogicalName  string
	LiveName     string
	CloneName    string
	Backup       bool
}

// IsolatedComponentInput identifies one component in the isolated clone project.
type IsolatedComponentInput struct {
	ID            string
	Role          string
	Lifecycle     string
	HealthFailure string
	ImageRef      string
	ImageDigest   string
}

// IsolatedApplicationRecoveryRequest holds all verified inputs needed to recover one application in an isolated clone.
type IsolatedApplicationRecoveryRequest struct {
	DrillID                 string
	WorkloadRef             string
	LiveProject             string
	CloneProject            string
	ComposePath             string
	EnvironmentPath         string
	StagingVolume           string
	StagingPath             string
	KopiaHelperImage        string
	EntryComponent          string
	EntryHealthPath         string
	EntryHealthPort         int
	DatabaseComponent       string
	DatabaseName            string
	DatabaseUser            string
	HasRestoreActivationEnv bool
	InternalNetworks        []string
	HealthNetwork           string
	Components              []IsolatedComponentInput
	Volumes                 []IsolatedVolumeInput
}

// IsolatedDependencyCheck records observed runtime readiness for one clone component.
type IsolatedDependencyCheck struct {
	ComponentRef string `json:"componentRef"`
	Role         string `json:"role"`
	Lifecycle    string `json:"lifecycle"`
	State        string `json:"state"`
	Health       string `json:"health,omitempty"`
	ExitCode     int    `json:"exitCode"`
	Ready        bool   `json:"ready"`
}

// IsolatedVolumeVerification records staged-to-clone byte copy and tree digest verification for one volume.
type IsolatedVolumeVerification struct {
	LiveName      string `json:"liveName"`
	CloneName     string `json:"cloneName"`
	Files         int64  `json:"files"`
	Bytes         int64  `json:"bytes"`
	Digest        string `json:"digest"`
	MatchesStaged bool   `json:"matchesStaged"`
}

// IsolatedRuntimeIsolationEvidence records live Docker inspection checks proving the clone could not mutate production.
type IsolatedRuntimeIsolationEvidence struct {
	LiveVolumesUnmounted           bool `json:"liveVolumesUnmounted"`
	InternalNetworksOnly           bool `json:"internalNetworksOnly"`
	MasqueradeDisabled             bool `json:"masqueradeDisabled"`
	RoutingLabelsStripped          bool `json:"routingLabelsStripped"`
	PublishedHostPortsOnlyLoopback bool `json:"publishedHostPortsOnlyLoopback"`
	CloneResourcesCleanedUp        bool `json:"cloneResourcesCleanedUp"`
}

// IsolatedApplicationRecoveryResult reports verified readiness, database readback, data readback, and cleanup.
type IsolatedApplicationRecoveryResult struct {
	DatabaseReady      bool                             `json:"databaseReady"`
	DatabaseName       string                           `json:"databaseName"`
	PublicTableCount   int64                            `json:"publicTableCount"`
	EntryHTTPStatus    int                              `json:"entryHttpStatus"`
	EntryHTTPReady     bool                             `json:"entryHttpReady"`
	DependencyChecks   []IsolatedDependencyCheck        `json:"dependencyChecks"`
	VolumeVerification []IsolatedVolumeVerification     `json:"volumeVerification"`
	Isolation          IsolatedRuntimeIsolationEvidence `json:"isolation"`
}

// ExecuteIsolatedApplicationRecovery copies staged application data into fresh labeled clone volumes, boots the
// isolated Compose stack offline, verifies isolation, dependencies, PostgreSQL readback, and entry HTTP readiness,
// and always cleans up all temporary clone resources.
func ExecuteIsolatedApplicationRecovery(
	ctx context.Context,
	workspace string,
	req IsolatedApplicationRecoveryRequest,
) (IsolatedApplicationRecoveryResult, error) {
	if err := validateIsolatedRecoveryRequest(req); err != nil {
		return IsolatedApplicationRecoveryResult{}, err
	}
	rt := &dockerRuntime{workspace: workspace}
	var result IsolatedApplicationRecoveryResult

	if err := verifyLocalCloneImages(ctx, rt, req.Components); err != nil {
		return result, err
	}
	if err := requireNoExistingCloneResources(ctx, rt, req); err != nil {
		return result, err
	}

	var cleanupErr error
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), isolatedCloneCleanupTimeout)
		defer cancel()
		err := cleanupIsolatedCloneResources(cleanupCtx, rt, req)
		if err != nil {
			cleanupErr = err
		} else {
			result.Isolation.CloneResourcesCleanedUp = true
		}
	}()

	volChecks, err := stageIsolatedCloneVolumes(ctx, rt, req)
	if err != nil {
		return result, err
	}
	result.VolumeVerification = volChecks

	if err := startIsolatedCloneCompose(ctx, rt, req); err != nil {
		return result, err
	}
	isoEvidence, err := inspectRunningCloneIsolation(ctx, rt, req)
	if err != nil {
		return result, err
	}
	result.Isolation = isoEvidence

	depChecks, err := verifyIsolatedDependencies(ctx, rt, req)
	if err != nil {
		return result, err
	}
	result.DependencyChecks = depChecks

	tableCount, err := verifyIsolatedPostgreSQL(ctx, rt, req)
	if err != nil {
		return result, err
	}
	result.DatabaseReady = true
	result.DatabaseName = req.DatabaseName
	result.PublicTableCount = tableCount

	status, err := probeIsolatedEntryHTTP(ctx, rt, req)
	if err != nil {
		return result, err
	}
	result.EntryHTTPStatus = status
	result.EntryHTTPReady = true

	// Trigger deferred cleanup before checking cleanupErr.
	if err := cleanupIsolatedCloneNow(ctx, rt, req, &result, &cleanupErr); err != nil {
		return result, err
	}
	return result, nil
}

func cleanupIsolatedCloneNow(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
	result *IsolatedApplicationRecoveryResult,
	cleanupErr *error,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), isolatedCloneCleanupTimeout)
	defer cancel()
	if err := cleanupIsolatedCloneResources(cleanupCtx, rt, req); err != nil {
		*cleanupErr = err
		return &IsolatedRecoveryError{Code: "cleanup_failed", Cause: err}
	}
	result.Isolation.CloneResourcesCleanedUp = true
	return nil
}

func validateIsolatedRecoveryRequest(req IsolatedApplicationRecoveryRequest) error {
	if runtime.GOOS != "linux" {
		return &IsolatedRecoveryError{
			Code:  "unsupported_workload",
			Cause: fmt.Errorf("isolated application recovery requires Linux (current OS: %s)", runtime.GOOS),
		}
	}
	if strings.TrimSpace(req.DrillID) == "" || strings.TrimSpace(req.CloneProject) == "" ||
		strings.TrimSpace(req.ComposePath) == "" || strings.TrimSpace(req.EnvironmentPath) == "" ||
		strings.TrimSpace(req.StagingVolume) == "" || strings.TrimSpace(req.StagingPath) == "" ||
		strings.TrimSpace(req.KopiaHelperImage) == "" || strings.TrimSpace(req.DatabaseComponent) == "" ||
		strings.TrimSpace(req.EntryComponent) == "" || req.EntryHealthPort <= 0 {
		return &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: errors.New("isolated application recovery request is incomplete"),
		}
	}
	if req.CloneProject == req.LiveProject {
		return &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("clone project %q collides with live project %q", req.CloneProject, req.LiveProject),
		}
	}
	return nil
}

func verifyLocalCloneImages(ctx context.Context, rt *dockerRuntime, components []IsolatedComponentInput) error {
	seen := map[string]bool{}
	for _, comp := range components {
		ref := comp.ImageRef
		if comp.ImageDigest != "" {
			ref += "@" + comp.ImageDigest
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if _, err := rt.docker(ctx, "image", "inspect", "--format", "{{.Id}}", ref); err != nil {
			return &IsolatedRecoveryError{
				Code:  "missing_dependency",
				Cause: fmt.Errorf("required local container image %q for component %q is unavailable: %w", ref, comp.ID, err),
			}
		}
	}
	return nil
}

func requireNoExistingCloneResources(ctx context.Context, rt *dockerRuntime, req IsolatedApplicationRecoveryRequest) error {
	psOut, err := rt.docker(ctx, "ps", "-a", "--filter", "label=com.docker.compose.project="+req.CloneProject, "--format", "{{.ID}}")
	if err == nil && strings.TrimSpace(string(psOut)) != "" {
		return &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("unexpected existing containers found for clone project %q", req.CloneProject),
		}
	}
	for _, vol := range req.Volumes {
		if _, err := rt.docker(ctx, "volume", "inspect", vol.CloneName); err == nil {
			return &IsolatedRecoveryError{
				Code:  "isolation_violation",
				Cause: fmt.Errorf("unexpected existing clone volume %q", vol.CloneName),
			}
		}
	}
	return nil
}

func stageIsolatedCloneVolumes(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
) ([]IsolatedVolumeVerification, error) {
	stagingRoot := "/" + strings.TrimPrefix(path.Clean(req.StagingPath), "/")
	if stagingRoot == "/" {
		return nil, &IsolatedRecoveryError{
			Code:  "missing_staged_data",
			Cause: errors.New("staged restore path must stay isolated inside staging volume"),
		}
	}
	var verified []IsolatedVolumeVerification
	for _, vol := range req.Volumes {
		createArgs := []string{
			"volume", "create",
			"--label", "com.docker.compose.project=" + req.CloneProject,
			"--label", "com.docker.compose.volume=" + vol.LogicalName,
			"--label", "io.stackkit.restore-drill.operation=" + req.DrillID,
			"--label", "io.stackkit.restore-drill.role=isolated-clone",
			"--label", "io.stackkit.restore-drill.workload=" + req.WorkloadRef,
			"--label", "io.stackkit.restore-drill.live-volume=" + vol.LiveName,
			vol.CloneName,
		}
		if _, err := rt.docker(ctx, createArgs...); err != nil {
			return nil, &IsolatedRecoveryError{
				Code:  "missing_staged_data",
				Cause: fmt.Errorf("create clone volume %q: %w", vol.CloneName, err),
			}
		}
		if !vol.Backup {
			continue
		}

		stagedLeaf := path.Join(stagingRoot, vol.LiveName, "_data")
		stagedReadback, err := digestStagingPath(ctx, rt, req, stagedLeaf)
		if err != nil {
			return nil, &IsolatedRecoveryError{
				Code:  "missing_staged_data",
				Cause: fmt.Errorf("staged backup data for volume %q is missing or unreadable: %w", vol.LiveName, err),
			}
		}
		if err := copyStagedToCloneVolume(ctx, rt, req, stagedLeaf, vol); err != nil {
			return nil, &IsolatedRecoveryError{
				Code:  "missing_staged_data",
				Cause: fmt.Errorf("copy staged data into clone volume %q: %w", vol.CloneName, err),
			}
		}
		cloneReadback, err := digestCloneVolume(ctx, rt, req, vol)
		if err != nil {
			return nil, &IsolatedRecoveryError{
				Code:  "missing_staged_data",
				Cause: fmt.Errorf("read back clone volume %q: %w", vol.CloneName, err),
			}
		}
		if stagedReadback.Files != cloneReadback.Files || stagedReadback.Bytes != cloneReadback.Bytes || stagedReadback.Digest != cloneReadback.Digest {
			return nil, &IsolatedRecoveryError{
				Code:  "missing_staged_data",
				Cause: fmt.Errorf("clone volume %q readback mismatch: staged (%d files, %s) != clone (%d files, %s)", vol.CloneName, stagedReadback.Files, stagedReadback.Digest, cloneReadback.Files, cloneReadback.Digest),
			}
		}
		verified = append(verified, IsolatedVolumeVerification{
			LiveName:      vol.LiveName,
			CloneName:     vol.CloneName,
			Files:         cloneReadback.Files,
			Bytes:         cloneReadback.Bytes,
			Digest:        cloneReadback.Digest,
			MatchesStaged: true,
		})
	}
	return verified, nil
}

func digestStagingPath(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
	stagedLeaf string,
) (backuplifecycle.VolumeReadback, error) {
	args := []string{
		"run", "--rm", "--network", "none", "--read-only",
		"--label", "io.stackkit.restore-drill.operation=" + req.DrillID,
		"--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE",
		"--security-opt", "no-new-privileges",
		"--mount", "type=volume,src=" + req.StagingVolume + ",dst=/source,readonly",
		"--entrypoint", "/bin/sh",
		req.KopiaHelperImage,
		"-ceu", digestScript, "--", "/source" + stagedLeaf,
	}
	raw, err := rt.docker(ctx, args...)
	if err != nil {
		return backuplifecycle.VolumeReadback{}, err
	}
	return parseVolumeDigest(raw)
}

func copyStagedToCloneVolume(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
	stagedLeaf string,
	vol IsolatedVolumeInput,
) error {
	args := []string{
		"run", "--rm", "--network", "none", "--read-only",
		"--label", "io.stackkit.restore-drill.operation=" + req.DrillID,
		"--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE",
		"--security-opt", "no-new-privileges",
		"--mount", "type=volume,src=" + req.StagingVolume + ",dst=/source,readonly",
		"--mount", "type=volume,src=" + vol.CloneName + ",dst=/target",
		"--entrypoint", "/bin/sh",
		req.KopiaHelperImage,
		"-ceu", copyScript, "--", "/source" + stagedLeaf, "/target",
	}
	_, err := rt.docker(ctx, args...)
	return err
}

func digestCloneVolume(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
	vol IsolatedVolumeInput,
) (backuplifecycle.VolumeReadback, error) {
	args := []string{
		"run", "--rm", "--network", "none", "--read-only",
		"--label", "io.stackkit.restore-drill.operation=" + req.DrillID,
		"--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE",
		"--security-opt", "no-new-privileges",
		"--mount", "type=volume,src=" + vol.CloneName + ",dst=/source,readonly",
		"--entrypoint", "/bin/sh",
		req.KopiaHelperImage,
		"-ceu", digestScript, "--", "/source",
	}
	raw, err := rt.docker(ctx, args...)
	if err != nil {
		return backuplifecycle.VolumeReadback{}, err
	}
	return parseVolumeDigest(raw)
}

func cloneComposeBaseArgs(req IsolatedApplicationRecoveryRequest) []string {
	return []string{
		"compose",
		"--project-name", req.CloneProject,
		"--env-file", req.EnvironmentPath,
		"-f", req.ComposePath,
	}
}

func startIsolatedCloneCompose(ctx context.Context, rt *dockerRuntime, req IsolatedApplicationRecoveryRequest) error {
	args := append(cloneComposeBaseArgs(req), "up", "-d", "--no-build", "--pull", "never", "--wait", "--wait-timeout", strconv.Itoa(int(isolatedCloneStartTimeout.Seconds())))
	var extra []string
	if req.HasRestoreActivationEnv {
		extra = []string{architecturev2renderer.RestoreActivationComposeVariable + "=true"}
	}
	if _, err := rt.dockerWithEnvironment(ctx, extra, args...); err != nil {
		return &IsolatedRecoveryError{
			Code:  "app_not_ready",
			Cause: fmt.Errorf("start isolated clone Compose project %q: %w", req.CloneProject, err),
		}
	}
	return nil
}

type inspectContainerRecord struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode  string `json:"NetworkMode"`
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type   string `json:"Type"`
		Name   string `json:"Name"`
		Source string `json:"Source"`
		RW     bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]any `json:"Networks"`
	} `json:"NetworkSettings"`
}

type inspectNetworkRecord struct {
	Name     string            `json:"Name"`
	Internal bool              `json:"Internal"`
	Options  map[string]string `json:"Options"`
	Labels   map[string]string `json:"Labels"`
}

func inspectRunningCloneIsolation(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
) (IsolatedRuntimeIsolationEvidence, error) {
	psRaw, err := rt.docker(ctx, append(cloneComposeBaseArgs(req), "ps", "-a", "-q")...)
	if err != nil {
		return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("list clone containers: %w", err),
		}
	}
	ids := strings.Fields(string(psRaw))
	if len(ids) == 0 {
		return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: errors.New("no clone containers were created"),
		}
	}

	inspectRaw, err := rt.docker(ctx, append([]string{"inspect"}, ids...)...)
	if err != nil {
		return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("inspect clone containers: %w", err),
		}
	}
	var containers []inspectContainerRecord
	if err := json.Unmarshal(inspectRaw, &containers); err != nil {
		return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("decode clone container inspection: %w", err),
		}
	}

	liveSet := map[string]bool{req.StagingVolume: true}
	cloneVolSet := map[string]bool{}
	for _, vol := range req.Volumes {
		liveSet[vol.LiveName] = true
		cloneVolSet[vol.CloneName] = true
	}

	netNames := map[string]bool{}
	for _, c := range containers {
		for k := range c.Config.Labels {
			if strings.HasPrefix(k, "traefik.") || strings.HasPrefix(k, "io.stackkit.route.") {
				return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
					Code:  "isolation_violation",
					Cause: fmt.Errorf("clone container retained forbidden routing label %q", k),
				}
			}
		}
		for _, m := range c.Mounts {
			if m.Source == "/var/run/docker.sock" || strings.HasSuffix(m.Source, "/docker.sock") {
				return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
					Code:  "isolation_violation",
					Cause: errors.New("clone container mounts Docker socket"),
				}
			}
			if m.Type == "volume" {
				if liveSet[m.Name] || !cloneVolSet[m.Name] {
					return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
						Code:  "isolation_violation",
						Cause: fmt.Errorf("clone container mounted unauthorized or live volume %q", m.Name),
					}
				}
			}
			if m.Type == "bind" && m.RW {
				return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
					Code:  "isolation_violation",
					Cause: fmt.Errorf("clone container holds writable host bind mount %q", m.Source),
				}
			}
		}
		for _, bindings := range c.HostConfig.PortBindings {
			for _, b := range bindings {
				if b.HostIP != "127.0.0.1" {
					return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
						Code:  "isolation_violation",
						Cause: fmt.Errorf("clone container published port on non-loopback host IP %q", b.HostIP),
					}
				}
			}
		}
		for netName := range c.NetworkSettings.Networks {
			if netName == "bridge" || netName == "host" || netName == "none" ||
				netName == "stackkit-routing" || netName == "stackkit-workload-egress" || strings.Contains(netName, "routing") {
				return IsolatedRuntimeIsolationEvidence{}, &IsolatedRecoveryError{
					Code:  "isolation_violation",
					Cause: fmt.Errorf("clone container attached to disallowed network %q", netName),
				}
			}
			netNames[netName] = true
		}
	}

	masqueradeDisabled, err := verifyCloneNetworks(ctx, rt, req, netNames)
	if err != nil {
		return IsolatedRuntimeIsolationEvidence{}, err
	}
	return IsolatedRuntimeIsolationEvidence{
		LiveVolumesUnmounted:           true,
		InternalNetworksOnly:           true,
		MasqueradeDisabled:             masqueradeDisabled,
		RoutingLabelsStripped:          true,
		PublishedHostPortsOnlyLoopback: true,
	}, nil
}

func verifyCloneNetworks(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
	netNames map[string]bool,
) (bool, error) {
	if len(netNames) == 0 {
		return false, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: errors.New("clone containers have no attached networks"),
		}
	}
	args := []string{"network", "inspect"}
	sorted := make([]string, 0, len(netNames))
	for n := range netNames {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	args = append(args, sorted...)
	raw, err := rt.docker(ctx, args...)
	if err != nil {
		return false, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("inspect clone networks: %w", err),
		}
	}
	var networks []inspectNetworkRecord
	if err := json.Unmarshal(raw, &networks); err != nil {
		return false, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("decode clone network inspection: %w", err),
		}
	}

	healthNetworkFull := req.CloneProject + "_" + req.HealthNetwork
	sawHealth := false
	masqueradeDisabled := true
	for _, netRec := range networks {
		if netRec.Name == healthNetworkFull {
			sawHealth = true
			if netRec.Options["com.docker.network.bridge.enable_ip_masquerade"] != "false" {
				masqueradeDisabled = false
				return false, &IsolatedRecoveryError{
					Code:  "isolation_violation",
					Cause: fmt.Errorf("clone health bridge %q does not disable outbound masquerading", netRec.Name),
				}
			}
			continue
		}
		if !netRec.Internal {
			return false, &IsolatedRecoveryError{
				Code:  "isolation_violation",
				Cause: fmt.Errorf("clone network %q is not internal-only", netRec.Name),
			}
		}
	}
	return sawHealth && masqueradeDisabled, nil
}

func verifyIsolatedDependencies(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
) ([]IsolatedDependencyCheck, error) {
	raw, err := rt.docker(ctx, append(cloneComposeBaseArgs(req), "ps", "--all", "--no-trunc", "--format", "json")...)
	if err != nil {
		return nil, &IsolatedRecoveryError{
			Code:  "missing_dependency",
			Cause: fmt.Errorf("observe clone service statuses: %w", err),
		}
	}
	statuses, err := parseComposeReadinessStatuses(raw)
	if err != nil {
		return nil, &IsolatedRecoveryError{Code: "missing_dependency", Cause: err}
	}

	checks := make([]IsolatedDependencyCheck, 0, len(req.Components))
	for _, comp := range req.Components {
		st, ok := statuses[comp.ID]
		if !ok {
			return nil, &IsolatedRecoveryError{
				Code:  "missing_dependency",
				Cause: fmt.Errorf("clone component %q is missing from runtime status", comp.ID),
			}
		}
		ready := false
		if comp.Lifecycle == "one-shot" {
			ready = st.State == "exited" && st.ExitCode == 0
		} else {
			ready = st.State == "running" && (st.Health == "" || st.Health == "healthy")
		}
		if !ready && comp.HealthFailure != "degraded" {
			return nil, &IsolatedRecoveryError{
				Code:  "app_not_ready",
				Cause: fmt.Errorf("clone component %q is not ready (state=%q, health=%q, exitCode=%d)", comp.ID, st.State, st.Health, st.ExitCode),
			}
		}
		checks = append(checks, IsolatedDependencyCheck{
			ComponentRef: comp.ID,
			Role:         comp.Role,
			Lifecycle:    comp.Lifecycle,
			State:        st.State,
			Health:       st.Health,
			ExitCode:     st.ExitCode,
			Ready:        ready,
		})
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].ComponentRef < checks[j].ComponentRef })
	return checks, nil
}

const pgReadbackScript = `user=${POSTGRES_USER:-$1}
db=${POSTGRES_DB:-$2}
if [ -n "${POSTGRES_PASSWORD_FILE:-}" ] && [ -r "${POSTGRES_PASSWORD_FILE}" ]; then
  export PGPASSWORD="$(cat "${POSTGRES_PASSWORD_FILE}")"
elif [ -n "${POSTGRES_PASSWORD:-}" ]; then
  export PGPASSWORD="${POSTGRES_PASSWORD}"
fi
pg_isready -U "$user" -d "$db" >/dev/null
psql -X -v ON_ERROR_STOP=1 -U "$user" -d "$db" -tAc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public';"`

func verifyIsolatedPostgreSQL(
	ctx context.Context,
	rt *dockerRuntime,
	req IsolatedApplicationRecoveryRequest,
) (int64, error) {
	idOut, err := rt.docker(ctx, append(cloneComposeBaseArgs(req), "ps", "-q", req.DatabaseComponent)...)
	if err != nil || strings.TrimSpace(string(idOut)) == "" {
		return 0, &IsolatedRecoveryError{
			Code:  "database_not_ready",
			Cause: fmt.Errorf("locate clone PostgreSQL container for %q: %w", req.DatabaseComponent, err),
		}
	}
	containerID := strings.Fields(string(idOut))[0]
	out, err := rt.docker(ctx, "exec", containerID, "/bin/sh", "-ceu", pgReadbackScript, "--", req.DatabaseUser, req.DatabaseName)
	if err != nil {
		return 0, &IsolatedRecoveryError{
			Code:  "database_not_ready",
			Cause: fmt.Errorf("PostgreSQL readiness/readback check failed: %w", err),
		}
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return 0, &IsolatedRecoveryError{
			Code:  "database_not_ready",
			Cause: errors.New("PostgreSQL readback returned empty output"),
		}
	}
	count, err := strconv.ParseInt(lines[len(lines)-1], 10, 64)
	if err != nil || count < 0 {
		return 0, &IsolatedRecoveryError{
			Code:  "database_not_ready",
			Cause: fmt.Errorf("PostgreSQL public table count is malformed: %q", string(out)),
		}
	}
	return count, nil
}

func probeIsolatedEntryHTTP(ctx context.Context, rt *dockerRuntime, req IsolatedApplicationRecoveryRequest) (int, error) {
	portOut, err := rt.docker(ctx, append(cloneComposeBaseArgs(req), "port", req.EntryComponent, strconv.Itoa(req.EntryHealthPort))...)
	if err != nil {
		return 0, &IsolatedRecoveryError{
			Code:  "app_not_ready",
			Cause: fmt.Errorf("resolve loopback health port for %q: %w", req.EntryComponent, err),
		}
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(string(portOut)))
	if err != nil || host != "127.0.0.1" {
		return 0, &IsolatedRecoveryError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("clone entry port binding %q is not restricted to 127.0.0.1", strings.TrimSpace(string(portOut))),
		}
	}
	target := fmt.Sprintf("http://%s%s", net.JoinHostPort(host, port), req.EntryHealthPath)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	deadline := time.Now().Add(isolatedCloneHTTPTimeout)
	var lastStatus int
	var lastErr error
	for {
		httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if reqErr != nil {
			return 0, &IsolatedRecoveryError{Code: "app_not_ready", Cause: reqErr}
		}
		resp, doErr := client.Do(httpReq)
		if doErr == nil {
			_ = resp.Body.Close()
			lastStatus = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return resp.StatusCode, nil
			}
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, req.EntryHealthPath)
		} else {
			lastErr = doErr
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastStatus, &IsolatedRecoveryError{Code: "app_not_ready", Cause: ctx.Err()}
		case <-timer.C:
		}
	}
	return lastStatus, &IsolatedRecoveryError{
		Code:  "app_not_ready",
		Cause: fmt.Errorf("isolated application HTTP readiness check failed: %w", lastErr),
	}
}

func cleanupIsolatedCloneResources(ctx context.Context, rt *dockerRuntime, req IsolatedApplicationRecoveryRequest) error {
	var errs []error
	if _, err := rt.docker(ctx, append(cloneComposeBaseArgs(req), "down", "--volumes", "--remove-orphans")...); err != nil {
		errs = append(errs, err)
	}

	labelFilter := "label=" + "io.stackkit.restore-drill.operation=" + req.DrillID
	if psOut, err := rt.docker(ctx, "ps", "-a", "-q", "--filter", labelFilter); err == nil {
		for _, id := range strings.Fields(string(psOut)) {
			if _, rmErr := rt.docker(ctx, "rm", "-f", id); rmErr != nil {
				errs = append(errs, rmErr)
			}
		}
	}
	for _, vol := range req.Volumes {
		if _, err := rt.docker(ctx, "volume", "inspect", vol.CloneName); err == nil {
			if _, rmErr := rt.docker(ctx, "volume", "rm", "-f", vol.CloneName); rmErr != nil {
				errs = append(errs, rmErr)
			}
		}
	}
	if netOut, err := rt.docker(ctx, "network", "ls", "-q", "--filter", labelFilter); err == nil {
		for _, id := range strings.Fields(string(netOut)) {
			if _, rmErr := rt.docker(ctx, "network", "rm", id); rmErr != nil {
				errs = append(errs, rmErr)
			}
		}
	}

	// Verify zero lingering labeled resources remain.
	if remPS, err := rt.docker(ctx, "ps", "-a", "-q", "--filter", labelFilter); err == nil && strings.TrimSpace(string(remPS)) != "" {
		errs = append(errs, fmt.Errorf("clone containers remain after cleanup: %s", strings.TrimSpace(string(remPS))))
	}
	if remNet, err := rt.docker(ctx, "network", "ls", "-q", "--filter", labelFilter); err == nil && strings.TrimSpace(string(remNet)) != "" {
		errs = append(errs, fmt.Errorf("clone networks remain after cleanup: %s", strings.TrimSpace(string(remNet))))
	}
	for _, vol := range req.Volumes {
		if !slices.Contains([]string{req.StagingVolume, vol.LiveName}, vol.CloneName) {
			if _, err := rt.docker(ctx, "volume", "inspect", vol.CloneName); err == nil {
				errs = append(errs, fmt.Errorf("clone volume %q remains after cleanup", vol.CloneName))
			}
		}
	}
	return errors.Join(errs...)
}
