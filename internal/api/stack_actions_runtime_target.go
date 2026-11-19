package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	sshpin "github.com/kombifyio/stackkits/internal/ssh"
)

type preparedRuntimeTargetStackAction struct {
	dockerHost    string
	env           []string
	target        *stackActionTarget
	keyPath       string
	workspaceRoot string
	hostKey       *sshpin.HostKeyPin
}

func prepareStackActionRemoteTarget(ctx context.Context, tofuDir string, target *stackActionTarget, resolver StackActionReferenceResolver) (*preparedRuntimeTargetStackAction, func(), error) {
	target = normalizeStackActionTarget(target)
	if target == nil {
		return nil, nil, nil
	}
	dockerHost, err := runtimeTargetDockerHostStackAction(target)
	if err != nil {
		return nil, nil, err
	}
	keyPath, homeDir, hostKey, cleanup, err := materializeRuntimeTargetSSHKeyStackAction(ctx, target, resolver)
	if err != nil {
		return nil, cleanup, err
	}
	if err := bootstrapRuntimeTargetDockerStackAction(ctx, target, keyPath); err != nil {
		return nil, cleanup, err
	}
	workspaceRoot := runtimeTargetWorkspaceRootStackAction(tofuDir)
	if err := writeRuntimeTargetDockerHostTFVarsStackAction(tofuDir, dockerHost, workspaceRoot); err != nil {
		return nil, cleanup, err
	}
	env := []string{"DOCKER_HOST=" + dockerHost}
	if homeDir != "" {
		env = append(env, "HOME="+homeDir)
	}
	if keyPath != "" {
		env = append(env, "DOCKER_SSH_COMMAND="+runtimeTargetSSHCommandStackAction(target, keyPath))
	}
	return &preparedRuntimeTargetStackAction{dockerHost: dockerHost, env: env, target: target, keyPath: keyPath, workspaceRoot: workspaceRoot, hostKey: hostKey}, cleanup, nil
}

func materializeRuntimeTargetSSHKeyStackAction(ctx context.Context, target *stackActionTarget, resolver StackActionReferenceResolver) (string, string, *sshpin.HostKeyPin, func(), error) {
	if target == nil {
		return "", "", nil, nil, fmt.Errorf("runtime target is required")
	}
	accessRef, err := normalizeStackActionReference(target.AccessProfileRef, stackActionScopeRuntimeSSH, time.Now())
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("invalid runtime access_profile_ref: %w", err)
	}
	if resolver == nil {
		return "", "", nil, nil, fmt.Errorf("StackAction reference resolver is not configured")
	}
	material, err := resolver.ResolveAccessProfile(ctx, *accessRef)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("resolve runtime access profile: %w", err)
	}
	key := append([]byte(nil), material.PrivateKey...)
	defer clear(key)
	hostKey, err := sshpin.ResolveHostKey(ctx, target.Host, target.Port, target.HostKey, target.HostKeyFirstContact)
	if err != nil {
		return "", "", nil, nil, err
	}
	if len(bytes.TrimSpace(key)) == 0 {
		return "", "", nil, nil, fmt.Errorf("resolved runtime access profile has no SSH private key")
	}
	dir, err := os.MkdirTemp("", "stackkits-runtime-ssh-")
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("create runtime SSH key dir: %w", err)
	}
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, nil, fmt.Errorf("create runtime SSH config dir: %w", err)
	}
	keyPath := filepath.Join(sshDir, "id_runtime")
	keyData := append(bytes.TrimSpace(key), '\n')
	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, nil, fmt.Errorf("write runtime SSH key: %w", err)
	}
	if err := hostKey.WriteKnownHosts(runtimeKnownHostsPathStackAction(keyPath)); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, nil, fmt.Errorf("write runtime SSH known_hosts: %w", err)
	}
	config := runtimeSSHConfigStackAction(target, keyPath)
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(config), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, nil, fmt.Errorf("write runtime SSH config: %w", err)
	}
	restoreUserConfig, err := installRuntimeUserSSHConfigStackAction(target, keyPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, nil, err
	}
	cleanup := func() {
		if restoreUserConfig != nil {
			restoreUserConfig()
		}
		_ = os.RemoveAll(dir)
	}
	return keyPath, dir, hostKey, cleanup, nil
}

func runtimeSSHConfigStackAction(target *stackActionTarget, keyPath string) string {
	return fmt.Sprintf(`Host %s
  HostName %s
  User %s
  IdentityFile %s
  IdentitiesOnly yes
  StrictHostKeyChecking yes
  UserKnownHostsFile %s
  GlobalKnownHostsFile /dev/null
  LogLevel ERROR
  Port %d
`, target.Host, target.Host, target.User, keyPath, runtimeKnownHostsPathStackAction(keyPath), target.Port)
}

func installRuntimeUserSSHConfigStackAction(target *stackActionTarget, keyPath string) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil, nil
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return nil, fmt.Errorf("create user SSH config dir: %w", err)
	}
	configPath := filepath.Join(sshDir, "config")
	previous, readErr := os.ReadFile(configPath)
	existed := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read user SSH config: %w", readErr)
	}
	next := append([]byte(runtimeSSHConfigStackAction(target, keyPath)+"\n"), previous...)
	// #nosec G703 -- configPath is fixed to $HOME/.ssh/config for the local operator user.
	if err := os.WriteFile(configPath, next, 0600); err != nil {
		return nil, fmt.Errorf("write user SSH config: %w", err)
	}
	return func() {
		if existed {
			// #nosec G703 -- configPath is fixed to $HOME/.ssh/config for the local operator user.
			_ = os.WriteFile(configPath, previous, 0600)
		} else {
			_ = os.Remove(configPath)
		}
	}, nil
}

func bootstrapRuntimeTargetDockerStackAction(ctx context.Context, target *stackActionTarget, keyPath string) error {
	if keyPath == "" {
		return fmt.Errorf("runtime target SSH key path is required")
	}
	args := runtimeTargetSSHArgsStackAction(target, keyPath)
	script := `set -eu
if command -v sudo >/dev/null 2>&1 && [ "$(id -u)" != "0" ]; then SUDO="sudo -n"; else SUDO=""; fi
if command -v cloud-init >/dev/null 2>&1; then
  timeout 300 cloud-init status --wait >/dev/null 2>&1 || true
fi
apt_busy() {
  pgrep -x apt >/dev/null 2>&1 ||
  pgrep -x apt-get >/dev/null 2>&1 ||
  pgrep -x dpkg >/dev/null 2>&1 ||
  pgrep -f unattended-upgrade >/dev/null 2>&1
}
wait_for_apt() {
  for i in $(seq 1 90); do
    if ! apt_busy; then return 0; fi
    sleep 2
  done
  return 1
}
apt_get() {
  wait_for_apt
  $SUDO env DEBIAN_FRONTEND=noninteractive apt-get "$@"
}
install_docker() {
  for i in $(seq 1 12); do
    wait_for_apt || true
    if curl -fsSL https://get.docker.com | $SUDO sh; then return 0; fi
    sleep 10
  done
  curl -fsSL https://get.docker.com | $SUDO sh
}
if ! command -v docker >/dev/null 2>&1; then
  if ! command -v curl >/dev/null 2>&1; then
    apt_get update
    apt_get install -y ca-certificates curl
  fi
  install_docker
fi
if [ "$(id -u)" != "0" ]; then
  $SUDO usermod -aG docker "$(id -un)" || true
fi
$SUDO systemctl enable --now docker >/dev/null 2>&1 || true
for i in $(seq 1 60); do
  if $SUDO docker info >/dev/null 2>&1; then exit 0; fi
  sleep 2
done
$SUDO docker info >/dev/null`
	args = append(args, "sh", "-c", script)
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	var lastOutput string
	var lastErr error
	for attempt := 1; attempt <= 8; attempt++ {
		cmd := exec.CommandContext(runCtx, "ssh", args...) // #nosec G204 -- command args are assembled without shell interpolation.
		output, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = err
		lastOutput = strings.TrimSpace(string(output))
		if sshpin.SSHVerificationFailed(lastOutput) {
			// The pinned key was refused; waiting will not change the host.
			return &sshpin.HostKeyError{Reason: sshpin.ReasonHostKeyMismatch, Address: target.Host, Err: fmt.Errorf("bootstrap remote Docker over SSH: %w", err)}
		}
		if runCtx.Err() != nil {
			break
		}
		select {
		case <-runCtx.Done():
			return fmt.Errorf("bootstrap remote Docker over SSH: %w: %s", lastErr, lastOutput)
		case <-time.After(time.Duration(attempt) * 10 * time.Second):
		}
	}
	return fmt.Errorf("bootstrap remote Docker over SSH: %w: %s", lastErr, lastOutput)
}

func writeRuntimeTargetDockerHostTFVarsStackAction(tofuDir, dockerHost string, workspaceRoot ...string) error {
	tfvarsPath := filepath.Join(filepath.Clean(tofuDir), "terraform.tfvars.json")
	values := map[string]any{}
	if data, err := os.ReadFile(tfvarsPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("parse terraform.tfvars.json: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read terraform.tfvars.json: %w", err)
	}
	values["docker_host"] = dockerHost
	if len(workspaceRoot) > 0 && strings.TrimSpace(workspaceRoot[0]) != "" {
		values["workspace_root"] = strings.TrimSpace(workspaceRoot[0])
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal terraform.tfvars.json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(tfvarsPath, data, 0600); err != nil {
		return fmt.Errorf("write terraform.tfvars.json: %w", err)
	}
	return nil
}

func runtimeTargetWorkspaceRootStackAction(tofuDir string) string {
	workspaceDir := filepath.Clean(filepath.Join(filepath.Clean(tofuDir), ".."))
	sum := sha256.Sum256([]byte(workspaceDir))
	hash := fmt.Sprintf("%x", sum[:])
	return "/opt/stackkits/runtime-workspaces/" + hash[:16]
}
