package nativehost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// ImportStandaloneComposeJellyfinMedia is part of the existing Plan-bound owner
// setup. It adds one immutable file to the already mounted named library; it
// never writes through Jellyfin, switches mounts or changes existing bytes.
func ImportStandaloneComposeJellyfinMedia(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, source string) (string, error) {
	if deployment.WorkloadRef != "media" || deployment.ModuleRef != "stackkits-jellyfin-runtime" || deployment.RuntimeAdapter.ID != "standalone-compose" {
		return "", errors.New("media import requires the applied standalone Jellyfin workload")
	}
	if _, err := architecturev2renderer.ParseJellyfinWorkloadBundle(deployment.Bundle); err != nil {
		return "", err
	}
	ops, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return "", err
	}
	o := ops.(*osStandaloneComposeWorkloadOperations)
	project, err := o.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return "", err
	}
	containerID := ""
	inspect := func() (dockerVolumeInspect, error) {
		if err := o.verifyPersisted(project); err != nil {
			return dockerVolumeInspect{}, err
		}
		raw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
		if err != nil {
			return dockerVolumeInspect{}, err
		}
		statuses, err := parseStandaloneComposeStatuses(raw)
		if err != nil {
			return dockerVolumeInspect{}, err
		}
		if _, err := observeStandaloneComposeComponentsWithIdentity(project.bundle.Components, statuses, true); err != nil {
			return dockerVolumeInspect{}, err
		}
		currentID := statuses["jellyfin"].ID
		if containerID != "" && currentID != containerID {
			return dockerVolumeInspect{}, errors.New("Jellyfin container identity changed during media import")
		}
		containerID = currentID
		r := standaloneComposeRemover{ops: o}
		containers, err := r.inspectPresentContainers(ctx, project, statuses)
		if err != nil {
			return dockerVolumeInspect{}, err
		}
		declared, hostPaths := standaloneComposeVolumeDeclarations(project.bundle)
		for _, host := range hostPaths {
			if host.Target == "/media" {
				return dockerVolumeInspect{}, errors.New("media import does not write to an owner-managed host library")
			}
		}
		volumes, err := r.volumesFromContainerMounts(ctx, project, declared, containers)
		if err != nil {
			return dockerVolumeInspect{}, err
		}
		volume, ok := volumes["jellyfin-library"]
		if !ok || volume.Name != project.name+"_jellyfin-library" || volume.Mountpoint != filepath.Join("/var/lib/docker/volumes", volume.Name, "_data") {
			return dockerVolumeInspect{}, errors.New("media import requires the exact local named library")
		}
		for _, c := range containers {
			for _, m := range c.Mounts {
				if m.Destination == "/media" && (!m.RW && m.Type == "volume" && m.Name == volume.Name && m.Source == volume.Mountpoint) {
					return volume, nil
				}
			}
		}
		return dockerVolumeInspect{}, errors.New("the admitted media library is not mounted read-only")
	}
	before, err := inspect()
	if err != nil {
		return "", err
	}
	digest, err := importMediaFile(ctx, workspace, source, before.Mountpoint)
	if err != nil {
		return "", err
	}
	after, err := inspect()
	if err != nil {
		return "", err
	}
	if before.Name != after.Name || before.CreatedAt != after.CreatedAt || before.Mountpoint != after.Mountpoint {
		return "", errors.New("media volume identity changed during import")
	}
	return digest, nil
}

func importMediaFile(ctx context.Context, workspace, source, destination string) (string, error) {
	// A dedicated non-secret input directory prevents a credential/custody file
	// from becoming public media through a caller-selected workspace path.
	source = filepath.ToSlash(source)
	if !strings.HasPrefix(source, "media-import/") || len(strings.Split(source, "/")) != 2 || !filepath.IsLocal(source) || strings.Contains(source, "\\") {
		return "", errors.New("media source must be a regular file directly beneath workspace media-import/")
	}
	ext := strings.ToLower(filepath.Ext(source))
	switch ext {
	case ".mp4", ".mkv", ".webm", ".mov", ".m4v", ".mp3", ".flac", ".wav", ".ogg", ".opus", ".m4a":
	default:
		return "", errors.New("media source has no supported audio or video extension")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	view, err := root.View("media-import")
	if err != nil {
		return "", err
	}
	input, err := view.Open(filepath.Base(source))
	if err != nil {
		return "", err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	held, err := confinedfs.Open(destination)
	if err != nil {
		return "", err
	}
	defer held.Close()
	outRoot, err := os.OpenRoot(destination)
	if err != nil {
		return "", err
	}
	defer outRoot.Close()
	if err := held.VerifyPathIdentity(); err != nil {
		return "", err
	}
	if err := outRoot.Mkdir("Imports", 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := outRoot.Lstat("Imports")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("media import destination is not a plain directory")
	}
	output, err := outRoot.OpenRoot("Imports")
	if err != nil {
		return "", err
	}
	defer output.Close()
	opened, err := output.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("media import directory changed while opening it")
	}
	name := digest + ext
	verify := func() error {
		info, err := output.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("media import will not replace an existing non-regular entry")
		}
		file, err := output.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		h := sha256.New()
		if _, err := io.Copy(h, file); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return errors.New("media import will not replace conflicting owner bytes")
		}
		return nil
	}
	if err := verify(); err == nil {
		return digest, held.VerifyPathIdentity()
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Publish without replacing a racing existing entry. Temporary incomplete
	// bytes are never indexed and are removed on every return path.
	tempName := ".media-import-" + rand.Text()
	temp, err := output.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer func() { _ = temp.Close(); _ = output.Remove(tempName) }()
	written := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, written), input); err != nil {
		return "", err
	}
	if hex.EncodeToString(written.Sum(nil)) != digest {
		return "", errors.New("media source changed during import")
	}
	if err := temp.Chmod(0644); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := held.VerifyPathIdentity(); err != nil {
		return "", err
	}
	if err := output.Link(tempName, name); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("publish imported media: %w", err)
	}
	if err := verify(); err != nil {
		return "", err
	}
	return digest, held.VerifyPathIdentity()
}
