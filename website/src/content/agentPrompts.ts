import basekitAutonomousRollout from './agent-prompts/basekit-autonomous-rollout.md?raw'
import inspectExistingRollout from './agent-prompts/inspect-existing-rollout.md?raw'
import diagnoseFailedRollout from './agent-prompts/diagnose-failed-rollout.md?raw'
import sshRollout from './agent-prompts/ssh-rollout.md?raw'

export type PromptScope = 'mutates' | 'read-only' | 'remote'

export type AgentPrompt = {
  id: string
  title: string
  summary: string
  scopes: PromptScope[]
  shortPrompt: string
  fullPrompt: string
  markdown: string
  markdownPath: string
}

const extractBlock = (markdown: string, heading: string): string => {
  const escaped = heading.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const match = markdown.match(new RegExp(`## ${escaped}\\s+\\x60\\x60\\x60text\\s+([\\s\\S]*?)\\s+\\x60\\x60\\x60`, 'm'))
  return match?.[1].trim() ?? ''
}

const definePrompt = (
  id: string,
  title: string,
  summary: string,
  scopes: PromptScope[],
  markdown: string,
): AgentPrompt => ({
  id,
  title,
  summary,
  scopes,
  shortPrompt: extractBlock(markdown, 'Short prompt'),
  fullPrompt: extractBlock(markdown, 'Full prompt'),
  markdown,
  markdownPath: `/getting-started/agents/${id}.md`,
})

export const agentPrompts: AgentPrompt[] = [
  definePrompt(
    'basekit-autonomous-rollout',
    'Autonomous BaseKit rollout',
    'End-to-end non-interactive deploy on a fresh host. init → prepare → validate → generate → plan → apply → verify.',
    ['mutates'],
    basekitAutonomousRollout,
  ),
  definePrompt(
    'inspect-existing-rollout',
    'Inspect an existing rollout',
    'Read-only triage of the current workspace. Service health, manifest, evidence, and edit-detection.',
    ['read-only'],
    inspectExistingRollout,
  ),
  definePrompt(
    'diagnose-failed-rollout',
    'Diagnose a failed rollout',
    'Triage broken applies — collect logs, classify the failure, propose a recovery plan, do not mutate.',
    ['read-only'],
    diagnoseFailedRollout,
  ),
  definePrompt(
    'ssh-rollout',
    'Generate and apply through SSH',
    'Drive a remote BaseKit rollout from the operator workstation without leaking secrets onto the target.',
    ['mutates', 'remote'],
    sshRollout,
  ),
]

export const agentPromptById = (id: string): AgentPrompt | undefined =>
  agentPrompts.find((p) => p.id === id)
