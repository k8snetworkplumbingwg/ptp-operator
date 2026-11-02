package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestLink creates a Link wired to an in-memory net.Pipe.
// Returns the Link (not yet running) and the remote end of the pipe.
// Because dial() is called once per Run() connection cycle, the dialFn
// returns the same linkEnd every time; for reconnection tests a more
// sophisticated factory would be needed.
func newTestLink(cache *Cache) (*Link, net.Conn) {
	linkEnd, remoteEnd := net.Pipe()

	l := NewLink("", cache)
	l.dialFn = func(context.Context) (net.Conn, error) {
		return linkEnd, nil
	}
	return l, remoteEnd
}

func TestLinkSnapshotOnStatusRequest(t *testing.T) {
	cache := NewCache(64)
	cache.Send(Message{Type: TypePTPState, Profile: "ptp4l.0.config", IFace: "ens2f0", Values: StateValue{State: StateLocked}})
	cache.Send(Message{Type: TypeClockClass, Profile: "ptp4l.0.config", Values: ClockClassValue{ClockClass: 6}})
	for len(cache.outCh) > 0 {
		<-cache.outCh
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l, remote := newTestLink(cache)
	defer remote.Close()
	go l.Run(ctx)

	err := Transmit(remote, Message{Type: TypeStatusRequest, Version: Version})
	require.NoError(t, err)

	scanner := bufio.NewScanner(remote)
	received := map[string]Message{}
	for len(received) < 2 {
		remote.SetReadDeadline(time.Now().Add(5 * time.Second))
		if !scanner.Scan() {
			t.Fatalf("scanner stopped after %d messages: %v", len(received), scanner.Err())
		}
		var msg Message
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &msg))
		received[msg.Type] = msg
	}

	assert.Contains(t, received, TypePTPState)
	assert.Contains(t, received, TypeClockClass)
	assert.Equal(t, StateLocked, received[TypePTPState].Values.(StateValue).State)
	assert.Equal(t, uint8(6), received[TypeClockClass].Values.(ClockClassValue).ClockClass)
}

func TestLinkLiveMessages(t *testing.T) {
	cache := NewCache(64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l, remote := newTestLink(cache)
	defer remote.Close()
	go l.Run(ctx)

	cache.Send(Message{Type: TypeGNSSState, Profile: "ts2phc.0.config", IFace: "ens2f0", Values: GNSSStateValue{State: GNSSSynchronized}})

	scanner := bufio.NewScanner(remote)
	remote.SetReadDeadline(time.Now().Add(5 * time.Second))
	require.True(t, scanner.Scan(), "expected to read a message")

	var msg Message
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &msg))
	assert.Equal(t, TypeGNSSState, msg.Type)
	assert.Equal(t, GNSSSynchronized, msg.Values.(GNSSStateValue).State)
}

func TestLinkShutdown(t *testing.T) {
	cache := NewCache(64)

	ctx, cancel := context.WithCancel(context.Background())

	l, remote := newTestLink(cache)
	defer remote.Close()

	done := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(done)
	}()

	// Give Run() time to dial and start loops
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Link.Run() did not exit after context cancellation")
	}
}
