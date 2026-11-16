#!/usr/bin/env bash
# Writes the signed update catalogs edges read under OUT/<channel>/<os>/<arch>
# (`ozal update -catalog`, the update executor's catalog_base), for the
# newest published release of each channel. The site workflow runs it on every
# deploy, so the catalogs ship with ozal.dev at /api/v1/releases and are
# re-signed with a fresh window each time.
#
# Needs: gh (GH_TOKEN), OZAL (an ozal binary from this checkout),
# OZAL_RELEASE_KEY (the release signing key) and GITHUB_REPOSITORY.
set -euo pipefail

out=${1:?usage: publish-catalogs.sh OUT_DIR}
repo=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is not set}
ozal=${OZAL:?OZAL must point to an ozal binary}
if [ -z "${OZAL_RELEASE_KEY:-}" ]; then
  echo "OZAL_RELEASE_KEY is not set: the site would ship without update catalogs and every edge would stop seeing updates" >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
gh api "repos/$repo/releases?per_page=100" > "$work/releases.json"

published=0
for channel in stable preview; do
  line=$("$ozal" release latest -releases "$work/releases.json" -channel "$channel")
  if [ -z "$line" ]; then
    echo "$channel: no published release yet; nothing to offer"
    continue
  fi
  tag=${line%% *}
  previous=""
  [ "$line" != "$tag" ] && previous=${line#* }

  dir="$work/$channel"
  mkdir -p "$dir"
  gh api "repos/$repo/releases/tags/$tag" > "$dir/release.json"
  gh release download "$tag" -R "$repo" -p checksums.txt -p checksums.txt.sig -D "$dir"
  commit=$(gh api "repos/$repo/commits/$tag" --jq .sha)

  # The release may migrate the database if its schema changed since the
  # previous release of the channel. With no previous release, or a
  # comparison too large to list, say so: it only adds a warning.
  flags=()
  if [ -z "$previous" ]; then
    flags+=(-migrations)
  else
    gh api "repos/$repo/compare/$previous...$tag" > "$dir/compare.json"
    if [ "$(jq '.files | length' "$dir/compare.json")" -ge 300 ] ||
      jq -r '.files[].filename' "$dir/compare.json" | grep -q '^internal/store/sqlite/migrations/'; then
      flags+=(-migrations)
    fi
  fi

  "$ozal" release catalog -key env:OZAL_RELEASE_KEY -channel "$channel" -release "$dir/release.json" \
    -checksums "$dir/checksums.txt" -signature "$dir/checksums.txt.sig" -commit "$commit" -out "$out" "${flags[@]}"
  published=$((published + 1))
done

if [ "$published" -eq 0 ]; then
  echo "no channel has a published release; no catalog was written" >&2
fi
