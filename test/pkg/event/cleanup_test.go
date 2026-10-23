package event

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/redhat-cne/sdk-go/pkg/event/ptp"
	"github.com/redhat-cne/sdk-go/pkg/pubsub"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestCleanupLogCountsWithClockSkew(t *testing.T) {
	for _, skew := range []time.Duration{-time.Hour, time.Hour} {
		t.Run(skew.String(), func(t *testing.T) {
			resource := "/cluster/node/worker" + string(ptp.PtpLockState)
			subscription := pubsub.PubSub{ID: "test-sub"}
			// IPC errors identify the subscription; standalone endpoint matching
			// is covered by TestFailedDeliveriesSelectsOnlyTheFixture.
			stamp := time.Now().Add(skew).UTC().Format(time.RFC3339)
			applicationStamp := time.Now().Add(-skew).UTC().Format(time.RFC3339)
			failureLine := applicationStamp + " failed to write event to subscriber test-sub: timeout\n"
			notification := fmt.Sprintf(`%s received event {"source":%q,"type":%q,"data":{"values":[{"ResourceAddress":"/cluster/node/worker/ens4f2/master","data_type":"notification","value_type":"enumeration","value":"LOCKED"}]}}`, stamp, resource, ptp.PtpStateChange) + "\n"
			var failures, notifications atomic.Int32
			failures.Store(2)      // Retained failures from an earlier fixture.
			notifications.Store(1) // A prior LOCKED must not satisfy recovery.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("tailLines") == "1" {
					// CRI/node time can differ from both runner and application.
					fmt.Fprintln(w, stamp+" boundary")
					return
				}
				if r.URL.Query().Get("container") == pkg.EventProxyContainerName &&
					r.URL.Query().Get("sinceTime") != stamp {
					t.Errorf("window must use the node timestamp: %s", r.URL)
				}
				switch r.URL.Query().Get("container") {
				case pkg.EventProxyContainerName:
					fmt.Fprint(w, strings.Repeat(failureLine, int(failures.Load())))
				case ConsumerContainerName:
					fmt.Fprint(w, strings.Repeat(notification, int(notifications.Load())))
				default:
					t.Errorf("unexpected log request: %s", r.URL)
				}
			}))
			defer server.Close()
			kube := kubernetes.NewForConfigOrDie(&rest.Config{Host: server.URL})
			original := client.Client
			client.Client = &client.ClientSet{Interface: kube}
			t.Cleanup(func() { client.Client = original })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "publisher", Namespace: ProducerNamespace}}
			logs, err := NewFailureLogs(ctx, pod, subscription)
			if err != nil || logs.baseline != 2 {
				t.Fatalf("failure logs = %+v, error = %v", logs, err)
			}
			for want := 1; want <= 2; want++ {
				got, err := ConsumerPTPStateCount(ctx, resource, "ens4f2", "LOCKED")
				if err != nil || got != want {
					t.Fatalf("notification count = %d, want %d; error = %v", got, want, err)
				}
				notifications.Add(1)
			}
			// Interface transitions can complete several failures between polls.
			for _, want := range []int{1, 4, 9, 12, 15} {
				failures.Store(int32(logs.baseline + want))
				if got, err := logs.Count(ctx); err != nil || got != want {
					t.Fatalf("failures = %d, want %d; error = %v", got, want, err)
				}
			}
			failures.Store(1)
			if _, err := logs.Count(ctx); err == nil {
				t.Fatal("loss of the baseline logs must fail observation")
			}
		})
	}
}

// Old log history must never be transferred, even on long-lived noisy pods.
func TestFailureLogsRequiresNodeBoundary(t *testing.T) {
	for _, boundary := range []string{"", "not-a-timestamp log", "2026-10-16T05:00:00.987654321Z last line\n"} {
		t.Run(boundary, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					if r.URL.Query().Get("tailLines") != "1" || r.URL.Query().Get("timestamps") != "true" {
						t.Errorf("boundary must request one timestamped line: %s", r.URL)
					}
					fmt.Fprint(w, boundary)
					return
				}
				if r.URL.Query().Get("sinceTime") != "2026-10-16T05:00:00Z" || r.URL.Query().Has("tailLines") {
					t.Errorf("count must use the fixed window without truncating failures: %s", r.URL)
					http.Error(w, "unbounded history request", http.StatusBadRequest)
					return
				}
				fmt.Fprintln(w, "failed to write event to subscriber test-sub: timeout")
			}))
			defer server.Close()
			original := client.Client
			client.Client = &client.ClientSet{Interface: kubernetes.NewForConfigOrDie(&rest.Config{Host: server.URL})}
			t.Cleanup(func() { client.Client = original })
			logs, err := NewFailureLogs(context.Background(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "publisher", Namespace: ProducerNamespace}}, pubsub.PubSub{ID: "test-sub"})
			if strings.HasPrefix(boundary, "2026-") {
				if err != nil || logs.baseline != 1 {
					t.Fatalf("logs=%+v, err=%v", logs, err)
				}
				if count, err := logs.Count(context.Background()); err != nil || count != 0 {
					t.Fatalf("count=%d, err=%v", count, err)
				}
			} else if err == nil || requests != 1 {
				t.Fatalf("invalid boundary must fail before fetching history: requests=%d err=%v", requests, err)
			}
		})
	}
}

func TestParseHTTPResponse(t *testing.T) {
	for _, tc := range []struct {
		name, output, body string
		status             int
		wantError          bool
	}{
		{name: "multiline body", output: "{\n  \"status\": \"ok\"\n}\n200", body: "{\n  \"status\": \"ok\"\n}", status: 200},
		{name: "empty response", output: "\n204", status: 204},
		{name: "HTTP error remains a status", output: "unavailable\n503", body: "unavailable", status: 503},
		{name: "missing status", output: "200", wantError: true},
		{name: "transport failure status", output: "\n000", wantError: true},
		{name: "invalid status", output: "\nHTTP 200", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body, err := parseHTTPResponse(tc.output)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantError && (status != tc.status || string(body) != tc.body) {
				t.Fatalf("got HTTP %d body %q, want HTTP %d body %q", status, body, tc.status, tc.body)
			}
		})
	}
}

func TestFailedDeliveriesSelectsOnlyTheFixture(t *testing.T) {
	endpoint := "http://test-consumer:9043/event"
	logs := `error posting event at http://test-consumer:9043/event : timeout
error posting event at http://test-consumer:9043/event-extra : timeout
error posting event at http://other-consumer:9043/event : timeout
failed to write event to subscriber test-sub: connection refused
failed to write event to subscriber test-sub-extra: timeout
client not responding, waiting 9 times before marking to delete subscriber
`
	if got := failedDeliveries(logs, "test-sub", endpoint); got != 2 {
		t.Fatalf("got %d failed deliveries, want 2", got)
	}
}

func TestPublisherAPIDeleteDoesNotHideServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusInternalServerError} {
		api := &PublisherAPI{exec: func([]string) (string, error) { return fmt.Sprintf("\n%d", status), nil }}
		err := api.Delete(context.Background(), "subscriptions/id")
		if (err != nil) != (status == http.StatusInternalServerError) {
			t.Fatalf("DELETE with status %d returned %v", status, err)
		}
	}
}

func TestStopConsumerWaitsForEndpointRemoval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		service    string
		addresses  []string
		wantCancel bool
	}{
		{name: "no endpoint addresses", service: ConsumerServiceName},
		{name: "terminating endpoint still has an address", service: ConsumerServiceName, addresses: []string{"10.0.0.1"}, wantCancel: true},
		{name: "unrelated Service ignored", service: "another-service", addresses: []string{"10.0.0.2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready := false
			objects := []runtime.Object{
				&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: ConsumerPodName, Namespace: ConsumerNamespace}},
				&discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "consumer-endpoints", Namespace: ConsumerNamespace,
						Labels: map[string]string{discoveryv1.LabelServiceName: tc.service}},
					AddressType: discoveryv1.AddressTypeIPv4,
					Endpoints:   []discoveryv1.Endpoint{{Addresses: tc.addresses, Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
				},
			}
			original := client.Client
			client.Client = &client.ClientSet{Interface: fake.NewClientset(objects...)}
			t.Cleanup(func() { client.Client = original })
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := StopConsumer(ctx)
			if tc.wantCancel {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("terminating endpoints must prevent injections: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A failed cleanup assertion must still leave a clean fixture for the next
// serial case, including when the proxy has no subscription ConfigMap.
func TestResetSubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configMap bool
		status    int
	}{
		{"stale persistence", true, http.StatusNoContent},
		{"no ConfigMap", false, http.StatusNoContent},
		{"REST error", true, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var objects []runtime.Object
			if tc.configMap {
				objects = append(objects, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: ProducerNamespace},
					Data:       map[string]string{"stale-client": "subscription"},
					BinaryData: map[string][]byte{"stale": []byte("subscription")},
				})
			}
			kube := fake.NewClientset(objects...)
			original := client.Client
			client.Client = &client.ClientSet{Interface: kube}
			t.Cleanup(func() { client.Client = original })
			api := &PublisherAPI{exec: func(command []string) (string, error) {
				if command[len(command)-1] != "http://"+ApiBaseV2+"/subscriptions" {
					t.Fatalf("expected collection deletion, got %v", command)
				}
				return fmt.Sprintf("\n%d", tc.status), nil
			}}
			err := ResetSubscriptions(context.Background(), api, "worker")
			if (err != nil) != (tc.status == http.StatusInternalServerError) {
				t.Fatalf("unexpected reset error: %v", err)
			}
			maps, err := kube.CoreV1().ConfigMaps(ProducerNamespace).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.configMap {
				if len(maps.Items) != 0 {
					t.Fatal("reset must not create a ConfigMap")
				}
			} else if len(maps.Items) != 1 {
				t.Fatal("reset must retain the node ConfigMap")
			} else if tc.status == http.StatusNoContent && (len(maps.Items[0].Data) != 0 || len(maps.Items[0].BinaryData) != 0) {
				t.Fatal("reset must empty the node ConfigMap")
			} else if tc.status == http.StatusInternalServerError && len(maps.Items[0].Data) != 1 {
				t.Fatal("REST failure must not be hidden by clearing persistence")
			}
		})
	}
}

func TestPTPNotificationState(t *testing.T) {
	resource := "/cluster/node/worker" + string(ptp.PtpLockState)
	for _, tc := range []struct {
		name, source, iface, address, eventType, dataType, state string
		wantError                                                bool
	}{
		{"IPC interface", resource, "ens4f2", "/cluster/node/worker/ens4f2/master", string(ptp.PtpStateChange), "notification", "LOCKED", false},
		{"standalone alias", string(ptp.PtpLockState), "ens4f2", "/cluster/node/worker/ens4fx/master", string(ptp.PtpStateChange), "notification", "FREERUN", false},
		{"holdover", resource, "ens4f2", "/cluster/node/worker/ens4f2/master", string(ptp.PtpStateChange), "notification", "HOLDOVER", false},
		{"VLAN alias", resource, "ens4f2.100", "/cluster/node/worker/ens4fx.100/master", string(ptp.PtpStateChange), "notification", "LOCKED", false},
		{"np alias", resource, "ens4f2np0", "/cluster/node/worker/ens4fx/master", string(ptp.PtpStateChange), "notification", "LOCKED", false},
		{"unmasked port", string(ptp.PtpLockState), "eth0", "/cluster/node/worker/eth0/master", string(ptp.PtpStateChange), "notification", "LOCKED", false},
		{"other node in source", "/cluster/node/other" + string(ptp.PtpLockState), "ens4f2", "/cluster/node/worker/ens4f2/master", string(ptp.PtpStateChange), "notification", "LOCKED", true},
		{"other node in payload", string(ptp.PtpLockState), "ens4f2", "/cluster/node/other/ens4fx/master", string(ptp.PtpStateChange), "notification", "LOCKED", true},
		{"other interface", resource, "ens4f2", "/cluster/node/worker/ens3fx/master", string(ptp.PtpStateChange), "notification", "LOCKED", true},
		{"other concrete port", resource, "ens4f2", "/cluster/node/worker/ens4f1/master", string(ptp.PtpStateChange), "notification", "LOCKED", true},
		{"OS clock address", resource, "ens4f2", "/cluster/node/worker/CLOCK_REALTIME", string(ptp.PtpStateChange), "notification", "LOCKED", true},
		{"OS clock event", resource, "ens4f2", "/cluster/node/worker/ens4f2/master", string(ptp.OsClockSyncStateChange), "notification", "LOCKED", true},
		{"combined event", resource, "ens4f2", "/cluster/node/worker/ens4f2/master", string(ptp.SyncStateChange), "notification", "LOCKED", true},
		{"metric is not notification", resource, "ens4f2", "/cluster/node/worker/ens4f2/master", string(ptp.PtpStateChange), "metric", "LOCKED", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"source":%q,"type":%q,"data":{"version":"1.0","values":[{"ResourceAddress":%q,"data_type":%q,"value_type":"enumeration","value":%q}]}}`, tc.source, tc.eventType, tc.address, tc.dataType, tc.state)
			state, err := ptpNotificationState([]byte(body), resource, tc.iface)
			if (err != nil) != tc.wantError || (!tc.wantError && state != tc.state) {
				t.Fatalf("state = %q, error = %v; wantError %t", state, err, tc.wantError)
			}
			if consumerPTPStateCount("Got CurrentState: "+body, resource, tc.iface, tc.state) != 0 {
				t.Fatal("CurrentState must not count as delivered notification")
			}
			logs := `time="now" level=info msg="received event ` + strings.ReplaceAll(body, `"`, `\"`) + `"`
			if got := consumerPTPStateCount(logs, resource, tc.iface, tc.state) != 0; got == tc.wantError {
				t.Fatalf("delivered notification matched = %t; want %t", got, !tc.wantError)
			}
			if consumerPTPStateCount(logs, resource, tc.iface, "ANOTHER_STATE") != 0 {
				t.Fatal("delivered notification must match the requested state")
			}
		})
	}
}

func TestPTPLockState(t *testing.T) {
	resource := "/cluster/node/worker" + string(ptp.PtpLockState)
	body := fmt.Sprintf(`{"source":%q,"type":%q,"data":{"values":[{"ResourceAddress":"/cluster/node/worker/ens4f2/master","data_type":"notification","value_type":"enumeration","value":"LOCKED"}]}}`, resource, ptp.PtpStateChange)
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
		api := &PublisherAPI{exec: func(command []string) (string, error) {
			if command[len(command)-1] != "http://"+ApiBaseV2+resource+"/CurrentState" {
				t.Fatalf("wrong CurrentState URL: %v", command)
			}
			return fmt.Sprintf("%s\n%d", body, status), nil
		}}
		state, err := api.PTPLockState(context.Background(), resource, "ens4f2")
		if (err != nil) != (status != http.StatusOK) || (status == http.StatusOK && state != "LOCKED") {
			t.Fatalf("HTTP %d: state = %q, error = %v", status, state, err)
		}
	}
}

func TestSubscriptionConfigMapReadDeadline(t *testing.T) {
	for _, timeout := range []time.Duration{30 * time.Millisecond, 5 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			original := client.Client
			client.Client = &client.ClientSet{Interface: kubernetes.NewForConfigOrDie(&rest.Config{Host: server.URL})}
			t.Cleanup(func() { client.Client = original })
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			start := time.Now()
			_, err := SubscriptionConfigMapData(ctx, "worker")
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("stalled ConfigMap read must preserve its deadline: %v", err)
			}
			if elapsed := time.Since(start); elapsed > min(timeout, 3*time.Second)+time.Second {
				t.Fatalf("read exceeded its deadline: %s", elapsed)
			}
		})
	}
}

func TestSubscriptionConfigMapMustExist(t *testing.T) {
	original := client.Client
	client.Client = &client.ClientSet{Interface: fake.NewClientset()}
	t.Cleanup(func() { client.Client = original })
	if _, err := SubscriptionConfigMapData(context.Background(), "worker"); err == nil {
		t.Fatal("a missing ConfigMap must not satisfy the empty-data assertion")
	}
}
