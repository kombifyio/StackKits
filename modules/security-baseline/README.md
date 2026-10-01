# Module: security-baseline

Universal host-level hardening for StackKits. **Foundation layer, applied to every managed node.**

## What it does

`stackkit apply` runs the exact CUE-owned Architecture v2 host policy (`internal/securitybaseline`) on the node:

| Area | What is applied | Notes |
|------|-----------------|-------|
| **unattended-upgrades** | security updates only (not full upgrades), automatic reboot disabled | apt hosts; Alpine gets a daily `apk upgrade` job |
| **sysctl** | `net.ipv4.tcp_syncookies`, `kernel.kptr_restrict`, `kernel.dmesg_restrict`, `fs.protected_hardlinks`/`symlinks`/`fifos`/`regular` | `/etc/sysctl.d/99-stackkit-foundation-hardening.conf`; routing and reverse-path filtering are deliberately absent because their safe values depend on the topology |

It does **not** install or configure a firewall, sshd hardening or fail2ban. Those need site-kind inputs this input-free unit does not have, so the evidence records them as `delegated`:

| Area | Owner |
|------|-------|
| Home sites | `stackkits-home-host-security-runtime`: nftables default-drop inbound admitting the LAN, overlay interfaces and container bridges; key-only sshd; fail2ban sshd jail; never cuts the management path |
| Cloud sites | `stackkits-cloud-host-security-runtime`: nftables default-deny with declared-service ingress; key-only sshd without root login; fail2ban |

The earlier UFW-based script (`ModeLegacyV1`) has no live applier.

## Evidence

`stackkit apply` writes `.stackkit/security-baseline.json` (`stackkit.security-baseline/v2`). It is written once, at apply time, and does not expire.

`stackkit host security verify` observes every control again (the delegated ones included), at any time, as `stackkit.host-security-evidence/v1` with an explicit freshness budget; `stackkit host security repair` restores drift. See [docs/SECURITY.md](../../docs/SECURITY.md).

## Settings

`module.cue` still carries the legacy v1 `settings:` block (UFW policies, ports, fail2ban values). Nothing in the Architecture v2 path reads it.

## Non-Goals

- AppArmor / SELinux profiles (post-V6)
- Full CIS Benchmark compliance (post-V6 - this module covers the high-impact subset)
- Per-user SSH key provisioning (handled by provider lease bootstrap or the user, not this module)
