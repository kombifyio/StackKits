#!/bin/sh
# Governed by StackKits. Called only by `stackkit setup mail` through the
# fixed Compose exec contract. Stores the owner's IMAP and SMTP endpoints
# (never a password) on the persistent mailbox volume, atomically.
#   mailbox.sh set <imap-uri> <smtp-uri>   activate endpoints, keep previous
#   mailbox.sh revert                      restore the previous endpoints
set -eu
dir=/var/roundcube/mailbox
file=$dir/mailbox.php
valid() {
	printf '%s\n' "$1" | grep -Eq '^(ssl|tls)://[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?:[0-9]{1,5}$'
}
case "${1:-}" in
set)
	[ "$#" -eq 3 ] && valid "$2" && valid "$3" || { echo "invalid mailbox endpoints" >&2; exit 2; }
	install -d -o root -g www-data -m 0750 "$dir"
	tmp=$(mktemp "$dir/.mailbox.XXXXXX")
	printf "<?php\nreturn ['imap' => '%s', 'smtp' => '%s'];\n" "$2" "$3" > "$tmp"
	chown root:www-data "$tmp"
	chmod 0640 "$tmp"
	if [ -f "$file" ]; then cp -p "$file" "$dir/mailbox.previous.php"; else rm -f "$dir/mailbox.previous.php"; fi
	mv -f "$tmp" "$file"
	;;
revert)
	[ "$#" -eq 1 ] || exit 2
	if [ -f "$dir/mailbox.previous.php" ]; then mv -f "$dir/mailbox.previous.php" "$file"; else rm -f "$file"; fi
	;;
*)
	echo "usage: mailbox.sh set <imap-uri> <smtp-uri> | revert" >&2
	exit 2
	;;
esac
