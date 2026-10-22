# Shared installer recovery and host-bootstrap helpers.
# Inlined into all website entrypoints; sourced beside explicitly invoked local
# scripts. Shell journals never replace native Apply recovery or node custody.

installer_journal_read() {
  [ -f "${INSTALLER_JOURNAL:-}" ] || return 0
  awk -F '\t' -v key="$1" '$1 == key { sub(/^[^\t]*\t/, ""); print; exit }' "$INSTALLER_JOURNAL"
}

installer_journal_write() {
  _journal_key="$1"
  _journal_value="$2"
  # Plain data only: never source/evaluate a saved journal.
  case "$_journal_value" in
    *"$(printf '\t')"*|*'
'*) printf 'Installer journal values must be single-line text.\n' >&2; return 1 ;;
  esac
  _journal_temp=$(mktemp "$INSTALLER_JOURNAL_DIR/.journal.XXXXXX") || return 1
  if [ -f "$INSTALLER_JOURNAL" ]; then
    awk -F '\t' -v key="$_journal_key" '$1 != key' "$INSTALLER_JOURNAL" > "$_journal_temp"
  fi
  printf '%s\t%s\n' "$_journal_key" "$_journal_value" >> "$_journal_temp"
  if [ -n "${INSTALLER_JOURNAL_OWNER:-}" ] && [ "$(id -u)" -eq 0 ]; then
    chown "$INSTALLER_JOURNAL_OWNER" "$_journal_temp"
  fi
  chmod 600 "$_journal_temp"
  mv -f "$_journal_temp" "$INSTALLER_JOURNAL"
}

installer_journal_begin() {
  # Core may run under sudo while its journal belongs to the invoking user.
  # Keep the recipient explicit; workspace journals default to their caller.
  INSTALLER_JOURNAL_OWNER="${3:-}"
  INSTALLER_JOURNAL_DIR="$2"
  mkdir -p "$INSTALLER_JOURNAL_DIR"
  INSTALLER_JOURNAL_DIR=$(cd "$INSTALLER_JOURNAL_DIR" && pwd -P)
  if [ -n "$INSTALLER_JOURNAL_OWNER" ] && [ "$(id -u)" -eq 0 ]; then
    [ "$INSTALLER_JOURNAL_DIR" = "$2" ] || {
      printf 'Installer journal directory is redirected: %s. Keep the original recovery directory; remove its redirection before retrying ownership recovery.\n' "$2" >&2
      return 1
    }
    chown -h "$INSTALLER_JOURNAL_OWNER" "$INSTALLER_JOURNAL_DIR"
  fi
  INSTALLER_JOURNAL="$INSTALLER_JOURNAL_DIR/$1.tsv"
  INSTALLER_PREVIOUS_STATE=""
  if [ -f "$INSTALLER_JOURNAL" ]; then
    [ -r "$INSTALLER_JOURNAL" ] || {
      printf 'Cannot read installer journal: %s. Its private recovery state belongs to another OS user. Keep it and re-run the same installer with sudo for the original invoking user.\n' "$INSTALLER_JOURNAL" >&2
      return 1
    }
    [ "$(installer_journal_read schema)" = "stackkit.installer-journal/v1" ] || {
      printf 'Unsupported installer journal: %s. Inspect it before retrying.\n' "$INSTALLER_JOURNAL" >&2
      return 1
    }
    INSTALLER_PREVIOUS_STATE=$(installer_journal_read state)
    if [ "$INSTALLER_PREVIOUS_STATE" != complete ]; then
      printf 'Resuming after installer phase %s; native checks re-observe the target.\n' "$(installer_journal_read phase)"
    fi
  else
    installer_journal_write schema stackkit.installer-journal/v1
  fi
  installer_journal_write state running
}

installer_phase() { installer_journal_write phase "$1"; }
installer_complete() { installer_journal_write state complete; }
installer_interrupted() {
  [ -n "${INSTALLER_JOURNAL:-}" ] || return 0
  [ "$1" -eq 0 ] && return 0
  [ "$(installer_journal_read state)" = complete ] && return 0
  installer_journal_write state interrupted || true
  printf 'Install interrupted during %s. Re-run the same installer with the same workspace; journal: %s\n' "$(installer_journal_read phase)" "$INSTALLER_JOURNAL" >&2
}

installer_workspace_begin() {
  mkdir -p "$HOMELAB_DIR"
  HOMELAB_DIR=$(cd "$HOMELAB_DIR" && pwd -P)
  export HOMELAB_DIR
  installer_journal_begin workspace "$HOMELAB_DIR/.stackkit/installer"
  INSTALLER_SAVED_KIT=$(installer_journal_read kit)
  # Intent saved before init also survives interruption before a spec exists.
  case "$INSTALLER_SAVED_KIT" in
    basement-kit) STACKKIT_PLACEMENT=home; export STACKKIT_PLACEMENT ;;
    cloud-kit) STACKKIT_PLACEMENT=cloud; export STACKKIT_PLACEMENT ;;
    *)
      if [ "${STACKKIT_PLACEMENT+x}" != x ]; then
        STACKKIT_PLACEMENT=$(installer_journal_read placement)
        export STACKKIT_PLACEMENT
      fi
      ;;
  esac
  # Recover omitted authoring inputs, including an explicitly empty selection
  # saved before init. Supplied environment values always take precedence.
  if [ "${STACKKIT_USE_CASES+x}" != x ] && [ "$(installer_journal_read use_cases_set)" = 1 ]; then
    STACKKIT_USE_CASES=$(installer_journal_read use_cases)
    export STACKKIT_USE_CASES
  fi
  if [ "${DOMAIN+x}" != x ]; then DOMAIN=$(installer_journal_read domain); export DOMAIN; fi
  if [ "${STACKKIT_ADMIN_EMAIL+x}" != x ]; then STACKKIT_ADMIN_EMAIL=$(installer_journal_read owner_email); export STACKKIT_ADMIN_EMAIL; fi
  if [ "${STACKKIT_OWNER_USERNAME+x}" != x ]; then STACKKIT_OWNER_USERNAME=$(installer_journal_read owner_username); export STACKKIT_OWNER_USERNAME; fi
  if [ "${STACKKIT_NAME+x}" != x ]; then STACKKIT_NAME=$(installer_journal_read name); export STACKKIT_NAME; fi
  if [ "${STACKKIT_PLATFORM+x}" != x ]; then STACKKIT_PLATFORM=$(installer_journal_read platform); export STACKKIT_PLATFORM; fi
  trap 'installer_workspace_exit "$?"' EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}

installer_workspace_exit() {
  _workspace_exit_code="$1"
  installer_interrupted "$_workspace_exit_code"
  if [ -n "${STACKKIT_SERVER_IMAGE_DIR:-}" ]; then
    rm -rf "$STACKKIT_SERVER_IMAGE_DIR"
  fi
  return "$_workspace_exit_code"
}

installer_save_inputs() {
  installer_journal_write domain "${DOMAIN_VALUE:-${DOMAIN:-}}"
  installer_journal_write name "${STACK_NAME:-}"
  installer_journal_write owner_email "${ADMIN_EMAIL:-${STACKKIT_ADMIN_EMAIL:-${KOMBIFY_USER_EMAIL:-}}}"
  installer_journal_write owner_username "${OWNER_USERNAME:-${STACKKIT_OWNER_USERNAME:-}}"
  installer_journal_write platform "${PLATFORM_SELECTION:-}"
  installer_journal_write placement "${STACKKIT_PLACEMENT:-}"
  if [ "${STACKKIT_USE_CASES+x}" = x ] || [ "${INSTALLER_INTENT_COLLECTED:-}" = 1 ]; then
    installer_journal_write use_cases "${USE_CASES:-}"
    installer_journal_write use_cases_set 1
  fi
}

installer_save_intent() {
  installer_journal_write kit "$SELECTED_KIT"
  INSTALLER_INTENT_COLLECTED=1
  installer_save_inputs
}

# A failed/empty download must not look like a successful shell installation.
installer_run_entry() (
  _entry_url="$1"
  shift
  _entry_script=$(mktemp) || return 1
  trap 'rm -f "$_entry_script"' EXIT
  curl -fsSL "$_entry_url" -o "$_entry_script" || return 1
  [ -s "$_entry_script" ] || { printf 'Empty installer download: %s\n' "$_entry_url" >&2; return 1; }
  sh "$_entry_script" "$@"
)

cloud_execution_channel_key_dir() {
  printf '%s' "$HOMELAB_DIR/.stackkit/cloud-host-security/execution-channel"
}

execution_channel_username_from_workspace() {
  _user="${OWNER_USERNAME:-}"
  if [ -z "$_user" ] && [ -f "$HOMELAB_DIR/.stackkit/custody/owner.json" ]; then
    _owner_json="$HOMELAB_DIR/.stackkit/custody/owner.json"
    _user=$(sed -n '/"pocketId"/,/}/p' "$_owner_json" 2>/dev/null | sed -n 's/.*"username"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
    if [ -z "$_user" ]; then
      _user=$(sed -n 's/.*"username"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$_owner_json" 2>/dev/null | head -1)
    fi
  fi
  if [ -z "$_user" ]; then
    _user="kombify"
  fi
  printf '%s' "$_user" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g; s/^[.-]*//; s/[.-]*$//; s/--*/-/g' | cut -c1-32
}

install_execution_channel_pubkey() {
  _home="${1:-}"
  _pub="${2:-}"
  _user="${3:-}"
  [ -n "$_home" ] && [ -s "$_pub" ] || return 0
  [ "$(id -u)" -eq 0 ] || return 0
  if [ -z "$_user" ]; then
    _user=$(basename "$_home")
  fi
  if ! id "$_user" >/dev/null 2>&1; then
    return 1
  fi
  _uid=$(id -u "$_user")
  _gid=$(id -g "$_user")
  _ssh_dir="$_home/.ssh"
  mkdir -p "$_ssh_dir"
  chown "$_uid:$_gid" "$_ssh_dir"
  chmod 700 "$_ssh_dir"
  touch "$_ssh_dir/authorized_keys"
  chown "$_uid:$_gid" "$_ssh_dir/authorized_keys"
  chmod 600 "$_ssh_dir/authorized_keys"
  _line=$(head -n 1 "$_pub")
  [ -n "$_line" ] || return 1
  if grep -Fqx "$_line" "$_ssh_dir/authorized_keys" 2>/dev/null; then
    return 0
  fi
  printf '%s\n' "$_line" >> "$_ssh_dir/authorized_keys"
}

install_execution_channel_pub_on_root() {
  install_execution_channel_pubkey "/root" "${1:-}" root
}

ensure_execution_channel_linux_user() {
  _user="${1:-}"
  [ -n "$_user" ] || return 1
  [ "$(id -u)" -eq 0 ] || return 1
  if id "$_user" >/dev/null 2>&1; then
    return 0
  fi
  command -v useradd >/dev/null 2>&1 || return 1
  useradd --create-home --shell /bin/bash "$_user"
}

install_execution_channel_sudoers() {
  _user="${1:-}"
  [ -n "$_user" ] || return 1
  [ "$(id -u)" -eq 0 ] || return 1
  _sudoers="/etc/sudoers.d/60-stackkits-execution-channel"
  _content="# Managed by StackKits Cloud host security. Do not edit.
$_user ALL=(ALL) NOPASSWD:ALL
"
  if [ -f "$_sudoers" ] && grep -Fq "$_user ALL=(ALL) NOPASSWD:ALL" "$_sudoers" 2>/dev/null; then
    return 0
  fi
  printf '%s' "$_content" > "$_sudoers"
  chmod 440 "$_sudoers"
}

provision_execution_channel_account() {
  _pub="${1:-}"
  _user="${2:-}"
  [ -s "$_pub" ] && [ -n "$_user" ] || return 1
  ensure_execution_channel_linux_user "$_user" || return 1
  _home=$(getent passwd "$_user" 2>/dev/null | cut -d: -f6)
  [ -n "$_home" ] || _home="/home/$_user"
  install_execution_channel_pubkey "$_home" "$_pub" "$_user" || return 1
  install_execution_channel_sudoers "$_user" || return 1
}

ensure_cloud_execution_channel_key() {
  _key_dir=$(cloud_execution_channel_key_dir)
  _key="$_key_dir/id_ed25519"
  _pub="$_key.pub"
  command -v ssh-keygen >/dev/null 2>&1 || return 1
  if [ ! -s "$_key" ] || [ ! -s "$_pub" ]; then
    mkdir -p "$_key_dir"
    chmod 700 "$_key_dir"
    rm -f "$_key" "$_pub"
    ssh-keygen -q -t ed25519 -N '' -C stackkits-cloud-execution-channel -f "$_key"
    chmod 600 "$_key"
    chmod 644 "$_pub"
  fi
  if [ "$(id -u)" -ne 0 ]; then
    return 0
  fi
  # POSIX sh has no locals: the helpers below reassign _user, so keep the
  # channel account name in its own variable.
  _channel_user=$(execution_channel_username_from_workspace)
  install_execution_channel_pub_on_root "$_pub" || return 1
  provision_execution_channel_account "$_pub" "$_channel_user" || return 1
}

verify_execution_channel_account_ready() {
  _user=$(execution_channel_username_from_workspace)
  _pub="$(cloud_execution_channel_key_dir)/id_ed25519.pub"
  [ -s "$_pub" ] || return 1
  id "$_user" >/dev/null 2>&1 || return 1
  _home=$(getent passwd "$_user" 2>/dev/null | cut -d: -f6)
  [ -n "$_home" ] || return 1
  _line=$(head -n 1 "$_pub")
  [ -n "$_line" ] || return 1
  grep -Fqx "$_line" "$_home/.ssh/authorized_keys" 2>/dev/null
}

# StackKits install into a guest VM on a hypervisor, never onto the hypervisor
# host: Docker and the StackKit services would share the host with its guests.
# Keep in sync with the copy in install.sh, which cannot source this file.
stop_on_hypervisor_host() {
  [ -d /etc/pve ] || command -v pveversion >/dev/null 2>&1 || return 0
  [ "${STACKKIT_ALLOW_HYPERVISOR_HOST:-}" = "1" ] && return 0
  printf '\033[1;31m==> %s\033[0m\n' "This host is a Proxmox VE hypervisor. StackKits install into a guest VM, not onto the hypervisor host." >&2
  echo "  Create an Ubuntu 26.04 LTS VM on this host (or let kombify Techstack create it) and run the installer inside that VM (Ubuntu 24.04 LTS remains supported):" >&2
  echo "    curl -sSL https://install.stackkit.cc | sh" >&2
  echo "  To install on the hypervisor host anyway, rerun with STACKKIT_ALLOW_HYPERVISOR_HOST=1." >&2
  exit 1
}

# `stackkit host environment` (CLI releases up to v0.32.1) compares the public
# IP it sees with the interface addresses. On a dual-stack home network that
# public IP is the host's own global IPv6 address, which sits on the interface,
# so a home server reads as a public server. A public server carries its public
# IPv4 on an interface or answers a cloud metadata endpoint; a home server
# reaches the internet through NAT with a private IPv4. Re-check a "vps" verdict
# over IPv4 and export the corrected environment for the CLI steps that follow.
# Keep in sync with the copy in install.sh, which cannot source this file.
refine_home_network_detection() {
  [ "${DETECTED_ENV:-}" = "vps" ] || return 0
  [ -z "${STACKKIT_NETWORK_ENV:-}" ] || return 0
  command -v curl >/dev/null 2>&1 || return 0
  if curl -s -o /dev/null --connect-timeout 1 --max-time 2 http://169.254.169.254/ 2>/dev/null; then
    return 0
  fi
  _public_ipv4=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
  case "$_public_ipv4" in
    ''|*[!0-9.]*) return 0 ;;
  esac
  if command -v ip >/dev/null 2>&1; then
    _local_ipv4=$(ip -4 -o addr show 2>/dev/null | awk '{print $4}' | cut -d/ -f1)
  else
    _local_ipv4=$(hostname -I 2>/dev/null || true)
  fi
  [ -n "$_local_ipv4" ] || return 0
  if printf '%s\n' $_local_ipv4 | grep -Fxq "$_public_ipv4"; then
    return 0
  fi
  DETECTED_ENV="home"
  PUBLIC_SERVER="false"
  STACKKIT_NETWORK_ENV="home"
  export STACKKIT_NETWORK_ENV
}

workspace_has_deployment_intent() {
  [ -f "$HOMELAB_DIR/stack-spec.yaml" ] || \
  [ -f "$HOMELAB_DIR/.stackkit/resolved-plan.json" ] || \
  [ -f "$HOMELAB_DIR/deploy/.stackkit/resolved-plan.json" ]
}

workspace_apply_incomplete() {
  workspace_has_deployment_intent || return 1
  [ -f "$HOMELAB_DIR/.stackkit/access.json" ] && return 1
  return 0
}

# `stackkit host prepare` reads the workspace from its working directory, and
# the installer calls this before it changes into the workspace, so run it
# there explicitly.
run_stackkit_host_prepare() {
  command -v stackkit >/dev/null 2>&1 || return 0
  stackkit host prepare --help >/dev/null 2>&1 || return 0
  if [ "$(id -u)" -eq 0 ]; then
    (cd "$HOMELAB_DIR" && stackkit host prepare)
  elif command -v sudo >/dev/null 2>&1; then
    _stackkit_bin=$(command -v stackkit 2>/dev/null)
    [ -n "$_stackkit_bin" ] && (cd "$HOMELAB_DIR" && sudo -n "$_stackkit_bin" host prepare)
  fi
}

prepare_cloud_install_host() {
  case "${SELECTED_KIT:-cloud-kit}" in
    cloud-kit) ;;
    *) return 0 ;;
  esac
  ensure_cloud_execution_channel_key || return 1
  run_stackkit_host_prepare || return 1
  if [ "$(id -u)" -eq 0 ]; then
    verify_execution_channel_account_ready || return 1
  fi
}

# --- Release channels (CI-CD-PLATFORM-STANDARD 4.6 "Channel model") ---------
# stable is the default; early-access is the opt-in user channel that gets the
# builds the owner switched live. preview is the internal development Nightly:
# it still installs for the owner and the team but is never offered to users.
# The choice lives in ~/.stackkits/channel, the file `stackkit channel set`
# writes.

installer_channel_normalize() {
  case "$1" in
    stable|early-access|preview) printf '%s' "$1" ;;
    *) return 1 ;;
  esac
}

# installer_store_channel <state-dir> <channel> [owner]
installer_store_channel() {
  mkdir -p "$1" || return 1
  printf '%s\n' "$2" > "$1/channel.tmp.$$" && mv "$1/channel.tmp.$$" "$1/channel" || return 1
  if [ "$(id -u)" -eq 0 ] && [ -n "${3:-}" ] && [ "$3" != root ]; then
    chown "$3" "$1" "$1/channel" 2>/dev/null || true
  fi
}

# installer_channel_tags <GitHub releases list JSON> <preview|early-access>
# prints every published prerelease tagged vX.Y.Z-<channel>.<YYYYMMDD>.<sha>,
# newest first: latest date, and for one date the order GitHub lists them in.
# Drafts (a withdrawn Early Access build is a draft) are never printed.
installer_channel_tags() {
  printf '%s' "$1" | tr '\n' ' ' | awk -v channel="$2" '
    {
      gsub(/"tag_name"/, "\n&")
      count = split($0, parts, "\n")
      for (i = 2; i <= count; i++) {
        chunk = parts[i]
        if (!match(chunk, /^"tag_name"[ ]*:[ ]*"[^"]*"/)) continue
        tag = substr(chunk, RSTART, RLENGTH)
        sub(/^"tag_name"[ ]*:[ ]*"/, "", tag)
        sub(/"$/, "", tag)
        rest = tag
        if (!sub(/^v[0-9]+\.[0-9]+\.[0-9]+-/, "", rest)) continue
        if (substr(rest, 1, length(channel) + 1) != channel ".") continue
        rest = substr(rest, length(channel) + 2)
        if (rest !~ /^[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]\.[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]+$/) continue
        if (chunk !~ /"prerelease"[ ]*:[ ]*true/) continue
        if (chunk ~ /"draft"[ ]*:[ ]*true/) continue
        printf "%s %06d %s\n", substr(rest, 1, 8), i, tag
      }
    }' | sort -k1,1r -k2,2n | awk '{ print $3 }'
}

# installer_newest_channel_tag <GitHub releases list JSON> <preview|early-access>
installer_newest_channel_tag() {
  installer_channel_tags "$1" "$2" | head -n 1
}

# installer_newest_preview_tag <GitHub releases list JSON> prints the newest
# published Nightly prerelease tagged vX.Y.Z-preview.<YYYYMMDD>.<sha>.
installer_newest_preview_tag() {
  installer_newest_channel_tag "$1" preview
}

# installer_semver_compare <a> <b> prints -1, 0 or 1 (SemVer precedence).
installer_semver_compare() {
  awk -v a="$1" -v b="$2" '
    function parse(v, out,   i, c) {
      sub(/^v/, "", v)
      sub(/\+.*/, "", v)
      out["pre"] = ""
      i = index(v, "-")
      if (i > 0) { out["pre"] = substr(v, i + 1); v = substr(v, 1, i - 1) }
      split(v, c, ".")
      out[1] = c[1] + 0; out[2] = c[2] + 0; out[3] = c[3] + 0
    }
    function isnum(s) { return s ~ /^[0-9]+$/ }
    function cmpid(x, y) {
      if (isnum(x) && isnum(y)) return (x + 0 < y + 0) ? -1 : ((x + 0 > y + 0) ? 1 : 0)
      if (isnum(x)) return -1
      if (isnum(y)) return 1
      return (x < y) ? -1 : ((x > y) ? 1 : 0)
    }
    BEGIN {
      parse(a, A); parse(b, B)
      for (k = 1; k <= 3; k++) {
        if (A[k] < B[k]) { print -1; exit }
        if (A[k] > B[k]) { print 1; exit }
      }
      if (A["pre"] == "" && B["pre"] == "") { print 0; exit }
      if (A["pre"] == "") { print 1; exit }
      if (B["pre"] == "") { print -1; exit }
      na = split(A["pre"], pa, "."); nb = split(B["pre"], pb, ".")
      for (k = 1; k <= na && k <= nb; k++) {
        c = cmpid(pa[k], pb[k])
        if (c != 0) { print c; exit }
      }
      print (na < nb) ? -1 : ((na > nb) ? 1 : 0)
    }'
}
