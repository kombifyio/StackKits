# Run your own container

An own application is one HTTP container on an existing Direct Compose Home
installation. It uses that installation's router, TLS, Homelab sign-in, encrypted
secret custody and backup. **V1 has no outbound Internet access: this application cannot make outbound connections.** Many images require outbound APIs and will not work in this version. It does not provision a server or install a platform.

The same lifecycle runs a Compose **project** of several containers through
`techstack.own-application/v3`; see [Compose projects (v3)](#compose-projects-v3).
An existing single container that StackKits did not install can be rebuilt into
this lifecycle as `techstack.own-application/v4` ("Stack it"); see
[Stack an existing container (v4)](#stack-an-existing-container-v4).

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

## Optional outbound Internet (v2)

[`techstack.own-application/v2`](../schemas/techstack-own-application-v2.schema.json)
adds exactly one field, `"outboundInternet": true`. Omitted or `false` keeps the
v1 internal bridge and makes no host firewall changes. V1 refuses this field,
including `false` and `null`; an older StackKits reader refuses the v2 version.
There is no screen or separate CLI flag. Use the v2 desired document with the
same `app own --document` and review/execute sequence. Techstack authoring remains
subject to its verified released CLI pin; this package adds no UI control.

Enabling it permits **ordinary public IPv4 destinations only**. The application
still joins its own single bridge, has no published port, and uses the unchanged
TLS, route, sign-in and approval admission. It cannot initiate connections to
its ingress routers either. Only conntrack replies to inbound requests may
return to those routers or host readiness probes.

The node requires Linux nftables with inet/bridge hooks, bridge conntrack,
`ip -j -4 route get`, a routed IPv4 uplink with a gateway and an assigned IPv4
source, host `CAP_NET_ADMIN`, and systemd 244 or newer (the boot-time restore
below uses `Restart=on-failure` on a oneshot unit; an older systemd refuses the
start with `systemd_too_old`). Public and NAT uplinks follow the same
admission: the default gateway, every host-route next hop (even outside a
connected prefix), and the household public egress addresses are dropped.
Carrier-grade NAT intermediate addresses are covered by the special-range deny;
1:1 NAT is admitted by this same rule. Azure's public-looking WireServer address
is explicitly denied too. Missing gateways, unclassifiable routes, IPv6-only,
rootless/remote Docker and unknown public egress addresses refuse the grant.

The installation must already have a Me registration. The kit asks that existing
origin for `GET /_kombify/api/v1/subdomains/reach/:base/egress-addresses`, using
the existing reach authentication. The server must return
`{"addresses":["<observed caller IPv4>"]}` from its trusted edge connection
address. **This route is a server-side prerequisite, not implemented by this
StackKits client change.** A missing registration, route or valid answer refuses
the grant with an unknown-egress reason. No third-party host, proxy or redirect
is used; discovery binds the observed uplink source over IPv4. Disconnected
installations retain v1/v2-off operation and cannot enable the optional grant.

The named nft set `household_egress_v4` holds runtime addresses. The kit watcher
refreshes it every 30 seconds (bounded discovery traffic with a short DHCP-change
window), retaining earlier observed addresses for the container's lifetime.
Failed refresh retains the last set; an answer 90 seconds old stops the container
(three refresh periods tolerate a brief outage without an indefinite unknown
grant). More than 64 observed addresses requires a reviewed restart.

StackKits installs two per-application tables named
`stackkit_own_<16 hex identity>` in the `inet` and `bridge` families, with the
exact network identity in their ownership comments. It never flushes another
table, the general Home/Cloud host firewall, or Docker's rules. Unsupported
backends, unknown ownership, failed application or incomplete/changed kernel
readback refuse startup with the reason. The container and network are first created stopped; the bridge must exist
in the firewall host namespace (rootless/remote Docker is refused). Every rule,
chain and set definition is compared with the kernel's apply acknowledgement
before startup and again after startup and HTTP readiness. The kit-owned
`stackkit-own-watch-<16 hex identity>.service` is the only starter and holds the
container's lifetime. It verifies on `nft monitor` events and every three seconds;
network membership must be exactly the application's network with its fixed MAC.
A mismatch, missing table/set, extra network, monitor failure or stale echo exits
the watcher with the reason in its journal. Systemd's `ExecStopPost` kills the
exact container on watcher stop or crash, including SIGKILL. The start path checks
that the watcher is active. Discovery runs asynchronously so an unresponsive echo
does not delay firewall-change checks. No table is silently reinstalled after a
flush in the same boot; the next reviewed start must apply and verify it (a new
boot is restored as described under restart below). A failed start removes the admitted application container;
its operation stays pending and data remains intact.

The [named range data](../internal/ownappfirewall/special-use.json) records the
IANA IPv4/IPv6 special-purpose registries retrieved on 2026-11-12, supplemented
by multicast and deprecated compatible/site-local space. All registry entries
are denied, including globally reachable special-purpose exceptions. The
policy blocks RFC 1918, loopback, link-local/metadata, CGNAT, multicast,
broadcast, documentation, benchmarking, reserved and translation ranges. Host
addresses are rejected using the kernel's local-address lookup **before DNAT**,
so host public addresses, bridge gateways and published stack ports cannot
bypass the boundary. Forwarding repeats destination filtering **after DNAT**.
Host-connected IPv4 prefixes and every non-default IPv4 route, including
globally addressed LANs and routed home networks, are excluded.
Forwarding additionally requires the observed default uplink and next-hop
gateway; directly connected destinations and alternate routes do not qualify.
Bridge hooks prevent new connections to same-bridge peers independently of
`br_netfilter`. Do not add interfaces, routes, peers or rules to the managed
network outside this lifecycle; a changed host uplink needs a reviewed update.

DNS is explicitly `1.1.1.1` and `9.9.9.9`; Docker's embedded resolver forwards
to these public resolvers. The packet policy applies to the resolved address,
including subsequent answers, so DNS rebinding and a public name pointing at
an internal address cannot authorize it. Applications can use other public
resolvers; their answers never widen destination permission.

**There is no application IPv6 path in this version**, even on an IPv6 host.
The bridge explicitly disables IPv6, container sysctls disable it (including
link-local), bridge prerouting drops all IPv6 frames on this bridge, and inet hooks also drop IPv6. The fixed capability policy excludes
`NET_ADMIN` and `NET_RAW`, so the image cannot re-enable interfaces, forge a
source MAC, or inject raw IPv6/ARP frames. IPv4-mapped, compatible, translated,
6to4 and other IPv6 forms cannot become an IPv4 permission. Native IPv6 Internet
permission would require another enforcement package.

The review binds the desired flag, firewall definition digest (rule compiler,
registry and dynamic host-exclusion contract), and concrete static ruleset digest
observed on the host. Apply refuses changed static host facts until reviewed again.
The signed operation records the same applied ruleset digest, exact table names,
and named dynamic sets; set contents are runtime state, not static review input. A flag change needs
a new reviewed update. Network-mode changes tear down the exact old container
and bridge, retain its named volume and secrets, and create the new bridge only
after rule verification. Signed write-ahead transition custody retains the old
recipe if replacement is interrupted. A new reviewed reconciliation can rebuild
that signed scrubbed recipe before exact teardown if the prior prepare crashed.
Secret rotation occurs after old-recipe teardown. Removal deletes only this application's
two tables after container/network absence is verified, and removes its exact
watcher and runtime snapshot. Stop stops the watcher and keeps rules.

Outbound containers use `restart: "no"`: the per-application tables live only
in the kernel, so Docker restarting the container at boot would run it before
its rules exist, behind Docker's ordinary NAT. Docker is therefore never the
starter. Instead every reviewed start also writes the applied snapshot
(reviewed static ruleset, kernel acknowledgement, container ID and workspace) to
root-only `/var/lib/stackkit-own-egress/` (owned by root, mode 0700) and enables the
persistent `stackkit-own-restore-<16 hex identity>.service`
(`WantedBy=multi-user.target`, after `network-online.target` and Docker, with
`NoNewPrivileges`, read-only home directories and a private `/tmp`). The unit runs
the kit executable that started the application; that file and every parent
directory must be root-owned, no symlink and writable by neither group nor
others, so no other user can replace what root runs at boot. The path is the
one the running kit was started from (Linux reports it from `/proc/self/exe`
and the kit drops the ` (deleted)` suffix the kernel adds once that file was
replaced), so after an in-place upgrade that renames a new kit over the same
path the unit runs the new kit at that path; a path whose file is gone is
refused. The unit needs systemd 244 or newer. A start whose restore unit cannot
be admitted, installed and enabled does not happen.

**After a host reboot the application comes back by itself, fail closed.** Only
a start that completed with an active, verified watcher marks the snapshot with
the kernel boot ID. The restore unit does nothing in a boot it is marked for, so
it never reinstalls a table that was flushed or changed after a completed start.
Otherwise (a new boot, or a boot whose restore attempt has not completed) it:

1. reads the signed operation record and requires a running application whose
   last reviewed operation succeeded and applied its rules;
2. observes the host again and requires that record's ruleset digest, the
   current rule definition digest and the snapshot to agree (changed prefixes,
   gateway, next hops or uplink source keep the application stopped until a
   reviewed update);
3. observes a fresh egress echo, applies the same two tables, compares them with
   the kernel's acknowledgement and requires the resulting receipt to equal the
   signed one exactly;
4. requires the managed bridge, and only then hands the start to the watcher
   above.

A failed attempt leaves the container stopped (a watcher it started is stopped
again) and systemd starts the next attempt 30 seconds after it ends, without
limit, so a boot that is briefly offline or whose Me origin is briefly
unreachable recovers once it is back. A refusal no retry can change ends the
retries instead: the attempt exits with status 78 (`RestartPreventExitStatus=78`)
when the signed record is missing, ambiguous, pending, stopped or in transition
(`record_missing`, `record_ambiguous`, `record_not_running`), when the rule
definition, snapshot or applied receipt differ from the reviewed ones
(`rule_definition_changed`), or when the host's network facts still differ
(`host_facts_changed`) once the kernel has been up for 5 minutes; before that a
host-facts mismatch is retried, so late interfaces and routes settle first. The
application then stays stopped until a reviewed update or start, which enables
the unit again; the next boot tries once more. The reason is in
`journalctl -u stackkit-own-restore-<16 hex identity>`. `stop` disables the
restore unit, stops a running attempt, resets its failed state and verifies its
boot link is gone, so a stopped application stays stopped across reboots; a
`multi-user.target.wants` link whose unit file is already gone is deleted
rather than blocking stop or remove;
`remove`, network-mode changes and kit rollback also delete the unit and the
snapshot together with the tables. A kit upgrade that keeps the executable path
keeps the restore; a moved or removed executable keeps the application stopped.
An outbound application started by an earlier kit has no restore unit yet; it
gets one with its next reviewed start or update.

Within one boot the rules stay as before: a watcher exit (changed rules, stale
echo, extra network) stops the application until a reviewed update or backup
resume. Docker daemon restart leaves these non-Docker tables intact; a stopped
container likewise requires a reviewed update. Docker live-restore may keep an
already running container and its existing rules. Kit upgrades preserve the
network and rules. Backup's exact
container restart verifies the retained applied snapshot against the signed
ruleset digest, refreshes the egress answer, and delegates start to the watcher.
It never reapplies or adopts fresh host facts. A lost live snapshot or
changed/missing rules within one boot requires a reviewed application start;
after a reboot the restore unit recreates both before any start.

Every kit executor recovery (Standard or Advanced) first removes outbound
application containers, their exact bridges and rules while retaining volumes,
secrets and signed v2 intent, and records them stopped. Any unverified cleanup
blocks invocation of the recovered executor. Thus a rollback to a kit without
this feature inherits no running outbound application; its old reader refuses
the retained v2 documents. Returning to a capable release requires a reviewed
update to start them. Application **image rollback** also revokes outbound
permission and returns the document to v1; re-enabling requires a v2 update.
These rules do not roll back application data or rotated secrets.

The deterministic boundary tests cover destination tables, missing/changed
rules, unsupported backend refusal, exact removal, review binding and rollback
ordering. Kernel packet traversal, DNS rebinding, Docker/host restart, backup
resumption and old-release rollback acceptance belong to the named M16 test
window. Rendering and synthetic kernel-readback tests do not constitute that
acceptance evidence.

## Published ports without outbound Internet

An adopted application (v4, below) that keeps published host ports but has no
outbound Internet (`"outboundInternet"` omitted or `false`, or adopted with
`--no-outbound`) runs on the same managed firewall bridge as an outbound one:
Docker publishes no port of a container attached only to an internal network.
"Firewalled" therefore means outbound Internet, published ports, or both, and
every firewalled application gets the same fixed MAC, IPv6 off, public DNS
resolvers, `restart: "no"`, kit watcher, boot-time restore unit, stop, removal,
backup resume and kit-rollback handling described above.

Its ruleset is **ingress-only**. The two per-application tables have the same
names and owner comments, but the bridge-sourced `prerouting`, `forward` and
`input` chains accept only conntrack replies from the application's MAC
(`ether saddr <MAC> ct direction reply`) and drop every other packet the
bridge sources, and the bridge `peers` chain is the same as above. There is no
egress set, no Me echo and no uplink, gateway or route observation, so its
static ruleset digest depends only on the network name, and it also starts and
restores on an offline host. Replies to clients of the published ports (through
Docker's DNAT or proxy), to the ingress routers and to the readiness probe pass;
anything the application starts itself, including DNS, is dropped. The
published ports keep their original bind addresses: `127.0.0.1` stays local, an
address-less binding stays IPv4 on every address.

## Compose projects (v3)

[`techstack.own-application/v3`](../schemas/techstack-own-application-v3.schema.json)
is the multi-container form: a Compose project of one to eight containers that
share one internal bridge. Exactly one service declares `httpPort` and is routed
on the project hostname. Every other service stays on the project's private
network, where it is reachable only from that project.

```json
{
  "apiVersion": "techstack.own-application/v3",
  "name": "hello",
  "hostname": "own-hello.lab.home",
  "services": [
    {
      "name": "web",
      "image": "docker.io/library/nginx:1.29-alpine",
      "httpPort": 80,
      "volume": {"mountPath": "/usr/share/nginx/html"}
    },
    {
      "name": "db",
      "image": "docker.io/library/postgres:16-alpine",
      "volume": {"mountPath": "/var/lib/postgresql/data"},
      "secretRefs": {"POSTGRES_PASSWORD": "secret://own-hello/db/POSTGRES_PASSWORD"}
    }
  ]
}
```

Service names are lowercase DNS labels starting with a letter, at most 40
characters, and unique within the project. Exactly one service must declare
`httpPort`; zero or two are refused. `dependsOn` may name other services of the
same project; unknown names, self references and dependency cycles are refused.
Each service may declare at most one named volume, one environment map and one
secret map. The project hostname is still exactly `own-<name>.<domain>`.

Limits are per service and are the same as for one container: 64..8192 MiB of
memory and 0.1..4 CPUs, defaulting to 512 MiB and 1 CPU. A service declares its
own `resources`; a document that puts `resources` at the top level is refused,
because the project has no shared default. **There is no project-wide ceiling:
a project of eight services may declare eight times the one-container limit,
and the installation does not reserve that memory up front.** The limits are
admitted per service, and the host still refuses a container that exceeds
them.

The same refusals as for one container apply to every service: no host ports, no
privileged mode, no host mounts, no devices, no capabilities, no extra networks,
and no image that inherits `traefik.*` labels or declares a volume the contract
did not admit. A container port is never published on the host, including
loopback, and the private services are never published either.

**A project has no outbound Internet access.** `outboundInternet` is a v2 field
and is refused in a v3 document; the managed host firewall grant belongs to the
one-container form only.

Each service resolves and imports its own image. A dry review resolves every tag
and shows `record.bundle.resolvedImages`; the `reviewDigest` binds those exact
digests. Pass them back on the executing call as `--resolved-images-file`, a JSON
object of service name and digest reference. Offline `render` requires every
service image to be digest-pinned.

Secrets are namespaced by service. Each key `K` of a service must reference
exactly `secret://own-<name>/<service>/K`. The private secrets file stays a flat
JSON object, but a project uses the slot `<service>.<KEY>`; the review binds its
content without returning values.

Every declared named volume of every service is included in the installation's
backup as `user-content`, and the exact runtime graph records all of the
project's services. `backupVolumes` lists them. Removal retains them in backup by
default; `remove --delete-data` deletes exactly the admitted local named volumes
of the project.

`update` changes only the image of the declared services. The service set, the
service order and every other field stay immutable, and an update never changes
between the one-container and the project form. A document that only reorders
the same services is refused: the service order is part of the admitted
document. `rollback` returns every service to its
previous successful pinned image. `stop`, `remove`, `status` and `logs` act on
the whole project; `logs` returns the bounded tail of every service. Readiness is
observed on the routed service before the router is attached.

A project routes exactly one hostname in this version. A second routed hostname
for another service of the same project is not admitted yet.

## Stack an existing container (v4)

"Stack it" takes one container that already runs on the node but that StackKits
did not install and rebuilds it as an own application, so it gets everything an
own application has: digest-pinned `update` (also to a newer tag of its image,
see [Tag update](#tag-update)) and `rollback`, backup of its volume, `logs`,
`start`, `stop`, `restart` and `status`. It works for any application that fits the
rules below, not only for profiles StackKits knows. The Home Assistant in-place
adoption ([Existing application adoption](APPLICATION_ADOPTION.md)) stays
separate: it keeps the original container and only takes power control.

The rebuilt application is a
[`techstack.own-application/v4`](../schemas/techstack-own-application-v4.schema.json)
document: the v2 single-container form plus `adoptedFrom` (container ID, image
digest, the one data source, `compose:` or `docker:` configuration reference)
and `publishedPorts`. Only `stackkit app own --from-container` creates it; a
hand-written v4 document is refused on create, and update keeps both fields.

```sh
stackkit app own --from-container mailpit --phase inspect --json
stackkit app own --from-container mailpit --name mail --phase plan --json
stackkit app own --from-container mailpit --name mail --phase adopt \
  --plan-digest sha256:<plan-digest> --operation-id mail-stack-1 --owner-approve --json
stackkit app own --from-container mailpit --name mail --phase verify --json
```

**Plan.** `inspect` and `plan` change nothing. Every observed setting becomes one
row, `kept`, `changed` or `refused`, with a stable machine `code`, structured
`values` and its reason ([Plan row codes](APPLICATION_ADOPTION.md#plan-row-codes)). The plan states plainly
that the container is **recreated as a new container (new container ID)** and
estimates the downtime (stop up to 30 s, copy and checksum, start and HTTP
readiness up to 60 s). It measures the data with the installed backup helper and
refuses unless at least twice its size is free on the Docker volume filesystem.
It probes the original the way the rebuilt one is probed (HTTP 2xx/3xx on `/` at
the web port, redirects not followed). The `planDigest` binds the container, the
document, every non-volatile row and the installation; adopt refuses a plan whose
digest no longer matches, before anything is stopped.

**Refused.** A container that belongs to a Compose project (Compose projects
come later), one on a user-defined network shared with other containers (a
database link would break), host or container networking, privileged mode, added
capabilities, devices, shared host namespaces, relaxed security profiles,
`--volumes-from`, custom runtimes, the Docker socket, a mount of `/`, a bind
mount outside `/srv`, `/opt`, `/data`, `/home`, `/mnt` or `/media`, more than one
data mount, an image volume that is not the one data mount, a custom command or
entrypoint, a user override, and an image without a registry digest (a
local-only or locally built image). Legacy links and the host user namespace
are refused too, and so is an existing application volume of the same name
(it may hold retained data; only the copy a rolled-back adoption of the same
container left is replaced). The image must still be served by its public HTTPS
registry at that digest.

**Kept and changed.**

- Image: pinned by the digest the original runs.
- Environment: every variable the container sets beyond its image. **Every
  non-empty value is hidden**: it moves into application-scoped encrypted secret
  custody (`secretRefs`, 0600), and plans, logs and receipts carry names only
  (`hiddenKeys`). Names that look like credentials (`*_KEY`, `*_TOKEN`,
  `*_PASSWORD`, `*_SECRET`) or that `--secret-env <NAME>` marks are also listed as
  `secretKeys`. Command arguments are never shown, only counted.
- Data: the one named volume or admitted bind mount is copied into the
  application's named volume with the source mounted read-only; then both are
  checksummed (content, and type, mode, owner and group of every entry) and
  must match. The original volume or directory is never changed,
  moved or renamed.
- Ports: the routed web port is chosen automatically when the container has one
  TCP port; otherwise pass `--http-port`. Every published host port is kept with
  **exactly its original host address**: `127.0.0.1` stays `127.0.0.1`, `0.0.0.0`
  stays IPv4, an unspecified address stays every address. Nothing is widened.
  Random host ports, IPv6-only bindings and TCP 80/443 (the router owns them) are
  dropped and shown. Kept ports stay exactly as reachable and as unauthenticated
  as they were; the new `own-<name>` hostname route requires Homelab sign-in
  (`signIn: true` is written explicitly) and follows the installation exposure.
- Outbound Internet: kept when the original had it, through the managed host
  firewall of v2 with all of its prerequisites. `--no-outbound` adopts without
  Internet and **keeps the published ports**: they run on the same firewall
  bridge with the ingress-only ruleset (see "Published ports without outbound
  Internet"), which answers connections made to the application and needs no
  uplink or Me echo. When the outbound firewall prerequisites are missing, the
  review row is refused and names `--no-outbound`.
- Restart: `unless-stopped`, or `no` with outbound Internet or published ports
  (as for every firewalled application, Docker never starts it; after a host
  reboot the restore unit re-applies its reviewed rules and the watcher starts
  it, see "Optional outbound Internet (v2)").
- Resources: the original's limits, or 2048 MiB and 2 CPUs for an unlimited
  original, within 64..8192 MiB and 0.1..4 CPUs; `--memory-mib` and `--cpus`
  choose others.
- Host-level overrides (extra hosts, DNS, sysctls, tmpfs, a read-only root, a
  container-level healthcheck) are dropped and shown.

**Adopt.** Only with `--owner-approve` and the matching `--plan-digest`, and
with the same options as the reviewed plan. Adopt sets the original's restart
policy to `no` and stops it, copies and checks the data, creates the application
through the reviewed own-application create, then requires its signed status, an
answer on every published TCP port and a container that keeps running without a
restart for a 2-minute stability window. On any failure it removes the new
workload (its volume is retained), restores the original's restart policy and
starts it again: outcome `rolled_back`. If that recovery itself fails, the
outcome is `rollback_failed`, the command exits nonzero after printing its
result, and `--restore-original` retries the recovery. The same command
recovers an adoption whose process was killed or whose host rebooted midway. A checkpoint
(`.stackkit/own-applications/adoptions/<name>.json`, 0600) records each step;
the signed own-application custody stays the only authority.

**After adoption** the original stays stopped with restart policy `no`. For 7
days (`retainUntil`, shown by adopt and verify) `--restore-original` removes the
rebuilt application (volume retained, with any data written since) and gives
the original its restart policy back, starting it when it ran before adoption. `--release-original` removes the
stopped original once the owner confirms with `--owner-approve`; its volume or
bind directory stays. Nothing is deleted automatically. The application itself is
operated like every own application by `--name`. An update keeps `adoptedFrom`
and the published ports (it may only clear the ports); rollback of an adopted
application keeps its outbound grant and ports, because the adoption review
granted them to the application, not to one image.

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

A project uses the same command with its own document and its own resolved
images:

```sh
stackkit app own --action create --name hello \
  --document .techstack/own-applications/hello.json \
  --secrets-file hello-secrets.json \
  --resolved-images-file hello-resolved-images.json \
  --owner-approve --operation-id hello-create-1 --review-digest sha256:<review-digest> --json
```

`hello-resolved-images.json` is the `record.bundle.resolvedImages` object the
dry review returned, one digest reference per service.

## Lifecycle and data

Use `--action update` with the next document to change `image` or supply
`--secrets-file` to rotate existing declared secrets. Other v1
fields stay immutable; remove and recreate to change them. A reviewed update
can also adopt changed same-node installation routing, including its new
hostname, without changing the image; reads and teardown keep using held
custody. Updating a stopped application to its recorded image starts it again. Use `rollback` to
return to the previous successful pinned image (and revoke any v2 outbound grant), `stop` to stop the container,
`start` to start a stopped application again, `restart` to stop and start a running one,
`remove` to remove it, and `status` or `logs` to inspect it. All mutations follow
the same dry-run, approval, digest and operation-ID sequence. These actions
select the stored application by `--name`; only create/update take a document.
`render` previews a document without creating an application.

`start` and `restart` run the exact signed bundle again: its admitted local
image (nothing is pulled), its network and, for a firewalled application
(outbound Internet or published ports), the host firewall rules freshly
observed in the review. A firewalled application has restart policy `no` and
starts only through its firewall watcher once the reviewed rules are applied
and verified, never by a plain `docker start` around them; this also brings an
application back after a kit rollback stopped it. `start` needs a stopped
application and `restart` a running one; neither settles a pending operation.

Every result carries an `outcome` code: `reviewed` (dry run or render),
`observed` or `observation_unavailable` (status, logs), `created`,
`updated`, `rolled_back`, `started`, `stopped`, `restarted` or `removed`.
A refused call still writes one result with `outcome: "refused"` (nothing
changed), `"pending"` (signed custody records the started mutation; inspect
status before a new reviewed operation) or `"denied"` (capability admission),
a stable `reasonCode` (`invalid_request`, `decision_receipt_refused`,
`document_invalid`, `resolved_images_invalid`, `secret_input_invalid`,
`installation_unavailable`, `name_collision`, `custody_unavailable`,
`backup_coverage_missing`, `operation_pending`, `action_refused` or an
`advanced_capability_*` denial) and the message in `reason`.

### Tag update

`--action update --image-tag <tag>` moves a one-container application,
including one taken over with "Stack it", to a newer version tag of its own
image repository without writing a document:

```bash
stackkit app own --name hello --action update --image-tag 1.27.4 --dry-run --json
stackkit app own --name hello --action update --image-tag 1.27.4 \
  --resolved-image <target.resolvedImage> --review-digest <reviewDigest> \
  --operation-id hello-update-1 --owner-approve --json
```

The tag must be a version (`1.27.4`, `v2.4`, `3.1.0-alpine`) newer than the
running one, with the same major and the same variant suffix. `--allow-major`
admits another major; it never admits a downgrade, the same version or another
variant. A running image without a tag (an adopted application is
digest-pinned) is compared with its `org.opencontainers.image.version` label;
without it the update needs `--allow-major`. The dry run resolves the tag to its
registry digest and echoes `allowMajor`; the tag and `--allow-major` are part
of its review digest.

The execution resolves the tag again and refuses a moved tag (`tag_moved`)
before anything stops. For an application with a volume it measures the data
and the free space of the Docker volume filesystem first (the copy needs its
size plus 10 % and 64 MiB; otherwise `checkpoint_space_insufficient`, nothing
changed), then stops the application and takes an **application-scoped data
checkpoint**: an operation-bound copy of each of its named volumes, made with
the installed backup helper and read back by a digest over file contents and
the type, mode, owner (uid:gid) and path of every entry, every symlink target
and every empty directory, recorded in the signed application record. It fails
closed (`checkpoint_failed`, nothing changed, the copy is removed and the
application starts again) when the helper is not installed or the copy does not
read back. It then runs the reviewed update and requires every container
running and, when the image has a healthcheck, `healthy` (a container still
`starting` is waited for up to 3 minutes and never counts as verified), now and
after a 30-second stability window.

Any failure rolls back **image and data**, on its own budget even if the
calling process is cancelled: the application is stopped, the checkpoint copy is
read back and copied into the live volumes, the live data is read back against
the checkpoint, the reviewed rollback returns to the prior image, and the
verification runs again. `rolled_back` means image and data are both back.
When a step fails the outcome is `rollback_failed` with `reasonCode`
`stop_failed`, `data_restore_failed`, `image_rollback_failed` or
`rollback_verify_failed`; the retained copy stays intact and `checkpoint` names
it with the exact `restoreCommand`:

```bash
stackkit app own --name hello --action rollback --checkpoint hello-update-1 --dry-run --json
stackkit app own --name hello --action rollback --checkpoint hello-update-1 \
  --review-digest <reviewDigest> --operation-id hello-update-1-restore --owner-approve --json
```

`--checkpoint` stops the application, restores that checkpoint's data and
returns to the image recorded with it (or starts that image again when an
earlier rollback already returned to it).

The signed record keeps the checkpoint's `state`: `taken` while its update has
not verified, `retained` after a verified update (or one that never ran),
`restoring` from just before a rollback writes live data until it finishes,
`restored` once a rollback copied it back and read the live data back,
`kept` when a rollback did not restore it (the copy is then the only intact
data), and `discarded`. While a checkpoint is `taken`, `restoring` or `kept`, a new tag
update is refused (`checkpoint_unrestored`, with the restore and discard
commands); a new checkpoint removes an earlier copy only after it read back
itself and only when the earlier one is `retained`, `restored` or
`discarded`. The retained copy uses disk space: every result reports it in
`checkpoint.retainedBytes` with the exact `discardCommand`:

```bash
stackkit app own --name hello --action discard-checkpoint --checkpoint hello-update-1 --dry-run --json
stackkit app own --name hello --action discard-checkpoint --checkpoint hello-update-1 \
  --review-digest <reviewDigest> --operation-id hello-update-1-discard --owner-approve --json
```

Discarding is the owner's explicit decision, also for a `kept` copy: the live
data then stays as it is. A `taken` or `restoring` checkpoint (its update or
rollback has not finished) is never discarded (`checkpoint_in_flight`). The discard checks the state and
its review digest, removes the copy and records `discarded` under one
lifecycle lock; the state moves only along taken → retained or kept, kept →
restoring or discarded, restoring → restored or kept, retained or restored →
kept or discarded, and nothing leaves `discarded`. A tag update's dry run names the copy it will replace in
`replacesCheckpoint` (state and `retainedBytes`); the next checkpoint
replaces a retained or restored copy once its own copy read back.
`remove --delete-data` deletes a retained, restored or discarded copy with the
data and refuses while a checkpoint is `kept`, `taken` or `restoring`: restore or discard
it first.

The readback digest was verified against the real copy on 2026-11-12 with the
installed helper image (`kopia/kopia:0.18.2`, GNU cp 8.32) and with BusyBox
1.37: `cp -a src/. dst/` keeps the volume root's own mode and owner (0700
70:70 in the probe), nested modes and owners, symlink targets and empty
directories, the digests of source and copy are equal, and a changed root mode
changes the digest.

The result (`stackkit.own-application-update/v1`) carries `outcome`
(`reviewed`, `updated`, `refused`, `rolled_back`, `rollback_failed`),
`reasonCode` (`application_not_found`, `project_unsupported`,
`operation_pending`, `tag_not_versioned`, `variant_change`,
`current_version_unknown`, `not_newer`, `major_change`, `resolve_failed`,
`tag_moved`, `checkpoint_failed`, `checkpoint_unavailable`,
`approval_missing`, `update_refused`, `update_failed`, `verify_failed`,
`stop_failed`, `data_restore_failed`, `image_rollback_failed`,
`rollback_verify_failed`, `checkpoint_unrestored`,
`checkpoint_space_insufficient`, or a reason code of the call's admission),
`allowMajor`, the prior and target image and version, `checkpoint`
(`operationId`, `volumes`, `state`, `retainedBytes`, `restoreCommand`,
`discardCommand`) and the inner update and rollback results; a discard answers
`outcome: "discarded"`. `targetCheck` is always `not_checked_by_kombify`:
kombify tests catalog releases, never the image of an own application. A
project updates through its reviewed document.

### Managed nodes

On a node Techstack manages (install mode `advanced` or an imported Advanced
trust), an executed `start`, `stop`, `restart`, `update` or `rollback` also
(or `discard-checkpoint`) needs `--capability <file>`: a Techstack capability whose only
`allowedOperations` entry is `own-application.lifecycle`, valid for at most 15
minutes, for this stack and Owner, whose `ownApplicationLifecycle` scope names
exactly this application, action, `--operation-id` and the `--review-digest` of
its dry run. Any other call is denied (`advanced_capability_*`) before anything
changes, so a capability is never replayed for another application, action,
operation or review. Dry runs, `status` and `logs` need none; `create` and
`remove` keep their admission. A standalone node runs these actions in Standard
Mode under `--owner-approve` and refuses a capability. Techstack dispatches them
as the catalog operations `own-application.status`, `.logs`, `.review`,
`.start`, `.stop`, `.restart`, `.update.review`, `.update`, `.rollback`,
`.checkpoint-rollback.review`, `.checkpoint-rollback`,
`.checkpoint-discard.review`, `.checkpoint-discard` and `.backup`
(`stackkit backup run`, which holds every own application volume).

A project follows the same rules with its own scope: an update changes only the
image of the declared services, and the service set, the service order, the
routed service, the hostname and every other field stay immutable.

Status reports signed intent separately from current Compose observation.
A missing or failed observation is unknown, not healthy. Startup checks container readiness (including any image healthcheck) and
requires an HTTP 2xx/3xx response on `/` at the admitted private container port
before attaching the router and admitting the route. Redirects are not followed.
Pending signed state keeps the route closed on updates too; readiness is not
evidence that a full application journey passed. A plain `rollback` restores an image, not application data or a database
schema; a tag update's automatic rollback and `rollback --checkpoint` restore its data checkpoint as well.

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

Each v1 or outbound-disabled application without published ports joins one internal bridge. Only the existing LAN/remote ingress routers join it in
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
`resolved_image`, `image_tag`, `allow_major`, `capability_file`, `secrets_file`, `keep_secrets`, `review_digest`,
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
