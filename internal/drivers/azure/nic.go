package azure

import (
	"context"

	"github.com/drone/runner-go/logger"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

const (
	defaultResourceGroup = "harness-runner-resource"
)

func (c *config) createResourceGroup(ctx context.Context, cred azcore.TokenCredential) (*armresources.ResourceGroup, error) {
	logr := logger.FromContext(ctx)
	if c.resourceGroupName == "" {
		logr.Debugln("Using default resource group name")
		c.resourceGroupName = defaultResourceGroup
	}
	resourceGroupClient, err := armresources.NewResourceGroupsClient(c.subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}

	parameters := armresources.ResourceGroup{
		Location: to.Ptr(c.location),
	}
	rg, err := resourceGroupClient.Get(ctx, c.resourceGroupName, nil)
	if err != nil {
		resp, createErr := resourceGroupClient.CreateOrUpdate(ctx, c.resourceGroupName, parameters, nil)
		if createErr != nil {
			return nil, createErr
		}
		logr.Debugf("Created resource group: %s", c.resourceGroupName)
		return &resp.ResourceGroup, nil
	}
	logr.Debugf("Resource group %s already exists", c.resourceGroupName)
	return &rg.ResourceGroup, nil
}

func (c *config) createVirtualNetwork(ctx context.Context, cred azcore.TokenCredential, vnetName string) (*armnetwork.VirtualNetwork, error) {
	logr := logger.FromContext(ctx)
	vnetClient, err := armnetwork.NewVirtualNetworksClient(c.subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	parameters := armnetwork.VirtualNetwork{
		Location: to.Ptr(c.location),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{
				AddressPrefixes: []*string{
					to.Ptr("10.1.0.0/16"), // example 10.1.0.0/16
				},
			},
		},
	}

	pollerResponse, createErr := vnetClient.BeginCreateOrUpdate(ctx, c.resourceGroupName, vnetName, parameters, nil)
	if createErr != nil {
		return nil, createErr
	}

	resp, poolErr := pollerResponse.PollUntilDone(ctx, nil)
	if poolErr != nil {
		return nil, poolErr
	}
	logr.Debugf("Created vnet: %s", vnetName)
	return &resp.VirtualNetwork, nil
}

func (c *config) deleteVirtualNetWork(ctx context.Context, cred azcore.TokenCredential, vnetName string) error {
	vnetClient, err := armnetwork.NewVirtualNetworksClient(c.subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	pollerResponse, err := vnetClient.BeginDelete(ctx, c.resourceGroupName, vnetName, nil)
	if err != nil {
		return err
	}

	_, err = pollerResponse.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *config) createSubnets(ctx context.Context, cred azcore.TokenCredential, subnetName, vnetName string) (*armnetwork.Subnet, error) {
	logr := logger.FromContext(ctx)

	subnetClient, err := armnetwork.NewSubnetsClient(c.subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	parameters := armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr("10.1.10.0/24"),
		},
	}
	pollerResponse, createErr := subnetClient.BeginCreateOrUpdate(ctx, c.resourceGroupName, vnetName, subnetName, parameters, nil)
	if createErr != nil {
		return nil, createErr
	}

	resp, pollErr := pollerResponse.PollUntilDone(ctx, nil)
	if pollErr != nil {
		return nil, pollErr
	}
	logr.Debugf("Created subnet: %s", subnetName)
	return &resp.Subnet, nil
}

func (c *config) createPublicIP(ctx context.Context, cred azcore.TokenCredential, publicIPName string) (*armnetwork.PublicIPAddress, error) {
	logr := logger.FromContext(ctx)
	publicIPAddressClient, err := armnetwork.NewPublicIPAddressesClient(c.subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}

	parameters := armnetwork.PublicIPAddress{
		Location: to.Ptr(c.location),
		Zones:    c.zones,
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic), // Static or Dynamic
		},
	}

	pollerResponse, createErr := publicIPAddressClient.BeginCreateOrUpdate(ctx, c.resourceGroupName, publicIPName, parameters, nil)
	if createErr != nil {
		return nil, createErr
	}

	resp, poolErr := pollerResponse.PollUntilDone(ctx, nil)
	if poolErr != nil {
		return nil, poolErr
	}
	logr.Debugf("Created IP: %s", publicIPName)
	return &resp.PublicIPAddress, nil
}

func (c *config) deletePublicIP(ctx context.Context, cred azcore.TokenCredential, publicIPName string) error {
	publicIPAddressClient, err := armnetwork.NewPublicIPAddressesClient(c.subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	pollerResponse, err := publicIPAddressClient.BeginDelete(ctx, c.resourceGroupName, publicIPName, nil)
	if err != nil {
		return err
	}

	_, err = pollerResponse.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}
	return nil
}

func (c *config) createNetworkInterface(ctx context.Context, cred azcore.TokenCredential, networkInterfaceName, subnetID, publicIPID string) (*armnetwork.Interface, error) {
	logr := logger.FromContext(ctx)
	nicClient, err := armnetwork.NewInterfacesClient(c.subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}

	parameters := armnetwork.Interface{
		Location: to.Ptr(c.location),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
				{
					Name: to.Ptr("ipConfig"),
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
						Subnet: &armnetwork.Subnet{
							ID: to.Ptr(subnetID),
						},
						PublicIPAddress: &armnetwork.PublicIPAddress{
							ID: to.Ptr(publicIPID),
						},
					},
				},
			},
		},
	}

	pollerResponse, createErr := nicClient.BeginCreateOrUpdate(ctx, c.resourceGroupName, networkInterfaceName, parameters, nil)
	if createErr != nil {
		return nil, createErr
	}

	resp, pollErr := pollerResponse.PollUntilDone(ctx, nil)
	if pollErr != nil {
		return nil, pollErr
	}
	logr.Debugf("Created network interface: %s", networkInterfaceName)
	return &resp.Interface, nil
}

func (c *config) deleteNetworkInterface(ctx context.Context, cred azcore.TokenCredential, networkInterfaceName string) error {
	nicClient, err := armnetwork.NewInterfacesClient(c.subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	pollerResponse, err := nicClient.BeginDelete(ctx, c.resourceGroupName, networkInterfaceName, nil)
	if err != nil {
		return err
	}

	_, err = pollerResponse.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *config) deleteDisk(ctx context.Context, cred azcore.TokenCredential, diskName string) error {
	diskClient, err := armcompute.NewDisksClient(c.subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	pollerResponse, err := diskClient.BeginDelete(ctx, c.resourceGroupName, diskName, nil)
	if err != nil {
		return err
	}

	_, err = pollerResponse.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}
	return nil
}
