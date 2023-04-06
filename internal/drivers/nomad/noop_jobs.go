// This file is used for scale testing nomad but without actually creating VMs.
// This allows us to test nomad at scale without needing the full infrastructure
// required to support extensive VM creation.
package nomad

import (
	"fmt"

	"github.com/hashicorp/nomad/api"
)

// initJobNoop creates a job which is targeted to a node. It doesn't create or run a VM,
// all it does is sleep for 30 seconds (approximate time of VM creation). This job can be used for scale testing.
func (p *config) initJobNoop(vm, startupScript string, hostPort int, nodeID string) (initJob *api.Job, initjobID, initTaskGroup string) { //nolint:unparam
	initjobID = initJobID(vm)
	initTaskGroup = fmt.Sprintf("init_task_group_%s", vm)

	initJob = &api.Job{
		ID:          &initjobID,
		Name:        stringToPtr(vm),
		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		Constraints: []*api.Constraint{
			{
				LTarget: "${node.unique.id}",
				RTarget: nodeID,
				Operand: "=",
			},
		},
		Reschedule: &api.ReschedulePolicy{
			Attempts:  intToPtr(0),
			Unlimited: boolToPtr(false),
		},
		TaskGroups: []*api.TaskGroup{
			{
				StopAfterClientDisconnect: &clientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(0),
				},
				Name:  stringToPtr(initTaskGroup),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "sleep",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", "sleep 7"},
						},
					},
				},
			},
		},
	}
	return initJob, initjobID, initTaskGroup
}

// resourceJob creates a job which occupies resources until the VM lifecycle
func (p *config) resourceJobNoop(cpus, memGB int, vm string) (job *api.Job, id string) { //nolint:unparam
	id = resourceJobID(vm)
	portLabel := vm

	// This job stays alive to keep resources on nomad busy until the VM is destroyed
	// It sleeps until the max VM creation timeout, after which it periodically checks whether the VM is alive or not
	job = &api.Job{
		ID:          &id,
		Name:        stringToPtr(id),
		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		// TODO (Vistaar): This can be updated once we have more data points
		Reschedule: &api.ReschedulePolicy{
			Attempts:  intToPtr(0),
			Unlimited: boolToPtr(false),
		},
		TaskGroups: []*api.TaskGroup{
			{
				Networks:                  []*api.NetworkResource{{DynamicPorts: []api.Port{{Label: portLabel}}}},
				StopAfterClientDisconnect: &clientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(0),
				},
				Name:  stringToPtr(fmt.Sprintf("init_task_group_resource_%s", vm)),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{

						Name:      "sleep_and_ping",
						Resources: minNomadResources(),
						Driver:    "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", "sleep 3"},
						},
					},
				},
			},
		},
	}
	return job, id
}

// destroyJob returns a job targeted to the given node which stops and removes the VM
func (p *config) destroyJobNoop(vm, nodeID string) (job *api.Job, id string) {
	id = destroyJobID(vm)
	constraint := &api.Constraint{
		LTarget: "${node.unique.id}",
		RTarget: nodeID,
		Operand: "=",
	}
	job = &api.Job{
		ID:   &id,
		Name: stringToPtr(random(20)), //nolint:gomnd

		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		Constraints: []*api.Constraint{
			constraint,
		},
		TaskGroups: []*api.TaskGroup{
			{
				StopAfterClientDisconnect: &clientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(destroyRetryAttempts),
				},
				Name:  stringToPtr(fmt.Sprintf("delete_task_group_%s", vm)),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "ignite_stop_and_rm",
						Resources: minNomadResources(),
						Driver:    "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", "sleep 2"},
						},
					},
				},
			},
		}}
	return job, id
}
