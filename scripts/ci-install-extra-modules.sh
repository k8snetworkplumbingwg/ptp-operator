#!/bin/bash
# Install linux-modules-extra-$(uname -r) when apt has the package.
#
# gnss.ko lives in extra-modules on Azure/GHA kernels; netdevsim will not
# load without those GNSS symbols. Extra-modules is missing on some HWE
# ABIs (e.g. 7.0 generic / Ubuntu 26.04) — skip in that case.
set -euo pipefail

extra="linux-modules-extra-$(uname -r)"
here="$(cd "$(dirname "$0")" && pwd)"

if ! apt-cache show "${extra}" >/dev/null 2>&1; then
  echo "${extra} not in apt — continuing (gnss may already be in linux-modules)"
  exit 0
fi

# 71MB package: 180s per attempt, 3 tries, fallback mirror on retry.
APT_TIMEOUT_SECS=180 APT_ATTEMPTS=3 \
  bash "${here}/ci-apt.sh" install "${extra}"
