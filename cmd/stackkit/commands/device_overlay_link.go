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

// The VPN add-on WireGuard interface is brought up by wg-quick through its
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

// homeOverlayRunInput runs a tool with input on standard input, so a secret
// never appears in an argument or a temporary file.
var homeOverlayRunInput = func(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = bytes.NewReader(input)
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

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

// requireHomeOverlaySelected fails closed unless the Owner switched the VPN
// add-on on with the WireGuard tool: the overlay is never a default.
func requireHomeOverlaySelected(root string) error {
	selection, err := identity.LoadHomeVPNSelection(root)
	if errors.Is(err, identity.ErrHomeVPNOff) {
		return errors.New("device overlay up: the VPN add-on is off; the Owner switches it on with 'sudo stackkit vpn enable --tool wireguard --endpoint <host:port>'")
	}
	if err != nil {
		return err
	}
	if selection.Tool != identity.HomeVPNToolWireGuard {
		return fmt.Errorf("device overlay up: the VPN add-on runs the %s tool, not WireGuard", selection.Tool)
	}
	return nil
}

// ensureHomeOverlayInterface renders the current peer set, installs it as the
// wg-quick configuration, and creates or resynchronizes the interface. It is
// idempotent: a second run with an unchanged peer set changes nothing.
func ensureHomeOverlayInterface(ctx context.Context, root string, now time.Time) (homeOverlayLinkResult, error) {
	if err := preflightHomeOverlayHost(); err != nil {
		return homeOverlayLinkResult{}, err
	}
	if err := requireHomeOverlaySelected(root); err != nil {
		// An interface from before the opt-in (P0) or of a switched-off
		// add-on never keeps serving its last peer set: it is taken down,
		// also when the resync watcher runs this.
		if homeOverlayLinkExists(ctx) || homeOverlayUnitInstalled() {
			if downErr := teardownHomeOverlayInterface(ctx); downErr != nil {
				return homeOverlayLinkResult{}, fmt.Errorf("%w; taking the interface down failed: %v", err, downErr)
			}
			return homeOverlayLinkResult{}, fmt.Errorf("%w (the WireGuard interface was taken down)", err)
		}
		return homeOverlayLinkResult{}, err
	}
	if _, err := homeOverlayLookPath("nft"); err != nil {
		return homeOverlayLinkResult{}, errors.New("device overlay up: nft is not installed; install nftables (apt install nftables), which confines the overlay to the site address")
	}
	node, err := identity.EnsureHomeOverlayNodeKey(root)
	if err != nil {
		return homeOverlayLinkResult{}, err
	}
	// Distributions that confine wg to /etc/wireguard (Ubuntu 26.04's
	// AppArmor profile) get a site-local grant for exactly the node key in
	// custody before wg-quick or wg set reads it.
	if err := grantHomeOverlayKeyRead(ctx, root); err != nil {
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
			// The stripped configuration goes below /etc/wireguard, the only
			// place a confined wg reads configuration from.
			temp, err := os.CreateTemp(homeOverlaySystemConfigDir, ".wg-stackkit-sync-*.conf")
			if err != nil {
				return result, fmt.Errorf("device overlay up: %w", err)
			}
			defer os.Remove(temp.Name())
			err = temp.Chmod(0o600)
			if err == nil {
				_, err = temp.Write(stripped)
			}
			if closeErr := temp.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return result, fmt.Errorf("device overlay up: %w", err)
			}
			if _, err := homeOverlayRun(ctx, "wg", "syncconf", identity.HomeOverlayInterfaceName, temp.Name()); err != nil {
				return result, fmt.Errorf("device overlay up: resync peers: %w", err)
			}
			// The stripped configuration carries no private key (it stays in
			// custody and PostUp loads it at interface start), and wg syncconf
			// replaces the whole configuration: set the key again from custody,
			// or the interface stops serving every peer.
			keyPath, err := homeOverlayNodeKeyPath(root)
			if err != nil {
				return result, err
			}
			if _, err := homeOverlayRun(ctx, "wg", "set", identity.HomeOverlayInterfaceName, "private-key", keyPath); err != nil {
				return result, fmt.Errorf("device overlay up: restore the node key after the resync: %w", err)
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
	if err := applyVPNGuard(ctx, root, vpnGuardSpec{iface: identity.HomeOverlayInterfaceName, local: []string{homeOverlayNodeAddress}}); err != nil {
		return result, err
	}
	if err := installHomeOverlayResync(ctx, root); err != nil {
		return result, err
	}
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
// command that applies the change instead of failing the registry update. A
// running interface while the VPN add-on is off (or runs another tool) is
// taken down: the overlay never outlives the Owner's opt-in.
func resyncHomeOverlayIfActive(ctx context.Context, root string, now time.Time) (string, error) {
	// A coordinated tool is reconciled here as well, so a revocation at the
	// console removes the node at once instead of waiting for the service.
	if selection, err := identity.LoadHomeVPNSelection(root); err == nil && selection.Tool != identity.HomeVPNToolWireGuard {
		if runtime.GOOS != "linux" || os.Geteuid() != 0 {
			return "pending: stackkit-vpn.service removes the " + selection.Tool + " peer within seconds", nil
		}
		if homeOverlayLinkExists(ctx) || homeOverlayUnitInstalled() {
			if err := teardownHomeOverlayInterface(ctx); err != nil {
				return "", err
			}
		}
		host, err := loadVPNHostFor(root, selection)
		if err != nil {
			return "", err
		}
		driver, err := newVPNDriver(host)
		if err != nil {
			return "", err
		}
		removals, err := newVPNPeerService(host, driver).reconcile(ctx)
		if err != nil {
			return "", fmt.Errorf("the %s peers were not reconciled: %w", selection.Tool, err)
		}
		return fmt.Sprintf("reconciled: %d %s nodes and %d join keys removed", len(removals.Nodes), selection.Tool, len(removals.Keys)), nil
	}
	if runtime.GOOS != "linux" || !homeOverlayLinkExists(ctx) {
		return "not-running: the VPN add-on's WireGuard interface is not up", nil
	}
	if os.Geteuid() != 0 {
		return "pending: run 'sudo stackkit device overlay up' to apply this change to the running overlay", nil
	}
	if requireHomeOverlaySelected(root) != nil {
		if err := teardownHomeOverlayInterface(ctx); err != nil {
			return "", err
		}
		return "removed: the VPN add-on is not on with the WireGuard tool, so its interface was taken down", nil
	}
	result, err := ensureHomeOverlayInterface(ctx, root, now)
	if err != nil {
		return "", err
	}
	return result.Action, nil
}

// teardownHomeOverlayInterface stops the WireGuard interface, its boot unit,
// the resync watcher and the guard, and removes their files.
func teardownHomeOverlayInterface(ctx context.Context) error {
	var failures []string
	for _, unit := range []string{homeOverlayResyncUnit + ".path", homeOverlayUnit()} {
		if _, err := homeOverlayRun(ctx, "systemctl", "disable", "--now", unit); err != nil && !strings.Contains(err.Error(), "not loaded") &&
			!strings.Contains(err.Error(), "does not exist") {
			failures = append(failures, err.Error())
		}
	}
	if homeOverlayLinkExists(ctx) {
		if _, err := homeOverlayRun(ctx, "ip", "link", "del", "dev", identity.HomeOverlayInterfaceName); err != nil {
			failures = append(failures, err.Error())
		}
	}
	for _, file := range []string{homeOverlayConfigFile(), filepath.Join(homeOverlaySystemdDir, homeOverlayResyncUnit+".service"), filepath.Join(homeOverlaySystemdDir, homeOverlayResyncUnit+".path")} {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err.Error())
		}
	}
	if err := removeVPNGuard(ctx); err != nil {
		failures = append(failures, err.Error())
	}
	if err := revokeHomeOverlayKeyRead(ctx); err != nil {
		failures = append(failures, err.Error())
	}
	_, _ = homeOverlayRun(ctx, "systemctl", "daemon-reload")
	if len(failures) > 0 {
		return errors.New("VPN add-on: the WireGuard interface was not fully removed: " + strings.Join(failures, "; "))
	}
	return nil
}

// homeOverlayNodeAddress is the node's overlay address (#VPNAddOnV1
// wireguard.nodeAddress).
const homeOverlayNodeAddress = "10.8.0.1"

// homeOverlayUnitInstalled reports a wg-quick configuration or boot unit of
// the overlay, which would bring the interface back at boot.
func homeOverlayUnitInstalled() bool {
	if _, err := os.Stat(homeOverlayConfigFile()); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join("/etc/systemd/system/multi-user.target.wants", homeOverlayUnit()))
	return err == nil
}

// homeOverlayNodeKeyPath is the node key's real path in custody, symlinks
// resolved, which is the path a confinement grant must name.
func homeOverlayNodeKeyPath(root string) (string, error) {
	absolute, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(identity.HomeOverlayNodeKeyFile)))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("device overlay up: the node key is not in custody: %w", err)
	}
	return resolved, nil
}

// The distribution's AppArmor profile for wg (Ubuntu 26.04) admits only
// /etc/wireguard and includes local/wg for site-specific additions. The
// add-on keeps the node key in custody and adds exactly one read rule for it
// there, in a marked block it alone manages; the profile stays enforced.
const (
	homeOverlayAppArmorProfile = "/etc/apparmor.d/wg"
	homeOverlayAppArmorLocal   = "/etc/apparmor.d/local/wg"
	homeOverlayAppArmorBegin   = "# BEGIN stackkit VPN add-on (managed by stackkit; do not edit)"
	homeOverlayAppArmorEnd     = "# END stackkit VPN add-on"
)

func grantHomeOverlayKeyRead(ctx context.Context, root string) error {
	if _, err := os.Stat(homeOverlayAppArmorProfile); err != nil {
		return nil // no wg profile on this distribution
	}
	keyPath, err := homeOverlayNodeKeyPath(root)
	if err != nil {
		return err
	}
	if strings.ContainsAny(keyPath, " \t\n\"'#,{}[]*?^") {
		return errors.New("device overlay up: the workspace path holds characters an AppArmor rule cannot carry; move it to a plain path")
	}
	return writeHomeOverlayAppArmorBlock(ctx, keyPath+" r,\n")
}

func revokeHomeOverlayKeyRead(ctx context.Context) error {
	if _, err := os.Stat(homeOverlayAppArmorLocal); err != nil {
		return nil
	}
	return writeHomeOverlayAppArmorBlock(ctx, "")
}

// writeHomeOverlayAppArmorBlock replaces the managed block of local/wg with
// rules (empty removes it), keeps every other line, and reloads the wg
// profile when the file changed and AppArmor is active.
func writeHomeOverlayAppArmorBlock(ctx context.Context, rules string) error {
	current, err := os.ReadFile(homeOverlayAppArmorLocal)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("VPN add-on: read %s: %w", homeOverlayAppArmorLocal, err)
	}
	// Every line outside the managed block stays exactly as the Owner wrote
	// it; a block without its end marker is refused rather than guessed.
	var kept []string
	inBlock := false
	if len(current) > 0 {
		for _, line := range strings.Split(strings.TrimSuffix(string(current), "\n"), "\n") {
			switch {
			case line == homeOverlayAppArmorBegin && !inBlock:
				inBlock = true
			case line == homeOverlayAppArmorEnd && inBlock:
				inBlock = false
			case !inBlock:
				kept = append(kept, line)
			}
		}
	}
	if inBlock {
		return fmt.Errorf("VPN add-on: %s has the add-on's begin marker without its end marker; fix the file by hand", homeOverlayAppArmorLocal)
	}
	next := strings.Join(kept, "\n")
	if len(kept) > 0 {
		next += "\n"
	}
	if rules != "" {
		next += homeOverlayAppArmorBegin + "\n" + rules + homeOverlayAppArmorEnd + "\n"
	}
	if next == string(current) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(homeOverlayAppArmorLocal), 0o755); err != nil {
		return fmt.Errorf("VPN add-on: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(homeOverlayAppArmorLocal), ".wg-stackkit-*")
	if err != nil {
		return fmt.Errorf("VPN add-on: write %s: %w", homeOverlayAppArmorLocal, err)
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0o644); err == nil {
		_, err = temp.WriteString(next)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), homeOverlayAppArmorLocal)
	}
	if err != nil {
		return fmt.Errorf("VPN add-on: write %s: %w", homeOverlayAppArmorLocal, err)
	}
	if _, err := os.Stat("/sys/kernel/security/apparmor/profiles"); err != nil {
		return nil // AppArmor is not active; the rule takes effect when it is
	}
	if _, err := homeOverlayLookPath("apparmor_parser"); err != nil {
		return errors.New("VPN add-on: AppArmor is active but apparmor_parser is missing; the wg profile cannot load the custody grant")
	}
	if _, err := homeOverlayRun(ctx, "apparmor_parser", "-r", homeOverlayAppArmorProfile); err != nil {
		return fmt.Errorf("VPN add-on: reload the wg AppArmor profile with the custody grant: %w", err)
	}
	return nil
}

// The overlay resync units apply a revocation made anywhere, also through the
// identity routes of the Basement core, which runs without root and outside
// the host network: a systemd path unit watches the device registry and the
// peer registry, and its service reruns "device overlay up" as root, which
// drops a revoked device's peer with wg syncconf within seconds (homelab
// identity target §6.2, revocation target 60 s).
const (
	homeOverlayResyncUnit = "stackkit-overlay-resync"
	homeOverlaySystemdDir = "/etc/systemd/system"
)

func homeOverlayResyncUnits(root, binary string) (service, path string) {
	service = "[Unit]\nDescription=StackKits VPN add-on peer resync after a device registry change\n" +
		"After=wg-quick@" + identity.HomeOverlayInterfaceName + ".service\n\n" +
		"[Service]\nType=oneshot\nWorkingDirectory=" + root + "\nExecStart=" + binary + " --chdir " + root + " device overlay up\n"
	path = "[Unit]\nDescription=Watch the StackKits device registry for revocations\n\n" +
		"[Path]\nPathChanged=" + filepath.Join(root, identity.HomeDeviceRegistryFile) + "\n" +
		"PathChanged=" + filepath.Join(root, identity.HomeOverlayPeerRegistryFile) + "\n" +
		"PathChanged=" + filepath.Join(root, identity.HomeVPNSelectionFile) + "\n" +
		"Unit=" + homeOverlayResyncUnit + ".service\n\n[Install]\nWantedBy=multi-user.target\n"
	return service, path
}

func installHomeOverlayResync(ctx context.Context, root string) error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("device overlay up: locate the stackkit executable for the resync unit: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(binary); resolveErr == nil {
		binary = resolved
	}
	if strings.ContainsAny(root+binary, " \t\n\"'%$") {
		return errors.New("device overlay up: the workspace or stackkit path holds characters a systemd unit cannot carry; move it to a plain path")
	}
	service, path := homeOverlayResyncUnits(root, binary)
	changed := false
	for name, content := range map[string]string{homeOverlayResyncUnit + ".service": service, homeOverlayResyncUnit + ".path": path} {
		file := filepath.Join(homeOverlaySystemdDir, name)
		if current, readErr := os.ReadFile(file); readErr == nil && string(current) == content {
			continue
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			return fmt.Errorf("device overlay up: write %s: %w", file, err)
		}
		changed = true
	}
	if changed {
		if _, err := homeOverlayRun(ctx, "systemctl", "daemon-reload"); err != nil {
			return fmt.Errorf("device overlay up: reload systemd for the resync unit: %w", err)
		}
	}
	if _, err := homeOverlayRun(ctx, "systemctl", "enable", "--now", homeOverlayResyncUnit+".path"); err != nil {
		return fmt.Errorf("device overlay up: enable the resync watcher: %w", err)
	}
	return nil
}
