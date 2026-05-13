# Monitoring Module
# Deploys ONE of: Uptime Kuma (default), Beszel, or Netdata (minimal)

variable "variant" {
  description = "Service variant: default, beszel, or minimal"
  type        = string
  default     = "default"
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

variable "uptime_kuma_port" {
  type    = number
  default = 4001
}

variable "beszel_port" {
  type    = number
  default = 8090
}

variable "netdata_port" {
  type    = number
  default = 19999
}

variable "memory_limit" {
  description = "Memory limit in MB"
  type        = number
  default     = 512
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

locals {
  is_default       = var.variant == "default"
  is_beszel        = var.variant == "beszel"
  is_minimal       = var.variant == "minimal"
  use_proxy        = var.access_mode == "proxy"
  proxy_entrypoint = local.use_proxy && var.enable_https ? "websecure" : "web"
  status_host      = "status.${var.domain}"
  monitor_host     = "monitor.${var.domain}"
}

# =============================================================================
# UPTIME KUMA (default variant)
# =============================================================================

resource "docker_volume" "uptime_kuma_data" {
  count = local.is_default ? 1 : 0
  name  = "uptime-kuma-data"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_image" "uptime_kuma" {
  count        = local.is_default ? 1 : 0
  name         = "louislam/uptime-kuma:1"
  keep_locally = true
}

resource "docker_container" "uptime_kuma" {
  count = local.is_default ? 1 : 0

  name  = "uptime-kuma"
  image = docker_image.uptime_kuma[0].image_id

  restart = "unless-stopped"

  ports {
    internal = 3001
    external = var.uptime_kuma_port
  }

  volumes {
    volume_name    = docker_volume.uptime_kuma_data[0].name
    container_path = "/app/data"
  }

  networks_advanced {
    name = var.network_name
  }

  dynamic "labels" {
    for_each = merge(
      var.common_labels,
      local.use_proxy ? merge(
        {
          "traefik.enable"                                             = "true"
          "traefik.http.routers.uptime-kuma.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.uptime-kuma.rule"                      = "Host(`${local.status_host}`)"
          "traefik.http.services.uptime-kuma.loadbalancer.server.port" = "3001"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.uptime-kuma.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.uptime-kuma.tls" = "true"
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

# =============================================================================
# BESZEL (beszel variant)
# =============================================================================

resource "docker_volume" "beszel_data" {
  count = local.is_beszel ? 1 : 0
  name  = "beszel-data"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_image" "beszel" {
  count        = local.is_beszel ? 1 : 0
  name         = "henrygd/beszel:latest"
  keep_locally = true
}

resource "docker_container" "beszel" {
  count = local.is_beszel ? 1 : 0

  name  = "beszel"
  image = docker_image.beszel[0].image_id

  restart = "unless-stopped"

  ports {
    internal = 8090
    external = var.beszel_port
  }

  volumes {
    volume_name    = docker_volume.beszel_data[0].name
    container_path = "/beszel_data"
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
          "traefik.http.routers.beszel.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.beszel.rule"                      = "Host(`${local.monitor_host}`)"
          "traefik.http.services.beszel.loadbalancer.server.port" = "8090"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.beszel.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.beszel.tls" = "true"
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

# =============================================================================
# NETDATA (minimal variant)
# =============================================================================

resource "docker_volume" "netdata_config" {
  count = local.is_minimal ? 1 : 0
  name  = "netdata-config"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_volume" "netdata_lib" {
  count = local.is_minimal ? 1 : 0
  name  = "netdata-lib"
  labels { label = "managed-by"; value = "kombistack" }
}

resource "docker_image" "netdata" {
  count        = local.is_minimal ? 1 : 0
  name         = "netdata/netdata:stable"
  keep_locally = true
}

resource "docker_container" "netdata" {
  count = local.is_minimal ? 1 : 0

  name  = "netdata"
  image = docker_image.netdata[0].image_id

  restart = "unless-stopped"

  ports {
    internal = 19999
    external = var.netdata_port
  }

  capabilities {
    add = ["SYS_PTRACE"]
  }

  security_opts = ["apparmor:unconfined"]

  volumes {
    host_path      = "/proc"
    container_path = "/host/proc"
    read_only      = true
  }

  volumes {
    host_path      = "/sys"
    container_path = "/host/sys"
    read_only      = true
  }

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
    read_only      = true
  }

  volumes {
    volume_name    = docker_volume.netdata_config[0].name
    container_path = "/etc/netdata"
  }

  volumes {
    volume_name    = docker_volume.netdata_lib[0].name
    container_path = "/var/lib/netdata"
  }

  networks_advanced {
    name = var.network_name
  }

  dynamic "labels" {
    for_each = merge(
      var.common_labels,
      local.use_proxy ? merge(
        {
          "traefik.enable"                                         = "true"
          "traefik.http.routers.netdata.entrypoints"               = local.proxy_entrypoint
          "traefik.http.routers.netdata.rule"                      = "Host(`${local.monitor_host}`)"
          "traefik.http.services.netdata.loadbalancer.server.port" = "19999"
        },
        (var.enable_https ? (var.enable_letsencrypt ? {
          "traefik.http.routers.netdata.tls.certresolver" = "letsencrypt"
        } : {
          "traefik.http.routers.netdata.tls" = "true"
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
