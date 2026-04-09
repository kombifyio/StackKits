# StackKit CLI Reference

Complete reference for the `stackkit` command-line interface.

## Installation

One-line install (Linux/macOS — downloads the latest release and OpenTofu):

```bash
curl -sSL https://raw.githubusercontent.com/kombifyio/stackKits/main/install.sh | bash
```

Build from source:

```bash
git clone https://github.com/kombifyio/stackKits.git && cd stackKits && make install
```

Go install (requires Go 1.25+):

```bash
go install github.com/kombifyio/stackkits/cmd/stackkit@latest
```

Verify:

```bash
stackkit version
```

---

## Quick Start

```bash
curl -sSL https://raw.githubusercontent.com/kombifyio/stackKits/main/install.sh | bash
stackkit init base-kit && stackkit apply --auto-approve
```

The install script installs the `stackkit` binary. When `apply` runs, it checks for Docker and OpenTofu and offers to install them if missing.

---

## Global Flags

Every subcommand accepts these flags:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--verbose` | `-v` | `false` | Enable verbose output |
| `--quiet` | `-q` | `false` | Suppress non-essential output |
| `--chdir` | `-C` | `.` | Change to directory before running |
| `--spec` | `-s` | `stack-spec.yaml` | Path to stack specification file |
| `--context` | | auto-detect | Node context override (`local`, `cloud`, `pi`) |

---

## Commands

### `stackkit init [stackkit]`

Create a new `stack-spec.yaml` in the current directory. When run without arguments an interactive wizard guides you through StackKit selection, variant, domain, and email configuration.

```bash
stackkit init                              # interactive wizard
stackkit init base-kit                     # skip kit selection
stackkit init base-kit --variant minimal   # specify variant
stackkit init base-kit --non-interactive   # fail if input required
stackkit init ./path/to/custom-kit         # use local kit path
```

**Flags:**

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--variant` | | auto | Service variant |
| `--mode` | | `simple` | Deployment mode (`simple`, `advanced`) |
| `-o, --output` | `-o` | `deploy` | Output directory for generated files |
| `-f, --force` | `-f` | `false` | Overwrite existing files |
| `--non-interactive` | | `false` | Fail instead of prompting for input |

**What it does:**

1. Discovers available StackKits in current and parent directories
2. Prompts for variant, mode, domain, and admin email (unless flags given)
3. Writes `stack-spec.yaml` with the chosen configuration
4. Prints next steps (`prepare` -> `apply`)

---

### `stackkit prepare` (alias: `prep`)

Prepare a system for StackKit deployment. Checks (and installs if missing) Docker and OpenTofu, validates the spec against CUE schemas, and reports system resources.

```bash
stackkit prepare                                  # local system
stackkit prepare --spec ./stack-spec.yaml         # validate specific spec
stackkit prepare --host 192.168.1.100 --user root # remote via SSH
stackkit prepare --dry-run                        # preview only
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | `localhost` | Target host IP or hostname |
| `--user` | | SSH username (remote only) |
| `--key` | | SSH private key path (remote only) |
| `--dry-run` | `false` | Show what would be done without changes |
| `--skip-docker` | `false` | Skip Docker check/install |
| `--skip-tofu` | `false` | Skip OpenTofu check/install |
| `--auto-fix` | `true` | Auto-correct fixable issues |

**Local mode** checks and installs Docker (via `get.docker.com`) and OpenTofu, validates the spec file, and reports CPU/memory stats.

**Remote mode** connects via SSH, gathers OS info, installs Docker and OpenTofu if missing (supports Ubuntu/Debian and RHEL/Rocky/Fedora), and checks that ports 80/443 are free.

---

### `stackkit generate` (alias: `gen`)

Generate OpenTofu files from the stack spec and StackKit templates. The output is placed in the `deploy/` directory by default. These files are generated artifacts — never edit them directly.

```bash
stackkit generate                # generate into ./deploy
stackkit generate -o ./out       # custom output directory
stackkit generate --force        # overwrite existing output
```

**Flags:**

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | `deploy` | Output directory |
| `--force` | `-f` | `false` | Overwrite existing output directory |

**What it does:**

1. Loads `stack-spec.yaml`
2. Locates the StackKit directory and its templates
3. Validates CUE schemas (warnings only — does not block)
4. Renders Go templates into the output directory
5. Generates `main.tf` and `terraform.tfvars.json`
6. Prints file count and next steps

**Generated files:**

| File | Purpose |
|------|---------|
| `main.tf` | OpenTofu resource definitions |
| `terraform.tfvars.json` | Variable values derived from spec |
| Template outputs | Mode-specific files from `templates/<mode>/` |

---

### `stackkit plan`

Preview what `apply` would change without modifying anything. Runs `tofu plan` inside the deploy directory.

```bash
stackkit plan                     # preview changes
stackkit plan -o plan.tfplan      # save plan to file
stackkit plan --destroy           # preview a destroy
```

**Flags:**

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--out` | `-o` | | Save plan to file (default: `deploy/plan.tfplan`) |
| `--destroy` | | `false` | Create a destroy plan |

**What it does:**

1. Loads spec and locates `deploy/` directory (fails if missing — run `generate` first)
2. Initializes OpenTofu if `.terraform/` does not exist
3. Runs `tofu plan`
4. Prints resource summary: how many to add, change, destroy
5. If saved to file, prints the `stackkit apply <plan-file>` command to run next

---

### `stackkit apply [plan-file]`

Deploy the infrastructure. Runs `tofu apply` inside the deploy directory. If the deploy directory is missing or contains no `.tf` files, `generate` runs automatically first.

```bash
stackkit apply                    # deploy with confirmation prompt
stackkit apply --auto-approve     # deploy without confirmation
stackkit apply plan.tfplan        # apply a previously saved plan
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--auto-approve` | `false` | Skip the interactive "yes" confirmation |

**What it does:**

1. Loads spec
2. Auto-generates if `deploy/` is missing or has no `.tf` files
3. Initializes OpenTofu if `.terraform/` does not exist
4. Runs `tofu apply`
5. Saves deployment state to `.stackkit/state.yaml`
6. Prints deployment outputs and total duration

---

### `stackkit remove`

Tear down all infrastructure managed by the deployment. Runs `tofu destroy` inside the deploy directory. Requires typing `yes` to confirm unless `--auto-approve` is passed.

```bash
stackkit remove                          # remove with confirmation
stackkit remove --auto-approve           # no confirmation
stackkit remove --auto-approve --force   # ignore errors, keep going
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--auto-approve` | `false` | Skip the "type yes" confirmation |
| `--force` | `false` | Continue even if errors occur |

**What it does:**

1. Loads spec (continues with "unknown" if spec can't be loaded)
2. Checks for `deploy/` directory (fails unless `--force`)
3. Prompts for `yes` confirmation (unless `--auto-approve`)
4. Runs `tofu destroy`
5. Updates deployment state to `destroyed`

---

### `stackkit status`

Show the current deployment status: which services are running, their health, and container IDs.

```bash
stackkit status             # table output
stackkit status --json      # JSON output
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as JSON |

**Table output** shows service name, status, health, and container ID. **JSON output** includes stackkit name, variant, mode, last applied time, overall status, and a services array.

---

### `stackkit validate [file]`

Validate configuration files: the stack spec against CUE schemas, StackKit CUE definitions, and (if present) the generated OpenTofu files.

```bash
stackkit validate                   # validate current spec
stackkit validate my-spec.yaml      # validate a specific file
stackkit validate --all             # also validate all .cue files
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--all` | `false` | Also scan and validate all `.cue` files under the working directory |

**What it does:**

1. Validates spec against CUE schema — reports errors and warnings
2. With `--all`: finds all `.cue` files and validates each
3. If `deploy/` exists: checks for `.tf` files and runs `tofu validate`
4. Returns non-zero exit code if any validation fails

---

### `stackkit addon`

Manage composable add-ons (monitoring, backup, VPN, media server, etc.). Add-ons are declared in `stack-spec.yaml` and resolved at generate time.

#### `stackkit addon list`

List all available add-ons with their layer, activation status, and description.

```bash
stackkit addon list
```

Example output:

```
NAME            LAYER   STATUS   DESCRIPTION
monitoring      L2      active   Prometheus and Grafana monitoring
backup          L2               Automated backup solutions
vpn-overlay     L3               VPN tunnel overlay network
media-server    L3               Media server stack (Jellyfin, *arr)
```

#### `stackkit addon add <name>`

Add an add-on to `stack-spec.yaml`. After adding, run `stackkit generate --force` to regenerate deployment files.

```bash
stackkit addon add monitoring
stackkit addon add vpn-overlay
```

#### `stackkit addon remove <name>`

Remove an add-on from `stack-spec.yaml`. After removing, run `stackkit generate --force` to regenerate.

```bash
stackkit addon remove monitoring
```

---

### `stackkit version`

Print version, git commit, build date, Go version, and OS/architecture.

```bash
stackkit version
```

Example output:

```
stackkit version v0.3.0
  Git commit: a1b2c3d
  Build date: 2026-03-01T12:00:00Z
  Go version: go1.25
  OS/Arch:    linux/amd64
```

---

### `stackkit completion <shell>`

Generate shell completion scripts for tab-completion in your terminal.

```bash
# Bash
stackkit completion bash > /etc/bash_completion.d/stackkit

# Zsh
stackkit completion zsh > "${fpath[1]}/_stackkit"

# Fish
stackkit completion fish > ~/.config/fish/completions/stackkit.fish

# PowerShell
stackkit completion powershell > stackkit.ps1
```

---

## Typical Workflows

### First deployment on a fresh server

```bash
curl -sSL https://raw.githubusercontent.com/kombifyio/stackKits/main/install.sh | bash
mkdir my-homelab && cd my-homelab
stackkit init base-kit
# edit stack-spec.yaml (set domain, email, etc.)
stackkit prepare
stackkit apply --auto-approve
stackkit status
```

### Changing configuration

```bash
# edit stack-spec.yaml
stackkit generate --force
stackkit plan
stackkit apply
```

### Adding an add-on

```bash
stackkit addon list
stackkit addon add monitoring
stackkit generate --force
stackkit apply
```

### Tearing down

```bash
stackkit remove --auto-approve
```

---

## Files Created by StackKit CLI

| Path | Created by | Purpose |
|------|-----------|---------|
| `stack-spec.yaml` | `init` | Deployment specification |
| `deploy/` | `generate` | Generated OpenTofu files (never edit) |
| `deploy/main.tf` | `generate` | OpenTofu resource definitions |
| `deploy/terraform.tfvars.json` | `generate` | Variable values from spec |
| `deploy/.terraform/` | `apply` / `plan` | OpenTofu state and provider cache |
| `.stackkit/state.yaml` | `apply` / `destroy` | Deployment state tracking |
