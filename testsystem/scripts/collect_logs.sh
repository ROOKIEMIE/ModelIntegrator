#!/usr/bin/env bash
set -euo pipefail

LOG_ROOT="${1:-${TEST_LOG_ROOT:-./testsystem/logs}}"
if [[ ! -d "$LOG_ROOT" ]]; then
  echo "log root not found: $LOG_ROOT"
  exit 1
fi

echo "log root: $LOG_ROOT"
find "$LOG_ROOT" -maxdepth 2 -type f \( -name '*.log' -o -name '*.json' \) | sort
