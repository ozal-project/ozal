#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	printf '%s\n' 'run this installer with sudo' >&2
	exit 1
fi
if [ "$#" -ne 6 ]; then
	printf 'usage: sudo %s ACCOUNT HELPER_BINARY PROFILE_JSON AUTHORIZED_KEY_PUB RELEASE_PUBLIC_KEY SHA256\n' "$0" >&2
	exit 2
fi

account=$1
binary=$2
profile=$3
pubkey=$4
release_key=$5
expected=$6
[ "$account" = ozal-pair ] || { echo 'the dedicated account name must be ozal-pair' >&2; exit 2; }
case "$expected" in *[!a-f0-9]*|'') echo 'invalid expected SHA-256' >&2; exit 2;; esac
[ "${#expected}" -eq 64 ] || { echo 'invalid expected SHA-256 length' >&2; exit 2; }
for file in "$binary" "$profile" "$pubkey" "$release_key"; do
	[ -f "$file" ] && [ ! -L "$file" ] || { echo "expected a regular input file: $file" >&2; exit 1; }
done

actual=$(sha256sum "$binary" | cut -d' ' -f1)
[ "$actual" = "$expected" ] || { echo 'helper binary checksum mismatch' >&2; exit 1; }
release_bytes=$(base64 -d "$release_key" 2>/dev/null | wc -c)
[ "$release_bytes" -eq 32 ] || { echo 'release key must be a base64 Ed25519 public key' >&2; exit 1; }
key=$(cat "$pubkey")
[ "$(wc -l < "$pubkey")" -eq 1 ] || { echo 'expected one Ed25519 public key line' >&2; exit 1; }
IFS=' ' read -r key_type key_blob key_comment extra < "$pubkey"
[ "$key_type" = ssh-ed25519 ] && [ -n "$key_blob" ] && [ -z "${extra:-}" ] || { echo 'expected one Ed25519 public key line' >&2; exit 1; }
printf '%s' "$key_blob" | base64 -d >/dev/null 2>&1 || { echo 'invalid Ed25519 public key data' >&2; exit 1; }

if id "$account" >/dev/null 2>&1; then
	echo "account already exists: $account" >&2
	exit 1
fi

install -o root -g root -m 0755 "$binary" /usr/local/sbin/ozal-pair-helper
install -o root -g root -m 0600 "$profile" /etc/ozal/pair-helper.json
install -o root -g root -m 0644 "$release_key" /etc/ozal/release.pub

# Validate profile parsing and the read-only edge preflight before granting any
# account access. This action cannot create files or change service state.
printf '%s\n' '{"action":"preflight"}' | /usr/local/sbin/ozal-pair-helper

groupadd --system "$account"
useradd --system --gid "$account" --create-home --home-dir "/var/lib/$account" --shell /bin/sh "$account"
passwd --lock "$account" >/dev/null
install -d -o "$account" -g "$account" -m 0700 "/var/lib/$account/stage" "/var/lib/$account/locks"
install -d -o "$account" -g "$account" -m 0700 "/var/lib/$account/.ssh"
# This login needs an unprivileged shell for locked-down staging and inspection
# commands. Root actions still pass through the argument-free sudoers rule.
printf 'restrict %s\n' "$key" > "/var/lib/$account/.ssh/authorized_keys"
chown "$account:$account" "/var/lib/$account/.ssh/authorized_keys"
chmod 0600 "/var/lib/$account/.ssh/authorized_keys"

rule=$(mktemp /etc/sudoers.d/.ozal-pair-helper.XXXXXX)
trap 'rm -f "$rule"' EXIT HUP INT TERM
printf '%s ALL=(root) NOPASSWD: /usr/local/sbin/ozal-pair-helper ""\n' "$account" > "$rule"
chmod 0440 "$rule"
visudo -cf "$rule"
mv "$rule" /etc/sudoers.d/ozal-pair-helper
trap - EXIT HUP INT TERM
printf 'installed restricted pair helper account %s\n' "$account"
