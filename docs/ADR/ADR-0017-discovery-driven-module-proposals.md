# ADR-0017 — Discovery-Driven Module Proposals

**Status:** Accepted (2026-05-06)
**Author:** Marcel Makosch (decisions D1-D6 in the strategic plan)
**Supersedes:** —
**Superseded by:** —
**Related:** ADR-0008 (CUE Decision Logic), ADR-0009 (Three-Tier Provisioning), ADR-0010 (DB-First StackKit Registry), ADR-0012 (StackKit Kit Definition)

## Context

Until 2026-05, the only path for a new tool to become a curated kombify-StackKits module was **ADR-0009 Tier-3 Promotion**: AI-Assisted patterns (Tier 2) that recur ≥N times per month get reviewed by product, and engineering writes a curated CUE module + ADR + PR. The trigger is *user-intent-pattern-frequency* — modules curate what users keep asking for.

The 2026 Discovery -> Evaluate -> Watch -> Promote pipeline (kombify-Agents Phase 1-4) introduces a **second, orthogonal trigger**: an agent automatically discovers tools across the 2026 Homelab/Self-Hosting taxonomy, evaluates them, and wants to surface candidates the kombify team should consider curating *even if no user has asked for them yet*.

Without this ADR, two governance ambiguities exist:
1. Can an agent author CUE modules?
2. If two trigger paths exist, which one wins / which is canonical?

## Decision

### 1. Agents may DRAFT, never AUTHOR-OF-RECORD

The kombify-Agents `promotion-drafter` agent (Phase 4) generates:
- a **draft `module.cue`** skeleton based on the kombify base schemas,
- a **draft `stackkit.yaml`** snippet referencing the new module,

and persists both as the `evidence.draft_cue` / `evidence.draft_stackkit_yaml` fields of an `SkModuleProposal` row in kombify-Administration (status=`'proposed'`).

**An agent is never the writer of record for a CUE module.** The author-of-record is always the engineer whose `git commit` lands on `kombify-StackKits/main`. ADR-0008's "CUE is the source of truth, written by humans" stance is preserved.

### 2. Two parallel trigger paths

Two orthogonal paths to a curated module both terminate in the same gate (engineer-led PR):

| Path | Trigger | Source-of-record | Lifecycle table |
|---|---|---|---|
| **ADR-0009 Tier-3 Promotion** | user-intent-pattern-frequency (≥N occurrences in `sk_intent_telemetry` within window) | Tier-2 AI-Assisted patterns | `sk_tier3_promotion` (governance) → engineer-PR |
| **ADR-0017 Discovery-Driven** *(new)* | agent discovers + evaluator says `adopt`/`trial` + admin clicks Promote | promotion-drafter agent | `sk_module_proposal` (kombify-Administration) → engineer-PR |

**The two paths are NOT merged.** They feed different evidence shapes into engineering review and have different velocity profiles (intent-frequency is slow + high-confidence; discovery-driven is fast + lower-confidence). Engineers reviewing PRs may see proposals from either path and treat them on their merits.

### 3. No auto-promotion in Phase 4a

The first cut of the discovery-driven path is **manual-button-only**: the operator clicks **"Promote"** in `/stackkits/watched-tools`. There is no automatic agent-triggered creation of `SkModuleProposal` rows. Auto-promotion (Phase 4b) requires a defined *demand signal* (wizard-search-count? bookmark-count?) that must be specified before that capability is enabled.

This is conservative on purpose — locked-in decision **D4** of the strategic plan: avoid curating modules nobody has asked for.

### 4. Evidence shape

Every `SkModuleProposal.evidence` JSON written by `promotion-drafter` MUST contain:

```json
{
  "source": "agent-draft",
  "draft_cue": "<CUE module source>",
  "draft_stackkit_yaml": "<YAML snippet>",
  "evaluator_run_id": "<UUID — SkToolEvaluationRun.id>",
  "generated_at": "<ISO-8601>"
}
```

Manual proposals (created via Admin UI without the agent) MUST set `source: "manual"` and may omit `draft_cue` / `draft_stackkit_yaml`.

### 5. Promote-to-PR is a separate Admin action

The `SkModuleProposal` row is a *staged draft*. The Admin UI surfaces a **"Promote to PR"** button on the proposal detail page. When clicked:
1. Admin uses GitHub MCP to open a PR on `kombify-StackKits/main`,
2. Branch name: `module-proposal/<slug>`,
3. Files: `modules/<slug>/module.cue` (from `evidence.draft_cue`) + `modules/<slug>/stackkit.yaml` patches (from `evidence.draft_stackkit_yaml`),
4. PR body links back to `SkModuleProposal.id` for traceability,
5. PR co-authored-by `promotion-drafter` (the agent), but `Author:` is the human who clicked.

**The PR commit is the author-of-record.** When the PR merges, `release-please` bumps the module version normally — there is no special discovery-driven release path.

## Consequences

**Positive:**
- The kombify-Agents pipeline becomes a real proposal-generator, not just an inventory of "tools we found".
- Two orthogonal trigger paths give engineering parallel signal sources without conflict.
- ADR-0008 (CUE-as-truth) stays intact — agents never write CUE-of-record.
- The Admin lifecycle (`SkModuleProposal.status` transitions: `proposed` → `merged` / `rejected` / `superseded`) becomes the single audit trail for both paths.

**Negative:**
- Engineers reviewing PRs now have to grade two qualitatively different evidence shapes (intent-frequency vs evaluator-verdict). Mitigation: PR template that surfaces which path triggered the proposal.
- Agent-draft CUE quality is variable. Phase 4 ships without `validate_against_cue_schema` (deterministic `cue vet` gate); engineers must catch invalid CUE in review. Phase 4+ adds the deterministic gate once the agent reaches >95% valid-output rates.

**Neutral:**
- `sk_module_proposal` table already exists (kombify-Administration migration 000100); no schema changes required.

## Implementation status

| Component | PR | Status |
|---|---|---|
| Admin `POST /api/v1/sk/module-proposals` endpoint | kombify-Administration#38 | open |
| `promotion-drafter` Cloud Run agent scaffold | kombify-Agents#11 | open |
| `validate_against_cue_schema` deterministic gate | — | Phase 4+ |
| Admin **Promote** button on `/stackkits/watched-tools` | — | Phase 4 follow-up |
| Admin **Promote-to-PR** button + GitHub MCP integration | — | Phase 4 follow-up |
| Auto-trigger on `adopt` + demand-signal | — | Phase 4b (deferred) |

## References

- Strategic discovery decisions D1-D6 are tracked in kombify-Agents project planning and Beads.
- ADR-0008 — CUE Decision Logic
- ADR-0009 — Three-Tier Provisioning (intent-frequency path)
- ADR-0010 — DB-First StackKit Registry
- ADR-0012 — StackKit Kit Definition (lock + canonical-hash)
