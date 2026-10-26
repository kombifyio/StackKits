// Package pocketidruntime observes the existing Core's single-host Docker/SQLite
// provider. Callers hold the installation lifecycle mutation lock throughout a
// transition or managed command; this package does not create another lifecycle.
package pocketidruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
	"gopkg.in/yaml.v3"
)

var identifier = regexp.MustCompile(`^[a-f0-9]{64}$`)
var imageDigest = regexp.MustCompile(`^(?:[^\s]+@)?sha256:[a-f0-9]{64}$`)

type container struct {
	ID    string `json:"Id"`
	Image string
	State struct {
		Running, Restarting, Dead bool
		Pid                       int
		StartedAt                 string
		FinishedAt                string
		ExitCode                  int
	}
	HostConfig struct{ RestartPolicy struct{ Name string } }
	Config     struct {
		Image, WorkingDir string
		Env               []string
		Labels            map[string]string
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
}

func validProject(project string) bool {
	return project == "stackkit-basement-core" || project == "stackkit-cloud-core" || project == "stackkit-cloud-core-standalone"
}

func managedImage(image string) bool {
	// The product compiler admits only the declared derivative Registry route.
	// A config digest also permits the isolated, unpublished local qualification.
	return strings.HasPrefix(image, "ghcr.io/kombifyio/stackkits:pocketid-cas-") || strings.HasPrefix(image, "sha256:")
}

// docker never returns provider output in errors: inspect includes secret env.
func docker(ctx context.Context, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("PocketID runtime needs a context")
	}
	bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := exec.CommandContext(bounded, "docker", args...).Output() //nolint:gosec // closed commands and validated IDs below
	if err != nil || len(out) > 8<<20 {
		return nil, errors.New("PocketID runtime Docker observation/operation failed")
	}
	return out, nil
}

func inventory(ctx context.Context) ([]container, error) {
	raw, err := docker(ctx, "container", "ls", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(raw))
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 256 {
		return nil, errors.New("PocketID runtime inventory exceeds bounded scope")
	}
	for _, id := range ids {
		if !identifier.MatchString(id) {
			return nil, errors.New("PocketID container identity malformed")
		}
	}
	raw, err = docker(ctx, append([]string{"container", "inspect"}, ids...)...)
	if err != nil {
		return nil, err
	}
	var result []container
	if json.Unmarshal(raw, &result) != nil || len(result) != len(ids) {
		return nil, errors.New("PocketID runtime inventory malformed")
	}
	return result, nil
}

func selectedImage(definition []byte) (string, error) {
	var compose struct {
		Services map[string]struct{ Image string }
	}
	if yaml.Unmarshal(definition, &compose) != nil || !imageDigest.MatchString(compose.Services["pocketid"].Image) {
		return "", errors.New("PocketID runtime requires the compiler's immutable provider image")
	}
	return compose.Services["pocketid"].Image, nil
}

func owned(items []container, project string) (*container, error) {
	if !validProject(project) {
		return nil, errors.New("PocketID runtime project outside supported Core")
	}
	var found *container
	for i := range items {
		c := &items[i]
		if c.Config.Labels["com.docker.compose.project"] == project && c.Config.Labels["com.docker.compose.service"] == "pocketid" {
			if found != nil {
				return nil, errors.New("PocketID runtime has duplicate provider containers")
			}
			found = c
		}
	}
	return found, nil
}

func localVolume(ctx context.Context, items []container, selected *container, project string) (string, error) {
	if selected == nil || !identifier.MatchString(selected.ID) || selected.Config.WorkingDir != "/app" {
		return "", errors.New("PocketID runtime provider unavailable")
	}
	for _, entry := range selected.Config.Env {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "DB_CONNECTION_STRING":
			if value != "" && value != "data/pocket-id.db" {
				return "", errors.New("PocketID drain supports only the existing local SQLite database")
			}
		case "DB_PROVIDER":
			if value != "" && value != "sqlite" {
				return "", errors.New("PocketID drain refuses an external database")
			}
		case "DB_CONNECTION_STRING_FILE", "DB_PROVIDER_FILE":
			if value != "" {
				return "", errors.New("PocketID drain refuses database file overrides")
			}
		}
	}
	volume, source := "", ""
	for _, mount := range selected.Mounts {
		if mount.Destination == "/app/data" {
			if volume != "" || mount.Type != "volume" || mount.Name != project+"_pocketid-data" || !mount.RW {
				return "", errors.New("PocketID drain requires the existing named data volume")
			}
			volume, source = mount.Name, mount.Source
		} else if strings.HasPrefix(mount.Destination, "/app/data/") {
			return "", errors.New("PocketID drain refuses overlapping data mounts")
		}
	}
	if volume == "" || source == "" {
		return "", errors.New("PocketID runtime data volume unavailable")
	}
	for _, c := range items {
		if c.ID == selected.ID {
			continue
		}
		for _, m := range c.Mounts {
			// Include stopped containers: they could restart the old writer.
			if m.Name == volume || m.Source == source {
				return "", errors.New("PocketID data volume has another container consumer")
			}
		}
	}
	raw, err := docker(ctx, "volume", "inspect", volume)
	if err != nil {
		return "", err
	}
	var volumes []struct {
		Driver, Scope   string
		Options, Labels map[string]string
	}
	if json.Unmarshal(raw, &volumes) != nil || len(volumes) != 1 || volumes[0].Driver != "local" || volumes[0].Scope != "local" || len(volumes[0].Options) != 0 || volumes[0].Labels["com.docker.compose.project"] != project {
		return "", errors.New("PocketID drain refuses shared/network/unowned storage")
	}
	return volume, nil
}

// Prepare runs before every supported executor's up. The old container's
// process, HTTP queues, and restart identity must be gone before replacement.
func Prepare(ctx context.Context, workspace, project string, definition []byte) error {
	image, err := selectedImage(definition)
	if err != nil {
		return err
	}
	if !validProject(project) {
		return errors.New("PocketID transition project unavailable")
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	bindingMissing := errors.Is(err, localevidence.ErrOwnerRuntimeBindingMissing)
	if err != nil && !errors.Is(err, localevidence.ErrOwnerRuntimeBindingMissing) {
		return err
	}
	previous := binding.PocketIDRuntime
	if !managedImage(image) && (previous == nil || previous.Contract != pocketid.MembershipContract) {
		return nil // Preserve the existing official/manual installation lifecycle.
	}
	if previous != nil && previous.Contract == pocketid.MembershipContract && !strings.HasPrefix(image, "ghcr.io/kombifyio/stackkits:pocketid-cas-") {
		return errors.New("PocketID managed storage cannot downgrade to an unfenced provider")
	}
	items, err := inventory(ctx)
	if err != nil {
		return err
	}
	old, err := owned(items, project)
	if err != nil {
		return err
	}
	if old != nil && bindingMissing {
		return errors.New("PocketID existing provider requires its owner installation binding before transition")
	}
	if old != nil {
		if _, err := localVolume(ctx, items, old, project); err != nil {
			return err
		}
	}
	if previous != nil && previous.Ready && old != nil && old.State.Running && !old.State.Restarting && !old.State.Dead && previous.ImageRef == image && previous.ContainerID == old.ID && previous.StartedAt == old.State.StartedAt && previous.ImageID == old.Image {
		return nil // Reconcile the same running incarnation without a needless restart.
	}
	if err := localevidence.InvalidatePocketIDRuntime(workspace); err != nil {
		return err
	}
	if old == nil {
		return nil
	}
	if _, err := docker(ctx, "container", "update", "--restart=no", old.ID); err != nil {
		return err
	}
	if _, err := docker(ctx, "container", "stop", "--time", "30", old.ID); err != nil {
		return err
	}
	items, err = inventory(ctx)
	if err != nil {
		return err
	}
	stopped, err := owned(items, project)
	if err != nil || stopped == nil || stopped.ID != old.ID || stopped.State.Running || stopped.State.Restarting || stopped.State.Pid != 0 || stopped.HostConfig.RestartPolicy.Name != "no" {
		return errors.New("PocketID old process did not exit")
	}
	if _, err := localVolume(ctx, items, stopped, project); err != nil {
		return err
	}
	if _, err := docker(ctx, "container", "rm", old.ID); err != nil {
		return err
	}
	items, err = inventory(ctx)
	if err != nil {
		return err
	}
	for _, c := range items {
		if c.ID == old.ID {
			return errors.New("PocketID old restart identity still exists")
		}
	}
	return localevidence.RecordPocketIDDrain(workspace, old.ID, time.Now().UTC(), localevidence.PocketIDStoppedProcess{
		PID: stopped.State.Pid, ExitCode: stopped.State.ExitCode, FinishedAt: stopped.State.FinishedAt, RestartPolicy: stopped.HostConfig.RestartPolicy.Name,
	})
}

// Complete binds the actual new incarnation to existing signed installation
// evidence. A restart/recreate invalidates it until the executor re-observes it.
func Complete(ctx context.Context, workspace, project string, definition []byte, installed localevidence.OwnerRuntimeBinding) (localevidence.OwnerRuntimeBinding, error) {
	image, err := selectedImage(definition)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	if !managedImage(image) && (installed.PocketIDRuntime == nil || installed.PocketIDRuntime.Contract != pocketid.MembershipContract) {
		return installed, nil
	}
	if !managedImage(image) {
		return localevidence.OwnerRuntimeBinding{}, errors.New("PocketID managed storage cannot lose its qualified provider")
	}
	items, err := inventory(ctx)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	current, err := owned(items, project)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	volume, err := localVolume(ctx, items, current, project)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	if !current.State.Running || current.State.Restarting || current.State.Dead || current.State.Pid <= 0 || current.Config.Image != image {
		return localevidence.OwnerRuntimeBinding{}, errors.New("PocketID started incarnation differs from authorized image")
	}
	raw, err := docker(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil || strings.TrimSpace(string(raw)) != current.Image {
		return localevidence.OwnerRuntimeBinding{}, errors.New("PocketID image content differs from selected immutable reference")
	}
	key, err := localevidence.ReadBasementRuntimePocketIDAdminKey(workspace)
	if err != nil {
		return localevidence.OwnerRuntimeBinding{}, err
	}
	contract := ""
	err = pocketid.NewClient("http://127.0.0.1:1411", key).RequireMembershipCAS(ctx)
	if err == nil {
		contract = pocketid.MembershipContract
	} else if !errors.Is(err, pocketid.ErrNotFound) {
		return localevidence.OwnerRuntimeBinding{}, errors.New("PocketID authenticated capability unavailable")
	}
	return localevidence.BindPocketIDRuntime(workspace, localevidence.PocketIDRuntimeBinding{Project: project, ContainerID: current.ID, ImageRef: image, ImageID: current.Image, StartedAt: current.State.StartedAt, Volume: volume, Contract: contract, Ready: true})
}

// Require is called by the managed receiver before any provider effect. It
// reads signed installation custody and real Docker state, never request data.
func Require(ctx context.Context, workspace string) error {
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return err
	}
	proof := binding.PocketIDRuntime
	if proof == nil || !proof.Ready || proof.Contract != pocketid.MembershipContract {
		return errors.New("PocketID managed admission requires an executor drain binding")
	}
	items, err := inventory(ctx)
	if err != nil {
		return err
	}
	current, err := owned(items, proof.Project)
	if err != nil {
		return err
	}
	volume, err := localVolume(ctx, items, current, proof.Project)
	if err != nil {
		return err
	}
	if !current.State.Running || current.State.Restarting || current.State.Dead || current.State.Pid <= 0 || current.ID != proof.ContainerID || current.Image != proof.ImageID || current.Config.Image != proof.ImageRef || current.State.StartedAt != proof.StartedAt || volume != proof.Volume {
		return fmt.Errorf("PocketID managed admission: installed incarnation changed")
	}
	return nil
}
