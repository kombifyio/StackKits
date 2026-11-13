# Neutral golden-image preparation

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
