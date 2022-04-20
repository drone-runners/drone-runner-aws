package drivers

import (
	"context"
	"errors"

	"github.com/drone-runners/drone-runner-aws/types"
)

var ErrorNoInstanceAvailable = errors.New("no free instances available")
var ErrHostIsNotRunning = errors.New("host is not running")

type Pool struct {
	Name string
	// GetMaxSize and GetMinSize should be used for managing pool size: Number of VM instances available in the pool.
	MaxSize int
	MinSize int

	Driver Driver
}

type Driver interface {
	Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error)
	Destroy(ctx context.Context, instanceIDs ...string) (err error)
	Hibernate(ctx context.Context, instanceID string) error
	Start(ctx context.Context, instanceID string) (ipAddress string, err error)
	Ping(ctx context.Context) error

	ProviderName() string
	RootDir() string
	OS() string
}
