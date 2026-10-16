#!/bin/bash
# Run apt-get with a hard timeout and retries.
#
# A stalled Azure mirror fetch prints Get: then waits forever. apt's own
# Acquire::Retries does not fire because the download never fails. Kill
# the apt-get process (not just the shell), recover dpkg, and retry.
# Attempt 2+ switches azure.archive.ubuntu.com -> archive.ubuntu.com.
#
# Usage: ci-apt.sh update
#        ci-apt.sh install pkg [pkg...]
set -euo pipefail

ATTEMPTS="${APT_ATTEMPTS:-3}"
TIMEOUT_SECS="${APT_TIMEOUT_SECS:-120}"
cmd="$1"
shift

APT_OPTS=(
  -o Acquire::ForceIPv4=true
  -o Acquire::http::Pipeline-Depth=0
  -o Acquire::http::Timeout=20
  -o Acquire::https::Timeout=20
  -o Acquire::Retries=0
)

recover_dpkg() {
  sudo killall -9 apt-get apt dpkg 2>/dev/null || true
  sleep 1
  sudo rm -f /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock \
    /var/cache/apt/archives/lock /var/lib/apt/lists/lock
  sudo dpkg --configure -a || true
}

switch_to_archive_ubuntu() {
  echo "switching apt mirror azure.archive.ubuntu.com -> archive.ubuntu.com"
  sudo grep -rl 'azure.archive.ubuntu.com' /etc/apt 2>/dev/null \
    | while read -r f; do
        sudo sed -i 's/azure.archive.ubuntu.com/archive.ubuntu.com/g' "$f"
      done
}

for i in $(seq 1 "${ATTEMPTS}"); do
  echo "apt ${cmd} attempt ${i}/${ATTEMPTS} (timeout ${TIMEOUT_SECS}s)"
  if [[ "${i}" -ge 2 ]]; then
    switch_to_archive_ubuntu
  fi

  set +e
  sudo timeout --kill-after=15 "${TIMEOUT_SECS}" \
    apt-get "${APT_OPTS[@]}" "${cmd}" -y "$@"
  rc=$?
  set -e

  if [[ "${rc}" -eq 0 ]]; then
    exit 0
  fi
  echo "apt ${cmd} attempt ${i} failed rc=${rc}; recovering dpkg"
  recover_dpkg
  sleep $((i * 5))
done

echo "::error::apt ${cmd} failed after ${ATTEMPTS} attempts"
exit 1
