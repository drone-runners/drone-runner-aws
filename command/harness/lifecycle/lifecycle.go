// Package lifecycle creates and destroys an ephemeral-delegate VM without
// writing pool state. Cleanup identity is the returned driverState.
package lifecycle

import (
	"context"
	"fmt"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/types"
)

const driverStateVersion = 1

// CreateRequest is one ephemeral VM create. Pool comes from poolfile.ProcessPool.
type CreateRequest struct {
	Pool                 *drivers.Pool
	RunnerName           string
	Env                  string
	AccountID            string
	StageRuntimeID       string
	PipelineExecutionID  string
	Platform             types.Platform
	ImageName            string
	MachineType          string
	Zones                []string
	Timeout              int64
	IsHosted             bool
	DelegatePath         string
	DelegateFallbackPath string
	Token                string
	ManagerEndpoint      string
	TransactionID        string
	DelegateName         string
	Tags                 string
	ParallelWorkers      string
	EgressNoProxy        string
	EgressCACert         string
	ProxyUser            string
	ProxyPassword        string
}

// DriverState is the cleanup blob stored by the Manager. It holds no
// certificates or proxy credentials.
type DriverState struct {
	Version           int    `json:"version"`
	ID                string `json:"id"`
	Name              string `json:"name"`
	Provider          string `json:"provider"`
	PoolName          string `json:"pool_name"`
	Zone              string `json:"zone"`
	StorageIdentifier string `json:"storage_identifier,omitempty"`
	TenantID          string `json:"tenant_id,omitempty"`
}

// Create provisions one Linux Google VM and returns its driverState.
// It does not write instances, stage_owner, capacity, or outbox rows.
func Create(ctx context.Context, req CreateRequest) (DriverState, error) {
	opts, err := drivers.PrepareEphemeralCreateOpts(drivers.EphemeralCreateInput{
		Pool:                 req.Pool,
		RunnerName:           req.RunnerName,
		Env:                  req.Env,
		AccountID:            req.AccountID,
		StageRuntimeID:       req.StageRuntimeID,
		PipelineExecutionID:  req.PipelineExecutionID,
		Platform:             req.Platform,
		ImageName:            req.ImageName,
		MachineType:          req.MachineType,
		Zones:                req.Zones,
		Timeout:              req.Timeout,
		IsHosted:             req.IsHosted,
		DelegatePath:         req.DelegatePath,
		DelegateFallbackPath: req.DelegateFallbackPath,
		Token:                req.Token,
		ManagerEndpoint:      req.ManagerEndpoint,
		TransactionID:        req.TransactionID,
		DelegateName:         req.DelegateName,
		Tags:                 req.Tags,
		ParallelWorkers:      req.ParallelWorkers,
		EgressNoProxy:        req.EgressNoProxy,
		EgressCACert:         req.EgressCACert,
		ProxyUser:            req.ProxyUser,
		ProxyPassword:        req.ProxyPassword,
	})
	if err != nil {
		return DriverState{}, err
	}
	inst, err := req.Pool.DriverForTenant(opts.TenantID).Create(ctx, opts)
	if err != nil {
		return DriverState{}, err
	}
	if inst == nil || inst.ID == "" {
		return DriverState{}, fmt.Errorf("ephemeral create returned no instance")
	}
	tenantID := opts.TenantID
	if inst.TenantID != "" {
		tenantID = inst.TenantID
	}
	return DriverState{
		Version:           driverStateVersion,
		ID:                inst.ID,
		Name:              inst.Name,
		Provider:          string(inst.Provider),
		PoolName:          req.Pool.Name,
		Zone:              inst.Zone,
		StorageIdentifier: inst.StorageIdentifier,
		TenantID:          tenantID,
	}, nil
}

// Destroy deletes the VM described by driverState. A VM that is already gone
// is success on Google. It does not call HandleDestroy or Lite Engine.
func Destroy(ctx context.Context, pool *drivers.Pool, state DriverState) error {
	if pool == nil {
		return fmt.Errorf("pool is required")
	}
	if state.ID == "" {
		return fmt.Errorf("driverState id is required")
	}
	tenantID := state.TenantID
	if tenantID == "" {
		tenantID = types.DefaultTenantID
	}
	inst := &types.Instance{
		ID:                state.ID,
		Name:              state.Name,
		Zone:              state.Zone,
		Provider:          types.DriverType(state.Provider),
		Pool:              state.PoolName,
		StorageIdentifier: state.StorageIdentifier,
		TenantID:          tenantID,
	}
	_, err := pool.DriverForTenant(tenantID).DestroyInstanceAndStorage(ctx, []*types.Instance{inst}, nil)
	return err
}
