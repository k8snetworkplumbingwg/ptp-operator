//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"fmt"
	"strings"

	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	ptptestconfig "github.com/k8snetworkplumbingwg/ptp-operator/test/conformance/config"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/logging"

	ptphelper "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptphelper"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/testconfig"
)

var junitPath *string
var DeletePtpConfig bool

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func TestTest(t *testing.T) {
	logging.InitLogLevel()
	RegisterFailHandler(Fail)
	suiteCfg, repCfg := GinkgoConfiguration()
	repCfg.SilenceSkips = true
	RunSpecs(t, "PTP e2e tests : Parallel", suiteCfg, repCfg)
}

var _ = SynchronizedBeforeSuite(func() []byte {
	// Get the test config parameters
	testParameters, err := ptptestconfig.GetPtpTestConfig()
	Expect(err).To(BeNil(), "Failed to get Test Config")

	if testParameters.SoakTestConfig.DisableSoakTest {
		Skip("Soak testing is disabled at the configuration file. Hence, skipping!")
	}

	// Run on all Ginkgo nodes
	logrus.Info("Executed from parallel suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())

	// Prefer reusing configs already installed by the serial suite for this mode.
	// CreatePtpConfigurations always clean.All() first; when parallel overlaps
	// serial on the same cluster that wipe deletes PtpConfigs mid-serial and
	// cascades into nil-pod panics (seen on tgm-serial vs tgm-parallel).
	desired := testconfig.GetDesiredConfig(false)
	existing := testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
	reusedSerialConfigs := existing.Status == testconfig.DiscoverySuccessStatus &&
		existing.PtpModeDiscovered == desired.PtpModeDesired &&
		existing.DiscoveredClockUnderTestPod != nil
	if reusedSerialConfigs {
		logrus.Infof("Reusing existing PTP configs for mode %s (skipping clean/create)", desired.PtpModeDesired)
		fullConfig = existing
	} else {
		err = testconfig.CreatePtpConfigurationsWithRetry(3)
		if err != nil {
			// Only topology "no … solution found" is a Skip (insufficient fabric).
			// Operator/API/apply failures must still Fail BeforeSuite.
			if strings.Contains(err.Error(), "no solution found") ||
				strings.Contains(err.Error(), "no T-BC solution found") {
				Skip(fmt.Sprintf("Could not create a ptp config (insufficient topology), err=%s", err))
			}
			Fail(fmt.Sprintf("Could not create a ptp config, err=%s", err))
		}
		By("Refreshing configuration", func() {
			ptphelper.WaitForPtpDaemonToExist()
			fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
		})
	}
	Expect(fullConfig.Status).To(Equal(testconfig.DiscoverySuccessStatus), "parallel suite requires successful PTP discovery")
	Expect(fullConfig.DiscoveredClockUnderTestPod).NotTo(BeNil(),
		"clock-under-test pod missing; label node with "+pkg.PtpClockUnderTestNodeLabel)
	// Avoid RestartPTPDaemon when reusing serial configs: a daemon bounce mid-serial
	// races the same way as clean.All. Only restart after we created configs.
	if !reusedSerialConfigs {
		ptphelper.RestartPTPDaemon()
	}

	isConsumerReady := true
	apiVersion := event.GetDefaultApiVersion()
	err = ptphelper.EnablePTPEvent(apiVersion, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
	Expect(err).To(BeNil(), "Error when enable ptp event")
	if apiVersion == "1.0" {
		logrus.Info("Deploy consumer app with sidecar for testing event API v1")
		err = event.CreateConsumerAppWithSidecar(fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
		if err != nil {
			logrus.Errorf("PTP events are not available due to consumer app/sidecar creation error err=%s", err)
			isConsumerReady = false
		}
	} else {
		logrus.Info("Deploy consumer app without sidecar for testing event API v2")
		err = event.CreateConsumerApp(fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
		if err != nil {
			logrus.Errorf("PTP events are not available due to consumer app creation error err=%s", err)
			isConsumerReady = false
		}
	}
	// stops the event listening framework
	DeferCleanup(func() {
		err = event.DeleteConsumerNamespace()
		if err != nil {
			logrus.Debugf("Deleting consumer namespace failed because of err=%s", err)
		}
	})

	logrus.Debugf("lib.Ps=%v", event.PubSub)
	return []byte(fmt.Sprintf("%t,%p", isConsumerReady, event.PubSub))
}, func(data []byte) {
	values := strings.Split(string(data), ",")
	testclient.Client = testclient.New("")
	isConsumerReady := false
	if string(values[0]) == "true" {
		isConsumerReady = true
	}

	// this is executed once per thread/test
	By("Refreshing configuration", func() {
		ptphelper.WaitForPtpDaemonToExist()
		fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
		fullConfig.PtpEventsIsConsumerReady = isConsumerReady
	})
	Expect(fullConfig.Status).To(Equal(testconfig.DiscoverySuccessStatus), "parallel suite requires successful PTP discovery")
	Expect(fullConfig.DiscoveredClockUnderTestPod).NotTo(BeNil(),
		"clock-under-test pod missing; label node with "+pkg.PtpClockUnderTestNodeLabel)
})
var _ = AfterSuite(func() {
	// Do not clean.All() here. Parallel often overlaps the serial suite for the
	// same PTP_TEST_MODE on one cluster; wiping configs from AfterSuite deletes
	// serial's PtpConfigs mid-run. Serial AfterSuite / next mode's create path
	// owns cleanup.
	if DeletePtpConfig {
		logrus.Info("parallel AfterSuite: skipping clean.All() to avoid racing serial suite")
	}
})
