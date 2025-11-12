#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1

JUNIT_OUTPUT_DIR="${JUNIT_OUTPUT_DIR:-/tmp/artifacts}"
JUNIT_OUTPUT_FILE="${JUNIT_OUTPUT_FILE:-unit_report.xml}"
SUITE=../test/conformance
export KUBECONFIG=~/.kube/config
go install github.com/onsi/ginkgo/v2/ginkgo

export MAX_OFFSET_IN_NS=10000
export MIN_OFFSET_IN_NS=-10000

cat <<EOF >config.yaml
global:
  maxoffset: $MAX_OFFSET_IN_NS
  minoffset: $MIN_OFFSET_IN_NS
  holdover_timeout: 5
  DisableAllSlaveRTUpdate: true
EOF
export USE_CONTAINER_CMDS=
export PTP_TEST_CONFIG_FILE="$(pwd)/config.yaml"
export PTP_LOG_LEVEL=info
export GOFLAGS=-mod=vendor
export KEEP_PTPCONFIG=true

export SKIP_INTERFACES="eth0"
export IMAGE_REGISTRY="$VM_IP/"
export CNF_TESTS_IMAGE=test:lptpd

# Configure switch1 for authentication testing
podman cp ptpswitchconfig.cfg switch1:/etc/ptp4l.conf
podman exec switch1 systemctl restart ptp4l
echo "✓ Switch1 restored"

systemctl stop chronyd

set -e
# OC
PTP_TEST_MODE=oc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# BC
PTP_TEST_MODE=bc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual NIC BC
PTP_TEST_MODE=dualnicbc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual NIC BC HA
PTP_TEST_MODE=dualnicbcha ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual port
PTP_TEST_MODE=dualfollower ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial

# Configure switch1 for authentication testing
kubectl apply -f ptp-security.yaml
./configure-switch-ptp-security.sh

# Run OC tests with authentication enabled
PTP_AUTH_ENABLED=true PTP_TEST_MODE=oc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
