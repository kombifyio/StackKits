---
name: agent-control-plane
description: Help the owner use Paperclip installed by StackKits as the Private AI agent control plane - prepare the host's gVisor runtime, sign in behind the private route, connect the installed Hermes assistant as an agent, keep budgets at zero and no cloud key by default, understand what the sandbox does and does not isolate, and what is backed up - without weakening the isolation or exposing Paperclip publicly.
---

# Agent control plane

StackKits installs Paperclip as the Private AI agent-control-plane module
(workload `ai-control-plane`): an org chart of agents with budgets, approvals
and tasks, plus its own PostgreSQL in the same bundle. Paperclip starts the
agents of its local adapters as child processes of its own container, so the
node runs that container under gVisor (`runsc`). It is reached only on the
private route `https://ai-control-plane.<domain>` behind the kit's login, and
then asks for Paperclip's own login.

## Prepare the host

The module refuses to install on a node whose Docker daemon has not
registered the `runsc` runtime. On Debian or Ubuntu the owner runs the host
fix once, as root, before Apply:

```sh
sudo stackkit host remediate --apply gvisor-runsc --yes
```

It installs gVisor from its apt source (signing key pinned in StackKits),
adds `runtimes.runsc` to `/etc/docker/daemon.json` without touching other
settings, and restarts Docker (which restarts every container). On other
distributions follow https://gvisor.dev/docs/user_guide/install/ and register
`runsc` by hand; `stackkit host preflight` shows the `sandbox-runtime` check.

## First sign-in

Paperclip runs in `authenticated` + `private` mode. The first account the
owner creates behind the kit's login becomes the instance admin; every later
sign-up on the same route is a further board member. Do not suggest switching
to `local_trusted` (no login) or `public` exposure. The route host is bound
to Paperclip's allowed hostnames at apply time; another hostname is refused
by Paperclip itself.

## Agents and models

Budgets default to zero for the company and every agent, and no cloud
provider key is configured; the container has no egress, so the Claude
Code, Codex, Gemini and Kimi adapters cannot reach their providers. The real
local integration is the installed Hermes assistant (Private AI assistant
module): create an agent with adapter type `hermes_gateway`, `apiBaseUrl`
`http://hermes:9119`, once the owner has enabled the Hermes API server in
Hermes (that is a Hermes grant the owner makes, not a StackKits default;
Hermes answers with the node's Ollama model). OpenHands has no Paperclip
adapter; the generic `http` adapter is the only way to call another agent
service. Do not suggest adding a cloud provider key unless the owner asks
for it explicitly and understands that the bundle has no egress.

## What the sandbox does and does not do

- Paperclip and every process it starts run with a user-space kernel between
  them and the host; there is no Docker socket, no host path and no owner
  home. Never suggest mounting the socket, a home directory or running
  without `runtime: runsc`.
- An agent process shares the container with the Paperclip server: it can
  read the Paperclip home (config, uploaded files, the secrets master key)
  and reach PostgreSQL on the bundle's network and everything on the private
  AI network (Ollama, Hermes, Open WebUI's API when chat is selected).
- The route is never public; Cloud Kit gives it no route at all.

## Backup

The Paperclip home (`/paperclip`: instance config, secrets master key,
uploaded files, agent workspaces, adapter CLI homes) and the PostgreSQL
volume are backed up together after quiescence. npm and CLI caches live
inside the home volume and come along; images are pulled again.

## What is not available

Langflow is not installable (recorded). Running the agents' own Docker
containers, remote sandbox providers, a public route, cloud provider keys and
plugin installs from npm are not part of this module.
