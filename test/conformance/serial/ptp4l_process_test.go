package test

import (
	"reflect"
	"testing"
)

func TestFirstTS2PhcInterfaceSkipsNonInterfaceSections(t *testing.T) {
	config := `[nmea]
ts2phc.master 1
[global]
use_syslog 0
[ens1f0]
ts2phc.extts_polarity rising
`
	if iface := firstTS2PhcInterface(config); iface != "ens1f0" {
		t.Fatalf("firstTS2PhcInterface() = %q, want %q", iface, "ens1f0")
	}
}

func TestPtp4lPIDsFromPS(t *testing.T) {
	const configFile = "/var/run/ptp4l.0.config"
	psOutput := `
  101 /usr/sbin/ptp4l -f /var/run/ptp4l.0.config -m
  102 /usr/sbin/ptp4l -f /var/run/ptp4l.1.config -m
  103 /bin/sh -c /usr/sbin/ptp4l -f /var/run/ptp4l.0.config -m
  104 /usr/sbin/ptp4l --config /var/run/ptp4l.0.config
  105 phc2sys -w -m -r -a 1
  not-a-ps-row
`

	if pids := ptp4lPIDsFromPS(psOutput, configFile); !reflect.DeepEqual(pids, []int{101}) {
		t.Fatalf("ptp4lPIDsFromPS() = %v, want [101]", pids)
	}
	// An empty config selects every live ptp4l process regardless of arguments;
	// the shell wrapper and malformed row must still be skipped.
	if pids := ptp4lPIDsFromPS(psOutput, ""); !reflect.DeepEqual(pids, []int{101, 102, 104}) {
		t.Fatalf("ptp4lPIDsFromPS() with empty config = %v, want [101 102 104]", pids)
	}
}
