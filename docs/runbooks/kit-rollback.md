# Runbook — `stackkit kit upgrade rollback`

> **Phase:** kit-update-phase-1 (Single-Node Base Kit). ADR-0018 §3.
> **Audience:** Operator who needs to revert a recent `stackkit kit upgrade`.
> **When to read:** After a failed upgrade, after a regression caused by an upgrade, or before testing your recovery procedure.

## What this command does

`stackkit kit upgrade rollback` restores a deployment to the state captured by the atomic-snapshot of an earlier upgrade. Phase-1 scope:

1. **Resolve the snapshot directory** — `--to-snapshot=<basename|path>`, fall back to `state.LastSnapshotDir`, fall back to the lexicographically newest entry under `.stackkit/snapshots/`.
2. **Verify** — manifest readable, tfstate file present, declared Kopia snapshots known.
3. **Confirm** — operator approves (skipped under `--auto-approve`).
4. **Restore tfstate** — copy `<snapshot>/state.tfstate` over `deploy/terraform.tfstate`.
5. **Restore Kopia volumes** — `kopia snapshot restore` per `manifest.yaml.kopiaSnapshots[]` entry.
6. **Update state.yaml** — set `KitSemver` back to the manifest's `oldKitVersion`, clear `KitVersionID` and `KitChannel`, mark status `degraded`.

## Phase-1 limitation — templates are not snapshotted

Phase-1 captures `deploy/terraform.tfstate` but **not** the `deploy/*.tf` templates. If you ran `stackkit generate` for the new kit-version before the upgrade, your `deploy/` directory still holds the new-version templates after rollback, while the restored tfstate describes the old-version resources.

**Symptom:** the next `stackkit apply` shows a large diff because tofu wants to bring resources back to the new-version shape.

**Fix (one of the two below):**

1. **Re-checkout the old kit-version's CUE source and regenerate** — recommended for production rollbacks.

   ```bash
   # If the kit lives in a git checkout under <kit>/:
   cd <kit>
   git checkout <old-kit-tag>           # e.g. v1.0.0
   cd ..
   stackkit generate                    # re-renders deploy/*.tf
   stackkit apply                       # no-op against the restored tfstate
   ```

2. **Accept the diff and let tofu fix the templates.** Only viable when the diff is small and you are sure the new-version resources are stable. Run `stackkit apply` and review the plan before approving.

## Common rollback flows

### Latest snapshot (most common)

After a freshly-failed upgrade, the snapshot bundle is still the most recent entry:

```bash
stackkit kit upgrade rollback --auto-approve
# Resolves to .stackkit/snapshots/<ts>-<old>/ from state.LastSnapshotDir.
```

### Specific snapshot by basename

When an upgrade succeeded but you want to revert a few generations back:

```bash
ls .stackkit/snapshots/
# 20260408T120000Z-0.9.0
# 20260501T120000Z-1.0.0
# 20260508T120000Z-1.0.0   <-- last one

stackkit kit upgrade rollback --to-snapshot=20260501T120000Z-1.0.0
```

### tfstate-only rollback (volumes are already healthy)

When the issue is purely in the tofu apply step (e.g. a misconfigured route) and your data is fine:

```bash
stackkit kit upgrade rollback --skip-volume-restore --auto-approve
```

This skips Kopia. Faster and avoids touching disk where data is intact.

### Volumes-only rollback (state is already healthy)

When the new kit-version is fine, but a Postgres migration corrupted data:

```bash
stackkit kit upgrade rollback --kopia-restore-only --auto-approve
```

This skips the tfstate restore. State stays at the new kit-version; volumes go back to pre-upgrade.

## Pre-flight checklist

```bash
# 1. The snapshot exists and is intact?
SNAP=.stackkit/snapshots/<ts>-<old>
ls $SNAP
# Expected: manifest.yaml, state.tfstate

cat $SNAP/manifest.yaml | grep -E 'oldKitVersion|kopiaSnapshots:'
# Expected: oldKitVersion is what you want to roll back TO. kopiaSnapshots
# is a list with one entry per --volumes path you used at upgrade time.

# 2. Kopia repo configured (only required if you will restore volumes)?
stackkit backup status

# 3. Kopia still holds the snapshots?
kopia snapshot list --tag=pre-update=<ts>
# Expected: one row per volume that was snapshotted.

# 4. No live tofu apply in flight?
ls .terraform.tfstate.lock.info 2>/dev/null && echo "LOCKED" || echo "free"
# Expected: free. If LOCKED, wait for the apply to finish or remove
# the lockfile only if you are SURE no tofu process is running.
```

## What the output looks like

```
Rollback plan
  snapshot timestamp:    2026-05-08T12:00:00Z
  rolling back to:       1.0.0
  was upgrading from →:  1.1.0
  tfstate restore:       .stackkit/snapshots/20260508T120000Z-1.0.0/state.tfstate → deploy/terraform.tfstate
  kopia restores:        2 volume(s)
    - /var/lib/postgres ← snap-7e3c
    - /var/lib/vaultwarden ← snap-9f2a
  resolver decisions:    14 module(s), 1 fallback, 0 override

Apply this rollback? [y/N] y
✓ tfstate restored from .stackkit/snapshots/20260508T120000Z-1.0.0/state.tfstate
ℹ restoring /var/lib/postgres from kopia snapshot snap-7e3c ...
ℹ restoring /var/lib/vaultwarden from kopia snapshot snap-9f2a ...
✓ 2 volume(s) restored from kopia
✓ rolled back to 1.0.0 (snapshot 20260508T120000Z-1.0.0)
ℹ templates in deploy/ may still reflect the new kit-version — see docs/runbooks/kit-rollback.md
```

The trailing info line is the Phase-1 limitation reminder — see [Phase-1 limitation — templates are not snapshotted](#phase-1-limitation--templates-are-not-snapshotted).

## Failure modes and recovery

| Symptom | What it means | What to do |
|---|---|---|
| `snapshot ... is not usable: tfstate copy missing` | The `state.tfstate` file inside the snapshot dir was deleted or never written. | Pick an older snapshot. If none exist, you cannot tfstate-rollback — see "Manual recovery" below. |
| `snapshot ... is not usable: load manifest` | `manifest.yaml` is malformed or missing. | Same as above. |
| `kopia restore <path>: ...` | One volume's restore failed mid-flight. | Some volumes may already be restored. Inspect each path, then re-run with `--kopia-restore-only` for the remaining volumes if needed, or restore manually with `kopia snapshot restore <id> <path>`. |
| `kopia repository not configured` | Pre-flight failed before restore began. | Either run `stackkit backup configure` to reconnect to the existing repo, or re-run with `--skip-volume-restore` if state-only rollback is enough. |
| Rollback succeeded but `stackkit apply` shows huge drift | Templates in `deploy/*.tf` are still the new kit-version's. | Apply the [Phase-1 limitation fix](#phase-1-limitation--templates-are-not-snapshotted): re-checkout old kit-version + regenerate. |

## Manual recovery (if rollback CLI cannot help)

This is for the case where `kit upgrade rollback` itself fails or no usable snapshot exists.

```bash
# 1. Inspect what's there.
ls .stackkit/snapshots/
kopia snapshot list

# 2. Manually restore tfstate.
cp .stackkit/snapshots/<ts>-<old>/state.tfstate deploy/terraform.tfstate

# 3. Manually restore each volume.
kopia snapshot restore <kopia-snapshot-id> /var/lib/postgres
kopia snapshot restore <other-id> /var/lib/vaultwarden

# 4. Hand-edit .stackkit/state.yaml — set kitSemver back to the old
#    value and clear kitVersionId + kitChannel so the next 'stackkit
#    apply' re-resolves cleanly.

# 5. Re-render old templates if needed (see Phase-1 limitation above).

# 6. Verify.
stackkit doctor --check-updates
```

## Related

- ADR-0018 — the rollback design.
- [Upgrade runbook](kit-upgrade.md) — what the `--volumes` flag and atomic-snapshot do at upgrade time.
- Kit update lifecycle - the canonical update model and phase status.
