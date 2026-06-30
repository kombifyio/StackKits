package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
)

const securityBaselineEvidencePath = ".stackkit/security-baseline.json"
const securityBaselineSchemaVersion = "stackkit.security-baseline/v1"
const securityBaselineMode = "public-beta"
const securityBaselineTimeout = 10 * time.Minute

type securityBaselineConfig struct {
	SSHPort         int
	PermitRootLogin string
	MaxAuthTries    int
}

type securityBaselineEvidence struct {
	SchemaVersion string            `json:"schemaVersion"`
	Status        string            `json:"status"`
	Mode          string            `json:"mode"`
	AppliedAt     string            `json:"appliedAt,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	Controls      map[string]string `json:"controls,omitempty"`
}

func applyPublicBetaSecurityBaseline(ctx context.Context, wd string, spec *models.StackSpec) error {
	if !securityBaselineApplies(spec) {
		return nil
	}
	if disabledByEnv("STACKKIT_SECURITY_BASELINE") {
		printWarning("Security baseline skipped because STACKKIT_SECURITY_BASELINE disables it")
		return writeSecurityBaselineEvidence(wd, securityBaselineEvidence{
			SchemaVersion: securityBaselineSchemaVersion,
			Status:        "skipped",
			Mode:          securityBaselineMode,
			Reason:        "disabled-by-env",
		})
	}
	if runtime.GOOS != "linux" {
		printWarning("Security baseline skipped on non-Linux host %s", runtime.GOOS)
		return writeSecurityBaselineEvidence(wd, securityBaselineEvidence{
			SchemaVersion: securityBaselineSchemaVersion,
			Status:        "skipped",
			Mode:          securityBaselineMode,
			Reason:        "non-linux-host",
		})
	}

	cfg := securityBaselineConfigForSpec(spec)
	baselineCtx, cancel := context.WithTimeout(ctx, securityBaselineTimeout)
	defer cancel()

	printInfo("Applying BaseKit public-beta security baseline...")
	rolloutEvent("security_baseline", "started", "applying host security baseline", nil)
	cmd := exec.CommandContext(baselineCtx, "sh", "-c", securityBaselineScript(cfg))
	cmd.Dir = wd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		failure := err
		if errors.Is(baselineCtx.Err(), context.DeadlineExceeded) {
			failure = fmt.Errorf("timed out after %s: %w", securityBaselineTimeout, baselineCtx.Err())
		}
		rolloutFailure("security_baseline", failure)
		return fmt.Errorf("security baseline failed: %w\n%s", failure, redactedSecurityBaselineOutput(out.String()))
	}

	evidence, err := readSecurityBaselineEvidence(wd)
	if err != nil {
		rolloutFailure("security_baseline", err)
		return err
	}
	if err := validateSecurityBaselineEvidence(evidence); err != nil {
		rolloutFailure("security_baseline", err)
		return err
	}
	rolloutEvent("security_baseline", "succeeded", "host security baseline applied", evidence.Controls)
	printSuccess("Security baseline applied")
	return nil
}

func securityBaselineApplies(spec *models.StackSpec) bool {
	// The host security baseline is a universal Foundation contract: every
	// single-environment server deployment (any kit) gets it. Non-apt / non-Linux
	// hosts still self-skip inside the script with status "skipped".
	return spec != nil
}

func disabledByEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "off", "skip", "disabled":
		return true
	default:
		return false
	}
}

func securityBaselineConfigForSpec(spec *models.StackSpec) securityBaselineConfig {
	cfg := securityBaselineConfig{
		SSHPort:         22,
		PermitRootLogin: "prohibit-password",
		MaxAuthTries:    3,
	}
	if spec == nil {
		return cfg
	}
	if spec.SSH.Port > 0 {
		cfg.SSHPort = spec.SSH.Port
	}
	if spec.SSH.MaxAuthTries > 0 {
		cfg.MaxAuthTries = spec.SSH.MaxAuthTries
	}
	if permit := safePermitRootLogin(spec.SSH.PermitRootLogin); permit != "" {
		cfg.PermitRootLogin = permit
	}
	return cfg
}

func safePermitRootLogin(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "no", "prohibit-password", "forced-commands-only":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func securityBaselineScript(cfg securityBaselineConfig) string {
	sshPort := cfg.SSHPort
	if sshPort <= 0 || sshPort > 65535 {
		sshPort = 22
	}
	permitRootLogin := safePermitRootLogin(cfg.PermitRootLogin)
	if permitRootLogin == "" {
		permitRootLogin = "prohibit-password"
	}
	maxAuthTries := cfg.MaxAuthTries
	if maxAuthTries <= 0 {
		maxAuthTries = 3
	}

	return fmt.Sprintf(`set -eu
%s
if ! command -v apt-get >/dev/null 2>&1; then
  echo "StackKit public-beta security baseline currently supports apt-based Ubuntu hosts." >&2
  exit 1
fi
if [ "$(id -u)" -eq 0 ]; then SUDO=""; else SUDO="sudo"; fi
if [ "$(id -u)" -ne 0 ] && ! command -v sudo >/dev/null 2>&1; then
  echo "sudo is required to apply the StackKit security baseline." >&2
  exit 1
fi
SYSTEMD_RUNNING=0
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ] && [ "$(cat /proc/1/comm 2>/dev/null || true)" = "systemd" ]; then
  SYSTEMD_RUNNING=1
fi
mkdir -p .stackkit
$SUDO mkdir -p /etc/ssh/sshd_config.d /etc/fail2ban/jail.d /etc/sysctl.d /var/log/stackkit
$SUDO apt-get update -y
$SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ufw fail2ban unattended-upgrades openssh-server procps

$SUDO ufw default deny incoming
$SUDO ufw default allow outgoing
$SUDO ufw allow %[2]d/tcp comment 'StackKit SSH'
$SUDO ufw limit %[2]d/tcp comment 'StackKit SSH rate limit'
$SUDO ufw allow 80/tcp comment 'StackKit HTTP'
$SUDO ufw allow 443/tcp comment 'StackKit HTTPS'
case "${DOCKER_HOST:-}" in
  tcp://127.0.0.1:2375|tcp://localhost:2375|tcp://0.0.0.0:2375)
    $SUDO ufw allow in from 172.16.0.0/12 to any port 2375 proto tcp comment 'StackKit local Docker bridge API'
    $SUDO ufw allow in from fd00::/8 to any port 2375 proto tcp comment 'StackKit local Docker bridge API' || true
    ;;
esac
$SUDO ufw --force enable
$SUDO sh -c 'ufw status verbose > /var/log/stackkit/security-baseline-ufw.log'

if [ "$SYSTEMD_RUNNING" = "1" ]; then
  FAIL2BAN_BACKEND="systemd"
  FAIL2BAN_LOGPATH=""
else
  FAIL2BAN_BACKEND="polling"
  $SUDO touch /var/log/auth.log
  FAIL2BAN_LOGPATH="logpath = /var/log/auth.log"
fi
$SUDO tee /etc/fail2ban/jail.d/stackkit-sshd.conf >/dev/null <<'STACKKIT_FAIL2BAN'
[sshd]
enabled = true
port = %[2]d
filter = sshd
backend = STACKKIT_FAIL2BAN_BACKEND
STACKKIT_FAIL2BAN_LOGPATH
maxretry = 5
findtime = 10m
bantime = 1h
STACKKIT_FAIL2BAN
$SUDO sed -i "s/STACKKIT_FAIL2BAN_BACKEND/$FAIL2BAN_BACKEND/" /etc/fail2ban/jail.d/stackkit-sshd.conf
if [ -n "$FAIL2BAN_LOGPATH" ]; then
  $SUDO sed -i "s#STACKKIT_FAIL2BAN_LOGPATH#$FAIL2BAN_LOGPATH#" /etc/fail2ban/jail.d/stackkit-sshd.conf
else
  $SUDO sed -i "/STACKKIT_FAIL2BAN_LOGPATH/d" /etc/fail2ban/jail.d/stackkit-sshd.conf
fi
if [ "$SYSTEMD_RUNNING" = "1" ] && systemctl list-unit-files fail2ban.service >/dev/null 2>&1; then
  $SUDO systemctl enable --now fail2ban
  $SUDO systemctl restart fail2ban
elif command -v service >/dev/null 2>&1; then
  $SUDO service fail2ban restart || $SUDO fail2ban-client -x start
else
  $SUDO fail2ban-client -x start
fi
FAIL2BAN_READY=0
FAIL2BAN_WAIT=0
while [ "$FAIL2BAN_WAIT" -lt 30 ]; do
  if $SUDO sh -c 'fail2ban-client status sshd > /var/log/stackkit/security-baseline-fail2ban.log 2>&1'; then
    FAIL2BAN_READY=1
    break
  fi
  FAIL2BAN_WAIT=$((FAIL2BAN_WAIT + 1))
  sleep 1
done
if [ "$FAIL2BAN_READY" != "1" ]; then
  echo "fail2ban sshd jail did not become ready within 30s" >&2
  if [ "$SYSTEMD_RUNNING" = "1" ]; then
    $SUDO systemctl status fail2ban --no-pager >&2 || true
  fi
  $SUDO cat /var/log/stackkit/security-baseline-fail2ban.log >&2 || true
  exit 1
fi

$SUDO tee /etc/apt/apt.conf.d/20auto-upgrades >/dev/null <<'STACKKIT_UPGRADES'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
STACKKIT_UPGRADES
$SUDO tee /etc/apt/apt.conf.d/50unattended-upgrades-stackkit >/dev/null <<'STACKKIT_SECURITY_UPGRADES'
Unattended-Upgrade::Allowed-Origins {
        "${distro_id}:${distro_codename}-security";
};
Unattended-Upgrade::Automatic-Reboot "false";
STACKKIT_SECURITY_UPGRADES
if [ "$SYSTEMD_RUNNING" = "1" ] && systemctl list-unit-files unattended-upgrades.service >/dev/null 2>&1; then
  $SUDO systemctl enable --now unattended-upgrades || true
elif command -v service >/dev/null 2>&1; then
  $SUDO service unattended-upgrades restart || true
fi

$SUDO tee /etc/sysctl.d/99-stackkit-hardening.conf >/dev/null <<'STACKKIT_SYSCTL'
net.ipv4.tcp_syncookies=1
net.ipv4.conf.all.rp_filter=1
net.ipv4.conf.default.rp_filter=1
net.ipv4.conf.all.accept_redirects=0
net.ipv4.conf.default.accept_redirects=0
net.ipv4.conf.all.secure_redirects=0
net.ipv4.conf.default.secure_redirects=0
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.send_redirects=0
net.ipv4.conf.all.accept_source_route=0
net.ipv4.conf.default.accept_source_route=0
net.ipv4.conf.all.log_martians=1
kernel.kptr_restrict=1
kernel.dmesg_restrict=1
fs.protected_hardlinks=1
fs.protected_symlinks=1
fs.protected_fifos=2
fs.protected_regular=2
STACKKIT_SYSCTL
$SUDO sh -c 'sysctl --system > /var/log/stackkit/security-baseline-sysctl.log'

$SUDO rm -f /etc/ssh/sshd_config.d/99-stackkit-security-baseline.conf
$SUDO tee /etc/ssh/sshd_config.d/01-stackkit-security-baseline.conf >/dev/null <<STACKKIT_SSHD
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PermitEmptyPasswords no
PubkeyAuthentication yes
PermitRootLogin %[3]s
MaxAuthTries %[4]d
X11Forwarding no
STACKKIT_SSHD
SSHD_BIN="$(command -v sshd || true)"
if [ -z "$SSHD_BIN" ] && [ -x /usr/sbin/sshd ]; then SSHD_BIN=/usr/sbin/sshd; fi
if [ -z "$SSHD_BIN" ]; then
  echo "sshd not found after openssh-server install" >&2
  exit 1
fi
$SUDO "$SSHD_BIN" -t
if [ "$SYSTEMD_RUNNING" = "1" ] && systemctl list-unit-files ssh.service >/dev/null 2>&1; then
  $SUDO systemctl reload ssh || $SUDO systemctl restart ssh
elif command -v service >/dev/null 2>&1; then
  $SUDO service ssh reload || $SUDO service ssh restart || $SUDO pkill -HUP sshd || true
else
  $SUDO pkill -HUP sshd || true
fi

fail_stackkit_control() {
  echo "security baseline control probe failed: $1" >&2
  exit 1
}
UFW_STATUS="$($SUDO ufw status verbose 2>/dev/null || true)"
printf '%%s\n' "$UFW_STATUS" | grep -q '^Status: active$' || fail_stackkit_control "ufw is not active"
printf '%%s\n' "$UFW_STATUS" | grep -Eq 'Default:.*deny.*incoming.*allow.*outgoing' || fail_stackkit_control "ufw default policy is not deny incoming / allow outgoing"
CONTROL_FIREWALL="enabled"

$SUDO sh -c 'fail2ban-client status sshd > /var/log/stackkit/security-baseline-fail2ban.log 2>&1' || fail_stackkit_control "fail2ban sshd jail is not active"
CONTROL_FAIL2BAN="enabled"

$SUDO grep -q 'APT::Periodic::Unattended-Upgrade "1";' /etc/apt/apt.conf.d/20auto-upgrades || fail_stackkit_control "unattended upgrades are not enabled"
$SUDO grep -q '\${distro_id}:\${distro_codename}-security' /etc/apt/apt.conf.d/50unattended-upgrades-stackkit || fail_stackkit_control "security-only unattended upgrade origin is missing"
CONTROL_UNATTENDED_UPGRADES="security"

probe_sysctl() {
  key="$1"
  want="$2"
  got="$($SUDO sysctl -n "$key" 2>/dev/null || true)"
  if [ "$got" != "$want" ]; then
    echo "security baseline sysctl probe $key=$got, want $want" >&2
    return 1
  fi
  return 0
}
probe_sysctl net.ipv4.tcp_syncookies 1 || fail_stackkit_control "tcp_syncookies not applied"
probe_sysctl fs.protected_hardlinks 1 || fail_stackkit_control "protected_hardlinks not applied"
probe_sysctl fs.protected_symlinks 1 || fail_stackkit_control "protected_symlinks not applied"
CONTROL_SYSCTL="applied"

SSHD_EFFECTIVE="$($SUDO "$SSHD_BIN" -T 2>/dev/null || true)"
sshd_value() {
  printf '%%s\n' "$SSHD_EFFECTIVE" | awk -v key="$1" 'tolower($1)==tolower(key) {print tolower($2); exit}'
}
SSH_PASSWORD_AUTHENTICATION="$(sshd_value passwordauthentication)"
if [ "$SSH_PASSWORD_AUTHENTICATION" != "no" ]; then
  fail_stackkit_control "PasswordAuthentication effective value is $SSH_PASSWORD_AUTHENTICATION"
fi
CONTROL_SSH_PASSWORD_AUTHENTICATION="disabled"
SSH_ROOT_LOGIN="$(sshd_value permitrootlogin)"
case "$SSH_ROOT_LOGIN" in
  no)
    CONTROL_SSH_ROOT_LOGIN="disabled"
    ;;
  prohibit-password|without-password|forced-commands-only)
    CONTROL_SSH_ROOT_LOGIN="key-only"
    ;;
  *)
    fail_stackkit_control "PermitRootLogin effective value is $SSH_ROOT_LOGIN"
    ;;
esac
SSH_PORT_EFFECTIVE="$(sshd_value port)"
if [ "$SSH_PORT_EFFECTIVE" != "%[2]d" ]; then
  fail_stackkit_control "SSH port effective value is $SSH_PORT_EFFECTIVE"
fi
CONTROL_SSH_PORT="$SSH_PORT_EFFECTIVE"

applied_at="$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"
cat > .stackkit/security-baseline.json <<STACKKIT_SECURITY_BASELINE_JSON
{
  "schemaVersion": "%[5]s",
  "status": "pass",
  "mode": "%[6]s",
  "appliedAt": "$applied_at",
  "controls": {
    "firewall": "$CONTROL_FIREWALL",
    "sshPasswordAuthentication": "$CONTROL_SSH_PASSWORD_AUTHENTICATION",
    "sshRootLogin": "$CONTROL_SSH_ROOT_LOGIN",
    "sshPort": "$CONTROL_SSH_PORT",
    "fail2ban": "$CONTROL_FAIL2BAN",
    "unattendedUpgrades": "$CONTROL_UNATTENDED_UPGRADES",
    "sysctl": "$CONTROL_SYSCTL"
  }
}
STACKKIT_SECURITY_BASELINE_JSON
chmod 600 .stackkit/security-baseline.json
`, packageManagerLockWaitScript(), sshPort, permitRootLogin, maxAuthTries, securityBaselineSchemaVersion, securityBaselineMode)
}

func writeSecurityBaselineEvidence(wd string, evidence securityBaselineEvidence) error {
	if evidence.SchemaVersion == "" {
		evidence.SchemaVersion = securityBaselineSchemaVersion
	}
	path := filepath.Join(wd, filepath.FromSlash(securityBaselineEvidencePath))
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create security baseline evidence directory: %w", err)
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal security baseline evidence: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write security baseline evidence: %w", err)
	}
	return nil
}

func readSecurityBaselineEvidence(wd string) (securityBaselineEvidence, error) {
	path := filepath.Join(wd, filepath.FromSlash(securityBaselineEvidencePath))
	data, err := os.ReadFile(path)
	if err != nil {
		return securityBaselineEvidence{}, fmt.Errorf("read security baseline evidence: %w", err)
	}
	var evidence securityBaselineEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return securityBaselineEvidence{}, fmt.Errorf("parse security baseline evidence: %w", err)
	}
	return evidence, nil
}

func validateSecurityBaselineEvidence(evidence securityBaselineEvidence) error {
	if strings.TrimSpace(evidence.SchemaVersion) != securityBaselineSchemaVersion {
		return fmt.Errorf("security baseline evidence schemaVersion = %q, want %q", evidence.SchemaVersion, securityBaselineSchemaVersion)
	}
	if strings.TrimSpace(evidence.Mode) != securityBaselineMode {
		return fmt.Errorf("security baseline evidence mode = %q, want %q", evidence.Mode, securityBaselineMode)
	}
	if appliedAt := strings.TrimSpace(evidence.AppliedAt); appliedAt == "" {
		return fmt.Errorf("security baseline evidence appliedAt is missing")
	} else if _, err := time.Parse(time.RFC3339, appliedAt); err != nil {
		return fmt.Errorf("security baseline evidence appliedAt = %q, want RFC3339: %w", appliedAt, err)
	}
	if evidence.Status != "pass" {
		return fmt.Errorf("security baseline evidence status = %q, want pass", evidence.Status)
	}
	if evidence.Controls == nil {
		return fmt.Errorf("security baseline evidence controls are missing")
	}
	required := map[string]string{
		"firewall":                  "enabled",
		"sshPasswordAuthentication": "disabled",
		"fail2ban":                  "enabled",
		"unattendedUpgrades":        "security",
		"sysctl":                    "applied",
	}
	for key, want := range required {
		if got := strings.TrimSpace(evidence.Controls[key]); got != want {
			return fmt.Errorf("security baseline evidence controls[%s] = %q, want %q", key, got, want)
		}
	}
	if got := strings.TrimSpace(evidence.Controls["sshRootLogin"]); got != "key-only" && got != "disabled" {
		return fmt.Errorf("security baseline evidence controls[sshRootLogin] = %q, want key-only or disabled", got)
	}
	return nil
}

func redactedSecurityBaselineOutput(output string) string {
	const max = 6000
	if len(output) <= max {
		return output
	}
	return output[len(output)-max:]
}
