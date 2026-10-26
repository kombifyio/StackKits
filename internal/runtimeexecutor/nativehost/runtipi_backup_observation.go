package nativehost

import (
	"context"
	"errors"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"reflect"
	"slices"
	"strings"
)

// ErrRuntipiForeignWriter means a live foreign mount overlaps selected data.
// Unverifiable identities also refuse observation, but never assert this proof.
var ErrRuntipiForeignWriter = errors.New("foreign writable mount source overlaps selected external volume")

// ObserveRuntipiAppGroupGeneration is independent of API status/labels alone. Every
// local container is checked for foreign writers of the exact physical volumes.
// It never calls container stop/start, creates helpers, or adopts a generation.
func ObserveRuntipiAppGroupGeneration(ctx context.Context, g backuplifecycle.AppGroupControl, action string) (backuplifecycle.AppGroupGeneration, error) {
	var out backuplifecycle.AppGroupGeneration
	if err := backuplifecycle.ValidateAppGroups([]backuplifecycle.AppGroupControl{g}); err != nil {
		return out, err
	}
	negative := action == "settle-negative" || action == "verify" && g.Current().Partial
	absent := action == "stop" || action == "rollback-stop"
	if action == "verify" {
		absent = g.Current().Absent
	} else if action != "start" && action != "rollback-start" && !absent && !negative {
		return out, errors.New("invalid group observation action")
	}
	out.Absent = absent
	out.ConfigDigests = map[string]string{}
	client := docker.NewLocalClient()
	volumeNames := map[string]bool{}
	for _, held := range g.Original.Volumes {
		actual, err := client.InspectVolumeIdentity(ctx, held.Name)
		if err != nil {
			return out, err
		}
		if backuplifecycle.AppGroupDigest(actual) != held.IdentityDigest {
			return out, errors.New("external volume recreated or changed across API generation")
		}
		out.Volumes = append(out.Volumes, held)
		volumeNames[held.Name] = true
	}
	mountClient, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return out, err
	}
	scope, err := newRuntipiWriterScope(ctx, g.Original.Volumes)
	if err != nil {
		return out, err
	}
	anchors := map[string]backuplifecycle.SnapshotQuiescedContainer{}
	for _, c := range g.Original.Containers {
		anchors[c.ComponentRef] = c
	}
	listed, err := mountClient.ListMountContainers(ctx)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	inventory := map[string]string{}
	for _, id := range listed {
		c, err := client.InspectContainer(ctx, id)
		if err != nil {
			return out, err
		}
		if c.ID != id {
			return out, errors.New("local container inventory identity changed")
		}
		inventory[id] = runtipiWriterInventoryDigest(c)
		project := c.Config.Labels["com.docker.compose.project"]
		component := c.Config.Labels["com.docker.compose.service"]
		original, known := anchors[component]
		selected := project == g.Binding.PhysicalProject && known
		if c.State.Running || c.State.Paused || c.State.Restarting {
			for _, m := range c.Mounts {
				if selected && !absent {
					continue
				}
				if m.RW && volumeNames[m.Name] {
					return out, ErrRuntipiForeignWriter
				}
				// tmpfs is a newly allocated anonymous filesystem, never a bind alias.
				if m.Type == "tmpfs" {
					continue
				}
				if m.Type != "bind" && m.Type != "volume" {
					return out, errors.New("foreign writable mount source type is unverifiable")
				}
				overlaps, e := scope.overlapsMount(ctx, mountClient, c.ID, m.Source, m.Destination, m.RW)
				if e != nil {
					return out, e
				}
				if overlaps {
					return out, ErrRuntipiForeignWriter
				}
			}
		}
		if project != g.Binding.PhysicalProject {
			continue
		}
		if absent {
			return out, errors.New("API stop has not removed the exact native project")
		}
		if !known || seen[component] || c.State.Paused || c.State.Restarting || c.Config.Image != original.Image {
			return out, errors.New("successor component set/image/power invalid")
		}
		if action != "verify" && !negative && !c.State.Running {
			return out, errors.New("API start successor is not running")
		}
		seen[component] = true
		current := original
		current.ID = c.ID
		current.Name = strings.TrimPrefix(c.Name, "/")
		current.WasRunning = c.State.Running
		current.Mounts = nil
		for _, m := range c.Mounts {
			current.Mounts = append(current.Mounts, backuplifecycle.SnapshotQuiesceMount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW, Propagation: m.Propagation})
		}
		slices.SortFunc(current.Mounts, func(a, b backuplifecycle.SnapshotQuiesceMount) int {
			return strings.Compare(a.Destination, b.Destination)
		})
		before := append([]backuplifecycle.SnapshotQuiesceMount(nil), original.Mounts...)
		slices.SortFunc(before, func(a, b backuplifecycle.SnapshotQuiesceMount) int {
			return strings.Compare(a.Destination, b.Destination)
		})
		if !reflect.DeepEqual(current.Mounts, before) {
			return out, errors.New("successor exact mount graph changed")
		}
		out.ConfigDigests[component], err = AppGroupConfigDigest(c)
		if err != nil {
			return out, err
		}
		if out.ConfigDigests[component] != g.Original.ConfigDigests[component] {
			return out, errors.New("successor runtime config differs from original verified recipe")
		}
		out.Containers = append(out.Containers, current)
	}
	if negative {
		out.Absent = len(seen) == 0
		out.Partial = !out.Absent
	}
	if !absent && !negative && len(seen) != len(anchors) {
		return out, errors.New("incomplete successor component graph")
	}
	slices.SortFunc(out.Containers, func(a, b backuplifecycle.SnapshotQuiescedContainer) int {
		return strings.Compare(a.ComponentRef, b.ComponentRef)
	})
	// Refuse an observed inventory/power/mount change, including containers
	// created or started while scanning. This is observation, not a daemon lock.
	after, err := mountClient.ListMountContainers(ctx)
	if err != nil {
		return out, err
	}
	if len(after) != len(inventory) {
		return out, errors.New("local writer inventory changed during observation")
	}
	for _, id := range after {
		c, e := client.InspectContainer(ctx, id)
		if e != nil {
			return out, e
		}
		if c.ID != id || inventory[id] != runtipiWriterInventoryDigest(c) {
			return out, errors.New("local writer power or mounts changed during observation")
		}
	}
	for _, held := range g.Original.Volumes {
		actual, e := client.InspectVolumeIdentity(ctx, held.Name)
		if e != nil {
			return out, e
		}
		if backuplifecycle.AppGroupDigest(actual) != held.IdentityDigest {
			return out, errors.New("selected external volume changed during writer observation")
		}
	}
	if err := scope.verify(ctx, mountClient); err != nil {
		return out, err
	}
	return out, nil
}

// Ignore health churn, but bind all writer-relevant state, including stopped
// containers that could become writers before the final observation.
func runtipiWriterInventoryDigest(c *docker.ContainerInfo) string {
	return backuplifecycle.AppGroupDigest(struct {
		ID                          string
		Running, Paused, Restarting bool
		Mounts                      []docker.ContainerMount
		Project, Component          string
	}{c.ID, c.State.Running, c.State.Paused, c.State.Restarting, c.Mounts,
		c.Config.Labels["com.docker.compose.project"], c.Config.Labels["com.docker.compose.service"]})
}

// AppGroupConfigDigest binds generation-invariant semantics. Only Docker's
// default ID-derived hostname is normalized; custom host names, environment,
// command, host/security config and attached network names remain exact.
func AppGroupConfigDigest(c *docker.ContainerInfo) (string, error) {
	if c == nil || len(c.ID) != 64 || len(c.HostConfig.UnknownInspectionFields) != 0 {
		return "", errors.New("unqualified group container inspection")
	}
	config := c.Config
	if config.Hostname == c.ID[:12] {
		config.Hostname = "$docker-default-hostname"
	}
	networks := []string{}
	for name := range c.NetworkSettings.Networks {
		networks = append(networks, name)
	}
	slices.Sort(networks)
	return backuplifecycle.AppGroupDigest(struct {
		Config   docker.ContainerConfig
		Host     docker.ContainerHostConfig
		Networks []string
	}{config, c.HostConfig, networks}), nil
}
