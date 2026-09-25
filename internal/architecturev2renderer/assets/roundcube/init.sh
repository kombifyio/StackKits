#!/bin/sh
# Governed by StackKits. Runs as root before the upstream entrypoint: the
# executor persists governed files owner-only, so install copies the web
# server user can read (never write), then hand over to Roundcube.
set -eu
src=/var/roundcube/config/stackkit
install -o root -g www-data -m 0640 "$src/config.php" /var/roundcube/config/zz-stackkit.php
install -d -o root -g www-data -m 0750 /usr/local/share/stackkit-mail
install -o root -g www-data -m 0640 "$src/devices.php" /usr/local/share/stackkit-mail/devices.php
install -o root -g root -m 0644 "$src/devices.conf" /etc/apache2/conf-enabled/stackkit-mail-devices.conf
install -d -o root -g www-data -m 0750 /var/roundcube/mailbox
exec /docker-entrypoint.sh "$@"
