package ptptesthelper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
)

var ErrClosed = errors.New("log waiter closed")

const defaultLogWaitTimeout = 3 * time.Minute

type WaitOptions struct {
	Context context.Context
	Timeout time.Duration
}

type LogWaiterOptions struct {
	Namespace      string
	NodeName       string
	Container      string
	LogOptions     corev1.PodLogOptions
	WaitOptions    WaitOptions
	FollowRestarts bool
	Dedupe         bool
	// ChannelFactory transfers ownership of its channel to the waiter. Close closes it.
	ChannelFactory func() chan Match
}

type Match struct {
	Tag  string
	Line string
}

type LogTagHistory struct {
	Match  Match
	Status string
}

type LogWaitError struct {
	Err             error
	History         []LogTagHistory
	Pending         int
	LastStreamError error
}

func (e *LogWaitError) Error() string {
	return fmt.Sprintf("%v; history: %v; pending matches: %d; last stream error: %v", e.Err, e.History, e.Pending, e.LastStreamError)
}

func (e *LogWaitError) Unwrap() error { return e.Err }

// TaggedLogWaiter follows one container on a node and queues tags in log order.
type TaggedLogWaiter struct {
	LogWaiterOptions
	tags       chan Match
	mu         sync.Mutex
	predicates []tagPredicate
	history    []LogTagHistory
	lastTag    string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	finished   chan struct{}
	streamErr  error
	closeOnce  sync.Once
}

type tagPredicate struct {
	tag   string
	match func(string) bool
}

func NewTaggedLogWaiter(options LogWaiterOptions) (*TaggedLogWaiter, error) {
	return newTaggedLogWaiter(options, nil)
}

func newTaggedLogWaiter(options LogWaiterOptions, initial *tagPredicate) (*TaggedLogWaiter, error) {
	if options.NodeName == "" || options.Container == "" {
		return nil, errors.New("node and container are required")
	}
	if options.Namespace == "" {
		options.Namespace = pkg.PtpLinuxDaemonNamespace
	}
	var tags chan Match
	if options.ChannelFactory != nil {
		tags = options.ChannelFactory()
	} else {
		tags = make(chan Match, 1024)
	}
	if tags == nil {
		return nil, errors.New("channel factory returned nil")
	}
	if cap(tags) == 0 {
		return nil, errors.New("channel factory must return a buffered channel")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &TaggedLogWaiter{LogWaiterOptions: options, tags: tags, ctx: ctx, cancel: cancel, done: make(chan struct{}), finished: make(chan struct{})}
	if initial != nil {
		w.predicates = append(w.predicates, *initial)
	}
	go w.follow()
	return w, nil
}

func (w *TaggedLogWaiter) Register(tag string, predicate func(string) bool) error {
	if predicate == nil {
		return errors.New("predicate is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.done:
		return ErrClosed
	default:
	}
	w.predicates = append(w.predicates, tagPredicate{tag, predicate})
	return nil
}

func (w *TaggedLogWaiter) emit(line string) bool {
	w.mu.Lock()
	predicates := slices.Clone(w.predicates)
	w.mu.Unlock()
	for _, p := range predicates {
		if !p.match(line) {
			continue
		}
		if w.Dedupe && p.tag == w.lastTag {
			continue
		}
		select {
		case <-w.done:
			return false
		case w.tags <- Match{Tag: p.tag, Line: line}:
			w.lastTag = p.tag
		}
	}
	return true
}

func (w *TaggedLogWaiter) Wait(tag string, options ...WaitOptions) (Match, error) {
	return w.WaitFor(func(got Match) bool { return got.Tag == tag }, options...)
}

func (w *TaggedLogWaiter) WaitFor(match func(Match) bool, options ...WaitOptions) (Match, error) {
	if match == nil {
		return Match{}, errors.New("match function is nil")
	}
	config := w.WaitOptions
	if len(options) > 0 {
		if options[0].Context != nil {
			config.Context = options[0].Context
		}
		if options[0].Timeout != 0 {
			config.Timeout = options[0].Timeout
		}
	}
	if config.Context == nil {
		config.Context = context.Background()
	}
	if config.Timeout == 0 {
		config.Timeout = defaultLogWaitTimeout
	}
	ctx, cancel := context.WithTimeout(config.Context, config.Timeout)
	defer cancel()
	for {
		select {
		case <-w.done:
			return Match{}, ErrClosed
		default:
		}
		result, received := w.take()
		if received {
			if w.consume(result, match) {
				return result, nil
			}
			continue
		}
		select {
		case <-w.done:
			return Match{}, ErrClosed
		case <-ctx.Done():
			return Match{}, w.waitError(fmt.Errorf("waiting for log tag: %w", ctx.Err()))
		case <-w.finished:
			select {
			case <-w.done:
				return Match{}, ErrClosed
			default:
			}
			// The final match may have arrived between the first queue check and this select.
			if result, received := w.take(); received {
				if w.consume(result, match) {
					return result, nil
				}
				continue
			}
			return Match{}, w.waitError(nil)
		case result := <-w.tags:
			if w.consume(result, match) {
				return result, nil
			}
		}
	}
}

func (w *TaggedLogWaiter) take() (Match, bool) {
	select {
	case result, ok := <-w.tags:
		return result, ok
	default:
		return Match{}, false
	}
}

func (w *TaggedLogWaiter) consume(result Match, match func(Match) bool) bool {
	matched := match(result)
	status := "discarded"
	if matched {
		status = "consumed"
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.history = append(w.history, LogTagHistory{Match: result, Status: status})
	return matched
}

func (w *TaggedLogWaiter) waitError(err error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err == nil {
		err = fmt.Errorf("log stream stopped: %w", w.streamErr)
	}
	return &LogWaitError{Err: err, History: slices.Clone(w.history), Pending: len(w.tags), LastStreamError: w.streamErr}
}

func (w *TaggedLogWaiter) Close() {
	w.closeOnce.Do(func() {
		close(w.done)
		w.cancel()
		<-w.finished
		close(w.tags)
	})
}

func (w *TaggedLogWaiter) follow() {
	defer close(w.finished)
	first := true
	var lastTime *metav1.Time
	for {
		if w.ctx.Err() != nil {
			return
		}
		pod, err := w.podOnNode()
		if err == nil {
			options := w.LogOptions
			options.Container = w.Container
			options.Follow = true
			options.Timestamps = true
			if first && options.TailLines == nil && options.SinceTime == nil && options.SinceSeconds == nil {
				zero := int64(0)
				options.TailLines = &zero
			}
			if !first && lastTime != nil {
				options.SinceTime = lastTime
				options.SinceSeconds = nil
				options.TailLines = nil
			}
			var stream io.ReadCloser
			stream, err = client.Client.CoreV1().Pods(w.Namespace).GetLogs(pod.Name, &options).Stream(w.ctx)
			if err == nil {
				lastTime, err = w.readStream(stream, lastTime)
				stream.Close()
			}
		}
		w.mu.Lock()
		w.streamErr = err
		w.mu.Unlock()
		if !w.FollowRestarts {
			return
		}
		first = false
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (w *TaggedLogWaiter) podOnNode() (*corev1.Pod, error) {
	pods, err := client.Client.CoreV1().Pods(w.Namespace).List(w.ctx, metav1.ListOptions{LabelSelector: pkg.PtpLinuxDaemonPodsLabel})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName == w.NodeName && p.Status.Phase == corev1.PodRunning {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no running linuxptp-daemon pod on node %s", w.NodeName)
}

func (w *TaggedLogWaiter) readStream(stream io.Reader, last *metav1.Time) (*metav1.Time, error) {
	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		line := scanner.Text()
		timestamp, text, ok := strings.Cut(line, " ")
		if ok {
			if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
				last = &metav1.Time{Time: parsed.Add(time.Nanosecond)}
				line = text
			}
		}
		if !w.emit(line) {
			return last, ErrClosed
		}
	}
	if err := scanner.Err(); err != nil {
		return last, err
	}
	return last, io.EOF
}

const untaggedLogTag = "match"

type LogWaiter struct{ tagged *TaggedLogWaiter }

func NewLogWaiter(options LogWaiterOptions, predicate func(string) bool) (*LogWaiter, error) {
	if predicate == nil {
		return nil, errors.New("predicate is nil")
	}
	w, err := newTaggedLogWaiter(options, &tagPredicate{untaggedLogTag, predicate})
	if err != nil {
		return nil, err
	}
	return &LogWaiter{tagged: w}, nil
}

func (w *LogWaiter) Wait(options ...WaitOptions) (Match, error) {
	return w.tagged.Wait(untaggedLogTag, options...)
}

func (w *LogWaiter) Close() { w.tagged.Close() }
