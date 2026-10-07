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

## Node-owned agent connector credential

For the admitted native Standard Mode Home Assistant workload, the existing
private owner setup JSON can also contain an explicit `connector` object with
`action` (`ensure` or `revoke`), `lifespanDays` (1–365), and `binding`:
`resourceId`, `containerId` (64 lowercase hexadecimal characters), `imageDigest`,
`serviceRevision` (positive), and `node` (`siteRef`, `nodeRef`,
`executionChannelRef`). Keep the existing username/password/display-name/language
fields and private file permissions. No URL, token or workspace override is
accepted. Run the existing owner-approved `stackkit setup smart-home` action;
an owner-approved Apply also processes this explicit selection even when the
owner setup has already succeeded. Omitting `connector` preserves ordinary
setup behavior. Reading status never enrolls a credential.

The CLI narrows the current signed Apply to the local workload bundle, verifies
the requested image and node against that source and Owner custody, and reads
the exact container through the existing persisted-Compose/Docker custody
observer before issuance and after result readback. `setup --json` and signed
setup lifecycle projection retain `homeAssistantConnector.issuer` and
`verifiedLocalDeployment` (node, instance, container, image). Only the latter
are independently verified local deployment facts. `issuer.requestedBinding`
keeps `resourceId` and `serviceRevision` as caller-selected external correlation;
it does not prove a Techstack inventory revision. A changed or missing container
withholds a verified result and never authorizes silent replacement enrollment.
External/imported Home Assistant instances have no native workload bundle and
cannot enter this bootstrap path; their preservation-first adoption is unchanged.

The local HTTP/WebSocket source fixture uses generated pinned workload source,
synthetic daemon observations and real signed node/lifecycle custody. It proves
binding denial, enrollment replay/revoke and secret-free receipt projection;
it does not qualify a deployed Docker installation or native MCP tools.
StackKits remains pinned at 2026.7.2, while the separately reviewed native MCP
fixture uses 2026.9.4. Gateway connection eligibility, equipment assignment,
Techstack handoff and actual agent use remain unavailable until those joins
and the current-pin customer journey are qualified.

### Metadata carrier and signed receipt reconciliation

An orchestrator can select the same local setup with a separate private,
workspace-relative metadata document (at most 64 KiB), without editing the
owner credential file:

```json
{
  "schemaVersion": "stackkit.home-assistant-connector-request/v1",
  "connector": {
    "action": "ensure",
    "lifespanDays": 30,
    "binding": {
      "resourceId": "owned-service-reference",
      "containerId": "<current full 64-character container identity>",
      "imageDigest": "sha256:<current immutable image digest>",
      "serviceRevision": 7,
      "node": {
        "siteRef": "<owned site>",
        "nodeRef": "<owned node>",
        "executionChannelRef": "<local owner channel>"
      }
    }
  }
}
```

Use `stackkit setup connector --workload smart-home --request-file <relative-file>
--operation-id <stable-id> --owner-approve --json`. The request contains no
workspace, URL, token, credential file override or account fields. The existing
private owner credential document must omit its own `connector` selection when
using this separate carrier. The node reads its existing owner credentials;
this path never rewrites them or bootstraps an imported application.

The admitted native bundle must declare its secure Pocket ID callback route.
The source fixture uses current v2alpha2 authoring with catalog defaults and
the declared HTTPS route. A compatibility bundle without that route remains
unavailable and reports its admission error; this carrier does not infer or
weaken the route contract.

The command-result data uses `stackkit.home-assistant-connector-result/v1`:
`signatureVerified`, `receiptBase64`, `receiptDigest` and
`currentLocalDeployment`. Base64 retains the exact canonical bytes of the
already signed immutable setup receipt, including the outer lifecycle
signature covering the local deployment facts. Consumers must decode and
verify those bytes and match the authenticated pinned-CLI result to their
current service authority; an inner issuer signature alone does not prove the
local deployment. External resource/revision correlation is still not a
Techstack inventory attestation.

If the command acknowledgement is lost, use the same request and operation
with `--verify-only` instead of `--owner-approve`. Verification does not issue,
revoke, initialize or reconfigure the application. It checks current signed
Apply, node/source/container identity, the signed receipt and encrypted local
credential state; active credentials also undergo the existing native owner
and version read. A changed request conflicts with the retained operation
intent. A completed operation cannot be submitted as another mutation. A
missing/nonterminal receipt, changed deployment, expired active credential or
older active receipt after revocation fails closed. Do not retry uncertain
issuance under a new operation identity. A new explicit revoke request uses
`action: revoke` and a new stable operation ID, retaining its own signed
receipt. Revoked verification reports retained node revocation; it does not
claim independent MCP reachability or current tool qualification.

The release catalog exposes `application.connector` and
`application.connector.verify` with fixed argv and the request/result schemas.
These local lifecycle operations can be dispatched within an existing
Techstack Advanced deployment once the normally published pinned release and
its capability have been admitted; they do not introduce a standalone Advanced
executor. Current source qualification does not activate a Gateway grant or
agent equipment.

The local `appsetup.BootstrapHomeAssistantOwner` API accepts an optional
`HomeAssistantOwnerRequest.Connector`. Ordinary owner/OIDC setup does not issue
an agent credential. An authorized local caller explicitly selects `ensure`
or `revoke`, a lifetime of 1–365 days, and the requested resource/container/image/
service-revision tuple and established local node binding. The API admits only
the existing StackKits HA pin, currently 2026.7.2; this is credential setup
compatibility, **not qualification of that release's native MCP tools**.

The issuer uses the pinned upstream authenticated WebSocket commands
`auth/long_lived_access_token`, `auth/refresh_tokens`, and
`auth/delete_refresh_token`. A long-lived token carries the authenticated
user's authority; it is not a scoped read-only key. The existing native local
relay and current Gateway read/write lease remain the use-policy boundary.
The upstream implementation is
[HA 2026.7.2 auth](https://github.com/home-assistant/core/blob/2026.7.2/homeassistant/components/auth/__init__.py).

Credential lifecycle uses the existing `secret://` local issued-secret store.
The additive encrypted record kind uses the established Owner key, a distinct
owner/node/reference wrapping context, and existing age encryption. Legacy
issued records remain readable; a consumer requiring encrypted custody rejects
a legacy record. There is no Wallet replica, new master key, or backup-key reuse.
Local owner files and recovery remain the existing node custody authority.

A cross-process node lock serializes enrollment and use. An encrypted, synced
intent precedes token creation. Restart verifies the retained token and reuses
it. An uncertain issuance never creates another token: the next invocation
reconciles its unique random client name and creation time against the current
owner's token list, deletes only that exact credential, and retains a revoked
tombstone. Ambiguous identities fail closed. Explicit revoke verifies removal;
restart cannot silently re-enroll. Temporary owner sessions are revoked on
success and failure, including uncertain issuance. A filesystem that cannot
confirm durable encrypted intent installation cannot dispatch issuance.

`HomeAssistantConnectorEvidence` contains no token or endpoint. Its Owner
signature covers JSON with the `Signature` field set to its zero value. It
reports the HA owner/version observed and the **requested** container binding;
this setup API does not observe Docker and must not promote that tuple to a
verified installation. The downstream deployment owner must freshly verify
the container identity/revision before projecting a Gateway connection.
`WithHomeAssistantConnectorCredential` supplies an active credential to a
local callback only, under the same lock and after current authenticated HA
owner/version readback. The caller still owes current relay admission.

Production deployment-to-node handoff, automatic Gateway connection creation,
agent equipment and local relay consumption remain separate integration work.
No plaintext credential may enter Techstack, Gateway, UI, logs, or receipts.
