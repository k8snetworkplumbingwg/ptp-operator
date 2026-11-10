package hardwareconfig

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
)

var hardwareConfigGVK = schema.GroupVersionKind{
	Group:   "ptp.openshift.io",
	Version: "v2alpha1",
	Kind:    "HardwareConfig",
}

var hardwareConfigListGVK = schema.GroupVersionKind{
	Group:   "ptp.openshift.io",
	Version: "v2alpha1",
	Kind:    "HardwareConfigList",
}

const tbcTemplate = `apiVersion: ptp.openshift.io/v2alpha1
kind: HardwareConfig
metadata:
  name: %s
  namespace: %s
spec:
  relatedPtpProfileName: %s
  profile:
    name: tbc
    clockChain:
      behavior:
        conditions:
        - desiredStates:
          - sysfs:
              path: /sys/class/net/{interface}/device/ptp/ptp*/pins/SMA2
              value: "2 2"
              sourceName: PTP
          - sysfs:
              path: /sys/class/net/{interface}/device/ptp/ptp*/period
              value: "2 0 0 1 0"
              sourceName: PTP
          - dpll:
              subsystem: leader
              boardLabel: GNSS-1PPS
              eec:
                priority: 255
              pps:
                priority: 255
          - dpll:
              subsystem: leader
              boardLabel: CVL-SDP20
              eec:
                priority: 255
              pps:
                priority: 255
          - dpll:
              subsystem: leader
              boardLabel: CVL-SDP21
              eec:
                state: disconnected
              pps:
                state: disconnected
          name: Initialize T-BC
          triggers:
          - conditionType: init
            sourceName: PTP
        - desiredStates:
          - dpll:
              subsystem: leader
              boardLabel: CVL-SDP22
              eec:
                priority: 255
              pps:
                priority: 0
          - dpll:
              subsystem: leader
              boardLabel: CVL-SDP23
              eec:
                state: disconnected
              pps:
                state: disconnected
          name: PTP Source Active
          triggers:
          - conditionType: locked
            sourceName: PTP
        - desiredStates:
          - dpll:
              subsystem: leader
              boardLabel: CVL-SDP22
              eec:
                priority: 255
              pps:
                priority: 255
          - dpll:
              subsystem: leader
              boardLabel: CVL-SDP23
              eec:
                state: connected
              pps:
                state: connected
          name: PTP Source Lost - Leader Holdover
          triggers:
          - conditionType: lost
            sourceName: PTP
        sources:
        - boardLabel: CVL-SDP22
          subsystem: leader
          name: PTP
          ptpTimeReceivers:
          - %s
          sourceType: ptpTimeReceiver
      commonDefinitions:
        eSyncDefinitions:
        - name: esync-10-1
          eSyncConfig:
            transferFrequency: 10000000
            embeddedSyncFrequency: 1
            dutyCyclePct: 25
      structure:
      - dpll:
          networkInterface: %s
          holdoverParameters:
            maxInSpecOffset: %d
            localMaxHoldoverOffset: %d
            localHoldoverTimeout: %d
          phaseInputs:
            CVL_SDP22:
              description: PTP time receiver input
              frequency: 1
          phaseOutputs:
            REF-SMA1:
              connector: SMA1
              description: Output to the follower
              eSyncConfigName: esync-10-1
        ethernet:
        - ports:
%s
        hardwareSpecificDefinitions: intel/e810
        name: leader
`

const tgmTemplate = `apiVersion: ptp.openshift.io/v2alpha1
kind: HardwareConfig
metadata:
  name: %s
  namespace: %s
spec:
  relatedPtpProfileName: %s
  profile:
    name: tgm
    clockType: T-GM
    clockChain:
      structure:
      - dpll:
          networkInterface: %s
          holdoverParameters:
            maxInSpecOffset: %d
            localMaxHoldoverOffset: %d
            localHoldoverTimeout: %d
        ethernet:
        - ports:
%s
        hardwareSpecificDefinitions: intel/e810
        name: leader
`

// CreateTBC creates the HardwareConfig for a WPC T-BC profile. It is used on
// releases whose PTP operator consumes the v2alpha1 HardwareConfig API instead
// of the legacy e810 plugin settings in the PtpConfig.
func CreateTBC(configName, relatedProfile, leaderIface, ptpReceiverIface string, ports []string, maxInSpecOffset, localMaxHoldoverOffset, localHoldoverTimeout int) error {
	portsYAML := formatPorts(ports)
	data := fmt.Sprintf(tbcTemplate,
		configName+"-hwconfig",
		pkg.PtpLinuxDaemonNamespace,
		relatedProfile,
		ptpReceiverIface,
		leaderIface,
		maxInSpecOffset,
		localMaxHoldoverOffset,
		localHoldoverTimeout,
		portsYAML,
	)
	return apply(configName+"-hwconfig", data)
}

// CreateTGM creates the HardwareConfig for a WPC T-GM profile.
func CreateTGM(configName, relatedProfile, leaderIface string, ports []string, maxInSpecOffset, localMaxHoldoverOffset, localHoldoverTimeout int) error {
	portsYAML := formatPorts(ports)
	data := fmt.Sprintf(tgmTemplate,
		configName+"-hwconfig",
		pkg.PtpLinuxDaemonNamespace,
		relatedProfile,
		leaderIface,
		maxInSpecOffset,
		localMaxHoldoverOffset,
		localHoldoverTimeout,
		portsYAML,
	)
	return apply(configName+"-hwconfig", data)
}

func formatPorts(ports []string) string {
	var b strings.Builder
	for _, port := range ports {
		if port == "" {
			continue
		}
		fmt.Fprintf(&b, "          - %s\n", port)
	}
	return b.String()
}

func apply(name, data string) error {
	ctx := context.Background()

	// Remove an existing object so a re-created mode starts from the new settings.
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(hardwareConfigGVK)
	existing.SetName(name)
	existing.SetNamespace(pkg.PtpLinuxDaemonNamespace)
	if err := client.Client.Client.Delete(ctx, existing); err != nil {
		logrus.Debugf("hardwareconfig: no existing %s to delete: %v", name, err)
	}

	var object map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &object); err != nil {
		return fmt.Errorf("failed to unmarshal HardwareConfig %s: %w", name, err)
	}

	hw := &unstructured.Unstructured{Object: object}
	hw.SetGroupVersionKind(hardwareConfigGVK)
	if err := client.Client.Client.Create(ctx, hw); err != nil {
		return fmt.Errorf("failed to create HardwareConfig %s: %w", name, err)
	}
	logrus.Infof("Created HardwareConfig %s for profile %s", name, object["spec"])
	return nil
}

// MutateHoldover updates the leading clock holdover parameters of the
// HardwareConfig related to the given PtpConfig profile. It returns a cleanup
// function that restores the original object.
func MutateHoldover(relatedProfile string, maxInSpecOffset, localMaxHoldoverOffset, localHoldoverTimeout int) (func(), error) {
	ctx := context.Background()
	hw, err := findByRelatedProfile(ctx, relatedProfile)
	if err != nil {
		return nil, err
	}
	original := hw.DeepCopy()

	structure, found, err := unstructured.NestedSlice(hw.Object, "spec", "profile", "clockChain", "structure")
	if err != nil || !found {
		return nil, fmt.Errorf("HardwareConfig for profile %s has no clock chain structure: %w", relatedProfile, err)
	}

	for i, entry := range structure {
		subsystem, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		dpll, found, err := unstructured.NestedMap(subsystem, "dpll")
		if err != nil || !found {
			continue
		}
		dpll["holdoverParameters"] = map[string]interface{}{
			"maxInSpecOffset":        int64(maxInSpecOffset),
			"localMaxHoldoverOffset": int64(localMaxHoldoverOffset),
			"localHoldoverTimeout":   int64(localHoldoverTimeout),
		}
		subsystem["dpll"] = dpll
		structure[i] = subsystem
	}

	if err := unstructured.SetNestedSlice(hw.Object, structure, "spec", "profile", "clockChain", "structure"); err != nil {
		return nil, fmt.Errorf("failed to update HardwareConfig structure: %w", err)
	}
	if err := client.Client.Client.Update(ctx, hw); err != nil {
		return nil, fmt.Errorf("failed to update HardwareConfig for profile %s: %w", relatedProfile, err)
	}
	logrus.Infof("Updated HardwareConfig holdover parameters for profile %s: maxInSpec=%d localMax=%d timeout=%d",
		relatedProfile, maxInSpecOffset, localMaxHoldoverOffset, localHoldoverTimeout)

	cleanup := func() {
		current, err := findByRelatedProfile(context.Background(), relatedProfile)
		if err != nil {
			logrus.Errorf("Failed to find HardwareConfig for restore: %v", err)
			return
		}
		current.Object["spec"] = original.Object["spec"]
		if err := client.Client.Client.Update(context.Background(), current); err != nil {
			logrus.Errorf("Failed to restore HardwareConfig for profile %s: %v", relatedProfile, err)
		}
	}
	return cleanup, nil
}

func findByRelatedProfile(ctx context.Context, relatedProfile string) (*unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(hardwareConfigListGVK)
	if err := client.Client.Client.List(ctx, list, crclient.InNamespace(pkg.PtpLinuxDaemonNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list HardwareConfigs: %w", err)
	}
	for i := range list.Items {
		item := &list.Items[i]
		related, _, _ := unstructured.NestedString(item.Object, "spec", "relatedPtpProfileName")
		if related == relatedProfile {
			item.SetGroupVersionKind(hardwareConfigGVK)
			return item, nil
		}
	}
	return nil, fmt.Errorf("no HardwareConfig found for related PTP profile %s", relatedProfile)
}

// DeleteTestConfigs removes HardwareConfigs created by the test suite. It is a
// no-op (with a debug log) on releases where the API is not installed.
func DeleteTestConfigs() {
	ctx := context.Background()
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(hardwareConfigListGVK)
	if err := client.Client.Client.List(ctx, list, crclient.InNamespace(pkg.PtpLinuxDaemonNamespace)); err != nil {
		logrus.Debugf("hardwareconfig: unable to list test HardwareConfigs: %v", err)
		return
	}
	for i := range list.Items {
		item := &list.Items[i]
		item.SetGroupVersionKind(hardwareConfigGVK)
		if !strings.HasPrefix(item.GetName(), "test-") {
			continue
		}
		if err := client.Client.Client.Delete(ctx, item); err != nil {
			logrus.Infof("hardwareconfig: failed to delete %s: %v", item.GetName(), err)
		}
	}
}
