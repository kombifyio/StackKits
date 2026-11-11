# Embedded after the custody-derived zone, resolver and public CA.
case "${1:-}" in
  --approve|--remove) action=$1 ;;
  *) fail client_trust_approval_required ;;
esac
[ "$(id -u)" -eq 0 ] || fail client_trust_approval_required
client_user=${SUDO_USER:-}
[ -n "$client_user" ] && [ "$client_user" != root ] || fail client_trust_browser_user_missing
client_home=$(getent passwd "$client_user" | cut -d: -f6)
case "$client_home" in /*) ;; *) fail client_trust_browser_user_missing ;; esac
for tool in openssl nmcli dnsmasq update-ca-certificates certutil runuser; do
  command -v "$tool" >/dev/null 2>&1 || fail client_trust_adapter_unavailable "$tool"
done
nmcli general status >/dev/null 2>&1 || fail client_trust_adapter_unavailable NetworkManager

umask 077
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
trap 'exit 130' HUP INT TERM
printf '%s\n' "$OWNER_CA" > "$work/root.crt"
actual=$(openssl x509 -in "$work/root.crt" -outform DER | openssl dgst -sha256 | awk '{print $NF}')
[ "$actual" = "$CA_SHA256" ] || fail client_trust_ca_mismatch
printf 'server=/%s/%s\n' "$ZONE" "$RESOLVER" > "$work/dns.conf"
printf '[main]\ndns=dnsmasq\nsystemd-resolved=false\nrc-manager=symlink\n' > "$work/manager.conf"
profile_root=/var/lib/stackkit-client-trust
manager_state=$profile_root/manager
state=$profile_root/$PROFILE_ID
dns_path=/etc/NetworkManager/dnsmasq.d/stackkit-$PROFILE_ID.conf
ca_path=/usr/local/share/ca-certificates/stackkit-$PROFILE_ID.crt
manager_path=/etc/NetworkManager/conf.d/90-stackkit-client-trust.conf
# Chromium M146+ uses the XDG store, retaining an existing legacy NSS store.
# https://chromium.googlesource.com/chromium/src/+/main/docs/linux/cert_management.md
nss_path=$client_home/.local/share/pki/nssdb
[ ! -d "$client_home/.pki/nssdb" ] || nss_path=$client_home/.pki/nssdb
if [ -f "$state/nss-user" ]; then
  [ "$(cat "$state/nss-user")" = "$client_user" ] || fail client_trust_local_conflict "$state/nss-user"
  recorded_nss=$(cat "$state/nss-path")
  case "$recorded_nss" in "$client_home/.pki/nssdb"|"$client_home/.local/share/pki/nssdb") nss_path=$recorded_nss ;; *) fail client_trust_local_conflict "$state/nss-path" ;; esac
fi
nickname=StackKits-$PROFILE_ID

owned_or_absent() {
  [ ! -e "$1" ] || cmp -s "$1" "$2" || fail client_trust_local_conflict "$1"
}
owned_or_absent "$dns_path" "$work/dns.conf"
owned_or_absent "$ca_path" "$work/root.crt"
owned_or_absent "$manager_path" "$work/manager.conf"
if [ -d "$nss_path" ] && runuser -u "$client_user" -- certutil -L -d "sql:$nss_path" -n "$nickname" -a > "$work/browser.crt" 2>/dev/null; then
  browser_sha=$(openssl x509 -in "$work/browser.crt" -outform DER | openssl dgst -sha256 | awk '{print $NF}')
  [ "$browser_sha" = "$CA_SHA256" ] || fail client_trust_local_conflict "$nss_path/$nickname"
fi

if [ "$action" = --remove ]; then
  [ -d "$state" ] || fail client_trust_profile_missing "$state"
  rm -f "$dns_path" "$ca_path"
  runuser -u "$client_user" -- certutil -D -d "sql:$nss_path" -n "$nickname" 2>/dev/null || true
  rm -f "$state/approved" "$state/nss-user" "$state/nss-path"
  rmdir "$state"
  # Restore the prior resolver link only after the last profile is removed,
  # and only while our own NetworkManager link still owns it.
  profiles=$(find /etc/NetworkManager/dnsmasq.d -maxdepth 1 -name 'stackkit-*.conf' -print)
  if [ -z "$profiles" ] && [ -d "$manager_state" ]; then
    [ "$(readlink /etc/resolv.conf || true)" = /run/NetworkManager/resolv.conf ] || fail client_trust_local_conflict /etc/resolv.conf
    prior_link=$(cat "$manager_state/resolv-link")
    if [ -n "$prior_link" ]; then
      ln -sfn "$prior_link" /etc/resolv.conf
    else
      cp -p "$manager_state/resolv-file" /etc/resolv.conf.stackkit-restore
      mv -f /etc/resolv.conf.stackkit-restore /etc/resolv.conf
    fi
    rm -f "$manager_path" "$manager_state/resolv-link" "$manager_state/resolv-file"
    rmdir "$manager_state"
  fi
  update-ca-certificates >/dev/null
  nmcli general reload conf
  nmcli general reload dns-full
  printf 'client_trust_removed: Removed only profile %s; existing network connection DNS remains unchanged.\n' "$PROFILE_ID"
  exit 0
fi

# dnsmasq receives NetworkManager's connection upstreams with their interface
# binding. A loopback-only upstream would stop ordinary lookups on that link.
# Reject that observed boundary before changing DNS or installing a CA.
upstreams=$(nmcli -g IP4.DNS,IP6.DNS device show)
if printf '%s\n' "$upstreams" | awk '/^(127\.|::1$)/ { found=1 } END { exit !found }'; then
  fail client_trust_dns_upstream_unsupported
fi

install -d -m 0755 /etc/NetworkManager/dnsmasq.d /etc/NetworkManager/conf.d /usr/local/share/ca-certificates
install -d -m 0700 "$profile_root" "$state"
printf '%s\n' "$client_user" > "$state/nss-user"
printf '%s\n' "$nss_path" > "$state/nss-path"
if [ ! -d "$manager_state" ]; then
  install -d -m 0700 "$manager_state"
  readlink /etc/resolv.conf > "$manager_state/resolv-link" || :
  if [ ! -s "$manager_state/resolv-link" ]; then
    cp -p /etc/resolv.conf "$manager_state/resolv-file"
  fi
fi
install -m 0644 "$work/dns.conf" "$dns_path"
install -m 0644 "$work/manager.conf" "$manager_path"
install -m 0644 "$work/root.crt" "$ca_path"
update-ca-certificates >/dev/null
runuser -u "$client_user" -- mkdir -p "$nss_path"
if ! runuser -u "$client_user" -- certutil -L -d "sql:$nss_path" >/dev/null 2>&1; then
  runuser -u "$client_user" -- certutil -N --empty-password -d "sql:$nss_path"
fi
runuser -u "$client_user" -- certutil -A -d "sql:$nss_path" -n "$nickname" -t C,, -i "$ca_path"
# NetworkManager retains each connection's original DHCP/static upstream DNS.
# dnsmasq forwards only this private zone to the kit's resolver.
ln -sfn /run/NetworkManager/resolv.conf /etc/resolv.conf
nmcli general reload conf
nmcli general reload dns-full
printf '%s\n' "$CA_SHA256" > "$state/approved"
printf 'client_trust_installed: %s uses %s; approved CA SHA256:%s. Open the original printed HTTPS URLs and sign in normally.\n' "$ZONE" "$RESOLVER" "$CA_SHA256"
