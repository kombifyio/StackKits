export type KitStatus = 'stable' | 'alpha'

export type KitFeature = {
  title: string
  body: string
  icon: string
}

export type Kit = {
  id: 'base'
  name: string
  status: KitStatus
  statusLabel: string
  tagline: string
  description: string
  oneLiner: string
  initCommand: string
  features: KitFeature[]
  services: string[]
  notSuitableFor: string[]
}

export const kits: Kit[] = [
  {
    id: 'base',
    name: 'BaseKit',
    status: 'stable',
    statusLabel: 'verified beta',
    tagline: 'Single-environment default. The one-command path.',
    description:
      'BaseKit is the verified beta single-environment StackKit. It bootstraps Docker, packaged OpenTofu, identity, dashboard, and the platform baseline on a fresh Ubuntu VM or host, and exposes everything over browser-native `*.home.localhost` links. Product-bundled L3 applications are PaaS-intended by default; Coolify remains the default PaaS while Komodo is the beta-supported alternative. Dokploy remains draft. User-installed apps outside that path are state-unmanaged.',
    oneLiner: 'curl -sSL https://base.stackkit.cc | sh',
    initCommand: 'stackkit init base-kit',
    features: [
      { title: 'One-command install', body: 'A single shell command bootstraps Docker, OpenTofu, the CLI, and the kit catalog.', icon: 'rocket_launch' },
      { title: 'Browser-native links', body: 'Generated services open at target-local `*.home.localhost` URLs — no hosts file, no trust store work, no port suffixes.', icon: 'language' },
      { title: 'Identity + dashboard out of the box', body: 'PocketID, TinyAuth, Homepage, Uptime Kuma, and Vaultwarden are wired and routed from the first apply.', icon: 'verified_user' },
      { title: 'Photos via Immich', body: 'Immich + Immich-ML + Postgres + Redis ship with explicit on-demand setup in the verified local path.', icon: 'photo_library' },
      { title: 'Backup ready', body: 'Local Kopia backup flows and a backup controller stub are installed with the CLI.', icon: 'cloud_sync' },
      { title: 'Agent-first surfaces', body: 'llms.txt, OpenAPI, JSON schemas, MCP connector, and prompt Markdown ship in every release.', icon: 'smart_toy' },
    ],
    services: ['Coolify default PaaS', 'Komodo beta-supported alternative', 'Dokploy draft adapter', 'PocketID', 'TinyAuth', 'Homepage dashboard', 'Uptime Kuma', 'Vaultwarden via PaaS', 'Immich (Photos) via PaaS', 'stackkit-server (Node Hub API)'],
    notSuitableFor: ['Multi-node clusters with quorum failover', 'Hybrid cloud-plus-local deployments'],
  },
]

export const kitById = (id: string): Kit | undefined => kits.find((k) => k.id === id)
