#!/usr/bin/env bash
# Install Podman + a current static crun for GitHub Actions / netdevsim CI.
#
# The OBS kubic unstable repo for Ubuntu 24.04 no longer ships a `podman`
# package (only helpers like buildah/netavark), so we use the distro podman
# and overlay a modern crun from GitHub releases. Podman 5 previously needed
# crun >= 1.14.3; Ubuntu noble's crun 1.14.1 rejects OCI runtime spec v1.2.1
# ("OCI runtime error: crun: unknown version specified").
set -euo pipefail

sudo apt-get update
sudo apt-get install -y podman

CRUN_VER=1.28
ARCH="$(dpkg --print-architecture)"
case "${ARCH}" in
  amd64) CRUN_ARCH=amd64 ;;
  arm64) CRUN_ARCH=arm64 ;;
  *) echo "::error::unsupported arch ${ARCH} for crun"; exit 1 ;;
esac

curl -fsSL --connect-timeout 20 --max-time 120 -o /tmp/crun \
  "https://github.com/containers/crun/releases/download/${CRUN_VER}/crun-${CRUN_VER}-linux-${CRUN_ARCH}"
sudo install -m 0755 /tmp/crun /usr/local/bin/crun
sudo install -m 0755 /tmp/crun /usr/bin/crun
hash -r

podman --version
crun --version
sudo crun --version
sudo podman run --rm quay.io/libpod/alpine:latest echo ok

# Serialize podman CLI via one API server (avoids Kind exec SQLite deadlock).
sudo bash "${GITHUB_WORKSPACE}/scripts/start-podman-api-service.sh"
