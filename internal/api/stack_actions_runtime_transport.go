package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	sshpin "github.com/kombifyio/stackkits/internal/ssh"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
)

// runtimeTargetDockerHost accepts only the Docker-over-SSH transport derived
// from the validated runtime target. A caller must not redirect a managed
// rollout/observation at arbitrary tcp:// or unix:// Docker endpoints.
func runtimeTargetDockerHostStackAction(target *stackActionTarget) (string, error) {
	host, user, port, err := validatedRuntimeTargetDockerEndpointStackAction(target)
	if err != nil {
		return "", err
	}
	if dockerHost := strings.TrimSpace(target.DockerHost); dockerHost != "" {
		if err := validateRuntimeTargetDockerHostOverrideStackAction(dockerHost, user, host, port); err != nil {
			return "", err
		}
	}
	return runtimeTargetDockerSSHURLStackAction(user, host, port), nil
}

func validatedRuntimeTargetDockerEndpointStackAction(target *stackActionTarget) (host, user string, port int, err error) {
	if target == nil {
		return "", "", 0, fmt.Errorf("runtime target is required for Docker transport")
	}
	host, err = validateRuntimeTargetHostStackAction(target.Host)
	if err != nil {
		return "", "", 0, err
	}
	user, err = validateRuntimeTargetSSHUserStackAction(target.User)
	if err != nil {
		return "", "", 0, err
	}
	port, err = runtimeTargetSSHPortStackAction(target.Port)
	if err != nil {
		return "", "", 0, err
	}
	return host, user, port, nil
}

func runtimeTargetSSHPortStackAction(port int) (int, error) {
	if port <= 0 {
		return 22, nil
	}
	if port > 65535 {
		return 0, fmt.Errorf("runtime target SSH port %d is invalid", port)
	}
	return port, nil
}

func validateRuntimeTargetDockerHostOverrideStackAction(value, user, host string, port int) error {
	parsed, err := parseRuntimeTargetDockerSSHURLStackAction(value)
	if err != nil {
		return err
	}
	if err := validateRuntimeTargetDockerHostUserStackAction(parsed, user); err != nil {
		return err
	}
	if err := validateRuntimeTargetDockerHostPathStackAction(parsed); err != nil {
		return err
	}
	if err := validateRuntimeTargetDockerHostHostStackAction(parsed, host); err != nil {
		return err
	}
	return validateRuntimeTargetDockerHostPortStackAction(parsed, port)
}

func parseRuntimeTargetDockerSSHURLStackAction(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("runtime target docker_host must be a validated ssh:// URL")
	}
	if parsed.Scheme != "ssh" || parsed.Opaque != "" {
		return nil, fmt.Errorf("runtime target docker_host must be a validated ssh:// URL")
	}
	return parsed, nil
}

func validateRuntimeTargetDockerHostUserStackAction(parsed *url.URL, user string) error {
	if parsed.User == nil || parsed.User.Username() != user {
		return fmt.Errorf("runtime target docker_host user must match runtime target user")
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return fmt.Errorf("runtime target docker_host must not contain credentials")
	}
	return nil
}

func validateRuntimeTargetDockerHostPathStackAction(parsed *url.URL) error {
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("runtime target docker_host must not contain a path, query, or fragment")
	}
	return nil
}

func validateRuntimeTargetDockerHostHostStackAction(parsed *url.URL, host string) error {
	overrideHost, err := validateRuntimeTargetHostStackAction(parsed.Hostname())
	if err != nil {
		return fmt.Errorf("runtime target docker_host must match the validated runtime target host")
	}
	if !runtimeTargetHostsEqualStackAction(host, overrideHost) {
		return fmt.Errorf("runtime target docker_host must match the validated runtime target host")
	}
	return nil
}

func validateRuntimeTargetDockerHostPortStackAction(parsed *url.URL, port int) error {
	overridePort, err := runtimeTargetDockerHostOverridePortStackAction(parsed)
	if err != nil {
		return err
	}
	if overridePort != port {
		return fmt.Errorf("runtime target docker_host port must match runtime target port")
	}
	return nil
}

func runtimeTargetDockerHostOverridePortStackAction(parsed *url.URL) (int, error) {
	rawPort := parsed.Port()
	if rawPort == "" {
		return 22, nil
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("runtime target docker_host SSH port is invalid")
	}
	return port, nil
}

func runtimeTargetDockerSSHURLStackAction(user, host string, port int) string {
	authority := host
	if port != 22 {
		authority = net.JoinHostPort(host, strconv.Itoa(port))
	} else if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
		authority = "[" + addr.String() + "]"
	}
	return (&url.URL{Scheme: "ssh", User: url.User(user), Host: authority}).String()
}

func validateRuntimeTargetHostStackAction(value string) (string, error) {
	host := strings.TrimSpace(value)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n/@?#\\") {
		return "", fmt.Errorf("runtime target host is invalid")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String(), nil
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || len(host) > 253 {
		return "", fmt.Errorf("runtime target host is invalid")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("runtime target host is invalid")
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return "", fmt.Errorf("runtime target host is invalid")
			}
		}
	}
	return host, nil
}

// runtimeTargetSSHUserPattern admits portable POSIX login names only. A
// leading '-' would let the value reach ssh(1) as an option such as
// -oProxyCommand, and shell or config metacharacters have no place in a login.
var runtimeTargetSSHUserPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)

func validateRuntimeTargetSSHUserStackAction(value string) (string, error) {
	user := strings.TrimSpace(value)
	if !runtimeTargetSSHUserPattern.MatchString(user) {
		return "", fmt.Errorf("runtime target SSH user is invalid")
	}
	return user, nil
}

func runtimeTargetHostsEqualStackAction(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftAddr, leftIsIP := netip.ParseAddr(left)
	rightAddr, rightIsIP := netip.ParseAddr(right)
	if leftIsIP == nil || rightIsIP == nil {
		return leftIsIP == nil && rightIsIP == nil && leftAddr == rightAddr
	}
	return strings.EqualFold(left, right)
}

func runtimeTargetSSHCommandStackAction(target *stackActionTarget, keyPath string) string {
	return strings.Join(runtimeTargetSSHBaseArgsStackAction(target, keyPath), " ")
}

// runtimeTargetSSHArgsStackAction ends option parsing before the destination
// so a target value can never be read as an ssh(1) option.
func runtimeTargetSSHArgsStackAction(target *stackActionTarget, keyPath string) []string {
	return append(runtimeTargetSSHBaseArgsStackAction(target, keyPath), "--", target.User+"@"+target.Host)
}

func syncRuntimeTargetWorkspaceStackAction(ctx context.Context, remote *preparedRuntimeTargetStackAction, tofuDir string) error {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" || strings.TrimSpace(remote.workspaceRoot) == "" {
		return nil
	}
	workspaceDir := filepath.Clean(filepath.Join(filepath.Clean(tofuDir), ".."))
	info, err := os.Stat(workspaceDir)
	if err != nil {
		return fmt.Errorf("stat local runtime workspace: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local runtime workspace %q is not a directory", workspaceDir)
	}

	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	tarCmd := exec.CommandContext(syncCtx, "tar", runtimeTargetWorkspaceArchiveArgsStackAction(workspaceDir)...) // #nosec G204 -- workspaceDir is a local path, passed as an argv value.
	pipe, err := tarCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("prepare runtime workspace archive: %w", err)
	}

	script := runtimeTargetWorkspaceSyncScriptStackAction(remote.workspaceRoot)
	sshArgs := append(runtimeTargetSSHArgsStackAction(remote.target, remote.keyPath), "sh", "-c", shellQuoteStackAction(script))
	sshCmd := exec.CommandContext(syncCtx, "ssh", sshArgs...) // #nosec G204 -- command args are assembled without shell interpolation except a quoted constant path.
	sshCmd.Stdin = pipe

	var tarErr bytes.Buffer
	var sshErr bytes.Buffer
	tarCmd.Stderr = &tarErr
	sshCmd.Stderr = &sshErr

	if err := sshCmd.Start(); err != nil {
		return fmt.Errorf("start runtime workspace sync target: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		_ = sshCmd.Process.Kill()
		return fmt.Errorf("start runtime workspace archive: %w", err)
	}
	tarWaitErr := tarCmd.Wait()
	sshWaitErr := sshCmd.Wait()
	if tarWaitErr != nil {
		return fmt.Errorf("archive runtime workspace: %w: %s", tarWaitErr, strings.TrimSpace(tarErr.String()))
	}
	if sshWaitErr != nil {
		return fmt.Errorf("copy runtime workspace to target: %w: %s", sshWaitErr, strings.TrimSpace(sshErr.String()))
	}
	return nil
}

func runtimeTargetWorkspaceArchiveArgsStackAction(workspaceDir string) []string {
	excludes := []string{
		".git",
		"*/.git",
		".terraform",
		"*/.terraform",
		"terraform.tfstate",
		"terraform.tfstate.backup",
		"*.tfstate",
		"*.tfstate.backup",
		"node_modules",
		"*/node_modules",
		".stackkit/logs",
		"*/.stackkit/logs",
		"artifacts",
		"*/artifacts",
		"coverage",
		"*/coverage",
		"dist",
		"*/dist",
		"build",
		"*/build",
	}
	args := []string{"-C", workspaceDir}
	for _, exclude := range excludes {
		args = append(args, "--exclude="+exclude)
	}
	return append(args, "-cf", "-", ".")
}

func runtimeTargetWorkspaceSyncScriptStackAction(workspaceRoot string) string {
	remotePath := shellQuoteStackAction(workspaceRoot)
	return "rm -rf -- " + remotePath + " && mkdir -p " + remotePath + " && tar -C " + remotePath + " -xf -"
}

func shellQuoteStackAction(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func runtimeTargetSSHBaseArgsStackAction(target *stackActionTarget, keyPath string) []string {
	return []string{
		"-i", keyPath,
		"-p", strconv.Itoa(target.Port),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + runtimeKnownHostsPathStackAction(keyPath),
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=20",
	}
}

// runtimeKnownHostsPathStackAction is the per-operation known_hosts file that
// holds exactly the pinned host key; it lives next to the materialized key and
// is removed with it.
func runtimeKnownHostsPathStackAction(keyPath string) string {
	return filepath.Join(filepath.Dir(keyPath), "known_hosts")
}

// validateRuntimeTargetHostKeyStackAction refuses a runtime target that has no
// pinned SSH host key and did not explicitly request first-contact pinning.
func validateRuntimeTargetHostKeyStackAction(target *stackActionTarget) *skerrors.StackKitError {
	hostKey := strings.TrimSpace(target.HostKey)
	if hostKey == "" {
		if target.HostKeyFirstContact {
			return nil
		}
		return skerrors.NewValidationError(
			sshpin.ReasonHostKeyRequired,
			"runtime_target host_key is required: supply the expected SSH host key, or set host_key_first_contact to pin the presented key for this action",
		)
	}
	if _, err := sshpin.ParseHostKey(hostKey); err != nil {
		return skerrors.NewValidationError(
			sshpin.ReasonHostKeyInvalid,
			"runtime_target host_key must be one OpenSSH public key",
			skerrors.WithField("error", err.Error()),
		)
	}
	return nil
}

// recordRuntimeTargetHostKeyStackAction writes the pin into the action
// evidence. A first-contact pin is named explicitly so the receipt shows that
// the key was observed rather than supplied.
func recordRuntimeTargetHostKeyStackAction(resp *stackActionResponse, remote *preparedRuntimeTargetStackAction) {
	if remote == nil || remote.hostKey == nil {
		return
	}
	source := "supplied"
	if remote.hostKey.FirstContact {
		source = "first-contact"
	}
	detail := fmt.Sprintf("%s pinned %s (%s)", remote.hostKey.Address, remote.hostKey.Fingerprint, source)
	if remote.hostKey.FirstContact {
		slog.Warn("stackaction ssh host key pinned on first contact", "pin", detail)
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "ssh_host_key", Status: stackaction.CheckStatusOK, Detail: detail})
}

// runtimeTargetPrepareErrorStackAction keeps the host-key reason as the error
// code so callers can tell a pin refusal from a transport failure.
func runtimeTargetPrepareErrorStackAction(fallbackCode, message string, err error) *skerrors.StackKitError {
	if reason := sshpin.HostKeyReason(err); reason != "" {
		return tofuActionErrorStackAction(reason, "Runtime target SSH host key was refused", err, "")
	}
	return tofuActionErrorStackAction(fallbackCode, message, err, "")
}
