package controllers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type eventCertNode struct {
	Name      string
	ShortName string
}

// Use stable ordering when reconciling node Services.
// Services retain their established short-node names. Reject ambiguity rather
// than silently routing two node identities through the same Service.
func eventCertificateNodes(nodes *corev1.NodeList) ([]eventCertNode, error) {
	result := make([]eventCertNode, 0, len(nodes.Items))
	seen := make(map[string]string)
	for _, node := range nodes.Items {
		short := strings.Split(node.Name, ".")[0]
		if previous, exists := seen[short]; exists {
			return nil, fmt.Errorf("event Service name collision for nodes %q and %q", previous, node.Name)
		}
		seen[short] = node.Name
		result = append(result, eventCertNode{Name: node.Name, ShortName: short})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func isEventDaemonPod(obj client.Object) bool {
	owner := metav1.GetControllerOf(obj)
	return obj.GetNamespace() == names.Namespace && owner != nil &&
		owner.Kind == "DaemonSet" && owner.Name == "linuxptp-daemon"
}

// CEPv1 used to add this label from the sidecar. The operator now maintains it
// so per-node Service selectors work with the native CEPv2 binary too.
func (r *PtpOperatorConfigReconciler) syncEventPodLabels(ctx context.Context) error {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(names.Namespace)); err != nil {
		return err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !isEventDaemonPod(pod) || pod.Spec.NodeName == "" {
			continue
		}
		short := strings.Split(pod.Spec.NodeName, ".")[0]
		if pod.Labels["nodeName"] == short {
			continue
		}
		before := pod.DeepCopy()
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		pod.Labels["nodeName"] = short
		if err := r.Patch(ctx, pod, client.MergeFrom(before)); err != nil {
			return fmt.Errorf("label event pod %s: %w", pod.Name, err)
		}
	}
	return nil
}

func (r *PtpOperatorConfigReconciler) mapEventPodToRequest(_ context.Context, obj client.Object) []reconcile.Request {
	if !isEventDaemonPod(obj) {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: names.Namespace, Name: names.DefaultOperatorConfigName}}}
}

func (r *PtpOperatorConfigReconciler) deleteLegacyEventService(ctx context.Context) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-server", Namespace: names.Namespace}}
	if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("remove shared event Service: %w", err)
	}
	return nil
}
