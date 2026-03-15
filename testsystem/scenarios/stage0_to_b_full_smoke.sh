#!/usr/bin/env bash
set -euo pipefail

RUN_DIR="${1:?run dir required}"
REPORT_FILE="$RUN_DIR/stage0_to_b_full_report.json"

step() {
  printf '[%s] [step] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

run_phase() {
  local scenario="$1"
  local phase_dir="$RUN_DIR/$scenario"
  mkdir -p "$phase_dir"
  step "run phase=$scenario"
  "$(dirname "$0")/${scenario}.sh" "$phase_dir"
}

phases=(
  "stage0_runtime_object_smoke"
  "local_agent_execution_smoke"
  "e5_embedding_smoke"
  "e5_gating_blocked_smoke"
)

phase_statuses=()
for phase in "${phases[@]}"; do
  if run_phase "$phase"; then
    phase_statuses+=("$phase:success")
  else
    phase_statuses+=("$phase:failed")
    echo "phase failed: $phase"
    exit 1
  fi
done

status_json="$(printf '%s\n' "${phase_statuses[@]}" | jq -R -s -c 'split("\n") | map(select(length>0))')"

cat >"$REPORT_FILE" <<JSON
{
  "scenario": "stage0_to_b_full_smoke",
  "phases": $status_json,
  "status": "success",
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON

step "scenario success phases=${phase_statuses[*]}"
