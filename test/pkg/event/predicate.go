package event

import (
	"encoding/json"
	"fmt"
	fbprotocol "github.com/facebook/time/ptp/protocol"
	"time"

	"github.com/sirupsen/logrus"
)

// CloudEvent represents the structure of PTP Cloud Events
type CloudEvent struct {
	SpecVersion string    `json:"specversion"`
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Type        string    `json:"type"`
	Time        string    `json:"time"`
	Data        EventData `json:"data"`
}

type EventData struct {
	Version string       `json:"version"`
	Values  []EventValue `json:"values"`
}

type EventValue struct {
	ResourceAddress string      `json:"ResourceAddress"`
	DataType        string      `json:"data_type"`
	ValueType       string      `json:"value_type"`
	Value           interface{} `json:"value"`
}

// EventPredicate defines a function type for checking if an event matches certain criteria
type EventPredicate func(*CloudEvent) bool

// CreateEventPredicate creates a predicate function for checking events based on source, type, and value criteria
func CreateEventPredicate(source string, eventType string, valueChecks ...func(EventValue) bool) EventPredicate {
	return func(event *CloudEvent) bool {
		// Check source and type
		if event.Source != source || event.Type != eventType {
			return false
		}
		// If no value checks provided, just check source and type
		if len(valueChecks) == 0 {
			return true
		}
		// Check each value against the provided predicates
		for _, value := range event.Data.Values {
			for _, check := range valueChecks {
				if check(value) {
					return true
				}
			}
		}
		return false
	}
}

// Helper functions for common value checks
func HasMetricValue(expectedValue string) func(EventValue) bool {
	return func(value EventValue) bool {
		return value.DataType == "metric" && fmt.Sprintf("%v", value.Value) == expectedValue
	}
}

// IsClockClassEventPredicate returns a predicate that checks if an event is a clock class event
func IsClockClassEventPredicate(class fbprotocol.ClockClass) EventPredicate {
	return func(event *CloudEvent) bool {
		logrus.Debugf("Checking clock class %s predicate for event - Type: %s, Source: %s", string(class), event.Type, event.Source)

		// Check if it's a PTP clock class change event
		if event.Type != "event.sync.ptp-status.ptp-clock-class-change" {
			logrus.Debugf("Event type mismatch - expected: event.sync.ptp-status.ptp-clock-class-change, got: %s", event.Type)
			return false
		}

		// Check if it's a clock class event
		if event.Source != "/sync/ptp-status/clock-class" {
			logrus.Debugf("Event source mismatch - expected: /sync/ptp-status/clock-class, got: %s", event.Source)
			return false
		}

		logrus.Debugf("Event data - Version: %s, Values count: %d", event.Data.Version, len(event.Data.Values))

		// Check each value in the event data
		for i, value := range event.Data.Values {
			logrus.Debugf("Checking value %d - ResourceAddress: %s, DataType: %s, ValueType: %s, Value: %v",
				i+1, value.ResourceAddress, value.DataType, value.ValueType, value.Value)

			// Check if this is a metric value with the expected clock class
			if value.DataType == "metric" {
				// Try to convert the value to a number for comparison
				switch v := value.Value.(type) {
				case float64:
					if int(v) == int(class) {
						logrus.Infof("Found clock class %d event!", int(class))
						return true
					}
				case int:
					if v == int(class) {
						logrus.Infof("Found clock class %d event!", int(class))
						return true
					}
				case string:
					if v == fmt.Sprintf("%d", int(class)) {
						logrus.Infof("Found clock class %d event!", int(class))
						return true
					}
				default:
					// Try string comparison as fallback
					if fmt.Sprintf("%v", value.Value) == fmt.Sprintf("%d", int(class)) {
						logrus.Infof("Found clock class %d event!", int(class))
						return true
					}
				}
			}
		}

		logrus.Debugf("No clock class %s value found in event", string(class))
		return false
	}
}

// WaitForEventAfterTimeWithPredicate waits for an event matching the predicate after a given time
// readEventsFromLogAfterTime must be provided by the caller (e.g., as a closure or via dependency injection)
func WaitForEventAfterTimeWithPredicate(
	readEventsFromLogAfterTime func(time.Time) ([]string, error),
	since time.Time,
	predicate EventPredicate,
	timeout, pollInterval time.Duration,
) (*CloudEvent, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events, err := readEventsFromLogAfterTime(since)
		if err != nil {
			return nil, err
		}
		for _, eventJSON := range events {
			var cloudEvent CloudEvent
			if err := json.Unmarshal([]byte(eventJSON), &cloudEvent); err != nil {
				logrus.Warnf("Failed to parse event JSON: %v", err)
				continue
			}
			if predicate(&cloudEvent) {
				return &cloudEvent, nil
			}
		}
		time.Sleep(pollInterval)
	}
	return nil, fmt.Errorf("event not found within timeout")
}

// GetLatestEvent reads all events from the log and returns the most recent one
func GetLatestEvent(readEventsFromLog func() ([]string, error)) (*CloudEvent, error) {
	events, err := readEventsFromLog()
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no events found")
	}
	// Parse the last event
	var latestEvent CloudEvent
	err = json.Unmarshal([]byte(events[len(events)-1]), &latestEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal latest event: %v", err)
	}
	return &latestEvent, nil
}
