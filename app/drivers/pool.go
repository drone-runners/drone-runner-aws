package drivers

import (
	"context"
	"errors"

	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
)

var ErrorNoInstanceAvailable = errors.New("no free instances available")
var ErrHostIsNotRunning = errors.New("host is not running")

type Pool struct {
	RunnerName string
	Name       string
	// GetMaxSize and GetMinSize should be used for managing pool size: Number of VM instances available in the pool.
	MaxSize int
	MinSize int

	Platform types.Platform
	Driver   Driver
	// Spec stores only the provider-specific spec from the pool YAML (e.g., *config.Google, *config.Amazon, etc.).
	Spec interface{}
	// variant specific data
	VariantID    string
	PoolVariants []types.PoolVariant
}

type Driver interface {
	ReserveCapacity(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.CapacityReservation, err error)
	DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) (err error)
	Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error)
	Destroy(ctx context.Context, instances []*types.Instance) (err error)
	DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) (err error)
	Hibernate(ctx context.Context, instanceID, poolName, zone string) error
	Start(ctx context.Context, instance *types.Instance, poolName string) (ipAddress string, err error)
	SetTags(context.Context, *types.Instance, map[string]string) error
	Ping(ctx context.Context) error
	// Logs returns the console logs for the instance.
	Logs(ctx context.Context, instanceID string) (string, error)

	RootDir() string
	DriverName() string
	CanHibernate() bool
	// GetFullyQualifiedImage returns the fully qualified image name based on the provided VMImageConfig
	GetFullyQualifiedImage(ctx context.Context, config *types.VMImageConfig) (string, error)
	// GetMachineType returns the machine type based on resource class and nested virtualization fallback to default pool
	GetMachineType(resourceClass string, nestedVirt bool) string
}
