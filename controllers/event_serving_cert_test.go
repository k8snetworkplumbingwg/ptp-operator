package controllers

import (
	"context"
	"testing"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestPerNodeServingCertificates(t *testing.T) {
	nodes := &corev1.NodeList{Items: []corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "worker-b.example.com"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "worker-a.example.com"}},
	}}
	certs, err := eventCertificateNodes(nodes, nil, true)
	require.NoError(t, err)
	require.Equal(t, "worker-a.example.com", certs[0].Name)
	data := makeTestRenderData()
	data.Data["EnableEventAuth"] = true
	data.Data["TLSMinVersion"] = ""
	data.Data["TLSCipherSuites"] = ""
	objects, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	require.NoError(t, err)
	volume := daemonVolume(t, objects, "server-certs")
	require.NotNil(t, volume["emptyDir"])
	require.Nil(t, volume["secret"])
	require.Nil(t, volume["projected"])
	for _, node := range certs {
		data.Data["NodeName"] = node.ShortName
		services, err := render.RenderTemplate("../bindata/linuxptp/event-service.yaml", data)
		require.NoError(t, err)
		require.Len(t, services, 1)
		svc := services[0]
		require.Equal(t, "ptp-event-publisher-service-"+node.ShortName, svc.GetName())
		secretName := svc.GetAnnotations()["service.beta.openshift.io/serving-cert-secret-name"]
		require.Equal(t, "ptp-event-publisher-server-tls-"+node.ShortName, secretName)
		selector, _, err := unstructured.NestedString(svc.Object, "spec", "selector", "nodeName")
		require.NoError(t, err)
		require.Equal(t, node.ShortName, selector)

	}
	data.Data["EnableEventAuth"] = false
	services, err := render.RenderTemplate("../bindata/linuxptp/event-service.yaml", data)
	require.NoError(t, err)
	require.Empty(t, services[0].GetAnnotations()["service.beta.openshift.io/serving-cert-secret-name"])
	objects, err = render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	require.NoError(t, err)
	require.Nil(t, daemonVolume(t, objects, "server-certs"))
}

func TestEventCertificateNodeCollision(t *testing.T) {
	_, err := eventCertificateNodes(&corev1.NodeList{Items: []corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "worker.first.example"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "worker.second.example"}},
	}}, nil, true)
	require.ErrorContains(t, err, "collision")
}

func TestEventCertificateNodesScopeCollisionToSelectedSecuredNodes(t *testing.T) {
	nodes := &corev1.NodeList{Items: []corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "worker.first.example", Labels: map[string]string{"ptp": "true"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "worker.second.example"}},
	}}
	selected, err := eventCertificateNodes(nodes, map[string]string{"ptp": "true"}, true)
	require.NoError(t, err)
	require.Equal(t, []eventCertNode{{Name: "worker.first.example", ShortName: "worker"}}, selected)

	allUnsecured, err := eventCertificateNodes(nodes, nil, false)
	require.NoError(t, err)
	require.Len(t, allUnsecured, 2)
}

func TestEventPodNodeLabels(t *testing.T) {
	controller := true
	owner := metav1.OwnerReference{Kind: "DaemonSet", Name: "linuxptp-daemon", Controller: &controller}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "daemon-a", Namespace: authTestNamespace, OwnerReferences: []metav1.OwnerReference{owner}},
		Spec: corev1.PodSpec{NodeName: "worker-a.example.com"}}
	other := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: authTestNamespace},
		Spec: corev1.PodSpec{NodeName: "worker-b.example.com"}}
	r := newAuthTestReconciler(t, pod, other)
	require.Len(t, r.mapEventPodToRequest(context.Background(), pod), 1)
	require.Empty(t, r.mapEventPodToRequest(context.Background(), other))
	require.NoError(t, r.syncEventPodLabels(context.Background()))
	got := &corev1.Pod{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, got))
	require.Equal(t, "worker-a", got.Labels["nodeName"])
	rv := got.ResourceVersion
	require.NoError(t, r.syncEventPodLabels(context.Background()))
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, got))
	require.Equal(t, rv, got.ResourceVersion)
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: other.Name, Namespace: other.Namespace}, got))
	require.Empty(t, got.Labels["nodeName"])
}
