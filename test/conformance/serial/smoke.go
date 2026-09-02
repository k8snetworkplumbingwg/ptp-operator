//go:build !unittests
// +build !unittests

package test

import (
	"context"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
)

// Smoke tests are minimal infrastructure validation tests that run quickly
// to verify the test framework, cluster connectivity, and basic setup.
// Set PTP_TEST_MODE=smoke to run only these tests.

var _ = Describe("[smoke]", Serial, func() {
	BeforeEach(func() {
		// Only run smoke tests when PTP_TEST_MODE=smoke
		mode := strings.ToLower(os.Getenv("PTP_TEST_MODE"))
		if mode != "smoke" {
			Skip("Skipping smoke tests - PTP_TEST_MODE is not set to 'smoke'")
		}
	})

	Context("Smoke Tests - Infrastructure Validation", func() {
		It("Should validate test infrastructure is working", func() {
			logrus.Info("=== SMOKE TEST: Hello World ===")
			logrus.Info("Test framework initialized successfully")
			logrus.Info("Ginkgo/Gomega test runner operational")

			// Basic assertion to verify test framework
			Expect(true).To(BeTrue(), "Basic assertion check")

			logrus.Info("=== SMOKE TEST: PASSED ===")
		})

		It("Should verify cluster client connectivity", func() {
			logrus.Info("=== SMOKE TEST: Client Connectivity ===")

			// Verify k8s client is initialized
			Expect(client.Client).NotTo(BeNil(), "Kubernetes client should be initialized")

			// Try to connect to the cluster
			_, err := client.Client.Nodes().List(context.Background(), metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should be able to list nodes")

			logrus.Info("Cluster connectivity verified")
			logrus.Info("=== SMOKE TEST: PASSED ===")
		})

		It("Should verify PTP namespace exists", func() {
			logrus.Info("=== SMOKE TEST: PTP Namespace Check ===")

			// Check if openshift-ptp namespace exists
			ns, err := client.Client.Namespaces().Get(context.Background(), "openshift-ptp", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "openshift-ptp namespace should exist")
			Expect(ns).NotTo(BeNil())

			logrus.Infof("PTP namespace found: %s", ns.Name)
			logrus.Info("=== SMOKE TEST: PASSED ===")
		})
	})
})
