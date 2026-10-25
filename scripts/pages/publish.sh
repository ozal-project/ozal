#!/usr/bin/env bash
# Uploads a site archive to Switchboard's deploy endpoint with a GitHub
# Actions OIDC token, then confirms the deployment through its status. The job
# needs `permissions: id-token: write`; no deploy secret is stored anywhere.
# Writes the deployment JSON to RESULT_FILE. Stdout carries only the runner's
# ::add-mask:: command, so redirecting it can never capture the token: the
# runner must read that line itself. Credentials go to curl on stdin, never in
# argv or logs.
set -euo pipefail

archive=${1:?usage: publish.sh ARCHIVE RESULT_FILE}
result=${2:?usage: publish.sh ARCHIVE RESULT_FILE}
: "${DEPLOY_ENDPOINT:?set DEPLOY_ENDPOINT, e.g. https://deploy.example.com}"
: "${DEPLOY_PROJECT:?set DEPLOY_PROJECT}"
: "${ACTIONS_ID_TOKEN_REQUEST_URL:?the job needs permissions: id-token: write}"
: "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:?the job needs permissions: id-token: write}"

# The audience is the endpoint itself: Switchboard refuses tokens minted for
# anything else.
audience=$(jq -rn --arg value "$DEPLOY_ENDPOINT" '$value | @uri')
token=$(curl -sSf --max-time 20 --config - "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=${audience}" \
	<<<"header = \"Authorization: bearer ${ACTIONS_ID_TOKEN_REQUEST_TOKEN}\"" | jq -r '.value')
[ -n "$token" ] && [ "$token" != null ] || { echo "GitHub issued no OIDC token" >&2; exit 1; }
echo "::add-mask::$token"
auth="header = \"Authorization: Bearer ${token}\""

headers=$(mktemp)
trap 'rm -f "$headers"' EXIT
response=$(curl -sS --max-time 120 --config - -X POST -D "$headers" \
	-H "Content-Type: application/gzip" --data-binary "@${archive}" \
	-w '\n%{http_code}' "${DEPLOY_ENDPOINT}/v1/projects/${DEPLOY_PROJECT}/deployments" <<<"$auth")
code=${response##*$'\n'}
body=${response%$'\n'*}
if [ "$code" != 201 ]; then
	refusal=$(jq -r '.code // empty' <<<"$body" 2>/dev/null || true)
	echo "deploy refused: HTTP ${code} ${refusal:-(no Switchboard code: a proxy answered?)}" >&2
	if [ -z "$refusal" ]; then
		# Who answered: a Cloudflare challenge carries cf-mitigated, the edge does not.
		grep -iE '^(server|cf-mitigated|cf-ray|content-type):' "$headers" | tr -d '\r' >&2 || true
		printf 'body: %.200s\n' "$(tr -d '\r\n' <<<"$body")" >&2
	fi
	exit 1
fi
id=$(jq -r '.id' <<<"$body")
[ "$(jq -r '.live' <<<"$body")" = true ] || { echo "deployment $id is not live" >&2; exit 1; }

# A status read reuses the upload token: it spends neither the token's jti nor
# the project's deploy budget.
status=$(curl -sSf --max-time 20 --config - \
	"${DEPLOY_ENDPOINT}/v1/projects/${DEPLOY_PROJECT}/deployments/${id}" <<<"$auth")
[ "$(jq -r '.id' <<<"$status")" = "$id" ] || { echo "the status does not name deployment $id" >&2; exit 1; }
printf '%s\n' "$body" > "$result"
