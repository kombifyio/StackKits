# Dozzle - Log Viewer Module
# Always deployed in all variants

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
  description = "Host port for Dozzle"
  type        = number
  default     = 8888
}

variable "memory_limit" {
  type    = number
  default = 256
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

locals {
  use_proxy        = var.access_mode == "proxy"
  proxy_entrypoint = local.use_proxy && var.enable_https ? "websecure" : "web"
  logs_host        = "logs.${var.domain}"
}

resource "docker_image" "dozzle" {
  name         = "amir20/dozzle:latest"
  keep_locally = true
}

resource "docker_container" "dozzle" {
  name  = "dozzle"
  image = docker_image.dozzle.image_id

  restart = "unless-stopped"

  ports {
    internal = 8080
    external = var.port
  }

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
    read_only      = true
  }

  networks_advanced {
    name = var.network_name
  }

  dynamic "labels" {
    for_each = merge(
      var.common_labels,
      local.use_proxy ? merge(
        {
          "traefik.enable"                                        = "true"
          "traefik.http.routers.dozzle.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.dozzle.rule"                      = "Host(`${local.logs_host}`)"
          "traefik.http.services.dozzle.loadbalancer.server.port" = "8080"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.dozzle.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.dozzle.tls" = "true"
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
