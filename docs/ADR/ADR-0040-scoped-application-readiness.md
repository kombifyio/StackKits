# ADR-0040: Scope application readiness and client onboarding

- Status: Accepted for the Homelab quality-review implementation
- Date: 2026-09-02
- Scope: Local access, application status, State Console, CLI/MCP and onboarding
- Related: [ADR-0031](ADR-0031-stackkits-standalone-lifecycle-boundary.md), [ADR-0039](ADR-0039-module-local-compute-profiles.md)

## Context

The old default-link rule promised directly usable local links without DNS or
trust setup. Native authoring uses `home.test`, while compatibility paths also
use `*.home.localhost`. Neither name establishes access from another device:
`.localhost` refers to the device opening the URL, and `home.test` needs an
explicit resolver path. Internal service health does not prove browser access,
first-user setup, or application recovery.

## Decision

The existing application lifecycle and runtime evidence remain the authorities.
Status derives five independent axes: installed, reachable, setup, usable and
recoverable. Every positive claim must retain its Plan/workload binding, time,
source and applicable observation scope. Missing evidence remains unverified.
A probe from the CLI host proves access from that host; it cannot certify all
household, LAN, mobile, VPN or public clients.

Generated links must identify the intended access context. A target-local
`.localhost` link must not be presented as a LAN address. A `home.test` value
is intent until its resolver and route are established. Home access remains
private by default; public exposure requires explicit declared intent and its
governed realization.

Onboarding must make required resolver, certificate trust, device enrollment
and first-user setup steps visible. Approved capability adapters may automate
them. A missing adapter or missing client evidence produces a concrete next
step, never a ready badge. TLS verification must not be disabled to conceal an
unfinished trust step. Generated artifacts remain outputs and must not be
manually patched to make a printed link work.

An encrypted archive, a snapshot or a verified staged copy establishes only
the corresponding data evidence. Recoverable application status requires
verified application activation and a relevant functional check through the
existing restore lifecycle. An internal health probe alone establishes neither
client reachability nor application usability.

## Consequences and implementation status

This supersedes the unconditional zero-configuration link guarantees in
Golden Rules §1.10–11. Stable links and guided setup remain product goals;
observable scope and honest readiness are mandatory behavior.

The derived status/State Console projection is implemented. `stackkit verify
--http --json` and the opt-in `stackkit status --http --json` now produce the
same Plan- and Apply-bound route observation. A positive result proves only the
`verifier-host` vantage; each failed probe retains a bounded failure class.
Plan- and Apply-bound setup receipts now cover the selected native standalone
Photos, Cloudreve Files, Jellyfin Media and Home Assistant adapters, including authenticated owner readback and
temporary session cleanup. Manual personal onboarding remains visible when an
application requires it. Vaultwarden adds signed invitation preparation through
existing administrator-token custody; personal registration, keys and client
decryption remain with the official client and cannot become a setup-complete
claim from the admin readback. Restore verification now also checks the selected
standalone application deployments through their existing product observation
contracts, including actual HTTP status codes. This is runtime health evidence;
database/content checks, client vantages and the final live rollout remain
pending. This decision does
not add a v0.x publication gate.
