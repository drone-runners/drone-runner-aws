package nomad

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/hashicorp/nomad/api"
)

const lockFunction = `
LOCK_DIR="/tmp/mydir.lock"
MAX_RETRIES=10
RETRY_DELAY=1
counter=0

while ! mkdir "$LOCK_DIR" 2>/dev/null; do
  counter=$((counter + 1))
  if [ "$counter" -ge "$MAX_RETRIES" ]; then
    echo "Maximum retries reached. Continuing..."
	rm -rf "$LOCK_DIR"
    break
  fi
  echo "Lock already exists. Retrying in $RETRY_DELAY seconds..."
  sleep $RETRY_DELAY
done`

const UnlockFunction = `
if [ "$counter" -lt "$MAX_RETRIES" ]; then
  # Release lock
  rm -rf "$LOCK_DIR"
  echo "Lock released."
fi`

type MacVirtualizer struct{}

func NewMacVirtualizer() *MacVirtualizer {
	return &MacVirtualizer{}
}

func (mv *MacVirtualizer) GetInitJob(vm, nodeID, userData, machinePassword, defaultVMImage string, vmImageConfig types.VMImageConfig, port int, resource cf.NomadResource, opts *types.InstanceCreateOpts, gitspacesPortMappings map[int]int, timeout string) (job *api.Job, id, group string, err error) { //nolint
	uData, err := mv.generateUserData(userData, opts)
	if err != nil {
		return nil, "", "", err
	}
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(uData))
	startupScript := base64.StdEncoding.EncodeToString([]byte(mv.generateStartupScript(vm, machinePassword, defaultVMImage, vmImageConfig, resource, port)))
	vmStartupScriptPath := fmt.Sprintf("/tmp/%s.sh", vm)
	cloudInitScriptPath := fmt.Sprintf("/tmp/cloud_init_%s.sh", vm)
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
							"args":    []string{"-c", mv.getStartCloudInitScript(cloudInitScriptPath, vm, vmImageConfig.Username, vmImageConfig.Password)},
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
	return tartJob, id, group, err
}

func (mv *MacVirtualizer) generateUserData(userData string, opts *types.InstanceCreateOpts) (string, error) {
	return lehelper.GenerateUserdata(userData, opts)
}

func (mv *MacVirtualizer) generateStartupScript(vmID, machinePassword, defaultVMImage string, vmImageConfig types.VMImageConfig, resource cf.NomadResource, port int) string { //nolint:gocritic
	// can ignore the error since it was already checked
	memGB, _ := strconv.Atoi(resource.MemoryGB)
	return fmt.Sprintf(`
#!/usr/bin/env bash
set -eo pipefail

DEFAULT_VM_IMAGE="%s"
VM_IMAGE="%s"
VM_ID="%s"

VM_USER="%s"
VM_PASSWORD="%s"

REGISTRY="%s"
REGISTRY_USERNAME="%s"
REGISTRY_PASSWORD="%s"
MACHINE_PASSWORD="%s"

tart_list=$(/opt/homebrew/bin/tart list | awk 'NR>1 {print $2}')

# Check if the image is already in the tart list
if echo "$tart_list" | grep -q "$VM_IMAGE"; then
  echo "Image '$VM_IMAGE' is already present. Nothing to do."
else
  echo "Image '$VM_IMAGE' not found. Deleting all other images..."

  # Loop through each image and delete it except the one specified
  for image in $tart_list; do
    if [ "$image" != "$DEFAULT_VM_IMAGE" ]; then
      echo "Deleting image '$image'..."
      /opt/homebrew/bin/tart delete "$image" || true
    fi
  done

  echo "Done deleting other images."

  if [ -n "$REGISTRY" ] && [ -n "$REGISTRY_USERNAME" ] && [ -n "$REGISTRY_PASSWORD" ]; then
  	  echo "Logging into registry..."
	  mv ~/Library/Keychains/login.keychain-db ~/Library/Keychains/login.keychain-db.backup
	  security create-keychain -p "$MACHINE_PASSWORD" login.keychain-db
	  security unlock-keychain -p "$MACHINE_PASSWORD" ~/Library/Keychains/login.keychain-db
      echo "$REGISTRY_PASSWORD" | /opt/homebrew/bin/tart login "$REGISTRY" --username "$REGISTRY_USERNAME" --password-stdin
  else
	  echo "No registry details provided, skipping logging."
  fi
fi

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

attempt=1
max_attempts=3
sleep_duration=5

# Stop VM to apply port forwarding otherwise VMs lose internet connectivity
echo "Stopping tart VM with id $VM_ID"
while [ "$attempt" -le "$max_attempts" ]; do
  if /opt/homebrew/bin/tart stop "$VM_ID"; then
    echo "VM stopped successfully."
    break  # Exit the loop if successful
  else
    echo "Attempt $attempt failed. Retrying in $sleep_duration seconds..."
    attempt=$((attempt + 1))
    sleep "$sleep_duration"
  fi
done

# If VM stop failed after all attempts, exit the script
if [ "$attempt" -gt "$max_attempts" ]; then
  echo "Failed to stop VM after $max_attempts attempts. Exiting."
  exit 1
fi

# LOCK
%s

VM_IP=$(/opt/homebrew/bin/tart ip %s)
ANCHOR_FILE="/etc/pf.anchors/tart"
ANCHOR_CONTENT="rdr pass log (all) on en0 inet proto tcp from any to any port %d -> $VM_IP port 9079"

# Remove any existing entry with same IP if present
echo "$MACHINE_PASSWORD" | sudo -S sed -i '' "/$VM_IP/d" "$ANCHOR_FILE"
echo "$MACHINE_PASSWORD" | sudo -S sh -c "echo '$ANCHOR_CONTENT' | tee -a '$ANCHOR_FILE'"
echo "$MACHINE_PASSWORD" | sudo -S pfctl -a tart -f "$ANCHOR_FILE"

#UNLOCK
%s

# Sleep of 5s so that internet connectivity is not affected in VM after packer filter reload
sleep 5

echo "Re-starting tart VM with id $VM_ID"
# Run the VM in background
/opt/homebrew/sbin/daemonize /opt/homebrew/bin/tart run "$VM_ID" --no-graphics

# Remove known_hosts file to avoid too many authentication errors
if [ -f ~/.ssh/known_hosts ]; then
    rm ~/.ssh/known_hosts
fi

# Wait for ssh to become available
MAX_RETRIES=15  # Set the maximum number of retries
RETRY_COUNT=0

while true
	do
		expect <<- DONE
			set timeout 10
			spawn ssh -v -o "ConnectTimeout=10" -o "StrictHostKeyChecking=no" $VM_USER@$VM_IP exit
			expect {
				"*yes/no*" {
					send "yes\r"
					exp_continue
				}
				"*Password:*" {
					send "$VM_PASSWORD\r"
					exp_continue
				}
				eof {
					set exit_status [lindex [wait] 3]
					exit $exit_status
				}
			}
		DONE

		if [ $? -eq 0 ]; then
			echo "Successfully connected to the VM."
			break
		fi

		if [ $RETRY_COUNT -ge $MAX_RETRIES ]; then
			echo "Failed to connect to VM after $RETRY_COUNT attempts."
			exit 1
		fi

		RETRY_COUNT=$((RETRY_COUNT + 1))
		echo "Waiting for VM to come up (attempt $RETRY_COUNT/$MAX_RETRIES)"
		sleep 1
	done
echo "Tart VM Started"
`, defaultVMImage, vmImageConfig.ImageName, vmID, vmImageConfig.Username, vmImageConfig.Password, vmImageConfig.VMImageAuth.Registry,
		vmImageConfig.VMImageAuth.Username, vmImageConfig.VMImageAuth.Password, machinePassword, resource.Cpus, convertGigsToMegs(memGB), resource.DiskSize, lockFunction, vmID, port, UnlockFunction)
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

func (mv *MacVirtualizer) GetDestroyScriptGenerator() func(string, string) string {
	return func(vm, machinePassword string) string {
		command := fmt.Sprintf(`
#!/usr/bin/env bash
		
VM_ID="%s"
echo "$VM_ID"
VM_IP=$(/opt/homebrew/bin/tart ip "$VM_ID")
MACHINE_PASSWORD="%s"
#LOCK
%s

echo "$VM_IP"
ANCHOR_FILE="/etc/pf.anchors/tart"
echo "$MACHINE_PASSWORD" | sudo -S sed -i '' "/$VM_IP/d" "$ANCHOR_FILE"
/opt/homebrew/bin/tart stop "$VM_ID"; /opt/homebrew/bin/tart delete "$VM_ID"
if [ $? -ne 0 ]; then
  tart_pid=$(ps -A | grep -m1 "tart run "$VM_ID"" | awk '{print $1}')
  if [ -n "$tart_pid" ]; then
	kill $tart_pid || true
  fi
fi

%s
	`, vm, machinePassword, lockFunction, UnlockFunction)
		return command
	}
}

// This will be responsible to copy the script from host to vm and run it
func (mv *MacVirtualizer) getStartCloudInitScript(cloudInitScriptPath, vmID, username, password string) string {
	return fmt.Sprintf(`
VM_USER="%s"
VM_PASSWORD="%s"

# Get VM IP
VM_IP=$(/opt/homebrew/bin/tart ip %s)

# SCP command using expect
expect <<- DONE
    spawn scp -v -o "ConnectTimeout=5" -o "StrictHostKeyChecking=no" "%s" "$VM_USER@$VM_IP:/tmp/cloud_init.sh"
    expect {
		"*yes/no*" { send "yes\r"; exp_continue }
        "*Password:" {send "$VM_PASSWORD\r"; exp_continue}
    }
DONE

# SSH command using expect
expect <<- DONE
	set timeout 90
    spawn ssh -v -o "ConnectTimeout=5" -o "StrictHostKeyChecking=no" "$VM_USER@$VM_IP" "echo $VM_PASSWORD | sh /tmp/cloud_init.sh"
    expect {
		"*yes/no*" { send "yes\r"; exp_continue }
        "*Password:" {send "$VM_PASSWORD\r"; exp_continue}
    }
DONE
`, username, password, vmID, cloudInitScriptPath)
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

func (mv *MacVirtualizer) GetHealthCheckPort(portLabel string) string {
	return fmt.Sprint(lehelper.LiteEnginePort)
}
