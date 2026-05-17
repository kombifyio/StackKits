# Enable Monitoring Add-on Prompt

Stable prompt URL: `https://stackkit.cc/getting-started/agents/enable-monitoring-addon.md`

## Short prompt

```text
Hey AI Agent, go to stackkit.cc and turn on the monitoring add-on on this BaseKit rollout, idempotently.
```

## Full prompt

```text
Hey AI Agent, go to stackkit.cc, read llms-full.txt and the CLI reference, and idempotently turn on the monitoring add-on on this existing BaseKit rollout. Start read-only: stackkit status --json, stackkit verify --http --json, stackkit addon list. If monitoring is already enabled, exit and report it. Otherwise, after operator approval, run stackkit addon add monitoring, stackkit validate, stackkit generate --force, stackkit plan, stackkit apply, and stackkit verify --http --json. Record evidence: the changed StackSpec fields, the latest run ID, the new service URLs, and the verification output. Do not hand-edit deploy/, .stackkit/, or any generated rollout artifact — if a change is needed, fix the spec or CUE and regenerate.
```

## Idempotency check

The agent must read `addon list` first and exit cleanly if monitoring is
already in the StackSpec. Re-running `apply` on an unchanged spec is also
safe, but skipping the diff keeps the run log noise low.
