# Traefik Stack - Advanced Mode
# Reverse Proxy und Ingress Controller

stack {
  name        = "traefik"
  description = "Traefik Reverse Proxy - Ingress Controller for Cloud Kit"
  id          = "cloud-kit-traefik"

  tags = [
    "core",
    "networking",
    "reverse-proxy",
    "traefik",
  ]

  # This stack must always be provisioned first
  after = []
}

globals "traefik" {
  # Service-spezifische Konfiguration
  version = global.versions.traefik
  
  # Ports
  ports = {
    http     = 80
    https    = 443
    dashboard = 8080
  }

  # Volumes
  volumes = {
    certs = "traefik-certs"
  }

  # Network
  network = "traefik"
}
