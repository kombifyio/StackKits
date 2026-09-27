# Media sign-in and Jellyfin upgrade qualification

Implementation status: source implemented; real-host fresh setup, browser sign-in,
existing-install upgrade and full restore qualification are **pending**. This is a
release qualification hold for the Jellyfin 12.1 catalog change. Do not publish
this pin to existing Media installations until the lane below passes. A generic
successful Kopia checkpoint is not sufficient evidence of full Jellyfin recovery.

Normal Apply and Advanced reconcile enforce this hold before changing Pocket ID,
persisting runtime files or starting Compose. A fresh 12.1 installation and a
same-major reapply remain available. Existing data without matching persisted
runtime custody is refused, as is any major upgrade or downgrade. The full stopped
restore flow remains the only admitted way to return a migrated database to its
matching prior runtime; there is no migration-evidence or bypass flag.

## Governed sign-in

Media selects Jellyfin `12.1` (server API `12.1.0`) and the maintained
[K0lin SSO plugin 5.1.1](https://github.com/K0lin/jellyfin-plugin-sso/releases/tag/v5.1.1).
The CLI packages the exact upstream archive; its CUE SHA-256 and closed renderer
must match the embedded bytes. Apply mounts its three assemblies read-only,
without a download, install script, plugin repository or automatic updater.
Jellyfin owns writable plugin metadata and account links on its `/config` volume.
The [packaged provenance](../internal/jellyfinsso/README.md) records the source,
license and digest. Older plugin builds are not an alternative: the maintained
5.1 series fixes SSO-created accounts having an empty local password.

Apply registers `stackkit-media` as a confidential PKCE client in Pocket ID,
restricted to the governed owners, admins and household groups. The exact HTTPS
callbacks are `/sso/OID/redirect/pocketid` and `/sso/OID/r/pocketid` on the Media
origin. The application uses the existing home identity network access and a
public-plus-private CA bundle; issuer, endpoint and TLS checks remain enabled.

For selected Media workloads, Apply, change-set apply and Advanced reconcile run
the admitted Go setup automatically, including startup completion and SSO binding.
The local recovery credentials are retained owner-only in
`.stackkit/setup/media-owner.json`; first setup derives the username from signed
owner custody and generates a private random password. Retries reuse that file.
A changed Plan re-verifies setup with the same owner. If an established owner's
credentials are unavailable, restore the actual username/password to that file
and retry `stackkit setup media --owner-approve --complete-onboarding`; setup
never resets an existing password. The major-version guard above runs before
this automation, without a bypass.

The Go owner-setup action authenticates explicit application credentials, verifies
the administrator and startup completion, then configures the installed plugin
through its elevated API. The immutable owner Pocket ID subject from signed
custody links to that exact Jellyfin administrator ID. Existing links and unrelated
provider settings survive; conflicting administrator links fail before mutation.
The `groups` claim grants application administration only to `owners` and `admins`;
`household` admits ordinary users. The immutable `sub` claim keys new accounts,
so their initial display usernames may be subject identifiers. Existing household
accounts can use the plugin's authenticated account-linking flow to keep history.
No shared account or generated personal decryption key is introduced.

Use `https://<media-origin>/sso/OID/start/pocketid` for browser sign-in after owner
setup. App-local owner credentials remain the recovery path. Setup does not reset
passwords or promote unrelated Pocket ID users. A setup retry reads back converged
settings without rewriting them. For a changed established provider configuration,
quiesce Media sign-in while running owner setup: the upstream API replaces a whole
provider object and offers no compare-and-swap operation. Verification and backups
do not mutate plugin configuration.

TinyAuth admission remains in front of Media. Browser SSO does not establish native
TV/mobile client compatibility through that perimeter. Quick Connect is the
application's intended device-linking path, but each client still needs verified
network access through the existing route; no automatic TV login is claimed.

## Mandatory existing-install acceptance lane

[Jellyfin's 12.0 migration guidance](https://jellyfin.org/posts/jellyfin-release-12.0/)
applies to the 12.1 pair: direct upgrade from 10.10.7 is supported, the databases
change, a full library scan is required, and rollback requires a full restore.
The following is required evidence, not a claim that it has already passed:

1. Install the published prior StackKits release and its exact Jellyfin 10.10.7 pin.
   Seed distinct owner and household users, a real library item, playback/resume
   history, user policies and existing plugin configuration. Record application
   IDs and data digests without recording credentials or tokens.
2. Before stopping it, enumerate every Jellyfin username through the authenticated
   API. Compare names case-insensitively; **block** on collisions (for example
   `Alex` and `alex`) and resolve explicitly on the prior server before proceeding.
3. Stop Jellyfin and prove it is stopped. Inventory the exact Docker config/data
   volume mounts and any externally configured database/data paths. Back up the
   complete `/config` volume, including all database files, plugin configuration
   and links; include every additional data path from that inventory. Keep the
   library's owner-custodied backup and the StackKits workspace/identity custody.
   Record a restorable, durable backup and verify its files before changing images.
   Cache is disposable; the media library remains read-only and is not implicitly
   included in StackKits application backups.
4. Inventory installed plugins. If an older SSO plugin is present, preserve its
   configuration in the stopped backup and move its assemblies outside the live
   plugin directory while stopped; do not load two SSO assemblies together.
   With the normal sealed upgrade checkpoint/rollback authority in place, install
   the candidate pair and allow database migration to finish without interruption.
   Run and finish the full library scan. Confirm server version `12.1.0`, plugin
   GUID `505ce9d1-d916-42fa-86ca-673ef241d7df`, active version `5.1.1`/`5.1.1.0`,
   unchanged user IDs/history/library, and the application's owner recovery login.
5. Run owner setup, then real owner/admin and household browser sign-ins. Check
   exact callback/PKCE exchange, private-CA trust, owner-to-existing-user linkage,
   ordinary household policy, nonmember denial, nonempty local-password protection
   for newly created SSO users, and idempotent apply/setup. Exercise one interrupted
   setup/retry without losing links. Capture only secret-free outcomes.
6. Prove rollback on the isolated lane: stop 12.1 completely; preserve failure
   evidence; restore the full stopped prior config/data backup **as a unit**, the
   matching prior workspace/checkpoint authority and exact prior runtime pin.
   Never start the old image against a 12.x database or mix old/new database files.
   Restart the prior release, verify the seeded accounts, library and playback
   state, and verify original identity/CA/secret digests. A failed downgrade without
   restore is not rollback evidence.

Only after both forward migration and restore pass may the release owner remove
this hold. Source and deterministic tests cannot substitute for these DB and client
compatibility observations.
