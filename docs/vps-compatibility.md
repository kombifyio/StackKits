# VPS compatibility

stackkits deploys homelab stacks on bare VPS via `curl -sSL https://base.stackkit.cc | sh`. Not all VPS providers support Docker — this document explains which providers work and why.

## Quick check

Run on your VPS before purchasing a plan or deploying:

    stackkit compat

This checks virtualization type, kernel features, and classifies your VPS into one of three tiers.

## Compatibility tiers

### Full

Docker works perfectly with all features. No workarounds needed.

**Requirements:** KVM or bare-metal virtualization, unshare(2) available, overlay2 support, bridge networking, iptables NAT.

### Degraded

Docker works with automatic workarounds applied by `stackkit prepare`:
- vfs storage driver (instead of overlay2) — slower, uses more disk
- Host networking (instead of bridge) — no network isolation between containers
- Explicit DNS servers (1.1.1.1, 8.8.8.8) injected into daemon.json
- Images pre-pulled from host network when container DNS is broken

### Incompatible

The kernel blocks `unshare(2)` — Docker cannot create containers at all. The Docker daemon may start, but every container operation fails with "operation not permitted".

**Cause:** Container-based virtualization (OpenVZ, restricted LXC) that doesn't expose namespace syscalls to the guest.

## Provider matrix

| Provider | Virtualization | Tier | Starting price | Notes |
|----------|---------------|------|---------------|-------|
| Hetzner Cloud | KVM | Full | ~$4/mo | hetzner.cloud |
| DigitalOcean | KVM | Full | ~$4/mo | digitalocean.com |
| Linode (Akamai) | KVM | Full | ~$5/mo | linode.com |
| Vultr | KVM | Full | ~$5/mo | vultr.com |
| Contabo (KVM) | KVM | Full | ~$5/mo | contabo.com |
| OVH Cloud | KVM | Full | ~$4/mo | ovhcloud.com |
| Scaleway | KVM | Full | ~$4/mo | scaleway.com |
| Oracle Cloud Free (ARM) | KVM | Full | Free | cloud.oracle.com, ARM architecture |
| Proxmox LXC (nested) | LXC | Degraded | varies | Requires `nesting=true` in container config |
| Contabo (OpenVZ) | OpenVZ | Incompatible | ~$3/mo | Kernel blocks unshare |
| Hostinger VPS | OpenVZ/LXC | Incompatible | ~$3/mo | Kernel blocks unshare |
| Budget $2-3 VPS | OpenVZ | Incompatible | ~$2/mo | Kernel blocks unshare |
| Proxmox LXC (restricted) | LXC | Incompatible | varies | `nesting=false` blocks unshare |

## How it works

### Detection

`stackkit prepare` detects the virtualization environment before installing Docker:

1. **Virtualization type** — uses `systemd-detect-virt`, `/proc/vz`, `/proc/1/environ`, DMI data
2. **unshare(2)** — tests `unshare --mount --pid --fork true`
3. **OverlayFS** — attempts `mount -t overlay`
4. **Bridge networking** — attempts `ip link add type bridge`
5. **iptables NAT** — tests `iptables -t nat -L`
6. **cgroup version** — checks `/sys/fs/cgroup/cgroup.controllers`

If the system is incompatible, `stackkit prepare` exits with a clear message and VPS recommendations before installing anything.

### Workarounds (degraded tier)

When Docker works but some features are missing, `stackkit prepare` applies automatic workarounds:

- **Storage driver fallback:** overlay2 -> fuse-overlayfs -> vfs
- **Network fallback:** bridge -> host networking
- **DNS fix:** Inject explicit DNS servers, pre-pull images from host
- **iptables fallback:** nf_tables -> iptables-legacy -> disabled

These workarounds are stored in `~/.stackkits/capabilities.json` and used by `stackkit generate` to adapt the deployment.

### CUE standard

Virtualization is a Layer 1 Foundation standard defined in `base/virtualization.cue`. Every StackKit declares:

- Minimum kernel features required (unshare is always mandatory)
- Supported virtualization types
- Minimum compatibility tier
- Which automatic workarounds are allowed

## Testing

The compatibility matrix is tested weekly using kombify Simulate's Incus VM engine. Each provider profile is simulated by:

1. Launching an Incus VM (boots its own kernel)
2. Applying kernel restrictions matching the provider (seccomp filters, module blacklists)
3. Running `stackkit prepare` and verifying the expected outcome
4. Reporting results to the compatibility matrix

Run a single profile test locally:

    VPS_PROFILE=hetzner-cloud go test -tags=vpscompat -timeout=30m ./tests/integration/

Run the full matrix:

    VPS_PROFILE=all go test -tags=vpscompat -timeout=3h ./tests/integration/
