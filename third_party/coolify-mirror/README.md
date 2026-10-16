> **StackKits fork:** This is [upstream documentation at the pinned commit](https://github.com/mtalavi/coolify-mirror/blob/f46fa093644268759fc0802e7d098e9e45dee984/README.md).
> See [KOMBIFY-FORK.md](KOMBIFY-FORK.md) for the bundled engine and its changes.
> Upstream installation, update, workflow and lab instructions describe the
> upstream project; the bundled engine updates only with a StackKits release.

<p align="center">
  <img src="docs/banner.png" alt="Coolify Mirror: move Coolify apps between servers with one command" width="100%">
</p>

<p align="center">
  <a href="https://github.com/mtalavi/coolify-mirror/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/mtalavi/coolify-mirror?style=flat-square&color=8b5cf6"></a>
  <a href="https://github.com/mtalavi/coolify-mirror/actions/workflows/ci.yml"><img alt="ci" src="https://img.shields.io/github/actions/workflow/status/mtalavi/coolify-mirror/ci.yml?style=flat-square&label=tests"></a>
  <img alt="go" src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white">
  <img alt="coolify" src="https://img.shields.io/badge/Coolify-v4.3%20%7C%20v4.4-6366f1?style=flat-square">
  <img alt="platform" src="https://img.shields.io/badge/linux-amd64%20%7C%20arm64-22c55e?style=flat-square">
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-a1a1aa?style=flat-square"></a>
</p>

<p align="center">
  <b>Back up one domain, a few, or a whole Coolify server — then restore it on another Coolify by pasting one command.</b><br>
  Projects, environments, env vars, secrets, volumes, files, images and proxy settings arrive intact and start through Coolify itself.
</p>

<p align="center">
  <a href="https://coolify-mirror.pages.dev"><picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/demo-dark.gif">
    <img src="docs/demo-light.gif" width="100%" alt="Animated demo of a real move: on the old server one command installs coolify-mirror, a project is backed up and shared; on the new server the pasted command downloads, verifies and restores it, and the shop site answers from the new server">
  </picture></a>
</p>

<p align="center">
  <a href="https://coolify-mirror.pages.dev"><b>Website</b></a> ·
  <a href="#-quick-start--move-apps-in-4-steps"><b>Quick start</b></a> ·
  <a href="#-install">Install</a> ·
  <a href="#-walkthrough--every-screen">Walkthrough</a> ·
  <a href="#-how-it-works">How it works</a> ·
  <a href="#-command-line">CLI</a> ·
  <a href="#-safety">Safety</a> ·
  <a href="#-limitations">Limitations</a>
</p>

---

## ⚡ Quick start — move apps in 4 steps

> [!NOTE]
> **You need:** two servers with **Coolify v4.3 or v4.4, both on the same version** (the tool checks it and says which one to upgrade) · SSH as `root` or a `sudo` user on both · the new server must reach the old one on **port 443** (Coolify's proxy port — already open). On big servers, run inside `tmux` so a dropped SSH connection can't stop a long copy.

### ① Old server — make the backup

SSH into the **old** server and run:

```bash
curl -fsSL https://raw.githubusercontent.com/mtalavi/coolify-mirror/main/install.sh | sudo sh
```

The menu opens. Answer the screens like this:

| Screen | What to do |
|---|---|
| *What do you want to do?* | **Back up apps** → `enter` &nbsp;(everything at once: *Back up the whole server*) |
| *Which project should be backed up?* | `↑` `↓` to the project → `enter` — everything in it comes along &nbsp;·&nbsp; several: `space` on each, then `enter` |
| *Ready to back up* | check the list → **Start the backup** |
| *How should the other server get this backup?* | **Share a link through Coolify's proxy on port 443** |

A green line starting with `curl` appears — **copy it**. It ends with the **share code** (`203.0.113.10/hi4i-2dzx-…`). Keep this window open while the new server downloads (it shows the progress), or press `b` to keep sharing in the background for 24 h.

<img src="docs/shots/07-share.png" width="760" alt="the command for the new server">

### ② New server — restore

SSH into the **new** server and **paste the line you copied**. It looks like this:

```bash
curl -fsSL https://raw.githubusercontent.com/mtalavi/coolify-mirror/main/install.sh | sudo sh -s restore 203.0.113.10/hi4i-2dzx-hmeg-42qp-palx-52s7-zq
```

It installs the tool, downloads the backup and verifies it. **Nothing changes on this server until you answer Yes:**

| Screen | What to do |
|---|---|
| *Restore these resources into this Coolify?* | read the list → **Yes** |
| *Domains* | `enter` on each line keeps the domain — or type the new one |
| *Rebuild N application(s) once now?* (only for apps Coolify builds) | **Yes** proves the next deploy works here · on a slow connection **No** (redeploy later from Coolify) |

Wait for the green **Restore complete · everything verified**, then open the new Coolify: the project is there, running.

<img src="docs/shots/13-complete.png" width="760" alt="restore complete">

### ③ Switch over

- Point each domain's DNS **A record** to the new server's IP.
- On the old server, stop the moved apps and turn off their scheduled tasks and backups.

### ④ Free the disk space — on both servers

```bash
sudo coolify-mirror
```

**Saved files & disk space** → **All backups** → first line **ALL** → `enter` → **Yes**. On the new server, once everything works, also delete the safety copies (*Everything kept here*). Without the menu: `sudo coolify-mirror files delete --backups`.

<details>
<summary><b>Keys</b> · <b>if something goes wrong</b></summary>

| Key | Does |
|---|---|
| `↑` `↓` | move |
| `enter` | choose / continue |
| `space` | tick several lines |
| `/` | search a list |
| `esc` | back (in the main menu: quit) |
| `ctrl+c` | stop |

| You see | Do this |
|---|---|
| the Coolify versions differ | upgrade the older Coolify to the same version, then run the same command again |
| the new server can't connect | port 443 of the old server is blocked. On the old server press `q`, then *Share a saved backup* → the same backup → *Share a link on port 8123* (the tool offers to open it in `ufw`), allow 8123 in your provider's firewall too, and run the **new** command it shows |
| SSH dropped during the download | keep the old server sharing and run the same command again — the download continues where it stopped |
| the new server has no GitHub access | use the second command the old server shows (*No GitHub access there?*) |
| *Restored, but NOT operational* | the reason is printed right above it; full log in `/data/coolify-mirror/logs/` |
</details>

---

## ✨ Why

Coolify's own backup covers its database. Moving **one app** to a new server — with its database data, volumes, env vars, domains and the exact built image — is a manual afternoon. Coolify Mirror makes it one menu:

| | |
|---|---|
| 🎯 **Pick by project** | A list like Coolify's dashboard: pick a project and everything in it — apps, databases, services, domains — moves together (space to tick several). One app only: the last line. A database in another project that an app uses (via `DATABASE_URL`, etc.) is found and added for you. |
| 🔒 **Encrypted at rest and in transit** | The whole backup — `APP_KEY`, env vars, SSH keys, tokens, S3 keys, database passwords — is one [age](https://age-encryption.org)-encrypted file (passphrase, scrypt). It travels over **HTTPS with a pinned certificate**; the key and the pin live only in the link's `#fragment`. |
| 🔗 **One share code** | The old server shares the backup over HTTPS for as long as you need, through Coolify's own proxy on port 443 (TLS passthrough) or a direct port, and prints one command with a short share code for the new server. Plain HTTP is never served. |
| 🗄️ **Native database dumps** | PostgreSQL, MySQL and MariaDB are saved with `pg_dumpall` / `mysqldump` while they keep running, and loaded with the same image on the target. |
| 🧾 **Coolify's own format inside** | Every backup also carries Coolify's official *Server Transfer* bundle (`schema_version 1`, made by Coolify's exporter) and the target validates it with Coolify's own validator. |
| ⚡ **No rebuild** | The exact images your app runs are shipped. Dockerfile apps: Coolify logs *“Build step skipped”*. Docker Compose apps: started from those images without cloning or building (Coolify would rebuild them on every deploy). |
| 🧰 **Host dependencies travel too** | Files on the host that custom build/start commands or helper files use (a build policy script under `/data/coolify/ops`, …) and named `docker buildx` builders are carried and restored; system files are checked on the target before anything changes. A selective restore keeps an identical file already on the target and never replaces a different one (it stops before changing anything). |
| ✅ **SUCCESS means it really works** | A resource counts as running only when the services that ran on the source are up, stay healthy for 30 s without restarting, and each domain answers through the local Traefik. `--verify-redeploy` also rebuilds every app once on the target to prove later deploys work. |
| 🌐 **Domains last** | Right before anything starts you can keep or change every domain. |
| ♻️ **Preflight + rollback** | Exact-version check, conflict and domain detection, disk space, bundle validation and a trial import in a rolled-back transaction run **before** anything changes; a failure in the middle puts the target back the way it was. |

### Compared with copying the whole server

The usual way to move Coolify is a script that packs `/data/coolify` and the Docker volumes and unpacks them on the new server over root SSH. That is fine for copying everything onto an empty machine. The differences:

| | Copying the server (tar over SSH) | Coolify Mirror |
|---|---|---|
| What moves | Everything, always | One project, a few resources or the whole server |
| Link between the servers | A root SSH key of the old server on the new one | A share code over HTTPS on port 443, no keys between servers |
| The new server | Files are unpacked over whatever is there | Merged into a Coolify already in use, its other resources untouched (a whole-server restore needs an empty Coolify) |
| Databases | Raw data files, consistent only with Docker stopped | Native dumps while they keep running |
| Coolify version | Not checked | The same version on both servers, the tool says which one to upgrade |
| Before anything changes | A connection test | Disk space, conflicts, domains and a trial import in a rolled-back transaction |
| At the end | Nothing is checked | SUCCESS only when every resource runs as on the source and its domains answer |
| If something fails | No way back | Rollback |

---

## 🚀 Install

Two commands in total. **On the source server**, this installs the tool and opens it:

```bash
curl -fsSL https://raw.githubusercontent.com/mtalavi/coolify-mirror/main/install.sh | sudo sh
```

After the backup it prints **one command for the target server** — paste it there, it installs the tool and restores:

```bash
curl -fsSL https://raw.githubusercontent.com/mtalavi/coolify-mirror/main/install.sh | sudo sh -s restore 203.0.113.10/k7qf-2m9x-pwab-cdef-ghjk-mnpq-rs
```

The last part is the **share code**: the source address and a 128-bit secret. The share token, the TLS key of the share (Ed25519 — the target accepts only that certificate) and the key that unlocks the backup's own key are all derived from it, so nothing else has to be copied. Once sharing stops, the code is useless.

The installer picks `amd64`/`arm64`, downloads the [latest release](https://github.com/mtalavi/coolify-mirror/releases/latest), verifies its SHA-256 and installs `/usr/local/bin/coolify-mirror` (it retries GitHub hiccups). Pin a version with `… | sudo VERSION=v1.5.0 sh`, only install with `NO_RUN=1`. Later: `sudo coolify-mirror`, and `sudo coolify-mirror update` for a new release.

> [!TIP]
> A target without GitHub access: the source also prints a command that downloads the tool **from the source server itself** over the same pinned connection and restores.

<details>
<summary><b>Build from source</b></summary>

```bash
git clone https://github.com/mtalavi/coolify-mirror && cd coolify-mirror
scripts/build.sh            # → dist/coolify-mirror-linux-{amd64,arm64} + SHA256SUMS
scp dist/coolify-mirror-linux-amd64 root@SERVER:/usr/local/bin/coolify-mirror
```
Needs Go 1.26+. The binaries are static (no CGO) and have no runtime dependencies besides Docker and Coolify on the server.
</details>

---

## 🎬 Walkthrough — every screen

A real move of the `Shop` project (two apps, Redis and Postgres) from an old server to a **brand-new Coolify 4.4.2**, with the released 1.9.1 and the commands above. The screens are not edited; the lab servers use documentation IP addresses.

### On the old server

**1 · Install and start.** One command downloads the release, checks its SHA-256 and opens the menu. It also checks Docker, Coolify and its version (*✓ tested with this version*).

<img src="docs/shots/01-menu.png" width="760" alt="install and main menu">

**2 · Pick the project.** The projects as Coolify's dashboard shows them: main domain, what is inside and whether it runs. Move to the project and press `enter` — its apps, databases, services and domains all come along. Several projects: `space` on each, then `enter`. `/` searches.

<img src="docs/shots/02-select.png" width="760" alt="pick the project">

**3 · Ready to back up.** Before anything starts you see exactly what goes into the backup, project by project, and the settings. **Start the backup** — or *Change the settings* first. **Recommended** pauses containers for the few seconds their data is copied and puts the app's image in the backup, so the new server doesn't build anything ([other modes below](#backup-settings)).

<img src="docs/shots/04-options.png" width="760" alt="ready to back up">

<details>
<summary><b>One app only</b> (the last line: <i>Pick single apps instead</i>)</summary>

<br>The list then shows every resource on its own. A database the picked app uses through `DATABASE_URL` (also from another project) is found by itself — answer **Yes** and it comes along.

<img src="docs/shots/03-deps.png" width="760" alt="dependencies">

</details>

**4 · Live progress** — every step with ✓, sizes, speed and time left. Databases are dumped with their own tools while they keep running.

<img src="docs/shots/05-progress.png" width="760" alt="backup progress">

**5 · Done — now share it.** The backup is one encrypted file. Choose **Share a link through Coolify's proxy on port 443** — that port is already open.

<img src="docs/shots/06-done.png" width="760" alt="backup complete">

**6 · The command for the new server.** The green line is all the new server needs: it installs the tool and restores the **share code** at its end. Below it: the bare code with the short command for a server that already has the tool, and a fallback that fetches the tool from this server when the new one can't reach GitHub. This screen shows the download live; `b` keeps sharing in the background for 24 h.

<img src="docs/shots/07-share.png" width="760" alt="share code">

### On the new server

**7 · Paste the command.** The tool installs, connects to the old server (it accepts only that server's certificate), downloads — resumable — and verifies every checksum **before** anything changes.

<img src="docs/shots/08-paste.png" width="760" alt="paste the command">

**8 · Check, then confirm.** It tries the encryption with this Coolify and runs the whole import once in a transaction that is rolled back. Then it lists what will be added, with anything to know — and waits for your **Yes**.

<img src="docs/shots/10-confirm.png" width="760" alt="confirm the restore">

**9 · Restore, then domains — last.** Files, volumes, images and database dumps are restored and the resources are added to Coolify in one transaction. Then each domain: `enter` keeps it, or type a new one (several: comma-separated; empty: none). A Docker Compose service that had no domain is marked *(had no domain)*; taking a domain off one service and giving it to such a service (often a worker or a backup job) asks first.

<img src="docs/shots/12-domains.png" width="760" alt="restore and domains">

**10 · Running — and proven.** Coolify itself starts everything. The tool then checks the real state: the same services as on the old server, every container stable for 30 s, every domain answering through the proxy. Anything else would end as *Restored, but NOT operational* with the reason.

<img src="docs/shots/13-complete.png" width="760" alt="restore complete">

Refresh Coolify — the project is there with its environments, variables, storages, tags, scheduled tasks and backups.

### Afterwards · free the disk space (both servers)

Nothing is deleted behind your back. `sudo coolify-mirror` → **Saved files & disk space** shows what the tool keeps on the server and asks what to clean up: **All backups** (only the ready backups — made here or downloaded) or **Everything kept here** (also safety copies, old volumes and logs).

<img src="docs/shots/14-files.png" width="760" alt="saved files">

In **All backups** the first line, **ALL**, takes every backup at once — or press `enter` on one, or tick several with `space`, then `enter`.

<img src="docs/shots/16-all-backups.png" width="760" alt="all backups">

Every deletion lists what goes and needs a **Yes** (the default is *No*):

<img src="docs/shots/17-delete.png" width="760" alt="confirm delete">

<img src="docs/shots/18-freed.png" width="760" alt="space freed">

| Kind | Where | What | Delete it when |
|---|---|---|---|
| `backup` | old server | backups made here (`.cmb` + key) — the apps inside are named | the new server has restored it |
| `download` | new server | a downloaded backup whose restore did not finish (removed automatically after a successful one) | you won't restore it again |
| `unfinished` | both | interrupted backups / downloads (`.part`) | any time |
| `safety copy` | new server | `pre-restore-*` (previous Coolify database + `.env`), `replaced-*` (folders a restore replaced) | everything works |
| `old volume` | new server | `<name>.cm-old-<time>` — previous data of a volume that already existed | everything works |
| `logs`, `leftover` | both | logs of earlier runs, remains of interrupted runs | any time |

**ALL** leaves anything in use alone and deletes the rest. In use means: a backup or restore is running, a container uses the volume, or the backup is being shared from another menu window. A backup shared in the background is unshared first. Only the tool's own folder (`/data/coolify-mirror`) and its own `*.cm-old-*` volumes are ever touched. Without the menu: `coolify-mirror files`, `coolify-mirror files delete --backups`.

<details>
<summary>The built-in guide (<i>How it works</i> in the menu)</summary>

<img src="docs/shots/15-guide.png" width="760" alt="How it works">
</details>

<a id="backup-settings"></a>
<details>
<summary><b>Backup settings</b> (<i>Change the settings</i>)</summary>

| Consistency | What happens |
|---|---|
| **Pause** *(default)* | Containers using a volume are paused for the seconds it takes to copy it. Databases are dumped live and never paused. |
| **Stop** | Stopped and started again — safest, a short downtime. |
| **Live** | Nothing is touched; databases may be copied mid-write. |

| Images | What happens on the new server |
|---|---|
| **Application images** *(default)* | Apps built by Coolify start from the shipped image — no rebuild. Public images (postgres, wordpress…) are pulled. |
| **All images** | Everything is in the file — works without internet / Docker Hub. Bigger file. |
| **No images** | Smallest file; Coolify pulls everything and **rebuilds** apps from Git. |
</details>

---

## 🧠 How it works

```mermaid
flowchart LR
  subgraph S[Source server]
    A[Coolify DB<br/>rows of the picked resources] --> P
    B[Volumes + bind folders<br/>paused while copied] --> P
    C[Images<br/>docker save] --> P
    E[PostgreSQL / MySQL / MariaDB<br/>pg_dumpall · mysqldump] --> P
    O[Coolify's own transfer bundle<br/>schema_version 1] --> P
    P[[tar → zstd → age]] --> F[(backup.cmb)]
    F --> H{{temporary HTTPS link<br/>pinned self-signed cert}}
  end
  H -- "link + #key" --> D
  subgraph T[Target server]
    D[download · resume · verify] --> Q[preflight<br/>same version · conflicts · space<br/>Coolify validates the bundle]
    Q --> R[trial import<br/>in a rolled-back transaction]
    R --> X[restore files, volumes, images<br/>load database dumps]
    X --> I[import rows<br/>new IDs · re-encrypted secrets]
    I --> N[your domains]
    N --> Z[Coolify starts everything]
    Z --> V[verify: services · health 30 s<br/>domains through the proxy<br/>build dependencies · optional rebuild]
  end
```

**Selective backup** (one or more resources) exports their rows from Coolify's Postgres — application, service, database, environment variables, storages, tags, scheduled tasks, backup schedules, S3, keys, GitHub App, secret manager links (Vault, Doppler, Infisical — Coolify 4.4), deployment snapshot — plus their volumes, file mounts and images. On restore, every row gets a fresh ID, every encrypted value (env vars, passwords, keys) is decrypted with the source key and re-encrypted with the target's `APP_KEY`, and the import runs in **one transaction**.

**Full backup** takes the whole Coolify: database dump, `.env` (including `APP_KEY`), SSH keys, proxy config and certificates, and every resource's data. A full restore is only allowed onto a **fresh, empty** Coolify; it still makes a safety copy and rolls back on any error. To move resources into a Coolify that already has projects, use a selective backup — it merges.

**Databases.** PostgreSQL, MySQL and MariaDB containers (standalone or inside a service) are saved with their native dump tool while they run. On restore their data volume is created empty, initialised by the same image with the same credentials in a temporary container (no network), the dump is loaded, and only then does Coolify start the real container. Other engines (Redis, MongoDB, ClickHouse, SQLite on Coolify 4.4…) are copied as files with the chosen consistency mode. A SQLite database connected to an app is added with the app and its shared volume is saved once.

**Coolify's official format.** Coolify 4.3.23 and later ship an (internal) *Server Transfer* exporter/importer. It moves the Coolify records of a remote server between instances, but no data, and refuses the Coolify host itself. Every backup embeds that official bundle, produced by Coolify's own exporter code for the selected resources, and the target checks it with Coolify's `ServerTransferBundle::validate` and shows Coolify's own warnings (webhook URLs, credentials to refresh). The rows themselves are imported by this tool, because the official importer always creates a new server record instead of using the target's localhost.

| | Selective | Full |
|---|---|---|
| Use it for | moving / cloning projects | moving a whole server |
| Target Coolify | any — merges, existing resources are kept | **fresh and empty only** |
| Logins | target's users | source's users, API tokens, settings |
| Same resource already there | restore as copy, or skip | — |

---

## ⌨️ Command line

Everything in the menu also works headless, for scripts:

```bash
coolify-mirror list                                     # projects, resources and domains
coolify-mirror backup --project Shop,Blog               # whole projects (a name, name/env or a domain)
coolify-mirror backup --domain shop.com,blog.com        # single resources (+ dependencies)
coolify-mirror backup --all                             # every resource
coolify-mirror backup --full                            # the whole Coolify
coolify-mirror backup --domain shop.com --serve --mode proxy
coolify-mirror serve FILE.cmb --detach                  # share for 24 h in the background
coolify-mirror restore 203.0.113.10/k7qf-2m9x-…       # share code (in a terminal: the menu's restore)
coolify-mirror restore 203.0.113.10/k7qf-2m9x-… --yes # no questions
coolify-mirror restore FILE.cmb --key KEY --yes \
  --set-domain shop.com=shop.new.com --set-domain www.shop.com=-
coolify-mirror start-all                                # start anything stopped (compose apps from their images)
coolify-mirror update                                   # install the latest release (SHA-256 checked)
coolify-mirror files                                    # what the tool keeps here, with sizes
coolify-mirror files delete NAME… | --backups | --all [--yes]   # free the disk space
```

<details>
<summary><b>All flags</b></summary>

| Command | Flag | Meaning |
|---|---|---|
| `backup` | `--project p,q` / `--domain a,b` / `--uuid u` / `--all` / `--full` | what to back up (`--project`: everything in the project; its name, `name/environment` or one of its domains) |
| | `--no-deps` | don't add databases/services the apps depend on |
| | `--consistency pause\|stop\|live` | see the table above |
| | `--images apps\|all\|none` | see the table above |
| | `--include-backups` | full: also `/data/coolify/backups` |
| | `--output DIR` | default `/data/coolify-mirror/backups` |
| | `--serve --mode proxy\|direct --port 8123 --host IP --open-firewall` | share right after (HTTPS; proxy = port 443) |
| `serve` | `--key`, `--mode`, `--port`, `--host`, `--open-firewall`, `--detach`, `--ttl 24h` | share an existing file |
| `restore` | `CODE` / link / file | a share code `HOST[:PORT]/xxxx-…`, a full link, or a `.cmb` file |
| | `--key KEY` | for a file (or a link without `#key=`) |
| | `--yes` | no questions |
| | `--on-conflict copy\|skip` | resource already exists here |
| | `--team ID` | target team (selective) |
| | `--set-domain OLD=NEW` | change a domain; `NEW` = `-` removes it (repeatable) |
| | `--no-start` | restore but don't start |
| | `--verify-redeploy` | after starting, rebuild every app Coolify builds (same commit, no cache) and check it again |
| | `--keep-download` | keep the downloaded file |
| `files` | `delete NAME…` / `--backups` / `--all` / `--yes` | list, or delete, saved backups, downloads, safety copies, old volumes, logs (`--backups`: every backup not in use) |
</details>

---

## 🛡️ Safety

- **Everything sensitive is encrypted.** The backup is a single age-encrypted file (random 130-bit passphrase, scrypt): `APP_KEY`, environment variables and secrets, API tokens, private SSH keys, GitHub/GitLab app credentials, S3 access/secret keys, database users, passwords and connection strings, webhook secrets and registry logins never exist unencrypted outside the two servers. The key is written only to `<file>.key` (mode 600) and the link's `#fragment`.
- **No plain HTTP.** Shares are HTTPS only. The certificate's Ed25519 key is derived from the share code's 128-bit secret, so the downloader accepts exactly that public key (a mismatch aborts). Through Coolify's proxy the TLS connection is passed through untouched (SNI routing), so it ends in this tool, not in Traefik. The code does not contain the backup key: the target fetches it, encrypted with the code, from the running share. Sharing stops when you quit (or after `--ttl`, 24 h max in the background).
- **Same Coolify version only.** Restores require exactly the same Coolify version on both servers; the message says which server to upgrade to which version.
- **Coolify updates are checked, not trusted.** See below.
- **Full restore only onto an empty Coolify.** A target with projects, resources, extra servers or S3 storages is refused — use a selective (merge) restore.
- **Mandatory preflight.** Version, disk space, Coolify's bundle validation, conflicts (same resources, volumes, host folders, domains) and a trial import in a rolled-back transaction run before anything is written. `--yes` only skips the confirmation, never these checks.
- **Verified result.** `SUCCESS` is printed only when every restored resource runs like on the source and its domains answer through the proxy; build dependencies on the host are checked too. A resource held back by a domain clash or failing any check makes the restore end with *NOT operational* and exit code 1.
- **Rollback.** Selective: everything created is removed if the import fails (including parent folders it created). Full: the previous Coolify database and `.env` are restored and its containers started again.
- **Nothing is deleted.** Volumes that already exist on the target keep their old data in `<name>.cm-old-<time>`; replaced folders go to `/data/coolify-mirror/replaced-<time>`. They stay until you delete them in *Saved files & disk space*.
- **One run at a time.** A lock prevents two backups/restores on a server. Paused containers are always resumed — even after `Ctrl+C` or a crash.
- **Logs** of every run: `/data/coolify-mirror/logs/` (no secrets).

> [!IMPORTANT]
> Run it inside `tmux`/`screen` on big servers so an SSH disconnect doesn't interrupt a long copy.

---

## 🔄 Coolify updates

Coolify ships often, and the tool relies on parts of it (database tables, PHP classes and methods). So:

- **Before every backup and restore** it checks that this Coolify still has everything it needs — every class and method (probed inside Coolify) and every table and reference column. A missing essential part stops it **before anything changes**, naming exactly what is missing. A missing optional part makes that feature fall back to the safe path (e.g. Coolify deploys a compose app itself), and says so.
- **Schema drift is reported at backup time:** a table Coolify added that holds data of the selected resources, or a reference column this version doesn't know, is listed instead of being lost silently. New plain columns travel automatically.
- **Every new Coolify release is tested automatically.** A daily workflow ([`compat.yml`](https://github.com/mtalavi/coolify-mirror/blob/f46fa093644268759fc0802e7d098e9e45dee984/.github/workflows/compat.yml)) installs it on two fresh servers, migrates real resources (a git compose app, Postgres with data, an image app with a volume and a domain) with a share code and compares everything ([`lab/e2e.sh`](https://github.com/mtalavi/coolify-mirror/blob/f46fa093644268759fc0802e7d098e9e45dee984/lab/e2e.sh)). The result goes to `compat.json` on the `compat` branch; a failure opens an issue and a later pass closes it. A test whose servers could not even be prepared (Docker, install) publishes nothing and runs again the next day.
- **The tool reads that result:** next to the Coolify version it shows *tested* or *not tested yet*; on a version that failed, backups still work but a restore is refused with a clear message; an untested one only warns — the trial import, rollback and result verification run regardless.
- **It works:** Coolify 4.4 renamed containers and removed an internal method the tool used. The daily test caught it on 4.4.1 the day that version came out ([#18](https://github.com/mtalavi/coolify-mirror/issues/18)), and 1.9.0 supports 4.4 two days after Coolify released it.
- **`coolify-mirror update`** installs a new release; the menu says when one exists. Turn off Coolify's auto-update on both servers while you migrate.

---

## ⚠️ Limitations

- Coolify **v4.3 and v4.4** (tested with 4.3.23, 4.4.2 and 4.4.6); both servers must run **exactly the same** version (upgrade the older one first).
- MySQL/MariaDB dumps contain the user databases; extra database users created by hand (beyond the image's `MYSQL_USER`) are not recreated. PostgreSQL dumps include all roles.
- Resources running on **remote servers**, **preview deployments** and **Swarm** are not part of a selective backup.
- **GitHub webhooks**, **DNS** and **scheduled tasks/backups** still point to / run on the old server — switch them when you move. Cloudflare DNS records that Coolify 4.4 manages are not carried by a selective backup (they point at the old server); create them again from the resource's Domains page.
- Coolify 4.4 only connects to private addresses (an S3 or a secret manager on your LAN) that are allowed under *Settings → Advanced*. That is a server setting, not part of a selective backup — the restore names any such address before you confirm, so you can allow it on the new server first.
- Different CPU architecture (amd64 → arm64): shipped images can't be used, Coolify rebuilds.
- Host dependencies are found in Coolify's settings and the resource's folder on the source. A host file referenced only from inside the git repository can't be seen; `--verify-redeploy` proves (or disproves) the build on the target.
- Coolify builds Docker Compose applications on every deploy (it clones the repository even when the images are there). After a restore the tool starts them from the shipped images instead — with the compose file and `.env` Coolify generates for the target and Coolify's own start command — so a slow or offline target is no problem. Later deploys are Coolify's again, which is why build dependencies are carried and checked too.

---

## 🧪 Tested

The [walkthrough](#-walkthrough--every-screen) is a real run of the released 1.9.1: the one-line installer on both servers, a whole project (two apps, Redis and Postgres) moved to a freshly installed Coolify 4.4.2 through port 443, verified and running in 2m30s, then every backup deleted with **ALL**. Automatically, for every new Coolify release: [`lab/e2e.sh`](https://github.com/mtalavi/coolify-mirror/blob/f46fa093644268759fc0802e7d098e9e45dee984/lab/e2e.sh) (two fresh servers, official installer, a git compose app + Postgres + an image app with a volume and domains, backup → share code → restore, data compared through the proxy) — passing on 4.4.6 (1.9.3), 4.4.2 (1.9.0) and on 4.3.23 (1.5.0 to 1.8.1; 1.9.0 was checked on 4.3.23 by hand: five resources in three projects, the compose app started without a build). On Coolify 4.4.2 by hand: a SQLite database connected to an app and an app reading its secrets from HashiCorp Vault, moved through port 443 (321 SQLite rows, the Vault secret resolved on the new server once its address was allowed — the tool named it before the restore), restored a second time as a copy (the secret manager reused), and a whole-server move of five resources onto a fresh 4.4.2 (the compose app started from the restored images, every row and secret identical). By hand on real Coolify 4.3.23 installs (lab in [`lab/`](https://github.com/mtalavi/coolify-mirror/tree/f46fa093644268759fc0802e7d098e9e45dee984/lab)): **a real migration with zero manual fixes** — healthy source → new backup → freshly installed Coolify → selective and full restore with `--verify-redeploy` (a multi-service compose app whose build uses a host policy script and a named buildx builder, a Dockerfile app, an image app, WordPress + MariaDB, Postgres): `SUCCESS`, the same secrets, volume data and database rows as the source, every domain answering through Traefik, and a full rebuild on the target; merge into an existing Coolify without touching its other resources; an app that exits and one that turns unhealthy after the restore reported as *NOT operational*; rollback leaving the target exactly as before; backups made by 1.2.0. Also: native Postgres/MariaDB dumps (identical row counts and checksums after restore), HTTPS share through Traefik passthrough with pin check (a wrong pin is refused, plain HTTP gets nothing), full restore refused on a non-empty Coolify and accepted on a fresh one, Postgres/MariaDB/Redis data, WordPress, compose apps, Git apps without rebuild, copies next to originals (a single-container app restored as a copy is checked under its new name), saved-file cleanup on both servers (all backups at once, some, or one; a backup shared in the background is unshared first; one shared from another window and a volume in use are refused), restore into a fresh never-used Coolify, full server restore with rollback (fault injection), interrupted backups, domain changes at the end, plus unit tests for Laravel encryption, the archive format, the import planner and resumable downloads.

---

## 🧩 Project layout

```
cmd/coolify-mirror     CLI + entry point
internal/ui            interactive menu (Bubble Tea, huh, lipgloss)
internal/engine        backup, share, fetch, selective/full restore, start, domains, saved files
internal/dbx           export + import planner for Coolify's database
internal/coolify       Coolify detection, psql, PHP runner, Laravel encryption
internal/archive       tar + zstd + age stream format
internal/transfer      share server + resumable download
lab/                   Docker lab with real Coolify servers
site/                  the website (static, Cloudflare Pages): coolify-mirror.pages.dev
```

Contributions and issues are welcome. **Not affiliated with Coolify / coolLabs** — “Coolify” is their trademark.

## 📄 License

[MIT](LICENSE)
