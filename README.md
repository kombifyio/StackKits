# StackKits

**A professional homelab on a computer you already have, with one command.**
StackKits sets up a secured, backed-up homelab at home or in the cloud and keeps
the whole setup as one blueprint you can read, version and take anywhere. The
standard is open and works entirely on its own.

## Install

Run this on the computer that becomes your homelab, as a user who can use `sudo`:

<!-- public-path:begin install -->
```sh
curl -sSL https://install.stackkit.cc | sh
```
<!-- public-path:end install -->

The installer asks a few questions and every one has a default you accept with
Enter, except your email address, which becomes your owner login. It
installs the kit that fits the computer (Basement Kit at home, Cloud Kit on a
public server) and ends with your access details in one file. You get running
services and a sign-in for you.

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
kombify account is required for this private home mode. Public-domain or
kombify.me installations use the printed HTTPS Hub directly, with no client DNS
or CA setup; these modes require their own domain/Me registration and reach setup.

## Let kombify AI operate it

The homelab runs on its own. To have kombify AI look after it, connect the server
to your kombify account: start the Techstack setup for your own server at
<https://app.kombify.io/dashboard/tools/stack/connect>. On the server where you
installed StackKits, copy and run the setup’s complete command, then copy its
one-time pairing and paste it when the terminal asks **Pairing (hidden):**.
Press Enter. The pairing is not displayed, saved to a file or entered in shell
history. Never put it in a command argument. The setup shows when it expires
and **Get a new pairing** replaces it when needed.

The public command downloads the Techstack installer and runs it as root to
install the kombify Guard service:

<!-- public-path:begin connect -->
```sh
sudo stackkit connect --server https://techstack.kombify.io
```
<!-- public-path:end connect -->

For self-hosted Techstack, copy the complete command from that installation's
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
| Basement Kit | an existing Linux host at home | alpha at v0.57.11; no public-package pass recorded |
| Cloud Kit | an existing VPS with your own domain | alpha at v0.57.11; no public-package pass recorded |
| Modern Homelab | Home + Cloud, joined by an explicit federation bridge | alpha definition archive at v0.57.11; no public-package pass recorded |
<!-- kit-support:end -->

A published release is source and distribution evidence, not runtime
acceptance. Each release ships `release-evidence.json`; kit and use-case
runtime evidence is summarized on <https://stackkit.cc> and a status only
widens when a cited run exists. Modern Homelab ships in every release as a
definition archive for authoring and validation; it is not an install target.

## How it works

Every deployment is executed and tracked by OpenTofu; managed deployments are
orchestrated by Terramate (ADR-0045). Docker Compose workloads run inside that
tracked stack, and CUE files are the technical source of truth for schemas,
defaults and kit composition. Komodo and Coolify are explicit opt-in
integrations. See [the standalone decision](docs/ADR/ADR-0042-standalone-default-and-optional-platforms.md)
for the application-default contract.

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

## Source Of Truth

CUE files are the technical source of truth for schemas, defaults,
constraints, module contracts, and kit composition. Generated OpenTofu,
Compose, tfvars, state, and rollout snapshots are build output.

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
