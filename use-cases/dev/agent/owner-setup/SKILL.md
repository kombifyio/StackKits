---
name: dev-owner-setup
description: Verify the custodied administrator of the applied Gitea or Forgejo workload.
---

# Dev owner login

After the existing standalone lifecycle applies Dev, use:

```sh
stackkit setup dev --owner-approve --json
```

The selected Gitea or Forgejo runtime already bootstraps `owner` with the
workload's `owner-password` and the local owner's email. Setup reuses that
custody, authenticates the current user through the admitted application API,
and requires the exact active administrator and email before recording the
existing Plan-bound setup result. It never creates or resets an account, mints
a token, or creates a credential file. Omit `--credentials-file` and
`--complete-onboarding`; no separate onboarding action exists.

An authentication or MFA refusal remains blocked. Correct the reported cause
without replacing the existing custody and retry with `--operation-id ID` when
resuming. This proof covers the local administrator login; Git push/clone,
Pocket ID SSO, CI runners and backup/restore remain separate outcomes.

Pinned API contracts:
[Gitea 1.27.3 current user](https://github.com/go-gitea/gitea/blob/v1.27.3/routers/api/v1/user/user.go)
and [Forgejo 16.0.5 current user](https://codeberg.org/forgejo/forgejo/src/tag/v16.0.5/routers/api/v1/user/user.go).
Both return the authenticated user's ID, login, email, active/admin status and
login prohibition. Authentication policy remains owned by the application.
