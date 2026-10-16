// Package docker wraps the docker CLI calls this tool needs.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/run"
)

// Volume is the part of `docker volume inspect` we keep.
type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	Labels     map[string]string `json:"Labels"`
	Options    map[string]string `json:"Options"`
}

// InspectVolume returns volume details, or (nil, nil) if it does not exist.
func InspectVolume(ctx context.Context, name string) (*Volume, error) {
	out, err := run.Output(ctx, "docker", "volume", "inspect", "--format", "{{json .}}", name)
	if err != nil {
		if strings.Contains(err.Error(), "no such volume") || strings.Contains(err.Error(), "No such volume") {
			return nil, nil
		}
		return nil, err
	}
	var v Volume
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// VolumeNames lists all volume names.
func VolumeNames(ctx context.Context) ([]string, error) {
	out, err := run.Text(ctx, "docker", "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// CreateVolume creates a local volume with the given labels / driver options.
func CreateVolume(ctx context.Context, v Volume) error {
	args := []string{"volume", "create"}
	if v.Driver != "" {
		args = append(args, "--driver", v.Driver)
	}
	for _, k := range sortedKeys(v.Labels) {
		args = append(args, "--label", k+"="+v.Labels[k])
	}
	for _, k := range sortedKeys(v.Options) {
		args = append(args, "--opt", k+"="+v.Options[k])
	}
	args = append(args, v.Name)
	_, err := run.Output(ctx, "docker", args...)
	return err
}

// RemoveVolume deletes a volume.
func RemoveVolume(ctx context.Context, name string) error {
	_, err := run.Output(ctx, "docker", "volume", "rm", name)
	return err
}

// Container is the part of `docker ps` we use.
type Container struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Labels string `json:"Labels"`
}

// Label returns one label value.
func (c Container) Label(key string) string {
	for _, kv := range strings.Split(c.Labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Containers runs docker ps -a with the given filters.
func Containers(ctx context.Context, filters ...string) ([]Container, error) {
	args := []string{"ps", "-a", "--no-trunc", "--format", "{{json .}}"}
	for _, f := range filters {
		args = append(args, "--filter", f)
	}
	out, err := run.Text(ctx, "docker", args...)
	if err != nil {
		return nil, err
	}
	var cs []Container
	for _, l := range lines(out) {
		var c Container
		if err := json.Unmarshal([]byte(l), &c); err == nil {
			cs = append(cs, c)
		}
	}
	return cs, nil
}

// RunningUsing returns running containers that mount the given volume name.
func RunningUsing(ctx context.Context, volume string) ([]Container, error) {
	cs, err := Containers(ctx, "volume="+volume, "status=running")
	if err != nil {
		return nil, err
	}
	return cs, nil
}

// Mount is one mount of a container.
type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// Mounts returns the mounts of a container.
func Mounts(ctx context.Context, id string) ([]Mount, error) {
	out, err := run.Output(ctx, "docker", "inspect", "--format", "{{json .Mounts}}", id)
	if err != nil {
		return nil, err
	}
	var ms []Mount
	err = json.Unmarshal(out, &ms)
	return ms, err
}

// Image returns the image reference a container was created from.
func Image(ctx context.Context, id string) (string, error) {
	return run.Text(ctx, "docker", "inspect", "--format", "{{.Config.Image}}", id)
}

// Pause freezes containers.
func Pause(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := run.Output(ctx, "docker", append([]string{"pause"}, ids...)...)
	return err
}

// Unpause resumes containers.
func Unpause(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := run.Output(ctx, "docker", append([]string{"unpause"}, ids...)...)
	return err
}

// Stop stops containers.
func Stop(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := run.Output(ctx, "docker", append([]string{"stop", "-t", "60"}, ids...)...)
	return err
}

// Start starts containers.
func Start(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := run.Output(ctx, "docker", append([]string{"start"}, ids...)...)
	return err
}

// ImageSize returns the size of a local image, or -1 if it does not exist.
func ImageSize(ctx context.Context, ref string) int64 {
	out, err := run.Text(ctx, "docker", "image", "inspect", "--format", "{{.Size}}", ref)
	if err != nil {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// Save streams `docker save` of the given images to w.
func Save(ctx context.Context, refs []string, w io.Writer) error {
	_, err := run.Do(ctx, run.Spec{Name: "docker", Args: append([]string{"save"}, refs...), Stdout: w})
	return err
}

// Load runs `docker load` reading an image tarball from r.
func Load(ctx context.Context, r io.Reader) error {
	_, err := run.Do(ctx, run.Spec{Name: "docker", Args: []string{"load", "-q"}, Stdin: r})
	return err
}

// Tag adds a tag to an image.
func Tag(ctx context.Context, src, dst string) error {
	_, err := run.Output(ctx, "docker", "tag", src, dst)
	return err
}

// NetworkExists reports whether a docker network exists.
func NetworkExists(ctx context.Context, name string) bool {
	_, err := run.Output(ctx, "docker", "network", "inspect", "--format", "{{.Name}}", name)
	return err == nil
}

// EnsureNetwork creates an attachable bridge network when missing.
func EnsureNetwork(ctx context.Context, name string) error {
	if NetworkExists(ctx, name) {
		return nil
	}
	_, err := run.Output(ctx, "docker", "network", "create", "--attachable", name)
	return err
}

// Connect attaches a container to a network (no error if already attached).
func Connect(ctx context.Context, network, container string) error {
	_, err := run.Output(ctx, "docker", "network", "connect", network, container)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// Remove force-removes containers.
func Remove(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := run.Output(ctx, "docker", append([]string{"rm", "-f"}, ids...)...)
	return err
}

// State reports whether a container is running and whether it is paused.
func State(ctx context.Context, id string) (running, paused bool) {
	out, err := run.Text(ctx, "docker", "inspect", "--format", "{{.State.Running}} {{.State.Paused}}", id)
	if err != nil {
		return false, false
	}
	f := strings.Fields(out)
	return len(f) == 2 && f[0] == "true", len(f) == 2 && f[1] == "true"
}

// infraNames are Coolify's own containers (never paused or stopped by backups).
var infraNames = map[string]bool{"coolify": true, "coolify-db": true, "coolify-redis": true,
	"coolify-realtime": true, "coolify-proxy": true, "coolify-proxy-logrotate": true, "coolify-sentinel": true}

// BindUsers returns running, non-infrastructure containers that bind-mount
// path, a directory inside it, or a directory containing it.
func BindUsers(ctx context.Context, path string) ([]string, error) {
	cs, err := Containers(ctx, "status=running")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range cs {
		if infraNames[strings.TrimPrefix(c.Names, "/")] || c.Label("coolify-mirror.share") != "" {
			continue
		}
		ms, err := Mounts(ctx, c.ID)
		if err != nil {
			continue
		}
		for _, m := range ms {
			if m.Type != "bind" || m.Source == "" {
				continue
			}
			if m.Source == path || strings.HasPrefix(m.Source, path+"/") || strings.HasPrefix(path, m.Source+"/") {
				out = append(out, c.ID)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// IsInfra reports whether a container name belongs to Coolify itself.
func IsInfra(name string) bool { return infraNames[strings.TrimPrefix(name, "/")] }

// Health returns State.Health.Status ("" when the container has no healthcheck).
func Health(ctx context.Context, id string) string {
	out, _ := run.Text(ctx, "docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{end}}", id)
	return out
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Describe renders a compact list of container names for messages.
func Describe(cs []Container) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Names
	}
	return fmt.Sprint(strings.Join(names, ", "))
}

// Details is the part of `docker inspect` used to judge a container's health.
type Details struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

// HealthStatus returns the healthcheck state ("" without a healthcheck).
func (d Details) HealthStatus() string {
	if d.State.Health == nil {
		return ""
	}
	return d.State.Health.Status
}

// Networks lists the networks the container is attached to.
func (d Details) Networks() []string {
	return sortedKeysRaw(d.NetworkSettings.Networks)
}

// Inspect returns the details of containers (missing ones are left out).
func Inspect(ctx context.Context, ids ...string) ([]Details, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := run.Output(ctx, "docker", append([]string{"inspect", "--type", "container"}, ids...)...)
	var ds []Details
	if jerr := json.Unmarshal(out, &ds); jerr != nil {
		if err != nil {
			return nil, err
		}
		return nil, jerr
	}
	return ds, nil
}

// Images lists local image references (repository:tag).
func Images(ctx context.Context) ([]string, error) {
	out, err := run.Text(ctx, "docker", "images", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// BuildxAvailable reports whether the docker buildx plugin is installed.
func BuildxAvailable(ctx context.Context) bool {
	_, err := run.Output(ctx, "docker", "buildx", "version")
	return err == nil
}

// BootstrapBuilder checks that a buildx builder exists and can start.
func BootstrapBuilder(ctx context.Context, name string) error {
	_, err := run.Output(ctx, "docker", "buildx", "inspect", "--bootstrap", name)
	return err
}

func sortedKeysRaw(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
