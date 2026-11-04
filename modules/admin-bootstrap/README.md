# Module: admin-bootstrap

Bootstrap contract for the initial administrator of a StackKit. **Foundation layer; draft, not the Owner path.**

## Current role

The Owner of a homelab is bootstrapped by the StackKits core identity path, not by this module:

1. `stackkit init --owner-source=local` creates the stable `ownerRef`, the evidence key and the local step-ca trust material.
2. The first `apply` realizes the desired Owner in PocketID and ensures the `owners` group.
3. The Owner activates PocketID through its passkey-first one-time setup flow, which creates the Homelab account. StackKits never creates a PocketID password.

There is no LLDAP user, no temporary password and no password rotation step in this path. A homelab has exactly one PocketID, the Authority, and every person who signs in has a Homelab account there; see the internal StackKits identity architecture and the workspace `HOMELAB-IDENTITY-ACCESS-TARGET.md`. Account recovery is the recovery set and recovery code of that target, not a regenerated password.

## What this module is

`module.cue` declares the contract `admin-user-bootstrap` and `oidc-claim-mapping` for a technical bootstrap administrator (username `admin`, groups `admins` and `users`). Its provisioner containers are placeholders, so it provisions nothing today. It is registered as a draft module (`maturity: "draft"`); a technical bootstrap administrator is a service-local identity and never the Owner.

## Settings

| Setting | Type | Default | Kind |
|---------|------|---------|------|
| `adminUsername` | string (regex) | `admin` | perma |
| `adminGroups` | []string | `["admins", "users"]` | perma |
| `adminEmail` | email | `admin@example.com` | flexible |
| `adminDisplayName` | string | `Administrator` | flexible |
| `passwordEntropyBytes` | int | `32` | flexible |

## Non-Goals

- Owner creation, passkeys and recovery (core identity path above).
- Bulk user creation and group membership (`stackkit user add`, the PocketID `household` group).
- SCIM/SAML user provisioning.
