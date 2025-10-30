package v2alpha1

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClockChain represents the root configuration structure for clock chain configuration.
// It defines the complete system including shared definitions, subsystem structure,
// and behavioral rules for source management.
type ClockChain struct {
	// CommonDefinitions includes definitions applied to multiple entities within the chain,
	// such as ESync configurations. They can be referenced in the relevant entities by name,
	// to avoid multiple copies.
	CommonDefinitions *CommonDefinitions `json:"commonDefinitions,omitempty" yaml:"commonDefinitions,omitempty"`

	// Structure defines the system structure as a list of atomic synchronization subsystems.
	// Must contain at least one subsystem.
	Structure []Subsystem `json:"structure" yaml:"structure"`

	// Behavior defines the system behavior based on synchronization sources, conditions and
	// associated actions. The conditions for the sources can be "default", "locked" or "lost".
	// The "default" condition initializes the hardware in each subsystem to allow the "Acquiring" state.
	// Bidirectional links between different subsystems can remain disconnected, as the desired link
	// direction is still unknown. The "locked" condition in one of the subsystems will configure
	// the bidirectional links to be disciplined by the locked subsystem. If more than one subsystem
	// is locked, the source with the smaller index will have higher priority. If the active source
	// is lost, and no other sources are "locked", the subsystem of the last active source may enter
	// holdover (subject to the daemon holdover decision). Other subsystems will be connected to
	// follow the DPLL in holdover.
	Behavior *Behavior `json:"behavior,omitempty" yaml:"behavior,omitempty"`
}

// CommonDefinitions contains shared definitions used across the configuration.
// This section includes definitions applied to multiple entities within the chain,
// such as ESync configurations. They can be referenced in the relevant entities by name,
// to avoid multiple copies.
type CommonDefinitions struct {
	// ESyncDefinitions is an array of named eSync configurations that can be referenced
	// by name from pin configurations throughout the system.
	ESyncDefinitions []ESyncDefinition `json:"eSyncDefinitions,omitempty" yaml:"eSyncDefinitions,omitempty"`

	// RefSyncDefinitions is an array of named reference sync configurations that can be
	// referenced by name from pin configurations throughout the system.
	// A ref-sync configuration typically ties a reference sync definition to a specific
	// related pin or board label.
	RefSyncDefinitions []RefSyncDefinition `json:"refSyncDefinitions,omitempty" yaml:"refSyncDefinitions,omitempty"`

	// ClockIdentifiers defines aliases for clock IDs to simplify configuration files
	ClockIdentifiers []ClockIdentifier `json:"clockIdentifiers,omitempty" yaml:"clockIdentifiers,omitempty"`
}

// ESyncDefinition defines a named eSync configuration that can be referenced by name from pin configurations.
type ESyncDefinition struct {
	// Name is a unique identifier for this eSync configuration
	Name string `json:"name" yaml:"name"`

	// ESyncConfig contains the eSync feature configuration parameters
	ESyncConfig ESyncConfig `json:"eSyncConfig" yaml:"eSyncConfig"`
}

// RefSyncDefinition defines a named reference sync configuration that can be
// referenced by name from pin configurations. It optionally relates to a specific
// pin board label.
type RefSyncDefinition struct {
	// Name is a unique identifier for this ref-sync configuration
	Name string `json:"name" yaml:"name"`

	// RelatedPinBoardLabel is an optional label for a related pin/board
	RelatedPinBoardLabel string `json:"relatedPinBoardLabel,omitempty" yaml:"relatedPinBoardLabel,omitempty"`
}

// ClockIdentifier defines a mapping between a human-friendly alias and a clock ID
type ClockIdentifier struct {
	// Alias is the short human-friendly identifier
	Alias string `json:"alias" yaml:"alias"`

	// ClockID is the actual clock ID (decimal or hex)
	ClockID string `json:"clockId" yaml:"clockId"`

	// ClockIDParsed is the parsed uint64 value of ClockID (populated during processing)
	ClockIDParsed uint64 `json:"-" yaml:"-"`

	// Description is optional context for the mapping
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// ESyncConfig represents eSync feature configuration.
// eSync provides a method to embed synchronization information in phase signals.
type ESyncConfig struct {
	// TransferFrequency is the configurable transfer frequency in Hz (required)
	TransferFrequency int64 `json:"transferFrequency" yaml:"transferFrequency"`

	// EmbeddedSyncFrequency is the embedded sync frequency in Hz. If omitted, set to 1Hz (1PPS). Default: 1
	EmbeddedSyncFrequency int64 `json:"embeddedSyncFrequency,omitempty" yaml:"embeddedSyncFrequency,omitempty"`

	// DutyCyclePct is the phase signal pulse duty cycle in percent. If omitted, set to 25%. Default: 25
	DutyCyclePct int64 `json:"dutyCyclePct,omitempty" yaml:"dutyCyclePct,omitempty"`
}

// Behavior defines the system behavior based on synchronization sources, conditions and associated actions.
// The conditions for the sources can be "default", "locked" or "lost".
type Behavior struct {
	// Sources of frequency, phase and time reference. Sources are identified by clock ID and pin board label,
	// tying them to the specific subsystem entity. Sources are characterized by type and can be referenced
	// system-wide by the name.
	Sources []SourceConfig `json:"sources,omitempty" yaml:"sources,omitempty"`

	// Conditions define behavior rules that evaluate sources and apply desired states when triggered.
	Conditions []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// SourceConfig defines a source of frequency, phase and time reference.
// Sources are identified by clock ID and pin board label, tying them to the specific subsystem entity.
// Sources are characterized by type and can be referenced system-wide by the name.
type SourceConfig struct {
	// Name is the source name that must be unique system-wide
	Name string `json:"name" yaml:"name"`

	// ClockID is the subsystem clock ID (decimal or hex format: "5799633565432596414" or "0xaabbccfffeddeeff")
	ClockID string `json:"clockId" yaml:"clockId"`

	// ClockIDParsed is the parsed uint64 value of ClockID (populated during processing)
	ClockIDParsed uint64 `json:"-" yaml:"-"`

	// SourceType identifies the source type. Valid values: "ptpTimeReceiver", "gnss"
	// If sourceType is ptpTimeReceiver, ptpTimeReceivers must be specified.
	// In all cases, boardLabel must be specified.
	SourceType string `json:"sourceType" yaml:"sourceType"`

	// BoardLabel and clock ID together unambiguously identify the subsystem and the DPLL pin receiving the source
	BoardLabel string `json:"boardLabel" yaml:"boardLabel"`

	// PTPTimeReceivers are ports configured to act as PTP time receivers
	// (required if the sourceType is set to 'ptpTimeReceiver')
	PTPTimeReceivers []string `json:"ptpTimeReceivers,omitempty" yaml:"ptpTimeReceivers,omitempty"`
}

// Condition defines a condition that evaluates an array of sources with implicit AND logic between them.
// The first condition in the array is the Triggering Condition, while all others are Supporting Conditions
// (that must be true for the desired states to be applied). For example, if two different subsystems have
// two different sources, there is still only one subsystem that will activate holdover if all other sources are lost.
type Condition struct {
	// Name is a human-readable condition name
	Name string `json:"name" yaml:"name"`

	// Sources is an array of source conditions that must ALL be true (implicit AND operation).
	// The first condition in the array is the Triggering Condition, while all others are Supporting Conditions.
	Sources []SourceState `json:"sources" yaml:"sources"`

	// DesiredStates is a list of pin and connector settings that together define the desired state.
	// The configurations are applied (in the order they are listed) when the condition is triggered.
	DesiredStates []DesiredState `json:"desiredStates" yaml:"desiredStates"`
}

// SourceState represents the state of a source in a condition evaluation.
type SourceState struct {
	// SourceName is the name of the source being evaluated
	SourceName string `json:"sourceName" yaml:"sourceName"`

	// ConditionType is the state condition of the source.
	// Valid values: "default", "locked", "lost"
	ConditionType string `json:"conditionType" yaml:"conditionType"`
}

// DesiredState defines the desired configuration that is applied when a condition is triggered.
// It supports either DPLL pin configurations OR sysFS-based Ethernet subsystem configurations.
type DesiredState struct {
	// DPLL defines DPLL pin configurations for the subsystem
	DPLL *DPLLDesiredState `json:"dpll,omitempty" yaml:"dpll,omitempty"`

	// SysFS defines a sysFS-based configuration for the Ethernet part of the subsystem.
	// This configuration is applied by writing a value to a sysFS path, often including interface names
	// that can be obtained from PTP sources' ptpTimeReceivers field.
	SysFS *SysFSDesiredState `json:"sysfs,omitempty" yaml:"sysfs,omitempty"`
}

// DPLLDesiredState defines the desired DPLL pin configuration for a subsystem.
type DPLLDesiredState struct {
	// ClockID is the subsystem clock ID (decimal or hex format)
	ClockID string `json:"clockId,omitempty" yaml:"clockId,omitempty"`

	// ClockIDParsed is the parsed uint64 value of ClockID (populated during processing)
	ClockIDParsed uint64 `json:"-" yaml:"-"`

	// BoardLabel and clock ID together unambiguously identify the subsystem and the DPLL pin,
	// together with an optional external connector, if defined.
	// If the pin is routed through an external connector, the connector settings (direction, frequency, etc.)
	// are derived from the pin configuration.
	BoardLabel string `json:"boardLabel,omitempty" yaml:"boardLabel,omitempty"`

	// EEC defines the desired state for the Enhanced Ethernet Clock pin
	EEC *PinState `json:"eec,omitempty" yaml:"eec,omitempty"`

	// PPS defines the desired state for the Pulse Per Second pin
	PPS *PinState `json:"pps,omitempty" yaml:"pps,omitempty"`
}

// SysFSDesiredState defines a sysFS-based configuration for Ethernet subsystems.
// It specifies a path in sysFS and the value to write to it, with support for interface name templating.
type SysFSDesiredState struct {
	// Path is the sysFS path where the value should be written.
	// It supports templating with interface names using the format: /sys/class/net/{interface}/...
	// The {interface} placeholder will be replaced with actual interface names from PTP sources.
	Path string `json:"path" yaml:"path"`

	// Value is the value to write to the sysFS path
	Value string `json:"value" yaml:"value"`

	// SourceName specifies which source to use for obtaining interface names.
	// If specified, the interface names will be taken from the PTP source's ptpTimeReceivers field.
	// If not specified, all available PTP sources will be considered for interface name resolution.
	SourceName string `json:"sourceName,omitempty" yaml:"sourceName,omitempty"`

	// Description provides optional context about this sysFS configuration
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// PinState represents the desired state of a pin.
// Input pins are controlled through priority.
// Output pins are controlled through state.
// Connectors, if referenced in pin config, are automatically set to the same state and frequency as the pin.
type PinState struct {
	// Priority is the pin input priority (for input pins only)
	Priority *int64 `json:"priority,omitempty" yaml:"priority,omitempty"`

	// State is the pin desired state. Valid values: "connected", "disconnected", "selectable"
	State string `json:"state,omitempty" yaml:"state,omitempty"`
}

// Subsystem defines an atomic synchronization subsystem of a single DPLL and one or more Ethernet subsystems linked together.
// Each subsystem represents a cohesive unit that can operate independently or in coordination with other subsystems.
type Subsystem struct {
	// Name is a human-readable identifier for this subsystem
	Name string `json:"name" yaml:"name"`

	// HardwareSpecificDefinitions is the hardware-specific identifier that handles default configurations
	HardwareSpecificDefinitions string `json:"hardwareSpecificDefinitions,omitempty" yaml:"hardwareSpecificDefinitions,omitempty"`

	// DPLL contains the DPLL configuration for this subsystem
	DPLL DPLL `json:"dpll" yaml:"dpll"`

	// Ethernet defines one or more Ethernet subsystems associated with this synchronization subsystem
	Ethernet []Ethernet `json:"ethernet" yaml:"ethernet"`
}

// HoldoverParameters defines the combination of the DPLL complex hardware parameters and the holdover specification threshold.
type HoldoverParameters struct {
	// MaxInSpecOffset is the holdover specification threshold in nanoseconds. Default - 100ns
	MaxInSpecOffset uint64 `json:"maxInSpecOffset,omitempty" yaml:"maxInSpecOffset,omitempty"`

	// LocalMaxHoldoverOffset is the maximum holdover offset in nanoseconds. Default - 1500ns
	LocalMaxHoldoverOffset uint64 `json:"localMaxHoldoverOffset,omitempty" yaml:"localMaxHoldoverOffset,omitempty"`

	// LocalHoldoverTimeout is the time the clock will stay in the holdover state before reaching the
	// LocalMaxHoldoverOffset (in seconds). Default - 14400s
	LocalHoldoverTimeout uint64 `json:"localHoldoverTimeout,omitempty" yaml:"localHoldoverTimeout,omitempty"`
}

// DPLL represents generic DPLL configuration within a synchronization subsystem.
// Configuration of this section will result in DPLL device configurations through the Netlink driver.
type DPLL struct {
	// ClockID is an optional clock ID. If omitted, the hardware must support clock ID discovery.
	// Format: decimal or hex ("5799633565432596414" or "0xaabbccfffeddeeff")
	ClockID string `json:"clockId,omitempty" yaml:"clockId,omitempty"`

	// HoldoverParameters defines the combination of the DPLL complex hardware parameters and the holdover specification threshold.
	HoldoverParameters *HoldoverParameters `json:"holdoverParameters,omitempty" yaml:"holdoverParameters,omitempty"`

	// ClockIDParsed is the parsed uint64 value of ClockID (populated during processing)
	ClockIDParsed uint64 `json:"-" yaml:"-"`

	// PhaseInputs are phase reference input pins, keyed by board label
	PhaseInputs map[string]PinConfig `json:"phaseInputs,omitempty" yaml:"phaseInputs,omitempty"`

	// PhaseOutputs are optional phase output pins, keyed by board label
	PhaseOutputs map[string]PinConfig `json:"phaseOutputs,omitempty" yaml:"phaseOutputs,omitempty"`

	// FrequencyInputs are optional frequency reference inputs, keyed by board label
	FrequencyInputs map[string]PinConfig `json:"frequencyInputs,omitempty" yaml:"frequencyInputs,omitempty"`

	// FrequencyOutputs are optional frequency outputs for other devices or measurements, keyed by board label
	FrequencyOutputs map[string]PinConfig `json:"frequencyOutputs,omitempty" yaml:"frequencyOutputs,omitempty"`
}

// Ethernet defines the Ethernet subsystem and unambiguously identifies Ethernet ports belonging to it.
// This may be required to support various port naming schemes.
type Ethernet struct {
	// Ports is a list of Ethernet port names associated with this Ethernet subsystem.
	// The default port, or the port used to address the network adapter configuration through sysfs, is listed first.
	Ports []string `json:"ports" yaml:"ports"`
}

// PinConfig represents pin configuration for DPLL phase or frequency signals in a dictionary format
// (boardLabel is the key). The frequency and esyncConfigName properties are mutually exclusive.
type PinConfig struct {
	// Connector is an optional identifier on the device (e.g., "SMA1", "U.FL2").
	// Defines the physical connector this pin is statically or dynamically routed to.
	// Used by the hardware plugin software to configure connector logic, if present.
	Connector string `json:"connector,omitempty" yaml:"connector,omitempty"`

	// PhaseAdjustment is optional phase adjustment in picoseconds
	PhaseAdjustment *PhaseAdjustment `json:"phaseAdjustment,omitempty" yaml:"phaseAdjustment,omitempty"`

	// Frequency is the frequency value in Hz (for frequency pins) or phase reference frequency
	// (for phase pins, defaults to 1 PPS). Mutually exclusive with esyncConfigName.
	Frequency *int64 `json:"frequency,omitempty" yaml:"frequency,omitempty"`

	// ESyncConfigName is an optional eSync configuration name (defined in CommonDefinitions).
	// Mutually exclusive with frequency.
	ESyncConfigName string `json:"eSyncConfigName,omitempty" yaml:"eSyncConfigName,omitempty"`

	// Description is an optional description for this pin configuration
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// ReferenceSync applies to frequency pins that can be paired to a phase pin by board label
	// The value should match a phase pin label (from phaseInputs) within the same subsystem
	ReferenceSync string `json:"referenceSync,omitempty" yaml:"referenceSync,omitempty"`
}

// PhaseAdjustment represents phase adjustment that must be applied to the input or the output pin
// to compensate for phase delays from routing, logic and cables.
// Usually internal delay is applied to output pins, and the sum of internal and external delays is applied to input pins.
// Sometimes the above adjustment is not possible (e.g. if the input side is not programmable). In this case external delays
// will be summed with the internal delays and applied to the output side.
type PhaseAdjustment struct {
	// Internal is the internal phase adjustment in picoseconds (required).
	// Usually compensates for the board hardware delays and should not be changed by the user.
	Internal int `json:"internal" yaml:"internal"`

	// External is the external phase adjustment in picoseconds.
	// Compensates for delays introduced by external cables.
	External *int `json:"external,omitempty" yaml:"external,omitempty"`

	// Description is an optional description for this phase adjustment
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Custom validation functions

// ValidateClockID validates clock ID format (decimal or hex)
func ValidateClockID(clockID string) error {
	clockIdPattern := regexp.MustCompile(`^(?:0[xX][0-9a-fA-F]+|[0-9]+)$`)
	if clockIdPattern.MatchString(clockID) {
		return nil
	}
	return fmt.Errorf("invalid clock ID format: %s (must be decimal or hex)", clockID)
}

// ParseClockID parses a clock ID string (decimal or hexadecimal) to uint64
func ParseClockID(clockID string) (uint64, error) {
	if clockID == "" {
		return 0, fmt.Errorf("empty clock ID")
	}

	// Validate format first
	if err := ValidateClockID(clockID); err != nil {
		return 0, err
	}

	// Parse hexadecimal format (0x or 0X prefix)
	if strings.HasPrefix(strings.ToLower(clockID), "0x") {
		value, err := strconv.ParseUint(clockID, 0, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse hexadecimal clock ID '%s': %w", clockID, err)
		}
		return value, nil
	}

	// Parse decimal format
	value, err := strconv.ParseUint(clockID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse decimal clock ID '%s': %w", clockID, err)
	}
	return value, nil
}

// ParseClockIDSafe parses a clock ID string and returns both the uint64 value and string representation
// This is useful when you need both formats for different operations
func ParseClockIDSafe(clockID string) (uint64, string, error) {
	if clockID == "" {
		return 0, "", fmt.Errorf("empty clock ID")
	}

	// Validate format first
	if err := ValidateClockID(clockID); err != nil {
		return 0, "", err
	}

	// Parse to uint64
	value, err := ParseClockID(clockID)
	if err != nil {
		return 0, "", err
	}

	return value, clockID, nil
}

// ValidateAlphanumDash validates alphanumeric characters with dashes and underscores
func ValidateAlphanumDash(value string) error {
	pattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !pattern.MatchString(value) {
		return fmt.Errorf("value must contain only alphanumeric characters, dashes, and underscores: %s", value)
	}
	return nil
}

// BuildClockAliasMap constructs a mapping from alias to clock ID and validates entries
func (cc *ClockChain) BuildClockAliasMap() (map[string]string, error) {
	aliasToClock := make(map[string]string)
	if cc.CommonDefinitions == nil {
		return aliasToClock, nil
	}

	for _, ident := range cc.CommonDefinitions.ClockIdentifiers {
		if ident.Alias == "" {
			return nil, fmt.Errorf("clockIdentifiers: alias must not be empty")
		}
		if err := ValidateAlphanumDash(ident.Alias); err != nil {
			return nil, fmt.Errorf("clockIdentifiers: invalid alias '%s': %w", ident.Alias, err)
		}
		if err := ValidateClockID(ident.ClockID); err != nil {
			return nil, fmt.Errorf("clockIdentifiers: alias '%s' has invalid clockId: %w", ident.Alias, err)
		}
		if _, exists := aliasToClock[ident.Alias]; exists {
			return nil, fmt.Errorf("clockIdentifiers: duplicate alias '%s'", ident.Alias)
		}
		aliasToClock[ident.Alias] = ident.ClockID
	}

	return aliasToClock, nil
}

// resolveClockIDValue returns a validated clock ID string. If the input is not a valid
// clock ID, it will try to resolve it as an alias using aliasToClock map.
func resolveClockIDValue(value string, aliasToClock map[string]string) (string, error) {
	if value == "" {
		return value, nil
	}
	if err := ValidateClockID(value); err == nil {
		return value, nil
	}
	if resolved, ok := aliasToClock[value]; ok {
		return resolved, nil
	}
	return "", fmt.Errorf("value '%s' is neither a valid clock ID nor a known alias", value)
}

// ResolveClockAliases walks the configuration and replaces any clock alias usages
// with their corresponding clock IDs. This should be called early in the program
// lifecycle before merges and validation.
func (cc *ClockChain) ResolveClockAliases() error {
	aliasToClock, err := cc.BuildClockAliasMap()
	if err != nil {
		return err
	}

	// Parse clock identifiers to uint64 values
	for i := range cc.CommonDefinitions.ClockIdentifiers {
		if cc.CommonDefinitions.ClockIdentifiers[i].ClockID != "" {
			parsed, parseErr := ParseClockID(cc.CommonDefinitions.ClockIdentifiers[i].ClockID)
			if parseErr != nil {
				return fmt.Errorf("commonDefinitions.clockIdentifiers[%d].clockId: %w", i, parseErr)
			}
			cc.CommonDefinitions.ClockIdentifiers[i].ClockIDParsed = parsed
		}
	}

	// Resolve DPLL clock IDs
	for si := range cc.Structure {
		if cc.Structure[si].DPLL.ClockID != "" {
			resolved, resolveErr := resolveClockIDValue(cc.Structure[si].DPLL.ClockID, aliasToClock)
			if resolveErr != nil {
				return fmt.Errorf("structure[%d] DPLL.clockId: %w", si, resolveErr)
			}
			cc.Structure[si].DPLL.ClockID = resolved

			// Parse the resolved clock ID to uint64
			parsed, structParseErr := ParseClockID(resolved)
			if structParseErr != nil {
				return fmt.Errorf("structure[%d] DPLL.clockId parse: %w", si, structParseErr)
			}
			cc.Structure[si].DPLL.ClockIDParsed = parsed
		}
	}

	if cc.Behavior == nil {
		return nil
	}

	// Resolve source clock IDs
	for i := range cc.Behavior.Sources {
		resolved, sourceErr := resolveClockIDValue(cc.Behavior.Sources[i].ClockID, aliasToClock)
		if sourceErr != nil {
			return fmt.Errorf("behavior.sources[%d].clockId: %w", i, sourceErr)
		}
		cc.Behavior.Sources[i].ClockID = resolved

		// Parse the resolved clock ID to uint64
		parsed, behaviorParseErr := ParseClockID(resolved)
		if behaviorParseErr != nil {
			return fmt.Errorf("behavior.sources[%d].clockId parse: %w", i, behaviorParseErr)
		}
		cc.Behavior.Sources[i].ClockIDParsed = parsed
	}

	// Resolve desired state clock IDs
	for ci := range cc.Behavior.Conditions {
		for di := range cc.Behavior.Conditions[ci].DesiredStates {
			desiredState := &cc.Behavior.Conditions[ci].DesiredStates[di]

			// Resolve DPLL clock ID if present
			if desiredState.DPLL != nil && desiredState.DPLL.ClockID != "" {
				resolved, desiredErr := resolveClockIDValue(desiredState.DPLL.ClockID, aliasToClock)
				if desiredErr != nil {
					return fmt.Errorf("behavior.conditions[%d].desiredStates[%d].dpll.clockId: %w", ci, di, desiredErr)
				}
				desiredState.DPLL.ClockID = resolved

				// Parse the resolved clock ID to uint64
				parsed, desiredParseErr := ParseClockID(resolved)
				if desiredParseErr != nil {
					return fmt.Errorf("behavior.conditions[%d].desiredStates[%d].dpll.clockId parse: %w", ci, di, desiredParseErr)
				}
				desiredState.DPLL.ClockIDParsed = parsed
			}
		}
	}
	return nil
}

// Validate ensures frequency and esyncConfigName are mutually exclusive
func (pc *PinConfig) Validate() error {
	if pc.Frequency != nil && pc.ESyncConfigName != "" {
		return fmt.Errorf("frequency and esyncConfigName are mutually exclusive")
	}

	if pc.Connector != "" {
		if err := ValidateAlphanumDash(pc.Connector); err != nil {
			return fmt.Errorf("invalid connector format: %w", err)
		}
	}

	return nil
}

// Validate ensures PTPTimeReceivers is specified when sourceType is ptpTimeReceiver
func (sc *SourceConfig) Validate() error {
	if err := ValidateClockID(sc.ClockID); err != nil {
		return fmt.Errorf("invalid clock ID: %w", err)
	}

	if sc.SourceType == "ptpTimeReceiver" && len(sc.PTPTimeReceivers) == 0 {
		return fmt.Errorf("ptpTimeReceivers must be specified when sourceType is ptpTimeReceiver")
	}

	for _, receiver := range sc.PTPTimeReceivers {
		if err := ValidateAlphanumDash(receiver); err != nil {
			return fmt.Errorf("invalid PTP time receiver format: %w", err)
		}
	}

	return nil
}

// Validate performs comprehensive validation of the entire configuration
func (cc *ClockChain) Validate() error {
	// Validate that structure has at least one subsystem
	if len(cc.Structure) == 0 {
		return fmt.Errorf("structure must contain at least one subsystem")
	}

	// Collect all clock IDs and source names for cross-reference validation
	clockIDs := make(map[string]bool)
	sourceNames := make(map[string]bool)
	esyncNames := make(map[string]bool)
	refsyncNames := make(map[string]bool)

	// Collect eSync definition names
	if cc.CommonDefinitions != nil {
		for _, esync := range cc.CommonDefinitions.ESyncDefinitions {
			if esync.Name == "" {
				return fmt.Errorf("eSync definition name must not be empty")
			}
			if esyncNames[esync.Name] {
				return fmt.Errorf("duplicate eSync definition name: %s", esync.Name)
			}
			esyncNames[esync.Name] = true
		}
		for _, refsync := range cc.CommonDefinitions.RefSyncDefinitions {
			if refsync.Name == "" {
				return fmt.Errorf("refSync definition name must not be empty")
			}
			if refsyncNames[refsync.Name] {
				return fmt.Errorf("duplicate refSync definition name: %s", refsync.Name)
			}
			refsyncNames[refsync.Name] = true
		}
	}

	// Validate subsystems and collect clock IDs
	for _, subsystem := range cc.Structure {
		if subsystem.DPLL.ClockID != "" {
			if err := ValidateClockID(subsystem.DPLL.ClockID); err != nil {
				return fmt.Errorf("invalid clock ID in subsystem %s: %w", subsystem.Name, err)
			}
			clockIDs[subsystem.DPLL.ClockID] = true
		}

		// Validate pin configs
		allPinConfigs := make(map[string]PinConfig)
		phaseLabels := make(map[string]struct{})
		freqInputLabels := make(map[string]struct{})
		freqOutputLabels := make(map[string]struct{})

		for label, config := range subsystem.DPLL.PhaseInputs {
			allPinConfigs[label] = config
			phaseLabels[label] = struct{}{}
		}
		for label, config := range subsystem.DPLL.PhaseOutputs {
			allPinConfigs[label] = config
			phaseLabels[label] = struct{}{}
		}
		for label, config := range subsystem.DPLL.FrequencyInputs {
			allPinConfigs[label] = config
			freqInputLabels[label] = struct{}{}
		}
		for label, config := range subsystem.DPLL.FrequencyOutputs {
			allPinConfigs[label] = config
			freqOutputLabels[label] = struct{}{}
		}

		for label, config := range allPinConfigs {
			if err := config.Validate(); err != nil {
				return fmt.Errorf("invalid pin config %s in subsystem %s: %w", label, subsystem.Name, err)
			}

			// Check if referenced eSync config exists
			if config.ESyncConfigName != "" && !esyncNames[config.ESyncConfigName] {
				return fmt.Errorf("referenced eSync config %s not found in subsystem %s, pin %s",
					config.ESyncConfigName, subsystem.Name, label)
			}

			// Validate referenceSync semantics: only allowed on frequency INPUT pins and must reference an existing phase pin
			if config.ReferenceSync != "" {
				if _, isFreqInput := freqInputLabels[label]; !isFreqInput {
					if _, isFreqOutput := freqOutputLabels[label]; isFreqOutput {
						return fmt.Errorf("referenceSync is not supported on frequency output pin %s in subsystem %s", label, subsystem.Name)
					}
					return fmt.Errorf("referenceSync specified on non-frequency-input pin %s in subsystem %s", label, subsystem.Name)
				}
				if _, exists := phaseLabels[config.ReferenceSync]; !exists {
					return fmt.Errorf("referenceSync '%s' not found among phase pins in subsystem %s (referenced by %s)",
						config.ReferenceSync, subsystem.Name, label)
				}
			}
		}
	}

	// Validate behavior section if present
	if cc.Behavior != nil {
		// Collect source names and validate sources
		for _, source := range cc.Behavior.Sources {
			if err := source.Validate(); err != nil {
				return fmt.Errorf("invalid source %s: %w", source.Name, err)
			}

			if sourceNames[source.Name] {
				return fmt.Errorf("duplicate source name: %s", source.Name)
			}
			sourceNames[source.Name] = true
		}

		// Validate conditions
		for _, condition := range cc.Behavior.Conditions {
			for _, sourceState := range condition.Sources {
				// Check if referenced source exists (unless it's a special default source)
				if sourceState.SourceName != "Default on profile (re)load" &&
					!sourceNames[sourceState.SourceName] {
					return fmt.Errorf("referenced source %s not found in condition %s",
						sourceState.SourceName, condition.Name)
				}
			}

			// Validate desired states
			for _, desiredState := range condition.DesiredStates {
				// Validate DPLL clock ID if present
				if desiredState.DPLL != nil && desiredState.DPLL.ClockID != "" {
					if err := ValidateClockID(desiredState.DPLL.ClockID); err != nil {
						return fmt.Errorf("invalid clock ID in DPLL desired state: %w", err)
					}
				}
			}
		}
	}

	return nil
}

// String methods for pretty printing

func (cc *ClockChain) String() string {
	var sb strings.Builder
	sb.WriteString("Clock Chain Configuration:\n")
	sb.WriteString(fmt.Sprintf("  Subsystems: %d\n", len(cc.Structure)))

	if cc.CommonDefinitions != nil {
		sb.WriteString(fmt.Sprintf("  eSync Definitions: %d\n", len(cc.CommonDefinitions.ESyncDefinitions)))
	}

	if cc.Behavior != nil {
		sb.WriteString(fmt.Sprintf("  Sources: %d\n", len(cc.Behavior.Sources)))
		sb.WriteString(fmt.Sprintf("  Conditions: %d\n", len(cc.Behavior.Conditions)))
	}

	return sb.String()
}

func (s *Subsystem) String() string {
	key := s.HardwareSpecificDefinitions
	if key == "" {
		key = "default"
	}
	return fmt.Sprintf("Subsystem %s (Plugin: %s, Clock ID: %s, Ethernet Ports: %d)",
		s.Name, key, s.DPLL.ClockID, len(s.Ethernet))
}

// Plugin system types and functions

// PluginInfo contains metadata about a hardware plugin
type PluginInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Vendor      string `json:"vendor"`
}

// PluginPinDefaults defines default pin configurations for a hardware plugin
type PluginPinDefaults struct {
	Priority *int64 `json:"priority,omitempty"`
	State    string `json:"state,omitempty"`
}

// PinOverrides defines specific pin overrides for common pin names
type PinOverrides struct {
	EEC *PluginPinDefaults `json:"eec,omitempty"`
	PPS *PluginPinDefaults `json:"pps,omitempty"`
}

// PluginSpecificDefaults defines specific pin overrides for common pin names
type PluginSpecificDefaults map[string]*PinOverrides

// HardwareOptionsConfig represents a complete hardware-specific options file
// Previously HardwarePluginConfig
type HardwareOptionsConfig struct {
	PluginInfo       PluginInfo             `json:"pluginInfo" yaml:"pluginInfo"`
	SpecificDefaults PluginSpecificDefaults `json:"specificDefaults,omitempty" yaml:"specificDefaults,omitempty"`
	BehaviorNotes    string                 `json:"behaviorNotes,omitempty" yaml:"behaviorNotes,omitempty"`
}

// HardwareConfigSpec defines the desired state of HardwareConfig
type HardwareConfigSpec struct {
	// Profile contains the hardware profile with its configuration
	Profile HardwareProfile `json:"profile" yaml:"profile"`

	// RelatedPtpProfileName specifies the name of the related PTP profile
	RelatedPtpProfileName string `json:"relatedPtpProfileName,omitempty" yaml:"relatedPtpProfileName,omitempty"`
}

// HardwareConfigStatus defines the observed state of HardwareConfig
type HardwareConfigStatus struct {
	// MatchedNodes contains the list of nodes that have been matched to this hardware config
	// based on PTP profile recommendations
	MatchedNodes []MatchedNode `json:"matchedNodes,omitempty" yaml:"matchedNodes,omitempty"`
}

// MatchedNode represents a node that has been matched to this hardware config
type MatchedNode struct {
	// NodeName is the name of the matched node
	NodeName string `json:"nodeName" yaml:"nodeName"`

	// PtpProfile is the PTP profile that was recommended for this node
	PtpProfile string `json:"ptpProfile" yaml:"ptpProfile"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HardwareConfig is the Schema for the hardwareconfigs API
type HardwareConfig struct {
	metav1.TypeMeta   `json:",inline" yaml:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`

	Spec   HardwareConfigSpec   `json:"spec,omitempty" yaml:"spec,omitempty"`
	Status HardwareConfigStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HardwareConfigList contains a list of HardwareConfig
type HardwareConfigList struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Items           []HardwareConfig `json:"items" yaml:"items"`
}

// HardwareProfile defines a hardware configuration profile
type HardwareProfile struct {
	// Name is the unique identifier for this hardware profile
	Name *string `json:"name" yaml:"name"`

	// ClockChain contains the complete clock chain configuration for this profile
	ClockChain *ClockChain `json:"clockChain" yaml:"clockChain"`

	// Description provides optional context about this hardware profile
	Description *string `json:"description,omitempty" yaml:"description,omitempty"`
}

func init() {
	SchemeBuilder.Register(&HardwareConfig{}, &HardwareConfigList{})
}
