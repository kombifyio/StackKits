// =============================================================================
// GENERATED FILE - DO NOT EDIT DIRECTLY
// =============================================================================
// This file is auto-generated from the kombify-admin database.
// To modify the tool catalog, update the database and re-run the generator.
//
// Generated: 2026-02-11T13:27:29.121Z
// Source: kombify-admin/prisma/seed.ts -> Tool + ToolCategory + Service tables
// =============================================================================

package base

// =============================================================================
// TOOL CATALOG DEFINITIONS
// =============================================================================

#Layer: "1" | "2" | "3"

#CatalogTool: {
  name:         string
  displayName:  string
  description?: string
  layer:        #Layer
  category:     string
  image:        string
  defaultTag:   string
  supportsArm:  bool | *false
  supportsX86:  bool | *true
  minMemoryMB:  int | *0
}

#ToolCategoryDef: {
  slug:         string
  displayName:  string
  layer:        #Layer
  standardTool: string
  alternatives: [...string]
}

#IdentityPolicy: "none" | "forwardauth" | "oidc" | "provider"

#OwnerProvisioningPolicy: "none" | "required"

#CatalogService: {
  key:                     string
  displayName:             string
  description?:            string
  toolName:                string
  moduleSlug:              string
  localSlug:               string
  publicSlug:              string
  legacyAliases:           [...string]
  identityPolicy:          #IdentityPolicy
  ownerProvisioningPolicy: #OwnerProvisioningPolicy
  icon?:                   string
  badge?:                  string
  section?:                string
  order:                   int
  enableVar?:              string
  default:                 bool | *false
}

// =============================================================================
// TOOL CATEGORIES
// =============================================================================

#ToolCategories: {
  "identity": {
    slug:         "identity"
    displayName:  "Identity & Directory"
    layer:        "1"
    standardTool: "lldap"
    alternatives: ["openldap", "freeipa"]
  }
  "management": {
    slug:         "management"
    displayName:  "Container Management"
    layer:        "2"
    standardTool: "dozzle"
    alternatives: ["portainer", "dockge", "lazydocker"]
  }
  "paas": {
    slug:         "paas"
    displayName:  "Platform-as-a-Service"
    layer:        "2"
    standardTool: "dokploy"
    alternatives: ["coolify", "caprover"]
  }
  "platform-identity": {
    slug:         "platform-identity"
    displayName:  "Platform Identity & Auth Proxy"
    layer:        "2"
    standardTool: "tinyauth"
    alternatives: ["pocketid", "authelia", "authentik"]
  }
  "reverse-proxy": {
    slug:         "reverse-proxy"
    displayName:  "Reverse Proxy & Ingress"
    layer:        "2"
    standardTool: "traefik"
    alternatives: ["caddy", "nginx-proxy-manager", "haproxy"]
  }
  "monitoring": {
    slug:         "monitoring"
    displayName:  "Monitoring & Observability"
    layer:        "3"
    standardTool: "uptime-kuma"
    alternatives: ["beszel", "netdata", "prometheus", "grafana"]
  }
}

// =============================================================================
// APPROVED TOOLS
// =============================================================================

#ToolCatalog: {
  "lldap": {
    name:        "lldap"
    displayName: "LLDAP"
    description: "Lightweight LDAP server for user/group directory services"
    layer:       "1"
    category:    "identity"
    image:       "lldap/lldap"
    defaultTag:  "stable"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "step-ca": {
    name:        "step-ca"
    displayName: "Step-CA"
    description: "Private certificate authority for mTLS and internal PKI"
    layer:       "1"
    category:    "identity"
    image:       "smallstep/step-ca"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "dockge": {
    name:        "dockge"
    displayName: "Dockge"
    description: "Docker Compose stack manager with web UI"
    layer:       "2"
    category:    "management"
    image:       "louislam/dockge"
    defaultTag:  "1"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "dozzle": {
    name:        "dozzle"
    displayName: "Dozzle"
    description: "Real-time Docker log viewer"
    layer:       "2"
    category:    "management"
    image:       "amir20/dozzle"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "portainer": {
    name:        "portainer"
    displayName: "Portainer"
    description: "Container management UI for Docker and Kubernetes"
    layer:       "2"
    category:    "management"
    image:       "portainer/portainer-ce"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "coolify": {
    name:        "coolify"
    displayName: "Coolify"
    description: "Self-hosted Heroku/Netlify alternative with git deployments"
    layer:       "2"
    category:    "paas"
    image:       "ghcr.io/coollabsio/coolify"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "dokploy": {
    name:        "dokploy"
    displayName: "Dokploy"
    description: "Self-hosted PaaS for deploying applications with Docker"
    layer:       "2"
    category:    "paas"
    image:       "dokploy/dokploy"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "pocketid": {
    name:        "pocketid"
    displayName: "PocketID"
    description: "Lightweight OIDC provider with LDAP sync"
    layer:       "2"
    category:    "platform-identity"
    image:       "stonith404/pocket-id"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "tinyauth": {
    name:        "tinyauth"
    displayName: "TinyAuth"
    description: "Lightweight authentication proxy for Traefik"
    layer:       "2"
    category:    "platform-identity"
    image:       "ghcr.io/steveiliop56/tinyauth"
    defaultTag:  "v3"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "traefik": {
    name:        "traefik"
    displayName: "Traefik"
    description: "Cloud-native reverse proxy and load balancer"
    layer:       "2"
    category:    "reverse-proxy"
    image:       "traefik"
    defaultTag:  "v3.1"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "beszel": {
    name:        "beszel"
    displayName: "Beszel"
    description: "Lightweight server metrics and monitoring dashboard"
    layer:       "3"
    category:    "monitoring"
    image:       "henrygd/beszel"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "netdata": {
    name:        "netdata"
    displayName: "Netdata"
    description: "Real-time performance and health monitoring"
    layer:       "3"
    category:    "monitoring"
    image:       "netdata/netdata"
    defaultTag:  "stable"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "uptime-kuma": {
    name:        "uptime-kuma"
    displayName: "Uptime Kuma"
    description: "Self-hosted monitoring tool for endpoints and services"
    layer:       "3"
    category:    "monitoring"
    image:       "louislam/uptime-kuma"
    defaultTag:  "1"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "whoami": {
    name:        "whoami"
    displayName: "Whoami"
    description: "Simple HTTP request info service for testing"
    layer:       "3"
    category:    "utility"
    image:       "traefik/whoami"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "dashboard": {
    name:        "dashboard"
    displayName: "StackKits Dashboard"
    description: "Service hub and owner onboarding surface for StackKits"
    layer:       "3"
    category:    "utility"
    image:       "ghcr.io/kombify/stackkits-dashboard"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "immich": {
    name:        "immich"
    displayName: "Immich"
    description: "Photo and video management with mobile backup"
    layer:       "3"
    category:    "utility"
    image:       "ghcr.io/immich-app/immich-server"
    defaultTag:  "release"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "jellyfin": {
    name:        "jellyfin"
    displayName: "Jellyfin"
    description: "Media server for movies, TV, music, and photos"
    layer:       "3"
    category:    "utility"
    image:       "jellyfin/jellyfin"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "vaultwarden": {
    name:        "vaultwarden"
    displayName: "Vaultwarden"
    description: "Bitwarden-compatible password vault"
    layer:       "3"
    category:    "utility"
    image:       "vaultwarden/server"
    defaultTag:  "latest"
    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
}

// =============================================================================
// SERVICE CATALOG
// =============================================================================

#ServiceCatalog: {
  "base": {
    key:                     "base"
    displayName:             "Dashboard"
    description:             "StackKits service hub"
    toolName:                "dashboard"
    moduleSlug:              "dashboard"
    localSlug:               "base"
    publicSlug:              "base"
    legacyAliases:           ["dashboard", "dash"]
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128421;"
    badge:                   "L3 - Hub"
    section:                 "Platform"
    order:                   -1
    enableVar:               "enable_dashboard"
    default:                 true
  }
  "id": {
    key:                     "id"
    displayName:             "PocketID"
    description:             "OIDC identity provider with passkey authentication"
    toolName:                "pocketid"
    moduleSlug:              "pocketid"
    localSlug:               "id"
    publicSlug:              "id"
    legacyAliases:           ["pocketid"]
    identityPolicy:          "provider"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128100;"
    badge:                   "L1 - IdP"
    section:                 "Platform"
    order:                   10
    enableVar:               "enable_pocketid"
    default:                 true
  }
  "auth": {
    key:                     "auth"
    displayName:             "TinyAuth"
    description:             "ForwardAuth gateway backed by PocketID"
    toolName:                "tinyauth"
    moduleSlug:              "tinyauth"
    localSlug:               "auth"
    publicSlug:              "auth"
    legacyAliases:           ["tinyauth"]
    identityPolicy:          "oidc"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128274;"
    badge:                   "L1 - ForwardAuth"
    section:                 "Platform"
    order:                   20
    enableVar:               "enable_tinyauth"
    default:                 true
  }
  "traefik": {
    key:                     "traefik"
    displayName:             "Traefik"
    description:             "Routes all service traffic"
    toolName:                "traefik"
    moduleSlug:              "traefik"
    localSlug:               "traefik"
    publicSlug:              "traefik"
    legacyAliases:           []
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                    "&#9889;"
    badge:                   "L2 - Reverse Proxy"
    section:                 "Platform"
    order:                   30
    enableVar:               "enable_traefik"
    default:                 true
  }
  "dokploy": {
    key:                     "dokploy"
    displayName:             "Dokploy"
    description:             "Self-hosted PaaS for deploying applications"
    toolName:                "dokploy"
    moduleSlug:              "dokploy"
    localSlug:               "dokploy"
    publicSlug:              "dokploy"
    legacyAliases:           []
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128640;"
    badge:                   "L2 - PaaS"
    section:                 "Platform"
    order:                   40
    enableVar:               "enable_dokploy"
    default:                 true
  }
  "coolify": {
    key:                     "coolify"
    displayName:             "Coolify"
    description:             "Self-hosted deployment platform"
    toolName:                "coolify"
    moduleSlug:              "coolify"
    localSlug:               "coolify"
    publicSlug:              "coolify"
    legacyAliases:           []
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128171;"
    badge:                   "L2 - PaaS"
    section:                 "Platform"
    order:                   41
    enableVar:               "enable_coolify"
    default:                 false
  }
  "dockge": {
    key:                     "dockge"
    displayName:             "Dockge"
    description:             "Docker Compose stack manager"
    toolName:                "dockge"
    moduleSlug:              "dockge"
    localSlug:               "dockge"
    publicSlug:              "dockge"
    legacyAliases:           []
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128230;"
    badge:                   "L2 - Compose Manager"
    section:                 "Platform"
    order:                   42
    enableVar:               "enable_dockge"
    default:                 false
  }
  "kuma": {
    key:                     "kuma"
    displayName:             "Uptime Kuma"
    description:             "Service uptime monitoring and status pages"
    toolName:                "uptime-kuma"
    moduleSlug:              "uptime-kuma"
    localSlug:               "kuma"
    publicSlug:              "kuma"
    legacyAliases:           ["uptime-kuma"]
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128202;"
    badge:                   "L3 - Monitoring"
    section:                 "Applications"
    order:                   10
    enableVar:               "enable_uptime_kuma"
    default:                 true
  }
  "whoami": {
    key:                     "whoami"
    displayName:             "Whoami"
    description:             "HTTP echo service for routing diagnostics"
    toolName:                "whoami"
    moduleSlug:              "whoami"
    localSlug:               "whoami"
    publicSlug:              "whoami"
    legacyAliases:           []
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                    "&#129302;"
    badge:                   "L3 - Test"
    section:                 "Applications"
    order:                   20
    default:                 true
  }
  "vault": {
    key:                     "vault"
    displayName:             "Vaultwarden"
    description:             "Bitwarden-compatible password vault"
    toolName:                "vaultwarden"
    moduleSlug:              "vaultwarden"
    localSlug:               "vault"
    publicSlug:              "vault"
    legacyAliases:           ["vaultwarden"]
    identityPolicy:          "oidc"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128272;"
    badge:                   "L3 - Vault"
    section:                 "Applications"
    order:                   30
    enableVar:               "enable_vaultwarden"
    default:                 true
  }
  "media": {
    key:                     "media"
    displayName:             "Jellyfin"
    description:             "Media server for movies, TV, music, and photos"
    toolName:                "jellyfin"
    moduleSlug:              "jellyfin"
    localSlug:               "media"
    publicSlug:              "media"
    legacyAliases:           ["jellyfin"]
    identityPolicy:          "oidc"
    ownerProvisioningPolicy: "required"
    icon:                    "&#127916;"
    badge:                   "L3 - Media"
    section:                 "Applications"
    order:                   40
    enableVar:               "enable_jellyfin"
    default:                 true
  }
  "photos": {
    key:                     "photos"
    displayName:             "Immich"
    description:             "Photo and video management with mobile backup"
    toolName:                "immich"
    moduleSlug:              "immich"
    localSlug:               "photos"
    publicSlug:              "photos"
    legacyAliases:           ["immich"]
    identityPolicy:          "oidc"
    ownerProvisioningPolicy: "required"
    icon:                    "&#128247;"
    badge:                   "L3 - Photos"
    section:                 "Applications"
    order:                   50
    enableVar:               "enable_immich"
    default:                 true
  }
}
