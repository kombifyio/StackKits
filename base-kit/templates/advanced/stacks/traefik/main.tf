# Traefik Stack - OpenTofu Configuration (Advanced Mode)
# Managed by Terramate for change detection and drift management

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0"
    }
  }
}

# Terramate-injizierte Variablen
variable "domain" {
  description = "Primary domain"
  type        = string
}

variable "acme_email" {
  description = "ACME/Let's Encrypt email"
  type        = string
}

variable "dashboard_insecure" {
  description = "Enable insecure dashboard"
  type        = bool
  default     = false
}

# Locals aus Terramate globals
locals {
  traefik_url = "traefik.${var.domain}"
  
  common_tags = tm_try(global.common_tags, {
    managed-by = "kombistack"
    stackkit   = "base-kit"
  })
}

provider "docker" {
}

# Network
resource "docker_network" "traefik" {
  name   = tm_try(global.traefik.network, "traefik")
  driver = "bridge"

  labels {
    label = "managed-by"
    value = "kombistack"
  }
}

# Certificate Volume
resource "docker_volume" "certs" {
  name = tm_try(global.traefik.volumes.certs, "traefik-certs")

  labels {
    label = "managed-by"
    value = "kombistack"
  }
}

# Traefik Image
resource "docker_image" "traefik" {
  name         = "traefik:${tm_try(global.traefik.version, "v3.0")}"
  keep_locally = true
}

# Traefik Container
resource "docker_container" "traefik" {
  name  = "traefik"
  image = docker_image.traefik.image_id

  restart = "unless-stopped"

  ports {
    internal = tm_try(global.traefik.ports.http, 80)
    external = 80
  }

  ports {
    internal = tm_try(global.traefik.ports.https, 443)
    external = 443
  }

  ports {
    internal = tm_try(global.traefik.ports.dashboard, 8080)
    external = 8080
  }

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
    read_only      = true
  }

  volumes {
    volume_name    = docker_volume.certs.name
    container_path = "/certs"
  }

  networks_advanced {
    name = docker_network.traefik.name
  }

  command = [
    "--api.dashboard=true",
    "--api.insecure=${var.dashboard_insecure}",
    "--providers.docker=true",
    "--providers.docker.exposedbydefault=false",
    "--providers.docker.network=traefik",
    "--entrypoints.web.address=:80",
    "--entrypoints.web.http.redirections.entrypoint.to=websecure",
    "--entrypoints.web.http.redirections.entrypoint.scheme=https",
    "--entrypoints.websecure.address=:443",
    "--certificatesresolvers.letsencrypt.acme.email=${var.acme_email}",
    "--certificatesresolvers.letsencrypt.acme.storage=/certs/acme.json",
    "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web",
    "--ping=true",
    "--log.level=INFO",
  ]

  dynamic "labels" {
    for_each = local.common_tags
    content {
      label = labels.key
      value = labels.value
    }
  }

  labels {
    label = "traefik.enable"
    value = "true"
  }

  labels {
    label = "traefik.http.routers.traefik.entrypoints"
    value = "websecure"
  }

  labels {
    label = "traefik.http.routers.traefik.rule"
    value = "Host(`${local.traefik_url}`)"
  }

  labels {
    label = "traefik.http.routers.traefik.service"
    value = "api@internal"
  }

  labels {
    label = "traefik.http.routers.traefik.tls.certresolver"
    value = "letsencrypt"
  }

  healthcheck {
    test         = ["CMD", "traefik", "healthcheck", "--ping"]
    interval     = "10s"
    timeout      = "5s"
    retries      = 3
    start_period = "5s"
  }
}

# Outputs
output "network_name" {
  description = "Traefik network name for dependent services"
  value       = docker_network.traefik.name
}

output "dashboard_url" {
  description = "Traefik dashboard URL"
  value       = "https://${local.traefik_url}"
}
