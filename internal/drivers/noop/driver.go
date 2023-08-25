package noop

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/google/uuid"
)

type config struct {
	rootDir           string
	hibernate         bool
	hibernateWaitSecs int
	startWaitSecs     int
	createWaitSecs    int
	destroyWaitSecs   int
	tagWaitSecs       int
	leIP              string
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}

	p.hibernateWaitSecs = 5
	p.startWaitSecs = 10
	p.createWaitSecs = 15
	p.destroyWaitSecs = 5
	p.tagWaitSecs = 1
	p.leIP = "127.0.0.1"

	return p, nil
}

func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	time.Sleep(time.Duration(p.createWaitSecs) * time.Second)
	id := uuid.New().String()
	return &types.Instance{
		ID:           id,
		Name:         id,
		Provider:     types.Noop, // this is driver, though its the old legacy name of provider
		State:        types.StateCreated,
		Pool:         opts.PoolName,
		Platform:     opts.Platform,
		Address:      p.leIP,
		CACert:       opts.CACert,
		CAKey:        opts.CAKey,
		TLSCert:      opts.TLSCert,
		TLSKey:       opts.TLSKey,
		Started:      time.Now().Unix(),
		Updated:      time.Now().Unix(),
		IsHibernated: false,
		Port:         le.LiteEnginePort,
	}, nil
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
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
