---
name: media-music-library
description: Set up the selected native Navidrome music library without exposing administrator or playback credentials.
---

# Music library

Select `media-music` in addition to the existing Media workload. Set
`storage.mediaRoot` to an existing readable music directory on the execution
host before generating. The native module mounts it read-only at `/music`;
StackKits does not create, repair or own the external library. Its independent
backup/recovery source is required. Navidrome's own persistent `/data` volume
contains its native application database and is included in module backups.

Apply runs the declared owner setup automatically using the local owner
projection and a stable private generated credential document. For an existing
installation, retain its administrator credentials in the owner-only workspace
file `.stackkit/setup/media-music-owner.json`:

```json
{"username":"<existing administrator>","password":"<private password>"}
```

```sh
stackkit setup media-music --credentials-file .stackkit/setup/media-music-owner.json --owner-approve --json
```

Setup uses the Plan's exact applied native container. It logs in or uses
Navidrome's guarded first-administrator endpoint, verifies the same administrator,
the applied version and the existing `/music` library, then requests a native
scan. It never replaces an existing password or silently adds another library.
An existing-owner authentication failure requires that owner's retained
credentials; setup does not reset the account. There is no separate startup
wizard, so omit `--complete-onboarding`.

Navidrome owns playback and Subsonic clients. Use separate non-administrator
household accounts in the application; administrator setup credentials are not
household/client credentials. Browser routes retain the compiled TinyAuth and
PocketID protection. Navidrome has no native PocketID OIDC implementation; do
not claim an OIDC client or forward a trusted-user header from an untrusted
client. Full route and household acceptance requires its own current receipt.

Play an actual indexed file through the intended client, observe its decoded
audio and application playback state, then verify that state after recreation.
Health, setup and scan acknowledgement alone do not prove playback, persistence,
transcoding, client compatibility or recovery. The temporary setup JWT stays
inside the call and is discarded; upstream provides no server-side JWT logout,
so do not claim that it was remotely revoked.
