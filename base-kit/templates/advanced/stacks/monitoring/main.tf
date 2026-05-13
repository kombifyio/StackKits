# Monitoring Stack - OpenTofu Configuration (Advanced Mode)
# Dynamisch: Netdata für standard/high, Glances für low compute

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

variable "compute_tier" {
  description = "Compute tier"
  type        = string
  default     = "standard"
}

variable "traefik_network" {
  description = "Traefik network name"
  type        = string
  default     = "traefik"
}

variable "netdata_claim_token" {
  description = "Netdata Cloud claim token"
  type        = string
  default     = ""
  sensitive   = true
}

locals {
  monitor_url  = "monitor.${var.domain}"
  use_netdata  = var.compute_tier != "low"
  
  netdata_memory = tm_try(
    global.monitoring.netdata.memory_limits[var.compute_tier],
    512
  )
}

provider "docker" {
}

data "docker_network" "traefik" {
  name = var.traefik_network
}

# ============================================================================
# Netdata (standard/high compute tiers)
# ============================================================================

resource "docker_volume" "netdata_config" {
  count = local.use_netdata ? 1 : 0
  name  = "netdata-config"

  labels {
    label = "managed-by"
    value = "kombistack"
  }
}

resource "docker_volume" "netdata_lib" {
  count = local.use_netdata ? 1 : 0
  name  = "netdata-lib"

  labels {
    label = "managed-by"
    value = "kombistack"
  }
}

resource "docker_volume" "netdata_cache" {
  count = local.use_netdata ? 1 : 0
  name  = "netdata-cache"

  labels {
    label = "managed-by"
    value = "kombistack"
  }
}

resource "docker_image" "netdata" {
  count        = local.use_netdata ? 1 : 0
  name         = "netdata/netdata:${tm_try(global.monitoring.netdata.version, "stable")}"
  keep_locally = true
}

resource "docker_container" "netdata" {
  count = local.use_netdata ? 1 : 0

  name  = "netdata"
  image = docker_image.netdata[0].image_id

  restart = "unless-stopped"

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

  volumes {
    volume_name    = docker_volume.netdata_cache[0].name
    container_path = "/var/cache/netdata"
  }

  networks_advanced {
    name = data.docker_network.traefik.name
  }

  env = var.netdata_claim_token != "" ? [
    "NETDATA_CLAIM_TOKEN=${var.netdata_claim_token}",
    "NETDATA_CLAIM_URL=https://app.netdata.cloud",
  ] : []

  labels {
    label = "traefik.enable"
    value = "true"
  }

  labels {
    label = "traefik.http.routers.monitoring.entrypoints"
    value = "websecure"
  }

  labels {
    label = "traefik.http.routers.monitoring.rule"
    value = "Host(`${local.monitor_url}`)"
  }

  labels {
    label = "traefik.http.routers.monitoring.tls.certresolver"
    value = "letsencrypt"
  }

  labels {
    label = "traefik.http.services.monitoring.loadbalancer.server.port"
    value = tostring(tm_try(global.monitoring.netdata.port, 19999))
  }

  labels {
    label = "managed-by"
    value = "kombistack"
  }

  labels {
    label = "stackkit"
    value = "base-kit"
  }

  memory = local.netdata_memory
}

# ============================================================================
# Glances (low compute tier only)
# ============================================================================

resource "docker_image" "glances" {
  count        = local.use_netdata ? 0 : 1
  name         = "nicolargo/glances:${tm_try(global.monitoring.glances.version, "latest-alpine")}"
  keep_locally = true
}

resource "docker_container" "glances" {
  count = local.use_netdata ? 0 : 1

  name  = "glances"
  image = docker_image.glances[0].image_id

  restart = "unless-stopped"

  volumes {
    host_path      = "/var/run/docker.sock"
    container_path = "/var/run/docker.sock"
    read_only      = true
  }

  networks_advanced {
    name = data.docker_network.traefik.name
  }

  env = [
    "GLANCES_OPT=-w",
  ]

  labels {
    label = "traefik.enable"
    value = "true"
  }

  labels {
    label = "traefik.http.routers.monitoring.entrypoints"
    value = "websecure"
  }

  labels {
    label = "traefik.http.routers.monitoring.rule"
    value = "Host(`${local.monitor_url}`)"
  }

  labels {
    label = "traefik.http.routers.monitoring.tls.certresolver"
    value = "letsencrypt"
  }

  labels {
    label = "traefik.http.services.monitoring.loadbalancer.server.port"
    value = tostring(tm_try(global.monitoring.glances.port, 61208))
  }

  labels {
    label = "managed-by"
    value = "kombistack"
  }

  labels {
    label = "stackkit"
    value = "base-kit"
  }

  memory = tm_try(global.monitoring.glances.memory, 256)
}

# ============================================================================
# Outputs
# ============================================================================

output "url" {
  description = "Monitoring dashboard URL"
  value       = "https://${local.monitor_url}"
}

output "service_type" {
  description = "Active monitoring service"
  value       = local.use_netdata ? "netdata" : "glances"
}

output "compute_tier" {
  description = "Active compute tier"
  value       = var.compute_tier
}
