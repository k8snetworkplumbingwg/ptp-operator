package dpll_netlink

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	testPinREF0P = "REF0P"
	testPinREF0N = "REF0N"
)

func prio(v uint32) *uint32 { return &v }

func TestIsLockedState(t *testing.T) {
	assert.False(t, isLockedState(DpllLockStatusUnlocked))
	assert.False(t, isLockedState(DpllLockStatusHoldover))
	assert.True(t, isLockedState(DpllLockStatusLocked))
	assert.True(t, isLockedState(DpllLockStatusLockedHoldoverAcquired))
}

func TestIsEnabledParent(t *testing.T) {
	assert.True(t, isEnabledParent(&PinParentDevice{State: PinStateSelectable}))
	assert.True(t, isEnabledParent(&PinParentDevice{State: PinStateConnected}))
	assert.False(t, isEnabledParent(&PinParentDevice{State: PinStateDisconnected}))
}

func TestIsActiveParent(t *testing.T) {
	// Newer stacks: admin state stays "selectable", operstate reports "active".
	assert.True(t, isActiveParent(&PinParentDevice{State: PinStateSelectable, Operstate: PinOperstateActive}))
	// Legacy stacks: admin state goes to "connected", operstate may be unreported/unknown.
	assert.True(t, isActiveParent(&PinParentDevice{State: PinStateConnected, Operstate: 0}))
	// Neither signal present: not active.
	assert.False(t, isActiveParent(&PinParentDevice{State: PinStateSelectable, Operstate: PinOperstateStandby}))
	assert.False(t, isActiveParent(&PinParentDevice{State: PinStateDisconnected, Operstate: 0}))
}

func TestBuildPinRows_FiltersByClockIDAndDevice(t *testing.T) {
	pins := []*PinInfo{
		{
			ID: 16, ClockID: 0xAA, BoardLabel: testPinREF0P,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateSelectable, Operstate: PinOperstateActive, Prio: prio(6)},
				{ParentID: 2, Direction: PinDirectionInput, State: PinStateDisconnected, Operstate: PinOperstateStandby, Prio: prio(14)},
			},
		},
		{
			// Different clock ID entirely: must be excluded.
			ID: 17, ClockID: 0xBB, BoardLabel: "OTHERCHIP",
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateSelectable, Operstate: PinOperstateActive, Prio: prio(1)},
			},
		},
	}

	// Unlocked: pin is enabled for device 1, should be included.
	rows := buildPinRows(pins, 0xAA, 1, DpllLockStatusUnlocked)
	assert.Len(t, rows, 1)
	assert.Equal(t, uint32(16), rows[0].id)
	assert.Equal(t, testPinREF0P, rows[0].boardLabel)
	assert.Equal(t, "6", rows[0].prio)

	// Device 2's parent entry for the same pin is disconnected, so nothing shows for it.
	rows = buildPinRows(pins, 0xAA, 2, DpllLockStatusUnlocked)
	assert.Empty(t, rows)
}

func TestBuildPinRows_SkipsOutputPins(t *testing.T) {
	pins := []*PinInfo{
		{
			ID: 25, ClockID: 0xAA, BoardLabel: "OUT3",
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionOutput, State: PinStateConnected, Operstate: PinOperstateActive},
			},
		},
	}
	rows := buildPinRows(pins, 0xAA, 1, DpllLockStatusLocked)
	assert.Empty(t, rows, "output pins must never be included")
}

func TestBuildPinRows_LockedShowsOnlyActive(t *testing.T) {
	pins := []*PinInfo{
		{
			ID: 16, ClockID: 0xAA, BoardLabel: testPinREF0P,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateSelectable, Operstate: PinOperstateActive, Prio: prio(6)},
			},
		},
		{
			ID: 17, ClockID: 0xAA, BoardLabel: testPinREF0N,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateSelectable, Operstate: PinOperstateStandby, Prio: prio(7)},
			},
		},
	}

	rows := buildPinRows(pins, 0xAA, 1, DpllLockStatusLocked)
	assert.Len(t, rows, 1, "locked state should only show the active input")
	assert.Equal(t, uint32(16), rows[0].id)

	rows = buildPinRows(pins, 0xAA, 1, DpllLockStatusLockedHoldoverAcquired)
	assert.Len(t, rows, 1, "locked-ho-acq state should only show the active input")
	assert.Equal(t, uint32(16), rows[0].id)
}

// TestBuildPinRows_LockedFallsBackToLegacyConnectedState covers hardware/drivers
// that never report Operstate as active (it stays "unknown"), but do report the
// legacy admin state "connected" for the pin actually in use. Without this
// fallback the table would always print "no relevant pins" once locked.
func TestBuildPinRows_LockedFallsBackToLegacyConnectedState(t *testing.T) {
	pins := []*PinInfo{
		{
			ID: 16, ClockID: 0xAA, BoardLabel: testPinREF0P,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateConnected, Operstate: 0, Prio: prio(6)},
			},
		},
		{
			ID: 17, ClockID: 0xAA, BoardLabel: testPinREF0N,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateSelectable, Operstate: 0, Prio: prio(7)},
			},
		},
	}

	rows := buildPinRows(pins, 0xAA, 1, DpllLockStatusLocked)
	assert.Len(t, rows, 1, "the legacy 'connected' admin state should count as active even with an unreported operstate")
	assert.Equal(t, uint32(16), rows[0].id)
}

func TestBuildPinRows_UnlockedShowsAllEnabled(t *testing.T) {
	pins := []*PinInfo{
		{
			ID: 16, ClockID: 0xAA, BoardLabel: testPinREF0P,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateSelectable, Operstate: PinOperstateStandby, Prio: prio(6)},
			},
		},
		{
			ID: 17, ClockID: 0xAA, BoardLabel: testPinREF0N,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateDisconnected, Operstate: PinOperstateNoSignal, Prio: prio(7)},
			},
		},
	}

	rows := buildPinRows(pins, 0xAA, 1, DpllLockStatusUnlocked)
	assert.Len(t, rows, 1, "only the enabled (selectable/connected) input should show")
	assert.Equal(t, uint32(16), rows[0].id)

	rows = buildPinRows(pins, 0xAA, 1, DpllLockStatusHoldover)
	assert.Len(t, rows, 1, "holdover behaves the same as unlocked")
	assert.Equal(t, uint32(16), rows[0].id)
}

func TestBuildPinRows_PrioNil(t *testing.T) {
	pins := []*PinInfo{
		{
			ID: 5, ClockID: 0xBB, BoardLabel: "IN0",
			ParentDevice: []PinParentDevice{
				{ParentID: 0, Direction: PinDirectionInput, State: PinStateConnected, Operstate: PinOperstateActive, Prio: nil},
			},
		},
	}
	rows := buildPinRows(pins, 0xBB, 0, DpllLockStatusLocked)
	assert.Len(t, rows, 1)
	assert.Equal(t, "n/a", rows[0].prio)
}

func TestBuildPinRows_Empty(t *testing.T) {
	assert.Empty(t, buildPinRows(nil, 0xAA, 0, DpllLockStatusUnlocked))
}

func TestInvalidatePinTableConn_NilSafe(t *testing.T) {
	pinTableConnMu.Lock()
	pinTableConn = nil
	pinTableConnMu.Unlock()

	assert.NotPanics(t, invalidatePinTableConn)
}

// TestDialPinTableConn_ReturnsPromptly guards against dialPinTableConn ever
// silently blocking for the full pinTableNetlinkTimeout. Whether the "dpll"
// netlink family is available depends on the environment (e.g. it fails
// immediately on non-Linux OSes, but some Linux CI kernels register the
// family even without real DPLL hardware behind it), so this only asserts
// on timing, not on a specific dial outcome.
func TestDialPinTableConn_ReturnsPromptly(t *testing.T) {
	start := time.Now()
	conn, err := dialPinTableConn()
	if err == nil {
		defer conn.Close() //nolint:errcheck
	}
	assert.Less(t, time.Since(start), pinTableNetlinkTimeout)
}

func TestRenderTable(t *testing.T) {
	selectable := GetPinState(PinStateSelectable)
	out := renderTable(pinTableHeaders, [][]string{
		{"16", "REF0P", "", "6", selectable, pinOperstateActive},
		{"17", "REF0N", "", "7", selectable, pinOperstateStandby},
	})
	assert.Contains(t, out, "id")
	assert.Contains(t, out, "REF0P")
	assert.Contains(t, out, "REF0N")
	assert.Contains(t, out, pinOperstateActive)
}

func TestRenderTable_Empty(t *testing.T) {
	out := renderTable(pinTableHeaders, nil)
	// Header row should still render even with zero data rows.
	assert.Contains(t, out, "id")
}

func TestPinRowColumns(t *testing.T) {
	selectable := GetPinState(PinStateSelectable)
	r := pinRow{id: 16, boardLabel: testPinREF0P, packageLabel: "", prio: "6", admin: selectable, oper: pinOperstateActive}
	assert.Equal(t, []string{"16", testPinREF0P, "", "6", selectable, pinOperstateActive}, r.columns())
}

func TestPinRowsToColumns(t *testing.T) {
	selectable := GetPinState(PinStateSelectable)
	rows := []pinRow{
		{id: 16, boardLabel: testPinREF0P, prio: "6", admin: selectable, oper: pinOperstateActive},
		{id: 17, boardLabel: testPinREF0N, prio: "7", admin: selectable, oper: pinOperstateStandby},
	}
	cols := pinRowsToColumns(rows)
	assert.Len(t, cols, 2)
	assert.Equal(t, "16", cols[0][0])
	assert.Equal(t, testPinREF0N, cols[1][1])
}

// TestLogPins_DoesNotPanic exercises LogPins (and, through it,
// LogPinConfirmation) across a few shapes that could plausibly trip up the
// row-building loop: no pins, a pin with no parent devices, and a pin with
// multiple parents including an output direction (which LogPins, unlike
// LogPinTable/buildPinRows, must still show).
func TestLogPins_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() { LogPins("empty", nil) })
	assert.NotPanics(t, func() { LogPins("no-parents", []*PinInfo{{ID: 1, BoardLabel: "X"}}) })
	assert.NotPanics(t, func() {
		LogPins("mixed", []*PinInfo{
			{
				ID: 25, ClockID: 0xAA, BoardLabel: "OUT3",
				ParentDevice: []PinParentDevice{
					{ParentID: 1, Direction: PinDirectionOutput, State: PinStateConnected, Prio: prio(3)},
					{ParentID: 2, Direction: PinDirectionInput, State: PinStateSelectable, Prio: nil},
				},
			},
		})
	})
}

func TestLogPinConfirmation_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		LogPinConfirmation(&PinInfo{
			ID: 16, ClockID: 0xAA, BoardLabel: testPinREF0P,
			ParentDevice: []PinParentDevice{
				{ParentID: 1, Direction: PinDirectionInput, State: PinStateConnected, Prio: prio(6)},
			},
		})
	})
}

func TestBuildPinRows_WithPackageLabel(t *testing.T) {
	pins := []*PinInfo{
		{
			ID: 5, ClockID: 0xDD, BoardLabel: "SDP0", PackageLabel: "U1901",
			ParentDevice: []PinParentDevice{
				{ParentID: 0, Direction: PinDirectionInput, State: PinStateSelectable, Operstate: PinOperstateStandby, Prio: prio(3)},
			},
		},
	}
	rows := buildPinRows(pins, 0xDD, 0, DpllLockStatusUnlocked)
	assert.Len(t, rows, 1)
	assert.Equal(t, "SDP0", rows[0].boardLabel)
	assert.Equal(t, "U1901", rows[0].packageLabel)
}
