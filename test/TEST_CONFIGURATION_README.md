# PTP Operator Test Configuration Guide

## Overview

This document provides comprehensive guidance on configuring tests for the PTP Operator, including how to add new Config types and understanding the existing test coverage. The test framework now includes comprehensive T-GM (Telco GrandMaster) event-based testing capabilities.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Test Environment Setup](#test-environment-setup)
3. [Adding New Config Types](#adding-new-config-types)
4. [Test Coverage Analysis](#test-coverage-analysis)
5. [T-GM Event-Based Testing](#t-gm-event-based-testing)
6. [Test Configuration Examples](#test-configuration-examples)

## Prerequisites

### Required Tools
- Ginkgo v2 CLI (follow the [Migration Guide](https://onsi.github.io/ginkgo/MIGRATING_TO_V2))
- Go 1.19+
- Kubernetes cluster (OpenShift preferred)
- Access to container registries (quay.io)

### Environment Variables
```bash
# Required
export KUBECONFIG="/path/to/your/kubeconfig"
export PTP_TEST_MODE="Discovery"  # or "OC", "BC", "TGM"

# Optional but recommended
export PTP_LOG_LEVEL="info"  # trace, debug, info, warn, error, fatal, panic
export ENABLE_TEST_CASE="reboot"  # comma-separated list
export SKIP_INTERFACES="eno1,ens2f1"  # interfaces to skip
export KEEP_PTPCONFIG="true"  # keep test-created configs
export MAX_OFFSET_IN_NS="100"  # max clock offset in nanoseconds
export MIN_OFFSET_IN_NS="-100"  # min clock offset in nanoseconds
export ENABLE_PTP_EVENT="true"  # enable event-based tests
export EVENT_API_VERSION="2.0"  # REST-API version for events
export EXTERNAL_GM="false"  # enable external grandmaster scenarios
export PTP_TEST_CONFIG_FILE="ptptestconfig.yaml"  # test configuration file
```

## Test Environment Setup

### 1. Basic Setup
```bash
# Clone the repository
git clone https://github.com/k8snetworkplumbingwg/ptp-operator-k8.git
cd ptp-operator-k8

# Install dependencies
go mod download
```

### 2. Running Tests
```bash
# Run all tests
make functests

# Run specific test mode
PTP_TEST_MODE=Discovery make functests

# Run with additional test cases
ENABLE_TEST_CASE=reboot PTP_TEST_MODE=Discovery make functests

# Run T-GM event-based tests
PTP_TEST_MODE=TGM ENABLE_PTP_EVENT=true make functests

# Run with container
docker run -e PTP_TEST_MODE=OC -e ENABLE_TEST_CASE=reboot \
  -v /path/to/kubeconfig:/tmp/config:Z \
  -v ./output:/output:Z \
  quay.io/redhat-cne/ptp-operator-test:latest
```

## Adding New Config Types

### 1. Define New Config Type

Create a new configuration type by extending the existing test configuration structure:

```go
// In test/conformance/config/ptptestconfig.go

type NewConfigType struct {
    Spec NewConfigSpec `yaml:"spec"`
    Desc string        `yaml:"desc"`
}

type NewConfigSpec struct {
    Enable          bool   `yaml:"enable"`
    Duration        int    `yaml:"duration"`
    FailureThreshold int  `yaml:"failure_threshold"`
    CustomParams    NewConfigParams `yaml:"custom_params"`
}

type NewConfigParams struct {
    // Add your custom parameters here
    Parameter1 string `yaml:"parameter1"`
    Parameter2 int    `yaml:"parameter2"`
}
```

### 2. Update Test Configuration Structure

```go
// In test/conformance/config/ptptestconfig.go

type PtpTestConfig struct {
    Global     GlobalConfig     `yaml:"global"`
    SoakTest   SoakTestConfig  `yaml:"soaktest"`
    // Add your new config type
    NewConfig  NewConfigType   `yaml:"new_config"`
}
```

### 3. Create Test Implementation

```go
// In test/conformance/serial/ptp.go or test/conformance/parallel/ptp.go

func testNewConfigType(fullConfig testconfig.TestConfig, testParameters *ptptestconfig.PtpTestConfig) {
    // Your test implementation
    newConfigSpec := testParameters.NewConfig.Spec
    
    if !newConfigSpec.Enable {
        Skip("skip the test - the test is disabled")
    }
    
    // Implement your test logic here
    // Example:
    // 1. Setup test environment
    // 2. Execute test scenario
    // 3. Validate results
    // 4. Cleanup
}
```

### 4. Add Test Case to Test Suite

```go
// In test/conformance/serial/ptp.go

Context("New Config Type Tests", func() {
    BeforeEach(func() {
        // Setup for your test
    })
    
    It("Should test new config type functionality", func() {
        testNewConfigType(fullConfig, testParameters)
    })
    
    AfterEach(func() {
        // Cleanup after test
    })
})
```

### 5. Update Configuration File

```yaml
# In test/conformance/config/ptptestconfig.yaml

new_config:
  spec:
    enable: true
    duration: 10
    failure_threshold: 3
    custom_params:
      parameter1: "value1"
      parameter2: 100
  desc: "Test description for new config type"
```

## Test Coverage Analysis

### Current Test Coverage

#### 1. Validation Tests
- **Namespace Validation**: Checks `openshift-ptp` namespace exists
- **Operator Deployment**: Validates ptp-operator deployment is running
- **DaemonSet Validation**: Ensures linuxptp-daemon is running on all nodes
- **CRD Availability**: Verifies all PTP CRDs are available

#### 2. Serial Tests
- **PTP Event Configuration**: Validates PTP events are properly configured
- **Resource Existence**: Checks PTP operator resources are available
- **Node Coverage**: Ensures all nodes have linuxptp-daemon replicas
- **Operator Deployment**: Validates operator deployment status
- **Network Interface Discovery**: Verifies PTP interfaces are correctly discovered
- **Hardware Details**: Retrieves and validates PTP hardware information
- **Network Outage Recovery**: Tests interface down/up scenarios
- **Node Reboot Recovery**: Tests node reboot scenarios
- **Clock Synchronization**: Validates clock sync states
- **Process Status**: Monitors PTP process states
- **Clock Class State**: Validates clock class transitions
- **DPLL State**: Monitors DPLL frequency and phase states
- **NMEA Status**: Validates PTP NMEA functionality

#### 3. Parallel Tests
- **CPU Utilization**: Monitors CPU usage of PTP components
- **Event-Based Testing**: Tests PTP event framework
- **Slave Clock Sync**: Validates slave clock synchronization
- **V1 Regression**: Tests backward compatibility with v1 API

#### 4. T-GM (Telco GrandMaster) Tests
- **WPC GM Verification**: Validates WPC GrandMaster state based on logs
- **Process Status**: Checks required processes (phc2sys, gpspipe, ts2phc, gpsd, ptp4l, dpll)
- **Clock Class State**: Validates clock class is locked
- **GM State Stability**: Monitors GM state using metrics
- **GNSS Signal Loss**: Tests holdover through connection loss
- **Events Verification V1**: Verifies events during GNSS loss flow (V1)
- **Events Verification V2**: Verifies events during GNSS loss flow (V2)

### Test Modes Supported

1. **Discovery Mode**: Automatically discovers existing PTP configurations
2. **OC (Ordinary Clock)**: Tests ordinary clock configurations
3. **BC (Boundary Clock)**: Tests boundary clock configurations
4. **Dual NIC BC**: Tests dual NIC boundary clock configurations
5. **TGM (Telco GrandMaster)**: Tests telco grandmaster configurations
6. **Dual Follower**: Tests dual follower configurations

## T-GM Event-Based Testing

The PTP Operator test framework now includes comprehensive T-GM event-based testing capabilities. This implementation extends the existing event framework to support T-GM-specific events and scenarios.

### T-GM Event Types

The framework supports the following T-GM-specific event types:

```go
const (
    TgmClockClassChangeEvent = "TGM_CLOCK_CLASS_CHANGE"
    TgmGmStateChangeEvent = "TGM_GM_STATE_CHANGE"
    TgmDpllStateChangeEvent = "TGM_DPLL_STATE_CHANGE"
    TgmGnssSignalLossEvent = "TGM_GNSS_SIGNAL_LOSS"
    TgmHoldoverStateChangeEvent = "TGM_HOLDOVER_STATE_CHANGE"
    TgmFailoverEvent = "TGM_FAILOVER"
    TgmRecoveryEvent = "TGM_RECOVERY"
    TgmMultiInterfaceEvent = "TGM_MULTI_INTERFACE"
)
```

### T-GM Event Structure

```go
type TgmEvent struct {
    Type      string    `json:"type"`
    Source    string    `json:"source"`
    Timestamp time.Time `json:"timestamp"`
    Data      TgmEventData `json:"data"`
}

type TgmEventData struct {
    ClockClass    int    `json:"clock_class"`
    GmState       string `json:"gm_state"`
    DpllState     string `json:"dpll_state"`
    HoldoverState string `json:"holdover_state"`
    Interface     string `json:"interface,omitempty"`
    Quality       string `json:"quality,omitempty"`
}
```

### T-GM Test Categories

#### 1. Event Framework Integration Tests
- **T-GM Event Publishing**: Tests T-GM event publishing capabilities
- **T-GM Event Subscription**: Tests T-GM event subscription mechanism
- **T-GM Event Format Validation**: Validates T-GM event format and content

#### 2. Cloud Event Proxy Integration Tests
- **T-GM Sidecar Communication**: Tests T-GM sidecar communication
- **T-GM Event Transport**: Tests T-GM event transport via HTTP
- **T-GM API Endpoints**: Validates T-GM API endpoint availability

#### 3. Linuxptp Daemon Integration Tests
- **T-GM Daemon Configuration**: Tests T-GM daemon configuration loading
- **T-GM Process Management**: Tests T-GM process management
- **T-GM Multi-Interface Support**: Tests T-GM multi-interface support

#### 4. Advanced T-GM Functionality Tests
- **T-GM Clock Quality Metrics**: Tests T-GM clock quality metrics
- **T-GM Failover Scenarios**: Tests T-GM failover scenarios
- **T-GM Holdover State**: Tests T-GM holdover state management

#### 5. Performance and Reliability Tests
- **T-GM Performance Under Load**: Tests T-GM performance under high load
- **T-GM Long-term Stability**: Tests T-GM long-term stability
- **T-GM Event Reliability**: Tests T-GM event delivery reliability

### Running T-GM Event Tests

```bash
# Run T-GM event tests
PTP_TEST_MODE=TGM ENABLE_PTP_EVENT=true make functests

# Run specific T-GM event tests
ENABLE_TEST_CASE=tgm_event_publishing,tgm_clock_quality PTP_TEST_MODE=TGM make functests

# Run T-GM tests with event framework
ENABLE_PTP_EVENT=true EVENT_API_VERSION=2.0 PTP_TEST_MODE=TGM make functests
```

## Test Configuration Examples

### Example 1: Basic T-GM Configuration
```yaml
# ptptestconfig.yaml
global:
  maxoffset: 100
  minoffset: -100
  holdover_timeout: 5

soaktest:
  disable_all: false
  duration: 60
  failure_threshold: 3
  
  tgm_event_publishing:
    spec:
      enable: true
      duration: 30
      failure_threshold: 2
      custom_params:
        event_types: ["TGM_CLOCK_CLASS_CHANGE", "TGM_GM_STATE_CHANGE"]
        transport_protocol: "HTTP"
        api_version: "2.0"
    desc: "Test T-GM event publishing capabilities"
    
  tgm_clock_quality:
    spec:
      enable: true
      duration: 45
      failure_threshold: 1
      custom_params:
        quality_threshold: 100
        class_threshold: 6
    desc: "Test T-GM clock quality metrics"
```

### Example 2: Advanced T-GM Configuration
```yaml
# ptptestconfig.yaml
global:
  maxoffset: 50
  minoffset: -50
  holdover_timeout: 10

soaktest:
  disable_all: false
  duration: 120
  failure_threshold: 5
  
  tgm_multi_interface:
    spec:
      enable: true
      duration: 60
      failure_threshold: 3
      custom_params:
        interface_count: 4
        load_balancing: true
    desc: "Test T-GM multi-interface support"
    
  tgm_failover_scenarios:
    spec:
      enable: true
      duration: 90
      failure_threshold: 2
      custom_params:
        failover_timeout: 30
        recovery_timeout: 60
        backup_gm_enabled: true
    desc: "Test T-GM failover scenarios"
```

### Example 3: Event Framework Configuration
```yaml
# ptptestconfig.yaml
global:
  maxoffset: 100
  minoffset: -100

soaktest:
  tgm_event_integration:
    spec:
      enable: true
      duration: 45
      failure_threshold: 2
      custom_params:
        event_types: ["CLOCK_CLASS_CHANGE", "PORT_STATE_CHANGE", "SYNC_STATE_CHANGE"]
        transport_protocol: "HTTP"
        api_version: "2.0"
    desc: "Test T-GM event framework integration"
    
  tgm_sidecar_communication:
    spec:
      enable: true
      duration: 30
      failure_threshold: 1
      custom_params:
        sidecar_image: "quay.io/redhat-cne/cloud-event-proxy:latest"
        api_port: 9043
        metrics_port: 9091
    desc: "Test T-GM sidecar communication"
```

## Running Specific Test Cases

### Run T-GM Tests Only
```bash
PTP_TEST_MODE=TGM make functests
```

### Run Event Framework Tests
```bash
ENABLE_PTP_EVENT=true EVENT_API_VERSION=2.0 make functests
```

### Run Performance Tests
```bash
ENABLE_TEST_CASE=performance PTP_TEST_MODE=TGM make functests
```

### Run T-GM Event Tests
```bash
ENABLE_TEST_CASE=tgm_event_publishing,tgm_clock_quality PTP_TEST_MODE=TGM make functests
```

## Troubleshooting

### Common Issues

1. **Test Discovery Failure**
   ```bash
   # Check node labels
   oc get nodes --show-labels | grep ptp
   
   # Verify PTP configs
   oc get ptpconfigs -n openshift-ptp
   ```

2. **Event Framework Issues**
   ```bash
   # Check cloud-event-proxy logs
   oc logs -n openshift-ptp -l app=linuxptp-daemon -c cloud-event-proxy
   
   # Verify event API endpoints
   curl -k https://localhost:8443/api/ocloudNotifications/v2/health
   ```

3. **T-GM Configuration Issues**
   ```bash
   # Check T-GM processes
   oc exec -n openshift-ptp <pod-name> -- ps aux | grep -E "(ptp4l|phc2sys|gpsd)"
   
   # Verify clock state
   oc exec -n openshift-ptp <pod-name> -- pmc -u -b 0 "GET CLOCK_CLASS"
   ```

4. **T-GM Event Issues**
   ```bash
   # Check T-GM event logs
   oc logs -n openshift-ptp <pod-name> -c linuxptp-daemon-container | grep -i event
   
   # Verify T-GM event endpoints
   curl -k https://localhost:9043/api/ocloudNotifications/v2/health
   ```

## Implementation Status

### Completed Features ✅
- T-GM event framework integration
- T-GM event types and structures
- T-GM event publishing and subscription
- T-GM sidecar communication
- T-GM clock quality monitoring
- T-GM failover scenarios
- T-GM multi-interface support
- T-GM performance and reliability tests

### In Progress 🔄
- Advanced T-GM event validation
- T-GM event performance optimization
- T-GM event reliability enhancements

### Planned Features 📋
- T-GM event scalability testing
- T-GM event recovery mechanisms
- T-GM event integration with existing frameworks

## Contributing

When adding new test cases:

1. Follow the existing code structure and patterns
2. Add appropriate documentation
3. Include configuration examples
4. Add unit tests for new functionality
5. Update this README with new test cases
6. For T-GM tests, follow the event-based testing patterns

## References

- [PTP Operator Documentation](https://github.com/k8snetworkplumbingwg/ptp-operator-k8)
- [Cloud Event Proxy](https://github.com/redhat-cne/cloud-event-proxy)
- [Linuxptp Daemon](https://github.com/openshift/linuxptp-daemon)
- [Ginkgo Testing Framework](https://onsi.github.io/ginkgo/)
- [T-GM Event Testing Implementation Plan](test/plans.md) 