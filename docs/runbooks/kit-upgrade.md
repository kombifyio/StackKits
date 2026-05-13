# Runbook — `stackkit kit upgrade`

> **Phase:** kit-update-phase-1 (Single-Node Base Kit). [ADR-0018](../ADR/ADR-0018-kit-update-lifecycle.md).
> **Audience:** Operator with shell access to the node where the kit is deployed.
> **When to read:** Before your first upgrade, when a new kit-version is announced, or when troubleshooting a stalled upgrade.

## What this command does

`stackkit kit upgrade` moves a deployed kit on this node from one version to a newer one in a controlled way. The flow is:

1. **Pre-flight** — verify Kopia is configured (mandatory, ADR-0018 §3) and the Admin API is reachable.
2. **Resolve target** — pick the explicit semver from `--to`, or the latest version in `--kit-channel` (default `stable`).
3. **Resolver call** — query `/api/v1/sk/compat/resolve` for the module-version map. Show fallbacks and overrides.
4. **Tofu plan** — render the diff using the templates already on disk.
5. **Confirm** — operator reviews and approves (skipped under `--auto-approve`).
6. **Atomic-Snapshot** — Kopia snapshot of every `--volumes` path + copy of `deploy/terraform.tfstate` + `manifest.yaml` describing both anchors.
7. **Tofu apply** — apply against the new templates.
8. **State update** — write the new kit-version into `.stackkit/state.yaml` and PATCH `sk_node_deployment` on the Admin side (best-effort).

## Pre-flight checklist

Run these once before your first upgrade:

```bash
# 1. Kopia repository configured?
stackkit backup status
# Expected: configured, with the storage type you set up (filesystem, S3, ...).

# 2. Admin API reachable?
echo $STACKKIT_ADMIN_ENDPOINT
echo $STACKKIT_ADMIN_TOKEN | head -c 8
# Expected: both set; token is a HS256 service-token or operator API key.

# 3. State file has version metadata?
grep -E 'kitVersionId|kitChannel|kitSemver' .stackkit/state.yaml
# Expected: 3 non-empty lines. If missing, re-apply once with the current
# CLI to backfill — older CLI versions did not pin this metadata.

# 4. Doctor reports no blockers?
stackkit doctor --check-updates
# Expected: status=pass, plus an "updates" line listing newer versions
# if any are available.
```

## Common upgrades

### Latest stable

The default — picks the freshest `release_channel='stable'` version of your kit.

```bash
stackkit kit upgrade --dry-run
# Review the plan + channel-map. Re-run without --dry-run to actually apply.

stackkit kit upgrade --auto-approve --volumes=/var/lib/postgres,/var/lib/vaultwarden
```

### Pinned version

When a specific version is required (e.g. for parity with another node):

```bash
stackkit kit upgrade --to=1.2.0 --kit-channel=stable --volumes=/var/lib/postgres
```

If `1.2.0` is not in the stable channel, the CLI errors with `version 1.2.0 not found in channel stable`. Either pin to the channel that holds it (`--kit-channel=beta`) or wait for the promotion.

### Beta / edge testing

Beta and edge channels skip some quality bars by definition. Only run these on test nodes — do not put production stacks on `--kit-channel=edge`.

```bash
stackkit kit upgrade --kit-channel=beta --dry-run
stackkit kit upgrade --kit-channel=edge   # extra confirm gate — Phase 1 design
```

### Mixed channels (Kit stable + Module edge)

Useful when you need an early-access fix in one module while keeping the kit composition stable.

```bash
stackkit kit upgrade --kit-channel=stable --module-channel=edge --dry-run
# Resolver shows which modules picked edge versions and the reason
# (matched if an edge version is available, fallback otherwise).
```

If the resolver falls back outside `--module-channel` for any module, the CLI refuses to proceed unless you pass `--allow-channel-mismatch`.

## Volumes — what to pass to `--volumes`

The atomic-snapshot step calls `kopia snapshot create` for each path in `--volumes`. Pass the host-side persistent volume mounts that hold data you cannot regenerate:

| Module | Typical host path |
|---|---|
| Postgres (Dokploy/Coolify) | `/var/lib/dokploy/postgres` or per-tenant `/var/lib/postgres-<slug>` |
| Vaultwarden | `/var/lib/vaultwarden` |
| Immich | `/var/lib/immich/upload` and `/var/lib/immich/db` |
| Jellyfin | `/var/lib/jellyfin/config` (the `/media` library can be omitted if backed up elsewhere) |
| Step-CA | `/var/lib/step-ca` |

If `--volumes` is empty, the upgrade still runs — but the only rollback anchor is the tfstate copy. Use `--volumes` for anything stateful that a tofu state revert alone cannot heal.

## What the output looks like

```
ℹ target kit-version: 1.1.0 (kv-7e3c..., channel=stable)

Plan summary
  tofu: +0 ~3 -0 (changes=true)
  modules: 14 matched, 1 fallback, 0 override

Apply this upgrade? [y/N] y
✓ snapshot bundle written to .stackkit/snapshots/20260508T120000Z-1.0.0
✓ kit base-kit upgraded 1.0.0 → 1.1.0
```

The `snapshot bundle written to ...` line is the rollback anchor. Note the directory name — you will need it for `stackkit kit upgrade rollback` if anything goes sideways.

## Failure modes and recovery

| Symptom | What it means | What to do |
|---|---|---|
| `kopia repository not configured` | Pre-flight A failed before any state changes. | Run `stackkit backup configure` first. The upgrade did **not** start; nothing to clean up. |
| `version X not found in channel Y` | The resolver could not match `--to` to a published version. | Either drop `--to` (pick latest), pin a different channel, or wait for the version to be promoted. |
| `channel mismatches present` | Resolver fell back outside `--module-channel`. | Either accept with `--allow-channel-mismatch`, or pin `--module-channel` differently. |
| Tofu apply fails mid-flight | The new templates partially applied. State and volumes are in flux. | The snapshot bundle at `.stackkit/snapshots/<ts>-<old>/` holds both rollback anchors. Run [`stackkit kit upgrade rollback`](kit-rollback.md). |
| `KitVersionID missing in state.yaml` | An older CLI version applied this kit. | Run `stackkit apply` once with the current CLI to backfill the version metadata, then upgrade. |

## Timing expectations

The Kopia snapshot dominates wall time on stateful kits.

| Persistent data | Typical Kopia snapshot wall time | Total upgrade wall time |
|---|---|---|
| < 100 MB (e.g. config-only) | seconds | < 1 minute |
| ~10 GB (modest Vaultwarden + Postgres) | 1-3 minutes | 2-5 minutes |
| ~100 GB (Immich photo library) | 5-15 minutes | 8-20 minutes |

These are first-snapshot times; subsequent Kopia snapshots are dedup-aware and faster on the same data.

## Related

- [ADR-0018](../ADR/ADR-0018-kit-update-lifecycle.md) — the design decisions behind this command.
- [ADR-0016](../ADR/ADR-0016-backup-single-engine-kopia.md) — why Kopia is the standard backup engine.
- [Rollback runbook](kit-rollback.md) — how to undo an upgrade.
- [Kit update lifecycle](../KIT_UPDATE_LIFECYCLE.md) - the canonical update model.
