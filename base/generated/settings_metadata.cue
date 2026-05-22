// =============================================================================
// GENERATED FILE - DO NOT EDIT DIRECTLY
// =============================================================================
// This file is auto-generated from the kombify-admin database.
// To modify settings classification, update the database and re-run the generator.
//
// Generated: 2026-05-22T08:50:42.488Z
// Source: kombify-admin/prisma/seed.ts → Setting table
// =============================================================================

package base

// =============================================================================
// SETTINGS TYPE DEFINITIONS
// =============================================================================

#SettingType: "perma" | "flexible"

#SettingClassification: {
  layer:             "1" | "2" | "3"
  path:              string
  name:              string
  settingType:       #SettingType
  description?:      string
  whyClassification?: string
  changeMethod?:     string
  defaultValue?:     _
  cueType?:          string
}

// =============================================================================
// SETTINGS CLASSIFICATION REGISTRY
// =============================================================================

#SettingsRegistry: {
  // Perma-settings: Immutable after deployment
  perma: {
    // Layer 1: Foundation
    layer1: [
      {
        layer:             "1"
        path:              "identity.lldap.domain.base"
        name:              "LLDAP Base DN"
        settingType:       "perma"
        description:      "LDAP base distinguished name"
        whyClassification: "All user/group references use this; changing invalidates all identity lookups"
        cueType:          "string"
      },
      {
        layer:             "1"
        path:              "identity.stepCA.pki.rootCommonName"
        name:              "Root CA Name"
        settingType:       "perma"
        description:      "Root certificate authority common name"
        whyClassification: "Changing requires complete PKI rebuild and re-issuing all certificates"
        cueType:          "string"
      },
      {
        layer:             "1"
        path:              "security.firewall.backend"
        name:              "Firewall Backend"
        settingType:       "perma"
        description:      "Firewall management backend"
        whyClassification: "ufw vs iptables vs nftables have incompatible rule formats; migration is manual"
        cueType:          "\"ufw\" | \"iptables\" | \"nftables\""
      },
      {
        layer:             "1"
        path:              "security.ssh.port"
        name:              "SSH Port"
        settingType:       "perma"
        description:      "SSH daemon listening port"
        whyClassification: "Changing requires firewall rules update, client reconfiguration, and potential lockout"
        cueType:          "int & >=1 & <=65535"
      },
    ]

    // Layer 2: Platform
    layer2: [
      {
        layer:             "2"
        path:              "network.defaults.subnet"
        name:              "Network Subnet"
        settingType:       "perma"
        description:      "Docker network subnet"
        whyClassification: "All containers use this; changing requires network recreation"
        cueType:          "string"
      },
      {
        layer:             "2"
        path:              "paas.type"
        name:              "PAAS Type"
        settingType:       "perma"
        description:      "Platform-as-a-Service selection"
        whyClassification: "Migrating applications between Coolify/Komodo/Dokploy requires manual export/import and adapter-specific evidence"
        cueType:          "\"coolify\" | \"komodo\" | \"dokploy\""
      },
      {
        layer:             "2"
        path:              "platform"
        name:              "Platform Type"
        settingType:       "perma"
        description:      "Container orchestration platform"
        whyClassification: "Migration from docker to swarm/k8s requires workload evacuation and complete redeployment"
        cueType:          "\"docker\" | \"docker-swarm\" | \"kubernetes\" | \"bare-metal\""
      },
    ]
  }

  // Flexible-settings: Can be changed via Day-2 operations
  flexible: {
    // Layer 1: Foundation
    layer1: [
      {
        layer:             "1"
        path:              "packages.extra"
        name:              "Extra Packages"
        settingType:       "flexible"
        description:      "Additional system packages to install"
        changeMethod:     "terramate run -- tofu apply"
        cueType:          "[...string]"
      },
      {
        layer:             "1"
        path:              "system.timezone"
        name:              "System Timezone"
        settingType:       "flexible"
        description:      "System timezone configuration"
        changeMethod:     "terramate run -- tofu apply"
        cueType:          "string"
      },
    ]

    // Layer 2: Platform
    layer2: [
      {
        layer:             "2"
        path:              "network.defaults.domain"
        name:              "Domain"
        settingType:       "flexible"
        description:      "Base domain for services"
        changeMethod:     "terramate run -- tofu apply (updates Traefik rules)"
        cueType:          "string"
      },
      {
        layer:             "2"
        path:              "paas.dokploy.version"
        name:              "Dokploy Version"
        settingType:       "flexible"
        description:      "Dokploy container image version"
        changeMethod:     "terramate run -- tofu apply"
        cueType:          "string"
      },
      {
        layer:             "2"
        path:              "platformIdentity.tinyauth.enabled"
        name:              "TinyAuth Enabled"
        settingType:       "flexible"
        description:      "Enable TinyAuth authentication proxy"
        changeMethod:     "terramate run -- tofu apply"
        cueType:          "bool"
      },
    ]
  }
}
