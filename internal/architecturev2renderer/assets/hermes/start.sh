#!/bin/sh
# StackKits governed start for the Private AI assistant (Hermes Agent).
#
# Runs as root before the upstream entrypoint. It reads the dashboard
# credentials from their custody files, then does three things:
#   1. installs the StackKits policy as Hermes managed scope (/etc/hermes,
#      root-owned, pins specific keys over the owner's own config);
#   2. seeds the owner's config once, with every grant off;
#   3. after a restore, pauses every schedule until the owner resumes it.
# Then it hands over to the upstream s6-overlay entrypoint, which runs Hermes
# as the unprivileged hermes user (UID 10000).
set -eu

policy=/opt/stackkit

# The dashboard credentials come from their custody files (root, 0400) into
# the environment the upstream entrypoint hands to Hermes; neither is in the
# container configuration.
HERMES_DASHBOARD_BASIC_AUTH_PASSWORD="$(cat "$STACKKIT_DASHBOARD_PASSWORD_FILE")"
HERMES_DASHBOARD_BASIC_AUTH_SECRET="$(cat "$STACKKIT_DASHBOARD_SESSION_SECRET_FILE")"
export HERMES_DASHBOARD_BASIC_AUTH_PASSWORD HERMES_DASHBOARD_BASIC_AUTH_SECRET
unset STACKKIT_DASHBOARD_PASSWORD_FILE STACKKIT_DASHBOARD_SESSION_SECRET_FILE
home="${HERMES_HOME:-/opt/data}"

install -d -m 0755 /etc/hermes
install -m 0644 -o root -g root "$policy/managed/config.yaml" /etc/hermes/config.yaml

install -d -m 0755 -o 10000 -g 10000 "$home"
if [ ! -e "$home/config.yaml" ]; then
	install -m 0600 -o 10000 -g 10000 "$policy/seed/config.yaml" "$home/config.yaml"
fi

if [ "${STACKKIT_HERMES_RESTORE_ACTIVATION:-false}" = "true" ] && [ -f "$home/cron/jobs.json" ]; then
	# A restored schedule stays disabled (the state `hermes cron create
	# --paused` writes) until the owner runs `hermes cron resume <id>`.
	python3 - "$home/cron/jobs.json" <<'PY'
import json, os, sys
path = sys.argv[1]
with open(path, encoding="utf-8") as handle:
    data = json.load(handle)
jobs = data.get("jobs", []) if isinstance(data, dict) else data
paused = 0
for job in jobs:
    if isinstance(job, dict) and job.get("enabled", True):
        job["enabled"] = False
        paused += 1
if paused:
    stat = os.stat(path)
    tmp = path + ".stackkit"
    with open(tmp, "w", encoding="utf-8") as handle:
        json.dump(data, handle, indent=2)
    os.chown(tmp, stat.st_uid, stat.st_gid)
    os.chmod(tmp, stat.st_mode & 0o777)
    os.replace(tmp, path)
print(f"[stackkit] restore activation: {paused} schedule(s) paused", file=sys.stderr)
PY
fi

exec /opt/hermes/docker/entrypoint-dispatch.sh "$@"
