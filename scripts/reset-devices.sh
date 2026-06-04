#!/bin/bash
set -x
set -euo pipefail

# Ensure CLOCK_TAI has the correct leap-second offset so that
# ktime_get_clocktai_ns() returns real TAI.  Without this, the
# mock PHC's extts timestamps are in UTC and ts2phc (which converts
# NMEA to TAI via the leapfile) sees a permanent ~37 s offset.
python3 -c "
import ctypes, time
class Timex(ctypes.Structure):
    _fields_ = [('modes',ctypes.c_uint),('offset',ctypes.c_long),
        ('freq',ctypes.c_long),('maxerror',ctypes.c_long),
        ('esterror',ctypes.c_long),('status',ctypes.c_int),
        ('constant',ctypes.c_long),('precision',ctypes.c_long),
        ('tolerance',ctypes.c_long),('time_sec',ctypes.c_long),
        ('time_usec',ctypes.c_long),('tick',ctypes.c_long),
        ('ppsfreq',ctypes.c_long),('jitter',ctypes.c_long),
        ('shift',ctypes.c_int),('stabil',ctypes.c_long),
        ('jitcnt',ctypes.c_long),('calcnt',ctypes.c_long),
        ('errcnt',ctypes.c_long),('stbcnt',ctypes.c_long),
        ('tai',ctypes.c_int)]
tx = Timex(modes=0x0080, constant=37)
ctypes.CDLL('libc.so.6').adjtimex(ctypes.byref(tx))
d = time.clock_gettime(11) - time.clock_gettime(0)
print(f'TAI offset set: {d:.0f}s')
" 2>/dev/null || true

# Always do a full unload/reload cycle so updated DKMS modules
# on disk are picked up by the running kernel.
modprobe -r netdevsim 2>/dev/null || true
modprobe -r nsim_dpll 2>/dev/null || true
modprobe -r nsim_ptp_mock 2>/dev/null || true
modprobe -r nsim_ptp 2>/dev/null || true
modprobe -r gnss 2>/dev/null || true

modprobe gnss
if [[ "${DKMS_MODE:-}" == "true" ]]; then
    modprobe nsim_ptp
    modprobe nsim_dpll
fi
modprobe netdevsim pci_bus_nr=0x1f
chmod 666 /dev/nsim_ptp* 2>/dev/null || true
udevadm trigger --subsystem-match=gnss 2>/dev/null || true
udevadm settle
modprobe openvswitch