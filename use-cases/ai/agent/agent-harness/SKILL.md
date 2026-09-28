---
name: agent-harness
description: Help the owner use OpenHands installed by StackKits as the Private AI agent harness - prepare the host's gVisor runtime, open the private route, point the agent at a pulled Ollama model, understand what the sandbox does and does not isolate, and what is backed up - without weakening the isolation or exposing OpenHands publicly.
---

# Agent harness

StackKits installs OpenHands Agent Canvas as the Private AI agent-harness
module (workload `ai-harness`). The agent's shell, editor and automations run
inside one container that the node runs under gVisor (`runsc`). It is reached
only on the private route `https://ai-harness.<domain>` behind the kit's
login; the OpenHands session key is generated inside its settings volume.

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

## Model

The first start points the agent at the node's Ollama
(`ollama_chat/qwen3.5:9b` at `http://ollama:11434`). The owner pulls that
model in Open WebUI first, or picks another pulled model in the OpenHands
settings. No external key is configured; do not suggest adding a cloud
provider key unless the owner asks for it explicitly.

## What the sandbox does and does not do

- The agent runs with a user-space kernel between it and the host; it has no
  Docker socket, no host path and no owner home. Never suggest mounting the
  socket, a home directory or running without `runtime: runsc`.
- The agent shares its container with the OpenHands server: it can read the
  OpenHands settings and conversations. It has outbound network access and
  reaches Ollama (and Open WebUI's API when chat is selected).
- The route is never public; Cloud Kit gives it no route at all.

## Backup

The settings volume (conversations, automations, settings) and the workspace
(`/projects`) are backed up. Package caches are not; images are pulled again.

## What is not available

Goose is not installable (no self-hostable web surface yet). Running the
agent's own Docker containers, a public route and GPU passthrough are not
part of this module.
