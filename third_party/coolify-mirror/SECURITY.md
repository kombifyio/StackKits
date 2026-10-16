# Security policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems.
Report them privately through GitHub: **Security → Report a vulnerability**
(<https://github.com/mtalavi/coolify-mirror/security/advisories/new>).

Include the version (`coolify-mirror version`), the Coolify version and steps to
reproduce. You will get an answer within a few days; fixes are released as a new
version and credited in the advisory unless you prefer otherwise.

## Supported versions

Only the latest release receives security fixes.

## Scope notes

- Backup files (`.cmb`) contain your Coolify configuration and secrets
  (`APP_KEY`, env vars, SSH keys, tokens, database passwords). They are encrypted
  with [age](https://age-encryption.org) using a random passphrase. Anyone who
  has **both** the file and its key can read them.
- The key is saved next to the backup as `<backup>.key`, readable by root only.
  Treat it like a password and delete old backups you no longer need
  (`coolify-mirror files`).
- Sharing is **HTTPS only**. Plain HTTP is never served, and `http://` links are
  refused by the target.
- A share code (`HOST[:PORT]/xxxx-…`) carries a 128-bit secret. The share token,
  the share's TLS key (Ed25519: the target accepts only that certificate) and the
  key that unlocks the backup's key are all derived from it. The backup key
  itself is not in the code, so the code is useless once sharing stops.
- Older full links keep the key and the certificate pin in the `#fragment`,
  which is never sent to the server.
- Shares are temporary. On the share screen, `q` stops sharing. A share kept in
  the background (`b` on that screen, or `serve --detach`) stops by itself after
  24 hours (`--ttl` changes this).
