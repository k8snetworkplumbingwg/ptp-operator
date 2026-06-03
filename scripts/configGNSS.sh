#!/bin/bash
#
# configGNSS.sh — Sets up the GNSS simulator in the CI environment.
#
# When a kernel GNSS device exists (/dev/gnss0, created by netdevsim's
# DPLL+GNSS emulation), gnss-sim writes NMEA directly into the kernel
# device. Readers (ts2phc, gpsd) open /dev/gnss0 and receive the stream
# from the kernel's read FIFO — no PTY needed for the primary NMEA
# consumer.
#
# A second PTY is still created for /dev/ttyGNSS_GNSS0 so a secondary
# consumer (e.g. gpsd) can read independently.
#
# Falls back to pure PTY mode when no kernel GNSS device is present.
#
# Usage: ./configGNSS.sh
#
set -x
set -euo pipefail

GNSS_SIM_API_PORT="${GNSS_SIM_API_PORT:-9200}"

# Auto-detect the first kernel GNSS char device if not explicitly set.
if [ -z "${GNSS_KERNEL_DEV:-}" ]; then
    for g in /dev/gnss*; do
        [ -c "$g" ] && GNSS_KERNEL_DEV="$g" && break
    done
fi
GNSS_KERNEL_DEV="${GNSS_KERNEL_DEV:-/dev/gnss0}"

echo "=== Setting up GNSS simulator ==="

# Kill any previous gnss-sim process
pkill -f 'gnss-sim.*--api-port' || true
rm -f /dev/ttyGNSS_TS2PHC /dev/ttyGNSS_GNSS0

# Build gnss-sim directly from source.
GNSS_SIM_BIN="/usr/local/bin/gnss-sim"
GNSS_SIM_SRC="$(cd "$(dirname "$0")/../ptp-tools/gnss-sim" && pwd)"
echo "Building gnss-sim from $GNSS_SIM_SRC"
( cd "$GNSS_SIM_SRC" && CGO_ENABLED=0 go build -o "$GNSS_SIM_BIN" . )
chmod +x "$GNSS_SIM_BIN"

echo "=== Starting GNSS simulator on host ==="

if [ -c "$GNSS_KERNEL_DEV" ]; then
    echo "Kernel GNSS device found at $GNSS_KERNEL_DEV — using hybrid mode"
    "$GNSS_SIM_BIN" \
        --gnss-dev "$GNSS_KERNEL_DEV" \
        --pty-links /dev/ttyGNSS_GNSS0 \
        --api-port "${GNSS_SIM_API_PORT}" &
    GNSS_PID=$!
    NMEA_SOURCE="$GNSS_KERNEL_DEV"
else
    echo "No kernel GNSS device found — using PTY-only mode"
    "$GNSS_SIM_BIN" \
        --pty-links /dev/ttyGNSS_TS2PHC,/dev/ttyGNSS_GNSS0 \
        --api-port "${GNSS_SIM_API_PORT}" &
    GNSS_PID=$!
    NMEA_SOURCE="/dev/ttyGNSS_TS2PHC"
fi
echo "gnss-sim PID: $GNSS_PID"

# Wait for the GNSS simulator to be ready
echo "Waiting for GNSS simulator to become healthy..."
retries=0
while [ $retries -lt 30 ]; do
    if curl -sf "http://localhost:${GNSS_SIM_API_PORT}/health" >/dev/null 2>&1; then
        echo "GNSS simulator is healthy"
        break
    fi
    sleep 1
    retries=$((retries + 1))
done

if [ $retries -ge 30 ]; then
    echo "ERROR: GNSS simulator did not become healthy after 30 seconds"
    exit 1
fi

# Verify GNSS outputs
if [ -c "$GNSS_KERNEL_DEV" ]; then
    echo "Kernel GNSS device $GNSS_KERNEL_DEV present"
else
    if [ ! -e /dev/ttyGNSS_TS2PHC ]; then
        echo "ERROR: PTY symlink /dev/ttyGNSS_TS2PHC not found on host"
        exit 1
    fi
    echo "PTY symlink /dev/ttyGNSS_TS2PHC exists"
fi

if [ -e /dev/ttyGNSS_GNSS0 ]; then
    echo "PTY symlink /dev/ttyGNSS_GNSS0 exists"
fi

# Verify NMEA output on the secondary PTY
if [ -e /dev/ttyGNSS_GNSS0 ]; then
    echo "Verifying NMEA output on /dev/ttyGNSS_GNSS0..."
    NMEA_LINE=$(timeout 3 head -n 1 /dev/ttyGNSS_GNSS0 2>/dev/null || true)
    if echo "$NMEA_LINE" | grep -q "GNRMC\|GNGGA\|GPZDA"; then
        echo "GNSS simulator producing valid NMEA: $NMEA_LINE"
    else
        echo "WARNING: Could not read NMEA from /dev/ttyGNSS_GNSS0 (may need a moment to start)"
    fi
fi

echo "=== GNSS simulator setup complete ==="
echo "  PID:           $GNSS_PID"
echo "  API:           http://localhost:${GNSS_SIM_API_PORT}"
echo "  NMEA source:   $NMEA_SOURCE"
echo "  GNSS device:   /dev/ttyGNSS_GNSS0"
