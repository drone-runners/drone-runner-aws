package nomad

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/hashicorp/nomad/api"
)

type LinuxVirtualizer struct{}

func NewLinuxVirtualizer() *LinuxVirtualizer {
	return &LinuxVirtualizer{}
}

func (lv *LinuxVirtualizer) GetInitJob(vm, nodeID, vmImage, userData, username, password string, port int, resource cf.NomadResource, opts *types.InstanceCreateOpts, gitspacesPortMappings map[int]int) (job *api.Job, id, group string) { //nolint
	id = initJobID(vm)
	group = fmt.Sprintf("init_task_group_%s", vm)
	uData := lv.generateUserData(opts)
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(uData))

	hostPath := fmt.Sprintf("/usr/local/bin/%s.sh", vm)
	vmPath := fmt.Sprintf("/usr/bin/%s.sh", vm)
	var runCmdFormat string
	runCmdFormat = "%s run %s --name %s --cpus %s --memory %sGB --size %s --ssh --runtime=docker --ports %d:%s --copy-files %s:%s"
	args := []interface{}{ignitePath, vmImage, vm, resource.Cpus, resource.MemoryGB, resource.DiskSize, port, strconv.Itoa(lehelper.LiteEnginePort), hostPath, vmPath}
	for vmPort, hostPort := range gitspacesPortMappings {
		runCmdFormat += " --ports %d:%d"
		args = append(args, hostPort, vmPort)
	}
	runCmd := fmt.Sprintf(runCmdFormat, args...)
	entrypoint := lv.GetEntryPoint()

	job = &api.Job{
		ID:          &id,
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
				Name:  stringToPtr(group),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "create_startup_script_on_host",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", fmt.Sprintf("echo %s >> %s", encodedUserData, hostPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "prestart",
						},
					},

					{
						Name:      "enable_port_forwarding",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", "iptables -P FORWARD ACCEPT"},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "prestart",
						},
					},

					{
						Name:      "ignite_run",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", runCmd},
						},
					},

					{
						Name:      "ignite_exec",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", fmt.Sprintf("%s exec %s 'cat %s | base64 --decode | bash'", ignitePath, vm, vmPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "poststop",
						},
					},
					{
						Name:      "cleanup_startup_script_from_host",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", fmt.Sprintf("rm %s", hostPath)},
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
	return job, id, group
}

func (lv *LinuxVirtualizer) generateUserData(opts *types.InstanceCreateOpts) string {
	params := &cloudinit.Params{
		Platform:               opts.Platform,
		CACert:                 string(opts.CACert),
		LiteEngineLogsPath:     oshelp.GetLiteEngineLogsPath(opts.OS),
		TLSCert:                string(opts.TLSCert),
		TLSKey:                 string(opts.TLSKey),
		LiteEnginePath:         opts.LiteEnginePath,
		HarnessTestBinaryURI:   opts.HarnessTestBinaryURI,
		PluginBinaryURI:        opts.PluginBinaryURI,
		Tmate:                  opts.Tmate,
		AutoInjectionBinaryURI: opts.AutoInjectionBinaryURI,
	}
	if opts.GitspaceOpts.Secret != "" && opts.GitspaceOpts.AccessToken != "" {
		params.GitspaceAgentConfig = types.GitspaceAgentConfig{Secret: opts.GitspaceOpts.Secret, AccessToken: opts.GitspaceOpts.AccessToken}
	}
	return cloudinit.LinuxBash(params)
}

func (lv *LinuxVirtualizer) GetMachineFrequency() int {
	return machineFrequencyMhz
}

func (lv *LinuxVirtualizer) GetGlobalAccountID() string {
	return globalAccount
}

func (lv *LinuxVirtualizer) GetEntryPoint() string {
	return "/usr/bin/su"
}

func (lv *LinuxVirtualizer) GetHealthCheckupGenerator() func(time.Duration, string, string) string {
	return func(sleep time.Duration, vm, port string) string {
		sleepSecs := sleep.Seconds()
		return fmt.Sprintf(`
#!/usr/bin/bash
sleep %f
echo "done sleeping, port is: %s"
cntr=0
while true
	do
		nc -vz localhost %s
		if [ $? -eq 1 ]; then
		    echo "port check failed, incrementing counter:"
			echo "cntr: "$cntr
			((cntr++))
			if [ $cntr -eq 3 ]; then
				echo "port check failed three times. output of ignite command:"
				ignite ps
				echo "output of iptables:"
				iptables -L -v -n
				exit 1
			fi
		else
			cntr=0
			echo "Port check passed..."
		fi
		sleep 20
	done`, sleepSecs, port, port)
	}
}

func (lv *LinuxVirtualizer) GetDestroyScriptGenerator() func(string) string {
	return func(vm string) string {
		return fmt.Sprintf(`
	    %s stop %s; %s rm %s
		if [ $? -ne 0 ]; then
		  %s stop -f %s; %s rm -f %s
		fi
	`, ignitePath, vm, ignitePath, vm, ignitePath, vm, ignitePath, vm)
	}
}
