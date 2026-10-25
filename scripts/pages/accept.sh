#!/usr/bin/env bash
# Checks from outside, through Cloudflare, that the public site serves this
# build. Cloudflare rewrites HTML (email obfuscation), so the build is matched
# through a content-hashed asset, which only this build can contain.
set -euo pipefail

site=${1:?usage: accept.sh SITE_URL DIST_DIR}
dist=${2:?usage: accept.sh SITE_URL DIST_DIR}
asset=$(cd "$dist" && find _astro -type f \( -name '*.css' -o -name '*.js' \) 2>/dev/null | sort | head -n 1)
[ -n "$asset" ] || { echo "no content-hashed asset under $dist/_astro to compare" >&2; exit 1; }
want=$(sha256sum "$dist/$asset" | cut -d' ' -f1)

failed=0
check() {
	if [ "$2" = yes ]; then echo "ok  $1"; else echo "BAD $1"; failed=$((failed + 1)); fi
}
status_of() { curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$1" || true; }
is() { [ "$1" = "$2" ] && echo yes || echo no; }

home=000
for _ in $(seq 10); do
	home=$(status_of "$site/")
	[ "$home" = 200 ] && break
	sleep 3
done
check "/ $home" "$(is "$home" 200)"
got=$(curl -sf --max-time 20 "$site/$asset" | sha256sum | cut -d' ' -f1 || true)
check "/$asset matches this build" "$(is "$got" "$want")"
for path in /_headers /.env /no-such-file-deploy-check.js /no-such-page-deploy-check; do
	code=$(status_of "$site$path")
	check "$path $code" "$(is "$code" 404)"
done
redirect=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' --max-time 20 "http://${site#https://}/" || true)
[[ "$redirect" =~ ^30[18]\ https:// ]] && ok=yes || ok=no
check "http:// -> $redirect" "$ok"

if [ "$failed" -ne 0 ]; then
	echo "$failed acceptance check(s) failed" >&2
	exit 1
fi
