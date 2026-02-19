#!/bin/bash
#
# Usage: ./run-tests.sh <VM_IP>
#
# Environment variables:
#   RUN_KIND        - serial | parallel | both  (default: serial)
#   TEST_MODES      - comma-separated modes     (default: oc,bc,dualnicbc,dualnicbcha,dualfollower)
#   KUBECONFIG      - path to kubeconfig        (default: ~/.kube/config)
#   IMAGE_REGISTRY  - registry prefix           (default: <VM_IP>/)
#   CNF_TESTS_IMAGE - test image                (default: test:lptpd)
#   PTP_LOG_LEVEL   - log level                 (default: info)
#
set -x
set -euo pipefail

VM_IP="${1:-}"
if [[ -z "${VM_IP}" ]]; then
  echo "Error: VM_IP is required as the first argument."
  echo "Usage: $0 <VM_IP>"
  exit 1
fi

JUNIT_OUTPUT_DIR="${JUNIT_OUTPUT_DIR:-/tmp/artifacts}"
JUNIT_OUTPUT_FILE="${JUNIT_OUTPUT_FILE:-unit_report.xml}"
SUITE=../test/conformance
export KUBECONFIG=${KUBECONFIG:-~/.kube/config}
go install github.com/onsi/ginkgo/v2/ginkgo

mkdir -p "$JUNIT_OUTPUT_DIR"

export MAX_OFFSET_IN_NS="${MAX_OFFSET_IN_NS:-10000}"
export MIN_OFFSET_IN_NS="${MIN_OFFSET_IN_NS:--10000}"

cat <<EOF >config.yaml
global:
  maxoffset: $MAX_OFFSET_IN_NS
  minoffset: $MIN_OFFSET_IN_NS
  holdover_timeout: 5
  DisableAllSlaveRTUpdate: true
EOF
export USE_CONTAINER_CMDS=
export PTP_TEST_CONFIG_FILE="$(pwd)/config.yaml"
export PTP_LOG_LEVEL="${PTP_LOG_LEVEL:-info}"
export GOFLAGS=-mod=vendor
export KEEP_PTPCONFIG="${KEEP_PTPCONFIG:-true}"

export SKIP_INTERFACES="${SKIP_INTERFACES:-eth0}"
export IMAGE_REGISTRY="${IMAGE_REGISTRY:-$VM_IP/}"
export CNF_TESTS_IMAGE="${CNF_TESTS_IMAGE:-test:lptpd}"

RUN_KIND="${RUN_KIND:-serial}"
TEST_MODES_RAW="${TEST_MODES:-oc,bc,dualnicbc,dualnicbcha,dualfollower}"
IFS=',' read -r -a TEST_MODES <<< "${TEST_MODES_RAW}"

SKIP_PATTERNS=(
  ".*The interfaces supporting ptp can be discovered correctly.*"
  "Negative - run pmc in a new unprivileged pod on the slave node.*"
)

case "${RUN_KIND}" in
  serial|parallel|both) ;;
  *)
    echo "Invalid RUN_KIND value: ${RUN_KIND}. Use serial, parallel, or both."
    exit 2
    ;;
esac

# Function to disable switch1 authentication
disable_switch_auth() {
    echo "Disabling switch1 authentication..."
    podman cp ptpswitchconfig.cfg switch1:/etc/ptp4l.conf
    podman exec switch1 systemctl restart ptp4l
    echo "✓ Switch1 authentication disabled"
}

# Function to enable switch1 authentication
enable_switch_auth() {
    echo "Configuring switch1 with PTP authentication..."
    
    # 1. Copy auth-enabled ptp4l.conf to switch1
    podman cp test-config/ptpswitchconfig_auth.cfg switch1:/etc/ptp4l.conf
    
    # 2. Create directory and copy security file
    podman exec switch1 mkdir -p /etc/ptp-secret-mount/ptp-security-conf
    podman cp test-config/ptp-security.conf switch1:/etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf
    
    # 3. Restart ptp4l with authentication enabled
    podman exec switch1 systemctl restart ptp4l || {
    echo "WARNING: systemctl restart failed, trying pkill..."
    podman exec switch1 pkill ptp4l 2>/dev/null || true
    sleep 2
}
    
    echo "✓ Switch1 configured with authentication"
}

disable_switch_auth

systemctl stop chronyd

set -e

run_ginkgo_suite() {
  local mode="$1"
  local suite_kind="$2"
  local junit_base="${JUNIT_OUTPUT_FILE%.xml}"
  local ginkgo_args=(
    --keep-going
    --output-dir="${JUNIT_OUTPUT_DIR}"
    --junit-report="${junit_base}_${mode}_${suite_kind}.xml"
    -v
  )

  for skip in "${SKIP_PATTERNS[@]}"; do
    ginkgo_args+=(--skip="${skip}")
  done

  if [[ "${suite_kind}" == "parallel" ]]; then
    PTP_TEST_MODE="${mode}" ginkgo -p "${ginkgo_args[@]}" "${SUITE}/parallel"
  else
    PTP_TEST_MODE="${mode}" ginkgo "${ginkgo_args[@]}" "${SUITE}/serial"
  fi
}

for mode in "${TEST_MODES[@]}"; do
  if [[ "${RUN_KIND}" == "serial" || "${RUN_KIND}" == "both" ]]; then
    run_ginkgo_suite "${mode}" "serial"
  fi
  if [[ "${RUN_KIND}" == "parallel" || "${RUN_KIND}" == "both" ]]; then
    run_ginkgo_suite "${mode}" "parallel"
  fi
done

# Configure switch1 for authentication testing
# kubectl apply -f test-config/ptp-security.yaml
# enable_switch_auth

# Run tests with authentication enabled
# tests with auth will be enabled once the ci-github tests can last more than 1 hour
# PTP_AUTH_ENABLED=true PTP_TEST_MODE=oc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
