package nomad

import (
	"time"

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/hashicorp/nomad/api"
)

type Virtualizer interface {
	// InitJob creates a job which is targeted to a specific node. The job does the following:
	//  1. Starts a VM with the provided config
	//  2. Runs a startup script inside the VM
	GetInitJob(vm, nodeID, userData, machinePassword string, vmImageConfig types.VMImageConfig, port int, resource cf.NomadResource, opts *types.InstanceCreateOpts, gitspacesPortMappings map[int]int) (job *api.Job, id, group string, err error) //nolint
	// Returns Machine Frequency
	GetMachineFrequency() int
	// Returns GlobalAccountId
	GetGlobalAccountID() string
	// To make nomad keep resources occupied until the VM is alive, we do a periodic health check
	// by checking whether the lite engine port on the VM is open or not.
	GetHealthCheckupGenerator() func(time.Duration, string, string) string
	// Returns destroy script generator
	GetDestroyScriptGenerator() func(vm, machinePassword string) string
	// Returns entrypoint
	GetEntryPoint() string
	// Returns healthcheck port
	GetHealthCheckPort(portLabel string) string
}
