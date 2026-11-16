// Package eventauth selects node-specific serving credentials for daemon pods.
package eventauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	PodLabel            = "ptp.openshift.io/event-auth"
	WebhookPath         = "/mutate-ptp-event-serving-secret"
	ServingSecretPrefix = "ptp-event-publisher-server-tls-"
)

// PodWebhook shares the operator's existing TLS admission server. It does not
// read Secret data or grant the daemon ServiceAccount permission to read Secrets.
type PodWebhook struct {
	Namespace string
}

// +kubebuilder:webhook:path=/mutate-ptp-event-serving-secret,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create,versions=v1,name=event-serving-secret.ptp.openshift.io,admissionReviewVersions=v1

func (h *PodWebhook) Handle(_ context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create || req.Resource.Group != "" || req.Resource.Resource != "pods" ||
		req.SubResource != "" || req.Namespace != h.Namespace {
		return admission.Allowed("outside event pod scope")
	}
	pod := &corev1.Pod{}
	if err := json.Unmarshal(req.Object.Raw, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if pod.Labels[PodLabel] != "true" {
		return admission.Allowed("event authentication disabled")
	}
	if err := selectServingSecret(pod); err != nil {
		return admission.Denied(err.Error())
	}
	updated, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, updated)
}

func selectServingSecret(pod *corev1.Pod) error {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "DaemonSet" || owner.Name != "linuxptp-daemon" {
		return fmt.Errorf("secured event pod must belong to the linuxptp-daemon DaemonSet")
	}
	node, err := targetNode(pod)
	if err != nil {
		return err
	}
	short := strings.Split(node, ".")[0]
	secretName := ServingSecretPrefix + short
	if problems := validation.IsDNS1123Subdomain(secretName); len(problems) != 0 {
		return fmt.Errorf("invalid node serving Secret name")
	}
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name != "server-certs" {
			continue
		}
		// Replace the whole source, not merely a subpath into an all-node projection.
		pod.Spec.Volumes[i].VolumeSource = corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
				Items:      []corev1.KeyToPath{{Key: "tls.crt", Path: "tls.crt"}, {Key: "tls.key", Path: "tls.key"}},
			},
		}
		pod.Labels["nodeName"] = short
		return nil
	}
	return fmt.Errorf("secured event pod is missing its server-certs volume")
}

// DaemonSet-created Pods carry a required metadata.name node affinity before
// scheduling. Require one unambiguous target; never trust an arbitrary pod label.
func targetNode(pod *corev1.Pod) (string, error) {
	node := pod.Spec.NodeName
	affinity := pod.Spec.Affinity
	if affinity != nil && affinity.NodeAffinity != nil && affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		if len(terms) != 1 {
			return "", fmt.Errorf("event pod must target exactly one node")
		}
		fields := terms[0].MatchFields
		if len(fields) != 1 || fields[0].Key != "metadata.name" || fields[0].Operator != corev1.NodeSelectorOpIn || len(fields[0].Values) != 1 {
			return "", fmt.Errorf("event pod needs an exact metadata.name node affinity")
		}
		target := fields[0].Values[0]
		if node != "" && node != target {
			return "", fmt.Errorf("event pod nodeName conflicts with node affinity")
		}
		node = target
	}
	if node == "" || len(validation.IsDNS1123Subdomain(node)) != 0 {
		return "", fmt.Errorf("event pod is missing a valid target node")
	}
	return node, nil
}
