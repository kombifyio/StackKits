---
adr: ADR-0050
title: The kombify Content Bridge runs on the node as a separate, read-only, opt-in component
status: Accepted; Apply wiring pending
date: 2026-10-04
last_verified: 2026-10-04
identity_verified: 2026-10-12
---

# ADR-0050: The kombify Content Bridge runs on the node as a separate, read-only, opt-in component

**Decision owner:** Marcel. Owner decisions 2026-10-03: consent first and off by
default, never through kombify Guard, read-only, Standard-rollout apps only,
starting with Immich. This ADR records the node side (slice S2).
**Related:** ADR-0044, ADR-0042.

## Context

kombify Cloud and the Companion show a user's homelab content (first photo
counts) with the user's consent. The standard fixes the boundary: Guard never
carries application content, so the node needs its own component that reads
the apps and answers `homelab-content/v1` to the Gateway. StackKits acts on
the server it is installed on and holds its custody, so the component lives
here.

## Decision

- **Binary.** `cmd/stackkit-content-bridge`, a fourth binary in every release
  archive next to `stackkit`, `stackkit-server` and `stackkit-mcp`. A
  subcommand of an existing binary was rejected: `stackkit` and
  `stackkit-server` carry the whole CLI, API and MCP surface, which a
  read-only, capability-free, no-egress container should not contain. The
  bridge links only the standard library and `internal/contentbridge`, so its
  code is the audited surface. Like ADR-0044, the container runs the release's
  own binary on a digest-pinned `alpine` base and no image is published.
- **Endpoints.** `GET /content/v1/summary?use_cases=photos&tier=…` returns a
  `homelab-content/v1` document. `GET /healthz` answers only to the loopback.
  Anything else is 404, and every method but GET is refused. A use case the
  node has no adapter for answers `not_configured`; a name outside the
  contract is a 400, because the contract cannot name it.
- **Tier.** The bridge answers only the tier it is granted and refuses a
  request without one before reading anything (`off` reads nothing and answers
  `consent_required`). The grant is the signed `tier` claim of the Gateway's
  installation envelope, which arrives in S3. Until then `LoopbackQueryTier`
  accepts the `tier` query parameter only from a caller on the bridge's own
  loopback, and never reads a forwarded-for header. Behind the router every
  request comes from the router, so the published route answers 403 until the
  envelope authority replaces it. The effective tier is the minimum of the
  grant and the adapter's ceiling. Photos answers `summary` at most in S2, so
  a `previews` request returns the summary counts at tier `summary` and no
  items; titles and thumbnails come with the node-signed thumbnail slice.
- **Photos counts (Immich 2.7).** `assets`, `images`, `videos` from
  `GET /api/assets/statistics?visibility=timeline`, `albums` (owned) from
  `GET /api/albums/statistics`, `on_this_day` from
  `GET /api/memories/statistics?type=on_this_day&for=<now>`. The timeline
  filter keeps archived, hidden and locked assets out of every count. Assets
  are required; albums and on-this-day are omitted when Immich does not
  answer them. An app that cannot be read is `app_unreachable` (also for a 5xx
  and for an unexpected shape), a 401 or 403 is `credential_invalid`, and no
  issued key is `not_configured`. None of these carries counts: unreachable is
  never reported as empty.
- **Read-only, enforced.** The HTTP client has no method for anything but GET,
  and its transport refuses every request that is not a GET without a body to
  the configured Immich origin, to one of three allowlisted paths, with only
  the allowlisted query parameters. Redirects are not followed and proxy
  environment variables are ignored. The allowlist contains no endpoint that
  lists, names or serves an asset, album, person, location or file.
- **Immich credential.** `appsetup.IssueImmichAddOnAPIKey` takes an explicit
  permission list (Kiosk and Power Tools stay on `all`). The bridge key is
  named `stackkits-content-bridge` and carries exactly `asset.statistics`,
  `album.statistics` and `memory.statistics`, the three statistics permissions
  of the Immich 2.7 permission enum that this release pins. The key can count
  and cannot list, open or change anything. `previews` will add `album.read`,
  `memory.read`, `asset.view` and `timeline.read` with S6. The key is custodied
  with `localevidence.StoreLocalIssuedSecret` as an owner-signed issued secret
  under `secret://workloads/photos-content-bridge/immich-api-key`, and reaches
  the container as the file `/run/secrets/immich-api-key`
  (`CONTENT_BRIDGE_IMMICH_API_KEY_FILE`). The bridge reads the file on every
  upstream read, so a rotation needs no restart. The key never reaches
  Techstack, the Gateway, Cloud or a client.
- **Memory and logs.** The bridge keeps an app answer for at most 60 seconds
  (a failure for 5 seconds, as a status only) and stores nothing. It logs the
  use case, the status, the tier and the latency, never a count, an identifier,
  a URL or a credential.
- **Module.** `modules/content-bridge` (`maturity: opt-in`, off by default).
  Container: read-only root file system, every capability dropped,
  `no-new-privileges`, its own uid. Networks: the Immich app network and one
  `internal: true` network of its own, so it has no route to the internet.
  Route: `Host(base.<domain>) && PathPrefix(/content/v1)` with a router rate
  limit (5 requests per second, burst 20), no TinyAuth forward-auth and access
  policy `none-explicit` with a stated reason. `/healthz` is not routed.

## Consequences

- S3 supplies the envelope verification: a `TierAuthority` that checks the
  Gateway's signed installation envelope, its audience and its `tier` claim,
  and refuses an envelope without one. Nothing else changes in the bridge.
- The module contract is complete, but Apply does not render it yet. Still
  open: the selected-PaaS workload `photos-content-bridge` in the
  Architecture v2 catalog (with the `immich-internal` peer network, the
  internal network, the base-host path route and the `immich-api-key` secret
  file), staging of `stackkit-content-bridge` beside compose.yaml and in the
  upgrade checkpoint, the setup action that calls
  `appsetup.IssueContentBridgeImmichKey`, and the Verify probes for "no egress
  route" and "no payload fields in bridge logs". Until then no kit installs the
  bridge, and no use-case toggle offers it.
- Evidence is local. The Go tests run against a fake Immich and validate every
  answer against the committed copy of `homelab-content.v1`
  (`mise run content-bridge:schema:check` compares it with the workspace
  authority when a checkout is available). `scripts/e2e/content-bridge-immich.mjs`
  runs the pinned real Immich 2.7.0 with Docker: it issues the statistics-only
  key, shows that Immich itself refuses that key a list, a read and every
  write, checks the counts without archived assets, rotation and
  `app_unreachable`, and runs the hardened container on internal networks to
  show that it has no route to the internet. It is manual evidence, not a gate.
  A real Linux Apply of the module and the Verify probes are pending.
