package eventauth

import (
	"context"
	"encoding/json"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func daemonPod(node string) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "daemon-test", Namespace: "openshift-ptp",
			Labels:          map[string]string{PodLabel: "true", "app": "linuxptp-daemon"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "linuxptp-daemon", Controller: &controller}},
		},
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{node}}}}},
			}}},
			Volumes: []corev1.Volume{
				{Name: "server-certs", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}}},
				{Name: "client-certs", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "ptp-event-publisher-client-tls"}}},
			},
		},
	}
}

func invoke(t *testing.T, pod *corev1.Pod, extraNodes ...client.Object) (admission.Response, []byte) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	objects := append([]client.Object{}, extraNodes...)
	if node, err := targetNode(pod); err == nil {
		found := false
		for _, object := range objects {
			found = found || object.GetName() == node
		}
		if !found {
			objects = append(objects, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node, Labels: pod.Spec.NodeSelector}})
		}
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	raw, err := json.Marshal(pod)
	require.NoError(t, err)
	response := (&PodWebhook{Namespace: "openshift-ptp", Client: reader}).Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Namespace: pod.Namespace, Operation: admissionv1.Create,
		Resource: metav1.GroupVersionResource{Version: "v1", Resource: "pods"},
		Object:   runtime.RawExtension{Raw: raw},
	}})
	return response, raw
}

func TestAdmissionRejectsSelectedNodeShortNameCollision(t *testing.T) {
	pod := daemonPod("worker.second.example")
	pod.Spec.NodeSelector = map[string]string{"ptp": "true"}
	colliding := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker.first.example", Labels: map[string]string{"ptp": "true"}}}
	response, _ := invoke(t, pod, colliding)
	require.False(t, response.Allowed)
	require.Contains(t, response.Result.Message, "collision")
}

func TestAdmissionMountsOnlyOwnServingSecret(t *testing.T) {
	for _, node := range []string{"worker-a", "worker-b"} {
		t.Run(node, func(t *testing.T) {
			pod := daemonPod(node + ".example.com")
			pod.Labels["nodeName"] = "wrong-node"
			response, raw := invoke(t, pod)
			require.True(t, response.Allowed)
			patchBytes, err := json.Marshal(response.Patches)
			require.NoError(t, err)
			patch, err := jsonpatch.DecodePatch(patchBytes)
			require.NoError(t, err)
			updated, err := patch.Apply(raw)
			require.NoError(t, err)
			got := &corev1.Pod{}
			require.NoError(t, json.Unmarshal(updated, got))
			require.Len(t, got.Spec.Volumes, 2)
			require.Nil(t, got.Spec.Volumes[0].Projected)
			require.Equal(t, ServingSecretPrefix+node, got.Spec.Volumes[0].Secret.SecretName)
			require.Nil(t, got.Spec.Volumes[0].Secret.Optional)
			require.Equal(t, []corev1.KeyToPath{{Key: "tls.crt", Path: "tls.crt"}, {Key: "tls.key", Path: "tls.key"}}, got.Spec.Volumes[0].Secret.Items)
			require.Equal(t, pod.Spec.Volumes[1], got.Spec.Volumes[1])
			require.Equal(t, node, got.Labels["nodeName"])
			second, _ := invoke(t, got)
			require.True(t, second.Allowed)
			require.Empty(t, second.Patches, "reinvocation must be idempotent")
		})
	}
}

func TestAdmissionRejectsAmbiguousOrInvalidTarget(t *testing.T) {
	for name, modify := range map[string]func(*corev1.Pod){
		"no node": func(p *corev1.Pod) { p.Spec.Affinity = nil },
		"two nodes": func(p *corev1.Pod) {
			p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchFields[0].Values = []string{"a", "b"}
		},
		"two terms": func(p *corev1.Pod) {
			selector := p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
			selector.NodeSelectorTerms = append(selector.NodeSelectorTerms, selector.NodeSelectorTerms[0])
		},
		"conflicting node": func(p *corev1.Pod) { p.Spec.NodeName = "worker-b" },
		"no owner":         func(p *corev1.Pod) { p.OwnerReferences = nil },
		"no volume":        func(p *corev1.Pod) { p.Spec.Volumes = nil },
	} {
		t.Run(name, func(t *testing.T) {
			pod := daemonPod("worker-a")
			modify(pod)
			response, _ := invoke(t, pod)
			require.False(t, response.Allowed)
		})
	}
}

func TestAdmissionSkipsUnsecuredAndUnrelatedPods(t *testing.T) {
	for _, change := range []func(*corev1.Pod){
		func(p *corev1.Pod) { delete(p.Labels, PodLabel) },
		func(p *corev1.Pod) { p.Namespace = "another-namespace" },
	} {
		pod := daemonPod("worker-a")
		change(pod)
		response, _ := invoke(t, pod)
		require.True(t, response.Allowed)
		require.Empty(t, response.Patches)
	}
}

func TestAdmissionRemovesAllNodeProjection(t *testing.T) {
	pod := daemonPod("worker-a")
	pod.Spec.Volumes[0].VolumeSource = corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
		Sources: []corev1.VolumeProjection{{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: ServingSecretPrefix + "worker-b"}}}},
	}}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a"}}).Build()
	require.NoError(t, selectServingSecret(context.Background(), reader, pod))
	require.Nil(t, pod.Spec.Volumes[0].Projected)
	require.Equal(t, ServingSecretPrefix+"worker-a", pod.Spec.Volumes[0].Secret.SecretName)
}
