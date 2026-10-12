---
name: documents-owner-setup
description: Verify the custodied administrator of the applied Paperless-ngx workload.
---

# Documents owner login

After the existing standalone lifecycle applies Documents, use:

```sh
stackkit setup documents --owner-approve --json
```

The command authenticates the local `owner` administrator that Paperless creates
from its already-custodied `owner-password` at startup. It observes the active
staff/superuser through the admitted application API and records the existing
Plan-bound setup result. It does not create an account, reset a password, mint
an API token, or create a credential file. Omit `--credentials-file` and
`--complete-onboarding`; no separate onboarding action exists.

This local administrator is the existing break-glass account, whose email is
deliberately separate from the Pocket ID owner. Its login proof does not prove
Pocket ID SSO, document ingestion, OCR, or backup/restore. MFA refusal remains
a refusal; setup does not bypass it or fall back to another authentication mode.

Correct the reported cause, then retry the same pending operation with
`--operation-id ID`. Keep the existing account, data and secret custody.

Pinned API contract: Paperless-ngx 3.1.3 enables
[Basic authentication with MFA refusal](https://github.com/paperless-ngx/paperless-ngx/blob/v3.1.3/src/paperless/auth.py)
in its [REST settings](https://github.com/paperless-ngx/paperless-ngx/blob/v3.1.3/src/paperless/settings/__init__.py).
The observation uses the filtered [user API](https://github.com/paperless-ngx/paperless-ngx/blob/v3.1.3/src/paperless/views.py),
never the profile API that can include an existing authentication token.
