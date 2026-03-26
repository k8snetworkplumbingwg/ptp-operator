#!/bin/bash
set -euo pipefail
set -x

# Save full run output under /tmp/ptp-operator (timestamped file; also shown on the terminal).
mkdir -p /tmp/ptp-operator
RUN_ON_VM_LOG="/tmp/ptp-operator/run-on-vm-$(date +%Y%m%d-%H%M%S).log"

# Prefix all script output lines with timestamp, then tee to RUN_ON_VM_LOG.
exec > >(awk '{ print strftime("%Y-%m-%d %H:%M:%S"), $0; fflush(); }' | tee "$RUN_ON_VM_LOG") 2>&1
echo "Full script log: $RUN_ON_VM_LOG"

PS4='+ [$(date "+%Y-%m-%d %H:%M:%S")] '

VM_IP=$1

COLOR_STEP='\033[1;36m'
COLOR_PHASE='\033[1;35m'
COLOR_OK='\033[1;32m'
COLOR_ERR='\033[1;31m'
COLOR_RESET='\033[0m'

step() {
  echo -e "${COLOR_STEP}STEP:${COLOR_RESET} $*"
}

run_quiet_with_log_dump_on_failure() {
  local phase_type="$1"
  local phase="$2"
  local phase_label="${phase_type} ${phase}"
  shift
  shift

  local log_file
  log_file="$(mktemp "/tmp/ptp-operator/${phase_label// /_}.XXXXXX.log")"

  echo -e "${COLOR_PHASE}START ${phase_type}:${COLOR_RESET} ${phase}"
  if "$@" >"${log_file}" 2>&1; then
    echo -e "${COLOR_OK}END ${phase_type}:${COLOR_RESET} ${phase}"
    rm -f "${log_file}"
    return 0
  fi

  local rc=$?
  echo -e "${COLOR_ERR}FAIL ${phase_type}:${COLOR_RESET} ${phase} (exit code ${rc})"
  echo -e "${COLOR_ERR}---- BEGIN ${phase_label} LOG ----${COLOR_RESET}"
  cat "${log_file}"
  echo -e "${COLOR_ERR}---- END ${phase_label} LOG ----${COLOR_RESET}"
  rm -f "${log_file}"
  return "${rc}"
}

# Navigate to the directory where this script is located
step "Switching to script directory"
cd "$(dirname "${BASH_SOURCE[0]}")"

echo "Now in: $(pwd)"

export GOMAXPROCS=$(nproc)

step "Installing required tools"
source ./install-tools.sh

# refresh bashrc
export BASHRCSOURCED=1
source ~/.bashrc

# cleaning go deps
step "Tidying and vendoring Go dependencies"
go mod tidy
go mod vendor

# Clean containers
step "Cleaning existing kind cluster and containers"
kind delete cluster --name kind-netdevsim || true
podman rm -f switch1 || true

# Build images
step "Building ptp-tools images"
cd ../ptp-tools
# configure images for local registry
export IMG_PREFIX="$VM_IP/test"

# Clean all images and manifests
make -j 5 podman-cleanall

# Build images
run_quiet_with_log_dump_on_failure "IMAGE BUILD" "ptp-tools podman-buildall" make -j 5 podman-buildall
cd -

# build kustomize
step "Building kustomize"
cd ..
make kustomize
cd -
step "Creating local registry"
./create-local-registry.sh "$VM_IP"

# push images
step "Pushing images to local registry"
cd ../ptp-tools
run_quiet_with_log_dump_on_failure "IMAGE PUSH" "ptp-tools podman-pushall" make -j 5 podman-pushall
cd -

# deploy kind cluster
step "Starting kind cluster"
./k8s-start.sh "$VM_IP"

# deploy ptp-operator
step "Deploying ptp-operator manifests"
cd ../ptp-tools
# Start deployment, it will fail because it is missing certs
make deploy-all || true
sleep 5
cd -

# Build certificates
step "Applying certificate manifests"
kubectl apply -f certs.yaml

# Fix certificates
step "Fixing certificates"
./retry.sh 60 5 ./fix-certs.sh
sleep 5

# delete ptp-operator pod
step "Restarting ptp-operator pod"
kubectl delete pods -l name=ptp-operator -n openshift-ptp

# wait for operator to come up
step "Waiting for ptp-operator rollout"
kubectl rollout status deployment ptp-operator -n openshift-ptp

# Patch ptpoperatorconfig to start events (in case it is not configured yet )
step "Patching ptpoperatorconfig for event publishing"
./retry.sh 60 5 kubectl patch ptpoperatorconfig default -nopenshift-ptp --type=merge --patch '{"spec": {"ptpEventConfig": {"enableEventPublisher": true, "transportHost": "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043", "storageType": "local-sc"}, "daemonNodeSelector": {"node-role.kubernetes.io/worker": ""}}}'

step "Waiting for linuxptp-daemon rollout"
./retry.sh 30 3 kubectl rollout status ds linuxptp-daemon -n openshift-ptp

# Fix prometheus monitoring
step "Fixing Prometheus monitoring"
./fix-ptp-prometheus-monitoring.sh

step "Listing openshift-ptp pods"
kubectl get pods -n openshift-ptp -o wide

# run tests
step "Running serial conformance tests"
./run-tests.sh --kind serial --mode oc,bc,dualnicbc,dualnicbcha,dualfollower \
  --linuxptp-daemon-image "$VM_IP/test:lptpd" \
  --must-gather-image "$VM_IP/test:ptpmg" \
  --debug-image "$VM_IP/test:debug"
