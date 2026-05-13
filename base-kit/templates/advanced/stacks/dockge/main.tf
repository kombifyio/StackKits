# Dockge Stack - OpenTofu Configuration (Advanced Mode)

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0"
    }
  }
}

variable "domain" {
  description = "Primary domain"
  type        = string
}

variable "stacks_dir" {
  description = "Directory for Docker Compose stacks"
  type        = string
  default     = "/opt/stacks"
}

variable "compute_tier" {
  description = "Compute tier for resource limits"
  type        = string
  default     = "standard"
}

variable "traefik_network" {
  description = "Traefik network name (output from traefik stack)"
  type        = string
  default     = "traefik"
}

locals {
  dockge_url = "dockge.${var.domain}"
  
  memory_limit = tm_try(
    global.dockge.memory_limits[var.compute_tier],
    512
  )
}

provider "docker" {
}

# Data source für Traefik Network
data "docker_network" "traefik" {
  name = var.traefik_network
}

# Volume
resource "docker_volume" "dockge_data" {
  name = tm_try(global.dockge.volumes.data, "dockge-data")

  labels {
    label = "managed-by"
    value = "kombistack"
  }
}

# Image
resource "docker_image" "dockge" {
  name         = "louislam/dockge:${tm_try(global.dockge.version, "1")}"
  keep_locally = true
}

# Container
resource "docker_container" "dockge" {
  name  = "dockge"
  image = docker_image.dockge.image_id

  restart = "unless-stopped"

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
  }

  volumes {
    volume_name    = docker_volume.dockge_data.name
    container_path = "/app/data"
  }

  volumes {
    host_path      = var.stacks_dir
    container_path = "/opt/stacks"
  }

  networks_advanced {
    name = data.docker_network.traefik.name
  }

  env = [
    "DOCKGE_STACKS_DIR=/opt/stacks",
  ]

  labels {
    label = "traefik.enable"
    value = "true"
  }

  labels {
    label = "traefik.http.routers.dockge.entrypoints"
    value = "websecure"
  }

  labels {
    label = "traefik.http.routers.dockge.rule"
    value = "Host(`${local.dockge_url}`)"
  }

  labels {
    label = "traefik.http.routers.dockge.tls.certresolver"
    value = "letsencrypt"
  }

  labels {
    label = "traefik.http.services.dockge.loadbalancer.server.port"
    value = tostring(tm_try(global.dockge.port, 5001))
  }

  labels {
    label = "managed-by"
    value = "kombistack"
  }

  labels {
    label = "stackkit"
    value = "base-kit"
  }

  memory = local.memory_limit
}

output "url" {
  description = "Dockge URL"
  value       = "https://${local.dockge_url}"
}
