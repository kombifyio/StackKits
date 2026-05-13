# Whoami - Test Container Module
# Deployed in default and beszel variants

variable "enabled" {
  type    = bool
  default = true
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
  default = 9080
}

variable "memory_limit" {
  type    = number
  default = 64
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

locals {
  use_proxy        = var.access_mode == "proxy"
  proxy_entrypoint = local.use_proxy && var.enable_https ? "websecure" : "web"
  whoami_host      = "whoami.${var.domain}"
}

resource "docker_image" "whoami" {
  count        = var.enabled ? 1 : 0
  name         = "traefik/whoami:latest"
  keep_locally = true
}

resource "docker_container" "whoami" {
  count = var.enabled ? 1 : 0

  name  = "whoami"
  image = docker_image.whoami[0].image_id

  restart = "unless-stopped"

  ports {
    internal = 80
    external = var.port
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
          "traefik.http.routers.whoami.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.whoami.rule"                      = "Host(`${local.whoami_host}`)"
          "traefik.http.services.whoami.loadbalancer.server.port" = "80"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.whoami.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.whoami.tls" = "true"
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
