//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/clean"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/logging"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/testconfig"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

var junitPath *string
var DeletePtpConfig bool

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func InitDeletePtpConfig() {
	value, isSet := os.LookupEnv("KEEP_PTPCONFIG")
	value = strings.ToLower(value)
	DeletePtpConfig = !isSet || strings.Contains(value, "false")
	logrus.Infof("DeletePtpConfig=%t", DeletePtpConfig)
}

func TestTest(t *testing.T) {
	logging.InitLogLevel()
	RegisterFailHandler(Fail)
	InitDeletePtpConfig()
	RunSpecs(t, "PTP e2e integration tests")
}

var _ = BeforeSuite(func() {
	logrus.Info("Executed from serial suite")
	By("Creating Kubernetes client and initializing event pub/sub", func() {

		testclient.Client = testclient.New("")
		Expect(testclient.Client).NotTo(BeNil())
		event.InitPubSub()
	})
	By("Starting log collection from PTP pods", func() {

		err := logging.StartLogCollection("serial")
		if err != nil {
			logrus.Errorf("Failed to start log collection: %v", err)
		}
	})
})

var _ = AfterSuite(func() {
	By("Cleaning up PTP configs", func() {
		if DeletePtpConfig && testconfig.GetDesiredConfig(false).PtpModeDesired != testconfig.Discovery {
			clean.All()
		}
	})
	By("Stopping log collection and saving artifacts", func() {
		logging.StopLogCollection()
	})
})

var _ = ReportBeforeEach(func(report SpecReport) {
	By("Writing test start marker to log files")
	logging.WriteTestStart(report)
})

var _ = ReportAfterEach(func(report SpecReport) {
	By("Writing test end marker to log files")
	logging.WriteTestEnd(report)
})
