package v2alpha1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestWPCHardwareConfigValidation(t *testing.T) {
	// Load the test data file
	testFile := filepath.Join("testdata", "wpc-hwconfig.yaml")
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read test file %s: %v", testFile, err)
	}

	// Unmarshal into HardwareConfig
	var hwConfig HardwareConfig
	if err := yaml.Unmarshal(data, &hwConfig); err != nil {
		t.Fatalf("Failed to unmarshal YAML: %v", err)
	}

	// Validate the ClockChain
	if hwConfig.Spec.Profile.ClockChain == nil {
		t.Fatal("ClockChain is nil")
	}

	// Run validation
	if err := hwConfig.Spec.Profile.ClockChain.Validate(); err != nil {
		t.Errorf("Validation failed: %v", err)
	}

	// Additional checks
	t.Run("Structure validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if len(cc.Structure) == 0 {
			t.Error("Structure should contain at least one subsystem")
		}

		// Check subsystem names are defined
		for i, subsystem := range cc.Structure {
			if subsystem.Name == "" {
				t.Errorf("Subsystem[%d] name is empty", i)
			}
			if subsystem.DPLL.NetworkInterface == "" {
				t.Errorf("Subsystem[%d] networkInterface is empty", i)
			}
		}
	})

	t.Run("Sources validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if cc.Behavior == nil {
			t.Skip("No behavior section defined")
		}

		// Check that all sources reference valid subsystems
		subsystemNames := make(map[string]bool)
		for _, subsystem := range cc.Structure {
			subsystemNames[subsystem.Name] = true
		}

		for _, source := range cc.Behavior.Sources {
			if source.Subsystem == "" {
				t.Errorf("Source %s has empty subsystem", source.Name)
			}
			if !subsystemNames[source.Subsystem] {
				t.Errorf("Source %s references non-existent subsystem: %s", source.Name, source.Subsystem)
			}
		}
	})

	t.Run("DesiredStates validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if cc.Behavior == nil {
			t.Skip("No behavior section defined")
		}

		// Check that all desired states reference valid subsystems
		subsystemNames := make(map[string]bool)
		for _, subsystem := range cc.Structure {
			subsystemNames[subsystem.Name] = true
		}

		for _, condition := range cc.Behavior.Conditions {
			for _, desiredState := range condition.DesiredStates {
				if desiredState.DPLL != nil && desiredState.DPLL.Subsystem != "" {
					if !subsystemNames[desiredState.DPLL.Subsystem] {
						t.Errorf("Condition %s references non-existent subsystem: %s",
							condition.Name, desiredState.DPLL.Subsystem)
					}
				}
			}
		}
	})

	t.Run("ESync references validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if cc.CommonDefinitions == nil {
			t.Skip("No common definitions section")
		}

		// Collect eSync names
		esyncNames := make(map[string]bool)
		for _, esync := range cc.CommonDefinitions.ESyncDefinitions {
			esyncNames[esync.Name] = true
		}

		// Check that all eSync references are valid
		for _, subsystem := range cc.Structure {
			allPins := make(map[string]PinConfig)
			for label, config := range subsystem.DPLL.PhaseInputs {
				allPins[label] = config
			}
			for label, config := range subsystem.DPLL.PhaseOutputs {
				allPins[label] = config
			}
			for label, config := range subsystem.DPLL.FrequencyInputs {
				allPins[label] = config
			}
			for label, config := range subsystem.DPLL.FrequencyOutputs {
				allPins[label] = config
			}

			for label, config := range allPins {
				if config.ESyncConfigName != "" && !esyncNames[config.ESyncConfigName] {
					t.Errorf("Pin %s in subsystem %s references non-existent eSync: %s",
						label, subsystem.Name, config.ESyncConfigName)
				}
			}
		}
	})
}

func TestSourceTypeValidation(t *testing.T) {
	tests := []struct {
		name    string
		source  *SourceConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty structure requires subsystem",
			source:  &SourceConfig{},
			wantErr: true,
			errMsg:  "subsystem must be specified",
		},
		{
			name: "ptpTimeReceiver without receivers",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypePTP,
			},
			wantErr: true,
			errMsg:  "ptpTimeReceivers must be specified",
		},
		{
			name: "ptpTimeReceiver with invalid receiver name",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypePTP,
				PTPTimeReceivers: []string{
					"eth0",
					"%@$bad**",
					"eth1",
				},
			},
			wantErr: true,
			errMsg:  "invalid PTP time receiver format",
		},
		{
			name: "valid ptpTimeReceiver",
			source: &SourceConfig{
				Subsystem:        "subsystem",
				SourceType:       SourceTypePTP,
				PTPTimeReceivers: []string{"eth0", "eth1"},
			},
			wantErr: false,
		},
		{
			name: "gnss without gnssConfig",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
			},
			wantErr: true,
			errMsg:  "gnssConfig must be specified when sourceType is gnss",
		},
		{
			name: "valid gnss with gnssConfig",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{
						AntennaVoltage: true,
						Constellations: []ConstellationID{"GPS", "Galileo"},
						SurveyIn: GNSSSurveyParameters{
							ObservationTime: 300,
							Accuracy:        5,
						},
					},
					Match: &GNSSMatcher{
						TTYDevice: "/dev/gnss0",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with USB device matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						USBDevice: &USBDevice{Vendor: "1546", Product: "01a9"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with USB device path matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						USBDevice: &USBDevice{Vendor: "1546", Product: "01a9", Path: "2-1.4"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with ACPI serial device matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						SerialDevice: &SerialDevice{
							ACPI: &ACPIDevice{HID: "INTC10EE", UID: "00"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with deprecated Ethernet interface matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						EthernetInterface: "eno1",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with Ethernet device PCI matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						EthernetDevice: &EthernetDevice{PCIAddress: "0000:86:00.0"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with Intel E810 PCI vendor and device ID matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						EthernetDevice: &EthernetDevice{VendorID: "8086", DeviceID: "159B"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with Ethernet device permanent MAC matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						EthernetDevice: &EthernetDevice{PermanentMACAddress: "00:11:22:aa:bb:cc"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with Ethernet device slot matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						EthernetDevice: &EthernetDevice{Slot: "2"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss with Ethernet device name matcher",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{
						EthernetDevice: &EthernetDevice{Name: "ens2f0"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid gnss without match (autodetect)",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS"}},
				},
			},
			wantErr: false,
		},
		{
			name: "gnss with both ttyDevice and ethernetInterface",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init:  GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{TTYDevice: "/dev/gnss0", EthernetInterface: "eth0"},
				},
			},
			wantErr: true,
			errMsg:  "exactly one of ttyDevice, serialDevice, ethernetInterface, ethernetDevice, or usbDevice must be provided",
		},
		{
			name: "gnss with empty match",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init:  GNSSInit{Constellations: []ConstellationID{"GPS"}},
					Match: &GNSSMatcher{},
				},
			},
			wantErr: true,
			errMsg:  "exactly one of ttyDevice, serialDevice, ethernetInterface, ethernetDevice, or usbDevice must be provided",
		},
		{
			name: "gnss with empty constellations",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{}},
				},
			},
			wantErr: true,
			errMsg:  "at least one constellation must be specified",
		},
		{
			name: "gnss with invalid constellation",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeGNSS,
				BoardLabel: "GNSS",
				GNSSConfig: &GNSSConfig{
					Init: GNSSInit{Constellations: []ConstellationID{"GPS", "InvalidConstellation"}},
				},
			},
			wantErr: true,
			errMsg:  "invalid constellation",
		},
		{
			name: "valid dpllPhaseLocked source",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeDPLL,
				BoardLabel: "DPLL",
			},
			wantErr: false,
		},
		{
			name: "unknown sourceType falls through default",
			source: &SourceConfig{
				Subsystem:  "subsystem",
				SourceType: SourceTypeID("future-type"),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.source.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGNSSConfigValidateDefaultsOmittedConstellations(t *testing.T) {
	var config GNSSConfig
	assert.NoError(t, yaml.Unmarshal([]byte("init: {}"), &config))
	assert.NoError(t, config.Validate())
	assert.Equal(t, []ConstellationID{ConstellationGPS}, config.Init.Constellations)

	var explicitlyEmpty GNSSConfig
	assert.NoError(t, yaml.Unmarshal([]byte("init:\n  constellations: []"), &explicitlyEmpty))
	assert.NotNil(t, explicitlyEmpty.Init.Constellations)
	assert.ErrorContains(t, explicitlyEmpty.Validate(), "at least one constellation must be specified")
}

func TestGNSSMatcherValidateRejectsIncompleteNestedSelectors(t *testing.T) {
	tests := []struct {
		name    string
		matcher *GNSSMatcher
		errMsg  string
	}{
		{
			name:    "empty serial device",
			matcher: &GNSSMatcher{SerialDevice: &SerialDevice{}},
			errMsg:  "serialDevice must specify an ACPI hardware ID",
		},
		{
			name: "serial device without ACPI hardware ID",
			matcher: &GNSSMatcher{SerialDevice: &SerialDevice{
				ACPI: &ACPIDevice{},
			}},
			errMsg: "serialDevice must specify an ACPI hardware ID",
		},
		{
			name:    "empty ethernet device",
			matcher: &GNSSMatcher{EthernetDevice: &EthernetDevice{}},
			errMsg:  "ethernetDevice must specify at least one selection criterion",
		},
		{
			name:    "USB device without vendor ID",
			matcher: &GNSSMatcher{USBDevice: &USBDevice{Product: "01a9"}},
			errMsg:  "usbDevice must specify both vendor and product IDs",
		},
		{
			name:    "USB device without product ID",
			matcher: &GNSSMatcher{USBDevice: &USBDevice{Vendor: "1546"}},
			errMsg:  "usbDevice must specify both vendor and product IDs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.matcher.Validate()
			assert.ErrorContains(t, err, tt.errMsg)
		})
	}
}

func TestClockChainValidation_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		config  *ClockChain
		wantErr bool
		errMsg  string
	}{
		{
			name: "empty structure",
			config: &ClockChain{
				Structure: []Subsystem{},
			},
			wantErr: true,
			errMsg:  "structure must contain at least one subsystem",
		},
		{
			name: "duplicate subsystem names",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "test", DPLL: DPLL{NetworkInterface: "eth0"}},
					{Name: "test", DPLL: DPLL{NetworkInterface: "eth1"}},
				},
			},
			wantErr: true,
			errMsg:  "duplicate subsystem name",
		},
		{
			name: "source referencing non-existent subsystem",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "subsys1", DPLL: DPLL{NetworkInterface: "eth0"}},
				},
				Behavior: &Behavior{
					Sources: []SourceConfig{
						{
							Name:       "source1",
							Subsystem:  "nonexistent",
							SourceType: SourceTypeDPLL,
							BoardLabel: "DPLL",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "references non-existent subsystem",
		},
		{
			name: "desired state referencing non-existent subsystem",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "subsys1", DPLL: DPLL{NetworkInterface: "eth0"}},
				},
				Behavior: &Behavior{
					Sources: []SourceConfig{
						{
							Name:       "source1",
							Subsystem:  "subsys1",
							SourceType: SourceTypeDPLL,
							BoardLabel: "DPLL",
						},
					},
					Conditions: []Condition{
						{
							Name: "test condition",
							Triggers: []SourceState{
								{SourceName: "source1", ConditionType: "locked"},
							},
							DesiredStates: []DesiredState{
								{
									DPLL: &DPLLDesiredState{
										Subsystem:  "nonexistent",
										BoardLabel: "test",
									},
								},
							},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "references non-existent subsystem",
		},
		{
			name: "valid minimal config",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "subsys1", DPLL: DPLL{NetworkInterface: "eth0"}},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error containing %q, got nil", tt.errMsg)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}
