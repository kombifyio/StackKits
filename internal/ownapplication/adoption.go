package ownapplication

// "Stack it": rebuild one observed container StackKits did not install as an
// own application (techstack.own-application/v4), so it gets the own-application
// lifecycle: digest-pinned update and rollback, volume backup, logs, stop and
// status. The original is never edited; it is stopped and retained until the
// owner releases it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const (
	AdoptionResultVersion     = "stackkit.own-application-adoption/v1"
	adoptionCheckpointVersion = "stackkit.own-application-adoption-checkpoint/v1"
	adoptionDirectory         = ".stackkit/own-applications/adoptions"

	RowKept    = "kept"
	RowChanged = "changed"
	RowRefused = "refused"

	// Defaults for an original without limits: an own application is always
	// bounded, and 512 MiB is too small for most applications people run.
	adoptedDefaultMemoryMiB = 2048
	adoptedDefaultCPUs      = 2
)

var secretKey = regexp.MustCompile(`(?:^|_)(?:KEY|TOKEN|PASSWORD|SECRET)$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var nameCleaner = regexp.MustCompile(`[^a-z0-9]+`)

// AdoptionRequest selects one phase of one adoption.
type AdoptionRequest struct {
	Phase     string // inspect, plan, adopt, verify, restore-original or release-original
	Container string
	// SecretEnv marks further variable names as credentials. Every value is
	// already hidden; a marked name is also classified as a secret.
	SecretEnv     []string
	Name          string
	HTTPPort      int
	NoOutbound    bool
	MemoryMiB     int
	CPUs          float64
	PlanDigest    string
	OperationID   string
	OwnerApproved bool
	// HelperImage is the installed, digest-pinned backup helper that measures,
	// copies and checksums data. Empty only for an offline plan.
	HelperImage  string
	Installation contract.Installation
	// Offline plans against an explicit installation file: no node custody,
	// no secret commitment and no data measurement.
	Offline bool
}

// AdoptionRow is one observed setting and what the adopted application does
// with it.
type AdoptionRow struct {
	// Code is the stable machine code of the row (docs/APPLICATION_ADOPTION.md
	// "Plan row codes"); Values carries its structured facts. Reason is the
	// English explanation of the same row.
	Code     string            `json:"code"`
	Values   map[string]string `json:"values,omitempty"`
	Field    string            `json:"field"`
	Status   string            `json:"status"`
	Observed string            `json:"observed,omitempty"`
	Adopted  string            `json:"adopted,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	// volatile rows (data size, readiness) never enter the plan digest.
	volatile bool
}

type AdoptionData struct {
	Source    string `json:"source"`
	MountPath string `json:"mountPath"`
	Volume    string `json:"volume"`
	Measured  bool   `json:"measured"`
	Files     int64  `json:"files,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	FreeBytes int64  `json:"freeBytes,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type AdoptionCheckpoint struct {
	APIVersion            string    `json:"apiVersion"`
	Name                  string    `json:"name"`
	ContainerID           string    `json:"containerId"`
	PlanDigest            string    `json:"planDigest"`
	OperationID           string    `json:"operationId"`
	OriginalRunning       bool      `json:"originalRunning"`
	OriginalRestartPolicy string    `json:"originalRestartPolicy"`
	DataSource            string    `json:"dataSource,omitempty"`
	DataVolume            string    `json:"dataVolume,omitempty"`
	DataDigest            string    `json:"dataDigest,omitempty"`
	Phase                 string    `json:"phase"`
	Error                 string    `json:"error,omitempty"`
	At                    time.Time `json:"at"`
	// RetainUntil ends the --restore-original window of an adopted original.
	RetainUntil time.Time `json:"retainUntil,omitzero"`
}

type AdoptionResult struct {
	APIVersion    string                `json:"apiVersion"`
	Phase         string                `json:"phase"`
	ContainerID   string                `json:"containerId"`
	ContainerName string                `json:"containerName"`
	Name          string                `json:"name,omitempty"`
	Admissible    bool                  `json:"admissible"`
	PlanDigest    string                `json:"planDigest,omitempty"`
	Rows          []AdoptionRow         `json:"rows"`
	Application   *contract.Application `json:"application,omitempty"`
	// HiddenKeys names every environment variable whose value moved into
	// secret custody (all non-empty values); SecretKeys the ones classified as
	// credentials by name or --secret-env. Values never appear.
	HiddenKeys []string `json:"hiddenKeys"`
	SecretKeys []string `json:"secretKeys"`
	// Outcome is the machine outcome a control plane notifies on: inspected,
	// planned, refused, adopted, rolled_back, rollback_failed, verified,
	// unhealthy, restored, released.
	Outcome string `json:"outcome"`
	// RetainUntil is the end of the window in which --restore-original
	// returns to the original; the original is never deleted automatically.
	RetainUntil      *time.Time               `json:"retainUntil,omitempty"`
	OutboundInternet bool                     `json:"outboundInternet"`
	PublishedPorts   []contract.PublishedPort `json:"publishedPorts"`
	Data             *AdoptionData            `json:"data,omitempty"`
	// DowntimeSeconds estimates the downtime of the recreation (stop, copy,
	// start); the stability window follows it with the application running.
	DowntimeSeconds int64               `json:"downtimeSeconds,omitempty"`
	ReviewDigest    string              `json:"reviewDigest,omitempty"`
	Checkpoint      *AdoptionCheckpoint `json:"checkpoint,omitempty"`
	OwnApplication  *Result             `json:"ownApplication,omitempty"`
	OriginalState   string              `json:"originalState,omitempty"`
	PortProbes      map[string]string   `json:"portProbes,omitempty"`
	Notice          string              `json:"notice"`
}

// AdoptionHooks lets the command layer apply the checks and follow-ups every
// own-application mutation already runs.
type AdoptionHooks struct {
	BeforeCreate  func(contract.Application) error
	AfterMutation func()
}

type observedContainer struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Image  string `json:"Image"`
	Config struct {
		Image        string                     `json:"Image"`
		Env          []string                   `json:"Env"`
		Cmd          []string                   `json:"Cmd"`
		Entrypoint   []string                   `json:"Entrypoint"`
		User         string                     `json:"User"`
		Labels       map[string]string          `json:"Labels"`
		ExposedPorts map[string]json.RawMessage `json:"ExposedPorts"`
		Healthcheck  json.RawMessage            `json:"Healthcheck"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode    string            `json:"NetworkMode"`
		Privileged     bool              `json:"Privileged"`
		CapAdd         []string          `json:"CapAdd"`
		Devices        []json.RawMessage `json:"Devices"`
		DeviceRequests []json.RawMessage `json:"DeviceRequests"`
		PortBindings   map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
		RestartPolicy struct {
			Name              string `json:"Name"`
			MaximumRetryCount int    `json:"MaximumRetryCount"`
		} `json:"RestartPolicy"`
		Memory         int64             `json:"Memory"`
		NanoCpus       int64             `json:"NanoCpus"`
		PidMode        string            `json:"PidMode"`
		IpcMode        string            `json:"IpcMode"`
		UTSMode        string            `json:"UTSMode"`
		SecurityOpt    []string          `json:"SecurityOpt"`
		VolumesFrom    []string          `json:"VolumesFrom"`
		Tmpfs          map[string]string `json:"Tmpfs"`
		ReadonlyRootfs bool              `json:"ReadonlyRootfs"`
		ExtraHosts     []string          `json:"ExtraHosts"`
		DNS            []string          `json:"Dns"`
		Sysctls        map[string]string `json:"Sysctls"`
		Runtime        string            `json:"Runtime"`
		Links          []string          `json:"Links"`
		UsernsMode     string            `json:"UsernsMode"`
		CgroupnsMode   string            `json:"CgroupnsMode"`
		GroupAdd       []string          `json:"GroupAdd"`
		Ulimits        []json.RawMessage `json:"Ulimits"`
		CapDrop        []string          `json:"CapDrop"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
}

type observedImage struct {
	ID          string   `json:"Id"`
	RepoDigests []string `json:"RepoDigests"`
	Config      struct {
		Env         []string                   `json:"Env"`
		Cmd         []string                   `json:"Cmd"`
		Entrypoint  []string                   `json:"Entrypoint"`
		User        string                     `json:"User"`
		Volumes     map[string]json.RawMessage `json:"Volumes"`
		Healthcheck json.RawMessage            `json:"Healthcheck"`
	} `json:"Config"`
}

// adoptionPlan is the deterministic part of a plan plus the private secret
// values that never leave the process.
type adoptionPlan struct {
	result  AdoptionResult
	source  *nativehost.OwnAdoptionMount
	secrets map[string]string
	running bool
	restart string
	address string
}

// rowValues pairs keys and values into a row's structured values.
func rowValues(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	values := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		values[pairs[i]] = pairs[i+1]
	}
	return values
}
func (p *adoptionPlan) row(code, field, status, observed, adopted, reason string, values ...string) {
	p.result.Rows = append(p.result.Rows, AdoptionRow{Code: code, Values: rowValues(values), Field: field, Status: status, Observed: observed, Adopted: adopted, Reason: reason})
}
func (p *adoptionPlan) refuse(code, field, observed, reason string, values ...string) {
	p.row(code, field, RowRefused, observed, "", reason, values...)
}

// restartCode names the restart row: a firewalled application is started by
// its host firewall watcher, every other one restarts unless stopped.
func restartCode(policy string) string {
	if policy == "no" {
		return "restart_by_firewall_watcher"
	}
	return "restart_unless_stopped"
}

// Adopt runs one phase of a container adoption.
func Adopt(ctx context.Context, workspace string, req AdoptionRequest, hooks AdoptionHooks) (AdoptionResult, error) {
	docker := nativehost.NewOwnAdoptionDocker()
	switch req.Phase {
	case "inspect":
		p, err := observe(ctx, docker, req)
		if err != nil {
			return AdoptionResult{}, err
		}
		p.result.Outcome, p.result.Admissible = "inspected", !hasRefusal(p.result.Rows)
		p.result.Notice = "Inspection only; nothing has changed. Run --phase plan for the admitted application, its data copy, the downtime and the plan digest."
		return p.result, nil
	case "plan":
		p, err := plan(ctx, workspace, docker, req)
		if err != nil {
			return AdoptionResult{}, err
		}
		return p.result, nil
	case "adopt":
		return adopt(ctx, workspace, docker, req, hooks)
	case "verify":
		return verifyAdoption(ctx, workspace, docker, req)
	case "restore-original":
		return restoreOriginal(ctx, workspace, docker, req, hooks)
	case "release-original":
		return releaseOriginal(ctx, workspace, docker, req)
	}
	return AdoptionResult{}, errors.New("phase must be inspect, plan, adopt or verify (or --restore-original / --release-original)")
}

// observe inspects the container and builds every row that needs no node.
func observe(ctx context.Context, docker nativehost.OwnAdoptionDocker, req AdoptionRequest) (*adoptionPlan, error) {
	raw, err := docker.InspectContainer(ctx, req.Container)
	if err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", req.Container, err)
	}
	var containers []observedContainer
	if err := json.Unmarshal(raw, &containers); err != nil {
		return nil, fmt.Errorf("Docker container inspection is not a Docker Engine answer: %w", err)
	}
	if len(containers) != 1 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(containers[0].ID) {
		return nil, errors.New("Docker inspection did not return exactly one container")
	}
	c := containers[0]
	rawImage, err := docker.InspectImage(ctx, c.Image)
	if err != nil {
		return nil, fmt.Errorf("inspect image of container: %w", err)
	}
	var images []observedImage
	if json.Unmarshal(rawImage, &images) != nil || len(images) != 1 || images[0].ID != c.Image {
		return nil, errors.New("Docker image inspection did not match the container image")
	}
	image := images[0]
	p := &adoptionPlan{secrets: map[string]string{}, running: c.State.Running, restart: restartPolicy(c)}
	p.result = AdoptionResult{APIVersion: AdoptionResultVersion, Phase: req.Phase, ContainerID: c.ID, ContainerName: strings.TrimPrefix(c.Name, "/"), Rows: []AdoptionRow{}, HiddenKeys: []string{}, SecretKeys: []string{}, PublishedPorts: []contract.PublishedPort{}}
	for _, network := range c.NetworkSettings.Networks {
		if p.address == "" && network.IPAddress != "" {
			p.address = network.IPAddress
		}
	}
	app := contract.Application{APIVersion: contract.APIVersionV4}

	// Identity: never adopt what StackKits already runs.
	labels := c.Config.Labels
	project, service := labels["com.docker.compose.project"], labels["com.docker.compose.service"]
	managed := strings.HasPrefix(project, "stackkit-") || strings.HasPrefix(p.result.ContainerName, "stackkit-")
	for key := range labels {
		if strings.HasPrefix(key, "io.stackkit.") {
			managed = true
		}
	}
	if managed {
		p.refuse("refused_already_managed", "source", p.result.ContainerName, "StackKits already manages this container", "container", p.result.ContainerName)
	}
	configRef := "docker:" + p.result.ContainerName
	if project != "" {
		// A Compose-launched container is part of a project whose other
		// services (often a database) link to it by name; rebuilding one
		// service alone would break them silently.
		configRef = "compose:" + project + "/" + service
		p.refuse("refused_compose_project", "compose", project+"/"+service, "the container belongs to a Compose project; Stack it adopts plain single containers (Compose projects come later)", "project", project, "service", service)
	}
	for network := range c.NetworkSettings.Networks {
		if network == "bridge" || network == "host" || network == "none" {
			continue
		}
		peers, err := docker.NetworkPeers(ctx, network)
		if err != nil {
			return nil, fmt.Errorf("inspect network %s: %w", network, err)
		}
		others := 0
		for _, peer := range peers {
			if peer != c.ID {
				others++
			}
		}
		if others != 0 {
			p.refuse("refused_shared_user_network", "network "+network, fmt.Sprintf("%d other containers", others), "other containers on this user-defined network may reach it by name (for example a database link); rebuilding it alone would break them", "network", network, "containers", strconv.Itoa(others))
		}
	}
	if !regexp.MustCompile(`^(?:compose|docker):[A-Za-z0-9][A-Za-z0-9_./-]{0,255}$`).MatchString(configRef) {
		configRef = "docker:" + c.ID[:12]
	}

	// Image: a pullable registry image pinned by the digest the original ran.
	pinned, digest := pinnedImage(c.Config.Image, image.RepoDigests)
	if pinned == "" {
		p.refuse("refused_local_only_image", "image", c.Config.Image, "no registry digest: a local-only or locally built image cannot be adopted; push it to a public registry first", "image", c.Config.Image)
	} else if _, _, err := contract.SplitImage(pinned); err != nil {
		p.refuse("refused_image_reference", "image", c.Config.Image, err.Error(), "image", c.Config.Image)
	} else {
		app.Image = pinned
		p.row("image_pinned_by_digest", "image", RowKept, c.Config.Image, pinned, "pinned by the digest the original runs; updates and rollback stay digest-pinned", "image", c.Config.Image, "pinned", pinned)
	}
	if !slices.Equal(c.Config.Cmd, image.Config.Cmd) || !slices.Equal(c.Config.Entrypoint, image.Config.Entrypoint) {
		// Arguments may carry credentials: the row names only their count.
		p.refuse("refused_custom_command", "command", fmt.Sprintf("custom entrypoint/command (%d arguments)", len(c.Config.Entrypoint)+len(c.Config.Cmd)), "a custom command or entrypoint is not admitted; own applications run the image's own command", "arguments", strconv.Itoa(len(c.Config.Entrypoint)+len(c.Config.Cmd)))
	}
	if c.Config.User != image.Config.User {
		p.refuse("refused_user_override", "user", c.Config.User, "a user override is not admitted; own applications run the image's USER", "user", c.Config.User)
	}

	// Privilege and host namespaces.
	switch mode := c.HostConfig.NetworkMode; {
	case mode == "host":
		p.refuse("refused_host_network", "network", mode, "host networking is not admitted")
	case strings.HasPrefix(mode, "container:"):
		p.refuse("refused_container_network", "network", mode, "sharing another container's network is not admitted", "mode", mode)
	}
	if c.HostConfig.Privileged {
		p.refuse("refused_privileged", "privileged", "true", "privileged containers are not admitted")
	}
	if len(c.HostConfig.CapAdd) != 0 {
		p.refuse("refused_added_capabilities", "capabilities", strings.Join(c.HostConfig.CapAdd, ","), "added capabilities are not admitted", "capabilities", strings.Join(c.HostConfig.CapAdd, ","))
	}
	if len(c.HostConfig.Devices) != 0 || len(c.HostConfig.DeviceRequests) != 0 {
		p.refuse("refused_devices", "devices", strconv.Itoa(len(c.HostConfig.Devices)+len(c.HostConfig.DeviceRequests)), "device passthrough is not admitted", "count", strconv.Itoa(len(c.HostConfig.Devices)+len(c.HostConfig.DeviceRequests)))
	}
	for field, mode := range map[string]string{"pid": c.HostConfig.PidMode, "ipc": c.HostConfig.IpcMode, "uts": c.HostConfig.UTSMode} {
		if mode == "host" || strings.HasPrefix(mode, "container:") {
			p.refuse("refused_shared_namespace", field, mode, "sharing a host or container namespace is not admitted", "namespace", field, "mode", mode)
		}
	}
	for _, option := range c.HostConfig.SecurityOpt {
		if strings.Contains(option, "unconfined") || strings.HasPrefix(option, "label:disable") {
			p.refuse("refused_relaxed_security", "security", option, "a relaxed security profile is not admitted", "option", option)
		}
	}
	if len(c.HostConfig.VolumesFrom) != 0 {
		p.refuse("refused_volumes_from", "volumes-from", strings.Join(c.HostConfig.VolumesFrom, ","), "shared volumes from another container are not admitted", "containers", strings.Join(c.HostConfig.VolumesFrom, ","))
	}
	if c.HostConfig.Runtime != "" && c.HostConfig.Runtime != "runc" {
		p.refuse("refused_custom_runtime", "runtime", c.HostConfig.Runtime, "a custom container runtime is not admitted", "runtime", c.HostConfig.Runtime)
	}
	if len(c.HostConfig.Links) != 0 {
		p.refuse("refused_legacy_links", "links", strconv.Itoa(len(c.HostConfig.Links))+" links", "legacy container links would break when the container is rebuilt", "count", strconv.Itoa(len(c.HostConfig.Links)))
	}
	if c.HostConfig.UsernsMode == "host" {
		p.refuse("refused_host_user_namespace", "userns", "host", "the host user namespace is not admitted")
	}
	if c.HostConfig.CgroupnsMode == "host" {
		p.row("cgroupns_daemon_default", "cgroupns", RowChanged, "host", "daemon default", "own applications take the daemon default cgroup namespace (host is the default only on cgroup v1 hosts)")
	}
	if len(c.HostConfig.CapDrop) != 0 {
		p.row("capabilities_fixed_policy", "cap-drop", RowChanged, strings.Join(c.HostConfig.CapDrop, ","), "ALL, then CHOWN, SETUID, SETGID, NET_BIND_SERVICE", "own applications use the fixed capability policy", "dropped", strings.Join(c.HostConfig.CapDrop, ","))
	}
	for field, dropped := range map[string]int{"extra-hosts": len(c.HostConfig.ExtraHosts), "dns": len(c.HostConfig.DNS), "sysctls": len(c.HostConfig.Sysctls), "tmpfs": len(c.HostConfig.Tmpfs), "group-add": len(c.HostConfig.GroupAdd), "ulimits": len(c.HostConfig.Ulimits)} {
		if dropped != 0 {
			p.row("host_override_dropped", field, RowChanged, strconv.Itoa(dropped)+" entries", "dropped", "own applications take no host-level overrides", "override", field, "count", strconv.Itoa(dropped))
		}
	}
	if c.HostConfig.ReadonlyRootfs {
		p.row("read_only_root_dropped", "read-only-root", RowChanged, "true", "false", "own applications have a writable root filesystem")
	}

	// Data: at most one named volume or bind below an admitted data root.
	var data []int
	for i, m := range c.Mounts {
		switch {
		case strings.HasSuffix(m.Source, "docker.sock") || strings.HasSuffix(m.Destination, "docker.sock"):
			p.refuse("refused_docker_socket", "mount "+m.Destination, m.Source, "the Docker socket is never mounted into an own application", "mountPath", m.Destination)
		case m.Type == "tmpfs":
			p.row("tmpfs_mount_dropped", "mount "+m.Destination, RowChanged, "tmpfs", "dropped", "own applications keep no tmpfs mounts", "mountPath", m.Destination)
		case m.Type == "bind" && !m.RW && (m.Source == "/etc/localtime" || m.Source == "/etc/timezone"):
			p.row("timezone_mount_dropped", "mount "+m.Destination, RowChanged, m.Source, "dropped", "set the TZ environment variable for the time zone", "mountPath", m.Destination, "source", m.Source)
		case m.Type == "bind" && (m.Source == "/" || !contract.AdoptableBindSource(m.Source)):
			p.refuse("refused_bind_outside_data_roots", "mount "+m.Destination, m.Source, "a bind mount outside "+strings.Join(contract.AdoptedDataRoots, ", ")+" is not admitted", "mountPath", m.Destination, "source", m.Source, "dataRoots", strings.Join(contract.AdoptedDataRoots, ","))
		case m.Type == "bind" || m.Type == "volume":
			data = append(data, i)
		default:
			p.refuse("refused_mount_type", "mount "+m.Destination, m.Type, "unsupported mount type", "mountPath", m.Destination, "type", m.Type)
		}
	}
	if len(data) > 1 {
		p.refuse("refused_multiple_data_mounts", "data", strconv.Itoa(len(data))+" data mounts", "more than one data mount is not admitted; own applications keep one named volume", "count", strconv.Itoa(len(data)))
	}
	if len(data) == 1 {
		m := c.Mounts[data[0]]
		source := nativehost.OwnAdoptionMount{Type: m.Type, Source: m.Source}
		ref := "bind:" + m.Source
		if m.Type == "volume" {
			source.Source, ref = m.Name, "volume:"+m.Name
		}
		p.source = &source
		app.Volume = &contract.Volume{MountPath: m.Destination}
		app.AdoptedFrom = &contract.AdoptedFrom{Volumes: []string{ref}}
		reason := "copied into the application's named volume; the original stays untouched"
		if !m.RW {
			reason += "; the mount becomes writable"
		}
		p.row("volume_copied_checksummed", "data", RowKept, ref+" at "+m.Destination, "named volume at "+m.Destination, reason, "source", ref, "mountPath", m.Destination, "madeWritable", strconv.FormatBool(!m.RW))
		p.result.Data = &AdoptionData{Source: ref, MountPath: m.Destination}
	}
	for declared := range image.Config.Volumes {
		if app.Volume == nil || declared != app.Volume.MountPath {
			p.refuse("refused_image_volume", "image volume "+declared, declared, "the image declares a volume the application does not keep as its one named volume", "mountPath", declared)
		}
	}
	if app.AdoptedFrom == nil {
		app.AdoptedFrom = &contract.AdoptedFrom{}
	}
	app.AdoptedFrom.ContainerID = c.ID
	app.AdoptedFrom.ImageDigest = digest
	app.AdoptedFrom.OriginalConfigRef = configRef

	// Environment: what the container sets beyond its image. Every value is
	// hidden by default: it moves into application-scoped encrypted secret
	// custody, and plans, logs and receipts carry only its name. An empty value
	// has nothing to hide and stays a plain variable.
	imageEnv := map[string]bool{}
	for _, entry := range image.Config.Env {
		imageEnv[entry] = true
	}
	environment := map[string]string{}
	for _, entry := range c.Config.Env {
		if imageEnv[entry] {
			continue
		}
		key, value, found := strings.Cut(entry, "=")
		if !found || !envName.MatchString(key) || len(key) > 128 {
			// An invalid name may itself be a value: show nothing of it.
			p.refuse("refused_environment_entry", "environment", "an entry without a valid NAME=value form", "an environment entry without a valid name is not admitted")
			continue
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			p.refuse("refused_multiline_environment", "environment "+key, "", "a multi-line environment value is not admitted", "name", key)
			continue
		}
		if value == "" {
			environment[key] = ""
			continue
		}
		if len(value) > 8192 {
			p.refuse("refused_environment_value_size", "environment "+key, "", "an environment value is limited to 8192 bytes", "name", key, "limitBytes", "8192")
			continue
		}
		p.secrets[key] = value
	}
	if len(environment) > 64 || len(p.secrets) > 64 {
		p.refuse("refused_environment_count", "environment", strconv.Itoa(len(environment))+" variables, "+strconv.Itoa(len(p.secrets))+" secrets", "at most 64 environment variables and 64 secrets", "variables", strconv.Itoa(len(environment)), "secrets", strconv.Itoa(len(p.secrets)), "limit", "64")
	}
	if len(environment) != 0 {
		app.Environment = environment
		keys := make([]string, 0, len(environment))
		for key := range environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		p.row("environment_empty_kept", "environment", RowKept, strings.Join(keys, ","), "empty values", "", "names", strings.Join(keys, ","))
	}
	if len(p.secrets) != 0 {
		for key := range p.secrets {
			p.result.HiddenKeys = append(p.result.HiddenKeys, key)
			if secretKey.MatchString(strings.ToUpper(key)) {
				p.result.SecretKeys = append(p.result.SecretKeys, key)
			}
		}
		sort.Strings(p.result.HiddenKeys)
		p.row("environment_values_to_custody", "environment-values", RowChanged, strings.Join(p.result.HiddenKeys, ","), "secretRefs", "every value moves into encrypted node custody (0600, application-scoped); plans, logs and receipts carry names only and values never leave the node", "names", strings.Join(p.result.HiddenKeys, ","))
	}
	for _, key := range req.SecretEnv {
		_, hidden := p.secrets[key]
		_, empty := environment[key]
		switch {
		case !hidden && !empty:
			p.refuse("refused_unknown_secret_env", "secret "+key, "", "--secret-env names a variable the container does not set", "name", key)
		case hidden && !slices.Contains(p.result.SecretKeys, key):
			p.result.SecretKeys = append(p.result.SecretKeys, key)
		}
	}
	sort.Strings(p.result.SecretKeys)
	if len(p.result.SecretKeys) != 0 {
		p.row("secrets_classified", "secrets", RowKept, strings.Join(p.result.SecretKeys, ","), "classified secret", "named like a credential or marked with --secret-env; never shown, logged or returned, and rotated with app own --action update --secrets-file", "names", strings.Join(p.result.SecretKeys, ","))
	}

	// Ports: one routed HTTP port, and the host ports LAN clients use.
	tcp := map[int]bool{}
	published := map[int]bool{}
	for spec := range c.Config.ExposedPorts {
		if port, proto, ok := parsePortSpec(spec); ok && proto == "tcp" {
			tcp[port] = true
		}
	}
	var bindings []portBinding
	for spec, list := range c.HostConfig.PortBindings {
		port, proto, ok := parsePortSpec(spec)
		if !ok {
			continue
		}
		if proto == "tcp" {
			tcp[port] = true
		}
		for _, b := range list {
			bindings = append(bindings, portBinding{spec: spec, ip: b.HostIP, host: b.HostPort, port: port, proto: proto})
			if proto == "tcp" {
				published[port] = true
			}
		}
	}
	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].spec+bindings[i].ip+bindings[i].host < bindings[j].spec+bindings[j].ip+bindings[j].host
	})
	httpPort := req.HTTPPort
	candidates := sortedPorts(tcp)
	if httpPort == 0 {
		switch {
		case len(candidates) == 1:
			httpPort = candidates[0]
		case len(sortedPorts(published)) == 1:
			httpPort = sortedPorts(published)[0]
		}
	}
	if httpPort < 1 || httpPort > 65535 {
		p.refuse("refused_http_port_ambiguous", "http-port", joinPorts(candidates), "choose the web port with --http-port; own applications serve HTTP on one port", "candidates", joinPorts(candidates))
	} else {
		app.HTTPPort = httpPort
		p.row("http_port_routed", "http-port", RowKept, joinPorts(candidates), strconv.Itoa(httpPort), "routed on https://own-<name>.<domain> behind Homelab sign-in; readiness needs HTTP 2xx/3xx on /", "port", strconv.Itoa(httpPort), "candidates", joinPorts(candidates))
	}

	outbound := c.HostConfig.NetworkMode != "none"
	switch {
	case !outbound:
		p.row("outbound_none_kept", "outbound-internet", RowKept, "none", "off", "")
	case req.NoOutbound:
		outbound = false
		p.row("outbound_turned_off", "outbound-internet", RowChanged, "on", "off", "--no-outbound: the application keeps no Internet access; its published ports stay, answering only connections made to it")
	default:
		p.row("outbound_kept_firewalled", "outbound-internet", RowKept, "on", "on", "public IPv4 only through the managed host firewall; no home, host or special-use destinations; IPv6 off")
	}
	if outbound {
		enabled := true
		app.OutboundInternet = &enabled
	}
	for _, b := range bindings {
		// Each binding keeps exactly its original host address: never widened
		// (127.0.0.1 stays 127.0.0.1, 0.0.0.0 stays IPv4-only).
		address := b.ip
		if address == "" {
			address = "*"
		}
		observed := address + ":" + b.host + "->" + b.spec
		ip := b.ip
		hostPort, err := strconv.Atoi(b.host)
		switch {
		case b.host == "" || err != nil:
			p.row("port_random_dropped", "port "+b.spec, RowChanged, observed, "dropped", "a random host port is not kept", "binding", observed)
		case ip != "" && !isIPv4(ip):
			if !hasIPv4Twin(bindings, b.spec, b.host) {
				p.row("port_ipv6_only_dropped", "port "+b.spec, RowChanged, observed, "dropped", "IPv6-only host bindings are not kept", "binding", observed)
			}
		case b.proto == "tcp" && (hostPort == 80 || hostPort == 443):
			p.row("port_router_owned_dropped", "port "+b.spec, RowChanged, observed, "dropped", "the installation router owns 80 and 443; use the own-<name> hostname", "binding", observed, "hostPort", b.host)
		default:
			port := contract.PublishedPort{HostIP: ip, HostPort: hostPort, ContainerPort: b.port, Protocol: b.proto}
			if !slices.Contains(app.PublishedPorts, port) {
				app.PublishedPorts = append(app.PublishedPorts, port)
				p.row("port_kept", "port "+b.spec, RowKept, observed, observed, "same host address and port, so clients keep using host:port; it stays exactly as reachable and as unauthenticated as before", "hostIP", ip, "hostPort", b.host, "containerPort", strconv.Itoa(b.port), "protocol", b.proto)
			}
		}
	}
	p.result.OutboundInternet = outbound
	if app.PublishedPorts != nil {
		p.result.PublishedPorts = app.PublishedPorts
	}

	restart := "unless-stopped"
	reason := "own applications restart unless stopped"
	if app.Firewalled() {
		restart, reason = "no", "the managed host firewall starts it: after a host reboot its restore unit starts it again once the reviewed rules are restored and verified"
	}
	if p.restart != restart {
		p.row(restartCode(restart), "restart", RowChanged, p.restart, restart, reason, "from", p.restart, "to", restart)
	}
	networks := make([]string, 0, len(c.NetworkSettings.Networks))
	for network := range c.NetworkSettings.Networks {
		networks = append(networks, network)
	}
	sort.Strings(networks)
	p.row("own_private_network", "networks", RowChanged, strings.Join(networks, ","), "own private network", "no container reached it by name; it joins its own private network", "networks", strings.Join(networks, ","))
	signIn := true
	app.SignIn = &signIn
	p.row("hostname_route_with_sign_in", "access", RowChanged, "published ports only", "https://own-<name>.<domain> with Homelab sign-in, plus the kept ports", "the new hostname route requires Homelab sign-in and follows the installation's exposure; nothing becomes publicly reachable that was not before")
	p.row("container_recreated", "recreation", RowChanged, "container "+c.ID[:12], "a new container (new container ID)", "the original is stopped, never edited, and kept stopped with restart policy no; it is recreated as a new container, so there is downtime (see the downtime row)", "containerId", c.ID[:12])
	if len(c.Config.Healthcheck) != 0 && string(c.Config.Healthcheck) != "null" && string(c.Config.Healthcheck) != string(image.Config.Healthcheck) {
		p.row("healthcheck_from_image", "healthcheck", RowChanged, "container override", "image healthcheck", "own applications use the image's healthcheck and an HTTP readiness check")
	}

	// Resources: own applications are always bounded.
	memory := req.MemoryMiB
	observedMemory := "unlimited"
	if c.HostConfig.Memory > 0 {
		observedMemory = strconv.FormatInt(c.HostConfig.Memory>>20, 10) + " MiB"
		if memory == 0 {
			memory = int(c.HostConfig.Memory >> 20)
		}
	}
	if memory == 0 {
		memory = adoptedDefaultMemoryMiB
	}
	memory = min(max(memory, 64), 8192)
	cpus := req.CPUs
	observedCPUs := "unlimited"
	if c.HostConfig.NanoCpus > 0 {
		observedCPUs = strconv.FormatFloat(float64(c.HostConfig.NanoCpus)/1e9, 'f', -1, 64)
		if cpus == 0 {
			cpus = float64(c.HostConfig.NanoCpus) / 1e9
		}
	}
	if cpus == 0 {
		cpus = adoptedDefaultCPUs
	}
	cpus = min(max(cpus, 0.1), 4)
	app.Resources = contract.Resources{MemoryMiB: memory, CPUs: cpus}
	limits := strconv.Itoa(memory) + " MiB, " + strconv.FormatFloat(cpus, 'f', -1, 64) + " CPUs"
	if observedMemory+", "+observedCPUs+" CPUs" != limits {
		p.row("resources_bounded", "resources", RowChanged, observedMemory+", "+observedCPUs+" CPUs", limits, "own applications are bounded (64..8192 MiB, 0.1..4 CPUs); set --memory-mib and --cpus to choose", "memoryMiB", strconv.Itoa(memory), "cpus", strconv.FormatFloat(cpus, 'f', -1, 64))
	}

	{
		name := req.Name
		if name == "" {
			name = strings.Trim(nameCleaner.ReplaceAllString(strings.ToLower(p.result.ContainerName), "-"), "-")
			if name != "" && (name[0] < 'a' || name[0] > 'z') {
				name = "app-" + name
			}
			if len(name) > 40 {
				name = strings.Trim(name[:40], "-")
			}
		}
		if !contract.ValidName(name) || strings.HasPrefix(name, "kombify") || strings.HasPrefix(name, "stackkit") {
			p.refuse("refused_name", "name", name, "choose --name: 1..40 lowercase DNS characters starting with a letter, not kombify* or stackkit*", "name", name)
		}
		app.Name = name
		p.result.Name = name
	}
	if len(p.secrets) != 0 {
		app.SecretRefs = map[string]string{}
		for key := range p.secrets {
			app.SecretRefs[key] = "secret://own-" + app.Name + "/" + key
		}
	}
	p.result.Application = &app
	return p, nil
}

func secretMaterial(secrets map[string]string) map[string]string {
	if len(secrets) == 0 {
		return nil
	}
	return secrets
}

func hasRefusal(rows []AdoptionRow) bool {
	for _, row := range rows {
		if row.Status == RowRefused {
			return true
		}
	}
	return false
}

func restartPolicy(c observedContainer) string {
	policy := c.HostConfig.RestartPolicy.Name
	if policy == "" {
		return "no"
	}
	if policy == "on-failure" && c.HostConfig.RestartPolicy.MaximumRetryCount > 0 {
		return policy + ":" + strconv.Itoa(c.HostConfig.RestartPolicy.MaximumRetryCount)
	}
	return policy
}

// pinnedImage returns the registry reference pinned by the digest the
// original runs, preferring the repository the container names.
func pinnedImage(configured string, repoDigests []string) (string, string) {
	want := normalizeRepository(configured)
	chosen := ""
	for _, candidate := range repoDigests {
		repository, digest, ok := strings.Cut(candidate, "@")
		if !ok || !strings.HasPrefix(digest, "sha256:") {
			continue
		}
		pinned := normalizeRepository(repository) + "@" + digest
		if normalizeRepository(repository) == want {
			return pinned, digest
		}
		if chosen == "" {
			chosen = pinned
		}
	}
	if chosen == "" {
		return "", ""
	}
	_, digest, _ := strings.Cut(chosen, "@")
	return chosen, digest
}

// normalizeRepository spells a Docker reference as registry/repository
// without tag or digest: nginx is docker.io/library/nginx.
func normalizeRepository(reference string) string {
	reference, _, _ = strings.Cut(reference, "@")
	if slash, colon := strings.LastIndex(reference, "/"), strings.LastIndex(reference, ":"); colon > slash {
		reference = reference[:colon]
	}
	first, rest, found := strings.Cut(reference, "/")
	switch {
	case !found:
		return "docker.io/library/" + reference
	case !strings.ContainsAny(first, ".:") && first != "localhost":
		return "docker.io/" + reference
	case first == "index.docker.io" || first == "registry-1.docker.io":
		return "docker.io/" + rest
	}
	return reference
}

func parsePortSpec(spec string) (int, string, bool) {
	port, proto, found := strings.Cut(spec, "/")
	if !found {
		proto = "tcp"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || (proto != "tcp" && proto != "udp") {
		return 0, "", false
	}
	return n, proto, true
}

func sortedPorts(ports map[int]bool) []int {
	list := make([]int, 0, len(ports))
	for port := range ports {
		list = append(list, port)
	}
	sort.Ints(list)
	return list
}

func joinPorts(ports []int) string {
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ",")
}

func isIPv4(value string) bool {
	ip, err := netip.ParseAddr(value)
	return err == nil && ip.Is4()
}

// portBinding is one observed host binding of a container port.
type portBinding struct {
	spec  string
	ip    string
	host  string
	port  int
	proto string
}

// hasIPv4Twin reports whether the same container port is also bound on IPv4
// (or every address) at the same host port, so its IPv6 twin adds nothing.
func hasIPv4Twin(bindings []portBinding, spec, host string) bool {
	for _, b := range bindings {
		if b.spec == spec && b.host == host && (b.ip == "" || isIPv4(b.ip)) {
			return true
		}
	}
	return false
}

func checkpointPath(name string) string { return adoptionDirectory + "/" + name + ".json" }

func loadCheckpoint(workspace, name string) (AdoptionCheckpoint, bool, error) {
	var checkpoint AdoptionCheckpoint
	if !contract.ValidName(name) {
		return checkpoint, false, errors.New("valid own application name is required")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return checkpoint, false, err
	}
	defer root.Close()
	t, err := root.BeginTransaction()
	if err != nil {
		return checkpoint, false, err
	}
	defer t.Close()
	raw, _, err := t.ReadStableBounded(checkpointPath(name), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return checkpoint, false, nil
	}
	if err != nil {
		return checkpoint, false, err
	}
	if json.Unmarshal(raw, &checkpoint) != nil || checkpoint.APIVersion != adoptionCheckpointVersion || checkpoint.Name != name {
		return checkpoint, false, errors.New("own application adoption checkpoint is malformed")
	}
	return checkpoint, true, nil
}

func listCheckpoints(workspace string) ([]AdoptionCheckpoint, error) {
	state, err := Load(workspace)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for name := range state.Applications {
		names[name] = true
	}
	entries, err := os.ReadDir(workspace + "/" + adoptionDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".json"); ok && contract.ValidName(name) {
			names[name] = true
		}
	}
	var checkpoints []AdoptionCheckpoint
	for name := range names {
		checkpoint, found, err := loadCheckpoint(workspace, name)
		if err != nil {
			return nil, err
		}
		if found {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	return checkpoints, nil
}

func saveCheckpoint(workspace string, checkpoint AdoptionCheckpoint) error {
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	t, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err = t.MkdirAll(adoptionDirectory, 0700); err != nil {
		t.Close()
		return err
	}
	t.Close()
	view, err := root.View("")
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600(checkpointPath(checkpoint.Name), raw)
	return err
}
