# Monitoring Stack - Advanced Mode
# Adaptive monitoring: Netdata (standard/high) oder Glances (low)

stack {
  name        = "monitoring"
  description = "System Monitoring - Netdata or Glances based on compute tier"
  id          = "base-kit-monitoring"

  tags = [
    "core",
    "monitoring",
    "observability",
  ]

  # Abhängig von Traefik
  after = ["stacks/traefik"]
}

globals "monitoring" {
  # Netdata für standard/high compute
  netdata = {
    version = global.versions.netdata
    port    = 19999
    
    memory_limits = {
      high     = 1024
      standard = 512
    }
  }

  # Glances für low compute
  glances = {
    version = global.versions.glances
    port    = 61208
    memory  = 256
  }
}
