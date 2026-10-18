package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

type VolumeInfo struct {
	Name       string
	Driver     string
	Mountpoint string
	CreatedAt  string
	Scope      string
	Labels     map[string]string
	Options    map[string]string
}

type DaemonIdentity struct {
	ID            string
	DockerRootDir string
}

func (c *Client) InspectVolume(ctx context.Context, name string) (VolumeInfo, error) {
	if err := validateName(name); err != nil {
		return VolumeInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := c.command(ctx, "volume", "inspect", name)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return VolumeInfo{}, errors.New("inspect selected local Docker volume failed")
	}
	var volumes []VolumeInfo
	if err := json.Unmarshal(out.Bytes(), &volumes); err != nil || len(volumes) != 1 || volumes[0].Name != name {
		return VolumeInfo{}, errors.New("selected local Docker volume identity is ambiguous")
	}
	return volumes[0], nil
}

func (c *Client) DaemonIdentity(ctx context.Context) (DaemonIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := c.command(ctx, "info", "--format", "{{json .}}")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return DaemonIdentity{}, errors.New("inspect local Docker daemon failed")
	}
	var identity DaemonIdentity
	if err := json.Unmarshal(out.Bytes(), &identity); err != nil || identity.ID == "" || identity.DockerRootDir == "" {
		return identity, errors.New("local Docker daemon has no exact identity/data root")
	}
	return identity, nil
}
