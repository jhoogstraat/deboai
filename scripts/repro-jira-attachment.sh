#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat >&2 <<'EOF'
Usage: scripts/repro-jira-attachment.sh TICKET ATTACHMENT

Required environment:
  JIRA_URL          Jira root URL
  JIRA_API_TOKEN    Jira bearer/PAT token

Optional environment:
  JIRA_API_PATH     REST path below JIRA_URL (default: rest/api/2)
  JIRA_COOKIE       Raw Cookie header, for SSO-protected Jira

The script performs read-only requests and never follows redirects. It prints
the status and redirect target with query strings removed, but never prints
the token or Cookie value.
EOF
}

if [[ $# -gt 2 ]]; then
	usage
	exit 2
fi

ticket="${1:-${JIRA_TICKET:-}}"
selector="${2:-${JIRA_ATTACHMENT:-}}"
base_url="${JIRA_URL:-}"
token="${JIRA_API_TOKEN:-}"
api_path="${JIRA_API_PATH:-rest/api/2}"

if [[ -z "$ticket" || -z "$selector" || -z "$base_url" || -z "$token" ]]; then
	usage
	exit 2
fi

for command in curl jq; do
	if ! command -v "$command" >/dev/null 2>&1; then
		echo "missing required command: $command" >&2
		exit 2
	fi
done

ticket=$(printf '%s' "$ticket" | tr '[:lower:]' '[:upper:]')
if [[ ! "$ticket" =~ ^[A-Z][A-Z0-9]+-[0-9]+$ ]]; then
	echo "invalid Jira ticket key: $ticket" >&2
	exit 2
fi

base_url="${base_url%/}"
api_path="${api_path#/}"
api_path="${api_path%/}"

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/deboai-jira-repro.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT

request_headers=(
	-H "Authorization: Bearer $token"
	-H "X-Atlassian-Token: no-check"
)
if [[ -n "${JIRA_COOKIE:-}" ]]; then
	request_headers+=( -H "Cookie: $JIRA_COOKIE" )
fi

redact_url() {
	local value="$1"
	value="${value%%\?*}"
	value="${value%%#*}"
	printf '%s' "$value"
}

probe() {
	local label="$1"
	local url="$2"
	local slug="$3"
	local headers_file="$tmp_dir/$slug.headers"
	local body_file="$tmp_dir/$slug.body"
	local status location content_type body_bytes

	if ! status=$(curl --silent --show-error --request GET \
		--dump-header "$headers_file" --output "$body_file" \
		-H 'Accept: */*' "${request_headers[@]}" \
		--write-out '%{http_code}' --url "$url"); then
		echo "$label: curl failed (HTTP ${status:-000})" >&2
		return 1
	fi
	location=$(sed -n 's/^[Ll][Oo][Cc][Aa][Tt][Ii][Oo][Nn]:[[:space:]]*//p' "$headers_file" | tail -n 1 | tr -d '\r')
	content_type=$(sed -n 's/^[Cc][Oo][Nn][Tt][Ee][Nn][Tt]-[Tt][Yy][Pp][Ee]:[[:space:]]*//p' "$headers_file" | tail -n 1 | tr -d '\r')
	body_bytes=$(wc -c < "$body_file" | tr -d '[:space:]')

	printf '%s: HTTP %s, %s bytes' "$label" "$status" "$body_bytes"
	if [[ -n "$content_type" ]]; then
		printf ', Content-Type %s' "$content_type"
	fi
	if [[ -n "$location" ]]; then
		printf ', Location %s' "$(redact_url "$location")"
	fi
	printf '\n'
}

issue_file="$tmp_dir/issue.json"
issue_url="$base_url/$api_path/issue/$ticket?fields=attachment"
if ! issue_status=$(curl --silent --show-error --request GET \
	--dump-header "$tmp_dir/issue.headers" --output "$issue_file" \
	-H 'Accept: application/json' "${request_headers[@]}" \
	--write-out '%{http_code}' --url "$issue_url"); then
	echo "issue request failed" >&2
	exit 1
fi
if [[ "$issue_status" != 2?? ]]; then
	echo "issue request: HTTP $issue_status" >&2
	exit 1
fi
echo "Issue request: HTTP $issue_status"

id_count=$(jq --arg selector "$selector" \
	'[.fields.attachment[]? | select((.id | tostring) == $selector)] | length' "$issue_file")
if (( id_count > 0 )); then
	attachment=$(jq -c --arg selector "$selector" \
		'first(.fields.attachment[]? | select((.id | tostring) == $selector))' "$issue_file")
else
	filename_count=$(jq --arg selector "$selector" \
		'[.fields.attachment[]? | select(.filename == $selector)] | length' "$issue_file")
	if (( filename_count > 1 )); then
		echo "multiple Jira attachments are named $selector; select by ID" >&2
		exit 1
	fi
	if (( filename_count == 0 )); then
		echo "Jira attachment not found: $selector" >&2
		exit 1
	fi
	attachment=$(jq -c --arg selector "$selector" \
		'first(.fields.attachment[]? | select(.filename == $selector))' "$issue_file")
fi

attachment_id=$(jq -r '.id // empty' <<<"$attachment")
filename=$(jq -r '.filename // empty' <<<"$attachment")
mime_type=$(jq -r '.mimeType // empty' <<<"$attachment")
content_url=$(jq -r '.content // empty' <<<"$attachment")
if [[ -z "$attachment_id" || -z "$content_url" ]]; then
	echo "attachment metadata has no ID or content URL" >&2
	exit 1
fi
if [[ "$content_url" == /* ]]; then
	content_url="$base_url$content_url"
fi

echo "Attachment: id=$attachment_id filename=$filename mimeType=${mime_type:-unknown}"
echo "Metadata content URL: $(redact_url "$content_url")"
probe 'Metadata content request' "$content_url" metadata-content

case "$attachment_id" in
	*[!0-9]*)
		exit 0
		;;
esac
rest_content_url="$base_url/$api_path/attachment/content/$attachment_id?redirect=false"
echo "REST content candidate: $(redact_url "$rest_content_url")"
probe 'REST content request (redirect=false)' "$rest_content_url" rest-content
