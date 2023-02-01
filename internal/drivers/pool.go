package drivers

import (
	"context"
	"errors"

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

	Driver Driver
}

type Driver interface {
	Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error)
	Destroy(ctx context.Context, instances []*types.Instance) (err error)
	Hibernate(ctx context.Context, instanceID, poolName string) error
	Start(ctx context.Context, instanceID, poolName string) (ipAddress string, err error)
	SetTags(context.Context, *types.Instance, map[string]string) error
	Ping(ctx context.Context) error
	// Logs returns the console logs for the instance.
	Logs(ctx context.Context, instanceID string) (string, error)
	// InstanceExists on provisioning check db instance still exists in cloud
	InstanceExists(ctx context.Context, instanceID string) (bool, error)
	RootDir() string
	DriverName() string
	CanHibernate() bool
}
