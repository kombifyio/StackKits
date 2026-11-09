package nativehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The Basement core's identity workspace holds verification custody only
// (homelab identity target §4, §9; StackKits enpy). Apply prepares what the
// core mounts (internal/architecturev2renderer): the verification custody
// directory (read-write: device registry, device issuer, Home trust, sessions,
// app passes), the public Authority record (read-only) and the directory of
// the host-side identity signer's socket, and runs that signer as a systemd
// service with the Owner custody, the step-ca root and the Pocket ID admin
// key, which never enter the container.
const (
	coreIdentitySignerUnit       = "stackkit-identity-signer.service"
	coreIdentitySignerSystemdDir = "/etc/systemd/system"
	// CoreIdentitySignerSocketDir is below the workspace; the core mounts it
	// read-only at /run/stackkit-identity-signer.
	CoreIdentitySignerSocketDir  = ".stackkit/identity-signer"
	coreIdentitySignerSocketName = "signer.sock"
)

// prepareCoreIdentityCustody creates the directories the Basement core mounts
// and writes the public Authority record, then installs and starts the
// host-side signer. An Authority without its signer fails Apply: its LAN
// enrollment listener would have no certificate, device recovery would be
// gone and the Home trust would lapse within a day.
func prepareCoreIdentityCustody(workspaceRoot string) error {
	// lan-tls/live holds the LAN-name certificate once `stackkit reach
	// lan-certificate issue` obtained it; the core mounts it read-only.
	// public-tls/live holds the public-names certificate of a public identity
	// origin (`stackkit reach public-certificate issue`); the router mounts it.
	for _, directory := range []string{".stackkit/custody/home-verifier", localevidence.HomeAuthorityCustodyRelDir, CoreIdentitySignerSocketDir, ".stackkit/custody/lan-tls/live", localevidence.PublicRouterCertificateRelDir} {
		if err := os.MkdirAll(filepath.Join(workspaceRoot, filepath.FromSlash(directory)), 0o700); err != nil {
			return fmt.Errorf("create %s for the Basement core: %w", directory, err)
		}
	}
	if _, err := localevidence.WriteHomeAuthorityPublic(workspaceRoot, time.Now()); err != nil {
		if errors.Is(err, localevidence.ErrOwnerCustodyMissing) {
			// A Fleet member is no Authority: no signer, no Authority record.
			return nil
		}
		return fmt.Errorf("write the public Authority record the core verifies against: %w", err)
	}
	if err := ensureCoreIdentitySigner(workspaceRoot); err != nil {
		return fmt.Errorf("start the host-side identity signer (stackkit-identity-signer.service), which holds the Authority's Owner custody for the Basement core: %w; run stackkit apply as root on a systemd host", err)
	}
	return nil
}

// coreIdentitySignerBinary is the signer's own copy of the stackkit release:
// root-owned, so no account that could write the CLI Apply ran from can
// replace what the service runs with the Owner custody.
const coreIdentitySignerBinary = "/usr/local/lib/stackkit/stackkit-identity-signer"

// ensureCoreIdentitySigner installs the signer's binary and systemd unit
// (only what changed) and keeps it enabled and running as the account that
// owns the custody and runs the core server; every Apply restarts it, so a
// new release replaces the running signer.
func ensureCoreIdentitySigner(workspaceRoot string) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("the signer is a systemd service on Linux that Apply installs as root")
	}
	if info, err := os.Stat("/run/systemd/system"); err != nil || !info.IsDir() {
		return errors.New("systemd is not the running init")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(source); resolveErr == nil {
		source = resolved
	}
	if strings.HasSuffix(source, ".test") {
		return errors.New("a test binary never becomes a system service")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	uid, gid, err := localevidence.StackKitServerOwner(root)
	if err != nil {
		return err
	}
	if strings.ContainsAny(root, " \t\n\"'%$\\") {
		return errors.New("the workspace path holds characters a systemd unit cannot carry; move it to a plain path")
	}
	if err := installCoreIdentitySignerBinary(source); err != nil {
		return err
	}
	socket := filepath.Join(root, filepath.FromSlash(CoreIdentitySignerSocketDir), coreIdentitySignerSocketName)
	unit := "[Unit]\nDescription=StackKits identity signer: the Authority's Owner custody for the Basement core\n" +
		"After=network-online.target docker.service\nWants=network-online.target\n\n" +
		"[Service]\nType=simple\nUser=" + strconv.Itoa(uid) + "\nGroup=" + strconv.Itoa(gid) + "\nWorkingDirectory=" + root + "\n" +
		"ExecStart=" + coreIdentitySignerBinary + " --chdir " + root + " device signer --socket " + socket + " --peer-uid " + strconv.Itoa(uid) + "\n" +
		"Restart=always\nRestartSec=5\nUMask=0077\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=full\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
	path := filepath.Join(coreIdentitySignerSystemdDir, coreIdentitySignerUnit)
	changed := true
	if current, readErr := os.ReadFile(path); readErr == nil && string(current) == unit { //nolint:gosec // fixed systemd unit path
		changed = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if changed {
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil { //nolint:gosec // a systemd unit is world-readable
			return err
		}
		if err := runSystemctl(ctx, "daemon-reload"); err != nil {
			return err
		}
	}
	if err := runSystemctl(ctx, "enable", coreIdentitySignerUnit); err != nil {
		return err
	}
	if err := runSystemctl(ctx, "restart", coreIdentitySignerUnit); err != nil {
		return err
	}
	return runSystemctl(ctx, "is-active", "--quiet", coreIdentitySignerUnit)
}

// installCoreIdentitySignerBinary copies the running release into the
// root-owned signer path (0755 under a root-owned 0755 directory), replacing
// it atomically when its content changed.
func installCoreIdentitySignerBinary(source string) error {
	directory := filepath.Dir(coreIdentitySignerBinary)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if err := os.Chown(directory, 0, 0); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o755); err != nil { //nolint:gosec // a root-owned program directory
		return err
	}
	want, err := os.ReadFile(source) //nolint:gosec // the running stackkit release
	if err != nil {
		return err
	}
	if current, readErr := os.ReadFile(coreIdentitySignerBinary); readErr == nil && bytes.Equal(current, want) {
		return nil
	}
	temporary, err := os.CreateTemp(directory, ".stackkit-identity-signer-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(want); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chown(temporary.Name(), 0, 0); err != nil {
		return err
	}
	if err := os.Chmod(temporary.Name(), 0o755); err != nil { //nolint:gosec // an executable program
		return err
	}
	return os.Rename(temporary.Name(), coreIdentitySignerBinary)
}

func runSystemctl(ctx context.Context, args ...string) error {
	output, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput() //nolint:gosec // fixed program, fixed arguments
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
