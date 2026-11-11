# Run your own container

An own application is one HTTP container on an existing Direct Compose Home
installation. It uses that installation's router, TLS, Homelab sign-in, encrypted
secret custody and backup. **V1 has no outbound Internet access: this application cannot make outbound connections.** Many images require outbound APIs and will not work in this version. It does not provision a server or install a platform.

The desired document belongs to Techstack. The versioned contract is
[`techstack.own-application/v1`](../schemas/techstack-own-application-v1.schema.json).
StackKits admits it into the same application descriptor, Compose renderer and
native executor as catalog applications. Do not edit generated Compose files.

```json
{
  "apiVersion": "techstack.own-application/v1",
  "name": "hello",
  "image": "docker.io/library/nginx:1.29-alpine",
  "httpPort": 80,
  "hostname": "own-hello.lab.home",
  "environment": {"WELCOME": "hello"},
  "secretRefs": {"API_KEY": "secret://own-hello/API_KEY"},
  "volume": {"mountPath": "/usr/share/nginx/html"}
}
```

Use the installation's actual domain instead of `lab.home`. Names are lowercase
DNS labels starting with a letter, at most 40 characters; `kombify*` and
`stackkit*` are reserved. The workload and hostname always use `own-<name>` to
keep them separate from catalog and core services. A live call rejects a
collision with an installation route or catalog workload.

Images require a registry addressed by a public DNS name over HTTPS, a repository, and a tag or SHA-256 digest. Only the default HTTPS port or explicit `:443` is accepted; IP literals, localhost, single-label names, private/reserved DNS suffixes and non-public DNS answers are refused. Redirects and token endpoints follow the same HTTPS/public-address policy; there is no HTTP fallback.
`docker.io/library/nginx:1.29-alpine` and
`ghcr.io/example/app@sha256:<64 lowercase hex digits>` are supported.
Implicit tags, image URLs and builds are refused. Images carrying inherited
`traefik.*` labels are refused because they could create an unprotected router.
Any image-declared volume must match the one admitted mount path. During create/update dry review, a registry manifest request resolves a tag without pulling
image layers. The preview shows `record.bundle.resolvedImage`, and
`reviewDigest` binds that exact digest. Pass it as `--resolved-image` on the
executing call. Execution fetches the approved manifest, config and layers through the same public-address HTTPS transport, verifies their hashes, and imports a private 0600 archive into the node-local daemon. Compose runs the derived immutable config image ID with `pull_policy: never`; the daemon never pulls a user-selected registry reference. A moved tag cannot change the approved content. A rollback uses the
previous successful reference **and its recorded digest**, even if a tag moved.

Sign-in is on by default. The Access Gate uses the existing Hub's device-bound
Homelab browser session on LAN and remote routes. Owners, admins and household
members can open the application. Set `"signIn": false` to serve anonymous
content through the installation's existing reach; this does not establish a
new tunnel or public listener. TLS remains required. The container's HTTP port
is never published on the host, including loopback.

Plain `environment` values are single-line strings. Each secret environment key
`K` must reference exactly `secret://own-<name>/K`; other namespaces, another
application's secrets and arbitrary node custody are refused. Create the secrets
through this operation with `--secrets-file`, a private regular JSON file (0600
or stricter) mapping the declared keys to single-line values. The same file must
be supplied during dry review and execution; the review binds its content
without returning values. Create requires every declared secret; update can
rotate declared secrets without changing the image. No secret values belong in
the desired document or MCP arguments. The operation stores values in existing
encrypted issued-secret custody, and MCP confines the file to its workspace.

Runtime delivery uses a YAML-serialized Compose environment mapping in the
owner-only 0600 recipe, with Compose dollar escaping. It uses no dotenv quoting:
quotes, dollar signs, whitespace, Unicode and trailing backslashes keep their
exact values. NUL, CR and LF remain refused. Removal deletes the application's
secrets and scrubs its runtime recipe. Only `remove --keep-secrets` with a retained
named volume keeps them; `--delete-data --keep-secrets` is refused. Rollback rolls
back the image only, not rotated secrets. Logs are bounded to 100 lines and redact
known custody values; transformed values can escape redaction, so readers must
be trusted.

## Author and review

A Techstack installation with an authenticated StackKits release pin can admit
and store a document in `.techstack/own-applications/hello.json`:

```sh
techstack own-app --workspace /path/to/installation --document hello.json
```

`TECHSTACK_STACKKIT_RELEASE_PIN` and `TECHSTACK_STACKKIT_RELEASE_CACHE` select
its existing verified release installation. A pin without the new contract
refuses authoring; it never falls back to a source checkout. `--dry-run` previews
without replacing the desired document. Authoring uses offline `render`, so its image must be digest-pinned; first resolve a tag through create/update dry review and put that returned digest reference in the desired document. This command authors intent only;
managed rollout remains under Techstack's Advanced dispatch authority.

On the node, review the exact action against the desired document:

```sh
stackkit app own --action create --name hello \
  --document .techstack/own-applications/hello.json --secrets-file hello-secrets.json --dry-run --json
```

The versioned result contains `compose`, `route`, `backupVolumes`, the intended
`record` and `reviewDigest`. It writes no custody and contacts no Docker daemon.
It reads an explicitly supplied private secret input and the existing private
installation authority to compute a domain-separated HMAC commitment; it never
returns the material or an unkeyed password verifier. Offline `render` requires a digest-pinned image, accepts secret references only, needs no private authority and makes no registry request. Create/update dry reviews resolve tagged images through the read-only HTTPS registry request. For offline inspection only, `--installation-file`
accepts JSON containing `domain`, `siteRef`, `nodeRef`, `coreModuleRef`,
`tlsIssuerRef`, `tlsProfileRef` and `exposure`; this flag requires `--dry-run`.
A real mutation always derives this authority from the verified applied node.

Execute the reviewed action with its exact digest and a new stable operation ID:

```sh
stackkit app own --action create --name hello \
  --document .techstack/own-applications/hello.json \
  --secrets-file hello-secrets.json --resolved-image docker.io/library/nginx@sha256:<image-digest> \
  --owner-approve --operation-id hello-create-1 --review-digest sha256:<review-digest> --json
```

Every mutation uses the existing idle-lifecycle lock and signed node custody.
Changing the document, action, installation or current revision invalidates the
review. Replaying a completed operation ID with the same request returns its
receipt. An uncertain operation stays pending; inspect status and make a new
reviewed reconciliation request. Never assume a failed call had no effects.

## Lifecycle and data

Use `--action update` with the next document to change `image` or supply
`--secrets-file` to rotate existing declared secrets. Other v1
fields stay immutable; remove and recreate to change them. A reviewed update
can also adopt changed same-node installation routing, including its new
hostname, without changing the image; reads and teardown keep using held
custody. Updating a stopped application to its recorded image starts it again. Use `rollback` to
return to the previous successful pinned image, `stop` to stop the container,
`remove` to remove it, and `status` or `logs` to inspect it. All mutations follow
the same dry-run, approval, digest and operation-ID sequence. These actions
select the stored application by `--name`; only create/update take a document.
`render` previews a document without creating an application.

Status reports signed intent separately from current Compose observation.
A missing or failed observation is unknown, not healthy. Startup checks container readiness (including any image healthcheck) and
requires an HTTP 2xx/3xx response on `/` at the admitted private container port
before attaching the router and admitting the route. Redirects are not followed.
Pending signed state keeps the route closed on updates too; readiness is not
evidence that a full application journey passed. Rollback restores an image, not application data or a database
schema; applications with irreversible data migrations need a backup restore.

The optional volume is a Docker local named volume mounted at the one declared
container path. Host paths, system paths and the Docker socket are refused.
Default limits are 512 MiB memory and 1 CPU; `resources.memoryMiB` permits
64–8192 and `resources.cpus` permits 0.1–4 (zero selects the default).
The fixed policy drops all capabilities, adds only `CHOWN`, `SETUID`, `SETGID`
and `NET_BIND_SERVICE`, enables no-new-privileges, limits processes to 256 and
caps swap at the memory limit. This permits common entrypoints to set file
ownership, drop to their own user and bind the declared port. No devices or
hardening override are admitted. The image's USER is respected; writable root
filesystems are allowed for mainstream images, with no v1 knob for this policy.

Each application joins one internal bridge. Only the existing LAN/remote ingress routers join it in
addition; the application never joins the shared core/catalog network, another
own application's network or an egress bridge. It cannot reach core services, catalog applications or other own applications **directly**. It can reach the router on its web ports like an anonymous client on the home network: applications published there without sign-in protection are reachable through the router. Protected routes still require their normal sign-in/device proof. V1 provides no outbound Internet access. Router administrative diagnostics are handled by the separate core-hardening change; network isolation alone does not hide a router endpoint. Stop detaches both routers; removal also deletes the empty application network. Core recreation restores attachments only for running applications. Stop/remove verify the private on-disk recipe against its authorized rendering (or the exact scrubbed recipe from an interrupted remove) before touching containers.
Named volumes and writable image layers have **no disk quota**: an application
can fill the node's disk. Logs have their existing rotation bounds. Use trusted
images and monitor free disk space; volume backup is not a disk bound.
At most 32 application records are admitted per installation.

The existing backup source includes the named volume and exact runtime graph
as `user-content`. Volume creation requires that class in the installation's
existing backup coverage; it never widens a coverage grant automatically.
Removal retains it in backup by default. `remove --delete-data` explicitly
removes only the admitted local named volume. This option must be part of the
review. A source change invokes the existing backup rebind path; an existing
schedule may need renewed backup authorization under the installation's normal
backup policy. A pending application operation blocks backup until reconciled,
because its runtime/data custody is uncertain. Removing an application does not
remove its receipt. V1 does not garbage-collect retained application records.

## Agents

The local `stackkit_own_application` MCP tool exposes this same operation with
`action`, `name`, `document_file`, `installation_file`, `dry_run`,
`resolved_image`, `secrets_file`, `keep_secrets`, `review_digest`,
`operation_id` and `delete_data`. The connection must admit
writes, and every call requires `operation_confirmation: "stackkit.app.own"`
and `owner_approved: true`, including previews and reads. Existing per-call
agent approval and execution receipts still apply. Local `--owner-approve` is
the existing self-asserted CLI convention, not independent proof of a person
approving; the common receipt-bound approval gap is recorded on platform-8f0n2.20 (following the notes on platform-8f0n2.1). File arguments stay inside
the MCP workspace. Read the local `stackkit_homelab_agent` prompt/profile before
operating an own application; it is also available through
`stackkit agent prompt homelab-agent`.

V1 supports no Git builds, arbitrary Compose, custom commands, extra containers,
host networks, host mounts, privileged containers, capabilities, devices, arbitrary
ports, Layer-2 platforms or marketplace publication. A fresh-guest install,
browser sign-in and backup/restore journey are later acceptance; rendering is
not evidence that an application is live.
