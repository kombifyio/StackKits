---
name: audiobook-library
description: Set up the selected Audiobookshelf audiobook library and verify native listening and household progress without changing existing accounts or media custody.
---

# Audiobook library

Select the existing `media-audiobooks` workload separately from Jellyfin video
or Navidrome music. `storage.mediaRoot` supplies the read-only `/audiobooks`
mount. The owner retains media custody and its backup; application config and
metadata use the existing native backup lifecycle. Setup never uploads media,
changes host paths, imports a database or resets existing credentials.

After the workload is applied, run:

```sh
stackkit setup --workload media-audiobooks
```

Automatic owner setup uses the existing owner identity and private credential
writer. To verify an existing native administrator, keep that account's exact
username/password in the private workspace-relative file
`.stackkit/setup/media-audiobooks-owner.json`; use `--credentials-file` to select
another protected file. Never print or commit it. A wrong password fails closed.
First registration uses Audiobookshelf's native first-root guard, never a default
login or password-reset fallback. There is no separate onboarding flag.

Setup checks the applied server version and the actual active administrator,
reuses a book library bound solely to `/audiobooks` or creates it if absent,
and requests the native scan. It never rewrites an existing library; ambiguous
or broader bindings fail closed. Newly created libraries allow audiobooks only
and disable scripted EPUB content. The short-lived API session is logged out.

A signed setup result proves owner/library preparation. For listening, use the
ordinary upstream client and an explicit regular household account, restricted
to the intended libraries and permissions. Stream actual audio, verify playback
and progress/bookmarks through the application's API, and confirm that a new
application container retains those effects on the same native data volumes.
Setup or a healthy service alone does not prove that journey.

The compiled route/TinyAuth policy remains authoritative. Native standalone Compose Apply
registers the separate `stackkit-media-audiobooks` Pocket ID client. Setup uses
its custodied secret, asks the application to discover the home issuer through
its trusted CA mount, and configures OIDC through the upstream admin API. A
different established provider/client fails closed to preserve account links.
New subjects become ordinary users; mutable username/email matching and
automatic administrator mapping remain disabled. The existing local root
account keeps its password and library state for recovery. A previous applied
bundle without the OIDC declaration keeps its native login until regenerated
and applied.

Limit a household member with `stackkit user apps <username> --app
media-audiobooks --owner-approve`; this replaces their complete app list. The
`media` grant alone does not grant Audiobookshelf. Preserve other intended
grants explicitly. Verify the actual browser/mobile sign-in and permissions
before claiming household access. Setup readback alone is not SSO acceptance.
Never expose a native token through URL parameters, logs, receipts or an agent
surface.
