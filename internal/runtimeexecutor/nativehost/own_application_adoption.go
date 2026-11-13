package nativehost

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// The closed Docker vectors of own-application adoption ("Stack it"): observe
// one container StackKits did not install, stop and restart it, and copy its
// one data source into the admitted named volume of the rebuilt application.
// Every vector is built here from validated identities; callers never pass
// Docker arguments.

const (
	// ownAdoptionMeasureScript prints the files and bytes below the source and
	// the free bytes of the Docker volume filesystem (an anonymous probe volume).
	ownAdoptionMeasureScript = `src=$1
probe=$2
test -d "$src"
files=$(find "$src" -type f | wc -l)
bytes=$(find "$src" -type f -exec stat -c %s {} + | awk '{s+=$1} END {print s+0}')
free=$(df -Pk "$probe" | awk 'NR==2 {print $4*1024}')
echo "$files $bytes $free"`
	// ownAdoptionCopyScript empties the destination, then copies the source
	// directory itself so ownership and permissions survive.
	ownAdoptionCopyScript = `src=$1
dst=$2
test -d "$src"
test -d "$dst"
for entry in "$dst"/* "$dst"/.[!.]* "$dst"/..?*; do
  test -e "$entry" || test -L "$entry" || continue
  rm -rf -- "$entry" || exit $?
done
cp -a -- "$src"/. "$dst"/ || exit $?`
	// ownAdoptionDigestScript prints files, bytes, a digest of the sorted
	// per-file SHA-256 list and a digest of every entry's type, mode, owner and
	// group (directories, symlinks and special files included), so equal trees
	// give equal output wherever mounted and a copy that lost permissions or
	// entries does not pass.
	ownAdoptionDigestScript = `root=$1
test -d "$root"
cd "$root"
list=$(find . -type f -exec sha256sum {} + | LC_ALL=C sort -k 2)
files=$(printf '%s\n' "$list" | grep -c . || true)
bytes=$(find . -type f -exec stat -c %s {} + | awk '{s+=$1} END {print s+0}')
digest=$(printf '%s\n' "$list" | sha256sum | cut -d' ' -f1)
meta=$(find . -exec stat -c '%F %a %u %g %n' {} + | LC_ALL=C sort -k 5 | sha256sum | cut -d' ' -f1)
echo "$files $bytes $digest $meta"`
)

var (
	ownAdoptionContainerRef = regexp.MustCompile(`^(?:[a-f0-9]{12,64}|[A-Za-z0-9][A-Za-z0-9_.-]{0,127})$`)
	ownAdoptionImageID      = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	ownAdoptionVolumeName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
	ownAdoptionHelperImage  = regexp.MustCompile(`^[a-z0-9]+(?:[._/:-][a-z0-9]+)*@sha256:[a-f0-9]{64}$`)
	ownAdoptionLabelValue   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
)

// OwnAdoptionMount is one data source or destination of an adoption copy.
type OwnAdoptionMount struct {
	Type   string // volume or bind
	Source string
}

func (m OwnAdoptionMount) argument(target string, readOnly bool) (string, error) {
	switch m.Type {
	case "volume":
		if !ownAdoptionVolumeName.MatchString(m.Source) {
			return "", errors.New("adoption volume name is outside the closed contract")
		}
	case "bind":
		if !strings.HasPrefix(m.Source, "/") || path.Clean(m.Source) != m.Source || m.Source == "/" || strings.ContainsAny(m.Source, ",=\"'\x00\r\n") {
			return "", errors.New("adoption bind source must be a clean absolute path without mount separators")
		}
	default:
		return "", errors.New("adoption mount type must be volume or bind")
	}
	argument := "type=" + m.Type + ",src=" + m.Source + ",dst=" + target
	if readOnly {
		argument += ",readonly"
	}
	return argument, nil
}

// OwnAdoptionDocker runs the closed adoption vectors on the local daemon.
type OwnAdoptionDocker struct {
	run func(context.Context, []string) ([]byte, error)
}

func NewOwnAdoptionDocker() OwnAdoptionDocker { return OwnAdoptionDocker{run: runClosedDockerCLI} }

func validOwnAdoptionContainerID(id string) bool {
	return validStandaloneComposeContainerID(id) && id == strings.ToLower(id)
}

// InspectContainer returns `docker container inspect` of one container
// selected by ID, short ID or name.
func (d OwnAdoptionDocker) InspectContainer(ctx context.Context, ref string) ([]byte, error) {
	if !ownAdoptionContainerRef.MatchString(ref) {
		return nil, errors.New("container reference must be a Docker ID or name")
	}
	return d.run(ctx, []string{"container", "inspect", ref})
}

// InspectImage returns `docker image inspect` of one local image ID.
func (d OwnAdoptionDocker) InspectImage(ctx context.Context, id string) ([]byte, error) {
	if !ownAdoptionImageID.MatchString(id) {
		return nil, errors.New("image identity is outside the closed contract")
	}
	return d.run(ctx, []string{"image", "inspect", id})
}

// ComposePeers lists every container of one Compose project.
func (d OwnAdoptionDocker) ComposePeers(ctx context.Context, project string) ([]string, error) {
	if !ownAdoptionLabelValue.MatchString(project) {
		return nil, errors.New("compose project label is outside the closed contract")
	}
	raw, err := d.run(ctx, []string{"ps", "-aq", "--no-trunc", "--filter", "label=com.docker.compose.project=" + project})
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(raw)), nil
}

// NetworkPeers lists the IDs of every container, running or stopped, that
// is attached to one network.
func (d OwnAdoptionDocker) NetworkPeers(ctx context.Context, network string) ([]string, error) {
	if !ownAdoptionLabelValue.MatchString(network) {
		return nil, errors.New("network name is outside the closed contract")
	}
	raw, err := d.run(ctx, []string{"ps", "-aq", "--no-trunc", "--filter", "network=" + network})
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(raw)), nil
}

// Stop stops one exact container with a 30-second grace period.
func (d OwnAdoptionDocker) Stop(ctx context.Context, id string) error {
	if !validOwnAdoptionContainerID(id) {
		return errors.New("container identity is outside the closed contract")
	}
	_, err := d.run(ctx, []string{"stop", "--time", "30", id})
	return err
}

// Start starts one exact container.
func (d OwnAdoptionDocker) Start(ctx context.Context, id string) error {
	if !validOwnAdoptionContainerID(id) {
		return errors.New("container identity is outside the closed contract")
	}
	_, err := d.run(ctx, []string{"start", id})
	return err
}

// SetRestartPolicy changes the restart policy of one exact container, so a
// stopped original never returns by itself after a reboot.
func (d OwnAdoptionDocker) SetRestartPolicy(ctx context.Context, id, policy string) error {
	if !validOwnAdoptionContainerID(id) {
		return errors.New("container identity is outside the closed contract")
	}
	name, retries, found := strings.Cut(policy, ":")
	switch name {
	case "no", "always", "unless-stopped":
		if found {
			return errors.New("restart policy is outside the closed contract")
		}
	case "on-failure":
		if found {
			if n, err := strconv.Atoi(retries); err != nil || n < 0 || n > 1000 {
				return errors.New("restart policy is outside the closed contract")
			}
		}
	default:
		return errors.New("restart policy is outside the closed contract")
	}
	_, err := d.run(ctx, []string{"update", "--restart", policy, id})
	return err
}

// Remove removes one exact stopped container. Its volumes stay.
func (d OwnAdoptionDocker) Remove(ctx context.Context, id string) error {
	if !validOwnAdoptionContainerID(id) {
		return errors.New("container identity is outside the closed contract")
	}
	_, err := d.run(ctx, []string{"rm", id})
	return err
}

// InspectVolume returns `docker volume inspect` of one volume.
func (d OwnAdoptionDocker) InspectVolume(ctx context.Context, name string) ([]byte, error) {
	if !ownAdoptionVolumeName.MatchString(name) {
		return nil, errors.New("volume name is outside the closed contract")
	}
	return d.run(ctx, []string{"volume", "inspect", name})
}

// CreateComposeVolume creates the named volume of the rebuilt application with
// the Compose identity its project later admits as its own.
func (d OwnAdoptionDocker) CreateComposeVolume(ctx context.Context, name, project, key string) error {
	if !ownAdoptionVolumeName.MatchString(name) || !ownAdoptionLabelValue.MatchString(project) || !ownAdoptionLabelValue.MatchString(key) || name != project+"_"+key {
		return errors.New("adoption volume identity is outside the closed contract")
	}
	_, err := d.run(ctx, []string{"volume", "create", "--driver", "local", "--label", composeProjectLabel + "=" + project, "--label", composeVolumeLabel + "=" + key, name})
	return err
}

func (d OwnAdoptionDocker) helper(ctx context.Context, helperImage, label string, mounts []string, script string, args ...string) ([]byte, error) {
	if !ownAdoptionHelperImage.MatchString(helperImage) || !ownAdoptionLabelValue.MatchString(label) {
		return nil, errors.New("adoption helper identity is outside the closed contract")
	}
	vector := []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE",
		"--security-opt", "no-new-privileges", "--label", "io.stackkit.own-adoption=" + label}
	for _, mount := range mounts {
		vector = append(vector, "--mount", mount)
	}
	vector = append(vector, "--entrypoint", "/bin/sh", helperImage, "-ceu", script, "--")
	return d.run(ctx, append(vector, args...))
}

// Measure returns the files and bytes of the source and the free bytes of
// the Docker volume filesystem the copy would fill.
func (d OwnAdoptionDocker) Measure(ctx context.Context, helperImage, label string, source OwnAdoptionMount) (files, bytes, free int64, err error) {
	from, err := source.argument("/source", true)
	if err != nil {
		return 0, 0, 0, err
	}
	raw, err := d.helper(ctx, helperImage, label, []string{from, "type=volume,dst=/probe"}, ownAdoptionMeasureScript, "/source", "/probe")
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 3 {
		return 0, 0, 0, errors.New("adoption data measurement is malformed")
	}
	values := make([]int64, 3)
	for i, field := range fields {
		if values[i], err = strconv.ParseInt(field, 10, 64); err != nil || values[i] < 0 {
			return 0, 0, 0, errors.New("adoption data measurement is malformed")
		}
	}
	return values[0], values[1], values[2], nil
}

// Copy replaces the destination volume's content with the read-only source.
func (d OwnAdoptionDocker) Copy(ctx context.Context, helperImage, label string, source OwnAdoptionMount, destination string) error {
	from, err := source.argument("/source", true)
	if err != nil {
		return err
	}
	to, err := OwnAdoptionMount{Type: "volume", Source: destination}.argument("/target", false)
	if err != nil {
		return err
	}
	_, err = d.helper(ctx, helperImage, label, []string{from, to}, ownAdoptionCopyScript, "/source", "/target")
	return err
}

// Digest returns files, bytes and the content digest of one mount, read-only.
func (d OwnAdoptionDocker) Digest(ctx context.Context, helperImage, label string, source OwnAdoptionMount) (string, error) {
	from, err := source.argument("/source", true)
	if err != nil {
		return "", err
	}
	raw, err := d.helper(ctx, helperImage, label, []string{from}, ownAdoptionDigestScript, "/source")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 4 || len(fields[2]) != 64 || len(fields[3]) != 64 {
		return "", errors.New("adoption data digest is malformed")
	}
	return fmt.Sprintf("files=%s bytes=%s sha256:%s metadata=sha256:%s", fields[0], fields[1], fields[2], fields[3]), nil
}
