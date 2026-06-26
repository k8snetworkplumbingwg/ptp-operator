#!/bin/bash
#
# configGNSS.sh — Deploys the GNSS simulator as a Kubernetes Pod.
#
# gnss-sim runs as a Pod in the openshift-ptp namespace with hostNetwork,
# privileged access, and host device mounts. Kubernetes manages its lifecycle
# with restartPolicy: Always, so it survives test script exits.
#
# The pod auto-detects the kernel GNSS device (/dev/gnss0) and DPLL sysfs
# path at startup. Falls back to PTY-only mode when no kernel GNSS device
# is present.
#
# Usage: ./configGNSS.sh
#   Env: GNSS_SIM_IMAGE  — container image (required, e.g. 192.168.64.52/test:gnss-sim)
#        GNSS_SIM_API_PORT — API port (default 9200)
#
set -euo pipefail

GNSS_SIM_API_PORT="${GNSS_SIM_API_PORT:-9200}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ -z "${GNSS_SIM_IMAGE:-}" ]; then
    echo "ERROR: GNSS_SIM_IMAGE must be set (e.g. 192.168.64.52/test:gnss-sim)"
    exit 1
fi

echo "=== Setting up GNSS simulator pod ==="

# Remove any leftover host gnss-sim processes from older runs
pkill -f 'gnss-sim.*--api-port' || true

# Delete existing pod if present (idempotent re-deploy)
kubectl delete pod gnss-sim -n openshift-ptp --ignore-not-found --wait=false 2>/dev/null || true
kubectl wait --for=delete pod/gnss-sim -n openshift-ptp --timeout=30s 2>/dev/null || true

# Substitute the image placeholder in the manifest and apply
sed "s|GNSS_SIM_IMAGE|${GNSS_SIM_IMAGE}|g" "${SCRIPT_DIR}/gnss-sim-pod.yaml" \
    | kubectl apply -f -

echo "Waiting for gnss-sim pod to become ready..."
if ! kubectl wait --for=condition=ready pod/gnss-sim -n openshift-ptp --timeout=60s; then
    echo "ERROR: gnss-sim pod did not become ready after 60 seconds"
    kubectl describe pod gnss-sim -n openshift-ptp 2>/dev/null || true
    kubectl logs gnss-sim -n openshift-ptp --tail=20 2>/dev/null || true
    exit 1
fi
echo "gnss-sim pod is running and ready"

# Get the Kind node IP where gnss-sim is running (for test framework API access).
# With hostNetwork: true, the pod listens on the node's IP.
GNSS_NODE_IP=$(kubectl get pod gnss-sim -n openshift-ptp \
    -o jsonpath='{.status.hostIP}')
echo "gnss-sim node IP: $GNSS_NODE_IP"

# Verify health endpoint is reachable from the host via the node IP
retries=0
while [ $retries -lt 15 ]; do
    if curl -sf "http://${GNSS_NODE_IP}:${GNSS_SIM_API_PORT}/health" >/dev/null 2>&1; then
        echo "gnss-sim API reachable at http://${GNSS_NODE_IP}:${GNSS_SIM_API_PORT}"
        break
    fi
    sleep 1
    retries=$((retries + 1))
done

if [ $retries -ge 15 ]; then
    echo "WARNING: gnss-sim API not reachable from host at ${GNSS_NODE_IP}:${GNSS_SIM_API_PORT}"
    echo "Pod is running but API may only be reachable from within the cluster"
fi

# Write the API host to a well-known file so callers can source it.
# (exports from a subprocess don't propagate to the parent shell)
echo "$GNSS_NODE_IP" > /tmp/gnss-sim-api-host

# Verify NMEA output
GNSS_KERNEL_DEV=""
for g in /dev/gnss*; do
    [ -c "$g" ] && GNSS_KERNEL_DEV="$g" && break
done

if [ -n "$GNSS_KERNEL_DEV" ]; then
    NMEA_SOURCE="$GNSS_KERNEL_DEV"
    echo "Kernel GNSS device $GNSS_KERNEL_DEV present"
else
    NMEA_SOURCE="/var/run/ptp/ttyGNSS_TS2PHC"
fi

echo "=== GNSS simulator pod setup complete ==="
echo "  Pod:           gnss-sim (openshift-ptp)"
echo "  API:           http://${GNSS_NODE_IP}:${GNSS_SIM_API_PORT}"
echo "  NMEA source:   $NMEA_SOURCE"
