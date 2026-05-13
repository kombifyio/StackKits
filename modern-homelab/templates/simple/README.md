# Modern Homelab - Simple Template

> **Status: TODO** - Template structure defined, implementation pending

This template deploys a basic modern-homelab setup with:
- 1 cloud node (VPS with public IP)
- N local nodes (on-premises, connected via VPN)

## Prerequisites

- SSH key pair
- Domain/DNS is optional (only needed if you enable public ingress + ACME)

## Files (TODO)

- `main.tf` - Cloud node provisioning
- `variables.tf` - Input variables
- `outputs.tf` - Output values
- `docker.tf` - Docker installation
- `coolify.tf` - Coolify setup
- `headscale.tf` - VPN coordination setup

## Usage

```bash
tofu init
tofu plan -var-file=terraform.tfvars
tofu apply -var-file=terraform.tfvars
```

## Networking Standard

This StackKit follows the StackKits local-first standard:
- Always provide an IP/port access path (works in every network)
- Use mDNS `HOSTNAME.local` as a convenience (never rely on `.local` subdomains)
- Only enable ACME/Let’s Encrypt when a real domain exists
