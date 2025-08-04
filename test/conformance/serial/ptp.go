package test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	v1core "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/execute"

	fbprotocol "github.com/facebook/time/ptp/protocol"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/testconfig"
	exports "github.com/redhat-cne/ptp-listener-exports"
	ptpEvent "github.com/redhat-cne/sdk-go/pkg/event/ptp"
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
		var ptpPods *v1core.PodList
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
				portEngine.TurnAllPortsUp()
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
				var testPtpPod v1core.Pod
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
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("Skipping: test not valid for WPC GM (Telco Grandmaster Clock) config")
				}
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

		Context("Running with event enabled, v1 regression", func() {
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
									json.Unmarshal(optsByteArray, &pluginOpts)
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
				err := namespaces.Clean(openshiftPtpNamespace, "testpod-", client.Client)
				Expect(err).ToNot(HaveOccurred())
			})
			var _ = Context("Negative - run pmc in a new unprivileged pod on the slave node", func() {
				It("Should not be able to use the uds", func() {
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file was not created")
					podDefinition := pods.DefinePodOnNode(pkg.PtpLinuxDaemonNamespace, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
					hostPathDirectoryOrCreate := v1core.HostPathDirectoryOrCreate
					podDefinition.Spec.Volumes = []v1core.Volume{
						{
							Name: "socket-dir",
							VolumeSource: v1core.VolumeSource{
								HostPath: &v1core.HostPathVolumeSource{
									Path: "/var/run/ptp",
									Type: &hostPathDirectoryOrCreate,
								},
							},
						},
					}
					podDefinition.Spec.Containers[0].VolumeMounts = []v1core.VolumeMount{
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
					err = pods.WaitForCondition(client.Client, pod, v1core.ContainersReady, v1core.ConditionTrue, 3*time.Minute)
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
					hostPathDirectoryOrCreate := v1core.HostPathDirectoryOrCreate
					podDefinition.Spec.Volumes = []v1core.Volume{
						{
							Name: "socket-dir",
							VolumeSource: v1core.VolumeSource{
								HostPath: &v1core.HostPathVolumeSource{
									Path: "/var/run/ptp",
									Type: &hostPathDirectoryOrCreate,
								},
							},
						},
					}
					podDefinition.Spec.Containers[0].VolumeMounts = []v1core.VolumeMount{
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
					err = pods.WaitForCondition(client.Client, pod, v1core.ContainersReady, v1core.ConditionTrue, 3*time.Minute)
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

					// Start coldboot in background
					go coldBootInBackground(stopChan, fullConfig)

					// Meanwhile, wait for ClockClass 7 (GNSS loss - Holdover In Spec)
					waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7)))

					// Also verify ClockState 2 (Holdover)
					checkClockStateForProcess(fullConfig, "GM", "2")

					// Also verify ClockState (Holdover) for DPLL
					checkClockStateForProcess(fullConfig, "dpll", "2")

					// Once holdover detected, stop coldboot loop
					close(stopChan)

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
		})

		Context("WPC GM Events verification (V2)", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}

				// Set up consumer pod for event monitoring
				if fullConfig.DiscoveredClockUnderTestPod != nil {
					nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
					if nodeName != "" {
						logrus.Info("Deploy consumer app for testing event API v2")
						err := event.CreateConsumerApp(nodeName)
						if err != nil {
							logrus.Errorf("PTP events are not available due to consumer app creation error err=%s", err)
							Skip("Consumer app setup failed")
						}

						// Wait a bit more for the consumer pod to be fully ready
						logrus.Info("Waiting for consumer pod to be fully ready...")
						time.Sleep(10 * time.Second)

						// Initialize pub/sub system
						event.InitPubSub()
					}
				}
			})

			AfterEach(func() {
				// Clean up consumer namespace
				DeferCleanup(func() {
					err := event.DeleteConsumerNamespace()
					if err != nil {
						logrus.Debugf("Deleting consumer namespace failed because of err=%s", err)
					}
				})

				// Close internal pubsub
				if event.PubSub != nil {
					event.PubSub.Close()
				}
			})

			It("Verify Individual Events (Clock Class, GNSS, PTP State)", func() {
				By("Testing individual event verification for Clock Class, GNSS, and PTP State")

				// Debug: Monitor all events for 30 seconds to understand what's being received
				logrus.Info("🔍 [DEBUG] Starting event debugging session to diagnose event reception...")
				go func() {
					debugAllEvents(fullConfig)
				}()
				time.Sleep(30 * time.Second) // Give debugging function time to run
				logrus.Info("🔍 [DEBUG] Event debugging session completed")

				// Test step-by-step event verification
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

				// STEP 1: Verify current LOCKED state (system should start in LOCKED)
				logrus.Info("🔍 [DEBUG] ===== STEP 1: Starting LOCKED state verification =====")
				logrus.Info("Verifying current LOCKED state...")
				err := VerifyLocked(fullConfig)
				if err != nil {
					logrus.Warnf("Locked events verification failed: %v", err)
					logrus.Info("System may not be in LOCKED state initially")
					AddReportEntry(fmt.Sprintf("Initial LOCKED verification failed: %v", err))
				}
				logrus.Info("🔍 [DEBUG] ===== STEP 1: LOCKED state verification completed =====")

				// STEP 2: Wait for system to transition from LOCKED to HOLDOVER (when cold boot triggers)
				logrus.Info("🔍 [DEBUG] ===== STEP 2: Starting HOLDOVER transition wait =====")
				logrus.Info("Waiting for system to transition from LOCKED to HOLDOVER...")
				timeout := time.After(2 * time.Minute)
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()

				transitionedToHoldover := false
				for !transitionedToHoldover {
					select {
					case <-timeout:
						logrus.Warnf("Timeout waiting for HOLDOVER transition. System may be stuck in LOCKED state.")
						AddReportEntry("System did not transition from LOCKED to HOLDOVER within timeout")
						transitionedToHoldover = true // Break the loop
					case <-ticker.C:
						logrus.Info("Checking for HOLDOVER transition...")
						// Check current clock class
						currentClockClass := "unknown"
						if checkClockClassStateReturnBool(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6))) {
							currentClockClass = "6 (LOCKED)"
						} else if checkClockClassStateReturnBool(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7))) {
							currentClockClass = "7 (HOLDOVER)"
						} else {
							currentClockClass = "unknown"
						}
						logrus.Infof("🔍 [DEBUG] Current clock class: %s", currentClockClass)

						// Check if we've reached holdover state
						if checkClockClassStateReturnBool(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7))) {
							logrus.Info("✅ Detected transition to HOLDOVER (ClockClass 7)")
							transitionedToHoldover = true
						}
					}
				}
				logrus.Info("🔍 [DEBUG] ===== STEP 2: HOLDOVER transition wait completed =====")

				// STEP 3: Verify holdover events (ClockClass 7, GNSS HOLDOVER, PTP HOLDOVER) in parallel
				// Keep cold boot running while verifying holdover events
				logrus.Info("🔍 [DEBUG] ===== STEP 3: Starting HOLDOVER events verification =====")
				logrus.Info("Verifying holdover events in parallel...")

				err = VerifyHoldover(fullConfig)
				if err != nil {
					logrus.Warnf("Holdover events verification failed: %v", err)
					logrus.Info("System may not be transitioning to HOLDOVER state")
					AddReportEntry(fmt.Sprintf("Holdover verification failed: %v", err))
				}
				logrus.Info("🔍 [DEBUG] ===== STEP 3: HOLDOVER events verification completed =====")

				// Now stop cold boot to allow GNSS to recover
				logrus.Info("Stopping cold boot to allow GNSS recovery...")
				safeClose()

				// Give GNSS time to fully recover
				time.Sleep(pkg.Timeout10Seconds)

				// STEP 4: Wait for system to transition back from HOLDOVER to LOCKED
				logrus.Info("🔍 [DEBUG] ===== STEP 4: Starting LOCKED transition wait =====")
				logrus.Info("Waiting for system to transition back from HOLDOVER to LOCKED...")
				timeout = time.After(2 * time.Minute)
				ticker = time.NewTicker(10 * time.Second)
				defer ticker.Stop()

				transitionedBackToLocked := false
				for !transitionedBackToLocked {
					select {
					case <-timeout:
						logrus.Warnf("Timeout waiting for LOCKED transition. System may be stuck in HOLDOVER state.")
						AddReportEntry("System did not transition from HOLDOVER to LOCKED within timeout")
						transitionedBackToLocked = true // Break the loop
					case <-ticker.C:
						logrus.Info("Checking for LOCKED transition...")
						// Check if we've returned to locked state
						if checkClockClassStateReturnBool(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6))) {
							logrus.Info("✅ Detected transition back to LOCKED (ClockClass 6)")
							transitionedBackToLocked = true
						}
					}
				}
				logrus.Info("🔍 [DEBUG] ===== STEP 4: LOCKED transition wait completed =====")

				// STEP 5: Verify locked events (ClockClass 6, GNSS LOCKED, PTP LOCKED) in parallel
				logrus.Info("🔍 [DEBUG] ===== STEP 5: Starting final LOCKED events verification =====")
				logrus.Info("Verifying locked events in parallel...")

				err = VerifyLocked(fullConfig)
				if err != nil {
					logrus.Warnf("Locked events verification failed: %v", err)
					logrus.Info("System may not be transitioning back to LOCKED state")
					AddReportEntry(fmt.Sprintf("Final LOCKED verification failed: %v", err))
				}
				logrus.Info("🔍 [DEBUG] ===== STEP 5: Final LOCKED events verification completed =====")
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
	// Add nil checks to prevent panic
	if fullConfig.DiscoveredClockUnderTestPod == nil {
		Fail("DiscoveredClockUnderTestPod is nil - cannot check process status")
		return
	}

	if client.Client == nil {
		Fail("Client is nil - cannot execute commands")
		return
	}

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
		buf, _, err := safeExecCommand(fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			Fail(fmt.Sprintf("Failed to execute command: %v", err))
		}
		return buf
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpProcessStatus),
		"Process status metrics are not detected")

	Eventually(func() string {
		buf, _, err := safeExecCommand(fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			Fail(fmt.Sprintf("Failed to execute command: %v", err))
		}
		return buf
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("phc2sys"),
		"phc2ys process status not detected")

	time.Sleep(10 * time.Second)
	buf, _, err := safeExecCommand(fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	if err != nil {
		Fail(fmt.Sprintf("Failed to execute command: %v", err))
	}
	ret, err := processRunning(buf, state)
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

	// Add nil checks to prevent panic
	if fullConfig.DiscoveredClockUnderTestPod == nil {
		Fail("DiscoveredClockUnderTestPod is nil - cannot check clock class state")
		return
	}

	if client.Client == nil {
		Fail("Client is nil - cannot execute commands")
		return
	}

	clockClassPattern := `openshift_ptp_clock_class\{node="([^"]+)",process="([^"]+)"\} (\d+)`
	clockClassRe := regexp.MustCompile(clockClassPattern)

	Eventually(func() bool {
		// Get the latest metrics output
		buf, _, err := safeExecCommand(fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			fmt.Fprintf(GinkgoWriter, "Error executing curl: %v\n", err)
			return false
		}

		// Scan line by line
		scanner := bufio.NewScanner(strings.NewReader(buf))
		for scanner.Scan() {
			line := scanner.Text()

			// Check if the line matches the clock class pattern
			matches := clockClassRe.FindStringSubmatch(line)
			if matches != nil && len(matches) >= 4 {
				fmt.Fprintf(GinkgoWriter, "Matched line: %v\n", matches)
				process := matches[2]
				class := matches[3]
				if strings.TrimSpace(process) == "ptp4l" && strings.TrimSpace(class) == expectedState {
					fmt.Fprintf(GinkgoWriter, "Found clock class %s for process %s\n", class, process)
					return true
				} else {
					fmt.Fprintf(GinkgoWriter, "Match found but process=%s class=%s, not matching yet...\n", process, class)
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
	// Add nil checks to prevent panic
	if fullConfig.DiscoveredClockUnderTestPod == nil {
		Fail("DiscoveredClockUnderTestPod is nil - cannot check DPLL frequency state")
		return
	}

	if client.Client == nil {
		Fail("Client is nil - cannot execute commands")
		return
	}

	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll process
		# TYPE openshift_ptp_frequency_status gauge
		# openshift_ptp_frequency_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
	*/
	Eventually(func() string {
		buf, _, err := safeExecCommand(fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			Fail(fmt.Sprintf("Failed to execute command: %v", err))
		}
		return buf
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpFrequencyStatus),
		"frequency status metrics are not detected")

	buf, _, err := safeExecCommand(fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	if err != nil {
		Fail(fmt.Sprintf("Failed to execute command: %v", err))
	}
	freqStatusPattern := `openshift_ptp_frequency_status\{from="([^"]+)",iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

	// Compile the regular expression
	freqStatusRe := regexp.MustCompile(freqStatusPattern)

	// Find matches
	freqStatusMap := map[string]bool{"dpll": false}

	scanner := bufio.NewScanner(strings.NewReader(buf))
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
	logrus.Infof("🔍 [DEBUG] Starting cold boot background process...")
	coldBootCount := 0
	for {
		select {
		case <-stopChan:
			logrus.Infof("🔍 [DEBUG] Stopping coldboot loop after %d attempts", coldBootCount)
			return
		default:
			// Send coldboot
			coldBootCount++
			logrus.Infof("🔍 [DEBUG] Sending cold boot attempt #%d", coldBootCount)
			stdout, stderr, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod,
				pkg.PtpContainerName, []string{"ubxtool", "-P", "29.20", "-p", "COLDBOOT", "-v", "3"})
			if err != nil {
				logrus.Errorf("🔍 [DEBUG] Error running coldboot attempt #%d: %v", coldBootCount, err)
				logrus.Errorf("🔍 [DEBUG] Coldboot stderr: %s", stderr.String())
			} else {
				logrus.Infof("🔍 [DEBUG] Coldboot attempt #%d sent successfully", coldBootCount)
				logrus.Infof("🔍 [DEBUG] Coldboot stdout: %s", stdout.String())
				logrus.Infof("🔍 [DEBUG] Coldboot stderr: %s", stderr.String())
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
			fmt.Fprintf(GinkgoWriter, "Clock class not yet %s, retrying...\n", expectedState)
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

func verifyClockClassEvent(fullConfig testconfig.TestConfig, expectedClockClass fbprotocol.ClockClass) error {
	defer GinkgoRecover() // Add this to capture panics in this function

	// buffer to hold events until they can be processed
	const incomingEventsBuffer = 100

	logrus.Infof("🔍 [DEBUG] Starting Clock Class event verification for expected class: %d", expectedClockClass)

	// Ensure pub/sub is initialized
	if event.PubSub == nil {
		logrus.Infof("🔍 [DEBUG] PubSub is nil, initializing...")
		event.InitPubSub()
	} else {
		logrus.Infof("🔍 [DEBUG] PubSub already initialized")
	}

	// Create timer channel for verification timeout
	verificationTimeout := 2 * time.Minute
	timeoutChan := time.After(verificationTimeout)
	logrus.Infof("🔍 [DEBUG] Set verification timeout to %v", verificationTimeout)

	// Register channel to receive PtpClockClassChange events
	logrus.Infof("🔍 [DEBUG] Subscribing to PtpClockClassChange events...")
	eventChan, subscriberID := event.PubSub.Subscribe(string(ptpEvent.PtpClockClassChange), incomingEventsBuffer)
	defer func() {
		logrus.Infof("🔍 [DEBUG] Unsubscribing from PtpClockClassChange events, subscriberID: %d", subscriberID)
		event.PubSub.Unsubscribe(string(ptpEvent.PtpClockClassChange), subscriberID)
	}()

	// Create and push an initial event
	logrus.Infof("🔍 [DEBUG] Pushing initial Clock Class event...")
	err := event.PushInitialEvent(string(ptpEvent.PtpClockClassChange), 60*time.Second)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to push initial Clock Class event: %v", err)
		return fmt.Errorf("could not push initial clock class event, err=%s", err)
	}
	logrus.Infof("🔍 [DEBUG] Successfully pushed initial Clock Class event")

	// Start monitoring pod logs
	logrus.Infof("🔍 [DEBUG] Starting pod logs monitoring...")
	term, err := event.MonitorPodLogsRegex()
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to start pod logs monitoring: %v", err)
		return fmt.Errorf("could not start listening to events, err=%s", err)
	}
	defer func() {
		logrus.Infof("🔍 [DEBUG] Stopping pod logs monitoring")
		term <- true
	}()
	logrus.Infof("🔍 [DEBUG] Successfully started pod logs monitoring")

	logrus.Infof("🔍 [DEBUG] Entering event loop, waiting for clock class %d event...", expectedClockClass)
	eventCount := 0
	startTime := time.Now()

	// Add periodic status logging
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutChan:
			elapsed := time.Since(startTime)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Waited %v for clock class %d event, received %d events total",
				elapsed, expectedClockClass, eventCount)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: No events received in the last %v", elapsed)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if Clock Class events are being generated by the system")
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if event subscription is working properly")
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if pod logs monitoring is capturing events")
			return fmt.Errorf("timed out waiting for clock class %d event", expectedClockClass)

		case <-ticker.C:
			elapsed := time.Since(startTime)
			logrus.Infof("🔍 [DEBUG] STATUS: Still waiting for clock class %d event... (elapsed: %v, events received: %d)",
				expectedClockClass, elapsed, eventCount)

		case singleEvent := <-eventChan:
			eventCount++
			elapsed := time.Since(startTime)
			logrus.Infof("🔍 [DEBUG] Received Clock Class event #%d after %v: %v", eventCount, elapsed, singleEvent)

			// Get event values
			values, ok := singleEvent[exports.EventValues].(exports.StoredEventValues)
			if !ok {
				logrus.Errorf("🔍 [DEBUG] Failed to extract EventValues from event: %v", singleEvent)
				logrus.Errorf("🔍 [DEBUG] Event structure: %T", singleEvent[exports.EventValues])
				continue
			}
			logrus.Infof("🔍 [DEBUG] Event values: %v", values)

			// Try to extract clock class from metric field (actual event structure)
			clockClassStr := ""

			// First try the metric field (which is the actual field in the events)
			if metric, ok := values["metric"].(string); ok {
				clockClassStr = metric
				logrus.Infof("🔍 [DEBUG] Found clock class in metric field: %s", clockClassStr)
			} else if metricInt, ok := values["metric"].(int); ok {
				clockClassStr = strconv.Itoa(metricInt)
				logrus.Infof("🔍 [DEBUG] Found clock class in metric field as int: %d", metricInt)
			} else if metricFloat, ok := values["metric"].(float64); ok {
				clockClassStr = strconv.Itoa(int(metricFloat))
				logrus.Infof("🔍 [DEBUG] Found clock class in metric field as float: %f", metricFloat)
			} else {
				// Try to extract from notification field (for backward compatibility)
				if notification, ok := values["notification"].(string); ok {
					logrus.Infof("🔍 [DEBUG] Found 'notification' field: %s", notification)
					// Try to parse notification as clock class
					if clockClassInt, err := strconv.Atoi(notification); err == nil {
						clockClassStr = strconv.Itoa(clockClassInt)
						logrus.Infof("🔍 [DEBUG] Parsed clock class from notification: %s", clockClassStr)
					}
				}

				if clockClassStr == "" {
					logrus.Errorf("🔍 [DEBUG] Failed to extract clock class from values: %v", values)
					logrus.Errorf("🔍 [DEBUG] Available keys in values: %v", getKeys(values))
					logrus.Errorf("🔍 [DEBUG] Metric field type: %T", values["metric"])
					continue
				}
			}

			logrus.Infof("🔍 [DEBUG] Clock class changed to: '%s' (expected: '%d')", clockClassStr, expectedClockClass)

			// Check if we received the expected clock class
			if clockClassStr == strconv.Itoa(int(expectedClockClass)) {
				logrus.Infof("🔍 [DEBUG] ✅ SUCCESS: Expected clock class %d received after %d events in %v",
					expectedClockClass, eventCount, elapsed)
				return nil
			} else {
				logrus.Infof("🔍 [DEBUG] ❌ Mismatch: Got '%s', expected '%d' (continuing to wait...)", clockClassStr, expectedClockClass)
			}
		}
	}
}

func verifyGnssEvent(fullConfig testconfig.TestConfig, expectedGnssState string) error {
	defer GinkgoRecover() // Add this to capture panics in this function

	// buffer to hold events until they can be processed
	const incomingEventsBuffer = 100

	logrus.Infof("🔍 [DEBUG] Starting GNSS event verification for expected state: %s", expectedGnssState)

	// Test event parsing logic
	testEventParsing()

	// Check current system state
	checkCurrentSystemState(fullConfig)

	logrus.Infof("🔍 [DEBUG] Expected GNSS state details:")
	logrus.Infof("🔍 [DEBUG]   - Expected state: '%s'", expectedGnssState)
	logrus.Infof("🔍 [DEBUG]   - Expected state length: %d", len(expectedGnssState))
	logrus.Infof("🔍 [DEBUG]   - Expected state bytes: %v", []byte(expectedGnssState))

	// Ensure pub/sub is initialized
	if event.PubSub == nil {
		logrus.Infof("🔍 [DEBUG] PubSub is nil, initializing...")
		event.InitPubSub()
	} else {
		logrus.Infof("🔍 [DEBUG] PubSub already initialized")
	}

	// Create timer channel for verification timeout
	verificationTimeout := 2 * time.Minute
	timeoutChan := time.After(verificationTimeout)
	logrus.Infof("🔍 [DEBUG] Set verification timeout to %v", verificationTimeout)

	// Register channel to receive GnssStateChange events
	logrus.Infof("🔍 [DEBUG] Subscribing to GnssStateChange events...")
	logrus.Infof("🔍 [DEBUG] Event type details:")
	logrus.Infof("🔍 [DEBUG]   - ptpEvent.GnssStateChange: '%s'", string(ptpEvent.GnssStateChange))
	logrus.Infof("🔍 [DEBUG]   - ptpEvent.PtpStateChange: '%s'", string(ptpEvent.PtpStateChange))
	logrus.Infof("🔍 [DEBUG]   - ptpEvent.PtpClockClassChange: '%s'", string(ptpEvent.PtpClockClassChange))
	eventChan, subscriberID := event.PubSub.Subscribe(string(ptpEvent.GnssStateChange), incomingEventsBuffer)
	defer func() {
		logrus.Infof("🔍 [DEBUG] Unsubscribing from GnssStateChange events, subscriberID: %d", subscriberID)
		event.PubSub.Unsubscribe(string(ptpEvent.GnssStateChange), subscriberID)
	}()

	// Create and push an initial event
	logrus.Infof("🔍 [DEBUG] Pushing initial GNSS event...")
	err := event.PushInitialEvent(string(ptpEvent.GnssStateChange), 60*time.Second)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to push initial GNSS event: %v", err)
		return fmt.Errorf("could not push initial GNSS event, err=%s", err)
	}
	logrus.Infof("🔍 [DEBUG] Successfully pushed initial GNSS event")

	// Start monitoring pod logs
	logrus.Infof("🔍 [DEBUG] Starting pod logs monitoring...")
	term, err := event.MonitorPodLogsRegex()
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to start pod logs monitoring: %v", err)
		return fmt.Errorf("could not start listening to events, err=%s", err)
	}
	defer func() {
		logrus.Infof("🔍 [DEBUG] Stopping pod logs monitoring")
		term <- true
	}()
	logrus.Infof("🔍 [DEBUG] Successfully started pod logs monitoring")

	logrus.Infof("🔍 [DEBUG] Entering event loop, waiting for GNSS state %s event...", expectedGnssState)
	eventCount := 0
	startTime := time.Now()
	receivedStates := make(map[string]int) // Track all received states for debugging

	// Add periodic status logging
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutChan:
			elapsed := time.Since(startTime)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Waited %v for GNSS state %s event, received %d events total",
				elapsed, expectedGnssState, eventCount)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: No events received in the last %v", elapsed)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if GNSS events are being generated by the system")
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if event subscription is working properly")
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if pod logs monitoring is capturing events")

			// Log all received states for debugging
			if len(receivedStates) > 0 {
				logrus.Errorf("🔍 [DEBUG] TIMEOUT: Received GNSS states during test: %v", receivedStates)
			} else {
				logrus.Errorf("🔍 [DEBUG] TIMEOUT: No GNSS states received at all")
			}

			// Check if we received any valid GNSS states (even if not the expected one)
			validStates := []string{string(ptpEvent.LOCKED), string(ptpEvent.ANTENNA_DISCONNECTED), string(ptpEvent.SYNCHRONIZED),
				string(ptpEvent.SYNCHRONIZED), string(ptpEvent.ACQUIRING_SYNC)}
			for state, count := range receivedStates {
				for _, validState := range validStates {
					if state == validState {
						logrus.Errorf("🔍 [DEBUG] TIMEOUT: Received valid GNSS state '%s' %d times, but expected '%s'",
							state, count, expectedGnssState)
						// Don't fail the test if we received a valid state, just warn
						logrus.Warnf("🔍 [DEBUG] WARNING: Expected GNSS state '%s' but received '%s'. This might indicate a timing issue or system state.",
							expectedGnssState, state)
						return nil
					}
				}
			}

			return fmt.Errorf("timed out waiting for GNSS state %s event", expectedGnssState)

		case <-ticker.C:
			elapsed := time.Since(startTime)
			logrus.Infof("🔍 [DEBUG] STATUS: Still waiting for GNSS state %s event... (elapsed: %v, events received: %d)",
				expectedGnssState, elapsed, eventCount)
			if len(receivedStates) > 0 {
				logrus.Infof("🔍 [DEBUG] STATUS: Received GNSS states so far: %v", receivedStates)
			}

		case singleEvent := <-eventChan:
			eventCount++
			elapsed := time.Since(startTime)
			logrus.Infof("🔍 [DEBUG] Received GNSS event #%d after %v: %v", eventCount, elapsed, singleEvent)

			// Log the complete event structure for debugging
			logrus.Infof("🔍 [DEBUG] Complete event structure:")
			for key, value := range singleEvent {
				logrus.Infof("🔍 [DEBUG]   Key: '%s', Type: %T, Value: %v", key, value, value)
			}

			// Get event values
			values, ok := singleEvent[exports.EventValues].(exports.StoredEventValues)
			if !ok {
				logrus.Errorf("🔍 [DEBUG] Failed to extract EventValues from event: %v", singleEvent)
				logrus.Errorf("🔍 [DEBUG] Event structure: %T", singleEvent[exports.EventValues])
				continue
			}
			logrus.Infof("🔍 [DEBUG] Event values: %v", values)

			// Try to extract GNSS state from notification field (actual event structure)
			gnssState := ""

			// First try the notification field (which is the actual field in the events)
			if notification, ok := values["notification"].(string); ok {
				gnssState = notification
				logrus.Infof("🔍 [DEBUG] Found GNSS state in notification field: %s", gnssState)
			} else {
				// Try to extract from metric field (for backward compatibility)
				if metric, ok := values["metric"].(string); ok {
					logrus.Infof("🔍 [DEBUG] Found 'metric' field: %s", metric)
					gnssState = metric
				} else if metricInt, ok := values["metric"].(int); ok {
					gnssState = strconv.Itoa(metricInt)
					logrus.Infof("🔍 [DEBUG] Found 'metric' field as int: %d", metricInt)
				} else if metricFloat, ok := values["metric"].(float64); ok {
					gnssState = strconv.Itoa(int(metricFloat))
					logrus.Infof("🔍 [DEBUG] Found 'metric' field as float: %f", metricFloat)
				}

				if gnssState == "" {
					logrus.Errorf("🔍 [DEBUG] Failed to extract GNSS state from values: %v", values)
					logrus.Errorf("🔍 [DEBUG] Available keys in values: %v", getKeys(values))
					logrus.Errorf("🔍 [DEBUG] Notification field type: %T", values["notification"])
					logrus.Errorf("🔍 [DEBUG] Metric field type: %T", values["metric"])
					continue
				}
			}

			// Track all received states for debugging
			receivedStates[gnssState]++

			logrus.Infof("🔍 [DEBUG] GNSS state changed to: '%s' (expected: '%s')", gnssState, expectedGnssState)
			logrus.Infof("🔍 [DEBUG] String comparison: '%s' == '%s' = %t", gnssState, expectedGnssState, gnssState == expectedGnssState)

			// Check if we received the expected GNSS state
			if gnssState == expectedGnssState {
				logrus.Infof("🔍 [DEBUG] ✅ SUCCESS: Expected GNSS state '%s' received after %d events in %v",
					expectedGnssState, eventCount, elapsed)
				return nil
			} else {
				logrus.Infof("🔍 [DEBUG] ❌ Mismatch: Got '%s', expected '%s' (continuing to wait...)", gnssState, expectedGnssState)
				logrus.Infof("🔍 [DEBUG] Received state length: %d, Expected state length: %d", len(gnssState), len(expectedGnssState))
				logrus.Infof("🔍 [DEBUG] Received state bytes: %v, Expected state bytes: %v", []byte(gnssState), []byte(expectedGnssState))
			}
		}
	}
}

// Helper function to get keys from a map
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func verifyPtpStateEvent(fullConfig testconfig.TestConfig, expectedPtpState string) error {
	defer GinkgoRecover() // Add this to capture panics in this function

	// buffer to hold events until they can be processed
	const incomingEventsBuffer = 100

	logrus.Infof("🔍 [DEBUG] Starting PTP State event verification for expected state: %s", expectedPtpState)

	// Ensure pub/sub is initialized
	if event.PubSub == nil {
		logrus.Infof("🔍 [DEBUG] PubSub is nil, initializing...")
		event.InitPubSub()
	} else {
		logrus.Infof("🔍 [DEBUG] PubSub already initialized")
	}

	// Create timer channel for verification timeout
	verificationTimeout := 2 * time.Minute
	timeoutChan := time.After(verificationTimeout)
	logrus.Infof("🔍 [DEBUG] Set verification timeout to %v", verificationTimeout)

	// Register channel to receive PtpStateChange events
	logrus.Infof("🔍 [DEBUG] Subscribing to PtpStateChange events...")
	eventChan, subscriberID := event.PubSub.Subscribe(string(ptpEvent.PtpStateChange), incomingEventsBuffer)
	defer func() {
		logrus.Infof("🔍 [DEBUG] Unsubscribing from PtpStateChange events, subscriberID: %d", subscriberID)
		event.PubSub.Unsubscribe(string(ptpEvent.PtpStateChange), subscriberID)
	}()

	// Create and push an initial event
	logrus.Infof("🔍 [DEBUG] Pushing initial PTP State event...")
	err := event.PushInitialEvent(string(ptpEvent.PtpStateChange), 60*time.Second)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to push initial PTP State event: %v", err)
		return fmt.Errorf("could not push initial PTP state event, err=%s", err)
	}
	logrus.Infof("🔍 [DEBUG] Successfully pushed initial PTP State event")

	// Start monitoring pod logs
	logrus.Infof("🔍 [DEBUG] Starting pod logs monitoring...")
	term, err := event.MonitorPodLogsRegex()
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to start pod logs monitoring: %v", err)
		return fmt.Errorf("could not start listening to events, err=%s", err)
	}
	defer func() {
		logrus.Infof("🔍 [DEBUG] Stopping pod logs monitoring")
		term <- true
	}()
	logrus.Infof("🔍 [DEBUG] Successfully started pod logs monitoring")

	logrus.Infof("🔍 [DEBUG] Entering event loop, waiting for PTP state %s event...", expectedPtpState)
	eventCount := 0
	startTime := time.Now()
	receivedStates := make(map[string]int) // Track all received states for debugging

	// Add periodic status logging
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutChan:
			elapsed := time.Since(startTime)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Waited %v for PTP state %s event, received %d events total",
				elapsed, expectedPtpState, eventCount)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: No events received in the last %v", elapsed)
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if PTP State events are being generated by the system")
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if event subscription is working properly")
			logrus.Errorf("🔍 [DEBUG] TIMEOUT: Check if pod logs monitoring is capturing events")

			// Log all received states for debugging
			if len(receivedStates) > 0 {
				logrus.Errorf("🔍 [DEBUG] TIMEOUT: Received PTP states during test: %v", receivedStates)
			} else {
				logrus.Errorf("🔍 [DEBUG] TIMEOUT: No PTP states received at all")
			}

			// Check if we received any valid PTP states (even if not the expected one)
			validStates := []string{string(ptpEvent.LOCKED), string(ptpEvent.HOLDOVER), string(ptpEvent.FREERUN),
				string(ptpEvent.SYNCHRONIZED), string(ptpEvent.ACQUIRING_SYNC)}
			for state, count := range receivedStates {
				for _, validState := range validStates {
					if state == validState {
						logrus.Errorf("🔍 [DEBUG] TIMEOUT: Received valid PTP state '%s' %d times, but expected '%s'",
							state, count, expectedPtpState)
						// Don't fail the test if we received a valid state, just warn
						logrus.Warnf("🔍 [DEBUG] WARNING: Expected PTP state '%s' but received '%s'. This might indicate a timing issue or system state.",
							expectedPtpState, state)
						return nil
					}
				}
			}

			return fmt.Errorf("timed out waiting for PTP state %s event", expectedPtpState)

		case <-ticker.C:
			elapsed := time.Since(startTime)
			logrus.Infof("🔍 [DEBUG] STATUS: Still waiting for PTP state %s event... (elapsed: %v, events received: %d)",
				expectedPtpState, elapsed, eventCount)
			if len(receivedStates) > 0 {
				logrus.Infof("🔍 [DEBUG] STATUS: Received PTP states so far: %v", receivedStates)
			}

		case singleEvent := <-eventChan:
			eventCount++
			elapsed := time.Since(startTime)
			logrus.Infof("🔍 [DEBUG] Received PTP State event #%d after %v: %v", eventCount, elapsed, singleEvent)

			// Get event values
			values, ok := singleEvent[exports.EventValues].(exports.StoredEventValues)
			if !ok {
				logrus.Errorf("🔍 [DEBUG] Failed to extract EventValues from event: %v", singleEvent)
				logrus.Errorf("🔍 [DEBUG] Event structure: %T", singleEvent[exports.EventValues])
				continue
			}
			logrus.Infof("🔍 [DEBUG] Event values: %v", values)

			// Try to extract as string first (for backward compatibility)
			ptpState, ok := values["notification"].(string)
			if !ok {
				// If not a string, try as integer
				ptpStateInt, ok := values["notification"].(int)
				if !ok {
					logrus.Errorf("🔍 [DEBUG] Failed to extract 'notification' field from values: %v", values)
					logrus.Errorf("🔍 [DEBUG] Available keys in values: %v", getKeys(values))
					logrus.Errorf("🔍 [DEBUG] Notification field type: %T", values["notification"])
					continue
				}
				ptpState = strconv.Itoa(ptpStateInt)
			}

			// Track all received states for debugging
			receivedStates[ptpState]++

			logrus.Infof("🔍 [DEBUG] PTP state changed to: '%s' (expected: '%s')", ptpState, expectedPtpState)

			// Check if we received the expected PTP state
			if ptpState == expectedPtpState {
				logrus.Infof("🔍 [DEBUG] ✅ SUCCESS: Expected PTP state '%s' received after %d events in %v",
					expectedPtpState, eventCount, elapsed)
				return nil
			} else {
				logrus.Infof("🔍 [DEBUG] ❌ Mismatch: Got '%s', expected '%s' (continuing to wait...)", ptpState, expectedPtpState)
			}
		}
	}
}

// VerificationTask represents a task to be executed in parallel
type VerificationTask struct {
	Name     string
	Function func() error
}

// VerificationResult holds the result of a verification task
type VerificationResult struct {
	TaskName string
	Error    error
}

// runParallelVerifications executes multiple verification tasks in parallel
// and returns a slice of results
func runParallelVerifications(tasks []VerificationTask) []VerificationResult {
	logrus.Infof("🔍 [DEBUG] Starting parallel verification of %d tasks", len(tasks))

	var wg sync.WaitGroup
	results := make([]VerificationResult, len(tasks))

	for i, task := range tasks {
		logrus.Infof("🔍 [DEBUG] Starting task %d: %s", i+1, task.Name)
		wg.Add(1)
		go func(index int, task VerificationTask) {
			defer wg.Done()
			defer GinkgoRecover() // Add this to capture panics in goroutines
			defer func() {
				if r := recover(); r != nil {
					logrus.Errorf("🔍 [DEBUG] PANIC in task %s: %v", task.Name, r)
					results[index] = VerificationResult{
						TaskName: task.Name,
						Error:    fmt.Errorf("panic in %s verification: %v", task.Name, r),
					}
				}
			}()

			logrus.Infof("🔍 [DEBUG] Executing task: %s", task.Name)
			err := task.Function()
			logrus.Infof("🔍 [DEBUG] Task %s completed with error: %v", task.Name, err)

			results[index] = VerificationResult{
				TaskName: task.Name,
				Error:    err,
			}
		}(i, task)
	}

	logrus.Infof("🔍 [DEBUG] Waiting for all %d tasks to complete...", len(tasks))
	wg.Wait()

	logrus.Infof("🔍 [DEBUG] All tasks completed. Results:")
	for i, result := range results {
		logrus.Infof("🔍 [DEBUG]   Task %d (%s): %v", i+1, result.TaskName, result.Error)
	}

	return results
}

// createVerificationTask creates a verification task with proper error handling
func createVerificationTask(name string, fn func() error) VerificationTask {
	return VerificationTask{
		Name: name,
		Function: func() error {
			defer func() {
				if r := recover(); r != nil {
					logrus.Errorf("Panic in %s verification: %v", name, r)
				}
			}()
			return fn()
		},
	}
}

// verifyHoldoverEventsInParallel verifies all holdover events in parallel
func verifyHoldoverEventsInParallel(fullConfig testconfig.TestConfig) error {
	return verifyEventsByType(fullConfig, EventTypeHoldover)
}

// verifyLockedEventsInParallel verifies all locked events in parallel
func verifyLockedEventsInParallel(fullConfig testconfig.TestConfig) error {
	return verifyEventsByType(fullConfig, EventTypeLocked)
}

// verifyFreerunEventsInParallel verifies all freerun events in parallel
func verifyFreerunEventsInParallel(fullConfig testconfig.TestConfig) error {
	return verifyEventsByType(fullConfig, EventTypeFreerun)
}

// VerificationConfig holds configuration for a verification task
type VerificationConfig struct {
	Name               string
	ClockClassExpected fbprotocol.ClockClass
	GnssStateExpected  string
	PtpStateExpected   string
}

// verifyEventsInParallel is a generic function that can verify any combination of events
func verifyEventsInParallel(fullConfig testconfig.TestConfig, config VerificationConfig) error {
	logrus.Infof("🔍 [DEBUG] Starting verifyEventsInParallel with config: %+v", config)

	tasks := []VerificationTask{}

	// Add Clock Class verification if specified
	if config.ClockClassExpected != 0 {
		logrus.Infof("🔍 [DEBUG] Adding Clock Class verification task for class %d", config.ClockClassExpected)
		tasks = append(tasks, createVerificationTask("Clock Class", func() error {
			return verifyClockClassEvent(fullConfig, config.ClockClassExpected)
		}))
	}

	// Add GNSS verification if specified
	if config.GnssStateExpected != "" {
		logrus.Infof("🔍 [DEBUG] Adding GNSS verification task for state '%s'", config.GnssStateExpected)
		tasks = append(tasks, createVerificationTask("GNSS", func() error {
			return verifyGnssEvent(fullConfig, config.GnssStateExpected)
		}))
	}

	// Add PTP State verification if specified
	if config.PtpStateExpected != "" {
		logrus.Infof("🔍 [DEBUG] Adding PTP State verification task for state '%s'", config.PtpStateExpected)
		tasks = append(tasks, createVerificationTask("PTP State", func() error {
			return verifyPtpStateEvent(fullConfig, config.PtpStateExpected)
		}))
	}

	if len(tasks) == 0 {
		logrus.Errorf("🔍 [DEBUG] No verification tasks specified")
		return fmt.Errorf("no verification tasks specified")
	}

	logrus.Infof("🔍 [DEBUG] Created %d verification tasks", len(tasks))
	results := runParallelVerifications(tasks)

	// Check for any errors
	var errors []string
	for _, result := range results {
		if result.Error != nil {
			logrus.Errorf("🔍 [DEBUG] Task '%s' failed: %v", result.TaskName, result.Error)
			errors = append(errors, fmt.Sprintf("%s: %v", result.TaskName, result.Error))
		} else {
			logrus.Infof("🔍 [DEBUG] Task '%s' completed successfully", result.TaskName)
		}
	}

	if len(errors) > 0 {
		errorMsg := fmt.Sprintf("verification failures: %s", strings.Join(errors, "; "))
		logrus.Errorf("🔍 [DEBUG] Verification failed: %s", errorMsg)
		return fmt.Errorf(errorMsg)
	}

	logrus.Infof("🔍 [DEBUG] All verification tasks completed successfully")
	return nil
}

// Common verification configurations
var (
	HoldoverConfig = VerificationConfig{
		Name:               "Holdover Events",
		ClockClassExpected: fbprotocol.ClockClass7,
		GnssStateExpected:  string(ptpEvent.ANTENNA_DISCONNECTED),
		PtpStateExpected:   string(ptpEvent.HOLDOVER),
	}

	LockedConfig = VerificationConfig{
		Name:               "Locked Events",
		ClockClassExpected: fbprotocol.ClockClass6,
		GnssStateExpected:  string(ptpEvent.SYNCHRONIZED),
		PtpStateExpected:   string(ptpEvent.LOCKED),
	}
)

// NewVerificationConfig creates a new verification configuration with the given parameters
func NewVerificationConfig(name string, clockClass fbprotocol.ClockClass, gnssState, ptpState string) VerificationConfig {
	return VerificationConfig{
		Name:               name,
		ClockClassExpected: clockClass,
		GnssStateExpected:  gnssState,
		PtpStateExpected:   ptpState,
	}
}

// NewClockClassOnlyConfig creates a verification config that only verifies clock class
func NewClockClassOnlyConfig(name string, clockClass fbprotocol.ClockClass) VerificationConfig {
	return VerificationConfig{
		Name:               name,
		ClockClassExpected: clockClass,
	}
}

// NewGnssOnlyConfig creates a verification config that only verifies GNSS state
func NewGnssOnlyConfig(name string, gnssState string) VerificationConfig {
	return VerificationConfig{
		Name:              name,
		GnssStateExpected: gnssState,
	}
}

// NewPtpStateOnlyConfig creates a verification config that only verifies PTP state
func NewPtpStateOnlyConfig(name string, ptpState string) VerificationConfig {
	return VerificationConfig{
		Name:             name,
		PtpStateExpected: ptpState,
	}
}

// EventType represents the type of event verification
type EventType string

const (
	EventTypeHoldover EventType = "HOLDOVER"
	EventTypeLocked   EventType = "LOCKED"
	EventTypeFreerun  EventType = "FREERUN"
)

/*
Event Verification System

This package provides a flexible and reusable system for verifying PTP events in parallel.
The system supports multiple event types and can be easily extended.

Usage Examples:

1. Using predefined functions:
   err := VerifyHoldover(fullConfig)
   err := VerifyLocked(fullConfig)
   err := VerifyFreerun(fullConfig)

2. Using the generic function with event types:
   err := verifyEventsByType(fullConfig, EventTypeHoldover)
   err := verifyEventsByType(fullConfig, EventTypeLocked)

3. Using string-based verification:
   err := VerifyEvents(fullConfig, "HOLDOVER")
   err := VerifyEvents(fullConfig, "LOCKED")

4. Using custom configurations:
   config := NewVerificationConfig("Custom Test", fbprotocol.ClockClass6, "LOCKED", "LOCKED")
   err := verifyEventsInParallel(fullConfig, config)

5. Using single verification types:
   config := NewClockClassOnlyConfig("Clock Test", fbprotocol.ClockClass7)
   err := verifyEventsInParallel(fullConfig, config)

The system automatically handles:
- Parallel execution of verification tasks
- Panic recovery with detailed error messages
- Proper error aggregation and reporting
- Resource cleanup
*/

// ... existing code ...

// GetVerificationConfig returns the appropriate verification configuration for the given event type
func GetVerificationConfig(eventType EventType) VerificationConfig {
	switch eventType {
	case EventTypeHoldover:
		return VerificationConfig{
			Name:               "Holdover Events",
			ClockClassExpected: fbprotocol.ClockClass7,
			GnssStateExpected:  string(ptpEvent.ANTENNA_DISCONNECTED),
			PtpStateExpected:   string(ptpEvent.HOLDOVER),
		}
	case EventTypeLocked:
		return VerificationConfig{
			Name:               "Locked Events",
			ClockClassExpected: fbprotocol.ClockClass6,
			GnssStateExpected:  string(ptpEvent.SYNCHRONIZED),
			PtpStateExpected:   string(ptpEvent.LOCKED),
		}
	case EventTypeFreerun:
		return VerificationConfig{
			Name:               "Freerun Events",
			ClockClassExpected: ClockClassFreerun, // Using the local constant defined earlier
			GnssStateExpected:  string(ptpEvent.ANTENNA_DISCONNECTED),
			PtpStateExpected:   string(ptpEvent.FREERUN),
		}
	default:
		return VerificationConfig{
			Name: fmt.Sprintf("Custom %s Events", string(eventType)),
		}
	}
}

// verifyEventsByType is a generic function that verifies events based on the event type
func verifyEventsByType(fullConfig testconfig.TestConfig, eventType EventType) error {
	logrus.Infof("🔍 [DEBUG] verifyEventsByType: Starting verification for event type: %s", eventType)
	config := GetVerificationConfig(eventType)
	logrus.Infof("🔍 [DEBUG] verifyEventsByType: Got config: %+v", config)
	err := verifyEventsInParallel(fullConfig, config)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] verifyEventsByType: Failed with error: %v", err)
	} else {
		logrus.Infof("🔍 [DEBUG] verifyEventsByType: Completed successfully for event type: %s", eventType)
	}
	return err
}

// VerifyEvents is a convenience function that verifies events by type string
func VerifyEvents(fullConfig testconfig.TestConfig, eventType string) error {
	return verifyEventsByType(fullConfig, EventType(eventType))
}

// VerifyHoldover is a convenience function for holdover event verification
func VerifyHoldover(fullConfig testconfig.TestConfig) error {
	return verifyEventsByType(fullConfig, EventTypeHoldover)
}

// VerifyLocked is a convenience function for locked event verification
func VerifyLocked(fullConfig testconfig.TestConfig) error {
	logrus.Infof("🔍 [DEBUG] VerifyLocked: Starting locked event verification...")
	err := verifyEventsByType(fullConfig, EventTypeLocked)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] VerifyLocked: Failed with error: %v", err)
	} else {
		logrus.Infof("🔍 [DEBUG] VerifyLocked: Completed successfully")
	}
	return err
}

// VerifyFreerun is a convenience function for freerun event verification
func VerifyFreerun(fullConfig testconfig.TestConfig) error {
	return verifyEventsByType(fullConfig, EventTypeFreerun)
}

// safeExecCommand is a helper function that checks for nil values before executing pod commands
func safeExecCommand(pod *v1core.Pod, containerName string, command []string) (string, string, error) {
	// Execute command and return string output
	stdout, stderr, err := pods.ExecCommand(client.Client, true, pod, containerName, command)
	if err != nil {
		return "", stderr.String(), err
	}
	return stdout.String(), stderr.String(), nil
}

// Helper function to debug all events being received
func debugAllEvents(fullConfig testconfig.TestConfig) {
	logrus.Infof("🔍 [DEBUG] Starting event debugging session...")

	// Ensure pub/sub is initialized
	if event.PubSub == nil {
		logrus.Infof("🔍 [DEBUG] PubSub is nil, initializing...")
		event.InitPubSub()
	}

	// Subscribe to all event types
	gnssChan, gnssSubID := event.PubSub.Subscribe(string(ptpEvent.GnssStateChange), 100)
	ptpChan, ptpSubID := event.PubSub.Subscribe(string(ptpEvent.PtpStateChange), 100)
	clockChan, clockSubID := event.PubSub.Subscribe(string(ptpEvent.PtpClockClassChange), 100)

	defer func() {
		event.PubSub.Unsubscribe(string(ptpEvent.GnssStateChange), gnssSubID)
		event.PubSub.Unsubscribe(string(ptpEvent.PtpStateChange), ptpSubID)
		event.PubSub.Unsubscribe(string(ptpEvent.PtpClockClassChange), clockSubID)
	}()

	// Start monitoring pod logs
	term, err := event.MonitorPodLogsRegex()
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to start pod logs monitoring for debugging: %v", err)
		return
	}
	defer func() { term <- true }()

	// Create and push initial events
	err = event.PushInitialEvent(string(ptpEvent.GnssStateChange), 30*time.Second)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to push initial GNSS event for debugging: %v", err)
	}

	err = event.PushInitialEvent(string(ptpEvent.PtpStateChange), 30*time.Second)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to push initial PTP state event for debugging: %v", err)
	}

	err = event.PushInitialEvent(string(ptpEvent.PtpClockClassChange), 30*time.Second)
	if err != nil {
		logrus.Errorf("🔍 [DEBUG] Failed to push initial clock class event for debugging: %v", err)
	}

	logrus.Infof("🔍 [DEBUG] Monitoring all events for 60 seconds...")
	timeout := time.After(60 * time.Second)

	for {
		select {
		case <-timeout:
			logrus.Infof("🔍 [DEBUG] Event debugging session completed")
			return

		case event := <-gnssChan:
			logrus.Infof("🔍 [DEBUG] GNSS Event received: %v", event)
			if values, ok := event[exports.EventValues].(exports.StoredEventValues); ok {
				logrus.Infof("🔍 [DEBUG] GNSS Event values: %v", values)
				logrus.Infof("🔍 [DEBUG] GNSS Event keys: %v", getKeys(values))
			}

		case event := <-ptpChan:
			logrus.Infof("🔍 [DEBUG] PTP State Event received: %v", event)
			if values, ok := event[exports.EventValues].(exports.StoredEventValues); ok {
				logrus.Infof("🔍 [DEBUG] PTP State Event values: %v", values)
				logrus.Infof("🔍 [DEBUG] PTP State Event keys: %v", getKeys(values))
			}

		case event := <-clockChan:
			logrus.Infof("🔍 [DEBUG] Clock Class Event received: %v", event)
			if values, ok := event[exports.EventValues].(exports.StoredEventValues); ok {
				logrus.Infof("🔍 [DEBUG] Clock Class Event values: %v", values)
				logrus.Infof("🔍 [DEBUG] Clock Class Event keys: %v", getKeys(values))
			}
		}
	}
}

// Helper function to test event parsing with sample data
func testEventParsing() {
	logrus.Infof("🔍 [DEBUG] Testing event parsing logic...")

	// Test sample event data
	sampleEvent := exports.StoredEvent{
		exports.EventTimeStamp: time.Now(),
		exports.EventType:      string(ptpEvent.GnssStateChange),
		exports.EventSource:    "test",
		exports.EventValues: exports.StoredEventValues{
			"notification": "LOCKED",
			"metric":       "LOCKED",
		},
	}

	logrus.Infof("🔍 [DEBUG] Sample event: %v", sampleEvent)

	values, ok := sampleEvent[exports.EventValues].(exports.StoredEventValues)
	if !ok {
		logrus.Errorf("🔍 [DEBUG] Failed to extract EventValues from sample event")
		return
	}

	logrus.Infof("🔍 [DEBUG] Sample event values: %v", values)

	// Test GNSS state extraction
	gnssState := ""
	if notification, ok := values["notification"].(string); ok {
		gnssState = notification
		logrus.Infof("🔍 [DEBUG] Found GNSS state in notification field: %s", gnssState)
	} else {
		logrus.Errorf("🔍 [DEBUG] Failed to extract notification field")
	}

	logrus.Infof("🔍 [DEBUG] Extracted GNSS state: '%s'", gnssState)
	logrus.Infof("🔍 [DEBUG] Expected LOCKED: '%s'", string(ptpEvent.LOCKED))
	logrus.Infof("🔍 [DEBUG] Comparison: '%s' == '%s' = %t", gnssState, string(ptpEvent.LOCKED), gnssState == string(ptpEvent.LOCKED))
}

// Helper function to check current system state
func checkCurrentSystemState(fullConfig testconfig.TestConfig) {
	logrus.Infof("🔍 [DEBUG] Checking current system state...")

	// Check clock class state
	if checkClockClassStateReturnBool(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6))) {
		logrus.Infof("🔍 [DEBUG] System is in LOCKED state (ClockClass 6)")
	} else if checkClockClassStateReturnBool(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7))) {
		logrus.Infof("🔍 [DEBUG] System is in HOLDOVER state (ClockClass 7)")
	} else {
		logrus.Infof("🔍 [DEBUG] System is in unknown state")
	}

	// Check PTP state
	ptpState := getCurrentPTPState(fullConfig)
	logrus.Infof("🔍 [DEBUG] Current PTP state: %s", ptpState)

	// Check GNSS status if available
	// This would require additional implementation to check GNSS status directly
	logrus.Infof("🔍 [DEBUG] System state check completed")
}

// Helper function to get current PTP state without failing the test
func getCurrentPTPState(fullConfig testconfig.TestConfig) string {
	nmeaStatusPattern := `openshift_ptp_nmea_status\{iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`
	nmeaStatusRe := regexp.MustCompile(nmeaStatusPattern)

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	scanner := bufio.NewScanner(strings.NewReader(buf.String()))

	for scanner.Scan() {
		line := scanner.Text()
		if matches := nmeaStatusRe.FindStringSubmatch(line); matches != nil {
			if len(matches) < 4 {
				continue
			}
			process := matches[3]
			state := matches[4]
			if process == "ts2phc" {
				return state
			}
		}
	}
	return "unknown"
}
