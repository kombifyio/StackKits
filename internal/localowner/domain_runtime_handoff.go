package localowner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	stackdocker "github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"gopkg.in/yaml.v3"
)

const (
	domainRuntimeProject = "stackkit-basement-core"
	domainRuntimeDir     = ".stackkit/runtime/basement-core"
	domainRuntimeCompose = domainRuntimeDir + "/compose.yaml"
	domainRuntimeGrace   = 30 * time.Second
)

var domainContainerIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type domainRuntimeMount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"readWrite"`
}

type domainRuntimeContainer struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Service     string               `json:"service"`
	ConfigHash  string               `json:"configHash"`
	ImageID     string               `json:"imageId"`
	SourceImage string               `json:"sourceImage"`
	LongLived   bool                 `json:"longLived"`
	WasRunning  bool                 `json:"wasRunning"`
	DependsOn   []string             `json:"dependsOn,omitempty"`
	Mounts      []domainRuntimeMount `json:"mounts"`
}

type domainRuntimeHandoff struct {
	Phase         string                   `json:"phase"`
	SourceDomain  string                   `json:"sourceDomain"`
	Project       string                   `json:"project"`
	RuntimeDir    string                   `json:"runtimeDir"`
	ComposeSHA256 string                   `json:"composeSha256"`
	Containers    []domainRuntimeContainer `json:"containers"`
}

type domainRuntimeHandoffRuntime interface {
	Capture(context.Context, string) (domainRuntimeHandoff, error)
	VerifySource(context.Context, string, domainRuntimeHandoff) error
	Quiesce(context.Context, string, domainRuntimeHandoff) error
}

type domainRuntimeDocker interface {
	GetContainersByLabel(context.Context, string) ([]stackdocker.ContainerInfo, error)
	InspectContainer(context.Context, string) (*stackdocker.ContainerInfo, error)
	StopContainerWithTimeout(context.Context, string, time.Duration) error
	RemoveStoppedContainer(context.Context, string) error
}

type domainRuntimeComposeResolver interface {
	Resolve(context.Context, string) (map[string]domainResolvedService, string, error)
}

type domainRuntimeHost struct {
	docker  domainRuntimeDocker
	compose domainRuntimeComposeResolver
}

type domainResolvedService struct {
	Image      string
	ConfigHash string
	LongLived  bool
	DependsOn  []string
}

type osDomainRuntimeComposeResolver struct{}

func newDomainRuntimeHost() domainRuntimeHandoffRuntime {
	return &domainRuntimeHost{
		docker:  stackdocker.NewLocalClient(stackdocker.WithTimeout(45 * time.Second)),
		compose: osDomainRuntimeComposeResolver{},
	}
}

func (h *domainRuntimeHost) Capture(ctx context.Context, workspace string) (domainRuntimeHandoff, error) {
	result := domainRuntimeHandoff{
		Phase: "captured", SourceDomain: localevidence.LegacyBasementDomain,
		Project: domainRuntimeProject, RuntimeDir: domainRuntimeDir,
	}
	if h == nil || h.docker == nil || h.compose == nil {
		return result, errors.New("localowner: domain runtime handoff is unavailable")
	}
	services, composeDigest, err := h.compose.Resolve(ctx, workspace)
	if err != nil {
		return result, err
	}
	containers, err := h.projectContainers(ctx)
	if err != nil {
		return result, err
	}
	if len(containers) != len(services) || len(containers) == 0 {
		return result, errors.New("localowner: source Core runtime does not contain its exact Compose service set")
	}
	seen := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		service := container.Config.Labels["com.docker.compose.service"]
		resolved, ok := services[service]
		if !ok {
			return result, errors.New("localowner: source Core runtime contains an ungoverned project container")
		}
		if _, duplicate := seen[service]; duplicate {
			return result, errors.New("localowner: source Core runtime contains duplicate service containers")
		}
		seen[service] = struct{}{}
		record, err := captureDomainRuntimeContainer(workspace, container, resolved)
		if err != nil {
			return result, err
		}
		result.Containers = append(result.Containers, record)
	}
	for service := range services {
		if _, ok := seen[service]; !ok {
			return result, errors.New("localowner: source Core runtime is missing a governed service container")
		}
	}
	sort.Slice(result.Containers, func(i, j int) bool { return result.Containers[i].Service < result.Containers[j].Service })
	if _, err := domainRuntimeStopOrder(result.Containers); err != nil {
		return result, err
	}
	result.ComposeSHA256 = composeDigest
	return result, nil
}

func (h *domainRuntimeHost) VerifySource(ctx context.Context, workspace string, captured domainRuntimeHandoff) error {
	if h == nil || h.docker == nil {
		return errors.New("localowner: domain runtime handoff is unavailable")
	}
	if err := validateDomainRuntimeHandoff(captured); err != nil {
		return err
	}
	if err := verifyDomainRuntimeCompose(workspace, captured.ComposeSHA256); err != nil {
		return err
	}
	current, err := h.projectContainers(ctx)
	if err != nil {
		return err
	}
	if len(current) != len(captured.Containers) {
		return errors.New("localowner: captured source Core runtime changed before migration")
	}
	byID := make(map[string]stackdocker.ContainerInfo, len(current))
	for _, container := range current {
		byID[container.ID] = container
	}
	for _, record := range captured.Containers {
		container, ok := byID[record.ID]
		if !ok || container.State.Running != record.WasRunning || !domainContainerMatches(workspace, container, record) {
			return errors.New("localowner: captured source Core runtime changed before migration")
		}
	}
	return nil
}

// Quiesce resumes only after the caller has durably signed the quiescing
// phase. Missing captured IDs are then an allowed result of our own earlier
// remove attempt; any new project ID remains foreign and is never touched.
func (h *domainRuntimeHost) Quiesce(ctx context.Context, workspace string, captured domainRuntimeHandoff) error {
	if h == nil || h.docker == nil {
		return errors.New("localowner: domain runtime handoff is unavailable")
	}
	if err := validateDomainRuntimeHandoff(captured); err != nil {
		return err
	}
	if err := verifyDomainRuntimeCompose(workspace, captured.ComposeSHA256); err != nil {
		return err
	}
	current, err := h.projectContainers(ctx)
	if err != nil {
		return err
	}
	capturedByID := make(map[string]domainRuntimeContainer, len(captured.Containers))
	for _, record := range captured.Containers {
		capturedByID[record.ID] = record
	}
	currentByID := make(map[string]stackdocker.ContainerInfo, len(current))
	for _, container := range current {
		record, ok := capturedByID[container.ID]
		if !ok || !domainContainerMatches(workspace, container, record) {
			return errors.New("localowner: source Core project changed after runtime handoff capture")
		}
		currentByID[container.ID] = container
	}
	// Stop every still-running captured identity before removing any of them.
	// The signed Compose dependency graph keeps dependencies available until
	// every service that names them has received its graceful signal.
	stopOrder, err := domainRuntimeStopOrder(captured.Containers)
	if err != nil {
		return err
	}
	for _, record := range stopOrder {
		container, ok := currentByID[record.ID]
		if !ok || !container.State.Running {
			continue
		}
		if err := h.docker.StopContainerWithTimeout(ctx, record.ID, domainRuntimeGrace); err != nil {
			return fmt.Errorf("localowner: gracefully stop captured Core service %s: %w", record.Service, err)
		}
	}
	stopped, err := h.projectContainers(ctx)
	if err != nil {
		return err
	}
	for _, container := range stopped {
		record, ok := capturedByID[container.ID]
		if !ok || container.State.Running || !domainContainerMatches(workspace, container, record) {
			return errors.New("localowner: captured Core runtime did not reach an exact stopped state")
		}
	}
	for _, record := range stopOrder {
		if !slices.ContainsFunc(stopped, func(container stackdocker.ContainerInfo) bool { return container.ID == record.ID }) {
			continue
		}
		if err := h.docker.RemoveStoppedContainer(ctx, record.ID); err != nil {
			return fmt.Errorf("localowner: remove captured Core service %s: %w", record.Service, err)
		}
	}
	remaining, err := h.projectContainers(ctx)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return errors.New("localowner: captured Core project still has containers after quiescence")
	}
	return nil
}

func (h *domainRuntimeHost) projectContainers(ctx context.Context) ([]stackdocker.ContainerInfo, error) {
	summaries, err := h.docker.GetContainersByLabel(ctx, "com.docker.compose.project="+domainRuntimeProject)
	if err != nil {
		return nil, errors.New("localowner: list source Core project containers")
	}
	result := make([]stackdocker.ContainerInfo, 0, len(summaries))
	seen := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		container, err := h.docker.InspectContainer(ctx, summary.ID)
		if err != nil || container == nil || !domainContainerIDPattern.MatchString(container.ID) {
			return nil, errors.New("localowner: inspect source Core project container")
		}
		if _, duplicate := seen[container.ID]; duplicate {
			return nil, errors.New("localowner: duplicate source Core project container identity")
		}
		seen[container.ID] = struct{}{}
		result = append(result, *container)
	}
	return result, nil
}

func captureDomainRuntimeContainer(workspace string, container stackdocker.ContainerInfo, service domainResolvedService) (domainRuntimeContainer, error) {
	record := domainRuntimeContainer{
		ID: container.ID, Name: container.Name,
		Service:    container.Config.Labels["com.docker.compose.service"],
		ConfigHash: container.Config.Labels["com.docker.compose.config-hash"],
		ImageID:    container.Image, SourceImage: container.Config.Image,
		LongLived: service.LongLived, WasRunning: container.State.Running,
		DependsOn: append([]string(nil), service.DependsOn...),
		Mounts:    normalizedDomainRuntimeMounts(container.Mounts),
	}
	if service.Image == "" || record.SourceImage != service.Image || record.ConfigHash != service.ConfigHash ||
		(service.LongLived && !record.WasRunning) || !domainContainerMatches(workspace, container, record) {
		return domainRuntimeContainer{}, errors.New("localowner: source Core container differs from its governed Compose service")
	}
	return record, nil
}

func domainContainerMatches(workspace string, container stackdocker.ContainerInfo, record domainRuntimeContainer) bool {
	runtimeDir := filepath.Join(workspace, filepath.FromSlash(domainRuntimeDir))
	labels := container.Config.Labels
	return domainContainerIDPattern.MatchString(record.ID) && container.ID == record.ID &&
		container.Name == record.Name && labels["com.docker.compose.project"] == domainRuntimeProject &&
		labels["com.docker.compose.service"] == record.Service &&
		filepath.Clean(labels["com.docker.compose.project.working_dir"]) == runtimeDir &&
		filepath.Clean(labels["com.docker.compose.project.config_files"]) == filepath.Join(runtimeDir, "compose.yaml") &&
		labels["com.docker.compose.config-hash"] == record.ConfigHash && record.ConfigHash != "" &&
		container.Image == record.ImageID && container.Config.Image == record.SourceImage &&
		slices.Equal(normalizedDomainRuntimeMounts(container.Mounts), record.Mounts)
}

func normalizedDomainRuntimeMounts(mounts []stackdocker.ContainerMount) []domainRuntimeMount {
	result := make([]domainRuntimeMount, len(mounts))
	for index, mount := range mounts {
		result[index] = domainRuntimeMount{
			Type: mount.Type, Name: mount.Name, Source: mount.Source,
			Destination: mount.Destination, ReadWrite: mount.RW,
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Destination + "\x00" + result[i].Type + "\x00" + result[i].Source + "\x00" + result[i].Name
		right := result[j].Destination + "\x00" + result[j].Type + "\x00" + result[j].Source + "\x00" + result[j].Name
		return left < right
	})
	return result
}

func validateDomainRuntimeHandoff(handoff domainRuntimeHandoff) error {
	if handoff.SourceDomain != localevidence.LegacyBasementDomain || handoff.Project != domainRuntimeProject ||
		handoff.RuntimeDir != domainRuntimeDir || len(handoff.ComposeSHA256) != 64 || len(handoff.Containers) == 0 {
		return errors.New("localowner: signed source runtime handoff is incomplete")
	}
	if _, err := hex.DecodeString(handoff.ComposeSHA256); err != nil {
		return errors.New("localowner: signed source runtime handoff is incomplete")
	}
	if handoff.Phase != "captured" && handoff.Phase != "quiescing" && handoff.Phase != "quiesced" {
		return errors.New("localowner: signed source runtime handoff phase is invalid")
	}
	previous := ""
	seen := make(map[string]struct{}, len(handoff.Containers))
	for _, container := range handoff.Containers {
		if container.Service <= previous || !domainContainerIDPattern.MatchString(container.ID) || len(container.ConfigHash) != 64 ||
			container.ImageID == "" || container.SourceImage == "" || (container.LongLived && !container.WasRunning) {
			return errors.New("localowner: signed source runtime container evidence is invalid")
		}
		if _, err := hex.DecodeString(container.ConfigHash); err != nil {
			return errors.New("localowner: signed source runtime container evidence is invalid")
		}
		if _, duplicate := seen[container.ID]; duplicate {
			return errors.New("localowner: signed source runtime contains duplicate containers")
		}
		seen[container.ID] = struct{}{}
		previous = container.Service
	}
	if _, err := domainRuntimeStopOrder(handoff.Containers); err != nil {
		return err
	}
	return nil
}

func domainRuntimeStopOrder(containers []domainRuntimeContainer) ([]domainRuntimeContainer, error) {
	byService := make(map[string]domainRuntimeContainer, len(containers))
	remaining := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		if _, exists := byService[container.Service]; exists {
			return nil, errors.New("localowner: signed source runtime contains duplicate services")
		}
		if !slices.IsSorted(container.DependsOn) || len(slices.Compact(append([]string(nil), container.DependsOn...))) != len(container.DependsOn) {
			return nil, errors.New("localowner: signed source runtime dependency evidence is invalid")
		}
		byService[container.Service] = container
		remaining[container.Service] = struct{}{}
	}
	for service, container := range byService {
		for _, dependency := range container.DependsOn {
			if dependency == service {
				return nil, errors.New("localowner: signed source runtime dependency evidence is invalid")
			}
			if _, ok := byService[dependency]; !ok {
				return nil, errors.New("localowner: signed source runtime dependency is outside the captured project")
			}
		}
	}
	result := make([]domainRuntimeContainer, 0, len(containers))
	for len(remaining) > 0 {
		candidates := make([]string, 0, len(remaining))
		for service := range remaining {
			hasRemainingDependent := false
			for other := range remaining {
				if other != service && slices.Contains(byService[other].DependsOn, service) {
					hasRemainingDependent = true
					break
				}
			}
			if !hasRemainingDependent {
				candidates = append(candidates, service)
			}
		}
		if len(candidates) == 0 {
			return nil, errors.New("localowner: signed source runtime dependency graph contains a cycle")
		}
		sort.Strings(candidates)
		for _, service := range candidates {
			result = append(result, byService[service])
			delete(remaining, service)
		}
	}
	return result, nil
}

func verifyDomainRuntimeCompose(workspace, expected string) error {
	path := filepath.Join(workspace, filepath.FromSlash(domainRuntimeCompose))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 8<<20 {
		return errors.New("localowner: captured source Core Compose file is unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("localowner: read captured source Core Compose file")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expected {
		return errors.New("localowner: captured source Core Compose file changed")
	}
	return nil
}

func (osDomainRuntimeComposeResolver) Resolve(ctx context.Context, workspace string) (map[string]domainResolvedService, string, error) {
	runtimeDir := filepath.Join(workspace, filepath.FromSlash(domainRuntimeDir))
	composePath := filepath.Join(workspace, filepath.FromSlash(domainRuntimeCompose))
	info, err := os.Lstat(composePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 8<<20 {
		return nil, "", errors.New("localowner: source Core Compose file is unavailable")
	}
	raw, err := os.ReadFile(composePath)
	if err != nil {
		return nil, "", errors.New("localowner: read source Core Compose file")
	}
	digest := sha256.Sum256(raw)
	interpolation, err := localevidence.ComposeInterpolationEnvironment(workspace)
	if err != nil {
		return nil, "", err
	}
	resolved, err := runLocalDockerCompose(ctx, runtimeDir, interpolation, nil,
		"compose", "--project-name", domainRuntimeProject, "-f", composePath, "config")
	if err != nil {
		return nil, "", err
	}
	defer clear(resolved)
	var model struct {
		Services map[string]struct {
			Image     string    `yaml:"image"`
			Restart   string    `yaml:"restart"`
			DependsOn yaml.Node `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(resolved, &model) != nil || len(model.Services) == 0 {
		return nil, "", errors.New("localowner: resolved source Core Compose model is invalid")
	}
	result := make(map[string]domainResolvedService, len(model.Services))
	for name, service := range model.Services {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(service.Image) == "" {
			return nil, "", errors.New("localowner: source Core Compose service identity is incomplete")
		}
		hashOutput, err := runLocalDockerCompose(ctx, runtimeDir, interpolation, resolved,
			"compose", "--project-name", domainRuntimeProject, "-f", "-", "config", "--hash", name)
		if err != nil {
			return nil, "", err
		}
		fields := strings.Fields(string(hashOutput))
		clear(hashOutput)
		if len(fields) != 2 || fields[0] != name || len(fields[1]) != 64 {
			return nil, "", errors.New("localowner: source Core Compose service hash is invalid")
		}
		if _, err := hex.DecodeString(fields[1]); err != nil {
			return nil, "", errors.New("localowner: source Core Compose service hash is invalid")
		}
		restart := strings.ToLower(strings.TrimSpace(service.Restart))
		dependencies, err := resolvedDomainDependencies(service.DependsOn)
		if err != nil {
			return nil, "", err
		}
		result[name] = domainResolvedService{
			Image: service.Image, ConfigHash: fields[1],
			LongLived: restart == "always" || restart == "unless-stopped" || strings.HasPrefix(restart, "on-failure"),
			DependsOn: dependencies,
		}
	}
	for _, service := range result {
		for _, dependency := range service.DependsOn {
			if _, ok := result[dependency]; !ok {
				return nil, "", errors.New("localowner: source Core Compose dependency is incomplete")
			}
		}
	}
	return result, hex.EncodeToString(digest[:]), nil
}

func resolvedDomainDependencies(node yaml.Node) ([]string, error) {
	var result []string
	switch node.Kind {
	case 0:
		return nil, nil
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode || strings.TrimSpace(item.Value) == "" {
				return nil, errors.New("localowner: source Core Compose dependency is invalid")
			}
			result = append(result, item.Value)
		}
	case yaml.MappingNode:
		for index := 0; index < len(node.Content); index += 2 {
			if index+1 >= len(node.Content) || node.Content[index].Kind != yaml.ScalarNode || strings.TrimSpace(node.Content[index].Value) == "" {
				return nil, errors.New("localowner: source Core Compose dependency is invalid")
			}
			result = append(result, node.Content[index].Value)
		}
	default:
		return nil, errors.New("localowner: source Core Compose dependency is invalid")
	}
	sort.Strings(result)
	if len(slices.Compact(append([]string(nil), result...))) != len(result) {
		return nil, errors.New("localowner: source Core Compose dependency is invalid")
	}
	return result, nil
}

func runLocalDockerCompose(ctx context.Context, directory string, interpolation []string, input []byte, args ...string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = "npipe:////./pipe/docker_engine"
	}
	command := exec.CommandContext(bounded, "docker", append([]string{"--host", endpoint}, args...)...) //nolint:gosec // fixed local binary, endpoint, and validated service names
	command.Dir = directory
	command.Env = domainRuntimeDockerEnvironment(interpolation)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.Output()
	if err != nil || bounded.Err() != nil || len(output) == 0 || len(output) > 8<<20 {
		clear(output)
		return nil, errors.New("localowner: resolve source Core Compose model")
	}
	return output, nil
}

func domainRuntimeDockerEnvironment(interpolation []string) []string {
	blocked := map[string]struct{}{
		"DOCKER_HOST": {}, "DOCKER_CONTEXT": {}, "DOCKER_TLS": {}, "DOCKER_TLS_VERIFY": {},
		"DOCKER_CERT_PATH": {}, "DOCKER_SSH_COMMAND": {}, "DOCKER_SSH_CONFIG": {},
	}
	result := make([]string, 0, len(os.Environ())+len(interpolation))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, forbidden := blocked[strings.ToUpper(key)]; !forbidden {
			result = append(result, entry)
		}
	}
	return append(result, interpolation...)
}
