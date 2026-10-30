package intel

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/k8snetworkplumbingwg/linuxptp-daemon/pkg/ublox"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/stretchr/testify/assert"
)

func TestFindLeadingInterface(t *testing.T) {
	tests := []struct {
		name          string
		ts2phcConf    string
		expectedIface string
		expectedFound bool
	}{
		{
			name: "T-GM single interface, no nmea_serialport",
			ts2phcConf: `[nmea]
ts2phc.master 1
[global]
use_syslog 0
verbose 1
logging_level 7
ts2phc.pulsewidth 100000000
[ens7f0]
ts2phc.extts_polarity rising
ts2phc.extts_correction 0`,
			expectedIface: "ens7f0",
			expectedFound: true,
		},
		{
			name: "T-GM with nmea_serialport already set",
			ts2phcConf: `[nmea]
ts2phc.master 1
[global]
use_syslog 0
verbose 1
ts2phc.nmea_serialport /dev/gnss1
[ens7f0]
ts2phc.extts_polarity rising`,
			expectedIface: "",
			expectedFound: false,
		},
		{
			name: "T-BC config without [nmea] section",
			ts2phcConf: `[global]
use_syslog 0
verbose 1
logging_level 7
ts2phc.pulsewidth 100000000
[ens4f0]
ts2phc.extts_polarity rising
ts2phc.master 0
[ens8f0]
ts2phc.extts_polarity rising
ts2phc.master 0`,
			expectedIface: "",
			expectedFound: false,
		},
		{
			name: "multi-interface T-GM: one leading, two slaves",
			ts2phcConf: `[nmea]
ts2phc.master 1
[global]
use_syslog 0
verbose 1
ts2phc.pulsewidth 100000000
[ens7f0]
ts2phc.extts_polarity rising
ts2phc.extts_correction 0
[ens4f0]
ts2phc.extts_polarity rising
ts2phc.master 0
[ens8f0]
ts2phc.extts_polarity rising
ts2phc.master 0`,
			expectedIface: "ens7f0",
			expectedFound: true,
		},
		{
			name: "multi-interface T-GM: all without ts2phc.master 0 warns, returns first",
			ts2phcConf: `[nmea]
ts2phc.master 1
[global]
use_syslog 0
[ens7f0]
ts2phc.extts_polarity rising
[ens8f0]
ts2phc.extts_polarity rising`,
			expectedIface: "ens7f0",
			expectedFound: true,
		},
		{
			name: "T-GM with [nmea] but no interface sections",
			ts2phcConf: `[nmea]
ts2phc.master 1
[global]
use_syslog 0
verbose 1`,
			expectedIface: "",
			expectedFound: false,
		},
		{
			name:          "empty config",
			ts2phcConf:    "",
			expectedIface: "",
			expectedFound: false,
		},
		{
			name: "[nmea] without ts2phc.master still triggers auto-detection",
			ts2phcConf: `[nmea]
[global]
use_syslog 0
[ens7f0]
ts2phc.extts_polarity rising`,
			expectedIface: "ens7f0",
			expectedFound: true,
		},
		{
			name: "comments and blank lines are ignored",
			ts2phcConf: `# This is a comment
[nmea]
ts2phc.master 1

[global]
# serial port not set
use_syslog 0

[ens5f0]
ts2phc.extts_polarity rising`,
			expectedIface: "ens5f0",
			expectedFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iface, found := findLeadingInterface(tt.ts2phcConf)
			assert.Equal(t, tt.expectedFound, found, "found mismatch")
			assert.Equal(t, tt.expectedIface, iface, "interface mismatch")
		})
	}
}

func setupUbloxReadDirMock(entries map[string][]os.DirEntry, errs map[string]error) func() {
	orig := ublox.ReadDir
	ublox.ReadDir = func(name string) ([]os.DirEntry, error) {
		if errs != nil {
			if err, ok := errs[name]; ok {
				return nil, err
			}
		}
		if entries != nil {
			if e, ok := entries[name]; ok {
				return e, nil
			}
		}
		return nil, errors.New("not found")
	}
	return func() { ublox.ReadDir = orig }
}

func TestGNSSDeviceFromInterface(t *testing.T) {
	t.Run("single GNSS device found", func(t *testing.T) {
		restore := setupUbloxReadDirMock(
			map[string][]os.DirEntry{
				fmt.Sprintf(ublox.GNSSDeviceSysfsTemplate, "ens7f0"): {MockDirEntry{name: "gnss0"}},
			}, nil)
		defer restore()

		result, err := ublox.GNSSDeviceFromInterface("ens7f0")
		assert.NoError(t, err)
		assert.Equal(t, "/dev/gnss0", result)
	})

	t.Run("multiple GNSS devices, returns first sorted", func(t *testing.T) {
		restore := setupUbloxReadDirMock(
			map[string][]os.DirEntry{
				fmt.Sprintf(ublox.GNSSDeviceSysfsTemplate, "ens7f0"): {
					MockDirEntry{name: "gnss1"},
					MockDirEntry{name: "gnss0"},
				},
			}, nil)
		defer restore()

		result, err := ublox.GNSSDeviceFromInterface("ens7f0")
		assert.NoError(t, err)
		assert.Equal(t, "/dev/gnss0", result)
	})

	t.Run("sysfs directory does not exist", func(t *testing.T) {
		restore := setupUbloxReadDirMock(nil,
			map[string]error{
				fmt.Sprintf(ublox.GNSSDeviceSysfsTemplate, "ens7f0"): errors.New("no such file or directory"),
			})
		defer restore()

		_, err := ublox.GNSSDeviceFromInterface("ens7f0")
		assert.Error(t, err)
	})

	t.Run("sysfs directory is empty", func(t *testing.T) {
		restore := setupUbloxReadDirMock(
			map[string][]os.DirEntry{
				fmt.Sprintf(ublox.GNSSDeviceSysfsTemplate, "ens7f0"): {},
			}, nil)
		defer restore()

		_, err := ublox.GNSSDeviceFromInterface("ens7f0")
		assert.Error(t, err)
	})
}

func TestAutoDetectGNSSSerialPort(t *testing.T) {
	ts2phcConf := `[nmea]
ts2phc.master 1
[global]
use_syslog 0
verbose 1
[ens7f0]
ts2phc.extts_polarity rising`

	t.Run("patches config when GNSS device found", func(t *testing.T) {
		restore := setupUbloxReadDirMock(
			map[string][]os.DirEntry{
				fmt.Sprintf(ublox.GNSSDeviceSysfsTemplate, "ens7f0"): {MockDirEntry{name: "gnss0"}},
			}, nil)
		defer restore()

		conf := ts2phcConf
		profile := &ptpv1.PtpProfile{Ts2PhcConf: &conf}
		autoDetectGNSSSerialPort(profile)

		assert.Contains(t, *profile.Ts2PhcConf, "ts2phc.nmea_serialport /dev/gnss0")
		lines := strings.Split(*profile.Ts2PhcConf, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "[global]" {
				assert.Equal(t, "ts2phc.nmea_serialport /dev/gnss0", lines[i+1])
				break
			}
		}
	})

	t.Run("skips when nmea_serialport already set", func(t *testing.T) {
		confWithPort := `[nmea]
ts2phc.master 1
[global]
ts2phc.nmea_serialport /dev/gnss1
[ens7f0]
ts2phc.extts_polarity rising`
		conf := confWithPort
		profile := &ptpv1.PtpProfile{Ts2PhcConf: &conf}
		autoDetectGNSSSerialPort(profile)

		assert.Equal(t, confWithPort, *profile.Ts2PhcConf, "config should be unchanged")
	})

	t.Run("skips when Ts2PhcConf is nil", func(t *testing.T) {
		profile := &ptpv1.PtpProfile{Ts2PhcConf: nil}
		autoDetectGNSSSerialPort(profile)
		assert.Nil(t, profile.Ts2PhcConf)
	})

	t.Run("skips when sysfs lookup fails", func(t *testing.T) {
		restore := setupUbloxReadDirMock(nil,
			map[string]error{
				fmt.Sprintf(ublox.GNSSDeviceSysfsTemplate, "ens7f0"): errors.New("no such file or directory"),
			})
		defer restore()

		conf := ts2phcConf
		profile := &ptpv1.PtpProfile{Ts2PhcConf: &conf}
		autoDetectGNSSSerialPort(profile)

		assert.NotContains(t, *profile.Ts2PhcConf, "ts2phc.nmea_serialport")
	})
}
