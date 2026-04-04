# Bootstrap Templates

> Teil der **IaC-First Architektur** von kombify-TechStack

## Zweck

Dieses Verzeichnis enthält OpenTofu-Templates für die initiale Node-Vorbereitung (Bootstrap-Phase). Diese Templates werden ausgeführt, **bevor** ein Node dem Cluster beitritt.

## Was gehört hierher?

- **OS-Preparation**: Basis-Konfiguration des Betriebssystems
- **Package-Installation**: Installation von Grundpaketen (Docker, systemd-Konfiguration, etc.)
- **User-Setup**: Erstellung von Service-Accounts und SSH-Key-Deployment
- **Storage-Preparation**: Vorbereitung von Mountpoints und Verzeichnisstrukturen
- **Security-Hardening**: Firewall-Basisregeln, SSH-Härtung

## IaC-First Prinzip

Der kombify-TechStack-Agent führt **keine Shell-Commands direkt** aus. Stattdessen:

1. Core generiert OpenTofu-Konfigurationen basierend auf diesen Templates
2. Agent führt `tofu init`, `tofu plan`, `tofu apply` aus
3. Alle Änderungen sind deklarativ, idempotent und nachvollziehbar

## Erwartete Template-Files

```
bootstrap/
├── main.tf.tmpl          # Haupt-Modul für Bootstrap
├── variables.tf.tmpl     # Input-Variablen
├── outputs.tf.tmpl       # Output-Werte für nachfolgende Phasen
├── providers.tf.tmpl     # Provider-Konfiguration (local, null, etc.)
├── packages.tf.tmpl      # Package-Installation via system provider
├── users.tf.tmpl         # User- und Group-Management
├── ssh.tf.tmpl           # SSH-Konfiguration und Key-Deployment
└── storage.tf.tmpl       # Storage-Vorbereitung
```

## Template-Variablen

Templates erhalten Variablen aus der `kombination.yaml`:

- `${node.hostname}` - Hostname des Nodes
- `${node.role}` - Rolle (manager/worker)
- `${stack.network.*}` - Netzwerk-Konfiguration
- `${stack.security.*}` - Security-Einstellungen

## Beispiel

```hcl
# packages.tf.tmpl
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
