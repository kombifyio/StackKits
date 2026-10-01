# Neutral golden-image preparation

StackKits can preinstall its attested release, packaged OpenTofu provider closure,
Terramate and immutable core container images before a machine image is captured.
`stackkit image` owns this neutral cache boundary. Techstack owns VM construction,
OS sysprep, publication and per-clone Guard enrollment.

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
account-free GitHub release resolver and installer, and stages its attested
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

For an account-free standalone Cloud clone:

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
Standard Mode stays account-free and retains its regular lifecycle.

The image flag admits a fresh deployment before even deploy logging writes local
state. Normal init then establishes each clone's new Owner key, CA/runtime custody
and secrets through the existing lifecycle. Once initialized, use ordinary init
and its expected-spec-hash CAS behavior for configuration retries. Repeating image
admission refuses an initialized target. Never capture an initialized clone as
another neutral image.

The cache reduces downloads but records `networkRequired: true`: certificate and
service integrations, optional workloads and cache recovery may need network
access. Cache verification is separate from running-stack and public live proof.
