#!/usr/bin/env bash
set -euo pipefail

RUN_DIR="${1:?run dir required}"
CONTROLLER_BASE_URL="${CONTROLLER_BASE_URL:-http://127.0.0.1:8080}"
TOKEN="${CONTROLLER_AUTH_TOKEN:-}"
MODEL_ID="${E5_MODEL_ID:-local-multilingual-e5-base}"
REPORT_FILE="$RUN_DIR/stage0_runtime_object_report.json"

AUTH_ARGS=()
if [[ -n "$TOKEN" ]]; then
  AUTH_ARGS=(-H "Authorization: Bearer $TOKEN")
fi

step() {
  printf '[%s] [step] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

json_get() {
  local url="$1"
  curl -sS "${AUTH_ARGS[@]}" "$url"
}

step "discover runtime instance"
instances_json="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-instances")"
instance_row="$(printf '%s' "$instances_json" | jq -c --arg model "$MODEL_ID" '(.data[] | select(.model_id==$model))' | head -n1)"
if [[ -z "$instance_row" ]]; then
  instance_row="$(printf '%s' "$instances_json" | jq -c '.data[0] // empty')"
fi
if [[ -z "$instance_row" ]]; then
  echo "no runtime instance found"
  exit 1
fi

instance_id="$(printf '%s' "$instance_row" | jq -r '.id // ""')"
model_id="$(printf '%s' "$instance_row" | jq -r '.model_id // ""')"
binding_id="$(printf '%s' "$instance_row" | jq -r '.binding_id // ""')"
template_id="$(printf '%s' "$instance_row" | jq -r '.template_id // ""')"
manifest_id="$(printf '%s' "$instance_row" | jq -r '.manifest_id // ""')"

if [[ -z "$instance_id" || -z "$binding_id" || -z "$template_id" ]]; then
  echo "runtime instance chain missing ids: instance=$instance_id binding=$binding_id template=$template_id"
  exit 1
fi

step "verify runtime binding"
binding_json="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-bindings/$binding_id")"
binding_ok="$(printf '%s' "$binding_json" | jq -r '.success // false')"
if [[ "$binding_ok" != "true" ]]; then
  echo "binding query failed: $binding_json"
  exit 1
fi
binding_template_id="$(printf '%s' "$binding_json" | jq -r '.data.template_id // ""')"
binding_model_id="$(printf '%s' "$binding_json" | jq -r '.data.model_id // ""')"
binding_mode="$(printf '%s' "$binding_json" | jq -r '.data.binding_mode // ""')"
if [[ "$binding_template_id" != "$template_id" ]]; then
  echo "binding/template mismatch: binding_template_id=$binding_template_id template_id=$template_id"
  exit 1
fi
if [[ "$binding_model_id" != "$model_id" ]]; then
  echo "binding/model mismatch: binding_model_id=$binding_model_id model_id=$model_id"
  exit 1
fi

step "verify runtime template + manifest"
manifest_json="$(json_get "$CONTROLLER_BASE_URL/api/v1/runtime-templates/$template_id/manifest")"
manifest_ok="$(printf '%s' "$manifest_json" | jq -r '.success // false')"
if [[ "$manifest_ok" != "true" ]]; then
  echo "runtime template manifest query failed: $manifest_json"
  exit 1
fi
manifest_template_id="$(printf '%s' "$manifest_json" | jq -r '.data.template_id // ""')"
manifest_runtime_kind="$(printf '%s' "$manifest_json" | jq -r '.data.runtime_kind // ""')"
manifest_binding_mode="$(printf '%s' "$manifest_json" | jq -r '.data.metadata.binding_mode // ""')"
if [[ -n "$manifest_template_id" && "$manifest_template_id" != "$template_id" ]]; then
  echo "manifest/template mismatch: manifest_template_id=$manifest_template_id template_id=$template_id"
  exit 1
fi

cat >"$REPORT_FILE" <<JSON
{
  "scenario": "stage0_runtime_object_smoke",
  "runtime_instance_id": "$instance_id",
  "model_id": "$model_id",
  "runtime_binding_id": "$binding_id",
  "runtime_template_id": "$template_id",
  "manifest_id": "$manifest_id",
  "binding_mode": "$binding_mode",
  "manifest_runtime_kind": "$manifest_runtime_kind",
  "manifest_template_id": "$manifest_template_id",
  "manifest_binding_mode_hint": "$manifest_binding_mode",
  "status": "success",
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON

step "scenario success instance=$instance_id model=$model_id template=$template_id"
