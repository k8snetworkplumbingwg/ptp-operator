#!/bin/bash
#
# Stability test harness for ptp-operator test suites.
#
# Runs the test suite N times for a given mode (or all modes) and collects
# JUnit XML results into per-run directories. After all runs complete, calls
# aggregate-stability.py to produce a stability report.
#
# Usage:
#   ./stability-test.sh --mode <mode|all> [--runs N] [OPTIONS]
#
# Required:
#   --mode <mode>   Test mode (oc, bc, dualnicbc, dualnicbcha, dualfollower)
#                   or "all" for all modes sequentially
#
# Optional:
#   --runs <N>                    Number of runs (default: 10)
#   --output-dir <path>           Base directory for results (default: /tmp/stability-results)
#   --linuxptp-daemon-image <url> Passed through to run-tests.sh
#   --must-gather-image <url>     Passed through to run-tests.sh
#   --debug-image <url>           Passed through to run-tests.sh
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

MODE=""
NUM_RUNS=10
OUTPUT_DIR="/tmp/stability-results"
PASSTHROUGH_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)       MODE="$2"; shift 2 ;;
    --runs)       NUM_RUNS="$2"; shift 2 ;;
    --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
    --linuxptp-daemon-image|--must-gather-image|--debug-image)
      PASSTHROUGH_ARGS+=("$1" "$2"); shift 2 ;;
    *)
      echo "Unknown flag: $1"; exit 1 ;;
  esac
done

if [[ -z "${MODE}" ]]; then
  echo "Error: --mode is required (oc, bc, dualnicbc, dualnicbcha, dualfollower, or all)"
  exit 1
fi

if [[ "${MODE}" == "all" ]]; then
  MODES=(oc bc dualnicbc dualnicbcha dualfollower)
else
  IFS=',' read -r -a MODES <<< "${MODE}"
fi

TIMESTAMP=$(date +%Y%m%d-%H%M%S)

echo "=============================================="
echo " PTP-Operator Test Stability Harness"
echo "=============================================="
echo " Modes:   ${MODES[*]}"
echo " Runs:    ${NUM_RUNS}"
echo " Output:  ${OUTPUT_DIR}"
echo " Started: $(date)"
echo "=============================================="

run_suite_for_mode() {
  local mode="$1"
  local mode_dir="${OUTPUT_DIR}/${mode}"
  mkdir -p "${mode_dir}"

  local passed=0
  local failed=0

  for run_num in $(seq 1 "${NUM_RUNS}"); do
    local padded
    padded=$(printf "%03d" "${run_num}")
    local run_dir="${mode_dir}/run-${padded}"
    mkdir -p "${run_dir}"

    echo ""
    echo "── [${mode}] Run ${run_num}/${NUM_RUNS} ──────────────────────────"
    echo "  Output dir: ${run_dir}"
    echo "  Start time: $(date)"

    local start_ts
    start_ts=$(date +%s)

    # Raw stability data: disable flake retries so every failure is visible.
    # Clean state between runs so PTP configs don't leak.
    JUNIT_OUTPUT_DIR="${run_dir}" \
    FLAKE_ATTEMPTS=1 \
    KEEP_PTPCONFIG=false \
      "${SCRIPT_DIR}/run-tests.sh" \
        --kind serial \
        --mode "${mode}" \
        "${PASSTHROUGH_ARGS[@]}" \
      > "${run_dir}/output.log" 2>&1 \
      && run_rc=0 || run_rc=$?

    local end_ts
    end_ts=$(date +%s)
    local duration=$(( end_ts - start_ts ))

    echo "${run_rc}" > "${run_dir}/exit_code"
    echo "${duration}" > "${run_dir}/duration_seconds"

    if [[ ${run_rc} -eq 0 ]]; then
      echo "  Result: PASSED (${duration}s)"
      (( passed++ ))
    else
      echo "  Result: FAILED exit=${run_rc} (${duration}s)"
      (( failed++ ))
    fi
  done

  echo ""
  echo "── [${mode}] Summary: ${passed}/${NUM_RUNS} runs passed, ${failed}/${NUM_RUNS} failed ──"
}

for mode in "${MODES[@]}"; do
  run_suite_for_mode "${mode}"
done

echo ""
echo "=============================================="
echo " All runs complete. Generating stability report..."
echo "=============================================="

REPORT_FILE="${OUTPUT_DIR}/stability-report-${TIMESTAMP}.md"

if command -v python3 &>/dev/null; then
  python3 "${SCRIPT_DIR}/aggregate-stability.py" \
    --results-dir "${OUTPUT_DIR}" \
    --output "${REPORT_FILE}" \
    --modes "${MODES[*]}"

  echo ""
  echo "Report written to: ${REPORT_FILE}"
  echo ""
  cat "${REPORT_FILE}"
else
  echo "WARNING: python3 not found, skipping report generation."
  echo "Run manually: python3 ${SCRIPT_DIR}/aggregate-stability.py --results-dir ${OUTPUT_DIR} --output ${REPORT_FILE} --modes '${MODES[*]}'"
fi

echo ""
echo "Finished: $(date)"
