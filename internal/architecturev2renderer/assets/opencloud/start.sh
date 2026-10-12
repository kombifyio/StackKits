#!/bin/sh
set -eu
config_dir=${OC_CONFIG_DIR:-/etc/opencloud}
if [ ! -f "$config_dir/opencloud.yaml" ]; then
  if [ -z "${IDM_ADMIN_PASSWORD:-}" ]; then
    printf '%s\n' 'OpenCloud bootstrap material is absent from node custody.' >&2
    exit 1
  fi
  if ! opencloud init >/dev/null 2>&1; then
    unset IDM_ADMIN_PASSWORD
    printf '%s\n' 'OpenCloud initial configuration failed; the server was not started.' >&2
    exit 1
  fi
fi
unset IDM_ADMIN_PASSWORD
exec opencloud server
