# Autonomous BaseKit Rollout Prompt

Stable prompt URL: `https://stackkit.cc/getting-started/agents/basekit-autonomous-rollout.md`

## Short prompt

```text
Hey AI Agent, go to stackkit.cc and roll out BaseKit autonomously on this fresh host.
```

## Full prompt

```text
Hey AI Agent, go to stackkit.cc, read llms-full.txt, and roll out BaseKit autonomously on this current host. Treat the host as fresh and controlled; do not touch unrelated services. Deploy BaseKit only — Modern Home Lab and HA Kit are alpha/scaffolding and must not be treated as release-ready. Drive the rollout non-interactively: stackkit init base-kit --non-interactive --admin-email <operator-email>, stackkit prepare --dry-run, stackkit validate, stackkit generate --force, stackkit plan, stackkit apply, stackkit verify --http --json. Preserve logs and rollout evidence. Do not hand-edit anything under deploy/, .stackkit/, or generated snapshots — if generated output needs to change, fix CUE or the spec and regenerate. When done, report the Hub URL http://base.home.localhost, the StackKit name base-kit, checked_via_agent=true, no_hand_edited_generated_artifacts=true, a run-manifest schema match against /schemas/stackkit-agent-run-manifest.schema.json, and a functional-result schema match against /schemas/stackkit-agent-functional-result.schema.json.
```

## Required artifacts

- `.stackkit/runs/<runID>/manifest.json` matching `/schemas/stackkit-agent-run-manifest.schema.json`
- `.stackkit/runs/<runID>/functional-result.json` matching `/schemas/stackkit-agent-functional-result.schema.json`
- A reachable Hub at `http://base.home.localhost`

## Useful sources

- Site context: `/llms.txt`, `/llms-full.txt`, `/llms-snippets.txt`
- CLI reference: `/cli`
- API contract: `/api/openapi.v1.yaml`
- MCP helper: `stackkit agent mcp-config`
