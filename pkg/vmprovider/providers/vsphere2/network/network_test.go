// Copyright (c) 2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	goctx "context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/vim25/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"
	netopv1alpha1 "github.com/vmware-tanzu/vm-operator/external/net-operator/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/api/v1alpha2/common"
	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/network"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var _ = Describe("CreateAndWaitForNetworkInterfaces", func() {

	var (
		testConfig builder.VCSimTestConfig
		ctx        *builder.TestContextForVCSim

		vmCtx          context.VirtualMachineContextA2
		vm             *vmopv1.VirtualMachine
		interfaceSpecs []vmopv1.VirtualMachineNetworkInterfaceSpec

		results     network.NetworkInterfaceResults
		err         error
		initObjects []client.Object
	)

	BeforeEach(func() {
		testConfig = builder.VCSimTestConfig{WithV1A2: true}

		vm = &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "network-test-vm",
				Namespace: "network-test-ns",
			},
		}

		vmCtx = context.VirtualMachineContextA2{
			Context: goctx.Background(),
			Logger:  suite.GetLogger().WithName("network_test"),
			VM:      vm,
		}

		interfaceSpecs = nil
	})

	JustBeforeEach(func() {
		ctx = suite.NewTestContextForVCSim(testConfig, initObjects...)

		results, err = network.CreateAndWaitForNetworkInterfaces(
			vmCtx,
			ctx.Client,
			ctx.VCClient.Client,
			ctx.Finder,
			nil,
			interfaceSpecs)
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
		initObjects = nil
	})

	Context("Named Network", func() {
		// Use network vcsim automatically creates.
		const networkName = "DC0_DVPG0"

		BeforeEach(func() {
			testConfig.WithNetworkEnv = builder.NetworkEnvNamed
		})

		Context("network exists", func() {
			BeforeEach(func() {
				interfaceSpecs = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    "eth0",
						Network: common.PartialObjectRef{Name: networkName},
						DHCP6:   true,
					},
				}
			})

			It("returns success", func() {
				Expect(err).ToNot(HaveOccurred())
				Expect(results.Results).To(HaveLen(1))

				result := results.Results[0]
				By("has expected backing", func() {
					Expect(result.Backing).ToNot(BeNil())
					backing, err := result.Backing.EthernetCardBackingInfo(ctx)
					Expect(err).ToNot(HaveOccurred())
					backingInfo, ok := backing.(*types.VirtualEthernetCardDistributedVirtualPortBackingInfo)
					Expect(ok).To(BeTrue())
					Expect(backingInfo.Port.PortgroupKey).To(Equal(ctx.NetworkRef.Reference().Value))
				})

				Expect(result.DHCP4).To(BeTrue())
				Expect(result.DHCP6).To(BeTrue()) // Only enabled if explicitly requested (which it is above).
			})
		})

		Context("network does not exist", func() {
			BeforeEach(func() {
				interfaceSpecs = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    "eth0",
						Network: common.PartialObjectRef{Name: "bogus"},
					},
				}
			})

			It("returns error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unable to find named network"))
				Expect(results.Results).To(BeEmpty())
			})
		})
	})

	Context("VDS", func() {
		const (
			interfaceName = "eth0"
			networkName   = "my-vds-network"
		)

		BeforeEach(func() {
			network.RetryTimeout = 1 * time.Second
			testConfig.WithNetworkEnv = builder.NetworkEnvVDS
		})

		Context("Simulate workflow", func() {
			BeforeEach(func() {
				interfaceSpecs = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name: interfaceName,
						Network: common.PartialObjectRef{
							Name: networkName,
						},
					},
				}
			})

			It("returns success", func() {
				// Assert test env is what we expect.
				Expect(ctx.NetworkRef.Reference().Type).To(Equal("DistributedVirtualPortgroup"))

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("network interface is not ready yet"))
				Expect(results.Results).To(BeEmpty())

				By("simulate successful NetOP reconcile", func() {
					netInterface := &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NetOPCRName(vm.Name, networkName, interfaceName, false),
							Namespace: vm.Namespace,
						},
					}
					Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)).To(Succeed())
					Expect(netInterface.Spec.NetworkName).To(Equal(networkName))

					netInterface.Status.NetworkID = ctx.NetworkRef.Reference().Value
					netInterface.Status.MacAddress = "" // NetOP doesn't set this.
					netInterface.Status.IPConfigs = []netopv1alpha1.IPConfig{
						{
							IP:         "192.168.1.110",
							IPFamily:   netopv1alpha1.IPv4Protocol,
							Gateway:    "192.168.1.1",
							SubnetMask: "255.255.255.0",
						},
						{
							IP:         "fd1a:6c85:79fe:7c98:0000:0000:0000:000f",
							IPFamily:   netopv1alpha1.IPv6Protocol,
							Gateway:    "fd1a:6c85:79fe:7c98:0000:0000:0000:0001",
							SubnetMask: "ffff:ffff:ffff:ff00:0000:0000:0000:0000",
						},
					}
					netInterface.Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
						{
							Type:   netopv1alpha1.NetworkInterfaceReady,
							Status: corev1.ConditionTrue,
						},
					}
					Expect(ctx.Client.Status().Update(ctx, netInterface)).To(Succeed())
				})

				results, err = network.CreateAndWaitForNetworkInterfaces(
					vmCtx,
					ctx.Client,
					ctx.VCClient.Client,
					ctx.Finder,
					nil,
					interfaceSpecs)
				Expect(err).ToNot(HaveOccurred())

				Expect(results.Results).To(HaveLen(1))
				result := results.Results[0]
				Expect(result.MacAddress).To(BeEmpty())
				Expect(result.ExternalID).To(BeEmpty())
				Expect(result.NetworkID).To(Equal(ctx.NetworkRef.Reference().Value))
				Expect(result.Backing).ToNot(BeNil())
				Expect(result.Backing.Reference()).To(Equal(ctx.NetworkRef.Reference()))
				Expect(result.Name).To(Equal(interfaceName))

				Expect(result.IPConfigs).To(HaveLen(2))
				ipConfig := result.IPConfigs[0]
				Expect(ipConfig.IPCIDR).To(Equal("192.168.1.110/24"))
				Expect(ipConfig.IsIPv4).To(BeTrue())
				Expect(ipConfig.Gateway).To(Equal("192.168.1.1"))
				ipConfig = result.IPConfigs[1]
				Expect(ipConfig.IPCIDR).To(Equal("fd1a:6c85:79fe:7c98::f/56"))
				Expect(ipConfig.IsIPv4).To(BeFalse())
				Expect(ipConfig.Gateway).To(Equal("fd1a:6c85:79fe:7c98:0000:0000:0000:0001"))
			})

			When("v1a1 network interface exists", func() {
				BeforeEach(func() {
					netIf := &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NetOPCRName(vm.Name, networkName, interfaceName, true),
							Namespace: vm.Namespace,
						},
						Spec: netopv1alpha1.NetworkInterfaceSpec{
							NetworkName: networkName,
							Type:        netopv1alpha1.NetworkInterfaceTypeVMXNet3,
						},
					}

					initObjects = append(initObjects, netIf)
				})

				It("returns success", func() {
					// Assert test env is what we expect.
					Expect(ctx.NetworkRef.Reference().Type).To(Equal("DistributedVirtualPortgroup"))

					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("network interface is not ready yet"))
					Expect(results.Results).To(BeEmpty())

					By("simulate successful NetOP reconcile", func() {
						netInterface := &netopv1alpha1.NetworkInterface{
							ObjectMeta: metav1.ObjectMeta{
								Name:      network.NetOPCRName(vm.Name, networkName, interfaceName, true),
								Namespace: vm.Namespace,
							},
						}
						Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)).To(Succeed())
						Expect(netInterface.Spec.NetworkName).To(Equal(networkName))

						netInterface.Status.NetworkID = ctx.NetworkRef.Reference().Value
						netInterface.Status.MacAddress = "" // NetOP doesn't set this.
						netInterface.Status.IPConfigs = []netopv1alpha1.IPConfig{
							{
								IP:         "192.168.1.110",
								IPFamily:   netopv1alpha1.IPv4Protocol,
								Gateway:    "192.168.1.1",
								SubnetMask: "255.255.255.0",
							},
							{
								IP:         "fd1a:6c85:79fe:7c98:0000:0000:0000:000f",
								IPFamily:   netopv1alpha1.IPv6Protocol,
								Gateway:    "fd1a:6c85:79fe:7c98:0000:0000:0000:0001",
								SubnetMask: "ffff:ffff:ffff:ff00:0000:0000:0000:0000",
							},
						}
						netInterface.Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
							{
								Type:   netopv1alpha1.NetworkInterfaceReady,
								Status: corev1.ConditionTrue,
							},
						}
						Expect(ctx.Client.Status().Update(ctx, netInterface)).To(Succeed())
					})

					results, err = network.CreateAndWaitForNetworkInterfaces(
						vmCtx,
						ctx.Client,
						ctx.VCClient.Client,
						ctx.Finder,
						nil,
						interfaceSpecs)
					Expect(err).ToNot(HaveOccurred())

					Expect(results.Results).To(HaveLen(1))
					result := results.Results[0]
					Expect(result.MacAddress).To(BeEmpty())
					Expect(result.ExternalID).To(BeEmpty())
					Expect(result.NetworkID).To(Equal(ctx.NetworkRef.Reference().Value))
					Expect(result.Backing).ToNot(BeNil())
					Expect(result.Backing.Reference()).To(Equal(ctx.NetworkRef.Reference()))
					Expect(result.Name).To(Equal(interfaceName))

					Expect(result.IPConfigs).To(HaveLen(2))
					ipConfig := result.IPConfigs[0]
					Expect(ipConfig.IPCIDR).To(Equal("192.168.1.110/24"))
					Expect(ipConfig.IsIPv4).To(BeTrue())
					Expect(ipConfig.Gateway).To(Equal("192.168.1.1"))
					ipConfig = result.IPConfigs[1]
					Expect(ipConfig.IPCIDR).To(Equal("fd1a:6c85:79fe:7c98::f/56"))
					Expect(ipConfig.IsIPv4).To(BeFalse())
					Expect(ipConfig.Gateway).To(Equal("fd1a:6c85:79fe:7c98:0000:0000:0000:0001"))
				})
			})
		})
	})

	Context("NCP", func() {
		const (
			interfaceName = "eth0"
			interfaceID   = "my-interface-id"
			networkName   = "my-ncp-network"
			macAddress    = "01-23-45-67-89-AB-CD-EF"
		)

		BeforeEach(func() {
			network.RetryTimeout = 1 * time.Second
			testConfig.WithNetworkEnv = builder.NetworkEnvNSXT
		})

		Context("Simulate workflow", func() {
			BeforeEach(func() {
				interfaceSpecs = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name: interfaceName,
						Network: common.PartialObjectRef{
							Name: networkName,
						},
					},
				}
			})

			It("returns success", func() {
				// Assert test env is what we expect.
				Expect(ctx.NetworkRef.Reference().Type).To(Equal("DistributedVirtualPortgroup"))

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("network interface is not ready yet"))
				Expect(results.Results).To(BeEmpty())

				By("simulate successful NCP reconcile", func() {
					netInterface := &ncpv1alpha1.VirtualNetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NCPCRName(vm.Name, networkName, interfaceName, false),
							Namespace: vm.Namespace,
						},
					}
					Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)).To(Succeed())
					Expect(netInterface.Spec.VirtualNetwork).To(Equal(networkName))

					netInterface.Status.InterfaceID = interfaceID
					netInterface.Status.MacAddress = macAddress
					netInterface.Status.ProviderStatus = &ncpv1alpha1.VirtualNetworkInterfaceProviderStatus{
						NsxLogicalSwitchID: builder.NsxTLogicalSwitchUUID,
					}
					netInterface.Status.IPAddresses = []ncpv1alpha1.VirtualNetworkInterfaceIP{
						{
							IP:         "192.168.1.110",
							Gateway:    "192.168.1.1",
							SubnetMask: "255.255.255.0",
						},
						{
							IP:         "fd1a:6c85:79fe:7c98:0000:0000:0000:000f",
							Gateway:    "fd1a:6c85:79fe:7c98:0000:0000:0000:0001",
							SubnetMask: "ffff:ffff:ffff:ff00:0000:0000:0000:0000",
						},
					}
					netInterface.Status.Conditions = []ncpv1alpha1.VirtualNetworkCondition{
						{
							Type:   "Ready",
							Status: "True",
						},
					}
					Expect(ctx.Client.Status().Update(ctx, netInterface)).To(Succeed())
				})

				results, err = network.CreateAndWaitForNetworkInterfaces(
					vmCtx,
					ctx.Client,
					ctx.VCClient.Client,
					ctx.Finder,
					nil,
					interfaceSpecs)
				Expect(err).ToNot(HaveOccurred())

				Expect(results.Results).To(HaveLen(1))
				result := results.Results[0]
				Expect(result.MacAddress).To(Equal(macAddress))
				Expect(result.ExternalID).To(Equal(interfaceID))
				Expect(result.NetworkID).To(Equal(builder.NsxTLogicalSwitchUUID))
				Expect(result.Name).To(Equal(interfaceName))

				Expect(result.IPConfigs).To(HaveLen(2))
				ipConfig := result.IPConfigs[0]
				Expect(ipConfig.IPCIDR).To(Equal("192.168.1.110/24"))
				Expect(ipConfig.IsIPv4).To(BeTrue())
				Expect(ipConfig.Gateway).To(Equal("192.168.1.1"))
				ipConfig = result.IPConfigs[1]
				Expect(ipConfig.IPCIDR).To(Equal("fd1a:6c85:79fe:7c98::f/56"))
				Expect(ipConfig.IsIPv4).To(BeFalse())
				Expect(ipConfig.Gateway).To(Equal("fd1a:6c85:79fe:7c98:0000:0000:0000:0001"))

				// Without the ClusterMoRef on the first call this will be nil for NSXT.
				Expect(result.Backing).To(BeNil())

				clusterMoRef := ctx.GetSingleClusterCompute().Reference()
				results, err = network.CreateAndWaitForNetworkInterfaces(
					vmCtx,
					ctx.Client,
					ctx.VCClient.Client,
					ctx.Finder,
					&clusterMoRef,
					interfaceSpecs)
				Expect(err).ToNot(HaveOccurred())
				Expect(results.Results).To(HaveLen(1))
				Expect(results.Results[0].Backing).ToNot(BeNil())
				Expect(results.Results[0].Backing.Reference()).To(Equal(ctx.NetworkRef.Reference()))
			})

			When("v1a1 NCP network interface exists", func() {
				BeforeEach(func() {
					vnetIf := &ncpv1alpha1.VirtualNetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NCPCRName(vm.Name, networkName, interfaceName, true),
							Namespace: vm.Namespace,
						},
						Spec: ncpv1alpha1.VirtualNetworkInterfaceSpec{
							VirtualNetwork: networkName,
						},
					}

					initObjects = append(initObjects, vnetIf)
				})

				It("returns success", func() {
					// Assert test env is what we expect.
					Expect(ctx.NetworkRef.Reference().Type).To(Equal("DistributedVirtualPortgroup"))

					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("network interface is not ready yet"))
					Expect(results.Results).To(BeEmpty())

					By("simulate successful NCP reconcile", func() {
						netInterface := &ncpv1alpha1.VirtualNetworkInterface{
							ObjectMeta: metav1.ObjectMeta{
								Name:      network.NCPCRName(vm.Name, networkName, interfaceName, true),
								Namespace: vm.Namespace,
							},
						}
						Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)).To(Succeed())
						Expect(netInterface.Spec.VirtualNetwork).To(Equal(networkName))

						netInterface.Status.InterfaceID = interfaceID
						netInterface.Status.MacAddress = macAddress
						netInterface.Status.ProviderStatus = &ncpv1alpha1.VirtualNetworkInterfaceProviderStatus{
							NsxLogicalSwitchID: builder.NsxTLogicalSwitchUUID,
						}
						netInterface.Status.IPAddresses = []ncpv1alpha1.VirtualNetworkInterfaceIP{
							{
								IP:         "192.168.1.110",
								Gateway:    "192.168.1.1",
								SubnetMask: "255.255.255.0",
							},
							{
								IP:         "fd1a:6c85:79fe:7c98:0000:0000:0000:000f",
								Gateway:    "fd1a:6c85:79fe:7c98:0000:0000:0000:0001",
								SubnetMask: "ffff:ffff:ffff:ff00:0000:0000:0000:0000",
							},
						}
						netInterface.Status.Conditions = []ncpv1alpha1.VirtualNetworkCondition{
							{
								Type:   "Ready",
								Status: "True",
							},
						}
						Expect(ctx.Client.Status().Update(ctx, netInterface)).To(Succeed())
					})

					results, err = network.CreateAndWaitForNetworkInterfaces(
						vmCtx,
						ctx.Client,
						ctx.VCClient.Client,
						ctx.Finder,
						nil,
						interfaceSpecs)
					Expect(err).ToNot(HaveOccurred())

					Expect(results.Results).To(HaveLen(1))
					result := results.Results[0]
					Expect(result.MacAddress).To(Equal(macAddress))
					Expect(result.ExternalID).To(Equal(interfaceID))
					Expect(result.NetworkID).To(Equal(builder.NsxTLogicalSwitchUUID))
					Expect(result.Name).To(Equal(interfaceName))

					Expect(result.IPConfigs).To(HaveLen(2))
					ipConfig := result.IPConfigs[0]
					Expect(ipConfig.IPCIDR).To(Equal("192.168.1.110/24"))
					Expect(ipConfig.IsIPv4).To(BeTrue())
					Expect(ipConfig.Gateway).To(Equal("192.168.1.1"))
					ipConfig = result.IPConfigs[1]
					Expect(ipConfig.IPCIDR).To(Equal("fd1a:6c85:79fe:7c98::f/56"))
					Expect(ipConfig.IsIPv4).To(BeFalse())
					Expect(ipConfig.Gateway).To(Equal("fd1a:6c85:79fe:7c98:0000:0000:0000:0001"))

					// Without the ClusterMoRef on the first call this will be nil for NSXT.
					Expect(result.Backing).To(BeNil())

					clusterMoRef := ctx.GetSingleClusterCompute().Reference()
					results, err = network.CreateAndWaitForNetworkInterfaces(
						vmCtx,
						ctx.Client,
						ctx.VCClient.Client,
						ctx.Finder,
						&clusterMoRef,
						interfaceSpecs)
					Expect(err).ToNot(HaveOccurred())
					Expect(results.Results).To(HaveLen(1))
					Expect(results.Results[0].Backing).ToNot(BeNil())
					Expect(results.Results[0].Backing.Reference()).To(Equal(ctx.NetworkRef.Reference()))
				})
			})
		})
	})
})
