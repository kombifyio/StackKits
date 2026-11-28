# StackKits

**StackKits turns a spare computer, a mini PC or a Proxmox VE guest into a
running homelab with Photos, Files and Vault — on your own hardware, with no
kombify account required.**

One command installs the CLI and the kit. You get running services, an owner
passkey you create yourself, and your access details in one file. The whole
homelab stays one readable blueprint you can keep, version and take anywhere.

## Install

On a spare Linux machine, mini PC or appliance (as a user who can use `sudo`):

<!-- public-path:begin install -->
```sh
curl -sSL https://install.stackkit.cc | sh
```
<!-- public-path:end install -->

On Proxmox VE, create an Ubuntu 24.04 or 26.04 guest VM first and run the same
command inside it. StackKits never installs on the Proxmox host itself. The
installer asks for your email address, shows the use-case selection and starts
the services.

![Photos running in the homelab after setup](https://docs.kombify.io/images/guides/photos/immich/web-01-photo.webp)

## What you get in about ten minutes

The installer picks the address zone that fits the host it detects, so there is
no domain to configure before you start. All three zones are equal first-class
modes; choose one explicitly with `stackkit init --address-zone`
(`custom-domain`, `kombify-me` or `local-only`):

| Zone | Addresses | What you get |
| --- | --- | --- |
| Custom domain | `*.example.com` | Your own domain. Kit-Traefik requests certificates directly (ACME HTTP-01 or DNS-01) and serves sign-in at `id.example.com` and `auth.example.com`, with no `kombify.me` dependency. |
| kombify.me | `*.kombify.me` | The default on a public VPS or cloud host: HTTPS names that need no own domain, no DNS change and no certificate on your devices. On a home server, set `DOMAIN=kombify.me` and turn on a reach path with `stackkit reach`. |
| Local only | `*.lab.home` | The default on a home server, and an explicit choice anywhere with `DOMAIN=lab.home`. A private home zone with a local owner CA and resolver for devices on your own network; it needs no internet service and no kombify account. |

Three use cases are checked by default and need nothing from you:

| Use case | What you get |
| --- | --- |
| Photos | Your family photos, backed up and reachable from your phone |
| Files | Your documents, shared across the household |
| Vault | Your passwords, behind a passkey sign-in with no default passwords |

It installs the recommended release: the newest one on which every declared
test lane passed. While no release is recommended it installs the newest release
and says so; `stackkit compat` and the
[Compatibility page](https://stackkit.cc/compatibility) show which it is.

On your phone or laptop, join the same home network and open the **Second device**
address printed by the installer (`http://<server-LAN-IP>/device/setup`). This page
opens without DNS or certificate setup. Choose your device type and follow its
two steps: configure homelab DNS and approve the public homelab CA after comparing
its SHA-256 fingerprint with the install terminal. On Linux, also compare the
whole downloaded adapter file’s SHA-256 with the installer’s Linux adapter digest
before running it with sudo. It has instructions for iPhone,
Android, Windows, macOS and Linux. Then open the HTTPS Hub, create your Pocket ID
owner passkey on first use and sign in. The address is also `secondDevice.setupUrl`
in `.stackkit/access.json`, and the Hub links back to setup. No router change or
kombify account is required for this private home mode. Custom-domain and
kombify.me installations use the printed HTTPS Hub directly, with no client DNS
or CA setup; they need the domain or the kombify.me registration and a reach
path.

## The six commands

If you want to drive the lifecycle yourself instead of taking the guided path,
these six commands cover standalone setup, day-2 operation and the optional
connection to kombify:

| Command | Purpose |
| --- | --- |
| `stackkit init` | Initialize a new deployment from a StackKit |
| `stackkit apply` | Apply infrastructure changes |
| `stackkit status` | Show deployment status |
| `stackkit verify` | Run post-deployment verification checks |
| `stackkit backup` | Manage backups (Kopia engine) |
| `stackkit connect` | Bind the server to your kombify account for public `*.kombify.me` names |

For a fresh Linux target, prepare Docker and the CLI as root or with passwordless
sudo and Docker socket access before following the steps by hand:

<!-- public-path:begin linux-target -->
```sh
curl -sSL https://install.stackkit.cc | STACKKIT_CLI_ONLY=1 STACKKIT_PREPARE_HOST=1 sh
```
<!-- public-path:end linux-target -->

## Let kombify AI operate it

The homelab runs on its own. To have kombify AI look after it, connect the server
to your kombify account: start the Homelab setup for your own server at
<https://app.kombify.io/dashboard/tools/stack/connect>. On the server where you
installed StackKits, copy and run the setup’s complete command, then copy its
one-time pairing and paste it when the terminal asks **Pairing (hidden):**.
Press Enter. The pairing is not displayed, saved to a file or entered in shell
history. Never put it in a command argument. The setup shows when it expires
and **Get a new pairing** replaces it when needed.

The public command downloads the Homelab installer and runs it as root to
install the kombify Guard service:

<!-- public-path:begin connect -->
```sh
sudo stackkit connect --server https://homelab.kombify.io
```
<!-- public-path:end connect -->

For self-hosted Homelab, copy the complete command from that installation's
own-server setup; it includes its actual configured `--server` origin.
`KOMBI_SERVER` also overrides the public default (plain sudo can drop environment
variables, so prefer `--server`). If an origin is wrong or unreachable, the command
names it and points back to this setup. A private LAN origin needs the setup's
pinned install command, which also asks for the pairing without echo.
For automation, keep a root-owned pairing file with mode `0600` and pass
`--token-file /root/kombify-pairing` instead of using the terminal prompt.

A free account is enough: the server then shows its state in kombify Cloud and
kombify AI supports it. Changes that kombify AI makes for you come with the paid
plans.

## The step-by-step path

Prefer to see each step? Install only the CLI, then run the same steps by hand:

<!-- public-path:begin step-by-step -->
```sh
curl -sSL https://install.stackkit.cc | STACKKIT_CLI_ONLY=1 sh
mkdir my-homelab && cd my-homelab
stackkit init --catalog-defaults --owner-source=local --owner-email you@example.com
stackkit validate
stackkit generate
stackkit apply
stackkit logs latest --json
cat .stackkit/access.json
```
<!-- public-path:end step-by-step -->

## Kits

Select the kit with `stackkit init`:

<!-- kit-support:begin -->
| Kit | Target | Status |
| --- | --- | --- |
| Basement Kit | an existing Linux host at home | alpha at v0.61.19; no public-package pass recorded |
| Cloud Kit | an existing VPS with your own domain | alpha at v0.61.19; no public-package pass recorded |
| Modern Homelab | Home + Cloud, joined by an explicit federation bridge | alpha definition archive at v0.61.19; no public-package pass recorded |
<!-- kit-support:end -->

A published release is source and distribution evidence, not runtime
acceptance. Each release ships `release-evidence.json`; kit and use-case
runtime evidence is summarized on <https://stackkit.cc> and a status only
widens when a cited run exists. Modern Homelab ships in every release as a
definition archive for authoring and validation; it is not an install target.

## How it works

CUE files are the technical source of truth for schemas, defaults and kit
composition. Every curated workload is a typed application module
([`#AppModuleV3`](foundation/app_module_v3.cue)) that the CLI renders into
Docker Compose and executes as OpenTofu Stage 1 per-stack roots
([ADR-0054](docs/ADR/ADR-0054-single-core-engine-and-coolify-extensibility-boundary.md),
[ADR-0055](docs/ADR/ADR-0055-declarative-app-module-v3-and-three-zone-lifecycle.md)).
Standard mode runs those roots one after another; Homelab-managed advanced mode
orchestrates the same roots with Terramate. Status and diagnosis always pair the
OpenTofu plan with live container, image-digest, health and route observation.

A rollout has two tiers:

- **Tier 1, always:** core rollout plus the required safety setup
  (digest-pinned images, private networks, generated secrets, no default
  passwords, closed open registration, Pocket ID with the access gate,
  private-by-default routing). A failure here fails the rollout.
- **Tier 2, Bootstrapped only (the default):** per-app personalization such as
  the owner account and single sign-on wiring. A Tier-2 failure never fails
  the rollout: the app stays private behind the access gate and
  `stackkit retry-appsetup` runs the step again. Choose `--setup-style clean`
  (or `--clean-rollout`) on `stackkit init` or `stackkit apply` for a Clean
  Rollout, which runs Tier 1 only.

On the host, `stackkit diagnose` writes a redacted, secret-free health snapshot
and `stackkit repair <action>` runs bounded, effect-classified repairs behind a
repair policy and circuit breaker; a watchdog timer refreshes the snapshot
([ADR-0051](docs/ADR/ADR-0051-server-mcp-host-execution-authority.md)).

Coolify `4.7.5+` is an optional Layer-3 extension for its template marketplace
and your own custom or Git-built apps. It never manages the curated apps and
holds no custody: Kit-Traefik and the access gate keep sole ownership of host
ports 80/443, certificates and sign-in policy in every address zone, and
Coolify's own proxy runs HTTP-only behind them. See
[the standalone decision](docs/ADR/ADR-0042-standalone-default-and-optional-platforms.md)
for the application-default contract and the [ADR index](docs/ADR/README.md)
for all public decisions.

## Website, docs and community

- Product site and installers: <https://stackkit.cc>
- Documentation: <https://docs.kombify.io/stackkits>
- The StackKit Open Spec (architecture snapshot, lifecycle verbs, placement
  taxonomy, verification evidence): [docs/OPEN-SPEC.md](docs/OPEN-SPEC.md)
- Support: see [SUPPORT.md](SUPPORT.md). Questions and ideas go to GitHub
  Discussions on this repository, bugs to the bug form, vulnerabilities to
  GitHub private vulnerability reporting or e-mail (see `SECURITY.md`). This
  repository is a release mirror; pull requests are reviewed here and imported
  upstream by maintainers (see `CONTRIBUTING.md`)
- How the project is built, including the AI-assisted development
  provenance: [docs/BUILT-WITH.md](docs/BUILT-WITH.md)

## Works with agents

StackKits is agent-native without depending on any AI service. The CLI embeds
the `stackkit-mcp` connector and the prompt Markdown; `llms.txt`, OpenAPI and
the JSON schemas are published on <https://stackkit.cc> for every release:

```sh
stackkit agent mcp-config --client codex   # or claude / generic
stackkit agent prompt --list
```

## Documentation

- [CLI reference](docs/CLI.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Concepts](docs/CONCEPTS.md)
- [Stack spec reference](docs/stack-spec-reference.md)
- [Architecture decisions](docs/ADR/README.md)
- [Moving a legacy PaaS install to OpenTofu](docs/runbooks/paas-to-opentofu-restore-cutover.md)

## Source Of Truth

CUE files are the technical source of truth for schemas, defaults,
constraints, module contracts (`#ModuleContract`, `#AppModuleV3`), and kit
composition. Generated OpenTofu, Compose, tfvars, state, and rollout snapshots
are build output.

The [Architecture v2 contract proof](architecture/v2/fixtures/contract-fixtures.manifest.json)
reproducibly binds a two-node Basement topology, named runtime daemons,
provider/consumer interfaces, runtime networks, and an approved direct-socket
exception through the compiler and renderer contract. It uses a separate
contract-only catalog and is explicitly ineligible for product graduation.
Validate the committed hashes and catalog boundary with
`node scripts/release/validate-architecture-contract-fixture.mjs --repo-root .`.

## License

Apache-2.0 OR GPL-3.0-or-later, at your option. See [LICENSING.md](LICENSING.md)
and the complete [Apache-2.0](LICENSE-APACHE) and
[GPL-3.0](LICENSE-GPL-3.0-or-later) texts.
The [WebMCP package](webmcp/LICENSE) is Apache-2.0-only.
