# StackKit Settings Classification

> **Version:** 1.0
> **Status:** Production Ready

This document defines the classification of all StackKit settings as either **Perma** (immutable after deployment) or **Flexible** (can be changed via Day-2 operations).

## Classification Principles

### Perma-Settings

Settings that are **immutable after initial deployment**. Changing these requires significant manual intervention, data migration, or complete redeployment.

**Characteristics:**
- Changing affects many dependent components
- Requires manual intervention or complex migration
- May cause data loss or service disruption
- Often involves cryptographic material or identity roots

### Flexible-Settings

Settings that **can be changed via Day-2 operations** using `terramate run -- tofu apply` or similar IaC commands.

**Characteristics:**
- Isolated impact (single service or component)
- Can be changed without data loss
- No manual intervention required
- Changes are automatically applied by IaC

---

## Layer 1: Foundation Settings

### Perma-Settings (Immutable)

| Path | Name | Default | Why Immutable |
|------|------|---------|---------------|
| `security.ssh.port` | SSH Port | `22` | Changing requires firewall rules update, client reconfiguration, and potential lockout |
| `security.firewall.backend` | Firewall Backend | `ufw` | ufw vs iptables vs nftables have incompatible rule formats; migration is manual |
| `security.firewall.defaultInbound` | Default Inbound Policy | `deny` | Security policy foundation; changing could expose services unintentionally |
| `identity.lldap.domain.base` | LLDAP Base DN | `dc=homelab,dc=local` | All user/group references use this; changing invalidates all identity lookups |
| `identity.lldap.admin.email` | LLDAP Admin Email | - | Used for initial admin account; changing requires manual user migration |
| `identity.stepCA.pki.rootCommonName` | Root CA Name | `StackKits Root CA` | Changing requires complete PKI rebuild and re-issuing all certificates |
| `identity.stepCA.pki.rootOrganization` | Root CA Organization | - | Embedded in all certificates; requires PKI rebuild to change |
| `identity.stepCA.provisioners` | CA Provisioners | - | Changing authentication methods affects all certificate issuance |

### Flexible-Settings (Day-2 Changeable)

| Path | Name | Default | Change Method |
|------|------|---------|---------------|
| `system.timezone` | System Timezone | `UTC` | `terramate run -- tofu apply` |
| `system.locale` | System Locale | `en_US.UTF-8` | `terramate run -- tofu apply` |
| `system.hostname` | Hostname | - | `terramate run -- tofu apply` (requires reboot) |
| `system.swap` | Swap Configuration | `auto` | `terramate run -- tofu apply` |
| `system.unattendedUpgrades` | Auto Updates | `security` | `terramate run -- tofu apply` |
| `packages.base` | Base Packages | - | `terramate run -- tofu apply` |
| `packages.extra` | Extra Packages | `[]` | `terramate run -- tofu apply` |
| `security.ssh.permitRootLogin` | Root SSH Login | `no` | `terramate run -- tofu apply` |
| `security.ssh.passwordAuth` | SSH Password Auth | `false` | `terramate run -- tofu apply` |
| `security.ssh.maxAuthTries` | SSH Max Auth Tries | `3` | `terramate run -- tofu apply` |
| `security.firewall.rules` | Firewall Rules | - | `terramate run -- tofu apply` (additive changes safe) |
| `security.secrets.backend` | Secrets Backend | `file` | `terramate run -- tofu apply` (requires secret migration) |
| `security.tls.minVersion` | TLS Minimum Version | `1.2` | `terramate run -- tofu apply` |
| `identity.lldap.enabled` | LLDAP Enabled | `true` | Cannot disable after users created |

---

## Layer 2: Platform Settings

### Perma-Settings (Immutable)

| Path | Name | Default | Why Immutable |
|------|------|---------|---------------|
| `platform` | Platform Type | `docker` | Migration from docker to swarm/k8s requires workload evacuation and complete redeployment |
| `network.defaults.driver` | Network Driver | `bridge` | Changing requires recreating all container networks and redeploying services |
| `network.defaults.subnet` | Network Subnet | `172.20.0.0/16` | All containers use this; changing requires network recreation |
| `paas.type` | PAAS Type | `dokploy` | Migrating applications between Dokploy/Coolify requires manual export/import |
| `paas.installMethod` | PAAS Install Method | `container` | Changing from container to bare-metal requires complete reinstallation |
| `container.engine` | Container Engine | `docker` | Docker vs Podman have different socket paths and behaviors |
| `container.rootless` | Rootless Mode | `false` | Requires complete reinstallation and permission changes |
| `container.storageDriver` | Storage Driver | - | Changing loses all container data; requires complete rebuild |

### Flexible-Settings (Day-2 Changeable)

| Path | Name | Default | Change Method |
|------|------|---------|---------------|
| `network.defaults.domain` | Domain | `local` | `terramate run -- tofu apply` (updates Traefik rules) |
| `network.dns.servers` | DNS Servers | `["1.1.1.1", "8.8.8.8"]` | `terramate run -- tofu apply` |
| `network.ntp.enabled` | NTP Enabled | `true` | `terramate run -- tofu apply` |
| `network.vpn.enabled` | VPN Enabled | `false` | `terramate run -- tofu apply` |
| `container.liveRestore` | Live Restore | `true` | `terramate run -- tofu apply` (Docker daemon restart) |
| `container.logDriver` | Log Driver | `json-file` | `terramate run -- tofu apply` (new containers only) |
| `container.networkDriver` | Default Network Driver | `bridge` | `terramate run -- tofu apply` (new networks only) |
| `paas.dokploy.version` | Dokploy Version | `latest` | `terramate run -- tofu apply` |
| `paas.dokploy.traefik.enabled` | Dokploy Traefik Integration | `true` | `terramate run -- tofu apply` |
| `platformIdentity.tinyauth.enabled` | TinyAuth Enabled | `false` | `terramate run -- tofu apply` |
| `platformIdentity.tinyauth.version` | TinyAuth Version | `v3` | `terramate run -- tofu apply` |
| `platformIdentity.tinyauth.logLevel` | TinyAuth Log Level | `info` | `terramate run -- tofu apply` |
| `ingress.traefik.enabled` | Traefik Enabled | `true` | Cannot disable if services depend on it |
| `ingress.traefik.version` | Traefik Version | `v3.1` | `terramate run -- tofu apply` |
| `ingress.traefik.dashboard` | Traefik Dashboard | `true` | `terramate run -- tofu apply` |

---

## Layer 3: Application Settings

### General Principle

> **All Layer 3 settings are Flexible** because they are managed by the PAAS (Dokploy/Coolify), not by Terraform.

Applications are deployed and configured through the PAAS dashboard, not through IaC. This means:

1. **Configuration changes** are made in Dokploy/Coolify UI
2. **Scaling** is managed by the PAAS
3. **Environment variables** are set per-application in PAAS
4. **Resource limits** are configured in PAAS

### Service-Level Settings (Managed by PAAS)

| Service | Settings Location | Change Method |
|---------|------------------|---------------|
| Uptime Kuma | Dokploy Dashboard | PAAS UI |
| Whoami | Dokploy Dashboard | PAAS UI |
| Beszel | Dokploy Dashboard | PAAS UI |
| Custom Applications | Dokploy Dashboard | PAAS UI |

---

## Change Procedures

### Changing Flexible Settings

```bash
# 1. Update the stack-spec.yaml or kombination.yaml
# 2. Run validation
cue vet ./base/... ./base-kit/...

# 3. Apply changes
# Simple mode:
tofu plan
tofu apply

# Advanced mode:
terramate run -- tofu plan
terramate run -- tofu apply
```

### Changing Perma Settings (Not Recommended)

**Warning:** Changing perma-settings requires careful planning and may cause data loss.

1. **Backup all data** before attempting changes
2. **Plan migration path** for dependent services
3. **Schedule maintenance window** for service disruption
4. **Test in base-kit** before production changes

#### Example: Changing LLDAP Base DN

```bash
# 1. Export all users and groups
ldapsearch -x -H ldap://localhost:3890 -D "cn=admin,dc=homelab,dc=local" -w password -b "dc=homelab,dc=local" > backup.ldif

# 2. Stop all services depending on LLDAP
docker stop tinyauth pocketid

# 3. Recreate LLDAP with new base DN
# (Requires modifying stack-spec and redeploying)

# 4. Import users with modified DNs
# (Requires manual LDIF editing)

# 5. Update all services with new LDAP configuration
# (TinyAuth, PocketID, etc.)

# 6. Restart services
docker start tinyauth pocketid
```

---

## Decision Matrix

Use this matrix to determine if a setting change is safe:

| Question | Yes | No |
|----------|-----|-----|
| Does it affect cryptographic material? | **Perma** | Continue |
| Does it affect identity (users, groups, DN)? | **Perma** | Continue |
| Does it require data migration? | **Perma** | Continue |
| Does it affect multiple services? | **Perma** | Continue |
| Can IaC handle the change automatically? | **Flexible** | **Perma** |
| Can it be rolled back without data loss? | **Flexible** | **Perma** |

---

## Related Documentation

- [ARCHITECTURE.md](./ARCHITECTURE.md) - 3-Layer architecture overview
- [ADR-0003-paas-strategy.md](./ADR/ADR-0003-paas-strategy.md) - PAAS selection rationale
