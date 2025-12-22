package drivers

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

type IManager interface {
	Inspect(name string) (platform types.Platform, rootDir, driver string)
	List(ctx context.Context, pool string, queryParams *types.QueryParams) (busy, free, hibernating []*types.Instance, err error)
	Exists(name string) bool
	Find(ctx context.Context, instanceID string) (*types.Instance, error)
	GetInstanceByStageID(ctx context.Context, poolName, stage string) (*types.Instance, error)
	Update(ctx context.Context, instance *types.Instance) error
	Add(pools ...Pool) error
	StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree, freeCapacityMaxAge, purgerTime time.Duration) error
	Provision(ctx context.Context, poolName, serverName, ownerID, resourceClass string, machineConfig *types.MachineConfig, query *types.QueryParams, gitspaceAgentConfig *types.GitspaceAgentConfig, storageConfig *types.StorageConfig, info *common.InstanceInfo, timeout int64, isMarkedForInfraReset bool, reservedCapacity *types.CapacityReservation, isCapacityTask bool) (*types.Instance, *types.CapacityReservation, bool, error) //nolint 	//nolint
	Destroy(ctx context.Context, poolName string, instanceID string, instance *types.Instance, storageCleanupType *storage.CleanupType) error
	DestroyCapacity(ctx context.Context, capacityReservation *types.CapacityReservation) error
	BuildPools(ctx context.Context) error
	CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error
	StartInstance(ctx context.Context, poolName, instanceID string, info *common.InstanceInfo) (*types.Instance, error)
	InstanceLogs(ctx context.Context, poolName, instanceID string) (string, error)
	SetInstanceTags(ctx context.Context, poolName string, instance *types.Instance, tags map[string]string) error
	PingDriver(ctx context.Context) error
	GetInstanceStore() store.InstanceStore
	GetStageOwnerStore() store.StageOwnerStore
	GetCapacityReservationStore() store.CapacityReservationStore
	GetTLSServerName() string
	IsDistributed() bool
	GetRunnerConfig() types.RunnerConfig
	GetHealthCheckTimeout(os string, provider types.DriverType) time.Duration
	GetHealthCheckConnectivityDuration() time.Duration
	GetSetupTimeout() time.Duration
	Suspend(ctx context.Context, poolID string, instance *types.Instance) error
	GetPoolSpec(poolName string) (interface{}, error)
}
