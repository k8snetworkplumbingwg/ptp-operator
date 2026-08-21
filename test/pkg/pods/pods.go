package pods

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/images"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/pointer"
)

// ExecCommand runs command in the pod and returns buffer output
// If mergeOutput is true, stderr will be merged into stdout, otherwise they are separate
func ExecCommand(cs *testclient.ClientSet, mergeOutput bool, pod *corev1.Pod, containerName string, command []string) (stdoutBuf, stderrBuf bytes.Buffer, err error) {
	req := testclient.Client.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     false, // Disable stdin for non-interactive commands
			Stdout:    true,
			Stderr:    true,
			TTY:       false, // Always disable TTY
		}, scheme.ParameterCodec)

	// Note: Using SPDY executor (deprecated but still functional)
	// TODO: Upgrade to WebSocket executor when client-go version supports it
	exec, err := remotecommand.NewSPDYExecutor(cs.Config, "POST", req.URL())
	if err != nil {
		return stdoutBuf, stderrBuf, err
	}

	// Always stream to separate buffers first
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdin:  nil, // No stdin for non-interactive commands
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
		Tty:    false, // Always disable TTY
	})

	// If mergeOutput is true, append stderr content to stdout buffer
	if mergeOutput {
		stdoutBuf.Write(stderrBuf.Bytes())
	}

	logrus.Tracef("ExecCommand podName=%s containerName=%s command=%v stdout=%s stderr=%s err=%s", pod.Name, containerName, command, stdoutBuf.String(), stderrBuf.String(), err)
	if err != nil {
		return stdoutBuf, stderrBuf, fmt.Errorf("exec.StreamWithContext failure. Stdout: %s, Stderr: %s, Err: %w", stdoutBuf.String(), stderrBuf.String(), err)
	}

	return stdoutBuf, stderrBuf, nil
}

// returns true if the pod passed as paremeter is running on the node selected by the label passed as a parameter.
// the label represent a ptp conformance test role such as: grandmaster, clock under test, slave1, slave2
func PodRole(runningPod *corev1.Pod, label string) (bool, error) {
	nodeList, err := testclient.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return false, fmt.Errorf("error getting node list")
	}
	for NodeNumber := range nodeList.Items {
		if runningPod.Spec.NodeName == nodeList.Items[NodeNumber].Name {
			return true, nil
		}
	}
	return false, nil
}

// returns true if a pod has a given label or node name
func HasPodLabelOrNodeName(pod *corev1.Pod, label *string, nodeName *string) (result bool, err error) {
	if label == nil && nodeName == nil {
		return result, fmt.Errorf("label and nodeName are nil")
	}
	// node name might be present and will be superseded by label
	/*if label != nil && nodeName != nil {
		return result, fmt.Errorf("label or nodeName must be nil")
	}*/
	if label != nil {
		result, err = PodRole(pod, *label)
		if err != nil {
			return result, fmt.Errorf("could not check %q pod role, err: %s", pkg.PtrStringOrDefault(label, "<nil>"), err)
		}
	}
	if nodeName != nil {
		result = pod.Spec.NodeName == *nodeName
	}
	return result, nil
}

// WaitForCondition waits until the pod will have specified condition type with the expected status
func WaitForCondition(cs *testclient.ClientSet, pod *corev1.Pod, conditionType corev1.PodConditionType, conditionStatus corev1.ConditionStatus, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		updatePod, err := cs.Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		for _, c := range updatePod.Status.Conditions {
			if c.Type == conditionType && c.Status == conditionStatus {
				return true, nil
			}
		}
		return false, nil
	})
}

func compileLogRegex(regex string, isLiteralText bool) *regexp.Regexp {
	const matchOnlyFullLines = `\s*^`
	if isLiteralText {
		regex = regexp.QuoteMeta(regex)
	} else {
		regex += matchOnlyFullLines
	}
	return regexp.MustCompile(regex)
}

// snapshotPodLogsRegex reads a non-follow log snapshot and returns every regex
// match. Callers that need the current value must use matches[len(matches)-1].
//
// Non-follow only: Follow + find-first returns the oldest hit in the stream,
// which is wrong for "current phc2sys HA source" when daemon logs are large.
// TailLines (or SinceTime) bounds the read so the client can finish the stream.
//
// Incomplete reads are hard errors: the API delivers oldest→newest, so a
// truncated buffer's last match is not the latest line and must not be trusted.
func snapshotPodLogsRegex(namespace, podName, containerName string, r *regexp.Regexp, tailLines *int64, since *time.Time) (matches [][]string, err error) {
	opts := corev1.PodLogOptions{
		Container: containerName,
		Follow:    false,
	}
	if tailLines != nil {
		opts.TailLines = tailLines
	}
	if since != nil {
		opts.SinceTime = &metav1.Time{Time: *since}
	}

	ctx, cancel := context.WithTimeout(context.Background(), pkg.TimeoutIn1Minute)
	defer cancel()
	stream, err := testclient.Client.CoreV1().Pods(namespace).GetLogs(podName, &opts).Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open log snapshot for %s/%s container=%s: %w", namespace, podName, containerName, err)
	}
	defer stream.Close()

	logContent, readErr := io.ReadAll(stream)
	if readErr != nil {
		return nil, fmt.Errorf("incomplete log snapshot for %s/%s container=%s (%d bytes): %w",
			namespace, podName, containerName, len(logContent), readErr)
	}
	if len(logContent) == 0 {
		return nil, nil
	}
	return r.FindAllStringSubmatch(string(logContent), -1), nil
}

func pollPodLogsRegex(namespace, podName, containerName string, r *regexp.Regexp, timeout time.Duration, tailLines *int64, since *time.Time) (matches [][]string, err error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		matches, err = snapshotPodLogsRegex(namespace, podName, containerName, r, tailLines, since)
		if err != nil {
			lastErr = err
		} else if len(matches) > 0 {
			return matches, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("could not find regex in logs for %s/%s container=%s: %w", namespace, podName, containerName, lastErr)
			}
			return nil, fmt.Errorf("could not find regex in logs for %s/%s container=%s: timed out after %s", namespace, podName, containerName, timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// GetPodLogsRegexSince returns all regex matches from pod logs at or after since.
// Callers that need the current/latest value should use matches[len(matches)-1].
//
// Semantics: repeatedly snapshot (Follow=false, SinceTime=since) until at least
// one match appears or timeout. This preserves transition sequences for HA
// tests and never returns a stale first-hit from a Follow stream.
func GetPodLogsRegexSince(namespace string, podName string, containerName, regex string, isLiteralText bool, timeout time.Duration, since time.Time) (matches [][]string, err error) {
	return pollPodLogsRegex(namespace, podName, containerName, compileLogRegex(regex, isLiteralText), timeout, nil, &since)
}

// GetPodLogsRegex returns regex matches from a recent TailLines snapshot of the
// pod logs. Callers that need the current/latest value should use
// matches[len(matches)-1].
//
// If no match is present yet, it re-snapshots until timeout. It does not use
// Follow-from-beginning / first-match semantics (unsafe with large daemon logs).
func GetPodLogsRegex(namespace string, podName string, containerName, regex string, isLiteralText bool, timeout time.Duration) (matches [][]string, err error) {
	// Bound the snapshot so ReadAll cannot stall on multi-100MB daemon logs.
	// Rare lines that may be older (e.g. phc2sys HA "selecting") need
	// GetPodLogsRegexTail or GetPodLogsRegexSince instead.
	var tailLines int64 = 20000
	return pollPodLogsRegex(namespace, podName, containerName, compileLogRegex(regex, isLiteralText), timeout, &tailLines, nil)
}

// GetPodLogsRegexTail is GetPodLogsRegex with an explicit TailLines bound.
// Use a large tail for rare log lines that remain the current truth (e.g.
// phc2sys HA source selection) without draining the entire daemon history.
func GetPodLogsRegexTail(namespace string, podName string, containerName, regex string, isLiteralText bool, timeout time.Duration, tailLines int64) (matches [][]string, err error) {
	if tailLines <= 0 {
		return nil, fmt.Errorf("tailLines must be > 0")
	}
	return pollPodLogsRegex(namespace, podName, containerName, compileLogRegex(regex, isLiteralText), timeout, &tailLines, nil)
}

func ExecutePtpInterfaceCommand(pod corev1.Pod, interfaceName string, command string) {
	const (
		pollingInterval = 3 * time.Second
	)
	gomega.Eventually(func() error {
		_, _, err := ExecCommand(client.Client, true, &pod, "container-00", []string{"sh", "-c", command})
		return err
	}, pkg.TimeoutIn10Minutes, pollingInterval).Should(gomega.BeNil())
}

func CheckRestart(pod corev1.Pod) {
	logrus.Printf("Restarting the node %s that pod %s is running on", pod.Spec.NodeName, pod.Name)

	const (
		pollingInterval = 3 * time.Second
	)

	gomega.Eventually(func() error {
		_, _, err := ExecCommand(client.Client, true, &pod, "container-00", []string{"chroot", "/host", "shutdown", "-r"})
		return err
	}, pkg.TimeoutIn10Minutes, pollingInterval).Should(gomega.BeNil())
}

func GetRebootDaemonsetPodsAt(node string) *corev1.PodList {

	rebootDaemonsetPodList, err := client.Client.CoreV1().Pods(pkg.RebootDaemonSetNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "name=" + pkg.RebootDaemonSetName, FieldSelector: fmt.Sprintf("spec.nodeName=%s", node)})
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	return rebootDaemonsetPodList
}

func getDefinition(namespace string) *corev1.Pod {
	podObject := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "testpod-",
			Namespace:    namespace},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: pointer.Int64Ptr(0),
			Containers: []corev1.Container{{Name: "test",
				Image:   images.For(images.TestUtils),
				Command: []string{"/bin/bash", "-c", "sleep INF"}}}}}

	return podObject
}

// DefinePodOnNode creates the pod defintion with a node selector
func DefinePodOnNode(namespace string, nodeName string) *corev1.Pod {
	pod := getDefinition(namespace)
	pod.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": nodeName}
	return pod
}

// RedefineAsPrivileged updates the pod definition to be privileged
func RedefineAsPrivileged(pod *corev1.Pod, containerName string) (*corev1.Pod, error) {
	c := containerByName(pod, containerName)
	if c == nil {
		return pod, fmt.Errorf("container with name: %s not found in pod", containerName)
	}
	if c.SecurityContext == nil {
		c.SecurityContext = &corev1.SecurityContext{}
	}
	c.SecurityContext.Privileged = pointer.BoolPtr(true)

	return pod, nil
}

func containerByName(pod *corev1.Pod, containerName string) *corev1.Container {
	if containerName == "" {
		return &pod.Spec.Containers[0]
	}

	for _, c := range pod.Spec.Containers {
		if c.Name == containerName {
			return &c
		}
	}

	return nil
}
