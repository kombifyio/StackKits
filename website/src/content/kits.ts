export type KitStatus = 'stable' | 'alpha'

export type KitFeature = {
  title: string
  body: string
  icon: string
}

export type Kit = {
  id: 'base' | 'modern' | 'ha'
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
      { title: 'Browser-native links', body: 'Generated services open at `*.home.localhost` URLs — no hosts file, no trust store work, no port suffixes.', icon: 'language' },
      { title: 'Identity + dashboard out of the box', body: 'PocketID, TinyAuth, Homepage, Uptime Kuma, and Vaultwarden are wired and routed from the first apply.', icon: 'verified_user' },
      { title: 'Photos via Immich', body: 'Immich + Immich-ML + Postgres + Redis ship with explicit on-demand setup in the verified local path.', icon: 'photo_library' },
      { title: 'Backup ready', body: 'Local Kopia backup flows and a backup controller stub are installed with the CLI.', icon: 'cloud_sync' },
      { title: 'Agent-first surfaces', body: 'llms.txt, OpenAPI, JSON schemas, MCP connector, and prompt Markdown ship in every release.', icon: 'smart_toy' },
    ],
    services: ['Coolify default PaaS', 'Komodo beta-supported alternative', 'Dokploy draft adapter', 'PocketID', 'TinyAuth', 'Homepage dashboard', 'Uptime Kuma', 'Vaultwarden via PaaS', 'Immich (Photos) via PaaS', 'stackkit-server (Node Hub API)'],
    notSuitableFor: ['Multi-node clusters with quorum failover (use HA Kit when graduated)', 'Hybrid cloud-plus-local deployments (use Modern Home Lab when graduated)'],
  },
  {
    id: 'modern',
    name: 'Modern Home Lab',
    status: 'alpha',
    statusLabel: 'alpha · scaffolding',
    tagline: 'Hybrid local + cloud direction. Preview only.',
    description:
      'Modern Home Lab is the in-progress StackKit for hybrid local-plus-cloud deployments. Its rollout matrix and one-click apply path are still being implemented — the CUE definitions are packaged for preview work, but production rollout via this kit is not yet supported.',
    oneLiner: 'stackkit init modern-homelab',
    initCommand: 'stackkit init modern-homelab',
    features: [
      { title: 'Hybrid by design', body: 'Targets a mix of local home-lab nodes and cloud-managed services.', icon: 'cloud_sync' },
      { title: 'CUE-first definitions', body: 'Same CUE contract source as BaseKit, packaged for inspection and preview.', icon: 'integration_instructions' },
      { title: 'Definition preview', body: 'Use to read, validate, and experiment with the modern composition.', icon: 'visibility' },
    ],
    services: ['Definitions only — no verified rollout path in current release'],
    notSuitableFor: ['Production deployments today (use BaseKit)', 'One-click first-run installs (matrix scenarios not yet implemented)'],
  },
  {
    id: 'ha',
    name: 'High Availability Kit',
    status: 'alpha',
    statusLabel: 'alpha · scaffolding',
    tagline: 'Cluster-first redundancy. Preview only.',
    description:
      'The High Availability Kit defines a cluster-first composition with redundancy, quorum, and failover for self-hosted production workloads. The contract is packaged in the release, but the rollout matrix and production gates are not yet implemented.',
    oneLiner: 'stackkit init ha-kit',
    initCommand: 'stackkit init ha-kit',
    features: [
      { title: 'Cluster-first', body: 'Multi-node membership, quorum, and failover modeled in CUE.', icon: 'hub' },
      { title: 'Redundant identity & storage', body: 'Replicated identity and storage layers across cluster members.', icon: 'lan' },
      { title: 'Preview only', body: 'Apply path remains scaffolding until production gates land.', icon: 'science' },
    ],
    services: ['Cluster membership contracts', 'Replication primitives (preview)'],
    notSuitableFor: ['Production deployments today (use BaseKit)', 'Single-node homelab installs (use BaseKit)'],
  },
]

export const kitById = (id: string): Kit | undefined => kits.find((k) => k.id === id)
