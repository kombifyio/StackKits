# Cloud Kit

> Cloud single-environment homelab (1..N nodes, exactly one `main`) for VPS/cloud deployments with a public domain. Installer: `https://cloud.stackkit.cc`. CUE is the source of truth; OpenTofu output is generated.

> **Taxonomy (ADR-0026):** Cloud Kit is the
> **cloud** product profile (`context cloud`) derived from the shared `base.#StackBase` library —
> the cloud adaptation of [Basement Kit](../basement-kit/README.md) (installer
> `base.stackkit.cc`). It is a single-*environment* kit (one trust domain, one `main`), **not**
> Modern Homelab: a hybrid local+cloud deployment is **Modern Homelab**, and redundant
> control planes are **HA Kit**. Cloud-only extensions (e.g. tunnel / public DNS) are added to
> this profile as they land.

## What differs from Basement Kit

Cloud Kit shares ~90% of its definition with Basement Kit (the `base/` library). Only the
context-derived defaults differ, resolved from `context: cloud` (`base/context.cue`):

| Concern | Basement (`context local/pi`) | Cloud (`context cloud`) |
|---|---|---|
| TLS | self-signed, `*.home.localhost` | Let's Encrypt (ACME) on a real domain |
| Access | host ports, private IP | reverse proxy, public IP |
| Domain | optional (`.home.localhost`) | required (own/custom domain or `kombify.me`) |
| Installer | `base.stackkit.cc` | `cloud.stackkit.cc` |

The platform baseline (Coolify default, PocketID + TinyAuth identity, Uptime Kuma, default L3
apps) and the Golden-Rules contract are identical — see [basement-kit/README.md](../basement-kit/README.md)
for the shared platform, security, and PaaS details.

## Requirements

| | Minimum | Recommended |
|--|---------|-------------|
| CPU | 2 cores | 4+ cores |
| RAM | 2 GB | 4+ GB |
| Disk | 20 GB | 40+ GB |
| OS | Ubuntu 22.04+ | Ubuntu 24.04 LTS |
| Network | Public IP + domain | Public IP + own/custom domain |

## Quick Start

```bash
stackkit init cloud-kit
stackkit prepare
stackkit generate
stackkit plan
stackkit apply
```

A public domain (own/custom or `kombify.me`) and DNS access (e.g. a `CLOUDFLARE_API_TOKEN`)
are required so the generated public HTTPS routes resolve and ACME certificates can be issued.

## Status

Cloud Kit is a derived product of the shared base; its cloud verification cells stay
`scaffolding` until the canonical cloud gates (SK-S3 provider-leased custom domain + SK-S2
managed `kombify.me` subdomain) pass from `cloud-kit` released contents (see
[`mode_matrix.cue`](mode_matrix.cue)). For a release-ready local homelab today, use
[basement-kit](../basement-kit/README.md).

## Development Gates

```bash
cue vet ./base/...
cue vet ./cloud-kit/...
go test ./...
mise run test:cue-binding
```
