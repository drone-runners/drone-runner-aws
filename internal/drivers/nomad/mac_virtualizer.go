package nomad

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/hashicorp/nomad/api"
)

type MacVirtualizer struct{}

func NewMacVirtualizer() *MacVirtualizer {
	return &MacVirtualizer{}
}

func (mv *MacVirtualizer) GetInitJob(vm, nodeID, vmImage, userData, username, password string, port int, resource cf.NomadResource, opts *types.InstanceCreateOpts, gitspacesPortMappings map[int]int) (job *api.Job, id, group string) { //nolint
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(mv.generateUserData(userData, opts)))
	startupScript := base64.StdEncoding.EncodeToString([]byte(mv.generateStartupScript(vm, vmImage, username, password, resource, port)))
	vmStartupScriptPath := fmt.Sprintf("/usr/local/bin/%s.sh", vm)
	cloudInitScriptPath := fmt.Sprintf("/usr/local/bin/cloud_init_%s.sh", vm)
	id = "tart_job_" + vm
	group = fmt.Sprintf("init_task_group_%s", vm)
	entrypoint := mv.GetEntryPoint()

	tartJob := &api.Job{
		ID:          stringToPtr(id),
		Name:        stringToPtr(id),
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
				Name:  stringToPtr(group),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "create_and_start_vm_prepare_script",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", fmt.Sprintf("echo %s >> %s; echo %s | base64 --decode >> %s; cat %s | base64 --decode | bash", startupScript, vmStartupScriptPath, encodedUserData, cloudInitScriptPath, vmStartupScriptPath)}, //nolint
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "prestart",
						},
					},
					{
						Name:      "run_cmd",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", mv.getStartCloudInitScript(cloudInitScriptPath, vm, username, password)},
						},
					},
					{
						Name:      "cleanup_vm_script",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", mv.getPostStartUpScript(vmStartupScriptPath, cloudInitScriptPath, vm)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "poststop",
						},
					},
				},
			},
		},
	}
	return tartJob, id, group
}

func (mv *MacVirtualizer) generateUserData(userData string, opts *types.InstanceCreateOpts) string {
	return lehelper.GenerateUserdata(userData, opts)
}

func (mv *MacVirtualizer) generateStartupScript(vmID, vmImage, username, password string, resource cf.NomadResource, port int) string {
	// can ignore the error since it was already checked
	memGB, _ := strconv.Atoi(resource.MemoryGB)
	return fmt.Sprintf(`
#!/usr/bin/env bash
set -eo pipefail

VM_IMAGE="%s"
VM_ID="%s"

VM_USER="%s"
VM_PASSWORD="%s"

echo "Cloning tart VM with id $VM_ID"
# Install the VM
/opt/homebrew/bin/tart clone "$VM_IMAGE" "$VM_ID"

echo "Setting tart VM config with id $VM_ID"
# Update VM configuration
/opt/homebrew/bin/tart set "$VM_ID" --cpu %s --memory %d --disk-size %s

echo "Starting tart VM with id $VM_ID"
# Run the VM in background
/opt/homebrew/sbin/daemonize /opt/homebrew/bin/tart run "$VM_ID" --no-graphics

# Wait for VM to get IP
echo "Waiting for VM to get IP"
VM_IP=$(/opt/homebrew/bin/tart ip "$VM_ID" --wait 30 || true)

if [ -n "$VM_IP" ]; then
    echo "VM got IP: $VM_IP"
else
    echo 'Waited 30 seconds for VM to start, exiting...'
    exit "1"
fi

# Stop VM to apply port forwarding otherwise VMs loose internet connectivity
echo "Stopping tart VM with id $VM_ID"
/opt/homebrew/bin/tart stop "$VM_ID"

# Port forwarding
ANCHOR_FILE="/etc/pf.anchors/tart"

# Content to add to the anchor file
ANCHOR_CONTENT="rdr pass log (all) on en0 inet proto tcp from any to any port %d -> $(/opt/homebrew/bin/tart ip %s) port 9079"

# Check if the anchor file exists and delete it
if [ -f "$ANCHOR_FILE" ]; then
    rm "$ANCHOR_FILE"
fi

# Create the anchor file and add the content
echo "$ANCHOR_CONTENT" > "$ANCHOR_FILE"

# Reload packet filter
sudo pfctl -Fa -f /etc/pf.conf

echo "Re-starting tart VM with id $VM_ID"
# Run the VM in background
/opt/homebrew/sbin/daemonize /opt/homebrew/bin/tart run "$VM_ID" --no-graphics

# Wait for ssh to become available
MAX_RETRIES=15  # Set the maximum number of retries
RETRY_COUNT=0

while ! /opt/homebrew/bin/sshpass -p "$VM_PASSWORD" ssh -o "ConnectTimeout=1" -o "StrictHostKeyChecking=no" $VM_USER@$VM_IP exit 0>/dev/null; do
  if [ $RETRY_COUNT -ge $MAX_RETRIES ]; then
    echo "Failed to connect to VM after $RETRY_COUNT attempts"
    exit 1
  fi

  RETRY_COUNT=$((RETRY_COUNT + 1))
  echo "Waiting for VM to come up (attempt $RETRY_COUNT/$MAX_RETRIES)"
  sleep 1
done

echo "Tart VM Started"

`, vmImage, vmID, username, password, resource.Cpus, convertGigsToMegs(memGB), resource.DiskSize, port, vmID)
}

func (mv *MacVirtualizer) GetMachineFrequency() int {
	return macMachineFrequencyMhz
}

func (mv *MacVirtualizer) GetGlobalAccountID() string {
	return globalAccountMac
}

func (mv *MacVirtualizer) GetEntryPoint() string {
	return "/bin/sh"
}

func (mv *MacVirtualizer) GetHealthCheckupGenerator() func(time.Duration, string, string) string {
	return func(sleep time.Duration, vm, port string) string {
		sleepSecs := sleep.Seconds()
		return fmt.Sprintf(`
#!/usr/bin/bash
sleep %f
echo "done sleeping, port is: %s, tart ip is $(/opt/homebrew/bin/tart ip %s)"
cntr=0
while true
	do
		nc -vz $(/opt/homebrew/bin/tart ip %s) %s
		if [ $? -eq 1 ]; then
		    echo "port check failed, incrementing counter:"
			echo "cntr: "$cntr
			((cntr++))
			if [ $cntr -eq 3 ]; then
				echo "port check failed three times, exiting..."
				exit 1
			fi
		else
			cntr=0
			echo "Port check passed..."
		fi
		sleep 20
	done`, sleepSecs, port, vm, vm, port)
	}
}

func (mv *MacVirtualizer) GetDestroyScriptGenerator() func(string) string {
	return func(vm string) string {
		return fmt.Sprintf(`
	    /opt/homebrew/bin/tart stop %s; /opt/homebrew/bin/tart delete %s
		if [ $? -ne 0 ]; then
		  tart_pid=$(ps -A | grep -m1 "tart run %s" | awk '{print $1}')
		  if [ -n "$tart_pid" ]; then
		  	kill $tart_pid || true
		  fi
		fi
	`, vm, vm, vm)
	}
}

// This will be responsible to copy the script from host to vm and run it
func (mv *MacVirtualizer) getStartCloudInitScript(cloudInitScriptPath, vmID, username, password string) string {
	return fmt.Sprintf(`
VM_USER="%s"
VM_PASSWORD="%s"
/opt/homebrew/bin/sshpass -p "$VM_PASSWORD" scp %s $VM_USER@$(/opt/homebrew/bin/tart ip %s):/Users/anka/cloud_init.sh

/opt/homebrew/bin/sshpass -p "$VM_PASSWORD" ssh -o "ConnectTimeout=1" -o "StrictHostKeyChecking no" $VM_USER@$(/opt/homebrew/bin/tart ip %s) "echo $VM_PASSWORD | sudo -S sh /Users/anka/cloud_init.sh"
`, username, password, cloudInitScriptPath, vmID, vmID)
}

// This will be responsible to port forward the traffic from host to VM
func (mv *MacVirtualizer) getPostStartUpScript(vmStartupScriptPath, cloudInitScriptPath, vmID string) string {
	return fmt.Sprintf(`
echo "Cleaning up vm startup and cloudinit script"
rm %s %s

echo "Doing lite-engine healthcheck"
nc -zv $(/opt/homebrew/bin/tart ip %s) 9079
`, vmStartupScriptPath, cloudInitScriptPath, vmID)
}
