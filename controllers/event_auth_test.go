package controllers

import (
	"encoding/json"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
)

// daemonSidecarArgs returns the args of the cloud-event-proxy container from a
// rendered ptp-daemon.yaml DaemonSet object.
func daemonSidecarArgs(t *testing.T, objs []*unstructured.Unstructured) []string {
	t.Helper()
	for _, obj := range objs {
		if obj.GetKind() != "DaemonSet" {
			continue
		}
		containers, found, err := unstructuredContainers(obj.Object)
		assert.NoError(t, err)
		assert.True(t, found)
		for _, c := range containers {
			container := c.(map[string]interface{})
			if container["name"] == "cloud-event-proxy" {
				var args []string
				if raw, ok := container["args"].([]interface{}); ok {
					for _, a := range raw {
						args = append(args, a.(string))
					}
				}
				return args
			}
		}
	}
	return nil
}

// TestEventAuthDisabled verifies the sidecar carries no auth wiring when
// ENABLE_EVENT_AUTH is off, so the operator stays usable without Service CA.
func TestEventAuthDisabled(t *testing.T) {
	data := makeTestRenderData()
	data.Data["EnableEventPublisher"] = true
	data.Data["SideCarV2"] = ""
	data.Data["EventTransportHost"] = "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043"
	data.Data["TLSMinVersion"] = ""
	data.Data["TLSCipherSuites"] = ""
	data.Data["EnableEventAuth"] = false

	objs, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	assert.NoError(t, err)
	args := daemonSidecarArgs(t, objs)
	for _, a := range args {
		assert.NotContains(t, a, "--auth-config", "auth-config must not be set when auth disabled")
	}
}

// TestEventAuthEnabled verifies the sidecar gets the auth-config flag and the
// auth manifest renders the expected resources with a valid, TLS-profiled JSON.
func TestEventAuthEnabled(t *testing.T) {
	profile := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	ianaCiphers := libgocrypto.OpenSSLToIANACipherSuites(profile.Ciphers)

	data := makeTestRenderData()
	data.Data["Namespace"] = "custom-ns"
	data.Data["EnableEventPublisher"] = true
	data.Data["SideCarV2"] = ""
	data.Data["EventTransportHost"] = "http://ptp-event-publisher-service-NODE_NAME.custom-ns.svc.cluster.local:9043"
	data.Data["TLSMinVersion"] = string(profile.MinTLSVersion)
	data.Data["TLSCipherSuites"] = strings.Join(ianaCiphers, ",")
	data.Data["TLSCipherSuitesJSON"] = ciphersToJSONArray(ianaCiphers)
	data.Data["EnableEventAuth"] = true

	// Sidecar must reference the auth config.
	objs, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	assert.NoError(t, err)
	args := daemonSidecarArgs(t, objs)
	assert.Contains(t, args, "--auth-config=/etc/cloud-event-proxy/auth/config.json")

	// Auth manifest must produce the CA bundle, serving-cert service, auth
	// ConfigMap and the auth-delegator ClusterRoleBinding.
	authObjs, err := render.RenderTemplate("../bindata/linuxptp/auth-config.yaml", data)
	assert.NoError(t, err)
	kinds := map[string]bool{}
	var cfgJSON string
	for _, o := range authObjs {
		kinds[o.GetKind()+"/"+o.GetName()] = true
		if o.GetKind() == "ConfigMap" && o.GetName() == "ptp-event-publisher-auth" {
			d, ok := o.Object["data"].(map[string]interface{})
			assert.True(t, ok)
			cfgJSON = d["config.json"].(string)
		}
	}
	assert.True(t, kinds["ConfigMap/ptp-event-publisher-ca-bundle"])
	assert.True(t, kinds["Service/ptp-event-publisher-server"])
	assert.True(t, kinds["ConfigMap/ptp-event-publisher-auth"])
	assert.True(t, kinds["ClusterRoleBinding/linuxptp-daemon-auth-delegator"])

	// config.json must be valid and carry the cluster TLS profile.
	var cfg map[string]interface{}
	assert.NoError(t, json.Unmarshal([]byte(cfgJSON), &cfg))
	assert.Equal(t, true, cfg["enableMTLS"])
	assert.Equal(t, true, cfg["enableOAuth"])
	assert.Equal(t, string(profile.MinTLSVersion), cfg["tlsMinVersion"])
	suites, ok := cfg["tlsCipherSuites"].([]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, suites)
}
