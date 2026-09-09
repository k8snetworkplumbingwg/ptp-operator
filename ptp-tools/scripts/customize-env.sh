#!/bin/bash
IMG_PREFIX=$1
ENV_PATH=$2

# linuxptp-daemon from main serves Prometheus metrics on :9091 and talks to
# CEPv2 over /var/run/ptp/ipc.sock. The v1 sidecar also binds :9091, so Kind
# must deploy v2 or daemon metrics (offset, role, clock class) never appear.
# Reuse :cep (redhat-cne/cloud-event-proxy) rather than a second image build.
ENABLE_CEPV2="${ENABLE_CEPV2:-true}"
EVENT_PROXY_IMAGE=""
if [[ "${ENABLE_CEPV2}" == "true" ]]; then
  EVENT_PROXY_IMAGE="$IMG_PREFIX:cep"
fi

cat <<EOF > $ENV_PATH/env.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ptp-operator
  namespace: openshift-ptp
spec:
  template:
    spec:
      containers:
        - name: ptp-operator
          imagePullPolicy: Always
          env:
            - name: OPERATOR_NAME
              value: "ptp-operator"
            - name: RELEASE_VERSION
              value: "v5.0.0"
            - name: LINUXPTP_DAEMON_IMAGE
              value: "$IMG_PREFIX:lptpd"
            - name: KUBE_RBAC_PROXY_IMAGE
              value: "$IMG_PREFIX:krp"
            - name: SIDECAR_EVENT_IMAGE
              value: "$IMG_PREFIX:cep"
            - name: EVENT_PROXY_IMAGE
              value: "$EVENT_PROXY_IMAGE"
            - name: IMAGE_PULL_POLICY
              value: "Always"
EOF
