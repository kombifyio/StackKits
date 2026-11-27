package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// This read-only script never loads user code or writes configuration. It
// hashes regular configuration files and records preservation notices for
// custom integrations and external recorder settings without mutating /config.
const adoptionConfigProbe = `import hashlib,json,pathlib,re
p=pathlib.Path('/config')
has_custom=(p/'custom_components').exists()
has_ext_db=False
files=sorted(list(p.rglob('*.yaml'))+[p/'.storage'/n for n in ['core.config','core.config_entries','auth_provider.homeassistant'] if (p/'.storage'/n).exists()])
h=hashlib.sha256()
for f in files:
 if f.is_symlink() or not f.is_file(): raise RuntimeError('non-regular configuration unsupported')
 if f.stat().st_size>4194304: raise RuntimeError('configuration exceeds bound')
 b=f.read_bytes()
 if f.suffix=='.yaml' and re.search(rb'(?m)^[^#\n]*db_url\s*:',b): has_ext_db=True
 h.update(str(f.relative_to(p)).encode()); h.update(b'\0'); h.update(b)
a=p/'.storage/auth'
if not a.is_file() or a.is_symlink() or not (p/'configuration.yaml').is_file(): raise RuntimeError('existing initialized configuration required')
auth=json.loads(a.read_bytes())['data']
accounts=json.dumps({'users':auth['users'],'groups':auth['groups']},sort_keys=True,separators=(',',':')).encode()
owners=[u['id'] for u in auth['users'] if u.get('is_owner')]
if len(owners)!=1: raise RuntimeError('unique native owner required')
print(json.dumps({'configurationDigest':'sha256:'+h.hexdigest(),'accountsDigest':'sha256:'+hashlib.sha256(accounts).hexdigest(),'ownerRef':owners[0],'hasCustomComponents':has_custom,'hasExternalRecorder':has_ext_db}))`

var (
	admittedHomeAssistantRepos = map[string]struct{}{
		"ghcr.io/home-assistant/home-assistant": {},
		"homeassistant/home-assistant":          {},
		"lscr.io/linuxserver/homeassistant":     {},
	}
	benignAdoptionEnvKeys = map[string]struct{}{
		"TZ":               {},
		"LANG":             {},
		"LC_ALL":           {},
		"PUID":             {},
		"PGID":             {},
		"UMASK":            {},
		"DISABLE_JEMALLOC": {},
	}
	admittedSerialDevicePath = regexp.MustCompile(`^/dev/(ttyUSB[0-9]+|ttyACM[0-9]+|ttyAMA[0-9]+|hidraw[0-9]+|serial/by-id/[A-Za-z0-9._:@+-]+|serial/by-path/[A-Za-z0-9._:@+-]+)$`)
	forbiddenDevicePath      = regexp.MustCompile(`^/dev/(sd|nvme|hd|vd|xvd|mmcblk|loop|dm-|md|mem$|kmem$|port$|kmsg$|shm)`)
)

type adoptionDeviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type adoptionContainer struct {
	ID      string `json:"Id"`
	Image   string
	Created string
	Config  struct {
		Image       string
		Labels      map[string]string
		Env         []string
		Entrypoint  []string
		Cmd         []string
		WorkingDir  string
		User        string
		Healthcheck json.RawMessage
	}
	HostConfig struct {
		NetworkMode  string
		Privileged   bool
		Devices      []adoptionDeviceMapping
		Binds        []string
		PortBindings map[string][]struct{ HostIP, HostPort string }
		VolumesFrom  []string
	}
	Mounts []struct {
		Type, Source, Destination, Name string
		RW                              bool
	}
	State struct {
		Running   bool
		StartedAt time.Time
	}
}

type AdoptionObservation struct {
	Source         applicationlifecycle.AdoptionSource
	Endpoint       string
	Running        bool
	StartedAt      time.Time
	NativeOwnerRef string
}

func InspectApplicationAdoption(ctx context.Context, id string, contract applicationlifecycle.AdoptionContract, withData bool) (AdoptionObservation, error) {
	return inspectApplicationAdoption(ctx, osStandaloneComposeDockerCLI{}, id, contract, withData)
}

func inspectApplicationAdoption(ctx context.Context, docker standaloneComposeDockerCLI, id string, contract applicationlifecycle.AdoptionContract, withData bool) (AdoptionObservation, error) {
	if !validStandaloneComposeContainerID(id) || id != strings.ToLower(id) || contract.ProfileRef != applicationlifecycle.AdoptionProfile {
		return AdoptionObservation{}, errors.New("source_identity_conflict: exact supported container identity is required")
	}
	raw, err := docker.Run(ctx, []string{"inspect", id})
	if err != nil {
		return AdoptionObservation{}, err
	}
	var containers []adoptionContainer
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 || containers[0].ID != id {
		return AdoptionObservation{}, errors.New("source_identity_conflict: Docker inspection did not match the approved container")
	}
	c := containers[0]
	if !strings.HasPrefix(c.Image, "sha256:") || c.HostConfig.Privileged || len(c.HostConfig.VolumesFrom) > 0 || len(c.Mounts) != 1 || c.Mounts[0].Destination != "/config" || !c.Mounts[0].RW || (c.Mounts[0].Type != "bind" && c.Mounts[0].Type != "volume") {
		return AdoptionObservation{}, errors.New("unsupported_source: only one persistent /config mount without privilege or shared mounts is admitted")
	}
	preservedDevices, err := validateAdoptionDevices(c.HostConfig.Devices)
	if err != nil {
		return AdoptionObservation{}, err
	}
	project := c.Config.Labels["com.docker.compose.project"]
	composeService := c.Config.Labels["com.docker.compose.service"]
	sourceOrigin := "docker"
	if project != "" || composeService != "" {
		if !validStandaloneComposeVolumeName(project) || composeService == "" {
			return AdoptionObservation{}, errors.New("unsupported_source: invalid Compose project ownership labels")
		}
		sourceOrigin = "compose"
	}
	all, err := docker.Run(ctx, []string{"ps", "-aq", "--no-trunc"})
	if err != nil {
		return AdoptionObservation{}, err
	}
	for _, otherID := range strings.Fields(string(all)) {
		if otherID == id {
			continue
		}
		if !validStandaloneComposeContainerID(otherID) {
			return AdoptionObservation{}, errors.New("missing_preservation_evidence: container inventory is invalid")
		}
		otherRaw, err := docker.Run(ctx, []string{"inspect", otherID})
		if err != nil {
			return AdoptionObservation{}, err
		}
		var other []adoptionContainer
		if json.Unmarshal(otherRaw, &other) != nil || len(other) != 1 || other[0].ID != otherID {
			return AdoptionObservation{}, errors.New("missing_preservation_evidence: container dependency inventory changed")
		}
		for _, mount := range other[0].Mounts {
			if mount.Source == c.Mounts[0].Source || (mount.Type == "volume" && c.Mounts[0].Type == "volume" && mount.Name == c.Mounts[0].Name) {
				return AdoptionObservation{}, errors.New("unsupported_source: /config is shared with another container")
			}
		}
	}
	image, err := docker.Run(ctx, []string{"image", "inspect", c.Image})
	if err != nil {
		return AdoptionObservation{}, err
	}
	var images []struct {
		ID           string `json:"Id"`
		RepoDigests  []string
		Architecture string
		OS           string
		Config       struct {
			Env              []string
			Entrypoint, Cmd  []string
			WorkingDir, User string
			Healthcheck      json.RawMessage
		}
	}
	if json.Unmarshal(image, &images) != nil || len(images) != 1 || images[0].ID != c.Image || images[0].OS != "linux" || (images[0].Architecture != "amd64" && images[0].Architecture != "arm64") {
		return AdoptionObservation{}, errors.New("unsupported_source: source image architecture is not admitted")
	}
	matchedRepo, observedDigest, versionDrift, ok := matchOfficialHomeAssistantDigest(images[0].RepoDigests, contract.ImageRef, contract.ImageDigest)
	if !ok {
		return AdoptionObservation{}, errors.New("source_identity_conflict: image is not an admitted official Home Assistant release with an immutable RepoDigest")
	}
	endpoint := ""
	trusted := images[0].Config
	if !slices.Equal(c.Config.Entrypoint, trusted.Entrypoint) || !slices.Equal(c.Config.Cmd, trusted.Cmd) || c.Config.WorkingDir != trusted.WorkingDir || c.Config.User != trusted.User || string(c.Config.Healthcheck) != string(trusted.Healthcheck) {
		return AdoptionObservation{}, errors.New("unsupported_source: startup execution must match the trusted immutable image")
	}
	if !adoptionEnvAdmitted(c.Config.Env, trusted.Env) {
		return AdoptionObservation{}, errors.New("unsupported_source: startup environment differs from trusted image")
	}
	networkMode := c.HostConfig.NetworkMode
	if networkMode == "host" {
		endpoint = "http://127.0.0.1:8123"
	} else {
		if networkMode == "" {
			networkMode = "bridge"
		}
		ports := c.HostConfig.PortBindings["8123/tcp"]
		if len(ports) != 1 {
			return AdoptionObservation{}, errors.New("unsupported_source: one explicit local Home Assistant listener is required")
		}
		port, err := strconv.Atoi(ports[0].HostPort)
		if err != nil || port < 1 || port > 65535 || (ports[0].HostIP != "127.0.0.1" && ports[0].HostIP != "0.0.0.0" && ports[0].HostIP != "") {
			return AdoptionObservation{}, errors.New("unsupported_source: Home Assistant listener is not locally bound")
		}
		endpoint = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	// Runtime configuration includes environment only inside a one-way digest.
	runtimeRaw, err := resolvedplan.CanonicalJSON(struct {
		Image, Created string
		Config         any
		HostConfig     any
		Mounts         any
	}{c.Image, c.Created, c.Config, c.HostConfig, c.Mounts})
	if err != nil {
		return AdoptionObservation{}, err
	}
	h := sha256.Sum256(runtimeRaw)
	source := applicationlifecycle.AdoptionSource{
		ContainerID:      id,
		ImageID:          c.Image,
		ImageDigest:      observedDigest,
		Revision:         "sha256:" + hex.EncodeToString(h[:]),
		ConfigMount:      c.Mounts[0].Type + ":" + c.Mounts[0].Source,
		SourceOrigin:     sourceOrigin,
		NetworkMode:      networkMode,
		PreservedDevices: preservedDevices,
		VersionDrift:     versionDrift,
	}
	if versionDrift {
		source.ObservedImageDigest = observedDigest
	}
	nativeOwnerRef := ""
	if withData {
		if !c.State.Running {
			return AdoptionObservation{}, errors.New("source_unavailable: existing application must be running for adoption verification")
		}
		data, err := docker.Run(ctx, []string{"exec", id, "/usr/local/bin/python3", "-I", "-S", "-c", adoptionConfigProbe})
		if err != nil && matchedRepo == "lscr.io/linuxserver/homeassistant" {
			data, err = docker.Run(ctx, []string{"exec", id, "/usr/bin/python3", "-I", "-S", "-c", adoptionConfigProbe})
		}
		if err != nil {
			return AdoptionObservation{}, errors.New("missing_preservation_evidence: source configuration could not be inspected without changes")
		}
		var proof struct {
			ConfigurationDigest string `json:"configurationDigest"`
			AccountsDigest      string `json:"accountsDigest"`
			OwnerRef            string `json:"ownerRef"`
			HasCustomComponents bool   `json:"hasCustomComponents"`
			HasExternalRecorder bool   `json:"hasExternalRecorder"`
		}
		if json.Unmarshal(data, &proof) != nil || len(proof.ConfigurationDigest) != 71 || len(proof.AccountsDigest) != 71 {
			return AdoptionObservation{}, errors.New("missing_preservation_evidence: source configuration proof is invalid")
		}
		source.ConfigurationDigest, source.AccountsDigest = proof.ConfigurationDigest, proof.AccountsDigest
		source.HasCustomComponents, source.HasExternalRecorder = proof.HasCustomComponents, proof.HasExternalRecorder
		nativeOwnerRef = proof.OwnerRef
	}
	return AdoptionObservation{Source: source, Endpoint: endpoint, Running: c.State.Running, StartedAt: c.State.StartedAt, NativeOwnerRef: nativeOwnerRef}, nil
}

func validateAdoptionDevices(devices []adoptionDeviceMapping) ([]string, error) {
	if len(devices) == 0 {
		return nil, nil
	}
	preserved := make([]string, 0, len(devices))
	for _, dev := range devices {
		hostPath := strings.TrimSpace(dev.PathOnHost)
		if hostPath == "" || path.Clean(hostPath) != hostPath || forbiddenDevicePath.MatchString(hostPath) || !admittedSerialDevicePath.MatchString(hostPath) {
			return nil, errors.New("unsupported_source: only non-privileged USB/serial radio devices are admitted for in-place adoption")
		}
		containerPath := strings.TrimSpace(dev.PathInContainer)
		if containerPath != "" {
			if path.Clean(containerPath) != containerPath || !strings.HasPrefix(containerPath, "/dev/") || strings.Contains(containerPath, "..") || forbiddenDevicePath.MatchString(containerPath) {
				return nil, errors.New("unsupported_source: container device mapping target is not admitted for in-place adoption")
			}
		}
		preserved = append(preserved, hostPath)
	}
	slices.Sort(preserved)
	return slices.Compact(preserved), nil
}

func normalizeOfficialHomeAssistantRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	if trimmed, ok := strings.CutPrefix(repo, "docker.io/"); ok {
		return trimmed
	}
	if trimmed, ok := strings.CutPrefix(repo, "index.docker.io/"); ok {
		return trimmed
	}
	return repo
}

func matchOfficialHomeAssistantDigest(repoDigests []string, contractImageRef, contractDigest string) (repo, digest string, versionDrift, ok bool) {
	wantRepo := normalizeOfficialHomeAssistantRepo(strings.Split(contractImageRef, ":")[0])
	var fallbackRepo, fallbackDigest string
	for _, entry := range repoDigests {
		r, d, hasAt := strings.Cut(entry, "@")
		if !hasAt || !strings.HasPrefix(d, "sha256:") || !validStandaloneComposeContainerID(strings.TrimPrefix(d, "sha256:")) {
			continue
		}
		normRepo := normalizeOfficialHomeAssistantRepo(r)
		if _, admitted := admittedHomeAssistantRepos[normRepo]; !admitted {
			continue
		}
		if normRepo == wantRepo && d == contractDigest {
			return normRepo, d, false, true
		}
		if fallbackDigest == "" {
			fallbackRepo, fallbackDigest = normRepo, d
		}
	}
	if fallbackDigest != "" {
		return fallbackRepo, fallbackDigest, fallbackDigest != contractDigest, true
	}
	return "", "", false, false
}

func adoptionEnvAdmitted(sourceEnv, trustedEnv []string) bool {
	filteredSource := filterNonBenignAdoptionEnv(sourceEnv)
	filteredTrusted := filterNonBenignAdoptionEnv(trustedEnv)
	slices.Sort(filteredSource)
	slices.Sort(filteredTrusted)
	return slices.Equal(filteredSource, filteredTrusted)
}

func filterNonBenignAdoptionEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if key, _, hasEq := strings.Cut(entry, "="); hasEq {
			if _, benign := benignAdoptionEnvKeys[key]; benign {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// ControlAdoptedApplication extends the same bounded Docker process boundary
// used by standalone Compose; it never renders, creates or removes a container.
func ControlAdoptedApplication(ctx context.Context, contract applicationlifecycle.AdoptionContract, source applicationlifecycle.AdoptionSource, action string) (AdoptionObservation, error) {
	return controlAdoptedApplication(ctx, osStandaloneComposeDockerCLI{}, contract, source, action)
}

func controlAdoptedApplication(ctx context.Context, docker standaloneComposeDockerCLI, contract applicationlifecycle.AdoptionContract, source applicationlifecycle.AdoptionSource, action string) (AdoptionObservation, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return AdoptionObservation{}, errors.New("unsupported application control action")
	}
	current, err := inspectApplicationAdoption(ctx, docker, source.ContainerID, contract, false)
	if err != nil {
		return AdoptionObservation{}, err
	}
	if current.Source.Revision != source.Revision || current.Source.ImageID != source.ImageID {
		return AdoptionObservation{}, errors.New("stale_revision: adopted runtime configuration changed; replan before control")
	}
	if _, err := docker.Run(ctx, []string{action, source.ContainerID}); err != nil {
		return AdoptionObservation{}, err
	}
	observed, err := inspectApplicationAdoption(ctx, docker, source.ContainerID, contract, false)
	if err != nil {
		return AdoptionObservation{}, err
	}
	if observed.Source.Revision != source.Revision || observed.Running != (action != "stop") {
		return AdoptionObservation{}, fmt.Errorf("application control did not verify expected unchanged runtime and power state")
	}
	return observed, nil
}
