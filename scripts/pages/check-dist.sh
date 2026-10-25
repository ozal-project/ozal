#!/usr/bin/env bash
# Refuses a build output that carries secrets, before the archive leaves the
# CI runner. Everything in dist/ is public the moment it is deployed, and a
# build tool will inline any env value it is asked to. Prints file names and
# the rule that matched, never the matched value.
set -euo pipefail

dist=${1:?usage: check-dist.sh DIST_DIR}
[ -d "$dist" ] || { echo "no build output at $dist" >&2; exit 1; }
status=0

# Files that only ever hold secrets or repository internals.
while IFS= read -r -d '' file; do
	echo "refused file: ${file#"$dist"/}" >&2
	status=1
done < <(find "$dist" \( -name '.env' -o -name '.env.*' -o -name '*.pem' -o -name '*.key' \
	-o -name 'id_rsa*' -o -name 'id_ed25519*' -o -name '.npmrc' -o -name '.git' \) -print0)

# Well-known credential shapes, as name=regex. Text files only (-I).
rules=(
	"private key=-----BEGIN [A-Z ]*PRIVATE KEY-----"
	"GitHub token=(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}"
	"GitHub fine-grained token=github_pat_[A-Za-z0-9_]{22,}"
	"AWS access key=AKIA[0-9A-Z]{16}"
	"Slack token=xox[abprs]-[A-Za-z0-9-]{10,}"
	"Stripe live key=(sk|rk)_live_[A-Za-z0-9]{16,}"
	"Google API key=AIza[0-9A-Za-z_-]{35}"
)
for rule in "${rules[@]}"; do
	name=${rule%%=*}
	pattern=${rule#*=}
	while IFS= read -r file; do
		[ -n "$file" ] || continue
		echo "refused content in ${file#"$dist"/}: looks like a $name" >&2
		status=1
	done < <(grep -rlIE -e "$pattern" "$dist" || true)
done

if [ "$status" -ne 0 ]; then
	echo "the build output contains secrets: nothing was deployed" >&2
	exit 1
fi
echo "no secrets found in $dist"
