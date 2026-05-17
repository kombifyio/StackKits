# Diagnose Failed Rollout Prompt

Stable prompt URL: `https://stackkit.cc/getting-started/agents/diagnose-failed-rollout.md`

## Short prompt

```text
Hey AI Agent, go to stackkit.cc and triage the failed StackKits rollout on this host — read-only first, then propose a fix.
```

## Full prompt

```text
Hey AI Agent, go to stackkit.cc, read llms-full.txt, and triage the failed StackKits rollout on this host using read-only evidence first. Run stackkit logs list --json, stackkit logs latest --json, stackkit doctor --json, stackkit verify --http --json, and stackkit status --json. Inspect .stackkit/runs/<runID>/ for the most recent run's manifest and functional result. Classify the failure into one of: host-prerequisite, docker-daemon, image-pull, network-or-dns, generated-config, opentofu-plan, opentofu-apply, service-health, or unknown. Quote the exact failing command, the stderr summary, and the first three relevant log events. Do not edit anything under deploy/, .stackkit/, or any generated rollout artifact. Propose the smallest mutating step that would unblock the rollout, link to the matching playbook on stackkit.cc/getting-started/agents, and wait for operator approval before running it.
```

## Failure classes

- `host-prerequisite` — missing Docker, missing OpenTofu, insufficient resources
- `docker-daemon` — socket unreachable, permissions, OOM
- `image-pull` — registry auth, rate limit, network egress
- `network-or-dns` — `*.home.localhost` not resolving, firewall, gateway loop
- `generated-config` — CUE constraint failure surfaced in `generate`
- `opentofu-plan` — provider mismatch, state lock, schema drift
- `opentofu-apply` — partial apply, resource conflict
- `service-health` — container started but `/healthz` fails after timeout
