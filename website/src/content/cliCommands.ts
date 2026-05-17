export type CliCommand = {
  name: string
  shortName?: string
  category: 'core' | 'lifecycle' | 'inspect' | 'addon' | 'agent' | 'release' | 'utility'
  purpose: string
}

export const cliCommands: CliCommand[] = [
  { name: 'init', category: 'core', purpose: 'Create a deployment spec (stack-spec.yaml) and initial output directory. Without arguments runs the interactive wizard.' },
  { name: 'prepare', shortName: 'prep', category: 'core', purpose: 'Prepare local or SSH target: prerequisites, Docker checks, packaged OpenTofu, spec validation, hardware checks.' },
  { name: 'generate', shortName: 'gen', category: 'core', purpose: 'Generate rollout artifacts from the spec and CUE contracts.' },
  { name: 'plan', category: 'core', purpose: 'Run an OpenTofu plan for the generated deployment.' },
  { name: 'apply', category: 'core', purpose: 'Apply generated infrastructure and optionally run verification (--verify).' },
  { name: 'verify', category: 'inspect', purpose: 'Run read-only post-deployment checks locally or over SSH (--http, --json).' },
  { name: 'remove', category: 'lifecycle', purpose: 'Destroy a StackKit deployment.' },
  { name: 'status', category: 'inspect', purpose: 'Show deployment state and service health.' },
  { name: 'validate', category: 'inspect', purpose: 'Validate stack specs, CUE files, and generated OpenTofu output where present.' },
  { name: 'addon', category: 'addon', purpose: 'Manage add-ons in stack-spec.yaml.' },
  { name: 'backup', category: 'lifecycle', purpose: 'Operate local Kopia backup flows and controller enrollment stubs.' },
  { name: 'break-glass', category: 'lifecycle', purpose: 'Inspect and rotate break-glass recovery bundles.' },
  { name: 'cluster', category: 'lifecycle', purpose: 'Manage multi-node cluster membership.' },
  { name: 'compat', category: 'inspect', purpose: 'Run a non-destructive VPS compatibility check.' },
  { name: 'doctor', category: 'inspect', purpose: 'Run local diagnostics for common StackKit issues.' },
  { name: 'agent', category: 'agent', purpose: 'Emit agent-native install plans, prompts, self-checks, and MCP config.' },
  { name: 'kit', category: 'release', purpose: 'Import, export, list, verify, upgrade, rollback, history, roundtrip, and unlock kit definitions.' },
  { name: 'logs', category: 'inspect', purpose: 'List and read structured deploy logs.' },
  { name: 'module', category: 'release', purpose: 'Release module versions and verify DB parity.' },
  { name: 'registry', category: 'release', purpose: 'Manage the embedded registry snapshot.' },
  { name: 'wizard', category: 'utility', purpose: 'Report wizard answers and free-form intents to the Admin API.' },
  { name: 'completion', category: 'utility', purpose: 'Generate shell completions.' },
  { name: 'version', category: 'utility', purpose: 'Print version, commit, build date, Go version, and OS/arch.' },
]

export const globalFlags = [
  { flag: '--verbose', short: '-v', def: 'false', purpose: 'Enable verbose output.' },
  { flag: '--quiet', short: '-q', def: 'false', purpose: 'Suppress non-essential output.' },
  { flag: '--chdir', short: '-C', def: '.', purpose: 'Change working directory before running.' },
  { flag: '--spec', short: '-s', def: 'stack-spec.yaml', purpose: 'Spec file path; kombination.yaml accepted as a read alias when default is missing.' },
  { flag: '--context', short: '', def: 'auto', purpose: 'Override node context: local, cloud, or pi.' },
  { flag: '--no-log', short: '', def: 'false', purpose: 'Disable structured deploy logging.' },
]

export const categoryLabels: Record<CliCommand['category'], string> = {
  core: 'Core workflow',
  lifecycle: 'Lifecycle',
  inspect: 'Inspect & diagnose',
  addon: 'Add-ons',
  agent: 'Agent surfaces',
  release: 'Release & catalog',
  utility: 'Utility',
}
