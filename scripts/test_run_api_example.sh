#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-http://127.0.0.1:8080}"
TOKEN="${2:-}"
AUTH_ARGS=()
if [[ -n "$TOKEN" ]]; then
  AUTH_ARGS=(-H "Authorization: Bearer $TOKEN")
fi

resp="$(curl -sS "${AUTH_ARGS[@]}" -H 'Content-Type: application/json' -X POST "$BASE_URL/api/v1/test-runs" -d '{"scenario":"e5_embedding_smoke","triggered_by":"script"}')"
echo "$resp" | jq
run_id="$(echo "$resp" | jq -r '.data.test_run_id')"

echo "polling test run: $run_id"
for i in $(seq 1 60); do
  run_json="$(curl -sS "${AUTH_ARGS[@]}" "$BASE_URL/api/v1/test-runs/$run_id")"
  status="$(echo "$run_json" | jq -r '.data.status')"
  echo "[$i] status=$status"
  if [[ "$status" == "success" || "$status" == "failed" ]]; then
    echo "$run_json" | jq
    break
  fi
  sleep 2
done
