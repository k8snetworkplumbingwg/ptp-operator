# CEPv2 secure event deployment

Upstream defaults SECURED and ENABLE_EVENT_AUTH on. Explicitly set both to false
for an unsecured deployment. Release 5.1 must retain off defaults on backport;
the recommended product configuration is full mTLS and OAuth (TokenReview).

```sh
make deploy SECURED=true ENABLE_EVENT_AUTH=true \
  IMG=quay.io/your-account/ptp-operator:dev \
  SIDECAR_EVENT_IMAGE=quay.io/your-account/linuxptp-daemon:dev
```

The sidecar image must contain the native CEPv2 binary at
/usr/local/bin/cloud-event-proxy. The proxy is always deployed; security wiring
must not depend on the legacy EventConfig enablement gate.

The operator creates the serving-cert Service, auth config, TokenReview RBAC and
trust resources. An external issuer/consumer setup supplies clientAuth keypairs
and publishes only its public CA to ptp-event-publisher-client-ca. Run the
event-consumer secured setup to provision both traffic directions. Required
client credentials must exist before the secured daemon can start.

Each existing `ptp-event-publisher-service-<short-node>` Service requests its own
Service CA certificate in `ptp-event-publisher-server-tls-<short-node>`. The
operator labels daemon pods with their node identity so each Service selects
the matching publisher. Requests and returned subscription URLs use that Service:
subscriptions and CurrentState stay on the node that owns them.

The single DaemonSet carries an empty serving-certificate volume placeholder.
On Pod creation, the operator's existing admission server reads the target node
from the DaemonSet-generated node affinity and replaces that volume with a direct
reference to `ptp-event-publisher-server-tls-<short-node>`. The resulting Pod
mounts only its own serving certificate and key at `server-certs/tls.crt` and
`server-certs/tls.key`; other nodes' Secrets are not projected into that Pod.
The proxy uses the existing certificate-path options without node substitution.

The mutating webhook uses `failurePolicy: Fail` and selects only daemon pods
labelled `ptp.openshift.io/event-auth: "true"`. Missing or ambiguous node identity
rejects admission. A missing node Secret leaves the admitted Pod waiting for its
own certificate. If the webhook configuration is accidentally omitted, the empty
placeholder prevents TLS startup; it provides no shared-key fallback. Running
pods do not need the webhook, but creation of secured replacement pods requires
the operator admission server to be available.

This isolates the mounted **serving keys**, not all workload privileges. The
publisher's outbound client certificate remains shared, and existing privileged
containers and ServiceAccount permissions are unchanged. Certificates still load
at startup, so renewals require a restart. Adding a node no longer changes the
DaemonSet's certificate volume template or rolls existing pods.

When auth is disabled, certificate mounts and TokenReview access are removed and
the per-node serving-cert annotations are cleared. Services remain for plaintext
compatibility. Service CA-issued Secrets are retained until their owning Services
are removed; externally supplied client credentials are also retained.

| Resource | Writer |
| --- | --- |
| ptp-event-publisher-service-ca / service-ca.crt | Service CA operator |
| ptp-event-publisher-client-ca | External client issuer/setup |
| ptp-event-publisher-ca-bundle / ca-bundle.crt | PTP operator merges both inputs |

Do not overwrite the derived bundle or scale the operator to zero. Mounts use
the existing CEPv1 paths and JSON security options. TLS version and cipher suites
come from the cluster TLS profile. Provision serving certificates through Service
CA and client certificates separately, then restart after certificate rotation.

Validate with `CGO_ENABLED=0 go test ./controllers ./pkg/apply` and
`CGO_ENABLED=0 make bundle-check`. Cluster testing is still required before merge.

For a later cluster validation, verify node routing and public certificate identity
before running the subscribe/pull/push demo (these commands have not been run here):

```sh
NODE=worker-a.example.com # replace with the full target node name
SHORT_NODE=${NODE%%.*}
oc -n openshift-ptp get pods -l app=linuxptp-daemon -o wide -L nodeName
oc -n openshift-ptp get service "ptp-event-publisher-service-$SHORT_NODE" -o yaml
oc -n openshift-ptp get endpointslices \
  -l "kubernetes.io/service-name=ptp-event-publisher-service-$SHORT_NODE"
oc -n openshift-ptp get secret "ptp-event-publisher-server-tls-$SHORT_NODE" \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -subject -ext subjectAltName
```

Repeat for two nodes. Each Service must resolve to its matching daemon pod, and
each certificate SAN must contain that Service's DNS name. The consumer's
`NODE_NAME`, resource address, and publisher Service must refer to the same node.

Verify admitted Pod volumes as well as the DaemonSet template:

```sh
oc -n openshift-ptp get pods -l app=linuxptp-daemon -o json | jq '
  .items[] | {pod: .metadata.name, node: .spec.nodeName,
    servingVolume: [.spec.volumes[] | select(.name == "server-certs")]}'
```

Each serving volume must contain one `secret.secretName` for that Pod's node,
with no `projected` source. Existing pods acquire the new volume layout when the
DaemonSet update replaces them. Keep the mutating webhook installed while secured
pods are created; disabling auth removes the selection label and serving volume.
