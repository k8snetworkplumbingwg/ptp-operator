package pods

import (
	"testing"
)

// Ensures compileLogRegex + FindAll yields the newest interface when the buffer
// contains multiple historical selections (regression for DualNICBCHA HA tests
// that mistook a stale first match for the current source).
func TestPhc2sysSelectionPatternReturnsLatestMatch(t *testing.T) {
	const pattern = `phc2sys(?m).*?:.* selecting (\w+) as out-of-domain source clock`
	r := compileLogRegex(pattern, false)

	log := "" +
		"phc2sys[34350.173]: [phc2sys.2.config:6] selecting ens2f1 as out-of-domain source clock\n" +
		"phc2sys[34360.000]: [phc2sys.2.config:6] CLOCK_REALTIME phc offset 1 s2 freq -100 delay 500\n" +
		"phc2sys[34433.188]: [phc2sys.2.config:6] selecting ens4f2 as out-of-domain source clock\n"

	matches := r.FindAllStringSubmatch(log, -1)
	if len(matches) < 2 {
		t.Fatalf("expected >= 2 matches, got %d: %#v", len(matches), matches)
	}
	got := matches[len(matches)-1][1]
	if got != "ens4f2" {
		t.Fatalf("latest selected iface = %q, want ens4f2 (stale first match would be ens2f1)", got)
	}
}
