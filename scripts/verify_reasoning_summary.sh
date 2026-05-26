#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   BASE_URL=http://localhost:8090 API_KEY=xxx MODEL=glm-4.7 \
#   ./scripts/verify_reasoning_summary.sh
#
# This script prints response headers and saves the SSE body to /tmp.

BASE_URL="${BASE_URL:-http://localhost:8090}"
API_KEY="${API_KEY:-}"
MODEL="${MODEL:-glm-4.7}"
OUT_FILE="${OUT_FILE:-/tmp/axonhub_reasoning_stream.json}"

if [[ -z "${API_KEY}" ]]; then
  echo "ERROR: API_KEY is required" >&2
  exit 1
fi

cat <<'EOF'
== Request ==
POST /v1/responses
Accept: text/event-stream
EOF

curl -sS -N \
  -D /tmp/axonhub_reasoning_headers.txt \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  "${BASE_URL}/v1/responses" \
  -o "${OUT_FILE}" \
  -d @- <<EOF
{
  "model": "${MODEL}",
  "stream": true,
  "input": "Hi",
  "reasoning": {
    "effort": "minimal",
    "summary": "detailed"
  }
}
EOF

echo "== Response Headers =="
cat /tmp/axonhub_reasoning_headers.txt

echo "== Saved Stream =="
echo "${OUT_FILE}"

echo "== Quick Check (summary in output_item.added) =="
grep -n "response.output_item.added" -n "${OUT_FILE}" | head -n 3 || true
grep -n '"summary": \[' -n "${OUT_FILE}" | head -n 3 || true
