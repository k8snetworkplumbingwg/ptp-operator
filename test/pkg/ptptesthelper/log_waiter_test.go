package ptptesthelper

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newLocalLogWaiter(buffer int) *TaggedLogWaiter {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaggedLogWaiter{tags: make(chan Match, buffer), done: make(chan struct{}), finished: make(chan struct{}), ctx: ctx, cancel: cancel}
}

func TestTaggedLogWaiterOrderingAndHistory(t *testing.T) {
	w := newLocalLogWaiter(10)
	w.Register("A", func(line string) bool { return strings.HasPrefix(line, "A") })
	w.Register("B", func(line string) bool { return strings.HasPrefix(line, "B") })
	for _, line := range []string{"B first", "A detail", "B second"} {
		w.emit(line)
	}
	if len(w.history) != 0 {
		t.Fatalf("history includes matches that have not been read: %v", w.history)
	}
	if got, err := w.WaitFor(func(m Match) bool { return m.Tag == "A" && m.Line == "A detail" }, WaitOptions{Timeout: time.Millisecond}); err != nil || got.Line != "A detail" {
		t.Fatalf("unexpected match %v, error %v", got, err)
	}
	if got, err := w.Wait("B", WaitOptions{Timeout: time.Millisecond}); err != nil || got.Line != "B second" {
		t.Fatalf("unexpected match %v, error %v", got, err)
	}
	_, err := w.Wait("B", WaitOptions{Timeout: time.Millisecond})
	var waitErr *LogWaitError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &waitErr) {
		t.Fatalf("expected structured timeout, got %v", err)
	}
	if len(waitErr.History) != 3 || waitErr.History[0].Status != "discarded" || waitErr.History[1].Status != "consumed" || waitErr.History[2].Status != "consumed" || waitErr.Pending != 0 || !strings.Contains(err.Error(), "discarded") {
		t.Fatalf("unexpected history: %v", waitErr.History)
	}
}

func TestTaggedLogWaiterDedupe(t *testing.T) {
	w := newLocalLogWaiter(10)
	w.Dedupe = true
	w.Register("A", func(line string) bool { return line == "A" })
	w.Register("B", func(line string) bool { return line == "B" })
	for _, line := range []string{"A", "B", "B", "B", "A", "A", "B", "B", "B", "A"} {
		w.emit(line)
	}
	if got := len(w.tags); got != 5 {
		t.Fatalf("wanted 5 tags, got %d", got)
	}
}

func TestTaggedLogWaiterCloseUnblocksFullChannel(t *testing.T) {
	w := newLocalLogWaiter(1)
	w.Register("A", func(string) bool { return true })
	w.emit("first")
	stopped := make(chan struct{})
	go func() { w.emit("second"); close(stopped); close(w.finished) }()
	w.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("emit remained blocked")
	}
	if _, err := w.Wait("A"); !errors.Is(err, ErrClosed) {
		t.Fatalf("wanted ErrClosed, got %v", err)
	}
}

func TestTaggedLogWaiterCloseUnblocksWait(t *testing.T) {
	w := newLocalLogWaiter(1)
	result := make(chan error, 1)
	go func() { _, err := w.Wait("A"); result <- err }()
	go func() { <-w.done; close(w.finished) }()
	w.Close()
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("wanted ErrClosed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait remained blocked")
	}
}

func TestTaggedLogWaiterDrainsMatchAtStreamEnd(t *testing.T) {
	for i := 0; i < 100; i++ {
		w := newLocalLogWaiter(1)
		w.Register("A", func(string) bool { return true })
		result := make(chan error, 1)
		go func() {
			got, err := w.Wait("A", WaitOptions{Timeout: time.Second})
			if err == nil && got.Line != "final line" {
				err = errors.New("wrong final line")
			}
			result <- err
		}()
		time.Sleep(time.Millisecond)
		w.emit("final line")
		close(w.finished)
		if err := <-result; err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}
