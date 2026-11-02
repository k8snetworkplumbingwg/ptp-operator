package dpll_netlink

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/rodaine/table"
)

// pinTableNetlinkTimeout bounds how long a single pin-table dial/dump is
// allowed to take. Pin dumps are diagnostic-only, so it's better to skip a
// table when the DPLL driver is slow/unresponsive than to stall on it.
const pinTableNetlinkTimeout = 2 * time.Second

// pinTableHeaders/pinTableHeadersWithDir are the column sets shared by every
// pin table rendered from this file. Kept as package vars (rather than
// inlined at each call site) so the two render paths (filtered/no-dir vs.
// unfiltered/with-dir) stay visibly in sync.
var (
	pinTableHeaders        = []interface{}{"id", "Brd. L", "Pkg. L", "prio", "adm.", "oper."}
	pinTableHeadersWithDir = []interface{}{"id", "Brd. L", "Pkg. L", "dir", "prio", "adm.", "oper."}
)

// pinTableConn is a DPLL netlink connection shared across LogPinTable calls,
// guarded by pinTableConnMu. Reusing one connection instead of dialing fresh
// on every device notification avoids the socket churn a per-call Dial would
// cause during lock-state flapping; the mutex also naturally serializes
// concurrent LogPinTable calls onto a single in-flight dial/dump at a time.
var (
	pinTableConnMu sync.Mutex
	pinTableConn   *Conn
)

// dialPinTableConn dials a new DPLL connection, bounded by
// pinTableNetlinkTimeout. The dial package doesn't expose a timeout/context
// knob, so the wait is bounded from the caller's side via a background
// goroutine: on timeout we give up and return an error instead of blocking
// forever, at the cost of abandoning that one dial attempt (harmless; it
// either fails on its own or yields a connection nobody uses).
func dialPinTableConn() (*Conn, error) {
	type result struct {
		conn *Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := Dial(nil)
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-time.After(pinTableNetlinkTimeout):
		return nil, fmt.Errorf("timed out dialing DPLL netlink after %s", pinTableNetlinkTimeout)
	}
}

// getPinTableConn returns the shared connection, dialing one lazily if there
// isn't a healthy one cached yet.
func getPinTableConn() (*Conn, error) {
	if pinTableConn != nil {
		return pinTableConn, nil
	}
	conn, err := dialPinTableConn()
	if err != nil {
		return nil, err
	}
	pinTableConn = conn
	return conn, nil
}

// invalidatePinTableConn discards the shared connection so the next call
// dials a fresh one. Called whenever an operation on it fails or times out,
// since the connection may be left in a broken/indeterminate state.
func invalidatePinTableConn() {
	if pinTableConn != nil {
		_ = pinTableConn.Close() //nolint:errcheck
		pinTableConn = nil
	}
}

// isLockedState returns true if the lock status means the DPLL has selected
// a single active reference (locked or locked-ho-acq), as opposed to
// unlocked/holdover where there is no single winning input yet.
func isLockedState(lockStatus uint32) bool {
	return lockStatus == DpllLockStatusLocked || lockStatus == DpllLockStatusLockedHoldoverAcquired
}

// isEnabledParent returns true if the parent device admin state indicates the
// pin is a candidate input for the DPLL (selectable or connected).
func isEnabledParent(pd *PinParentDevice) bool {
	return pd.State == PinStateSelectable || pd.State == PinStateConnected
}

// isActiveParent returns true if the parent device is the one currently
// selected/used by the DPLL as its synchronization source. Mirrors
// DpllConfig.ActivePhaseOffsetPin in pkg/dpll: legacy stacks report this via
// admin state "connected", while newer stacks report it via operstate
// "active" instead (in which case the admin state never exceeds
// "selectable"). Both signals are checked since not every driver populates
// both consistently.
func isActiveParent(pd *PinParentDevice) bool {
	return pd.State == PinStateConnected || pd.Operstate == PinOperstateActive
}

// pinRow holds the single relevant parent-device entry for one pin, ready to
// be rendered as one table row (no "dir" column: buildPinRows only ever
// keeps input pins, so it would be constant and redundant).
type pinRow struct {
	id           uint32
	boardLabel   string
	packageLabel string
	prio         string
	admin        string
	oper         string
}

// columns renders a pinRow as a table row.
func (r pinRow) columns() []string {
	return []string{fmt.Sprintf("%d", r.id), r.boardLabel, r.packageLabel, r.prio, r.admin, r.oper}
}

// pinRowsToColumns converts rows to the [][]string shape renderTable expects.
func pinRowsToColumns(rows []pinRow) [][]string {
	cols := make([][]string, len(rows))
	for i, r := range rows {
		cols[i] = r.columns()
	}
	return cols
}

// renderTable renders headers/rows into a table string using the shared
// rodaine/table formatting for this package. Pure function: no I/O, no
// logging, so it's trivial to unit test and safe to reuse from any call site
// that already has data shaped as rows of strings.
func renderTable(headers []interface{}, rows [][]string) string {
	var buf bytes.Buffer
	tbl := table.New(headers...)
	tbl.WithWriter(&buf)
	tbl.SetRows(rows)
	tbl.Print()
	return buf.String()
}

// LogPins logs the given pins as a compact table, one row per parent device
// of each pin. Unlike LogPinTable, this applies no filtering by direction,
// clock ID, device, or lock state — every parent device of every pin passed
// in is shown, including output pins.
//
// This is the shared rendering primitive behind LogPinTable and
// LogPinConfirmation, exported so other packages that already hold a
// []*PinInfo (their own DumpPinGet result, a single pin from DoPinGet, or a
// test fixture) can log a consistent table without duplicating table-building
// code or going through LogPinTable's dial/dump/notification-filtering path.
func LogPins(title string, pins []*PinInfo) {
	var rows [][]string
	for _, pin := range pins {
		for _, pd := range pin.ParentDevice {
			prioStr := "n/a"
			if pd.Prio != nil {
				prioStr = fmt.Sprintf("%d", *pd.Prio)
			}
			rows = append(rows, []string{
				fmt.Sprintf("%d", pin.ID),
				pin.BoardLabel,
				pin.PackageLabel,
				GetPinDirection(pd.Direction),
				prioStr,
				GetPinState(pd.State),
				GetPinOperstate(pd.Operstate),
			})
		}
	}
	if len(rows) == 0 {
		glog.Infof("=== DPLL pin table (%s): no relevant pins ===", title)
		return
	}
	glog.Infof("=== DPLL pin table (%s) ===\n%s", title, renderTable(pinTableHeadersWithDir, rows))
}

// LogPinConfirmation logs the current configuration of a single pin via
// LogPins. Unlike LogPinTable, this applies no filtering by direction, clock
// ID, device, or lock state — it's meant to show the full post-write state of
// exactly the pin that was just set (including output pins, which
// LogPinTable always skips), immediately after a pin-set command, for
// confirmation purposes.
func LogPinConfirmation(pin *PinInfo) {
	LogPins(fmt.Sprintf("confirm pin=%d %#x", pin.ID, pin.ClockID), []*PinInfo{pin})
}

// buildPinRows selects, among the pins belonging to clockID, the input pins
// whose parent-device entry matches deviceID (the DPLL device that triggered
// the notification), and returns one row per matching pin.
//
// Output pins are always skipped: they aren't related to a device lock-state
// change and are logged separately whenever they are set.
//
// Row selection also depends on lockStatus:
//   - locked / locked-ho-acq: only the active input pin (there's only one
//     reference that matters once a DPLL has locked onto it). See
//     isActiveParent for why both admin state and operstate are checked.
//   - unlocked / holdover: all enabled (selectable or connected) input pins,
//     to show every candidate the DPLL could pick next.
func buildPinRows(pins []*PinInfo, clockID uint64, deviceID uint32, lockStatus uint32) []pinRow {
	locked := isLockedState(lockStatus)
	var rows []pinRow

	for _, pin := range pins {
		if pin.ClockID != clockID {
			continue
		}
		for _, pd := range pin.ParentDevice {
			if pd.ParentID != deviceID || pd.Direction != PinDirectionInput {
				continue
			}
			include := false
			if locked {
				include = isActiveParent(&pd)
			} else {
				include = isEnabledParent(&pd)
			}
			if !include {
				continue
			}
			prioStr := "n/a"
			if pd.Prio != nil {
				prioStr = fmt.Sprintf("%d", *pd.Prio)
			}
			rows = append(rows, pinRow{
				id:           pin.ID,
				boardLabel:   pin.BoardLabel,
				packageLabel: pin.PackageLabel,
				prio:         prioStr,
				admin:        GetPinState(pd.State),
				oper:         GetPinOperstate(pd.Operstate),
			})
			break
		}
	}
	return rows
}

// LogPinTable logs a compact table of the input pins relevant to the DPLL
// device (deviceID) on the given clockID that just reported a lock-state
// change (reason is a free-form description used in the log line, e.g.
// "eth0 eec->LOCKED").
//
// This is a best-effort diagnostic: the actual netlink dial/dump happens in a
// background goroutine so callers on latency-sensitive paths (e.g.
// nlUpdateState, processing notifications inline) are never blocked or
// stalled by it.
func LogPinTable(reason string, clockID uint64, deviceID uint32, lockStatus uint32) {
	go fetchAndLogPinTable(reason, clockID, deviceID, lockStatus)
}

// fetchAndLogPinTable dials (or reuses) a DPLL connection, dumps every pin on
// the system, filters them down to the ones relevant to clockID/deviceID/
// lockStatus (see buildPinRows), and logs the result as a table. Only pins
// belonging to clockID are considered, and only the parent-device entry for
// deviceID is looked at, so unrelated clock chips and the other DPLL sharing
// the same chip (e.g. PPS vs EEC) don't pollute the table.
//
// It reuses a shared connection (see pinTableConn) rather than dialing fresh
// every call, and bounds the dump with pinTableNetlinkTimeout so a slow or
// unresponsive DPLL driver can't stall this indefinitely; either failure
// mode discards the shared connection so the next call starts clean.
func fetchAndLogPinTable(reason string, clockID uint64, deviceID uint32, lockStatus uint32) {
	pinTableConnMu.Lock()
	defer pinTableConnMu.Unlock()

	conn, err := getPinTableConn()
	if err != nil {
		glog.Errorf("pin table: failed to dial DPLL: %v", err)
		return
	}

	err = conn.GetGenetlinkConn().SetDeadline(time.Now().Add(pinTableNetlinkTimeout))
	if err != nil {
		glog.Errorf("pin table: failed to set netlink deadline: %v", err)
		invalidatePinTableConn()
		return
	}

	pins, err := conn.DumpPinGet()
	if err != nil {
		glog.Errorf("pin table: failed to dump pins: %v", err)
		invalidatePinTableConn()
		return
	}

	rows := buildPinRows(pins, clockID, deviceID, lockStatus)
	if len(rows) == 0 {
		glog.Infof("=== DPLL pin table (%s) clockID=%#x: no relevant pins ===", reason, clockID)
		return
	}

	glog.Infof("=== DPLL pin table (%s) clockID=%#x ===\n%s", reason, clockID, renderTable(pinTableHeaders, pinRowsToColumns(rows)))
}
