#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-http://127.0.0.1:8080}"
TOKEN="${2:-}"
AGENT_ID="${3:-}"
AUTH_ARGS=()
if [[ -n "$TOKEN" ]]; then
  AUTH_ARGS=(-H "Authorization: Bearer $TOKEN")
fi

curl -sS "$BASE_URL/healthz" | jq '{ok:.success,version:.data.version}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/nodes" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/agents" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/models" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/tasks?limit=5" | jq '{ok:.success,count:(.data|length)}'
curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/test-runs?limit=5" | jq '{ok:.success,count:(.data|length)}'

if [[ -n "$AGENT_ID" ]]; then
  curl -sS "${AUTH_ARGS[@]}" -X POST "$BASE_URL/api/v1/tasks/agent/node-local" \
    -H "Content-Type: application/json" \
    -d "{\"agent_id\":\"$AGENT_ID\",\"task_type\":\"agent.resource_snapshot\",\"triggered_by\":\"controller_api_smoke\"}" \
    | jq '{ok:.success,task_id:.data.id,type:.data.type,assigned_agent_id:.data.assigned_agent_id}'
fi
