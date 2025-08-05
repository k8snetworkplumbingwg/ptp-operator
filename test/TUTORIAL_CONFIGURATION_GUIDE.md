# PTP Operator Configuration Tutorial Guide

## Overview

This tutorial provides a step-by-step guide on how to use the `initAndSolveProblems()` function and how to define new configurations for testing, including hardware and port definitions. This guide is designed to help developers understand how to introduce new configurations for testing in the PTP Operator framework.

## Table of Contents

1. [Understanding the Problem Solver](#understanding-the-problem-solver)
2. [The `initAndSolveProblems()` Function](#the-initandsolveproblems-function)
3. [Defining New Hardware Configurations](#defining-new-hardware-configurations)
4. [Adding New Port Configurations](#adding-new-port-configurations)
5. [Creating Custom Test Configurations](#creating-custom-test-configurations)
6. [Step-by-Step Examples](#step-by-step-examples)
7. [Best Practices](#best-practices)
8. [Troubleshooting](#troubleshooting)

## Understanding the Problem Solver

The PTP Operator uses a sophisticated problem solver to determine the optimal configuration for different PTP modes (Ordinary Clock, Boundary Clock, Telco GrandMaster, etc.). The solver works by:

1. **Defining Problems**: Each PTP mode has a specific problem definition
2. **Setting Constraints**: Hardware and network constraints are applied
3. **Finding Solutions**: The solver finds valid configurations that meet all constraints
4. **Mapping Results**: Solutions are mapped to actual hardware interfaces

### Key Concepts

- **Problem**: A set of constraints that define a PTP configuration
- **Solution**: A valid assignment of interfaces to PTP roles
- **Step**: A constraint that must be satisfied (e.g., same NIC, same LAN, etc.)
- **Interface**: A network interface that can be used for PTP

## The `initAndSolveProblems()` Function

The `initAndSolveProblems()` function is the core of the configuration system. It initializes problem definitions and runs the solver to find valid configurations.

### Function Overview

```go
func initAndSolveProblems() {
    // Create maps for storing problems and solutions
    data.problems = make(map[string]*[][][]int)
    data.solutions = make(map[string]*[][]int)
    data.testClockRolesAlgoMapping = make(map[string]*[]int)

    // Initialize problems for each PTP mode
    // Each problem is defined as a series of steps with constraints
    // The solver finds solutions that satisfy all constraints
}
```

### Problem Structure

Each problem is defined as a 3D array where:
- **First dimension**: Problem name (e.g., "OC", "BC", "TGM")
- **Second dimension**: Steps in the problem
- **Third dimension**: Constraints for each step

```go
data.problems[AlgoOCString] = &[][][]int{
    {{int(solver.StepNil), 0, 0}},         // step1: No constraint
    {{int(solver.StepSameLan2), 2, 0, 1}}, // step2: Same LAN constraint
}
```

### Available Solver Steps

| Step Type | Description | Parameters |
|-----------|-------------|------------|
| `StepNil` | No constraint | `{StepNil, 0, 0}` |
| `StepSameLan2` | Interfaces must be on same LAN | `{StepSameLan2, 2, interface1, interface2}` |
| `StepSameNic` | Interfaces must be on same NIC | `{StepSameNic, 2, interface1, interface2}` |
| `StepDifferentNic` | Interfaces must be on different NICs | `{StepDifferentNic, 2, interface1, interface2}` |
| `StepSameNode` | Interfaces must be on same node | `{StepSameNode, 2, interface1, interface2}` |
| `StepIsPTP` | Interface must support PTP | `{StepIsPTP, 1, interface}` |
| `StepIsWPCNic` | Interface must be WPC NIC | `{StepIsWPCNic, 1, interface}` |

## Defining New Hardware Configurations

When you need to introduce new hardware configurations, you need to:

1. **Define the hardware interface**
2. **Specify PTP capabilities**
3. **Add to the discovery system**
4. **Create test configurations**

### Step 1: Define Hardware Interface

```go
// In test/pkg/testconfig/testconfig.go

// Define your new hardware type
const (
    NewHardwareType = "new_hardware"
)

// Add hardware detection
func detectNewHardware(nodeName string) ([]string, error) {
    // Implementation to detect your hardware
    // Return list of interface names
    return []string{"eth0", "eth1"}, nil
}
```

### Step 2: Specify PTP Capabilities

```go
// Define PTP capabilities for your hardware
type NewHardwarePtpCapabilities struct {
    SupportsPTP     bool
    HardwareTimestamp bool
    SyncE           bool
    Interfaces      []string
}

func getNewHardwarePtpCapabilities(interfaces []string) NewHardwarePtpCapabilities {
    return NewHardwarePtpCapabilities{
        SupportsPTP:      true,
        HardwareTimestamp: true,
        SyncE:            false,
        Interfaces:       interfaces,
    }
}
```

### Step 3: Add to Discovery System

```go
// In the discovery function
func discoverPTPConfiguration(namespace string) {
    // ... existing code ...
    
    // Add your new hardware detection
    if hasNewHardware(nodeName) {
        newHardwareInterfaces, err := detectNewHardware(nodeName)
        if err == nil {
            // Add to discovered interfaces
            discoveredInterfaces = append(discoveredInterfaces, newHardwareInterfaces...)
        }
    }
}
```

## Adding New Port Configurations

Port configurations define how interfaces are used in PTP setups. To add new port configurations:

### Step 1: Define Port Configuration

```go
// Define new port configuration
type NewPortConfig struct {
    InterfaceName string
    Role          string // "master", "slave", "passive"
    Priority      int
    SyncE         bool
    HardwareTimestamp bool
}

// Create port configuration function
func createNewPortConfig(interfaceName, role string, priority int) NewPortConfig {
    return NewPortConfig{
        InterfaceName:     interfaceName,
        Role:              role,
        Priority:          priority,
        SyncE:             false,
        HardwareTimestamp: true,
    }
}
```

### Step 2: Add to Problem Solver

```go
// Add new solver step for your port configuration
const (
    StepNewPortConfig = 100 // Use unique step number
)

// Add to initAndSolveProblems()
data.problems[AlgoNewConfigString] = &[][][]int{
    {{int(solver.StepNil), 0, 0}},                    // step1
    {{int(StepNewPortConfig), 1, 0}},                 // step2: Apply new port config
    {{int(solver.StepSameLan2), 2, 0, 1}},           // step3: Same LAN constraint
}
```

### Step 3: Create Configuration Function

```go
func PtpConfigNewConfig(isExtGM bool) error {
    // Get discovered configuration
    fullConfig := GetDesiredConfig(true)
    
    // Find valid solutions
    if len(fullConfig.FoundSolutions) == 0 {
        return fmt.Errorf("no valid solutions found for new config")
    }
    
    // Create PTP configuration
    profileName := "new-config-profile"
    nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
    interfaceName := fullConfig.DiscoveredFollowerInterfaces[0]
    
    return createConfig(profileName, &interfaceName, nil, "", nil, nodeName, nil, "SCHED_FIFO", nil)
}
```

## Creating Custom Test Configurations

To create custom test configurations, you need to:

1. **Define the configuration structure**
2. **Add to the test configuration system**
3. **Create test functions**
4. **Add to the problem solver**

### Step 1: Define Configuration Structure

```go
// In test/conformance/config/ptptestconfig.go

type NewTestConfig struct {
    Spec NewTestSpec `yaml:"spec"`
    Desc string      `yaml:"desc"`
}

type NewTestSpec struct {
    Enable          bool   `yaml:"enable"`
    Duration        int    `yaml:"duration"`
    FailureThreshold int  `yaml:"failure_threshold"`
    CustomParams    NewTestParams `yaml:"custom_params"`
}

type NewTestParams struct {
    HardwareType    string `yaml:"hardware_type"`
    PortConfig      string `yaml:"port_config"`
    SyncE           bool   `yaml:"sync_e"`
    HardwareTimestamp bool `yaml:"hardware_timestamp"`
}
```

### Step 2: Add to Test Configuration

```go
// In test/conformance/config/ptptestconfig.go

type PtpTestConfig struct {
    GlobalConfig   GlobalConfig   `yaml:"global"`
    SoakTestConfig SoakTestConfig `yaml:"soaktest"`
    NewTest        NewTestConfig  `yaml:"new_test"` // Add your new config
}
```

### Step 3: Create Test Function

```go
// In test/conformance/serial/ptp.go

func testNewConfiguration(fullConfig testconfig.TestConfig, testParameters *ptptestconfig.PtpTestConfig) {
    // Get test configuration
    newTestSpec := testParameters.NewTest.Spec
    
    if !newTestSpec.Enable {
        Skip("skip the test - the test is disabled")
    }
    
    By("setting up new hardware configuration", func() {
        // Setup your hardware
        err := setupNewHardware(newTestSpec.CustomParams.HardwareType)
        Expect(err).To(BeNil())
    })
    
    By("configuring ports", func() {
        // Configure ports
        err := configurePorts(newTestSpec.CustomParams.PortConfig)
        Expect(err).To(BeNil())
    })
    
    By("running new configuration test", func() {
        // Run your test
        err := runNewConfigurationTest(newTestSpec)
        Expect(err).To(BeNil())
    })
}
```

### Step 4: Add to Problem Solver

```go
// In test/pkg/testconfig/testconfig.go

// Add new algorithm string
const (
    AlgoNewConfigString = "new_config"
)

// Add to initAndSolveProblems()
data.problems[AlgoNewConfigString] = &[][][]int{
    {{int(solver.StepNil), 0, 0}},                    // step1
    {{int(solver.StepIsPTP), 1, 0}},                  // step2: Must support PTP
    {{int(solver.StepSameLan2), 2, 0, 1}},           // step3: Same LAN
    {{int(StepNewHardware), 1, 0}},                   // step4: Must be new hardware
}

// Add to enabled problems
var enabledProblems = []string{
    AlgoOCString,
    AlgoBCString,
    AlgoTelcoGMString,
    AlgoNewConfigString, // Add your new config
}
```

## Step-by-Step Examples

### Example 1: Adding a New Hardware Type

Let's say you want to add support for a new network card with specific PTP capabilities.

#### Step 1: Define Hardware Detection

```go
// Add to test/pkg/testconfig/testconfig.go

const (
    NewCardType = "new_card_type"
)

func detectNewCard(nodeName string) ([]string, error) {
    // Run command to detect new card
    cmd := exec.Command("lspci", "-d", "new_vendor:new_device")
    output, err := cmd.Output()
    if err != nil {
        return nil, err
    }
    
    // Parse output to get interface names
    interfaces := parseNewCardInterfaces(string(output))
    return interfaces, nil
}

func hasNewCard(nodeName string) bool {
    // Check if node has new card
    cmd := exec.Command("lspci", "-d", "new_vendor:new_device")
    err := cmd.Run()
    return err == nil
}
```

#### Step 2: Add to Discovery

```go
// In discoverPTPConfiguration function
func discoverPTPConfiguration(namespace string) {
    // ... existing code ...
    
    // Add new card detection
    if hasNewCard(nodeName) {
        newCardInterfaces, err := detectNewCard(nodeName)
        if err == nil {
            // Add to discovered interfaces
            for _, iface := range newCardInterfaces {
                discoveredInterfaces = append(discoveredInterfaces, iface)
            }
        }
    }
}
```

#### Step 3: Create Configuration

```go
func PtpConfigNewCard(isExtGM bool) error {
    fullConfig := GetDesiredConfig(true)
    
    if len(fullConfig.FoundSolutions) == 0 {
        return fmt.Errorf("no valid solutions found for new card")
    }
    
    profileName := "new-card-profile"
    nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
    interfaceName := fullConfig.DiscoveredFollowerInterfaces[0]
    
    // Create configuration with new card specific settings
    ptp4lOpts := "-2" // Enable hardware timestamping
    phc2sysOpts := "-a -r"
    
    return createConfig(profileName, &interfaceName, &ptp4lOpts, "", &phc2sysOpts, nodeName, nil, "SCHED_FIFO", nil)
}
```

#### Step 4: Add to Problem Solver

```go
// Add new solver step
const (
    StepIsNewCard = 101
)

// Add to initAndSolveProblems()
data.problems[AlgoNewCardString] = &[][][]int{
    {{int(solver.StepIsPTP), 1, 0}},                  // step1: Must support PTP
    {{int(StepIsNewCard), 1, 0}},                     // step2: Must be new card
    {{int(solver.StepSameLan2), 2, 0, 1}},           // step3: Same LAN
}

// Add to enabled problems
var enabledProblems = []string{
    AlgoOCString,
    AlgoBCString,
    AlgoTelcoGMString,
    AlgoNewCardString, // Add your new card
}
```

### Example 2: Adding a New Port Configuration

Let's say you want to add a new port configuration for SyncE support.

#### Step 1: Define Port Configuration

```go
type SyncEPortConfig struct {
    InterfaceName string
    SyncEEnabled  bool
    Priority      int
}

func createSyncEPortConfig(interfaceName string, syncEEnabled bool, priority int) SyncEPortConfig {
    return SyncEPortConfig{
        InterfaceName: interfaceName,
        SyncEEnabled:  syncEEnabled,
        Priority:      priority,
    }
}
```

#### Step 2: Add to Problem Solver

```go
// Add new solver step
const (
    StepSyncEConfig = 102
)

// Add to initAndSolveProblems()
data.problems[AlgoSyncEString] = &[][][]int{
    {{int(solver.StepIsPTP), 1, 0}},                  // step1: Must support PTP
    {{int(StepSyncEConfig), 1, 0}},                   // step2: Must support SyncE
    {{int(solver.StepSameLan2), 2, 0, 1}},           // step3: Same LAN
}
```

#### Step 3: Create Configuration Function

```go
func PtpConfigSyncE(isExtGM bool) error {
    fullConfig := GetDesiredConfig(true)
    
    if len(fullConfig.FoundSolutions) == 0 {
        return fmt.Errorf("no valid solutions found for SyncE config")
    }
    
    profileName := "synce-profile"
    nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
    interfaceName := fullConfig.DiscoveredFollowerInterfaces[0]
    
    // Create configuration with SyncE settings
    ptp4lOpts := "-2 -i" // Enable hardware timestamping and SyncE
    ptp4lConfig := BasePtp4lConfig + "\n[synce]\nenable 1"
    
    return createConfig(profileName, &interfaceName, &ptp4lOpts, ptp4lConfig, nil, nodeName, nil, "SCHED_FIFO", nil)
}
```

## Best Practices

### 1. Hardware Detection

- **Use standard tools**: Use `lspci`, `ip link`, `ethtool` for hardware detection
- **Check capabilities**: Verify PTP support with `ethtool -T`
- **Handle errors gracefully**: Always check for errors in hardware detection
- **Log discoveries**: Log what hardware is found for debugging

```go
func detectHardwareSafely(nodeName string) ([]string, error) {
    interfaces, err := detectNewHardware(nodeName)
    if err != nil {
        logrus.Warnf("Failed to detect hardware on node %s: %v", nodeName, err)
        return nil, err
    }
    
    logrus.Infof("Detected hardware interfaces on node %s: %v", nodeName, interfaces)
    return interfaces, nil
}
```

### 2. Problem Definition

- **Keep it simple**: Start with basic constraints and add complexity
- **Test thoroughly**: Verify that your problem definition produces expected solutions
- **Document constraints**: Clearly document what each constraint means
- **Use meaningful names**: Use descriptive names for your solver steps

```go
// Good: Clear and documented
data.problems[AlgoNewConfigString] = &[][][]int{
    {{int(solver.StepIsPTP), 1, 0}},                  // Must support PTP
    {{int(StepNewHardware), 1, 0}},                   // Must be new hardware
    {{int(solver.StepSameLan2), 2, 0, 1}},           // Must be on same LAN
}

// Bad: Unclear and undocumented
data.problems[AlgoNewConfigString] = &[][][]int{
    {{1, 0}}, {{100, 1, 0}}, {{2, 2, 0, 1}}
}
```

### 3. Configuration Creation

- **Validate inputs**: Always validate configuration parameters
- **Use defaults**: Provide sensible defaults for optional parameters
- **Handle errors**: Properly handle and report configuration errors
- **Test configurations**: Test your configurations in isolation

```go
func createNewConfig(profileName, interfaceName string, options map[string]string) error {
    // Validate inputs
    if profileName == "" {
        return fmt.Errorf("profile name cannot be empty")
    }
    if interfaceName == "" {
        return fmt.Errorf("interface name cannot be empty")
    }
    
    // Set defaults
    if options == nil {
        options = make(map[string]string)
    }
    if _, exists := options["priority"]; !exists {
        options["priority"] = "128"
    }
    
    // Create configuration
    return createConfig(profileName, &interfaceName, nil, "", nil, "node-label", nil, "SCHED_FIFO", nil)
}
```

### 4. Testing

- **Unit test**: Write unit tests for your hardware detection
- **Integration test**: Test your configuration with real hardware
- **Error cases**: Test error conditions and edge cases
- **Documentation**: Document how to test your new configuration

```go
func TestNewHardwareDetection(t *testing.T) {
    // Test hardware detection
    interfaces, err := detectNewHardware("test-node")
    if err != nil {
        t.Errorf("Hardware detection failed: %v", err)
    }
    
    if len(interfaces) == 0 {
        t.Error("No interfaces detected")
    }
    
    // Test configuration creation
    err = createNewConfig("test-profile", interfaces[0], nil)
    if err != nil {
        t.Errorf("Configuration creation failed: %v", err)
    }
}
```

## Troubleshooting

### Common Issues

#### 1. No Solutions Found

**Problem**: The solver returns no valid solutions for your configuration.

**Solution**:
- Check that your hardware is properly detected
- Verify that your constraints are not too restrictive
- Ensure that interfaces support the required PTP features
- Add logging to see what constraints are failing

```go
// Add debugging to see what's happening
func debugSolverSolutions() {
    for problemName, solutions := range data.solutions {
        if len(*solutions) == 0 {
            logrus.Warnf("No solutions found for problem: %s", problemName)
            // Log the problem definition
            if problem, exists := data.problems[problemName]; exists {
                logrus.Infof("Problem definition: %+v", problem)
            }
        }
    }
}
```

#### 2. Hardware Not Detected

**Problem**: Your new hardware is not being detected by the discovery system.

**Solution**:
- Verify hardware detection commands work manually
- Check that hardware detection is called in discovery
- Add logging to see what's happening during detection
- Test hardware detection in isolation

```go
// Add logging to hardware detection
func detectNewHardware(nodeName string) ([]string, error) {
    logrus.Infof("Detecting new hardware on node: %s", nodeName)
    
    cmd := exec.Command("lspci", "-d", "new_vendor:new_device")
    output, err := cmd.Output()
    if err != nil {
        logrus.Errorf("Hardware detection command failed: %v", err)
        return nil, err
    }
    
    logrus.Infof("Hardware detection output: %s", string(output))
    interfaces := parseNewCardInterfaces(string(output))
    logrus.Infof("Detected interfaces: %v", interfaces)
    
    return interfaces, nil
}
```

#### 3. Configuration Creation Fails

**Problem**: Your configuration creation function fails.

**Solution**:
- Check that all required parameters are provided
- Verify that the Kubernetes API is accessible
- Ensure that the namespace exists
- Add detailed error logging

```go
func createNewConfigWithLogging(profileName, interfaceName string) error {
    logrus.Infof("Creating new configuration: profile=%s, interface=%s", profileName, interfaceName)
    
    err := createConfig(profileName, &interfaceName, nil, "", nil, "node-label", nil, "SCHED_FIFO", nil)
    if err != nil {
        logrus.Errorf("Configuration creation failed: %v", err)
        return err
    }
    
    logrus.Infof("Configuration created successfully")
    return nil
}
```

### Debugging Tips

1. **Enable verbose logging**: Set log level to debug to see detailed information
2. **Test in isolation**: Test your hardware detection and configuration creation separately
3. **Use manual verification**: Manually verify that your hardware detection commands work
4. **Check Kubernetes resources**: Verify that your configurations are created in Kubernetes
5. **Monitor logs**: Watch the PTP operator logs for errors

```bash
# Enable debug logging
export PTP_LOG_LEVEL="debug"

# Test hardware detection manually
lspci -d new_vendor:new_device

# Check Kubernetes resources
oc get ptpconfigs -n openshift-ptp

# Monitor operator logs
oc logs -n openshift-ptp -l app=ptp-operator -f
```

## Summary

This tutorial has covered:

1. **Understanding the problem solver**: How the solver works and how to define problems
2. **Using `initAndSolveProblems()`**: How to initialize and run the solver
3. **Defining new hardware**: How to add support for new hardware types
4. **Adding port configurations**: How to create new port configurations
5. **Creating test configurations**: How to integrate new configurations into the test framework
6. **Step-by-step examples**: Practical examples of adding new hardware and configurations
7. **Best practices**: Guidelines for creating robust configurations
8. **Troubleshooting**: Common issues and how to resolve them

By following this tutorial, you should be able to:

- Add support for new hardware types
- Create new port configurations
- Integrate new configurations into the test framework
- Debug and troubleshoot configuration issues
- Follow best practices for robust configuration development

Remember to always test your configurations thoroughly and document your changes for future reference. 