# Neutral golden-image preparation

## Mini-PC USB installer

AP17.2 consumes the AP17.1 Ubuntu 26.04 candidate; it does not bake another
system. The release-matrix workflow's `mini-pc-installer` job assembles a hybrid
USB ISO using the pinned Ubuntu Server 26.04.1 installer. Ubuntu's existing
screens own network, local OS login, disk selection and erase confirmation.
The added payload is restricted to StackKits, the signed Guard binary and the
neutral release cache. Machine identity, SSH keys and Guard enrollment are
never copied. First boot converges the installed host through the canonical
`guard host-baseline prepare`, `stackkit image prepare` and `image verify`.
Installation currently requires internet access for OS packages and pinned
container images; subsequent local-only operation contacts no kombify service.
If preparation fails, the normal Ubuntu login remains available and setup
retries when the console next starts; missing network never traps the login.

The physical console then offers `local` or `connect`. `local` starts no
Cloud login and leaves Guard services disabled. `connect` uses the existing
Auth0 device-code prompt at `login.kombify.io`, the account-authorized
`/v1/techstack/trust/pairing-tokens` mint and `techstack agent enroll`. The
native access/refresh/ID tokens stay in memory; only the one-time capability
is retained owner-only to resume an interrupted enrollment, and the canonical
enroll command deletes it after use. Guard creates the host key on the mini
PC; the canonical privilege-split layout adopts it after enrollment. A
completed first boot is not repeated on restart. Local Owner and Homelab
account creation remain StackKits' existing lifecycle; this does not give a
Cloud account local Owner authority or initialize a workload.
An enrollment that did not establish a host-key binding is refused on retry;
it must be detached before pairing again, so retries cannot mint another server.

Assembly fails closed unless the golden image passed its existing sanitation
and clone checks and its signed Guard supports `agent pair --check`. The kit
release's signed Guard prerequisite must therefore include appliance pairing.
The workflow's non-secret repository variable
`TECHSTACK_CLOUD_NATIVE_CLIENT_ID` identifies the existing Auth0 Native
application with Device Authorization Grant and Gateway audience
`https://api.kombify.io`; no client secret is embedded. Missing configuration
publishes no installer and leaves the Proxmox image path independent.

Assets live only on the public StackKits GitHub Release. Their prefix is
`kombify-mini-pc-installer-vX.Y.Z-ubuntu-26.04-amd64`: `.iso` (or `.iso.part-NNN`
below GitHub's 2 GiB limit), `.manifest.json`, `-download.sh` and `-SHA256SUMS`.
Checksums are uploaded last; that marker makes the set immutable. The download
helper reassembles and verifies the whole ISO in a new directory. An image
writer can then flash the ISO to a USB drive. No installer bytes, proxy or
fallback pass through Render. A future graphical download/reassembly or
onboarding screen belongs to its design owner; this package adds no web screen.

### Acceptance owed to M13

Run `mini-pc-usb-install-firstboot-pairing` on **rack-pve** with a disposable
blank disk and the published ISO, then validate physical USB/firmware behavior
on a mini PC in Public Beta. No tests run on ryzen-pve. Bind evidence to the
release, ISO checksum, assembly manifest, installed kit and Guard digests.

- Boot the USB image using BIOS and UEFI; confirm the original Ubuntu storage
  confirmation precedes every destination write and cancel preserves a sentinel
  disk. Choose the destination with the stock installer and boot the installed
  disk with the USB removed.
- Select local-only with kombify endpoints blocked. Verify fresh machine/SSH
  identity, installed StackKits, no enrollment and no Cloud requests. Reboot
  without repeated onboarding.
- On a fresh install choose connect. Approve the displayed device code as a
  shared test account, observe one server in that account's Cloud inventory and
  a host-key-bound Guard with the canonical privilege split. Never use the
  owner's personal account. Confirm another account cannot consume its pairing.
- Exercise expired/denied sign-in, lost network, power interruption before and
  after redemption, and retry. Verify no user login token persists, the one-time
  capability is removed on success, and reboot creates no second server.
- Validate published-part reassembly, a corrupted part's refusal and the whole
  ISO digest. TPM, Wi-Fi drivers and Secure Boot on hardware remain Beta proof.

No installer assembly, VM installation or physical-device PASS is claimed by
the source change. M13 owns these runs; AP17.2 implementation closes on merge.

StackKits can preinstall its attested release, packaged OpenTofu provider closure,
Terramate and immutable core container images before a machine image is captured.
`stackkit image` owns this neutral cache boundary. Techstack owns VM construction,
OS sysprep, publication and per-clone Guard enrollment.

Every published kit release carries a ready image for Ubuntu 26.04 and 24.04
that imports as a Proxmox VE template: see "Release images", "Import into Proxmox
VE" and "First boot of a Proxmox clone" below. The sections before them describe
preparing an image by hand.

Ubuntu 26.04 is the default target; Ubuntu 24.04 remains compatible. Preparation
observes the actual Ubuntu version, CPU architecture and local Docker daemon.
These diagnostics are not OS compatibility PASS receipts. Each clone is observed
again; no previous machine's host-conformance receipt is reused.

The initial profiles are `cloud-core-compose` (default), `basement-core-compose`
and `basement-core-lite-compose`. Their names describe the container payload,
not the IaC generation target. Cloud retains its standalone Compose default;
Basement retains its CUE-owned OpenTofu default. Every Techstack-managed clone
uses Advanced Terramate from its first rollout. Modern Homelab, optional
applications and other platform adapters are outside these core cache profiles.

## Prepare an image

Use a dedicated Ubuntu host with a running local Docker daemon, no containers
(including stopped containers), no volumes, no builder cache and no unrelated
images. Partial caches of the selected authoritative core images are accepted.
The cache root and future deployment directory must be separate and empty.
Install an exact published tag that includes `stackkit image` through the existing
installer, then run:

```sh
STACKKIT_RELEASE_VERSION="$RELEASE_TAG" sh install.sh
sudo mkdir -p /opt/stackkits-image /srv/stackkit
stackkit image plan --profile cloud-core-compose
sudo stackkit image prepare --profile cloud-core-compose \
  --cache-root /opt/stackkits-image --target /srv/stackkit
sudo stackkit image verify \
  --cache-root /opt/stackkits-image --target /srv/stackkit
sha256sum /opt/stackkits-image/neutral-image.json
```

`prepare` resolves the running CLI's exact published tag through the existing
kombify-account-free GitHub release resolver and installer, and stages its attested
release under `/opt/stackkits-image/.stackkit/releases/`. This obtains a genuine
release receipt without creating an Owner or an initialized Stack. Development
binaries and version labels alone cannot prepare an image.

The actual installed CLI, server, MCP, OpenTofu and Terramate bytes must match the
verified archive. The existing installer places `providers/` at
`../lib/stackkit/providers` beside its binaries. Admission validates the existing
OpenTofu closure and compares every installed provider file against that same
archive. The regular OpenTofu executor reuses this mirror and its dependency lock;
no second mirror is created. Tool/provider environment overrides are refused.

Only catalog-derived `image-ref@sha256:digest` values are pulled. Docker always
uses the local Unix socket with a fresh empty configuration; contexts and
`DOCKER_HOST` cannot redirect the bake. No containers or volumes are created.
Neutrality and cache contents are checked before and after pulling.

Repeating `prepare` verifies an existing matching manifest. A different profile,
changed release, incomplete cache or modified package is refused. The metadata
root permits only its manifest and selected release cache. The deployment target
must stay empty. Paths may not traverse symlinks. Copied Owner keys, runtime
custody, journals, generated data and unexpected entries are refused and retained.
StackKits never deletes state to make an image neutral. Techstack's OS seal must
also exclude machine identity, SSH host keys and credentials outside these roots.

## First boot of each clone

The unsigned `stackkit.neutral-image/v1` manifest supplies cache metadata, not
identity or mutation authority. The CLI rederives its profile/pins from embedded
CUE and rechecks the attested release, installed bytes, actual host and cache.
Rewriting manifest assertions or recomputing its hash cannot authorize tampering.

For a kombify-account-free standalone Cloud clone:

```sh
cd /srv/stackkit
sudo stackkit init cloud-kit --catalog-defaults --non-interactive \
  --owner-source=local --owner-email "$OWNER_EMAIL" \
  --preinstalled-manifest /opt/stackkits-image/neutral-image.json
sudo stackkit validate
sudo stackkit generate
sudo stackkit apply
sudo stackkit verify
```

Basement's current catalog default uses the lite core and
`basement-core-lite-compose`. To use `basement-core-compose`, add
`--use-case-alternative basement-core=standalone` to native init. CUE validates
the final selection, and image admission refuses a different core module.

For Techstack-managed first boot, use its existing admitted native candidate with
`generation.target: terramate` and its existing Advanced capabilities. Keep the
candidate outside the empty deployment directory:

```sh
cd /srv/stackkit
sudo stackkit init cloud-kit --non-interactive \
  --candidate-spec /run/techstack/clone-candidate.yaml \
  --owner-source=local --owner-email "$OWNER_EMAIL" \
  --preinstalled-manifest /opt/stackkits-image/neutral-image.json
# Continue through the existing capability-gated Advanced generate/apply flow.
```

The managed handoff comes from the existing Techstack execution path. Selecting
Terramate or presenting a manifest does not grant an Advanced capability.
Standard Mode needs no kombify account and retains its regular lifecycle.

The image flag admits a fresh deployment before even deploy logging writes local
state. Normal init then establishes each clone's new Owner key, CA/runtime custody
and secrets through the existing lifecycle. Once initialized, use ordinary init
and its expected-spec-hash CAS behavior for configuration retries. Repeating image
admission refuses an initialized target. Never capture an initialized clone as
another neutral image.

The cache reduces downloads but records `networkRequired: true`: certificate and
service integrations, optional workloads and cache recovery may need network
access. Cache verification is separate from running-stack and public live proof.

## Release images

Every published kit release carries a ready image, so a person does not prepare
one by hand. After each release, the release pipeline's KVM lane on kombify's
own hardware bakes the `cloud-core-compose` profile for Ubuntu 26.04 and 24.04:
the exact kit release, a verified published Guard release, the neutral host
baseline, logical sanitation, offline inspection and two clone boots with
distinct identities. A
release that already carries its image is not baked again, and a published image
is never replaced. The run attaches these assets to the public release
`https://github.com/kombifyio/stackKits/releases/tag/<tag>`:

| Asset | Content |
| --- | --- |
| `kombify-golden-image-<tag>-ubuntu-<version>-amd64.qcow2` | the compressed image both clones booted; above 1.9 GiB it is published as `.qcow2.part-000`, `.qcow2.part-001`, … |
| `kombify-golden-image-<tag>-ubuntu-<version>-amd64.manifest.json` | OS image, Guard and StackKits pins, image digest |
| `kombify-golden-image-<tag>-ubuntu-<version>-amd64.receipt.json` | the passed bake, sanitation and clone receipt |
| `kombify-golden-image-<tag>-proxmox-import.sh` | the Proxmox VE import below |
| `kombify-golden-image-<tag>-SHA256SUMS` | SHA-256 of every asset and of the whole image; written last, so a release carries an image only once this file exists |

The images are downloaded from the GitHub release; the R2 mirror of the
installers leaves them out.

## Import into Proxmox VE

On the Proxmox VE host, as root, with the release tag of the kit you want:

```sh
TAG=v0.58.0   # the kit release
BASE="https://github.com/kombifyio/stackKits/releases/download/$TAG"
curl -fLO "$BASE/kombify-golden-image-$TAG-SHA256SUMS"
curl -fLO "$BASE/kombify-golden-image-$TAG-proxmox-import.sh"
sha256sum -c --ignore-missing "kombify-golden-image-$TAG-SHA256SUMS"   # must print "…proxmox-import.sh: OK"
bash "kombify-golden-image-$TAG-proxmox-import.sh" --release "$TAG" --ubuntu 26.04 \
  --vmid 9100 --storage local-lvm --bridge vmbr0 --from .
```

Check the script against the release's SHA256SUMS before running it as root.
The script then fetches what is missing in the `--from` directory, checks every
file against SHA256SUMS, reassembles a split image and checks the whole image
against SHA256SUMS, its manifest and its passed receipt. It then creates VM 9100 (q35, host CPU, 2 cores,
4 GiB, VirtIO SCSI, DHCP on `vmbr0`, serial console), imports the disk with a
cloud-init drive and turns the VM into a template. It refuses a VM id that
exists. `--from <dir>` imports files that were downloaded before; `--ubuntu 24.04`
selects the compatibility image; `--vmid` defaults to the next free id. Give each
clone the CPU, memory and disk the kits need (`qm set`, `qm resize`).

## First boot of a Proxmox clone

Each clone generates its own machine id and SSH host keys on first boot, and its
Guard starts unbound and waits. To enroll it with kombify, copy the server and
the one-time token from the connect command kombify shows for a new server
(`KOMBI_SERVER=… KOMBI_TOKEN=…`) into the clone's cloud-init user data:

```sh
pvesm set local --content iso,vztmpl,backup,snippets   # once, if snippets are not enabled
qm clone 9100 120 --name homelab --full
cat > /var/lib/vz/snippets/kombify-120.yaml <<'YAML'
#cloud-config
users:
  - name: admin
    groups: [sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    lock_passwd: true
    ssh_authorized_keys: ["ssh-ed25519 AAAA… you@laptop"]
write_files:
  - path: /run/kombify/pairing
    permissions: '0600'
    content: "<KOMBI_TOKEN>"
runcmd:
  - [sh, -c, '/usr/local/bin/techstack agent enroll --server "<KOMBI_SERVER>" --token-file /run/kombify/pairing && /usr/local/bin/techstack guard layout apply --activate']
YAML
qm set 120 --cicustom user=local:snippets/kombify-120.yaml
qm start 120
```

`agent enroll` creates the Guard's key, redeems the token and deletes the token
file (it lives on tmpfs); `guard layout apply --activate` hands the bound Guard
its runtime layout. The server shows as connected in kombify within a few
minutes. The token is single use: delete the snippet once the server is
connected. A control plane on a private LAN address whose connect command carries
`KOMBI_PRIVATE_LAN_PIN` is not covered by this user data.

Without kombify, leave out `write_files` and `runcmd`: the Guard stays unbound
and the clone runs the account-free Standard Mode from "First boot of each clone"
above. A release image keeps its cache under `/opt/stackkits-image` and its empty
deployment directory at `/opt/stackkit`, so run those commands in `/opt/stackkit`
instead of `/srv/stackkit`.
