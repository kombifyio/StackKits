export type WorksWithItem = {
  name: string
  detail: string
  mark?: string
  href?: string
}

export const worksWithRail: WorksWithItem[] = [
  { name: 'CUE', detail: 'Source of truth for contracts', mark: 'C', href: 'https://cuelang.org' },
  { name: 'OpenTofu', detail: 'Packaged provisioning engine', mark: 'T', href: 'https://opentofu.org' },
  { name: 'Docker', detail: 'Container runtime', mark: 'D', href: 'https://docker.com' },
  { name: 'Traefik', detail: 'Reverse proxy & routing', mark: 'T', href: 'https://traefik.io' },
  { name: 'Coolify', detail: 'Platform & app manager', mark: 'C', href: 'https://coolify.io' },
  { name: 'Komodo', detail: 'Explicit PaaS alternative', mark: 'K', href: 'https://komo.do' },
  { name: 'Dokploy', detail: 'Explicit PaaS alternative', mark: 'D', href: 'https://dokploy.com' },
  { name: 'PocketID', detail: 'OIDC identity provider', mark: 'P', href: 'https://pocket-id.org' },
  { name: 'TinyAuth', detail: 'Edge auth proxy', mark: 'T', href: 'https://github.com/steveiliop56/tinyauth' },
  { name: 'Immich', detail: 'Photos & media', mark: 'I', href: 'https://immich.app' },
  { name: 'Vaultwarden', detail: 'Self-hosted secrets', mark: 'V', href: 'https://github.com/dani-garcia/vaultwarden' },
  { name: 'Uptime Kuma', detail: 'Health monitoring', mark: 'U', href: 'https://uptime.kuma.pet' },
  { name: 'Homepage', detail: 'Service dashboard', mark: 'H', href: 'https://gethomepage.dev' },
  { name: 'Kopia', detail: 'Backup engine', mark: 'K', href: 'https://kopia.io' },
]
