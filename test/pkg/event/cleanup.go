package event

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"
	cneevent "github.com/redhat-cne/sdk-go/pkg/event"
	"github.com/redhat-cne/sdk-go/pkg/event/ptp"
	"github.com/redhat-cne/sdk-go/pkg/pubsub"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const publisherRequestTimeout = 15 * time.Second

// PublisherAPI accesses the publisher from its own pod, avoiding cluster DNS
// and ingress dependencies in the test runner.
type PublisherAPI struct {
	exec func([]string) (string, error)
}

func NewPublisherAPI(pod *corev1.Pod) *PublisherAPI {
	return &PublisherAPI{exec: func(command []string) (string, error) {
		// Containers share the pod network. The daemon image supplies curl,
		// including when the proxy itself uses a minimal runtime image.
		stdout, stderr, err := pods.ExecCommand(client.Client, false, pod, pkg.PtpContainerName, command)
		if err != nil {
			return "", fmt.Errorf("%w (stderr: %s)", err, stderr.String())
		}
		return stdout.String(), nil
	}}
}

// Request returns the HTTP status separately so unsupported APIs and absent
// subscriptions can be distinguished from transport failures.
func (a *PublisherAPI) Request(ctx context.Context, method, route string) (int, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	url := "http://" + ApiBaseV2 + "/" + route
	command := []string{"curl", "--silent", "--show-error", "--connect-timeout", "5", "--max-time", "10",
		"--request", method, "--write-out", "\n%{http_code}", url}
	output, err := a.exec(command)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	return parseHTTPResponse(output)
}

func parseHTTPResponse(output string) (int, []byte, error) {
	separator := strings.LastIndexByte(output, '\n')
	if separator < 0 {
		return 0, nil, fmt.Errorf("missing HTTP status in curl output: %q", output)
	}
	status, err := strconv.Atoi(strings.TrimSpace(output[separator+1:]))
	if err != nil || status < 100 || status > 599 {
		return 0, nil, fmt.Errorf("invalid HTTP status in curl output: %q", output[separator+1:])
	}
	return status, []byte(output[:separator]), nil
}

func (a *PublisherAPI) Subscriptions(ctx context.Context) ([]pubsub.PubSub, error) {
	status, body, err := a.Request(ctx, http.MethodGet, "subscriptions")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list subscriptions returned HTTP %d: %s", status, body)
	}
	var subscriptions []pubsub.PubSub
	err = json.Unmarshal(body, &subscriptions)
	return subscriptions, err
}

// Delete tolerates an already removed subscription during deferred cleanup.
func (a *PublisherAPI) Delete(ctx context.Context, route string) error {
	status, body, err := a.Request(ctx, http.MethodDelete, route)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("delete %s returned HTTP %d: %s", route, status, body)
	}
	return nil
}

// ResetSubscriptions clears the serial fixture's REST subscriptions and node
// ConfigMap. Call it before setup and during teardown, after cleanup assertions.
func ResetSubscriptions(ctx context.Context, api *PublisherAPI, nodeName string) error {
	if err := StopConsumer(ctx); err != nil {
		return err
	}
	if err := api.Delete(ctx, "subscriptions"); err != nil {
		return err
	}
	_, err := client.Client.CoreV1().ConfigMaps(ProducerNamespace).Patch(ctx, nodeName,
		k8stypes.MergePatchType, []byte(`{"data":null,"binaryData":null}`), metav1.PatchOptions{})
	if kerrors.IsNotFound(err) {
		return nil
	}
	return err
}

// SubscriptionConfigMapData keeps each polling request within three seconds
// and the caller's remaining deadline. A missing ConfigMap is still an error.
func SubscriptionConfigMapData(ctx context.Context, nodeName string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	configMap, err := client.Client.CoreV1().ConfigMaps(ProducerNamespace).Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return configMap.Data, nil
}

// StopConsumer waits for pod termination and removal of all Service endpoint
// addresses, so notifications cannot reach a terminating consumer.
func StopConsumer(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, consumerReadyTimeout)
	defer cancel()
	err := client.Client.CoreV1().Pods(ConsumerNamespace).Delete(ctx, ConsumerPodName, metav1.DeleteOptions{})
	if err != nil && !kerrors.IsNotFound(err) {
		return err
	}
	return wait.PollUntilContextTimeout(ctx, time.Second, consumerReadyTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := client.Client.CoreV1().Pods(ConsumerNamespace).Get(ctx, ConsumerPodName, metav1.GetOptions{})
		if err == nil {
			return false, nil
		}
		if !kerrors.IsNotFound(err) {
			return false, err
		}
		slices, err := client.Client.DiscoveryV1().EndpointSlices(ConsumerNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "kubernetes.io/service-name=" + ConsumerServiceName,
		})
		if err != nil {
			return false, err
		}
		for _, slice := range slices.Items {
			for _, endpoint := range slice.Endpoints {
				if len(endpoint.Addresses) != 0 {
					return false, nil
				}
			}
		}
		return true, nil
	})
}

// FailureLogs observes a fixed window of publisher logs. Its boundary comes
// from the kubelet's CRI timestamp, never the runner or application's clock.
// Counts remain relative to a baseline, including entries in the boundary second.
type FailureLogs struct {
	pod          *corev1.Pod
	subscription pubsub.PubSub
	since        metav1.Time
	baseline     int
}

func NewFailureLogs(ctx context.Context, pod *corev1.Pod, subscription pubsub.PubSub) (*FailureLogs, error) {
	ctx, cancel := context.WithTimeout(ctx, publisherRequestTimeout)
	defer cancel()
	tail := int64(1)
	data, err := client.Client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: pkg.EventProxyContainerName, TailLines: &tail, Timestamps: true,
	}).DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("capture publisher log boundary: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return nil, fmt.Errorf("publisher returned no timestamped log boundary")
	}
	stamp, err := time.Parse(time.RFC3339Nano, fields[0])
	if err != nil {
		return nil, fmt.Errorf("invalid publisher log boundary %q: %w", fields[0], err)
	}
	// SinceTime is serialized with second precision. Round down explicitly;
	// the baseline accounts for all retained entries in that second.
	logs := &FailureLogs{pod: pod, subscription: subscription, since: metav1.NewTime(stamp.Truncate(time.Second))}
	logs.baseline, err = logs.Count(ctx)
	return logs, err
}

func (l *FailureLogs) Count(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, publisherRequestTimeout)
	defer cancel()
	data, err := client.Client.CoreV1().Pods(l.pod.Namespace).GetLogs(l.pod.Name, &corev1.PodLogOptions{
		Container: pkg.EventProxyContainerName, SinceTime: &l.since,
	}).DoRaw(ctx)
	if err != nil {
		return 0, fmt.Errorf("read publisher failure logs since %s: %w", l.since.Format(time.RFC3339), err)
	}
	count := failedDeliveries(string(data), l.subscription.ID, l.subscription.GetEndpointURI())
	if count < l.baseline {
		return 0, fmt.Errorf("delivery log count fell below baseline: got %d, baseline %d; publisher logs may have restarted or rotated", count, l.baseline)
	}
	return count - l.baseline, nil
}

func failedDeliveries(logs, subscriptionID, endpoint string) int {
	count := 0
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "error posting event at "+endpoint+" :") ||
			strings.Contains(line, "failed to write event to subscriber "+subscriptionID+":") {
			count++
		}
	}
	return count
}

func (a *PublisherAPI) PTPLockState(ctx context.Context, resource, iface string) (string, error) {
	status, body, err := a.Request(ctx, http.MethodGet, strings.TrimPrefix(resource, "/")+"/CurrentState")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("CurrentState returned HTTP %d: %s", status, body)
	}
	return ptpNotificationState(body, resource, iface)
}

// Shared-PHC interfaces can use the daemon's masked port alias in standalone
// proxy payloads (ens4f2 -> ens4fx); IPC payloads use the interface itself.
var ptpInterfaceAlias = regexp.MustCompile(`^(.+?)(\d+)(?:np\d+)?(\..+)?$`)

func ptpNotificationState(body []byte, resource, iface string) (string, error) {
	var notification struct {
		Source string        `json:"source"`
		Type   string        `json:"type"`
		Data   cneevent.Data `json:"data"`
	}
	if err := json.Unmarshal(body, &notification); err != nil {
		return "", err
	}
	nodeResource := strings.TrimSuffix(resource, string(ptp.PtpLockState))
	if (notification.Source != resource && notification.Source != string(ptp.PtpLockState)) ||
		notification.Type != string(ptp.PtpStateChange) {
		return "", fmt.Errorf("unexpected PTP notification source %q or type %q", notification.Source, notification.Type)
	}
	alias := iface
	if matches := ptpInterfaceAlias.FindStringSubmatch(iface); len(matches) == 4 {
		alias = matches[1] + "x" + matches[3]
	}
	for _, value := range notification.Data.Values {
		if value.DataType != cneevent.NOTIFICATION ||
			(value.Resource != nodeResource+"/"+iface+"/master" && value.Resource != nodeResource+"/"+alias+"/master") {
			continue
		}
		if state, ok := value.Value.(string); ok {
			return state, nil
		}
	}
	return "", fmt.Errorf("notification has no PTP lock state for %q on %q", iface, nodeResource)
}

// ConsumerPTPStateCount counts delivered notifications
func ConsumerPTPStateCount(ctx context.Context, resource, iface, state string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, publisherRequestTimeout)
	defer cancel()
	data, err := client.Client.CoreV1().Pods(ConsumerNamespace).GetLogs(ConsumerPodName, &corev1.PodLogOptions{
		Container: ConsumerContainerName,
	}).DoRaw(ctx)
	if err != nil {
		return 0, err
	}
	return consumerPTPStateCount(string(data), resource, iface, state), nil
}

func consumerPTPStateCount(logs, resource, iface, state string) int {
	count := 0
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "received event ") {
			continue
		}
		line = strings.ReplaceAll(line, `\"`, `"`)
		start, end := strings.IndexByte(line, '{'), strings.LastIndexByte(line, '}')
		if start >= 0 && end > start {
			got, err := ptpNotificationState([]byte(line[start:end+1]), resource, iface)
			if err == nil && got == state {
				count++
			}
		}
	}
	return count
}
