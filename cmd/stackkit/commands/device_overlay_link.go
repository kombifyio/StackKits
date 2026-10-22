package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
)

// The kit-owned overlay interface is brought up by wg-quick through its
// systemd template unit, so the interface returns after a reboot. A running
// interface is resynchronized in place (wg syncconf) so a bind or a revocation
// takes effect without dropping the other peers' sessions.
const (
	homeOverlaySystemConfigDir = "/etc/wireguard"
	homeOverlayProbeInterface  = "wg-stackkit-chk"
)

var (
	homeOverlayLookPath = exec.LookPath
	homeOverlayRun      = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		var stderr bytes.Buffer
		command := exec.CommandContext(ctx, name, args...)
		command.Stderr = &stderr
		out, err := command.Output()
		if err != nil {
			if detail := strings.TrimSpace(stderr.String()); detail != "" {
				return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, detail)
			}
			return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return out, nil
	}
)

type homeOverlayLinkResult struct {
	Interface  string `json:"interface"`
	ConfigFile string `json:"configFile"`
	Unit       string `json:"systemdUnit"`
	Action     string `json:"action"`
	PublicKey  string `json:"publicKey"`
	ListenPort int    `json:"listenPort"`
	Persistent bool   `json:"enabledAtBoot"`
}

func homeOverlayUnit() string {
	return "wg-quick@" + identity.HomeOverlayInterfaceName + ".service"
}

func homeOverlayConfigFile() string {
	return filepath.Join(homeOverlaySystemConfigDir, identity.HomeOverlayInterfaceName+".conf")
}

// preflightHomeOverlayHost fails closed, with the step that fixes it, when the
// host cannot run the overlay interface.
func preflightHomeOverlayHost() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("device overlay up: the WireGuard overlay interface requires a Linux host (this is %s)", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return errors.New("device overlay up: creating the WireGuard interface requires root; rerun with sudo")
	}
	for _, tool := range []string{"wg", "wg-quick", "ip"} {
		if _, err := homeOverlayLookPath(tool); err != nil {
			return fmt.Errorf("device overlay up: %q is not installed; install wireguard-tools and iproute2 (e.g. apt install wireguard-tools)", tool)
		}
	}
	if _, err := homeOverlayLookPath("systemctl"); err != nil {
		return errors.New("device overlay up: systemd is required to keep the overlay interface up after a reboot; systemctl was not found")
	}
	return nil
}

func homeOverlayLinkExists(ctx context.Context) bool {
	_, err := homeOverlayRun(ctx, "ip", "link", "show", "dev", identity.HomeOverlayInterfaceName)
	return err == nil
}

// requireHomeOverlayKernelSupport loads the wireguard module and proves the
// kernel can create a WireGuard link before the real interface is attempted.
func requireHomeOverlayKernelSupport(ctx context.Context) error {
	if _, err := homeOverlayLookPath("modprobe"); err == nil {
		_, _ = homeOverlayRun(ctx, "modprobe", "wireguard")
	}
	_, _ = homeOverlayRun(ctx, "ip", "link", "del", "dev", homeOverlayProbeInterface)
	if _, err := homeOverlayRun(ctx, "ip", "link", "add", "dev", homeOverlayProbeInterface, "type", "wireguard"); err != nil {
		return fmt.Errorf("device overlay up: the kernel cannot create a WireGuard interface (needs Linux 5.6+ or the wireguard module; in a container, NET_ADMIN and the host module): %w", err)
	}
	_, _ = homeOverlayRun(ctx, "ip", "link", "del", "dev", homeOverlayProbeInterface)
	return nil
}

func writeHomeOverlaySystemConfig(config []byte) (bool, error) {
	path := homeOverlayConfigFile()
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, config) {
		return false, nil
	}
	if err := os.MkdirAll(homeOverlaySystemConfigDir, 0o700); err != nil {
		return false, fmt.Errorf("device overlay up: create %s: %w", homeOverlaySystemConfigDir, err)
	}
	temp, err := os.CreateTemp(homeOverlaySystemConfigDir, "."+identity.HomeOverlayInterfaceName+"-*.conf")
	if err != nil {
		return false, fmt.Errorf("device overlay up: write config: %w", err)
	}
	defer os.Remove(temp.Name())
	err = temp.Chmod(0o600)
	if err == nil {
		_, err = temp.Write(config)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		return false, fmt.Errorf("device overlay up: write config: %w", err)
	}
	return true, nil
}

// ensureHomeOverlayInterface renders the current peer set, installs it as the
// wg-quick configuration, and creates or resynchronizes the interface. It is
// idempotent: a second run with an unchanged peer set changes nothing.
func ensureHomeOverlayInterface(ctx context.Context, root string, now time.Time) (homeOverlayLinkResult, error) {
	if err := preflightHomeOverlayHost(); err != nil {
		return homeOverlayLinkResult{}, err
	}
	node, err := identity.EnsureHomeOverlayNodeKey(root)
	if err != nil {
		return homeOverlayLinkResult{}, err
	}
	config, err := identity.RenderHomeOverlayConfig(root, now)
	if err != nil {
		return homeOverlayLinkResult{}, err
	}
	exists := homeOverlayLinkExists(ctx)
	if !exists {
		if err := requireHomeOverlayKernelSupport(ctx); err != nil {
			return homeOverlayLinkResult{}, err
		}
	}
	changed, err := writeHomeOverlaySystemConfig(config)
	if err != nil {
		return homeOverlayLinkResult{}, err
	}
	result := homeOverlayLinkResult{
		Interface: identity.HomeOverlayInterfaceName, ConfigFile: homeOverlayConfigFile(), Unit: homeOverlayUnit(),
		PublicKey: node.PublicKey, ListenPort: node.ListenPort, Action: "unchanged",
	}
	if exists {
		if changed {
			stripped, err := homeOverlayRun(ctx, "wg-quick", "strip", identity.HomeOverlayInterfaceName)
			if err != nil {
				return result, fmt.Errorf("device overlay up: %w", err)
			}
			temp, err := os.CreateTemp("", "wg-stackkit-sync-*.conf")
			if err != nil {
				return result, fmt.Errorf("device overlay up: %w", err)
			}
			defer os.Remove(temp.Name())
			_, err = temp.Write(stripped)
			if closeErr := temp.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return result, fmt.Errorf("device overlay up: %w", err)
			}
			if _, err := homeOverlayRun(ctx, "wg", "syncconf", identity.HomeOverlayInterfaceName, temp.Name()); err != nil {
				return result, fmt.Errorf("device overlay up: resync peers: %w", err)
			}
			result.Action = "resynced"
		}
		if _, err := homeOverlayRun(ctx, "systemctl", "enable", homeOverlayUnit()); err != nil {
			return result, fmt.Errorf("device overlay up: enable at boot: %w", err)
		}
	} else {
		if _, err := homeOverlayRun(ctx, "systemctl", "enable", "--now", homeOverlayUnit()); err != nil {
			return result, fmt.Errorf("device overlay up: start %s (see journalctl -u %s): %w", homeOverlayUnit(), homeOverlayUnit(), err)
		}
		result.Action = "created"
	}
	result.Persistent = true
	live, err := homeOverlayRun(ctx, "wg", "show", identity.HomeOverlayInterfaceName, "public-key")
	if err != nil {
		return result, fmt.Errorf("device overlay up: read back interface: %w", err)
	}
	if strings.TrimSpace(string(live)) != node.PublicKey {
		return result, errors.New("device overlay up: the interface is up but does not hold the node key from local custody; restart " + homeOverlayUnit())
	}
	return result, nil
}

// resyncHomeOverlayIfActive applies a changed peer set to a running overlay
// interface. Without a running interface, or without root, it reports the
// command that applies the change instead of failing the registry update.
func resyncHomeOverlayIfActive(ctx context.Context, root string, now time.Time) (string, error) {
	if runtime.GOOS != "linux" || !homeOverlayLinkExists(ctx) {
		return "not-running: run 'sudo stackkit device overlay up' to bring the overlay up", nil
	}
	if os.Geteuid() != 0 {
		return "pending: run 'sudo stackkit device overlay up' to apply this change to the running overlay", nil
	}
	result, err := ensureHomeOverlayInterface(ctx, root, now)
	if err != nil {
		return "", err
	}
	return result.Action, nil
}
