#!/usr/bin/env bash
set -euo pipefail

RUN_DIR="${1:?run dir required}"
CONTROLLER_BASE_URL="${CONTROLLER_BASE_URL:-http://127.0.0.1:8080}"
TOKEN="${CONTROLLER_AUTH_TOKEN:-}"
MODEL_ID="${E5_MODEL_ID:-local-multilingual-e5-base}"
EXPECTED_DIM="${E5_EXPECTED_DIM:-768}"
REPORT_FILE="$RUN_DIR/e5_embedding_report.json"

AUTH_ARGS=()
if [[ -n "$TOKEN" ]]; then
  AUTH_ARGS=(-H "Authorization: Bearer $TOKEN")
fi

step() {
  printf '[%s] [step] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

json_post() {
  local url="$1"
  local data="$2"
  curl -sS "${AUTH_ARGS[@]}" -H "Content-Type: application/json" -X POST "$url" -d "$data"
}

json_get() {
  local url="$1"
  curl -sS "${AUTH_ARGS[@]}" "$url"
}

step "create runtime.start task"
start_resp="$(json_post "$CONTROLLER_BASE_URL/api/v1/tasks/runtime/start" "{\"model_id\":\"$MODEL_ID\",\"triggered_by\":\"testsystem\"}")"
start_task_id="$(printf '%s' "$start_resp" | jq -r '.data.id // empty')"
if [[ -z "$start_task_id" ]]; then
  echo "cannot create start task: $start_resp"
  exit 1
fi

step "wait start task=$start_task_id"
for i in $(seq 1 80); do
  task_json="$(json_get "$CONTROLLER_BASE_URL/api/v1/tasks/$start_task_id")"
  task_status="$(printf '%s' "$task_json" | jq -r '.data.status // ""')"
  if [[ "$task_status" == "success" ]]; then
    break
  fi
  if [[ "$task_status" == "failed" || "$task_status" == "timeout" || "$task_status" == "canceled" ]]; then
    echo "start task failed: $task_json"
    exit 1
  fi
  sleep 1.5
done

step "poll readiness"
endpoint=""
readiness=""
for i in $(seq 1 40); do
  _="$(json_post "$CONTROLLER_BASE_URL/api/v1/tasks/runtime/refresh" "{\"model_id\":\"$MODEL_ID\",\"triggered_by\":\"testsystem\"}")"
  model_json="$(json_get "$CONTROLLER_BASE_URL/api/v1/models/$MODEL_ID")"
  endpoint="$(printf '%s' "$model_json" | jq -r '.data.endpoint // ""')"
  readiness="$(printf '%s' "$model_json" | jq -r '.data.readiness // ""')"
  observed="$(printf '%s' "$model_json" | jq -r '.data.observed_state // ""')"
  echo "poll[$i] readiness=$readiness observed=$observed endpoint=$endpoint"
  if [[ "$readiness" == "ready" && -n "$endpoint" ]]; then
    break
  fi
  sleep 2
done

if [[ "$readiness" != "ready" || -z "$endpoint" ]]; then
  echo "model not ready, readiness=$readiness endpoint=$endpoint"
  exit 1
fi

step "request embedding (TEI /embed first)"
embed_resp="$(curl -sS -H "Content-Type: application/json" -X POST "${endpoint%/}/embed" -d '{"inputs":"hello world"}' || true)"
vector_dim="$(printf '%s' "$embed_resp" | jq -r 'if type=="array" then (.[0] // . | length) elif type=="object" and (.data|type)=="array" then (.data[0].embedding|length) else 0 end' 2>/dev/null || echo 0)"

if [[ "$vector_dim" -le 0 ]]; then
  step "fallback OpenAI /v1/embeddings"
  embed_resp="$(curl -sS -H "Content-Type: application/json" -X POST "${endpoint%/}/v1/embeddings" -d '{"input":["hello world"]}')"
  vector_dim="$(printf '%s' "$embed_resp" | jq -r '.data[0].embedding | length')"
fi

if [[ "$vector_dim" -le 0 ]]; then
  echo "invalid embedding response: $embed_resp"
  exit 1
fi

if [[ "$EXPECTED_DIM" -gt 0 && "$vector_dim" -ne "$EXPECTED_DIM" ]]; then
  echo "embedding dim mismatch: got=$vector_dim expected=$EXPECTED_DIM"
  exit 1
fi

cat >"$REPORT_FILE" <<JSON
{
  "scenario": "e5_embedding_smoke",
  "model_id": "$MODEL_ID",
  "endpoint": "$endpoint",
  "readiness": "$readiness",
  "dimension": $vector_dim,
  "expected_dimension": $EXPECTED_DIM,
  "status": "success",
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON

step "scenario success dimension=$vector_dim"
