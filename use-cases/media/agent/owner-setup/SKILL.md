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
signed result. First-run initialization discards the server's initial-user
response and immediately sets your explicit credentials; it never uses a
default first-user password.

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
The native library is mounted read-only at `/media`; Jellyfin can scan and
stream those files but cannot edit or delete the owner-custodied media bytes.
Its configuration and transcoding cache remain writable in separate volumes.

For the existing named library volume, place one audio or video file directly
in the workspace's `media-import/` directory on the runtime host, then use:

```sh
stackkit setup media --credentials-file .stackkit/setup/media-owner.json --owner-approve --media-file media-import/clip.mp4 --json
```

Complete onboarding first, or include `--complete-onboarding` for an unfinished
wizard. The existing owner setup verifies the administrator and exact applied
container, imports the file under its content hash in `/media/Imports`, registers
that directory with Jellyfin and requests a scan. Repeating the same import
reuses identical bytes. Conflicting files and links are refused. Other libraries
are never edited. Jellyfin keeps its read-only mount and cannot delete the
imported bytes. The original input remains yours; the imported library remains
excluded from StackKits backups. A successful setup requests indexing; verify
the item and play it through the intended household client afterward.

If library creation times out or its outcome is unconfirmed, retry setup to
observe Jellyfin. The durable setup journal prevents another create, including
under a new operation ID or Plan, until the existing library is observed.
An absent response does not prove that the original create failed. Keep the
journal intact; setup resumes scanning when Jellyfin exposes the library.

This import accepts the existing local named library, not an owner-managed
host bind. Keep copying into an existing host library through its normal
storage tools. No mount or library-source transition is performed by setup.

To use an existing library on the selected Docker host, set `storage.mediaRoot`
in the StackSpec to its existing absolute directory, for example
`/srv/household-media`, before generating and applying. StackKits binds that
directory at `/media` read-only and does not create it or change its permissions.
A missing directory fails delivery. Leave the setting absent to retain the
named library volume. The selected source remains outside StackKits backups;
provide its independent recovery source. This path names storage on the runtime
host, not the machine running a remote client.
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
