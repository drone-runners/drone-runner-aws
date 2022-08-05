package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
)

type config struct {
	tenantID          string
	clientID          string
	clientSecret      string
	subscriptionID    string
	resourceGroupName string

	securityGroupName string

	rootDir string

	location string // region, example: East US

	// image data
	publisher string
	offer     string
	sku       string
	version   string

	IPAddress string

	size        string
	tags        map[string]string
	zones       []*string
	userData    string
	userDataKey string

	username string
	password string

	service *armcompute.VirtualMachinesClient
	cred    azcore.TokenCredential
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}

	if p.service == nil {
		cred, err := azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, nil)
		p.cred = cred
		if err != nil {
			return nil, err
		}

		p.service, err = armcompute.NewVirtualMachinesClient(p.subscriptionID, cred, nil)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (c *config) RootDir() string {
	return c.rootDir
}

func (c *config) DriverName() string {
	return string(types.Azure)
}

func (c *config) InstanceType() string {
	return c.offer
}

func (c *config) CanHibernate() bool {
	return false
}

func (c *config) Zones() string {
	var z string
	if len(z) == 1 {
		return *c.zones[0]
	}
	for _, zone := range c.zones {
		z = fmt.Sprintf("%s%s,", z, *zone)
	}

	return z
}

func (c *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	sanitizedRunnerName := strings.ReplaceAll(opts.RunnerName, " ", "-")
	sanitizedPoolName := strings.ReplaceAll(opts.PoolName, " ", "-")
	var name = fmt.Sprintf("%s-%s-%d", sanitizedRunnerName, sanitizedPoolName, time.Now().Unix())

	vnetName := fmt.Sprintf("%s-vnet", name)
	subnetName := fmt.Sprintf("%s-subnet", name)
	publicIPName := fmt.Sprintf("%s-publicip", name)
	networkInterfaceName := fmt.Sprintf("%s-networkinterface", name)
	diskName := fmt.Sprintf("%s-disk", name)

	var tags = map[string]*string{}
	// add user defined tags
	for k, v := range c.tags {
		tagValue := v
		tags[k] = &tagValue
	}

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Azure).
		WithField("name", name).
		WithField("image", c.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("zone", c.zones).
		WithField("image", c.offer).
		WithField("size", c.size)

	logr.Info("starting Azure Setup")

	_, err = c.createResourceGroup(ctx)
	if err != nil {
		logr.WithError(err).Errorln("failed to get/create resource group")
		return
	}

	_, err = c.createVirtualNetwork(ctx, vnetName)
	if err != nil {
		logr.WithError(err).Error("could not create virtual network")
		return nil, err
	}
	subnet, err := c.createSubnets(ctx, subnetName, vnetName)
	if err != nil {
		logr.WithError(err).Error("could not create subnet")
		return nil, err
	}
	publicIP, err := c.createPublicIP(ctx, publicIPName)
	if err != nil {
		logr.WithError(err).Error("could not create public IP")
		return nil, err
	}
	c.IPAddress = *publicIP.Properties.IPAddress
	networkInterface, err := c.createNetworkInterface(ctx, networkInterfaceName, *subnet.ID, *publicIP.ID)
	if err != nil {
		logr.WithError(err).Error("could not create network interface")
		return nil, err
	}

	// create the instance
	startTime := time.Now()

	uData := base64.StdEncoding.EncodeToString([]byte(lehelper.GenerateUserdata(c.userData, opts)))

	logr.Traceln("azure: creating VM")

	in := armcompute.VirtualMachine{
		Location: to.Ptr(c.location),
		Zones:    c.zones,
		Tags:     tags,
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(c.size)),
			},
			StorageProfile: &armcompute.StorageProfile{
				ImageReference: &armcompute.ImageReference{
					Publisher: to.Ptr(c.publisher),
					Offer:     to.Ptr(c.offer),
					SKU:       to.Ptr(c.sku),
					Version:   to.Ptr(c.version),
				},
				OSDisk: &armcompute.OSDisk{
					Name:         to.Ptr(diskName),
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					Caching:      to.Ptr(armcompute.CachingTypesReadWrite),
					ManagedDisk: &armcompute.ManagedDiskParameters{
						StorageAccountType: to.Ptr(armcompute.StorageAccountTypesStandardLRS), // OSDisk type Standard/Premium HDD/SSD
					},
				},
			},
			OSProfile: &armcompute.OSProfile{ //
				ComputerName:             to.Ptr("vm-runner"),
				AdminUsername:            to.Ptr(c.username),
				AdminPassword:            to.Ptr(c.password),
				CustomData:               to.Ptr(uData),
				AllowExtensionOperations: to.Ptr(true),
			},
			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
					{
						ID: to.Ptr(*networkInterface.ID),
					},
				},
			},
		},
	}

	poller, err := c.service.BeginCreateOrUpdate(ctx, c.resourceGroupName, name, in, nil)
	if err != nil {
		return nil, err
	}
	vm, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}
	// if windows add extension to vm
	if opts.OS == oshelp.OSWindows {
		_, extensionErr := c.addExtension(ctx, name)
		if extensionErr != nil {
			return nil, extensionErr
		}
	}

	logr.Debugln("instance insert operation completed")

	logr.
		WithField("ip", vm.ID).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("azure: [provision] VM provisioned")

	instanceMap := c.mapToInstance(&vm, opts)
	logr.
		WithField("ip", instanceMap.Address).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("azure: [provision] complete")

	return &instanceMap, nil
}

func (c *config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	logr := logger.FromContext(ctx)
	if c.resourceGroupName == "" {
		c.resourceGroupName = defaultResourceGroup
	}
	logr.Debugln("azure destroy operation started")
	if len(instanceIDs) == 0 {
		return nil
	}
	for _, instanceID := range instanceIDs {
		vnetName := fmt.Sprintf("%s-vnet", instanceID)
		publicIPName := fmt.Sprintf("%s-publicip", instanceID)
		networkInterfaceName := fmt.Sprintf("%s-networkinterface", instanceID)
		diskName := fmt.Sprintf("%s-disk", instanceID)

		logr.Debugln("azure destroying instance:", instanceID)
		logr.WithField("id", instanceID).
			WithField("cloud", types.Google)

		poller, err := c.service.BeginDelete(ctx, c.resourceGroupName, instanceID, nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}
		logr.Info("azure instance destroyed:", instanceID)
		err = c.deleteNetworkInterface(ctx, networkInterfaceName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Info("azure: deleted network interface:", networkInterfaceName)
		err = c.deletePublicIP(ctx, publicIPName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Info("azure: deleted public ip:", publicIPName)
		err = c.deleteVirtualNetWork(ctx, vnetName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Info("azure: deleted virtual network:", vnetName)
		err = c.deleteDisk(ctx, diskName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Info("azure: deleted disk: %s", diskName)
		logr.Info("azure: VM terminated")
	}
	return nil
}

func (c *config) Hibernate(_ context.Context, _, _ string) error {
	return errors.New("unimplemented")
}

func (c *config) Start(_ context.Context, _, _ string) (ipAddress string, err error) {
	return "", errors.New("unimplemented")
}

func (c *config) Ping(ctx context.Context) error {
	_, err := azidentity.NewClientSecretCredential(c.tenantID, c.clientID, c.clientSecret, nil)
	if err != nil {
		return err
	}
	return nil
}

func (c *config) Logs(ctx context.Context, instanceID string) (string, error) {
	return "", nil
}

func (c *config) mapToInstance(vm *armcompute.VirtualMachinesClientCreateOrUpdateResponse, opts *types.InstanceCreateOpts) types.Instance {
	return types.Instance{
		ID:           *vm.Name,
		Name:         *vm.Name,
		Provider:     types.Azure,
		State:        types.StateCreated,
		Pool:         opts.PoolName,
		Image:        c.offer,
		Zone:         c.Zones(),
		Size:         c.size,
		Platform:     opts.Platform,
		Address:      c.IPAddress,
		CACert:       opts.CACert,
		CAKey:        opts.CAKey,
		TLSCert:      opts.TLSCert,
		TLSKey:       opts.TLSKey,
		Started:      vm.Properties.TimeCreated.Unix(),
		Updated:      time.Now().Unix(),
		IsHibernated: false,
		Port:         lehelper.LiteEnginePort,
	}
}
