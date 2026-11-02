#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	printf '%s\n' 'run this repair with sudo' >&2
	exit 1
fi
if [ "$#" -ne 4 ] || [ "$1" != ozal-pair ]; then
	printf 'usage: sudo %s ozal-pair HELPER_BINARY AUTHORIZED_KEY_PUB SHA256\n' "$0" >&2
	exit 2
fi
account=$1
binary=$2
pubkey=$3
expected=$4
case "$expected" in *[!a-f0-9]*|'') echo 'invalid expected SHA-256' >&2; exit 2;; esac
[ "${#expected}" -eq 64 ] || { echo 'invalid expected SHA-256 length' >&2; exit 2; }
for file in "$binary" "$pubkey"; do
	[ -f "$file" ] && [ ! -L "$file" ] || { echo "expected a regular input file: $file" >&2; exit 1; }
done
actual=$(sha256sum "$binary" | cut -d' ' -f1)
[ "$actual" = "$expected" ] || { echo 'helper binary checksum mismatch' >&2; exit 1; }
[ "$(wc -l < "$pubkey")" -eq 1 ] || { echo 'expected one Ed25519 public key line' >&2; exit 1; }
IFS=' ' read -r key_type key_blob key_comment extra < "$pubkey"
[ "$key_type" = ssh-ed25519 ] && [ -n "$key_blob" ] && [ -z "${extra:-}" ] || { echo 'expected one Ed25519 public key line' >&2; exit 1; }
printf '%s' "$key_blob" | base64 -d >/dev/null 2>&1 || { echo 'invalid Ed25519 public key data' >&2; exit 1; }
id "$account" >/dev/null 2>&1 || { echo 'dedicated account is missing' >&2; exit 1; }
visudo -c >/dev/null

helper_tmp=$(mktemp /usr/local/sbin/.ozal-pair-helper.XXXXXX)
key_tmp=
cleanup() { rm -f "$helper_tmp"; if [ -n "$key_tmp" ]; then rm -f "$key_tmp"; fi; }
trap cleanup EXIT HUP INT TERM
install -o root -g root -m 0755 "$binary" "$helper_tmp"
[ "$(sha256sum "$helper_tmp" | cut -d' ' -f1)" = "$expected" ] || { echo 'installed helper checksum mismatch' >&2; exit 1; }
printf '%s\n' '{"action":"preflight"}' | "$helper_tmp" pair-helper

home=/var/lib/ozal-pair
ssh_dir=$home/.ssh
keyfile=$ssh_dir/authorized_keys
[ -d "$ssh_dir" ] && [ ! -L "$ssh_dir" ] || { echo 'dedicated SSH directory is missing or unsafe' >&2; exit 1; }
[ "$(stat -c %U "$ssh_dir")" = "$account" ] || { echo 'dedicated SSH directory has the wrong owner' >&2; exit 1; }
key_tmp=$(mktemp "$ssh_dir/.authorized_keys.XXXXXX")
printf 'restrict %s\n' "$(cat "$pubkey")" > "$key_tmp"
chown "$account:$account" "$key_tmp"
chmod 0600 "$key_tmp"
mv -f "$helper_tmp" /usr/local/sbin/ozal-pair-helper
mv -f "$key_tmp" "$keyfile"
trap - EXIT HUP INT TERM
printf '%s\n' 'helper refreshed and SSH key repaired; sudo remains limited to ozal-pair-helper.'
