# StackKits Installation Processes


The supported ways a user or agent installs and rolls out a StackKit. It
separates discovery, installation, execution authority and post-install
management, so a read-only website surface is never mistaken for a
target-server control surface.

The standalone CLI runs **Standard Mode** with local Owner custody and no
kombify account. Advanced Mode, provider execution and Modern multi-site apply
belong to Techstack; installing Modern definitions admits no standalone
Advanced executor. Owner email initializes the node's own identity, not a
kombify signup. Standalone Compose is the complete primary experience,
applications included; the CLI is an optional interface to the shared
lifecycle. Coolify, Komodo and Dokploy are Layer-2 PaaS platforms and later
runtime targets (Marketplace installs Coolify as its engine); support needs a
current-pin receipt ([ADR-0042](ADR/ADR-0042-standalone-default-and-optional-platforms.md)).

Not implemented: managed-serverless provisioning, SaaS placement beyond S1,
`stackkit app add` customer-app deployment, internal kombify operator MCPs.

## User-Facing MCP Model

Users see one MCP connection: `stackkit`.

| Runtime form | Binary or endpoint | Meaning |
| --- | --- | --- |
| Local adapter | `stackkit-mcp` stdio or loopback HTTP | The `stackkit` connection on the CLI/workspace machine (P6) |
| Durable endpoint | `stackkit-server POST /mcp` | The same connection after install, only behind a protected access path (P5) |
| State Console authoring | `mcp-use/stackkits-app` | Build/Inspector layer for `ui://stackkits/state-console.html`; not a production connector or lifecycle owner |

## Common Contract

All paths share one lifecycle:

```text
discover -> collect intent -> install toolchain -> init -> validate -> generate -> plan -> apply -> verify -> preserve evidence
```

- `stack-spec.yaml` and CUE contracts are the editable contract; generated `deploy/`, OpenTofu state, `.stackkit/state.yaml`, logs, run evidence and snapshots are outputs agents must not hand-edit.
- Basement Kit is the primary standalone path; full current-release target lifecycle evidence is pending. Unreleased kit definitions stay outside the public beta install surface.

| Input | Purpose | Default or current stance |
| --- | --- | --- |
| Owner/admin email | Local Owner identity and technical admin material | Guided installers require a real email; shared test identities serve engineering receipts |
| StackKit | Kit to install | `basement-kit` |
| Product mode | Execution boundary | Standard only; Advanced is Techstack-only |
| Context | Target environment | `local`, `cloud` or `pi` |
| Domain strategy | Routing and user links | device-enrolled `home`, `kombify.me`, or one custom domain |
| Workspace/spec path | Where the contract lives | `./stack-spec.yaml` |
| Target access | Local shell, SSH or MCP endpoint | per path |
| Write approval | Permission to mutate | Explicit operator approval; MCP writes also need `STACKKIT_MCP_ALLOW_WRITE=true` |

Evidence to keep: latest run ID and `.stackkit/runs/<runId>/`; `stackkit verify --http --json` when routes are expected; `stackkit status --http --json` for a fresh route probe (proves only the verifier host; LAN, mobile, VPN and public clients need their own evidence); relevant `stackkit logs`; the final Hub URL for local Basement (`https://base.lab.home` from an enrolled device); confirmation that generated artifacts were not hand-edited.

Further decisions every path collects: execution place and authority boundary (website-only, local CLI, on-server agent, SSH agent, local MCP, protected target MCP), installer interaction (`STACKKIT_INSTALL_MODE=auto|guided|expert`, all Standard Mode), workload execution (Compose by default; PaaS per receipts), service profile (`default`, `admin-only`) and apply approval. Website/Web-MCP discovery (P0) decides and prepares but is never authority; execution happens through CLI, SSH, an on-server agent or the native connector.

## What StackSpec And Config Can Express

New installations use the native v2alpha2 fields and flags in [CONFIGURATION.md](CONFIGURATION.md#stack-spec); `context`, global `compute.tier`, `paas` and legacy bootstrap fields are migration input and must not be copied into new intent. Workload selection and resource profiles: [OPTIONS_AND_AUTHORING.md](OPTIONS_AND_AUTHORING.md).

| Capability | Config surface | Notes |
| --- | --- | --- |
| Owner and admin intent | `--owner-source=local`, `--owner-email`, `--owner-username` | Account-free node custody; legacy `adminEmail` is migration input |
| Kit selection | installer argument, `stackkit init <kit>` | `basement-kit` primary |
| Product mode | Standard Mode; retired `STACKKIT_MODE` warns and is ignored | A legacy value cannot select Advanced |
| Target context | legacy v1 `context` | Native `init`/`generate` reject `--context`; `local`/`cloud`/`pi` map to site kind and hardware profile |
| Domain strategy | `--domain`, `DOMAIN`, network intent | Local links use `*.lab.home`; public/custom domains and enrolled clients need their own DNS/TLS proof |
| Optional applications | `--use-case`, `STACKKIT_USE_CASES` | Empty or `none` selects core only; omitted offers the picker or catalog defaults |
| Compute and topology | Module profiles, sites, nodes, execution channels | CUE admits the attested local target; standalone acts on the installed server |
| Execution selection | Workload alternative plus module-local profile; `--catalog-defaults` | Compose default; Komodo is the explicit operations-platform path; Marketplace installs Coolify as an application; legacy `paas` selects nothing native |
| Setup policy | `bootstrap.*`, `application.*.setup.policy`, `services.*.setup.policy` | `manual`, `on_demand`, `automatic` |
| Demo data | `demoData.enabled` | Explicit opt-in |
| MCP write mode | `STACKKIT_MCP_ALLOW_WRITE`, `--mcp-allow-write` | Read-only tools stay available |
| Remote MCP protection | `STACKKIT_MCP_TOKEN`, `--mcp-token`, proxy/tunnel/VPN | Non-loopback target MCP is a protected day-2 posture |

## Automation And Individualization

Two independent axes. Automation never enlarges the CLI's execution authority; mutating operations always need shell, SSH or MCP write authority. Higher individualization needs more review and evidence (custom domains need DNS/TLS receipts; composition is still generated from StackSpec/CUE).

| Level | Automation | Level | Individualization |
| --- | --- | --- | --- |
| `A0` | Discovery only (website, `llms.txt`, OpenMCP) | `I0` | Default Basement Kit |
| `A1` | Manual CLI, user runs each command | `I1` | Owner email, stack name, workspace/spec path |
| `A2` | Guided agent, user supplies intent and approvals | `I2` | Kit, use cases, module profiles |
| `A3` | Autonomous approved rollout (one-liner, on-target agent) | `I3` | Domain, SSH target, custom DNS/TLS, PaaS |
| `A4` | Durable connector day-2 (`stackkit-server /mcp` with token and write gate) | `I4` | Explicit Standard-Mode composition; Advanced topology needs Techstack |

## Installation Matrix

| ID | Process | Agent location | Entrypoint | Auto | Indiv | Authority | Best fit |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `P0` | Website and Web-MCP discovery | Browser/remote agent | `https://stackkit.cc`, `/llms.txt`, `/openmcp.json` | `A0` | `I0-I4` planning | Read-only | Learn, choose a path, generate commands |
| `P1` | Basement one-line installer | Target shell | `curl -sSL https://base.stackkit.cc \| sh` | `A3` | `I0-I3` (env vars) | Target shell, root or sudo | Fastest fresh-server rollout |
| `P2` | Shared CLI installer + direct CLI | User/agent shell | `curl -sSL https://install.stackkit.cc \| sh` | `A1-A2` | `I0-I4` | Local shell; root/sudo only to prepare the host | Explicit, reviewable lifecycle steps |
| `P3` | Agent on the target server | Target shell | Website prompt or `stackkit agent prompt ...` | `A2-A3` | `I0-I4` | Shell user | Autonomous local rollout |
| `P4` | External agent over SSH | Operator/agent host | SSH plus installer/CLI | `A2-A3` | `I1-I4` | SSH user privileges | Agent outside the server, no MCP endpoint yet |
| `P5` | Protected durable MCP endpoint | Operator/agent host | `POST https://<protected-target>/mcp`, VPN or tunnel | target `A4` | `I1-I4` | MCP tools gated by token, scope and write policy | Durable day-2 after install without SSH |
| `P6` | Local MCP adapter | CLI machine | `stackkit-mcp` | `A2` | `I1-I4` | Local process; write mode opt-in | Colocated MCP client (over SSH the real boundary is `P4`) |

Decision rules: `P0` first when only discovering; `P1` for a fresh target when automation beats review; `P2` when plan review and customization matter; `P3` when the agent is on the target; `P4` when the agent is outside and must bootstrap before any connector exists; `P6` when MCP client and workspace are colocated; `P5` only after `stackkit-server /mcp` is installed and exposed through a protected path. Typical progression: `P0` -> `P1`-`P4` -> optional `P5`. Recommended UI: one `stackkit` MCP connection, the user chooses local adapter (`P6`) or protected endpoint (`P5`).

## P0: Website And Web-MCP Discovery

Read-only; installs nothing and acts on no target. Entrypoints: `https://stackkit.cc/llms.txt`, `/llms-full.txt`, `/llms-snippets.txt`, `/openmcp.json`, `/mcp/stackkit-mcp.md`. The agent reads docs, kit stance, installer URLs, OpenAPI, schemas and prompts, picks the execution path, asks for missing intent (email, domain mode, target host, SSH key, write approval) and produces commands or connects to a local/native surface. The website may point to the native local connector but does not host it.

## P1: Full Basement Kit One-Line Installer

```bash
curl -sSL https://base.stackkit.cc | sh
```

Run it in the target server's own shell (SSH, console or on-server agent). Default URLs use `*.lab.home` with Owner-CA HTTPS; each client enrolls once on the LAN so its OS-approved profile scopes `home` lookups to the StackKits resolver and trusts the public Owner-CA root. No router/DHCP changes, hosts entries or alternate local names. Beta validation pins the release:

```bash
env STACKKIT_RELEASE_VERSION='<exact-release-tag>' sh -c 'curl -fsSL https://base.stackkit.cc | sh'
```

Whoever wants stepwise control installs the CLI and drives the verbs (P2); this entrypoint carries the user to a running homelab:

1. Resolves the install mode: `STACKKIT_INSTALL_MODE=auto|guided|expert`, or an interactive menu on a TTY (Enter = Quick Install). Non-TTY runs use `auto`; `guided`/`expert` without a TTY fail closed and print the equivalent CLI chain.
2. Collects decisions per mode, mapping each to a CLI parameter; environment values are never re-asked. Guided: workspace, domain strategy, local Owner email, optional use cases. Expert adds stack name and workload adapter (adapter choice changes neither product mode nor Marketplace).
3. Re-running on a workspace with deployment intent skips init, prepares the host, re-resolves the plan (`stackkit generate`) and resumes `stackkit apply` (native Apply journal). The installer journal preserves pre-init selections and interrupted phases; any directory works with the same workspace; saved kit intent beats new network detection; a re-run never removes a stack or converts its kit.
4. Runs the shared CLI installer from `https://install.stackkit.cc` (binaries, packaged OpenTofu, kit definitions; SUDO_USER-safe).
5. `stackkit init basement-kit --catalog-defaults --non-interactive --owner-source=local` with the collected `--domain`, `--name`, `--owner-email`, `--owner-username`, `--use-case`, `--platform`, then `stackkit validate`. Empty use-case input is passed explicitly so it cannot become the default application set.
6. Bootstraps the container runtime (Docker via get.docker.com when missing, docker group activated for non-root runs). Native `stackkit prepare` only checks prerequisites; the guided installer owns bootstrap.
7. Builds the local FROM-scratch `stackkit-server:local` image with the host CA bundle, falling back to the registry image when Docker or the binary is unavailable.
8. `stackkit host preflight` admits the host read-only; in `auto` mode `stackkit host remediate --auto-reversible --yes` applies undoable fixes (bounded container logs, swap) and re-measures (`STACKKIT_REMEDIATE=0` opts out; a failed fix never stops the install). Then `stackkit generate` and one `stackkit apply --auto-approve`. Apply configures host firewall, sshd and fail2ban, which only root can change: a non-root run hands that Apply to root via passwordless `sudo` (restoring workspace ownership) or prints the exact command. Ports below 1024 are read from the kernel socket table when a non-root bind probe is refused. If the host has less RAM than the services reserve, `generate` names the amount and the remedy. The ResolvedPlan owns execution order, so no platform-app stage exists.
9. `stackkit verify` runs before the journal completes and the access summary prints (service URLs, initial admin credentials, machine-readable access summary path, project directory, shell-quoted whole-install removal commands with their scope). A refused host exits 3 untouched; a failed rollout exits non-zero and names how to inspect and re-apply.

Important environment variables (each pre-seeds one decision):

| Variable | Purpose |
| --- | --- |
| `STACKKIT_INSTALL_MODE` | `auto`, `guided`, or `expert`; non-TTY always `auto`. |
| `HOMELAB_DIR` | Workspace; defaults to `$HOME/my-homelab`. |
| `DOMAIN` | Canonical service domain. Basement defaults to `lab.home`; Cloud defaults to `kombify.me`. Local clients enroll the scoped resolver/Owner-CA profile. |
| `STACKKIT_NAME` | Deployment contract ID; default derives from the workspace name. |
| `STACKKIT_ADMIN_EMAIL` | Admin/owner email (`KOMBIFY_USER_EMAIL` fallback). |
| `STACKKIT_OWNER_USERNAME` | Node-local login username; default derives from the Owner email. |
| `STACKKIT_USE_CASES` | Optional workloads; `''` or `none` is an explicit core-only selection, preserved on retry. Omit to use the picker or noninteractive defaults. |
| `STACKKIT_PLATFORM` / `STACKKIT_PAAS` | Explicit compatibility workload-adapter selection (`coolify`/`komodo`); does not select Marketplace or define native defaults. |
| `STACKKIT_SERVER_IMAGE` | stackkit-server image override (skips the local build). |
| `STACKKIT_INSTALL_URL` | Alternate published release-installer endpoint for fixtures or mirrors. |
| `STACKKIT_RELEASE_VERSION` | Exact release tag consumed by the shared installer when testing a candidate. |
| `STACKKIT_CHANNEL` | Release channel, `stable` (default) or `preview` ("Nightly", unsigned). Same as `install.sh --channel`; `stackkit channel set` saves it in `~/.stackkits/channel`. An unknown value fails closed. |
| `STACKKIT_ALLOW_DOWNGRADE` | `1` lets the stable channel replace a newer running preview build; otherwise the installer keeps it until a stable release at least as new exists. |

Account-free Standard Mode, core only, with all decisions supplied:

```bash
STACKKIT_INSTALL_MODE=auto \
HOMELAB_DIR="$HOME/my-homelab" \
STACKKIT_ADMIN_EMAIL='<your-email>' \
STACKKIT_USE_CASES='' \
sh -c 'curl -fsSL https://base.stackkit.cc | sh'
```

Use when the user wants one command to a running homelab and can run shell on the target. Do not use when the operator wants to drive each verb (install the CLI and run P2) or the intent is outside the published Basement scope.

### Core installer custody under sudo

The Core installer keeps its private recovery journal in the invoking OS
user's `~/.stackkits/installer/core.tsv`, including when it runs through
`sudo`. Every atomic phase update retains that user's ownership and mode
`0600`. The invoking user can therefore resume from another directory without
losing the saved release, kit or explicit CLI-only intent. Writing binaries
to a protected system directory still needs the normal installation privilege.

An older installer may have left a root-owned private journal in that user's
home. Keep the original journal and re-run the same Core entrypoint with
`sudo` from the original invoking account; the installer repairs its own
journal ownership. Do not delete the journal, make it world-readable or copy
another user's recovery state. An unreadable journal fails closed with this
recovery guidance rather than claiming that its schema is unsupported.
Privileged ownership recovery rejects a redirected journal directory before
changing its ownership; keep the original recovery directory in place.

### Standalone removal and data retention

A repeated installer invocation is recovery, never removal. Review the
separate removal command and its data policy before intentionally deleting a
deployment. Upgrade and rollback use their native transactions; re-running
an installer is not a supported migration between kits.

The Basement and Cloud installer summaries print a copyable command using the
actual `HOMELAB_DIR`, safely quoted for the shell, including spaces and shell
metacharacters. Whole-install removal needs no workload or placement flags:

```bash
(cd -- "$HOMELAB_DIR" && stackkit remove)
```

This stops and removes **all `stackkit-*` Compose projects on the local host**
and other StackKits-labeled runtime containers and networks, including other
StackKits deployments. Named application volumes and the original workspace
are retained by default. The directory selects local file cleanup, not the
runtime removal scope. After reviewing that scope, this unattended equivalent
approves runtime removal with the same data-retention policy:

```bash
(cd -- "$HOMELAB_DIR" && stackkit remove --auto-approve)
```

Add `--delete-data` only to intentionally delete named volumes in that removal
scope, including orphaned StackKits-labeled volumes. `--purge` also deletes the
selected workspace's Owner/recovery state and deploy directory and removes
cached StackKits images after runtime cleanup. Host bind-mount files and
external backups are outside these deletion scopes. Keep the original workspace,
specification and release when reinstalling to reuse retained volumes; removal
and reinstallation are not a kit migration or upgrade.

An incomplete removal returns `remove_incomplete` and retains the original
workspace, even with `--purge --force`. Resolve the reported resource within
its approved ownership scope, then rerun the same command and data policy.

Whole-install removal does not support `--json` or `--terminal-evidence-json`.
For a single applied Architecture v2 workload, use its exact workload, Site,
node and execution-channel refs from the applied plan, in its workspace:

```bash
stackkit remove --workload "$WORKLOAD_REF" \
  --local-site "$SITE_REF" --local-node "$NODE_REF" \
  --local-execution-channel "$CHANNEL_REF" --auto-approve --json
```

Here `--auto-approve` explicitly consents to removing that workload. This
targeted path also retains application volumes by default; add `--delete-data` only
to explicitly delete data volumes owned by that exact applied workload. It
requires the existing applied plan/generation/Apply authority and does not
grant permission to remove unrelated workloads or provisioned servers.

## Interrupted Installer Recovery

The three shell entrypoints keep a private, atomically replaced TSV journal.
It contains progress and non-secret authoring choices, never credentials, and
is read as data. A phase is a retry hint: the CLI revalidates the workspace and
observes the host again; it does not blindly skip an earlier command.

| Entrypoint | Journal | Retry behavior |
| --- | --- | --- |
| `install.stackkit.cc` | Invoking user's `~/.stackkits/installer/core.tsv` | Reuse the selected version after interruption unless an explicit release override is supplied; verify the archive again and reinstall the selected binaries and definitions |
| `base.stackkit.cc` | `$HOMELAB_DIR/.stackkit/installer/workspace.tsv` | Preserve the saved Basement or Cloud intent and explicit empty use cases, skip init for a valid existing Owner, regenerate and resume native Apply, then verify |
| `cloud.stackkit.cc` | `$HOMELAB_DIR/.stackkit/installer/workspace.tsv` | Preserve Cloud intent even if network detection changes; a saved Basement workspace continues through its own entrypoint without removal |

Pass the **same absolute `HOMELAB_DIR`** when retrying from another directory.
An omitted Owner email, domain, name, platform or use-case selection is
recovered from the pre-init journal. Supplied authoring values take precedence;
the existing StackSpec remains authoritative once initialized. An explicit
empty `STACKKIT_USE_CASES=''` remains empty. Secrets still come from node
custody and its existing supported delivery path.

```bash
HOMELAB_DIR="$HOME/my-homelab" sh -c 'curl -fsSL https://base.stackkit.cc | sh'
stackkit -C "$HOME/my-homelab" verify --json
stackkit -C "$HOME/my-homelab" agent self-check --json
```

Interruptions and errors leave `state=interrupted` and name the last phase and
journal. A user-declined Apply leaves prepared intent. `state=complete` is
written only after the wrapper's native verification succeeds. A later rerun
still re-observes the target. Unsupported journal schemas fail closed with
an inspection action.

Use the complete website entrypoint for `curl | sh`. Repository scripts are
run by their explicit path with `install-host-bootstrap.sh` beside them;
streaming a raw repository script fails closed. No streamed script loads a
helper from the caller's directory. Nested installer downloads must succeed
and be nonempty before any shell runs.

The old `deploy-to-vm.sh` remote Docker-daemon path and retired persistent-VM
runbook are removed. Run installers inside an owned guest or its authorized
SSH session and preserve artifact, source, Apply and cleanup receipts. Broad
provider/device/compatibility acceptance remains test-window work.

## P2: Shared CLI Installer Plus Direct CLI

```bash
curl -sSL https://install.stackkit.cc | sh
mkdir my-homelab
cd my-homelab
stackkit init basement-kit --owner-source=local \
  --owner-email '<your-email>' --catalog-defaults --use-case ''
stackkit validate
stackkit generate
stackkit plan
stackkit apply          # requires a prepared host with Docker access
stackkit verify --http --json
```

`stackkit prepare` inspects local host prerequisites (no Docker install, no package changes); run it on the target through the owner-managed channel. Module-local capacity admission uses the attested Inventory and ResolvedPlan. A non-root user needs Docker group access before apply (`sudo usermod -aG docker $USER` and re-login) or lets the guided installer activate it.

The shared installer accepts kit arguments (`sh -s -- basement-kit`, `sh -s -- all`); `all` installs definitions and is not a kit slug for `init`. Modern definitions can be authored and inspected through the admitted standalone preview path; Modern multi-site apply stays Techstack-owned.

Native v2 init accepts the full authoring parameter surface (unknown values
fail closed with the kit's declared choices):

| Flag | Effect |
| --- | --- |
| `--non-interactive` | Fail instead of prompting. |
| `--owner-source=local` | Account-free local Owner custody (+ `--owner-email`, `--owner-username`, `--owner-display-name`). |
| `--domain <base>` | `network.domain.base` authoring override. |
| `--name <id>` | Deployment contract ID (default: normalized workspace name). |
| `--use-case photos,files,vault` | Optional kit workloads with the governed catalog default alternative and canonical `secret://workloads/<id>/<ref>` inputs. |
| `--platform coolify\|komodo` | Explicit compatibility workload-adapter selection, validated against `runtime.allowedAdapterRefs`; does not select Marketplace or define native defaults. |
| `--enable <capability-ids>` | Optional kit capabilities (`capabilities.enable`). |

Journey: install binaries and kit catalog; create or select a workspace (contract ID derives from its name; one host runs one active local deployment); `stackkit init`; ensure Docker access (apply installs Docker on demand); generate and review the plan; apply after explicit approval (`--auto-approve` for automation; host admission runs first and refuses before mutation, strictness via `--preflight strict|warn|skip`); verify and collect evidence.

## P3: Agent Already On The Target Server

Execution is local to the target with the shell user's authority (root/sudo may be needed for host preparation); no network-exposed MCP endpoint is required. The website is guidance only.

```bash
curl -sSL https://base.stackkit.cc | sh
# or:
curl -sSL https://install.stackkit.cc | sh
stackkit agent install-plan --json
stackkit agent self-check --json
stackkit init basement-kit --owner-source=local --non-interactive \
  --use-case-alternative basement-core=standalone \
  --module-compute-profile stackkits-basement-core-runtime=standard
stackkit validate
stackkit generate
stackkit plan
stackkit apply
stackkit verify --http --json
```

## P4: External Agent Through SSH Or Remote Shell

The operator provides target host, SSH user, key path, admin email and domain strategy; the agent confirms the host is dedicated to StackKits or safe to prepare, downloads the installer on the target and runs the one-liner or the explicit CLI lifecycle remotely. For a legacy v1 target only, `stackkit verify --host --legacy-remote-transport` is available; Architecture v2 invokes the on-host verifier through its externally resolved execution channel. SSH is the execution authority (not website MCP) and is not an Architecture v2 host-binding or execution-channel implementation. Preserve exact remote command output, the failing command, a stderr summary and the failure class.

## Remote MCP Transport And Day-2 Model

The standards-based remote MCP shape is Streamable HTTP, not WebSocket. MCP currently defines two standard transports:

- `stdio` for local subprocess connections;
- Streamable HTTP for remote-capable connections through a single MCP endpoint such as `/mcp`.

Streamable HTTP uses `POST` for client-to-server JSON-RPC messages. The server can answer with one JSON response or with an SSE stream (`text/event-stream`) when it needs to stream multiple messages. A client can also open `GET /mcp` with `Accept: text/event-stream` for server-to-client messages if the server supports that stream. WebSocket is possible only as a custom transport or gateway pattern; it should not be the default StackKits connector surface because it lowers interoperability with standard MCP clients.

Safe exposure patterns, from most conservative to broader:

| Pattern | Shape | Use |
| --- | --- | --- |
| Local only | `127.0.0.1:8082`, stdio, or loopback HTTP | Default developer/local agent mode |
| SSH tunnel | External agent opens a temporary tunnel to target loopback `/mcp` | Good for controlled maintenance without public exposure |
| Private network | VPN, Tailscale, WireGuard, private LAN | Good for a small operator fleet |
| Protected HTTPS | Reverse proxy, Cloudflare Tunnel/Access, mTLS, or OAuth-aware gateway | Target shape for durable external day-2 access |

Security requirements for any non-loopback MCP access:

- bind to loopback by default; require an explicit flag/config for non-loopback listeners;
- require TLS or a protected tunnel for remote access;
- validate `Origin` on HTTP requests to avoid browser/DNS-rebinding attacks;
- require `Authorization: Bearer <token>` or OAuth-style auth on every HTTP request;
- bind tokens to the MCP resource/audience, for example the canonical `/mcp` URI;
- keep read-only tools available first and require `STACKKIT_MCP_ALLOW_WRITE=true` or equivalent explicit server config for mutations;
- separate read scopes from write scopes;
- keep tool annotations accurate (`readOnly`, `idempotent`, `destructive`, `openWorld`);
- restrict operations to StackKits workspaces and generated evidence paths;
- log every mutating tool call with run ID, actor, target, input hash, and evidence location.

Current product stance: StackKit-owned durable day-2 through remote MCP is a target capability. Today, StackKits first-install flows should use `P1`, `P2`, `P3`, or `P4`. Once an installed StackKit owns a hardened `stackkit-server` process, `P5` becomes the interesting day-2 path for config read, status, verify, logs, drift detection, update planning, and gated updates without giving the agent a general SSH shell.

## P5: Protected Durable StackKits MCP Endpoint

The agent is outside the target host, which runs `stackkit-server` exposing the native connector (`POST https://<protected-target>/mcp`, `GET /openmcp.json`).

- Prerequisites: `stackkit-server` installed; firewall, tunnel, VPN, private network or reverse access explicitly configured; `STACKKIT_MCP_TOKEN` / `--mcp-token` for non-loopback access; `STACKKIT_MCP_ALLOW_WRITE=true` / `--mcp-allow-write` for mutations; a Streamable-HTTP MCP client.
- Journey: initial install via `P1`-`P4`; operator starts `stackkit-server` and configures protected access, token and optional write mode; the agent reads `/openmcp.json`, opens `ui://stackkits/state-console.html` where MCP Apps is supported, collects email, workspace, kit, core settings, domain and approval, uses read-only tools first (config, status, logs, verify, drift, generate preview), writes only after approval and the write gate, and reports evidence.
- Boundary: external management of a target-local connector with the authority of a user running StackKits on that server once write mode is on; narrower than SSH (StackKits tools, no shell). Native MCP exposes individual identity-bound commands, no combined rollout or update macro; `stackkit_apply` stays limited to the verified local artifact closure. Not the default first-install path.

## P6: Local StackKits MCP Adapter

`stackkit-mcp` ships beside the CLI and shares `stackkit-server`'s tool, resource and prompt registration.

```toml
[mcp_servers.stackkit]
command = "stackkit-mcp"
args = ["--mode", "docs,local,server,actions"]
```

Use when the MCP client runs on the CLI machine, no long-running `stackkit-server` exists, or MCP semantics are wanted without a network endpoint. stdio is local process authority; loopback HTTP stays loopback unless token and operator policy are configured; write tools are opt-in and local-only; started over SSH, the boundary is `P4`.

## Authority Summary

Website/Web-MCP discovery: none on the target. Installer and CLI: shell. SSH paths: remote shell. Local MCP: local process. Remote MCP: target-local StackKits authority behind explicit transport protection, token/scope checks and write gates.
