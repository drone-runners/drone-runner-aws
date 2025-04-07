package drivers

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/harness/lite-engine/engine/spec"
)

type IManager interface {
	Inspect(name string) (platform types.Platform, rootDir, driver string)
	Exists(name string) bool
	Find(ctx context.Context, instanceID string) (*types.Instance, error)
	GetInstanceByStageID(ctx context.Context, poolName, stage string) (*types.Instance, error)
	Update(ctx context.Context, instance *types.Instance) error
	Add(pools ...Pool) error
	StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree time.Duration, purgerTime time.Duration) error
	Provision(ctx context.Context, poolName, serverName, ownerID, resourceClass string, VMImageConfig *spec.VMImageConfig, query *types.QueryParams, gitspaceAgentConfig *types.GitspaceAgentConfig, storageConfig *types.StorageConfig, zone, machineType string, shouldUseGoogleDNS bool, info *common.InstanceInfo) (*types.Instance, error) //nolint
	Destroy(ctx context.Context, poolName string, instanceID string, instance *types.Instance, storageCleanupType *storage.CleanupType) error
	BuildPools(ctx context.Context) error
	CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error
	StartInstance(ctx context.Context, poolName, instanceID string) (*types.Instance, error)
	InstanceLogs(ctx context.Context, poolName, instanceID string) (string, error)
	SetInstanceTags(ctx context.Context, poolName string, instance *types.Instance, tags map[string]string) error
	PingDriver(ctx context.Context) error
	GetInstanceStore() store.InstanceStore
	GetStageOwnerStore() store.StageOwnerStore
	GetTLSServerName() string
	IsDistributed() bool
	Suspend(ctx context.Context, poolID string, instanceID string) error
}
