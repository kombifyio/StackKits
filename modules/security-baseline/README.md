# Module: security-baseline

Host-level security hardening for StackKits. **Foundation layer, mandatory for the BaseKit public beta.**

## What it does

Configures the host OS (Ubuntu 22.04+/24.04) with a safe-by-default baseline:

| Area | Default | Notes |
|------|---------|-------|
| **UFW firewall** | deny all incoming, allow SSH/80/443 | SSH port follows the StackSpec |
| **fail2ban** | SSH jail, 1h bantime, 5 max retries | systemd journal on servers, polling fallback for Fresh VM tests |
| **unattended-upgrades** | security updates only (not full upgrades) | reboot-on-security-update disabled |
| **SSH hardening** | no password auth, key-only root transport for lease servers | full `PermitRootLogin no` is safe only after a non-root transport exists |
| **sysctl** | SYN cookies, rp_filter, redirects/source routing disabled, kernel pointers hidden | `/etc/sysctl.d/99-stackkit-hardening.conf` |

## Why mandatory

The public BaseKit beta targets bare Ubuntu test servers. Defaults must be safe. A BaseKit deployment without UFW, fail2ban, unattended security updates, SSH password disablement, and kernel/network hardening is not release-ready. Making this optional would put the safe path behind documentation that beta users may not read.

`stackkit apply` writes machine-readable evidence to `.stackkit/security-baseline.json`. Canonical SK-S1, SK-S2, and SK-S3 release artifacts must include that evidence or release validation fails.

See [docs/SECURITY.md](../../docs/SECURITY.md) and [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md) for the platform-level rationale.

## Settings

See `module.cue` `settings:` block.

| Setting | Type | Default | Kind |
|---------|------|---------|------|
| `defaultIncomingPolicy` | `"deny"\|"reject"` | `deny` | perma |
| `defaultOutgoingPolicy` | `"allow"\|"deny"` | `allow` | perma |
| `sshPort` | int | `22` | flexible |
| `extraAllowedPorts` | []int | `[]` | flexible |
| `fail2banBanTime` | int seconds | `3600` | flexible |
| `fail2banMaxRetry` | int | `5` | flexible |
| `securityUpdatesOnly` | bool | `true` | flexible |
| `sshPasswordAuth` | bool | `false` | flexible |
| `sshPermitRoot` | bool | `false` | flexible |

## Status

**Beta implementation active** for the official BaseKit apply/installer path on apt-based Ubuntu hosts. Evidence is validated by production tests and release evidence import. Terraform fragment parity remains a later follow-up; public beta release evidence is based on the CLI apply path.

## Non-Goals

- AppArmor / SELinux profiles (post-V6)
- Full CIS Benchmark compliance (post-V6 — this module covers the high-impact subset)
- Per-user SSH key provisioning (handled by provider lease bootstrap or the user, not this module)
