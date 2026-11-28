# Write-Fenced Cutover Runbook: Retired Layer-2 PaaS & Legacy Core Coolify 4.1.2 → OpenTofu Stage 1

> **Authority:** [ADR-0054](../ADR/ADR-0054-single-core-engine-and-coolify-extensibility-boundary.md) §2. No migration engine is built for these pre-1.0 installs; this backup, fresh-install and restore path is the supported move.
> Applies to pre-1.0 test installations running curated workloads on retired Layer-2 PaaS executors (`komodo-provider`, `coolify-provider`, `dokploy-provider`, `runtipi-api`) or legacy Full-Core Coolify `4.1.2`.

StackKits fails closed (`ErrLegacyPaaSDetected`) whenever a workspace carries retired Layer-2 PaaS roots or legacy Full-Core Coolify `4.1.2` so two managers can never run dual active copies of the same application or corrupt shared volumes.

## Procedure (Write-Fenced Kopia Backup → Fresh Stage-1 Install → Verified Restore)

1. **Quiesce & Fence Source Writes (Before Final Snapshot):**
   - Stop incoming user writes on the legacy source application(s) (put source app container(s) in maintenance/stopped state while keeping database containers readable if logical dump hooks are needed, or stop the full app stack cleanly).
   - Verify no active StackKit lease or backup job is running.
2. **Capture Final Kopia Snapshot & Custody Checkpoint:**
   - Run a final verified Kopia snapshot covering all application volumes, database dumps (e.g., Immich Postgres + `vectorchord`, Cloudreve SQLite/data, Vaultwarden DB), and encrypted OpenTofu/custody state.
   - For legacy Full-Core Coolify `4.1.2` user projects: additionally export Coolify's Postgres database, `APP_KEY`, and user compose definitions before teardown.
   - Record the snapshot ID and verify `kopia snapshot verify` succeeds.
3. **Provision Fenced OpenTofu Stage-1 Destination:**
   - Initialize a clean OpenTofu Stage-1 StackKit workspace (on a replacement host or after archiving `.stackkit/runtime` into a timestamped backup directory on the same host — **never** deleting the archived source volumes yet).
   - Run `stackkit apply` so Core + curated Stage-1 workloads initialize cleanly behind Access Gate.
4. **Restore Into Fenced Destination & Verify Byte/Schema Integrity:**
   - Restore the recorded Kopia snapshot into the fenced Stage-1 destination volumes while routes remain private behind Access Gate.
   - Run application-specific readback verification:
     - **Photos (Immich):** verify Postgres + `vectorchord` extension health, asset count, and thumbnail/original file readability.
     - **Files (Cloudreve):** verify user/admin login and file download checksum parity.
     - **Vault (Vaultwarden):** verify cipher count and encrypted vault readback.
     - **Optional Coolify 4.7.5+ (`modules/coolify`):** fresh-install digest-pinned Coolify `4.7.5+` (`AUTOUPDATE=false`) and re-import exported user projects.
5. **Owner Commit & Cleanup:**
   - Retain the fenced legacy source archive and Kopia checkpoint until the Owner explicitly confirms every restored route and dataset is live and healthy.
   - Only after Owner confirmation, retire the archived legacy source directories and old PaaS containers.
