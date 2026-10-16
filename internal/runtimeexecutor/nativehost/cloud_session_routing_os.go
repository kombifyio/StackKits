package nativehost

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/addressplan"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	stackdocker "github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"gopkg.in/yaml.v3"
)

type CloudSessionRuntime struct {
	workspace        string
	docker           *stackdocker.Client
	migrateComponent bool
}

func NewCloudSessionMigrationRuntime(workspace string, migrate bool) *CloudSessionRuntime {
	runtime := NewCloudSessionRuntime(workspace)
	runtime.migrateComponent = migrate
	return runtime
}

func NewCloudSessionRuntime(workspace string) *CloudSessionRuntime {
	return &CloudSessionRuntime{workspace: workspace, docker: stackdocker.NewLocalClient(stackdocker.WithTimeout(45 * time.Second))}
}

func (r *CloudSessionRuntime) Capture(ctx context.Context, before, after addressplan.SessionTopology) (localevidence.CloudSessionCapture, error) {
	return r.inspectSession(ctx, before, after, true)
}

func (r *CloudSessionRuntime) Check(ctx context.Context, before, after addressplan.SessionTopology) error {
	_, err := r.inspectSession(ctx, before, after, false)
	return err
}

func (r *CloudSessionRuntime) inspectSession(ctx context.Context, before, after addressplan.SessionTopology, persist bool) (localevidence.CloudSessionCapture, error) {
	var capture localevidence.CloudSessionCapture
	source, err := localevidence.ReadCloudSessionFile(r.workspace, localevidence.CloudSessionSourcePath)
	if err != nil {
		return capture, err
	}
	containers, err := r.containers(ctx, before, before)
	if err != nil {
		return capture, err
	}
	if err := r.verifySourceConfigHashes(ctx, source, containers); err != nil {
		return capture, err
	}
	migration, err := r.captureComponentMigration(ctx, source)
	if err != nil {
		return capture, err
	}
	image := ""
	if migration != nil {
		image = migration.ToImage
	}
	overlay, err := architecturev2renderer.CloudSessionRoutingComponentOverlay(source, before, after, image)
	if err != nil {
		return capture, err
	}
	if persist {
		if err := localevidence.WriteCloudSessionFile(r.workspace, localevidence.CloudSessionOverlayPath, overlay); err != nil {
			return capture, err
		}
	}
	return localevidence.CloudSessionCapture{SourceDigest: localevidence.SessionRoutingDigest(source), OverlayDigest: localevidence.SessionRoutingDigest(overlay), Containers: containers, Migration: migration}, nil
}

func requireCloudSessionTinyAuthPin(source []byte) error {
	var model struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(source, &model) != nil {
		return errors.New("cloud_session_transition_invalid: retained component pin cannot be read")
	}
	for _, component := range architecturev2renderer.CloudStandaloneCoreServiceContracts() {
		if component.Ref == "tinyauth" && model.Services["tinyauth"].Image == component.ImageRef+"@"+component.ImageDigest {
			return nil
		}
	}
	return errors.New("cloud_session_tinyauth_pin_unsupported: retained TinyAuth differs from the current policy-qualified component; no route, callback or custody mutation admitted")
}

func (r *CloudSessionRuntime) Activate(ctx context.Context, state localevidence.CloudSessionRouting) (localevidence.CloudSessionRouting, error) {
	var admissionErr error
	state, admissionErr = r.admit(state)
	if admissionErr != nil {
		return state, admissionErr
	}
	if err := r.verify(ctx, state, false); err != nil {
		return state, err
	}
	state, admissionErr = r.prepareComponentMigration(ctx, state)
	if admissionErr != nil {
		return state, admissionErr
	}
	args := []string{"compose", "--project-name", "stackkit-cloud-core-standalone", "-f", filepath.Join(r.workspace, filepath.FromSlash(localevidence.CloudSessionSourcePath)),
		"-f", filepath.Join(r.workspace, filepath.FromSlash(localevidence.CloudSessionOverlayPath)), "up", "-d", "--wait", "--wait-timeout", "60", "--no-build", "--pull", "never", "--no-deps", "tinyauth", "hub"}
	operations, err := newOSCloudCoreOperations(r.workspace, "cloud-core-standalone")
	if err != nil {
		return state, err
	}
	native := operations.(*osCloudCoreOperations)
	environment, err := localevidence.ComposeInterpolationEnvironment(r.workspace)
	if err != nil {
		return state, err
	}
	_, err = native.runner.Run(ctx, args, filepath.Dir(args[4]), environment)
	if err != nil {
		return state, errors.New("cloud_session_runtime_pending: session consumer reconciliation interrupted; resume the same transition")
	}
	if err := r.Verify(ctx, state); err != nil {
		return state, err
	}
	if state.Migration != nil {
		state.Migration.Phase = "target-active"
		return r.saveComponent(state)
	}
	return state, nil
}

func (r *CloudSessionRuntime) Verify(ctx context.Context, state localevidence.CloudSessionRouting) error {
	var err error
	state, err = r.admit(state)
	if err != nil {
		return err
	}
	return r.verify(ctx, state, true)
}

func (r *CloudSessionRuntime) admit(state localevidence.CloudSessionRouting) (localevidence.CloudSessionRouting, error) {
	verified, err := localevidence.LoadCloudSessionRouting(r.workspace)
	if err != nil {
		return state, err
	}
	expected := verified
	expected.Signature = localevidence.OwnerPolicyStateSignature{}
	state.Signature = localevidence.OwnerPolicyStateSignature{}
	if localevidence.SessionRoutingValueDigest(state) != localevidence.SessionRoutingValueDigest(expected) {
		return state, errors.New("cloud_session_transition_invalid: runtime input differs from the verified Owner journal")
	}
	return verified, nil
}

func (r *CloudSessionRuntime) verify(ctx context.Context, state localevidence.CloudSessionRouting, requireNew bool) error {
	source, err := localevidence.ReadCloudSessionFile(r.workspace, localevidence.CloudSessionSourcePath)
	if err != nil || localevidence.SessionRoutingDigest(source) != state.SourceDigest {
		return errors.New("cloud_session_transition_drift: source Compose changed")
	}
	expected, err := sessionStateOverlay(source, state)
	actual, readErr := localevidence.ReadCloudSessionFile(r.workspace, localevidence.CloudSessionOverlayPath)
	if err != nil || readErr != nil || !bytes.Equal(expected, actual) || localevidence.SessionRoutingDigest(actual) != state.OverlayDigest {
		return errors.New("cloud_session_transition_drift: overlay is not the canonical public route derivative")
	}
	observed, err := r.containers(ctx, state.Before, state.After)
	if err != nil || len(observed) != len(state.Containers) {
		return errors.New("cloud_session_transition_drift: exact retained service set unavailable")
	}
	sourceHashes, err := r.sessionConfigHashes(ctx, false)
	if err != nil {
		return err
	}
	overlayHashes, err := r.sessionConfigHashes(ctx, true)
	if err != nil {
		return err
	}
	for index, current := range observed {
		before := state.Containers[index]
		if current.Service != before.Service || !sessionImageAdmitted(state, current, before) || current.MountsDigest != before.MountsDigest {
			return errors.New("cloud_session_transition_drift: retained image, mounts or non-routing configuration changed")
		}
		if current.Service == "hub" || current.Service == "tinyauth" {
			if current.ConfigHash != sourceHashes[current.Service] && current.ConfigHash != overlayHashes[current.Service] {
				return errors.New("cloud_session_transition_drift: consumer differs from both authorized Compose models")
			}
		} else if current.ID != before.ID || current.ConfigHash != before.ConfigHash || current.RuntimeDigest != before.RuntimeDigest {
			return errors.New("cloud_session_transition_drift: a non-session service was replaced")
		}
	}
	if requireNew {
		return r.verifyPublicProjection(ctx, state.After, expected)
	}
	// An interrupted up can leave either consumer on either admitted config.
	// Every consumer is checked independently; an arbitrary third state denies.
	return r.verifyPublicProjectionEither(ctx, state, expected)
}

func (r *CloudSessionRuntime) containers(ctx context.Context, original, effective addressplan.SessionTopology) ([]localevidence.CloudSessionContainer, error) {
	listed, err := r.docker.GetContainersByLabel(ctx, "com.docker.compose.project=stackkit-cloud-core-standalone")
	if err != nil || len(listed) == 0 {
		return nil, errors.New("cloud_session_runtime_unavailable: retained project unavailable")
	}
	seen := map[string]bool{}
	result := []localevidence.CloudSessionContainer{}
	for _, item := range listed {
		container, err := r.docker.InspectContainer(ctx, item.ID)
		if err != nil || container == nil {
			return nil, errors.New("cloud_session_runtime_unavailable: retained service is not running")
		}
		service := container.Config.Labels["com.docker.compose.service"]
		if !container.State.Running && (original == effective || (service != "tinyauth" && service != "hub")) {
			return nil, errors.New("cloud_session_runtime_unavailable: retained non-session service is not running")
		}
		if service == "" || seen[service] {
			return nil, errors.New("cloud_session_transition_drift: duplicate or unowned service")
		}
		seen[service] = true
		mounts := append([]stackdocker.ContainerMount(nil), container.Mounts...)
		sort.Slice(mounts, func(i, j int) bool { return mounts[i].Destination < mounts[j].Destination })
		config := container.Config
		if strings.HasPrefix(container.ID, config.Hostname) && len(config.Hostname) >= 12 {
			config.Hostname = ""
		}
		config.Labels = cloneSessionLabels(config.Labels)
		for key := range config.Labels {
			if key == "com.docker.compose.config-hash" || key == "com.docker.compose.project.config_files" || strings.Contains(key, "stackkit-session-hub-redirect") {
				delete(config.Labels, key)
			}
		}
		if service == "tinyauth" {
			config.Labels["traefik.http.routers.tinyauth.rule"] = "Host(`" + original.TinyAuthHost + "`)"
			env := []string{}
			for _, line := range config.Env {
				key, _, _ := strings.Cut(line, "=")
				if sessionPublicEnvironmentKey(key) {
					continue
				}
				env = append(env, line)
			}
			config.Env = env
		}
		if service == "hub" {
			config.Labels["traefik.http.routers.hub.rule"] = "Host(`" + original.HubHost + "`)"
			config.Cmd = append([]string(nil), config.Cmd...)
			for i, arg := range config.Cmd {
				config.Cmd[i] = strings.NewReplacer(effective.TinyAuthHost, original.TinyAuthHost, effective.HubHost, original.HubHost).Replace(arg)
			}
		}
		sort.Strings(config.Env)
		result = append(result, localevidence.CloudSessionContainer{Service: service, ID: container.ID, ImageID: container.Image,
			ConfigHash: container.Config.Labels["com.docker.compose.config-hash"], MountsDigest: localevidence.SessionRoutingValueDigest(mounts),
			RoutingDigest: sessionPublicConfigDigest(container.Config, service),
			RuntimeDigest: localevidence.SessionRoutingValueDigest(struct {
				Config   stackdocker.ContainerConfig
				Host     stackdocker.ContainerHostConfig
				Networks stackdocker.ContainerNetworkSettings
			}{config, container.HostConfig, container.NetworkSettings})})
	}
	if !seen["hub"] || !seen["tinyauth"] || !seen["pocketid"] {
		return nil, errors.New("cloud_session_transition_drift: required core service absent")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Service < result[j].Service })
	return result, nil
}

func cloneSessionLabels(input map[string]string) map[string]string {
	output := map[string]string{}
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (o *osCloudCoreOperations) observeSessionProject(ctx context.Context, project CloudCoreProject, state localevidence.CloudSessionRouting) (observation CloudCoreVerifyObservation, returnErr error) {
	defer func() { returnErr = localevidence.CloudSessionRoutingDiagnostic(returnErr) }()
	if err := o.ready(ctx); err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	if o.name() != "cloud-core-standalone" || o.validateModule(project) != nil || state.Phase != "active" {
		return CloudCoreVerifyObservation{}, errors.New("cloud_session_transition_pending: explicitly resume the session route transition")
	}
	if localevidence.SessionRoutingDigest(project.Definition) != state.SourceDigest {
		return CloudCoreVerifyObservation{}, errors.New("cloud_session_upgrade_unsupported: this transition retains the original Compose artifact; Cloud release upgrades require the supported migration route")
	}
	if err := NewCloudSessionRuntime(o.workspaceRoot).Verify(ctx, state); err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	services, err := sessionComponentServices(project.Services, state.Migration)
	if err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	project.Services = services
	compose := filepath.Join(o.workspaceRoot, filepath.FromSlash(localevidence.CloudSessionSourcePath))
	observed, err := o.observeProject(ctx, project, compose)
	if err != nil {
		return observed, err
	}
	observed.SessionTransitionDigest = localevidence.SessionRoutingValueDigest(state)
	observed.SessionOverlayDigest = state.OverlayDigest
	observed.SessionComponentMigration = state.Migration
	return observed, nil
}

func (r *CloudSessionRuntime) verifyPublicProjection(ctx context.Context, topology addressplan.SessionTopology, overlay []byte) error {
	return r.verifyPublicConsumers(ctx, topology, topology, overlay, true, nil)
}
func (r *CloudSessionRuntime) verifyPublicProjectionEither(ctx context.Context, state localevidence.CloudSessionRouting, overlay []byte) error {
	return r.verifyPublicConsumers(ctx, state.Before, state.After, overlay, false, state.Containers)
}
func (r *CloudSessionRuntime) verifyPublicConsumers(ctx context.Context, before, after addressplan.SessionTopology, overlay []byte, requireNew bool, baseline []localevidence.CloudSessionContainer) error {
	var projection struct {
		Services map[string]struct {
			Labels      map[string]string `yaml:"labels"`
			Environment map[string]any    `yaml:"environment"`
			Command     []string          `yaml:"command"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(overlay, &projection) != nil {
		return errors.New("cloud_session_transition_invalid: public projection unreadable")
	}
	for _, service := range []string{"tinyauth", "hub"} {
		container, err := r.resolveSessionConsumer(ctx, service, requireNew)
		if err != nil || container == nil {
			return errors.New("cloud_session_runtime_unavailable: session consumer unavailable")
		}
		router, oldHost, newHost := "hub", before.HubHost, after.HubHost
		if service == "tinyauth" {
			router, oldHost, newHost = "tinyauth", before.TinyAuthHost, after.TinyAuthHost
		}
		rule := container.Config.Labels["traefik.http.routers."+router+".rule"]
		if !requireNew && rule == "Host(`"+oldHost+"`)" {
			matched := false
			for _, original := range baseline {
				if original.Service == service && sessionPublicConfigDigest(container.Config, service) == original.RoutingDigest {
					matched = true
				}
			}
			if !matched {
				return errors.New("cloud_session_transition_drift: original consumer public configuration changed")
			}
			continue
		}
		if rule != "Host(`"+newHost+"`)" {
			return errors.New("cloud_session_transition_drift: consumer route is neither original nor intended")
		}
		want := projection.Services[service]
		for key, value := range want.Labels {
			if container.Config.Labels[key] != strings.ReplaceAll(value, "$$", "$") {
				return errors.New("cloud_session_transition_drift: consumer labels differ")
			}
		}
		if service == "hub" && !slices.Equal(container.Config.Cmd, want.Command) {
			return errors.New("cloud_session_transition_drift: Hub command differs")
		}
		if service == "tinyauth" {
			env := map[string]string{}
			for _, line := range container.Config.Env {
				key, value, _ := strings.Cut(line, "=")
				env[key] = value
			}
			for key, value := range want.Environment {
				if value == nil {
					if _, exists := env[key]; exists {
						return errors.New("cloud_session_transition_drift: retired session ACL remains")
					}
					continue
				}
				if env[key] != value {
					return errors.New("cloud_session_transition_drift: session environment differs")
				}
			}
			for key := range env {
				if strings.HasPrefix(key, "TINYAUTH_APPS_ROUTE") && want.Environment[key] == nil {
					return errors.New("cloud_session_transition_drift: unapproved route ACL present")
				}
			}
		}
	}
	return nil
}

func (r *CloudSessionRuntime) resolveSessionConsumer(ctx context.Context, service string, healthy bool) (*stackdocker.ContainerInfo, error) {
	if healthy {
		return r.docker.ResolveComposeServiceContainer(ctx, "stackkit-cloud-core-standalone", service)
	}
	rows, err := r.docker.GetContainersByLabel(ctx, "com.docker.compose.project=stackkit-cloud-core-standalone")
	if err != nil {
		return nil, err
	}
	var selected *stackdocker.ContainerInfo
	for _, row := range rows {
		current, err := r.docker.InspectContainer(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		if current.Config.Labels["com.docker.compose.service"] != service {
			continue
		}
		if selected != nil {
			return nil, errors.New("cloud_session_transition_drift: duplicate session consumer")
		}
		selected = current
	}
	if selected == nil {
		return nil, errors.New("cloud_session_runtime_unavailable: session consumer absent")
	}
	return selected, nil
}

func sessionPublicConfigDigest(config stackdocker.ContainerConfig, service string) string {
	public := struct {
		Labels      map[string]string
		Environment []string
		Command     []string
	}{Labels: map[string]string{}}
	for key, value := range config.Labels {
		if key == "traefik.http.routers.hub.rule" || key == "traefik.http.routers.tinyauth.rule" || strings.Contains(key, "stackkit-session-hub-redirect") {
			public.Labels[key] = value
		}
	}
	if service == "hub" {
		public.Command = config.Cmd
	}
	if service == "tinyauth" {
		for _, line := range config.Env {
			key, _, _ := strings.Cut(line, "=")
			if sessionPublicEnvironmentKey(key) {
				public.Environment = append(public.Environment, line)
			}
		}
		sort.Strings(public.Environment)
	}
	return localevidence.SessionRoutingValueDigest(public)
}

func sessionPublicEnvironmentKey(key string) bool {
	return key == "TINYAUTH_APPURL" || key == "TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL" || key == "TINYAUTH_AUTH_ACLS_POLICY" || key == "TINYAUTH_LABELPROVIDER" || key == "TINYAUTH_OAUTH_PROVIDERS_POCKETID_WHITELIST" || strings.HasPrefix(key, "TINYAUTH_APPS_ROUTE")
}

func (r *CloudSessionRuntime) verifySourceConfigHashes(ctx context.Context, source []byte, containers []localevidence.CloudSessionContainer) error {
	var model struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(source, &model) != nil || len(model.Services) != len(containers) {
		return errors.New("cloud_session_transition_drift: retained Compose service set differs")
	}
	allowed := map[string]bool{"hub": true, "tinyauth": true, "pocketid": true, "router": true, "socket-proxy": true, "kopia-agent": true, "stackkit-server": true}
	for name := range model.Services {
		if !allowed[name] {
			return errors.New("cloud_session_transition_unsupported: non-core service requires its own qualified route migration")
		}
	}
	environment, err := localevidence.ComposeInterpolationEnvironment(r.workspace)
	if err != nil {
		return err
	}
	compose := filepath.Join(r.workspace, filepath.FromSlash(localevidence.CloudSessionSourcePath))
	// Compose's live hash is computed from the resolved model. Hashing the
	// source directly retains env_file metadata and differs for custody-backed
	// services even when their realized environments are byte-identical.
	resolved, err := (osCloudCoreProcessRunner{}).Run(ctx, []string{"compose", "--project-name", "stackkit-cloud-core-standalone", "-f", compose, "config", "--format", "json"}, filepath.Dir(compose), environment)
	if err != nil {
		return errors.New("cloud_session_runtime_unavailable: retained Compose configuration cannot be resolved")
	}
	defer clear(resolved)
	raw, err := hashResolvedCloudSessionConfig(ctx, filepath.Dir(compose), environment, resolved)
	if err != nil {
		return errors.New("cloud_session_runtime_unavailable: retained Compose configuration cannot be resolved")
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return errors.New("cloud_session_transition_drift: retained Compose hash readback malformed")
		}
		hashes[fields[0]] = fields[1]
	}
	clear(raw)
	for _, container := range containers {
		current, err := r.docker.InspectContainer(ctx, container.ID)
		if err != nil || current == nil || model.Services[container.Service].Image != current.Config.Image || hashes[container.Service] != container.ConfigHash {
			return errors.New("cloud_session_transition_drift: running services do not match the retained Compose source")
		}
	}
	return nil
}

func hashResolvedCloudSessionConfig(ctx context.Context, directory string, environment []string, resolved []byte) ([]byte, error) {
	if filepath.Base(directory) != "cloud-core-standalone" || len(resolved) == 0 || len(resolved) > 8<<20 {
		return nil, errors.New("Cloud session config hash requires its bounded retained runtime model")
	}
	command := exec.CommandContext(ctx, "docker", "compose", "--project-name", "stackkit-cloud-core-standalone", "-f", "-", "config", "--hash", "*") //nolint:gosec // exact read-only source configuration hash
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdin = bytes.NewReader(resolved)
	output, err := command.CombinedOutput()
	if err != nil {
		clear(output)
		return nil, errors.New("Cloud session resolved configuration hash unavailable")
	}
	return output, nil
}
