---
name: marketplace-operations
description: Operate the optional native Marketplace application through StackKits lifecycle contracts while preserving node-private credentials, one runtime owner and one routing owner.
---

# Marketplace operations

Marketplace is the optional Coolify application on the installed node. StackKits
owns its integration, lifecycle and custody. The selected runtime target and the
StackKits router remain the execution and routing authorities for workloads.
Third-party application features remain upstream-owned.

## Admission and access

Use the current spec, resolved Plan and matching StackKits compiler. Read the
native Apply result before choosing a route or server binding. Keep Marketplace
catalog availability separate from a development receipt: this package remains
draft until its full current-pin admission evidence is accepted.

The owner signs in through the held PocketID origin with the enrolled passkey
and a verified email. Household accounts need explicit rights. The Marketplace
UI is private or behind TinyAuth. Do not substitute an administrator password,
change the account's identity or verification flag, or disable certificate
validation to bypass an incomplete login. Configure the kit's trusted issuer
through its existing lifecycle.

The Coolify API token, bootstrap credential, own-host SSH key and server binding
stay in node-private Marketplace custody. Use the native executor's held binding
for admitted API operations. Do not print, copy into agent client configuration
or export these values. Do not register the same app under a second Compose or
Komodo manager or enable an additional public proxy.

## Lifecycle

Read runtime state with `stackkit status --spec <spec> --resolved-plan <plan>
--json` and verify it with `stackkit verify` using the same arguments. These are
root commands. Use `stackkit service start marketplace`, `stop marketplace` or
`restart marketplace` with `--owner-approve --json`; read bounded logs with
`stackkit service logs marketplace --tail 3 --json`.

Use the supported `stackkit backup` commands for a Kopia snapshot, staged restore
and explicit activation. Follow their operation, Plan-hash, recipient-key and
Owner-approval requirements. The current Marketplace receipt demonstrates a
quiesced, crash-consistent filesystem snapshot of the database and volumes.
Do not label it an application-consistent dump or off-host backup. Restoration
preserves the kit's custody, Owner identity and unselected workloads.

Canonical removal uses the sealed installed workload and explicit local site,
node and execution-channel arguments:

```sh
stackkit remove --spec <spec> --resolved-plan <plan> --workload marketplace \
  --local-site <site> --local-node <node> --local-execution-channel <channel> \
  --auto-approve --json
```

The default removes only the admitted Marketplace containers and own-host SSH
exception while retaining data, API/server custody and the kit's original SSH
policy. Explicit `--delete-data` admits deletion of the originally held
Marketplace volume identities. A volume in use leaves a signed recoverable
operation; resolve the holder and resume through the same canonical contract.
Never force-delete a foreign holder or adopt a replacement volume. After a
completed intentional purge, a new explicit Apply creates new API/server
bindings; deleted data is not recovered by reapplying.

## Agent connection and evidence

`stackkit gen` selects this surface into `.stackkit/agent-surface.json`. Obtain
the lifecycle MCP connection with `stackkit agent mcp-config`; use the existing
StackKits token and write-admission policy. The surface exports operation and
custody references only, with `containsSecretValues: false`; it provisions no
additional product MCP or runtime controller.

Bind each observed operation to its source, compiler, artifact, resolved Plan
and environment. Keep implementation, merge, release and runtime acceptance
separate. Upgrade, Cloud, Depot, verified owner/household login and curated app
acceptance still require their own receipts; this surface does not admit them.
