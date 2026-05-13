# Module: admin-bootstrap

Bootstraps the initial administrator user in LLDAP and PocketID. **Foundation layer, mandatory in V6.**

## What it does

On first `stackkit apply`:

1. Waits for LLDAP to be healthy.
2. Creates a user in LLDAP with username `admin` (configurable), groups `admins` + `users`, a random password (32 bytes of entropy, base64url-encoded).
3. Waits for PocketID to be healthy.
4. Creates the matching OIDC user record in PocketID with the same email and groups.
5. Prints the generated password to CLI stdout **once**. It is not written to any file or secret service.

## The security model

The initial password is deliberately ephemeral:

- **Not stored on disk.** No `.env` file, no Docker secret, no Doppler.
- **Printed once** to stdout at the end of `stackkit apply`.
- **Must be rotated on first login.** The user opens `https://auth.<domain>`, logs in with the temp password, and PocketID forces a rotation.

If the password is lost before rotation:

```bash
stackkit admin reset-password
# Re-runs the bootstrap flow, prints a fresh password.
```

This is safer than writing the password to a file that may get committed, backed up unencrypted, or leaked via logs.

## Settings

| Setting | Type | Default | Kind |
|---------|------|---------|------|
| `adminUsername` | string (regex) | `admin` | perma |
| `adminGroups` | []string | `["admins", "users"]` | perma |
| `adminEmail` | email | `admin@example.com` | flexible |
| `adminDisplayName` | string | `Administrator` | flexible |
| `passwordEntropyBytes` | int | `32` | flexible |

## Dependencies

Requires LLDAP (`>= 0.5.0`) and PocketID to be deployed in the same StackKit.

## Status

**Scaffolded** for V6 Phase 2 (Q3/2026). Provisioner containers run a placeholder echo today; the real LLDAP + PocketID bootstrap logic lands in Phase 2.

## Non-Goals

- Bulk user creation (handled by LLDAP admin UI or a separate `user-provisioner` module, out of scope here).
- SCIM/SAML user provisioning (post-V6).
