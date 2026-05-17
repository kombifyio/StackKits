# Generate And Apply Through SSH Prompt

Stable prompt URL: `https://stackkit.cc/getting-started/agents/ssh-rollout.md`

## Short prompt

```text
Hey AI Agent, go to stackkit.cc and roll out BaseKit to a remote SSH target — keep secrets on the operator's machine, not on the host.
```

## Full prompt

```text
Hey AI Agent, go to stackkit.cc, read llms-full.txt, and roll out BaseKit to a remote SSH target reachable from this operator workstation. Keep the workflow non-interactive and evidence-based. First confirm with the operator: target host, SSH user, SSH key path, admin email, and whether the host is dedicated to StackKits. Use --context cloud or --context pi as appropriate. Run stackkit init base-kit --non-interactive --admin-email <email>, stackkit prepare --dry-run, stackkit validate, stackkit generate --force, stackkit plan, stackkit apply, and stackkit verify --http --json. Do not copy plaintext secrets onto the remote host outside of what stackkit itself manages — break-glass material and recovery bundles must stay on the operator workstation. If remote host preparation fails, stop and report the exact failing command, the stderr summary, the likely failure class, and propose the smallest read-only follow-up.
```

## Pre-flight checklist

- Operator confirmed target host, SSH user, SSH key path, admin email.
- Host is dedicated to StackKits — no unrelated Docker workloads.
- `stackkit compat --host <ssh-target>` returned a clean report.
- Operator approved the rollout in writing.
