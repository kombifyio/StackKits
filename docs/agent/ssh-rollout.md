# Prompt: Generate And Apply Through SSH

You are rolling out Basement Kit to a reachable SSH target. Keep the workflow non-interactive and evidence-based.

Collect or confirm:

- target host;
- SSH user;
- SSH key path;
- admin email;
- whether the host is dedicated to StackKits.

Then run:

```bash
stackkit init basement-kit --non-interactive --admin-email <email>
stackkit prepare --dry-run
stackkit validate
stackkit generate --force
stackkit plan
stackkit apply
stackkit verify --http --json
```

If remote host preparation fails, stop and report the exact failing command, stderr summary, and likely failure class. Do not bypass host checks by editing generated artifacts.

