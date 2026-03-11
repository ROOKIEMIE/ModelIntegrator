#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${1:-http://127.0.0.1:58001}"
TEXT="${2:-hello world}"

echo "[info] endpoint=$ENDPOINT"

resp="$(curl -sS -H 'Content-Type: application/json' -X POST "${ENDPOINT%/}/embed" -d "{\"inputs\":\"$TEXT\"}" || true)"
dim="$(printf '%s' "$resp" | jq -r 'if type=="array" then (.[0] // . | length) else 0 end' 2>/dev/null || echo 0)"

if [[ "$dim" -le 0 ]]; then
  resp="$(curl -sS -H 'Content-Type: application/json' -X POST "${ENDPOINT%/}/v1/embeddings" -d "{\"input\":[\"$TEXT\"]}")"
  dim="$(printf '%s' "$resp" | jq -r '.data[0].embedding | length')"
fi

echo "[info] embedding_dim=$dim"
echo "$resp"
