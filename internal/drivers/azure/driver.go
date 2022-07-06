package azure

import (
	"context"
	"fmt"
	"math/rand"
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

	tenantID       string
	clientID       string
	clientSecret   string
	subscriptionID string

	rootDir string

	// vm instance data
	location string // region, example: East US
	diskSize int64
	diskType string

	// image data
	publisher string
	offer     string
	sku       string
	version   string

	network     string
	subnetwork  string
	privateIP   bool
	scopes      []string
	size        string
	tags        []string
	zones       []string
	userData    string
	userDataKey string

	username string
	password string

	service *armcompute.VirtualMachinesClient
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}

	if p.service == nil {
		cred, err := azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, nil)
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

func (p *config) RootDir() string {
	return p.rootDir
}

func (p *config) DriverName() string {
	return string(types.Azure)
}

func (p *config) Zone() string {
	return p.zones[rand.Intn(len(p.zones))] //nolint: gosec
}

func (p *config) InstanceType() string {
	return p.offer
}

func (p *config) CanHibernate() bool {
	return false
}

func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	p.init.Do(func() {
		_ = p.setup(ctx)
	})

	var name = fmt.Sprintf(opts.RunnerName+"-"+opts.PoolName+"-%d", time.Now().Unix())
	var networkInterfaceID string

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Azure).
		WithField("name", name).
		WithField("image", p.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("zone", "").
		WithField("image", p.offer).
		WithField("size", p.size)

	// create the instance
	startTime := time.Now()

	logr.Traceln("azure: creating VM")

	in := armcompute.VirtualMachine{
		Location: to.Ptr(p.location),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(p.size)),
			},
			StorageProfile: &armcompute.StorageProfile{
				ImageReference: &armcompute.ImageReference{
					Publisher: to.Ptr(p.publisher),
					Offer:     to.Ptr(p.offer),
					SKU:       to.Ptr(p.sku),
					Version:   to.Ptr(p.version),
				},
				OSDisk: &armcompute.OSDisk{
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					Caching:      to.Ptr(armcompute.CachingTypesReadWrite),
					ManagedDisk: &armcompute.ManagedDiskParameters{
						StorageAccountType: to.Ptr(armcompute.StorageAccountTypesStandardLRS), // OSDisk type Standard/Premium HDD/SSD
					},
				},
			},
			OSProfile: &armcompute.OSProfile{ //
				ComputerName:  to.Ptr(name),
				AdminUsername: to.Ptr(p.username),
				AdminPassword: to.Ptr(p.password),
				CustomData:    to.Ptr(p.userData),
			},
			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
					{
						ID: to.Ptr(networkInterfaceID),
					},
				},
			},
		},
	}

	poller, err := p.service.BeginCreateOrUpdate(ctx, p.subscriptionID, name, in, nil)
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

	instanceMap := p.mapToInstance(&vm, opts)
	logr.
		WithField("ip", instanceMap.Address).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("azure: [provision] complete")

	return &instanceMap, nil
}

func (p *config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	//TODO implement me
	panic("implement me")
}

func (p *config) Hibernate(ctx context.Context, instanceID, poolName string) error {
	//TODO implement me
	panic("implement me")
}

func (p *config) Start(ctx context.Context, instanceID, poolName string) (ipAddress string, err error) {
	//TODO implement me
	panic("implement me")
}

func (p *config) Ping(ctx context.Context) error {
	//TODO implement me
	panic("implement me")
}

func (p *config) Logs(ctx context.Context, instanceID string) (string, error) {
	//TODO implement me
	panic("implement me")
}

func (p *config) setup(ctx context.Context) error {
	//if reflect.DeepEqual(p.tags, defaultTags) {
	//	return p.setupFirewall(ctx)
	//}
	return nil
}

func (p *config) mapToInstance(vm *armcompute.VirtualMachinesClientCreateOrUpdateResponse, opts *types.InstanceCreateOpts) types.Instance {
	//network := vm.NetworkInterfaces[0]
	//accessConfigs := network.AccessConfigs[0]

	instanceIP := ""
	return types.Instance{
		ID:           *vm.Properties.VMID,
		Name:         *vm.Name,
		Provider:     types.Azure,
		State:        types.StateCreated,
		Pool:         opts.PoolName,
		Image:        p.offer,
		Zone:         p.Zone(),
		Size:         p.size,
		Platform:     opts.Platform,
		Address:      instanceIP,
		CACert:       opts.CACert,
		CAKey:        opts.CAKey,
		TLSCert:      opts.TLSCert,
		TLSKey:       opts.TLSKey,
		Started:      vm.Properties.TimeCreated.Unix(),
		Updated:      time.Now().Unix(),
		IsHibernated: false,
	}
}
