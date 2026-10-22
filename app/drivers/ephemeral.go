package drivers

import (
	"fmt"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

// EphemeralCreateInput is the pool and caller data for one ephemeral VM.
// Store writes stay with setupInstance. This path does not call it.
type EphemeralCreateInput struct {
	Pool                 *Pool
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

// PrepareEphemeralCreateOpts builds create options for an ephemeral Linux Google
// VM. Certificates and instance-store fields are left empty. The Google driver
// fills egress control and the network proxy URL during Create.
func PrepareEphemeralCreateOpts(in EphemeralCreateInput) (*types.InstanceCreateOpts, error) {
	if in.Pool == nil {
		return nil, fmt.Errorf("pool is required")
	}
	if in.DelegatePath == "" {
		return nil, fmt.Errorf("delegate download path is required")
	}
	tenantID := in.Pool.ResolveTenant(in.AccountID)
	driver := in.Pool.DriverForTenant(tenantID)
	if driver == nil || driver.DriverName() != string(types.Google) {
		return nil, fmt.Errorf("ephemeral create supports the google driver only")
	}
	platform := in.Platform
	if platform.OS == "" {
		platform = in.Pool.Platform
	}
	if platform.OS != oshelp.OSLinux {
		return nil, fmt.Errorf("ephemeral create supports linux only")
	}
	egress, err := googlePoolEgress(in.Pool, tenantID)
	if err != nil {
		return nil, err
	}
	if egress && in.EgressCACert == "" {
		return nil, fmt.Errorf("egress CA certificate is required")
	}

	opts := &types.InstanceCreateOpts{
		EphemeralDelegate:       true,
		Platform:                platform,
		PoolName:                in.Pool.Name,
		RunnerName:              in.RunnerName,
		AccountID:               in.AccountID,
		StageRuntimeID:          in.StageRuntimeID,
		PipelineExecutionID:     in.PipelineExecutionID,
		TenantID:                tenantID,
		DriverName:              driver.DriverName(),
		Timeout:                 in.Timeout,
		IsHosted:                in.IsHosted,
		Zones:                   append([]string(nil), in.Zones...),
		MachineType:             in.MachineType,
		VMImageConfig:           types.VMImageConfig{ImageName: in.ImageName},
		DelegatePath:            in.DelegatePath,
		DelegateFallbackPath:    in.DelegateFallbackPath,
		DelegateToken:           in.Token,
		DelegateManagerEndpoint: in.ManagerEndpoint,
		DelegateTransactionID:   in.TransactionID,
		DelegateName:            in.DelegateName,
		DelegateTags:            in.Tags,
		DelegateParallelWorkers: in.ParallelWorkers,
		EgressNoProxy:           in.EgressNoProxy,
		EgressCACert:            in.EgressCACert,
		EgressProxyUser:         in.ProxyUser,
		EgressProxyPassword:     in.ProxyPassword,
	}
	opts.VMLabels = buildIdentityVMLabels(&types.SetupInstanceParams{
		AccountID:           in.AccountID,
		StageRuntimeID:      in.StageRuntimeID,
		PipelineExecutionID: in.PipelineExecutionID,
		Source:              types.InstanceSourceOnDemand,
	}, in.Timeout, in.Env, in.Pool.Name, types.InstanceSourceOnDemand)
	return opts, nil
}

func googlePoolEgress(pool *Pool, tenantID string) (bool, error) {
	spec := specForTenant(pool, tenantID)
	g, ok := spec.(*config.Google)
	if !ok || g == nil {
		return false, fmt.Errorf("ephemeral create requires a google pool spec")
	}
	return g.EgressControl, nil
}
