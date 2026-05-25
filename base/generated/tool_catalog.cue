// =============================================================================
// GENERATED FILE - DO NOT EDIT DIRECTLY
// =============================================================================
// This file is auto-generated from the kombify-admin database.
// To modify the tool catalog, update the database and re-run the generator.
//
// Generated: 2026-05-22T08:50:42.514Z
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
  logoUrl?:     string
  imageUrl?:    string
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

#IdentityPolicy: "none" | "forwardauth" | "oidc" | "provider" | "self-auth"

#OwnerProvisioningPolicy: "none" | "required"

#SetupPolicy: "manual" | "on_demand" | "automatic"

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
  logoUrl?:                string
  badge?:                  string
  layer?:                  string
  section?:                string
  order:                   int
  enableVar?:              string
  guideUrl?:               string
  setupPolicy?:            #SetupPolicy
  setupActionLabel?:       string
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
  "monitoring": {
    slug:         "monitoring"
    displayName:  "Monitoring & Observability"
    layer:        "2"
    standardTool: "uptime-kuma"
    alternatives: ["beszel", "netdata", "prometheus", "grafana"]
  }
  "paas": {
    slug:         "paas"
    displayName:  "Platform-as-a-Service"
    layer:        "2"
    standardTool: "coolify"
    alternatives: ["komodo"]
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
    logoUrl:     "https://cdn.simpleicons.org/openldap/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/letsencrypt/ffffff"

    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "whoami": {
    name:        "whoami"
    displayName: "Whoami"
    description: "Simple HTTP request info service for testing"
    layer:       "2"
    category:    "diagnostics"
    image:       "traefik/whoami"
    defaultTag:  "latest"
    logoUrl:     "https://cdn.simpleicons.org/httpie/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/docker/ffffff"

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
  "uptime-kuma": {
    name:        "uptime-kuma"
    displayName: "Uptime Kuma"
    description: "Self-hosted monitoring tool for endpoints and services"
    layer:       "2"
    category:    "monitoring"
    image:       "louislam/uptime-kuma"
    defaultTag:  "1"
    logoUrl:     "https://cdn.simpleicons.org/uptimekuma/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/coolify/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/openid/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/openid/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/traefikproxy/ffffff"

    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "dashboard": {
    name:        "dashboard"
    displayName: "StackKits Dashboard"
    description: "Service hub and owner onboarding surface for StackKits"
    layer:       "2"
    category:    "utility"
    image:       "ghcr.io/kombify/stackkits-dashboard"
    defaultTag:  "latest"
    logoUrl:     "https://stackkit.cc/favicon.svg"

    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
  "homepage": {
    name:        "homepage"
    displayName: "Homepage"
    description: "Generated homelab start dashboard backed by the StackKits service catalog"
    layer:       "2"
    category:    "utility"
    image:       "ghcr.io/gethomepage/homepage"
    defaultTag:  "latest"
    logoUrl:     "https://cdn.simpleicons.org/homeassistant/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/prometheus/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/netdata/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/immich/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/jellyfin/ffffff"

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
    logoUrl:     "https://cdn.simpleicons.org/bitwarden/ffffff"

    supportsArm: false
    supportsX86: true
    minMemoryMB: 0
  }
}

// =============================================================================
// SERVICE CATALOG
// =============================================================================

#ServiceCatalog: {
  "vault": {
    key:                     "vault"
    displayName:             "Vaultwarden"
    description:             "Bitwarden-compatible password vault"
    toolName:                "vaultwarden"
    moduleSlug:              "vaultwarden"
    localSlug:               "vault"
    publicSlug:              "vault"
    legacyAliases:           ["vaultwarden"]
    identityPolicy:          "self-auth"
    ownerProvisioningPolicy: "none"
    icon:                   "&#128272;"
    logoUrl:                "https://cdn.simpleicons.org/bitwarden/ffffff"
    badge:                  "L3 - Vault"
    layer:                  "L3-application"
    section:                "Applications"
    order:                   30
    enableVar:              "enable_vaultwarden"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/vaultwarden"
    setupPolicy:            "manual"

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
    identityPolicy:          "self-auth"
    ownerProvisioningPolicy: "none"
    icon:                   "&#127916;"
    logoUrl:                "https://cdn.simpleicons.org/jellyfin/ffffff"
    badge:                  "L3 - Media"
    layer:                  "L3-application"
    section:                "Applications"
    order:                   40
    enableVar:              "enable_jellyfin"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/jellyfin"
    setupPolicy:            "manual"

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
    identityPolicy:          "self-auth"
    ownerProvisioningPolicy: "required"
    icon:                   "&#128247;"
    logoUrl:                "https://cdn.simpleicons.org/immich/ffffff"
    badge:                  "L3 - Photos"
    layer:                  "L3-application"
    section:                "Applications"
    order:                   50
    enableVar:              "enable_immich"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/immich"
    setupPolicy:            "on_demand"
    setupActionLabel:       "Do the setup for me"
    default:                 true
  }
  "base": {
    key:                     "base"
    displayName:             "Node Hub"
    description:             "StackKits node hub with onboarding, recovery, and local service links"
    toolName:                "dashboard"
    moduleSlug:              "dashboard"
    localSlug:               "base"
    publicSlug:              "base"
    legacyAliases:           ["dashboard", "dash"]
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                   "&#128421;"
    logoUrl:                "https://stackkit.cc/favicon.svg"
    badge:                  "L2 - Node Hub"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   -1
    enableVar:              "enable_dashboard"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/node-hub"
    setupPolicy:            "automatic"

    default:                 true
  }
  "home": {
    key:                     "home"
    displayName:             "Homepage"
    description:             "IaC-managed homelab start dashboard generated from the StackKits service catalog"
    toolName:                "homepage"
    moduleSlug:              "homepage"
    localSlug:               "home"
    publicSlug:              "home"
    legacyAliases:           ["homepage", "homelab-dashboard"]
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                   "&#8962;"
    logoUrl:                "https://cdn.simpleicons.org/homeassistant/ffffff"
    badge:                  "L2 - Start"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   0
    enableVar:              "enable_homepage"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/homepage"
    setupPolicy:            "automatic"

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
    icon:                   "&#128100;"
    logoUrl:                "https://cdn.simpleicons.org/openid/ffffff"
    badge:                  "L1 - IdP"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   10
    enableVar:              "enable_pocketid"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/pocketid"
    setupPolicy:            "automatic"

    default:                 true
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
    icon:                   "&#128202;"
    logoUrl:                "https://cdn.simpleicons.org/uptimekuma/ffffff"
    badge:                  "L2 - Monitoring"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   10
    enableVar:              "enable_uptime_kuma"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/uptime-kuma"
    setupPolicy:            "automatic"

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
    icon:                   "&#128274;"
    logoUrl:                "https://cdn.simpleicons.org/openid/ffffff"
    badge:                  "L1 - ForwardAuth"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   20
    enableVar:              "enable_tinyauth"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/tinyauth"
    setupPolicy:            "automatic"

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
    icon:                   "&#129302;"
    logoUrl:                "https://cdn.simpleicons.org/httpie/ffffff"
    badge:                  "L2 - Routing test"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   20
    enableVar:              "enable_whoami"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/whoami"
    setupPolicy:            "automatic"

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
    icon:                   "&#9889;"
    logoUrl:                "https://cdn.simpleicons.org/traefikproxy/ffffff"
    badge:                  "L2 - Reverse Proxy"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   30
    enableVar:              "enable_traefik"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/traefik"
    setupPolicy:            "automatic"

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
    icon:                   "&#128640;"
    logoUrl:                "https://cdn.simpleicons.org/docker/ffffff"
    badge:                  "L2 - PaaS"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   40
    enableVar:              "enable_dokploy"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/dokploy"
    setupPolicy:            "automatic"

    default:                 false
  }
  "komodo": {
    key:                     "komodo"
    displayName:             "Komodo"
    description:             "Programmable self-hosted PaaS for Compose stack deployment through API keys"
    toolName:                "komodo"
    moduleSlug:              "komodo"
    localSlug:               "komodo"
    publicSlug:              "komodo"
    legacyAliases:           []
    identityPolicy:          "forwardauth"
    ownerProvisioningPolicy: "required"
    icon:                   "&#9881;"
    logoUrl:                "https://cdn.simpleicons.org/docker/ffffff"
    badge:                  "L2 - PaaS"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   41
    enableVar:              "enable_komodo"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/komodo"
    setupPolicy:            "automatic"

    default:                 false
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
    icon:                   "&#128171;"
    logoUrl:                "https://cdn.simpleicons.org/coolify/ffffff"
    badge:                  "L2 - PaaS"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   42
    enableVar:              "enable_coolify"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/coolify"
    setupPolicy:            "automatic"

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
    icon:                   "&#128230;"
    logoUrl:                "https://cdn.simpleicons.org/docker/ffffff"
    badge:                  "L2 - Compose Manager"
    layer:                  "L2-platform"
    section:                "Platform"
    order:                   43
    enableVar:              "enable_dockge"
    guideUrl:               "https://docs.kombify.io/guides/stackkits/services/dockge"
    setupPolicy:            "automatic"

    default:                 false
  }
}
