---
name: files-owner-setup
description: Set up or verify the administrator of an applied native Cloudreve Files workload using private local credentials.
---

# Files administrator

## Owner setup

For an applied native `standalone-compose` Files workload, create the private
file `.stackkit/setup/files-owner.json` with your chosen credentials:

```json
{
  "email": "owner@example.com",
  "password": "<your unique password>",
  "language": "en-US",
  "allowFirstOwnerRegistration": false
}
```

Restrict the file to its workspace owner. `language` is optional. Pass only
the workspace-relative file path to the CLI or MCP; keep passwords out of tool
arguments, generated artifacts and lifecycle evidence.

For a fresh installation whose first administrator you are creating, set
`allowFirstOwnerRegistration` to `true`. Leave it `false` when verifying an
existing account. Cloudreve's public API does not identify an empty user store;
this explicit choice permits registration if your email is absent. Check the
email carefully: an existing installation could create an ordinary account,
which cannot pass administrator verification.

```sh
stackkit setup files --credentials-file .stackkit/setup/files-owner.json --owner-approve --json
```

The command verifies the current Plan, signed Apply, exact local container and
pinned application version. It verifies an existing administrator login or
registers the first account when explicitly requested, then requires both current
user and administrator-only readback. A normal user login cannot complete the
owner setup. Temporary setup sessions are revoked before the signed result is
saved. If cleanup fails, setup remains incomplete.

Open Files through its declared private route and sign in with the same
credentials. Configure storage, quotas, registration and sharing in Cloudreve's
administration screen. Setup does not certify an upload/download, a household
client connection, backup recovery or those personal storage choices. Files
does not accept `--complete-onboarding`; its native receipt records the verified
administrator account. If interrupted, retry the reported `--operation-id`.

The native adapter is independent of PocketID session handoff and hosted
Kombify services. Other deployment adapters retain their declared manual setup
until their own implementation is available. Cloudreve exposes a product API;
do not invent a separate Files MCP server.
