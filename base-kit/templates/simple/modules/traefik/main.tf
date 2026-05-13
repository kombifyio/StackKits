# Traefik - Reverse Proxy Module
# Always deployed in all variants

variable "network_name" {
  description = "Docker network to join"
  type        = string
}

variable "access_mode" {
  description = "Access mode: 'ports' or 'proxy'"
  type        = string
  default     = "ports"
}

variable "enable_https" {
  description = "Enable HTTPS on Traefik"
  type        = bool
  default     = false
}

variable "enable_letsencrypt" {
  description = "Enable Let's Encrypt via ACME"
  type        = bool
  default     = false
}

variable "domain" {
  description = "Base domain for proxy hostnames"
  type        = string
  default     = ""
}

variable "acme_email" {
  description = "Email for ACME/Let's Encrypt"
  type        = string
  default     = ""
}

variable "dashboard_port" {
  description = "Host port for Traefik dashboard"
  type        = number
  default     = 8080
}

variable "bind_address" {
  description = "IP address to bind service ports"
  type        = string
  default     = "0.0.0.0"
}

variable "common_labels" {
  description = "Common labels for all resources"
  type        = map(string)
  default     = {}
}

locals {
  use_proxy        = var.access_mode == "proxy"
  proxy_entrypoint = local.use_proxy && var.enable_https ? "websecure" : "web"
  traefik_host     = "traefik.${var.domain}"
}

resource "docker_volume" "certs" {
  name = "traefik-certs"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_volume" "config" {
  name = "traefik-config"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_image" "traefik" {
  name         = "traefik:v3.1"
  keep_locally = true
}

resource "docker_container" "traefik" {
  name  = "traefik"
  image = docker_image.traefik.image_id

  restart = "unless-stopped"

  dynamic "ports" {
    for_each = local.use_proxy ? [1] : []
    content {
      internal = 80
      external = 80
    }
  }

  dynamic "ports" {
    for_each = local.use_proxy && var.enable_https ? [1] : []
    content {
      internal = 443
      external = 443
    }
  }

  ports {
    internal = 8080
    external = var.dashboard_port
    ip       = var.bind_address
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

  volumes {
    volume_name    = docker_volume.config.name
    container_path = "/etc/traefik"
  }

  networks_advanced {
    name = var.network_name
  }

  command = concat(
    [
      "--api.dashboard=true",
      "--api.insecure=true",
      "--providers.docker=true",
      "--providers.docker.exposedbydefault=false",
      "--providers.docker.network=traefik",
      "--entrypoints.web.address=:80",
    ],
    (local.use_proxy && var.enable_https) ? [
      "--entrypoints.web.http.redirections.entrypoint.to=websecure",
      "--entrypoints.web.http.redirections.entrypoint.scheme=https",
      "--entrypoints.websecure.address=:443",
    ] : [],
    (local.use_proxy && var.enable_https && var.enable_letsencrypt) ? [
      "--certificatesresolvers.letsencrypt.acme.email=${var.acme_email}",
      "--certificatesresolvers.letsencrypt.acme.storage=/certs/acme.json",
      "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web",
    ] : [],
    [
      "--ping=true",
      "--log.level=INFO",
    ]
  )

  dynamic "labels" {
    for_each = merge(
      var.common_labels,
      local.use_proxy ? merge(
        {
          "traefik.enable"                           = "true"
          "traefik.http.routers.traefik.entrypoints" = local.proxy_entrypoint
          "traefik.http.routers.traefik.rule"        = "Host(`${local.traefik_host}`)"
          "traefik.http.routers.traefik.service"     = "api@internal"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.traefik.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.traefik.tls" = "true"
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

  healthcheck {
    test         = ["CMD", "traefik", "healthcheck", "--ping"]
    interval     = "10s"
    timeout      = "5s"
    retries      = 3
    start_period = "10s"
  }
}

output "container_id" {
  value = docker_container.traefik.id
}
