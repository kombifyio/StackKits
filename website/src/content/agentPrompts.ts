export type AgentPrompt = {
  id: string
  title: string
  summary: string
  mdPath: string
}

export const agentPrompts: AgentPrompt[] = [
  {
    id: 'basekit-autonomous-rollout',
    title: 'Autonomous BaseKit rollout',
    summary: 'End-to-end non-interactive deployment: init → prepare → validate → generate → plan → apply → verify.',
    mdPath: '/getting-started/agents/basekit-autonomous-rollout.md',
  },
  {
    id: 'inspect-existing-rollout',
    title: 'Inspect an existing rollout',
    summary: 'Read state, service health, and the node-local manifest without mutating anything.',
    mdPath: '/getting-started/agents/inspect-existing-rollout.md',
  },
  {
    id: 'diagnose-failed-rollout',
    title: 'Diagnose a failed rollout',
    summary: 'Triage broken applies — collect logs, surface root cause, propose a recovery plan.',
    mdPath: '/getting-started/agents/diagnose-failed-rollout.md',
  },
  {
    id: 'enable-monitoring-addon',
    title: 'Enable the monitoring add-on',
    summary: 'Idempotent flow to enable optional monitoring on an existing BaseKit deployment.',
    mdPath: '/getting-started/agents/enable-monitoring-addon.md',
  },
  {
    id: 'ssh-rollout',
    title: 'Generate and apply over SSH',
    summary: 'Drive a remote target without copying secrets onto the controlling host.',
    mdPath: '/getting-started/agents/ssh-rollout.md',
  },
]

export const agentPromptById = (id: string) => agentPrompts.find((p) => p.id === id)
