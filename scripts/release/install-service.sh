#!/usr/bin/env bash
# Install Switchboard as a systemd service on Linux, following
# docs/operations/systemd.md: code and config belong to root, state to its own
# account, the console stays on loopback and nothing public is opened.
#
#   sudo scripts/release/install-service.sh --binary ./switchboard
#   sudo scripts/release/install-service.sh --archive switchboard_linux_amd64.tar.gz --sha256 HEX
#
# It never overwrites an existing installation, never touches DNS or the
# firewall and never prints a secret. Use --dry-run to read the plan first.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: install-service.sh (--binary PATH | --archive FILE.tar.gz --sha256 HEX)
                          [--target URL] [--group NAME] [--dry-run]
  --binary   an already verified switchboard binary
  --archive  a release archive; --sha256 is the checksum from a trusted channel
  --target   the existing application to protect (default http://127.0.0.1:3000)
  --group    the visitor group the first route requires (default members)
  --dry-run  print what would change and stop
Test hook: SWITCHBOARD_INSTALL_PREFIX installs under a directory, without
creating accounts or units (used by scripts/release/test-install-service.sh).
EOF
  exit 2
}

binary="" archive="" sha="" target="http://127.0.0.1:3000" group="members" dry=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) binary=${2:-}; shift 2 ;;
    --archive) archive=${2:-}; shift 2 ;;
    --sha256) sha=${2:-}; shift 2 ;;
    --target) target=${2:-}; shift 2 ;;
    --group) group=${2:-}; shift 2 ;;
    --dry-run) dry=1; shift ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done
[[ -n "$binary" || -n "$archive" ]] || usage
[[ -z "$binary" || -z "$archive" ]] || { echo 'give --binary or --archive, not both' >&2; exit 2; }
[[ -z "$archive" || -n "$sha" ]] || { echo '--archive needs --sha256 from a trusted channel' >&2; exit 2; }
[[ "$group" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || { echo 'group must be lowercase letters, digits, - or _' >&2; exit 2; }
[[ "$target" =~ ^https?://[A-Za-z0-9._:/-]+$ ]] || { echo 'target must be a plain http(s) URL such as http://127.0.0.1:3000' >&2; exit 2; }
[[ "$(uname -s)" == Linux ]] || { echo 'this installer supports Linux with systemd' >&2; exit 2; }

prefix=${SWITCHBOARD_INSTALL_PREFIX:-}
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
lib=$prefix/usr/local/lib/switchboard
etc=$prefix/etc/switchboard
state=$prefix/var/lib/switchboard
unit=$prefix/etc/systemd/system/switchboard.service
config=$etc/config.json

if [[ -z "$prefix" && $EUID -ne 0 && $dry -eq 0 ]]; then
  echo 'run as root (sudo): the binary and config must belong to root' >&2
  exit 2
fi
# A previous installation is never overwritten: use `switchboard upgrade`.
for path in "$config" "$lib/switchboard" "$unit"; do
  if [[ -e "$path" ]]; then
    echo "$path already exists. This installer only creates a new installation; use 'switchboard upgrade' (docs/operations/systemd.md)." >&2
    exit 1
  fi
done

echo "Plan"
echo "  binary   $lib/switchboard (root:root 755)"
echo "  config   $config (root:root 644, no secrets)"
echo "  token    $etc/admin.token (root:switchboard 640, random)"
echo "  state    $state (switchboard 700), console on 127.0.0.1:9090 only"
echo "  route    localhost:8888 -> $target, requires group '$group'"
echo "  service  switchboard.service, enabled and started"
echo "  not done DNS, firewall, public TLS, moving any existing website"
[[ $dry -eq 0 ]] || exit 0

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
if [[ -n "$archive" ]]; then
  "$here/install.sh" "$archive" "$sha" "$work/switchboard"
  binary=$work/switchboard
fi
[[ -f "$binary" && ! -L "$binary" ]] || { echo 'binary must be a regular file' >&2; exit 2; }
version=$("$binary" version 2>&1) || { echo 'the binary does not run: '"$version" >&2; exit 1; }
echo "Binary reports: $version"

if [[ -z "$prefix" ]] && ! getent passwd switchboard >/dev/null; then
  useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin switchboard
fi

install -d -m 755 "$lib" "$etc" "$prefix/etc/systemd/system"
install -m 755 "$binary" "$lib/switchboard"

# Admin token: random, readable only by root and the service account.
umask 0077
head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n' > "$work/admin.token"
if [[ -z "$prefix" ]]; then
  install -o root -g switchboard -m 640 "$work/admin.token" "$etc/admin.token"
else
  install -m 640 "$work/admin.token" "$etc/admin.token"
fi

# The config holds intent and references only. The console edits the
# operational file under the service's private state directory.
cat > "$work/config.json" <<EOF
{
  "runtime_file": "/var/lib/switchboard/runtime.json",
  "listen": "127.0.0.1:8888",
  "tls": { "enabled": false },
  "auth": { "passkeys": { "rp_id": "localhost", "login_url": "http://localhost:8888", "display_name": "Switchboard" } },
  "admin": { "listen": "127.0.0.1:9090", "token": "file:/etc/switchboard/admin.token", "socket": "/run/switchboard/admin.sock" },
  "pages": { "dir": "/var/lib/switchboard/pages", "keep": 5 },
  "routes": [ { "host": "localhost:8888", "target": "$target", "access": { "required_groups": ["$group"] } } ]
}
EOF
# The runtime file and token do not exist yet, so check the same config without
# the runtime file and with the token read from the staged copy.
sed -e '/"runtime_file"/d' -e "s#file:/etc/switchboard/admin.token#file:$etc/admin.token#" "$work/config.json" > "$work/validate.json"
"$binary" validate -config "$work/validate.json" >/dev/null || { echo 'the generated config did not validate; nothing was started' >&2; exit 1; }
install -m 644 "$work/config.json" "$config"
install -m 644 "$here/../../docs/operations/switchboard.service" "$unit"

if [[ -n "$prefix" ]]; then
  install -d -m 700 "$state"
  echo "Staged under $prefix (no account, unit or service was created)."
  exit 0
fi

install -d -o switchboard -g switchboard -m 700 "$state" "$state/pages"
sudo -u switchboard "$lib/switchboard" migrate up -db "$state/state.db"
sudo -u switchboard "$lib/switchboard" runtime init -config "$config"
systemctl daemon-reload
systemctl enable --now switchboard

for _ in $(seq 1 30); do
  if curl -fsS -o /dev/null http://127.0.0.1:9090/_/healthz 2>/dev/null; then ready=1; break; fi
  sleep 1
done
if [[ -z "${ready:-}" ]]; then
  echo 'Installed, but the console did not answer within 30 s. Read: journalctl -u switchboard -n 50' >&2
  exit 1
fi

cat <<EOF

Switchboard is running. Nothing is public: the proxy and the console listen on loopback.

Next
  1. Open the console (from your computer): ssh -N -L 9090:127.0.0.1:9090 USER@THIS_HOST
  2. Get your one-use private link, on this host:
       sudo -u switchboard $lib/switchboard console login -config $config -name YOUR_NAME
  3. Open it, then Applications: add your existing website and invite the first person.
  4. Back up $state and $etc. Recovery and upgrades: docs/operations/systemd.md
EOF
