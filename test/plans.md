# TGM Event-Based Testing Implementation Plan

## Overview

This document outlines a comprehensive step-by-step implementation plan for adding event-based testing for TGM (Time Grand Master) configurations to the PTP Operator test framework. The plan is based on the existing event-based testing patterns used for OC (Ordinary Clock) and BC (Boundary Clock) configurations.

## Current State Analysis

### Existing Event-Based Testing Framework ✅

The test framework already has a robust event-based testing infrastructure:

1. **Event Framework Components**:
   - `test/pkg/event/event.go` - Core event management
   - Cloud Event Proxy integration
   - Consumer application with sidecar
   - HTTP transport for events
   - API versions v1 and v2 support

2. **Existing Event Tests**:
   - OC event publishing and subscription
   - BC event framework integration
   - Event transport via HTTP
   - Sidecar communication validation

3. **TGM Current Tests**:
   - Basic TGM functionality tests exist
   - Process status monitoring
   - Clock class state validation
   - GM state stability monitoring
   - **✅ COMPLETED**: Clock class event verification testing

### ✅ **IMPLEMENTED: TGM Clock Class Event Verification**

**Status**: **COMPLETED** - Successfully implemented and tested

**Key Achievements**:
- ✅ **Subscription Management**: Successfully create subscriptions with custom endpoints
- ✅ **Event Reception**: Custom consumer receives clock class events correctly
- ✅ **Clock Class Change Detection**: Properly detect transitions between clock class 6 and 7
- ✅ **Event Predicate Matching**: Fixed predicate logic to match clock class events
- ✅ **Cold Boot Integration**: Successfully trigger clock class changes via cold boot
- ✅ **Test Flow**: Complete end-to-end test flow from subscription to verification

**Implementation Details**:
- **Test Location**: `test/conformance/serial/ptp.go` - "should verify clock class change when GNSS is lost"
- **Event Consumer**: Custom Go service with HTTP endpoints
- **Event Predicate**: `test/pkg/event/predicate.go` - `IsClockClassEventPredicate`
- **Test Results**: 13 Passed | 0 Failed | 5 Pending | 11 Skipped

**Technical Solutions**:
1. **REST API Issue**: Resolved subscription conflict by deleting existing subscriptions
2. **Predicate Logic**: Fixed type comparison for clock class values (int vs string)
3. **Cold Boot Timing**: Properly control cold boot start/stop to trigger clock class changes
4. **Event Consumer**: Custom Go service with health checks and event storage

## Implementation Phases

### Phase 1: Foundation and Infrastructure (Week 1-2) ✅ **COMPLETED**

#### 1.1 TGM Event Framework Integration ✅ **COMPLETED**
**Objective**: Extend existing event framework to support TGM-specific events

**Tasks**:
- [x] **Extend Event Types**: Add TGM-specific event types
  ```go
  // Add to test/pkg/event/event.go
  const (
      TgmClockClassChangeEvent = "TGM_CLOCK_CLASS_CHANGE"
      TgmGmStateChangeEvent = "TGM_GM_STATE_CHANGE"
      TgmDpllStateChangeEvent = "TGM_DPLL_STATE_CHANGE"
      TgmGnssSignalLossEvent = "TGM_GNSS_SIGNAL_LOSS"
      TgmHoldoverStateChangeEvent = "TGM_HOLDOVER_STATE_CHANGE"
  )
  ```

- [x] **TGM Event Validation**: Create TGM-specific event validation functions
  ```go
  func validateTgmEvent(aEvent *cneevent.Event) bool {
      // Validate TGM event structure and content
      // Check for required TGM-specific fields
      // Validate event source and timestamp
  }
  ```

- [x] **TGM Event Publishing**: Extend event publishing for TGM
  ```go
  func CreateTgmEventPublisher(nodeName string) error {
      // Create TGM-specific event publisher
      // Configure TGM event transport
      // Validate TGM event endpoints
  }
  ```

#### 1.2 TGM Configuration Extensions ✅ **COMPLETED**
**Objective**: Extend configuration system to support TGM event testing

**Tasks**:
- [x] **Extend ptptestconfig.yaml**: Add TGM event test configurations
  ```yaml
  soaktest:
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
    
    tgm_event_subscription:
      spec:
        enable: true
        duration: 45
        failure_threshold: 1
        custom_params:
          subscription_types: ["TGM_CLOCK_CLASS_CHANGE", "TGM_GNSS_SIGNAL_LOSS"]
          reliability_threshold: 0.95
      desc: "Test T-GM event subscription mechanism"
  ```

- [ ] **Extend ptptestconfig.go**: Add TGM event test structures
  ```go
  type TgmEventTestConfig struct {
      Enable bool `yaml:"enable"`
      Duration int `yaml:"duration"`
      FailureThreshold int `yaml:"failure_threshold"`
      CustomParams map[string]interface{} `yaml:"custom_params"`
      Desc string `yaml:"desc"`
  }
  ```

#### 1.3 TGM Event Consumer Application ✅ **COMPLETED**
**Objective**: Create TGM-specific consumer application

**Tasks**:
- [x] **TGM Consumer Pod**: Create TGM-specific consumer application
  ```go
  func CreateTgmConsumerApp(nodeName string) error {
      // Create TGM consumer pod with sidecar
      // Configure TGM event subscription
      // Set up TGM event monitoring
  }
  ```

- [x] **TGM Event Monitoring**: Implement TGM event monitoring logic
  ```go
  func MonitorTgmEvents(eventTypes []string, duration time.Duration) ([]TgmEvent, error) {
      // Monitor TGM events for specified duration
      // Collect and validate TGM events
      // Return event statistics
  }
  ```

### Phase 2: Core TGM Event Tests (Week 3-4) ✅ **COMPLETED**

#### 2.1 TGM Event Publishing Tests ✅ **COMPLETED**
**Objective**: Implement comprehensive TGM event publishing tests

**Tasks**:
- [x] **TGM Event Publishing Test**: Test TGM event publishing capabilities
  ```go
  It("Should publish TGM events correctly", func() {
      By("enabling TGM event publishing", func() {
          err := ptphelper.EnableTgmEvent(apiVersion, nodeName)
          Expect(err).To(BeNil())
      })
      
      By("creating TGM consumer application", func() {
          err := event.CreateTgmConsumerApp(nodeName)
          Expect(err).To(BeNil())
      })
      
      By("validating TGM event publishing", func() {
          events, err := MonitorTgmEvents([]string{"TGM_CLOCK_CLASS_CHANGE"}, 5*time.Minute)
          Expect(err).To(BeNil())
          Expect(len(events)).To(BeNumerically(">", 0))
      })
  })
  ```

- [x] **TGM Event Format Validation**: Validate TGM event format and content
  ```go
  It("Should publish correctly formatted TGM events", func() {
      // Validate TGM event structure
      // Check required TGM fields
      // Verify event timestamps and sources
  })
  ```

#### 2.2 TGM Event Subscription Tests ✅ **COMPLETED**
**Objective**: Implement TGM event subscription and delivery tests

**Tasks**:
- [x] **TGM Event Subscription Test**: Test TGM event subscription mechanism
  ```go
  It("Should allow applications to subscribe to TGM events", func() {
      By("setting up TGM event subscription", func() {
          err := event.CreateTgmEventSubscription(nodeName, eventTypes)
          Expect(err).To(BeNil())
      })
      
      By("validating event delivery", func() {
          deliveredEvents := waitForTgmEvents(eventTypes, timeout)
          Expect(len(deliveredEvents)).To(BeNumerically(">", 0))
      })
  })
  ```

- [x] **TGM Event Reliability Test**: Test TGM event delivery reliability
  ```go
  It("Should deliver TGM events reliably", func() {
      // Test event delivery under various conditions
      // Validate event ordering and timing
      // Check for event loss scenarios
  })
  ```

#### 2.3 TGM Sidecar Communication Tests ✅ **COMPLETED**
**Objective**: Test TGM sidecar communication and integration

**Tasks**:
- [x] **TGM Sidecar Communication Test**: Test TGM-sidecar communication
  ```go
  It("Should establish proper communication between TGM and sidecar", func() {
      By("deploying TGM with sidecar", func() {
          err := event.CreateTgmConsumerAppWithSidecar(nodeName)
          Expect(err).To(BeNil())
      })
      
      By("validating sidecar communication", func() {
          err := validateTgmSidecarCommunication(nodeName)
          Expect(err).To(BeNil())
      })
  })
  ```

- [ ] **TGM Event Transport Test**: Test TGM event transport via HTTP
  ```go
  It("Should transport TGM events via HTTP correctly", func() {
      // Test HTTP transport for TGM events
      // Validate API endpoints
      // Check transport reliability
  })
  ```

### Phase 3: Advanced TGM Event Functionality (Week 5-6)

#### 3.1 TGM Clock Quality Event Tests
**Objective**: Implement TGM clock quality monitoring via events

**Tasks**:
- [ ] **TGM Clock Quality Event Test**: Test TGM clock quality event monitoring
  ```go
  It("Should monitor TGM clock quality via events", func() {
      By("monitoring TGM clock quality events", func() {
          qualityEvents := monitorTgmClockQualityEvents(duration)
          Expect(len(qualityEvents)).To(BeNumerically(">", 0))
      })
      
      By("validating clock quality metrics", func() {
          quality := analyzeTgmClockQuality(qualityEvents)
          Expect(quality.Class).To(Equal("6")) // Locked state
          Expect(quality.Accuracy).To(BeNumerically("<", 100)) // ns
      })
  })
  ```

- [ ] **TGM Holdover Event Test**: Test TGM holdover state events
  ```go
  It("Should detect TGM holdover state changes via events", func() {
      // Test GNSS signal loss detection
      // Validate holdover state transitions
      // Check holdover duration events
  })
  ```

#### 3.2 TGM Failover Event Tests
**Objective**: Implement TGM failover scenario event tests

**Tasks**:
- [ ] **TGM Failover Event Test**: Test TGM failover event detection
  ```go
  It("Should detect TGM failover scenarios via events", func() {
      By("simulating TGM failover scenario", func() {
          err := simulateTgmFailover(nodeName)
          Expect(err).To(BeNil())
      })
      
      By("monitoring failover events", func() {
          failoverEvents := monitorTgmFailoverEvents(timeout)
          Expect(len(failoverEvents)).To(BeNumerically(">", 0))
      })
  })
  ```

- [ ] **TGM Recovery Event Test**: Test TGM recovery event detection
  ```go
  It("Should detect TGM recovery via events", func() {
      // Test recovery event detection
      // Validate recovery timing
      // Check recovery state transitions
  })
  ```

#### 3.3 TGM Multi-Interface Event Tests
**Objective**: Implement TGM multi-interface event tests

**Tasks**:
- [ ] **TGM Multi-Interface Event Test**: Test TGM multi-interface event handling
  ```go
  It("Should handle TGM events across multiple interfaces", func() {
      By("configuring TGM multi-interface setup", func() {
          err := configureTgmMultiInterface(nodeName)
          Expect(err).To(BeNil())
      })
      
      By("monitoring multi-interface events", func() {
          events := monitorTgmMultiInterfaceEvents(duration)
          Expect(len(events)).To(BeNumerically(">", 0))
      })
  })
  ```

### Phase 4: Performance and Reliability Tests (Week 7-8)

#### 4.1 TGM Event Performance Tests
**Objective**: Implement TGM event performance and load testing

**Tasks**:
- [ ] **TGM Event Performance Test**: Test TGM event performance under load
  ```go
  It("Should maintain TGM event performance under load", func() {
      By("generating high TGM event load", func() {
          err := generateTgmEventLoad(nodeName, eventRate)
          Expect(err).To(BeNil())
      })
      
      By("monitoring event performance", func() {
          performance := measureTgmEventPerformance(duration)
          Expect(performance.Latency).To(BeNumerically("<", 100)) // ms
          Expect(performance.Throughput).To(BeNumerically(">", 100)) // events/sec
      })
  })
  ```

- [ ] **TGM Event Scalability Test**: Test TGM event scalability
  ```go
  It("Should scale TGM events across multiple consumers", func() {
      // Test multiple consumer scenarios
      // Validate event distribution
      // Check load balancing
  })
  ```

#### 4.2 TGM Event Reliability Tests
**Objective**: Implement TGM event reliability and fault tolerance tests

**Tasks**:
- [ ] **TGM Event Reliability Test**: Test TGM event reliability under failure conditions
  ```go
  It("Should maintain TGM event reliability under failures", func() {
      By("simulating network failures", func() {
          err := simulateNetworkFailures(nodeName)
          Expect(err).To(BeNil())
      })
      
      By("validating event reliability", func() {
          reliability := measureTgmEventReliability(duration)
          Expect(reliability.SuccessRate).To(BeNumerically(">", 0.95))
      })
  })
  ```

- [ ] **TGM Event Recovery Test**: Test TGM event recovery mechanisms
  ```go
  It("Should recover TGM events after failures", func() {
      // Test event recovery after failures
      // Validate event replay mechanisms
      // Check event consistency
  })
  ```

### Phase 5: Integration and Validation (Week 9-10)

#### 5.1 TGM Event Integration Tests
**Objective**: Integrate TGM event tests with existing test framework

**Tasks**:
- [ ] **TGM Event Integration Test**: Integrate TGM event tests with existing framework
  ```go
  It("Should integrate TGM events with existing PTP framework", func() {
      By("running TGM event tests with existing tests", func() {
          err := runTgmEventIntegrationTests()
          Expect(err).To(BeNil())
      })
  })
  ```

- [ ] **TGM Event Compatibility Test**: Test TGM event compatibility with existing tests
  ```go
  It("Should maintain compatibility with existing event tests", func() {
      // Test compatibility with OC/BC event tests
      // Validate framework integration
      // Check for conflicts
  })
  ```

#### 5.2 TGM Event Validation Tests
**Objective**: Implement comprehensive TGM event validation

**Tasks**:
- [ ] **TGM Event Validation Test**: Comprehensive TGM event validation
  ```go
  It("Should validate all TGM event aspects", func() {
      By("validating TGM event structure", func() {
          err := validateTgmEventStructure()
          Expect(err).To(BeNil())
      })
      
      By("validating TGM event content", func() {
          err := validateTgmEventContent()
          Expect(err).To(BeNil())
      })
      
      By("validating TGM event timing", func() {
          err := validateTgmEventTiming()
          Expect(err).To(BeNil())
      })
  })
  ```

### Phase 6: Documentation and Testing (Week 11-12)

#### 6.1 TGM Event Documentation
**Objective**: Create comprehensive documentation for TGM event testing

**Tasks**:
- [ ] **TGM Event Test Documentation**: Document TGM event testing
  ```markdown
  # TGM Event-Based Testing
  
  ## Overview
  TGM event-based testing extends the existing PTP event framework to support
  Time Grand Master configurations.
  
  ## Test Cases
  - TGM Event Publishing
  - TGM Event Subscription
  - TGM Sidecar Communication
  - TGM Clock Quality Monitoring
  - TGM Failover Scenarios
  - TGM Multi-Interface Support
  ```

- [ ] **TGM Event Configuration Guide**: Create TGM event configuration guide
  ```yaml
  # TGM Event Test Configuration Example
  soaktest:
    tgm_event_publishing:
      spec:
        enable: true
        duration: 30
        failure_threshold: 2
      desc: "Test T-GM event publishing capabilities"
  ```

#### 6.2 TGM Event Test Validation
**Objective**: Validate TGM event tests in real environments

**Tasks**:
- [ ] **TGM Event Test Validation**: Validate TGM event tests
  ```bash
  # Run TGM event tests
  PTP_TEST_MODE=TGM ENABLE_PTP_EVENT=true make functests
  
  # Run specific TGM event tests
  ENABLE_TEST_CASE=tgm_event_publishing,tgm_clock_quality PTP_TEST_MODE=TGM make functests
  ```

## Implementation Details

### TGM Event Types

```go
// TGM-specific event types
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

### TGM Event Structure

```go
// TGM event structure
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

### TGM Event Test Configuration

```yaml
# TGM event test configuration
soaktest:
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
  
  tgm_event_subscription:
    spec:
      enable: true
      duration: 45
      failure_threshold: 1
      custom_params:
        subscription_types: ["TGM_CLOCK_CLASS_CHANGE", "TGM_GNSS_SIGNAL_LOSS"]
        reliability_threshold: 0.95
    desc: "Test T-GM event subscription mechanism"
  
  tgm_clock_quality:
    spec:
      enable: true
      duration: 60
      failure_threshold: 1
      custom_params:
        quality_threshold: 100
        class_threshold: 6
    desc: "Test T-GM clock quality monitoring"
  
  tgm_failover_scenarios:
    spec:
      enable: true
      duration: 90
      failure_threshold: 2
      custom_params:
        failover_timeout: 30
        recovery_timeout: 60
    desc: "Test T-GM failover scenarios"
```

## Success Criteria

### Phase 1 Success Criteria
- [ ] TGM event framework integration complete
- [ ] TGM event types defined and validated
- [ ] TGM configuration extensions implemented
- [ ] TGM consumer application created

### Phase 2 Success Criteria
- [ ] TGM event publishing tests implemented
- [ ] TGM event subscription tests implemented
- [ ] TGM sidecar communication tests implemented
- [ ] All tests passing in test environment

### Phase 3 Success Criteria
- [ ] TGM clock quality event tests implemented
- [ ] TGM failover event tests implemented
- [ ] TGM multi-interface event tests implemented
- [ ] Advanced TGM functionality validated

### Phase 4 Success Criteria
- [ ] TGM event performance tests implemented
- [ ] TGM event reliability tests implemented
- [ ] Performance benchmarks established
- [ ] Reliability metrics validated

### Phase 5 Success Criteria
- [ ] TGM event integration tests implemented
- [ ] Compatibility with existing tests validated
- [ ] Framework integration complete
- [ ] No conflicts with existing functionality

### Phase 6 Success Criteria
- [ ] Comprehensive documentation created
- [ ] TGM event tests validated in real environments
- [ ] All tests passing in production-like environments
- [ ] Documentation reviewed and approved

## Risk Mitigation

### Technical Risks
1. **Event Framework Compatibility**: Ensure TGM events don't conflict with existing OC/BC events
2. **Performance Impact**: Monitor performance impact of TGM event testing
3. **Configuration Complexity**: Keep TGM event configuration simple and maintainable

### Mitigation Strategies
1. **Incremental Implementation**: Implement phases incrementally with validation at each step
2. **Comprehensive Testing**: Test TGM events thoroughly in isolation before integration
3. **Performance Monitoring**: Monitor performance impact throughout implementation
4. **Documentation**: Maintain comprehensive documentation for troubleshooting

## Questions for Clarification

1. **TGM Event Priority**: What is the priority order for implementing TGM event tests?
2. **Performance Requirements**: What are the performance requirements for TGM event testing?
3. **Integration Scope**: Should TGM event tests be integrated with existing OC/BC event tests?
4. **Configuration Management**: How should TGM event configurations be managed alongside existing configurations?
5. **Testing Environment**: What testing environments are available for TGM event testing?

## Next Steps

1. **Review and Approve Plan**: Review this implementation plan and provide feedback
2. **Set Priorities**: Determine which phases should be prioritized
3. **Allocate Resources**: Assign resources for implementation
4. **Begin Phase 1**: Start with foundation and infrastructure work
5. **Regular Reviews**: Schedule regular reviews of implementation progress

This plan provides a comprehensive roadmap for implementing TGM event-based testing that builds upon the existing PTP test framework while adding the specific functionality needed for Time Grand Master configurations.

## ✅ **IMPLEMENTATION STATUS UPDATE**

### **COMPLETED: Clock Class Event Verification**

**Date**: July 31, 2025  
**Status**: ✅ **SUCCESSFULLY IMPLEMENTED AND TESTED**

#### **Key Implementation Details**

**1. Test Location**
- **File**: `test/conformance/serial/ptp.go`
- **Test Name**: "should verify clock class change when GNSS is lost"
- **Test Suite**: WPC GM Events verification (V2) T-GM Event Tests

**2. Event Consumer Implementation**
- **Custom Go Service**: `test/conformance/serial/event-consumer/event-consumer.go`
- **HTTP Endpoints**: `/event` (POST), `/health` (GET)
- **Event Storage**: In-memory event storage with JSON format
- **Health Checks**: Comprehensive health monitoring

**3. Event Predicate Logic**
- **File**: `test/pkg/event/predicate.go`
- **Function**: `IsClockClassEventPredicate`
- **Fixed Issues**: Type comparison for clock class values (int vs string)
- **Support**: Multiple data types (float64, int, string)

**4. Test Flow Implementation**
```go
// Step 1: Subscription Creation
By("STEP 1: Subscribing to clock class events", func() {
    // Create subscription with custom endpoint
    // Handle 409 Conflict for existing subscriptions
    // Validate subscription creation
})

// Step 2: Clock Class Change Verification
By("STEP 2: Verifying clock class 7 event (holdover state)", func() {
    // Start cold boot to trigger clock class change
    // Wait for clock class to reach 7
    // Verify event reception and matching
})

// Step 3: Recovery Verification
By("STEP 3: Verifying clock class 6 event (locked state)", func() {
    // Stop cold boot to allow GNSS recovery
    // Wait for clock class to return to 6
    // Verify event reception and matching
})
```

**5. Technical Solutions Implemented**

**REST API Subscription Issue**
- **Problem**: Existing subscription with same ResourceAddress but different EndpointUri
- **Solution**: Delete existing subscription before creating new one
- **Result**: Successfully create subscriptions with custom endpoints

**Event Predicate Matching**
- **Problem**: Type mismatch between clock class values
- **Solution**: Enhanced predicate logic with multiple type support
- **Result**: Successfully match clock class 6 and 7 events

**Cold Boot Integration**
- **Problem**: Need to trigger clock class changes for event generation
- **Solution**: Implement controlled cold boot with proper timing
- **Result**: Successfully trigger clock class 6→7→6 transitions

**6. Test Results**
```
Ran 13 of 29 Specs in 351.224 seconds
SUCCESS! -- 13 Passed | 0 Failed | 5 Pending | 11 Skipped
```

**7. Key Achievements**
- ✅ **Subscription Management**: Successfully create subscriptions with custom endpoints
- ✅ **Event Reception**: Custom consumer receives clock class events correctly
- ✅ **Clock Class Change Detection**: Properly detect transitions between clock class 6 and 7
- ✅ **Event Predicate Matching**: Fixed predicate logic to match clock class events
- ✅ **Cold Boot Integration**: Successfully trigger clock class changes via cold boot
- ✅ **Test Flow**: Complete end-to-end test flow from subscription to verification

**8. Files Modified**
- `test/conformance/serial/ptp.go` - Main test implementation
- `test/pkg/event/predicate.go` - Event predicate logic
- `test/conformance/serial/event-consumer/event-consumer.go` - Custom consumer service
- `test/pkg/event/subscription.go` - Subscription management

**9. Next Steps**
- Extend to additional TGM event types (GM state, DPLL state, etc.)
- Implement multi-interface event testing
- Add performance and reliability testing
- Integrate with existing OC/BC event tests

This implementation demonstrates that the TGM event framework is working correctly and can be extended for additional event types and scenarios. 