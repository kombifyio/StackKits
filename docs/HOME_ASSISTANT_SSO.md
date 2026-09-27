# Smart Home sign-in

Implementation status: source and deterministic authentication boundaries are
implemented. Real Home Assistant startup, browser/native-client sign-in, private
CA trust and restore qualification remain pending.

Smart Home keeps Home Assistant 2026.7.2 and packages `auth_oidc` 1.2.1 with the
reviewed StackKits owner-binding patch. The extension, private dependencies and
patch are pinned in CUE and embedded in the CLI; apply performs no extension
network fetch or Python package installation. See the
[artifact provenance and licenses](../internal/hassoidc/README.md).

Apply creates `stackkit-smart-home` as a public PKCE client restricted to the
owners, admins and household Pocket ID groups, with the exact HTTPS callback
`/auth/oidc/callback` on the Smart Home origin. It issues no client secret. The
component trusts the governed public/private CA bundle and retains TLS and issuer
validation. TinyAuth's existing route admission remains in force.

Apply, change-set apply and Advanced reconcile automatically run the admitted
Go owner setup for selected Smart Home workloads. A fresh owner uses the signed
Pocket ID owner identity and a generated recovery password held only in
`.stackkit/setup/home-assistant-owner.json` (owner-only permissions). Existing
custody is reused on retry. Setup verifies a new Plan's identity configuration
even if an older Plan previously completed setup. It never regenerates missing
credentials for an owner with completed setup history.

Repair an interrupted setup with the same custodied credentials:

```sh
stackkit setup smart-home --owner-approve --complete-onboarding
```

For an existing installation without custodied credentials, privately restore
the actual local owner credentials to that file before retrying; setup cannot
recover or reset an unknown password or bypass MFA. The private JSON supplies `username`, `password`, `displayName`, and optionally
`language`. Go authenticates the existing HA owner or completes the existing
onboarding path, reads back owner and administrator status, and then binds the
signed Pocket ID issuer/subject to that same owner through the packaged endpoint.
Only that authenticated HA owner may create this exact binding; conflicts fail
without moving credentials or changing an account's owner flag. Repeating setup
preserves the existing binding, password, user ID and configuration. Temporary
setup sessions are revoked by the existing cleanup path. Automatic setup finishes
HA's remaining onboarding markers using its current core settings and analytics
preferences. It does not opt in to telemetry, change location or add owner-chosen
devices; those remain editable in HA. Its pinned core-config endpoint may start
HA's normal default integration flows. Manual setup without
`--complete-onboarding` retains its owner-verification-only behavior.

OIDC cannot create accounts before the verified owner binding exists. The
extension uses the immutable issuer/subject identity, with automatic username
linking disabled. The owner already belongs to Pocket ID's `admins` group;
`admins` receive HA's administrator group, and `household` receive ordinary user
access. Existing non-owner OIDC users' groups converge on each verified login,
so an administrator demoted to household loses HA administrator privileges.
Neither setup nor sign-in changes HA's `is_owner` flag or local credentials.

Existing `configuration.yaml` files are preserved. If a pre-existing or custom
file does not enable `auth_oidc: !include stackkit-oidc.yaml`, setup reports the
missing component and stops. Retain the existing configuration, enable that
include and restart HA before retrying; setup does not overwrite unrelated YAML.

Local Home Assistant login remains available. The welcome screen does not force
an OIDC redirect. Existing household local accounts are preserved; no username
heuristic silently links them. The upstream authenticated account-link flow can
attach an OIDC identity to a previously existing non-owner account. A changed
issuer changes upstream's subject hash and needs an explicit migration; setup
refuses an ambiguous pre-existing owner OIDC binding.

An IdP membership change alone does not immediately revoke existing HA sessions.
Role convergence happens on the next verified OIDC login; use HA session revocation
when immediate removal is required. Browser SSO does not prove mobile, device or
native-client compatibility through TinyAuth.

Before claiming runtime support, qualify fresh and existing-owner setup, interrupted
setup/retry, exact callback and PKCE exchange, private-CA trust, owner identity
retention, ordinary household access, demotion, unauthorized/conflicting binding
denials, app-local recovery login, and backup/restore of the persistent HA config.
Capture user IDs, policy outcomes and digests without logging credentials/tokens.
