# Dockge Stack - Advanced Mode
# Docker Compose Management UI

stack {
  name        = "dockge"
  description = "Dockge - Docker Compose Stack Manager"
  id          = "base-kit-dockge"

  tags = [
    "core",
    "management",
    "docker",
    "ui",
  ]

  # Abhängig von Traefik für Routing
  after = ["stacks/traefik"]
}

globals "dockge" {
  version = global.versions.dockge
  
  # Service Port
  port = 5001

  # Volumes
  volumes = {
    data = "dockge-data"
  }

  # Memory Limits pro Compute Tier
  memory_limits = {
    high     = 1024
    standard = 512
    low      = 256
  }
}
