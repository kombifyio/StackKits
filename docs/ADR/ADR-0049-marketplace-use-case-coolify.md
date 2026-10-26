---
adr: ADR-0049
title: Marketplace on Coolify and the Layer-2 PaaS platform boundary
status: Accepted
date: 2026-10-10
last_verified: 2026-10-22
---

# ADR-0049: Marketplace on Coolify and the Layer-2 PaaS platform boundary

**Decision owner:** Marcel (2026-10-10, owner direction for the twelfth main
intent "Marketplace"; engine choice accepted on the evidence below).
**Amends:** ADR-0042, ADR-0045. **Related:** ADR-0035.

## Context

The owner added a twelfth main intent, Marketplace: "your own app store", the
answer to Umbrel OS, with Coolify or Dokploy as its engine and Komodo as the
operations platform without a store (`docs/use-case-expansion/portfolio.md`,
workspace `CREATION-EXPERIENCE-STANDARD.md` §3c).

Coolify was still carried in two places without a task:

- the explicit `standalone` (Full) alternatives of `basement-core` and
  `cloud-core` render Coolify 4.1.2 with Postgres, Redis and Realtime,
  without admin bootstrap and without owning any workload;
- the kit manifests still declared `paas` as a default capability with
  `defaultTool: coolify`, and `stackkit prepare` pre-pulled the Coolify images
  when no platform was named.

The native default alternatives (`standalone-compose`) already select the
Coolify-free Core Lite and Cloud standalone profiles.

A development probe on 2026-10-10 (disposable container, Coolify 4.4.6
official images, mock OIDC issuer behind its own CA) showed:

- The root user comes from `ROOT_USER_EMAIL`/`ROOT_USER_PASSWORD`; registration
  is then closed. The seeder validates `email:rfc,dns` and `uncompromised()`
  and fails silently, leaving registration open.
- API access and the API token are created through `artisan`; the OIDC
  provider (`OauthSetting`) has no REST route and is written through `artisan`.
- An OIDC login with the root user's e-mail binds to the root admin; new OIDC
  users are created as members of the root team, who cannot install apps
  (`ServicePolicy::create` requires admin).
- `POST /api/v1/services` deploys catalog apps (`type`) and custom Compose
  (`docker_compose_raw`); the catalog has 359 templates and is fixed to
  Coolify's CDN.
- Coolify needs root SSH to its own host, an HTTPS issuer with a trusted CA,
  and brings its own proxy on ports 80/443.

Dokploy keeps OIDC/SSO in `/proprietary` under the Dokploy Source Available
License (production use only with a contract) and enforces it with a licence
check. Without PocketID login it cannot meet the identity promise.

## Decision

1. **Coolify is the current Marketplace engine, as an application of the
   Marketplace use case, in addition to its Layer-2 PaaS platform role.**
   Coolify, Komodo and Dokploy are the Layer-2 PaaS platforms and OpenTofu
   runtime targets under ADR-0045. Direct Compose without a PaaS stays the
   default until further PaaS platforms are available; 1.0 requires both
   paths. StackKits installs and operates a pinned Coolify 4.4.x (digest pin,
   `AUTOUPDATE=false`), its backup and its upgrade. Apps installed from the
   store belong to Coolify and the user; curated kombify apps stay
   StackKits-owned. This keeps one execution and routing owner per workload
   (ADR-0042) and does not need the ADR-0045 platform-mode migration.
2. **Coolify is not part of the default core.** The kit manifests declare
   `paas` as `optional` with `coolify` and `komodo` as explicit alternatives,
   and `stackkit prepare` pre-pulls PaaS images only for an explicitly named
   platform. The `standalone` (Full) core alternatives with Coolify 4.1.2 stay
   selectable for existing installations until the Marketplace use case can
   take them over; they are not offered for new installations and are retired
   by a tracked package.
3. **Dokploy is not the Marketplace engine** because its OIDC/SSO is
   licence-gated; it remains a Layer-2 execution platform. Komodo is the
   execution and operations platform without a store.
4. **Runtipi is an accepted target for both the Marketplace app-store use case
   and use-case workload execution as a platform** (workspace
   [D-2026-10-20-09](https://github.com/kombify/kombify-workspace/blob/main/internal/records/DECISIONS.md)).
   The product-intent catalog records Runtipi as a Marketplace alternative.
   CUE contracts, native bootstrap and an API workload adapter are prepared;
   executable selection and current-pin lifecycle admission remain pending.
   Implementation must use the existing CUE,
   OpenTofu, application-adapter and node-custody seams; no Runtipi OpenTofu
   provider or API compatibility is presumed. Marketplace installation and
   workload execution need separate current-pin install, login, routing,
   app-operation, backup, restore and removal evidence. Scope admission
   changes neither the current Coolify engine nor selected/default execution
   adapters.
5. **Dockhand is a prospective platform option** subject to its BSL/license
   profile; this is not an unqualified open-source or runtime-support claim.
6. Identity: the owner e-mail is the Coolify root user and signs in through
   PocketID; household users are created on first OIDC login as members;
   further rights are granted explicitly by StackKits. The Coolify UI stays
   LAN-only or behind TinyAuth.

Support for any platform, Marketplace engine or runtime target requires
current-pin lifecycle evidence; no decision above promotes a compatibility
cell.

## Consequences

- The Public Beta demo matrix no longer lists `coolify` among the core
  surfaces; core verification covers `base`, `auth` and `id`.
- The Marketplace use case is planned work (about 16 to 29 person-days):
  use-case package, headless bootstrap with fail-closed admin check, proxy
  coexistence with the StackKits router, a Docker-bridge-scoped SSH exception
  in the host baseline, Kopia backup of the Coolify database and volumes,
  status/verify/remove/upgrade, and Depot evidence.
- A kombify-curated catalog inside the Coolify UI needs an upstream change or
  a patch and is not part of this decision; curated apps deploy through the
  API.
- The legacy v1 bridge (`internal/cue`, tfvars) resolves an omitted `paas`
  to none; Coolify stays an explicit
  choice there. The `standalone` (Full) core alternatives with Coolify 4.1.2
  remain until existing installations can be migrated to the Marketplace use
  case, because the beta4 upgrade checkpoint and the core runtime profiles
  still serve those installations.
