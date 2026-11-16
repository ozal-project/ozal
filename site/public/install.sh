#!/bin/sh
# Ozal installer.
#
#   curl -fsSL https://ozal.dev/install.sh | sh
#
# One command verifies the newest signed release, installs the Linux service and
# turns on installing later releases from the console. Downloading and
# verifying run as you; only the installation itself runs as root, through
# sudo when you are not root already (sudo may ask for your password).
#
# It installs nothing it cannot prove. It reads the signed release catalog,
# checks its Ed25519 signature with the release key written below, then checks
# the signed checksums and the archive's SHA-256 before it extracts a single
# file. Any mismatch stops it. With --plan it only shows what
# `ozal install service` would do and changes nothing.
#
# Trust: piped from https://ozal.dev, this script and the key below come from
# the same place, so the root of trust is that site's TLS. For more, download
# the script, compare the key with docs/operations/ozal-release.pub obtained
# another way, and read it before running it. The same file is attached to
# every release.
#
# Options:
#   --plan             verify and show the plan; install nothing
#   --no-updates       do not turn on installing updates from the console
#   --channel NAME     stable or preview (default: stable, else preview)
#   --version vX.Y.Z   a specific release instead of the channel's newest
#   --keep DIR         keep the verified archive and binary in DIR
#   -- ARGS            pass ARGS to `ozal install service` (e.g. -target URL)
# --apply and --updates are accepted for older instructions; they are the default.
set -eu

RELEASE_KEY="mLezEy+Uo1RnvqjOAr5+RYoGjny1IxHYgfHdtra51B8="
CATALOG_BASE="https://ozal.dev/api/v1/releases"
RELEASES="https://github.com/ozal-project/ozal/releases/download"

apply=1
updates=1
channel=""
version=""
keep=""

say() { printf '%s\n' "$*"; }
fail() { printf 'ozal install: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --plan|--dry-run) apply=0 ;;
    --apply) apply=1 ;;
    --no-updates) updates=0 ;;
    --updates) updates=1 ;;
    --channel) [ $# -ge 2 ] || fail "--channel needs a value"; channel=$2; shift ;;
    --version) [ $# -ge 2 ] || fail "--version needs a value"; version=$2; shift ;;
    --keep) [ $# -ge 2 ] || fail "--keep needs a directory"; keep=$2; shift ;;
    -h|--help) sed -n '2,30p' "$0" 2>/dev/null || true; exit 0 ;;
    --) shift; break ;;
    *) fail "unknown option $1 (pass installer flags after --)" ;;
  esac
  shift
done

# Only the script decides whether to install (default) or plan (--plan).
# Go's flag parser also accepts double dashes and explicit boolean values.
for arg do
  case "$arg" in
    -apply|--apply|-apply=*|--apply=*) fail "installation mode cannot be set in forwarded service arguments; the script installs by default, use --plan before -- to only show the plan" ;;
  esac
done

case "$channel" in ""|stable|preview) ;; *) fail "--channel must be stable or preview" ;; esac
case "$version" in ""|v[0-9]*.[0-9]*.[0-9]*) ;; *) fail "--version must look like v0.1.0" ;; esac

[ "$(uname -s)" = Linux ] || fail "the service installer runs on Linux; on other systems download a release from GitHub"
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "no release for $(uname -m)" ;;
esac
for tool in curl openssl sha256sum tar base64 mktemp sed grep date; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done
# Installing writes a system service, so it needs root. Ask for it before any
# download so a refusal costs nothing; everything up to the install runs as you.
as_root=""
if [ "$apply" = 1 ] && [ "$(id -u)" != 0 ]; then
  command -v sudo >/dev/null 2>&1 ||
    fail "installing the service needs root and sudo is not available: run this as root, or add --plan to only see the plan"
  say "Ozal installs a system service; sudo may ask for your password."
  sudo -v ||
    fail "installing the service needs root: sudo was refused (add --plan to only see the plan)"
  as_root=sudo
fi

# Resolve retained files against the caller before entering the workspace,
# whose contents are removed on exit.
case "$keep" in
  ""|/*) ;;
  *) keep="$(pwd)/$keep" ;;
esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM
cd "$work"

fetch() { curl -fsSL --proto '=https' --tlsv1.2 --retry 2 -o "$2" "$1"; }

# The release key as a standard public key: the RFC 8410 Ed25519 header
# followed by the 32-byte key.
{ printf '\060\052\060\005\006\003\053\145\160\003\041\000'; printf '%s' "$RELEASE_KEY" | base64 -d; } |
  openssl pkey -pubin -inform DER -out release.pem 2>/dev/null ||
  fail "openssl could not read the release key (OpenSSL 3.0 or later is required)"

verify() { # FILE SIGNATURE_FILE
  base64 -d "$2" > "$2.bin" 2>/dev/null || return 1
  openssl pkeyutl -verify -pubin -inkey release.pem -rawin -in "$1" -sigfile "$2.bin" >/dev/null 2>&1
}

field() { # NAME FILE: one string or number field of the catalog
  sed -n "s/^ *\"$1\": *\"\{0,1\}\([^\",]*\)\"\{0,1\},\{0,1\}\$/\1/p" "$2" | head -n 1
}

if [ -z "$version" ]; then
  for try in ${channel:-stable preview}; do
    if fetch "$CATALOG_BASE/$try/linux/$arch" catalog.json 2>/dev/null &&
      fetch "$CATALOG_BASE/$try/linux/$arch.sig" catalog.sig 2>/dev/null &&
      [ "$(head -c 1 catalog.json)" = "{" ]; then
      channel=$try
      break
    fi
    [ -n "$channel" ] && fail "no release is published in the $channel channel yet"
    say "No release in the $try channel yet."
  done
  if ! { [ -s catalog.json ] && [ "$(head -c 1 catalog.json)" = "{" ]; }; then
    fail "no release catalog could be read from $CATALOG_BASE"
  fi
  verify catalog.json catalog.sig || fail "the release catalog is not signed by the release key"
  [ "$(field channel catalog.json)" = "$channel" ] || fail "the catalog names another channel"
  if ! { [ "$(field os catalog.json)" = linux ] && [ "$(field arch catalog.json)" = "$arch" ]; }; then
    fail "the catalog is for another platform"
  fi
  expires=$(date -u -d "$(field expires_at catalog.json)" +%s 2>/dev/null) || fail "the catalog has no valid expiry"
  [ "$(date -u +%s)" -lt "$expires" ] || fail "the release catalog has expired; check this host's clock"
  version=$(field version catalog.json)
  archive_url=$(field archive_url catalog.json)
  want=$(field archive_sha256 catalog.json)
  checksums_url=$(field checksums_url catalog.json)
  signature_url=$(field signature_url catalog.json)
  say "Channel $channel offers $version."
else
  archive_url="$RELEASES/$version/ozal_${version#v}_linux_$arch.tar.gz"
  checksums_url="$RELEASES/$version/checksums.txt"
  signature_url="$RELEASES/$version/checksums.txt.sig"
  want=""
fi

archive="ozal_${version#v}_linux_$arch.tar.gz"
case "$archive_url" in
  https://github.com/ozal-project/ozal/releases/download/*/"$archive") ;;
  *) fail "unexpected archive address $archive_url" ;;
esac

fetch "$checksums_url" checksums.txt || fail "could not download checksums.txt for $version"
fetch "$signature_url" checksums.txt.sig || fail "could not download checksums.txt.sig for $version"
verify checksums.txt checksums.txt.sig || fail "checksums.txt of $version is not signed by the release key"
sum=$(grep " $archive\$" checksums.txt | cut -d' ' -f1)
[ -n "$sum" ] || fail "the signed checksums do not list $archive"
[ -z "$want" ] || [ "$want" = "$sum" ] || fail "the catalog and the signed checksums disagree about $archive"

say "Downloading $archive..."
fetch "$archive_url" "$archive" || fail "could not download $archive"
[ "$(sha256sum "$archive" | cut -d' ' -f1)" = "$sum" ] || fail "$archive does not match its signed checksum"
mkdir bin
tar -xzf "$archive" -C bin ozal || fail "the archive has no ozal binary"
say "Verified: signature, checksum and $(./bin/ozal version | head -n 1)."

if [ -n "$keep" ]; then
  mkdir -p "$keep"
  cp "$archive" checksums.txt checksums.txt.sig "$keep/"
  cp bin/ozal "$keep/ozal"
  say "Kept the verified files in $keep."
fi

if [ "$apply" = 0 ]; then
  ./bin/ozal install service -archive "$archive" -sha256 "$sum" "$@" || {
    status=$?
    printf '%s\n' 'ozal install: could not produce the service installation plan; resolve the error above before installing' >&2
    exit "$status"
  }
  say ""
  say "Nothing was installed. To install the service:"
  say "  curl -fsSL https://ozal.dev/install.sh | sh"
  exit 0
fi

$as_root ./bin/ozal install service -archive "$archive" -sha256 "$sum" "$@" -apply
if [ "$updates" = 1 ]; then
  if [ -n "$channel" ]; then
    (cd / && $as_root /usr/local/lib/ozal/ozal install updates -channel "$channel" -apply)
  else
    (cd / && $as_root /usr/local/lib/ozal/ozal install updates -apply)
  fi
fi
