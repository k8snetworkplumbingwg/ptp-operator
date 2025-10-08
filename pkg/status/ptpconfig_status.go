package status

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	clientset "github.com/k8snetworkplumbingwg/ptp-operator/pkg/client/clientset/versioned"
)

// UpsertPtpCondition updates or appends a condition on the PtpConfig status and issues UpdateStatus with retry
func UpsertPtpCondition(ctx context.Context, cs clientset.Interface, namespace, name string, cond ptpv1.PtpCondition) error {
	backoff := wait.Backoff{Duration: 100 * time.Millisecond, Factor: 1.5, Jitter: 0.1, Steps: 6}
	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		pc, err := cs.PtpV1().PtpConfigs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

        // upsert by composite key: Type + Profile + Filename + Process
		updated := false
		for i := range pc.Status.PtpStatus.Conditions {
			c := &pc.Status.PtpStatus.Conditions[i]
            if c.Type == cond.Type && c.Profile == cond.Profile && c.Filename == cond.Filename && c.Process == cond.Process {
				*c = cond
				updated = true
				break
			}
		}
		if !updated {
			pc.Status.PtpStatus.Conditions = append(pc.Status.PtpStatus.Conditions, cond)
		}

		// enforce max history
		const maxConditions = 100
		if n := len(pc.Status.PtpStatus.Conditions); n > maxConditions {
			pc.Status.PtpStatus.Conditions = pc.Status.PtpStatus.Conditions[n-maxConditions:]
		}

		_, err = cs.PtpV1().PtpConfigs(namespace).UpdateStatus(ctx, pc, metav1.UpdateOptions{})
		if err != nil {
			// retry on conflict or transient errors
			return false, nil
		}
		return true, nil
	})
}
