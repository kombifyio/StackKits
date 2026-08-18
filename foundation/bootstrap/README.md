# Bootstrap Templates

Shared OpenTofu templates for host preparation, kept in the StackKits base
library so kits do not each carry their own copy.

## Scope

These templates cover the preparation a host needs before workloads are
deployed onto it:

- OS baseline configuration
- Package installation (container engine, systemd units)
- Service accounts and SSH key deployment
- Mount points and directory layout
- Baseline firewall and SSH hardening

## Declarative execution

Host preparation is expressed as OpenTofu configuration rather than ad-hoc
shell commands: the configuration is rendered from these templates, then
applied with `tofu init`, `tofu plan`, `tofu apply`. Every change is
declarative, idempotent, and reviewable before it runs.

## Files

| File | Purpose |
| --- | --- |
| `_bootstrap.tf.tmpl` | Host preparation module |
| `_services.tf.tmpl` | Service unit preparation |
| `_variables.tf.tmpl` | Input variables for the rendered module |

## Template variables

Templates are rendered with values resolved from the canonical StackSpec and
the kit's CUE contracts, for example the node hostname, the node roles, and
the network and storage settings the kit declares. Templates never read user
input directly; the resolved plan is the only input authority.

## Example

```hcl
resource "system_packages_apt" "docker" {
  count = var.install_docker ? 1 : 0

  package {
    name = "docker-ce"
  }
  package {
    name = "docker-ce-cli"
  }
}
```
