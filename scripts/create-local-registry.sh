#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1

mkdir -p ~/registry

# Generate the private key/cert
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes  -keyout ~/registry/registry.key -out ~/registry/registry.crt -subj "/CN=registry"  -addext "subjectAltName=DNS:registry,DNS:localhost,IP:$VM_IP"

openssl x509 -in ~/registry/registry.crt -out ~/registry/registry.pem -outform PEM

if [[ "${DKMS_MODE:-}" == "true" ]]; then
    cp ~/registry/registry.pem /usr/local/share/ca-certificates/registry.crt
    update-ca-certificates
else
    sudo mv ~/registry/registry.pem /etc/pki/ca-trust/source/anchors/
    update-ca-trust
fi

# Docker Hub occasionally returns HTTP 500 on registry:2 pulls; retry a few times.
REGISTRY_IMG="${REGISTRY_IMG:-docker.io/library/registry:2}"
for attempt in 1 2 3 4 5; do
  if podman pull "${REGISTRY_IMG}"; then
    break
  fi
  echo "podman pull ${REGISTRY_IMG} attempt ${attempt} failed; retrying in $((attempt * 5))s..."
  sleep $((attempt * 5))
  if [[ "${attempt}" -eq 5 ]]; then
    echo "ERROR: failed to pull ${REGISTRY_IMG} after retries" >&2
    exit 1
  fi
done

podman run -d \
  --restart=always \
  --name registry \
  --replace \
  -v ~/registry:/certs:Z \
  -e REGISTRY_HTTP_ADDR=0.0.0.0:443 \
  -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt \
  -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
  -p 443:443 \
  "${REGISTRY_IMG}"

