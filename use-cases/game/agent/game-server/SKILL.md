---
name: game-server
description: Operate the owner's Pterodactyl game servers installed by StackKits (Minecraft Java and Bedrock) - create a curated server, share join details, manage the allow list, power and backups - without exposing administrative credentials.
---

# Game server

StackKits installs Pterodactyl (Panel and Wings) on the owner's node and keeps
its credentials in owner custody. Game servers run as containers owned by
Wings; the Panel at the workload's route (`https://game.<domain>`) is the
owner's management surface.

## Create a server

Creating a server is an owner-approved setup action. The owner must accept the
game's EULA personally; never accept it on their behalf.

```sh
stackkit setup game --owner-approve --credentials-file .stackkit/setup/game.json --json
```

```json
{"profile":"minecraft-java","name":"Family Survival","acceptEula":true,"allowList":["PlayerOne"]}
```

Profiles: `minecraft-java` (TCP 25565) and `minecraft-bedrock` (UDP 19132,
RakNet transport). Both start with online mode, an allow list, no RCON and no
automatic operator rights.

## Join details

Players on the home network join with the node's LAN address and the profile's
port. A home node is not reachable from the internet unless the owner
deliberately selects a managed VPS or bridge; never suggest router port
forwarding as a default.

## Routine operations

Use the Pterodactyl Client API (`/api/client`) with the owner's client key for
state, resources, power signals, allow-list edits and bounded file edits. Do
not use the Application API, arbitrary console commands or operator grants
without an explicit owner request.
