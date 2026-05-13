# Dockge - Docker Compose Manager Module
# Deployed in minimal variant only

variable "enabled" {
  type    = bool
  default = false
}

variable "network_name" {
  type = string
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
  type    = number
  default = 5001
}

variable "stacks_dir" {
  type    = string
  default = "/opt/stacks"
}

variable "memory_limit" {
  type    = number
  default = 1024
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

locals {
  use_proxy        = var.access_mode == "proxy"
  proxy_entrypoint = local.use_proxy && var.enable_https ? "websecure" : "web"
  dockge_host      = "dockge.${var.domain}"
}

resource "docker_volume" "data" {
  count = var.enabled ? 1 : 0
  name  = "dockge-data"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_image" "dockge" {
  count        = var.enabled ? 1 : 0
  name         = "louislam/dockge:1"
  keep_locally = true
}

resource "docker_container" "dockge" {
  count = var.enabled ? 1 : 0

  name  = "dockge"
  image = docker_image.dockge[0].image_id

  restart = "unless-stopped"

  ports {
    internal = 5001
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

  volumes {
    host_path      = var.stacks_dir
    container_path = "/opt/stacks"
  }

  networks_advanced {
    name = var.network_name
  }

  env = ["DOCKGE_STACKS_DIR=/opt/stacks"]

  dynamic "labels" {
    for_each = merge(
      var.common_labels,
      local.use_proxy ? merge(
        {
          "traefik.enable"                                        = "true"
          "traefik.http.routers.dockge.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.dockge.rule"                      = "Host(`${local.dockge_host}`)"
          "traefik.http.services.dockge.loadbalancer.server.port" = "5001"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.dockge.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.dockge.tls" = "true"
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
