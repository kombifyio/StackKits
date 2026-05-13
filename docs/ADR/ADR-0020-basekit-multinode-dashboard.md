# ADR-0020: BaseKit Multi-Node and Dashboard Architecture

## Status

Accepted

## Context

BaseKit is no longer treated as "one server". The product contract is one
homelab environment: one trust domain, one main node, and optional worker or
storage nodes. A single-node install remains the smallest valid form of that
environment.

The old `base.*` dashboard also carried too many roles. It was an onboarding
page, service launcher, status surface, and potential homelab homepage at the
same time.

## Decision

- BaseKit is **Single-Environment 1..N**.
- A topology must have exactly one main-like node. `main` is canonical;
  `control-plane` and `standalone` remain compatibility aliases.
- Worker and storage nodes only become part of the same homelab through the
  cluster join contract (`stackkit cluster join-token` plus
  `stackkit init --cluster-mode=join` target shape).
- Multi-node BaseKit resolves to Coolify by default. Single-node installs keep
  the existing context-sensitive PaaS defaults.
- StackKits remains the source of truth. Coolify is the deployment UI, TechStack
  is the orchestration/read mirror, and node-local `.stackkit/state.yaml`
  remains the recovery anchor.
- `base.<domain>` is the **StackKits Node Hub** for a single node: getting
  started, local recovery/access hints, and the compact service matrix.
- `home.<domain>` is the default **Homepage/gethomepage** homelab start
  dashboard, generated from the StackKits service catalog via
  `settings.yaml`, `services.yaml`, `widgets.yaml`, and `docker.yaml`.
- Homepage reads Docker metadata through a Docker Socket Proxy or remote Docker
  TLS endpoint. It must not mount the Docker socket directly.
- Uptime Kuma remains the status and monitoring tool. It is linked from the
  Node Hub and Homepage but is not the primary dashboard.
- Homarr is not the default because the current BaseKit requirement favors
  complete IaC preconfiguration and read-only Docker discovery.

## Consequences

- Multi-node schema validation can reject duplicate node names and topologies
  without exactly one main node before generation.
- Generated BaseKit artifacts always contain a node-local hub and a separate
  homelab start dashboard unless disabled explicitly.
- Coolify remote-server registration is the platform integration target for
  multi-node rollout work; rolling upgrades remain a later phase.

## References

- Homepage configuration and Docker discovery: <https://gethomepage.dev/>
- Coolify server model and API: <https://coolify.io/docs/knowledge-base/server/introduction>
- Uptime Kuma internal API note: <https://raw.githubusercontent.com/louislam/uptime-kuma-wiki/master/Internal-API.md>
- Homarr Docker integration: <https://homarr.dev/docs/integrations/docker/>
