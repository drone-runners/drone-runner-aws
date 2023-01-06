package noop

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/types"
)

type config struct {
	rootDir           string
	hibernate         bool
	hibernateWaitSecs int
	startWaitSecs     int
	createWaitSecs    int
	destroyWaitSecs   int
	tagWaitSecs       int
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}

	return p, nil
}

func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	time.Sleep(time.Duration(p.createWaitSecs) * time.Second)
	return &types.Instance{}, nil
}

func (p *config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	time.Sleep(time.Duration(p.destroyWaitSecs) * time.Second)
	return nil
}

func (p *config) Hibernate(ctx context.Context, instanceID, poolName string) error {
	time.Sleep(time.Duration(p.hibernateWaitSecs) * time.Second)
	return nil
}

func (p *config) Start(ctx context.Context, instanceID, poolName string) (ipAddress string, err error) {
	time.Sleep(time.Duration(p.startWaitSecs) * time.Second)
	return "127.0.0.1", nil
}

func (p *config) SetTags(context.Context, *types.Instance, map[string]string) error {
	time.Sleep(time.Duration(p.tagWaitSecs) * time.Second)
	return nil
}

func (p *config) Ping(_ context.Context) error {
	return nil
}

func (p *config) Logs(ctx context.Context, instanceID string) (string, error) {
	return "", nil
}

func (p *config) RootDir() string {
	return p.rootDir
}
func (p *config) DriverName() string {
	return string(types.Noop)
}

func (p *config) CanHibernate() bool {
	return p.hibernate
}
