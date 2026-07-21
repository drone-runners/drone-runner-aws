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

	// Multi-tenant data. For single-tenant pools these are empty and Driver is used directly
	// (backward compatible). For multi-tenant pools, TenantDrivers holds one driver per tenant
	// id (including the default tenant), AccountToTenant maps customer account IDs to tenant ids,
	// and Tenants holds per-tenant resolved metadata (spec/sizing/variants).
	TenantDrivers   map[string]Driver
	AccountToTenant map[string]string
	Tenants         []TenantPool
}

// TenantPool holds the resolved per-tenant configuration for a multi-tenant pool.
type TenantPool struct {
	ID           string
	Spec         interface{}
	MinSize      int
	MaxSize      int
	PoolVariants []types.PoolVariant
}

// IsMultiTenant reports whether the pool has explicit tenant configuration.
func (p *Pool) IsMultiTenant() bool {
	return len(p.TenantDrivers) > 0
}

// ResolveTenant maps a customer account ID to a tenant ID for this pool. Unknown accounts (and
// all accounts on single-tenant pools) resolve to the default tenant.
func (p *Pool) ResolveTenant(accountID string) string {
	if len(p.AccountToTenant) > 0 {
		if tenantID, ok := p.AccountToTenant[accountID]; ok {
			return tenantID
		}
	}
	return types.DefaultTenantID
}

// destroyByTenant destroys a batch of instances, grouping them by tenant id so that each group
// is destroyed with the correct per-tenant driver. For single-tenant pools it delegates directly
// to the pool driver (unchanged behavior).
func destroyByTenant(ctx context.Context, pool *Pool, instances []*types.Instance) ([]*types.Instance, error) {
	if !pool.IsMultiTenant() {
		return pool.Driver.Destroy(ctx, instances)
	}
	byTenant := map[string][]*types.Instance{}
	for _, inst := range instances {
		tid := inst.TenantID
		if tid == "" {
			tid = types.DefaultTenantID
		}
		byTenant[tid] = append(byTenant[tid], inst)
	}
	var failed []*types.Instance
	var firstErr error
	for tid, insts := range byTenant {
		f, err := pool.DriverForTenant(tid).Destroy(ctx, insts)
		failed = append(failed, f...)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return failed, firstErr
}

// DriverForTenant returns the driver responsible for the given tenant ID. It falls back to the
// pool's default Driver when the tenant is unknown or the pool is single-tenant, so existing
// call sites keep working unchanged.
func (p *Pool) DriverForTenant(tenantID string) Driver {
	if len(p.TenantDrivers) > 0 {
		if d, ok := p.TenantDrivers[tenantID]; ok && d != nil {
			return d
		}
		if d, ok := p.TenantDrivers[types.DefaultTenantID]; ok && d != nil {
			return d
		}
	}
	return p.Driver
}

type Driver interface {
	ReserveCapacity(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.CapacityReservation, err error)
	DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) (err error)
	Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error)
	Destroy(ctx context.Context, instances []*types.Instance) (failedInstances []*types.Instance, err error)
	DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) (failedInstances []*types.Instance, err error)
	Hibernate(ctx context.Context, instanceID, poolName, zone string) error
	Start(ctx context.Context, instance *types.Instance, poolName string) (ipAddress string, err error)
	SetTags(context.Context, *types.Instance, map[string]string) error
	SetLabels(context.Context, *types.Instance, map[string]string) error
	Ping(ctx context.Context) error
	// Logs returns the console logs for the instance.
	Logs(ctx context.Context, instanceID string) (string, error)

	RootDir() string
	DriverName() string
	CanHibernate() bool
	// GetFullyQualifiedImage returns the fully qualified image name based on the provided VMImageConfig
	GetFullyQualifiedImage(ctx context.Context, config *types.VMImageConfig) (string, error)
}
