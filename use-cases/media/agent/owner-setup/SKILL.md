---
name: media-owner-setup
description: Prepare or verify a native Jellyfin administrator and guide private household playback without exporting credentials.
---

# Media administrator

## Owner setup

For an applied native `standalone-compose` Media workload, create the private
file `.stackkit/setup/media-owner.json` with your chosen credentials:

```json
{
  "username": "<your administrator name>",
  "password": "<your unique password>"
}
```

Restrict the file to its workspace owner. Pass its relative path to the CLI or
MCP; keep the credential values out of command arguments and chat messages.

```sh
stackkit setup media --credentials-file .stackkit/setup/media-owner.json --owner-approve --json
```

The command verifies the current Plan, signed Apply, exact local container and
pinned Jellyfin version. During first run it sets your chosen administrator
credentials. On an existing installation it performs a normal login and
administrator readback. It revokes its temporary session before recording the
signed result. It does not read or use a default first-user password.

Without `--complete-onboarding`, an unfinished Jellyfin startup wizard remains
unfinished. Open Media at its declared private route to choose library paths,
language and household settings. Alternatively, if you want to complete startup
with the current application settings and configure your libraries afterward:

```sh
stackkit setup media --credentials-file .stackkit/setup/media-owner.json --owner-approve --complete-onboarding --json
```

The completion flag only finishes Jellyfin's startup wizard. It does not add
libraries, change remote-access settings, discover GPU devices or configure
transcoding. Follow the Plan's media volume mapping when adding a library.
Household viewers should receive separate non-administrator accounts through
Jellyfin. Connect each playback client to the declared Media URL and sign in
there; the setup session is not a client credential.

Test playback of an owner-provided media file from an intended client. Check
direct play and, only when explicitly configured, transcoding. Container health
and successful administrator setup do not prove client compatibility, library
content, bandwidth, GPU performance or recovery. Keep the application config
backup and media-byte coverage visible separately; excluded or independently
custodied media needs its own recovery source.

Retry an interrupted setup with its reported `--operation-id`. The CLI rechecks
the application state and never reopens an already completed wizard. Other
deployment adapters retain their declared instructions until an executable
native setup adapter exists. Jellyfin owns a REST API, not a product MCP server.
