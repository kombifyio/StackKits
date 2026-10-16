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

The existing compiled route/TinyAuth policy remains authoritative. Audiobookshelf
has native OIDC, but setup does not invent a PocketID client or claim a verified
OIDC/household route. Configure and verify that separate identity contract before
claiming per-app SSO. Never expose a native token through URL parameters, logs,
receipts or an agent surface.
