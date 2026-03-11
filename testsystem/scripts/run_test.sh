#!/usr/bin/env bash
set -euo pipefail

SCENARIO="${TEST_SCENARIO:-e5_embedding_smoke}"
LOG_ROOT="${TEST_LOG_ROOT:-/workspace/test-logs}"
RUN_ID="${TEST_RUN_ID:-manual-$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM}"
RUN_DIR="${LOG_ROOT%/}/${RUN_ID}"
SCENARIO_SCRIPT="/workspace/testsystem/scenarios/${SCENARIO}.sh"

mkdir -p "$RUN_DIR"
LOG_FILE="$RUN_DIR/run.log"
SUMMARY_FILE="$RUN_DIR/summary.json"

log() {
  printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG_FILE"
}

log "test run start"
log "scenario=${SCENARIO} controller=${CONTROLLER_BASE_URL:-unset}"
log "run_dir=${RUN_DIR}"

if [[ ! -x "$SCENARIO_SCRIPT" ]]; then
  log "scenario script not executable: $SCENARIO_SCRIPT"
  cat >"$SUMMARY_FILE" <<JSON
{
  "run_id": "$RUN_ID",
  "scenario": "$SCENARIO",
  "status": "failed",
  "error": "scenario script not executable: $SCENARIO_SCRIPT",
  "started_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "log_file": "$LOG_FILE"
}
JSON
  exit 1
fi

set +e
"$SCENARIO_SCRIPT" "$RUN_DIR" 2>&1 | tee -a "$LOG_FILE"
SCENARIO_EXIT=${PIPESTATUS[0]}
set -e

STATUS="success"
ERR=""
if [[ $SCENARIO_EXIT -ne 0 ]]; then
  STATUS="failed"
  ERR="scenario exit code=${SCENARIO_EXIT}"
fi

cat >"$SUMMARY_FILE" <<JSON
{
  "run_id": "$RUN_ID",
  "scenario": "$SCENARIO",
  "status": "$STATUS",
  "error": "$ERR",
  "started_at": "$(head -n 1 "$LOG_FILE" | sed -E 's/^\[([^]]+)\].*/\1/' || date -u +%Y-%m-%dT%H:%M:%SZ)",
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "log_file": "$LOG_FILE"
}
JSON

log "test run finished status=${STATUS}"
[[ "$STATUS" == "success" ]]
