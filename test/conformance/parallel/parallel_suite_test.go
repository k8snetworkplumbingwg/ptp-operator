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

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	ptptestconfig "github.com/k8snetworkplumbingwg/ptp-operator/test/conformance/config"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/clean"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/logging"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/metrics"

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

	// discovers valid ptp configurations based on clock type
	err = testconfig.CreatePtpConfigurationsWithRetry(3)
	Expect(err).To(BeNil(), "Could not create a ptp config")

	By("Refreshing configuration", func() {
		ptphelper.WaitForPtpDaemonToExist()
		fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
	})
	Expect(fullConfig.Status).To(Equal(testconfig.DiscoverySuccessStatus), "parallel suite requires successful PTP discovery")
	Expect(fullConfig.DiscoveredClockUnderTestPod).NotTo(BeNil(),
		"clock-under-test pod missing; label node with "+pkg.PtpClockUnderTestNodeLabel)
	ptphelper.RestartPTPDaemon()

	By("Waiting for ptp4l to synchronize before starting soak tests")
	if fullConfig.DiscoveredClockUnderTestPtpConfig == nil {
		logrus.Warn("DiscoveredClockUnderTestPtpConfig is nil — skipping sync wait")
	} else {
		ptpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
		slaveIfs := ptpv1.GetInterfaces(*ptpConfig, ptpv1.Slave)
		if len(slaveIfs) > 0 {
			if !ptphelper.IsExternalGM() {
				aLabel := pkg.PtpGrandmasterNodeLabel
				Eventually(func() error {
					_, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
					return err
				}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeNil(),
					"Timeout waiting for grandmaster clock ID before soak tests")
			}
			slaveRoles := make([]metrics.MetricRole, len(slaveIfs))
			for i := range slaveRoles {
				slaveRoles[i] = metrics.MetricRoleSlave
			}
			Eventually(func() error {
				return metrics.CheckClockRole(slaveRoles, slaveIfs, &fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
			}, pkg.TimeoutIn5Minutes, 5*pkg.Timeout1Seconds).Should(BeNil(),
				"Clock-under-test slave interfaces must reach SLAVE state before soak tests start")
			logrus.Info("ptp4l synchronized — starting soak tests")
		}
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
	if DeletePtpConfig {
		clean.All()
	}
})
