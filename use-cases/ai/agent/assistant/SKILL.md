---
name: assistant
description: Help the owner use the Hermes Agent personal assistant installed by StackKits in Private AI - reach the private dashboard, pick the local model, understand the default inspect-and-draft tools, grant messaging, web search, schedules or external writes deliberately, and know what is backed up - without weakening the pinned policy.
---

# Personal assistant

StackKits installs Hermes Agent as the Private AI assistant module (workload
`ai-assistant`, experimental, one owner). It talks only to the node's Ollama.
Its dashboard is reached on the private route `https://ai-assistant.<domain>`
behind the kit's login; Hermes then asks for its own login: user `owner`, the
password from `stackkit secrets reveal --workload ai-assistant --slot dashboard-password`.

## First chat

1. Pull a model in Open WebUI (or `ollama pull`). The seeded default is
   `qwen3.5:9b`; another pulled model is set with `model.default` in the
   dashboard or `hermes config`.
2. Open the dashboard and chat. Memory, skills and sessions persist under
   HERMES_HOME and are backed up.

## What the assistant may do

By default it inspects and drafts: it reads and writes files in its own
workspace, uses its skills, memory, notes and past sessions. It has no
terminal, web, browser, schedule or messaging tool until the owner grants one.

Grants are toolsets the owner adds per platform in `platform_toolsets`
(dashboard, `hermes tools` or the seeded `config.yaml`):

| Grant | Toolset | Note |
| --- | --- | --- |
| Web search and pages | `web` | Search runs through the node's SearXNG; select the Private AI web-search module first, otherwise search fails. |
| Browser | `browser` | Headless Chromium inside the container. |
| Schedules | `cronjob` | Jobs run unattended; dangerous commands are denied there. |
| Commands | `terminal` | Runs inside the assistant's container as an unprivileged user; dangerous commands ask the owner. |
| Messaging | `hermes gateway setup` | Configure a platform token; its tools follow `platform_toolsets`. |
| External writes and third-party services | `send_message`, `tts`, `image_gen`, `skills_hub`, `vision` | Check the data flow before granting. |

Never suggest editing `/etc/hermes/config.yaml`: it is the StackKits policy
(local inference only, local terminal backend, dangerous commands always ask,
no anonymous web endpoints) and is re-installed at every start. Do not
suggest a Docker socket, host mounts or a public route.

## After a restore

Every schedule comes back paused. The owner resumes the ones still wanted
with `hermes cron resume <id>`.
