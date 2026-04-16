#!/bin/bash
set -x
set -euo pipefail

modprobe -r netdevsim
# Reload gnss module to reset the GNSS IDA minor allocator so new
# devices start from gnss0.
modprobe -r gnss 2>/dev/null || true
modprobe gnss
modprobe netdevsim pci_bus_nr=0x1f
modprobe openvswitch