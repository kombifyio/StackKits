---
name: game-server
description: Operate the owner's Pterodactyl game servers installed by StackKits (Minecraft Java, Paper and Bedrock, Terraria, Valheim) - create a curated server, share join details, manage the allow list, power and backups - without exposing administrative credentials.
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

Password games take the owner's chosen join password instead of an allow list:

```json
{"profile":"valheim","name":"Family Valheim","acceptEula":true,"password":"<owner-chosen>"}
```

| Profile | Port | Access |
| --- | --- | --- |
| `minecraft-java` | TCP 25565 | allow list of Minecraft account names |
| `minecraft-paper` | TCP 25566 | allow list of Minecraft account names |
| `minecraft-bedrock` | UDP 19132 (RakNet transport) | allow list of Xbox gamertags |
| `terraria` | TCP 7777 | join password |
| `valheim` | UDP 2456-2457 | join password; off the public list and crossplay |

Minecraft profiles start with online mode, an allow list, no RCON and no
automatic operator rights. A join password has 8 to 20 characters and is never
part of the server name. Never repeat a password back into chat or logs.

## Join details

Players on the home network join with the node's LAN address and the profile's
port. A home node is not reachable from the internet unless the owner
deliberately selects a managed VPS or bridge; never suggest router port
forwarding as a default.

## Routine operations

Use the StackKits game operations; they reach the Panel with keys derived from
owner custody, so you never handle a Panel key:

| MCP tool | CLI | Effect |
| --- | --- | --- |
| `stackkit_game_list` | `stackkit game list --json` | Servers with identifier, name, state and port |
| `stackkit_game_power` | `stackkit game power <server> --signal start\|stop\|restart --owner-approve` | Power change; returns the observed state |
| `stackkit_game_allow` | `stackkit game allow <server> --player <name> --owner-approve` | Admits a player and reads the allow list back |

Power and allow-list changes need the owner's explicit approval. Terraria and
Valheim have no account allow list; they use the join password chosen at
setup. Backups and restores stop and restart running servers themselves.
Use the Panel UI for anything else; do not run arbitrary console commands or
grant operator rights without an explicit owner request.
