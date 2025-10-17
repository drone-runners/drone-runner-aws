package noop

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/google/uuid"
)

var _ drivers.Driver = (*config)(nil)

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

// ReserveCapacity reserves capacity for a VM
func (p *config) ReserveCapacity(ctx context.Context, opts *types.InstanceCreateOpts) (*types.CapacityReservation, error) {
	return nil, &ierrors.ErrCapacityReservationNotSupported{Driver: p.DriverName()}
}

// DestroyCapacity destroys capacity for a VM
func (p *config) DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) (err error) {
	return &ierrors.ErrCapacityReservationNotSupported{Driver: p.DriverName()}
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
		Port:         lehelper.LiteEnginePort,
	}, nil
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	return p.DestroyInstanceAndStorage(ctx, instances, nil)
}

func (p *config) DestroyInstanceAndStorage(_ context.Context, _ []*types.Instance, _ *storage.CleanupType) (err error) {
	time.Sleep(time.Duration(p.destroyWaitSecs) * time.Second)
	return nil
}

func (p *config) Hibernate(_ context.Context, _, _, _ string) error {
	time.Sleep(time.Duration(p.hibernateWaitSecs) * time.Second)
	return nil
}

func (p *config) Start(_ context.Context, _ *types.Instance, _ string) (ipAddress string, err error) {
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

func (p *config) GetFullyQualifiedImage(_ context.Context, config *types.VMImageConfig) (string, error) {
	// For Noop driver, just return the image name as is
	// This is a no-op implementation used for testing
	return config.ImageName, nil
}
