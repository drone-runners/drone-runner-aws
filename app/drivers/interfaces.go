package drivers

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// InstanceProvisioner handles instance provisioning and destruction.
type InstanceProvisioner interface {
	// Provision returns an instance for a job execution and tags it as in use.
	Provision(
		ctx context.Context,
		poolName, serverName, ownerID string,
		machineConfig *types.MachineConfig,
		query *types.QueryParams,
		gitspaceAgentConfig *types.GitspaceAgentConfig,
		storageConfig *types.StorageConfig,
		info *common.InstanceInfo,
		timeout int64,
		isMarkedForInfraReset bool,
		reservedCapacity *types.CapacityReservation,
		isCapacityTask bool,
	) (*types.Instance, *types.CapacityReservation, bool, error)

	// Destroy destroys an instance in a pool.
	Destroy(ctx context.Context, poolName, instanceID string, instance *types.Instance, storageCleanupType *storage.CleanupType) error

	// DestroyCapacity destroys a capacity reservation.
	DestroyCapacity(ctx context.Context, capacityReservation *types.CapacityReservation) error
}

// InstanceQuerier provides read operations for instances.
type InstanceQuerier interface {
	// Find finds an instance by ID.
	Find(ctx context.Context, instanceID string) (*types.Instance, error)

	// List lists instances in a pool by state.
	List(ctx context.Context, pool string, queryParams *types.QueryParams) (busy, free, hibernating, provisioning []*types.Instance, err error)

	// GetInstanceByStageID gets an instance by stage ID.
	GetInstanceByStageID(ctx context.Context, poolName, stage string) (*types.Instance, error)

	// Exists returns true if a pool with given name exists.
	Exists(name string) bool

	// Update updates an instance.
	Update(ctx context.Context, instance *types.Instance) error
}

// PoolManager handles pool operations.
type PoolManager interface {
	// Add adds pools to the manager.
	Add(pools ...Pool) error

	// BuildPools populates pools with instances.
	BuildPools(ctx context.Context) error

	// CleanPools cleans up pools.
	CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error

	// Inspect returns platform, root directory, and driver name for a pool.
	Inspect(name string) (platform types.Platform, rootDir, driver string)

	// GetPoolSpec returns the pool specification.
	GetPoolSpec(poolName string) (interface{}, error)
}

// InstanceLifecycle handles instance lifecycle operations.
type InstanceLifecycle interface {
	// StartInstance starts a hibernated instance.
	StartInstance(ctx context.Context, poolName, instanceID string, info *common.InstanceInfo) (*types.Instance, error)

	// Suspend suspends an instance.
	Suspend(ctx context.Context, poolID string, instance *types.Instance) error

	// SetInstanceTags sets tags on an instance.
	SetInstanceTags(ctx context.Context, poolName string, instance *types.Instance, tags map[string]string) error

	// InstanceLogs returns logs for an instance.
	InstanceLogs(ctx context.Context, poolName, instanceID string) (string, error)
}

// HealthChecker provides health check operations.
type HealthChecker interface {
	// PingDriver pings the driver to check connectivity.
	PingDriver(ctx context.Context) error

	// GetHealthCheckTimeout returns the appropriate health check timeout.
	GetHealthCheckTimeout(os string, provider types.DriverType, warmed, hibernated bool) time.Duration

	// GetHealthCheckConnectivityDuration returns the health check connectivity duration.
	GetHealthCheckConnectivityDuration() time.Duration
}

// StoreProvider provides access to data stores.
type StoreProvider interface {
	// GetInstanceStore returns the instance store.
	GetInstanceStore() store.InstanceStore

	// GetStageOwnerStore returns the stage owner store.
	GetStageOwnerStore() store.StageOwnerStore

	// GetCapacityReservationStore returns the capacity reservation store.
	GetCapacityReservationStore() store.CapacityReservationStore
}

// ConfigProvider provides access to configuration.
type ConfigProvider interface {
	// GetTLSServerName returns the TLS server name.
	GetTLSServerName() string

	// IsDistributed returns whether the manager is in distributed mode.
	IsDistributed() bool

	// GetRunnerConfig returns the runner configuration.
	GetRunnerConfig() types.RunnerConfig

	// GetSetupTimeout returns the setup timeout.
	GetSetupTimeout() time.Duration
}

// PurgerStarter starts the instance purger.
type PurgerStarter interface {
	// StartInstancePurger starts the background instance purger.
	StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree, freeCapacityMaxAge, purgerTime time.Duration) error
}
