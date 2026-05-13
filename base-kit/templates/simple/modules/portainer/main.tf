# Portainer - Container Management Module
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
  default = 9000
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
  portainer_host   = "portainer.${var.domain}"
}

resource "docker_volume" "data" {
  count = var.enabled ? 1 : 0
  name  = "portainer-data"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_image" "portainer" {
  count        = var.enabled ? 1 : 0
  name         = "portainer/portainer-ce:latest"
  keep_locally = true
}

resource "docker_container" "portainer" {
  count = var.enabled ? 1 : 0

  name  = "portainer"
  image = docker_image.portainer[0].image_id

  restart = "unless-stopped"

  ports {
    internal = 9000
    external = var.port
  }

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
  }

  volumes {
    volume_name    = docker_volume.data[0].name
    container_path = "/data"
  }

  networks_advanced {
    name = var.network_name
  }

  dynamic "labels" {
    for_each = merge(
      var.common_labels,
      local.use_proxy ? merge(
        {
          "traefik.enable"                                           = "true"
          "traefik.http.routers.portainer.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.portainer.rule"                      = "Host(`${local.portainer_host}`)"
          "traefik.http.services.portainer.loadbalancer.server.port" = "9000"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.portainer.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.portainer.tls" = "true"
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
