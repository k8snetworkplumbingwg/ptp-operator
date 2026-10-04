# Network Policies: Current Scope and Limitations

This document describes the `openshift-ptp` NetworkPolicy implementation in
this branch; it does not attest to installation on any particular cluster.
The operand work is tracked by [HPSTRAT-104](https://redhat.atlassian.net/browse/HPSTRAT-104)
and [CNF-19770](https://redhat.atlassian.net/browse/CNF-19770). Securing the
operator pod is separate, deferred work under
[HPSTRAT-278](https://redhat.atlassian.net/browse/HPSTRAT-278).

## What This Branch Implements

When deployed, during `PtpOperatorConfig` reconciliation the operator manages the following
`NetworkPolicy` objects in `openshift-ptp`, selecting pods labeled
`app: linuxptp-daemon`:

| Policy | Declared traffic |
| --- | --- |
| `06-linuxptp-daemon-default-deny` | Ingress and egress default deny for selected daemon pods. |
| `07-linuxptp-daemon-egress-dns` | DNS to `openshift-dns`, TCP/UDP 5353. |
| `08-linuxptp-daemon-egress-api-server` | Kubernetes API Service ClusterIP and port discovered on the target cluster. |
| `09-linuxptp-daemon-ptp-traffic` | IP PTP, UDP 319/320 in both directions. |
| `11-linuxptp-daemon-ingress-metrics` | Metrics from `openshift-monitoring` on TCP 8443. |
| `13-linuxptp-daemon-egress-ntp-optional` | TCP/UDP 123 in both directions, only when a `PtpConfig` profile configures `chronyd`. |

`10-linuxptp-daemon-ingress-health` documents health probes on TCP 8081/8082;
it is **not** a NetworkPolicy object. There is no policy for the pod-local
cloud-event-proxy API on port 9043. Policy `12-operator-to-daemon-management`
is absent until an actual direct network endpoint is verified; managing the
DaemonSet via the Kubernetes API is not such an endpoint. The controller
updates its owned operand policies and removes stale conditional declarations
without adopting or deleting unrelated policies.

## Enforcement Boundary

The `linuxptp-daemon` DaemonSet currently sets `hostNetwork: true`. LinuxPTP,
cloud-event-proxy and kube-rbac-proxy are containers in that same pod. On
OpenShift OVN-Kubernetes, a NetworkPolicy selecting this host-networked pod
does **not** govern the pod's own ingress or egress (see [OpenShift NetworkPolicy
documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/network_security/network-policy)). Its PTP synchronization,
API access, DNS resolution and metrics availability therefore demonstrate
workload health, **not** NetworkPolicy enforcement or denial. Raw Ethernet PTP
and SyncE are also outside Kubernetes NetworkPolicy's scope. No attempt is
made in this phase to move PTP interfaces or sidecars into another pod.

The `06` policy selects only `app: linuxptp-daemon`; it is **not** a
namespace-wide default deny. It does not secure the normal-networked
`ptp-operator` pod or unrelated pods in `openshift-ptp`. If a matching workload
uses the pod network in the future, these declarations would affect it and
must be revalidated before changing its network layout. Do not generalize the
host-network exception to other operands, non-host-networked pods, or RBAC.

## Separate Operator Work and Existing Objects

The bundle manifests in this branch contain audited `ptp-operator` CSV RBAC, but **no operator
NetworkPolicy objects**. RBAC grants permission to manage policies; it does
not install an operator policy or restrict any network path. Operator-pod
allows and an operator-label-scoped deny belong to HPSTRAT-278, after a
supported delivery mechanism and policy-enforcing validation are available.
The historical namespace-wide `01-default-deny-all` (`podSelector: {}`) must
not be bundled or newly applied: it could isolate other workloads. Older
installations can still have controller-owned `01-05` objects; this version
stops creating or updating them but leaves them for a separately validated
migration. Inventory them before claiming an installed policy set or changing
their ownership.

## Evidence and Release Gate

Policy objects and this traffic matrix document intent and may support a
CIS 5.3.2 review. Their presence alone does **not** prove a passing CIS scan,
enforcement of host-network traffic, or ProdSec approval. The requested
HPSTRAT-104 exception is limited to NetworkPolicy enforcement of the current
`linuxptp-daemon` pod's own host-network ingress and egress; it must be
formally reviewed rather than assumed from this document.

Before declaring the operand milestone complete, retain the actual policy
objects and selectors, cluster-specific DNS/API/monitoring checks, PTP health
and duration results, the CIS scan result, ProdSec signoff and formal narrow
waiver decision. None of those external outcomes is established by a local
unit test or a successful bundle build. Check the actual namespace state with:

```sh
oc -n openshift-ptp get pods -l app=linuxptp-daemon -o custom-columns=NAME:.metadata.name,HOST_NETWORK:.spec.hostNetwork
oc -n openshift-ptp get networkpolicy
```

If a policy obstructs a necessary path, investigate the specific rule and
roll back only that feature-owned policy after confirming its owner and the
impact on other workloads. Do not delete all namespace policies or leave a
temporary allow-all rule in place.
