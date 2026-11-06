package ipc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProfile = "ptp4l.0.config"
	testIFace   = "ens2f0"
)

func TestCacheSendAndDedup(t *testing.T) {
	c := NewCache(10)

	msg := Message{
		Type:    TypePTPState,
		Profile: testProfile,
		IFace:   testIFace,
		Values:  StateValue{State: StateLocked},
	}

	assert.True(t, c.Send(msg), "first send should store")
	assert.False(t, c.Send(msg), "duplicate should be suppressed")

	updated := msg
	updated.Values = StateValue{State: StateFreerun}
	assert.True(t, c.Send(updated), "changed values should store")
	assert.False(t, c.Send(updated), "same changed values should be suppressed")
}

func TestCacheSendDifferentKeys(t *testing.T) {
	c := NewCache(10)

	ptpMsg := Message{
		Type:    TypePTPState,
		Profile: testProfile,
		Values:  StateValue{State: StateLocked},
	}
	clockMsg := Message{
		Type:    TypeClockClass,
		Profile: testProfile,
		Values:  ClockClassValue{ClockClass: 6},
	}

	assert.True(t, c.Send(ptpMsg))
	assert.True(t, c.Send(clockMsg))

	snap := c.Snapshot()
	assert.Len(t, snap, 2)
}

func TestCacheSendDifferentProfiles(t *testing.T) {
	c := NewCache(10)

	msg0 := Message{
		Type:    TypePTPState,
		Profile: testProfile,
		Values:  StateValue{State: StateLocked},
	}
	msg1 := Message{
		Type:    TypePTPState,
		Profile: "ptp4l.1.config",
		Values:  StateValue{State: StateFreerun},
	}

	assert.True(t, c.Send(msg0))
	assert.True(t, c.Send(msg1))

	snap := c.Snapshot()
	assert.Len(t, snap, 2)
}

func TestCacheOutChannel(t *testing.T) {
	c := NewCache(10)

	msg := Message{
		Type:    TypePTPState,
		Profile: testProfile,
		Values:  StateValue{State: StateLocked},
	}
	c.Send(msg)

	select {
	case got := <-c.Out():
		assert.Equal(t, TypePTPState, got.Type)
		assert.Equal(t, StateValue{State: StateLocked}, got.Values)
	case <-time.After(time.Second):
		t.Fatal("expected message on outbound channel")
	}

	// Duplicate should not produce a channel message
	c.Send(msg)
	select {
	case <-c.Out():
		t.Fatal("duplicate should not produce outbound message")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCacheOutChannelNonBlocking(t *testing.T) {
	c := NewCache(1) // buffer size 1

	msg1 := Message{Type: TypePTPState, Profile: testProfile, Values: StateValue{State: StateLocked}}
	msg2 := Message{Type: TypeClockClass, Profile: testProfile, Values: ClockClassValue{ClockClass: 6}}

	c.Send(msg1) // fills the channel buffer
	c.Send(msg2) // should not block even though channel is full

	snap := c.Snapshot()
	assert.Len(t, snap, 2, "both messages should be in the cache store even if channel is full")
}

func TestCacheSnapshot(t *testing.T) {
	c := NewCache(10)

	c.Send(Message{Type: TypePTPState, Profile: testProfile, Values: StateValue{State: StateLocked}})
	c.Send(Message{Type: TypeClockClass, Profile: testProfile, Values: ClockClassValue{ClockClass: 7}})

	snap := c.Snapshot()
	require.Len(t, snap, 2)

	types := map[string]bool{}
	for _, m := range snap {
		types[m.Type] = true
	}
	assert.True(t, types[TypePTPState])
	assert.True(t, types[TypeClockClass])
}

func TestCacheClear(t *testing.T) {
	c := NewCache(10)

	c.Send(Message{Type: TypePTPState, Profile: testProfile, Values: StateValue{State: StateLocked}})
	assert.Len(t, c.Snapshot(), 1)

	c.Clear()
	assert.Len(t, c.Snapshot(), 0)

	// After clear, same message should be accepted again (no longer a duplicate)
	assert.True(t, c.Send(Message{Type: TypePTPState, Profile: testProfile, Values: StateValue{State: StateLocked}}))
}

func TestCacheAutoFillsVersionAndTimestamp(t *testing.T) {
	c := NewCache(10)

	msg := Message{
		Type:    TypePTPState,
		Profile: testProfile,
		Values:  StateValue{State: StateLocked},
	}
	c.Send(msg)

	got := <-c.Out()
	assert.Equal(t, Version, got.Version)
	assert.NotEmpty(t, got.Timestamp)

	_, err := time.Parse(time.RFC3339Nano, got.Timestamp)
	assert.NoError(t, err, "timestamp should be valid RFC3339Nano")
}

func TestCachePreservesExplicitVersionAndTimestamp(t *testing.T) {
	c := NewCache(10)

	msg := Message{
		Version:   42,
		Timestamp: "2024-01-15T10:30:00.123456789Z",
		Type:      TypePTPState,
		Profile:   testProfile,
		Values:    StateValue{State: StateLocked},
	}
	c.Send(msg)

	got := <-c.Out()
	assert.Equal(t, 42, got.Version)
	assert.Equal(t, "2024-01-15T10:30:00.123456789Z", got.Timestamp)
}

func TestCacheIFaceChangeIsNotDuplicate(t *testing.T) {
	c := NewCache(10)

	msg1 := Message{
		Type:    TypePTPState,
		Profile: testProfile,
		IFace:   testIFace,
		Values:  StateValue{State: StateLocked},
	}
	msg2 := Message{
		Type:    TypePTPState,
		Profile: testProfile,
		IFace:   "ens3f0",
		Values:  StateValue{State: StateLocked},
	}

	assert.True(t, c.Send(msg1))
	assert.True(t, c.Send(msg2), "different IFace should not be treated as duplicate")

	snap := c.Snapshot()
	assert.Len(t, snap, 2, "snapshot should retain both interface entries")
}
