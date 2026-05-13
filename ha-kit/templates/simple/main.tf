# High Availability Kit - OpenTofu Configuration (Scaffolding)
# Status: PLANNED - Not yet implemented
# Note: Advanced mode (Terramate) is recommended for HA deployments

# This file will contain the OpenTofu configuration for deploying
# a High Availability Kubernetes cluster with distributed storage.

# TODO: Implement the following modules:
# - Multi-master k3s with embedded etcd
# - MetalLB load balancer
# - Longhorn distributed storage
# - Velero backup system

terraform {
  required_version = ">= 1.6.0"
}

# Placeholder - implementation pending
output "status" {
  value = "scaffolding"
  description = "This StackKit is under development"
}

output "recommendation" {
  value = "Use 'advanced' mode with Terramate for HA deployments"
  description = "Recommended deployment mode"
}
