// Package releasechannel stores which release channel the user follows.
//
// The choice is a single word in ~/.stackkits/channel so the shell installer
// and the CLI read the same file. Stable is the default; preview ("Nightly")
// is an explicit opt-in (CI-CD-PLATFORM-STANDARD 4.6.2).
package releasechannel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Channel is a user-selectable release channel.
type Channel string

const (
	Stable  Channel = "stable"
	Preview Channel = "preview"

	// FileName is the channel file inside the user's ~/.stackkits directory.
	FileName = "channel"
)

// Parse accepts exactly "stable" or "preview". Anything else fails closed.
func Parse(value string) (Channel, error) {
	switch Channel(strings.TrimSpace(value)) {
	case Stable:
		return Stable, nil
	case Preview:
		return Preview, nil
	}
	return "", fmt.Errorf("unknown release channel %q: use stable or preview", strings.TrimSpace(value))
}

// Label is the interface name of the channel.
func (channel Channel) Label() string {
	if channel == Preview {
		return "preview (Nightly)"
	}
	return string(channel)
}

// Read returns the stored channel, stable when none was ever chosen. A file
// with any other content is an error rather than a silent stable.
func Read(dir string) (Channel, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return Stable, nil
	}
	if err != nil {
		return "", fmt.Errorf("read release channel: %w", err)
	}
	return Parse(string(data))
}

// Write stores the channel atomically.
func Write(dir string, channel Channel) error {
	if _, err := Parse(string(channel)); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, FileName+".*")
	if err != nil {
		return fmt.Errorf("write release channel: %w", err)
	}
	defer os.Remove(temp.Name())
	if _, err := temp.WriteString(string(channel) + "\n"); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write release channel: %w", err)
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write release channel: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("write release channel: %w", err)
	}
	return os.Rename(temp.Name(), filepath.Join(dir, FileName))
}
