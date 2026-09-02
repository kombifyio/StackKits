---
name: vault-owner-setup
description: Complete the owner-managed Vaultwarden account after a native StackKits apply without exposing the master password or client keys to StackKits.
---

# Vault owner setup

Native Architecture v2 can prepare the server-side owner invitation through
the bounded `vault-owner-invite` action. StackKits owns the digest-pinned
runtime, its route, backup intent and lifecycle evidence. The official
Bitwarden-compatible client owns the personal account's master password,
derived encryption keys and encrypted vault contents.

## Before the first account

1. Finish the local `stackkit apply` and verify the selected Vaultwarden
   runtime. Use the exact Vault route from the current Plan and Apply output;
   do not substitute a guessed hostname, the StackKit `/mcp` endpoint, or a
   container-only address.
2. Open that route over HTTPS with a certificate and hostname the owner
   trusts. Configure the official client with the route's origin and keep the
   client connection separate from the Vaultwarden `/admin` panel.
3. Keep app-local public signups disabled. The native Vaultwarden bundle
   declares `SIGNUPS_ALLOWED=false`; if the administrator panel exposes the
   setting, confirm it remains disabled after the invitation is prepared.

## Invitation and personal account

The closed-signup default means that an administrator must invite the owner's
email before the first personal account can be created. Prepare that invitation
from the applied Plan with the private email-only file:

```sh
stackkit setup vault --owner-approve --credentials-file .stackkit/setup/vault-owner.json --json
```

```json
{"email":"owner@example.com"}
```

The action authenticates the admin session from the exact signed Apply-bound
`secret://` custody, checks the pinned Vaultwarden release and closed signup
setting, and reads back only the bounded user id/status/enabled fields. It
invites a missing user, recognizes an existing invited or registered user, and
fails for a disabled user. It never reinvites an existing user. An invitation
only creates or confirms a server-side user record. It does not establish the
owner's master password, client key, or ability to decrypt a vault.

The native path has no supported command that prints or exports the Vaultwarden
admin token. `stackkit secrets materialize` only creates or reuses owner-bound
custody for declared local `secret://` references; it never emits the token.
If no signed Apply-bound custody is available, leave setup pending and report
the missing administrator handoff rather than copying or revealing the token.

After receiving the invitation, complete registration in the official web or
Bitwarden client. Create the master password and client-side encryption keys
there. Never put a master password, recovery code, encrypted private key,
session token, or client key in a StackKits credential file, CLI/MCP argument,
generated artifact, issue, or repository. The break-glass admin token and the
personal Vault account are separate credentials and must never be treated as
interchangeable.

## Client acceptance

The owner should create a disposable test login item in the official client,
save and sync it, lock the client, unlock it with the local master password,
and read the item back. Delete the disposable item afterwards. This is the
first evidence that the personal client can use its keys; `/alive`, an admin
session, or an admin user listing cannot replace this check. A successful
server-side registration still does not prove client decryption.

## Backup and isolated recovery

Confirm in the current Plan that the Vaultwarden `/data` volume is included in
the governed backup source. Check the authenticated backup history with:

```sh
stackkit backup status --json
```

If the plan has no current snapshot, create one through the owner-approved
native path:

```sh
stackkit backup run --json
```

Use a signed snapshot anchor to perform an isolated staging restore:

```sh
stackkit backup restore sha256:<snapshot-anchor-id> --owner-approve --json
```

An isolated restore receipt proves that the governed Vault data can be read
back into staging. It does not activate data, prove personal client login, or
replace the save/lock/unlock/read check above. Keep activation and any live
client recovery as separate owner-approved operations.

## Honest handoff state

Until an independent client-side acceptance result exists, report the stages
separately: runtime healthy, invitation pending or prepared, personal account
setup pending or registered by server metadata, client decrypt/read check
pending or verified, and backup/isolated-restore evidence pending or verified.
The native receipt records only invitation preparation; it cannot claim
personal vault registration, client decryption, or usability is complete.
