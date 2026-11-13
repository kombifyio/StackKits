# Decision receipts

A decision receipt is a person's confirmation of one exact operation, signed by
the platform's decision ledger (kombify-Gateway, store `stackkit_operation`).
It binds the operation, its target on one node, the digest of its canonical
arguments, the digest of its reviewed plan where the operation has one, the
revision it was resolved against and the acting principal. It is used once.

The command that performs the operation verifies the receipt offline and
spends it before the first effect. A receipt is never approval at the MCP
server: the server's own approval rules apply to every call as before, and the
receipt is an additional requirement of the calls listed below.

## The first slice

Only these calls take a receipt. Every other operation, action and flag
combination keeps its approval exactly as before and refuses a presented
receipt (`stackkit_receipt_invalid`) instead of ignoring it.

| Ledger operation | Command | MCP tool |
| --- | --- | --- |
| `application_remove` | `stackkit remove --workload <ref> --delete-data` | `stackkit_remove` with `delete_data` |
| `application_remove` | `stackkit app own --action remove --delete-data` (not `--dry-run`) | `stackkit_own_application` with `action: remove`, `delete_data`, no `dry_run` |
| `backup_restore` | `stackkit backup restore activate <restore-result-id> --expected-plan-hash <hash>` | `stackkit_backup_restore_activate` |
| `advanced_change_set_apply` | `stackkit advanced change-set apply` | `stackkit_advanced_change_set_apply` |
| `installation_remove` | `stackkit remove` without `--workload`, with `--delete-data`, `--purge` or both | none: no MCP tool removes the installation |

`stackkit remove` refuses a receipt together with `--force`, `--resolved-plan`,
`--artifact-manifest`, `--generation-receipt` or `--inventory`, because the
receipt binds the workspace's own plan. It also refuses one with `--use-case`
or `--candidate-output`.

## Binding definition

This section is the one definition the producer (Techstack, the ledger's only
filer) and the workspace decision contract copy. A producer files exactly
these facts; the consumer recomputes them from its own resolved call and
compares the intent digest.

The workspace decision contract copies this definition (the target reference
format, the arguments digest domain
`kombify.stackkit-operation-arguments.digest.v1`, the revision and the
principal spellings) into its contract check
(`scripts/check-decision-contract.mjs` and its vectors) in kombify-workspace
#4836, which is in review. Until it merges, this section is the only
definition, and the producer's own vectors (Techstack
`pkg/auth/stackkitdecision`) pin the same bytes.

### Digests and canonical form

- Canonical JSON is the ledger's `canonicalDecisionJson`: object members sorted
  by UTF-16 code unit, strings escaped as `JSON.stringify` escapes them, safe
  integers only, no whitespace.
- `argumentsSha256` is 64 lowercase hex characters:
  SHA-256 of `kombify.stackkit-operation-arguments.digest.v1` + `\n` +
  canonical JSON of the argument object + `\n`. This is the platform's
  domain-digest convention (the ledger's intent and record digests use the
  same shape with their own domain).
- `intentDigest` is the ledger's: `sha256:` + hex SHA-256 of
  `kombify.decision-intent.digest.v1` + `\n` + canonical JSON of
  `{operation, resource, risk: "destructive", parameters: {argumentsSha256,
  planSha256?, revision, actingPrincipalKind, actingPrincipalId}}` + `\n`.
  `planSha256` is a member only for an operation with a plan.
- `revision` is `sha256:` + hex SHA-256 of the bytes of the workspace's
  `stack-spec.yaml` on the node, read when the command starts.
- `planSha256`, where it exists, is the 64-hex part of a `sha256:<hex>` digest
  the command already takes, and the command itself refuses to run a plan
  whose digest differs.

### Target reference

`resource` is the exact target on one node. `<server>` is the `serverId` of the
node's enrolled identity, read from root-owned node custody
(`/var/lib/techstack-guard-executor/ril-local-identity.json`, written by the
privileged installer). It never comes from an argument, so a receipt for one
node is refused on its twin.

| Ledger operation | `resource` |
| --- | --- |
| `application_remove` (workload) | `stackkit:<server>:application:<workload ref>` |
| `application_remove` (own application) | `stackkit:<server>:own-application:<name>` |
| `backup_restore` | `stackkit:<server>:restore:<restore result id>` (`sha256:<hex>`) |
| `advanced_change_set_apply` | `stackkit:<server>:change-set:<change-set id>` (`sha256:<hex>`) |
| `installation_remove` | `stackkit:<server>` |

### Argument objects and plan digests

Every member is always present; an unused string member is `""`.

| Ledger operation | Canonical arguments | `planSha256` |
| --- | --- | --- |
| `application_remove` (workload) | `{"delete_data": true, "workload": "<ref>"}` | none: the removal resolves no reviewed plan digest |
| `application_remove` (own application) | `{"delete_data": true, "keep_secrets": <--keep-secrets>, "name": "<name>", "operation_id": "<--operation-id>"}` | the `--review-digest` of the dry run; required |
| `backup_restore` | `{"marketplace_service": "<--marketplace-service>", "operation_id": "<--operation-id>", "restore_result_id": "<id>", "workload": "<--workload>"}` | `--expected-plan-hash`; required |
| `advanced_change_set_apply` | `{"change_set_id": "<--change-set>"}` | `--expect-sha256`, the digest of the stored change-set bytes; required |
| `installation_remove` | `{"delete_data": <--delete-data>, "purge": <--purge>}` | none: the removal resolves no reviewed plan digest |

The capability and candidate paths of a change-set apply are host local and
enter no digest: the apply's own admission compares the stored, Owner-signed
change set with its digest before any effect.

### Where each fact comes from

| Fact | Source on the consumer |
| --- | --- |
| `operation` | the command and its flags (table above) |
| `resource` | the command's target and the node's enrolled `serverId` |
| `argumentsSha256` | the command's flags, canonicalised as above |
| `planSha256` | the plan digest flag named above |
| `revision` | the workspace `stack-spec.yaml` bytes |
| acting principal | `--decision-principal <person\|agent_instance>:<id>`, spelled as in "Acting principal" below |

### Acting principal

The acting principal is spelled exactly as the platform's filer files it, and
every entry point derives it from the table below and from nothing else. There
is one spelling per kind of caller: one for a person, and one for each of the
two ways an agent is authenticated (through the Gateway agent link on the
node, or by the producer's own route). An entry point never translates one
spelling into another; a receipt is spent only with the spelling it was filed
with.

| Caller | Principal the filer files and the command receives |
| --- | --- |
| a person (the installation's Owner) | `person:<platform subject>`: the Owner's subject as the platform authenticates it (for example `auth0\|owner-0001`), which the hosted enrollment pins as `ownerId` in the node's enrolled identity |
| an agent reaching the node through the Gateway agent link | `agent_instance:gateway:<link ref>:<agent instance>/<account>`, the agent id the Gateway envelope names |
| an agent the producer authenticated (Techstack route) | `agent_instance:<agent instance id>` as the Gateway origin proof names the instance; the producer passes the principal it filed unchanged |

The derivations on the consumer:

- **Producer path** (the privileged executor runs the command): the command
  takes `--decision-principal` exactly as the producer filed it.
- **MCP, envelope agent**: `agent_instance:gateway:<LinkRef>:<AgentInstance>/<Account>`
  from the verified envelope.
- **MCP, node-local agent credential** (a directly attached agent, or the agent
  token the server is started with): refused. A node-local credential has no
  platform identity, so no filer can file a decision for it, and a receipt can
  never be spent by it.
- **MCP, the local Owner**: `person:<ownerId>` read from the node's enrolled
  identity, never the Owner's email.

A person principal is admitted only when it equals the node's enrolled
`ownerId`; a receipt bound to another person is refused with
`stackkit_receipt_target_mismatch`, and a node whose enrollment names no Owner
admits no person principal.

### Refusals

Checked in this order, the contract's (`stackkitReceiptConsumerSemantics`):

| Refusal | When |
| --- | --- |
| `stackkit_receipt_invalid` | the node cannot verify (no purpose-key snapshot it trusts inside its window, no enrolled identity), the signature does not verify against a published decision-receipt key, the JWS type is not `kombify.decision-receipt.v1`, the store is not `stackkit_operation`, `expiresAt` is missing, the answer is not a person's `allow_once` on a surface other than `equipment`, the window is empty or longer than 900 seconds, the clock is before `issuedAt`, the receipt is presented on a call outside the first slice or on a dry run, or the node cannot write its spent record |
| `stackkit_receipt_target_mismatch` | the recomputed intent digest differs: another operation, node, target, arguments, plan, revision or acting principal |
| `stackkit_receipt_expired` | the clock is at or after `expiresAt` |
| `stackkit_receipt_spent` | the receipt id or the decision id is already in the node's spent record |

## The carrier

The command line takes the receipt and the acting principal:

```text
stackkit remove --workload photos --delete-data \
  --decision-receipt <compact JWS> --decision-principal agent_instance:ainst-kubi
```

The receipt is the compact JWS the ledger signed:
`base64url(canonical header {alg, kid, typ}) . base64url(canonical receipt
without signature) . signature.value`. The ledger's read route returns the
receipt object with `signature: {alg, kid, value}`; the dispatcher rebuilds the
compact form from it. `--decision-receipt @file` reads it from a file, which
keeps it out of the process list. The MCP input `decision_receipt` takes the
compact form only.

## Node custody

Everything a consumer trusts is node custody at fixed paths; no flag,
environment variable or workspace selects one.

- **Purpose keys**: `/etc/techstack/ril-purpose-keys.json`, the snapshot the
  privileged refresher writes (Techstack `guard-executor ril-keys-refresh`:
  root only, mode 0600, generation rollback refused). The reader requires the
  file and every parent directory to belong to root and to be writable by
  nobody else, the file to be a single-link regular file of mode 0600, and the
  snapshot to be inside its window (`issuedAt <= now < validUntil`, at most 45
  seconds), the same rules as the RIL proof verifier. On a hosted node the
  `techstack-ril-keys.timer` runs the refresher 15 seconds after boot and then
  20 seconds after each run, so the snapshot is inside its window whenever a
  command starts while the node reaches the platform. When it cannot, the
  snapshot lapses within 45 seconds and every receipt is refused.
- **Node identity**: `/var/lib/techstack-guard-executor/ril-local-identity.json`,
  pinned by the privileged installer after the hosted enrollment, with the
  same ownership rules.
- **Spent record**: `/var/lib/stackkit/decision-receipts`, a root-owned
  directory of exactly mode 0700, checked on every use, not only when it is
  created. Each spent receipt is written by exclusive create
  once under its receipt id and once under its decision id. The record holds
  the ids, the operation and the target, never the receipt. It survives a
  restart of the command, the MCP server and the node; a receipt spent before
  a crash stays spent, and an interrupted operation is resumed through the
  operation's own journal.

A process that cannot read the keys or the identity cannot verify and refuses
the receipt; a process that cannot write the spent record refuses the call. In
practice only a command the privileged executor runs as root, or an MCP server
running as root, can use a receipt.

## A node without the hosted enrollment has no receipt path

The ledger admits one filer: Techstack. A standalone installation, or a node
without the hosted enrollment, has no purpose-key snapshot and no enrolled
identity, so every receipt is refused there, and nothing on that host can ask
the ledger to file a decision.

The confirmation of the operations above stays with a person at the shell,
exactly as before: `stackkit remove --workload <ref> --delete-data
--auto-approve` and `stackkit backup restore activate <restore-result-id>
--owner-approve` behave as they did, with no prompt, no network call and no
account. A command without `--decision-receipt` reads no custody at all.
