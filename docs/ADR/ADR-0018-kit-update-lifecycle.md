# ADR-0018 — Kit-Update-Lifecycle (Channels, Atomic-Snapshot, Compatibility-Resolver)

**Status:** Accepted (2026-05-08)
**Author:** Marcel Makosch (decisions D1-D4 captured in project planning and Beads)
**Supersedes:** —
**Superseded by:** —
**Related:** ADR-0008 (CUE Decision Logic), ADR-0009 (Three-Tier Provisioning), ADR-0010 (DB-First StackKit Registry), ADR-0012 (StackKit Kit Definition), ADR-0014 (Kit Lifecycle Operations + DB-Driven Section Mapping), ADR-0016 (Backup — Single-Engine Kopia), ADR-0017 (Discovery-Driven Module Proposals)

## Context

Bis 2026-05 ist der Pfad **„User hat Kit v1.0 ausgerollt → Team released v1.1 → User aktualisiert sauber"** nicht systematisch end-to-end gelöst. Heute geschieht ein impliziter „re-apply" über `stackkit apply` — der OpenTofu-Plan/Apply rendert mit aktuellen CUE-Quellen neue Templates und propagiert die Änderungen. Das funktioniert, deckt aber nicht ab:

1. **Versions-Identität**: Ein Operator weiß nach `apply` nicht eindeutig, **welche** Kit-Version er gerade laufen lässt. `.stackkit/state.yaml` hält Variant + Status, aber keine pinned `sk_stackkit.id`/Semver-Metadaten.
2. **Channel-Konzept**: `sk_stackkit` und `sk_module_version` haben semver-Lineage, aber keine Reife-Klassifikation. Es gibt kein `release_channel`-Feld, kein „latest stable"-Begriff. Operatoren können nicht wählen, ob sie auf `edge`/`beta`/`stable` hören wollen.
3. **Compatibility-Resolver**: Wenn Kit v1.1 in `stable` released wird und ein referenziertes Module nur in `edge` als Update verfügbar ist — was passiert? Heute: undefiniert.
4. **Pre-Flight-Diff** vor Apply ist `tofu plan`, das ist nützlich aber nicht user-friendly aufbereitet (Module-Bumps, Channel-Map, contract_hash-Verify).
5. **Rollback-Anker**: Heute keine garantierte Snapshot-Strategie vor Apply. Wenn Apply auf v1.1 fehlschlägt mid-flight, gibt es keinen offiziellen Rückweg. Kopia (ADR-0016) ist als Backup-Engine standardisiert, wird aber nicht atomar mit Apply gekoppelt.
6. **Multi-Node-Update-Koordination**: Phase 4 (BreakGlass-Plan) macht Master+Worker-Join, aber `stackkit cluster upgrade` existiert nicht — Multi-Node-Setups bleiben Single-Node-Updates pro Hand.
7. **Update-Notifikation**: `stackkit doctor` checkt System-Health, nicht Kit-Updates. Operator merkt neue Releases nicht.

Alle sieben Lücken sind nicht kritisch für Initial-Installation (das funktioniert), aber sie blockieren das Produktversprechen „kombify-StackKits ist ein Continuous-Lifecycle-Produkt, nicht ein One-Shot-Installer".

## Decision

Wir bauen einen **Kit-Update-Lifecycle** ueber drei Phasen auf, mit Phase 1 als Haertungs-Iteration auf dem Base Kit (Single-Node).

### 1. Dual-Level Release-Channels

Sowohl `sk_stackkit` als Phase-1-Kit-Versionsträger als auch `sk_module_version` bekommen eine `release_channel`-Spalte:

| Wert | Bedeutung |
|---|---|
| `edge` | Frisch released, noch nicht im Production-Use validiert. Default für `stackkit module release` und `stackkit kit publish`. |
| `beta` | In min. einer Production-Umgebung verifiziert, Operator-Feedback eingearbeitet. Stable-Kandidat. |
| `stable` | Validiert für general use. Default für `stackkit kit upgrade` ohne explicit `--kit-channel`. |

**Promotion** ist **manuell** via Admin-UI (`/sk-stackkits/[slug]/versions`, `/sk-modules/[slug]/versions`) — nicht auto-cascade. Promotion-Endpoint POST `/api/v1/sk/registry/{stackkits|modules}/{id}/versions/{vid}/channel` mit body `{channel, reason}`. AFTER-Trigger schreibt `sk_stackkit_audit_log` mit `action='channel_promote'`, `target_kind ∈ {'stackkit','module'}` (analog ADR-0014-Lock-Audit).

**Auto-Promotion** (edge → beta → stable über demand-signal aus `sk_intent_telemetry`) ist explizit **kit-update-phase-3** und nicht Teil dieser ADR.

### 2. Compatibility-Resolver

Wenn Operator `--kit-channel=stable` wählt, müssen alle Module-Versions im Kit ein konsistentes Mapping bekommen. Resolver-Logik (server-side, View `sk_kit_module_compat`):

1. Bevorzugt **gleicher Channel**: für Kit-Channel `stable` zuerst Module-Versions in `stable` suchen.
2. Fallback-Hierarchie pro Module: `stable > beta > edge`. Wenn kein Module in stable verfügbar, fällt Resolver auf beta zurück.
3. Operator-Override via `--module-channel=<c>` (überschreibt Resolver-Default).
4. Reason-Annotation: jede aufgelöste Module-Version trägt `reason ∈ {'matched','fallback','override'}` — CLI zeigt Channel-Map mit Annotationen vor Apply.

Resolver ist exponiert als GET `/api/v1/sk/compat/resolve?kit_slug=&kit_version=&kit_channel=&module_channel=`.

### 3. Atomic-Snapshot vor Apply (Tofu + Kopia)

Jeder `stackkit kit upgrade` triggert vor `tofu apply` eine **zweistufige Snapshot-Sequenz**, beide Pflicht:

```
Step 9a (Kopia):  kopia snapshot create  --tag=pre-update-<ts>-<old-kit-version>  <persistent-volumes>
Step 9b (Tofu):   cp deploy/terraform.tfstate  .stackkit/snapshots/<ts>-<old-kit-version>/state.tfstate
Step 9c (Manifest): write .stackkit/snapshots/<ts>-<old-kit-version>/manifest.yaml
                  (kopia_snapshot_id, tofu_state_path, old/new versions, channel-map)
```

Wenn Step 9a oder 9b fehlschlagen → Apply wird **verweigert**, klare Fehlermeldung. Wenn Step 9c (Apply) selbst fehlschlägt → Operator hat beide Anker und kann via `stackkit kit upgrade rollback --to-snapshot=<ts>` zurück. Rollback macht: `tofu state push <snapshot>/state.tfstate` + `tofu apply` (auf alte Templates) + `kopia snapshot restore <kopia-id>` (interaktiv mit Volume-Liste).

**Kopia-Repo ist Pflicht-Vorbedingung.** `stackkit kit upgrade` schlägt Fast-Fail wenn `stackkit backup status` nicht `configured` zurückgibt. Kein `--skip-snapshot`-Override.

### 4. CLI-Surface

Neue Sub-Commands unter `stackkit kit`:

```
stackkit kit upgrade [--to=<ver|channel:stable>] [--kit-channel=<c>] [--module-channel=<c>]
                     [--allow-channel-mismatch] [--dry-run] [--auto-approve] [--snapshot-id=<id>]

stackkit kit upgrade rollback --to-snapshot=<ts> [--auto-approve]

stackkit doctor --check-updates    # erweitert: zeigt verfügbare neuere Versions
```

Default-Verhalten (`stackkit kit upgrade` ohne Flags): `--to=channel:stable`, `--kit-channel=stable`, Resolver mit Fallback. Confirm-Gate vor Apply (kein silent-update).

### 5. Server-Side Node-Inventory

Neue Tabelle `sk_node_deployment` spiegelt für Admin-Operatoren, **welche Node welche Version laufen lässt**. CLI postet nach erfolgreichem Apply via PATCH `/api/v1/sk/node-deployments/<id>`. Source-of-Truth bleibt `.stackkit/state.yaml` auf der Node — der DB-Mirror ist Read-Side für Multi-Node-Übersicht und kit-update-phase-2-Voraussetzung (Rolling-Update).

### 6. Phasen-Sequenz

| Plan | Scope | Status |
|---|---|---|
| Phase 1 | Single-Node Base Kit, alle 6 Bausteine oben | Diese ADR |
| Phase 2 | `stackkit cluster upgrade --kit-version <v>`: Master-First-Rolling, Worker-Drain+Update+Restore, River-Worker-Erweiterung fuer Massen-Updates | Tracked in Beads |
| Phase 3 | Auto-Promotion `edge -> beta -> stable` ueber demand-signal (Bauteile aus ADR-0017 Phase 4b), AI-assistierte Channel-Mismatch-Resolution | Tracked in Beads |

## Alternatives Considered

| Alternative | Rejected because |
|---|---|
| **Nur Kit-Komposition-Channels** (Module-Versions semver-only) | Einfacheres Mental-Model, aber blockiert das Use-Case „edge-Module in stable-Kit testen". Operator-Feedback aus 2026-04 zeigte dieses Pattern als wiederkehrend. Dual-Channel kostet ~30% mehr DB-Schema-Arbeit, lohnt sich. |
| **Module-Only Channels** (Kit immer „latest stable des Sets") | Verschiebt Komplexität ins Resolver-Frontend; Operator hat keinen Kit-Lifecycle-Anker mehr für Audit-Trail. Dual ist sauberer. |
| **Tofu-State-Copy als alleiniger Rollback-Anker** (kein Kopia) | Schnellere Updates (Sekunden statt Minuten). Aber: Daten-Migrationen während Apply (z.B. Postgres-Schema-Migration in upgrade-Container) können bei Mid-Flight-Crash zu inkonsistenten Volumes führen. Tofu-State allein kann das nicht heilen. Kopia-Pflicht ist die sicherere Wahl, auch wenn sie Update-Time von Sek auf Min hebt. |
| **Auto-Promotion per Telemetry-Cron** (kit-update-phase-3-Inhalt jetzt) | Premature. Ohne kit-update-phase-1-Härtung gibt es keine zuverlässige Manual-Promotion-Baseline. Auto-Promotion braucht zudem ein definiertes demand-signal — Diskussion läuft im kombify-Agents-Repo (ADR-0017). |
| **Multi-Node in Phase 1** (Single-Master + Worker-Update gemeinsam) | Verdoppelt Phase-1-Scope. Master-First-Rolling braucht Cluster-Failover-Coordination (Phase 5 BreakGlass-Plan), Worker-Drain braucht Service-Mesh-Awareness. Phase-1 = Single-Node, Phase-2 = Multi-Node ist sauberer. |
| **Snapshot via ZFS/LVM** (Block-Level statt Kopia) | Plattform-Abhängig (Linux only mit spezifischer Storage-Config). Kopia ist OS-portable, ist bereits ADR-0016-Standard. |

## Consequences

### Positive

- **Operator-Sicherheit**: Atomic-Snapshot vor jedem Update + verlässlicher Rollback-Pfad. Eliminiert „mid-flight-crash hat das System unrettbar gemacht"-Szenario.
- **Channel-Klarheit**: Operator weiß, in welchem Reife-Stadium er ist. `stackkit kit upgrade --kit-channel=stable` ist die Production-Default-Linie.
- **Audit-Trail**: Alle Channel-Promotions in `sk_stackkit_audit_log` mit `actor`+`reason` queryable.
- **Server-Side-Inventory**: Admin sieht „welche Nodes laufen welche Version" in einem Query (`sk_node_deployment`).
- **CUE-Authority bleibt intakt**: `contract_hash`-Gate (ADR-0010) wird beim Update für Kit + alle Module verifiziert. Drift zwischen DB und Disk-CUE wird Apply abbrechen.

### Negative

- **Update-Time steigt** von Sekunden (heute: tofu-apply) auf Minuten (Kopia-Snapshot 1–10 Min je nach Volumegrösse + tofu-apply). Wird in Runbook (`docs/runbooks/kit-upgrade.md`) klar kommuniziert. Kein Override-Flag.
- **Kopia-Repo wird Pflicht**. Operator muss `stackkit backup configure` machen, bevor er updaten kann. Friction für „nur-mal-schnell-updaten"-Workflows. Mitigation: `stackkit init` führt Backup-Setup als Standard-Schritt ein (kit-update-phase-1-T-followup).
- **Schema-Migrations koordiniert mit CLI-Release**: Migrations 000107–000109 müssen vor StackKits-CLI-Release auf Render-DB gelaufen sein. Standard-Pattern aus ADR-0010 (additive Spalten + Backfill, dann CLI), aber Coordination-Cost.
- **Compatibility-Resolver-View** wird mit jeder neuen Kit/Module-Version stale wenn nicht refreshed. Wir starten als regular VIEW (kein Materialized) — falls Performance-Hit messbar wird, Migration auf MV mit Refresh-Trigger.
- **Multi-Node ist explizit not-yet**. Operatoren mit Multi-Node-Setups müssen Phase-2 abwarten oder per Hand auf jeder Node updaten. Wird im Runbook deutlich gemacht.

### Neutral

- **`stackkit doctor --check-updates`** ist additive UX, ändert keine bestehenden Pfade.
- **CUE-Enums** (`#ToolType`, `#ToolCategory`, `#IaCDefaults`) werden im Repo zentralisiert, aber bestehende Module-Definitionen bleiben unverändert (keine Breaking-Changes).

## Implementation Status (kit-update-phase-1)

| Component | Status | Evidence |
|---|---|---|
| ADR-0018 (this document) | ✅ Shipped | `docs/ADR/ADR-0018-kit-update-lifecycle.md` |
| North-Star Doku | ✅ Shipped | `docs/KIT_UPDATE_LIFECYCLE.md` |
| Implementation scope | ✅ Shipped | ADR-0018 + `docs/KIT_UPDATE_LIFECYCLE.md` |
| Phase-2 + Phase-3 Skizzen | ✅ Shipped | Tracked in Beads |
| CUE: `#ToolType` + `#ToolCategory` | ✅ Shipped | `base/tool_categorization.cue` (+ tests) |
| CUE: `#IaCDefaults` | ✅ Shipped | `base/iac-defaults.cue` (+ tests) |
| `base-kit/stackfile.cue` consumes `#IaCDefaults` | ✅ Shipped | `base-kit/stackfile.cue` |
| IaC: `iac/defaults/main.tf` + `variables.tf` + `outputs.tf` + README | ✅ Shipped | `iac/defaults/` |
| Go snapshot package (Kopia + Atomic) | ✅ Shipped | `internal/snapshot/{kopia,atomic}.go` (+ tests) |
| Channel-resolver client | ✅ Shipped | `internal/registry/channel_resolver.go` (+ tests) |
| CLI: `stackkit kit upgrade` | ✅ Shipped | `cmd/stackkit/commands/kit_upgrade.go` (+ tests) |
| CLI: `stackkit kit upgrade rollback` | ✅ Shipped | `cmd/stackkit/commands/kit_upgrade_rollback.go` (+ tests) |
| CLI: `stackkit doctor --check-updates` | ✅ Shipped | `cmd/stackkit/commands/doctor.go` (+ tests) |
| `DeploymentState` version metadata fields | ✅ Shipped | `pkg/models/models.go` |
| Operator-Runbook | ✅ Shipped | `docs/runbooks/kit-upgrade.md` + `kit-rollback.md` |
| DB: Migrations 000107–000109 | ✅ LIVE (applied to Render `kombify-stackkits` Postgres) | `kombify-DB/migrations/000107_sk_release_channels.{up,down}.sql`, `000108_sk_node_deployment.{up,down}.sql`, `000109_sk_compatibility_resolver_view.{up,down}.sql` (renumbered from initial drafts 000090–000092 because slots 000086–000106 were claimed in parallel by other repos before apply) |
| Admin: Channel-Promotion + Resolver-Endpoint + Node-Deployments + UI | ✅ Shipped | `kombify-Administration` (`frontend/src/lib/server/sk/channel-service.ts`, `/api/v1/sk/registry/{stackkits,modules}/[id]/{versions/[vid]/}channel`, `/api/v1/sk/compat/resolve`, `/api/v1/sk/node-deployments`, `docs/STACKKITS_CHANNEL_PROMOTION.md`) |
| Test-Coverage (Update-Pfade >=35%/45%) | ⏳ T7 | tracked in Beads |
| VM-Smoketest v1.0→v1.1 + Rollback | ⏳ T9 | `tests/vm/kit_upgrade_test.go` |

## Lessons learned (post-deploy)

Captured 2026-05-08 after the Render production migration of 000107–109.

- **Sqlc-000106-Fix**: sqlc could not generate Go code for the new migrations because slot 000106 (added by another repo) introduced a custom domain type. Workaround: `sqlc-schema/000_pre_migrations_types.sql` overrides — pre-loaded type definitions ahead of `migrations/`. Kept sqlc green without re-ordering migrations.
- **000067-Replay-Fix**: Replay against a fresh shadow DB tripped on `admin_*_desk_handoff` views (introduced in 000067) because intermediate migrations broaden two columns. Fix: rewrite the views with column-tolerant `*` projections + explicit cast to the widened type. Now safe to replay across all later migrations.
- **GO_VERSION 1.26.2 -> 1.26.3 bump**: Buildkite bootstrap container was pinned to 1.26.2 and started failing on the new generic-typed snapshot helpers. Bumping the bootstrap image to 1.26.3 was sufficient — no Go-code change needed.
- **Renumbering 000090-92 -> 107-09**: Original ADR planned slots 000086–000088 (later 000090–000092 in the implementation plan), but kombify-DB reached migration 000106 between draft and apply because parallel feature work in other repos consumed the in-between slots. Renumbering was mechanical but reinforced the rule that migration numbers are reserved at PR-merge time, not at draft time.
- **Best-effort PATCH on `sk_node_deployment` is enough**: Operators with intermittent connectivity to the Admin API still complete upgrades successfully because the node-local `.stackkit/state.yaml` remains the SSoT. The Admin mirror catches up on the next successful PATCH and is purely an Admin-side fleet view.

## References

- ADR-0008 — CUE Decision Logic
- ADR-0009 — Three-Tier Provisioning
- ADR-0010 — DB-First StackKit Registry (`contract_hash`-Gate)
- ADR-0012 — StackKit Kit Definition (lock + canonical-hash)
- ADR-0014 — Kit Lifecycle Operations (audit-pattern für Channel-Promotion)
- ADR-0016 — Backup Single-Engine Kopia (Pflicht-Vorbedingung)
- ADR-0017 — Discovery-Driven Module Proposals (Auto-Promotion-Path für phase-3)
- Phase execution: Beads and [ROADMAP.md](../../ROADMAP.md)
