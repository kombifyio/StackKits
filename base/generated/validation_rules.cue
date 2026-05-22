// =============================================================================
// GENERATED FILE - DO NOT EDIT DIRECTLY
// =============================================================================
// This file is auto-generated from the kombify-admin database.
// To modify validation rules, update the database and re-run the generator.
//
// Generated: 2026-05-22T08:50:42.481Z
// Source: kombify-admin/prisma/seed.ts → ValidationRule table
// =============================================================================

package base

// =============================================================================
// VALIDATION RULE DEFINITIONS
// =============================================================================

#ValidationRuleCode: "L1_FIREWALL_REQUIRED" | "L1_LLDAP_REQUIRED" | "L1_SSH_PORT_VALID" | "L1_STEPCA_REQUIRED" | "L2_NETWORK_DEFAULTS" | "L2_PAAS_TYPE_VALID" | "L2_PLATFORM_DECLARED" | "L3_SERVICE_LAYER_LABEL" | "L3_SERVICE_MANAGED_BY"

#ValidationRuleSeverity: "error" | "warning" | "info"

#ValidationRule: {
  code:         #ValidationRuleCode
  layer:        "1" | "2" | "3"
  fieldPath:    string
  ruleType:     "required" | "type" | "constraint" | "custom"
  cueExpression: string
  errorMessage: string
  hint?:        string
  severity:     #ValidationRuleSeverity
}

// =============================================================================
// VALIDATION RULES REGISTRY
// =============================================================================

#ValidationRulesRegistry: {
  // Layer 1: Foundation Rules
  layer1: [
    {
      code:          "L1_FIREWALL_REQUIRED"
      layer:         "1"
      fieldPath:     "security.firewall.enabled"
      ruleType:      "required"
      cueExpression: "security.firewall.enabled == true"
      errorMessage:  "Firewall must be enabled for security"
      hint:          "Set security.firewall.enabled: true"
      severity:      "error"
    },
    {
      code:          "L1_LLDAP_REQUIRED"
      layer:         "1"
      fieldPath:     "identity.lldap.enabled"
      ruleType:      "required"
      cueExpression: "identity.lldap.enabled == true"
      errorMessage:  "LLDAP must be enabled for Zero-Trust architecture"
      hint:          "Set identity.lldap.enabled: true in your stack configuration"
      severity:      "error"
    },
    {
      code:          "L1_SSH_PORT_VALID"
      layer:         "1"
      fieldPath:     "security.ssh.port"
      ruleType:      "constraint"
      cueExpression: "security.ssh.port >= 1 & security.ssh.port <= 65535"
      errorMessage:  "SSH port must be between 1 and 65535"
      hint:          "Use a valid port number, typically 22 or a high port like 2222"
      severity:      "error"
    },
    {
      code:          "L1_STEPCA_REQUIRED"
      layer:         "1"
      fieldPath:     "identity.stepCA.enabled"
      ruleType:      "required"
      cueExpression: "identity.stepCA.enabled == true"
      errorMessage:  "Step-CA must be enabled for mTLS certificate management"
      hint:          "Set identity.stepCA.enabled: true in your stack configuration"
      severity:      "error"
    },
  ]

  // Layer 2: Platform Rules
  layer2: [
    {
      code:          "L2_NETWORK_DEFAULTS"
      layer:         "2"
      fieldPath:     "network.defaults"
      ruleType:      "required"
      cueExpression: "network.defaults != _|_"
      errorMessage:  "Network defaults must be configured"
      hint:          "Configure network.defaults with domain and subnet"
      severity:      "error"
    },
    {
      code:          "L2_PAAS_TYPE_VALID"
      layer:         "2"
      fieldPath:     "paas.type"
      ruleType:      "constraint"
      cueExpression: "paas.type =~ \"^(dokploy|coolify|komodo)$\""
      errorMessage:  "PAAS type must be one of: coolify, komodo, dokploy"
      hint:          "Use Coolify by default, or explicitly select Komodo/Dokploy where the adapter is supported"
      severity:      "error"
    },
    {
      code:          "L2_PLATFORM_DECLARED"
      layer:         "2"
      fieldPath:     "platform"
      ruleType:      "required"
      cueExpression: "platform != _|_"
      errorMessage:  "Platform type must be explicitly declared"
      hint:          "Set platform: \"docker\" | \"docker-swarm\" | \"kubernetes\" | \"bare-metal\""
      severity:      "error"
    },
  ]

  // Layer 3: Application Rules
  layer3: [
    {
      code:          "L3_SERVICE_LAYER_LABEL"
      layer:         "3"
      fieldPath:     "services[*].labels[\"stackkit.layer\"]"
      ruleType:      "required"
      cueExpression: "services[_].labels[\"stackkit.layer\"] != _|_"
      errorMessage:  "All services must have stackkit.layer label"
      hint:          "Add labels: {\"stackkit.layer\": \"3-application\"} to your service"
      severity:      "warning"
    },
    {
      code:          "L3_SERVICE_MANAGED_BY"
      layer:         "3"
      fieldPath:     "services[*].labels[\"stackkit.managed-by\"]"
      ruleType:      "required"
      cueExpression: "services[_].labels[\"stackkit.managed-by\"] != _|_"
      errorMessage:  "All services must have stackkit.managed-by label"
      hint:          "Add labels: {\"stackkit.managed-by\": \"selected-paas\"} to your service"
      severity:      "warning"
    },
  ]
}
