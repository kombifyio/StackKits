package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"
)

// VolumeIdentity retains daemon metadata needed to reject same-name recreation.
// It is read-only, local-only, and contains no application file contents.
// The first native Files slice admits plain local volumes only. Driver options
// could redirect a same-named volume to a foreign host path and are refused.
type VolumeIdentity struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Scope      string            `json:"Scope"`
	Labels     map[string]string `json:"Labels"`
	Options    map[string]string `json:"Options"`
}
type boundedVolumeBuffer struct{ bytes.Buffer }

func (b *boundedVolumeBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 65536 {
		return 0, errors.New("volume inspection exceeds bound")
	}
	return b.Buffer.Write(p)
}
func (c *Client) InspectVolumeIdentity(ctx context.Context, name string) (VolumeIdentity, error) {
	var out VolumeIdentity
	if c == nil || !c.localOnly {
		return out, errors.New("volume identity requires local daemon")
	}
	if err := validateName(name); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := c.command(ctx, "volume", "inspect", name)
	var stdout boundedVolumeBuffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return out, errors.New("exact volume inspection unavailable")
	}
	var rows []VolumeIdentity
	if json.Unmarshal(stdout.Bytes(), &rows) != nil || len(rows) != 1 {
		return out, errors.New("invalid volume identity response")
	}
	out = rows[0]
	if out.Name != name || out.Driver != "local" || out.Scope != "local" || out.CreatedAt == "" || len(out.Options) != 0 || out.Mountpoint != "/var/lib/docker/volumes/"+name+"/_data" {
		return out, errors.New("volume identity differs from selected local external volume")
	}
	return out, nil
}
