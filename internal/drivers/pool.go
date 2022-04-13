package drivers

import (
	"context"
	"errors"

	"github.com/drone-runners/drone-runner-aws/types"
)

var ErrorNoInstanceAvailable = errors.New("no free instances available")

type Pool interface {
	// GetProviderName returns VM provider name. It should be a fixed string for each implementation. The value is used for logging.
	GetProviderName() string

	GetName() string
	GetOS() string
	GetRootDir() string

	// GetMaxSize and GetMinSize should be used for managing pool size: Number of VM instances available in the pool.
	GetMaxSize() int
	GetMinSize() int

	// CanHibernate returns whether VMs of the pool can be hibernated or not.
	CanHibernate() bool

	PingProvider(ctx context.Context) error
	Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error)
	Destroy(ctx context.Context, instanceIDs ...string) (err error)
	Hibernate(ctx context.Context, instanceID string) error
	Start(ctx context.Context, instanceID string) (ipAddress string, err error)
}
