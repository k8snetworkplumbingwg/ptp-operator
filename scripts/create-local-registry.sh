#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1

mkdir -p ~/registry

# Generate the private key/cert
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes  -keyout ~/registry/registry.key -out ~/registry/registry.crt -subj "/CN=registry"  -addext "subjectAltName=DNS:registry,DNS:localhost,IP:$VM_IP"

openssl x509 -in ~/registry/registry.crt -out ~/registry/registry.pem -outform PEM

if [ -d /usr/local/share/ca-certificates ]; then
    cp ~/registry/registry.pem /usr/local/share/ca-certificates/registry.crt
    update-ca-certificates
elif [ -d /etc/pki/ca-trust/source/anchors ]; then
    cp ~/registry/registry.pem /etc/pki/ca-trust/source/anchors/registry.pem
    update-ca-trust
else
    echo "WARNING: Could not find CA certificate directory"
fi

# Create the containerd certificate trust bundle that Kind nodes will mount at
# /etc/containerd/certs.d/$VM_IP/ (via kind-config.yaml extraMounts).
cp ~/registry/registry.crt ~/registry/ca.crt
cat > ~/registry/hosts.toml <<EOF
server = "https://$VM_IP"

[host."https://$VM_IP"]
  ca = "/etc/containerd/certs.d/$VM_IP/ca.crt"
EOF

podman run -d \
  --restart=always \
  --name registry \
  --replace \
  -v ~/registry:/certs:Z \
  -e REGISTRY_HTTP_ADDR=0.0.0.0:443 \
  -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt \
  -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
  -p 443:443 \
  registry:2

