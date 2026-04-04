# Lifecycle Templates

> Teil der **IaC-First Architektur** von kombify-TechStack

## Zweck

Dieses Verzeichnis enthält OpenTofu-Templates für das Service-Deployment und Lifecycle-Management. Hier werden die eigentlichen Workloads (Container, VMs) definiert und verwaltet.

## Was gehört hierher?

- **Service-Deployment**: Docker Compose/Swarm Stacks
- **Container-Management**: Container-Lifecycle via Docker Provider
- **VM-Management**: QEMU/KVM VMs via libvirt Provider
- **Secrets-Management**: Secret-Deployment und Rotation
- **Config-Management**: ConfigMaps, Environment-Variables
- **Health-Checks**: Service-Health-Monitoring-Konfiguration

## IaC-First Prinzip

Der kombify-TechStack-Agent führt **keine Shell-Commands direkt** aus. Stattdessen:

1. Services werden deklarativ als OpenTofu-Ressourcen definiert
2. Agent führt `tofu apply` für Deployments aus
3. Updates erfolgen durch `tofu plan` → `tofu apply`
4. Rollbacks sind durch State-Management möglich

## Erwartete Template-Files

```
lifecycle/
├── main.tf.tmpl           # Haupt-Modul für Lifecycle
├── variables.tf.tmpl      # Input-Variablen
├── outputs.tf.tmpl        # Output-Werte (Service-URLs, etc.)
├── providers.tf.tmpl      # Docker/libvirt Provider
├── containers.tf.tmpl     # Container-Definitionen
├── volumes.tf.tmpl        # Persistent Volumes
├── secrets.tf.tmpl        # Secret-Management
├── configs.tf.tmpl        # Config-Files und Envs
├── services.tf.tmpl       # Docker Swarm Services
└── vms.tf.tmpl            # VM-Definitionen (optional)
```

## Template-Variablen

Templates erhalten Variablen aus der `kombination.yaml`:

- `${services[*].name}` - Service-Namen
- `${services[*].image}` - Container-Images
- `${services[*].replicas}` - Replica-Count
- `${services[*].env}` - Environment-Variablen
- `${services[*].volumes}` - Volume-Mounts
- `${secrets[*]}` - Secrets-Referenzen

## Beispiel

```hcl
# containers.tf.tmpl
resource "docker_container" "traefik" {
  name  = "traefik"
  image = docker_image.traefik.image_id
  
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
  
  labels {
    label = "managed-by"
    value = "kombify-TechStack"
  }
}
```

## Terramate-Integration

Für Multi-Node-Deployments wird Terramate verwendet:

```hcl
# terramate.tm.hcl
stack {
  name        = "service-deployment"
  description = "Deploy services to cluster"
  
  after = ["bootstrap", "network"]
}
```

## Abhängigkeiten

- Erfordert abgeschlossene `bootstrap/`-Phase
- Erfordert abgeschlossene `network/`-Phase
- Koordiniert mit anderen Nodes via Core
