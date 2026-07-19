// Package securitybaseline renders the host security baseline script shared by
// legacy StackKit execution and architecture-v2 renderers.
package securitybaseline

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

const (
	EvidenceSchemaVersion               = "stackkit.security-baseline/v1"
	EvidenceModePublicBeta              = "public-beta"
	EvidenceSchemaVersionArchitectureV2 = "stackkit.security-baseline/v2"
	EvidenceModeArchitectureV2          = "architecture-v2-foundation"
)

// Mode selects the governed policy generation. Legacy-v1 preserves the
// existing public-beta host rollout. Architecture-v2 deliberately contains
// only target-neutral controls; access, identity, firewall, and routing remain
// owned by typed product modules.
type Mode string

const (
	ModeLegacyV1       Mode = "legacy-v1"
	ModeArchitectureV2 Mode = "architecture-v2"
)

// Config contains inputs for rendering a security-baseline script. SSH fields
// are authoritative only for legacy-v1 and are ignored by Architecture-v2.
type Config struct {
	Mode                         Mode
	SSHPort                      int
	PermitRootLogin              string
	MaxAuthTries                 int
	PackageManagerLockWaitScript string
}

// NormalizePermitRootLogin returns a safe sshd value or an empty string when
// the input must be replaced with the secure default.
func NormalizePermitRootLogin(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "no", "prohibit-password", "forced-commands-only":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

// Build renders a POSIX-sh compatible baseline script.
func Build(cfg Config) (string, error) {
	switch cfg.Mode {
	case ModeLegacyV1:
		return buildLegacyV1(cfg)
	case ModeArchitectureV2:
		return buildArchitectureV2(cfg)
	default:
		return "", fmt.Errorf("unsupported security baseline mode %q", cfg.Mode)
	}
}

func buildLegacyV1(cfg Config) (string, error) {
	permitRootLogin := NormalizePermitRootLogin(cfg.PermitRootLogin)
	if permitRootLogin == "" {
		permitRootLogin = "prohibit-password"
	}

	maxAuthTries := cfg.MaxAuthTries
	if maxAuthTries <= 0 {
		maxAuthTries = 3
	}

	sshPort := cfg.SSHPort
	if sshPort <= 0 || sshPort > 65535 {
		sshPort = 22
	}
	sshPreFirewall := fmt.Sprintf(`SSH_PORTS="%d"
FAIL2BAN_PORTS="%d"`, sshPort, sshPort)
	sshFirewallRules := fmt.Sprintf(`$SUDO ufw limit %d/tcp comment 'StackKit SSH rate limit'`, sshPort)
	sshPortProbe := fmt.Sprintf(`SSH_PORT_EFFECTIVE="$(sshd_value port)"
if [ "$SSH_PORT_EFFECTIVE" != "%d" ]; then
  fail_stackkit_control "SSH port effective value is $SSH_PORT_EFFECTIVE"
fi
CONTROL_SSH_PORT="$SSH_PORT_EFFECTIVE"`, sshPort)

	replacer := strings.NewReplacer(
		"@@PACKAGE_MANAGER_LOCK_WAIT@@", strings.TrimSpace(cfg.PackageManagerLockWaitScript),
		"@@SSH_PRE_FIREWALL@@", sshPreFirewall,
		"@@SSH_FIREWALL_RULES@@", sshFirewallRules,
		"@@LEGACY_SERVICE_INGRESS_RULES@@", legacyHTTPIngressRules,
		"@@LEGACY_DOCKER_BRIDGE_RULES@@", legacyDockerBridgeAPIRules,
		"@@PERMIT_ROOT_LOGIN@@", permitRootLogin,
		"@@MAX_AUTH_TRIES@@", strconv.Itoa(maxAuthTries),
		"@@SSH_PORT_PROBE@@", sshPortProbe,
		"@@EVIDENCE_SCHEMA_VERSION@@", EvidenceSchemaVersion,
		"@@EVIDENCE_MODE@@", EvidenceModePublicBeta,
	)
	return resolveTemplate(replacer.Replace(legacyScriptTemplate))
}

func buildArchitectureV2(cfg Config) (string, error) {
	replacer := strings.NewReplacer(
		"@@PACKAGE_MANAGER_LOCK_WAIT@@", strings.TrimSpace(cfg.PackageManagerLockWaitScript),
		"@@EVIDENCE_SCHEMA_VERSION@@", EvidenceSchemaVersionArchitectureV2,
		"@@EVIDENCE_MODE@@", EvidenceModeArchitectureV2,
	)
	return resolveTemplate(replacer.Replace(architectureV2ScriptTemplate))
}

func resolveTemplate(rendered string) (string, error) {
	if strings.Contains(rendered, "@@") {
		return "", fmt.Errorf("security baseline script contains an unresolved template token")
	}
	return rendered, nil
}

// RenderV2HostPolicy renders the canonical, self-contained architecture-v2
// host policy. Renderers should use this convenience instead of constructing a
// v2 Config so the policy and its package-manager safety prelude cannot drift.
func RenderV2HostPolicy() ([]byte, error) {
	script, err := Build(Config{
		Mode:                         ModeArchitectureV2,
		PermitRootLogin:              "prohibit-password",
		MaxAuthTries:                 3,
		PackageManagerLockWaitScript: defaultPackageManagerLockWaitScript,
	})
	if err != nil {
		return nil, err
	}
	return []byte(script), nil
}

// ContractHash returns the canonical sha256-prefixed digest of an exact
// rendered policy. It can be stored directly alongside a render unit for drift
// detection.
func ContractHash(policy []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(policy))
}

const defaultPackageManagerLockWaitScript = `if command -v apt-get >/dev/null 2>&1; then
  for i in $(seq 1 144); do
    if command -v fuser >/dev/null 2>&1 && fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/cache/apt/archives/lock >/dev/null 2>&1; then
      echo "Waiting for apt/dpkg lock to be released..."
      sleep 5
      continue
    fi
    if pgrep -x apt-get >/dev/null 2>&1 || pgrep -x apt >/dev/null 2>&1 || pgrep -x dpkg >/dev/null 2>&1 || ps -eo args | grep -E '(^|[ /])unattended-upgrade([[:space:]]|$)' | grep -v 'unattended-upgrade-shutdown' | grep -v grep >/dev/null 2>&1; then
      echo "Waiting for apt/dpkg process to finish..."
      sleep 5
      continue
    fi
    break
  done
  if command -v fuser >/dev/null 2>&1 && fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/cache/apt/archives/lock >/dev/null 2>&1; then
    echo "apt_lock_timeout: Timed out waiting for apt/dpkg lock" >&2
    exit 1
  fi
  if pgrep -x apt-get >/dev/null 2>&1 || pgrep -x apt >/dev/null 2>&1 || pgrep -x dpkg >/dev/null 2>&1 || ps -eo args | grep -E '(^|[ /])unattended-upgrade([[:space:]]|$)' | grep -v 'unattended-upgrade-shutdown' | grep -v grep >/dev/null 2>&1; then
    echo "apt_process_timeout: Timed out waiting for apt/dpkg process" >&2
    ps -eo pid,comm,args | grep -E 'apt|dpkg|unattended' | grep -v grep >&2 || true
    exit 1
  fi
fi`

const legacyHTTPIngressRules = `$SUDO ufw allow 80/tcp comment 'StackKit HTTP'
$SUDO ufw allow 443/tcp comment 'StackKit HTTPS'`

const legacyDockerBridgeAPIRules = `case "${DOCKER_HOST:-}" in
  tcp://127.0.0.1:2375|tcp://localhost:2375|tcp://0.0.0.0:2375)
    $SUDO ufw allow in from 172.16.0.0/12 to any port 2375 proto tcp comment 'StackKit local Docker bridge API'
    $SUDO ufw allow in from fd00::/8 to any port 2375 proto tcp comment 'StackKit local Docker bridge API' || true
    ;;
esac`

const legacyScriptTemplate = `#!/bin/sh
set -eu
@@PACKAGE_MANAGER_LOCK_WAIT@@
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

SSHD_BIN="$(command -v sshd || true)"
if [ -z "$SSHD_BIN" ] && [ -x /usr/sbin/sshd ]; then SSHD_BIN=/usr/sbin/sshd; fi
if [ -z "$SSHD_BIN" ]; then
  echo "sshd not found after openssh-server install" >&2
  exit 1
fi
@@SSH_PRE_FIREWALL@@

@@SSH_FIREWALL_RULES@@
$SUDO ufw default deny incoming
$SUDO ufw default allow outgoing
@@LEGACY_SERVICE_INGRESS_RULES@@
@@LEGACY_DOCKER_BRIDGE_RULES@@
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
$SUDO tee /etc/fail2ban/jail.d/stackkit-sshd.conf >/dev/null <<STACKKIT_FAIL2BAN
[sshd]
enabled = true
port = $FAIL2BAN_PORTS
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
PermitRootLogin @@PERMIT_ROOT_LOGIN@@
MaxAuthTries @@MAX_AUTH_TRIES@@
X11Forwarding no
STACKKIT_SSHD
$SUDO "$SSHD_BIN" -t
if [ "$SYSTEMD_RUNNING" = "1" ] && systemctl list-unit-files ssh.service >/dev/null 2>&1; then
  $SUDO systemctl reload ssh || $SUDO systemctl restart ssh
elif command -v service >/dev/null 2>&1; then
  $SUDO service ssh reload || $SUDO service ssh restart || $SUDO pkill -HUP sshd
else
  $SUDO pkill -HUP sshd
fi

fail_stackkit_control() {
  echo "security baseline control probe failed: $1" >&2
  exit 1
}
UFW_STATUS="$($SUDO ufw status verbose 2>/dev/null || true)"
printf '%s\n' "$UFW_STATUS" | grep -q '^Status: active$' || fail_stackkit_control "ufw is not active"
printf '%s\n' "$UFW_STATUS" | grep -Eq 'Default:.*deny.*incoming.*allow.*outgoing' || fail_stackkit_control "ufw default policy is not deny incoming / allow outgoing"
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
  printf '%s\n' "$SSHD_EFFECTIVE" | awk -v key="$1" 'tolower($1)==tolower(key) {print tolower($2); exit}'
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
@@SSH_PORT_PROBE@@

applied_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cat > .stackkit/security-baseline.json <<STACKKIT_SECURITY_BASELINE_JSON
{
  "schemaVersion": "@@EVIDENCE_SCHEMA_VERSION@@",
  "status": "pass",
  "mode": "@@EVIDENCE_MODE@@",
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
`

// architectureV2ScriptTemplate intentionally does not own access or network
// semantics. In particular it must not install or configure SSH, UFW,
// fail2ban, routing, or device credentials. Those controls require node-bound
// Local/Cloud/Identity/Network inputs that this input-free Foundation unit does
// not possess.
const architectureV2ScriptTemplate = `#!/bin/sh
set -eu
@@PACKAGE_MANAGER_LOCK_WAIT@@
if ! command -v apt-get >/dev/null 2>&1; then
  echo "StackKit Architecture v2 Foundation host hardening currently supports apt-based Ubuntu hosts." >&2
  exit 1
fi
if [ "$(id -u)" -eq 0 ]; then SUDO=""; else SUDO="sudo"; fi
if [ "$(id -u)" -ne 0 ] && ! command -v sudo >/dev/null 2>&1; then
  echo "sudo is required to apply StackKit Architecture v2 Foundation host hardening." >&2
  exit 1
fi
SYSTEMD_RUNNING=0
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ] && [ "$(cat /proc/1/comm 2>/dev/null || true)" = "systemd" ]; then
  SYSTEMD_RUNNING=1
fi
mkdir -p .stackkit
$SUDO mkdir -p /etc/apt/apt.conf.d /etc/sysctl.d /var/log/stackkit
$SUDO apt-get update -y
$SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends unattended-upgrades procps

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
  $SUDO systemctl enable --now unattended-upgrades
elif command -v service >/dev/null 2>&1; then
  $SUDO service unattended-upgrades restart
fi

# Routing and reverse-path filtering are deliberately absent. Their safe values
# depend on Local, Cloud, multi-NIC, VPN, mesh, and federation topology.
$SUDO tee /etc/sysctl.d/99-stackkit-foundation-hardening.conf >/dev/null <<'STACKKIT_SYSCTL'
net.ipv4.tcp_syncookies=1
kernel.kptr_restrict=1
kernel.dmesg_restrict=1
fs.protected_hardlinks=1
fs.protected_symlinks=1
fs.protected_fifos=2
fs.protected_regular=2
STACKKIT_SYSCTL
$SUDO sh -c 'sysctl -p /etc/sysctl.d/99-stackkit-foundation-hardening.conf > /var/log/stackkit/security-baseline-sysctl.log'

fail_stackkit_control() {
  echo "security baseline control probe failed: $1" >&2
  exit 1
}
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

applied_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cat > .stackkit/security-baseline.json <<STACKKIT_SECURITY_BASELINE_JSON
{
  "schemaVersion": "@@EVIDENCE_SCHEMA_VERSION@@",
  "status": "pass",
  "mode": "@@EVIDENCE_MODE@@",
  "appliedAt": "$applied_at",
  "controls": {
    "firewall": "delegated",
    "sshPasswordAuthentication": "delegated",
    "sshRootLogin": "delegated",
    "sshPort": "delegated",
    "fail2ban": "delegated",
    "unattendedUpgrades": "$CONTROL_UNATTENDED_UPGRADES",
    "sysctl": "$CONTROL_SYSCTL"
  }
}
STACKKIT_SECURITY_BASELINE_JSON
chmod 600 .stackkit/security-baseline.json
`
