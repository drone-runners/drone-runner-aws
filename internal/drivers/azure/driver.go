package azure

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
)

type config struct {
	init sync.Once

	tenantID          string
	clientID          string
	clientSecret      string
	subscriptionID    string
	resourceGroupName string

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
	} else {
		for _, zone := range c.zones {
			z = fmt.Sprintf("%s%s,", z, *zone)
		}
	}
	return z
}

func (c *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	var name = fmt.Sprintf(opts.RunnerName+"-"+opts.PoolName+"-%d", time.Now().Unix())

	vnetName := fmt.Sprintf("%s-vnet", name)
	subnetName := fmt.Sprintf("%s-subnet", name)
	publicIPName := fmt.Sprintf("%s-publicip", name)
	networkInterfaceName := fmt.Sprintf("%s-networkinterface", name)
	diskName := fmt.Sprintf("%s-disk", name)

	var tags = map[string]*string{}
	// add user defined tags
	for k, v := range c.tags {
		tags[k] = &v
	}

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Azure).
		WithField("name", name).
		WithField("image", c.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("zone", "").
		WithField("image", c.offer).
		WithField("size", c.size)

	logr.Debugln("Starting Azure Setup")

	_, err = c.createResourceGroup(ctx, c.cred)
	if err != nil {
		logr.Errorln(err)
		return nil, err
	}
	_, err = c.createVirtualNetwork(ctx, c.cred, vnetName)
	if err != nil {
		logr.Errorln(err)
		return nil, err
	}
	subnet, err := c.createSubnets(ctx, c.cred, subnetName, vnetName)
	if err != nil {
		logr.Errorln(err)
		return nil, err
	}
	publicIP, err := c.createPublicIP(ctx, c.cred, publicIPName)
	if err != nil {
		logr.Errorln(err)
		return nil, err
	}
	c.IPAddress = *publicIP.Properties.IPAddress
	networkInterface, err := c.createNetworkInterface(ctx, c.cred, networkInterfaceName, *subnet.ID, *publicIP.ID)
	if err != nil {
		logr.Errorln(err)
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
				ComputerName:  to.Ptr(name),
				AdminUsername: to.Ptr(c.username),
				AdminPassword: to.Ptr(c.password),
				CustomData:    to.Ptr(uData),
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

func (c *config) Destroy(ctx context.Context, instanceIDs ...string) (error error) {
	logr := logger.FromContext(ctx)
	if c.resourceGroupName == "" {
		c.resourceGroupName = defaultResourceGroup
	}
	logr.Debugln("Azure destroy operation started")
	if len(instanceIDs) == 0 {
		return
	}
	for _, instanceID := range instanceIDs {
		vnetName := fmt.Sprintf("%s-vnet", instanceID)
		publicIPName := fmt.Sprintf("%s-publicip", instanceID)
		networkInterfaceName := fmt.Sprintf("%s-networkinterface", instanceID)
		diskName := fmt.Sprintf("%s-disk", instanceID)

		logr.Debugln("Azure destroying instance", instanceID)
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
		logr.Debugf("Azure instance destroyed %s", instanceID)
		err = c.deleteNetworkInterface(ctx, c.cred, networkInterfaceName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Debugf("Azure: deleted network interface: %s", networkInterfaceName)
		err = c.deletePublicIP(ctx, c.cred, publicIPName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Debugf("Azure: deleted public ip: %s", publicIPName)
		err = c.deleteVirtualNetWork(ctx, c.cred, vnetName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Debugf("Azure: deleted virtual network: %s", vnetName)
		err = c.deleteDisk(ctx, c.cred, diskName)
		if err != nil {
			logr.Errorln(err)
			return err
		}
		logr.Debugf("Azure: deleted disk: %s", diskName)
		logr.Traceln("azure: VM terminated")
	}
	return nil
}

func (c *config) Hibernate(ctx context.Context, instanceID, poolName string) error {
	//TODO implement me
	panic("implement me")
}

func (c *config) Start(ctx context.Context, instanceID, poolName string) (ipAddress string, err error) {
	//TODO implement me
	panic("implement me")
}

func (c *config) Ping(ctx context.Context) error {
	//TODO implement me
	return nil
}

func (c *config) Logs(ctx context.Context, instanceID string) (string, error) {
	//TODO implement me
	panic("implement me")
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
	}
}
