# Module: security-baseline

Host-level security hardening for StackKits. **Foundation layer, mandatory in V6.**

## What it does

Configures the host OS (Ubuntu 22.04+/24.04) with a safe-by-default baseline:

| Area | Default | Notes |
|------|---------|-------|
| **UFW firewall** | deny all incoming, allow 22/80/443 | `extraAllowedPorts` setting adds more |
| **fail2ban** | SSH + Traefik jails, 1h bantime, 5 max retries | jail configs in `terraform/fail2ban/` |
| **unattended-upgrades** | security updates only (not full upgrades) | reboot-on-security-update disabled |
| **SSH hardening** | no root login, no password auth, key-only | user must have a working SSH key |
| **sysctl** | SYN cookies, rp_filter, kernel pointers hidden | `/etc/sysctl.d/99-stackkits-hardening.conf` |

## Why mandatory

V6 targets the bare-Ubuntu test-user. Defaults must be safe. A BaseKit deployment without UFW or without SSH hardening is not production-ready. Making this optional means the safe path relies on the user reading docs, which test users do not.

See [ARCHITECTURE_V6.md §4](../../docs/ARCHITECTURE_V6.md) for the platform-level rationale.

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

## Pre-flight

`stackkit doctor` warns before applying:

```
[!] You are about to disable password SSH. Confirm you have a working SSH key. [y/N]
```

## Status

**Scaffolded** for V6 Phase 2 (Q3/2026). Terraform implementation pending — see [ROADMAP.md](../../docs/ROADMAP.md) Phase 2.

## Non-Goals

- AppArmor / SELinux profiles (post-V6)
- Full CIS Benchmark compliance (post-V6 — this module covers the high-impact subset)
- Per-user SSH key provisioning (handled by the user, not this module)
