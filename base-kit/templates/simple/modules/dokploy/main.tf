# Dokploy - PaaS Platform Module
# Deployed in default and beszel variants

variable "enabled" {
  description = "Whether to deploy Dokploy"
  type        = bool
  default     = true
}

variable "network_name" {
  description = "Docker network to join"
  type        = string
}

variable "access_mode" {
  type    = string
  default = "ports"
}

variable "enable_https" {
  type    = bool
  default = false
}

variable "enable_letsencrypt" {
  type    = bool
  default = false
}

variable "domain" {
  type    = string
  default = ""
}

variable "port" {
  description = "Host port for Dokploy"
  type        = number
  default     = 4000
}

variable "memory_limit" {
  description = "Memory limit in MB"
  type        = number
  default     = 1024
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

variable "depends_on_containers" {
  description = "Container IDs to depend on"
  type        = list(string)
  default     = []
}

locals {
  use_proxy        = var.access_mode == "proxy"
  proxy_entrypoint = local.use_proxy && var.enable_https ? "websecure" : "web"
  deploy_host      = "deploy.${var.domain}"
}

resource "docker_volume" "data" {
  count = var.enabled ? 1 : 0
  name  = "dokploy-data"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_image" "dokploy" {
  count        = var.enabled ? 1 : 0
  name         = "dokploy/dokploy:latest"
  keep_locally = true
}

resource "docker_container" "dokploy" {
  count = var.enabled ? 1 : 0

  name  = "dokploy"
  image = docker_image.dokploy[0].image_id

  restart = "unless-stopped"

  ports {
    internal = 3000
    external = var.port
  }

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
  }

  volumes {
    volume_name    = docker_volume.data[0].name
    container_path = "/app/data"
  }

  networks_advanced {
    name = var.network_name
  }

  env = ["NODE_ENV=production"]

  dynamic "labels" {
    for_each = merge(
      var.common_labels,
      local.use_proxy ? merge(
        {
          "traefik.enable"                                         = "true"
          "traefik.http.routers.dokploy.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.dokploy.rule"                      = "Host(`${local.deploy_host}`)"
          "traefik.http.services.dokploy.loadbalancer.server.port" = "3000"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.dokploy.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.dokploy.tls" = "true"
        }) : {})
      ) : {
        "traefik.enable" = "false"
      }
    )
    content {
      label = labels.key
      value = labels.value
    }
  }

  memory = var.memory_limit
}
