// =============================================================================
// GENERATED FILE - DO NOT EDIT DIRECTLY
// =============================================================================
// This file is auto-generated from the kombify-admin database.
// To modify add-ons, update the database and re-run the generator.
//
// Generated: 2026-02-11T13:27:29.105Z
// Source: kombify-admin/prisma/seed.ts → AddOn table
// =============================================================================

package base

// =============================================================================
// ADD-ON TYPE DEFINITIONS
// =============================================================================

#ArchitecturePattern: "BASE" | "MODERN" | "HA"
#NodeContext:          "local" | "cloud" | "pi"

#AddOn: {
  name:               string
  displayName:        string
  description?:       string
  category:           string
  version:            string
  compatibleKits:     [...#ArchitecturePattern]
  compatibleContexts: [...#NodeContext]
  dependsOn:          [...string]
  conflictsWith:      [...string]
  minMemoryMB:        int & >=0
  minCpuCores:        number & >=0
  requiresGpu:        bool
  includedTools:      [...string]
  autoActivate:       bool
  autoActivateCondition?: string
}

// =============================================================================
// ADD-ON REGISTRY
// =============================================================================

#AddOnRegistry: {
  // Applications
  "media": {
    name:               "media"
    displayName:        "Media Server"
    description:        "Jellyfin media server with *arr stack for automated media management"
    category:           "applications"
    version:            "1.0.0"
    compatibleKits:     ["BASE", "MODERN"]
    compatibleContexts: ["local", "cloud", "pi"]
    dependsOn:          []
    conflictsWith:      []
    minMemoryMB:        512
    minCpuCores:        0
    requiresGpu:        false
    includedTools:      ["jellyfin", "sonarr", "radarr", "prowlarr"]
    autoActivate:       false
    
  }

  // Compute
  "gpu-workloads": {
    name:               "gpu-workloads"
    displayName:        "GPU Workloads"
    description:        "NVIDIA/AMD GPU passthrough for AI, ML, and compute workloads"
    category:           "compute"
    version:            "1.0.0"
    compatibleKits:     ["BASE", "MODERN"]
    compatibleContexts: ["local", "cloud"]
    dependsOn:          []
    conflictsWith:      []
    minMemoryMB:        2048
    minCpuCores:        0
    requiresGpu:        true
    includedTools:      ["nvidia-container-toolkit"]
    autoActivate:       true
    autoActivateCondition: "Node reports GPU capability via agent hardware report"
  }

  // Data
  "backup": {
    name:               "backup"
    displayName:        "Backup & Restore"
    description:        "Automated backups with Restic to S3, B2, or local NAS targets"
    category:           "data"
    version:            "1.0.0"
    compatibleKits:     ["BASE", "MODERN", "HA"]
    compatibleContexts: ["local", "cloud", "pi"]
    dependsOn:          []
    conflictsWith:      []
    minMemoryMB:        128
    minCpuCores:        0
    requiresGpu:        false
    includedTools:      ["restic"]
    autoActivate:       false
    
  }

  // Development
  "ci-cd": {
    name:               "ci-cd"
    displayName:        "CI/CD Pipeline"
    description:        "Gitea git hosting with Drone CI for self-hosted continuous integration"
    category:           "development"
    version:            "1.0.0"
    compatibleKits:     ["BASE", "MODERN", "HA"]
    compatibleContexts: ["local", "cloud"]
    dependsOn:          []
    conflictsWith:      []
    minMemoryMB:        512
    minCpuCores:        0
    requiresGpu:        false
    includedTools:      ["gitea", "drone-ci"]
    autoActivate:       false
    
  }

  // Iot
  "smart-home": {
    name:               "smart-home"
    displayName:        "Smart Home"
    description:        "Home Assistant with MQTT broker and Zigbee2MQTT for IoT device management"
    category:           "iot"
    version:            "1.0.0"
    compatibleKits:     ["BASE"]
    compatibleContexts: ["local", "pi"]
    dependsOn:          []
    conflictsWith:      []
    minMemoryMB:        256
    minCpuCores:        0
    requiresGpu:        false
    includedTools:      ["home-assistant", "mosquitto", "zigbee2mqtt"]
    autoActivate:       false
    
  }

  // Networking
  "vpn-overlay": {
    name:               "vpn-overlay"
    displayName:        "VPN Mesh Overlay"
    description:        "Headscale/Tailscale mesh VPN for connecting nodes across networks"
    category:           "networking"
    version:            "1.0.0"
    compatibleKits:     ["MODERN", "HA"]
    compatibleContexts: ["local", "cloud"]
    dependsOn:          []
    conflictsWith:      []
    minMemoryMB:        64
    minCpuCores:        0
    requiresGpu:        false
    includedTools:      ["headscale"]
    autoActivate:       true
    autoActivateCondition: "StackKit pattern is MODERN (always requires VPN overlay)"
  }

  // Observability
  "monitoring": {
    name:               "monitoring"
    displayName:        "Monitoring Stack"
    description:        "Full observability with Prometheus, Grafana, and Alertmanager for metrics, dashboards, and alerting"
    category:           "observability"
    version:            "1.0.0"
    compatibleKits:     ["BASE", "MODERN", "HA"]
    compatibleContexts: ["local", "cloud"]
    dependsOn:          []
    conflictsWith:      []
    minMemoryMB:        512
    minCpuCores:        0
    requiresGpu:        false
    includedTools:      ["prometheus", "grafana", "alertmanager"]
    autoActivate:       false
    
  }

}

// =============================================================================
// ADD-ON COMPATIBILITY CONSTRAINTS
// =============================================================================

// These constraints can be imported in StackKit definitions to validate
// that activated add-ons are compatible with the selected pattern/context.

#ValidateAddOnCompatibility: {
  pattern:   #ArchitecturePattern
  context:   #NodeContext
  addons:    [...string]

  // Every activated add-on must be compatible
  _valid: true & and([
    for a in addons {
      let addon = #AddOnRegistry[a]
      // Pattern must be in compatibleKits
      or([ for k in addon.compatibleKits { k == pattern } ])
      // Context must be in compatibleContexts
      or([ for c in addon.compatibleContexts { c == context } ])
    }
  ])
}
