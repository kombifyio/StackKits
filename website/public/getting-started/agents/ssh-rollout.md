# Prompt: Generate And Apply Through SSH

Roll out BaseKit to a reachable SSH target. Keep the workflow non-interactive and evidence-based.

Confirm target host, SSH user, SSH key path, admin email, and whether the host is dedicated to StackKits.

Run:

```bash
stackkit init base-kit --non-interactive --admin-email <email>
stackkit prepare --dry-run
stackkit validate
stackkit generate --force
stackkit plan
stackkit apply
stackkit verify --http --json
```

If remote host preparation fails, stop and report the exact failing command, stderr summary, and likely failure class.

