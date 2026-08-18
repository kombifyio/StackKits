# Lifecycle Templates

Shared OpenTofu templates for workload deployment and lifecycle management,
kept in the StackKits base library so kits do not each carry their own copy.

## Scope

- Service deployment (Compose stacks)
- Container lifecycle through the Docker provider
- Persistent volumes and config material
- Secret deployment and rotation
- Health-check configuration
- Drift observation (`_drift.tf.tmpl`)

## Declarative execution

Workloads are declared as OpenTofu resources rather than applied by ad-hoc
shell commands: `tofu plan` shows the change, `tofu apply` performs it, and
state management makes rollback possible.

## Files

| File | Purpose |
| --- | --- |
| `_drift.tf.tmpl` | Drift observation for deployed workloads |

## Template variables

Templates are rendered from the resolved plan, which supplies the service
identities, images, replica counts, environment, volume mounts, and secret
references a kit declares. Templates never read user input directly.

## Example

```hcl
resource "docker_container" "router" {
  name  = "router"
  image = docker_image.router.image_id

  restart = "unless-stopped"

  ports {
    internal = 80
    external = 80
  }

  ports {
    internal = 443
    external = 443
  }

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
    read_only      = true
  }
}
```

## Multi-node deployments

Terramate orders the stacks for multi-node deployments:

```hcl
stack {
  name        = "service-deployment"
  description = "Deploy services to the cluster"

  after = ["bootstrap", "network"]
}
```

## Dependencies

Lifecycle templates assume the `bootstrap/` and `network/` phases completed on
the target host.
