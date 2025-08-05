package test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	ptpEvent "github.com/redhat-cne/sdk-go/pkg/event/ptp"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/metrics"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/namespaces"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptphelper"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptptesthelper"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/execute"

	fbprotocol "github.com/facebook/time/ptp/protocol"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/testconfig"

	k8sv1 "k8s.io/api/core/v1"
)

type TestCase string

const (
	Reboot TestCase = "reboot"
)

const (
	DPLL_LOCKED_HO_ACQ = 3
	DPLL_HOLDOVER      = 4
	DPLL_FREERUN       = 1
	DPLL_LOCKED        = 2
)
const (
	ClockClassFreerun = 248
)

var DesiredMode = testconfig.GetDesiredConfig(true).PtpModeDesired

var _ = Describe("["+strings.ToLower(DesiredMode.String())+"-serial]", Serial, func() {
	BeforeEach(func() {
		Expect(client.Client).NotTo(BeNil())
	})

	Context("PTP configuration verifications", func() {
		// Setup verification
		// if requested enabled  ptp events
		It("Should check whether PTP operator needs to enable PTP events", func() {
			By("Find if variable set to enable ptp events")
			if event.Enable() {
				apiVersion := event.GetDefaultApiVersion()
				err := ptphelper.EnablePTPEvent(apiVersion, "")
				Expect(err).To(BeNil(), "error when enable ptp event")
				ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(ptpConfig.Spec.EventConfig.EnableEventPublisher).Should(BeTrue(), "failed to enable ptp event")
			}
		})
		It("Should check whether PTP operator appropriate resource exists", func() {
			By("Getting list of available resources")
			rl, err := client.Client.ServerPreferredResources()
			Expect(err).ToNot(HaveOccurred())

			found := false
			By("Find appropriate resources")
			for _, g := range rl {
				if strings.Contains(g.GroupVersion, pkg.PtpResourcesGroupVersionPrefix) {
					for _, r := range g.APIResources {
						By("Search for resource " + pkg.PtpResourcesNameOperatorConfigs)
						if r.Name == pkg.PtpResourcesNameOperatorConfigs {
							found = true
						}
					}
				}
			}

			Expect(found).To(BeTrue(), fmt.Sprintf("resource %s not found", pkg.PtpResourcesNameOperatorConfigs))
		})
		// Setup verification
		It("Should check that all nodes are running at least one replica of linuxptp-daemon", func() {
			By("Getting ptp operator config")

			ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			listOptions := metav1.ListOptions{}
			if ptpConfig.Spec.DaemonNodeSelector != nil && len(ptpConfig.Spec.DaemonNodeSelector) != 0 {
				listOptions = metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: ptpConfig.Spec.DaemonNodeSelector})}
			}

			By("Getting list of nodes")
			nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), listOptions)
			Expect(err).NotTo(HaveOccurred())
			By("Checking number of nodes")
			Expect(len(nodes.Items)).To(BeNumerically(">", 0), "number of nodes should be more than 0")

			By("Get daemonsets collection for the namespace " + pkg.PtpLinuxDaemonNamespace)
			ds, err := client.Client.DaemonSets(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(ds.Items)).To(BeNumerically(">", 0), "no damonsets found in the namespace "+pkg.PtpLinuxDaemonNamespace)
			By("Checking number of scheduled instances")
			Expect(ds.Items[0].Status.CurrentNumberScheduled).To(BeNumerically("==", ds.Items[0].Status.DesiredNumberScheduled), "should be one instance per node")
		})
		// Setup verification
		It("Should check that operator is deployed", func() {
			By("Getting deployment " + pkg.PtpOperatorDeploymentName)
			dep, err := client.Client.Deployments(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpOperatorDeploymentName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			By("Checking availability of the deployment")
			for _, c := range dep.Status.Conditions {
				if c.Type == v1.DeploymentAvailable {
					Expect(string(c.Status)).Should(Equal("True"), pkg.PtpOperatorDeploymentName+" deployment is not available")
				}
			}
		})

	})

	Describe("PTP e2e tests", func() {
		var ptpPods *corev1.PodList
		var fifoPriorities map[string]int64
		var fullConfig testconfig.TestConfig
		portEngine := ptptesthelper.PortEngine{}

		execute.BeforeAll(func() {
			err := testconfig.CreatePtpConfigurations()
			if err != nil {
				fullConfig.Status = testconfig.DiscoveryFailureStatus
				Fail(fmt.Sprintf("Could not create a ptp config, err=%s", err))
			}
			fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, false)
			if fullConfig.Status != testconfig.DiscoverySuccessStatus {
				logrus.Printf(`ptpconfigs were not properly discovered, Check:
- the ptpconfig has a %s label only in the recommend section (no node section)
- the node running the clock under test is label with: %s`, pkg.PtpClockUnderTestNodeLabel, pkg.PtpClockUnderTestNodeLabel)

				Fail("Failed to find a valid ptp slave configuration")

			}
			if fullConfig.PtpModeDesired != testconfig.Discovery {
				ptphelper.RestartPTPDaemon()
			}

			portEngine.Initialize(fullConfig.DiscoveredClockUnderTestPod, fullConfig.DiscoveredFollowerInterfaces)

		})

		Context("PTP Reboot discovery", func() {
			BeforeEach(func() {
				Skip("This is covered by QE")
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})

			It("The slave node is rebooted and discovered and in sync", func() {
				if testCaseEnabled(Reboot) {
					By("Slave node is rebooted", func() {
						ptptesthelper.RebootSlaveNode(fullConfig)
					})
				} else {
					Skip("Skipping the reboot test")
				}
			})
		})

		Context("PTP Interfaces discovery", func() {

			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

			})

			It("The interfaces supporting ptp can be discovered correctly", func() {
				for podIndex := range ptpPods.Items {
					ptpNodeIfacesDiscoveredByL2 := ptphelper.GetPtpInterfacePerNode(ptpPods.Items[podIndex].Spec.NodeName, fullConfig.L2Config.GetPtpIfListUnfiltered())
					lenPtpNodeIfacesDiscoveredByL2 := len(ptpNodeIfacesDiscoveredByL2)
					ptpNodeIfacesFromPtpApi := ptphelper.PtpDiscoveredInterfaceList(ptpPods.Items[podIndex].Spec.NodeName)
					lenPtpNodeIfacesFromPtpApi := len(ptpNodeIfacesFromPtpApi)
					sort.Strings(ptpNodeIfacesDiscoveredByL2)
					sort.Strings(ptpNodeIfacesFromPtpApi)
					logrus.Infof("Interfaces supporting ptp for node        %s: %v", ptpPods.Items[podIndex].Spec.NodeName, ptpNodeIfacesDiscoveredByL2)
					logrus.Infof("Interfaces discovered by ptp API for node %s: %v", ptpPods.Items[podIndex].Spec.NodeName, ptpNodeIfacesFromPtpApi)

					// The discovered PTP interfaces should match exactly the list of interfaces calculated by test
					Expect(lenPtpNodeIfacesDiscoveredByL2).To(Equal(lenPtpNodeIfacesFromPtpApi))
					for index := range ptpNodeIfacesDiscoveredByL2 {
						Expect(ptpNodeIfacesDiscoveredByL2[index]).To(Equal(ptpNodeIfacesFromPtpApi[index]))
					}
				}
			})

			It("Should retrieve the details of hardwares for the Ptp", func() {
				By("Getting the version of the OCP cluster")

				ocpVersion, err := ptphelper.GetOCPVersion()
				if err != nil {
					logrus.Infof("Kubernetes cluster under test is not Openshift, cannot get OCP version")
				} else {
					logrus.Infof("Kubernetes cluster under test is Openshift, OCP version is %s", ocpVersion)
				}

				By("Getting the version of the PTP operator")

				ptpOperatorVersion, err := ptphelper.GetPtpOperatorVersion()
				Expect(err).ToNot(HaveOccurred())
				Expect(ptpOperatorVersion).ShouldNot(BeEmpty())

				By("Getting the NIC details of all the PTP enabled interfaces")

				ptpInterfacesList := fullConfig.L2Config.GetPtpIfList()

				for _, ptpInterface := range ptpInterfacesList {
					ifaceHwDetails := fmt.Sprintf("Device: %s, Function: %s, Description: %s",
						ptpInterface.IfPci.Device, ptpInterface.IfPci.Function, ptpInterface.IfPci.Description)

					logrus.Debugf("Node: %s, Interface Name: %s, %s", ptpInterface.NodeName, ptpInterface.IfName, ifaceHwDetails)

					AddReportEntry(fmt.Sprintf("Node %s, Interface: %s", ptpInterface.NodeName, ptpInterface.IfName), ifaceHwDetails)
				}

				By("Getting ptp config details")
				ptpConfig := testconfig.GlobalConfig

				masterPtpConfigStr := ptpConfig.DiscoveredGrandMasterPtpConfig.String()
				slavePtpConfigStr := ptpConfig.DiscoveredClockUnderTestPtpConfig.String()

				logrus.Infof("Discovered master ptp config %s", masterPtpConfigStr)
				logrus.Infof("Discovered slave ptp config %s", slavePtpConfigStr)

				AddReportEntry("master-ptp-config", masterPtpConfigStr)
				AddReportEntry("slave-ptp-config", slavePtpConfigStr)
			})
		})

		Context("PTP ClockSync", func() {
			err := metrics.InitEnvIntParamConfig("MAX_OFFSET_IN_NS", metrics.MaxOffsetDefaultNs, &metrics.MaxOffsetNs)
			Expect(err).NotTo(HaveOccurred(), "error getting max offset in nanoseconds %s", err)
			err = metrics.InitEnvIntParamConfig("MIN_OFFSET_IN_NS", metrics.MinOffsetDefaultNs, &metrics.MinOffsetNs)
			Expect(err).NotTo(HaveOccurred(), "error getting min offset in nanoseconds %s", err)

			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})
			AfterEach(func() {
				_ = portEngine.TurnAllPortsUp()
			})
			// 25733
			It("PTP daemon apply match rule based on nodeLabel", func() {

				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("This test needs the ptp-daemon to be rebooted but it is not possible in discovery mode, skipping")
				}
				profileSlave := fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredClockUnderTestPtpConfig.Name)
				profileMaster := ""
				if fullConfig.DiscoveredGrandMasterPtpConfig != nil {
					profileMaster = fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredGrandMasterPtpConfig.Name)
				}

				for podIndex := range ptpPods.Items {
					isClockUnderTest, err := ptphelper.IsClockUnderTestPod(&ptpPods.Items[podIndex])
					if err != nil {
						Fail(fmt.Sprintf("check clock under test clock type, err=%s", err))
					}
					isGrandmaster, err := ptphelper.IsGrandMasterPod(&ptpPods.Items[podIndex])
					if err != nil {
						Fail(fmt.Sprintf("check Grandmaster clock type, err=%s", err))
					}
					if isClockUnderTest {
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileSlave, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							Fail(fmt.Sprintf("could not get slave profile name, err=%s", err))
						}
					} else if isGrandmaster && fullConfig.DiscoveredGrandMasterPtpConfig != nil {
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileMaster, true, pkg.TimeoutIn5Minutes)
						if err != nil {
							Fail(fmt.Sprintf("could not get master profile name, err=%s", err))
						}
					}
				}
			})

			// Multinode clock sync test:
			// - waits for the foreign master to appear
			// - verifies that the foreign master has the expected grandmaster ID
			// - use metrics to verify that the offset is below threshold
			//
			// Single node clock sync test:
			// - waits for the foreign master to appear
			// - use metrics to verify that the offset is below threshold
			It("Slave can sync to master", func() {
				if fullConfig.PtpModeDesired == testconfig.TelcoGrandMasterClock {
					Skip("Skipping as slave interface is not available with a WPC-GM profile")
				}
				isExternalMaster := ptphelper.IsExternalGM()
				var grandmasterID *string
				if fullConfig.L2Config != nil && !isExternalMaster {
					aLabel := pkg.PtpGrandmasterNodeLabel
					aString, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
					grandmasterID = &aString
					Expect(err).To(BeNil())
				}
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock {
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
					Expect(err).To(BeNil())
				}
			})

			// Test That clock can sync in dual follower scenario when one port is down
			It("Dual follower can sync when one follower port goes down", func() {
				if fullConfig.PtpModeDesired != testconfig.DualFollowerClock {
					Skip("Test reserved for dual follower scenario")
				}
				Expect(len(fullConfig.DiscoveredFollowerInterfaces) == 2)
				isExternalMaster := ptphelper.IsExternalGM()
				var grandmasterID *string
				if fullConfig.L2Config != nil && !isExternalMaster {
					aLabel := pkg.PtpGrandmasterNodeLabel
					aString, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
					grandmasterID = &aString
					Expect(err).To(BeNil())
				}
				// Retry until there is no error or we timeout
				Eventually(func() error {
					return portEngine.RolesInOnly([]metrics.MetricRole{metrics.MetricRoleSlave, metrics.MetricRoleListening})
				}, 150*time.Second, 30*time.Second).Should(BeNil())

				By("Port0: down")
				err = portEngine.TurnPortDown(portEngine.Ports[0])
				Expect(err).To(BeNil())
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				err = portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleSlave)
				Expect(err).To(BeNil())

				By("Port1: down")
				err = portEngine.TurnPortDown(portEngine.Ports[1])
				Expect(err).To(BeNil())
				By("Check holdover")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateHoldOver, metrics.MetricRoleFaulty, false)
				Expect(err).To(BeNil())
				By("Check freerun")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateFreeRun, metrics.MetricRoleFaulty, false)
				Expect(err).To(BeNil())
				By("Check clock role")
				err = portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleFaulty)
				Expect(err).To(BeNil())

				By("Port1: up")
				err = portEngine.TurnPortUp(portEngine.Ports[1])
				Expect(err).To(BeNil())
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				err = portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleSlave)
				Expect(err).To(BeNil())

				By("Port0: up")
				err = portEngine.TurnPortUp(portEngine.Ports[0])
				Expect(err).To(BeNil())
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				err = portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], portEngine.InitialRoles[0], portEngine.InitialRoles[1])
				Expect(err).To(BeNil())

				By("Remove Grandmaster")
				err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig.Name, metav1.DeleteOptions{})
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleListening, metrics.MetricRoleListening)
				}, 120*time.Second, 1*time.Second).Should(BeNil())
				By("Check holdover")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateHoldOver, metrics.MetricRoleListening, false)
				Expect(err).To(BeNil())
				By("Check freerun")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateFreeRun, metrics.MetricRoleListening, false)
				Expect(err).To(BeNil())
				By("Recreate Grandmaster")
				tempPtpConfig := (*ptpv1.PtpConfig)(testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig)
				tempPtpConfig.SetResourceVersion("")
				_, err = client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), tempPtpConfig, metav1.CreateOptions{})
				Expect(err).To(BeNil())
			})

			// Multinode BCSlave clock sync
			// - waits for the BCSlave foreign master to appear (the boundary clock)
			// - verifies that the BCSlave foreign master has the expected boundary clock ID
			// - use metrics to verify that the offset with boundary clock is below threshold
			It("Downstream slave can sync to BC master", func() {
				if fullConfig.PtpModeDesired == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for WPC GM testing only valid for BC config in multi-node cluster ")
				}

				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock {
					Skip("test only valid for Boundary clock in multi-node clusters")
				}
				if !fullConfig.FoundSolutions[testconfig.AlgoBCWithSlavesString] &&
					!fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesString] &&
					!fullConfig.FoundSolutions[testconfig.AlgoBCWithSlavesExtGMString] &&
					!fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesExtGMString] {
					Skip("test only valid for Boundary clock in multi-node clusters with slaves")
				}
				aLabel := pkg.PtpClockUnderTestNodeLabel
				masterIDBc1, err := ptphelper.GetClockIDMaster(pkg.PtpBcMaster1PolicyName, &aLabel, nil, false)
				Expect(err).To(BeNil())
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave1PtpConfig), &masterIDBc1, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())

				if (fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock) && (fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesExtGMString] ||
					fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesString]) {
					aLabel := pkg.PtpClockUnderTestNodeLabel
					masterIDBc2, err := ptphelper.GetClockIDMaster(pkg.PtpBcMaster2PolicyName, &aLabel, nil, false)
					Expect(err).To(BeNil())
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave2PtpConfig), &masterIDBc2, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
					Expect(err).To(BeNil())
				}

			})

			// 25743
			It("Can provide a profile with higher priority", func() {
				var testPtpPod corev1.Pod
				isExternalMaster := ptphelper.IsExternalGM()
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("Skipping because adding a different profile and no modifications are allowed in discovery mode")
				}
				var policyName string
				var modifiedPtpConfig *ptpv1.PtpConfig
				By("Creating a config with higher priority", func() {
					if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
						Skip("WPC GM (T-GM) mode is not supported for this test")
					}
					switch fullConfig.PtpModeDiscovered {
					case testconfig.Discovery, testconfig.None:
						Skip("Skipping because Discovery or None is not supported yet for this test")
					case testconfig.OrdinaryClock:
						policyName = pkg.PtpSlave1PolicyName
					case testconfig.DualFollowerClock:
						policyName = pkg.PtpSlave1PolicyName
					case testconfig.BoundaryClock:
						policyName = pkg.PtpBcMaster1PolicyName
					case testconfig.DualNICBoundaryClock:
						policyName = pkg.PtpBcMaster1PolicyName
					default:
						panic("unhandled ptp config case")
					}
					ptpConfigToModify, err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), policyName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
						LabelSelector: pkg.PtpClockUnderTestNodeLabel,
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(nodes.Items)).To(BeNumerically(">", 0),
						fmt.Sprintf("PTP Nodes with label %s are not deployed on cluster", pkg.PtpClockUnderTestNodeLabel))

					ptpConfigTest := ptphelper.MutateProfile(ptpConfigToModify, pkg.PtpTempPolicyName, nodes.Items[0].Name)
					modifiedPtpConfig, err = client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), ptpConfigTest, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = ptphelper.GetPtpPodOnNode(nodes.Items[0].Name)
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = ptphelper.ReplaceTestPod(&testPtpPod, time.Minute)
					Expect(err).NotTo(HaveOccurred())
				})

				By("Checking if Node has Profile and check sync", func() {
					var grandmasterID *string
					if fullConfig.L2Config != nil && !isExternalMaster {
						aLabel := pkg.PtpGrandmasterNodeLabel
						aString, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
						grandmasterID = &aString
						Expect(err).To(BeNil())
					}
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, modifiedPtpConfig, grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
					Expect(err).To(BeNil())
				})

				By("Deleting the test profile", func() {
					err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), pkg.PtpTempPolicyName, metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
					Eventually(func() bool {
						_, err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpTempPolicyName, metav1.GetOptions{})
						return kerrors.IsNotFound(err)
					}, 1*time.Minute, 1*time.Second).Should(BeTrue(), "Could not delete the test profile")
				})

				By("Checking the profile is reverted", func() {
					_, err := pods.GetPodLogsRegex(testPtpPod.Namespace,
						testPtpPod.Name, pkg.PtpContainerName,
						"Profile Name: "+policyName, true, pkg.TimeoutIn3Minutes)
					if err != nil {
						Fail(fmt.Sprintf("could not get profile name, err=%s", err))
					}
				})
			})
		})

		Context("PTP metric is present", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				var err error

				_, err = pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					"Profile Name:", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("could not get slave profile name, err=%s", err))
				}

			})

			// 27324
			It("verifies on slave", func() {
				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpOffsetNs),
					"Time metrics are not detected")
			})
		})

		Context("Running with event enabled", func() {
			BeforeEach(func() {
				if ptphelper.PtpEventEnabled() == 0 {
					Skip("Skipping, PTP events not enabled")
				}

				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})

				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
			})

			It("Should check for ptp events ", func() {
				By("Checking event side car is present")
				apiVersion := ptphelper.PtpEventEnabled()
				var apiBase, endpointUri string
				if apiVersion == 1 {
					apiBase = event.ApiBaseV1
					endpointUri = "endpointUri"
				} else {
					apiBase = event.ApiBaseV2
					endpointUri = "EndpointUri"
				}
				cloudProxyFound := false
				Expect(len(fullConfig.DiscoveredClockUnderTestPod.Spec.Containers)).To(BeNumerically("==", 3), "linuxptp-daemon is not deployed on cluster with cloud event proxy")
				for _, c := range fullConfig.DiscoveredClockUnderTestPod.Spec.Containers {
					if c.Name == pkg.EventProxyContainerName {
						cloudProxyFound = true
					}
				}
				Expect(cloudProxyFound).ToNot(BeFalse(), "No event pods detected")

				By("Checking event api is healthy")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "health")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
					"Event API is not in healthy state")

				By("Checking ptp publisher is created")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "publishers")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(endpointUri),
					"Event API  did not return publishers")

				By("Checking events are generated")

				_, err := pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"Created publisher", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event publisher was not created in pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}
				_, err = pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"event sent", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event was not generated in the pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}

				By("Checking event metrics are present")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
					"Interface role metrics are not detected")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpThreshold),
					"Threshold metrics are not detected")
			})
		})

		Context("Running with event enabled, v1/v2 regression", func() {
			BeforeEach(func() {
				if !event.IsV1EventRegressionNeeded() {
					Skip("Skipping, test PTP events v1 regression is for 4.16 and 4.17 only")
				}

				ptphelper.EnablePTPEvent("1.0", fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
				// wait for pod info updated
				time.Sleep(5 * time.Second)

				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}

				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
			})

			It("Should check for ptp events ", func() {
				By("Checking event side car is present")

				cloudProxyFound := false
				Expect(len(fullConfig.DiscoveredClockUnderTestPod.Spec.Containers)).To(BeNumerically("==", 3), "linuxptp-daemon is not deployed on cluster with cloud event proxy")
				for _, c := range fullConfig.DiscoveredClockUnderTestPod.Spec.Containers {
					if c.Name == pkg.EventProxyContainerName {
						cloudProxyFound = true
					}
				}
				Expect(cloudProxyFound).ToNot(BeFalse(), "No event pods detected")

				By("Checking event api is healthy")
				apiVersion := ptphelper.PtpEventEnabled()
				var apiBase string
				if apiVersion == 1 {
					apiBase = event.ApiBaseV1
				} else {
					apiBase = event.ApiBaseV2
				}
				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "health")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
					"Event API is not in healthy state")

				By("Checking ptp publisher is created")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "publishers")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("endpointUri"),
					"Event API  did not return publishers")

				By("Checking events are generated")

				_, err := pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"Created publisher", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event publisher was not created in pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}
				_, err = pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"event sent", true, pkg.TimeoutIn5Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event was not generated in the pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}

				By("Checking event metrics are present")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
					"Interface role metrics are not detected")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpThreshold),
					"Threshold metrics are not detected")
				// reset to v2
				ptphelper.EnablePTPEvent("2.0", fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
			})
		})

		Context("Running with reference plugin", func() {
			BeforeEach(func() {
				By("Enabling reference plugin", func() {
					Expect(ptphelper.EnablePTPReferencePlugin()).NotTo(HaveOccurred())
				})
			})
			AfterEach(func() {
				By("Disabling reference plugin", func() {
					Expect(ptphelper.DisablePTPReferencePlugin()).NotTo(HaveOccurred())
				})
			})
			XIt("Should check whether plugin is loaded", func() {
				By("checking for plugin logs")
				foundMatch := false
				for i := 0; i < 3 && !foundMatch; i++ {
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
					ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
					pluginLog := "Trying to register plugin: reference"
					for podIndex := range ptpPods.Items {
						_, err := pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							pluginLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("Reference plugin not loaded, err=%s", err))
							continue
						}
						foundMatch = true
					}
				}
				Expect(foundMatch).To(BeTrue())
			})
			XIt("Should check whether test plugin executes", func() {
				By("Find if required logs are found")
				Expect(ptphelper.EnablePTPReferencePlugin()).NotTo(HaveOccurred())
				pluginConfigExists := false
				pluginOpts := ""
				masterConfigs, slaveConfigs := ptphelper.DiscoveryPTPConfiguration(pkg.PtpLinuxDaemonNamespace)
				ptpConfigs := append(masterConfigs, slaveConfigs...)
				for _, config := range ptpConfigs {
					for _, profile := range config.Spec.Profile {
						if profile.Plugins != nil {
							for name, opts := range profile.Plugins {
								if name == "reference" {
									optsByteArray, _ := json.Marshal(opts)
									_ = json.Unmarshal(optsByteArray, &pluginOpts)
									pluginConfigExists = true
								}
							}
						}
					}
				}
				if !pluginConfigExists {
					Skip("No plugin policies configured")
				}
				foundMatch := false
				for i := 0; i < 3 && !foundMatch; i++ {
					ptphelper.WaitForPtpDaemonToExist()
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

					ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
					pluginLog := fmt.Sprintf("OnPTPConfigChangeGeneric: (%s)", pluginOpts)
					for podIndex := range ptpPods.Items {
						_, err := pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							pluginLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("Reference plugin not running OnPTPConfigChangeGeneric, err=%s", err))
							continue
						}
						foundMatch = true
					}
				}
				Expect(foundMatch).To(BeTrue())
			})
		})
		Context("Running with fifo scheduling", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}

				masterConfigs, slaveConfigs := ptphelper.DiscoveryPTPConfiguration(pkg.PtpLinuxDaemonNamespace)
				ptpConfigs := append(masterConfigs, slaveConfigs...)

				fifoPriorities = make(map[string]int64)
				for _, config := range ptpConfigs {
					for _, profile := range config.Spec.Profile {
						if profile.PtpSchedulingPolicy != nil && *profile.PtpSchedulingPolicy == "SCHED_FIFO" {
							if profile.PtpSchedulingPriority != nil {
								fifoPriorities[*profile.Name] = *profile.PtpSchedulingPriority
							}
						}
					}
				}
				if len(fifoPriorities) == 0 {
					Skip("No SCHED_FIFO policies configured")
				}
				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
			})
			It("Should check whether using fifo scheduling", func() {
				By("checking for chrt logs")
				for name, priority := range fifoPriorities {
					ptp4lLog := fmt.Sprintf("/bin/chrt -f %d /usr/sbin/ptp4l", priority)
					for podIndex := range ptpPods.Items {
						profileName := fmt.Sprintf("Profile Name: %s", name)
						_, err := pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileName, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("error getting profile=%s, err=%s ", name, err))
							continue
						}
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							ptp4lLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("error getting ptp4l chrt line=%s, err=%s ", ptp4lLog, err))
							continue
						}
						delete(fifoPriorities, name)
					}
				}
				Expect(fifoPriorities).To(HaveLen(0))
			})
		})

		// old cnf-feature-deploy tests
		var _ = Describe("PTP socket sharing between pods", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("PTP socket test not supported in discovery mode")
				}
			})
			AfterEach(func() {
				err := namespaces.Clean(pkg.PtpLinuxDaemonNamespace, "testpod-", client.Client)
				Expect(err).ToNot(HaveOccurred())
			})
			var _ = Context("Negative - run pmc in a new unprivileged pod on the slave node", func() {
				It("Should not be able to use the uds", func() {
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file was not created")
					podDefinition := pods.DefinePodOnNode(pkg.PtpLinuxDaemonNamespace, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
					hostPathDirectoryOrCreate := corev1.HostPathDirectoryOrCreate
					podDefinition.Spec.Volumes = []corev1.Volume{
						{
							Name: "socket-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/run/ptp",
									Type: &hostPathDirectoryOrCreate,
								},
							},
						},
					}
					podDefinition.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
						{
							Name:      "socket-dir",
							MountPath: "/var/run",
						},
						{
							Name:      "socket-dir",
							MountPath: "/host",
						},
					}
					pod, err := client.Client.Pods(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					err = pods.WaitForCondition(client.Client, pod, corev1.ContainersReady, corev1.ConditionTrue, 3*time.Minute)
					Expect(err).ToNot(HaveOccurred())
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, true, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).Should(ContainSubstring("Permission denied"), "unprivileged pod can access the uds socket")
				})
			})

			var _ = Context("Run pmc in a new pod on the slave node", func() {
				It("Should be able to sync using a uds", func() {

					Expect(fullConfig.DiscoveredClockUnderTestPod).ToNot(BeNil())
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file was not created")
					podDefinition, _ := pods.RedefineAsPrivileged(
						pods.DefinePodOnNode(pkg.PtpLinuxDaemonNamespace, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName), "")
					hostPathDirectoryOrCreate := corev1.HostPathDirectoryOrCreate
					podDefinition.Spec.Volumes = []corev1.Volume{
						{
							Name: "socket-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/run/ptp",
									Type: &hostPathDirectoryOrCreate,
								},
							},
						},
					}
					podDefinition.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
						{
							Name:      "socket-dir",
							MountPath: "/var/run",
						},
						{
							Name:      "socket-dir",
							MountPath: "/host",
						},
					}
					pod, err := client.Client.Pods(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					err = pods.WaitForCondition(client.Client, pod, corev1.ContainersReady, corev1.ConditionTrue, 3*time.Minute)
					Expect(err).ToNot(HaveOccurred())
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, true, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file is not shared between pods")

					Eventually(func() int {
						buf, _, _ := pods.ExecCommand(client.Client, false, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return strings.Count(buf.String(), "offsetFromMaster")
					}, 3*time.Minute, 2*time.Second).Should(BeNumerically(">=", 1))
				})
			})
		})

		var _ = Describe("prometheus", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})
			AfterEach(func() {

			})
			Context("Metrics reported by PTP pods", func() {
				It("Should all be reported by prometheus", func() {
					// Check if test configuration discovery was successful
					if fullConfig.DiscoveredClockUnderTestPod == nil {
						Skip("Skipping prometheus metrics test - clock under test pod not discovered")
					}

					var err error
					ptpPods, err = client.Client.Pods(openshiftPtpNamespace).List(context.Background(), metav1.ListOptions{
						LabelSelector: "app=linuxptp-daemon",
					})
					Expect(err).ToNot(HaveOccurred())
					Expect(fullConfig.DiscoveredClockUnderTestPod).ToNot(BeNil())
					ptpMonitoredEntriesByPod, uniqueMetricKeys := collectPtpMetrics([]k8sv1.Pod{*fullConfig.DiscoveredClockUnderTestPod})
					Eventually(func() error {
						podsPerPrometheusMetricKey, err := collectPrometheusMetrics(uniqueMetricKeys)
						if err != nil {
							return err
						}
						return containSameMetrics(ptpMonitoredEntriesByPod, podsPerPrometheusMetricKey)
					}, 5*time.Minute, 2*time.Second).Should(Not(HaveOccurred()))

				})
			})
		})

		Context("PTP Outage recovery", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})

			It("The slave node network interface is taken down and up", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for WPC GM config")
				}
				if fullConfig.PtpModeDesired == testconfig.DualFollowerClock {
					Skip("Test not valid for dual follower scenario")
				}
				By("toggling network interfaces and syncing", func() {
					skippedInterfacesStr, isSet := os.LookupEnv("SKIP_INTERFACES")

					if !isSet {
						Skip("Mandatory to provide skipped interface to avoid making a node disconnected from the cluster")
					} else {
						skipInterfaces := make(map[string]bool)
						separated := strings.Split(skippedInterfacesStr, ",")
						for _, val := range separated {
							skipInterfaces[val] = true
						}
						logrus.Info("skipINterfaces", skipInterfaces)
						ptptesthelper.RecoverySlaveNetworkOutage(fullConfig, skipInterfaces)
					}
				})
			})
		})

		Context("WPC GM Verification Tests", func() {
			BeforeEach(func() {

				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
			})
			It("is verifying WPC GM state based on logs", func() {

				By("checking GM required processes status", func() {
					processesArr := [...]string{"phc2sys", "gpspipe", "ts2phc", "gpsd", "ptp4l", "dpll"}
					for _, val := range processesArr {
						logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, val, true, pkg.TimeoutIn1Minute)
						Expect(err).To(BeNil(), fmt.Sprintf("Error encountered looking for %s", val))
						Expect(logMatches).ToNot(BeEmpty(), fmt.Sprintf("Expected %s to be running for GM", val))
					}
				})

				By("checking clock class state is locked", func() {
					clockClassPattern := `ptp4l(?m)\[.*?\]:\[(.*?)\] CLOCK_CLASS_CHANGE 6`
					clockClassRe := regexp.MustCompile(clockClassPattern)

					Eventually(func() ([][]string, error) {
						logMatches, err := pods.GetPodLogsRegex(
							openshiftPtpNamespace,
							fullConfig.DiscoveredClockUnderTestPod.Name,
							pkg.PtpContainerName,
							clockClassRe.String(),
							false,                // don't follow logs
							pkg.TimeoutIn1Minute, // inner timeout for single call (can be shorter if you want)
						)
						return logMatches, err
					}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should( // <-- total wait 5 mins, check every 10s
						And(
							Not(BeEmpty()),
							Not(BeNil()),
						),
						"Expected ptp4l clock class state to eventually be Locked (class 6)",
					)
				})

				By("checking DPLL frequency and DPLL phase state to be locked", func() {
					/*
						dpll[1726600932]:[ts2phc.0.config] ens7f0 frequency_status 3 offset -1 phase_status 3 pps_status 1 s2
					*/
					dpllStatePattern := `dpll(?m).*?:\[(.*?)\] (.*?)frequency_status 3 offset (.*?) phase_status 3 pps_status (.*?) (.*?)`
					dpllStateRe := regexp.MustCompile(dpllStatePattern)
					logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, dpllStateRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for dpll frequency and phase state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected dpll frequency and phase state to be locked for GM")
					//TODO 2 Card Add loop to check ifaces

				})

				By("checking GM clock state locked", func() {
					/*
						I0917 19:22:15.000310 2843504 event.go:430] dpll State s2, gnss State s2, tsphc state s2, gm state s2
						phc2sys[2355322.441]: [ptp4l.0.config:6] CLOCK_REALTIME phc offset       137 s2 freq   -7709 delay    514
					*/

					gmClockStatePattern := `(?m).*?dpll State s2, gnss State s2, tsphc state s2, gm state s2,`
					gmClockStateRe := regexp.MustCompile(gmClockStatePattern)
					logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, gmClockStateRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for dpll, gnss,ts2phc and GM clock state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected dpll, gnss,ts2phc and GM clock state to be locked for GM")

					phc2sysPattern := `phc2sys(?m).*?: \[(.*?)\] CLOCK_REALTIME phc offset[ \t]+(.*?) s2 (.*?)`
					phc2sysRe := regexp.MustCompile(phc2sysPattern)
					logMatches, err = pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, phc2sysRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for phc2sys clock state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected phc2sys clock state to be locked for GM")
					//TODO 2 Card Add loop to check ifaces

				})

				By("checking PTP NMEA status for ts2phc", func() {
					/*
						# ts2phc[1726600506]:[ts2phc.0.config] ens7f0 nmea_status 1 offset 0 s2
					*/
					nmeaStatusPattern := `ts2phc(?m).*?:\[(.*?)\] (.*?) nmea_status 1 offset (.*?) (.*?)`
					nmeaStatusRe := regexp.MustCompile(nmeaStatusPattern)
					logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, nmeaStatusRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for phc2sys clock state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected ts2phc nmea state to be available for GM")
					//TODO 2 Card Add loop to check ifaces
				})
			})
			It("is verifying WPC GM state based on metrics", func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
				By("checking GM required processes status", func() {
					/*
						# TYPE openshift_ptp_process_status gauge
						openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="phc2sys"} 1
						openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ptp4l"} 1
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpsd"} 1
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"}
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"} 1
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ts2phc"} 1
					*/
					checkProcessStatus(fullConfig, "1")
					time.Sleep(1 * time.Minute)
				})

				By("checking clock class state is locked", func() {
					/*
						# TYPE openshift_ptp_clock_class gauge
						# openshift_ptp_clock_class{node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ptp4l"} 6
					*/
					checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

				})

				By("checking DPLL frequency state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll process
						# TYPE openshift_ptp_frequency_status gauge
						# openshift_ptp_frequency_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
					*/
					checkDPLLFrequencyState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))

				})

				By("checking DPLL phase state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll process
						# TYPE openshift_ptp_phase_status gauge
						# openshift_ptp_phase_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
					*/
					checkDPLLPhaseState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))

				})

				By("checking GM clock state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll and ts2phc processes
						# TYPE openshift_ptp_clock_state gauge
						openshift_ptp_clock_state{iface="CLOCK_REALTIME",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="phc2sys"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="GM"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="gnss"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ts2phc"} 1
					*/
					checkClockState(fullConfig, "1")

				})

				By("checking PTP NMEA status for ts2phc", func() {
					/*
						# TYPE openshift_ptp_nmea_status gauge
						# openshift_ptp_nmea_status{from="ts2phc",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ts2phc"} 1
					*/
					checkPTPNMEAStatus(fullConfig, "1")
				})
			})

		})

		Context("WPC GM GNSS signal loss tests", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
			})
			/*
					Step | Action
					1    | Check starting stability (ClockClass 6, locked)
					2    | Start continuous coldboot
					3	 | Wait for DPLL state = 3 (Holdover)
					3    | Wait for ClockClass 7 (in-spec holdover)
					4    | Check clock state = 2 (Holdover)
					5    | Stop coldboot
					6    | Wait a little (for GNSS to recover)
					7    | Wait for ClockClass 6 again
					8    | Confirm clock state = 1 for T-GM (Locked)
				    9    | Check Dpll State = 1 (Locked)
			*/
			It("Testing WPC T-GM holdover through connection loss", func() {
				By("Coldboot GNSS continuously while waiting for ClockClass 7 and clock state for GM an DPLL", func() {
					checkStabilityOfWPCGMUsingMetrics(fullConfig)

					// Initially system should be LOCKED (ClockClass 6)
					checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

					stopChan := make(chan struct{})
					var once sync.Once
					safeClose := func() {
						once.Do(func() {
							close(stopChan)
						})
					}
					defer safeClose()

					// Start coldboot in background
					go coldBootInBackground(stopChan, fullConfig)

					// Meanwhile, wait for ClockClass 7 (GNSS loss - Holdover In Spec)
					waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7)))

					// Also verify ClockState 2 (Holdover)
					checkClockStateForProcess(fullConfig, "GM", "2")

					// Also verify ClockState (Holdover) for DPLL
					checkClockStateForProcess(fullConfig, "dpll", "2")

					// Once holdover detected, stop coldboot loop
					safeClose()

					// Give GNSS time to fully recover
					time.Sleep(pkg.Timeout10Seconds)

					// Now wait for system to go back to LOCKED (ClockClass 6)
					waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))
					// Give DPLL time update
					time.Sleep(pkg.Timeout10Seconds)
					// Also verify ClockState (Holdover) for DPLL
					checkClockStateForProcess(fullConfig, "dpll", "1")

					// Also verify ClockState 1 (Locked)
					checkClockStateForProcess(fullConfig, "GM", "1")
				})
			})

		})

		Context("WPC GM Events verification (V1)", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
			})
			PIt("Verify Events during GNSS Loss flow (V1)", func() {

			})

		})

		Context("WPC GM Events verification (V2)", Ordered, func() {
			BeforeAll(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
				// Check if test configuration discovery was successful
				if fullConfig.DiscoveredClockUnderTestPod == nil {
					Skip("Skipping event tests - clock under test pod not discovered")
				}

				/*
					# TYPE openshift_ptp_clock_class gauge
					# openshift_ptp_clock_class{node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ptp4l"} 6
				*/
				checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

				// Setup event consumer pod and service
				err := setupEventConsumerPodAndService()
				Expect(err).NotTo(HaveOccurred())

				// Set up port forwarding to access the PTP event publisher service
				err = setupPTPEventPortForward(fullConfig)
				Expect(err).NotTo(HaveOccurred())

				// Set up port forwarding to access the event consumer service
				err = setupEventConsumerPortForward()
				Expect(err).NotTo(HaveOccurred())
			})
			AfterAll(func() {
				// Clean up port forwarding
				killCmd := exec.Command("pkill", "-f", "oc.*port-forward.*9043")
				killCmd.Run() // Ignore errors

				// Clean up event consumer port forwarding
				killCmd = exec.Command("pkill", "-f", "oc.*port-forward.*27018")
				killCmd.Run() // Ignore errors

				// Cleanup event consumer pod and service
				err := cleanupEventConsumerPodAndService()
				if err != nil {
					logrus.Warnf("Failed to cleanup event consumer pod and service: %v", err)
				}
			})

			var _ = Describe("T-GM Event Tests", func() {
				It("should verify clock class change when GNSS is lost ", func() {

					// Get the full node name for the subscription
					fullNodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName

					// Use reusable event subscription and verification
					publisherURL := "http://localhost:9043"
					consumerURL := "http://localhost:27018"
					// Use the correct endpoint URI that matches our event consumer service
					endpointURI := "http://ptp-event-consumer-service:27017/event"

					logrus.Infof("Full node name: %s", fullNodeName)
					logrus.Infof("EndpointUri: %s", endpointURI)
					logrus.Infof("Publisher URL: %s", publisherURL)
					logrus.Infof("Consumer URL: %s", consumerURL)

					// Check if the event consumer is ready
					logrus.Info("Checking if event consumer is ready...")
					if err := checkServiceHealth(consumerURL+"/health", 5*time.Second); err != nil {
						logrus.Warnf("Failed to check consumer health: %v", err)
					}

					// STEP 1: Subscribe to clock class events
					logrus.Info("=== STEP 1: Subscribing to clock class events ===")
					clockClassResourceAddress := event.FormatResourceAddress(event.ClockClassResourceAddress, fullNodeName)
					logrus.Infof("Clock class resource address: %s", clockClassResourceAddress)
					subscriptionErr := event.SubscribeToEvent(publisherURL, clockClassResourceAddress, endpointURI)
					if subscriptionErr != nil {
						logrus.Errorf("Clock class subscription failed: %v", subscriptionErr)
						Fail(fmt.Sprintf("Failed to subscribe to clock class events: %v", subscriptionErr))
					}
					logrus.Info("SUCCESS: Clock class subscription created successfully")

					// STEP 1.1: Subscribe to GNSS events
					logrus.Info("=== STEP 1.1: Subscribing to GNSS events ===")
					gnssResourceAddress := event.FormatResourceAddress(event.GNSSSyncStatusResourceAddress, fullNodeName)
					logrus.Infof("GNSS resource address: %s", gnssResourceAddress)
					gnssSubscriptionErr := event.SubscribeToEvent(publisherURL, gnssResourceAddress, endpointURI)
					if gnssSubscriptionErr != nil {
						logrus.Errorf("GNSS subscription failed: %v", gnssSubscriptionErr)
						Fail(fmt.Sprintf("Failed to subscribe to GNSS events: %v", gnssSubscriptionErr))
					}
					logrus.Info("SUCCESS: GNSS subscription created successfully")

					// STEP 1.2: Subscribe to PTP state events
					logrus.Info("=== STEP 1.2: Subscribing to PTP state events ===")
					ptpStateResourceAddress := event.FormatResourceAddress(event.LockStateResourceAddress, fullNodeName)
					logrus.Infof("PTP state resource address: %s", ptpStateResourceAddress)
					ptpStateSubscriptionErr := event.SubscribeToEvent(publisherURL, ptpStateResourceAddress, endpointURI)
					if ptpStateSubscriptionErr != nil {
						logrus.Errorf("PTP state subscription failed: %v", ptpStateSubscriptionErr)
						Fail(fmt.Sprintf("Failed to subscribe to PTP state events: %v", ptpStateSubscriptionErr))
					}
					logrus.Info("SUCCESS: PTP state subscription created successfully")

					stopChan := make(chan struct{})
					var once sync.Once
					safeClose := func() {
						once.Do(func() {
							close(stopChan)
						})
					}
					defer safeClose()

					// Start coldboot in background
					go coldBootInBackground(stopChan, fullConfig)

					// STEP 2: Verify events during GNSS loss (holdover state after cold boot)
					// Keep cold boot running while verifying events
					verifyClockClassEvent(fullConfig, consumerURL, fbprotocol.ClockClass7)
					verifyGnssEvent(fullConfig, consumerURL, ptpEvent.ANTENNA_DISCONNECTED)
					verifyPtpStateEvent(fullConfig, consumerURL, ptpEvent.HOLDOVER)

					// Now stop cold boot to allow GNSS to recover
					logrus.Info("Stopping cold boot to allow GNSS recovery...")
					safeClose()

					// Give GNSS time to fully recover
					time.Sleep(pkg.Timeout10Seconds)

					// STEP 3: Verify events during GNSS recovery (locked state)
					// Now wait for a system to go back to LOCKED (ClockClass 6)
					verifyClockClassEvent(fullConfig, consumerURL, fbprotocol.ClockClass6)
					verifyGnssEvent(fullConfig, consumerURL, ptpEvent.SYNCHRONIZED)
					verifyPtpStateEvent(fullConfig, consumerURL, ptpEvent.LOCKED)
				})
			})

		})
	})
})

func checkStabilityOfWPCGMUsingMetrics(fullConfig testconfig.TestConfig) {
	checkProcessStatus(fullConfig, "1")
	checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))
	checkDPLLFrequencyState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))
	checkDPLLPhaseState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))
	checkClockState(fullConfig, "1")
	checkPTPNMEAStatus(fullConfig, "1")
}

func verifyEventsV1(expectedState string) {
	//TODO
	switch expectedState {
	case "LOCKED":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state LOCKED

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state LOCKED

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status LOCKED

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class LOCKED
		*/
	case "HOLDOVER":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state HOLDOVER

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state HOLDOVER

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status HOLDOVER

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class HOLDOVER

		*/

	case "FREERUN":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state FREERUN

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state FREERUN

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status FREERUN

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class FREERUN

		*/

	}

}

func verifyEventsV2(expectedState string) {
	//TODO
	switch expectedState {
	case "LOCKED":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state LOCKED

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state LOCKED

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status LOCKED

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class LOCKED
		*/
	case "HOLDOVER":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state HOLDOVER

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state HOLDOVER

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status HOLDOVER

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class HOLDOVER

		*/

	case "FREERUN":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state FREERUN

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state FREERUN

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status FREERUN

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class FREERUN

		*/

	}

}

func testCaseEnabled(testCase TestCase) bool {

	enabledTests, isSet := os.LookupEnv("ENABLE_TEST_CASE")

	if isSet {
		tokens := strings.Split(enabledTests, ",")
		for _, token := range tokens {
			token = strings.TrimSpace(token)
			if strings.Contains(token, string(testCase)) {
				return true
			}
		}
	}
	return false
}

func processRunning(input string, state string) (map[string]bool, error) {
	// Regular expression pattern
	processStatusPattern := `openshift_ptp_process_status\{config="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

	// Compile the regular expression
	processStatusRe := regexp.MustCompile(processStatusPattern)

	// Find matches
	processRunning := map[string]bool{"phc2sys": false, "ptp4l": false, "ts2phc": false, "gpspipe": false, "gpsd": false}

	scanner := bufio.NewScanner(strings.NewReader(input))
	timeout := 10 * time.Second
	start := time.Now()
	for scanner.Scan() {
		t := time.Now()
		elapsed := t.Sub(start)
		if elapsed > timeout {
			fmt.Println("Timed out when reading metrics")
			break
		}
		line := scanner.Text()
		if matches := processStatusRe.FindStringSubmatch(line); matches != nil {
			if _, ok := processRunning[matches[3]]; ok && matches[4] == state {
				processRunning[matches[3]] = true
			}

		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading input:", err)
		return nil, err
	}
	return processRunning, nil
}

func clockStateByProcesses(input string, state string) (map[string]bool, error) {
	// Regular expression pattern
	clockStatePattern := `openshift_ptp_clock_state\{iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

	// Compile the regular expression
	processStatusRe := regexp.MustCompile(clockStatePattern)

	// Find matches
	processClockState := map[string]bool{"phc2sys": false, "GM": false, "dpll": false, "ts2phc": false, "gnss": false}

	scanner := bufio.NewScanner(strings.NewReader(input))
	timeout := 10 * time.Second
	start := time.Now()
	for scanner.Scan() {
		t := time.Now()
		elapsed := t.Sub(start)
		if elapsed > timeout {
			fmt.Println("Timed out when reading metrics")
			break
		}
		line := scanner.Text()
		if matches := processStatusRe.FindStringSubmatch(line); matches != nil {
			if _, ok := processClockState[matches[3]]; ok && matches[4] == state {
				processClockState[matches[3]] = true
			}

		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading input:", err)
		return nil, err
	}
	return processClockState, nil
}

func getClockStateByProcess(metrics, process string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(metrics))

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "openshift_ptp_clock_state") && strings.Contains(line, fmt.Sprintf(`process="%s"`, process)) {
			// split line to get value
			parts := strings.Fields(line)
			if len(parts) == 2 {
				return parts[1], true
			}
		}
	}
	return "", false
}

func checkProcessStatus(fullConfig testconfig.TestConfig, state string) {
	/*
		# TYPE openshift_ptp_process_status gauge
		openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="phc2sys"} 1
		openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ptp4l"} 1
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpsd"} 1
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"}
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"} 1
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ts2phc"} 1
	*/
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpProcessStatus),
		"Process status metrics are not detected")

	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("phc2sys"),
		"phc2ys process status not detected")

	time.Sleep(10 * time.Second)
	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	ret, err := processRunning(buf.String(), state)
	Expect(err).To(BeNil())
	Expect(ret["phc2sys"]).To(BeTrue(), fmt.Sprintf("Expected phc2sys to be  %s for GM", state))
	Expect(ret["ptp4l"]).To(BeTrue(), fmt.Sprintf("Expected ptp4l to be  %s for GM", state))
	Expect(ret["ts2phc"]).To(BeTrue(), fmt.Sprintf("Expected ts2phc to be  %s for GM", state))
	//TODO: Re-enable these checks once bugfix is merged
	// Expect(ret["gpspipe"]).To(BeTrue(), fmt.Sprintf("Expected gpspipe to be %s for GM", state))
	// Expect(ret["gpsd"]).To(BeTrue(), fmt.Sprintf("Expected gpsd to be q %s for GM", state))
}

func checkClockClassState(fullConfig testconfig.TestConfig, expectedState string) {
	By(fmt.Sprintf("Waiting for clock class to become %s", expectedState))

	clockClassPattern := `openshift_ptp_clock_class\{node="([^"]+)",process="([^"]+)"\} (\d+)`
	clockClassRe := regexp.MustCompile(clockClassPattern)

	Eventually(func() bool {
		// Get the latest metrics output
		buf, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Error executing curl: %v\n", err)
			return false
		}

		// Scan line by line
		scanner := bufio.NewScanner(strings.NewReader(buf.String()))
		for scanner.Scan() {
			line := scanner.Text()

			// Check if the line matches the clock class pattern
			matches := clockClassRe.FindStringSubmatch(line)
			if matches != nil && len(matches) >= 4 {
				fmt.Fprintf(GinkgoWriter, "Matched line: %v\n", matches)
				process := matches[2]
				class := matches[3]
				if strings.TrimSpace(process) == "ptp4l" && strings.TrimSpace(class) == expectedState {
					_, _ = fmt.Fprintf(GinkgoWriter, "Found clock class %s for process %s\n", class, process)
					return true
				} else {
					_, _ = fmt.Fprintf(GinkgoWriter, "Match found but process=%s class=%s, not matching yet...\n", process, class)
				}
			}
		}

		// If error during scan
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(GinkgoWriter, "Error scanning metrics: %v\n", err)
		}

		return false
	}, pkg.TimeoutIn5Minutes, pkg.Timeout1Seconds).Should(BeTrue(),
		fmt.Sprintf("Expected ptp4l clock class to eventually be %s for GM", expectedState))
}

func checkDPLLFrequencyState(fullConfig testconfig.TestConfig, state string) {
	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll process
		# TYPE openshift_ptp_frequency_status gauge
		# openshift_ptp_frequency_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
	*/
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpFrequencyStatus),
		"frequency status metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	freqStatusPattern := `openshift_ptp_frequency_status\{from="([^"]+)",iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

	// Compile the regular expression
	freqStatusRe := regexp.MustCompile(freqStatusPattern)

	// Find matches
	freqStatusMap := map[string]bool{"dpll": false}

	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	timeout := 10 * time.Second
	start := time.Now()
	for scanner.Scan() {
		t := time.Now()
		elapsed := t.Sub(start)
		if elapsed > timeout {
			Fail("Timedout reading input from metrics")
		}
		line := scanner.Text()
		if matches := freqStatusRe.FindStringSubmatch(line); matches != nil {
			if _, ok := freqStatusMap[matches[4]]; ok && matches[5] == state {
				freqStatusMap[matches[4]] = true
				break
			}

		}
	}
	if err := scanner.Err(); err != nil {
		Fail(fmt.Sprintf("Error reading input from metrics: %s", err))
	}
	Expect(freqStatusMap["dpll"]).To(BeTrue(), fmt.Sprintf("Expected dpll frequency status to be %s for GM", state))
}

func checkDPLLPhaseState(fullConfig testconfig.TestConfig, state string) {
	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll process
		# TYPE openshift_ptp_phase_status gauge
		# openshift_ptp_phase_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
	*/
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpPhaseStatus),
		"frequency status metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	phaseStatusPattern := `openshift_ptp_phase_status\{from="([^"]+)",iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

	// Compile the regular expression
	phaseStatusRe := regexp.MustCompile(phaseStatusPattern)

	// Find matches
	phaseStatusMap := map[string]bool{"dpll": false}

	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	timeout := 10 * time.Second
	start := time.Now()
	for scanner.Scan() {
		t := time.Now()
		elapsed := t.Sub(start)
		if elapsed > timeout {
			Fail("Timedout reading input from metrics")
		}
		line := scanner.Text()
		if matches := phaseStatusRe.FindStringSubmatch(line); matches != nil {
			if _, ok := phaseStatusMap[matches[4]]; ok && matches[5] == state {
				phaseStatusMap[matches[4]] = true
				break
			}

		}
	}
	if err := scanner.Err(); err != nil {
		Fail(fmt.Sprintf("Error reading input: %s", err))
	}
	Expect(phaseStatusMap["dpll"]).To(BeTrue(), fmt.Sprintf("Expected dpll phase status to be %s for GM", state))
}

func checkClockState(fullConfig testconfig.TestConfig, state string) {
	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll and ts2phc processes
		# TYPE openshift_ptp_clock_state gauge
		openshift_ptp_clock_state{iface="CLOCK_REALTIME",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="phc2sys"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="GM"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="gnss"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ts2phc"} 1
	*/
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(ContainSubstring(metrics.OpenshiftPtpClockState),
		"Clock state metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	ret, err := clockStateByProcesses(buf.String(), state)
	Expect(err).To(BeNil())
	Expect(ret["GM"]).To(BeTrue(), fmt.Sprintf("Expected GM clock state to be %s for GM", state))
	//Not needed for now
	// Expect(ret["phc2sys"]).To(BeTrue(), fmt.Sprintf("Expected phc2sys clock state to be %s for GM", state))
	// Expect(ret["dpll"]).To(BeTrue(), fmt.Sprintf("Expected dpll clock state to be %s for GM", state))
	// Expect(ret["ts2phc"]).To(BeTrue(), fmt.Sprintf("Expected ts2phc clock state to be %s for GM", state))
	// Expect(ret["gnss"]).To(BeTrue(), fmt.Sprintf("Expected gnss clock state to be %s for GM", state))
}

func checkClockStateForProcess(fullConfig testconfig.TestConfig, process string, state string) {
	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll and ts2phc processes
		# TYPE openshift_ptp_clock_state gauge
		openshift_ptp_clock_state{iface="CLOCK_REALTIME",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="phc2sys"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="GM"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="gnss"} 1
		openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ts2phc"} 1
	*/
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(ContainSubstring(metrics.OpenshiftPtpClockState),
		"Clock state metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	retState, found := getClockStateByProcess(buf.String(), process)
	Expect(found).To(BeTrue(), fmt.Sprintf("Expected %s clock state to be %s for GM but found %s", process, state, retState))
	Expect(retState).To(Equal(state), fmt.Sprintf("Expected %s clock state to be %s for GM %s", process, state, buf.String()))
}

func checkPTPNMEAStatus(fullConfig testconfig.TestConfig, expectedState string) {
	nmeaStatusPattern := `openshift_ptp_nmea_status\{iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`
	nmeaStatusRe := regexp.MustCompile(nmeaStatusPattern)

	Eventually(func() bool {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		scanner := bufio.NewScanner(strings.NewReader(buf.String()))
		foundState := ""

		for scanner.Scan() {
			line := scanner.Text()
			if matches := nmeaStatusRe.FindStringSubmatch(line); matches != nil {
				if len(matches) < 4 {
					continue
				}
				process := matches[3]
				state := matches[4]
				fmt.Fprintf(GinkgoWriter, "Matched process=%s, state=%s\n", process, state)
				if process == "ts2phc" {
					foundState = state
					break
				}
			}
		}

		return foundState == expectedState
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeTrue(), fmt.Sprintf("Expected ts2phc NMEA state to be %s for GM", expectedState))
}

func coldBootInBackground(stopChan chan struct{}, fullConfig testconfig.TestConfig) {
	for {
		select {
		case <-stopChan:
			fmt.Fprintf(GinkgoWriter, "Stopping coldboot loop\n")
			return
		default:
			// Send coldboot
			_, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod,
				pkg.PtpContainerName, []string{"ubxtool", "-P", "29.20", "-p", "COLDBOOT", "-v", "3"})
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "Error running coldboot: %v\n", err)
			} else {
				fmt.Fprintf(GinkgoWriter, "Coldboot sent\n")
			}
			time.Sleep(2 * time.Second) // Keep hammering every 2 sec
		}
	}
}

func waitForClockClass(fullConfig testconfig.TestConfig, expectedState string) {
	start := time.Now()

	for {
		if checkClockClassStateReturnBool(fullConfig, expectedState) {
			fmt.Fprintf(GinkgoWriter, "✅ Clock class reached %s\n", expectedState)
			break
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Clock class not yet %s, retrying...\n", expectedState)
		}

		time.Sleep(pkg.TimeoutInterval2Seconds)

		if time.Since(start) > pkg.TimeoutIn3Minutes {
			Fail(fmt.Sprintf("Timed out waiting for clock class %s", expectedState))
			break
		}
	}
}

func checkClockClassStateReturnBool(fullConfig testconfig.TestConfig, expectedState string) bool {
	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	scanner := bufio.NewScanner(strings.NewReader(buf.String()))

	clockClassPattern := `openshift_ptp_clock_class\{node="([^"]+)",process="([^"]+)"\} (\d+)`
	clockClassRe := regexp.MustCompile(clockClassPattern)

	for scanner.Scan() {
		line := scanner.Text()
		if matches := clockClassRe.FindStringSubmatch(line); matches != nil {
			process := matches[2]
			class := matches[3]
			if strings.TrimSpace(process) == "ptp4l" && strings.TrimSpace(class) == expectedState {
				return true
			}
		}
	}
	return false
}

func setupPTPEventPortForward(fullConfig testconfig.TestConfig) error {
	// Get the node name for the PTP event publisher service
	// The service name uses only the first part of the node name (before the first dot)
	fullNodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
	nodeName := strings.Split(fullNodeName, ".")[0]

	// Verify the service is accessible directly
	serviceName := fmt.Sprintf("ptp-event-publisher-service-%s", nodeName)
	logrus.Infof("Setting up port-forward to PTP event publisher service: %s", serviceName)

	// Kill any existing port-forward on port 9043
	killCmd := exec.Command("pkill", "-f", "oc.*port-forward.*9043")
	killCmd.Run() // Ignore errors, just try to kill existing processes
	time.Sleep(1 * time.Second)

	// Set up port-forward from the PTP event publisher service to localhost
	portForwardCmd := exec.Command("oc", "port-forward", "-n", "openshift-ptp", "svc/"+serviceName, "9043:9043")

	// Preserve KUBECONFIG
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		portForwardCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	}

	portForwardCmd.Stdout = os.Stdout
	portForwardCmd.Stderr = os.Stderr

	err := portForwardCmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start port-forward: %v", err)
	}

	// Wait for port-forward to establish and verify it's working
	logrus.Info("Waiting for port-forward to establish...")
	Eventually(func() error {
		resp, err := http.Get("http://localhost:9043/api/ocloudNotifications/v2/subscriptions")
		if err != nil {
			return fmt.Errorf("port-forward not ready: %v", err)
		}
		defer resp.Body.Close()
		// Accept both 200 (OK) and 405 (Method Not Allowed) as valid responses
		// 200 means GET is supported and returns existing subscriptions
		// 405 means GET is not supported but service is accessible
		if resp.StatusCode != 200 && resp.StatusCode != 405 {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		return nil
	}, 30*time.Second, 2*time.Second).Should(Succeed(), "Port-forward not established")

	logrus.Info("Port-forward established successfully")
	return nil
}

func setupEventConsumerPodAndService() error {
	logrus.Info("Setting up event consumer pod and service")

	// Clean up any existing pod and service first
	cleanupCmd := exec.Command("kubectl", "delete", "pod", "ptp-event-consumer", "-n", "openshift-ptp", "--ignore-not-found=true")
	cleanupCmd.Run() // Ignore errors, just try to clean up
	cleanupSvcCmd := exec.Command("kubectl", "delete", "service", "ptp-event-consumer-service", "-n", "openshift-ptp", "--ignore-not-found=true")
	cleanupSvcCmd.Run()         // Ignore errors, just try to clean up
	time.Sleep(2 * time.Second) // Wait for cleanup

	// Create the event consumer pod using YAML file
	logrus.Info("Creating event consumer pod...")
	// Use a relative path since we're already in the test/conformance/serial directory
	podYamlPath := "event-consumer-pod.yaml"
	logrus.Infof("Using pod YAML path: %s", podYamlPath)

	podCmd := exec.Command("kubectl", "apply", "-f", podYamlPath)
	podOutput, err := podCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create event consumer pod: %v, output: %s", err, string(podOutput))
	}
	logrus.Infof("Pod creation output: %s", string(podOutput))

	// Wait for the pod to be ready
	logrus.Info("Waiting for event consumer pod to be ready...")
	waitCmd := exec.Command("kubectl", "wait", "--for=condition=ready", "pod/ptp-event-consumer", "-n", "openshift-ptp", "--timeout=60s")
	if err := waitCmd.Run(); err != nil {
		return fmt.Errorf("failed to wait for event consumer pod to be ready: %v", err)
	}

	// Wait a bit more for pod to be fully ready
	time.Sleep(5 * time.Second)

	// Check pod status
	logrus.Info("Checking pod status...")
	statusCmd := exec.Command("kubectl", "get", "pod", "ptp-event-consumer", "-n", "openshift-ptp", "-o", "jsonpath={.status.phase}")
	statusOutput, err := statusCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get pod status: %v", err)
	}
	logrus.Infof("Pod status: %s", string(statusOutput))

	// Create the service for the event consumer using YAML file
	logrus.Info("Creating event consumer service...")
	// Use a relative path since we're already in the test/conformance/serial directory
	serviceYamlPath := "event-consumer-service.yaml"
	logrus.Infof("Using service YAML path: %s", serviceYamlPath)

	serviceCmd := exec.Command("kubectl", "apply", "-f", serviceYamlPath)
	serviceOutput, err := serviceCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create event consumer service: %v, output: %s", err, string(serviceOutput))
	}
	logrus.Infof("Service creation output: %s", string(serviceOutput))

	// Wait a bit for the service to be ready
	time.Sleep(5 * time.Second)

	// The pod will automatically compile and run the event consumer
	// Wait a bit for the server to start
	logrus.Info("Waiting for event consumer to start...")
	time.Sleep(10 * time.Second)

	// Test the event consumer health endpoint
	logrus.Info("Testing event consumer health endpoint...")
	testCmd := exec.Command("kubectl", "exec", "-n", "openshift-ptp", "ptp-event-consumer", "--", "wget", "-qO-", "http://localhost:27017/health")
	if err := testCmd.Run(); err != nil {
		logrus.Warnf("Failed to test event consumer health via kubectl exec: %v", err)
		// Try alternative health check method
		logrus.Info("Trying alternative health check method...")
		if err := checkServiceHealthWithRetry("http://localhost:27018/health", 10*time.Second, 1*time.Second); err != nil {
			return fmt.Errorf("failed to test event consumer health: %v", err)
		}
	}

	logrus.Info("Event consumer pod and service setup completed successfully")
	return nil
}

func cleanupEventConsumerPodAndService() error {
	logrus.Info("Cleaning up event consumer pod and service")

	// Delete the service using YAML file
	serviceYamlPath := "event-consumer-service.yaml"
	svcCmd := exec.Command("kubectl", "delete", "-f", serviceYamlPath, "--ignore-not-found=true")
	if err := svcCmd.Run(); err != nil {
		logrus.Warnf("Failed to delete event consumer service: %v", err)
	}

	// Delete the pod using YAML file
	podYamlPath := "event-consumer-pod.yaml"
	podCmd := exec.Command("kubectl", "delete", "-f", podYamlPath, "--ignore-not-found=true")
	if err := podCmd.Run(); err != nil {
		logrus.Warnf("Failed to delete event consumer pod: %v", err)
	}

	logrus.Info("Event consumer pod and service cleanup completed")
	return nil
}

func setupEventConsumerPortForward() error {
	logrus.Info("Setting up port-forward to event consumer service")

	// Kill any existing port-forward on port 27018
	killCmd := exec.Command("pkill", "-f", "oc.*port-forward.*27018")
	killCmd.Run() // Ignore errors, just try to kill existing processes
	time.Sleep(1 * time.Second)

	// Set up port-forward from the event consumer service to localhost
	portForwardCmd := exec.Command("oc", "port-forward", "-n", "openshift-ptp", "svc/ptp-event-consumer-service", "27018:27017")

	// Preserve KUBECONFIG
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		portForwardCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	}

	portForwardCmd.Stdout = os.Stdout
	portForwardCmd.Stderr = os.Stderr

	err := portForwardCmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start event consumer port-forward: %v", err)
	}

	// Wait for port-forward to establish and verify it's working
	logrus.Info("Waiting for event consumer port-forward to establish...")
	if err := checkServiceHealthWithRetry("http://localhost:27018/health", 30*time.Second, 2*time.Second); err != nil {
		return fmt.Errorf("event consumer port-forward not established: %v", err)
	}

	logrus.Info("Event consumer port-forward established successfully")
	return nil
}

// verifyClockClassEvent verifies that a clock class event is received
func verifyClockClassEvent(fullConfig testconfig.TestConfig, consumerURL string, expectedClockClass fbprotocol.ClockClass) {

	logrus.Infof("=== Verifying clock class %d event (holdover state) ===", expectedClockClass)

	// Check for existing events before starting
	logrus.Info("Checking for existing events before clock class change...")
	existingEvents, err := event.ReadEventsFromHTTP(consumerURL)
	if err != nil {
		logrus.Warnf("Failed to read existing events: %v", err)
	} else {
		logrus.Infof("Found %d existing events before clock class change", len(existingEvents))
		for i, eventStr := range existingEvents {
			logrus.Infof("Existing event %d: %s", i+1, eventStr[:min(len(eventStr), 200)])
		}
	}

	// First, wait for the clock class to actually change to the expected value
	logrus.Infof("Waiting for clock class to change to %d...", expectedClockClass)
	waitForClockClass(fullConfig, strconv.Itoa(int(expectedClockClass)))

	// Now that clock class has changed, wait for the corresponding event
	logrus.Infof("Clock class changed to %d, now waiting for clock class %d event...", expectedClockClass, expectedClockClass)
	result := event.VerifyEventWithPredicate(
		consumerURL,
		event.IsClockClassEventPredicate(expectedClockClass),
		10*time.Second,
	)

	if !result.Success {
		// If we get here, the test failed
		if result.Error != nil {
			logrus.Errorf("Event verification failed with error: %v", result.Error)
			Fail(fmt.Sprintf("Failed to verify clock class %d event: %v", expectedClockClass, result.Error))
		} else {
			logrus.Errorf("Clock class %d event not found. Retrieved %d events, waited %v", expectedClockClass, result.EventCount, result.WaitTime)
			Fail(fmt.Sprintf("Clock class %d event not found. Retrieved %d events, waited %v", expectedClockClass, result.EventCount, result.WaitTime))
		}
	}

	logrus.Infof("SUCCESS: Successfully verified clock class %d event (holdover state)! Retrieved %d events, waited %v", expectedClockClass, result.EventCount, result.WaitTime)
}

// verifyGnssEvent verifies that a GNSS state change event is received
func verifyGnssEvent(fullConfig testconfig.TestConfig, consumerURL string, expectedState ptpEvent.SyncState) {
	logrus.Infof("=== Verifying GNSS state %s event ===", string(expectedState))

	// Check for existing events before starting
	logrus.Info("Checking for existing events before GNSS state change...")
	existingEvents, err := event.ReadEventsFromHTTP(consumerURL)
	if err != nil {
		logrus.Warnf("Failed to read existing events: %v", err)
	} else {
		logrus.Infof("Found %d existing events before GNSS state change", len(existingEvents))
		for i, eventStr := range existingEvents {
			logrus.Infof("Existing event %d: %s", i+1, eventStr[:min(len(eventStr), 200)])
		}
	}

	// Wait for the GNSS state change event
	logrus.Infof("Waiting for GNSS state %s event...", string(expectedState))
	result := event.VerifyEventWithPredicate(
		consumerURL,
		event.IsGnssEventPredicate(expectedState),
		10*time.Second,
	)

	if !result.Success {
		// If we get here, the test failed
		if result.Error != nil {
			logrus.Errorf("Event verification failed with error: %v", result.Error)
			Fail(fmt.Sprintf("Failed to verify GNSS state %s event: %v", string(expectedState), result.Error))
		} else {
			logrus.Errorf("GNSS state %s event not found. Retrieved %d events, waited %v", string(expectedState), result.EventCount, result.WaitTime)
			Fail(fmt.Sprintf("GNSS state %s event not found. Retrieved %d events, waited %v", string(expectedState), result.EventCount, result.WaitTime))
		}
	}

	logrus.Infof("SUCCESS: Successfully verified GNSS state %s event! Retrieved %d events, waited %v", string(expectedState), result.EventCount, result.WaitTime)
}

// verifyPtpStateEvent verifies that a PTP state change event is received
func verifyPtpStateEvent(fullConfig testconfig.TestConfig, consumerURL string, expectedState ptpEvent.SyncState) {
	logrus.Infof("=== Verifying PTP state %s event ===", string(expectedState))

	// Check for existing events before starting
	logrus.Info("Checking for existing events before PTP state change...")
	existingEvents, err := event.ReadEventsFromHTTP(consumerURL)
	if err != nil {
		logrus.Warnf("Failed to read existing events: %v", err)
	} else {
		logrus.Infof("Found %d existing events before PTP state change", len(existingEvents))
		for i, eventStr := range existingEvents {
			logrus.Infof("Existing event %d: %s", i+1, eventStr[:min(len(eventStr), 200)])
		}
	}

	// Wait for the PTP state change event
	logrus.Infof("Waiting for PTP state %s event...", string(expectedState))
	result := event.VerifyEventWithPredicate(
		consumerURL,
		event.IsPtpStateEventPredicate(expectedState),
		10*time.Second,
	)

	if !result.Success {
		// If we get here, the test failed
		if result.Error != nil {
			logrus.Errorf("Event verification failed with error: %v", result.Error)
			Fail(fmt.Sprintf("Failed to verify PTP state %s event: %v", string(expectedState), result.Error))
		} else {
			logrus.Errorf("PTP state %s event not found. Retrieved %d events, waited %v", string(expectedState), result.EventCount, result.WaitTime)
			Fail(fmt.Sprintf("PTP state %s event not found. Retrieved %d events, waited %v", string(expectedState), result.EventCount, result.WaitTime))
		}
	}

	logrus.Infof("SUCCESS: Successfully verified PTP state %s event! Retrieved %d events, waited %v", string(expectedState), result.EventCount, result.WaitTime)
}

// checkServiceHealth performs a health check on a service endpoint
func checkServiceHealth(serviceURL string, timeout time.Duration) error {
	logrus.Infof("Checking health of service: %s", serviceURL)

	resp, err := http.Get(serviceURL)
	if err != nil {
		return fmt.Errorf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	logrus.Infof("Health check response - Status: %d, Body: %s", resp.StatusCode, string(body))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed with status: %d", resp.StatusCode)
	}

	return nil
}

// checkServiceHealthWithRetry performs a health check with retry logic
func checkServiceHealthWithRetry(serviceURL string, timeout time.Duration, interval time.Duration) error {
	logrus.Infof("Checking health of service with retry: %s", serviceURL)

	Eventually(func() error {
		return checkServiceHealth(serviceURL, timeout)
	}, timeout, interval).Should(Succeed())
	return nil
}

func checkAvailableEventTypes(fullConfig testconfig.TestConfig) {
	logrus.Info("Checking available publishers...")

	// Get the node name for the PTP event publisher service
	fullNodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
	nodeName := strings.Split(fullNodeName, ".")[0]
	serviceURL := fmt.Sprintf("http://ptp-event-publisher-service-%s.openshift-ptp.svc.cluster.local:9043", nodeName)

	// Get available publishers
	resp, err := http.Get(serviceURL + "/api/ocloudNotifications/v2/publishers")
	if err != nil {
		logrus.Warnf("Failed to get publishers: %v", err)
	} else {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		logrus.Infof("Publishers response - Status: %d, Body: %s", resp.StatusCode, string(body))
	}
}

func checkServiceDetails(fullConfig testconfig.TestConfig) {
	// Get the node name for the PTP event publisher service
	fullNodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
	nodeName := strings.Split(fullNodeName, ".")[0]
	serviceName := fmt.Sprintf("ptp-event-publisher-service-%s", nodeName)

	logrus.Infof("Checking service: %s", serviceName)

	// Check if service exists
	cmd := exec.Command("oc", "get", "svc", serviceName, "-n", "openshift-ptp", "-o", "yaml")
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		logrus.Errorf("Failed to get service details: %v", err)
		logrus.Errorf("Command output: %s", out.String())
		return
	}

	logrus.Infof("Service details:\n%s", out.String())

	// Check service endpoints
	cmd = exec.Command("oc", "get", "endpoints", serviceName, "-n", "openshift-ptp", "-o", "yaml")
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	}

	out.Reset()
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	if err != nil {
		logrus.Errorf("Failed to get endpoints details: %v", err)
		logrus.Errorf("Command output: %s", out.String())
		return
	}

	logrus.Infof("Endpoints details:\n%s", out.String())

	// Check if the service is accessible directly
	logrus.Info("Testing service accessibility directly...")
	serviceURL := fmt.Sprintf("http://ptp-event-publisher-service-%s.openshift-ptp.svc.cluster.local:9043", nodeName)
	resp, err := http.Get(serviceURL + "/api/ocloudNotifications/v2/subscriptions")
	if err != nil {
		logrus.Errorf("Direct service access test failed: %v", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	logrus.Infof("Direct service access test response - Status: %d, Body: %s", resp.StatusCode, string(body))
}
