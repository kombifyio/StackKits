// =============================================================================
// GENERATED FILE - DO NOT EDIT DIRECTLY
// =============================================================================
// This file is auto-generated from the kombify-admin database.
// To modify context defaults, update the database and re-run the generator.
//
// Generated: 2026-02-11T13:27:29.111Z
// Source: kombify-admin/prisma/seed.ts → ContextDefaults table
// =============================================================================

package base

// =============================================================================
// CONTEXT DEFAULTS DEFINITION
// =============================================================================

#ContextType: "LOCAL" | "CLOUD" | "PI"

#ContextDefault: {
  context:             #ContextType
  displayName:         string
  description?:        string
  defaultPaas?:        string
  defaultTlsMode?:     string
  defaultComputeTier?: string
  defaultMemoryLimitMB?: int
  defaultCpuShares?:   int
  defaultStorageDriver?: string
  defaultDnsStrategy?: string
  defaultBackupTarget?: string
  detectionCriteria?:  _
  hardwareProfile?:    _
}

// =============================================================================
// CONTEXT DEFAULTS REGISTRY
// =============================================================================

#ContextDefaults: {
  "LOCAL": {
    context:             "LOCAL"
    displayName:         "Local Hardware"
    description:         "Physical server with no cloud metadata — full control, local network, no egress costs"
    defaultPaas:         "dokploy"
    defaultTlsMode:      "self-signed"
    defaultComputeTier:  "standard"
    defaultMemoryLimitMB: 4096
    defaultCpuShares:    1024
    defaultStorageDriver: "overlay2"
    defaultDnsStrategy:  "local-dns"
    defaultBackupTarget: "local-nas"
  }
  "CLOUD": {
    context:             "CLOUD"
    displayName:         "Cloud VPS"
    description:         "Cloud provider metadata detected — public IP, egress costs, provider-managed networking"
    defaultPaas:         "coolify"
    defaultTlsMode:      "letsencrypt"
    defaultComputeTier:  "standard"
    defaultMemoryLimitMB: 2048
    defaultCpuShares:    1024
    defaultStorageDriver: "overlay2"
    defaultDnsStrategy:  "cloud-dns"
    defaultBackupTarget: "s3"
  }
  "PI": {
    context:             "PI"
    displayName:         "Raspberry Pi"
    description:         "ARM architecture with low memory — resource-constrained, SD card storage, power-efficient"
    defaultPaas:         "dockge"
    defaultTlsMode:      "self-signed"
    defaultComputeTier:  "low"
    defaultMemoryLimitMB: 256
    defaultCpuShares:    512
    defaultStorageDriver: "overlay2"
    defaultDnsStrategy:  "mdns"
    defaultBackupTarget: "local-nas"
  }
}

// =============================================================================
// CONTEXT DETECTION HELPERS
// =============================================================================

// Use this to apply context-specific defaults to a StackKit configuration.
// Example usage in a StackKit definition:
//
//   import "base/generated"
//
//   _detectedContext: #ContextType
//   _defaults: #ContextDefaults[_detectedContext]
//   paas: _defaults.defaultPaas
//
